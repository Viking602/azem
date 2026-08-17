package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

func TestHeaderMetadataClicksOpenContextAndExpandTodos(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.usage.ContextLimit = 500_000
	model.usage.InputTokens = 125_000
	model.todo = session.TodoList{Phases: []session.TodoPhase{{Items: []session.TodoItem{
		{Content: "done", Status: session.TodoCompleted},
		{Content: "next", Status: session.TodoPending},
	}}}}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)

	contextX, _ := renderedTextPoint(t, model.renderHeader(model.width), "125K / 500K")
	updated, _ = model.Update(tea.MouseClickMsg{X: contextX + 1, Y: 0, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.overlay != OverlayContext {
		t.Fatalf("context click opened overlay %q, want %q", model.overlay, OverlayContext)
	}
	contextOverlay := ansi.Strip(model.renderOverlay(model.width, model.height))
	if !strings.Contains(contextOverlay, "125K / 500K") {
		t.Fatalf("context click opened an overlay without occupancy details:\n%s", contextOverlay)
	}

	_ = model.closeOverlay()
	todoX, _ := renderedTextPoint(t, model.renderHeader(model.width), "1/2 ✓")
	updated, _ = model.Update(tea.MouseClickMsg{X: todoX + 1, Y: 0, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.overlay != OverlayNone || !model.todoExpanded || model.focus != focusTodo {
		t.Fatalf("todo click = overlay:%q expanded:%t focus:%d", model.overlay, model.todoExpanded, model.focus)
	}
	content := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"✓ done", "□ next"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("expanded todo pane omitted %q:\n%s", wanted, content)
		}
	}
}

func TestTodoCommandTogglesInlinePane(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, cmd := model.executeCommand(Command{Name: "todo"})
	model = updated.(AppModel)
	if cmd != nil || model.overlay != OverlayNone || !model.todoExpanded || model.focus != focusTodo {
		t.Fatalf("/todo open = cmd:%t overlay:%q expanded:%t focus:%d", cmd != nil, model.overlay, model.todoExpanded, model.focus)
	}
	if footer := ansi.Strip(model.renderDockFooter(80, 1)); !strings.Contains(footer, "h:hide done") {
		t.Fatalf("focused todo footer omitted hide hint: %q", footer)
	}

	updated, cmd = model.executeCommand(Command{Name: "todos"})
	model = updated.(AppModel)
	if model.todoExpanded || model.focus != focusComposer || !model.composer.Focused() {
		t.Fatalf("/todos close = cmd:%t expanded:%t focus:%d composer:%t", cmd != nil, model.todoExpanded, model.focus, model.composer.Focused())
	}
}

func TestComposerCaptionClicksOpenPickersAndCycleApproval(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	model.autoReviewAvailable = true
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)

	clickCaption := func(needle string) tea.Cmd {
		rendered := model.renderComposer()
		x, row := renderedTextPoint(t, rendered, needle)
		_, top, _, _ := model.composerBounds()
		var cmd tea.Cmd
		updated, cmd = model.Update(tea.MouseClickMsg{X: x + 1, Y: top + row, Button: tea.MouseLeft})
		model = updated.(AppModel)
		return cmd
	}

	clickCaption("gpt-5.6-sol")
	if model.overlay != OverlayModel {
		t.Fatalf("model caption click opened %q, want %q", model.overlay, OverlayModel)
	}
	_ = model.closeOverlay()
	clickCaption("(high)")
	if model.overlay != OverlayReasoning {
		t.Fatalf("reasoning caption click opened %q, want %q", model.overlay, OverlayReasoning)
	}
	_ = model.closeOverlay()
	cmd := clickCaption(model.approvalModeLabel())
	if cmd == nil {
		t.Fatal("approval caption click did not dispatch a mode change")
	}
	result := cmd().(actionResultMsg)
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionSetApprovalMode || runtime.actions[0].Target != string(ApprovalModeAutoReview) {
		t.Fatalf("approval caption action = %#v", runtime.actions)
	}
}

