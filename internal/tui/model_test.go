package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
)

type inertRuntime struct{}

func (inertRuntime) NextEvent(context.Context) (app.Event, error) {
	return app.Event{}, errors.New("closed")
}
func (inertRuntime) StartTurn(string) (string, error) { return "run_test", nil }
func (inertRuntime) CancelActive() bool               { return true }

type configuredTurnRuntime struct {
	inertRuntime
	request app.TurnRequest
}

func (r *configuredTurnRuntime) StartConfiguredTurn(request app.TurnRequest) (string, error) {
	r.request = request
	return "run_configured", nil
}

type skillCommandRuntime struct {
	inertRuntime
	request app.TurnRequest
	actions []Action
}

func (r *skillCommandRuntime) StartConfiguredTurn(request app.TurnRequest) (string, error) {
	r.request = request
	return "run_skill", nil
}

func (r *skillCommandRuntime) ExecuteAction(_ context.Context, action Action) error {
	r.actions = append(r.actions, action)
	return nil
}

type recordedRuntime struct {
	cancelled          bool
	actions            []Action
	shutdown           bool
	foregroundChildren bool
	cancelChildren     bool
}

func (*recordedRuntime) NextEvent(context.Context) (app.Event, error) {
	return app.Event{}, errors.New("closed")
}

func (*recordedRuntime) StartTurn(string) (string, error) { return "run_next", nil }

func (r *recordedRuntime) CancelActive() bool {
	r.cancelled = true
	return true
}

func (r *recordedRuntime) HasActiveForegroundChildren() bool {
	return r.foregroundChildren
}

func (r *recordedRuntime) CancelActiveWithChildren(children bool) bool {
	r.cancelled = true
	r.cancelChildren = children
	return true
}

func (r *recordedRuntime) ExecuteAction(_ context.Context, action Action) error {
	r.actions = append(r.actions, action)
	return nil
}

type blockingActionRuntime struct {
	recordedRuntime
	started chan struct{}
	release chan struct{}
}

func (r *blockingActionRuntime) ExecuteAction(ctx context.Context, _ Action) error {
	close(r.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.release:
		return nil
	}
}

func (r *recordedRuntime) Shutdown(context.Context) error {
	r.shutdown = true
	return nil
}

func TestCtrlCCancelsHangingActionAndRestoresSubmission(t *testing.T) {
	runtime := &blockingActionRuntime{started: make(chan struct{}), release: make(chan struct{})}
	defer close(runtime.release)
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")

	updated, actionCmd := model.beginAction(Action{Kind: ActionLogin, Target: "chatgpt"})
	model = updated.(AppModel)
	actionResult := make(chan tea.Msg, 1)
	go func() { actionResult <- actionCmd() }()
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("login action did not start")
	}

	updated, cancelCmd := model.updateKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	model = updated.(AppModel)
	if cancelCmd != nil || model.quitting || model.status != "Cancelling action" {
		t.Fatalf("cancel state = cmd:%v quitting:%v status:%q", cancelCmd != nil, model.quitting, model.status)
	}

	var result tea.Msg
	select {
	case result = <-actionResult:
	case <-time.After(time.Second):
		t.Fatal("login action did not observe cancellation")
	}
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if model.actionBusy || model.status != "Ready" {
		t.Fatalf("completed cancellation = busy:%v status:%q", model.actionBusy, model.status)
	}

	model.composer.SetValue("hi")
	updated, submitCmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if submitCmd == nil || model.composer.Value() != "" || model.status != "Starting" {
		t.Fatalf("submission after cancellation = cmd:%v composer:%q status:%q", submitCmd != nil, model.composer.Value(), model.status)
	}
}

func TestEscapeCancelsHangingOverlayAction(t *testing.T) {
	runtime := &blockingActionRuntime{started: make(chan struct{}), release: make(chan struct{})}
	defer close(runtime.release)
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.openOverlay(OverlayProvider)

	updated, actionCmd := model.beginAction(Action{Kind: ActionLogin, Target: "chatgpt"})
	model = updated.(AppModel)
	actionResult := make(chan tea.Msg, 1)
	go func() { actionResult <- actionCmd() }()
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("login action did not start")
	}

	updated, _ = model.updateOverlayKey("esc")
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.status != "Cancelling action" {
		t.Fatalf("escape state = overlay:%q status:%q", model.overlay, model.status)
	}
	select {
	case result := <-actionResult:
		updated, _ = model.Update(result)
		model = updated.(AppModel)
	case <-time.After(time.Second):
		t.Fatal("login action did not observe escape cancellation")
	}
	if model.actionBusy || model.status != "Ready" {
		t.Fatalf("completed escape cancellation = busy:%v status:%q", model.actionBusy, model.status)
	}
}

func TestViewUsesAltScreenAndResponsiveSizes(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	view := model.View()
	if !view.AltScreen {
		t.Fatal("View.AltScreen is false")
	}
	if model.width != 80 || model.height != 24 {
		t.Fatalf("size = %dx%d", model.width, model.height)
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	if model.width != 120 || model.height != 40 {
		t.Fatalf("resized size = %dx%d", model.width, model.height)
	}
	if !strings.Contains(model.View().Content, "Azem") {
		t.Fatal("view does not contain product title")
	}
}

func TestSlashCommandFuzzyRanking(t *testing.T) {
	matches := commandSuggestions("/mod")
	if len(matches) != 1 || matches[0].Name != "models" {
		t.Fatalf("/mod matches = %+v", matches)
	}
	matches = commandSuggestions("/mdl")
	if len(matches) != 1 || matches[0].Name != "models" {
		t.Fatalf("/mdl matches = %+v", matches)
	}
	if matches = commandSuggestions("/not-a-command"); len(matches) != 0 {
		t.Fatalf("unexpected matches = %+v", matches)
	}
	if matches = commandSuggestions("/"); len(matches) != len(slashCommands) {
		t.Fatalf("root command count = %d, want %d", len(matches), len(slashCommands))
	}
}

func TestSlashCommandCompletionAndExecution(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.composer.SetValue("/mod")

	updated, _ := model.updateKey(tea.KeyPressMsg{Code: tea.KeyDown})
	model = updated.(AppModel)
	updated, _ = model.updateKey(tea.KeyPressMsg{Code: tea.KeyTab})
	model = updated.(AppModel)
	if value := model.composer.Value(); value != "/models" {
		t.Fatalf("Tab completion = %q", value)
	}

	model.composer.SetValue("/mod")
	model.commandCursor = 0
	updated, cmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if cmd != nil || model.composer.Value() != "/models" || model.overlay != OverlayNone {
		t.Fatalf("partial Enter = cmd:%v composer:%q overlay:%q", cmd != nil, model.composer.Value(), model.overlay)
	}

	updated, _ = model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if model.overlay != OverlayModel || model.composer.Value() != "" {
		t.Fatalf("completed Enter = overlay:%q composer:%q", model.overlay, model.composer.Value())
	}

	model.closeOverlay()
	updated, _ = model.executeCommand(Command{Name: "models", Args: []string{"gpt-direct"}})
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.errorBanner != "usage: /models" {
		t.Fatalf("models argument handling = overlay:%q error:%q", model.overlay, model.errorBanner)
	}
}

func TestSlashCommandSuggestionsRenderWithinViewport(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{
		{width: 80, height: 24},
		{width: 40, height: 12},
	} {
		model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
		updated, _ := model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		model = updated.(AppModel)
		model.composer.SetValue("/mod")

		content := ansi.Strip(model.View().Content)
		for _, wanted := range []string{"› /models", "Tab complete"} {
			if !strings.Contains(content, wanted) {
				t.Fatalf("%dx%d autocomplete view missing %q:\n%s", size.width, size.height, wanted, content)
			}
		}
		if lines := strings.Count(content, "\n") + 1; lines != size.height {
			t.Fatalf("%dx%d autocomplete view height = %d", size.width, size.height, lines)
		}
	}
}

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

func TestCtrlJAddsNewlineWithoutSubmitting(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.composer.SetValue("line one")
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Mod: tea.ModCtrl}))
	model = updated.(AppModel)
	if got := model.composer.Value(); got != "line one\n" {
		t.Fatalf("composer value = %q", got)
	}
	if model.status != "Ready" {
		t.Fatalf("status = %q", model.status)
	}
}

