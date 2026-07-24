package recovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn/coding"
	"github.com/Viking602/go-hydaelyn/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type recordingRunResumer struct {
	runIDs []string
}

func (r *recordingRunResumer) ResumeRun(_ context.Context, runID string) error {
	r.runIDs = append(r.runIDs, runID)
	return nil
}

func TestRecoverProjectsPendingApprovalAndInterruptsSubagents(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	codingService, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer codingService.Close(ctx)
	run, err := codingService.StartRun(ctx, "edit note")
	if err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"path": "note.txt"})
	read, err := codingService.ExecuteTool(ctx, run, tool.Call{ID: "read-1", Name: coding.ToolReadFile, Arguments: readArgs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var readResult coding.ReadFileToolResult
	if err := json.Unmarshal(read.Result.Structured, &readResult); err != nil {
		t.Fatal(err)
	}
	editArgs, _ := json.Marshal(map[string]string{"input": readResult.Header + "\nreplace 1:\n+after\n"})
	edit, err := codingService.ExecuteTool(ctx, run, tool.Call{ID: "edit-1", Name: coding.ToolEditHashline, Arguments: editArgs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if edit.Approval == nil {
		t.Fatal("write tool did not pause for approval")
	}

	subagents, err := agentservice.NewSQLSubagentRunStore(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := subagents.Create(ctx, agentservice.SubagentRun{ID: "child-1", SessionID: "default", ParentRunID: edit.Approval.Request.RunID, Type: "explore", State: agentservice.SubagentRunning, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	recoveryService, err := NewService(store, codingService, subagents, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := recoveryService.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.InterruptedSubagents != 1 {
		t.Fatalf("interrupted subagents = %d", summary.InterruptedSubagents)
	}
	if len(summary.Runs) != 1 || summary.Runs[0].Run.ID != edit.Approval.Request.RunID {
		t.Fatalf("recovered runs = %+v", summary.Runs)
	}
	if len(summary.Approvals) != 1 || summary.Approvals[0].Approval.ApprovalID != edit.Approval.Request.ApprovalID {
		t.Fatalf("pending approvals = %+v", summary.Approvals)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "before\n" {
		t.Fatalf("pending edit replayed during recovery: %q", contents)
	}
	children, err := subagents.List(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].State != agentservice.SubagentInterrupted {
		t.Fatalf("recovered subagents = %+v", children)
	}
}

func TestRecoverResumesRedispatchedSingleAgentRun(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "resume.db")
	store, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	codingService, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := codingService.StartRun(ctx, "resume checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if err := codingService.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	recoveredCoding, err := agentservice.NewService(reopened, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer recoveredCoding.Close(ctx)
	resumer := &recordingRunResumer{}
	recoveryService, err := NewService(reopened, recoveredCoding, nil, nil, resumer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recoveryService.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if len(resumer.runIDs) != 1 || resumer.runIDs[0] != run.RunID {
		t.Fatalf("resumed runs=%v want=%s", resumer.runIDs, run.RunID)
	}
}
