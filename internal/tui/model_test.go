package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/app"
	backgroundservice "github.com/Viking602/azem/internal/background"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/i18n"
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

func assertTranscriptStatusOnly(t *testing.T, footer, label string) {
	t.Helper()
	fields := strings.Fields(ansi.Strip(footer))
	if len(fields) != 2 || fields[1] != label {
		t.Fatalf("transcript status is too detailed: %q", ansi.Strip(footer))
	}
}

func assertTranscriptTimedStatus(t *testing.T, footer, label string) {
	t.Helper()
	fields := strings.Fields(ansi.Strip(footer))
	if len(fields) != 3 || fields[1] != label || !strings.HasSuffix(fields[2], "s") {
		t.Fatalf("transcript status timer is missing or too detailed: %q", ansi.Strip(footer))
	}
}

func TestTextInputsUseBarCursors(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if model.composer.VirtualCursor() || model.composer.Styles().Cursor.Shape != tea.CursorBar {
		t.Fatalf("composer cursor = virtual:%v shape:%v, want real bar", model.composer.VirtualCursor(), model.composer.Styles().Cursor.Shape)
	}
	if model.modelSearch.VirtualCursor() || model.modelSearch.Styles().Cursor.Shape != tea.CursorBar {
		t.Fatalf("search cursor = virtual:%v shape:%v, want real bar", model.modelSearch.VirtualCursor(), model.modelSearch.Styles().Cursor.Shape)
	}
	if model.settingsSearch.VirtualCursor() || model.settingsSearch.Styles().Cursor.Shape != tea.CursorBar {
		t.Fatalf("settings search cursor = virtual:%v shape:%v, want real bar", model.settingsSearch.VirtualCursor(), model.settingsSearch.Styles().Cursor.Shape)
	}
	view := model.View()
	if view.Cursor == nil || view.Cursor.Shape != tea.CursorBar {
		t.Fatalf("view cursor = %#v, want visible bar", view.Cursor)
	}
	model.openOverlay(OverlayModel)
	view = model.View()
	if view.Cursor == nil || view.Cursor.Shape != tea.CursorBar {
		t.Fatalf("model search cursor = %#v, want visible bar", view.Cursor)
	}
	model.openOverlay(OverlaySettings)
	updated, _ := model.updateOverlayKeyMsg(tea.KeyPressMsg{Code: '/', Text: "/"})
	model = updated.(AppModel)
	view = model.View()
	if view.Cursor == nil || view.Cursor.Shape != tea.CursorBar {
		t.Fatalf("settings search cursor = %#v, want visible bar", view.Cursor)
	}
}

func TestSentUserMessageUsesElevatedBandWithoutSenderLabel(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	block := Block{Kind: BlockUser, Title: "You", Content: "为 hooks 单独设计一个提示，不要太明显"}
	lines := model.renderBlock(block, 0, 28)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if len(lines) < 3 || !strings.Contains(plain, "›") || strings.Contains(plain, model.tr("block.user")) || strings.Contains(plain, "You") {
		t.Fatalf("sent message did not render as an unlabeled prompt dialog:\n%s", plain)
	}
	if !strings.Contains(plain, "╭") || !strings.Contains(plain, "╰") {
		t.Fatalf("sent message is not a floating dialog card:\n%s", plain)
	}
	hasSurface := false
	for _, line := range lines {
		if strings.Contains(line, ";48;") {
			hasSurface = true
		}
		if width := ansi.StringWidth(line); width > 28 {
			t.Fatalf("sent message width = %d, exceeds 28: %q", width, ansi.Strip(line))
		}
	}
	if !hasSurface {
		t.Fatalf("sent message lacks its elevated background:\n%s", plain)
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
	userPlain := ansi.Strip(strings.Join(model.renderBlock(Block{Kind: BlockUser, Content: "hi"}, 0, 40), "\n"))
	if !strings.Contains(userPlain, "› hi") || strings.Contains(plain, "›") {
		t.Fatalf("prompt direction is not distinct: user=%q assistant=%q", userPlain, plain)
	}
	if !strings.Contains(userPlain, "╭") {
		t.Fatalf("user prompt is not a dialog card: %q", userPlain)
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
	content := ansi.Strip(model.View().Content)
	if !strings.Contains(content, "⌁") || !strings.Contains(content, "/tmp/workspace") || strings.Contains(content, "◈ Azem") {
		t.Fatalf("view does not use quiet session chrome:\n%s", content)
	}
	if strings.Contains(content, "⌁ default") {
		t.Fatalf("header still shows session id instead of branch/path chrome:\n%s", content)
	}
}

func TestModalOverlayRetainsMainViewAndFullScreenDetailReplacesIt(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/main-background", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	model = updated.(AppModel)
	model.openOverlay(OverlayHelp)
	modal := ansi.Strip(model.View().Content)
	if !strings.Contains(modal, "/tmp/main-background") || !strings.Contains(modal, "HELP") {
		t.Fatalf("modal did not preserve main view behind overlay:\n%s", modal)
	}

	model.agents = []AgentView{{ID: "agent-1", Role: "explore", State: "running"}}
	model.detailAgentID = "agent-1"
	model.openOverlay(OverlayAgentDetail)
	fullscreen := ansi.Strip(model.View().Content)
	if strings.Contains(fullscreen, "/tmp/main-background") || !strings.Contains(fullscreen, "TASK DETAIL") {
		t.Fatalf("full-screen detail did not replace main view:\n%s", fullscreen)
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

func TestPlanModeTogglesAndPropagatesToTurn(t *testing.T) {
	runtime := &configuredTurnRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "team")

	updated, _ := model.executeCommand(Command{Name: "plan"})
	model = updated.(AppModel)
	if !model.planMode || model.agentMode != "single" {
		t.Fatalf("enabled plan mode = plan:%v agent:%q", model.planMode, model.agentMode)
	}
	if rendered := ansi.Strip(model.renderComposer()); !strings.Contains(rendered, "PLAN") {
		t.Fatalf("plan mode is not visible in composer: %q", rendered)
	}

	model.composer.SetValue("inspect the cache path and plan a fix")
	updated, cmd := model.submit()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("plan prompt did not start a turn")
	}
	cmd()
	if !runtime.request.PlanMode || runtime.request.AgentMode != "single" {
		t.Fatalf("plan turn request = %+v", runtime.request)
	}

	model.status = "Ready"
	updated, _ = model.executeCommand(Command{Name: "team", Args: []string{"on"}})
	model = updated.(AppModel)
	if model.planMode || model.agentMode != "team" {
		t.Fatalf("team mode did not exit plan mode = plan:%v agent:%q", model.planMode, model.agentMode)
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

func TestWideLayoutKeepsTranscriptFullWidthWithoutContextRail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "team")
	model.applyEvent(app.Event{Kind: app.EventAgentState, AgentID: "child-1", State: "running", Agent: &app.AgentStatePayload{Type: "review"}})
	model.applyEvent(app.Event{Kind: app.EventMCPState, State: "ready", Data: map[string]string{"server": "files", "toolCount": "3"}})

	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 120, Height: 40}} {
		updated, _ := model.Update(size)
		model = updated.(AppModel)
		content := ansi.Strip(model.View().Content)
		if strings.Contains(content, "RUN CONTEXT") {
			t.Fatalf("%d-column layout rendered the old context rail:\n%s", size.Width, content)
		}
		if got := bodyTranscriptWidth(size.Width, size.Height); got != size.Width-1 {
			t.Fatalf("%d-column transcript width = %d, want %d", size.Width, got, size.Width-1)
		}
	}
}

func TestMCPContextRailShowsServerCountAndLocalizedConnectionState(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	empty := ansi.Strip(model.renderContextRail(31, 16))
	if !strings.Contains(empty, "MCP  0") {
		t.Fatalf("empty MCP rail does not show the server count:\n%s", empty)
	}
	model.applyEvent(app.Event{Kind: app.EventMCPState, State: "ready", Data: map[string]string{"server": "grep", "toolCount": "1"}})
	connected := ansi.Strip(model.renderContextRail(31, 16))
	if !strings.Contains(connected, "MCP  1") || !strings.Contains(connected, "grep · 已连接") || strings.Contains(connected, "grep · 1") {
		t.Fatalf("connected MCP rail does not separate server count and status:\n%s", connected)
	}
}

func TestMCPServerEnterOpensLocalizedToolDetailsAndEscReturns(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	encoded := `[{"name":"grep","state":"ready","toolCount":1,"tools":[{"name":"searchGitHub","description":"搜索公开 GitHub 代码","effect":"read_only","requiresApproval":false}]}]`
	model.applyEvent(app.Event{Kind: app.EventMCPState, State: "snapshot", Data: map[string]string{"servers": encoded}})
	model.openOverlay(OverlayMCP)

	updated, _ := model.updateOverlayKey("enter")
	model = updated.(AppModel)
	if model.overlay != OverlayMCPDetail || model.detailMCPName != "grep" {
		t.Fatalf("MCP enter opened overlay=%q detail=%q", model.overlay, model.detailMCPName)
	}
	content := ansi.Strip(model.renderOverlay(100, 30))
	for _, wanted := range []string{"MCP 服务器详情", "grep · 已连接", "searchGitHub", "搜索公开 GitHub 代码", "操作类型：只读", "审批：不需要"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("MCP detail missing %q:\n%s", wanted, content)
		}
	}

	updated, _ = model.updateOverlayKey("esc")
	model = updated.(AppModel)
	if model.overlay != OverlayMCP || model.overlayCursor != 0 {
		t.Fatalf("MCP detail escape returned overlay=%q cursor=%d", model.overlay, model.overlayCursor)
	}
}

func TestRunningSubagentAnimatesInContextRail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "team")
	updated, _ := model.Update(appEventMsg{Event: app.Event{
		Kind: app.EventAgentState, AgentID: "child-1", State: "running",
		Agent: &app.AgentStatePayload{Type: "worker"},
	}})
	model = updated.(AppModel)
	if !model.animationActive || !model.hasRunningAgents() {
		t.Fatalf("running subagent did not start animation: active=%v agents=%#v", model.animationActive, model.agents)
	}
	before := ansi.Strip(model.renderContextRail(32, 16))
	model.animationFrame++
	after := ansi.Strip(model.renderContextRail(32, 16))
	if before == after || !strings.Contains(before, "◇ worker") || !strings.Contains(after, "◈ worker") {
		t.Fatalf("subagent indicator did not animate:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	model.reducedMotion = true
	if mark := model.agentStateMark("running"); mark != "◆" {
		t.Fatalf("reduced-motion subagent mark=%q", mark)
	}
}

func TestReviewingApprovalUsesStandaloneAnimatedRunStatus(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if got, want := model.stateStyle("Reviewing approval").GetForeground(), model.theme.ApprovalSmart.GetForeground(); got != want {
		t.Fatalf("reviewing approval color=%v, want smart approval color=%v", got, want)
	}
	model.runID = "run-1"
	model.status = "Reviewing approval"
	before := ansi.Strip(model.renderTranscriptFooter(64, 0, 0))
	model.animationFrame++
	after := ansi.Strip(model.renderTranscriptFooter(64, 0, 0))
	if before == after || !strings.Contains(before, "REVIEWING APPROVAL") || len(model.transcript) != 0 {
		t.Fatalf("standalone approval status did not animate: before=%q after=%q transcript=%#v", before, after, model.transcript)
	}
}

func TestContextArtifactToolUsesLocalizedDisplayName(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	block := Block{Kind: BlockTool, Title: "context.read_artifact", State: "completed", Collapsed: true}
	rendered := ansi.Strip(strings.Join(model.renderBlock(block, 0, 64), "\n"))
	if !strings.Contains(rendered, "Read Artifact") || strings.Contains(rendered, "context.read_artifact") {
		t.Fatalf("artifact tool did not use its English display name:\n%s", rendered)
	}
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	rendered = ansi.Strip(strings.Join(model.renderBlock(block, 0, 64), "\n"))
	if !strings.Contains(rendered, "读取工件") || strings.Contains(rendered, "context.read_artifact") {
		t.Fatalf("artifact tool did not use its Chinese display name:\n%s", rendered)
	}
}

func TestSubagentToolsUseLocalizedDisplayNames(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	tests := []struct {
		name    string
		english string
		chinese string
	}{
		{name: "subagent.spawn", english: "Start Subagent", chinese: "启动子代理"},
		{name: "subagent.get_output", english: "Get Subagent Output", chinese: "获取子代理输出"},
		{name: "subagent.kill", english: "Stop Subagent", chinese: "停止子代理"},
	}
	for _, test := range tests {
		if got := model.toolDisplayName(test.name); got != test.english {
			t.Fatalf("English display name for %s = %q, want %q", test.name, got, test.english)
		}
	}
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		if got := model.toolDisplayName(test.name); got != test.chinese {
			t.Fatalf("Chinese display name for %s = %q, want %q", test.name, got, test.chinese)
		}
	}
}

