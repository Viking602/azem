package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
)

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
	editArguments := `{"dryRun":true,"input":"*** Begin Patch\n[README.md#720F]\nPUT 1.=2:\n+` + strings.Repeat("long content ", 200) + `\n*** End Patch\n"}`
	if got := summarizeToolArguments("coding.edit_hashline", editArguments); got != "Preview README.md" {
		t.Fatalf("edit arguments exposed raw patch: %q", got)
	}
	if got := summarizeToolArguments("coding.write_file", `{"path":"new.go","content":"package main\n\n"}`); got != "Create new.go · 2 lines" {
		t.Fatalf("write arguments exposed file content: %q", got)
	}
}

func TestFailedEditReplacesRawPatchWithTargetAndError(t *testing.T) {
	model := NewModel(inertRuntime{}, "/tmp/workspace", "chatgpt", "model", "high", "single")
	arguments := `{"dryRun":true,"input":"*** Begin Patch\n[README.md#720F]\nPUT 1.=1:\n+` + strings.Repeat("README body ", 200) + `\n*** End Patch\n"}`
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
	arguments := `{"input":"*** Begin Patch\n[internal/app.go#ABCD]\nPUT 1.=1:\n+` + strings.Repeat("source ", 200) + `\n*** End Patch\n"}`
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
