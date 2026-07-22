package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/session"
)

type inertRuntime struct{}

func (inertRuntime) NextEvent(context.Context) (app.Event, error) {
	return app.Event{}, errors.New("closed")
}
func (inertRuntime) StartTurn(string) (string, error) { return "run_test", nil }
func (inertRuntime) CancelActive() bool               { return true }

func TestTextInputsUseBarCursors(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if model.composer.VirtualCursor() || model.composer.Styles().Cursor.Shape != tea.CursorBar {
		t.Fatalf("composer cursor = virtual:%v shape:%v, want real bar", model.composer.VirtualCursor(), model.composer.Styles().Cursor.Shape)
	}
	if model.modelSearch.Styles().Cursor.Shape != tea.CursorBar {
		t.Fatalf("search cursor = %v, want bar", model.modelSearch.Styles().Cursor.Shape)
	}
	view := model.View()
	if view.Cursor == nil || view.Cursor.Shape != tea.CursorBar {
		t.Fatalf("view cursor = %#v, want visible bar", view.Cursor)
	}
}

func TestSentUserMessageUsesAccentCardWithoutSenderLabel(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	block := Block{Kind: BlockUser, Title: "You", Content: "为 hooks 单独设计一个提示，不要太明显"}
	lines := model.renderBlock(block, 0, 28)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if len(lines) < 2 || !strings.Contains(plain, "▌") || strings.Contains(plain, model.tr("block.user")) || strings.Contains(plain, "You") {
		t.Fatalf("sent message did not render as an unlabeled accent card:\n%s", plain)
	}
	for _, line := range lines {
		if strings.Contains(line, "\x1b[48;") {
			t.Fatalf("sent message contains a background color: %q", line)
		}
		if width := ansi.StringWidth(line); width > 28 {
			t.Fatalf("sent message width = %d, exceeds 28: %q", width, ansi.Strip(line))
		}
	}
}

func TestAssistantMessageOmitsGeneratingHeader(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	block := Block{Kind: BlockAssistant, Content: "Hi! How can I help?", State: "streaming"}
	assistantLines := model.renderBlock(block, 0, 40)
	plain := ansi.Strip(strings.Join(assistantLines, "\n"))
	if strings.Contains(plain, "AZEM") || strings.Contains(plain, model.tr("state.streaming")) || !strings.Contains(plain, block.Content) {
		t.Fatalf("assistant response contains a redundant generating header: %q", plain)
	}
	userLine := ansi.Strip(model.renderBlock(Block{Kind: BlockUser, Content: "hi"}, 0, 40)[0])
	assistantLine := ansi.Strip(assistantLines[0])
	userColumn := ansi.StringWidth(strings.SplitN(userLine, "hi", 2)[0])
	assistantColumn := ansi.StringWidth(strings.SplitN(assistantLine, "Hi", 2)[0])
	if userColumn != assistantColumn {
		t.Fatalf("user and assistant text are not aligned: user=%q assistant=%q", userLine, ansi.Strip(assistantLines[0]))
	}
}

type configuredTurnRuntime struct {
	inertRuntime
	request     app.TurnRequest
	guidance    []string
	guidanceErr error
}

func (r *configuredTurnRuntime) StartConfiguredTurn(request app.TurnRequest) (string, error) {
	r.request = request
	return "run_configured", nil
}

func (r *configuredTurnRuntime) GuideActiveTurn(_, _ string, text string) error {
	if r.guidanceErr != nil {
		return r.guidanceErr
	}
	r.guidance = append(r.guidance, text)
	return nil
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
	backgroundChildren bool
	cancelChildren     bool
	shells             []agentservice.ShellExecutionSnapshot
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

func (r *recordedRuntime) HasActiveChildren() bool {
	return r.foregroundChildren || r.backgroundChildren
}

func (r *recordedRuntime) CancelActiveWithChildren(children bool) bool {
	r.cancelled = true
	r.cancelChildren = children
	return true
}

func (r *recordedRuntime) ActiveShellExecutions() []agentservice.ShellExecutionSnapshot {
	return append([]agentservice.ShellExecutionSnapshot(nil), r.shells...)
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

func TestEnterSubmitsGuidanceWhileRunIsActive(t *testing.T) {
	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	model.runID = "run-active"
	model.composer.SetValue("先修复滚动，再处理样式")

	updated, cmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if cmd == nil || model.composer.Value() != "" || model.status != "Running" {
		t.Fatalf("guidance submission = cmd:%v composer:%q status:%q", cmd != nil, model.composer.Value(), model.status)
	}
	result, ok := cmd().(guidanceResultMsg)
	if !ok || result.Err != nil {
		t.Fatalf("guidance result = %#v", result)
	}
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.guidance) != 1 || runtime.guidance[0] != "先修复滚动，再处理样式" {
		t.Fatalf("runtime guidance = %#v", runtime.guidance)
	}
	if last := model.transcript[len(model.transcript)-1]; last.Kind != BlockUser || last.State != "guidance" || last.RunID != "run-active" {
		t.Fatalf("guidance transcript block = %#v", last)
	}
}

func TestGuidanceIsNotSubmittedBeforeRunStartsOrInTeamMode(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		runID  string
		mode   string
	}{
		{name: "starting", status: "Starting", mode: "single"},
		{name: "team", status: "Running", runID: "team-active", mode: "team"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := &configuredTurnRuntime{}
			model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", test.mode)
			model.status, model.runID = test.status, test.runID
			model.composer.SetValue("do not lose this")
			updated, cmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = updated.(AppModel)
			if cmd != nil || model.composer.Value() != "do not lose this" || len(runtime.guidance) != 0 {
				t.Fatalf("blocked guidance = cmd:%v composer:%q guidance:%#v", cmd != nil, model.composer.Value(), runtime.guidance)
			}
		})
	}
}

func TestRejectedGuidanceRestoresComposerWithoutAddingUserBlock(t *testing.T) {
	runtime := &configuredTurnRuntime{guidanceErr: errors.New("run is finishing")}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status, model.runID = "Running", "run-active"
	model.composer.SetValue("keep this guidance")

	updated, cmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	result := cmd().(guidanceResultMsg)
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if model.composer.Value() != "keep this guidance" || len(runtime.guidance) != 0 {
		t.Fatalf("rejected guidance = composer:%q runtime:%#v", model.composer.Value(), runtime.guidance)
	}
	for _, block := range model.transcript {
		if block.Kind == BlockUser {
			t.Fatalf("rejected guidance left a user block: %#v", model.transcript)
		}
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
	if len(matches) != 2 || matches[0].Name != "models" || matches[1].Name != "model-routing" {
		t.Fatalf("/mod matches = %+v", matches)
	}
	matches = commandSuggestions("/mdl")
	if len(matches) != 2 || matches[0].Name != "models" || matches[1].Name != "model-routing" {
		t.Fatalf("/mdl matches = %+v", matches)
	}
	if matches = commandSuggestions("/not-a-command"); len(matches) != 0 {
		t.Fatalf("unexpected matches = %+v", matches)
	}
	if matches = commandSuggestions("/"); len(matches) != len(slashCommands) {
		t.Fatalf("root command count = %d, want %d", len(matches), len(slashCommands))
	}
}

func TestAbsoluteTargetDirectoryIsNotParsedAsSlashCommand(t *testing.T) {
	for _, input := range []string{
		"/Users/viking/agents_dev/oh-my-pi",
		"/tmp",
		"/workspace 请检查这个目录",
	} {
		if command, ok, err := ParseCommand(input); err != nil || ok {
			t.Fatalf("ParseCommand(%q) = command:%#v ok:%v err:%v, want ordinary input", input, command, ok, err)
		}
	}

	command, ok, err := ParseCommand("/models")
	if err != nil || !ok || command.Name != "models" {
		t.Fatalf("registered command parse = command:%#v ok:%v err:%v", command, ok, err)
	}

	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	target := "/Users/viking/agents_dev/oh-my-pi"
	model.composer.SetValue(target)
	updated, cmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("absolute target directory did not start a turn")
	}
	cmd()
	if runtime.request.Prompt != target || model.errorBanner != "" {
		t.Fatalf("absolute target submission = prompt:%q error:%q", runtime.request.Prompt, model.errorBanner)
	}
}