func TestWideLayoutAddsAgentAndMCPContextRail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "team")
	model.applyEvent(app.Event{Kind: app.EventAgentState, AgentID: "child-1", State: "running", Agent: &app.AgentStatePayload{Type: "review"}})
	model.applyEvent(app.Event{Kind: app.EventMCPState, State: "ready", Data: map[string]string{"server": "files", "toolCount": "3"}})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	if content := ansi.Strip(model.View().Content); strings.Contains(content, "RUN CONTEXT") {
		t.Fatal("compact layout rendered the context rail")
	}

	updated, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	content := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"RUN CONTEXT", "AGENTS  1", "review", "MCP  1", "files · 3"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("wide layout missing %q:\n%s", wanted, content)
		}
	}
}

func TestTranscriptCardsAreKeyboardExpandable(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.transcript = []Block{{ID: "call-1", Kind: BlockTool, Title: "coding.read_file", Content: "result", State: "completed"}}
	model.width = 100
	if content := ansi.Strip(model.View().Content); !strings.Contains(content, "TOOL · Read File") || strings.Contains(content, "coding.read_file") {
		t.Fatalf("tool alias was not used:\n%s", content)
	}

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	model = updated.(AppModel)
	if model.focus != focusTranscript || model.transcriptCursor != 0 {
		t.Fatalf("focus=%d cursor=%d", model.focus, model.transcriptCursor)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(AppModel)
	if !model.transcript[0].Collapsed {
		t.Fatal("Enter did not collapse the selected tool card")
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	model = updated.(AppModel)
	if model.focus != focusComposer {
		t.Fatalf("focus after Tab = %d", model.focus)
	}
}

func TestApprovalOverlayExecutesExplicitDecision(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.runID = "run-1"
	model.applyEvent(app.Event{
		Kind: app.EventApprovalRequested, SessionID: "default", RunID: "run-1", ToolCallID: "call-1",
		Text: "write config", Data: map[string]string{"tool": "coding.edit_hashline", "target": "config.go", "risk": "high", "effect": "write"},
	})
	if model.overlay != OverlayApproval || model.status != "Awaiting approval" {
		t.Fatalf("overlay=%q status=%q", model.overlay, model.status)
	}

	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("approval did not return an action command")
	}
	result, ok := cmd().(actionResultMsg)
	if !ok {
		t.Fatalf("action command returned %T", cmd())
	}
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionResolveApproval || runtime.actions[0].Decision != "once" {
		t.Fatalf("actions=%#v", runtime.actions)
	}
	if model.approval != nil || model.overlay != OverlayNone || model.status != "Running" {
		t.Fatalf("approval=%#v overlay=%q status=%q", model.approval, model.overlay, model.status)
	}
}

func TestShiftTabTogglesPromptAndYoloApprovalModes(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if status := ansi.Strip(model.renderStatus(120)); !strings.Contains(status, "APPROVAL ASK") {
		t.Fatalf("initial approval mode is not visible: %q", status)
	}

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("Shift+Tab did not return an approval mode action")
	}
	result, ok := cmd().(actionResultMsg)
	if !ok {
		t.Fatalf("approval mode command returned %T", cmd())
	}
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionSetApprovalMode || runtime.actions[0].Target != "yolo" {
		t.Fatalf("yolo action = %#v", runtime.actions)
	}
	if status := ansi.Strip(model.renderStatus(120)); !strings.Contains(status, "APPROVAL YOLO") {
		t.Fatalf("yolo approval mode is not visible: %q", status)
	}

	model.openOverlay(OverlayModel)
	model.modelSearch.SetValue("grok")
	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	model = updated.(AppModel)
	if cmd == nil || model.overlay != OverlayModel || model.modelSearch.Value() != "grok" {
		t.Fatalf("overlay toggle = cmd:%v overlay:%q query:%q", cmd != nil, model.overlay, model.modelSearch.Value())
	}
	result, ok = cmd().(actionResultMsg)
	if !ok {
		t.Fatalf("prompt mode command returned %T", cmd())
	}
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 || runtime.actions[1].Target != "prompt" || model.approvalMode != ApprovalModePrompt {
		t.Fatalf("prompt action = actions:%#v mode:%q", runtime.actions, model.approvalMode)
	}
}

func TestShiftTabIncludesAutomaticReviewOnlyWhenChatGPTCapabilityIsAvailable(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{
		Kind: app.EventApprovalMode, State: "prompt",
		Data: map[string]string{"auto_review_available": "true"},
	})
	shiftTab := func() string {
		updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		model = updated.(AppModel)
		if cmd == nil {
			t.Fatal("Shift+Tab returned no command")
		}
		result, ok := cmd().(actionResultMsg)
		if !ok {
			t.Fatalf("Shift+Tab command returned %T", cmd())
		}
		updated, _ = model.Update(result)
		model = updated.(AppModel)
		return runtime.actions[len(runtime.actions)-1].Target
	}

	if target := shiftTab(); target != "auto_review" {
		t.Fatalf("first capable mode=%q", target)
	}
	if status := ansi.Strip(model.renderStatus(120)); !strings.Contains(status, "APPROVAL AUTO") {
		t.Fatalf("automatic approval mode is not visible: %q", status)
	}
	if target := shiftTab(); target != "yolo" {
		t.Fatalf("second capable mode=%q", target)
	}
	if target := shiftTab(); target != "prompt" {
		t.Fatalf("third capable mode=%q", target)
	}

	model.applyEvent(app.Event{
		Kind: app.EventApprovalMode, State: "prompt",
		Data: map[string]string{"auto_review_available": "false"},
	})
	if model.autoReviewAvailable || strings.Contains(ansi.Strip(model.renderStatus(120)), "APPROVAL AUTO") {
		t.Fatalf("automatic mode remained visible after capability loss: %+v", model)
	}
	if target := shiftTab(); target != "yolo" {
		t.Fatalf("unavailable capability cycled to %q", target)
	}
}

