package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
)

const maxGitCommandOutput = 1 << 20

type preparedSubagentWorktree struct {
	CWD       string
	Path      string
	RepoRoot  string
	Isolation string
	Warning   string
}

func prepareSubagentWorktree(ctx context.Context, workspaceRoot, worktreeRoot, taskID string) (preparedSubagentWorktree, error) {
	fail := func(err error) (preparedSubagentWorktree, error) {
		return preparedSubagentWorktree{}, fmt.Errorf("worktree isolation unavailable: %w", err)
	}
	if filepath.Base(taskID) != taskID || taskID == "." || taskID == "" {
		return fail(fmt.Errorf("invalid task ID"))
	}
	repoRoot, err := runGit(ctx, workspaceRoot, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return fail(fmt.Errorf("workspace is not a Git repository: %w", err))
	}
	repoRoot = strings.TrimSpace(repoRoot)
	repoRoot, err = filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return fail(fmt.Errorf("resolve repository root: %w", err))
	}
	workspaceRoot, err = filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return fail(fmt.Errorf("resolve workspace root: %w", err))
	}
	relativeWorkspace, err := filepath.Rel(repoRoot, workspaceRoot)
	if err != nil || relativeWorkspace == ".." || strings.HasPrefix(relativeWorkspace, ".."+string(filepath.Separator)) {
		return fail(fmt.Errorf("workspace is outside repository root"))
	}
	head, err := runGit(ctx, repoRoot, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fail(fmt.Errorf("repository has no usable HEAD: %w", err))
	}
	head = strings.TrimSpace(head)
	if err := os.MkdirAll(worktreeRoot, 0o700); err != nil {
		return fail(fmt.Errorf("create worktree root: %w", err))
	}
	indexFile, err := os.CreateTemp(worktreeRoot, ".index-*")
	if err != nil {
		return fail(fmt.Errorf("create temporary Git index: %w", err))
	}
	indexPath := indexFile.Name()
	if closeErr := indexFile.Close(); closeErr != nil {
		_ = os.Remove(indexPath)
		return fail(fmt.Errorf("close temporary Git index: %w", closeErr))
	}
	if err := os.Remove(indexPath); err != nil {
		return fail(fmt.Errorf("prepare temporary Git index: %w", err))
	}
	defer os.Remove(indexPath)
	indexEnv := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err := runGit(ctx, repoRoot, indexEnv, "read-tree", head); err != nil {
		return fail(fmt.Errorf("seed temporary Git index: %w", err))
	}
	if _, err := runGit(ctx, repoRoot, indexEnv, "add", "-A", "--", "."); err != nil {
		return fail(fmt.Errorf("snapshot dirty workspace: %w", err))
	}
	tree, err := runGit(ctx, repoRoot, indexEnv, "write-tree")
	if err != nil {
		return fail(fmt.Errorf("write workspace snapshot tree: %w", err))
	}
	tree = strings.TrimSpace(tree)
	commitEnv := []string{
		"GIT_AUTHOR_NAME=Azem", "GIT_AUTHOR_EMAIL=azem@localhost",
		"GIT_COMMITTER_NAME=Azem", "GIT_COMMITTER_EMAIL=azem@localhost",
	}
	commit, err := runGit(ctx, repoRoot, commitEnv, "commit-tree", tree, "-p", head, "-m", "Azem subagent workspace snapshot")
	if err != nil {
		return fail(fmt.Errorf("create detached snapshot commit: %w", err))
	}
	path := filepath.Join(worktreeRoot, taskID)
	if _, err := os.Lstat(path); err == nil {
		return fail(fmt.Errorf("worktree path already exists"))
	} else if !os.IsNotExist(err) {
		return fail(fmt.Errorf("inspect worktree path: %w", err))
	}
	if _, err := runGit(ctx, repoRoot, nil, "worktree", "add", "--detach", path, strings.TrimSpace(commit)); err != nil {
		_, _ = runGit(context.WithoutCancel(ctx), repoRoot, nil, "worktree", "remove", "--force", path)
		_ = os.RemoveAll(path)
		return fail(fmt.Errorf("create detached worktree: %w", err))
	}
	cwd := path
	if relativeWorkspace != "." {
		cwd = filepath.Join(path, relativeWorkspace)
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		_, _ = runGit(context.WithoutCancel(ctx), repoRoot, nil, "worktree", "remove", "--force", path)
		return fail(fmt.Errorf("isolated workspace directory is unavailable"))
	}
	return preparedSubagentWorktree{CWD: cwd, Path: path, RepoRoot: repoRoot, Isolation: "worktree"}, nil
}

func finalizeSubagentWorktree(run *agentservice.SubagentRun, repoRoot string) {
	if run == nil || run.WorktreePath == "" || repoRoot == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := runGit(ctx, run.WorktreePath, nil, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		run.Warning = appendWarning(run.Warning, fmt.Sprintf("worktree retained at %s because status inspection failed: %v", run.WorktreePath, err))
		return
	}
	if strings.TrimSpace(status) != "" {
		run.Warning = appendWarning(run.Warning, fmt.Sprintf("worktree retained at %s with unmerged child changes; review and merge manually", run.WorktreePath))
		return
	}
	if _, err := runGit(ctx, repoRoot, nil, "worktree", "remove", "--force", run.WorktreePath); err != nil {
		run.Warning = appendWarning(run.Warning, fmt.Sprintf("clean worktree retained at %s because removal failed: %v", run.WorktreePath, err))
		return
	}
	run.WorktreePath = ""
}

type boundedGitBuffer struct {
	bytes.Buffer
}

func (buffer *boundedGitBuffer) Write(data []byte) (int, error) {
	remaining := maxGitCommandOutput - buffer.Len()
	if remaining <= 0 {
		return 0, fmt.Errorf("Git command output exceeds %d bytes", maxGitCommandOutput)
	}
	if len(data) > remaining {
		_, _ = buffer.Buffer.Write(data[:remaining])
		return remaining, fmt.Errorf("Git command output exceeds %d bytes", maxGitCommandOutput)
	}
	return buffer.Buffer.Write(data)
}

func runGit(ctx context.Context, directory string, extraEnv []string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), extraEnv...)
	var output boundedGitBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(arguments, " "), message)
	}
	return output.String(), nil
}