func TestTranscriptCardsAreKeyboardExpandable(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.transcript = []Block{{ID: "call-1", Kind: BlockTool, Title: "coding.read_file", Content: "result", State: "completed"}}
	model.width = 100
	if content := ansi.Strip(model.View().Content); !strings.Contains(content, " Read File") || strings.Contains(content, "coding.read_file") {
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

func TestCompletedToolBlocksDefaultToCollapsed(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.updateTool(app.Event{
		Kind: app.EventToolStarted, RunID: "run", ToolCallID: "call-1",
		Data: map[string]string{"name": "coding.shell", "arguments": `{"command":"pwd"}`},
	})
	if model.transcript[0].Collapsed {
		t.Fatal("running tool block should remain expanded")
	}
	model.updateTool(app.Event{
		Kind: app.EventToolFinished, RunID: "run", ToolCallID: "call-1", State: "completed",
		Data: map[string]string{"name": "coding.shell"}, Text: "/tmp/workspace",
	})
	if !model.transcript[0].Collapsed {
		t.Fatal("completed tool block should collapse by default")
	}
	lines := model.renderBlock(model.transcript[0], 0, 80)
	if len(lines) != 1 || strings.Contains(ansi.Strip(lines[0]), "/tmp/workspace") {
		t.Fatalf("collapsed tool rendered its result: %#v", lines)
	}
}

func TestExpandingLongToolKeepsHeaderAtViewportStart(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	model = updated.(AppModel)
	var content strings.Builder
	for line := 1; line <= 40; line++ {
		fmt.Fprintf(&content, "result line %02d\n", line)
	}
	model.transcript = []Block{
		{Kind: BlockUser, RunID: "run-1", Content: "analyze the workspace", State: "completed"},
		{
			ID: "call-1", Kind: BlockTool, RunID: "run-1", Title: "coding.shell",
			Arguments: `{"command":"git status --short"}`,
			Content:   content.String(), State: "completed", Collapsed: true,
		},
	}
	_ = model.View()
	_, _, width, height := model.transcriptBounds()
	collapsed := strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")
	headerRow := -1
	for index, line := range collapsed {
		if strings.Contains(line, "Run Command") {
			headerRow = index
			break
		}
	}
	if headerRow < 0 || !model.toggleTranscriptBlockAt(4, headerRow) {
		t.Fatalf("tool header did not expand:\n%s", strings.Join(collapsed, "\n"))
	}

	_ = model.View()
	_, _, width, height = model.transcriptBounds()
	visible := strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")
	first := ""
	for _, line := range visible {
		if strings.TrimSpace(line) != "" {
			first = line
			break
		}
	}
	if !strings.Contains(first, "Run Command") {
		t.Fatalf("expanded tool header was not kept at the viewport start; first=%q:\n%s", first, strings.Join(visible, "\n"))
	}
	if strings.Contains(strings.Join(visible, "\n"), "result line 40") {
		t.Fatalf("expanded tool remained anchored to its last line:\n%s", strings.Join(visible, "\n"))
	}
}

func TestToolHeaderAnimatesThenSettlesToCheckmark(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	running := Block{ID: "call-1", Kind: BlockTool, Title: "coding.shell", State: "running"}
	if !transcriptBlockAnimated(running) {
		t.Fatal("running tool was excluded from transcript animation invalidation")
	}
	before := ansi.Strip(model.renderToolHeader(running, 80, false, false))
	model.animationFrame++
	after := ansi.Strip(model.renderToolHeader(running, 80, false, false))
	if before == after || !strings.Contains(before, "◇ Run Command") || !strings.Contains(after, "◈ Run Command") {
		t.Fatalf("running tool indicator did not animate: before=%q after=%q", before, after)
	}
	completed := running
	completed.State = "completed"
	if header := ansi.Strip(model.renderToolHeader(completed, 80, false, false)); !strings.Contains(header, " Run Command") {
		t.Fatalf("completed tool header=%q", header)
	}
	completed.Collapsed = true
	if header := ansi.Strip(model.renderToolHeader(completed, 80, false, false)); !strings.Contains(header, "✓ Run Command") || strings.Contains(header, "›") {
		t.Fatalf("collapsed tool header=%q", header)
	}
	hovered := model.renderToolHeader(completed, 80, false, true)
	if header := ansi.Strip(hovered); !strings.Contains(header, " Run Command") || strings.Contains(header, "✓") {
		t.Fatalf("hovered tool header=%q", header)
	}
	wantHovered := model.theme.Tool.Background(model.theme.UserSurface.GetBackground()).Render(padOrTrim("   Run Command", 80))
	if hovered != wantHovered {
		t.Fatalf("hovered tool header did not use the hover surface: %q", hovered)
	}
	unselected := model.renderToolHeader(completed, 80, false, false)
	wantSelected := model.theme.Selected.Background(model.theme.UserSurface.GetBackground()).Render(ansi.Strip(unselected))
	if selected := model.renderToolHeader(completed, 80, true, false); selected != wantSelected {
		t.Fatalf("selected tool header did not use normal text on selected surface: %q", selected)
	}
	model.reducedMotion = true
	if header := ansi.Strip(model.renderToolHeader(running, 80, false, false)); !strings.Contains(header, "◆ Run Command") {
		t.Fatalf("reduced-motion running header=%q", header)
	}
	if got, want := model.theme.Tool.GetForeground(), model.theme.Thinking.GetForeground(); got != want {
		t.Fatalf("tool foreground=%v want subdued gray=%v", got, want)
	}
}

func TestNonSourceToolOutputsUseSubduedGray(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	want := model.theme.BlockRail.Render("      │ ") + model.theme.Tool.Render("tool output")
	for _, title := range []string{"coding.shell", "coding.go_test", "coding.gofmt", "todo", "memory.search", "subagent.spawn"} {
		rows := model.renderToolContent(Block{Kind: BlockTool, Title: title, Content: "tool output", State: "completed"}, 80)
		if len(rows) != 1 || rows[0] != want {
			t.Fatalf("%s output=%q, want subdued gray %q", title, rows, want)
		}
	}
	listRows := model.renderToolContent(Block{Kind: BlockTool, Title: "coding.list_files", Content: "main.go", State: "completed"}, 80)
	wantList := model.theme.MetaDivider.Render("      │ ") + model.theme.Tool.Render("main.go")
	if len(listRows) != 1 || listRows[0] != wantList {
		t.Fatalf("list output=%q, want subdued gray %q", listRows, wantList)
	}
}

func TestCollapsedToolTimelineRendersWithoutBlankRows(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.transcript = []Block{
		{ID: "call-1", Kind: BlockTool, RunID: "run", Title: "coding.shell", State: "completed", Collapsed: true},
		{ID: "call-2", Kind: BlockTool, RunID: "run", Title: "coding.search", State: "completed", Collapsed: true},
		{Kind: BlockThinking, RunID: "run", Content: "Continue reasoning", State: "streaming"},
	}
	lines := model.transcriptLines(80)
	if len(lines) < 3 || !strings.Contains(ansi.Strip(lines[0]), "✓ Run Command") ||
		!strings.Contains(ansi.Strip(lines[1]), "✓ Search Code") ||
		!strings.Contains(ansi.Strip(lines[2]), "THINKING") {
		t.Fatalf("compact tool timeline=%q", ansi.Strip(strings.Join(lines, "\n")))
	}
}

func TestTranscriptToolHeaderTogglesWithMouseClick(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.transcript = []Block{{
		ID: "call-1", Kind: BlockTool, Title: "coding.shell", Content: "mouse result",
		State: "completed", Collapsed: true,
	}}
	_, top, width, height := model.transcriptBounds()
	rows := strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")
	headerRow := -1
	for index, row := range rows {
		if strings.Contains(row, "✓ Run Command") {
			headerRow = index
			break
		}
	}
	if headerRow < 0 {
		t.Fatalf("tool header was not rendered:\n%s", strings.Join(rows, "\n"))
	}
	click := tea.MouseClickMsg{X: 4, Y: top + headerRow, Button: tea.MouseLeft}
	release := tea.MouseReleaseMsg{X: 4, Y: top + headerRow, Button: tea.MouseLeft}
	updated, _ = model.Update(click)
	model = updated.(AppModel)
	updated, command := model.Update(release)
	model = updated.(AppModel)
	if model.transcript[0].Collapsed || command != nil {
		t.Fatalf("first click = collapsed:%v command:%v, want expanded without copy", model.transcript[0].Collapsed, command != nil)
	}
	if content := ansi.Strip(model.renderTranscript(width, height)); !strings.Contains(content, " Run Command") || !strings.Contains(content, "mouse result") {
		t.Fatalf("expanded tool result is not visible:\n%s", content)
	}

	rows = strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")
	for index, row := range rows {
		if strings.Contains(row, " Run Command") {
			headerRow = index
			break
		}
	}
	click.Y, release.Y = top+headerRow, top+headerRow
	updated, _ = model.Update(click)
	model = updated.(AppModel)
	updated, command = model.Update(release)
	model = updated.(AppModel)
	if !model.transcript[0].Collapsed || command != nil {
		t.Fatalf("second click = collapsed:%v command:%v, want collapsed without copy", model.transcript[0].Collapsed, command != nil)
	}
}

func TestToolHeaderHoverSwapsCheckmarkForExpandIndicator(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.transcript = []Block{
		{ID: "call-1", Kind: BlockTool, Title: "coding.shell", State: "completed", Collapsed: true},
		{Kind: BlockAssistant, Content: "## Stable assistant output", State: "completed"},
	}
	_, top, width, height := model.transcriptBounds()
	headerRow := -1
	for index, row := range strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n") {
		if strings.Contains(row, "✓ Run Command") {
			headerRow = index
			break
		}
	}
	if headerRow < 0 {
		t.Fatal("collapsed tool header was not rendered")
	}
	stableAssistantLine := &model.transcriptLayout.blocks[1].lines[0]
	updated, _ = model.Update(tea.MouseMotionMsg{X: 4, Y: top + headerRow})
	model = updated.(AppModel)
	if content := ansi.Strip(model.renderTranscript(width, height)); !strings.Contains(content, " Run Command") || strings.Contains(content, "✓ Run Command") {
		t.Fatalf("hovered collapsed tool did not replace the checkmark:\n%s", content)
	}
	if stableAssistantLine != &model.transcriptLayout.blocks[1].lines[0] {
		t.Fatal("hovering a tool rerendered an unrelated transcript block")
	}
	updated, _ = model.Update(tea.MouseMotionMsg{X: 4, Y: top + height})
	model = updated.(AppModel)
	if content := ansi.Strip(model.renderTranscript(width, height)); !strings.Contains(content, "✓ Run Command") || strings.Contains(content, " Run Command") {
		t.Fatalf("tool did not restore the checkmark after hover left:\n%s", content)
	}
}

func TestSecondCompactToolCanExpandAndCollapseWithMouse(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.transcript = []Block{
		{ID: "call-1", Kind: BlockTool, Title: "coding.shell", State: "completed", Collapsed: true},
		{ID: "call-2", Kind: BlockTool, Title: "coding.search", Content: "mouse result", State: "completed", Collapsed: true},
	}
	_, top, width, height := model.transcriptBounds()
	toggleSecond := func(current AppModel) AppModel {
		rows := strings.Split(ansi.Strip(current.renderTranscript(width, height)), "\n")
		headerRow := -1
		header := "✓ Search Code"
		if !current.transcript[1].Collapsed {
			header = " Search Code"
		}
		for index, row := range rows {
			if strings.Contains(row, header) {
				headerRow = index
				break
			}
		}
		if headerRow < 0 {
			t.Fatalf("second compact tool header was not rendered:\n%s", strings.Join(rows, "\n"))
		}
		next, _ := current.Update(tea.MouseClickMsg{X: 4, Y: top + headerRow, Button: tea.MouseLeft})
		current = next.(AppModel)
		next, command := current.Update(tea.MouseReleaseMsg{X: 4, Y: top + headerRow, Button: tea.MouseLeft})
		if command != nil {
			t.Fatal("tool toggle unexpectedly copied transcript text")
		}
		return next.(AppModel)
	}

	model = toggleSecond(model)
	if model.transcript[1].Collapsed {
		t.Fatal("second compact tool did not expand")
	}
	model = toggleSecond(model)
	if !model.transcript[1].Collapsed {
		t.Fatal("expanded second compact tool did not collapse")
	}
}

func TestMouseClickTargetsToolHeaderAfterUserMessageWithoutBlankSeparator(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.transcript = []Block{
		{Kind: BlockUser, Content: "run this", State: "completed"},
		{ID: "call-1", Kind: BlockTool, Title: "coding.shell", Content: "result", State: "completed", Collapsed: true},
	}
	_, top, width, height := model.transcriptBounds()
	rows := strings.Split(ansi.Strip(model.renderTranscript(width, height)), "\n")
	headerRow := -1
	for index, row := range rows {
		if strings.Contains(row, "✓ Run Command") {
			headerRow = index
			break
		}
	}
	if headerRow < 0 {
		t.Fatalf("tool header was not rendered:\n%s", strings.Join(rows, "\n"))
	}
	updated, _ = model.Update(tea.MouseClickMsg{X: 4, Y: top + headerRow, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, _ = model.Update(tea.MouseReleaseMsg{X: 4, Y: top + headerRow, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.transcript[1].Collapsed {
		t.Fatal("tool header after user message did not expand")
	}
}

func TestMouseClickReactivatesComposer(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockTool, State: "completed"}}
	if !model.selectTranscript() || model.composer.Focused() {
		t.Fatal("test setup did not move focus away from the composer")
	}

	left, top, width, height := model.composerBounds()
	updated, _ = model.Update(tea.MouseClickMsg{X: left + width/2, Y: top + height/2, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.focus != focusComposer || !model.composer.Focused() {
		t.Fatalf("composer focus after click = area:%d focused:%v", model.focus, model.composer.Focused())
	}
	updated, _ = model.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	model = updated.(AppModel)
	if got := model.composer.Value(); got != "x" {
		t.Fatalf("composer value after click = %q, want keyboard input", got)
	}
}

func renderedTextPoint(t *testing.T, rendered, needle string) (int, int) {
	t.Helper()
	for row, line := range strings.Split(ansi.Strip(rendered), "\n") {
		offset := strings.Index(line, needle)
		if offset >= 0 {
			return ansi.StringWidth(line[:offset]), row
		}
	}
	t.Fatalf("rendered output does not contain %q:\n%s", needle, ansi.Strip(rendered))
	return 0, 0
}

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

func TestScrollbarClickAndDragMovesTranscript(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	for index := range 40 {
		model.transcript = append(model.transcript, Block{Kind: BlockUser, Content: fmt.Sprintf("message %02d", index)})
	}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	model = updated.(AppModel)
	_, top, width, height := model.transcriptBounds()
	maxOffset := model.transcriptMaxOffset()
	if maxOffset == 0 {
		t.Fatal("test transcript does not overflow")
	}

	updated, _ = model.Update(tea.MouseClickMsg{X: width, Y: top, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.transcriptTop != maxOffset {
		t.Fatalf("top scrollbar click offset = %d, want %d", model.transcriptTop, maxOffset)
	}
	updated, _ = model.Update(tea.MouseMotionMsg{X: width, Y: top + height - 1, Button: tea.MouseLeft})
	model = updated.(AppModel)
	updated, _ = model.Update(tea.MouseReleaseMsg{X: width, Y: top + height - 1, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.transcriptTop != 0 {
		t.Fatalf("bottom scrollbar drag offset = %d, want 0", model.transcriptTop)
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

func TestShellLifecycleUpdatesDoNotPolluteCommandSummary(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.updateTool(app.Event{
		Kind: app.EventToolStarted, RunID: "run", ToolCallID: "shell", Data: map[string]string{
			"name": "coding.shell", "arguments": `{"command":"sleep 10"}`,
		},
	})
	want := model.transcript[0].Content
	for _, update := range []app.Event{
		{Kind: app.EventToolUpdate, RunID: "run", ToolCallID: "shell", State: "started", Text: "sleep 10"},
		{Kind: app.EventToolUpdate, RunID: "run", ToolCallID: "shell", State: "progress", Text: "0 output bytes"},
		{Kind: app.EventToolUpdate, RunID: "run", ToolCallID: "shell", State: "finished", Text: "exit 0 (exited)"},
	} {
		model.updateTool(update)
	}
	if got := model.transcript[0].Content; got != want || strings.Contains(got, "output bytes") {
		t.Fatalf("shell command summary=%q, want %q", got, want)
	}
}

func TestShellFinishedUpdateSettlesCommandState(t *testing.T) {
	for _, test := range []struct {
		name, status, exitCode, reason, want string
	}{
		{name: "success", status: "exited", exitCode: "0", want: "completed"},
		{name: "failure", status: "exited", exitCode: "1", want: "failed"},
		{name: "stopped", status: "stopped", exitCode: "-1", reason: "timeout", want: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
			model.updateTool(app.Event{Kind: app.EventToolStarted, RunID: "run", ToolCallID: "shell", Data: map[string]string{"name": "coding.shell"}})
			model.updateTool(app.Event{Kind: app.EventToolUpdate, RunID: "run", ToolCallID: "shell", State: "finished", Data: map[string]string{
				"status": test.status, "exit_code": test.exitCode, "reason": test.reason,
			}})
			if got := model.transcript[0].State; got != test.want {
				t.Fatalf("state=%q, want %q", got, test.want)
			}
		})
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

func TestFileChangesRemainNamedToolsWithInlineDiffs(t *testing.T) {
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
	if edit.Kind != BlockTool || edit.Title != "coding.edit_hashline" {
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
	if created.Kind != BlockTool || created.Title != "coding.write_file" {
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
		"12 - old value",
		"12 + new value",
		"13   unchanged",
		"A internal/tui/view.go  +1 -0",
		"@@ line 40 @@",
	} {
		if !strings.Contains(plain, wanted) {
			t.Fatalf("rendered diff omitted %q:\n%s", wanted, plain)
		}
	}
	if strings.Contains(plain, "13 13") {
		t.Fatalf("context line rendered duplicate old/new line numbers:\n%s", plain)
	}
	shifted := ansi.Strip(strings.Join(model.renderDiffContent(
		"diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -74,2 +77,2 @@\n-old\n+new\n context",
		72,
	), "\n"))
	for _, wanted := range []string{"74 - old", "77 + new", "78   context"} {
		if !strings.Contains(shifted, wanted) {
			t.Fatalf("shifted diff omitted %q:\n%s", wanted, shifted)
		}
	}
	if strings.Contains(shifted, "75 78") {
		t.Fatalf("shifted context rendered two line-number columns:\n%s", shifted)
	}
	aligned := strings.Split(ansi.Strip(strings.Join(model.renderDiffContent(
		"diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -53 +66 @@\n+first\n@@ -60 +106 @@\n+second",
		72,
	), "\n")), "\n")
	markerColumns := make([]int, 0, 2)
	for _, row := range aligned {
		if strings.Contains(row, "+ first") || strings.Contains(row, "+ second") {
			markerColumns = append(markerColumns, strings.Index(row, "+"))
		}
	}
	if len(markerColumns) != 2 || markerColumns[0] != markerColumns[1] {
		t.Fatalf("diff marker columns are not aligned: %v\n%s", markerColumns, strings.Join(aligned, "\n"))
	}
}

func TestGitDiffToolUsesAccessibleForegroundChangeStyling(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	emptyBackground := fmt.Sprint(lipgloss.NewStyle().GetBackground())
	for name, style := range map[string]lipgloss.Style{
		"added": model.theme.DiffAdd, "deleted": model.theme.DiffDel, "hunk": model.theme.DiffHunk,
	} {
		if background := fmt.Sprint(style.GetBackground()); background != emptyBackground {
			t.Fatalf("%s diff style background = %s, want unset", name, background)
		}
	}
	if fmt.Sprint(model.theme.DiffAdd.GetForeground()) == fmt.Sprint(model.theme.DiffDel.GetForeground()) {
		t.Fatal("added and deleted rows use the same foreground color")
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
	for _, wanted := range []string{"M main.go  +1 -1", "@@ -7 +7 @@", "7 - ", "7 + ", "oldCall()", "newCall()"} {
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
	// ChatGPT uses write-token cache semantics; W counters should surface in /status.
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-test", "high", "single")
	model.usage.ContextLimit = 500_000
	model.updateUsage(map[string]string{
		"inputTokens": "100000", "cachedInputTokens": "60000", "uncachedInputTokens": "40000", "outputTokens": "5000",
		"cacheWriteTokens": "10000", "cacheStatus": "reported", "requestKind": "main", "transport": "codex-responses",
		"cacheModel": "write-tokens", "provider": "chatgpt",
	})
	model.updateUsage(map[string]string{"reasoningTokens": "3000", "requestKind": "main", "aggregateOnly": "true"})
	model.updateUsage(map[string]string{
		"inputTokens": "20000", "cachedInputTokens": "5000", "uncachedInputTokens": "15000", "cacheWriteTokens": "2000", "outputTokens": "2000", "reasoningTokens": "500",
		"cacheStatus": "reported", "requestKind": "compaction", "aggregateOnly": "true", "transport": "codex-responses",
	})
	model.updateUsage(map[string]string{
		"inputTokens": "30000", "cachedInputTokens": "18000", "uncachedInputTokens": "12000", "cacheWriteTokens": "1000", "outputTokens": "4000", "reasoningTokens": "700",
		"cacheStatus": "reported", "requestKind": "team", "aggregateOnly": "true", "transport": "codex-responses",
	})
	// Dense diagnostics belong in /status, not the footer strip.
	footer := ansi.Strip(model.renderContextUsage(320))
	for _, unwanted := range []string{"U 40K", "CMP ", "TEAM ", "codex-responses"} {
		if strings.Contains(footer, unwanted) {
			t.Fatalf("footer still leaks diagnostic %q: %q", unwanted, footer)
		}
	}
	report := strings.Join(model.statusReportLines(), "\n")
	for _, wanted := range []string{
		"Cache model: write-tokens (cached + cache write)",
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
		"Transport: codex-responses",
	} {
		if !strings.Contains(report, wanted) {
			t.Fatalf("status report missing %q:\n%s", wanted, report)
		}
	}
}

func TestGrokAutomaticCacheHidesWriteCounters(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single")
	model.usage.ContextLimit = 500_000
	model.updateUsage(map[string]string{
		"inputTokens": "100000", "cachedInputTokens": "60000", "uncachedInputTokens": "40000", "outputTokens": "5000",
		"cacheWriteTokens": "10000", "cacheStatus": "reported", "requestKind": "main", "transport": "xai-responses",
		"cacheModel": "automatic", "provider": "grok",
	})
	if model.usage.CacheWriteTokens != 0 || model.showsCacheWrite() {
		t.Fatalf("automatic cache still kept write counters: %+v", model.usage)
	}
	report := strings.Join(model.statusReportLines(), "\n")
	if strings.Contains(report, "Cache write") || strings.Contains(report, "(W):") {
		t.Fatalf("automatic cache still shows write diagnostics:\n%s", report)
	}
	if !strings.Contains(report, "automatic (hits via cached tokens; write N/A)") {
		t.Fatalf("status report missing automatic cache model:\n%s", report)
	}
}

func TestFactSnapshotPreservesExplicitZeroCacheWriteTelemetry(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "gpt-5.6-sol", "high", "single")
	model.updateUsage(map[string]string{
		"factSnapshot": "true", "usageSnapshot": `{"cacheWriteReported":true,"lastProvider":"chatgpt"}`,
		"requestKind": "review", "provider": "chatgpt", "model": "gpt-5.6-luna",
		"transport": "chatgpt-codex-responses", "cacheModel": "write-tokens",
	})
	if !model.usage.CacheWriteReported || model.usage.CacheModel != "write-tokens" || model.usage.LastRequestKind != "review" || model.usage.LastModel != "gpt-5.6-luna" {
		t.Fatalf("fact snapshot metadata=%+v", model.usage)
	}
	report := strings.Join(model.statusReportLines(), "\n")
	if !strings.Contains(report, "Cache write (W): 0") || strings.Contains(report, "Cache write (W): N/A") {
		t.Fatalf("explicit zero cache write was not distinguished from missing telemetry:\n%s", report)
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

	model.applyEvent(app.Event{Kind: app.EventModelRoutes, Data: map[string]string{"subagent_max_concurrency": "2"}, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "title", Label: "Title"},
		{Scope: "plan", Label: "Plan"},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("grok", "grok-4.5", "low")},
	}})
	if model.overlay != OverlayModelRoutes || len(model.overlayOptions()) != 4 {
		t.Fatalf("model routes overlay = %q options=%#v", model.overlay, model.overlayOptions())
	}
	rendered := ansi.Strip(model.renderOverlay(100, 24))
	for _, wanted := range []string{"MODEL ROUTING", "Session title", "Plan model", "Compaction", "Inherit from active agent", "explore", "grok/grok-4.5/low"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("model routes missing %q:\n%s", wanted, rendered)
		}
	}
}

func settingsMenuModel(t *testing.T) (AppModel, *recordedRuntime) {
	t.Helper()
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "gpt-main", "high", "single")
	model.modelsByProvider = map[string][]ModelChoice{
		"grok": {{ID: "grok-fast", SupportsReasoning: false}},
	}
	model.openOverlay(OverlayCommand)
	options := model.overlayOptions()
	if len(options) == 0 {
		t.Fatalf("settings menu entry = %#v", options)
	}
	if options[0].Label != "Settings" {
		t.Fatalf("settings menu label = %q", options[0].Label)
	}
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("settings menu did not load routes")
	}
	if model.overlayPurpose != "settings" {
		t.Fatalf("settings purpose = %q", model.overlayPurpose)
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 1 {
		t.Fatalf("settings list action = %#v", runtime.actions)
	}
	if runtime.actions[0].Kind != ActionListModelRoutes {
		t.Fatalf("settings list action kind = %q", runtime.actions[0].Kind)
	}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan"},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("chatgpt", "old-worker", "high")},
	}})
	return model, runtime
}

func assertTextContainsAll(t *testing.T, text string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("text missing %q:\n%s", value, text)
		}
	}
}

func settingsEntryCursor(t *testing.T, model AppModel, kind settingsEntryKind, section string, routeIndex int) int {
	t.Helper()
	for index, entry := range model.settingsEntries() {
		if entry.Kind == kind && entry.Section == section && (kind != settingsEntryRoute || entry.RouteIndex == routeIndex) {
			return index
		}
	}
	t.Fatalf("settings entry kind=%d section=%q route=%d not found", kind, section, routeIndex)
	return 0
}

func expandSettingsEntry(t *testing.T, model AppModel, kind settingsEntryKind, section string, routeIndex int) AppModel {
	t.Helper()
	model.overlayCursor = settingsEntryCursor(t, model, kind, section, routeIndex)
	entry := model.settingsEntries()[model.overlayCursor]
	updated, cmd := model.updateOverlayKey("right")
	model = updated.(AppModel)
	if cmd != nil || !model.settingsExpanded[entry.Key] {
		t.Fatalf("settings entry %q did not expand: expanded=%v cmd=%v", entry.Key, model.settingsExpanded, cmd != nil)
	}
	return model
}

func TestSettingsMenuRendersFunctionalCategoriesAndRoleModels(t *testing.T) {
	model, _ := settingsMenuModel(t)
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan"},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("chatgpt", "old-worker", "high")},
		{Scope: "subagent", Role: "plan", Label: "Produce a decision-complete implementation plan without changing the workspace."},
		{Scope: "subagent", Role: "review", Label: "Review a delegated change for requirement, correctness, and regression risks without editing."},
		{Scope: "subagent", Role: "verify", Label: "Run governed checks without editing and report exact outcomes."},
		{Scope: "subagent", Role: "worker", Label: "Implement one scoped coding task end-to-end and return verified evidence."},
	}})
	options := model.overlayOptions()
	if model.overlay != OverlaySettings {
		t.Fatalf("settings overlay = %q", model.overlay)
	}
	if len(options) != 13 {
		t.Fatalf("settings options = %#v", options)
	}
	for _, wanted := range []string{"Role models", "Plan model", "explore", "plan", "review", "verify", "worker", "Subagent runtime", "Max concurrency", "Codex subscription", "Fast mode", "Interface", "Language"} {
		found := false
		for _, option := range options {
			found = found || option.Label == wanted
		}
		if !found {
			t.Fatalf("settings options missing %q: %#v", wanted, options)
		}
	}
	rendered := ansi.Strip(model.renderOverlay(100, 24))
	assertTextContainsAll(t, rendered, "Settings", "[×]", "/ to search settings", "Role models", "Plan model", "Inherit from active agent", "explore", "chatgpt/old-worker/high", "Subagent runtime", "Codex subscription", "Interface")
	if strings.Contains(rendered, "Inspect the workspace") {
		t.Fatalf("collapsed setting leaked its description into the value column:\n%s", rendered)
	}

	model = expandSettingsEntry(t, model, settingsEntryRoute, settingsSectionModels, 1)
	expanded := ansi.Strip(model.renderOverlay(100, 24))
	assertTextContainsAll(t, expanded, "▾ explore", "Inspect the workspace")
	updated, _ := model.updateOverlayKey("left")
	model = updated.(AppModel)
	if len(model.settingsExpanded) != 0 {
		t.Fatalf("left did not collapse the focused setting: %v", model.settingsExpanded)
	}
	model = expandSettingsEntry(t, model, settingsEntryRoute, settingsSectionModels, 2)
	model.width, model.height = 100, 24
	descriptionClicked := false
	for row, line := range strings.Split(ansi.Strip(model.renderOverlay(model.width, model.height)), "\n") {
		if !strings.Contains(line, "decision-complete implementation plan") {
			continue
		}
		descriptionClicked = true
		left := strings.IndexRune(line, '│')
		updated, cmd := model.handleOverlayClick(tea.Mouse{X: left + 8, Y: row, Button: tea.MouseLeft})
		model = updated.(AppModel)
		if cmd != nil || model.pendingModelRoute != nil || model.overlay != OverlaySettings {
			t.Fatalf("clicking an expanded description activated its setting: cmd=%v pending=%#v overlay=%q", cmd != nil, model.pendingModelRoute, model.overlay)
		}
		break
	}
	if !descriptionClicked {
		t.Fatal("expanded plan description was not rendered")
	}

	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "Plan model") {
			if !strings.Contains(line, "›") || strings.Index(line, "Inherit from active agent")-strings.Index(line, "Plan model") < 8 {
				t.Fatalf("settings row is not a left-label/right-value layout: %q", line)
			}
			break
		}
	}
	if background := fmt.Sprint(model.theme.OverlaySelected.GetBackground()); background == fmt.Sprint(lipgloss.NewStyle().GetBackground()) {
		t.Fatal("settings selection has no highlighted background")
	}
	for _, viewport := range [][2]int{{40, 12}, {16, 8}, {8, 6}} {
		for row, line := range strings.Split(model.renderOverlay(viewport[0], viewport[1]), "\n") {
			if width := ansi.StringWidth(line); width > viewport[0] {
				t.Fatalf("settings viewport %dx%d row %d width=%d: %q", viewport[0], viewport[1], row, width, ansi.Strip(line))
			}
		}
	}
	if strings.Contains(rendered, "Compaction") {
		t.Fatalf("settings exposed non-subagent route:\n%s", rendered)
	}
	updated, _ = model.updateOverlayKeyMsg(tea.KeyPressMsg{Code: '/', Text: "/"})
	model = updated.(AppModel)
	for _, key := range "explore" {
		updated, _ = model.updateOverlayKeyMsg(tea.KeyPressMsg{Code: key, Text: string(key)})
		model = updated.(AppModel)
	}
	filtered := ansi.Strip(model.renderOverlay(100, 24))
	assertTextContainsAll(t, filtered, "Role models", "explore")
	if strings.Contains(filtered, "Plan model") || strings.Contains(filtered, "Subagent runtime") || strings.Contains(filtered, "Codex subscription") {
		t.Fatalf("settings search retained non-matching rows:\n%s", filtered)
	}
	updated, _ = model.updateOverlayKeyMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(AppModel)
	if model.settingsSearch.Value() != "" {
		t.Fatalf("settings search did not clear: %q", model.settingsSearch.Value())
	}
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	assertTextContainsAll(t, ansi.Strip(model.renderOverlay(100, 24)), "设置", "搜索设置", "角色模型", "规划模型", "子代理运行", "Codex 订阅", "界面")
}