func TestAutomaticApprovalEventsRenderReviewingAndResolvedTranscriptStates(t *testing.T) {
	tests := []struct {
		state      string
		data       map[string]string
		text       string
		blockState string
		want       string
	}{
		{
			state: "auto_approved", blockState: "completed", want: "Rationale: bounded write",
			data: map[string]string{"risk": "low", "rationale": "bounded write"},
		},
		{
			state: "auto_denied", blockState: "failed", want: "Risk: high",
			data: map[string]string{"risk": "high", "rationale": "target not authorized"},
		},
		{
			state: "auto_failed", blockState: "failed", text: "Automatic review failed (parse)", want: "Failure: Automatic review failed (parse)",
			data: map[string]string{"risk": "medium"},
		},
		{state: "auto_timed_out", blockState: "failed", text: "Automatic review timed out", want: "timed out"},
	}
	for _, test := range tests {
		t.Run(test.state, func(t *testing.T) {
			model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
			model.runID = "run-1"
			model.status = "Running"
			model.applyEvent(app.Event{
				Kind: app.EventApprovalRequested, SessionID: "default", RunID: "run-1",
				ToolCallID: "call-1", ApprovalID: "approval-1", State: "reviewing",
				Data: map[string]string{"tool": "coding.write_file", "action": "write config"},
			})
			if model.status != "Reviewing approval" || model.overlay != OverlayNone ||
				len(model.pendingApprovals) != 0 || len(model.transcript) != 1 ||
				model.transcript[0].State != "running" {
				t.Fatalf("reviewing projection=%+v", model)
			}
			model.applyEvent(app.Event{
				Kind: app.EventApprovalResolved, SessionID: "default", RunID: "run-1",
				ToolCallID: "call-1", ApprovalID: "approval-1", State: test.state,
				Text: test.text, Data: test.data,
			})
			block := model.transcript[0]
			if model.status != "Running" || block.State != test.blockState ||
				!strings.Contains(block.Title, strings.TrimPrefix(test.state, "auto_")) ||
				!strings.Contains(block.Content, test.want) {
				t.Fatalf("resolved projection: status=%q block=%+v", model.status, block)
			}
		})
	}
}

func TestCtrlCInOverlayCancelsRunBeforeQuitting(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.runID = "run-1"
	model.openOverlay(OverlayHelp)

	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("Ctrl+C did not return cancel command")
	}
	_ = cmd()
	if !runtime.cancelled || model.quitting || model.status != "Cancelling" {
		t.Fatalf("cancelled=%t quitting=%t status=%q", runtime.cancelled, model.quitting, model.status)
	}
}

func TestCancelDuringStartAcceptsEitherMessageOrdering(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Cancelling"
	updated, _ := model.Update(startTurnResultMsg{RunID: "run-result-first"})
	model = updated.(AppModel)
	if model.runID != "run-result-first" || model.status != "Cancelling" {
		t.Fatalf("result-first runID=%q status=%q", model.runID, model.status)
	}
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-result-first"})
	model.applyEvent(app.Event{Kind: app.EventRunCancelled, SessionID: "default", RunID: "run-result-first"})
	if model.runID != "" || model.status != "Cancelled" {
		t.Fatalf("result-first terminal runID=%q status=%q", model.runID, model.status)
	}

	model.status = "Cancelling"
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-event-first"})
	if model.runID != "run-event-first" || model.status != "Cancelling" {
		t.Fatalf("event-first runID=%q status=%q", model.runID, model.status)
	}
	updated, _ = model.Update(startTurnResultMsg{RunID: "run-event-first"})
	model = updated.(AppModel)
	model.applyEvent(app.Event{Kind: app.EventRunCancelled, SessionID: "default", RunID: "run-event-first"})
	if model.runID != "" || model.status != "Cancelled" {
		t.Fatalf("event-first terminal runID=%q status=%q", model.runID, model.status)
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

func TestToolStateTransitionsRequireLifecycleEvents(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Starting"
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-tool"})
	model.applyEvent(app.Event{
		Kind: app.EventToolStarted, SessionID: "default", RunID: "run-tool", ToolCallID: "call-1",
		State: "completed", Data: map[string]string{"name": "coding.read_file", "arguments": `{"path":"go.mod"}`},
	})
	if len(model.transcript) != 1 || model.transcript[0].State != "running" {
		t.Fatalf("started tool = %#v", model.transcript)
	}

	model.applyEvent(app.Event{
		Kind: app.EventToolUpdate, SessionID: "default", RunID: "run-tool", ToolCallID: "call-1",
		State: "failed", Text: "halfway",
	})
	if model.transcript[0].State != "running" || !strings.Contains(model.transcript[0].Content, "halfway") {
		t.Fatalf("updated tool = %#v", model.transcript[0])
	}

	model.applyEvent(app.Event{
		Kind: app.EventToolFinished, SessionID: "default", RunID: "run-tool", ToolCallID: "call-1",
		State: "completed", Text: "done",
	})
	model.applyEvent(app.Event{
		Kind: app.EventToolFinished, SessionID: "default", RunID: "run-tool", ToolCallID: "call-1",
		State: "failed", Text: "duplicate",
	})
	if model.transcript[0].State != "completed" || strings.Contains(model.transcript[0].Content, "duplicate") {
		t.Fatalf("finished tool = %#v", model.transcript[0])
	}
}

func TestReadAndSkillToolResultsUseDisplaySummaries(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.updateTool(app.Event{
		Kind: app.EventToolStarted, RunID: "run", ToolCallID: "read", Data: map[string]string{
			"name": "coding.read_file", "arguments": `{"path":"internal/skills/catalog.go","startLine":3,"endLine":5}`,
		},
	})
	model.updateTool(app.Event{
		Kind: app.EventToolFinished, RunID: "run", ToolCallID: "read", State: "completed",
		Text: "¶internal/skills/catalog.go#HASH\n3:import (\n4:\t\"embed\"\n5:)", Data: map[string]string{"name": "coding.read_file"},
	})
	if got := model.transcript[0].Content; got != "Read internal/skills/catalog.go · lines 3-5" {
		t.Fatalf("read display summary = %q", got)
	}

	model.updateTool(app.Event{
		Kind: app.EventToolStarted, RunID: "run", ToolCallID: "skill",
		Data: map[string]string{"name": "hydaelyn_activate_skill", "arguments": `{"name":"verify"}`},
	})
	model.updateTool(app.Event{
		Kind: app.EventToolFinished, RunID: "run", ToolCallID: "skill", State: "completed",
		Text: "Active Hydaelyn skills:\n--- skill: verify ---\nSECRET SKILL BODY", Data: map[string]string{"name": "hydaelyn_activate_skill"},
	})
	if got := model.transcript[1].Content; got != "Loaded skill verify" {
		t.Fatalf("skill display summary = %q", got)
	}

	model.updateTool(app.Event{
		Kind: app.EventToolFinished, RunID: "run", ToolCallID: "failed", State: "failed",
		Text: "permission denied", Data: map[string]string{"name": "coding.read_file"},
	})
	if got := model.transcript[2].Content; got != "permission denied" {
		t.Fatalf("failed read diagnostic was summarized: %q", got)
	}

	jsonOutput := `{"description":"分析项目架构","status":"queued","task_id":"subagent_123","type":"explore","warning":""}`
	if got := summarizeToolResult("subagent.spawn", "", jsonOutput); got != "description: 分析项目架构\nstatus: queued\ntask_id: subagent_123\ntype: explore" {
		t.Fatalf("JSON display summary = %q", got)
	}

	files := strings.Join([]string{"1.go", "2.go", "3.go", "4.go", "5.go", "6.go", "7.go", "8.go", "9.go", "10.go"}, "\n")
	if got := summarizeToolResult("coding.list_files", "", files); got != "1.go\n2.go\n3.go\n4.go\n5.go\n6.go\n7.go\n8.go\n… 2 more entries (10 total)" {
		t.Fatalf("list display summary = %q", got)
	}

	if got := summarizeToolArguments("coding.read_file", `{"path":"internal/config/loader.go","endLine":500,"maxBytes":50000}`); got != "Read internal/config/loader.go · lines 1-500" {
		t.Fatalf("running read arguments = %q", got)
	}
	if got := summarizeToolArguments("coding.go_test", `{"package":"./..."}`); got != "Test package ./..." {
		t.Fatalf("running test arguments = %q", got)
	}
	if got := summarizeToolArguments("coding.search", `{"query":"SessionGrants","regexp":true,"glob":"internal/**/*.go"}`); strings.ContainsAny(got, "{}") || !strings.Contains(got, "query: SessionGrants") {
		t.Fatalf("generic running arguments were not parsed: %q", got)
	}
}

func TestRunTerminalStateResolvesIncompleteTools(t *testing.T) {
	tests := []struct {
		name      string
		kind      app.EventKind
		wantState string
		orphaned  bool
	}{
		{name: "finished", kind: app.EventRunFinished, wantState: "failed", orphaned: true},
		{name: "failed", kind: app.EventRunFailed, wantState: "failed"},
		{name: "cancelled", kind: app.EventRunCancelled, wantState: "cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
			model.status = "Starting"
			model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-terminal"})
			model.applyEvent(app.Event{
				Kind: app.EventToolStarted, SessionID: "default", RunID: "run-terminal", ToolCallID: "call-terminal",
				Data: map[string]string{"name": "coding.read_file"},
			})
			model.applyEvent(app.Event{Kind: test.kind, SessionID: "default", RunID: "run-terminal", Text: "run failed"})

			var toolBlock *Block
			for index := range model.transcript {
				if model.transcript[index].Kind == BlockTool {
					toolBlock = &model.transcript[index]
					break
				}
			}
			if toolBlock == nil || toolBlock.State != test.wantState || toolBlock.Orphaned != test.orphaned {
				t.Fatalf("terminal tool = %#v", toolBlock)
			}
		})
	}
}

func TestLateToolFinishedOnlyReplacesOrphanedFallback(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Starting"
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-late"})
	model.applyEvent(app.Event{
		Kind: app.EventToolStarted, SessionID: "default", RunID: "run-late", ToolCallID: "call-late",
		Data: map[string]string{"name": "coding.read_file"},
	})
	model.applyEvent(app.Event{Kind: app.EventRunFinished, SessionID: "default", RunID: "run-late"})
	model.applyEvent(app.Event{
		Kind: app.EventToolFinished, SessionID: "default", RunID: "run-late", ToolCallID: "call-late",
		State: "completed", Text: "real result",
	})
	if block := model.transcript[0]; block.State != "completed" || block.Orphaned || block.Content != "real result" {
		t.Fatalf("late result block = %#v", block)
	}
	model.applyEvent(app.Event{
		Kind: app.EventToolFinished, SessionID: "default", RunID: "run-late", ToolCallID: "call-late",
		State: "failed", Text: "later duplicate",
	})
	if block := model.transcript[0]; block.State != "completed" || block.Content != "real result" {
		t.Fatalf("duplicate result changed block = %#v", block)
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

func TestReasoningPickerUsesSelectedModelLevelsAndConfiguresTurn(t *testing.T) {
	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "grok", "grok-4.5", "xhigh", "single")
	model.applyEvent(app.Event{
		Kind: app.EventModelCatalog,
		Data: map[string]string{
			"provider": "grok",
			"models":   `[{"id":"grok-4.5","name":"Grok 4.5","supportsReasoning":true,"reasoningLevels":["low","medium","high"],"defaultReasoning":"medium"}]`,
		},
	})
	if model.reasoning != "medium" {
		t.Fatalf("catalog default reasoning = %q, want medium", model.reasoning)
	}

	updated, _ := model.updateKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	model = updated.(AppModel)
	if model.overlay != OverlayReasoning || model.overlayOptionCount() != 3 || model.overlayCursor != 1 {
		t.Fatalf("reasoning overlay = overlay:%q count:%d cursor:%d", model.overlay, model.overlayOptionCount(), model.overlayCursor)
	}
	updated, _ = model.updateOverlayKey("up")
	model = updated.(AppModel)
	updated, _ = model.updateOverlayKey("enter")
	model = updated.(AppModel)
	if model.reasoning != "low" || model.overlay != OverlayNone {
		t.Fatalf("selected reasoning = %q overlay:%q", model.reasoning, model.overlay)
	}

	model.composer.SetValue("use the selected thinking level")
	updated, startCmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if startCmd == nil {
		t.Fatal("reasoning turn command is nil")
	}
	_ = startCmd()
	if runtime.request.Reasoning != "low" {
		t.Fatalf("turn reasoning = %q, want low", runtime.request.Reasoning)
	}

	model.status = "Ready"
	model.runID = ""
	model.errorBanner = ""
	updated, _ = model.executeCommand(Command{Name: "reasoning", Args: []string{"xhigh"}})
	model = updated.(AppModel)
	if !strings.Contains(model.errorBanner, "low|medium|high") {
		t.Fatalf("unsupported reasoning error = %q", model.errorBanner)
	}
	if footer := ansi.Strip(model.renderStatus(120)); !strings.Contains(footer, "Ctrl+R reasoning") {
		t.Fatalf("reasoning shortcut missing from footer: %q", footer)
	}
}

func TestContextTokenCountUsesCompactUnits(t *testing.T) {
	tests := []struct {
		tokens int
		want   string
	}{
		{tokens: 0, want: "0"},
		{tokens: 999, want: "999"},
		{tokens: 1_000, want: "1K"},
		{tokens: 1_500, want: "1.5K"},
		{tokens: 500_000, want: "500K"},
		{tokens: 999_999, want: "999K"},
		{tokens: 1_000_000, want: "1M"},
		{tokens: 1_250_000, want: "1.2M"},
		{tokens: 10_000_000, want: "10M"},
	}
	for _, test := range tests {
		if got := formatTokens(test.tokens); got != test.want {
			t.Errorf("formatTokens(%d) = %q, want %q", test.tokens, got, test.want)
		}
	}
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 500_000
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "0 / 500K") {
		t.Fatalf("500K context footer = %q", footer)
	}
	model.usage.ContextLimit = 1_000_000
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "0 / 1M") {
		t.Fatalf("1M context footer = %q", footer)
	}
}

