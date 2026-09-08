package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopclient"
	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/session"
)

func TestTUIReconnectSnapshotRestoresTypedRunTranscriptControlsTodoAndQueue(t *testing.T) {
	started := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Millisecond)
	snapshot := desktop.ReconnectSnapshot{
		DaemonEpoch: "epoch", WireSequence: 42, SelectedSessionID: "session", Base: desktop.Snapshot{
			Workspace: "/workspace", SessionID: "startup", Provider: "test", Model: "model", Reasoning: "high", AgentMode: "single", ApprovalMode: "prompt", QueueMode: "queue", AutoReviewAvailable: true,
		},
		Sessions:       []session.Session{{ID: "session", Title: "Recovered"}},
		ContextProfile: &app.ContextProfile{Source: "bootstrap", Estimated: true, Contributions: []app.ContextContribution{{Category: app.ContextCategoryCore, Name: "azem.core_instructions", Tokens: 900}}},
		Session: &app.SessionProjection{
			Version: 1, Session: session.Session{ID: "session", Title: "Recovered", ProviderID: "test", ModelID: "model", Reasoning: "high", AgentMode: "single"},
			Blocks: []app.TranscriptBlock{
				{ID: "block:1", Sequence: 1, Kind: "user", RunID: "run", Content: "task", Attachments: []session.Attachment{}, Data: map[string]string{}},
				{ID: "block:2", Sequence: 2, Kind: "commentary", RunID: "run", Content: "working", TextPhase: "commentary", State: "completed", Attachments: []session.Attachment{}, Data: map[string]string{}},
			},
			ToolRecords:    []session.ToolRecord{{RunID: "run", ToolCallID: "tool", AnchorSequence: 2, Name: "coding.shell", State: "running", Arguments: json.RawMessage(`{"command":"sleep 10"}`), StartedAt: started}},
			Todo:           session.TodoList{Revision: 3, Phases: []session.TodoPhase{{ID: "phase", Title: "Work", Items: []session.TodoItem{{ID: "todo", Content: "Observe", Status: session.TodoInProgress}}}}},
			AgentSnapshots: []app.AgentSnapshotPayload{{ID: "child", State: "running", Agent: app.AgentStatePayload{Description: "review"}}},
			Usage:          session.Usage{}, LastRunID: "run",
		},
		Runs: []app.RunProjection{{
			SessionID: "session", RunID: "run", State: "running", StartedAt: started, UpdatedAt: started, LastActivityAt: started,
			Activity: app.RunActivityRunningTools, GuidanceOpen: true, HasActiveChildren: true,
			Provider: "test", Model: "model", ActiveOperations: []app.ActiveOperation{{ID: "tool:tool", ToolCallID: "tool", Name: "coding.shell", State: "running", StartedAt: started}},
			AllowedActions: []string{"stop", "guide"},
		}},
		LiveBlocks: []app.LiveBlockProjection{
			{ID: "session:run::tool::tool", SessionID: "session", RunID: "run", Kind: "tool", ToolCallID: "tool", State: "running", Data: map[string]string{"name": "coding.shell"}, UpdatedAt: started},
			{ID: "session:run::text", SessionID: "session", RunID: "run", Kind: "text", TextPhase: "commentary", Content: "live progress", State: "streaming", Data: map[string]string{}, UpdatedAt: started},
			{ID: "session:run:child:thinking", SessionID: "session", RunID: "run", AgentID: "child", Kind: "thinking", Content: "private child thought", State: "streaming", Data: map[string]string{}, UpdatedAt: started},
		},
		Controls:        []app.PendingControlProjection{{Kind: "approval", ID: "approval", SessionID: "session", RunID: "run", ToolCallID: "tool", State: "pending", Data: map[string]string{"tool": "coding.shell"}, AllowedActions: []string{"approve", "deny"}}},
		PromptQueues:    []session.PromptQueueV1{{Version: 1, SessionID: "session", Revision: 4, State: session.PromptQueueActive, Items: []session.QueuedPromptV1{{ID: "queued", Text: "next", State: session.QueuedPromptQueued, CreatedAt: started, UpdatedAt: started}}}},
		RuntimeRecovery: app.RecoveryProjection{State: "clear", Items: []app.PendingControlProjection{}},
	}
	model := NewModel(inertRuntime{}, "/workspace", "test", "model", "high", "single", "session")
	model.composer.SetValue("local draft")
	model.ApplyReconnectSnapshot(snapshot)
	if model.sessionID != "session" || model.runID != "run" || !model.runStartedAt.Equal(started) || model.lastWireCursor != 42 {
		t.Fatalf("restored identity = session:%q run:%q started:%s cursor:%d", model.sessionID, model.runID, model.runStartedAt, model.lastWireCursor)
	}
	if len(model.transcript) != 4 || model.transcript[1].TextPhase != "commentary" || model.transcript[2].ToolCallID != "tool" ||
		model.transcript[3].TextPhase != "commentary" || model.transcript[3].Content != "live progress" {
		t.Fatalf("restored transcript = %#v", model.transcript)
	}
	if model.todo.Revision != 3 || len(model.agents) != 1 || model.approval == nil || model.currentPromptQueue().Revision != 4 {
		t.Fatalf("restored todo/agents/control/queue = %#v %#v %#v %#v", model.todo, model.agents, model.approval, model.currentPromptQueue())
	}
	if model.contextProfile.Source != "bootstrap" || model.contextProfile.TotalTokens() != 900 {
		t.Fatalf("restored preloaded context profile = %+v", model.contextProfile)
	}
	if model.composer.Value() != "local draft" {
		t.Fatalf("snapshot discarded local draft: %q", model.composer.Value())
	}
}

