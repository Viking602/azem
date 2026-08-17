package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
	backgroundservice "github.com/Viking602/azem/internal/background"
	"github.com/Viking602/azem/internal/session"
)

func TestLateRunDeltaIsDiscarded(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.runID = "current"
	model.applyEvent(app.Event{Kind: app.EventTextDelta, SessionID: "default", RunID: "old", Text: "stale"})
	if len(model.transcript) != 0 {
		t.Fatalf("late event added %d transcript blocks", len(model.transcript))
	}
	model.applyEvent(app.Event{Kind: app.EventTextDelta, SessionID: "default", RunID: "current", Text: "fresh"})
	if len(model.transcript) != 1 || model.transcript[0].Content != "fresh" {
		t.Fatalf("current event transcript = %#v", model.transcript)
	}
}

func TestAutoWakeRunStartedAfterParentFailureBecomesVisibleAndAcceptsGuidance(t *testing.T) {
	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.runID = "parent-run"
	model.applyEvent(app.Event{Kind: app.EventRunFailed, SessionID: "default", RunID: "parent-run", Text: "stream interrupted"})
	if model.status != "Failed" || model.runID != "" {
		t.Fatalf("parent terminal state runID=%q status=%q", model.runID, model.status)
	}

	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "auto-wake-run"})
	if model.status != "Running" || model.runID != "auto-wake-run" {
		t.Fatalf("auto-wake run was hidden: runID=%q status=%q", model.runID, model.status)
	}

	model.composer.SetValue("继续")
	updated, cmd := model.submit()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("guidance for auto-wake run returned no command")
	}
	message := cmd()
	result, ok := message.(guidanceResultMsg)
	if !ok || result.Err != nil || result.RunID != "auto-wake-run" || result.Text != "继续" {
		t.Fatalf("guidance result = %#v", message)
	}
	if len(runtime.guidance) != 1 || runtime.guidance[0] != "继续" {
		t.Fatalf("auto-wake guidance = %#v", runtime.guidance)
	}
}

func TestForegroundChildCancellationPromptsForScope(t *testing.T) {
	runtime := &recordedRuntime{foregroundChildren: true}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	updated, cmd := model.requestTurnCancellation()
	model = updated.(AppModel)
	if cmd != nil || model.overlay != OverlayCancel || model.status != "Choose cancellation scope" {
		t.Fatalf("cancel prompt overlay=%q status=%q cmd=%v", model.overlay, model.status, cmd)
	}
	model.overlayCursor = 1
	updated, cmd = model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil || model.overlay != OverlayNone || model.status != "Cancelling" {
		t.Fatalf("cancel selection overlay=%q status=%q cmd=%v", model.overlay, model.status, cmd)
	}
	message := cmd()
	if result, ok := message.(cancelResultMsg); !ok || !result.Cancelled || !runtime.cancelled || !runtime.cancelChildren {
		t.Fatalf("cancel result=%#v runtime=%#v", message, runtime)
	}
}

func TestBackgroundOnlyChildCancellationPromptsAndCancelsAll(t *testing.T) {
	runtime := &recordedRuntime{backgroundChildren: true}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	updated, cmd := model.requestTurnCancellation()
	model = updated.(AppModel)
	if cmd != nil || model.overlay != OverlayCancel {
		t.Fatalf("background child did not prompt: overlay=%q cmd=%v", model.overlay, cmd)
	}
	options := model.overlayOptions()
	if len(options) != 2 || options[0].Label != "Cancel current agent only" || options[1].Label != "Cancel current agent and all child agents" {
		t.Fatalf("cancellation choices = %#v", options)
	}
	model.overlayCursor = 1
	updated, cmd = model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("cancel-all choice returned no command")
	}
	message := cmd()
	if result, ok := message.(cancelResultMsg); !ok || !result.Cancelled || !runtime.cancelChildren {
		t.Fatalf("cancel result=%#v runtime=%#v", message, runtime)
	}
}

func TestTerminalEventClearsActiveRunAndRejectsLateDelta(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Starting"
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-1"})
	model.applyEvent(app.Event{Kind: app.EventRunFinished, SessionID: "default", RunID: "run-1"})
	if model.runID != "" || model.lastRunID != "run-1" {
		t.Fatalf("runID=%q lastRunID=%q", model.runID, model.lastRunID)
	}
	model.applyEvent(app.Event{Kind: app.EventTextDelta, SessionID: "default", RunID: "run-1", Text: "late"})
	if len(model.transcript) != 0 {
		t.Fatalf("late terminal delta added transcript: %#v", model.transcript)
	}

	model.status = "Starting"
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-2"})
	if model.runID != "run-2" || model.status != "Running" {
		t.Fatalf("next runID=%q status=%q", model.runID, model.status)
	}
}

func TestTerminalEventReactivatesComposer(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.runID = "run-1"
	model.transcript = []Block{{Kind: BlockTool, State: "completed"}}
	if !model.selectTranscript() || model.composer.Focused() {
		t.Fatal("test setup did not move focus away from the composer")
	}

	model.applyEvent(app.Event{Kind: app.EventRunFinished, SessionID: "default", RunID: "run-1"})
	if model.focus != focusComposer || !model.composer.Focused() {
		t.Fatalf("composer focus after completion = area:%d focused:%v", model.focus, model.composer.Focused())
	}
	updated, _ := model.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	model = updated.(AppModel)
	if got := model.composer.Value(); got != "x" {
		t.Fatalf("composer value after completion = %q, want keyboard input", got)
	}
}