func TestModelFooterShowsCatalogContextAndLiveOccupancy(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single")
	model.applyEvent(app.Event{
		Kind: app.EventModelCatalog,
		Data: map[string]string{
			"provider": "grok",
			"models":   `[{"id":"grok-4.5","name":"Grok 4.5","contextWindow":131072,"supportsTools":true,"supportsReasoning":true}]`,
		},
	})

	header := ansi.Strip(model.renderHeader(120))
	if strings.Contains(header, "grok-4.5") {
		t.Fatalf("header still contains model metadata: %q", header)
	}
	footer := ansi.Strip(model.renderModelStatus(120))
	for _, wanted := range []string{"MODEL grok/grok-4.5 · THINK high", "CTX [", "0 / 131K", "0.0%"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("initial model footer missing %q: %q", wanted, footer)
		}
	}

	model.updateUsage(map[string]string{
		"inputTokens":  "30000",
		"outputTokens": "2768",
	})
	footer = ansi.Strip(model.renderModelStatus(120))
	for _, wanted := range []string{"32K / 131K", "25.0%", "■"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("occupied model footer missing %q: %q", wanted, footer)
		}
	}
}

func TestModelFooterUpdatesFromProviderContextUsageEvent(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	model.selectModels([]ModelChoice{{ID: "gpt-5.6-sol", ContextWindow: 272_000}})
	model.status = "Running"
	model.runID = "run-1"

	model.applyEvent(app.Event{
		Kind: app.EventContextUsage, SessionID: model.sessionID, RunID: "run-1",
		Data: map[string]string{
			"inputTokens": "68000", "cachedInputTokens": "34000", "outputTokens": "4000", "totalTokens": "72000", "cacheStatus": "reported",
		},
	})

	footer := ansi.Strip(model.renderModelStatus(120))
	for _, wanted := range []string{"72K / 272K", "26.5%", "CACHE 34K/68K", "50.0%", "■"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("context usage footer missing %q: %q", wanted, footer)
		}
	}

	model.applyEvent(app.Event{Kind: app.EventContextUsage, SessionID: model.sessionID, RunID: "run-1", State: "reported"})
	if footer = ansi.Strip(model.renderModelStatus(120)); !strings.Contains(footer, "72K / 272K") {
		t.Fatalf("missing provider usage reset estimated occupancy: %q", footer)
	}
}

