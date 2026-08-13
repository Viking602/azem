package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxWorkspaceChanges   = 4000
	maxWorkspaceGitOutput = 8 << 20
	maxWorkspacePatch     = 6 << 20
)

type WorkspaceChangeFile struct {
	Path           string `json:"path"`
	Status         string `json:"status"`
	IndexStatus    string `json:"indexStatus,omitempty"`
	WorktreeStatus string `json:"worktreeStatus,omitempty"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Binary         bool   `json:"binary,omitempty"`
}

type WorkspaceChangeSet struct {
	Repository bool                  `json:"repository"`
	Branch     string                `json:"branch,omitempty"`
	Base       string                `json:"base,omitempty"`
	Additions  int                   `json:"additions"`
	Deletions  int                   `json:"deletions"`
	Files      []WorkspaceChangeFile `json:"files"`
	Truncated  bool                  `json:"truncated,omitempty"`
}

type WorkspaceChange struct {
	WorkspaceChangeFile
	Patch     string `json:"patch,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type gitStatusEntry struct {
	path     string
	index    byte
	worktree byte
}

type gitNumstat struct {
	additions int
	deletions int
	binary    bool
}

func (b *Bridge) WorkspaceChanges() (WorkspaceChangeSet, error) {
	ctx, cancel := b.workspaceGitContext()
	defer cancel()
	if !isGitWorktree(ctx, b.workspace) {
		return WorkspaceChangeSet{Files: []WorkspaceChangeFile{}}, nil
	}

	entries, err := readGitStatus(ctx, b.workspace)
	if err != nil {
		return WorkspaceChangeSet{}, err
	}
	numstats, err := readGitNumstat(ctx, b.workspace)
	if err != nil {
		return WorkspaceChangeSet{}, err
	}
	files := make([]WorkspaceChangeFile, 0, len(entries))
	additions, deletions := 0, 0
	for _, entry := range entries {
		stat := numstats[entry.path]
		if entry.index == '?' && entry.worktree == '?' {
			stat.additions, stat.binary = countUntrackedLines(filepath.Join(b.workspace, filepath.FromSlash(entry.path)))
		}
		file := WorkspaceChangeFile{
			Path: filepath.ToSlash(entry.path), Status: workspaceChangeStatus(entry.index, entry.worktree),
			IndexStatus: gitStatusName(entry.index), WorktreeStatus: gitStatusName(entry.worktree),
			Additions: stat.additions, Deletions: stat.deletions, Binary: stat.binary,
		}
		files = append(files, file)
		additions += file.Additions
		deletions += file.Deletions
	}
	sort.SliceStable(files, func(i, j int) bool { return strings.ToLower(files[i].Path) < strings.ToLower(files[j].Path) })
	result := WorkspaceChangeSet{
		Repository: true, Branch: currentGitBranch(ctx, b.workspace), Base: "HEAD",
		Additions: additions, Deletions: deletions, Files: files,
	}
	if len(result.Files) > maxWorkspaceChanges {
		result.Files = result.Files[:maxWorkspaceChanges]
		result.Truncated = true
	}
	return result, nil
}