func TestSettingsMenuUpdatesPlanModelAndReturns(t *testing.T) {
	model, runtime := settingsMenuModel(t)
	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryRoute, settingsSectionModels, 0)
	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.pendingModelRoute == nil {
		t.Fatal("settings did not select the plan route")
	}
	if model.pendingModelRoute.Entry.Scope != "plan" || model.pendingModelRoute.Entry.Role != "" {
		t.Fatalf("selected route = %#v", model.pendingModelRoute)
	}
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("settings model selection did not save")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 {
		t.Fatalf("settings model save action = %#v", runtime.actions)
	}
	if runtime.actions[1].Kind != ActionSetModelRoute {
		t.Fatalf("settings model save kind = %q", runtime.actions[1].Kind)
	}
	if runtime.actions[1].Route == nil {
		t.Fatal("settings model save route is nil")
	}
	if runtime.actions[1].Route.Scope != "plan" || runtime.actions[1].Route.Route.Model != "grok-fast" {
		t.Fatalf("settings model save route = %#v", runtime.actions[1].Route)
	}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan", Route: appModelRoute("grok", "grok-fast", "")},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace", Route: appModelRoute("chatgpt", "old-worker", "high")},
	}})
	if model.overlay != OverlaySettings {
		t.Fatalf("settings return after save = %q", model.overlay)
	}
}

