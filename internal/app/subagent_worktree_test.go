package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
)

func TestPrepareSubagentWorktreeSnapshotsDirtyTrackedAndUntrackedWithoutTouchingIndex(t *testing.T) {
	ctx := context.Background()
	repoRoot := t.TempDir()
	workspace := filepath.Join(repoRoot, "nested")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoRoot, nil, "init", "--initial-branch=main"); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(workspace, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoRoot, nil, "add", "--", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoRoot, nil,
		"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("dirty tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "untracked.txt"), []byte("dirty untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	statusBefore, err := runGit(ctx, repoRoot, nil, "status", "--short")
	if err != nil {
		t.Fatal(err)
	}

	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	prepared := prepareSubagentWorktree(ctx, workspace, worktreeRoot, "task-one")
	if prepared.Isolation != "worktree" || prepared.Path == "" || prepared.RepoRoot == "" || prepared.Warning != "" {
		t.Fatalf("prepared worktree = %#v", prepared)
	}
	if prepared.CWD != filepath.Join(prepared.Path, "nested") {
		t.Fatalf("isolated cwd = %q", prepared.CWD)
	}
	for name, want := range map[string]string{"tracked.txt": "dirty tracked\n", "untracked.txt": "dirty untracked\n"} {
		got, err := os.ReadFile(filepath.Join(prepared.CWD, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("isolated %s = %q, want %q", name, got, want)
		}
	}
	isolatedStatus, err := runGit(ctx, prepared.Path, nil, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(isolatedStatus) != "" {
		t.Fatalf("new worktree is dirty: %q", isolatedStatus)
	}
	statusAfter, err := runGit(ctx, repoRoot, nil, "status", "--short")
	if err != nil {
		t.Fatal(err)
	}
	if statusAfter != statusBefore {
		t.Fatalf("main Git state changed:\nbefore=%q\nafter=%q", statusBefore, statusAfter)
	}

	run := agentservice.SubagentRun{WorktreePath: prepared.Path}
	finalizeSubagentWorktree(&run, prepared.RepoRoot)
	if run.WorktreePath != "" || run.Warning != "" {
		t.Fatalf("clean finalization = %#v", run)
	}
	if _, err := os.Stat(prepared.Path); !os.IsNotExist(err) {
		t.Fatalf("clean worktree still exists: %v", err)
	}
}

func TestFinalizeSubagentWorktreeRetainsChildChangesAndNonGitFallsBack(t *testing.T) {
	ctx := context.Background()
	repoRoot := t.TempDir()
	if _, err := runGit(ctx, repoRoot, nil, "init", "--initial-branch=main"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repoRoot, "tracked.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoRoot, nil, "add", "--", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoRoot, nil,
		"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	prepared := prepareSubagentWorktree(ctx, repoRoot, filepath.Join(t.TempDir(), "worktrees"), "task-dirty")
	if prepared.Isolation != "worktree" {
		t.Fatalf("prepared worktree = %#v", prepared)
	}
	if err := os.WriteFile(filepath.Join(prepared.CWD, "tracked.txt"), []byte("child change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := agentservice.SubagentRun{WorktreePath: prepared.Path}
	finalizeSubagentWorktree(&run, prepared.RepoRoot)
	if run.WorktreePath != prepared.Path || !strings.Contains(run.Warning, "review and merge manually") {
		t.Fatalf("dirty finalization = %#v", run)
	}
	if _, err := os.Stat(prepared.Path); err != nil {
		t.Fatalf("dirty worktree was removed: %v", err)
	}
	if _, err := runGit(ctx, repoRoot, nil, "worktree", "remove", "--force", prepared.Path); err != nil {
		t.Fatal(err)
	}

	nonGit := t.TempDir()
	fallback := prepareSubagentWorktree(ctx, nonGit, filepath.Join(t.TempDir(), "worktrees"), "task-fallback")
	if fallback.CWD != nonGit || fallback.Isolation != "none" || fallback.Path != "" || !strings.Contains(fallback.Warning, "using shared workspace") {
		t.Fatalf("non-Git fallback = %#v", fallback)
	}
	emptyRepo := t.TempDir()
	if _, err := runGit(ctx, emptyRepo, nil, "init", "--initial-branch=main"); err != nil {
		t.Fatal(err)
	}
	noHead := prepareSubagentWorktree(ctx, emptyRepo, filepath.Join(t.TempDir(), "worktrees"), "task-no-head")
	if noHead.Isolation != "none" || !strings.Contains(noHead.Warning, "no usable HEAD") {
		t.Fatalf("no-HEAD fallback = %#v", noHead)
	}
}

type staticSubagentAnswerDriver struct{}

func (staticSubagentAnswerDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "test", Models: []string{"model"}}
}

func (staticSubagentAnswerDriver) Stream(context.Context, hyprovider.Request) (hyprovider.Stream, error) {
	return hyprovider.NewSliceStream([]hyprovider.Event{
		{Kind: hyprovider.EventTextDelta, Text: "isolated answer"},
		{Kind: hyprovider.EventDone},
	}), nil
}

func TestRuntimeUsesAndCleansEffectiveIsolatedCWD(t *testing.T) {
	ctx := context.Background()
	repoRoot := t.TempDir()
	workspace := filepath.Join(repoRoot, "nested")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoRoot, nil, "init", "--initial-branch=main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoRoot, nil, "add", "--", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoRoot, nil,
		"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	coding, err := agentservice.NewService(providerStore, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	runStore, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	subagents := config.Default().Agents.Subagents
	explore := subagents.Roles["explore"]
	explore.Isolation = "worktree"
	subagents.Roles["explore"] = explore
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	runtime, err := newSubagentRuntime(ctx, subagents, runStore, worktreeRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(ctx)
	parent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "parent", ProviderID: "test", ModelID: "model", Reasoning: "high",
		Driver: staticSubagentAnswerDriver{}, Coding: coding, WorkspaceRoot: workspace,
	}
	run, err := runtime.Spawn(ctx, subagentSpawnInput{Prompt: "inspect", Description: "inspect", SubagentType: "explore"}, parent)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := runtime.Query(ctx, "session", []string{run.ID}, 5*time.Second)
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %#v", snapshots)
	}
	snapshot := snapshots[0]
	if !snapshot.Found || snapshot.Run.State != agentservice.SubagentCompleted || snapshot.Run.Output != "isolated answer" {
		t.Fatalf("terminal snapshot = %#v", snapshot)
	}
	if snapshot.Run.Isolation != "worktree" || snapshot.Run.WorktreePath != "" {
		t.Fatalf("isolation result = %#v", snapshot.Run)
	}
	expectedPrefix := filepath.Join(worktreeRoot, run.ID, "nested")
	if snapshot.Run.CWD != expectedPrefix {
		t.Fatalf("effective cwd = %q, want %q", snapshot.Run.CWD, expectedPrefix)
	}
	if _, err := os.Stat(snapshot.Run.CWD); !os.IsNotExist(err) {
		t.Fatalf("clean effective cwd still exists: %v", err)
	}
}