func TestTUIReconnectDoesNotTreatReconcileRequiredHistoryAsRunning(t *testing.T) {
	snapshot := desktop.ReconnectSnapshot{
		DaemonEpoch: "epoch", SelectedSessionID: "session", Base: desktop.Snapshot{
			Workspace: "/workspace", SessionID: "session", Provider: "test", Model: "model", Reasoning: "high", AgentMode: "single",
		},
		Session: &app.SessionProjection{
			Version: 1, Session: session.Session{ID: "session"},
			Blocks: []app.TranscriptBlock{{
				ID: "answer", Sequence: 1, Kind: "assistant", RunID: "stale-run",
				Content: "partial", State: "streaming", Attachments: []session.Attachment{}, Data: map[string]string{},
			}},
			ToolRecords: []session.ToolRecord{}, Todo: session.TodoList{Phases: []session.TodoPhase{}},
			AgentSnapshots: []app.AgentSnapshotPayload{}, LastRunID: "stale-run",
		},
		Runs: []app.RunProjection{{
			SessionID: "session", RunID: "stale-run", State: "reconcile_required",
			BindingState: "reconcile_required", Activity: app.RunActivityRecovering,
			ActiveOperations: []app.ActiveOperation{}, AllowedActions: []string{},
		}},
		LiveBlocks: []app.LiveBlockProjection{}, Controls: []app.PendingControlProjection{},
		PromptQueues: []session.PromptQueueV1{}, RuntimeRecovery: app.RecoveryProjection{State: "attention_required", Items: []app.PendingControlProjection{}},
	}
	model := NewModel(inertRuntime{}, "/workspace", "test", "model", "high", "single", "session")
	model.ApplyReconnectSnapshot(snapshot)
	if model.runID != "" || model.status != "Ready" {
		t.Fatalf("reconcile-required history looked active: run=%q status=%q", model.runID, model.status)
	}
	if len(model.transcript) != 1 || model.transcript[0].State != "interrupted" {
		t.Fatalf("stale live block = %#v", model.transcript)
	}
}