func (b *Bridge) WorkspaceChange(path string) (WorkspaceChange, error) {
	relative, err := normalizeWorkspaceChangePath(path)
	if err != nil {
		return WorkspaceChange{}, err
	}
	ctx, cancel := b.workspaceGitContext()
	defer cancel()
	if !isGitWorktree(ctx, b.workspace) {
		return WorkspaceChange{}, errors.New("workspace is not a Git repository")
	}
	entries, err := readGitStatus(ctx, b.workspace)
	if err != nil {
		return WorkspaceChange{}, err
	}
	var selected *gitStatusEntry
	for index := range entries {
		if filepath.ToSlash(entries[index].path) == relative {
			selected = &entries[index]
			break
		}
	}
	if selected == nil {
		return WorkspaceChange{}, fmt.Errorf("workspace path %q has no uncommitted change", relative)
	}

	base := WorkspaceChangeFile{Path: relative, Status: workspaceChangeStatus(selected.index, selected.worktree), IndexStatus: gitStatusName(selected.index), WorktreeStatus: gitStatusName(selected.worktree)}
	if selected.index == '?' && selected.worktree == '?' {
		return b.untrackedWorkspaceChange(relative, base)
	}
	patch, truncated, diffErr := runGitBounded(ctx, b.workspace, maxWorkspacePatch,
		"diff", "--no-ext-diff", "--no-color", "--no-renames", "--unified=3", "HEAD", "--", relative)
	if diffErr != nil {
		staged, stagedTruncated, stagedErr := runGitBounded(ctx, b.workspace, maxWorkspacePatch/2,
			"diff", "--cached", "--no-ext-diff", "--no-color", "--no-renames", "--unified=3", "--", relative)
		unstaged, unstagedTruncated, unstagedErr := runGitBounded(ctx, b.workspace, maxWorkspacePatch/2,
			"diff", "--no-ext-diff", "--no-color", "--no-renames", "--unified=3", "--", relative)
		if stagedErr != nil || unstagedErr != nil {
			return WorkspaceChange{}, fmt.Errorf("read workspace change %q: %w", relative, errors.Join(diffErr, stagedErr, unstagedErr))
		}
		patch = append(staged, unstaged...)
		truncated = stagedTruncated || unstagedTruncated
	}
	base.Additions, base.Deletions, base.Binary = countPatchChanges(patch)
	return WorkspaceChange{WorkspaceChangeFile: base, Patch: string(patch), Truncated: truncated}, nil
}