func TestAgentLifecycleUpdatesSingleAgentBlock(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{
		Kind: app.EventAgentState, SessionID: "default", AgentID: "child-1", State: "running",
		Agent: &app.AgentStatePayload{Type: "review", ParentRunID: "parent-run", Activity: "reviewing"},
	})
	model.applyEvent(app.Event{
		Kind: app.EventAgentState, SessionID: "default", RunID: "parent-run", AgentID: "child-1", State: "completed", Text: "done",
		Agent: &app.AgentStatePayload{Type: "review", ParentRunID: "parent-run", Activity: "done"},
	})

	if len(model.agents) != 1 || model.agents[0].State != "completed" {
		t.Fatalf("agents = %#v", model.agents)
	}
	if len(model.transcript) != 1 || model.transcript[0].Kind != BlockAgent || model.transcript[0].State != "completed" {
		t.Fatalf("agent blocks = %#v", model.transcript)
	}
}

func TestActiveAgentsExcludeTerminalHistory(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	for index, state := range []string{"initializing", "queued", "running", "cancelling", "completed", "failed", "cancelled", "interrupted"} {
		model.agents = append(model.agents, AgentView{ID: string(rune('a' + index)), State: state})
	}
	if active := model.activeAgents(); len(active) != 4 {
		t.Fatalf("active agents = %#v", active)
	}
}

func TestRecoveryEventOpensExplicitApprovalAndReconcileInterface(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{Kind: app.EventRecoveryState, SessionID: "default", State: "attention_required", Data: map[string]string{
		"runs":  "1",
		"items": `[{"kind":"approval","id":"approval-1","runId":"run-1","taskId":"task-1","title":"Pending approval","detail":"writes note.txt","state":"pending"},{"kind":"reconcile","id":"attempt-1","runId":"run-1","taskId":"task-1","title":"Unknown side effect","detail":"check external result","state":"unknown","toolName":"coding.shell"}]`,
	}})
	if model.overlay != OverlayRecovery || model.status != "Recovery attention" || len(model.recovery) != 2 {
		t.Fatalf("recovery state = overlay:%q status:%q items:%+v", model.overlay, model.status, model.recovery)
	}
	updated, _ := model.updateOverlayKey("enter")
	model = updated.(AppModel)
	if model.overlay != OverlayApproval || model.approval == nil || model.approval.ToolCallID != "approval-1" {
		t.Fatalf("approval projection = overlay:%q approval:%+v", model.overlay, model.approval)
	}

	_ = model.closeOverlay()
	model.openOverlay(OverlayRecovery)
	model.overlayCursor = 1
	updated, _ = model.updateOverlayKey("enter")
	model = updated.(AppModel)
	if !strings.Contains(model.errorBanner, "/reconcile attempt-1") {
		t.Fatalf("reconcile guidance = %q", model.errorBanner)
	}

	updated, cmd := model.executeCommand(Command{Name: "reconcile", Args: []string{"attempt-1", "succeeded"}})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("reconcile command did not execute an action")
	}
	msg := cmd()
	result, ok := msg.(actionResultMsg)
	if !ok || result.Err != nil {
		t.Fatalf("reconcile action result = %#v", msg)
	}
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionReconcileAttempt || runtime.actions[0].Target != "attempt-1" || runtime.actions[0].Decision != "succeeded" {
		t.Fatalf("reconcile actions = %+v", runtime.actions)
	}
}

func TestRecoveredApprovalResolutionReturnsToIdleState(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{Kind: app.EventRecoveryState, SessionID: "default", State: "attention_required", Data: map[string]string{
		"runs":  "1",
		"items": `[{"kind":"approval","id":"approval-1","runId":"run-1","taskId":"task-1","title":"Pending approval","detail":"writes note.txt","state":"pending"}]`,
	}})
	updated, _ := model.updateOverlayKey("enter")
	model = updated.(AppModel)
	updated, cmd := model.updateOverlayKey("d")
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("recovered approval denial did not execute")
	}
	updated, _ = model.Update(cmd())
	model = updated.(AppModel)
	if model.status != "Ready" || model.overlay != OverlayNone || model.approval != nil || len(model.recovery) != 0 {
		t.Fatalf("resolved recovery state = status:%q overlay:%q approval:%+v recovery:%+v", model.status, model.overlay, model.approval, model.recovery)
	}
}

func TestAutomaticRecoveryWithoutPendingWorkRemainsReady(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{Kind: app.EventRecoveryState, State: "attention_required", Data: map[string]string{
		"runs": "1", "items": `[]`,
	}})
	if model.status != "Ready" || model.overlay != OverlayNone {
		t.Fatalf("automatic recovery should remain idle: status=%q overlay=%q", model.status, model.overlay)
	}
}

func TestSessionListEventOpensSessionsWithoutDecodingBlocks(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{Kind: app.EventSessionLoaded, SessionID: "default", State: "list", Data: map[string]string{
		"sessions": `[{"id":"session-1","title":"First","providerId":"chatgpt","modelId":"gpt-test","updatedAt":"2026-07-16T00:00:00Z"}]`,
	}})
	if model.overlay != OverlaySessions || len(model.sessions) != 1 || model.errorBanner != "" {
		t.Fatalf("session list state = overlay:%q sessions:%+v error:%q", model.overlay, model.sessions, model.errorBanner)
	}
}

