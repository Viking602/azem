package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn/coding"
	"github.com/Viking602/go-hydaelyn/tool"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestSQLSubagentRunStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := providerStore.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	store, err := NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Truncate(time.Nanosecond)
	run := SubagentRun{
		ID: "child-1", SessionID: "session", ParentRunID: "parent-run", ParentAgentID: "parent-agent",
		ParentToolCallID: "parent-call", ChildRunID: "child-run", Description: "inspect workspace", Type: "explore",
		State: SubagentRunning, Summary: "reading files", Provider: "grok", Model: "model", Reasoning: "high",
		CapabilityMode: "read-only", RequestedIsolation: "worktree", Isolation: "none", CWD: "/workspace",
		Background: true, Output: "partial", Warning: "shared fallback", Transcript: json.RawMessage(`[{"role":"user","text":"inspect"}]`),
		ToolCalls: 2, Turns: 3, TokensUsed: 42, ToolsUsed: []string{"coding.read_file", "coding.search"},
		WorktreePath: "/worktrees/child-1", StartedAt: started,
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSubagentRunEqual(t, got, run)

	finished := started.Add(time.Second)
	run.State = SubagentCompleted
	run.Summary = "done"
	run.Output = "final answer"
	run.Warning = ""
	run.FinishedAt = finished
	if err := store.Save(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCompletionDelivered(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	run.CompletionDelivered = true
	listed, err := store.List(ctx, run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed runs = %#v", listed)
	}
	assertSubagentRunEqual(t, listed[0], run)

	for _, state := range []SubagentState{SubagentInitializing, SubagentQueued, SubagentRunning, SubagentCancelling} {
		id := "incomplete-" + string(state)
		if err := store.Create(ctx, SubagentRun{ID: id, SessionID: "session", ParentRunID: "parent", Type: "verify", State: state, StartedAt: started}); err != nil {
			t.Fatal(err)
		}
	}
	count, err := store.InterruptIncomplete(ctx, finished)
	if err != nil || count != 4 {
		t.Fatalf("interrupt count=%d err=%v", count, err)
	}
	completed, err := store.Get(ctx, run.ID)
	if err != nil || completed.State != SubagentCompleted {
		t.Fatalf("completed run changed during recovery: %#v, %v", completed, err)
	}
}

func assertSubagentRunEqual(t *testing.T, got, want SubagentRun) {
	t.Helper()
	gotTranscript := string(got.Transcript)
	wantTranscript := string(want.Transcript)
	got.Transcript = nil
	want.Transcript = nil
	gotTools, _ := json.Marshal(got.ToolsUsed)
	wantTools, _ := json.Marshal(want.ToolsUsed)
	got.ToolsUsed = nil
	want.ToolsUsed = nil
	if !reflect.DeepEqual(got, want) || gotTranscript != wantTranscript || string(gotTools) != string(wantTools) {
		t.Fatalf("subagent run\n got: %#v transcript=%s tools=%s\nwant: %#v transcript=%s tools=%s", got, gotTranscript, gotTools, want, wantTranscript, wantTools)
	}
}

func TestWorkspaceDriversFilterGitDiffByEffectiveRoot(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "workspace-tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(providerStore, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	}()

	nonGit := t.TempDir()
	drivers, err := service.WorkspaceDrivers(ctx, nonGit)
	if err != nil {
		t.Fatal(err)
	}
	if hasToolDriver(drivers, coding.ToolGitDiff) {
		t.Fatalf("non-Git workspace exposed %q", coding.ToolGitDiff)
	}

	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "azem@example.invalid")
	runGit(t, repository, "config", "user.name", "Azem Test")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "tracked.txt")
	runGit(t, repository, "commit", "-m", "fixture")

	subdirectory := filepath.Join(repository, "nested", "workspace")
	if err := os.MkdirAll(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	drivers, err = service.WorkspaceDrivers(ctx, subdirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !hasToolDriver(drivers, coding.ToolGitDiff) {
		t.Fatalf("repository subdirectory omitted %q", coding.ToolGitDiff)
	}

	worktree := filepath.Join(t.TempDir(), "child-worktree")
	runGit(t, repository, "worktree", "add", "--detach", worktree, "HEAD")
	drivers, err = service.WorkspaceDrivers(ctx, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if !hasToolDriver(drivers, coding.ToolGitDiff) {
		t.Fatalf("Git worktree omitted %q", coding.ToolGitDiff)
	}
}

func hasToolDriver(drivers []tool.Driver, name string) bool {
	for _, driver := range drivers {
		if driver.Definition().Name == name {
			return true
		}
	}
	return false
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