func (b *Bridge) untrackedWorkspaceChange(relative string, base WorkspaceChangeFile) (WorkspaceChange, error) {
	file, err := b.WorkspaceFile(relative)
	if err != nil {
		return WorkspaceChange{}, err
	}
	if file.Kind != "text" {
		base.Binary = true
		return WorkspaceChange{WorkspaceChangeFile: base}, nil
	}
	content := strings.ReplaceAll(file.Content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	base.Additions = len(lines)
	var patch boundedCapture
	patch.limit = maxWorkspacePatch
	fmt.Fprintf(&patch, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", relative, relative, relative, len(lines))
	for _, line := range lines {
		fmt.Fprintf(&patch, "+%s\n", line)
	}
	return WorkspaceChange{WorkspaceChangeFile: base, Patch: patch.String(), Truncated: file.Truncated || patch.truncated}, nil
}

func (b *Bridge) workspaceGitContext() (context.Context, context.CancelFunc) {
	parent := b.ctx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, 12*time.Second)
}

func isGitWorktree(ctx context.Context, workspace string) bool {
	output, _, err := runGitBounded(ctx, workspace, 64, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

func readGitStatus(ctx context.Context, workspace string) ([]gitStatusEntry, error) {
	output, _, err := runGitBounded(ctx, workspace, maxWorkspaceGitOutput, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, fmt.Errorf("list workspace changes: %w", err)
	}
	entries := make([]gitStatusEntry, 0)
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) < 4 || record[2] != ' ' {
			continue
		}
		entries = append(entries, gitStatusEntry{path: string(record[3:]), index: record[0], worktree: record[1]})
	}
	return entries, nil
}

func readGitNumstat(ctx context.Context, workspace string) (map[string]gitNumstat, error) {
	output, _, err := runGitBounded(ctx, workspace, maxWorkspaceGitOutput, "diff", "--numstat", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		staged, _, stagedErr := runGitBounded(ctx, workspace, maxWorkspaceGitOutput/2, "diff", "--cached", "--numstat", "-z", "--no-renames", "--")
		unstaged, _, unstagedErr := runGitBounded(ctx, workspace, maxWorkspaceGitOutput/2, "diff", "--numstat", "-z", "--no-renames", "--")
		if stagedErr != nil || unstagedErr != nil {
			return nil, fmt.Errorf("read workspace line changes: %w", errors.Join(err, stagedErr, unstagedErr))
		}
		output = append(staged, unstaged...)
	}
	result := make(map[string]gitNumstat)
	for _, record := range bytes.Split(output, []byte{0}) {
		fields := bytes.SplitN(record, []byte{'\t'}, 3)
		if len(fields) != 3 {
			continue
		}
		path := filepath.ToSlash(string(fields[2]))
		current := result[path]
		added, addedErr := strconv.Atoi(string(fields[0]))
		deleted, deletedErr := strconv.Atoi(string(fields[1]))
		if addedErr != nil || deletedErr != nil {
			current.binary = true
		} else {
			current.additions += added
			current.deletions += deleted
		}
		result[path] = current
	}
	return result, nil
}

func runGitBounded(ctx context.Context, workspace string, limit int, arguments ...string) ([]byte, bool, error) {
	args := append([]string{"-C", workspace, "-c", "core.quotepath=false"}, arguments...)
	command := exec.CommandContext(ctx, "git", args...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_EXTERNAL_DIFF=", "GIT_PAGER=cat", "LC_ALL=C")
	stdout := &boundedCapture{limit: limit}
	stderr := &boundedCapture{limit: 64 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return stdout.Bytes(), stdout.truncated, fmt.Errorf("git %s: %w: %s", arguments[0], err, message)
		}
		return stdout.Bytes(), stdout.truncated, fmt.Errorf("git %s: %w", arguments[0], err)
	}
	return stdout.Bytes(), stdout.truncated, nil
}

type boundedCapture struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (capture *boundedCapture) Write(data []byte) (int, error) {
	if remaining := capture.limit - capture.buffer.Len(); remaining > 0 {
		stored := min(remaining, len(data))
		_, _ = capture.buffer.Write(data[:stored])
	}
	if capture.buffer.Len() >= capture.limit && len(data) > 0 {
		capture.truncated = true
	}
	return len(data), nil
}

func (capture *boundedCapture) Bytes() []byte  { return capture.buffer.Bytes() }
func (capture *boundedCapture) String() string { return capture.buffer.String() }

func normalizeWorkspaceChangePath(path string) (string, error) {
	if strings.ContainsRune(path, 0) || filepath.IsAbs(path) {
		return "", errors.New("workspace change path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace change path escapes the workspace root")
	}
	return filepath.ToSlash(clean), nil
}

func workspaceChangeStatus(index, worktree byte) string {
	if index == '?' && worktree == '?' {
		return "untracked"
	}
	if index == 'D' || worktree == 'D' {
		return "deleted"
	}
	if index == 'A' || worktree == 'A' {
		return "added"
	}
	if index == 'R' || worktree == 'R' {
		return "renamed"
	}
	if index == 'T' || worktree == 'T' {
		return "type_changed"
	}
	return "modified"
}

func gitStatusName(status byte) string {
	switch status {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'M':
		return "modified"
	case 'R':
		return "renamed"
	case 'T':
		return "type_changed"
	case '?':
		return "untracked"
	default:
		return ""
	}
}

func countPatchChanges(patch []byte) (int, int, bool) {
	additions, deletions, binary := 0, 0, false
	for _, line := range bytes.Split(patch, []byte{'\n'}) {
		switch {
		case bytes.HasPrefix(line, []byte("Binary files ")) || bytes.HasPrefix(line, []byte("GIT binary patch")):
			binary = true
		case bytes.HasPrefix(line, []byte{'+'}) && !bytes.HasPrefix(line, []byte("+++")):
			additions++
		case bytes.HasPrefix(line, []byte{'-'}) && !bytes.HasPrefix(line, []byte("---")):
			deletions++
		}
	}
	return additions, deletions, binary
}

func countUntrackedLines(path string) (int, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 1, false
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	buffer := make([]byte, 32*1024)
	lines, size, last := 0, 0, byte(0)
	for {
		read, readErr := file.Read(buffer)
		if read > 0 {
			chunk := buffer[:read]
			if bytes.IndexByte(chunk, 0) >= 0 {
				return 0, true
			}
			lines += bytes.Count(chunk, []byte{'\n'})
			size += read
			last = chunk[read-1]
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, false
		}
	}
	if size > 0 && last != '\n' {
		lines++
	}
	return lines, false
}