func TestResumeCommandOpensPickerAndResumesSelectedSession(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")

	updated, listCmd := model.executeCommand(Command{Name: "resume"})
	model = updated.(AppModel)
	if listCmd == nil {
		t.Fatal("/resume did not request saved sessions")
	}
	updated, _ = model.Update(listCmd())
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionListSessions {
		t.Fatalf("/resume actions = %+v", runtime.actions)
	}

	model.applyEvent(app.Event{Kind: app.EventSessionLoaded, SessionID: model.sessionID, State: "list", Data: map[string]string{
		"sessions": `[{"id":"session-1","title":"First","providerId":"chatgpt","modelId":"gpt-test"},{"id":"session-2","title":"Second","providerId":"grok","modelId":"grok-test"}]`,
	}})
	updated, _ = model.updateOverlayKey("down")
	model = updated.(AppModel)
	updated, resumeCmd := model.updateOverlayKey("enter")
	model = updated.(AppModel)
	if resumeCmd == nil {
		t.Fatal("session selection did not start resume")
	}
	updated, _ = model.Update(resumeCmd())
	model = updated.(AppModel)

	if len(runtime.actions) != 2 || runtime.actions[1].Kind != ActionResumeSession || runtime.actions[1].Target != "session-2" {
		t.Fatalf("picker actions = %+v", runtime.actions)
	}
	if model.overlay != OverlayNone || model.actionBusy {
		t.Fatalf("picker completion = overlay:%q busy:%v", model.overlay, model.actionBusy)
	}
}

func TestSessionTransitionAdoptsNewIDAndClearsPriorState(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single", "startup-session")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: "old conversation"}}
	model.agents = []AgentView{{ID: "old-agent"}}
	model.usage = UsageView{InputTokens: 99, OutputTokens: 42}
	model.todo = TodoView{Revision: 1, Phases: []session.TodoPhase{{Items: []session.TodoItem{{
		ID: "old-todo", Content: "old task", Status: session.TodoPending,
	}}}}}
	model.todoExpanded = true
	model.focus = focusTodo
	model.composer.Blur()
	_ = model.View()

	model.applyEvent(app.Event{
		Kind: app.EventSessionLoaded, SessionID: "next-session", State: "new",
		Data: map[string]string{"blocks": "[]", "provider": "grok", "model": "grok-model", "reasoning": "medium", "agentMode": "team"},
	})

	if model.sessionID != "next-session" || len(model.transcript) != 0 || len(model.agents) != 0 {
		t.Fatalf("session transition = id:%q transcript:%+v agents:%+v", model.sessionID, model.transcript, model.agents)
	}
	if model.usage.InputTokens != 0 || model.usage.OutputTokens != 0 {
		t.Fatalf("new session retained usage: %+v", model.usage)
	}
	if model.todoItemCount() != 0 || model.todoExpanded || model.focus != focusComposer || !model.composer.Focused() {
		t.Fatalf("new session retained todo presentation state: items:%d expanded:%t focus:%d composer:%t",
			model.todoItemCount(), model.todoExpanded, model.focus, model.composer.Focused())
	}
	if model.paint.todoRender != "" {
		t.Fatalf("new session retained cached todo render: %q", ansi.Strip(model.paint.todoRender))
	}
	if model.provider != "grok" || model.model != "grok-model" || model.reasoning != "medium" || model.agentMode != "team" {
		t.Fatalf("new session preferences = %s/%s %s %s", model.provider, model.model, model.reasoning, model.agentMode)
	}
}

func TestSessionTransitionInvalidatesScrollMetricsBeforeNextFrame(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: "short session"}}
	_ = model.View()

	blocks := make([]map[string]string, 80)
	for index := range blocks {
		blocks[index] = map[string]string{
			"kind": "assistant", "content": fmt.Sprintf("message %02d %s", index, strings.Repeat("history ", 8)),
		}
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	model.applyEvent(app.Event{
		Kind: app.EventSessionLoaded, SessionID: "long-session", State: "loaded",
		Data: map[string]string{"blocks": string(encoded)},
	})

	model.scrollTranscript(3)
	if model.transcriptTop == 0 {
		t.Fatal("first wheel event after session switch was clamped by stale scroll metrics")
	}
	_ = model.View()
	for range 500 {
		model.scrollTranscript(3)
	}
	if model.transcriptTop != model.transcriptMaxOffset() {
		t.Fatalf("oldest offset = %d, want %d", model.transcriptTop, model.transcriptMaxOffset())
	}
	width, height := model.transcriptViewportSize()
	if oldest := ansi.Strip(model.renderTranscript(width, height)); !strings.Contains(oldest, "message 00") {
		t.Fatalf("scrolling did not reach oldest session content:\n%s", oldest)
	}
}