func TestSlashCommandCompletionAndExecution(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.composer.SetValue("/mod")

	updated, _ := model.updateKey(tea.KeyPressMsg{Code: tea.KeyDown})
	model = updated.(AppModel)
	updated, _ = model.updateKey(tea.KeyPressMsg{Code: tea.KeyTab})
	model = updated.(AppModel)
	if value := model.composer.Value(); value != "/model-routing" {
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

func TestRunningSubagentAnimatesInContextRail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "team")
	updated, _ := model.Update(appEventMsg{Event: app.Event{
		Kind: app.EventAgentState, AgentID: "child-1", State: "running",
		Agent: &app.AgentStatePayload{Type: "general-purpose"},
	}})
	model = updated.(AppModel)
	if !model.animationActive || !model.hasRunningAgents() {
		t.Fatalf("running subagent did not start animation: active=%v agents=%#v", model.animationActive, model.agents)
	}
	before := ansi.Strip(model.renderContextRail(32, 16))
	model.animationFrame++
	after := ansi.Strip(model.renderContextRail(32, 16))
	if before == after || !strings.Contains(before, "◇ general-purpose") || !strings.Contains(after, "◈ general-purpose") {
		t.Fatalf("subagent indicator did not animate:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	model.reducedMotion = true
	if mark := model.agentStateMark("running"); mark != "◆" {
		t.Fatalf("reduced-motion subagent mark=%q", mark)
	}
}

func TestReviewingApprovalUsesSmartApprovalColor(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if got, want := model.stateStyle("Reviewing approval").GetForeground(), model.theme.ApprovalSmart.GetForeground(); got != want {
		t.Fatalf("reviewing approval color=%v, want smart approval color=%v", got, want)
	}
	model.status = "Reviewing approval"
	model.transcript = []Block{{Kind: BlockApproval, State: "reviewing", Title: "Reviewing"}}
	before := ansi.Strip(strings.Join(model.renderBlock(model.transcript[0], 0, 64), "\n"))
	model.animationFrame++
	after := ansi.Strip(strings.Join(model.renderBlock(model.transcript[0], 0, 64), "\n"))
	if before == after {
		t.Fatalf("reviewing approval indicator did not animate: %q", before)
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

func TestManualCompactShowsModelProgressAndRestoresReadyOnNoop(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.sessionID = "session-1"

	updated, cmd := model.beginAction(Action{Kind: ActionCompact, Target: model.sessionID})
	model = updated.(AppModel)
	if cmd == nil || !model.actionBusy || model.status != "Compacting" || !model.isRunning() {
		t.Fatalf("compact start = cmd:%v busy:%v status:%q running:%v", cmd != nil, model.actionBusy, model.status, model.isRunning())
	}
	footer := ansi.Strip(model.renderTranscriptFooter(100, 0, 0))
	if !strings.Contains(footer, "COMPACTING") || !strings.Contains(footer, "Waiting for the compaction model") {
		t.Fatalf("compact footer = %q", footer)
	}

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

func TestAutomaticApprovalEventsRenderReviewingAndResolvedTranscriptStates(t *testing.T) {
	tests := []struct {
		state      string
		data       map[string]string
		text       string
		blockState string
		title      string
		want       string
	}{
		{
			state: "auto_approved", blockState: "completed", title: "Allowed", want: "Rationale: bounded write",
			data: map[string]string{"risk": "low", "rationale": "bounded write"},
		},
		{
			state: "auto_denied", blockState: "denied", title: "Denied", want: "Risk: high",
			data: map[string]string{"risk": "high", "rationale": "target not authorized"},
		},
		{
			state: "auto_failed", blockState: "failed", title: "Review failed", text: "Automatic review failed (parse)", want: "Failure: Automatic review failed (parse)",
			data: map[string]string{"risk": "medium"},
		},
		{state: "auto_timed_out", blockState: "failed", title: "Timed out", text: "Automatic review timed out", want: "timed out"},
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
				model.transcript[0].Kind != BlockApproval || model.transcript[0].State != "reviewing" {
				t.Fatalf("reviewing projection=%+v", model)
			}
			model.applyEvent(app.Event{
				Kind: app.EventApprovalResolved, SessionID: "default", RunID: "run-1",
				ToolCallID: "call-1", ApprovalID: "approval-1", State: test.state,
				Text: test.text, Data: test.data,
			})
			block := model.transcript[0]
			if model.status != "Running" || block.Kind != BlockApproval || block.State != test.blockState ||
				block.Title != test.title ||
				!strings.Contains(block.Content, test.want) {
				t.Fatalf("resolved projection: status=%q block=%+v", model.status, block)
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

func TestAutomaticApprovalAndEditUseSeparateAnimatedBlocks(t *testing.T) {
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
	before := ansi.Strip(strings.Join(model.renderBlock(model.transcript[0], 0, 80), "\n"))
	model.animationFrame++
	animated := ansi.Strip(strings.Join(model.renderBlock(model.transcript[0], 0, 80), "\n"))
	if before == animated || strings.Contains(before, "raw patch") || !strings.Contains(before, "Edit File · README.md") {
		t.Fatalf("approval animation/summary before=%q after=%q", before, animated)
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
	if len(model.transcript) != 2 {
		t.Fatalf("approval and edit were not separated: %#v", model.transcript)
	}
	if approval, edit := model.transcript[0], model.transcript[1]; approval.Kind != BlockApproval || approval.State != "completed" || edit.Kind != BlockDiff || edit.State != "completed" {
		t.Fatalf("approval/edit lifecycle collided: approval=%#v edit=%#v", approval, edit)
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
	if got := model.transcript[0].Content; !strings.Contains(got, "¶internal/skills/catalog.go#HASH") || !strings.Contains(got, "4:\t\"embed\"") {
		t.Fatalf("read source output was not retained for highlighting: %q", got)
	}

	model.updateTool(app.Event{
		Kind: app.EventToolStarted, RunID: "run", ToolCallID: "skill",
		Data: map[string]string{"name": "hydaelyn_activate_skill", "arguments": `{"name":"verify"}`},
	})
	model.updateTool(app.Event{
		Kind: app.EventToolFinished, RunID: "run", ToolCallID: "skill", State: "completed",
		Text: "Active Hydaelyn skills:\n--- skill: verify ---\nSECRET SKILL BODY", Data: map[string]string{"name": "hydaelyn_activate_skill"},
	})
	if got := model.transcript[1].Content; got != "Skill: verify\nStatus: Loaded" {
		t.Fatalf("skill display summary = %q", got)
	}
	if strings.Contains(model.transcript[1].Content, "SECRET") {
		t.Fatal("skill body leaked into transcript")
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

	subagentOutput := `{"tasks":[{"description":"分析整体架构","status":"failed","type":"explore","elapsed_ms":40457,"tool_calls":33,"turns":5,"tokens_used":143698,"error":"agent loop budget exhausted: max tokens"},{"description":"分析测试与质量","status":"completed","type":"explore","elapsed_ms":12000,"tool_calls":7,"turns":2,"tokens_used":12400,"output":"Found concrete evidence."}]}`
	subagentSummary := summarizeToolResult("subagent.get_output", "", subagentOutput)
	for _, wanted := range []string{
		"[1] 分析整体架构", "Failed · explore · 33 tools · 5 turns · 143K tokens · 40.5s",
		"Error: agent loop budget exhausted: max tokens", "[2] 分析测试与质量",
		"Completed · explore · 7 tools · 2 turns · 12K tokens · 12.0s", "Output: Found concrete evidence.",
	} {
		if !strings.Contains(subagentSummary, wanted) {
			t.Fatalf("subagent summary omitted %q:\n%s", wanted, subagentSummary)
		}
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
	editArguments := `{"dryRun":true,"input":"¶README.md#720F\nreplace 1-2:\n+` + strings.Repeat("long content ", 200) + `"}`
	if got := summarizeToolArguments("coding.edit_hashline", editArguments); got != "Preview README.md" {
		t.Fatalf("edit arguments exposed raw patch: %q", got)
	}
	if got := summarizeToolArguments("coding.write_file", `{"path":"new.go","content":"package main\n\n"}`); got != "Create new.go · 2 lines" {
		t.Fatalf("write arguments exposed file content: %q", got)
	}
}

func TestFailedEditReplacesRawPatchWithTargetAndError(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	arguments := `{"dryRun":true,"input":"¶README.md#720F\nreplace 1:\n+` + strings.Repeat("README body ", 200) + `"}`
	model.updateTool(app.Event{
		Kind: app.EventToolStarted, RunID: "run", ToolCallID: "edit", Data: map[string]string{
			"name": "coding.edit_hashline", "arguments": arguments,
		},
	})
	model.updateTool(app.Event{
		Kind: app.EventToolFinished, RunID: "run", ToolCallID: "edit", State: "failed",
		Text: "coding.edit_hashline failed: invalid replace range", Data: map[string]string{"name": "coding.edit_hashline"},
	})
	block := model.transcript[0]
	if block.Content != "Preview README.md\ninvalid replace range" {
		t.Fatalf("failed edit content = %q", block.Content)
	}
	if strings.Contains(block.Content, "README body") || len(block.Content) > 200 {
		t.Fatalf("failed edit exposed raw patch: %q", block.Content)
	}
}

func TestPersistedFailedAgentEditHidesRawPatch(t *testing.T) {
	arguments := `{"dryRun":false,"input":"¶internal/app.go#ABCD\nreplace 1:\n+` + strings.Repeat("source ", 200) + `"}`
	blocks := agentTranscriptBlocks([]app.AgentTranscriptBlock{{
		ID: "edit", Kind: "tool", ToolCallID: "edit", Title: "coding.edit_hashline", State: "failed",
		Content: arguments + "\ncoding.edit_hashline failed: stale tag; re-read the file",
	}})
	if len(blocks) != 1 || blocks[0].Content != "Edit internal/app.go\nstale tag; re-read the file" {
		t.Fatalf("persisted failed edit = %#v", blocks)
	}
}

func TestFileChangesBecomeInlineDiffBlocks(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.updateTool(app.Event{
		Kind: app.EventToolStarted, RunID: "run", ToolCallID: "edit", Data: map[string]string{
			"name": "coding.edit_hashline", "arguments": `{"input":"patch"}`,
		},
	})
	model.updateTool(app.Event{
		Kind: app.EventToolFinished, RunID: "run", ToolCallID: "edit", State: "completed",
		Data: map[string]string{
			"name":       "coding.edit_hashline",
			"structured": `{"sections":[{"path":"internal/app.go","firstChangedLine":12,"diff":"-old value\n+new value"}]}`,
		},
	})
	edit := model.transcript[0]
	if edit.Kind != BlockDiff || edit.Title != "internal/app.go  +1/-1" {
		t.Fatalf("edit block = %#v", edit)
	}
	if edit.Content != "@@ internal/app.go:12 @@\n-old value\n+new value" {
		t.Fatalf("edit diff = %q", edit.Content)
	}

	model.updateTool(app.Event{
		Kind: app.EventToolStarted, RunID: "run", ToolCallID: "write", Data: map[string]string{
			"name": "coding.write_file", "arguments": `{"path":"new.go","content":"package main\n\nfunc main() {}\n"}`,
		},
	})
	model.updateTool(app.Event{
		Kind: app.EventToolFinished, RunID: "run", ToolCallID: "write", State: "completed",
		Text: "¶new.go#1234\ncreated new.go", Data: map[string]string{"name": "coding.write_file"},
	})
	created := model.transcript[1]
	if created.Kind != BlockDiff || created.Title != "new.go  +3/-0" {
		t.Fatalf("write block = %#v", created)
	}
	for _, line := range []string{"@@ new.go:1 @@", "+package main", "+", "+func main() {}"} {
		if !strings.Contains(created.Content, line) {
			t.Fatalf("write diff missing %q: %q", line, created.Content)
		}
	}
}

func TestCompactEditOutputFallbackBecomesDiff(t *testing.T) {
	title, diff, ok := summarizeFileChange(
		"coding.edit_hashline",
		"",
		"",
		"¶foo.go#abcd\nupdated foo.go\nfirstChangedLine: 4\n\n--- compact diff ---\n-\treturn a-b\n+\treturn a + b",
	)
	if !ok || title != "foo.go  +1/-1" {
		t.Fatalf("fallback title = %q, ok=%v", title, ok)
	}
	if diff != "@@ foo.go:4 @@\n-\treturn a-b\n+\treturn a + b" {
		t.Fatalf("fallback diff = %q", diff)
	}
}

func TestDiffRendererSeparatesFilesHunksAndLineNumbers(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	content := strings.Join([]string{
		"@@ internal/app.go:12 @@",
		"-old value",
		"+new value",
		" unchanged",
		"",
		"@@ internal/tui/view.go:40 @@",
		"+new row",
	}, "\n")
	plain := ansi.Strip(strings.Join(model.renderDiffContent(content, 72), "\n"))
	for _, wanted := range []string{
		"M internal/app.go  +1 -1",
		"@@ line 12 @@",
		"12    - old value",
		"   12 + new value",
		"13 13   unchanged",
		"A internal/tui/view.go  +1 -0",
		"@@ line 40 @@",
	} {
		if !strings.Contains(plain, wanted) {
			t.Fatalf("rendered diff omitted %q:\n%s", wanted, plain)
		}
	}
}

func TestGitDiffToolUsesAccessibleFullRowChangeStyling(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if model.theme.DiffAdd.GetBackground() == nil || model.theme.DiffDel.GetBackground() == nil ||
		model.theme.DiffHunk.GetBackground() == nil {
		t.Fatal("diff change hierarchy is missing background colors")
	}
	if fmt.Sprint(model.theme.DiffAdd.GetBackground()) == fmt.Sprint(model.theme.DiffDel.GetBackground()) {
		t.Fatal("added and deleted rows use the same background color")
	}
	block := Block{
		Kind: BlockTool, Title: "coding.git_diff", State: "completed",
		Content: "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -7 +7 @@\n-\toldCall()\n+\tnewCall()",
	}
	directRows := model.renderDiffContent(block.Content, 74)
	if len(directRows) == 0 {
		t.Fatalf("Git diff content produced no rows: files=%#v", func() []diffViewFile {
			files, _ := parseDiffView(block.Content)
			return files
		}())
	}
	rows := model.renderBlock(block, 0, 72)
	plainRows := make([]string, len(rows))
	for index, row := range rows {
		plainRows[index] = ansi.Strip(row)
	}
	plain := strings.Join(plainRows, "\n")
	for _, wanted := range []string{"M main.go  +1 -1", "@@ -7 +7 @@", "7   - ", "  7 + ", "oldCall()", "newCall()"} {
		if !strings.Contains(plain, wanted) {
			t.Fatalf("Git diff tool omitted %q:\n%s", wanted, plain)
		}
	}
	for _, row := range rows {
		text := ansi.Strip(row)
		if strings.Contains(text, "oldCall()") || strings.Contains(text, "newCall()") || strings.Contains(text, "@@ line 7 @@") {
			if width := ansi.StringWidth(row); width != 74 {
				t.Fatalf("styled diff row width=%d, want 74: %q", width, text)
			}
		}
	}
}

func TestDiffRendererParsesUnifiedDiffAndDegradesOnNarrowScreens(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	content := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -7,2 +7,2 @@ func main() {",
		"-\toldCall()",
		"+\tnewCall()",
		" }",
	}, "\n")
	files, ok := parseDiffView(content)
	if !ok || len(files) != 1 || files[0].Path != "main.go" || files[0].Added != 1 || files[0].Deleted != 1 {
		t.Fatalf("parsed unified diff = %#v, ok=%v", files, ok)
	}
	rows := model.renderDiffContent(content, 24)
	plain := ansi.Strip(strings.Join(rows, "\n"))
	if !strings.Contains(plain, "│ - ") || !strings.Contains(plain, "oldCall()") ||
		!strings.Contains(plain, "│ + ") || !strings.Contains(plain, "newCall()") {
		t.Fatalf("narrow diff lost change markers:\n%s", plain)
	}
	for index, row := range rows {
		if width := ansi.StringWidth(row); width > 24 {
			t.Fatalf("narrow diff row %d width=%d, want <=24: %q", index, width, ansi.Strip(row))
		}
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

func TestDetailedUsageShowsReasoningCompactionAndTransport(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single")
	model.usage.ContextLimit = 500_000
	model.updateUsage(map[string]string{
		"inputTokens": "100000", "cachedInputTokens": "60000", "uncachedInputTokens": "40000", "outputTokens": "5000",
		"cacheWriteTokens": "10000", "cacheStatus": "reported", "requestKind": "main", "transport": "xai-responses",
	})
	model.updateUsage(map[string]string{"reasoningTokens": "3000", "requestKind": "main", "aggregateOnly": "true"})
	model.updateUsage(map[string]string{
		"inputTokens": "20000", "cachedInputTokens": "5000", "uncachedInputTokens": "15000", "cacheWriteTokens": "2000", "outputTokens": "2000", "reasoningTokens": "500",
		"cacheStatus": "reported", "requestKind": "compaction", "aggregateOnly": "true", "transport": "xai-responses",
	})
	model.updateUsage(map[string]string{
		"inputTokens": "30000", "cachedInputTokens": "18000", "uncachedInputTokens": "12000", "cacheWriteTokens": "1000", "outputTokens": "4000", "reasoningTokens": "700",
		"cacheStatus": "reported", "requestKind": "team", "aggregateOnly": "true", "transport": "xai-responses",
	})
	// Dense diagnostics belong in /status, not the footer strip.
	footer := ansi.Strip(model.renderContextUsage(320))
	for _, unwanted := range []string{"U 40K", "CMP ", "TEAM ", "xai-responses"} {
		if strings.Contains(footer, unwanted) {
			t.Fatalf("footer still leaks diagnostic %q: %q", unwanted, footer)
		}
	}
	report := strings.Join(model.statusReportLines(), "\n")
	for _, wanted := range []string{
		"Uncached input (U): 40K",
		"Cache write (W): 10K main / 13K all",
		"Reasoning tokens (R): 3K",
		"Compaction (CMP): 20K in / 2K out",
		"U 15K",
		"W 2K",
		"R 500",
		"Team usage (TEAM): 30K in / 4K out",
		"cache 60%",
		"U 12K",
		"W 1K",
		"R 700",
		"Last request kind: team",
		"Transport: xai-responses",
	} {
		if !strings.Contains(report, wanted) {
			t.Fatalf("status report missing %q:\n%s", wanted, report)
		}
	}
}

func TestStatusReportShowsActiveShellOwnerAndProcess(t *testing.T) {
	runtime := &recordedRuntime{shells: []agentservice.ShellExecutionSnapshot{{
		AgentID: "background-reviewer", ToolCallID: "shell-17", PID: 123, PGID: 123,
		OutputBytes: 4096, CommandHash: "1234567890abcdef",
	}}}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	report := strings.Join(model.statusReportLines(), "\n")
	for _, want := range []string{"Shell processes", "background-reviewer", "shell-17", "pid 123 / pgid 123", "4096 bytes", "1234567890ab"} {
		if !strings.Contains(report, want) {
			t.Fatalf("status report missing %q:\n%s", want, report)
		}
	}
}

func TestStatusCommandOpensDiagnosticsOverlay(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single", "session-status")
	model.status = "Ready"
	model.usage.ContextLimit = 500_000
	model.usage.InputTokens = 153_000
	model.usage.UncachedInputTokens = 339
	model.usage.ReasoningTokens = 8
	model.usage.LastRequestKind = "main"
	model.usage.LastTransport = "xai-responses"

	command, ok, err := ParseCommand("/status")
	if err != nil || !ok || command.Name != "status" {
		t.Fatalf("ParseCommand(/status) = %#v ok=%v err=%v", command, ok, err)
	}
	updated, _ := model.executeCommand(command)
	model = updated.(AppModel)
	if model.overlay != OverlayStatus {
		t.Fatalf("overlay = %q, want status", model.overlay)
	}
	content := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"RUNTIME STATUS", "Uncached input (U): 339", "Reasoning tokens (R): 8", "Last request kind: main", "Transport: xai-responses", "grok-4.5"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("status overlay missing %q:\n%s", wanted, content)
		}
	}

	updated, _ = model.executeCommand(Command{Name: "status", Args: []string{"extra"}})
	model = updated.(AppModel)
	if model.errorBanner != "usage: /status" {
		t.Fatalf("status usage error = %q", model.errorBanner)
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
	if model.overlay != OverlayReasoning || model.pendingSessionModel == nil {
		t.Fatalf("searched model should chain to reasoning: overlay:%q pending:%#v", model.overlay, model.pendingSessionModel)
	}
	if model.provider != "chatgpt" || model.model != "gpt-5.6-sol" {
		t.Fatalf("search selection applied before reasoning: provider:%q model:%q", model.provider, model.model)
	}
	updated, _ = model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.provider != "grok" || model.model != "grok-4.5" {
		t.Fatalf("searched model selection = overlay:%q provider:%q model:%q", model.overlay, model.provider, model.model)
	}
}

func TestModelRoutingCommandRendersConfiguredAndInheritedRoutes(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	updated, cmd := model.executeCommand(Command{Name: "model-routing"})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("/model-routing did not start a list action")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionListModelRoutes {
		t.Fatalf("model routing list action = %#v", runtime.actions)
	}

	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("grok", "grok-4.5", "low")},
	}})
	if model.overlay != OverlayModelRoutes || len(model.overlayOptions()) != 2 {
		t.Fatalf("model routes overlay = %q options=%#v", model.overlay, model.overlayOptions())
	}
	rendered := ansi.Strip(model.renderOverlay(100, 24))
	for _, wanted := range []string{"MODEL ROUTING", "Compaction", "Inherit from active agent", "explore", "grok/grok-4.5/low"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("model routes missing %q:\n%s", wanted, rendered)
		}
	}
}

func TestModelRoutingSelectionDoesNotMutateMainModel(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{
		"chatgpt": {{ID: "gpt-main", SupportsReasoning: true, ReasoningLevels: []string{"low", "high"}}},
		"grok":    {{ID: "grok-worker", SupportsReasoning: true, ReasoningLevels: []string{"low", "medium", "high"}}},
	}
	model.selectModels(model.modelsByProvider["chatgpt"])
	model.updateUsage(map[string]string{"inputTokens": "120", "outputTokens": "30"})
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "compaction", Label: "Compaction"},
	}})

	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayModel || model.pendingModelRoute == nil {
		t.Fatalf("route editor did not open model picker: overlay=%q pending=%#v", model.overlay, model.pendingModelRoute)
	}
	for index, entry := range model.modelPickerEntries() {
		if entry.Provider == "grok" && entry.Model.ID == "grok-worker" {
			model.overlayCursor = index
		}
	}
	updated, _ = model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayReasoning {
		t.Fatalf("reasoning picker overlay = %q", model.overlay)
	}
	levels := model.reasoningLevels()
	for index, level := range levels {
		if level == "medium" {
			model.overlayCursor = index
		}
	}
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("route reasoning selection did not start save action")
	}
	_ = cmd()
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionSetModelRoute || runtime.actions[0].Route == nil {
		t.Fatalf("route save action = %#v", runtime.actions)
	}
	route := runtime.actions[0].Route
	if route.Scope != "compaction" || route.Route.Provider != "grok" || route.Route.Model != "grok-worker" || route.Route.Reasoning != "medium" {
		t.Fatalf("saved route = %#v", route)
	}
	if model.provider != "chatgpt" || model.model != "gpt-main" || model.reasoning != "high" || model.usage.InputTokens != 120 || model.usage.OutputTokens != 30 {
		t.Fatalf("route editor mutated main selection: provider=%q model=%q reasoning=%q usage=%+v", model.provider, model.model, model.reasoning, model.usage)
	}
}

func TestModelRoutingNoReasoningResetAndEscape(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{"grok": {{ID: "grok-fast", SupportsReasoning: false}}}
	entry := app.ModelRouteEntry{Scope: "subagent", Role: "verify", Label: "Verify changes", Route: appModelRoute("chatgpt", "old", "high")}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{entry}})

	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("non-reasoning model did not save directly")
	}
	_ = cmd()
	if len(runtime.actions) != 1 || runtime.actions[0].Route == nil || runtime.actions[0].Route.Route.Reasoning != "" || runtime.actions[0].Route.Route.Model != "grok-fast" {
		t.Fatalf("non-reasoning save action = %#v", runtime.actions)
	}

	model.actionBusy = false
	model.pendingModelRoute = nil
	model.openOverlay(OverlayModelRoutes)
	updated, resetCmd := model.updateOverlayKey("R")
	model = updated.(AppModel)
	if resetCmd == nil {
		t.Fatal("R did not start reset action")
	}
	_ = resetCmd()
	if len(runtime.actions) != 2 || runtime.actions[1].Kind != ActionResetModelRoute || runtime.actions[1].Route == nil || runtime.actions[1].Route.Role != "verify" {
		t.Fatalf("route reset action = %#v", runtime.actions)
	}

	model.actionBusy = false
	model.pendingModelRoute = &pendingModelRoute{Entry: entry}
	model.openOverlay(OverlayModel)
	updated, _ = model.updateOverlayKey("esc")
	model = updated.(AppModel)
	if model.overlay != OverlayModelRoutes || model.pendingModelRoute != nil {
		t.Fatalf("route child escape = overlay:%q pending:%#v", model.overlay, model.pendingModelRoute)
	}

	model.pendingModelRoute = &pendingModelRoute{Entry: entry, Provider: "grok", Model: "grok-fast"}
	model.overlay = OverlayReasoning
	updated, _ = model.Update(actionResultMsg{Action: Action{Kind: ActionSetModelRoute, Route: &entry}, Err: errors.New("save failed")})
	model = updated.(AppModel)
	if model.overlay != OverlayModelRoutes || model.pendingModelRoute != nil || !strings.Contains(model.errorBanner, "save failed") {
		t.Fatalf("failed route save cleanup = overlay:%q pending:%#v error:%q", model.overlay, model.pendingModelRoute, model.errorBanner)
	}
	model.openOverlay(OverlayModel)
	if model.pendingModelRoute != nil || model.provider != "chatgpt" || model.model != "gpt-main" {
		t.Fatalf("normal model picker was hijacked after route failure: pending=%#v provider=%q model=%q", model.pendingModelRoute, model.provider, model.model)
	}
}