func TestTUIForeignSelectionDoesNotNavigateAndReconnectPreservesRunAndDraft(t *testing.T) {
	model := NewModel(inertRuntime{}, "/workspace", "test", "model", "high", "single", "session-a")
	model.runs["run-a"] = app.RunProjection{SessionID: "session-a", RunID: "run-a", State: "running", Activity: app.RunActivityThinking, AllowedActions: []string{"stop"}}
	model.applyCurrentRunProjection(model.runs["run-a"])
	model.composer.SetValue("draft")
	foreign := app.SessionProjection{Version: 1, Session: session.Session{ID: "session-b"}, Blocks: []app.TranscriptBlock{}, ToolRecords: []session.ToolRecord{}, Todo: session.TodoList{Phases: []session.TodoPhase{}}, AgentSnapshots: []app.AgentSnapshotPayload{}}
	model.applyEvent(app.Event{Kind: app.EventSessionProjection, SessionID: "session-b", SessionProjection: &foreign})
	if model.sessionID != "session-a" {
		t.Fatalf("foreign selection navigated TUI to %q", model.sessionID)
	}
	model.applyEvent(app.Event{Kind: connectionStateEvent, State: string(desktopclient.ConnectionReconnecting), Text: "network", Data: map[string]string{"wireSequence": "9"}})
	if model.runID != "run-a" || model.composer.Value() != "draft" || model.mutationsEnabled() {
		t.Fatalf("reconnect changed confirmed state: run=%q draft=%q enabled=%t", model.runID, model.composer.Value(), model.mutationsEnabled())
	}
	updated, cmd := model.submit()
	model = updated.(AppModel)
	if cmd != nil || model.composer.Value() != "draft" {
		t.Fatalf("offline submit lost draft: cmd=%v draft=%q", cmd != nil, model.composer.Value())
	}
}

type queueProjectionRuntime struct {
	recordedRuntime
	queue                    session.PromptQueueV1
	mutations                []app.PromptQueueMutation
	guidance                 []string
	cancelSession, cancelRun string
}

func (runtime *queueProjectionRuntime) Request(ctx context.Context, method desktopipc.Method, payload any, target any) error {
	if method != desktopipc.MethodMutatePromptQueue {
		return runtime.recordedRuntime.Request(ctx, method, payload, target)
	}
	encoded, _ := json.Marshal(payload)
	var mutation app.PromptQueueMutation
	_ = json.Unmarshal(encoded, &mutation)
	runtime.mutations = append(runtime.mutations, mutation)
	runtime.queue.Revision++
	switch mutation.Operation {
	case app.PromptQueueEnqueue:
		runtime.queue.Items = append(runtime.queue.Items, mutation.Item)
	case app.PromptQueuePause:
		runtime.queue.State, runtime.queue.PauseReason = session.PromptQueuePaused, mutation.PauseReason
	case app.PromptQueueResume:
		runtime.queue.State, runtime.queue.PauseReason = session.PromptQueueActive, ""
	case app.PromptQueueRemove:
		index := queueItemTestIndex(runtime.queue.Items, mutation.ItemID)
		if index >= 0 {
			runtime.queue.Items = append(runtime.queue.Items[:index], runtime.queue.Items[index+1:]...)
		}
	case app.PromptQueueRetry:
		index := queueItemTestIndex(runtime.queue.Items, mutation.ItemID)
		if index >= 0 {
			runtime.queue.Items[index].State, runtime.queue.Items[index].Error = session.QueuedPromptQueued, ""
		}
	case app.PromptQueueUpdate:
		index := queueItemTestIndex(runtime.queue.Items, mutation.ItemID)
		if index >= 0 {
			runtime.queue.Items[index].Text = mutation.Item.Text
		}
	}
	encoded, _ = json.Marshal(runtime.queue)
	return json.Unmarshal(encoded, target)
}

func (runtime *queueProjectionRuntime) GuideActiveTurnWithAttachments(_, _ string, text string, _ []session.Attachment) error {
	runtime.guidance = append(runtime.guidance, text)
	return nil
}

