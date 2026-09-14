package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/recovery"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestRuntimeProjectionUsesTypedCanonicalSessionState(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "projection.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Projection", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	for _, block := range []session.Block{
		{Kind: "user", RunID: "run", Content: "inspect", State: "submitted", Attachments: []session.Attachment{}},
		{Kind: "commentary", RunID: "run", Content: "working", TextPhase: "commentary", State: "completed", Data: map[string]string{"elapsedMs": "12"}},
		{Kind: "question", RunID: "run", State: "interrupted", Data: map[string]string{"userInputId": "input-1", "questions": "[]"}},
	} {
		if _, err := sessions.AppendBlock(ctx, "session", block); err != nil {
			t.Fatal(err)
		}
	}
	completedRecord, err := sessions.StartToolRecord(ctx, "session", session.ToolRecord{
		RunID: "run", ToolCallID: "tool-1", Name: "coding.read_file",
		Arguments: json.RawMessage(`{"path":"README.md"}`), StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	completedRecord.State = "completed"
	completedRecord.Content = "contents"
	completedRecord.CompletedAt = time.Now().UTC()
	if _, err := sessions.FinishToolRecord(ctx, "session", completedRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.StartToolRecord(ctx, "session", session.ToolRecord{
		RunID: "run", ToolCallID: "tool-running", Name: "coding.shell", State: "running",
		Arguments: json.RawMessage(`{"command":"sleep 10"}`), StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.UpdateTodo(ctx, "session", 0, func(todo *session.TodoList) error {
		todo.Goal = "Ship"
		todo.Phases = []session.TodoPhase{{ID: "phase-1", Title: "Work", Items: []session.TodoItem{{ID: "item-1", Content: "Project state", Status: session.TodoInProgress}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	defer service.Shutdown(ctx)

	projection, err := service.RuntimeProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Session == nil || projection.Session.Version != SessionProjectionVersion {
		t.Fatalf("session projection = %#v", projection.Session)
	}
	if len(projection.Session.Blocks) != 3 || projection.Session.Blocks[1].Sequence != 1 || projection.Session.Blocks[1].TextPhase != "commentary" || projection.Session.Blocks[1].ID != "block:1" {
		t.Fatalf("typed transcript = %#v", projection.Session.Blocks)
	}
	if len(projection.Session.ToolRecords) != 2 || projection.Session.ToolRecords[0].ToolCallID != "tool-1" {
		t.Fatalf("tool records = %#v", projection.Session.ToolRecords)
	}
	if len(projection.Runs) != 1 || len(projection.Runs[0].ActiveOperations) != 1 || projection.Runs[0].ActiveOperations[0].ToolCallID != "tool-running" {
		t.Fatalf("durable active operations = %#v", projection.Runs)
	}
	if projection.Session.Todo.Revision != 1 || len(projection.PendingControls) != 1 || projection.PendingControls[0].ID != "input-1" {
		t.Fatalf("todo/controls = %#v / %#v", projection.Session.Todo, projection.PendingControls)
	}
	if projection.Runs == nil || projection.LiveBlocks == nil || projection.Recovery.State != "clear" || projection.Recovery.Items == nil {
		t.Fatalf("explicit empty domains = %#v", projection)
	}
}

func TestRuntimeProjectionPreservesLiveTextAndActiveOperationsUntilSettlement(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Live", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	defer service.Shutdown(ctx)
	started := time.Now().UTC().Add(-time.Minute)
	service.mu.Lock()
	service.activeRun = "run-live"
	service.activeSession = "session"
	service.activeRunProjection = RunProjection{
		SessionID: "session", RunID: "run-live", State: "running", Activity: RunActivityWaitingModel,
		StartedAt: started, UpdatedAt: started, LastActivityAt: started,
		ActiveOperations: []ActiveOperation{}, AllowedActions: []string{"stop", "guide"}, GuidanceOpen: true,
	}
	service.mu.Unlock()
	if !service.emit(ctx, Event{Kind: EventThinkingDelta, SessionID: "session", RunID: "run-live", Text: "reason", State: "streaming"}) ||
		!service.emit(ctx, Event{Kind: EventTextDelta, SessionID: "session", RunID: "run-live", Text: "working", TextPhase: "commentary", State: "streaming"}) {
		t.Fatal("live events were rejected")
	}
	projection, err := service.RuntimeProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Runs) != 1 || !projection.Runs[0].StartedAt.Equal(started) || projection.Runs[0].Activity != RunActivityStreaming || len(projection.LiveBlocks) != 2 {
		t.Fatalf("live projection = %#v", projection)
	}
	if !service.emit(ctx, Event{
		Kind: EventToolStarted, SessionID: "session", RunID: "run-live", ToolCallID: "tool-live", State: "running",
		Data: map[string]string{"name": "coding.shell", "arguments": `{"command":"sleep 10"}`},
	}) {
		t.Fatal("tool event was rejected")
	}
	projection, err = service.RuntimeProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.LiveBlocks) != 1 || projection.LiveBlocks[0].ToolCallID != "tool-live" || len(projection.Runs[0].ActiveOperations) != 1 || projection.Runs[0].Activity != RunActivityRunningTools {
		t.Fatalf("active operation projection = %#v", projection)
	}
	service.emitTerminal(ctx, Event{Kind: EventRunCancelled, SessionID: "session", RunID: "run-live", State: "cancelled"})
	projection, err = service.RuntimeProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.LiveBlocks) != 0 || len(projection.Runs) != 0 {
		t.Fatalf("terminal run retained live state = %#v", projection)
	}
}

func TestRuntimeEventsCarryTypedRunAndSessionProjections(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Events", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	defer service.Shutdown(ctx)
	service.mu.Lock()
	service.activeRun = "run"
	service.activeSession = "session"
	service.activeRunProjection = runProjectionForRequest(TurnRequest{SessionID: "session", Provider: "test", Model: "model"}, time.Now().UTC())
	service.activeRunProjection.RunID = "run"
	service.mu.Unlock()
	if !service.emit(ctx, Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: "delta", TextPhase: "final_answer", State: "streaming"}) {
		t.Fatal("text event was rejected")
	}
	first, err := service.NextEvent(ctx)
	if err != nil || first.Kind != EventTextDelta {
		t.Fatalf("first event = %#v, %v", first, err)
	}
	second, err := service.NextEvent(ctx)
	if err != nil || second.Kind != EventRunState || second.RunProjection == nil || second.RunProjection.Activity != RunActivityStreaming {
		t.Fatalf("run projection event = %#v, %v", second, err)
	}
	if err := service.emitSessionProjectionState(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	third, err := service.NextEvent(ctx)
	if err != nil || third.Kind != EventSessionProjection || third.SessionProjection == nil || third.SessionProjection.Version != SessionProjectionVersion {
		t.Fatalf("session projection event = %#v, %v", third, err)
	}
}

func TestRuntimeProjectionRecoversTypedApprovalAndReconcileControls(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	defer service.Shutdown(context.Background())
	service.AttachRecovery(recovery.Summary{
		ExpiredLeases: 1,
		Approvals: []recovery.PendingApproval{{
			Approval: agentruntime.ApprovalRequest{ApprovalID: "approval-1", RunID: "run-1", RiskSummary: "writes file"},
			Token:    agentruntime.ResumeToken{TokenID: "token-1"},
		}},
		ReconcileAttempts: []agentruntime.ActionAttempt{{
			AttemptID: "attempt-1", RunID: "run-1", ToolName: "coding.shell", ExecutionID: "execution-1", AttemptKind: "tool",
		}},
	})
	projection, err := service.RuntimeProjection(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Recovery.State != "attention_required" || len(projection.Recovery.Items) != 2 {
		t.Fatalf("recovery projection = %#v", projection.Recovery)
	}
	if projection.Recovery.Items[0].ID != "approval-1" || projection.Recovery.Items[0].Data["tokenId"] != "token-1" ||
		projection.Recovery.Items[1].ID != "attempt-1" || len(projection.Recovery.Items[1].AllowedActions) != 3 {
		t.Fatalf("recovery controls = %#v", projection.Recovery.Items)
	}
}

func TestRuntimeProjectionHydratesBindingWithoutDurableExecutionGraph(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "runtime-projection.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Projection", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	run, err := coding.StartRunWithMetadata(ctx, "project", map[string]string{"session_id": "session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "session", session.Block{
		Kind: "user", RunID: run.RunID, Content: "inspect", State: "submitted", Attachments: []session.Attachment{},
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, coding)
	defer service.Shutdown(ctx)
	service.mu.Lock()
	service.activeRun = run.RunID
	service.activeSession = "session"
	service.activeRunProjection = RunProjection{
		SessionID: "session", RunID: run.RunID, State: "running", Activity: RunActivityUnknown,
		ActiveOperations: []ActiveOperation{}, AllowedActions: []string{},
	}
	service.mu.Unlock()
	projection, err := service.RuntimeProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Runs) != 1 || projection.Runs[0].RunID != run.RunID || projection.Runs[0].ExecutionID != run.ExecutionID {
		t.Fatalf("runs = %#v", projection.Runs)
	}
	if projection.Runs[0].BindingState == "" || projection.Runs[0].Activity == RunActivityUnknown {
		t.Fatalf("binding projection = %#v", projection.Runs[0])
	}
}