func TestSessionReloadRestoresContextUsageFooter(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.selectModels([]ModelChoice{{ID: "gpt-main", ContextWindow: 272_000, SupportsReasoning: true}})
	model.applyEvent(app.Event{
		Kind: app.EventSessionLoaded, SessionID: "restored", State: "loaded",
		Data: map[string]string{
			"blocks":   `[{"kind":"user","title":"You","content":"hello"}]`,
			"provider": "chatgpt",
			"model":    "gpt-main",
			"usage":    `{"inputTokens":68000,"outputTokens":4000,"cacheInputTokens":68000,"cachedInputTokens":34000,"mainCacheInput":68000,"mainCachedInput":34000,"contextLimit":272000,"cacheReported":true,"mainCacheReported":true}`,
		},
	})
	if model.usage.InputTokens != 68000 || model.usage.OutputTokens != 4000 {
		t.Fatalf("restored occupancy = %+v", model.usage)
	}
	if model.usage.MainCacheInput != 68000 || model.usage.MainCachedInput != 34000 || !model.usage.MainCacheReported {
		t.Fatalf("restored cache = %+v", model.usage)
	}
	if model.usage.ContextLimit != 272_000 {
		t.Fatalf("restored context limit = %d", model.usage.ContextLimit)
	}
	footer := ansi.Strip(model.renderContextUsage(120))
	for _, wanted := range []string{"72K / 272K", "CACHE", "50.0%"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("restored usage footer missing %q: %q", wanted, footer)
		}
	}
}

func TestSessionReloadKeepsCompleteFailedOutput(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	completeOutput := strings.Repeat("complete failed output 0123456789\n", 20_000)
	blocks, err := json.Marshal([]session.Block{{
		Kind: "assistant", RunID: "failed-run", Title: "Azem", Content: completeOutput, State: "failed",
	}})
	if err != nil {
		t.Fatal(err)
	}
	model.applyEvent(app.Event{
		Kind: app.EventSessionLoaded, SessionID: "restored", State: "loaded",
		Data: map[string]string{"blocks": string(blocks)},
	})
	if len(model.transcript) != 1 || model.transcript[0].State != "failed" || model.transcript[0].Content != completeOutput {
		gotBytes := 0
		if len(model.transcript) > 0 {
			gotBytes = len(model.transcript[0].Content)
		}
		t.Fatalf("restored transcript blocks=%d output_bytes=%d want_bytes=%d", len(model.transcript), gotBytes, len(completeOutput))
	}
}