func TestSettingsMenuReturnsFromLanguageAndResetsToInherit(t *testing.T) {
	model, runtime := settingsMenuModel(t)
	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryLanguage, settingsSectionInterface, -1)
	languageCursor := model.overlayCursor
	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlayLanguage {
		t.Fatalf("settings language picker = %q", model.overlay)
	}
	updated, _ = model.updateOverlayKey("esc")
	model = updated.(AppModel)
	if model.overlay != OverlaySettings {
		t.Fatalf("settings language return = %q", model.overlay)
	}
	if model.overlayCursor != languageCursor {
		t.Fatalf("settings language cursor = %d", model.overlayCursor)
	}

	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryRoute, settingsSectionModels, 0)
	updated, cmd := model.updateOverlayKey("d")
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("settings inherit reset did not start")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 {
		t.Fatalf("settings reset action = %#v", runtime.actions)
	}
	if runtime.actions[1].Kind != ActionResetModelRoute {
		t.Fatalf("settings reset kind = %q", runtime.actions[1].Kind)
	}
	if runtime.actions[1].Route == nil {
		t.Fatal("settings reset route is nil")
	}
	if runtime.actions[1].Route.Scope != "plan" || runtime.actions[1].Route.Role != "" {
		t.Fatalf("settings reset route = %#v", runtime.actions[1].Route)
	}
}

func TestSettingsMenuUpdatesSubagentConcurrencyAndReturns(t *testing.T) {
	model, runtime := settingsMenuModel(t)
	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryConcurrency, settingsSectionRuntime, -1)
	settingsCursor := model.overlayCursor
	updated, _ := model.activateOverlayOption()
	model = updated.(AppModel)
	if model.overlay != OverlaySubagentConcurrency || model.overlayCursor != 1 {
		t.Fatalf("concurrency picker = overlay:%q cursor:%d", model.overlay, model.overlayCursor)
	}
	model.overlayCursor = 5
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("concurrency selection did not save")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 || runtime.actions[1].Kind != ActionSetSubagentConcurrency || runtime.actions[1].Target != "6" {
		t.Fatalf("concurrency action = %#v", runtime.actions)
	}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, Data: map[string]string{"subagent_max_concurrency": "6"}, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan"},
		{Scope: "compaction", Label: "Compaction"},
		{Scope: "subagent", Role: "explore", Label: "Inspect the workspace"},
	}})
	if model.overlay != OverlaySettings || model.subagentConcurrency != 6 || model.overlayCursor != settingsCursor {
		t.Fatalf("settings return = overlay:%q concurrency:%d cursor:%d", model.overlay, model.subagentConcurrency, model.overlayCursor)
	}
}

func TestSettingsMenuTogglesChatGPTFastMode(t *testing.T) {
	model, runtime := settingsMenuModel(t)
	model.overlayCursor = settingsEntryCursor(t, model, settingsEntryFastMode, settingsSectionCodex, -1)
	settingsCursor := model.overlayCursor
	updated, cmd := model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("fast mode toggle did not save")
	}
	result := cmd()
	updated, _ = model.Update(result)
	model = updated.(AppModel)
	if len(runtime.actions) != 2 || runtime.actions[1].Kind != ActionSetChatGPTFastMode || runtime.actions[1].Target != "true" {
		t.Fatalf("fast mode action = %#v", runtime.actions)
	}
	model.applyEvent(app.Event{Kind: app.EventModelRoutes, Data: map[string]string{"chatgpt_fast_mode": "true"}, ModelRoutes: []app.ModelRouteEntry{
		{Scope: "plan", Label: "Plan"},
		{Scope: "subagent", Role: "explore"},
	}})
	if model.overlay != OverlaySettings || !model.chatGPTFastMode || model.overlayCursor != settingsCursor {
		t.Fatalf("fast mode return = overlay:%q enabled:%v cursor:%d", model.overlay, model.chatGPTFastMode, model.overlayCursor)
	}
	collapsed := ansi.Strip(model.renderOverlay(100, 24))
	assertTextContainsAll(t, collapsed, "Fast mode", "On")
	if strings.Contains(collapsed, "1.5x") {
		t.Fatalf("collapsed fast mode leaked description:\n%s", collapsed)
	}
	model = expandSettingsEntry(t, model, settingsEntryFastMode, settingsSectionCodex, -1)
	assertTextContainsAll(t, ansi.Strip(model.renderOverlay(100, 24)), "1.5x faster")
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