func TestContextCacheHitRateStatesAndBounds(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 1_000

	model.updateUsage(map[string]string{"inputTokens": "100", "outputTokens": "20", "cacheStatus": "pending"})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "CACHE --") {
		t.Fatalf("pending cache footer = %q", footer)
	}
	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "0", "outputTokens": "20", "cacheStatus": "reported"})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "CACHE 0/100 · 0.0%") {
		t.Fatalf("zero-hit cache footer = %q", footer)
	}

	model.resetTurnUsage()
	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "150", "cacheStatus": "reported"})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "100.0%") {
		t.Fatalf("over-reported cache footer = %q", footer)
	}
	model.resetTurnUsage()
	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "-5", "cacheStatus": "reported"})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "0.0%") {
		t.Fatalf("negative cache footer = %q", footer)
	}

	model.resetTurnUsage()
	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "50", "cacheStatus": "reported"})
	if footer := ansi.Strip(model.renderContextUsage(40)); !strings.Contains(footer, "CACHE 50.0%") {
		t.Fatalf("compact cache footer = %q", footer)
	}
	model.resetTurnUsage()
	model.updateUsage(map[string]string{"inputTokens": "80", "outputTokens": "0", "cacheStatus": "pending"})
	model.updateUsage(map[string]string{})
	if footer := ansi.Strip(model.renderContextUsage(120)); !strings.Contains(footer, "80 / 1K") || !strings.Contains(footer, "CACHE --") {
		t.Fatalf("omitted cache usage footer = %q", footer)
	}
}

func TestContextCacheHitRateAccumulatesModelCallsWithinTurn(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 1_000

	model.updateUsage(map[string]string{"inputTokens": "100", "cachedInputTokens": "80", "outputTokens": "20", "cacheStatus": "reported"})
	model.updateUsage(map[string]string{"inputTokens": "150", "outputTokens": "0", "cacheStatus": "pending"})
	model.updateUsage(map[string]string{"inputTokens": "150", "cachedInputTokens": "0", "outputTokens": "30", "cacheStatus": "reported"})

	footer := ansi.Strip(model.renderContextUsage(120))
	for _, wanted := range []string{"180 / 1K", "CACHE 80/250", "32.0%"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("turn cache footer missing %q: %q", wanted, footer)
		}
	}
}

func TestSubagentCacheUsageAggregatesWithoutReplacingMainContext(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 1_000
	model.updateUsage(map[string]string{
		"inputTokens": "100", "cachedInputTokens": "20", "outputTokens": "10", "cacheStatus": "reported",
	})
	model.updateUsage(map[string]string{
		"inputTokens": "50", "cachedInputTokens": "40", "outputTokens": "5", "cacheStatus": "reported", "aggregateOnly": "true",
	})
	if model.usage.InputTokens != 100 || model.usage.OutputTokens != 10 {
		t.Fatalf("subagent usage replaced main context occupancy: %+v", model.usage)
	}
	if model.usage.CacheInputTokens != 150 || model.usage.CachedInputTokens != 60 {
		t.Fatalf("subagent cache usage was not aggregated: %+v", model.usage)
	}
	footer := ansi.Strip(model.renderContextUsage(200))
	for _, wanted := range []string{"CACHE MAIN 20/100", "20.0%", "ALL 40.0%"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("separated main/all cache footer missing %q: %q", wanted, footer)
		}
	}
}

func TestModelSelectionUpdatesCatalogContextAndResetsOccupancy(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "first", "high", "single")
	model.selectModels([]ModelChoice{
		{ID: "first", ContextWindow: 131_072},
		{ID: "second", ContextWindow: 200_000},
		{ID: "million", ContextWindow: 1_000_000},
	})
	model.updateUsage(map[string]string{"inputTokens": "1000", "outputTokens": "200"})
	model.selectModel("second")

	if model.model != "second" || model.usage.ContextLimit != 200_000 {
		t.Fatalf("selected model context = model:%q limit:%d", model.model, model.usage.ContextLimit)
	}
	if model.usage.InputTokens != 0 || model.usage.OutputTokens != 0 {
		t.Fatalf("selected model retained occupancy: %+v", model.usage)
	}
	model.overlay = OverlayModel
	options := model.overlayOptions()
	if len(options) != 3 || !strings.Contains(options[1].Detail, "200K context") || !strings.Contains(options[2].Detail, "1M context") {
		t.Fatalf("model picker context detail = %+v", options)
	}
}

func TestModelOverlaySearchFiltersClearsAndSelects(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{
		"chatgpt": {
			{ID: "gpt-5.6-sol", Name: "GPT 5.6 Sol", ContextWindow: 272_000},
		},
		"grok": {
			{ID: "grok-4.3", Name: "Grok 4.3", ContextWindow: 1_000_000},
			{ID: "grok-4.5", Name: "Grok 4.5", ContextWindow: 500_000},
		},
	}
	model.selectModels(model.modelsByProvider["chatgpt"])
	model.openOverlay(OverlayModel)

	for _, key := range "grok 4.5" {
		updated, _ := model.updateKey(tea.KeyPressMsg{Code: key, Text: string(key)})
		model = updated.(AppModel)
	}
	options := model.overlayOptions()
	if len(options) != 1 || options[0].Label != "Grok 4.5" {
		t.Fatalf("filtered model options = %+v", options)
	}
	rendered := ansi.Strip(model.renderOverlay(120, 30))
	for _, wanted := range []string{"SEARCH", "grok 4.5", "Grok 4.5"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("search overlay missing %q:\n%s", wanted, rendered)
		}
	}
	if strings.Contains(rendered, "GPT 5.6 Sol") {
		t.Fatalf("search overlay retained a non-match:\n%s", rendered)
	}

	updated, _ := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(AppModel)
	if model.overlay != OverlayModel || len(model.overlayOptions()) != 3 {
		t.Fatalf("first Esc did not clear search: overlay:%q options:%+v", model.overlay, model.overlayOptions())
	}
	for _, key := range "grok 4.5" {
		updated, _ = model.updateKey(tea.KeyPressMsg{Code: key, Text: string(key)})
		model = updated.(AppModel)
	}
	updated, _ = model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.provider != "grok" || model.model != "grok-4.5" {
		t.Fatalf("searched model selection = overlay:%q provider:%q model:%q", model.overlay, model.provider, model.model)
	}
}