func TestSessionReloadRestoresDurableToolTimelineInSequence(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	tools, err := json.Marshal([]session.ToolRecord{
		{
			SessionID: "restored", RunID: "run-1", ToolCallID: "read-1", AnchorSequence: 1,
			Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"note.txt"}`), State: session.ToolCompleted, Content: "hello",
		},
		{
			SessionID: "restored", RunID: "run-1", ToolCallID: "edit-1", AnchorSequence: 1,
			Name: "coding.edit_hashline", Arguments: json.RawMessage(`{"input":"patch"}`), State: session.ToolInterrupted,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model.applyEvent(app.Event{
		Kind: app.EventSessionLoaded, SessionID: "restored", State: "loaded",
		Data: map[string]string{
			"blocks":         `[{"kind":"user","runId":"run-1","content":"change note"},{"kind":"assistant","runId":"run-1","content":"done","state":"completed"}]`,
			"blockSequences": `[1,2]`,
			"toolRecords":    string(tools),
		},
	})
	if len(model.transcript) != 4 {
		t.Fatalf("restored transcript=%#v", model.transcript)
	}
	if model.transcript[0].Kind != BlockUser || model.transcript[1].ID != "read-1" ||
		model.transcript[2].ID != "edit-1" || model.transcript[3].Kind != BlockAssistant {
		t.Fatalf("restored timeline order=%#v", model.transcript)
	}
	if model.transcript[1].State != "completed" || !model.transcript[1].Collapsed ||
		model.transcript[2].State != "cancelled" {
		t.Fatalf("restored tool states=%#v", model.transcript[1:3])
	}
}

func TestQuitWaitsForRuntimeShutdownBeforeTeaQuit(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, cmd := model.updateKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	model = updated.(AppModel)
	if cmd == nil || !model.quitting || model.status != "Shutting down" {
		t.Fatalf("shutdown state = cmd:%v quitting:%v status:%q", cmd != nil, model.quitting, model.status)
	}
	if runtime.shutdown {
		t.Fatal("shutdown ran before command execution")
	}
	msg := cmd()
	if _, ok := msg.(shutdownResultMsg); !ok || !runtime.shutdown {
		t.Fatalf("shutdown result = %#v shutdown=%v", msg, runtime.shutdown)
	}
	_, quit := model.Update(msg)
	if quit == nil {
		t.Fatal("shutdown completion did not return tea.Quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatalf("shutdown command = %#v", quit())
	}
}

func TestChildStreamsStayNestedAndDetailReplacesSnapshot(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.runID = "parent-run"
	model.applyEvent(app.Event{
		Kind: app.EventAgentState, SessionID: "default", RunID: "parent-run", AgentID: "child-1", State: "running",
		Text: "running", Agent: &app.AgentStatePayload{
			Type: "explore", Description: "inspect files", Model: "child-model", CapabilityMode: "read-only",
			RequestedIsolation: "worktree", Isolation: "worktree", CWD: "/tmp/worktree", ParentRunID: "parent-run",
			ParentToolCallID: "spawn-1", Activity: "starting",
		},
	})
	model.applyEvent(app.Event{Kind: app.EventThinkingDelta, SessionID: "default", RunID: "child-run", AgentID: "child-1", Text: "checking"})
	model.applyEvent(app.Event{Kind: app.EventTextDelta, SessionID: "default", RunID: "child-run", AgentID: "child-1", Text: "found"})
	model.applyEvent(app.Event{
		Kind: app.EventToolStarted, SessionID: "default", RunID: "child-run", AgentID: "child-1",
		ToolCallID: "call-1", Data: map[string]string{"name": "coding.read_file", "arguments": `{"path":"a"}`},
	})
	model.applyEvent(app.Event{
		Kind: app.EventToolUpdate, SessionID: "default", RunID: "child-run", AgentID: "child-1",
		ToolCallID: "call-1", Text: "reading", Data: map[string]string{"name": "coding.read_file"},
	})
	model.applyEvent(app.Event{
		Kind: app.EventToolFinished, SessionID: "default", RunID: "child-run", AgentID: "child-1",
		ToolCallID: "call-1", State: "completed", Text: "contents", Data: map[string]string{"name": "coding.read_file"},
	})
	if len(model.transcript) != 1 || model.transcript[0].Kind != BlockAgent {
		t.Fatalf("child stream leaked into parent transcript: %#v", model.transcript)
	}
	if len(model.agents) != 1 || len(model.agents[0].Blocks) != 3 {
		t.Fatalf("nested child blocks = %#v", model.agents)
	}
	toolBlock := model.agents[0].Blocks[2]
	if toolBlock.Kind != BlockTool || toolBlock.State != "completed" ||
		toolBlock.Content != "Read a" {
		t.Fatalf("nested tool block = %#v", toolBlock)
	}

	model.applyEvent(app.Event{
		Kind: app.EventAgentDetail, SessionID: "default", AgentID: "child-1", State: "detail",
		AgentBlocks: []app.AgentTranscriptBlock{{ID: "msg-0-user", Kind: "user", Content: "fresh transcript", State: "completed"}},
	})
	if model.overlay != OverlayAgentDetail || model.detailAgentID != "child-1" ||
		len(model.agents[0].Blocks) != 1 || model.agents[0].Blocks[0].Content != "fresh transcript" {
		t.Fatalf("detail projection = overlay:%q detail:%q agents:%#v", model.overlay, model.detailAgentID, model.agents)
	}
	content := ansi.Strip(model.renderOverlay(120, 32))
	for _, wanted := range []string{"TASK DETAIL", "inspect files", "child-model", "/tmp/worktree", "fresh transcript"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("detail overlay missing %q:\n%s", wanted, content)
		}
	}
	if firstLine := strings.Split(content, "\n")[0]; !strings.Contains(firstLine, "TASK DETAIL") || strings.Contains(content, "┌") {
		t.Fatalf("agent detail is still a centered modal instead of a full-size workspace:\n%s", content)
	}
}

func TestAgentDetailUsesMainTranscriptRendering(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.detailAgentID = "child-1"
	model.agents = []AgentView{{
		ID: "child-1", Role: "review", State: "running", Description: "review provider changes",
		Blocks: []Block{{
			Kind: BlockTool, Title: "coding.git_diff", State: "running",
			Content: "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new",
		}},
	}}
	model.openOverlay(OverlayAgentDetail)
	rendered := ansi.Strip(model.renderOverlay(120, 32))
	if !strings.Contains(rendered, "View Git Diff") || strings.Contains(rendered, "VIEW GIT DIFF · RUNNING") {
		t.Fatalf("agent detail did not reuse the main transcript tool renderer:\n%s", rendered)
	}
	for _, viewport := range [][2]int{{120, 32}, {40, 12}} {
		output := model.renderOverlay(viewport[0], viewport[1])
		lines := strings.Split(output, "\n")
		if len(lines) != viewport[1] {
			t.Fatalf("agent detail height at %dx%d = %d", viewport[0], viewport[1], len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != viewport[0] {
				t.Fatalf("agent detail width at %dx%d = %d", viewport[0], viewport[1], ansi.StringWidth(line))
			}
		}
	}
}

func TestAgentDetailPreservesToolCollapseState(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	agent := AgentView{ID: "child-1", Blocks: []Block{
		{Kind: BlockTool, Title: "coding.shell", State: "completed", Collapsed: true},
		{Kind: BlockTool, Title: "coding.shell", State: "running"},
	}}
	detail := model.agentDetailTranscript(agent)
	if !detail.transcript[1].Collapsed || detail.transcript[2].Collapsed {
		t.Fatalf("agent detail changed tool collapse state: %#v", detail.transcript[1:])
	}
}

func TestToolDisplayNameReusesProvidedCatalog(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if allocations := testing.AllocsPerRun(10, func() {
		_ = model.toolDisplayName("coding.shell")
	}); allocations > 10 {
		t.Fatalf("tool display name allocations = %.0f, want at most 10", allocations)
	}
}

func TestSessionReloadRebuildsTypedTasksWithoutDuplicateLifecycleCards(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.agents = []AgentView{{ID: "stale"}}
	model.applyEvent(app.Event{
		Kind: app.EventSessionLoaded, SessionID: "reloaded", State: "loaded",
		Data: map[string]string{
			"blocks": `[{"kind":"agent","runId":"parent","agentId":"child-1","parentToolCallId":"spawn-1","title":"review","content":"done","state":"completed"}]`,
		},
		AgentSnapshots: []app.AgentSnapshotPayload{{
			ID: "child-1", State: "completed", Summary: "done",
			Agent: app.AgentStatePayload{Type: "review", ParentRunID: "parent", ParentToolCallID: "spawn-1", TokensUsed: 42},
		}},
	})
	if len(model.agents) != 1 || model.agents[0].ID != "child-1" || model.agents[0].TokensUsed != 42 {
		t.Fatalf("reloaded tasks = %#v", model.agents)
	}
	if len(model.transcript) != 1 || model.transcript[0].ID != "child-1" || model.transcript[0].ToolCallID != "spawn-1" {
		t.Fatalf("reloaded lifecycle blocks = %#v", model.transcript)
	}
	if model.status != "Ready" {
		t.Fatalf("reloaded session status = %q, want Ready", model.status)
	}
	model.applyEvent(app.Event{
		Kind: app.EventAgentState, SessionID: "reloaded", AgentID: "child-1", State: "completed", Text: "still done",
		Agent: &app.AgentStatePayload{Type: "review", ParentRunID: "parent", ParentToolCallID: "spawn-1"},
	})
	if len(model.agents) != 1 || len(model.transcript) != 1 {
		t.Fatalf("reload update duplicated task state: agents=%#v transcript=%#v", model.agents, model.transcript)
	}
}

func TestAgentCatalogOverlaysShowEffectiveSourceAndStatus(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{
		Kind: app.EventAgentDetail, SessionID: "default", State: "agent_types",
		AgentCatalog: []app.AgentCatalogEntry{{
			Name: "specialist", Model: "child-model", CapabilityMode: "read-only",
			Isolation: "worktree", Source: "/project/.azem/agents.yaml", Enabled: false,
		}},
	})
	if model.overlay != OverlayAgentTypes || len(model.agentTypes) != 1 {
		t.Fatalf("agent type catalog = overlay:%q entries:%#v", model.overlay, model.agentTypes)
	}
	content := ansi.Strip(model.renderOverlay(120, 30))
	for _, wanted := range []string{"AGENT TYPES", "specialist", "child-model", "/project/.azem/agents.yaml", "DISABLED"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("agent type overlay missing %q:\n%s", wanted, content)
		}
	}
	model.applyEvent(app.Event{
		Kind: app.EventAgentDetail, SessionID: "default", State: "personas",
		AgentCatalog: []app.AgentCatalogEntry{{Name: "analyst", Model: "inherit", Source: "builtin", Enabled: true}},
	})
	if model.overlay != OverlayPersonas || !strings.Contains(ansi.Strip(model.renderOverlay(100, 24)), "analyst") {
		t.Fatalf("persona catalog = overlay:%q entries:%#v", model.overlay, model.personas)
	}
}

func TestConcurrentChildApprovalsAreQueuedByPublicApprovalID(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.runID = "parent"
	for _, event := range []app.Event{
		{
			Kind: app.EventApprovalRequested, SessionID: "default", RunID: "child-run-1", AgentID: "child-1",
			ToolCallID: "same-call", ApprovalID: "approval-1", Text: "first", Data: map[string]string{"tool": "coding.write_file"},
		},
		{
			Kind: app.EventApprovalRequested, SessionID: "default", RunID: "child-run-2", AgentID: "child-2",
			ToolCallID: "same-call", ApprovalID: "approval-2", Text: "second", Data: map[string]string{"tool": "coding.write_file"},
		},
	} {
		model.applyEvent(event)
	}
	if len(model.pendingApprovals) != 2 || model.approval == nil || model.approval.ApprovalID != "approval-1" {
		t.Fatalf("queued approvals = current:%#v queue:%#v", model.approval, model.pendingApprovals)
	}
	model.applyEvent(app.Event{
		Kind: app.EventApprovalResolved, SessionID: "default", AgentID: "child-1",
		ToolCallID: "same-call", ApprovalID: "approval-1", State: "deny",
	})
	if len(model.pendingApprovals) != 1 || model.approval == nil || model.approval.ApprovalID != "approval-2" || model.overlay != OverlayApproval {
		t.Fatalf("second approval was not promoted: current:%#v queue:%#v overlay:%q", model.approval, model.pendingApprovals, model.overlay)
	}
	model.applyEvent(app.Event{
		Kind: app.EventApprovalResolved, SessionID: "default", AgentID: "child-2",
		ToolCallID: "same-call", ApprovalID: "approval-2", State: "once",
	})
	if len(model.pendingApprovals) != 0 || model.approval != nil || model.overlay != OverlayNone || model.status != "Running" {
		t.Fatalf("approval queue did not drain: current:%#v queue:%#v overlay:%q status:%q", model.approval, model.pendingApprovals, model.overlay, model.status)
	}
}

func overflowingAgentDetailModel() AppModel {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 80, 20
	model.detailAgentID = "child-1"
	model.agents = []AgentView{{
		ID: "child-1", Role: "review", State: "running", Description: "inspect changes",
		Blocks: []Block{{Kind: BlockAssistant, State: "completed", Content: strings.Repeat("detail output line\n", 120)}},
	}}
	model.openOverlay(OverlayAgentDetail)
	return model
}

func TestMouseWheelScrollsAgentDetailLikeMainTranscript(t *testing.T) {
	model := overflowingAgentDetailModel()
	model.transcriptTop = 7
	updated, _ := model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = updated.(AppModel)
	if model.overlayScroll != 3 || model.transcriptTop != 7 {
		t.Fatalf("wheel-up scroll state = overlay:%d transcript:%d", model.overlayScroll, model.transcriptTop)
	}
	for range 500 {
		updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		model = updated.(AppModel)
	}
	atTop := model.overlayScroll
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = updated.(AppModel)
	if atTop == 0 || model.overlayScroll != atTop {
		t.Fatalf("agent detail scroll escaped its top bound: %d -> %d", atTop, model.overlayScroll)
	}
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	model = updated.(AppModel)
	if model.overlayScroll != atTop-3 {
		t.Fatalf("agent detail wheel-down remained stuck after reaching top: %d", model.overlayScroll)
	}
	updated, _ = model.updateOverlayKey("end")
	model = updated.(AppModel)
	if model.overlayScroll != 0 {
		t.Fatalf("agent detail End offset = %d, want latest output", model.overlayScroll)
	}
}

func TestAgentDetailScrollbarClickAndDrag(t *testing.T) {
	model := overflowingAgentDetailModel()
	scrollbar, ok := model.overlayScrollbar(model.width, model.height)
	if !ok {
		t.Fatal("overflowing agent detail has no scrollbar geometry")
	}
	updated, _ := model.Update(tea.MouseClickMsg{X: scrollbar.x, Y: scrollbar.y, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.overlayScroll != scrollbar.maxOffset || !model.overlayScrollbarDragging {
		t.Fatalf("top scrollbar click = offset:%d dragging:%v want:%d", model.overlayScroll, model.overlayScrollbarDragging, scrollbar.maxOffset)
	}
	bottom := scrollbar.y + scrollbar.height - 1
	updated, _ = model.Update(tea.MouseMotionMsg{X: scrollbar.x, Y: bottom, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, _ = model.Update(tea.MouseReleaseMsg{X: scrollbar.x, Y: bottom, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.overlayScroll != 0 || model.overlayScrollbarDragging {
		t.Fatalf("bottom scrollbar drag = offset:%d dragging:%v", model.overlayScroll, model.overlayScrollbarDragging)
	}
}

func TestHookEventsUseTransientDedicatedPrompt(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.runID = "run"
	model.applyEvent(app.Event{Kind: app.EventToolStarted, RunID: "run", ToolCallID: "tool-1", Data: map[string]string{"name": "test"}})
	started := app.Event{Kind: app.EventHookStarted, AgentID: "main", ToolCallID: "tool-1", Data: map[string]string{
		"event": "PostToolUse", "name": "lint", "source": "/secret/hooks/lint.sh",
	}}
	model.applyEvent(started)
	if len(model.transcript) != 2 || model.transcript[1].Kind != BlockHook || len(model.transcript[1].Hooks) != 1 || model.transcript[1].Hooks[0].State != "running" {
		t.Fatalf("started hook lacks dedicated prompt: %#v", model.transcript)
	}
	if len(model.transcript[0].Hooks) != 0 || model.transcript[1].Hooks[0].Source != "lint.sh" || !model.hasRunningHooks() {
		t.Fatalf("hook was attached to tool or lost state: %#v", model.transcript)
	}
	updated, command := model.Update(animationTickMsg{})
	model = updated.(AppModel)
	if command == nil {
		t.Fatal("hook-only animation did not continue")
	}
	finished := started
	finished.Kind = app.EventHookFinished
	finished.State = "blocked"
	finished.Data["durationMS"] = "17"
	finished.Data["reason"] = "policy denied"
	finished.Data["stdout"] = `{"decision":"deny","command":"secret"}`
	model.applyEvent(finished)
	if len(model.transcript[1].Hooks) != 1 || model.transcript[1].Hooks[0].State != "blocked" || model.transcript[1].Hooks[0].Output != "policy denied" {
		t.Fatalf("finished hook was not replaced/sanitized: %#v", model.transcript[1].Hooks)
	}
	if strings.Contains(model.transcript[1].Hooks[0].Output, "decision") {
		t.Fatal("control JSON leaked into hook output")
	}
	updated, command = model.Update(animationTickMsg{})
	model = updated.(AppModel)
	if command != nil || model.hasRunningHooks() {
		t.Fatal("animation continued after the hook finished")
	}

	plain := hookRunFromEvent(app.Event{Kind: app.EventHookFinished, Data: map[string]string{
		"event": "Stop", "name": "report", "stdout": "one\ntwo\nthree\nfour",
	}})
	if plain.Output != "one\ntwo\nthree" {
		t.Fatalf("plain output was not line-clipped: %q", plain.Output)
	}

	model.applyEvent(app.Event{Kind: app.EventToolFinished, RunID: "run", ToolCallID: "tool-2", State: "completed", Data: map[string]string{"name": "test"}})
	model.applyEvent(app.Event{Kind: app.EventHookFinished, AgentID: "main", ToolCallID: "tool-2", State: "completed", Data: map[string]string{"event": "PostToolUse", "name": "audit"}})
	if len(model.transcript) != 3 || model.transcript[2].Kind != BlockTool {
		t.Fatalf("successful finish-only hook left transcript clutter: %#v", model.transcript)
	}

	success := app.Event{Kind: app.EventHookStarted, Data: map[string]string{"event": "Stop", "name": "notify"}}
	model.applyEvent(success)
	success.Kind, success.State = app.EventHookFinished, "completed"
	model.applyEvent(success)
	if model.transcript[len(model.transcript)-1].Kind == BlockHook {
		t.Fatalf("successful hook prompt did not disappear: %#v", model.transcript)
	}
}

func TestAgentAndLifecycleHooksRenderNarrowAndReducedMotion(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.agents = []AgentView{{ID: "agent-1", Blocks: []Block{{Kind: BlockTool, ToolCallID: "agent-tool", Title: "search"}}}}
	model.applyEvent(app.Event{Kind: app.EventHookStarted, AgentID: "agent-1", ToolCallID: "agent-tool", Data: map[string]string{"event": "PreToolUse", "name": "guard"}})
	if len(model.agents[0].Blocks) != 2 || model.agents[0].Blocks[1].Kind != BlockHook || len(model.agents[0].Blocks[0].Hooks) != 0 {
		t.Fatalf("agent hook lacks dedicated prompt: %#v", model.agents[0].Blocks)
	}
	model.applyEvent(app.Event{Kind: app.EventHookStarted, Data: map[string]string{"event": "SessionStart", "name": "setup"}})
	if len(model.transcript) != 1 || model.transcript[0].Kind != BlockHook {
		t.Fatalf("lifecycle hook did not create a block: %#v", model.transcript)
	}
	model.reducedMotion = true
	prompt := model.agents[0].Blocks[1]
	rendered := ansi.Strip(strings.Join(model.renderBlock(prompt, 0, 24), "\n"))
	if !strings.Contains(rendered, "•") {
		t.Fatalf("reduced-motion hook lacks static mark: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 26 {
			t.Fatalf("narrow hook line width %d: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestHookDiagnosticReasonRendersOnce(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{Kind: app.EventHookDiagnostic, Data: map[string]string{
		"event": "TypoEvent", "source": "/tmp/settings.json", "reason": "unknown event",
	}})
	if len(model.transcript) != 1 || model.transcript[0].Content != "" {
		t.Fatalf("diagnostic block = %#v", model.transcript)
	}
	rendered := ansi.Strip(strings.Join(model.renderBlock(model.transcript[0], 0, 60), "\n"))
	if strings.Count(rendered, "unknown event") != 1 {
		t.Fatalf("diagnostic reason rendered more than once:\n%s", rendered)
	}
}

func TestBackgroundCommandListAndLogDetail(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, cmd := model.executeCommand(Command{Name: "background", Args: []string{"start", "--name", "web", "--cwd", "cmd", "--", "go", "run", "."}})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("background start did not produce an action")
	}
	_ = cmd()
	if len(runtime.actions) != 1 {
		t.Fatalf("background actions = %#v", runtime.actions)
	}
	action := runtime.actions[0]
	if action.Kind != ActionStartBackground || action.Name != "web" || action.CWD != "cmd" || action.Target != "go run ." {
		t.Fatalf("background start action = %+v", action)
	}

	process := backgroundservice.Process{ID: "bg_1", Name: "web", Command: "go run .", CWD: "/tmp/workspace/cmd", PID: 42, State: "running", StartedAt: time.Now(), LogBytes: 128}
	model.applyEvent(app.Event{Kind: app.EventBackgroundState, Background: []backgroundservice.Process{process}})
	if model.overlay != OverlayBackground || !strings.Contains(ansi.Strip(model.renderOverlay(100, 24)), "web") {
		t.Fatalf("background list overlay=%q render=%q", model.overlay, ansi.Strip(model.renderOverlay(100, 24)))
	}
	model.actionBusy = false
	model.actionCancel = nil
	updated, cmd = model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil || model.detailBackgroundID != process.ID || !model.backgroundFollow {
		t.Fatalf("background detail selection = id:%q follow:%t cmd:%v", model.detailBackgroundID, model.backgroundFollow, cmd != nil)
	}
	_ = cmd()
	if got := runtime.actions[len(runtime.actions)-1]; got.Kind != ActionLogsBackground || got.Target != process.ID || got.Offset != -1 {
		t.Fatalf("background logs action = %+v", got)
	}

	model.applyEvent(app.Event{Kind: app.EventBackgroundLogs, BackgroundLogs: &backgroundservice.LogSnapshot{
		Process: process, Lines: []string{"listening on :8080", "request complete"}, TotalLines: 2,
	}})
	rendered := ansi.Strip(model.renderOverlay(110, 28))
	for _, want := range []string{"BACKGROUND LOGS", "PID 42", "listening on :8080", "request complete"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("background detail missing %q:\n%s", want, rendered)
		}
	}
}

func TestBackgroundManualScrollDisablesFollow(t *testing.T) {
	model := NewModel(&recordedRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.overlay = OverlayBackgroundDetail
	model.detailBackgroundID = "bg_1"
	model.backgroundFollow = true
	model.backgroundLogs = &backgroundservice.LogSnapshot{Process: backgroundservice.Process{ID: "bg_1", State: "running"}, Lines: make([]string, 100)}
	updated, _ := model.updateOverlayKey("up")
	model = updated.(AppModel)
	if model.backgroundFollow {
		t.Fatal("manual log scrolling did not pause follow mode")
	}
}
