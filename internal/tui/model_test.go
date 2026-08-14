package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/i18n"
	"github.com/Viking602/azem/internal/session"
)

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
		OverlayReasoning, OverlaySessions, OverlayBranches, OverlayBranchConfirm, OverlayApproval, OverlayUserInput, OverlayPlan, OverlayCancel, OverlayDiff, OverlayAgents,
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