func TestCompactOverlayFitsMinimumTerminal(t *testing.T) {
	model := NewModel(inertRuntime{}, "/a/very/long/workspace/path", "chatgpt", strings.Repeat("model-", 20), "xhigh", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = updated.(AppModel)
	model.openOverlay(OverlayHelp)
	content := model.View().Content
	lines := strings.Split(content, "\n")
	if len(lines) != 12 {
		t.Fatalf("view lines=%d, want 12\n%s", len(lines), ansi.Strip(content))
	}
	for index, line := range lines {
		if width := ansi.StringWidth(line); width > 40 {
			t.Fatalf("line %d width=%d: %q", index, width, ansi.Strip(line))
		}
	}
}

func TestViewFitsRealTerminalBoundsAcrossResponsiveLayouts(t *testing.T) {
	sizes := []struct {
		width  int
		height int
	}{{1, 1}, {5, 4}, {12, 5}, {20, 8}, {39, 12}, {40, 12}, {80, 24}, {120, 40}}
	overlays := []Overlay{
		OverlayNone, OverlayHelp, OverlayCommand, OverlayProvider, OverlayModel, OverlaySkills,
		OverlayReasoning, OverlaySessions, OverlayApproval, OverlayCancel, OverlayDiff, OverlayAgents,
		OverlayAgentDetail, OverlayAgentTypes, OverlayPersonas, OverlayMCP, OverlayRecovery, OverlayError,
	}
	for _, size := range sizes {
		for _, overlay := range overlays {
			model := NewModel(inertRuntime{}, "/a/very/long/workspace/path/that/must/not/overflow", "provider-with-a-long-name", strings.Repeat("model-", 20), "xhigh", "single")
			updated, _ := model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			model = updated.(AppModel)
			model.status = "Running with a status that is deliberately wider than the terminal"
			model.errorBanner = strings.Repeat("provider error ", 12)
			model.composer.SetValue("first line\nsecond line\nthird line\nfourth line\nfifth line")
			model.overlay = overlay

			content := model.View().Content
			lines := strings.Split(content, "\n")
			if len(lines) != size.height {
				t.Fatalf("%dx%d overlay %q rendered %d lines:\n%s", size.width, size.height, overlay, len(lines), ansi.Strip(content))
			}
			for index, line := range lines {
				if got := ansi.StringWidth(line); got != size.width {
					t.Fatalf("%dx%d overlay %q line %d width=%d: %q", size.width, size.height, overlay, index, got, ansi.Strip(line))
				}
			}
		}
	}
}

func TestShortTerminalPrioritizesModalActionsAndComposer(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 32, Height: 5})
	model = updated.(AppModel)
	model.openOverlay(OverlayAgentDetail)
	content := ansi.Strip(model.View().Content)
	if !strings.Contains(content, "TASK DETAIL") || !strings.Contains(content, "Esc back") {
		t.Fatalf("short modal omitted its identity or exit action:\n%s", content)
	}

	model.closeOverlay()
	model.composer.SetValue("one\ntwo\nthree\nfour\nfive")
	content = ansi.Strip(model.View().Content)
	if lines := strings.Count(content, "\n") + 1; lines != 5 {
		t.Fatalf("short composer rendered %d lines, want 5:\n%s", lines, content)
	}
}

func TestDescriptionOverlayScrollsLongContentInsideViewport(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 48, Height: 10})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockDiff, Content: strings.Join([]string{
		"line 01", "line 02", "line 03", "line 04", "line 05", "line 06", "line 07", "line 08", "line 09", "line 10",
	}, "\n")}}
	model.transcriptCursor = 0
	model.openOverlay(OverlayDiff)
	first := ansi.Strip(model.View().Content)
	if !strings.Contains(first, "line 01") || strings.Contains(first, "line 10") {
		t.Fatalf("diff overlay initial window is wrong:\n%s", first)
	}
	for range 8 {
		updated, _ = model.updateOverlayKey("down")
		model = updated.(AppModel)
	}
	last := ansi.Strip(model.View().Content)
	if !strings.Contains(last, "line 10") || strings.Contains(last, "line 01") {
		t.Fatalf("diff overlay did not expose later content:\n%s", last)
	}
	if lines := strings.Count(last, "\n") + 1; lines != 10 {
		t.Fatalf("scrolled diff escaped viewport with %d lines:\n%s", lines, last)
	}
}

func TestWideColumnsKeepTheirDeclaredAlignment(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	body := ansi.Strip(model.renderBody(120, 20))
	for index, line := range strings.Split(body, "\n") {
		if width := ansi.StringWidth(line); width != 120 {
			t.Fatalf("body line %d width=%d, want 120: %q", index, width, line)
		}
		if divider := strings.Index(line, "│"); divider != 88 {
			t.Fatalf("body line %d divider=%d, want 88: %q", index, divider, line)
		}
	}
	header := model.renderHeader(120)
	if width := ansi.StringWidth(header); width != 120 {
		t.Fatalf("header width=%d, want 120: %q", width, ansi.Strip(header))
	}
	modelStatus := model.renderModelStatus(120)
	if width := ansi.StringWidth(modelStatus); width != 120 {
		t.Fatalf("model status width=%d, want 120: %q", width, ansi.Strip(modelStatus))
	}
	status := model.renderStatus(120)
	if width := ansi.StringWidth(status); width != 120 {
		t.Fatalf("status width=%d, want 120: %q", width, ansi.Strip(status))
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
	if model.status != "Recovered" || model.overlay != OverlayNone || model.approval != nil || len(model.recovery) != 0 {
		t.Fatalf("resolved recovery state = status:%q overlay:%q approval:%+v recovery:%+v", model.status, model.overlay, model.approval, model.recovery)
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
	model.transcript = []Block{{Kind: BlockAssistant, Content: "old conversation"}}
	model.agents = []AgentView{{ID: "old-agent"}}
	model.usage = UsageView{InputTokens: 99, OutputTokens: 42}

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
	if model.provider != "grok" || model.model != "grok-model" || model.reasoning != "medium" || model.agentMode != "team" {
		t.Fatalf("new session preferences = %s/%s %s %s", model.provider, model.model, model.reasoning, model.agentMode)
	}
}

func TestProviderCatalogsSurviveSwitchAndLoginSelectsProvider(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-old", "high", "single")
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "chatgpt",
		"models":   `[{"id":"gpt-5.6","name":"GPT 5.6","supportsTools":true,"supportsReasoning":true}]`,
	}})
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "grok",
		"models":   `[{"id":"grok-4.20","name":"Grok 4.20","supportsTools":true,"supportsReasoning":true}]`,
	}})
	if model.provider != "chatgpt" || model.model != "gpt-5.6" || len(model.models) != 1 {
		t.Fatalf("ChatGPT selection changed while caching Grok: provider=%q model=%q models=%+v", model.provider, model.model, model.models)
	}

	model.applyActionResult(Action{Kind: ActionLogin, Target: "grok:import"})
	if model.provider != "grok" || model.model != "grok-4.20" || len(model.models) != 1 {
		t.Fatalf("Grok login selection = provider:%q model:%q models:%+v", model.provider, model.model, model.models)
	}

	updated, _ := model.executeCommand(Command{Name: "provider", Args: []string{"chatgpt"}})
	model = updated.(AppModel)
	if model.provider != "chatgpt" || model.model != "gpt-5.6" || len(model.models) != 1 {
		t.Fatalf("restored ChatGPT catalog = provider:%q model:%q models:%+v", model.provider, model.model, model.models)
	}
}

func TestModelPickerIncludesAllProviderCatalogs(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-old", "high", "single")
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "chatgpt",
		"models":   `[{"id":"gpt-5.6","name":"GPT 5.6","supportsTools":true,"supportsReasoning":true}]`,
	}})
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "grok",
		"models":   `[{"id":"grok-4.20","name":"Grok 4.20","supportsTools":true,"supportsReasoning":true}]`,
	}})
	model.openOverlay(OverlayModel)

	content := ansi.Strip(model.renderOverlay(120, 30))
	for _, wanted := range []string{"CHATGPT", "GPT 5.6", "GROK", "Grok 4.20"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("grouped model picker missing %q:\n%s", wanted, content)
		}
	}
	if got := model.overlayOptionCount(); got != 2 {
		t.Fatalf("model picker option count = %d, want 2", got)
	}
}