func (runtime *queueProjectionRuntime) CancelRunWithChildren(sessionID, runID string, children bool) (bool, error) {
	runtime.cancelSession, runtime.cancelRun, runtime.cancelChildren = sessionID, runID, children
	return true, nil
}

func TestTUIDeliveryQueueGuideEditingAndExactStop(t *testing.T) {
	runtime := &queueProjectionRuntime{queue: session.PromptQueueV1{Version: 1, SessionID: "session", Revision: 2, State: session.PromptQueueActive, Items: []session.QueuedPromptV1{}}}
	model := NewModel(runtime, "/workspace", "test", "model", "high", "single", "session")
	run := app.RunProjection{SessionID: "session", RunID: "run", State: "running", Activity: app.RunActivityThinking, GuidanceOpen: true, AllowedActions: []string{"stop", "guide"}}
	model.runs["run"] = run
	model.applyCurrentRunProjection(run)
	model.promptQueues["session"] = runtime.queue
	model.composer.SetValue("queued task")
	updated, cmd := model.submit()
	model = updated.(AppModel)
	if cmd == nil || len(model.transcript) != 0 {
		t.Fatalf("queue submission command=%v transcript=%#v", cmd != nil, model.transcript)
	}
	updated, _ = model.Update(cmd())
	model = updated.(AppModel)
	if len(runtime.mutations) != 1 || runtime.mutations[0].Operation != app.PromptQueueEnqueue || model.currentPromptQueue().Items[0].Text != "queued task" {
		t.Fatalf("queue mutation=%#v projection=%#v", runtime.mutations, model.currentPromptQueue())
	}

	updated, deliveryCmd := model.executeCommand(Command{Name: "delivery", Args: []string{"guide"}})
	model = updated.(AppModel)
	updated, _ = model.Update(deliveryCmd())
	model = updated.(AppModel)
	model.composer.SetValue("guide now")
	updated, cmd = model.submit()
	model = updated.(AppModel)
	result := cmd().(guidanceResultMsg)
	if result.Err != nil || len(runtime.guidance) != 1 || runtime.guidance[0] != "guide now" {
		t.Fatalf("guide result=%#v guidance=%#v", result, runtime.guidance)
	}

	model.openOverlay(OverlayQueue)
	model.overlayCursor = 0
	updated, cmd = model.updateQueueOverlayKey("e")
	model = updated.(AppModel)
	if model.queueEditItemID == "" || model.composer.Value() != "queued task" {
		t.Fatalf("queue edit state item=%q composer=%q", model.queueEditItemID, model.composer.Value())
	}
	model.composer.SetValue("edited task")
	updated, cmd = model.submit()
	model = updated.(AppModel)
	updated, _ = model.Update(cmd())
	model = updated.(AppModel)
	if model.currentPromptQueue().Items[0].Text != "edited task" {
		t.Fatalf("edited queue = %#v", model.currentPromptQueue())
	}

	updated, cancelCmd := model.requestTurnCancellation()
	model = updated.(AppModel)
	if cancelCmd == nil {
		t.Fatal("exact stop returned no command")
	}
	_ = cancelCmd()
	if runtime.cancelSession != "session" || runtime.cancelRun != "run" {
		t.Fatalf("exact stop session=%q run=%q", runtime.cancelSession, runtime.cancelRun)
	}

	model.openOverlay(OverlayQueue)
	updated, pauseCmd := model.updateQueueOverlayKey("p")
	model = updated.(AppModel)
	updated, _ = model.Update(pauseCmd())
	model = updated.(AppModel)
	if model.currentPromptQueue().State != session.PromptQueuePaused {
		t.Fatalf("paused queue = %#v", model.currentPromptQueue())
	}
	_, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
}

func queueItemTestIndex(items []session.QueuedPromptV1, itemID string) int {
	for index := range items {
		if items[index].ID == itemID {
			return index
		}
	}
	return -1
}
