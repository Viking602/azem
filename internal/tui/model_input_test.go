package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

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