func TestGroupedModelPickerSwitchesProviderOnSelection(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6", "high", "single")
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "chatgpt",
		"models":   `[{"id":"gpt-5.6","name":"GPT 5.6","supportsReasoning":true}]`,
	}})
	model.loadModels(app.Event{Data: map[string]string{
		"provider": "grok",
		"models":   `[{"id":"grok-4.20","name":"Grok 4.20","supportsReasoning":true}]`,
	}})
	model.openOverlay(OverlayModel)

	updated, _ := model.updateOverlayKey("down")
	model = updated.(AppModel)
	updated, _ = model.updateOverlayKey("enter")
	model = updated.(AppModel)

	if model.provider != "grok" || model.model != "grok-4.20" {
		t.Fatalf("grouped picker selection = %s/%s, want grok/grok-4.20", model.provider, model.model)
	}
	if model.overlay != OverlayNone {
		t.Fatalf("model picker remained open: %q", model.overlay)
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

func TestTranscriptSupportsMouseAndKeyboardScrolling(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	for index := range 24 {
		model.transcript = append(model.transcript, Block{
			Kind: BlockAssistant, Title: "Azem", Content: fmt.Sprintf("message %02d", index), State: "completed",
		})
	}
	viewportWidth, viewportHeight := model.transcriptViewportSize()
	latest := ansi.Strip(model.renderTranscript(viewportWidth, viewportHeight))
	if !strings.Contains(latest, "message 23") {
		t.Fatalf("latest transcript is not anchored to the bottom:\n%s", latest)
	}
	if view := model.View(); view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("transcript view mouse mode = %v", view.MouseMode)
	}
	updated, _ = model.Update(tea.MouseWheelMsg{X: 2, Y: 3, Button: tea.MouseWheelUp})
	model = updated.(AppModel)
	if model.transcriptTop == 0 {
		t.Fatal("mouse wheel did not move transcript history")
	}
	older := ansi.Strip(model.renderTranscript(viewportWidth, viewportHeight))
	if older == latest {
		t.Fatal("mouse wheel left the transcript viewport unchanged")
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
	model = updated.(AppModel)
	if top := ansi.Strip(model.renderTranscript(viewportWidth, viewportHeight)); !strings.Contains(top, "message 00") {
		t.Fatalf("Ctrl+Home did not reach oldest transcript content:\n%s", top)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	model = updated.(AppModel)
	if model.transcriptTop != 0 {
		t.Fatalf("Ctrl+End transcript offset = %d", model.transcriptTop)
	}
}

func TestAssistantMarkdownRendersWithoutSourceMarkers(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	block := Block{
		Kind:    BlockAssistant,
		Content: "# Design\n\n**Bold finding** with `inline code`.\n\n- first item\n- second item",
		State:   "completed",
	}
	rendered := ansi.Strip(strings.Join(model.renderBlock(block, 0, 72), "\n"))
	for _, marker := range []string{"# Design", "**Bold finding**", "`inline code`"} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("rendered markdown still contains source marker %q:\n%s", marker, rendered)
		}
	}
	for _, wanted := range []string{"Design", "Bold finding", "inline code", "first item", "second item"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("rendered markdown missing %q:\n%s", wanted, rendered)
		}
	}
}

func TestRunningIndicatorStaysVisibleInTranscript(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.transcriptTop = 100
	for index := range 12 {
		model.transcript = append(model.transcript, Block{
			Kind: BlockAssistant, Content: fmt.Sprintf("streamed message %d", index), State: "streaming",
		})
	}
	rendered := ansi.Strip(model.renderTranscript(80, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 8 {
		t.Fatalf("running transcript height = %d, want 8", len(lines))
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "RUNNING") || !strings.Contains(last, "Ctrl+C cancel") {
		t.Fatalf("running indicator is not fixed inside output viewport: %q", last)
	}
	if strings.TrimSpace(lines[len(lines)-2]) != "" {
		t.Fatalf("running indicator touches transcript body: %q", lines[len(lines)-2])
	}
	firstIndicator := last
	updated, command := model.Update(animationTickMsg{})
	model = updated.(AppModel)
	if command == nil {
		t.Fatal("running animation did not schedule its next frame")
	}
	animated := ansi.Strip(model.renderTranscript(80, 8))
	if animated == rendered || strings.Split(animated, "\n")[len(lines)-1] == firstIndicator {
		t.Fatal("running indicator did not animate")
	}
}

func TestTranscriptLayoutCacheReusesStableRender(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.transcript = []Block{{
		Kind: BlockAssistant, Content: "## Cached\n\n- one\n- two", State: "completed",
	}}
	first := model.transcriptLines(72)
	second := model.transcriptLines(72)
	if len(first) == 0 || len(second) == 0 || &first[0] != &second[0] {
		t.Fatal("unchanged transcript layout was rendered again")
	}
	model.transcript[0].Content = "## Updated\n\n- three"
	updated := model.transcriptLines(72)
	if len(updated) == 0 || &first[0] == &updated[0] {
		t.Fatal("changed transcript reused a stale layout")
	}
	if output := ansi.Strip(strings.Join(updated, "\n")); !strings.Contains(output, "Updated") || strings.Contains(output, "Cached") {
		t.Fatalf("updated transcript layout is stale:\n%s", output)
	}
}

func BenchmarkLongTranscriptScroll(b *testing.B) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	for index := range 120 {
		model.transcript = append(model.transcript, Block{
			Kind: BlockAssistant,
			Content: fmt.Sprintf(
				"## Finding %d\n\n**Summary:** terminal rendering must remain responsive.\n\n- first detail\n- second detail\n- third detail",
				index,
			),
			State: "completed",
		})
	}
	width, height := model.transcriptViewportSize()
	_ = model.renderTranscript(width, height)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		model.scrollTranscript(3)
		_ = model.renderTranscript(width, height)
	}
}