func TestHeaderBranchClickAndDirtyBranchConfirmation(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.branch = "main"
	model.width, model.height = 100, 24

	updated, cmd := model.handleMouseClick(tea.Mouse{X: 4, Y: 0, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("branch click did not start a list action")
	}
	result := cmd().(actionResultMsg)
	if result.Err != nil || result.Action.Kind != ActionListGitBranches {
		t.Fatalf("branch click result = %#v", result)
	}
	model.actionBusy = false

	model.applyEvent(app.Event{Kind: app.EventGitBranches, State: "listed", Text: "main", WorkspaceDirty: true, GitBranches: []app.GitBranchEntry{{Name: "feature"}, {Name: "main", Current: true}}})
	if model.overlay != OverlayBranches || model.overlayCursor != 1 || !model.branchDirty {
		t.Fatalf("branch list state = overlay:%q cursor:%d dirty:%v", model.overlay, model.overlayCursor, model.branchDirty)
	}
	model.overlayCursor = 0
	updated, cmd = model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd != nil || model.overlay != OverlayBranchConfirm || model.pendingBranch != "feature" {
		t.Fatalf("dirty selection = overlay:%q pending:%q cmd:%v", model.overlay, model.pendingBranch, cmd != nil)
	}
	updated, cmd = model.activateOverlayOption()
	model = updated.(AppModel)
	if cmd == nil {
		t.Fatal("confirmed switch did not start an action")
	}
	result = cmd().(actionResultMsg)
	if result.Err != nil || result.Action.Kind != ActionSwitchGitBranch || result.Action.Target != "feature" || result.Action.Decision != "confirm_dirty" {
		t.Fatalf("confirmed switch result = %#v", result)
	}

	model.actionBusy = false
	model.applyEvent(app.Event{Kind: app.EventGitBranches, State: "switched", Text: "feature", GitBranches: []app.GitBranchEntry{{Name: "feature", Current: true}, {Name: "main"}}})
	if model.branch != "feature" || model.overlay != OverlayNone || model.pendingBranch != "" {
		t.Fatalf("switched state = branch:%q overlay:%q pending:%q", model.branch, model.overlay, model.pendingBranch)
	}
}

func TestHeaderPathRemainsSeparateFromBranchClick(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.branch = "main"
	model.width, model.height = 100, 24
	segments := model.headerLeftSegments(model.width)
	branchStart, pathStart := -1, -1
	cursor := 0
	for _, segment := range segments {
		if segment.target == uiClickBranch {
			branchStart = cursor
		}
		if segment.target == uiClickWorkspace {
			pathStart = cursor
		}
		cursor += lipgloss.Width(segment.content)
	}
	if branchStart < 0 || pathStart < 0 || model.headerClickTarget(branchStart, 0) != uiClickBranch || model.headerClickTarget(pathStart, 0) != uiClickWorkspace {
		t.Fatalf("header targets = branch:%d/%v path:%d/%v", branchStart, model.headerClickTarget(branchStart, 0), pathStart, model.headerClickTarget(pathStart, 0))
	}
}

func TestToolAndThinkingIndicatorsShareColumn(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	tool := ansi.Strip(model.renderToolHeader(Block{Kind: BlockTool, Title: "coding.shell", State: "completed", Collapsed: true}, 80, false, false))
	thinking := ansi.Strip(model.renderThinkingMessage(Block{Kind: BlockThinking, State: "completed"}, 0, 80)[0])
	if !strings.HasPrefix(tool, "  ✓ ") || !strings.HasPrefix(thinking, "  ◆ ") {
		t.Fatalf("indicator columns differ: tool=%q thinking=%q", tool, thinking)
	}
}

func TestHeaderAgentsEntryIsAlwaysVisibleAndClickable(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "team")
	if err := model.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	model.width, model.height = 100, 24
	model.status, model.runID, model.runActivity = "Running", "run-1", "thinking"
	model.agents = []AgentView{{ID: "worker-1", State: "running"}, {ID: "reviewer-1", State: "completed"}}
	model.transcript = []Block{{Kind: BlockThinking, RunID: "run-1", State: "streaming", Content: "分析实现路径"}}
	header := ansi.Strip(model.renderHeader(model.width))
	entryByte := strings.Index(header, "子代理 2")
	if entryByte < 0 {
		t.Fatalf("header lacks the subagent entry while thinking: %q", header)
	}
	entryX := ansi.StringWidth(header[:entryByte])
	updated, command := model.handleMouseClick(tea.Mouse{X: entryX, Y: 0, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if command != nil || model.overlay != OverlayAgents {
		t.Fatalf("header subagent click = overlay:%q command:%v", model.overlay, command != nil)
	}
}

func TestActiveThinkingIndicatorAnimates(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "team")
	model.status, model.runID, model.runActivity = "Running", "run-1", "thinking"
	model.transcript = []Block{{Kind: BlockThinking, RunID: "run-1", State: "streaming", Content: "Inspecting"}}
	before := model.renderThinkingMessage(model.transcript[0], 0, 80)[0]
	model.animationFrame++
	after := model.renderThinkingMessage(model.transcript[0], 0, 80)[0]
	if before == after {
		t.Fatalf("active thinking indicator did not animate: %q", ansi.Strip(before))
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
		OverlayNone, OverlayHelp, OverlayStatus, OverlayCommand, OverlayProvider, OverlayModel, OverlayModelRoutes, OverlaySettings, OverlaySubagentConcurrency, OverlaySkills,
		OverlayReasoning, OverlaySessions, OverlayBranches, OverlayBranchConfirm, OverlayApproval, OverlayCancel, OverlayDiff, OverlayAgents,
		OverlayAgentDetail, OverlayAgentTypes, OverlayPersonas, OverlayMCP, OverlayMCPDetail, OverlayBackground, OverlayBackgroundDetail,
		OverlayRecovery, OverlayError,
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
		if edge := ansi.Cut(line, 119, 120); edge != " " {
			t.Fatalf("body line %d right edge=%q, want blank without overflow: %q", index, edge, line)
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

func TestTranscriptScrollbarTracksHistoryPositionAtPaneRightEdge(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	for index := range 80 {
		model.transcript = append(model.transcript, Block{
			Kind: BlockAssistant, Title: "Azem", Content: fmt.Sprintf("message %02d", index), State: "completed",
		})
	}
	_, _, transcriptWidth, transcriptHeight := model.transcriptBounds()

	thumbRows := func() []int {
		body := strings.Split(ansi.Strip(model.renderBody(model.width, transcriptHeight)), "\n")
		rows := make([]int, 0)
		for row, line := range body {
			if strings.Contains("▁▂▃▄▅▆▇█", ansi.Cut(line, transcriptWidth, transcriptWidth+1)) {
				rows = append(rows, row)
			}
		}
		return rows
	}

	latest := thumbRows()
	if len(latest) == 0 || latest[len(latest)-1] != transcriptHeight-1 {
		t.Fatalf("latest scrollbar thumb rows = %v, want bottom row %d", latest, transcriptHeight-1)
	}
	model.transcriptTop = model.transcriptMaxOffset()
	oldest := thumbRows()
	if len(oldest) == 0 || oldest[0] != 0 {
		t.Fatalf("oldest scrollbar thumb rows = %v, want top row", oldest)
	}
	if fmt.Sprint(oldest) == fmt.Sprint(latest) {
		t.Fatalf("scrollbar thumb did not move: latest=%v oldest=%v", latest, oldest)
	}
}

func TestTranscriptScrollbarAppearsOnlyWhenHistoryOverflows(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: "short history", State: "completed"}}
	_, _, transcriptWidth, transcriptHeight := model.transcriptBounds()

	rightEdge := func() string {
		var edge strings.Builder
		for _, line := range strings.Split(ansi.Strip(model.renderBody(model.width, transcriptHeight)), "\n") {
			edge.WriteString(ansi.Cut(line, transcriptWidth, transcriptWidth+1))
		}
		return edge.String()
	}
	if edge := rightEdge(); strings.TrimSpace(edge) != "" {
		t.Fatalf("non-overflowing transcript rendered a scrollbar: %q", edge)
	}

	model.transcript = nil
	for range 80 {
		model.transcript = append(model.transcript, Block{Kind: BlockAssistant, Content: "history line", State: "completed"})
	}
	if edge := rightEdge(); !strings.ContainsAny(edge, "▁▂▃▄▅▆▇█") {
		t.Fatalf("overflowing transcript lacks a scrollbar thumb: %q", edge)
	}
}

func TestScrollbarThumbAdvancesByEighthCell(t *testing.T) {
	start, startSize := transcriptScrollbarThumb(20, 1000, 982, 0)
	next, nextSize := transcriptScrollbarThumb(20, 1000, 982, 6)
	if start-next != 1 {
		t.Fatalf("eighth-cell thumb movement = %d -> %d, want one eighth-cell", start, next)
	}
	if glyph, reverse := scrollbarThumbGlyph(19, start, startSize); glyph != "█" || reverse {
		t.Fatalf("aligned thumb cell = %q reverse:%v, want full block", glyph, reverse)
	}
	if glyph, reverse := scrollbarThumbGlyph(18, next, nextSize); glyph != "▁" || reverse {
		t.Fatalf("leading eighth-cell = %q reverse:%v, want lower one-eighth block", glyph, reverse)
	}
	if glyph, reverse := scrollbarThumbGlyph(19, next, nextSize); glyph != "▁" || !reverse {
		t.Fatalf("trailing seven-eighths cell = %q reverse:%v, want reversed lower one-eighth block", glyph, reverse)
	}
}

func TestTranscriptScrollbarReservesRightmostColumnWithoutContextRail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	for range 80 {
		model.transcript = append(model.transcript, Block{Kind: BlockAssistant, Content: "history line", State: "completed"})
	}
	_, _, transcriptWidth, transcriptHeight := model.transcriptBounds()
	if transcriptWidth != model.width-1 {
		t.Fatalf("transcript width = %d, want scrollbar-reserved width %d", transcriptWidth, model.width-1)
	}
	for row, line := range strings.Split(ansi.Strip(model.renderBody(model.width, transcriptHeight)), "\n") {
		if ansi.StringWidth(line) != model.width {
			t.Fatalf("body row %d width = %d, want %d", row, ansi.StringWidth(line), model.width)
		}
		glyph := ansi.Cut(line, model.width-1, model.width)
		if glyph != "│" && !strings.Contains("▁▂▃▄▅▆▇█", glyph) {
			t.Fatalf("body row %d rightmost glyph = %q, want scrollbar", row, glyph)
		}
	}
}

func TestTranscriptDragSelectionClampsToConversationPaneAndCopies(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: "dialogue only\nsecond dialogue line", State: "completed"}}
	if view := model.View(); view.MouseMode != tea.MouseModeAllMotion {
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

func TestAssistantMarkdownTableUsesContentWidth(t *testing.T) {
	content := "| 类型 | 文件数 | 代码量 |\n" +
		"| --- | ---: | ---: |\n" +
		"| 已跟踪文件修改 | 32 | +1192 / -91 |\n" +
		"| 未跟踪新文件 | 2 | 581 行 |\n" +
		"| 暂存区修改 | 0 | 0 |\n" +
		"| 合计 | 34 | 约 +1773 / -91，净增加 1682 行 |"
	rendered, err := renderTerminalMarkdown(content, 120)
	if err != nil {
		t.Fatal(err)
	}
	maxWidth := 0
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		maxWidth = max(maxWidth, ansi.StringWidth(line))
	}
	if maxWidth > 72 {
		t.Fatalf("markdown table width=%d, want content-sized table <=72:\n%s", maxWidth, ansi.Strip(rendered))
	}
}