func appModelRoute(provider, model, reasoning string) config.ModelRouteConfig {
	return config.ModelRouteConfig{Provider: provider, Model: model, Reasoning: reasoning}
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
		OverlayNone, OverlayHelp, OverlayStatus, OverlayCommand, OverlayProvider, OverlayModel, OverlayModelRoutes, OverlaySkills,
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

	if model.provider != "chatgpt" || model.model != "gpt-5.6" {
		t.Fatalf("model applied before reasoning confirm: %s/%s", model.provider, model.model)
	}
	if model.overlay != OverlayReasoning || model.pendingSessionModel == nil {
		t.Fatalf("expected reasoning chain: overlay=%q pending=%#v", model.overlay, model.pendingSessionModel)
	}
	if model.pendingSessionModel.Provider != "grok" || model.pendingSessionModel.Model != "grok-4.20" {
		t.Fatalf("pending session model = %#v", model.pendingSessionModel)
	}

	levels := model.reasoningLevels()
	for index, level := range levels {
		if level == "high" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ = model.updateOverlayKey("enter")
	model = updated.(AppModel)

	if model.provider != "grok" || model.model != "grok-4.20" || model.reasoning != "high" {
		t.Fatalf("grouped picker selection = %s/%s/%s, want grok/grok-4.20/high", model.provider, model.model, model.reasoning)
	}
	if model.overlay != OverlayNone || model.pendingSessionModel != nil {
		t.Fatalf("picker state after confirm: overlay=%q pending=%#v", model.overlay, model.pendingSessionModel)
	}
}

func TestModelPickerChainsToReasoningBeforeApplying(t *testing.T) {
	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{
		"chatgpt": {
			{ID: "gpt-main", SupportsReasoning: true, ReasoningLevels: []string{"low", "high"}, DefaultReasoning: "high"},
			{ID: "gpt-plain", SupportsReasoning: false},
		},
		"grok": {
			{ID: "grok-worker", SupportsReasoning: true, ReasoningLevels: []string{"low", "medium", "high"}, DefaultReasoning: "medium"},
		},
	}
	model.selectModels(model.modelsByProvider["chatgpt"])
	model.updateUsage(map[string]string{"inputTokens": "42", "outputTokens": "7"})

	model.openOverlay(OverlayModel)
	for index, entry := range model.modelPickerEntries() {
		if entry.Provider == "grok" && entry.Model.ID == "grok-worker" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayReasoning {
		t.Fatalf("overlay after model pick = %q, want reasoning", model.overlay)
	}
	if model.provider != "chatgpt" || model.model != "gpt-main" || model.reasoning != "high" {
		t.Fatalf("selection mutated before confirm: %s/%s/%s", model.provider, model.model, model.reasoning)
	}
	if model.usage.InputTokens != 42 || model.usage.OutputTokens != 7 {
		t.Fatalf("usage cleared before confirm: %+v", model.usage)
	}
	rendered := ansi.Strip(model.renderOverlay(100, 24))
	for _, wanted := range []string{"THINKING LEVEL", "grok/grok-worker", "choose for the next turn", "medium"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("reasoning chain overlay missing %q:\n%s", wanted, rendered)
		}
	}

	// Esc cancels without applying the pending model.
	updated, _ = model.updateOverlayKey("esc")
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.pendingSessionModel != nil {
		t.Fatalf("esc cleanup = overlay:%q pending:%#v", model.overlay, model.pendingSessionModel)
	}
	if model.provider != "chatgpt" || model.model != "gpt-main" || model.reasoning != "high" {
		t.Fatalf("esc mutated selection: %s/%s/%s", model.provider, model.model, model.reasoning)
	}

	// Confirm applies provider/model/reasoning together.
	model.openOverlay(OverlayModel)
	for index, entry := range model.modelPickerEntries() {
		if entry.Provider == "grok" && entry.Model.ID == "grok-worker" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ = model.activateOverlayOption()
	model = updated.(AppModel)
	levels := model.reasoningLevels()
	for index, level := range levels {
		if level == "low" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ = model.activateOverlayOption()
	model = updated.(AppModel)
	if model.provider != "grok" || model.model != "grok-worker" || model.reasoning != "low" {
		t.Fatalf("confirmed selection = %s/%s/%s", model.provider, model.model, model.reasoning)
	}
	if model.overlay != OverlayNone || model.pendingSessionModel != nil {
		t.Fatalf("confirm cleanup = overlay:%q pending:%#v", model.overlay, model.pendingSessionModel)
	}
	if model.usage.InputTokens != 0 || model.usage.OutputTokens != 0 {
		t.Fatalf("usage should reset on model change: %+v", model.usage)
	}

	model.composer.SetValue("use chained model")
	updated, startCmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if startCmd == nil {
		t.Fatal("turn command is nil")
	}
	_ = startCmd()
	if runtime.request.Provider != "grok" || runtime.request.Model != "grok-worker" || runtime.request.Reasoning != "low" {
		t.Fatalf("turn request = %#v", runtime.request)
	}

	// Models without adjustable reasoning still apply immediately.
	model.status = "Ready"
	model.runID = ""
	model.openOverlay(OverlayModel)
	for index, entry := range model.modelPickerEntries() {
		if entry.Provider == "chatgpt" && entry.Model.ID == "gpt-plain" {
			model.overlayCursor = index
			break
		}
	}
	updated, _ = model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayNone || model.pendingSessionModel != nil {
		t.Fatalf("plain model should apply immediately: overlay=%q pending=%#v", model.overlay, model.pendingSessionModel)
	}
	if model.provider != "chatgpt" || model.model != "gpt-plain" {
		t.Fatalf("plain model selection = %s/%s", model.provider, model.model)
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

func TestTranscriptDragSelectionClampsToConversationPaneAndCopies(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: "dialogue only\nsecond dialogue line", State: "completed"}}
	if view := model.View(); view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("default view mouse mode = %v", view.MouseMode)
	}
	if status := ansi.Strip(model.renderStatus(140)); !strings.Contains(status, "Drag copy") {
		t.Fatalf("drag selection gesture is not visible in status: %q", status)
	}
	_, top, transcriptWidth, transcriptHeight := model.transcriptBounds()
	lines := strings.Split(ansi.Strip(model.renderTranscript(transcriptWidth, transcriptHeight)), "\n")
	row := -1
	column := 0
	for index, line := range lines {
		if offset := strings.Index(line, "dialogue only"); offset >= 0 {
			row, column = index, offset
			break
		}
	}
	if row < 0 {
		t.Fatalf("dialogue fixture was not rendered:\n%s", strings.Join(lines, "\n"))
	}

	var copied string
	previousClipboard := writeClipboard
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() { writeClipboard = previousClipboard })
	updated, _ = model.Update(tea.MouseClickMsg{X: column, Y: top + row, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, _ = model.Update(tea.MouseMotionMsg{X: 119, Y: top + row, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, command := model.Update(tea.MouseReleaseMsg{X: 119, Y: top + row, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.transcriptSelection == nil || model.transcriptSelection.endX != transcriptWidth-1 {
		t.Fatalf("selection escaped transcript width %d: %#v", transcriptWidth, model.transcriptSelection)
	}
	if command == nil {
		t.Fatal("selection release did not copy")
	}
	command()
	if !strings.Contains(copied, "dialogue only") || strings.Contains(copied, "RUN CONTEXT") || strings.Contains(copied, "TODO") {
		t.Fatalf("copied selection crossed into context rail: %q", copied)
	}
}

func TestTranscriptSelectionBackgroundSurvivesNestedMarkdownStyles(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	styled := model.theme.Assistant.Render("plain ") + model.theme.DiffAdd.Render("inline code") + model.theme.Assistant.Render(" tail")
	width := ansi.StringWidth(styled)
	model.transcriptSelection = &transcriptSelection{startX: 0, startY: 0, endX: width - 1, endY: 0}

	highlighted := model.highlightTranscriptSelection([]string{styled}, width)[0]
	wanted := model.theme.Selected.Render(ansi.Strip(styled))
	if !strings.Contains(highlighted, wanted) {
		t.Fatalf("nested ANSI styles interrupted selection background:\nwant segment: %q\ngot:          %q", wanted, highlighted)
	}
}

func TestTranscriptScrollStopsFollowingStreamingTail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = updated.(AppModel)
	model.status = "Running"
	model.runID = "run-1"
	model.transcript = []Block{{Kind: BlockAssistant, RunID: "run-1", Title: "Azem", Content: strings.Repeat("older content ", 80), State: "running"}}
	model.scrollTranscript(4)
	width, height := model.transcriptViewportSize()
	before := ansi.Strip(model.renderTranscript(width, height))

	updated, _ = model.Update(appEventMsg{Event: app.Event{Kind: app.EventTextDelta, RunID: "run-1", Text: strings.Repeat("new content ", 20)}})
	model = updated.(AppModel)
	after := ansi.Strip(model.renderTranscript(width, height))
	if after != before {
		t.Fatalf("streaming content moved a transcript scrolled into history:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestTranscriptOldestPositionStaysPinnedDuringStreaming(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = updated.(AppModel)
	model.status = "Running"
	model.runID = "run-1"
	model.transcript = []Block{{Kind: BlockAssistant, RunID: "run-1", Title: "Azem", Content: strings.Repeat("oldest content ", 80), State: "running"}}
	model.transcriptTop = model.transcriptMaxOffset()

	updated, _ = model.Update(appEventMsg{Event: app.Event{Kind: app.EventTextDelta, RunID: "run-1", Text: strings.Repeat("new content ", 20)}})
	model = updated.(AppModel)
	if want := model.transcriptMaxOffset(); model.transcriptTop != want {
		t.Fatalf("oldest position offset = %d, want new maximum %d", model.transcriptTop, want)
	}
}

func TestTranscriptBodyDoesNotJumpWhenLongFinalAnswerCompletes(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	model = updated.(AppModel)
	model.status = "Running"
	model.runID = "run-1"
	model.transcript = []Block{{
		Kind: BlockAssistant, RunID: "run-1", Title: "Azem",
		Content: strings.Repeat("final answer line\n", 40), State: "streaming",
	}}
	model.scrollTranscript(5)
	width, height := model.transcriptViewportSize()
	before := strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")

	updated, _ = model.Update(appEventMsg{Event: app.Event{Kind: app.EventRunFinished, RunID: "run-1"}})
	model = updated.(AppModel)
	after := strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")
	if got, want := strings.Join(after[:height-1], "\n"), strings.Join(before[:height-1], "\n"); got != want {
		t.Fatalf("finalization shifted the transcript body:\nbefore:\n%s\nafter:\n%s", want, got)
	}
}

func TestTranscriptNarrowViewportUsesRenderedVisualLineCount(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 8, Height: 8})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Title: "Azem", Content: "abcdefghijklmnopqrstuvwxyz", State: "completed"}}
	width, height := model.transcriptViewportSize()
	lineCount := len(model.transcriptLines(max(1, width-4)))
	want := model.transcriptOffsetLimit(lineCount, height)
	if got := model.transcriptMaxOffset(); got != want {
		t.Fatalf("narrow transcript max offset = %d, want rendered visual-line offset %d", got, want)
	}
}

func TestMouseWheelScrollsOverlayWithoutChangingTranscriptOffset(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.overlay = OverlayAgentDetail
	model.transcriptTop = 7
	updated, _ := model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	model = updated.(AppModel)
	if model.overlayScroll != 1 || model.transcriptTop != 7 {
		t.Fatalf("wheel scroll state = overlay:%d transcript:%d", model.overlayScroll, model.transcriptTop)
	}
}

func TestRecapMouseWheelMatchesTranscriptScrollStep(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.overlay = OverlayRecap
	model.recap = &recap.Recap{Summary: strings.Repeat("long recap line\n", 40)}
	model.transcriptTop = 7
	updated, _ := model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	model = updated.(AppModel)
	if model.overlayScroll != 3 || model.transcriptTop != 7 {
		t.Fatalf("recap wheel state = overlay:%d transcript:%d", model.overlayScroll, model.transcriptTop)
	}
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = updated.(AppModel)
	if model.overlayScroll != 0 {
		t.Fatalf("recap wheel did not return to top: %d", model.overlayScroll)
	}
	for range 100 {
		updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		model = updated.(AppModel)
	}
	if model.overlayScroll != model.recapScrollLimit() {
		t.Fatalf("recap wheel escaped valid range: offset=%d limit=%d", model.overlayScroll, model.recapScrollLimit())
	}
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = updated.(AppModel)
	if model.overlayScroll != max(0, model.recapScrollLimit()-3) {
		t.Fatalf("recap reverse wheel remained stuck after overscroll: %d", model.overlayScroll)
	}
}

func TestAssistantMarkdownRendersWithoutSourceMarkers(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	block := Block{
		Kind:    BlockAssistant,
		Content: "# Design\n\n## Plan\n\n### Soon\n\n**Bold finding** with `inline code`.\n\n- first item\n- second item\n\n---",
		State:   "completed",
	}
	rendered := ansi.Strip(strings.Join(model.renderBlock(block, 0, 72), "\n"))
	for _, marker := range []string{"# Design", "## Plan", "### Soon", "**Bold finding**", "`inline code`", "--------"} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("rendered markdown still contains source marker %q:\n%s", marker, rendered)
		}
	}
	for _, wanted := range []string{"Design", "Plan", "Soon", "Bold finding", "inline code", "first item", "second item", "──────"} {
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

	stable := model.transcriptLines(72)
	model.status = "Running"
	model.runID = "run-1"
	model.animationFrame++
	afterAnimation := model.transcriptLines(72)
	if &stable[0] != &afterAnimation[0] {
		t.Fatal("animation frame rerendered a transcript block without an animated indicator")
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
		if option == "skills" {
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

func TestZhCNCoreTUIRendering(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}

	skill := summarizeToolResult("hydaelyn_activate_skill", `{"name":"verify"}`, `{}`, model.catalog)
	if skill != "技能：verify\n状态：已加载" {
		t.Fatalf("localized skill result = %q", skill)
	}
	if got := model.approvalActionSummary("coding.edit_hashline", "internal/tui/view.go"); got != "编辑文件 · internal/tui/view.go" {
		t.Fatalf("localized approval summary = %q", got)
	}
	model.runID = "run-zh"
	model.status = "Running"
	model.applyEvent(app.Event{
		Kind: app.EventApprovalRequested, RunID: "run-zh", ToolCallID: "edit-zh", ApprovalID: "approval-zh", State: "reviewing",
		Data: map[string]string{"tool": "coding.edit_hashline", "target": "README.md"},
	})
	model.applyEvent(app.Event{
		Kind: app.EventApprovalResolved, RunID: "run-zh", ToolCallID: "edit-zh", ApprovalID: "approval-zh", State: "auto_approved",
		Data: map[string]string{"tool": "coding.edit_hashline", "target": "README.md", "risk": "low", "rationale": "bounded edit"},
	})
	approval := ansi.Strip(strings.Join(model.renderBlock(model.transcript[0], 0, 40), "\n"))
	for _, wanted := range []string{"已允许", "已完成", "风险：low", "理由：bounded edit"} {
		if !strings.Contains(approval, wanted) {
			t.Fatalf("localized approval missing %q:\n%s", wanted, approval)
		}
	}

	model.openOverlay(OverlayCommand)
	content := ansi.Strip(model.renderOverlay(40, 20))
	if !strings.Contains(content, "命令面板") || !strings.Contains(content, "登录") || !strings.Contains(content, "选择提供商") {
		t.Fatalf("localized command palette missing:\n%s", content)
	}
	for lineNumber, line := range strings.Split(model.renderOverlay(40, 20), "\n") {
		if width := ansi.StringWidth(line); width > 40 {
			t.Fatalf("command palette line %d width = %d, want <= 40: %q", lineNumber+1, width, ansi.Strip(line))
		}
	}
}

func TestLanguageCommandSwitchesImmediatelyAndAcceptsTypoAlias(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, cmd := model.executeCommand(Command{Name: "language"})
	model = updated.(AppModel)
	if cmd != nil || model.overlay != OverlayLanguage || model.overlayOptionCount() != 2 {
		t.Fatalf("language picker = overlay:%q count:%d cmd:%v", model.overlay, model.overlayOptionCount(), cmd != nil)
	}
	picker := ansi.Strip(model.renderOverlay(50, 16))
	for _, wanted := range []string{"INTERFACE LANGUAGE", "English", "简体中文", "en", "zh-CN", "SELECTED"} {
		if !strings.Contains(picker, wanted) {
			t.Fatalf("language picker missing %q:\n%s", wanted, picker)
		}
	}
	model.overlayCursor = 1
	updated, cmd = model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil || model.overlay != OverlayNone || model.catalog.Language() != "en" {
		t.Fatalf("language picker selection = overlay:%q language:%q cmd:%v", model.overlay, model.catalog.Language(), cmd != nil)
	}
	result := cmd().(actionResultMsg)
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if model.catalog.Language() != "zh-CN" || len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionSetLanguage || runtime.actions[0].Target != "zh-CN" {
		t.Fatalf("persisted language selection = language:%q actions:%#v", model.catalog.Language(), runtime.actions)
	}

	command, ok, err := ParseCommand("/langauge zh-CN")
	if err != nil || !ok || command.Name != "language" {
		t.Fatalf("language alias parse = %#v, %v, %v", command, ok, err)
	}
	updated, cmd = model.executeCommand(command)
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("language command did not persist selection")
	}
	result = cmd().(actionResultMsg)
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if model.catalog.Language() != "zh-CN" || model.composer.Placeholder != "描述要更改、检查或验证的内容" {
		t.Fatalf("language switch = language:%q placeholder:%q cmd:%v", model.catalog.Language(), model.composer.Placeholder, cmd != nil)
	}
	model.composer.SetValue("/lang")
	suggestions := model.visibleCommandSuggestions()
	if len(suggestions) == 0 || suggestions[0].Name != "language" || suggestions[0].Detail != "切换界面语言" {
		t.Fatalf("localized language suggestion = %#v", suggestions)
	}
	updated, _ = model.executeCommand(Command{Name: "language", Args: []string{"de"}})
	model = updated.(AppModel)
	if model.errorBanner != "语言必须是 en 或 zh-CN" || model.catalog.Language() != "zh-CN" {
		t.Fatalf("invalid language = banner:%q language:%q", model.errorBanner, model.catalog.Language())
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

func TestTodoOverlayNavigationStaleEventsAndBoundedRendering(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.todo = session.TodoList{Goal: "Ship", Revision: 3, Phases: []session.TodoPhase{{
		ID: "phase-1", Title: "Build", Items: []session.TodoItem{
			{ID: "done", Content: "Analyze", Status: session.TodoCompleted},
			{ID: "current", Content: "Implement", Status: session.TodoInProgress},
			{ID: "next", Content: "Verify", Status: session.TodoPending},
		},
	}}}
	model.openOverlay(OverlayTodos)
	if model.overlayOptionCount() != 3 {
		t.Fatalf("todo option count=%d", model.overlayOptionCount())
	}
	updated, _ := model.updateOverlayKey("down")
	model = updated.(AppModel)
	if model.overlayCursor != 1 {
		t.Fatalf("todo cursor=%d, want 1", model.overlayCursor)
	}
	updated, _ = model.updateOverlayKey("h")
	model = updated.(AppModel)
	if !model.todoHideCompleted || model.overlayOptionCount() != 2 {
		t.Fatalf("hidden todo options=%d hide=%v", model.overlayOptionCount(), model.todoHideCompleted)
	}

	model.applyEvent(app.Event{Kind: app.EventTodoUpdated, Todo: &session.TodoList{Revision: 2}})
	if model.todo.Revision != 3 {
		t.Fatalf("stale todo event regressed revision to %d", model.todo.Revision)
	}
	model.todoHideCompleted = false
	for _, size := range [][2]int{{20, 8}, {40, 12}, {80, 24}, {120, 40}} {
		rendered := model.renderOverlay(size[0], size[1])
		lines := strings.Split(rendered, "\n")
		if len(lines) != size[1] {
			t.Fatalf("todo overlay height=%d, want %d at %v", len(lines), size[1], size)
		}
		for lineNumber, line := range lines {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Fatalf("todo overlay line %d width=%d, want <=%d", lineNumber+1, width, size[0])
			}
		}
	}
	if rendered := model.renderOverlay(80, 24); !strings.Contains(rendered, ";9m") {
		t.Fatalf("completed todo is not rendered with strikethrough: %q", rendered)
	}
	if rail := model.renderContextRail(31, 16); !strings.Contains(rail, ";9m") {
		t.Fatalf("completed rail todo is not rendered with strikethrough: %q", rail)
	}
}

func TestTodoRailShowsProgressInsteadOfInternalRevision(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	emptyRail := ansi.Strip(model.renderContextRail(31, 16))
	if strings.Contains(emptyRail, "r0") || strings.TrimSpace(strings.Split(emptyRail, "\n")[2]) != "TODO" {
		t.Fatalf("empty todo header should hide revision: %q", emptyRail)
	}

	model.todo = session.TodoList{Revision: 9, Phases: []session.TodoPhase{{Items: []session.TodoItem{
		{Content: "done", Status: session.TodoCompleted},
		{Content: "working", Status: session.TodoInProgress},
		{Content: "cancelled", Status: session.TodoCancelled},
	}}}}
	rail := ansi.Strip(model.renderContextRail(31, 16))
	if strings.Contains(rail, "r9") || !strings.Contains(rail, "TODO  1/2") {
		t.Fatalf("todo header should show user-facing progress: %q", rail)
	}
}

func TestTodoRowsAreNumberedAndBoundRunningSubagentsAnimate(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.todo = session.TodoList{Phases: []session.TodoPhase{{Title: "Work", Items: []session.TodoItem{
		{ID: "main", Content: "Main task", Status: session.TodoInProgress},
		{ID: "delegated", Content: "Delegated task", Status: session.TodoPending, SubagentRunID: "child-1"},
		{ID: "next", Content: "Next task", Status: session.TodoPending},
	}}}}
	model.agents = []AgentView{{ID: "child-1", State: "running"}}

	before := ansi.Strip(model.renderContextRail(40, 16))
	for _, wanted := range []string{"◐  1. Main task", "◐  2. Delegated task", "○  3. Next task"} {
		if !strings.Contains(before, wanted) {
			t.Fatalf("numbered todo rail omitted %q:\n%s", wanted, before)
		}
	}
	model.animationFrame++
	after := ansi.Strip(model.renderContextRail(40, 16))
	for _, wanted := range []string{"◓  1. Main task", "◓  2. Delegated task"} {
		if !strings.Contains(after, wanted) {
			t.Fatalf("running todo did not animate as %q:\n%s", wanted, after)
		}
	}

	model.openOverlay(OverlayTodos)
	overlay := ansi.Strip(model.renderOverlay(80, 24))
	for _, wanted := range []string{"1. Main task", "2. Delegated task", "3. Next task"} {
		if !strings.Contains(overlay, wanted) {
			t.Fatalf("numbered todo overlay omitted %q:\n%s", wanted, overlay)
		}
	}
}

func TestMemoryAndRecapCommandsAndOverlays(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single", "session-1")
	runCommand := func(command Command) {
		updated, cmd := model.executeCommand(command)
		model = updated.(AppModel)
		if cmd == nil {
			t.Fatalf("/%s did not start an action: %s", command.Name, model.errorBanner)
		}
		updated, _ = model.Update(cmd())
		model = updated.(AppModel)
	}

	runCommand(Command{Name: "memory", Args: []string{"cache", "policy"}})
	runCommand(Command{Name: "remember", Args: []string{"Use", "workspace", "scope"}})
	runCommand(Command{Name: "forget", Args: []string{"mem_123"}})
	runCommand(Command{Name: "recap"})
	wantKinds := []ActionKind{ActionListMemories, ActionRemember, ActionForgetMemory, ActionShowRecap}
	if len(runtime.actions) != len(wantKinds) {
		t.Fatalf("memory actions = %#v", runtime.actions)
	}
	for index, kind := range wantKinds {
		if runtime.actions[index].Kind != kind || runtime.actions[index].SessionID != "session-1" {
			t.Fatalf("action %d = %#v, want %s for session-1", index, runtime.actions[index], kind)
		}
	}
	if runtime.actions[0].Target != "cache policy" || runtime.actions[1].Target != "Use workspace scope" {
		t.Fatalf("command arguments were not preserved: %#v", runtime.actions)
	}

	now := time.Now()
	model.applyEvent(app.Event{Kind: app.EventMemoryState, State: "listed", Memories: []memory.Memory{{
		ID: "mem_123", Content: "Use workspace-scoped native memory", Provenance: "manual", Importance: 50, UpdatedAt: now,
	}}})
	if model.overlay != OverlayMemory || model.overlayOptionCount() != 1 {
		t.Fatalf("memory overlay = %q count=%d", model.overlay, model.overlayOptionCount())
	}
	plain := ansi.Strip(model.renderOverlay(64, 16))
	if !strings.Contains(plain, "mem_123") || !strings.Contains(plain, "workspace-scoped") {
		t.Fatalf("memory overlay omitted evidence identity/content:\n%s", plain)
	}

	model.applyEvent(app.Event{Kind: app.EventRecapState, State: "loaded", Recap: &recap.Recap{
		SessionID: "session-1", Goal: "Implement native memory", Summary: "Storage is complete", OpenItems: "Verify TUI", CoveredBoundary: "run-7", Revision: 2,
	}})
	if model.overlay != OverlayRecap || model.recap == nil {
		t.Fatalf("recap overlay = %q recap=%#v", model.overlay, model.recap)
	}
	plain = ansi.Strip(model.renderOverlay(64, 16))
	for _, wanted := range []string{"Implement native memory", "Storage is complete", "Verify TUI", "run-7"} {
		if !strings.Contains(plain, wanted) {
			t.Fatalf("recap overlay omitted %q:\n%s", wanted, plain)
		}
	}
}

func TestMemoryRecallAppearsInTranscriptWithoutOpeningOverlay(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single", "session-1")
	model.applyEvent(app.Event{
		Kind: app.EventMemoryState, SessionID: "session-1", State: "recalled",
		Data: map[string]string{"count": "2"},
	})
	if model.overlay != OverlayNone {
		t.Fatalf("memory recall opened overlay %q", model.overlay)
	}
	if len(model.transcript) != 1 || model.transcript[0].Kind != BlockHook {
		t.Fatalf("memory recall transcript = %#v", model.transcript)
	}
	rendered := ansi.Strip(strings.Join(model.renderBlock(model.transcript[0], 0, 64), "\n"))
	if !strings.Contains(rendered, "Recalled 2 workspace memories") {
		t.Fatalf("memory recall is not visible: %q", rendered)
	}
}

func TestRecapOverlayCachesLongLayoutAndOnlySlicesVisibleWindow(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	var summary strings.Builder
	for index := 0; index < 500; index++ {
		fmt.Fprintf(&summary, "recap row %03d contains enough context to wrap predictably\n", index)
	}
	model.recap = &recap.Recap{Goal: "Keep scrolling smooth", Summary: summary.String(), OpenItems: "Verify cached rendering", Revision: 1}
	model.openOverlay(OverlayRecap)

	first := model.recapDescriptionLines(72)
	second := model.recapDescriptionLines(72)
	if len(first) < 500 || &first[0] != &second[0] {
		t.Fatal("stable recap layout was wrapped again")
	}
	top := ansi.Strip(model.renderOverlay(80, 24))
	model.overlayScroll = 300
	scrolled := ansi.Strip(model.renderOverlay(80, 24))
	third := model.recapDescriptionLines(72)
	if &first[0] != &third[0] {
		t.Fatal("scrolling invalidated the recap layout cache")
	}
	if top == scrolled || strings.Count(scrolled, "\n")+1 != 24 {
		t.Fatalf("recap viewport was not sliced to the visible window:\n%s", scrolled)
	}

	model.recap.Summary = "updated recap"
	updated := model.recapDescriptionLines(72)
	if len(updated) == 0 || &first[0] == &updated[0] || !strings.Contains(strings.Join(updated, "\n"), "updated recap") {
		t.Fatal("changed recap reused stale cached lines")
	}
}

func TestRecapUpdateIsVisibleWhenIdleAndResumesWithSession(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single", "session-1")
	value := recap.Recap{
		SessionID: "session-1", Goal: "Ship visible recap", Summary: "Recap now stays visible near the composer",
		OpenItems: "Verify the overlay", CoveredBoundary: "run-9", Revision: 3,
	}
	model.status = "Running"
	model.runID = "run-9"
	model.applyEvent(app.Event{Kind: app.EventRecapState, SessionID: "session-1", RunID: "run-9", State: "updated", Recap: &value})
	if model.overlay != OverlayNone || model.recap == nil {
		t.Fatalf("background recap update interrupted the UI: overlay=%q recap=%#v", model.overlay, model.recap)
	}
	if view := ansi.Strip(model.View().Content); strings.Contains(view, "※ recap:") {
		t.Fatalf("recap should not displace live run status:\n%s", view)
	}
	model.applyEvent(app.Event{Kind: app.EventRunFinished, SessionID: "session-1", RunID: "run-9", State: "completed"})
	view := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"※ recap:", "Recap now stays visible near the composer", "/recap for details"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("visible recap status omitted %q:\n%s", wanted, view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines != model.height {
		t.Fatalf("recap status escaped viewport: got %d lines, want %d", lines, model.height)
	}

	model.applyEvent(app.Event{
		Kind: app.EventSessionLoaded, SessionID: "session-2", State: "loaded", Recap: &recap.Recap{
			SessionID: "session-2", Summary: "Resumed continuity", Revision: 1,
		}, Data: map[string]string{"blocks": "[]"},
	})
	if model.sessionID != "session-2" || model.recap == nil || model.recap.Summary != "Resumed continuity" {
		t.Fatalf("resumed recap was not restored: session=%q recap=%#v", model.sessionID, model.recap)
	}
}

func TestRecapStatusUsesConcisePlainTextPreview(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width = 240
	original := "## Result\n\n**Recap display is complete.** The full session memory remains available. Implementation details should stay in the overlay."
	model.recap = &recap.Recap{Summary: original}
	status := strings.TrimSpace(ansi.Strip(model.renderRecapStatus(model.width)))
	for _, unwanted := range []string{"##", "**", "Implementation details"} {
		if strings.Contains(status, unwanted) {
			t.Fatalf("recap status retained %q: %q", unwanted, status)
		}
	}
	for _, wanted := range []string{"Recap display is complete.", "The full session memory remains available."} {
		if !strings.Contains(status, wanted) {
			t.Fatalf("recap status omitted %q: %q", wanted, status)
		}
	}
	if model.recap.Summary != original {
		t.Fatalf("status preview mutated persistent recap: %q", model.recap.Summary)
	}

	preview := recapStatusPreview(strings.Repeat("context ", 80))
	if len(strings.Fields(strings.TrimSuffix(preview, "…"))) > 40 || ansi.StringWidth(preview) > 120 || !strings.HasSuffix(preview, "…") {
		t.Fatalf("recap preview was not concise: words=%d width=%d value=%q", len(strings.Fields(preview)), ansi.StringWidth(preview), preview)
	}
}
func TestRecapStatusFallsBackWhenSummaryHasNoPlainText(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.recap = &recap.Recap{Summary: "```go\nfmt.Println(\"details\")\n```", Goal: "Keep continuity visible"}
	status := ansi.Strip(model.renderRecapStatus(100))
	if !strings.Contains(status, "Keep continuity visible") || strings.Contains(status, "fmt.Println") {
		t.Fatalf("recap fallback status = %q", status)
	}
}

func TestRecapStatusKeepsMutedDarkTreatment(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.recap = &recap.Recap{Summary: "Keep continuity visible"}
	status := model.renderRecapStatus(80)
	emptyBackground := fmt.Sprint(lipgloss.NewStyle().GetBackground())
	if background := fmt.Sprint(model.theme.Muted.GetBackground()); background != emptyBackground {
		t.Fatalf("muted recap background = %s, want unset (%s)", background, emptyBackground)
	}
	if !strings.Contains(ansi.Strip(status), "Keep continuity visible") {
		t.Fatalf("recap text missing: %q", ansi.Strip(status))
	}
}

func TestRecapStatusYieldsWhileUserIsTyping(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.recap = &recap.Recap{Summary: "Keep the transcript usable"}
	model.composer.SetValue("draft request")
	if status := model.visibleRecapStatus(80, 24); status != "" {
		t.Fatalf("recap status competed with composer draft: %q", ansi.Strip(status))
	}
}

func TestRecapStatusYieldsToTinyViewport(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.recap = &recap.Recap{Summary: "Keep the transcript usable"}
	model.width = 24
	model.height = 2
	view := ansi.Strip(model.View().Content)
	if strings.Contains(view, "recap") || strings.Count(view, "\n")+1 != model.height {
		t.Fatalf("tiny viewport should omit recap status:\n%s", view)
	}
}

func TestRecapCommandPaletteOpensRecap(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single", "session-1")
	model.openOverlay(OverlayCommand)
	for index, option := range commandPaletteOptions {
		if option == "recap" {
			model.overlayCursor = index
			break
		}
	}
	updated, cmd := model.activatePaletteOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("recap command-palette item did not start an action")
	}
	updated, _ = model.Update(cmd())
	model = updated.(AppModel)
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionShowRecap || runtime.actions[0].SessionID != "session-1" {
		t.Fatalf("recap palette actions = %#v", runtime.actions)
	}
}

func TestClipboardImagePasteAttachesAndSubmits(t *testing.T) {
	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if err := model.appendPendingImage(session.Attachment{ID: "1", Name: "a.png", MIME: "image/png", Path: "/tmp/a.png", Size: 10}); err != nil {
		t.Fatal(err)
	}
	if model.renderPendingAttachments(80) == "" {
		t.Fatal("expected pending attachment strip")
	}
	model.composer.SetValue("what is in the image?")
	updated, cmd := model.submit()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("submit cmd nil")
	}
	_ = cmd()
	if len(model.pendingImages) != 0 {
		t.Fatalf("pending images not cleared: %#v", model.pendingImages)
	}
	if len(model.transcript) != 1 || !strings.Contains(model.transcript[0].Content, "a.png") {
		t.Fatalf("transcript = %#v", model.transcript)
	}
	if runtime.request.Prompt != "what is in the image?" || len(runtime.request.Images) != 1 || runtime.request.Images[0].Name != "a.png" {
		t.Fatalf("turn request = %#v", runtime.request)
	}
}

func TestClipboardImageResultMsgAppendsPending(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(clipboardImageResultMsg{attachment: session.Attachment{ID: "x", Name: "clip.png", MIME: "image/png", Path: "/tmp/clip.png", Size: 3}})
	model = updated.(AppModel)
	if len(model.pendingImages) != 1 || model.pendingImages[0].Name != "clip.png" {
		t.Fatalf("pending = %#v", model.pendingImages)
	}
}

func TestSourceToolsRenderSyntaxAndStructuredGutters(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	search := Block{
		Kind: BlockTool, Title: "coding.search", State: "completed",
		Arguments: `{"query":"Remember","glob":"internal/**/*.go"}`,
		Content: strings.Join([]string{
			"¶internal/app/actions.go#7A99",
			`56:const ActionRemember ActionKind = "remember"`,
			"57:// Keep explicit memories private",
			"¶internal/app/app.go#33A9",
			"111:func (s *Service) AttachMemory() {}",
		}, "\n"),
	}
	rendered := strings.Join(model.renderBlock(search, 0, 72), "\n")
	plain := ansi.Strip(rendered)
	for _, wanted := range []string{"SEARCH CODE", "internal/app/actions.go", "56 │ const", "internal/app/app.go", "111 │ func"} {
		if !strings.Contains(strings.ToUpper(plain), strings.ToUpper(wanted)) {
			t.Fatalf("source search missing %q:\n%s", wanted, plain)
		}
	}
	if strings.Count(rendered, "\x1b[") < 8 {
		t.Fatalf("search output lacks token-level syntax styling:\n%s", plain)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 74 {
			t.Fatalf("search line width=%d, exceeds 74: %q", ansi.StringWidth(line), ansi.Strip(line))
		}
	}

	read := Block{
		Kind: BlockTool, Title: "coding.read_file", State: "completed",
		Arguments: `{"path":"internal/app/app.go"}`,
		Content:   "¶internal/app/app.go#33A9\n111:func answer() string { return \"ok\" }",
	}
	readRendered := strings.Join(model.renderBlock(read, 0, 72), "\n")
	if !strings.Contains(ansi.Strip(readRendered), "111 │ func answer() string") || readRendered == rendered {
		t.Fatalf("read_file did not use source rendering:\n%s", ansi.Strip(readRendered))
	}
}

func TestSourceToolRenderingFitsNarrowWidths(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	block := Block{
		Kind: BlockTool, Title: "coding.search", State: "completed",
		Content: "¶internal/example/really_long_file_name.go#HASH\n123:func VeryLongFunctionName(value string) string { return value + \"suffix\" }",
	}
	for _, width := range []int{12, 18, 28} {
		rendered := model.renderBlock(block, 0, width)
		if len(rendered) < 2 {
			t.Fatalf("width %d lost source rows: %#v", width, rendered)
		}
		for _, line := range rendered {
			if ansi.StringWidth(line) > width+2 {
				t.Fatalf("width %d rendered line width=%d: %q", width, ansi.StringWidth(line), ansi.Strip(line))
			}
		}
	}
}

func TestToolCategoriesAndApprovalBodyUseDistinctMutedAccents(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	names := []string{"coding.search", "coding.read_file", "coding.edit_hashline", "coding.shell", "todo", "memory.search"}
	foregrounds := make(map[string]string)
	for _, name := range names {
		_, accent := model.toolStyles(name)
		foreground := fmt.Sprint(accent.GetForeground())
		if previous := foregrounds[foreground]; previous != "" {
			t.Fatalf("tool categories %s and %s share accent %s", previous, name, foreground)
		}
		foregrounds[foreground] = name
	}
	approval := Block{
		Kind: BlockApproval, Title: "coding.edit_hashline", State: "reviewing",
		Content: "Edit file · internal/app/app.go\nRisk: low\nReason: dry-run preview only",
	}
	rendered := strings.Join(model.renderBlock(approval, 0, 64), "\n")
	plain := ansi.Strip(rendered)
	for _, wanted := range []string{"APPROVAL", "Risk:", "low", "Reason:", "dry-run preview only"} {
		if !strings.Contains(strings.ToUpper(plain), strings.ToUpper(wanted)) {
			t.Fatalf("approval body missing %q:\n%s", wanted, plain)
		}
	}
	if strings.Count(rendered, "\x1b[") < 5 {
		t.Fatalf("approval body is still rendered as one flat color:\n%s", plain)
	}
}

func TestTranscriptKindsUseDistinctMutedAccentsWithoutCardBackgrounds(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	styles := map[string]lipgloss.Style{
		"user": model.theme.UserSurface, "assistant": model.theme.AssistantTag,
		"thinking": model.theme.ThinkingTag, "tool": model.theme.ToolTag,
		"agent": model.theme.AgentTag, "approval": model.theme.ApprovalTag,
		"error": model.theme.ErrorTag,
	}
	emptyBackground := fmt.Sprint(lipgloss.NewStyle().GetBackground())
	seen := make(map[string]string)
	for kind, style := range styles {
		if background := fmt.Sprint(style.GetBackground()); background != emptyBackground {
			t.Fatalf("%s transcript accent has card background %s, want unset", kind, background)
		}
		foreground := fmt.Sprint(style.GetForeground())
		if previous := seen[foreground]; previous != "" {
			t.Fatalf("%s and %s share the same transcript accent", previous, kind)
		}
		seen[foreground] = kind
	}

	cases := []struct {
		block Block
		label string
	}{
		{Block{Kind: BlockAssistant, Content: "## Result\n\nDone.", State: "completed"}, "AZEM"},
		{Block{Kind: BlockThinking, Content: "Checking constraints", State: "streaming"}, model.tr("block.thinking")},
		{Block{Kind: BlockTool, Title: "coding.read_file", Content: "result", State: "completed"}, model.tr("block.tool")},
		{Block{Kind: BlockAgent, Title: "reviewer", Content: "reviewing", State: "running"}, model.tr("block.agent")},
		{Block{Kind: BlockApproval, Title: "shell", Content: "run command", State: "awaiting approval"}, model.tr("block.approval")},
		{Block{Kind: BlockError, Title: "provider", Content: "request failed", State: "failed"}, model.tr("block.error")},
	}
	for _, test := range cases {
		rendered := strings.Join(model.renderBlock(test.block, 0, 72), "\n")
		if !strings.Contains(ansi.Strip(rendered), test.label) || !strings.Contains(rendered, "\x1b[") {
			t.Fatalf("%s block lacks semantic label/style:\n%s", test.block.Kind, ansi.Strip(rendered))
		}
		for _, line := range strings.Split(rendered, "\n") {
			if ansi.StringWidth(line) > 74 {
				t.Fatalf("%s line width=%d, exceeds 74: %q", test.block.Kind, ansi.StringWidth(line), ansi.Strip(line))
			}
		}
	}
}

func TestAttachmentStripUsesStyledLabelAndFitsWidth(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.pendingImages = []session.Attachment{{Name: "design.png"}, {Name: "trace.webp"}}
	rendered := model.renderPendingAttachments(72)
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "ATTACHMENTS 2/6") || !strings.Contains(plain, "design.png") || !strings.Contains(plain, "Esc remove last") {
		t.Fatalf("attachment strip lacks hierarchy: %q", plain)
	}
	if ansi.StringWidth(rendered) != 72 || !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("attachment strip width/style invalid: width=%d value=%q", ansi.StringWidth(rendered), plain)
	}
}

func TestComposerRendersSingleRoundedPanel(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	styles := model.composer.Styles()
	if styles.Focused.Base.GetHorizontalFrameSize() != 0 || styles.Blurred.Base.GetHorizontalFrameSize() != 0 {
		t.Fatal("textarea Base must remain unframed to avoid nested placeholder borders")
	}
	// Default bubbles CursorLine uses a solid background; clear it so the dock has no inner bar.
	emptyBG := fmt.Sprint(lipgloss.NewStyle().GetBackground())
	if bg := fmt.Sprint(styles.Focused.CursorLine.GetBackground()); bg != emptyBG {
		t.Fatalf("focused CursorLine background = %s, want cleared (%s)", bg, emptyBG)
	}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	panel := ansi.Strip(model.renderComposer())
	lines := strings.Split(panel, "\n")
	if len(lines) != 3 {
		t.Fatalf("empty composer height = %d, want one 3-line panel:\n%s", len(lines), panel)
	}
	if !strings.HasPrefix(lines[0], "╭") || !strings.HasSuffix(lines[0], "╮") ||
		!strings.HasPrefix(lines[1], "│") || !strings.HasSuffix(lines[1], "│") ||
		!strings.HasPrefix(lines[2], "╰") || !strings.HasSuffix(lines[2], "╯") {
		t.Fatalf("composer panel chrome is malformed:\n%s", panel)
	}
	if strings.Contains(lines[1], "╭") || strings.Contains(lines[1], "╰") {
		t.Fatalf("composer contains a nested border:\n%s", panel)
	}
	for index, line := range lines {
		if ansi.StringWidth(line) != 80 {
			t.Fatalf("composer line %d width = %d, want 80", index, ansi.StringWidth(line))
		}
	}
	view := model.View()
	if view.Cursor == nil {
		t.Fatal("composer cursor missing from docked view")
	}
	if view.Cursor.Position.X < 2 || view.Cursor.Position.Y < 2 {
		t.Fatalf("composer cursor = %+v, expected offset inside external panel", view.Cursor.Position)
	}
}

func TestDockFooterDeaggregatesOnSpaciousTerminal(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single")
	model.approvalMode = ApprovalModeAutoReview
	model.autoReviewAvailable = true
	model.usage.ContextLimit = 500_000
	model.usage.InputTokens = 153_000
	model.usage.CacheInputTokens = 4_300_000
	model.usage.CachedInputTokens = 4_100_000
	model.usage.CacheReported = true
	model.usage.MainCacheInput = 4_300_000
	model.usage.MainCachedInput = 4_100_000
	model.usage.MainCacheReported = true
	model.status = "Ready"

	if got := dockFooterLines(24, 100); got != 3 {
		t.Fatalf("spacious footer lines = %d, want 3", got)
	}
	if got := dockFooterLines(10, 80); got != 2 {
		t.Fatalf("medium footer lines = %d, want 2", got)
	}
	if got := dockFooterLines(5, 40); got != 1 {
		t.Fatalf("compact footer lines = %d, want 1", got)
	}

	footer := ansi.Strip(model.renderDockFooter(100, 3))
	lines := strings.Split(footer, "\n")
	if len(lines) != 3 {
		t.Fatalf("dock footer height = %d, want 3:\n%s", len(lines), footer)
	}
	// Runtime identity, context meter, and help each own a row — no longer one jammed strip.
	if !strings.Contains(lines[0], "Ready") || !strings.Contains(lines[0], "grok/grok-4.5") {
		t.Fatalf("runtime strip missing status/model: %q", lines[0])
	}
	if !strings.Contains(lines[1], "CTX") || !strings.Contains(lines[1], "500K") || !strings.Contains(lines[1], "CACHE") {
		t.Fatalf("context strip missing meter/cache: %q", lines[1])
	}
	// Primary occupancy and cache are separate facts; transport noise stays off the default strip.
	if strings.Contains(lines[1], "xai-responses") || strings.Contains(lines[1], "U ") {
		t.Fatalf("default context strip is still overloaded: %q", lines[1])
	}
	if !strings.Contains(lines[2], "Drag") || !strings.Contains(lines[2], "Ctrl+R") {
		t.Fatalf("help strip missing shortcuts: %q", lines[2])
	}
	if strings.Contains(lines[0], "CTX") || strings.Contains(lines[1], "Ctrl+R") || strings.Contains(lines[2], "CTX") {
		t.Fatalf("footer zones are still aggregated:\n%s", footer)
	}

	// Medium terminals keep metrics alone — shortcuts stay on the 3-line layout / compact status line.
	medium := ansi.Strip(model.renderDockFooter(80, 2))
	mediumLines := strings.Split(medium, "\n")
	if len(mediumLines) != 2 || strings.Contains(medium, "Ctrl+R") {
		t.Fatalf("medium footer should not mix help into metrics:\n%s", medium)
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = updated.(AppModel)
	content := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"grok/grok-4.5", "CTX", "Drag", "SMART"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("spacious view missing %q:\n%s", wanted, content)
		}
	}
}

func TestMeasureViewLayoutReservesDockFooter(t *testing.T) {
	layout := measureViewLayout(24, 100, 3, 0, 0)
	if !layout.showChrome || layout.footerHeight != 3 {
		t.Fatalf("spacious layout = %+v", layout)
	}
	// header(1) + rule(1) + footer(3) + composer >=1 leave body room.
	if layout.bodyHeight < 10 || layout.composerHeight < 1 {
		t.Fatalf("body/composer heights collapsed: %+v", layout)
	}
	if composerOffsetY(layout) != 2+layout.bodyHeight {
		t.Fatalf("composer offset = %d, want %d", composerOffsetY(layout), 2+layout.bodyHeight)
	}

	medium := measureViewLayout(8, 40, 1, 0, 0)
	if medium.footerHeight != 1 || !medium.showChrome {
		t.Fatalf("medium layout = %+v", medium)
	}
	tiny := measureViewLayout(5, 40, 1, 0, 0)
	if tiny.footerHeight != 1 || tiny.showChrome {
		t.Fatalf("tiny layout = %+v", tiny)
	}
}