func TestSkillCommandsListReloadAndInvoke(t *testing.T) {
	runtime := &skillCommandRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")

	updated, actionCmd := model.executeCommand(Command{Name: "skills"})
	model = updated.(AppModel)
	if actionCmd == nil {
		t.Fatal("/skills did not start an action")
	}
	result := actionCmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionListSkills {
		t.Fatalf("/skills actions = %#v", runtime.actions)
	}

	model.applyEvent(app.Event{
		Kind: app.EventSkillCatalog,
		SkillCatalog: []app.SkillCatalogEntry{
			{Name: "disabled-demo", Description: "Disabled description", SourcePath: "/tmp/disabled/SKILL.md", Disabled: true},
			{Name: "eager-demo", Description: "Eager description", Eager: true, Bundled: true, ResourceCount: 1},
			{Name: "available-demo", Description: "Available description", SourcePath: "/tmp/available/SKILL.md", ModelVisible: true, ResourceCount: 2},
			{Name: "manual-demo", Description: "Manual description", SourcePath: "/tmp/manual/SKILL.md"},
		},
		SkillDiagnostics: []app.SkillDiagnostic{
			{Path: "/bad/one", Message: "warning one"},
			{Path: "/bad/two", Message: "warning two"},
			{Path: "/bad/three", Message: "warning three"},
			{Path: "/bad/four", Message: "warning four"},
		},
	})
	if model.overlay != OverlaySkills || model.overlayOptionCount() != 4 {
		t.Fatalf("skills overlay = %q count=%d", model.overlay, model.overlayOptionCount())
	}
	rendered := ansi.Strip(model.renderOverlay(120, 30))
	for _, wanted := range []string{
		"SKILLS", "Reload affects new turns only", "disabled-demo", "DISABLED",
		"eager-demo", "EAGER", "bundled", "1 resource", "available-demo",
		"AVAILABLE", "2 resources", "manual-demo", "MANUAL-ONLY", "1 more warnings",
	} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("skills overlay missing %q:\n%s", wanted, rendered)
		}
	}
	if strings.Contains(rendered, "warning four") {
		t.Fatalf("skills overlay rendered more than three warning details:\n%s", rendered)
	}

	_ = model.closeOverlay()
	model.status = "Running"
	model.runID = "run-active"
	updated, actionCmd = model.executeCommand(Command{Name: "skills", Args: []string{"reload"}})
	model = updated.(AppModel)
	if actionCmd == nil {
		t.Fatal("/skills reload was blocked during a run")
	}
	result = actionCmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 || runtime.actions[1].Kind != ActionReloadSkills {
		t.Fatalf("/skills reload actions = %#v", runtime.actions)
	}

	model.status = "Ready"
	model.runID = ""
	updated, startCmd := model.executeCommand(Command{Name: "skill", Args: []string{"DEMO", "inspect", "parser"}})
	model = updated.(AppModel)
	if startCmd == nil {
		t.Fatal("/skill did not start a configured turn")
	}
	_ = startCmd()
	if runtime.request.Prompt != "inspect parser" || runtime.request.AgentMode != "single" ||
		len(runtime.request.ActiveSkills) != 1 || runtime.request.ActiveSkills[0] != "demo" {
		t.Fatalf("/skill request = %+v", runtime.request)
	}
	if len(model.transcript) == 0 || model.transcript[len(model.transcript)-1].Content != "inspect parser" {
		t.Fatalf("/skill transcript = %#v", model.transcript)
	}

	fallbackRuntime := &skillCommandRuntime{}
	fallbackModel := NewModel(fallbackRuntime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, startCmd = fallbackModel.executeCommand(Command{Name: "skill", Args: []string{"DEMO"}})
	fallbackModel = updated.(AppModel)
	if startCmd == nil {
		t.Fatal("/skill fallback did not start a turn")
	}
	_ = startCmd()
	const fallback = `Apply the "demo" skill to the current workspace and report the result.`
	if fallbackRuntime.request.Prompt != fallback || fallbackModel.transcript[0].Content != fallback {
		t.Fatalf("/skill fallback request = %+v transcript=%#v", fallbackRuntime.request, fallbackModel.transcript)
	}

	runningRuntime := &skillCommandRuntime{}
	runningModel := NewModel(runningRuntime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	runningModel.status = "Running"
	updated, startCmd = runningModel.executeCommand(Command{Name: "skill", Args: []string{"demo"}})
	runningModel = updated.(AppModel)
	if startCmd != nil || runningRuntime.request.Prompt != "" || len(runningModel.transcript) != 0 {
		t.Fatalf("running /skill started work: request=%+v transcript=%#v", runningRuntime.request, runningModel.transcript)
	}

	teamRuntime := &skillCommandRuntime{}
	teamModel := NewModel(teamRuntime, "/tmp/workspace", "chatgpt", "model", "high", "team")
	updated, startCmd = teamModel.executeCommand(Command{Name: "skill", Args: []string{"demo"}})
	teamModel = updated.(AppModel)
	if startCmd != nil || teamModel.errorBanner != "skill invocation requires single-agent mode; use /team off" {
		t.Fatalf("team /skill = cmd:%v error:%q", startCmd != nil, teamModel.errorBanner)
	}

	usageModel := NewModel(&skillCommandRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, startCmd = usageModel.executeCommand(Command{Name: "skill"})
	usageModel = updated.(AppModel)
	if startCmd != nil || usageModel.errorBanner != "usage: /skill <name> [instruction]" {
		t.Fatalf("missing skill usage = cmd:%v error:%q", startCmd != nil, usageModel.errorBanner)
	}
	updated, startCmd = usageModel.executeCommand(Command{Name: "skills", Args: []string{"bad"}})
	usageModel = updated.(AppModel)
	if startCmd != nil || usageModel.errorBanner != "usage: /skills [reload]" {
		t.Fatalf("invalid skills usage = cmd:%v error:%q", startCmd != nil, usageModel.errorBanner)
	}

	paletteRuntime := &skillCommandRuntime{}
	paletteModel := NewModel(paletteRuntime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	paletteModel.openOverlay(OverlayCommand)
	for index, option := range commandPaletteOptions {
		if option.ID == "skills" {
			paletteModel.overlayCursor = index
			break
		}
	}
	updated, actionCmd = paletteModel.activatePaletteOption()
	paletteModel = updated.(AppModel)
	if actionCmd == nil {
		t.Fatal("Skills command-palette item did not start list action")
	}
	_ = actionCmd()
	if len(paletteRuntime.actions) != 1 || paletteRuntime.actions[0].Kind != ActionListSkills {
		t.Fatalf("Skills palette actions = %#v", paletteRuntime.actions)
	}

	emptyModel := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	emptyModel.applyEvent(app.Event{Kind: app.EventSkillCatalog})
	if content := ansi.Strip(emptyModel.renderOverlay(80, 20)); !strings.Contains(content, "No skills are available") {
		t.Fatalf("skills empty state missing:\n%s", content)
	}
}

func TestThinkingSegmentsKeepMarkdownBoundaries(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.appendDelta(BlockThinking, "run-1", "", "**Analyzing shutdown**")
	model.appendDelta(BlockThinking, "run-1", "", "**Confirming readiness**")
	if got := model.transcript[0].Content; got != "**Analyzing shutdown**\n\n**Confirming readiness**" {
		t.Fatalf("thinking segments = %q", got)
	}

	model.appendDelta(BlockAssistant, "run-2", "", "hello")
	model.appendDelta(BlockAssistant, "run-2", "", " world")
	if got := model.transcript[1].Content; got != "hello world" {
		t.Fatalf("ordinary stream was modified: %q", got)
	}

	blocks := []Block{{Kind: BlockThinking, RunID: "child", Content: "**First**", State: "streaming"}}
	appendAgentViewDelta(&blocks, BlockThinking, "child", "", "**Second**")
	if got := blocks[0].Content; got != "**First**\n\n**Second**" {
		t.Fatalf("subagent thinking segments = %q", got)
	}
}

func TestFooterPrioritizesReadableRuntimeState(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	model.status = "Failed"
	model.errorBanner = "agents.main.max_tokens in config.yaml is exhausted after a long coding task"
	model.transcript = append(model.transcript, Block{Kind: BlockError, Content: model.errorBanner})
	status := ansi.Strip(model.renderStatus(80))
	if !strings.Contains(status, "Failed") || !strings.Contains(status, "DETAILS IN TRANSCRIPT") || strings.Contains(status, "Shift+Tab") {
		t.Fatalf("failure footer competes with shortcuts: %q", status)
	}

	model.usage = UsageView{
		InputTokens: 68_000, OutputTokens: 4_000, ContextLimit: 272_000,
		CacheInputTokens: 68_000, CachedInputTokens: 34_000, CacheReported: true,
	}
	for _, width := range []int{64, 80, 100, 120} {
		footer := ansi.Strip(model.renderModelStatus(width))
		if ansi.StringWidth(footer) != width {
			t.Fatalf("model footer width=%d, want %d: %q", ansi.StringWidth(footer), width, footer)
		}
		if !strings.Contains(footer, "CTX") || !strings.Contains(footer, "50") {
			t.Fatalf("model footer lost complete context/cache signal at width %d: %q", width, footer)
		}
	}
}