func TestAssistantMarkdownDoesNotOverrideTerminalBackground(t *testing.T) {
	for _, key := range []markdownRendererKey{{width: 72}, {width: 72, dark: true}} {
		renderer, err := newTerminalMarkdownRenderer(key)
		if err != nil {
			t.Fatalf("create markdown renderer (dark=%t): %v", key.dark, err)
		}
		rendered, err := renderer.Render("# Result\n\n`inline code`\n\n```go\npackage main\n```")
		if err != nil {
			t.Fatalf("render markdown (dark=%t): %v", key.dark, err)
		}
		if strings.Contains(rendered, "\x1b[48;") {
			t.Fatalf("markdown emitted a background ANSI sequence (dark=%t): %q", key.dark, rendered)
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
	assertTranscriptTimedStatus(t, last, "RUNNING")
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

func TestRunActivitySummaryShowsPhaseElapsedAndSilence(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Running"
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	model.runStartedAt = now.Add(-84 * time.Second)
	model.runActivityAt = now.Add(-17 * time.Second)
	model.runActivity = "waiting_after_tool"
	model.runActivityDetail = "todo"

	summary := model.runActivitySummary(now)
	for _, wanted := range []string{"todo finished; waiting for model", "elapsed 1m24s", "no new events for 17s"} {
		if !strings.Contains(summary, wanted) {
			t.Fatalf("activity summary missing %q: %q", wanted, summary)
		}
	}
	assertTranscriptTimedStatus(t, model.renderTranscriptFooter(120, 0, 0), "RUNNING")
}

func TestActivityDurationFormatsSecondsMinutesAndHours(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{duration: 8 * time.Second, want: "8s"},
		{duration: 3*time.Minute + 8*time.Second, want: "3m08s"},
		{duration: time.Hour + 2*time.Minute + 3*time.Second, want: "1h02m03s"},
	}
	for _, test := range tests {
		if got := formatActivityDuration(test.duration); got != test.want {
			t.Fatalf("formatActivityDuration(%s) = %q, want %q", test.duration, got, test.want)
		}
	}
}

func TestRunActivityTracksToolCompletionAndReducedMotionHeartbeat(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Starting"
	model.beginRunActivity()
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-activity"})
	model.applyEvent(app.Event{
		Kind: app.EventToolFinished, SessionID: "default", RunID: "run-activity", ToolCallID: "todo-1",
		State: "completed", Data: map[string]string{"name": "todo"},
	})
	if model.runActivity != "waiting_after_tool" || model.runActivityDetail != "todo" {
		t.Fatalf("tool completion activity = %q %q", model.runActivity, model.runActivityDetail)
	}
	model.reducedMotion = true
	frame := model.animationFrame
	updated, command := model.Update(animationTickMsg{})
	model = updated.(AppModel)
	if command == nil || model.animationFrame != frame {
		t.Fatalf("reduced-motion heartbeat = command:%v frame:%d, want scheduled without animation", command != nil, model.animationFrame)
	}
}

func TestProviderRetryActivityShowsProgressAndRecovers(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.status = "Starting"
	model.beginRunActivity()
	model.applyEvent(app.Event{Kind: app.EventRunStarted, SessionID: "default", RunID: "run-retry"})
	model.applyEvent(app.Event{
		Kind: app.EventProviderRetry, SessionID: "default", RunID: "run-retry", Text: "upstream connection reset by peer",
		Data: map[string]string{"attempt": "2", "max": "5", "delay_ms": "400"},
	})
	if model.runActivity != "retrying" {
		t.Fatalf("retry activity=%q", model.runActivity)
	}
	summary := model.runActivitySummary(model.runActivityAt)
	for _, wanted := range []string{"retry 2/5 in 400ms", "upstream connection reset"} {
		if !strings.Contains(summary, wanted) {
			t.Fatalf("retry summary missing %q: %q", wanted, summary)
		}
	}

	model.applyEvent(app.Event{Kind: app.EventThinkingDelta, SessionID: "default", RunID: "run-retry", Text: "recovered"})
	if model.runActivity != "thinking" {
		t.Fatalf("activity after provider recovery=%q, want thinking", model.runActivity)
	}
}

func TestProviderSessionRetryDiscardsOnlyUncommittedAssistantTail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.sessionID = "default"
	model.runID = "run-retry"
	model.transcript = []Block{{Kind: BlockUser, RunID: "run-retry", Content: "keep me", State: "submitted"}}
	model.applyEvent(app.Event{Kind: app.EventThinkingDelta, SessionID: "default", RunID: "run-retry", Text: "partial thought"})
	model.applyEvent(app.Event{Kind: app.EventTextDelta, SessionID: "default", RunID: "run-retry", Text: "partial answer"})
	if len(model.transcript) != 3 {
		t.Fatalf("partial transcript blocks=%+v", model.transcript)
	}
	model.applyEvent(app.Event{
		Kind: app.EventProviderRetry, SessionID: "default", RunID: "run-retry",
		Data: map[string]string{"attempt": "1", "max": "10", "delay_ms": "0", "reset_partial": "true"},
	})
	if len(model.transcript) != 1 || model.transcript[0].Kind != BlockUser || model.transcript[0].Content != "keep me" {
		t.Fatalf("retry reset removed durable transcript or kept partial output: %+v", model.transcript)
	}
}

func TestSubagentSessionRetryDiscardsChildPartialTail(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.sessionID = "default"
	model.agents = []AgentView{{
		ID: "child-1",
		Blocks: []Block{
			{Kind: BlockTool, RunID: "child-run", Content: "durable tool", State: "completed"},
			{Kind: BlockThinking, RunID: "child-run", Content: "partial thought", State: "streaming"},
			{Kind: BlockAssistant, RunID: "child-run", Content: "partial answer", State: "streaming"},
		},
	}}
	model.applyEvent(app.Event{
		Kind: app.EventProviderRetry, SessionID: "default", RunID: "child-run", AgentID: "child-1",
		Text: "server overloaded",
		Data: map[string]string{"attempt": "1", "max": "10", "delay_ms": "0", "reset_partial": "true"},
	})
	if len(model.agents[0].Blocks) != 1 || model.agents[0].Blocks[0].Kind != BlockTool {
		t.Fatalf("child retry reset blocks=%+v", model.agents[0].Blocks)
	}
	if !strings.Contains(model.agents[0].Activity, "retry") {
		t.Fatalf("child retry activity=%q", model.agents[0].Activity)
	}
}

func TestProviderRetryActivityIsLocalizedInChinese(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.catalog, _ = i18n.New("zh-CN")
	model.status = "Running"
	model.runID = "run-retry-zh"
	model.applyEvent(app.Event{
		Kind: app.EventProviderRetry, SessionID: "default", RunID: "run-retry-zh", Text: "provider rate_limit error (server_is_overloaded)",
		Data: map[string]string{"attempt": "1", "max": "5", "delay_ms": "500"},
	})
	summary := model.runActivitySummary(model.runActivityAt)
	if !strings.Contains(summary, "正在重连") || !strings.Contains(summary, "500ms 后进行第 1/5 次重试") ||
		!strings.Contains(summary, "rate_limit") {
		t.Fatalf("Chinese retry summary=%q", summary)
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

func TestViewScrollReusesTranscriptLayout(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	model.transcript = []Block{{
		Kind:    BlockAssistant,
		Content: strings.Repeat("scroll performance content ", 200),
		State:   "completed",
	}}

	_ = model.View()
	contentWidth := max(1, bodyTranscriptWidth(model.width, model.paint.bodyHeight)-4)
	before := model.transcriptLines(contentWidth)
	model.scrollTranscript(3)
	_ = model.View()
	after := model.transcriptLines(contentWidth)

	if len(before) == 0 || len(after) == 0 || &before[0] != &after[0] {
		t.Fatal("pure scrolling invalidated the stable transcript layout")
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
	_ = model.View()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		model.scrollTranscript(3)
		_ = model.View()
	}
}

func BenchmarkLongTranscriptHover(b *testing.B) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(AppModel)
	for index := range 120 {
		model.transcript = append(model.transcript, Block{
			Kind:    BlockAssistant,
			Content: fmt.Sprintf("## Finding %d\n\n- first detail\n- second detail\n- third detail", index),
			State:   "completed",
		})
	}
	model.transcript = append(model.transcript,
		Block{Kind: BlockTool, Title: "coding.shell", Arguments: `{"command":"first"}`, State: "completed", Collapsed: true},
		Block{Kind: BlockTool, Title: "coding.shell", Arguments: `{"command":"second"}`, State: "completed", Collapsed: true},
	)
	_ = model.View()
	_, top, width, height := model.transcriptBounds()
	headerRows := make([]int, 0, 2)
	for row := range height {
		index, ok := model.transcriptBlockHeaderAt(row, width, height)
		if ok && index >= len(model.transcript)-2 {
			headerRows = append(headerRows, row)
		}
	}
	if len(headerRows) != 2 {
		b.Fatalf("visible tool headers=%v", headerRows)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		updated, _ = model.Update(tea.MouseMotionMsg{X: 4, Y: top + headerRows[index%2]})
		model = updated.(AppModel)
		_ = model.View()
	}
}

func BenchmarkAgentDetailToolChurn(b *testing.B) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 200, 60
	blocks := make([]Block, 0, 49)
	output := strings.Repeat("tool output line with enough content to render\n", 40)
	for index := range 48 {
		blocks = append(blocks, Block{
			ID: fmt.Sprintf("completed-%d", index), Kind: BlockTool, Title: "coding.shell",
			Content: output, State: "completed", Collapsed: true,
		})
	}
	blocks = append(blocks, Block{ID: "running", Kind: BlockTool, Title: "coding.shell", State: "running"})
	model.detailAgentID = "child-1"
	model.agents = []AgentView{{ID: "child-1", State: "running", Blocks: blocks}}
	model.openOverlay(OverlayAgentDetail)
	_ = model.renderOverlay(model.width, model.height)
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		model.agents[0].Blocks[len(blocks)-1].Content = fmt.Sprintf("progress %d\n%s", index, output)
		delta := 3
		if index%2 == 1 {
			delta = -3
		}
		model.scrollAgentDetail(delta)
		_ = model.renderOverlay(model.width, model.height)
	}
}