func TestOverlayOptionClickActivatesRenderedRow(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.openOverlay(OverlayReasoning)
	options := model.overlayOptions()
	if len(options) < 2 {
		t.Fatalf("reasoning options = %d, want at least 2", len(options))
	}
	target := options[0]
	x, y := renderedTextPoint(t, model.renderOverlay(model.width, model.height), target.Label)
	updated, _ = model.Update(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.reasoning != model.reasoningLevels()[0] {
		t.Fatalf("clicked reasoning row = overlay:%q reasoning:%q", model.overlay, model.reasoning)
	}
}

func overflowingContextOverlayModel() AppModel {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	categories := []app.ContextCategory{
		app.ContextCategoryCore,
		app.ContextCategorySkills,
		app.ContextCategoryBuiltinTools,
		app.ContextCategoryMCP,
		app.ContextCategoryConversation,
	}
	for index := range 120 {
		model.contextProfile.Contributions = append(model.contextProfile.Contributions, app.ContextContribution{
			Category: categories[index%len(categories)],
			Name:     fmt.Sprintf("contribution-%03d", index),
			Tokens:   50 + index,
		})
	}
	model.usage.ContextLimit = 272_000
	model.width, model.height = 100, 30
	model.openOverlay(OverlayContext)
	return model
}

func TestScrollableContextOverlayRendersScrollbar(t *testing.T) {
	model := overflowingContextOverlayModel()
	rendered := ansi.Strip(model.renderOverlay(model.width, model.height))
	if !strings.ContainsAny(rendered, "▁▂▃▄▅▆▇█") {
		t.Fatalf("overflowing context overlay has no scrollbar thumb:\n%s", rendered)
	}
}

func TestContextOverlayScrollStopsAtContentBounds(t *testing.T) {
	model := overflowingContextOverlayModel()
	for range 500 {
		updated, _ := model.updateOverlayKey("down")
		model = updated.(AppModel)
	}
	if model.overlayScroll >= 500 {
		t.Fatalf("context overlay scroll is unbounded: %d", model.overlayScroll)
	}
	atBottom := model.overlayScroll
	updated, _ := model.updateOverlayKey("down")
	model = updated.(AppModel)
	if model.overlayScroll != atBottom {
		t.Fatalf("context overlay moved past bottom: %d -> %d", atBottom, model.overlayScroll)
	}
}

func TestContextOverlayScrollbarClickAndDrag(t *testing.T) {
	model := overflowingContextOverlayModel()
	scrollbar, ok := model.overlayScrollbar(model.width, model.height)
	if !ok {
		t.Fatal("overflowing context overlay has no scrollbar geometry")
	}
	updated, _ := model.Update(tea.MouseClickMsg{
		X: scrollbar.x, Y: scrollbar.y + scrollbar.height - 1, Button: tea.MouseLeft,
	})
	model = updated.(AppModel)
	if model.overlayScroll != scrollbar.maxOffset || !model.overlayScrollbarDragging {
		t.Fatalf("bottom scrollbar click = offset:%d dragging:%v want:%d", model.overlayScroll, model.overlayScrollbarDragging, scrollbar.maxOffset)
	}
	updated, _ = model.Update(tea.MouseMotionMsg{X: scrollbar.x, Y: scrollbar.y, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, _ = model.Update(tea.MouseReleaseMsg{X: scrollbar.x, Y: scrollbar.y, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.overlayScroll != 0 || model.overlayScrollbarDragging {
		t.Fatalf("top scrollbar drag = offset:%d dragging:%v", model.overlayScroll, model.overlayScrollbarDragging)
	}
}

func TestContextOverlayWheelUsesResponsiveBoundedSteps(t *testing.T) {
	model := overflowingContextOverlayModel()
	updated, _ := model.Update(tea.MouseWheelMsg{X: 50, Y: 15, Button: tea.MouseWheelDown})
	model = updated.(AppModel)
	if model.overlayScroll != 3 {
		t.Fatalf("first wheel step = %d, want 3", model.overlayScroll)
	}
	model.overlayScroll = model.readOnlyOverlayScrollLimit()
	updated, _ = model.Update(tea.MouseWheelMsg{X: 50, Y: 15, Button: tea.MouseWheelDown})
	model = updated.(AppModel)
	if model.overlayScroll != model.readOnlyOverlayScrollLimit() {
		t.Fatalf("wheel moved past bottom: %d", model.overlayScroll)
	}
}

func TestContextOverlayReusesWrappedReportUntilDataChanges(t *testing.T) {
	model := overflowingContextOverlayModel()
	first := model.contextOverlayDescriptionLines(80)
	second := model.contextOverlayDescriptionLines(80)
	if len(first) == 0 || len(second) == 0 || &first[0] != &second[0] {
		t.Fatal("unchanged context report was rebuilt")
	}
	model.contextProfile.Contributions[0].Tokens++
	third := model.contextOverlayDescriptionLines(80)
	if len(third) == 0 || &second[0] == &third[0] {
		t.Fatal("changed context report reused stale wrapped lines")
	}
}

func TestHeaderWorkspaceCopiesAndStatusOpensDetails(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Cancelled"
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)

	copied := ""
	previousClipboard := writeClipboard
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() { writeClipboard = previousClipboard })

	workspaceX, _ := renderedTextPoint(t, model.renderHeader(model.width), "workspace")
	updated, cmd := model.Update(tea.MouseClickMsg{X: workspaceX + 1, Y: 0, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("workspace click did not return a clipboard command")
	}
	cmd()
	if copied != "/tmp/workspace" {
		t.Fatalf("workspace click copied %q", copied)
	}

	statusX, _ := renderedTextPoint(t, model.renderHeader(model.width), "Cancelled")
	updated, _ = model.Update(tea.MouseClickMsg{X: statusX + 1, Y: 0, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.overlay != OverlayStatus {
		t.Fatalf("status click opened %q, want %q", model.overlay, OverlayStatus)
	}
}

func TestCommandSuggestionClickCompletesRenderedRow(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.composer.SetValue("/mod")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	suggestions := model.visibleCommandSuggestions()
	if len(suggestions) < 2 {
		t.Fatalf("/mod suggestions = %d, want at least 2", len(suggestions))
	}
	target := suggestions[1]
	x, y := renderedTextPoint(t, model.View().Content, target.Usage)
	updated, _ = model.Update(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	model = updated.(AppModel)
	want := "/" + target.Name
	if strings.Contains(target.Usage, " ") {
		want += " "
	}
	if model.composer.Value() != want {
		t.Fatalf("clicked command value = %q, want %q", model.composer.Value(), want)
	}
}

func TestOverlayOutsideClickClosesModal(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.openOverlay(OverlayHelp)
	updated, _ = model.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.focus != focusComposer {
		t.Fatalf("outside click = overlay:%q focus:%d", model.overlay, model.focus)
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

func TestPlanningQuestionOverlayCollectsAndResolvesAnswers(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status, model.runID = "Running", "run-plan"
	questions := `[{"id":"scope","header":"范围","question":"选择实现范围","options":[{"label":"完整","description":"实现并验证","recommended":true},{"label":"最小","description":"仅核心路径"}]}]`
	model.applyEvent(app.Event{Kind: app.EventUserInputRequested, SessionID: "default", RunID: "run-plan", ToolCallID: "ask-call", UserInputID: "ask-1", State: "pending", Data: map[string]string{"questions": questions}})
	if model.overlay != OverlayUserInput || model.planningInput == nil || model.status != "Awaiting input" {
		t.Fatalf("planning question = overlay:%q input:%#v status:%q", model.overlay, model.planningInput, model.status)
	}

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(AppModel)
	if !model.planningInput.canConfirm() {
		t.Fatal("first option was not selected")
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	model = updated.(AppModel)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	model = updated.(AppModel)
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("question confirmation did not return an action")
	}
	result := cmd().(actionResultMsg)
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionResolveUserInput || runtime.actions[0].Target != "ask-1" || !strings.Contains(string(runtime.actions[0].Payload), `"完整"`) {
		t.Fatalf("planning action = %#v", runtime.actions)
	}
}

func TestPlanOverlayStartsExplicitOrdinaryExecution(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status, model.runID = "Running", "run-plan"
	model.applyEvent(app.Event{Kind: app.EventPlanProposed, SessionID: "default", RunID: "run-plan", PlanID: "plan-1", Text: "1. Implement\n2. Verify", State: "proposed", Data: map[string]string{"title": "Ship", "version": "2"}})
	if model.overlay != OverlayPlan || model.planReview == nil || !model.planMode {
		t.Fatalf("plan review = overlay:%q plan:%#v planMode:%v", model.overlay, model.planReview, model.planMode)
	}
	model.applyEvent(app.Event{Kind: app.EventRunFinished, SessionID: "default", RunID: "run-plan"})
	model.overlayCursor = 2
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("execute plan did not return an action")
	}
	result := cmd().(actionResultMsg)
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionResolvePlan || runtime.actions[0].Target != "plan-1" || runtime.actions[0].Decision != "execute" {
		t.Fatalf("plan action = %#v", runtime.actions)
	}
}

func TestManualCompactShowsModelProgressAndRestoresReadyOnNoop(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.sessionID = "session-1"

	updated, cmd := model.beginAction(Action{Kind: ActionCompact, Target: model.sessionID})
	model = updated.(AppModel)
	if cmd == nil || !model.actionBusy || model.status != "Compacting" || !model.isRunning() {
		t.Fatalf("compact start = cmd:%v busy:%v status:%q running:%v", cmd != nil, model.actionBusy, model.status, model.isRunning())
	}
	assertTranscriptStatusOnly(t, model.renderTranscriptFooter(100, 0, 0), "COMPACTING")

	updated, _ = model.Update(actionResultMsg{Action: Action{Kind: ActionCompact}, Err: app.ErrNothingToCompact})
	model = updated.(AppModel)
	if model.actionBusy || model.status != "Ready" || model.errorBanner != "There is not enough new conversation history to compact." {
		t.Fatalf("compact noop = busy:%v status:%q error:%q", model.actionBusy, model.status, model.errorBanner)
	}
}

func TestShiftTabTogglesPromptAndYoloApprovalModes(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if status := ansi.Strip(model.renderStatus(120)); !strings.Contains(status, "☝︎ ASK") {
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
	if status := ansi.Strip(model.renderStatus(120)); !strings.Contains(status, "⚠ FULL ACCESS") {
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
	if status := ansi.Strip(model.renderStatus(120)); !strings.Contains(status, "⛨ SMART") {
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
	if model.autoReviewAvailable || strings.Contains(ansi.Strip(model.renderStatus(120)), "⛨ SMART") {
		t.Fatalf("automatic mode remained visible after capability loss: %+v", model)
	}
	if target := shiftTab(); target != "yolo" {
		t.Fatalf("unavailable capability cycled to %q", target)
	}
}

type approvalSnapshotRuntime struct {
	recordedRuntime
	mode                ApprovalMode
	autoReviewAvailable bool
}

func (r approvalSnapshotRuntime) ApprovalModeState() (ApprovalMode, bool) {
	return r.mode, r.autoReviewAvailable
}

func TestNewModelReadsApprovalModeSnapshot(t *testing.T) {
	runtime := approvalSnapshotRuntime{mode: ApprovalModePrompt, autoReviewAvailable: true}
	model := NewModel(&runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if !model.autoReviewAvailable {
		t.Fatal("initial approval snapshot did not enable automatic review")
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("Shift+Tab did not return an approval mode action")
	}
	result, ok := cmd().(actionResultMsg)
	if !ok {
		t.Fatalf("Shift+Tab command returned %T", cmd())
	}
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Target != string(ApprovalModeAutoReview) {
		t.Fatalf("approval action = %#v", runtime.actions)
	}
	if status := ansi.Strip(model.renderStatus(120)); !strings.Contains(status, "⛨ SMART") {
		t.Fatalf("automatic approval mode is not visible: %q", status)
	}
}

func TestAutomaticApprovalEventsStayOutOfTranscript(t *testing.T) {
	tests := []struct {
		state      string
		text       string
		wantBanner bool
	}{
		{state: "auto_approved"},
		{state: "auto_denied"},
		{state: "auto_failed", text: "Automatic review failed (parse)", wantBanner: true},
		{state: "auto_timed_out", text: "Automatic review timed out", wantBanner: true},
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
			status := ansi.Strip(model.renderTranscriptFooter(80, 0, 0))
			if model.status != "Reviewing approval" || model.overlay != OverlayNone ||
				len(model.pendingApprovals) != 0 || len(model.transcript) != 0 ||
				!strings.Contains(status, "REVIEWING APPROVAL") {
				t.Fatalf("automatic review leaked into chat: status=%q footer=%q transcript=%#v", model.status, status, model.transcript)
			}
			model.applyEvent(app.Event{
				Kind: app.EventApprovalResolved, SessionID: "default", RunID: "run-1",
				ToolCallID: "call-1", ApprovalID: "approval-1", State: test.state, Text: test.text,
			})
			if model.status != "Running" || len(model.transcript) != 0 {
				t.Fatalf("resolved automatic review leaked into chat: status=%q transcript=%#v", model.status, model.transcript)
			}
			if got := model.errorBanner != ""; got != test.wantBanner {
				t.Fatalf("error banner present=%t, want %t: %q", got, test.wantBanner, model.errorBanner)
			}
		})
	}
}

func TestAutomaticDenialFallsBackToInteractiveApprovalOverlay(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.runID = "run-1"
	model.status = "Running"
	request := app.Event{
		Kind: app.EventApprovalRequested, SessionID: "default", RunID: "run-1",
		ToolCallID: "call-1", ApprovalID: "approval-1",
		Data: map[string]string{"tool": "coding.write_file", "target": "config.yaml", "action": "write config"},
	}
	reviewing := request
	reviewing.State = "reviewing"
	model.applyEvent(reviewing)
	model.applyEvent(app.Event{
		Kind: app.EventApprovalResolved, SessionID: "default", RunID: "run-1",
		ToolCallID: "call-1", ApprovalID: "approval-1", State: "auto_denied",
		Data: map[string]string{"tool": "coding.write_file", "target": "config.yaml", "risk": "high", "rationale": "needs confirmation"},
	})
	request.State = "pending"
	model.applyEvent(request)

	if model.status != "Awaiting approval" || model.overlay != OverlayApproval || model.approval == nil ||
		model.approval.ApprovalID != "approval-1" || len(model.pendingApprovals) != 1 {
		t.Fatalf("automatic denial did not open user approval: status=%q overlay=%q approval=%+v pending=%+v", model.status, model.overlay, model.approval, model.pendingApprovals)
	}
}

func TestAutomaticApprovalDoesNotCreateChatBlockBeforeEdit(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.runID = "run-1"
	model.status = "Running"
	model.applyEvent(app.Event{
		Kind: app.EventApprovalRequested, SessionID: "default", RunID: "run-1",
		ToolCallID: "edit-1", ApprovalID: "approval-1", State: "reviewing",
		Data: map[string]string{
			"tool": "coding.edit_hashline", "target": "README.md",
			"action": strings.Repeat("raw patch ", 500),
		},
	})
	if len(model.transcript) != 0 {
		t.Fatalf("automatic approval appeared as a chat block: %#v", model.transcript)
	}

	model.applyEvent(app.Event{
		Kind: app.EventApprovalResolved, SessionID: "default", RunID: "run-1",
		ToolCallID: "edit-1", ApprovalID: "approval-1", State: "auto_approved",
		Data: map[string]string{
			"tool": "coding.edit_hashline", "target": "README.md", "risk": "low", "rationale": "bounded edit",
		},
	})
	model.applyEvent(app.Event{
		Kind: app.EventToolStarted, SessionID: "default", RunID: "run-1", ToolCallID: "edit-1",
		Data: map[string]string{"name": "coding.edit_hashline", "arguments": `{"input":"¶README.md#ABCD\nreplace 1:\n+new"}`},
	})
	model.applyEvent(app.Event{
		Kind: app.EventToolFinished, SessionID: "default", RunID: "run-1", ToolCallID: "edit-1", State: "completed",
		Data: map[string]string{
			"name":       "coding.edit_hashline",
			"structured": `{"sections":[{"path":"README.md","firstChangedLine":1,"diff":"-old\n+new"}]}`,
		},
	})
	if len(model.transcript) != 1 {
		t.Fatalf("approval added a block beside the edit: %#v", model.transcript)
	}
	edit := model.transcript[0]
	if edit.Kind != BlockTool || edit.Title != "coding.edit_hashline" || edit.State != "completed" {
		t.Fatalf("edit lifecycle was not preserved: %#v", edit)
	}
}