func TestSkillSnapshotPopulatesSlashSuggestionsWithoutContextRail(t *testing.T) {
	root := t.TempDir()
	verifyDir := filepath.Join(root, "verify")
	if err := os.MkdirAll(verifyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	verifyPath := filepath.Join(verifyDir, "SKILL.md")
	if err := os.WriteFile(verifyPath, []byte("---\nname: verify\ndescription: Verify the current changes\n---\nVERIFY_SKILL_BODY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &skillCommandRuntime{}
	model := NewModel(runtime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.applyEvent(app.Event{Kind: app.EventSkillCatalog, State: "snapshot", SkillCatalog: []app.SkillCatalogEntry{
		{Name: "verify", Description: "Verify the current changes", SourcePath: verifyPath, ModelVisible: true},
		{Name: "simplify", Eager: true},
		{Name: "disabled", Disabled: true},
	}})
	if model.overlay != OverlayNone || len(model.skills) != 3 {
		t.Fatalf("skill snapshot overlay=%q skills=%d", model.overlay, len(model.skills))
	}
	model.composer.SetValue("/")
	suggestions := model.visibleCommandSuggestions()
	if len(suggestions) < 2 || suggestions[0].Skill != "verify" || suggestions[0].Usage != "/skill:verify" || suggestions[0].Detail != "Verify the current changes" || suggestions[1].Skill != "simplify" {
		t.Fatalf("skill slash suggestions = %+v", suggestions)
	}
	for _, suggestion := range suggestions {
		if suggestion.Skill == "disabled" {
			t.Fatalf("disabled skill rendered as a slash suggestion: %+v", suggestions)
		}
	}
	rail := ansi.Strip(model.renderContextRail(32, 20))
	for _, unwanted := range []string{"SKILLS", "verify", "simplify", "disabled"} {
		if strings.Contains(rail, unwanted) {
			t.Fatalf("skill context rail contains %q:\n%s", unwanted, rail)
		}
	}

	updated, startCmd := model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if startCmd != nil || model.composer.Value() != "/skill:verify " {
		t.Fatalf("skill slash completion cmd=%v composer=%q", startCmd != nil, model.composer.Value())
	}
	model.composer.SetValue("/skill:verify inspect the current changes")
	updated, startCmd = model.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(AppModel)
	if startCmd == nil {
		t.Fatal("sending with a selected skill did not start a turn")
	}
	_ = startCmd()
	for _, wanted := range []string{`The user has invoked the "verify" skill`, "VERIFY_SKILL_BODY", "[Skill directory: " + verifyDir + "]", "User: inspect the current changes"} {
		if !strings.Contains(runtime.request.Prompt, wanted) {
			t.Fatalf("expanded skill prompt missing %q:\n%s", wanted, runtime.request.Prompt)
		}
	}
	if len(runtime.request.ActiveSkills) != 0 {
		t.Fatalf("skill slash activation request = %+v", runtime.request)
	}
	if got := model.transcript[len(model.transcript)-1].Content; got != "/skill:verify inspect the current changes" {
		t.Fatalf("skill transcript = %q", got)
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
		Kind:  app.EventSkillCatalog,
		State: "listed",
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
		"AVAILABLE", "2 resources", "manual-demo", "MANUAL ONLY", "1 more warnings", "Enter invoke",
	} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("skills overlay missing %q:\n%s", wanted, rendered)
		}
	}
	if strings.Contains(rendered, "warning four") {
		t.Fatalf("skills overlay rendered more than three warning details:\n%s", rendered)
	}

	model.overlayCursor = 2
	updated, startCmd := model.updateOverlayKey("enter")
	model = updated.(AppModel)
	if startCmd == nil || model.overlay != OverlayNone || model.focus != focusComposer || !model.composer.Focused() || model.overlayCursor != 0 {
		t.Fatalf("skill overlay enter cmd=%v overlay=%q focus=%d composer focused=%t cursor=%d", startCmd != nil, model.overlay, model.focus, model.composer.Focused(), model.overlayCursor)
	}
	_ = startCmd()
	if runtime.request.Prompt != `Apply the "available-demo" skill to the current workspace and report the result.` ||
		len(runtime.request.ActiveSkills) != 1 || runtime.request.ActiveSkills[0] != "available-demo" {
		t.Fatalf("skill overlay request = %+v", runtime.request)
	}

	disabledRuntime := &skillCommandRuntime{}
	disabledModel := NewModel(disabledRuntime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	disabledModel.skills = []SkillCatalogView{{Name: "disabled-demo", Disabled: true}}
	disabledModel.openOverlay(OverlaySkills)
	updated, startCmd = disabledModel.updateOverlayKey("enter")
	disabledModel = updated.(AppModel)
	if startCmd != nil || disabledModel.overlay != OverlaySkills || disabledModel.errorBanner != "Skill disabled-demo is disabled" || disabledRuntime.request.Prompt != "" {
		t.Fatalf("disabled skill invocation cmd=%v overlay=%q error=%q request=%+v", startCmd != nil, disabledModel.overlay, disabledModel.errorBanner, disabledRuntime.request)
	}

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
	updated, startCmd = model.executeCommand(Command{Name: "skill", Args: []string{"DEMO", "inspect", "parser"}})
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
	chineseRuntime := &skillCommandRuntime{}
	chineseModel := NewModel(chineseRuntime, "/tmp/workspace", "chatgpt", "model", "high", "single")
	if err := chineseModel.SetLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	updated, startCmd = chineseModel.executeCommand(Command{Name: "skill", Args: []string{"DEMO"}})
	chineseModel = updated.(AppModel)
	if startCmd == nil {
		t.Fatal("localized /skill fallback did not start a turn")
	}
	_ = startCmd()
	if chineseRuntime.request.Prompt != fallback || chineseModel.transcript[0].Content != "将“demo”技能应用于当前工作区并报告结果。" {
		t.Fatalf("localized /skill request = %+v transcript=%#v", chineseRuntime.request, chineseModel.transcript)
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
	emptyModel.applyEvent(app.Event{Kind: app.EventSkillCatalog, State: "listed"})
	if content := ansi.Strip(emptyModel.renderOverlay(80, 20)); !strings.Contains(content, "No skills are available") {
		t.Fatalf("skills empty state missing:\n%s", content)
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
	assertTranscriptStatusOnly(t, model.renderTranscriptFooter(48, 0, 0), "正在审查")
	model.applyEvent(app.Event{
		Kind: app.EventApprovalResolved, RunID: "run-zh", ToolCallID: "edit-zh", ApprovalID: "approval-zh", State: "auto_approved",
		Data: map[string]string{"tool": "coding.edit_hashline", "target": "README.md", "risk": "low", "rationale": "bounded edit"},
	})
	if len(model.transcript) != 0 {
		t.Fatalf("localized approval appeared in chat: %#v", model.transcript)
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
	if model.catalog.Language() != "zh-CN" || model.composer.Placeholder != "构建任何内容" {
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

func TestTodoPaneFilteringStaleEventsAndBoundedRendering(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.todo = session.TodoList{Goal: "Ship", Revision: 3, Phases: []session.TodoPhase{{
		ID: "phase-1", Title: "Build", Items: []session.TodoItem{
			{ID: "done", Content: "Analyze", Status: session.TodoCompleted},
			{ID: "current", Content: "Implement", Status: session.TodoInProgress},
			{ID: "next", Content: "Verify", Status: session.TodoPending},
		},
	}}}
	model.todoExpanded = true
	model.focus = focusTodo
	model.width, model.height = 80, 24
	if model.visibleTodoItemCount() != 3 {
		t.Fatalf("todo item count=%d, want 3", model.visibleTodoItemCount())
	}
	updated, _ := model.updateTodoKey("h")
	model = updated.(AppModel)
	if !model.todoHideCompleted || model.visibleTodoItemCount() != 2 {
		t.Fatalf("hidden todo count=%d hide=%v", model.visibleTodoItemCount(), model.todoHideCompleted)
	}
	filtered := ansi.Strip(model.renderTodoPane(80, 3))
	if strings.Contains(filtered, "Analyze") || !strings.Contains(filtered, "Implement") || !strings.Contains(filtered, "Verify") {
		t.Fatalf("todo pane filtering is wrong:\n%s", filtered)
	}

	model.applyEvent(app.Event{Kind: app.EventTodoUpdated, Todo: &session.TodoList{Revision: 2}})
	if model.todo.Revision != 3 {
		t.Fatalf("stale todo event regressed revision to %d", model.todo.Revision)
	}
	model.todoHideCompleted = false
	for _, size := range [][2]int{{20, 8}, {40, 12}, {80, 24}, {120, 40}} {
		model.width, model.height = size[0], size[1]
		rendered := model.View().Content
		lines := strings.Split(rendered, "\n")
		if len(lines) != size[1] {
			t.Fatalf("todo view height=%d, want %d at %v", len(lines), size[1], size)
		}
		for lineNumber, line := range lines {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Fatalf("todo view line %d width=%d, want <=%d", lineNumber+1, width, size[0])
			}
		}
	}
	if rail := model.renderContextRail(31, 16); !strings.Contains(rail, ";9m") {
		t.Fatalf("completed rail todo is not rendered with strikethrough: %q", rail)
	}
}

func TestInlineTodoPaneScrollsAndCloseControlCollapses(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	items := make([]session.TodoItem, 12)
	for index := range items {
		items[index] = session.TodoItem{
			ID:      fmt.Sprintf("todo-%02d", index),
			Content: fmt.Sprintf("Task %02d", index),
			Status:  session.TodoPending,
		}
	}
	model.todo = session.TodoList{Phases: []session.TodoPhase{{Items: items}}}
	model.width, model.height = 100, 30
	model.todoExpanded = true
	model.focus = focusTodo
	model.composer.Blur()

	_, top, width, height := model.todoPaneBounds()
	// 30-row terminal × 30% cap → room for more of a long todo list.
	if height < 8 {
		t.Fatalf("todo pane height=%d, want at least 8", height)
	}
	initial := ansi.Strip(model.renderTodoPane(width, height))
	if !strings.Contains(initial, "Task 00") || !strings.ContainsAny(initial, "▁▂▃▄▅▆▇█") {
		t.Fatalf("scrollable todo pane omitted first item or thumb:\n%s", initial)
	}

	updated, _ := model.Update(tea.MouseWheelMsg{X: 10, Y: top + 1, Button: tea.MouseWheelDown})
	model = updated.(AppModel)
	if model.todoScroll != 1 {
		t.Fatalf("todo wheel offset=%d, want 1", model.todoScroll)
	}
	updated, _ = model.updateTodoKey("end")
	model = updated.(AppModel)
	if model.todoScroll != model.todoPaneScrollLimit(height) {
		t.Fatalf("todo end offset=%d, want %d", model.todoScroll, model.todoPaneScrollLimit(height))
	}

	updated, _ = model.Update(tea.MouseClickMsg{X: width - 1, Y: top, Button: tea.MouseLeft})
	model = updated.(AppModel)
	if model.todoExpanded || model.focus != focusComposer || !model.composer.Focused() {
		t.Fatalf("todo close = expanded:%t focus:%d composer:%t", model.todoExpanded, model.focus, model.composer.Focused())
	}
}

func TestTodoPaneHeightTracksContentUntilMaximum(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 100, 100
	model.todoExpanded = true

	model.todo = session.TodoList{Phases: []session.TodoPhase{{Items: []session.TodoItem{
		{Content: "First", Status: session.TodoPending},
		{Content: "Second", Status: session.TodoPending},
	}}}}
	_, _, _, height := model.todoPaneBounds()
	if height != 2 {
		t.Fatalf("short todo pane height=%d, want content height 2", height)
	}

	items := make([]session.TodoItem, maxTodoPaneHeight+5)
	for index := range items {
		items[index] = session.TodoItem{Content: fmt.Sprintf("Task %02d", index), Status: session.TodoPending}
	}
	model.todo = session.TodoList{Phases: []session.TodoPhase{{Items: items}}}
	_, _, width, height := model.todoPaneBounds()
	if height != maxTodoPaneHeight {
		t.Fatalf("long todo pane height=%d, want maximum %d", height, maxTodoPaneHeight)
	}
	if limit := model.todoPaneScrollLimit(height); limit <= 0 {
		t.Fatalf("long todo scroll limit=%d, want positive", limit)
	}
	if pane := ansi.Strip(model.renderTodoPane(width, height)); !strings.ContainsAny(pane, "▁▂▃▄▅▆▇█") {
		t.Fatalf("long todo pane omitted scrollbar:\n%s", pane)
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

func TestStickyInstructionPinsScrolledPastUserPrompt(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 100, 30
	model.transcript = []Block{
		{Kind: BlockUser, RunID: "run-1", Content: "optimize the sticky prompt dialog"},
		{Kind: BlockAssistant, RunID: "run-1", Content: strings.Repeat("tool output line\n", 40)},
	}
	model.status = "Running"
	model.runID = "run-1"
	// Follow the bottom so the user prompt has left the viewport.
	model.transcriptTop = model.transcriptMaxOffset()

	content, ok := model.stickyInstructionContent(76, 12)
	if !ok || !strings.Contains(content, "optimize the sticky prompt dialog") {
		t.Fatalf("sticky content = %q ok=%v, want current instruction", content, ok)
	}
	card := ansi.Strip(model.renderStickyInstruction(80, 12))
	if !strings.Contains(card, "╭") || !strings.Contains(card, "›") || !strings.Contains(card, "optimize the sticky") {
		t.Fatalf("sticky dialog card is wrong:\n%s", card)
	}

	// When the prompt is still on screen, do not pin a duplicate.
	model.transcriptTop = 0
	model.status = "Ready"
	model.runID = ""
	if content, ok = model.stickyInstructionContent(76, 40); ok {
		t.Fatalf("sticky should hide while prompt is visible, got %q", content)
	}
}

func TestTranscriptScrollDoesNotReverseAtStickyBoundary(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.width, model.height = 100, 30
	model.transcriptLayout = &transcriptLayoutCache{
		initialized: true,
		lines:       make([]string, 100),
		userMarks:   []stickyUserMark{{content: "keep this prompt pinned", endLine: 80}},
	}
	model.paint.width = model.width
	model.paint.height = model.height
	model.paint.preBodyHeight = 20
	model.paint.bodyHeight = 16
	model.paint.lineCount = 100
	model.transcriptTop = 5

	if !model.stickyVisibleWithCache(model.paint.preBodyHeight) {
		t.Fatal("test setup is below the sticky dismissal boundary")
	}
	model.scrollTranscript(3)
	if model.transcriptTop != 8 {
		t.Fatalf("wheel up reversed at sticky boundary: offset=%d, want 8", model.transcriptTop)
	}
	if model.stickyVisibleWithCache(model.paint.preBodyHeight) {
		t.Fatal("sticky prompt remained visible after crossing its dismissal boundary")
	}
}

func TestTranscriptScrollDownStillReturnsToLatest(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(AppModel)
	model.transcript = []Block{{Kind: BlockAssistant, Content: strings.Repeat("history ", 400), State: "completed"}}
	_ = model.View()
	model.scrollTranscript(12)
	if model.transcriptTop == 0 {
		t.Fatal("test transcript did not scroll into history")
	}
	model.scrollTranscript(-1000)
	if model.transcriptTop != 0 {
		t.Fatalf("scroll down offset = %d, want latest", model.transcriptTop)
	}
}

func TestInterruptedEmptyTurnScrollIsMonotonicAcrossStickyBoundary(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(AppModel)
	model.transcript = []Block{
		{Kind: BlockUser, RunID: "run-previous", Content: "previous interrupted prompt"},
		{Kind: BlockUser, RunID: "run-current", Content: "continue"},
		{Kind: BlockAssistant, RunID: "run-current", Content: strings.Repeat("completed answer line ", 400), State: "completed"},
	}
	model.status = "Ready"
	model.runID = ""
	_ = model.View()

	previous := model.transcriptTop
	stickySeen := false
	for range 40 {
		model.scrollTranscript(3)
		if model.transcriptTop < previous {
			t.Fatalf("wheel up reversed after interrupted empty turn: offset=%d, previous=%d", model.transcriptTop, previous)
		}
		_ = model.View()
		stickySeen = stickySeen || model.paint.stickyHeight > 0
		previous = model.transcriptTop
		if previous == model.transcriptMaxOffsetForTop(previous) {
			break
		}
	}
	if !stickySeen {
		t.Fatal("test did not show a sticky prompt")
	}
}

func TestHeaderShowsBranchAndCollapsesHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "GolandProjects", "azem")
	model := NewModel(inertRuntime{}, workspace, "chatgpt", "model", "high", "single")
	model.branch = "main"
	header := ansi.Strip(model.renderHeader(120))
	if !strings.Contains(header, "⎇ main") {
		t.Fatalf("header missing branch: %q", header)
	}
	if !strings.Contains(header, "~/GolandProjects/azem") {
		t.Fatalf("header did not collapse home path: %q", header)
	}
	if strings.Contains(header, home) || strings.Contains(header, "default") {
		t.Fatalf("header leaked absolute home path or session id: %q", header)
	}
	if got := collapseHomePath(home); got != "~" {
		t.Fatalf("collapseHomePath(home)=%q, want ~", got)
	}
	if got := collapseHomePath(filepath.Join(home, "src")); got != "~/src" {
		t.Fatalf("collapseHomePath nested=%q, want ~/src", got)
	}
	if got := collapseHomePath("/tmp/workspace"); got != "/tmp/workspace" {
		t.Fatalf("collapseHomePath non-home=%q", got)
	}
}

func TestTodoPaneShowsPhaseBoundariesAndCompletedGroupRails(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	model.todo = session.TodoList{Phases: []session.TodoPhase{
		{Title: "P1: 基础", Items: []session.TodoItem{
			{Content: "done-a", Status: session.TodoCompleted},
			{Content: "done-b", Status: session.TodoCompleted},
			{Content: "active", Status: session.TodoInProgress},
		}},
		{Title: "P2: 架构演进", Items: []session.TodoItem{
			{Content: "next", Status: session.TodoPending},
		}},
	}}
	model.todoExpanded = true
	model.focus = focusTodo
	pane := ansi.Strip(model.renderTodoPane(80, 10))
	for _, wanted := range []string{"▌ P1: 基础", "▌ P2: 架构演进", "┌ ✓", "└ ✓", "▶ active", "□ next"} {
		if !strings.Contains(pane, wanted) {
			t.Fatalf("todo pane missing phase boundary marker %q:\n%s", wanted, pane)
		}
	}
	// A blank gap separates the two phases so the list does not read as one block.
	lines := strings.Split(pane, "\n")
	p1, p2 := -1, -1
	for index, line := range lines {
		if strings.Contains(line, "P1: 基础") {
			p1 = index
		}
		if strings.Contains(line, "P2: 架构演进") {
			p2 = index
		}
	}
	if p1 < 0 || p2 <= p1+1 {
		t.Fatalf("phases are not separated:\n%s", pane)
	}
	foundGap := false
	for _, line := range lines[p1+1 : p2] {
		if strings.TrimSpace(ansi.Strip(line)) == "" || strings.Trim(strings.TrimSpace(line), "│┌┐└┘ ×┃") == "" {
			foundGap = true
			break
		}
	}
	if !foundGap {
		t.Fatalf("expected blank gap between phases:\n%s", pane)
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

	model.todoExpanded = true
	model.focus = focusTodo
	pane := ansi.Strip(model.renderTodoPane(80, 5))
	for _, wanted := range []string{"▌ Work", "▶ Main task", "▶ Delegated task", "□ Next task"} {
		if !strings.Contains(pane, wanted) {
			t.Fatalf("inline todo pane omitted %q:\n%s", wanted, pane)
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

func TestApprovalRendersAsAuditStatusInsteadOfToolBlock(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	block := Block{
		Kind: BlockApproval, Title: "Allowed", State: "completed",
		Content: "Edit File · README.md\nRisk: low\nRationale: bounded edit",
	}
	rendered := ansi.Strip(strings.Join(model.renderBlock(block, 0, 80), "\n"))
	for _, wanted := range []string{"APPROVAL", "Allowed", "Edit File · README.md", "Risk: low", "Rationale: bounded edit"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("approval audit status omitted %q:\n%s", wanted, rendered)
		}
	}
	if strings.Contains(rendered, "│") || strings.Contains(rendered, "COMPLETED") {
		t.Fatalf("approval still renders like a tool block:\n%s", rendered)
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
	flat := strings.ReplaceAll(strings.Join(strings.Fields(plain), " "), "- ", "-")
	for _, wanted := range []string{"APPROVAL", "Risk:", "low", "Reason:", "dry-run preview only"} {
		if !strings.Contains(strings.ToUpper(flat), strings.ToUpper(wanted)) {
			t.Fatalf("approval body missing %q:\n%s", wanted, plain)
		}
	}
	if strings.Count(rendered, "\x1b[") < 5 {
		t.Fatalf("approval body is still rendered as one flat color:\n%s", plain)
	}
}

func TestTranscriptKindsUseQuietHierarchyAndSemanticStyles(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	styles := map[string]lipgloss.Style{
		"assistant": model.theme.AssistantTag,
		"thinking":  model.theme.ThinkingTag,
		"tool":      model.theme.ToolTag,
		"agent":     model.theme.AgentTag,
		"approval":  model.theme.ApprovalTag,
		"error":     model.theme.ErrorTag,
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
	if background := fmt.Sprint(model.theme.UserSurface.GetBackground()); background == emptyBackground {
		t.Fatal("user prompt band must be elevated from the transcript")
	}

	cases := []struct {
		block Block
		label string
	}{
		{Block{Kind: BlockAssistant, Content: "## Result\n\nDone.", State: "completed"}, "Result"},
		{Block{Kind: BlockThinking, Content: "Checking constraints", State: "streaming"}, model.tr("block.thinking")},
		{Block{Kind: BlockTool, Title: "coding.read_file", Content: "result", State: "completed"}, model.tr("tool.read_file")},
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

func TestThemeSurfacesDoNotOverrideTerminalBackground(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	emptyBackground := fmt.Sprint(lipgloss.NewStyle().GetBackground())
	surfaces := map[string]lipgloss.Style{
		"header brand": model.theme.HeaderBrand, "header mode": model.theme.HeaderMode,
		"chrome": model.theme.Chrome, "runtime": model.theme.RuntimeStrip,
		"context": model.theme.ContextStrip, "help": model.theme.HelpStrip,
		"composer focused": model.theme.PanelFocused, "composer blurred": model.theme.PanelBlurred,
		"overlay title": model.theme.OverlayTitle, "overlay footer": model.theme.OverlayFooter,
		"selected": model.theme.Selected, "diff add": model.theme.DiffAdd,
		"diff delete": model.theme.DiffDel, "diff hunk": model.theme.DiffHunk,
		"chip": model.theme.Chip, "chip ask": model.theme.ChipAsk,
		"chip smart": model.theme.ChipSmart, "chip danger": model.theme.ChipDanger,
	}
	for name, style := range surfaces {
		if background := fmt.Sprint(style.GetBackground()); background != emptyBackground {
			t.Fatalf("%s background = %s, want unset", name, background)
		}
	}
}

func TestRenderSurfaceClearsConfiguredBackground(t *testing.T) {
	surface := lipgloss.NewStyle().Background(lipgloss.Color("#101820"))
	child := lipgloss.NewStyle().Foreground(lipgloss.Color("#67d4ee")).Render("Azem")
	rendered := renderSurface(surface, child+" gap")
	if strings.Contains(rendered, "48;") || strings.Contains(rendered, "\x1b[4") {
		t.Fatalf("surface emitted a background ANSI sequence: %q", rendered)
	}
	if ansi.Strip(rendered) != "Azem gap" {
		t.Fatalf("surface content = %q, want %q", ansi.Strip(rendered), "Azem gap")
	}
}

func TestUserMessageUsesElevatedPromptBand(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	rendered := strings.Join(model.renderUserMessage("Keep the body readable", 48), "\n")
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "› Keep the body readable") || !strings.Contains(plain, "╭") || !strings.Contains(plain, "╰") {
		t.Fatalf("user message dialog = %q", plain)
	}
	if !strings.Contains(rendered, ";48;") {
		t.Fatalf("user message lacks elevated surface: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := ansi.StringWidth(line); width > 48 {
			t.Fatalf("user message line width = %d: %q", width, ansi.Strip(line))
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

func TestGrokStyleChromeDistributesMetadata(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4.5", "high", "single")
	model.approvalMode = ApprovalModeAutoReview
	model.autoReviewAvailable = true
	model.usage.ContextLimit = 500_000
	model.usage.InputTokens = 153_000
	model.status = "Ready"

	for _, size := range []struct{ height, width int }{{24, 100}, {10, 80}, {5, 40}} {
		if got := dockFooterLines(size.height, size.width); got != 1 {
			t.Fatalf("%dx%d footer lines = %d, want 1", size.width, size.height, got)
		}
	}

	footer := ansi.Strip(model.renderDockFooter(100, 1))
	for _, wanted := range []string{"Drag:copy", "Shift+Tab:approval", "Ctrl+P:commands", "?:shortcuts"} {
		if !strings.Contains(footer, wanted) {
			t.Fatalf("shortcut bar missing %q: %q", wanted, footer)
		}
	}
	header := ansi.Strip(model.renderHeader(100))
	if !strings.Contains(header, "⌁") || !strings.Contains(header, "/tmp/workspace") || !strings.Contains(header, "153K / 500K") || strings.Contains(header, "grok-4.5") {
		t.Fatalf("top chrome hierarchy is wrong: %q", header)
	}
	if strings.Contains(header, "⌁ default") {
		t.Fatalf("top chrome still shows session id: %q", header)
	}
	composer := ansi.Strip(model.renderComposer())
	if !strings.Contains(composer, "grok-4.5 (high) · SMART") {
		t.Fatalf("composer caption is missing model/mode: %q", composer)
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = updated.(AppModel)
	content := ansi.Strip(model.View().Content)
	for _, wanted := range []string{"153K / 500K", "grok-4.5 (high)", "Drag:copy", "SMART"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("spacious view missing %q:\n%s", wanted, content)
		}
	}
	if strings.Contains(content, "RUN CONTEXT") {
		t.Fatalf("spacious view still contains the detached context rail:\n%s", content)
	}
}

func TestMeasureViewLayoutReservesSingleShortcutRow(t *testing.T) {
	layout := measureViewLayout(24, 100, 3, 0, 0, 0)
	if !layout.showChrome || layout.footerHeight != 1 {
		t.Fatalf("spacious layout = %+v", layout)
	}
	// header(1) + air(1) + footer(1) + composer >=1 leave body room.
	if layout.bodyHeight < 10 || layout.composerHeight < 1 {
		t.Fatalf("body/composer heights collapsed: %+v", layout)
	}
	if composerOffsetY(layout) != 2+layout.bodyHeight {
		t.Fatalf("composer offset = %d, want %d", composerOffsetY(layout), 2+layout.bodyHeight)
	}

	medium := measureViewLayout(8, 40, 1, 0, 0, 0)
	if medium.footerHeight != 1 || !medium.showChrome {
		t.Fatalf("medium layout = %+v", medium)
	}
	tiny := measureViewLayout(5, 40, 1, 0, 0, 0)
	if tiny.footerHeight != 1 || tiny.showChrome {
		t.Fatalf("tiny layout = %+v", tiny)
	}
}

func TestChineseGeneratedUIPaths(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "grok", "grok-4", "high", "single", "session-zh")
	model.SetLanguage("zh-CN")
	model.status = "Running"
	assertTranscriptTimedStatus(t, model.renderTranscriptFooter(120, 0, 0), "运行中")
	model.overlay = OverlayHelp
	heading, subtitle := model.overlayHeading()
	if heading != "键盘帮助" || subtitle != "所有操作均可通过键盘完成" || !strings.Contains(model.overlayFooter(), "关闭") {
		t.Fatalf("localized overlay = %q / %q / %q", heading, subtitle, model.overlayFooter())
	}
	catalog := i18n.Must("zh-CN")
	if got := summarizeToolArguments("coding.read_file", `{"path":"internal/main.go","endLine":20}`, catalog); got != "读取 internal/main.go · 第 1-20 行" {
		t.Fatalf("localized tool summary = %q", got)
	}
	if err := model.attachImagePath(""); err == nil || !strings.Contains(err.Error(), "图片路径为空") {
		t.Fatalf("localized attachment error = %v", err)
	}
	model.status = "Ready"
	updated, _ := model.executeCommand(Command{Name: "team"})
	model = updated.(AppModel)
	if model.errorBanner != "用法：/team on|off" {
		t.Fatalf("localized command usage = %q", model.errorBanner)
	}
	for state, wanted := range map[string]string{
		"Application stopped": "应用已停止", "Choose cancellation scope": "选择取消范围",
		"Shutting down": "正在退出", "Cancelling action": "正在取消操作", "Reconciled": "已核对",
	} {
		if got := model.displayState(state); got != wanted {
			t.Fatalf("localized state %q = %q, want %q", state, got, wanted)
		}
	}
	model.composer.SetValue("/")
	updated, _ = model.submit()
	model = updated.(AppModel)
	if model.errorBanner != "命令为空" {
		t.Fatalf("localized empty command = %q", model.errorBanner)
	}
	model.openOverlay(OverlayLanguage)
	if rendered := ansi.Strip(model.renderOverlay(80, 24)); !strings.Contains(rendered, "已选择") || strings.Contains(rendered, "SELECTED") {
		t.Fatalf("localized option state = %q", rendered)
	}
}
