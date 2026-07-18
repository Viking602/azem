package tui

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/i18n"
	"github.com/Viking602/azem/internal/session"
)

var commandPaletteOptions = []string{
	"login", "provider", "models", "skills", "reasoning", "sessions", "new", "agents", "mcp", "cancel", "help", "quit",
}

type overlayOption struct {
	Group  string
	Label  string
	Detail string
	State  string
}

type overlayRenderRow struct {
	Group       string
	OptionIndex int
}

func buildOverlayRenderRows(options []overlayOption, cursor int) ([]overlayRenderRow, int) {
	rows := make([]overlayRenderRow, 0, len(options)+2)
	selectedRow := 0
	previousGroup := ""
	for index, option := range options {
		if option.Group != "" && option.Group != previousGroup {
			rows = append(rows, overlayRenderRow{Group: option.Group, OptionIndex: -1})
			previousGroup = option.Group
		}
		if index == cursor {
			selectedRow = len(rows)
		}
		rows = append(rows, overlayRenderRow{Group: option.Group, OptionIndex: index})
	}
	return rows, selectedRow
}

func providerDisplayName(provider string) string {
	switch strings.ToLower(provider) {
	case "chatgpt":
		return "ChatGPT"
	case "grok":
		return "Grok"
	default:
		return provider
	}
}

func (m AppModel) View() tea.View {
	width := max(1, m.width)
	height := max(1, m.height)
	if m.overlay != OverlayNone {
		view := tea.NewView(fitViewport(m.renderOverlay(width, height), width, height))
		view.AltScreen = true
		view.ReportFocus = true
		view.MouseMode = tea.MouseModeCellMotion
		view.WindowTitle = "Azem"
		return view
	}
	header := m.renderHeader(width)
	composer := m.composer.View()
	suggestions := m.visibleCommandSuggestions()
	layout := measureViewLayout(height, strings.Count(composer, "\n")+1, len(suggestions))
	sections := make([]string, 0, 8)
	if layout.showChrome {
		sections = append(sections, header, m.theme.Border.Render(strings.Repeat("─", width)))
	}
	if layout.bodyHeight > 0 {
		sections = append(sections, m.renderBody(width, layout.bodyHeight))
	}
	if layout.showChrome {
		sections = append(sections, m.theme.Border.Render(strings.Repeat("─", width)))
	}
	if layout.suggestionHeight > 0 {
		sections = append(sections, m.renderCommandSuggestions(width, layout.suggestionHeight, suggestions))
	}
	sections = append(sections, fitViewport(composer, width, layout.composerHeight))
	if layout.showModelStatus {
		sections = append(sections, m.renderModelStatus(width))
	}
	if layout.showStatus {
		sections = append(sections, m.renderStatus(width))
	}
	view := tea.NewView(fitViewport(strings.Join(sections, "\n"), width, height))
	if cursor := m.composer.Cursor(); cursor != nil {
		cursor.Position.Y += composerOffsetY(layout)
		view.Cursor = cursor
	}
	view.AltScreen = true
	view.ReportFocus = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "Azem"
	return view
}

func composerOffsetY(layout viewLayout) int {
	offset := layout.bodyHeight + layout.suggestionHeight
	if layout.showChrome {
		offset += 3 // header and the separators above and below the body
	}
	return offset
}

type viewLayout struct {
	bodyHeight       int
	composerHeight   int
	suggestionHeight int
	showChrome       bool
	showModelStatus  bool
	showStatus       bool
}

func measureViewLayout(height, composerHeight, suggestionCount int) viewLayout {
	height = max(1, height)
	layout := viewLayout{
		showChrome:      height >= 6,
		showModelStatus: height >= 10,
		showStatus:      height >= 2,
	}
	fixedHeight := 0
	if layout.showChrome {
		fixedHeight += 3 // header and two separators
	}
	if layout.showModelStatus {
		fixedHeight++
	}
	if layout.showStatus {
		fixedHeight++
	}
	available := max(1, height-fixedHeight)
	layout.composerHeight = 1
	if available > 1 {
		layout.composerHeight = min(max(1, composerHeight), available-1)
		layout.bodyHeight = available - layout.composerHeight
	}
	if suggestionCount > 0 && layout.bodyHeight > 1 {
		desired := min(5, suggestionCount) + 1
		layout.suggestionHeight = min(desired, layout.bodyHeight-1)
		layout.bodyHeight -= layout.suggestionHeight
	}
	return layout
}

func (m AppModel) renderHeader(width int) string {
	left := m.theme.Header.Render(" Azem")
	if width >= 60 {
		left += m.theme.Muted.Render("  " + shortenPath(m.workspace, max(16, width/2)))
	}
	right := m.theme.Muted.Render(strings.ToUpper(m.agentMode) + " ")
	return joinSides(left, right, width)
}

func (m AppModel) renderBody(width int, height int) string {
	if width < 104 || height < 16 {
		return m.renderTranscript(width, height)
	}
	railWidth := min(31, width/3)
	transcriptWidth := width - railWidth - 1
	transcript := m.renderTranscript(transcriptWidth, height)
	divider := m.theme.Border.Render(strings.Repeat("│\n", height-1) + "│")
	rail := m.renderContextRail(railWidth, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, transcript, divider, rail)
}

func (m AppModel) renderTranscript(width int, height int) string {
	contentWidth := max(1, width-4)
	lines := m.transcriptLines(contentWidth)
	maxOffset := m.transcriptOffsetLimit(len(lines), height)
	offset := min(maxOffset, max(0, m.transcriptTop))
	contentHeight := height
	showFooter := m.isRunning() || maxOffset > 0
	footerGap := 0
	if showFooter {
		if m.isRunning() && height >= 3 {
			footerGap = 1
		}
		contentHeight = max(1, height-1-footerGap)
	}
	end := len(lines) - offset
	start := max(0, end-contentHeight)
	visible := append([]string(nil), lines[start:end]...)
	for len(visible) < contentHeight {
		visible = append(visible, "")
	}
	for index, line := range visible {
		visible[index] = padStyledLine(line, width)
	}
	if showFooter {
		if footerGap > 0 {
			visible = append(visible, padStyledLine("", width))
		}
		visible = append(visible, m.renderTranscriptFooter(width, maxOffset, offset))
	}
	for len(visible) < height {
		visible = append(visible, "")
	}
	return strings.Join(visible[:height], "\n")
}

func (m AppModel) transcriptLines(contentWidth int) []string {
	cache := m.transcriptLayout
	if cache == nil {
		cache = &transcriptLayoutCache{}
	}
	if cache.contentWidth != contentWidth {
		cache.contentWidth = contentWidth
		cache.initialized = false
		cache.blocks = nil
		cache.lines = nil
	}

	dirty := !cache.initialized || len(cache.blocks) != len(m.transcript)
	if len(cache.blocks) != len(m.transcript) {
		blocks := make([]transcriptBlockLayout, len(m.transcript))
		copy(blocks, cache.blocks)
		cache.blocks = blocks
	}
	lineCount := 0
	for index, block := range m.transcript {
		selected := m.focus == focusTranscript && m.transcriptCursor == index
		layout := &cache.blocks[index]
		if !cache.initialized || !reflect.DeepEqual(layout.block, block) || layout.selected != selected {
			layout.block = block
			layout.selected = selected
			layout.lines = m.renderBlock(block, index, contentWidth)
			dirty = true
		}
		lineCount += len(layout.lines)
	}
	if !dirty {
		return cache.lines
	}

	lines := make([]string, 0, lineCount+len(cache.blocks))
	if len(cache.blocks) == 0 {
		lines = append(lines,
			m.theme.Muted.Render("  "+m.tr("empty.title")),
			m.theme.Assistant.Render("  "+m.tr("empty.body")),
			m.theme.Muted.Render("  "+m.tr("empty.help")),
		)
	}
	for _, block := range cache.blocks {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, block.lines...)
	}
	cache.lines = lines
	cache.initialized = true
	return cache.lines
}

func (m AppModel) transcriptOffsetLimit(lineCount int, height int) int {
	contentHeight := max(1, height)
	if m.isRunning() || lineCount > contentHeight {
		footerRows := 1
		if m.isRunning() && height >= 3 {
			footerRows++
		}
		contentHeight = max(1, contentHeight-footerRows)
	}
	return max(0, lineCount-contentHeight)
}

func (m AppModel) renderTranscriptFooter(width int, maxOffset int, offset int) string {
	if m.isRunning() {
		label := strings.ToUpper(m.displayState(m.status))
		detail := "Azem is generating"
		switch m.status {
		case "Starting":
			detail = "Starting provider"
		case "Awaiting approval":
			detail = "Waiting for approval"
		case "Reviewing approval":
			detail = "Checking this action"
		case "Cancelling":
			detail = "Stopping run"
		}
		indicator := "◆"
		if !m.reducedMotion {
			frames := [...]string{"◇", "◈", "◆", "◈"}
			indicator = frames[m.animationFrame%len(frames)]
		}
		text := "  " + indicator + " " + label + "  " + detail + "  · Ctrl+C cancel"
		if offset > 0 {
			text += fmt.Sprintf("  · ↑ %d lines · Ctrl+End latest", offset)
		}
		return m.theme.Selected.Render(padOrTrim(text, width))
	}
	if offset > 0 {
		return m.theme.Muted.Render(padOrTrim(
			fmt.Sprintf("  ↑ HISTORY · %d lines from latest · Wheel/PgDn · Ctrl+End latest", offset),
			width,
		))
	}
	return m.theme.Muted.Render(padOrTrim(
		fmt.Sprintf("  ↕ HISTORY · %d lines · Wheel/PgUp · Ctrl+Home oldest", maxOffset),
		width,
	))
}

func (m AppModel) transcriptViewportSize() (int, int) {
	width := max(1, m.width)
	height := max(1, m.height)
	composer := m.composer.View()
	suggestions := m.visibleCommandSuggestions()
	layout := measureViewLayout(height, strings.Count(composer, "\n")+1, len(suggestions))
	bodyHeight := layout.bodyHeight
	if width >= 104 && bodyHeight >= 16 {
		width -= min(31, width/3) + 1
	}
	return width, bodyHeight
}

func (m AppModel) transcriptMaxOffset() int {
	width, height := m.transcriptViewportSize()
	lineCount := len(m.transcriptLines(max(10, width-4)))
	return m.transcriptOffsetLimit(lineCount, height)
}

func (m *AppModel) scrollTranscript(delta int) {
	m.transcriptTop = min(m.transcriptMaxOffset(), max(0, m.transcriptTop+delta))
}

func (m AppModel) renderBlock(block Block, index int, width int) []string {
	title := first(block.Title, string(block.Kind))
	if block.Kind == BlockTool {
		title = m.toolDisplayName(title)
	}
	state := strings.ToUpper(m.displayState(block.State))
	selected := m.focus == focusTranscript && m.transcriptCursor == index
	selector := " "
	if selected {
		selector = "›"
	}

	switch block.Kind {
	case BlockApproval:
		indicator := stateMark(block.State)
		if block.State == "running" {
			indicator = "◆"
			if !m.reducedMotion {
				frames := [...]string{"◇", "◈", "◆", "◈"}
				indicator = frames[m.animationFrame%len(frames)]
			}
		}
		header := fmt.Sprintf("  %s %s %s · %s", selector, indicator, m.tr("block.approval"), title)
		if state != "" {
			header += "  " + state
		}
		header = padOrTrim(header, width+2)
		style := m.stateStyle(block.State)
		if selected {
			header = m.theme.Selected.Render(header)
		} else {
			header = style.Bold(true).Render(header)
		}
		lines := []string{header}
		if block.Collapsed {
			return lines
		}
		for _, line := range wrapText(block.Content, max(4, width-4)) {
			lines = append(lines, m.theme.Muted.Render("      │ "+line))
		}
		return lines
	case BlockHook:
		return m.renderHookPrompt(block, selected, width)
	case BlockTool, BlockAgent, BlockDiff, BlockError:
		toggle := "▾"
		if block.Collapsed {
			toggle = "▸"
		}
		kind := m.tr("block." + string(block.Kind))
		header := fmt.Sprintf("  %s %s %s · %s", selector, toggle, kind, title)
		if state != "" {
			header += "  " + state
		}
		header = padOrTrim(header, width+2)
		style := m.stateStyle(block.State)
		if block.Kind == BlockDiff {
			style = m.theme.Diff
		} else if block.Kind == BlockError {
			style = m.theme.Error
		}
		if selected {
			header = m.theme.Selected.Render(header)
		} else {
			header = style.Render(header)
		}
		lines := []string{header}
		if block.Collapsed {
			return lines
		}
		if block.Kind == BlockDiff {
			lines = append(lines, m.renderDiffContent(block.Content, max(4, width-4))...)
			return lines
		}
		for _, line := range wrapText(block.Content, max(4, width-4)) {
			lines = append(lines, m.theme.Assistant.Render("      │ "+line))
		}
		return lines
	case BlockUser:
		return m.renderUserMessage(block.Content, width)
	case BlockThinking:
		label := m.tr("block.thinking")
		if state != "" {
			label += " · " + state
		}
		content := strings.ReplaceAll(block.Content, "****", "**\n\n**")
		return renderMarkdownBlock(m.theme.Thinking, label, content, width)
	case BlockAssistant:
		if state == "STREAMING" && !strings.Contains(block.Content, "\n") {
			return renderProseBlock(m.theme.Assistant, "", block.Content, width)
		}
		return renderMarkdownBlock(m.theme.Assistant, "", block.Content, width)
	default:
		return renderProseBlock(m.theme.Assistant, "AZEM", block.Content, width)
	}
}

func (m AppModel) renderUserMessage(content string, width int) []string {
	textWidth := max(1, width-2)
	lines := make([]string, 0)
	for _, line := range wrapText(content, textWidth) {
		lines = append(lines, m.theme.UserAccent.Render("▌")+" "+m.theme.User.Render(line))
	}
	return lines
}

func (m AppModel) renderHookPrompt(block Block, selected bool, width int) []string {
	lines := make([]string, 0, len(block.Hooks)*2)
	contentWidth := max(4, width-8)
	for _, hook := range block.Hooks {
		mark := stateMark(hook.State)
		if hook.State == "running" {
			mark = "•"
			if !m.reducedMotion {
				frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
				mark = string(frames[m.animationFrame%len(frames)])
			}
		}
		name := ""
		if hook.Name != "" && hook.Name != "hook" {
			name = " · " + hook.Name
		}
		label := padOrTrim(fmt.Sprintf("    %s %s · %s%s", mark, m.tr("hook.label"), hook.Event, name), width+2)
		style := m.theme.Muted
		if hook.State == "failed" || hook.State == "blocked" {
			style = m.stateStyle(hook.State)
		}
		if selected {
			lines = append(lines, m.theme.Selected.Render(label))
		} else {
			lines = append(lines, style.Render(label))
		}
		if (hook.State == "failed" || hook.State == "blocked") && first(hook.Reason, hook.Output) != "" {
			for _, line := range wrapText(first(hook.Reason, hook.Output), contentWidth) {
				lines = append(lines, m.stateStyle(hook.State).Render(padOrTrim("      │ "+line, width+2)))
			}
		}
	}
	return lines
}

func (m AppModel) renderDiffContent(content string, width int) []string {
	lines := make([]string, 0)
	for _, source := range strings.Split(content, "\n") {
		style := m.theme.Assistant
		switch {
		case strings.HasPrefix(source, "+"):
			style = m.theme.DiffAdd
		case strings.HasPrefix(source, "-"):
			style = m.theme.DiffDel
		case strings.HasPrefix(source, "@@"):
			style = m.theme.Diff
		}
		for _, line := range wrapText(source, width) {
			lines = append(lines, style.Render("      │ "+line))
		}
	}
	return lines
}

func toolDisplayName(name string) string {
	aliases := map[string]string{
		"coding.read_file":        "Read File",
		"coding.write_file":       "Write File",
		"coding.edit_hashline":    "Edit File",
		"coding.search":           "Search Code",
		"coding.list_files":       "List Files",
		"coding.shell":            "Run Command",
		"coding.go_test":          "Run Go Tests",
		"coding.gofmt":            "Format Go Code",
		"coding.git_diff":         "View Git Diff",
		"hydaelyn_activate_skill": "Load Skill",
		"subagent.spawn":          "Start Subagent",
	}
	if alias, ok := aliases[name]; ok {
		return alias
	}
	if !strings.ContainsAny(name, "._-") {
		return name
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	for index, part := range parts {
		if part != "" {
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

func (m AppModel) toolDisplayName(name string) string {
	keys := map[string]string{
		"coding.read_file": "tool.read_file", "coding.write_file": "tool.write_file", "coding.edit_hashline": "tool.edit_file",
		"coding.search": "tool.search", "coding.list_files": "tool.list_files", "coding.shell": "tool.shell",
		"coding.go_test": "tool.go_test", "coding.gofmt": "tool.gofmt", "coding.git_diff": "tool.git_diff",
		"hydaelyn_activate_skill": "tool.activate_skill", "subagent.spawn": "tool.spawn",
	}
	if key := keys[name]; key != "" {
		return m.tr(key)
	}
	return toolDisplayName(name)
}

func renderProseBlock(style lipgloss.Style, label string, content string, width int) []string {
	lines := make([]string, 0)
	if label != "" {
		lines = append(lines, style.Bold(true).Render("  "+label))
	}
	for _, line := range wrapText(content, max(4, width-2)) {
		lines = append(lines, style.Render("  "+line))
	}
	return lines
}

type markdownRendererKey struct {
	width int
	dark  bool
}

var markdownRendererCache = struct {
	sync.Mutex
	renderers map[markdownRendererKey]*glamour.TermRenderer
}{
	renderers: make(map[markdownRendererKey]*glamour.TermRenderer),
}

func renderMarkdownBlock(style lipgloss.Style, label string, content string, width int) []string {
	lines := make([]string, 0)
	if label != "" {
		lines = append(lines, style.Bold(true).Render("  "+label))
	}
	rendered, err := renderTerminalMarkdown(content, max(4, width-2))
	if err != nil {
		for _, line := range wrapText(content, max(4, width-2)) {
			lines = append(lines, style.Render("  "+line))
		}
		return lines
	}
	rendered = strings.Trim(rendered, "\n")
	if rendered == "" {
		return lines
	}
	return append(lines, strings.Split(rendered, "\n")...)
}

func renderTerminalMarkdown(content string, width int) (string, error) {
	markdownRendererCache.Lock()
	defer markdownRendererCache.Unlock()
	key := markdownRendererKey{width: width, dark: compat.HasDarkBackground}
	renderer := markdownRendererCache.renderers[key]
	if renderer == nil {
		if len(markdownRendererCache.renderers) >= 8 {
			markdownRendererCache.renderers = make(map[markdownRendererKey]*glamour.TermRenderer)
		}
		style := "light"
		if key.dark {
			style = "dark"
		}
		var err error
		renderer, err = glamour.NewTermRenderer(
			glamour.WithStylePath(style),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return "", err
		}
		markdownRendererCache.renderers[key] = renderer
	}
	return renderer.Render(content)
}

func (m AppModel) renderContextRail(width int, height int) string {
	activeAgents := m.activeAgents()
	rows := []string{
		m.theme.Muted.Render(padOrTrim("  RUN CONTEXT", width)),
		m.theme.Border.Render(padOrTrim("  "+strings.Repeat("─", max(0, width-4)), width)),
	}
	todoHeader := "  TODO"
	if completed, total := todoProgress(m.todo); total > 0 {
		todoHeader = fmt.Sprintf("  TODO  %d/%d", completed, total)
	}
	rows = append(rows, m.theme.Header.Render(padOrTrim(todoHeader, width)))
	todoRows, more := m.todoSummaryRows(4)
	if len(todoRows) == 0 {
		rows = append(rows, m.theme.Muted.Render(padOrTrim("  "+m.tr("rail.no_todos"), width)))
	}
	for _, row := range todoRows {
		style := m.stateStyle(string(row.status))
		if row.status == session.TodoCompleted || row.status == session.TodoCancelled {
			style = style.Strikethrough(true)
		}
		rows = append(rows, style.Render(padOrTrim("  "+row.text, width)))
	}
	if more > 0 {
		rows = append(rows, m.theme.Muted.Render(padOrTrim(fmt.Sprintf("  +%d more", more), width)))
	}
	rows = append(rows, "")
	rows = append(rows, m.theme.Header.Render(padOrTrim(fmt.Sprintf("  AGENTS  %d", len(activeAgents)), width)))
	if len(activeAgents) == 0 {
		rows = append(rows, m.theme.Muted.Render(padOrTrim("  "+m.tr("rail.no_agents"), width)))
	} else {
		for index, agent := range activeAgents {
			if index == 4 {
				rows = append(rows, m.theme.Muted.Render(padOrTrim(fmt.Sprintf("  +%d more", len(activeAgents)-index), width)))
				break
			}
			row := fmt.Sprintf("  %s %s", stateMark(agent.State), first(agent.Role, agent.ID))
			rows = append(rows, m.stateStyle(agent.State).Render(padOrTrim(row, width)))
		}
	}
	rows = append(rows, "", m.theme.Header.Render(padOrTrim(fmt.Sprintf("  MCP  %d", len(m.mcpServers)), width)))
	if len(m.mcpServers) == 0 {
		rows = append(rows, m.theme.Muted.Render(padOrTrim("  "+m.tr("rail.no_connections"), width)))
	} else {
		for index, server := range m.mcpServers {
			if index == 4 {
				rows = append(rows, m.theme.Muted.Render(padOrTrim(fmt.Sprintf("  +%d more", len(m.mcpServers)-index), width)))
				break
			}
			row := fmt.Sprintf("  %s %s · %d", stateMark(server.State), server.Name, server.ToolCount)
			rows = append(rows, m.stateStyle(server.State).Render(padOrTrim(row, width)))
		}
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return strings.Join(rows[:height], "\n")
}

type todoSummaryRow struct {
	text   string
	status session.TodoStatus
}

func todoProgress(todo session.TodoList) (completed int, total int) {
	for _, phase := range todo.Phases {
		for _, item := range phase.Items {
			if item.Status == session.TodoCancelled {
				continue
			}
			total++
			if item.Status == session.TodoCompleted {
				completed++
			}
		}
	}
	return completed, total
}

func (m AppModel) todoSummaryRows(limit int) ([]todoSummaryRow, int) {
	var current, pending, closed []todoSummaryRow
	for _, p := range m.todo.Phases {
		for _, item := range p.Items {
			row := todoSummaryRow{text: todoMark(item.Status, m.animationFrame) + "  " + item.Content, status: item.Status}
			switch item.Status {
			case "in_progress":
				current = append(current, row)
			case "pending":
				pending = append(pending, row)
			case "completed", "cancelled":
				closed = append(closed, row)
			}
		}
	}
	for left, right := 0, len(closed)-1; left < right; left, right = left+1, right-1 {
		closed[left], closed[right] = closed[right], closed[left]
	}
	all := append(append(current, pending...), closed...)
	if len(all) > limit {
		return all[:limit], len(all) - limit
	}
	return all, 0
}

func todoMark(status session.TodoStatus, frame int) string {
	switch status {
	case session.TodoCompleted:
		return "✓"
	case session.TodoCancelled:
		return "×"
	case session.TodoInProgress:
		return []string{"◐", "◓", "◑", "◒"}[frame%4]
	default:
		return "○"
	}
}

func (m AppModel) activeAgents() []AgentView {
	active := make([]AgentView, 0, len(m.agents))
	for _, agent := range m.agents {
		switch strings.ToLower(agent.State) {
		case "initializing", "queued", "running", "cancelling":
			active = append(active, agent)
		}
	}
	return active
}

func (m AppModel) renderModelStatus(width int) string {
	model := m.provider + "/" + first(m.model, "no model")
	if width >= 64 && m.reasoning != "" {
		model += " · " + m.tr("footer.reasoning") + " " + m.reasoning
	}
	left := m.theme.Header.Render(" "+m.tr("footer.model")+" ") + m.theme.Muted.Render(model)
	if width < 32 {
		return padStyledLine(left, width)
	}
	left = truncateStyledFallback(left, max(16, width/2))
	contextWidth := max(0, width-lipgloss.Width(left)-2)
	right := m.renderContextUsage(contextWidth)
	if right == "" {
		return padStyledLine(left, width)
	}
	return padStyledLine(left+"  "+right, width)
}

func (m AppModel) renderContextUsage(width int) string {
	if width <= 0 {
		return ""
	}
	used := max(0, m.usage.InputTokens+m.usage.OutputTokens)
	limit := m.usage.ContextLimit
	if limit <= 0 {
		return truncateStyledFallback(m.theme.Muted.Render(m.tr("footer.context")+" "+m.tr("footer.unavailable")), width)
	}
	percentage := float64(used) * 100 / float64(limit)
	contextLabel := m.tr("footer.context")
	cacheLabel := m.tr("footer.cache")
	cache := cacheLabel + " --"
	cacheRate := 0.0
	mainInput := m.usage.MainCacheInput
	mainCached := min(max(0, m.usage.MainCachedInput), mainInput)
	mainReported := m.usage.MainCacheReported
	if !mainReported && m.usage.CacheReported {
		mainInput = m.usage.CacheInputTokens
		mainCached = min(max(0, m.usage.CachedInputTokens), mainInput)
		mainReported = true
	}
	if mainReported && mainInput > 0 {
		cacheRate = float64(mainCached) * 100 / float64(mainInput)
		cache = fmt.Sprintf("%s %s/%s · %.1f%%", cacheLabel, formatTokens(mainCached), formatTokens(mainInput), cacheRate)
	}
	allInput := m.usage.CacheInputTokens
	allCached := min(max(0, m.usage.CachedInputTokens), allInput)
	allRate := 0.0
	if m.usage.CacheReported && allInput > 0 {
		allRate = float64(allCached) * 100 / float64(allInput)
	}
	hasChildUsage := allInput > mainInput
	if hasChildUsage && mainReported && mainInput > 0 {
		cache = fmt.Sprintf("%s %s %s/%s · %.1f%% · %s %.1f%%", cacheLabel, m.tr("footer.main"), formatTokens(mainCached), formatTokens(mainInput), cacheRate, m.tr("footer.all"), allRate)
	}
	compactCache := "--"
	if mainReported && mainInput > 0 {
		compactCache = fmt.Sprintf("%.0f%%", cacheRate)
	}
	cacheRateOnly := cacheLabel + " --"
	if mainReported && mainInput > 0 {
		cacheRateOnly = fmt.Sprintf("%s %.1f%%", cacheLabel, cacheRate)
	}
	if hasChildUsage && mainReported && mainInput > 0 {
		cacheRateOnly = fmt.Sprintf("%s M%.1f%% A%.1f%%", cacheLabel, cacheRate, allRate)
		compactCache = fmt.Sprintf("M%.0f/A%.0f%%", cacheRate, allRate)
	}
	candidates := []string{
		fmt.Sprintf("%s %s %s / %s · %.1f%% · %s", contextLabel, contextProgressBar(used, limit, 10), formatTokens(used), formatTokens(limit), percentage, cache),
		fmt.Sprintf("%s %s/%s · %.1f%% · %s", contextLabel, formatTokens(used), formatTokens(limit), percentage, cacheRateOnly),
		fmt.Sprintf("%s %s/%s %.0f%% C%s", contextLabel, formatTokens(used), formatTokens(limit), percentage, compactCache),
		fmt.Sprintf("%s %.0f%%", contextLabel, percentage),
	}
	text := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if ansi.StringWidth(candidate) <= width {
			text = candidate
			break
		}
	}
	text = ansi.Truncate(text, width, "…")
	switch {
	case percentage >= 90:
		return m.theme.Error.Render(text)
	case percentage >= 70:
		return m.theme.Warning.Render(text)
	default:
		return m.theme.Muted.Render(text)
	}
}

func contextProgressBar(used int, limit int, width int) string {
	if limit <= 0 || width <= 0 {
		return ""
	}
	clamped := min(max(0, used), limit)
	filled := int((int64(clamped)*int64(width) + int64(limit)/2) / int64(limit))
	return "[" + strings.Repeat("■", filled) + strings.Repeat("·", width-filled) + "]"
}

func (m AppModel) renderStatus(width int) string {
	statusText := stateMark(m.status) + " " + m.displayState(m.status)
	status := m.stateStyle(m.status).Bold(true).Render(" " + statusText)
	switch m.approvalMode {
	case ApprovalModeYolo:
		status += m.theme.FullAccess.Render(" · ⚠ " + m.tr("status.approval.yolo"))
	case ApprovalModeAutoReview:
		status += m.theme.ApprovalSmart.Render(" · ⛨ " + m.tr("status.approval.auto"))
	default:
		status += m.theme.ApprovalAsk.Render(" · ☝︎ " + m.tr("status.approval.ask"))
	}
	if m.actionBusy {
		status += m.theme.Warning.Render(" · " + m.tr("status.working"))
	} else if m.errorBanner != "" {
		errorInTranscript := false
		for index := len(m.transcript) - 1; index >= 0; index-- {
			if m.transcript[index].Kind == BlockError && m.transcript[index].Content == m.errorBanner {
				errorInTranscript = true
				break
			}
		}
		if errorInTranscript {
			status += m.theme.Error.Render(" · DETAILS IN TRANSCRIPT")
			return padStyledLine(status, width)
		}
		status += m.theme.Error.Render(" · " + m.errorBanner)
	}
	helpText := strings.Join([]string{m.tr("footer.help.approval"), m.tr("footer.help.reasoning"), m.tr("footer.help.commands"), m.tr("status.help")}, "  ")
	if width < 112 {
		helpText = strings.Join([]string{m.tr("footer.help.approval"), m.tr("footer.help.reasoning"), m.tr("status.help")}, "  ")
	}
	if width < 86 {
		helpText = strings.Join([]string{m.tr("footer.help.approval"), m.tr("status.help")}, "  ")
	}
	if width < 64 {
		helpText = m.tr("status.help")
	}
	return joinSides(status, m.theme.Muted.Render(helpText+" "), width)
}

func (m AppModel) renderCommandSuggestions(width int, height int, suggestions []SlashCommand) string {
	if height <= 0 || len(suggestions) == 0 {
		return ""
	}
	cursor := min(max(0, m.commandCursor), len(suggestions)-1)
	optionRows := height
	showFooter := height > 1
	if showFooter {
		optionRows--
	}
	start := optionWindowStart(cursor, len(suggestions), optionRows)
	end := min(len(suggestions), start+optionRows)
	rows := make([]string, 0, height)
	for index := start; index < end; index++ {
		command := suggestions[index]
		selected := index == cursor
		if selected {
			text := " › " + command.Usage
			if width >= 52 {
				text += "  " + command.Detail
			}
			rows = append(rows, m.theme.Selected.Render(padOrTrim(text, width)))
			continue
		}
		text := "   " + m.theme.Header.Render(command.Usage)
		if width >= 52 {
			text += m.theme.Muted.Render("  " + command.Detail)
		}
		rows = append(rows, padOrTrim(text, width))
	}
	for len(rows) < optionRows {
		rows = append(rows, "")
	}
	if showFooter {
		rows = append(rows, m.theme.Muted.Render(padOrTrim("   ↑/↓ select · Tab complete · Enter run", width)))
	}
	return strings.Join(rows, "\n")
}

func (m AppModel) renderOverlay(width int, height int) string {
	if width < 6 || height < 5 {
		title, _ := m.overlayHeading()
		if m.overlay == OverlayAgentDetail {
			title = "Task detail"
		}
		rows := []string{m.theme.Header.Render(strings.ToUpper(title))}
		if height > 1 {
			rows = append(rows, m.theme.Muted.Render(m.overlayFooterForWidth(width)))
		}
		return fitViewport(strings.Join(rows, "\n"), width, height)
	}
	if m.overlay == OverlayAgentDetail {
		return m.renderAgentDetailOverlay(width, height)
	}
	maxBoxWidth := 82
	if m.overlay == OverlayAgentTypes || m.overlay == OverlayPersonas || m.overlay == OverlaySkills {
		maxBoxWidth = 110
	}
	boxWidth := min(maxBoxWidth, max(3, width-2))
	innerWidth := max(1, boxWidth-2)
	innerHeight := max(1, min(height-2, 20))
	title, subtitle := m.overlayHeading()
	description := m.overlayDescription()
	options := m.overlayOptions()

	contentRows := innerHeight - 4
	if m.overlay == OverlayModel {
		contentRows--
	}
	if subtitle == "" {
		contentRows++
	}
	if contentRows < 1 {
		contentRows = 1
	}
	descriptionLines := make([]string, 0)
	for _, line := range description {
		descriptionLines = append(descriptionLines, wrapText(line, max(4, innerWidth-4))...)
	}
	maxDescription := max(0, contentRows-1)
	if len(options) == 0 {
		maxDescription = max(1, contentRows)
		start := min(max(0, m.overlayScroll), max(0, len(descriptionLines)-maxDescription))
		descriptionLines = descriptionLines[start:min(len(descriptionLines), start+maxDescription)]
	} else {
		maxDescription = min(maxDescription, 4)
		if len(descriptionLines) > maxDescription {
			descriptionLines = append(descriptionLines[:max(0, maxDescription-1)], "…")
		}
	}
	optionRows := max(0, contentRows-len(descriptionLines))
	renderRows, selectedRow := buildOverlayRenderRows(options, m.overlayCursor)
	start := optionWindowStart(selectedRow, len(renderRows), optionRows)
	stickyGroup := ""
	visibleRows := optionRows
	if start > 0 && visibleRows >= 2 && renderRows[start].OptionIndex >= 0 {
		stickyGroup = renderRows[start].Group
		visibleRows--
	}
	end := min(len(renderRows), start+visibleRows)

	rows := make([]string, 0, innerHeight)
	rows = append(rows, m.boxRow(" "+strings.ToUpper(title), innerWidth, m.theme.Header, false))
	if subtitle != "" {
		rows = append(rows, m.boxRow(" "+subtitle, innerWidth, m.theme.Muted, false))
	}
	if m.overlay == OverlayModel {
		rows = append(rows, m.boxRow(" "+m.modelSearch.View(), innerWidth, m.theme.Assistant, false))
	}
	rows = append(rows, m.boxRow(" "+strings.Repeat("─", max(0, innerWidth-2)), innerWidth, m.theme.Border, false))
	for _, line := range descriptionLines {
		rows = append(rows, m.boxRow(" "+line, innerWidth, m.theme.Assistant, false))
	}
	if stickyGroup != "" {
		rows = append(rows, m.boxRow(" "+strings.ToUpper(providerDisplayName(stickyGroup)), innerWidth, m.theme.Header, false))
	}
	for _, renderRow := range renderRows[start:end] {
		if renderRow.OptionIndex < 0 {
			rows = append(rows, m.boxRow(" "+strings.ToUpper(providerDisplayName(renderRow.Group)), innerWidth, m.theme.Header, false))
			continue
		}
		option := options[renderRow.OptionIndex]
		text := "   " + option.Label
		if option.State != "" {
			text += "  " + strings.ToUpper(option.State)
		}
		if option.Detail != "" {
			text += " · " + option.Detail
		}
		selected := renderRow.OptionIndex == m.overlayCursor
		if selected {
			text = " › " + strings.TrimLeft(text, " ")
		}
		style := m.stateStyle(option.State)
		if selected {
			style = m.theme.Selected
		}
		if m.overlay == OverlayTodos && option.State == string(session.TodoCompleted) {
			style = style.Strikethrough(true)
		}
		if m.overlay == OverlayTodos && option.State == string(session.TodoCancelled) {
			style = style.Strikethrough(true)
		}
		rows = append(rows, m.boxRow(text, innerWidth, style, false))
	}
	for len(rows) < innerHeight-1 {
		rows = append(rows, m.boxRow("", innerWidth, m.theme.Assistant, false))
	}
	footer := m.overlayFooterForWidth(max(1, innerWidth-1))
	rows = append(rows, m.boxRow(" "+footer, innerWidth, m.theme.Muted, false))
	if len(rows) > innerHeight {
		rows = append(rows[:innerHeight-1], m.boxRow(" "+footer, innerWidth, m.theme.Muted, false))
	}

	top := m.theme.Border.Render("┌" + strings.Repeat("─", innerWidth) + "┐")
	bottom := m.theme.Border.Render("└" + strings.Repeat("─", innerWidth) + "┘")
	box := []string{top}
	for _, row := range rows {
		box = append(box, m.theme.Border.Render("│")+row+m.theme.Border.Render("│"))
	}
	box = append(box, bottom)

	boxHeight := len(box)
	topPadding := max(0, (height-boxHeight)/2)
	leftPadding := strings.Repeat(" ", max(0, (width-boxWidth)/2))
	output := make([]string, 0, height)
	for range topPadding {
		output = append(output, "")
	}
	for _, line := range box {
		output = append(output, leftPadding+line)
	}
	for len(output) < height {
		output = append(output, "")
	}
	return strings.Join(output[:height], "\n")
}

func (m AppModel) renderAgentDetailOverlay(width, height int) string {
	boxWidth := min(96, max(3, width-2))
	innerWidth := max(1, boxWidth-2)
	innerHeight := max(1, min(height-2, 28))
	agent, found := m.agentDetail()
	content := make([]string, 0)
	if !found {
		content = append(content, "Task is no longer available.")
	} else {
		content = append(content,
			fmt.Sprintf("%s · %s", first(agent.Role, "Subagent"), strings.ToUpper(first(agent.State, "unknown"))),
			fmt.Sprintf("ID %s", agent.ID),
		)
		if agent.Description != "" {
			content = append(content, "Goal: "+agent.Description)
		}
		if agent.Activity != "" {
			content = append(content, "Activity: "+agent.Activity)
		}
		content = append(content,
			fmt.Sprintf("Model: %s · capability: %s · background: %t", first(agent.Model, "inherit"), first(agent.CapabilityMode, "inherit"), agent.Background),
			fmt.Sprintf("Isolation: %s · requested: %s", first(agent.Isolation, "none"), first(agent.RequestedIsolation, "none")),
			fmt.Sprintf("Stats: %d tools · %d turns · %d tokens · %.1fs", agent.ToolCalls, agent.Turns, agent.TokensUsed, float64(agent.ElapsedMS)/1000),
		)
		if agent.CWD != "" {
			content = append(content, "CWD: "+agent.CWD)
		}
		if agent.Warning != "" {
			content = append(content, "Warning: "+agent.Warning)
		}
		if agent.WorktreePath != "" {
			content = append(content, "Worktree: "+agent.WorktreePath)
		}
		content = append(content, strings.Repeat("─", max(0, innerWidth-4)))
		if len(agent.Blocks) == 0 {
			content = append(content, "No child transcript yet.")
		} else {
			for _, block := range agent.Blocks {
				if block.Kind == BlockHook {
					content = append(content, m.renderHookPrompt(block, false, max(4, innerWidth-2))...)
					continue
				}
				title := first(block.Title, string(block.Kind))
				if block.Kind == BlockTool {
					title = m.toolDisplayName(title)
				}
				content = append(content, fmt.Sprintf("%s · %s", strings.ToUpper(title), strings.ToUpper(first(block.State, "completed"))))
				for _, line := range wrapText(block.Content, max(4, innerWidth-4)) {
					content = append(content, "  "+line)
				}
			}
		}
	}
	rowsAvailable := max(1, innerHeight-4)
	start := min(max(0, m.overlayScroll), max(0, len(content)-rowsAvailable))
	end := min(len(content), start+rowsAvailable)
	rows := []string{
		m.boxRow(" TASK DETAIL", innerWidth, m.theme.Header, false),
		m.boxRow(" "+first(agent.Description, agent.ID, "Child execution"), innerWidth, m.theme.Muted, false),
		m.boxRow(" "+strings.Repeat("─", max(0, innerWidth-2)), innerWidth, m.theme.Border, false),
	}
	for _, line := range content[start:end] {
		rows = append(rows, m.boxRow(" "+line, innerWidth, m.theme.Assistant, false))
	}
	for len(rows) < innerHeight-1 {
		rows = append(rows, m.boxRow("", innerWidth, m.theme.Assistant, false))
	}
	footer := m.boxRow(" "+m.overlayFooterForWidth(max(1, innerWidth-1)), innerWidth, m.theme.Muted, false)
	rows = append(rows, footer)
	if len(rows) > innerHeight {
		rows = append(rows[:innerHeight-1], footer)
	}
	top := m.theme.Border.Render("┌" + strings.Repeat("─", innerWidth) + "┐")
	bottom := m.theme.Border.Render("└" + strings.Repeat("─", innerWidth) + "┘")
	box := []string{top}
	for _, row := range rows {
		box = append(box, m.theme.Border.Render("│")+row+m.theme.Border.Render("│"))
	}
	box = append(box, bottom)
	topPadding := max(0, (height-len(box))/2)
	leftPadding := strings.Repeat(" ", max(0, (width-boxWidth)/2))
	output := make([]string, 0, height)
	for range topPadding {
		output = append(output, "")
	}
	for _, line := range box {
		output = append(output, leftPadding+line)
	}
	for len(output) < height {
		output = append(output, "")
	}
	return strings.Join(output[:height], "\n")
}

func (m AppModel) agentDetail() (AgentView, bool) {
	for _, agent := range m.agents {
		if agent.ID == m.detailAgentID {
			return agent, true
		}
	}
	return AgentView{}, false
}

func (m AppModel) boxRow(text string, width int, style lipgloss.Style, selected bool) string {
	text = padOrTrim(text, width)
	if selected {
		return m.theme.Selected.Render(text)
	}
	return style.Render(text)
}

func (m AppModel) overlayHeading() (string, string) {
	switch m.overlay {
	case OverlayHelp:
		return "Keyboard help", "Every action remains keyboard reachable"
	case OverlayCommand:
		return m.tr("overlay.command.title"), m.tr("overlay.command.subtitle")
	case OverlayProvider:
		if m.overlayPurpose == "login" {
			return "Sign in", "Choose a subscription provider"
		}
		return "Provider", "Choose the active provider"
	case OverlayModel:
		count := m.modelCatalogCount()
		if count == 1 {
			return "Model", "1 configured provider catalog"
		}
		return "Model", fmt.Sprintf("%d configured provider catalogs", count)
	case OverlaySkills:
		return "Skills", "Reload affects new turns only"
	case OverlayLanguage:
		return m.tr("overlay.language.title"), m.tr("overlay.language.subtitle")
	case OverlayReasoning:
		return "Thinking level", m.provider + "/" + first(m.model, "no model") + " · applied to the next turn"
	case OverlaySessions:
		return "Resume session", "Choose a saved conversation"
	case OverlayApproval:
		return m.tr("overlay.approval.title"), m.tr("overlay.approval.subtitle")
	case OverlayCancel:
		return "Cancel run", "Choose whether child tasks should continue"
	case OverlayDiff:
		return "Diff", "Proposed workspace change"
	case OverlayAgents:
		return "Agents", strings.ToUpper(m.agentMode) + " mode"
	case OverlayTodos:
		return m.tr("overlay.todos.title"), m.tr("overlay.todos.subtitle")
	case OverlayAgentDetail:
		return "Task detail", "Live child transcript and execution metadata"
	case OverlayAgentTypes:
		return "Agent types", "Effective role configuration"
	case OverlayPersonas:
		return "Personas", "Effective persona configuration"
	case OverlayMCP:
		return "MCP servers", "External tools remain governed"
	case OverlayRecovery:
		return "Recovery requires attention", "Nothing uncertain is replayed automatically"
	case OverlayError:
		return "Application error", "Azem cannot continue normally"
	default:
		return "Azem", ""
	}
}

func (m AppModel) overlayDescription() []string {
	switch m.overlay {
	case OverlayTodos:
		if len(m.todo.Phases) == 0 {
			return []string{m.tr("overlay.todos.empty")}
		}
		if m.todo.Goal != "" {
			return []string{m.todo.Goal}
		}
	case OverlayHelp:
		return []string{
			"Enter submit · Ctrl+J newline · Esc close or cancel",
			"Ctrl+P commands · Ctrl+M model · Ctrl+R reasoning · Shift+Tab approval",
			"Ctrl+B agents · PageUp/PageDown transcript · Tab cards",
			"/login /logout /provider /models /skills /skill /new /sessions /resume /compact",
			"/team /agents /mcp /reconcile /cancel /help /quit",
		}
	case OverlayProvider:
		if m.overlayPurpose == "login" {
			return []string{
				"ChatGPT uses Codex-compatible OAuth; it is not an official Azem OAuth contract.",
				"Grok uses an experimental Grok CLI-compatible public-client flow.",
				"Existing credentials: /login chatgpt --import-codex · /login grok --import",
			}
		}
	case OverlayModel:
		if len(m.modelPickerEntries()) == 0 {
			if query := strings.TrimSpace(m.modelSearch.Value()); query != "" {
				return []string{m.tr("overlay.models.no_match", map[string]string{"query": strconv.Quote(query)})}
			}
			return []string{m.tr("overlay.models.empty")}
		}
	case OverlaySkills:
		if len(m.skills) == 0 {
			return []string{m.tr("overlay.skills.empty")}
		}
		warnings := make([]string, 0, min(4, len(m.skillDiagnostics)))
		for index, diagnostic := range m.skillDiagnostics {
			if index == 3 {
				break
			}
			path := first(shortenPath(diagnostic.Path, 32), "skill root")
			warnings = append(warnings, "Warning · "+path+": "+diagnostic.Message)
		}
		if remaining := len(m.skillDiagnostics) - len(warnings); remaining > 0 {
			warnings = append(warnings, fmt.Sprintf("%d more warnings", remaining))
		}
		return warnings
	case OverlayReasoning:
		if len(m.reasoningLevels()) == 0 {
			return []string{"The selected model does not advertise adjustable reasoning."}
		}
	case OverlaySessions:
		if len(m.sessions) == 0 {
			return []string{m.tr("overlay.sessions.empty")}
		}
		return []string{"Use Up/Down to choose, Enter to resume, Esc to close."}
	case OverlayApproval:
		if m.approval == nil {
			return []string{m.tr("overlay.approval.resolved")}
		}
		return []string{
			m.tr("overlay.approval.tool", map[string]string{"name": m.toolDisplayName(first(m.approval.Tool, "unknown"))}),
			m.tr("overlay.approval.target", map[string]string{"name": first(m.approval.Target, "workspace")}),
			m.tr("overlay.approval.risk", map[string]string{"risk": first(m.approval.Risk, "unspecified"), "effect": first(m.approval.Effect, "unspecified")}),
			first(m.approval.Action, m.approval.Diff),
		}
	case OverlayCancel:
		return []string{"The parent run is active with at least one foreground child task."}
	case OverlayDiff:
		if block, ok := m.selectedDiff(); ok {
			return strings.Split(block.Content, "\n")
		}
		return []string{m.tr("overlay.diff.empty")}
	case OverlayAgents:
		if len(m.agents) == 0 {
			return []string{m.tr("overlay.agents.empty")}
		}
	case OverlayMCP:
		if len(m.mcpServers) == 0 {
			return []string{m.tr("overlay.mcp.empty")}
		}
	case OverlayRecovery:
		if len(m.recovery) == 0 {
			return []string{"No pending recovery decisions."}
		}
		return []string{"Approvals can be opened with Enter. Unknown side effects must be checked outside Azem before /reconcile."}
	case OverlayError:
		return []string{first(m.errorBanner, "The application event stream stopped.")}
	}
	return nil
}

func (m AppModel) overlayOptions() []overlayOption {
	switch m.overlay {
	case OverlayTodos:
		options := []overlayOption{}
		for _, phase := range m.todo.Phases {
			for _, item := range phase.Items {
				if m.todoHideCompleted && item.Status == session.TodoCompleted {
					continue
				}
				detail := string(item.Status) + " · " + item.ID
				if item.SubagentRunID != "" {
					for _, agent := range m.agents {
						if agent.ID == item.SubagentRunID {
							detail += " · agent " + agent.State
						}
					}
				}
				options = append(options, overlayOption{Group: phase.Title, Label: todoMark(item.Status, m.animationFrame) + "  " + item.Content, Detail: detail, State: string(item.Status)})
			}
		}
		return options
	case OverlayCommand:
		options := make([]overlayOption, 0, len(commandPaletteOptions))
		for _, id := range commandPaletteOptions {
			options = append(options, overlayOption{Label: m.tr("command_palette." + id + ".label"), Detail: m.tr("command_palette." + id + ".detail")})
		}
		return options
	case OverlayProvider:
		options := make([]overlayOption, 0, 2)
		for _, provider := range []string{"chatgpt", "grok"} {
			auth := m.auth[provider]
			detail := "Not signed in"
			state := auth.State
			if auth.Email != "" {
				detail = auth.Email
				if auth.Plan != "" {
					detail += " · " + auth.Plan
				}
			}
			if provider == m.provider && m.overlayPurpose != "login" {
				state = "selected"
			}
			options = append(options, overlayOption{Label: provider, Detail: detail, State: state})
		}
		return options
	case OverlayModel:
		entries := m.modelPickerEntries()
		options := make([]overlayOption, 0, len(entries))
		for _, entry := range entries {
			model := entry.Model
			capabilities := make([]string, 0, 3)
			if model.ContextWindow > 0 {
				capabilities = append(capabilities, formatTokens(model.ContextWindow)+" context")
			}
			if model.SupportsTools {
				capabilities = append(capabilities, "tools")
			}
			if model.SupportsReasoning {
				capabilities = append(capabilities, "reasoning")
			}
			state := ""
			if entry.Provider == m.provider && model.ID == m.model {
				state = "selected"
			}
			options = append(options, overlayOption{
				Group: entry.Provider, Label: first(model.Name, model.ID),
				Detail: strings.Join(capabilities, " · "), State: state,
			})
		}
		return options
	case OverlaySkills:
		options := make([]overlayOption, 0, len(m.skills))
		for _, entry := range m.skills {
			source := first(shortenPath(entry.SourcePath, 30), "disk")
			if entry.Bundled {
				source = "bundled"
			}
			resourceLabel := fmt.Sprintf("%d resources", entry.ResourceCount)
			if entry.ResourceCount == 1 {
				resourceLabel = "1 resource"
			}
			state := "manual-only"
			switch {
			case entry.Disabled:
				state = "disabled"
			case entry.Eager:
				state = "eager"
			case entry.ModelVisible:
				state = "available"
			}
			options = append(options, overlayOption{
				Label: entry.Name, State: state,
				Detail: entry.Description + " · " + source + " · " + resourceLabel,
			})
		}
		return options
	case OverlayLanguage:
		languages := i18n.Languages()
		options := make([]overlayOption, 0, len(languages))
		for _, language := range languages {
			locale, err := i18n.New(language)
			label := language
			if err == nil {
				label = locale.T("language.name")
			}
			state := ""
			if language == m.catalog.Language() {
				state = "selected"
			}
			options = append(options, overlayOption{Label: label, Detail: language, State: state})
		}
		return options
	case OverlayReasoning:
		levels := m.reasoningLevels()
		options := make([]overlayOption, 0, len(levels))
		for _, level := range levels {
			state := ""
			if level == m.reasoning {
				state = "selected"
			}
			options = append(options, overlayOption{Label: level, Detail: reasoningLevelDetail(level), State: state})
		}
		return options
	case OverlaySessions:
		options := make([]overlayOption, 0, len(m.sessions))
		for _, session := range m.sessions {
			detail := strings.TrimSpace(session.ProviderID + "/" + session.ModelID)
			if session.UpdatedAt != "" {
				detail += " · " + session.UpdatedAt
			}
			state := ""
			if session.ID == m.sessionID {
				state = "current"
			}
			options = append(options, overlayOption{Label: first(session.Title, session.ID), Detail: detail, State: state})
		}
		return options
	case OverlayApproval:
		return []overlayOption{
			{Label: m.tr("overlay.approval.once"), Detail: m.tr("overlay.approval.once_detail"), State: "a"},
			{Label: m.tr("overlay.approval.session"), Detail: m.tr("overlay.approval.session_detail"), State: "shift+a"},
			{Label: m.tr("overlay.approval.deny"), Detail: m.tr("overlay.approval.deny_detail"), State: "d"},
		}
	case OverlayCancel:
		return []overlayOption{
			{Label: "Cancel parent only", Detail: "Foreground child tasks continue in the background"},
			{Label: "Cancel parent and children", Detail: "Cancel every active child spawned by this parent"},
		}
	case OverlayAgents:
		options := make([]overlayOption, 0, len(m.agents))
		for _, agent := range m.agents {
			options = append(options, overlayOption{Label: first(agent.Role, agent.ID), Detail: agent.Summary, State: agent.State})
		}
		return options
	case OverlayAgentTypes:
		options := make([]overlayOption, 0, len(m.agentTypes))
		for _, entry := range m.agentTypes {
			detail := strings.TrimSpace(strings.Join([]string{
				first(entry.Model, "inherit"), first(entry.CapabilityMode, "inherit"),
				first(entry.Isolation, "none"), first(entry.Source, "builtin"),
			}, " · "))
			state := "enabled"
			if !entry.Enabled {
				state = "disabled"
			}
			options = append(options, overlayOption{Label: entry.Name, Detail: detail, State: state})
		}
		return options
	case OverlayPersonas:
		options := make([]overlayOption, 0, len(m.personas))
		for _, entry := range m.personas {
			detail := strings.TrimSpace(strings.Join([]string{
				first(entry.Model, "inherit"), first(entry.CapabilityMode, "inherit"),
				first(entry.Isolation, "none"), first(entry.Source, "builtin"),
			}, " · "))
			options = append(options, overlayOption{Label: entry.Name, Detail: detail, State: "enabled"})
		}
		return options
	case OverlayMCP:
		options := make([]overlayOption, 0, len(m.mcpServers))
		for _, server := range m.mcpServers {
			detail := strconv.Itoa(server.ToolCount) + " tools"
			if server.Error != "" {
				detail = server.Error
			}
			options = append(options, overlayOption{Label: server.Name, Detail: detail, State: server.State})
		}
		return options
	case OverlayRecovery:
		options := make([]overlayOption, 0, len(m.recovery))
		for _, item := range m.recovery {
			label := item.Title
			if item.ToolName != "" {
				label += " · " + item.ToolName
			}
			options = append(options, overlayOption{Label: label, Detail: item.Detail, State: item.State})
		}
		return options
	}
	return nil
}

func reasoningLevelDetail(level string) string {
	switch level {
	case "minimal":
		return "Fastest · minimal deliberate reasoning"
	case "low":
		return "Fast · light reasoning"
	case "medium":
		return "Balanced depth and latency"
	case "high":
		return "Deep reasoning · higher latency"
	case "xhigh":
		return "Maximum supported reasoning effort"
	default:
		return "Provider-defined reasoning effort"
	}
}

func (m AppModel) overlayFooter() string {
	if m.actionBusy {
		return "Working · Esc cancels this action"
	}
	switch m.overlay {
	case OverlayModel:
		return "Type to search · ↑/↓ select · Enter confirm · Esc clear/close"
	case OverlaySkills:
		return "↑/↓ browse · R reload · Esc close"
	case OverlayApproval:
		return "Enter select · A once · Shift+A session · D deny · Esc inspect transcript"
	case OverlayCancel:
		return "↑/↓ choose scope · Enter cancel · Esc keep running"
	case OverlayAgents:
		return "↑/↓ select · Enter detail · X cancel child · Esc close"
	case OverlayTodos:
		return "↑/↓ select · H hide completed · Esc close"
	case OverlayAgentDetail:
		return "↑/↓ scroll · Esc return to tasks"
	case OverlayAgentTypes, OverlayPersonas:
		return "↑/↓ browse · Esc close"
	case OverlayMCP:
		return "↑/↓ select · R refresh · Shift+R reconnect · Esc close"
	case OverlayRecovery:
		return "↑/↓ select · Enter inspect · /reconcile confirms an unknown side effect · Esc close"
	case OverlayError:
		return "Q quit · Esc return"
	case OverlayHelp, OverlayDiff:
		return "↑/↓ scroll · Esc close"
	default:
		return "↑/↓ select · Enter confirm · Esc close"
	}
}

func (m AppModel) overlayFooterForWidth(width int) string {
	footer := m.overlayFooter()
	if ansi.StringWidth(footer) <= width {
		return footer
	}
	switch m.overlay {
	case OverlayApproval:
		return "A once · D deny · Esc back"
	case OverlayCancel:
		return "Enter cancel · Esc back"
	case OverlayAgents:
		return "Enter detail · Esc close"
	case OverlayAgentDetail:
		return "↑/↓ scroll · Esc back"
	case OverlayError:
		return "Q quit · Esc back"
	case OverlayHelp, OverlayDiff:
		return "↑/↓ · Esc close"
	default:
		return "↑/↓ · Enter · Esc close"
	}
}

func (m AppModel) selectedDiff() (Block, bool) {
	if m.transcriptCursor >= 0 && m.transcriptCursor < len(m.transcript) {
		block := m.transcript[m.transcriptCursor]
		if block.Kind == BlockDiff {
			return block, true
		}
	}
	for index := len(m.transcript) - 1; index >= 0; index-- {
		if m.transcript[index].Kind == BlockDiff {
			return m.transcript[index], true
		}
	}
	return Block{}, false
}

func (m AppModel) stateStyle(state string) lipgloss.Style {
	switch strings.ToLower(state) {
	case "ready", "completed", "approved", "succeeded", "active", "selected", "current", "eager", "available":
		return m.theme.Success
	case "failed", "error", "degraded", "denied", "cancelled", "disabled", "application stopped", "recovery attention":
		return m.theme.Error
	case "running", "reviewing", "starting", "streaming", "connecting", "queued", "cancelling", "awaiting approval", "blocked", "a", "shift+a", "d":
		return m.theme.Warning
	default:
		return m.theme.Muted
	}
}

func stateMark(state string) string {
	switch strings.ToLower(state) {
	case "ready", "completed", "succeeded", "active", "selected", "current":
		return "✓"
	case "failed", "error", "degraded", "denied", "blocked", "application stopped", "recovery attention":
		return "!"
	case "cancelled", "cancelling":
		return "×"
	case "awaiting approval":
		return "?"
	case "running", "starting", "streaming", "connecting", "queued":
		return "◆"
	default:
		return "○"
	}
}

func optionWindowStart(cursor int, total int, visible int) int {
	if total <= visible || visible <= 0 {
		return 0
	}
	start := cursor - visible/2
	if start < 0 {
		return 0
	}
	if start+visible > total {
		return total - visible
	}
	return start
}

func joinSides(left string, right string, width int) string {
	if width <= 0 {
		return ""
	}
	right = truncateStyledFallback(right, width)
	rightWidth := lipgloss.Width(right)
	gap := 0
	if left != "" && right != "" && width-rightWidth >= 2 {
		gap = 2
	}
	left = truncateStyledFallback(left, max(0, width-rightWidth-gap))
	leftWidth := lipgloss.Width(left)
	return left + strings.Repeat(" ", max(gap, width-leftWidth-rightWidth)) + right
}

func fitViewport(value string, width int, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index, line := range lines {
		lines[index] = padStyledLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func padStyledLine(value string, width int) string {
	if ansi.StringWidth(value) > width {
		return ansi.Truncate(value, width, "…")
	}
	return value + strings.Repeat(" ", width-ansi.StringWidth(value))
}

func truncateStyledFallback(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 1 {
		return ansi.Truncate(value, max(0, width), "")
	}
	return ansi.Truncate(value, width, "…")
}

func wrapText(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	return strings.Split(ansi.Wrap(text, max(1, width), " "), "\n")
}

func padOrTrim(value string, width int) string {
	if width <= 0 {
		return ""
	}
	displayWidth := ansi.StringWidth(value)
	if displayWidth > width {
		if width == 1 {
			return ansi.Truncate(value, 1, "")
		}
		return ansi.Truncate(value, width, "…")
	}
	return value + strings.Repeat(" ", width-displayWidth)
}

func shortenPath(path string, width int) string {
	if utf8.RuneCountInString(path) <= width {
		return path
	}
	base := filepath.Base(path)
	if utf8.RuneCountInString(base)+2 <= width {
		return "…/" + base
	}
	runes := []rune(base)
	if len(runes) >= width {
		return "…" + string(runes[len(runes)-width+1:])
	}
	return base
}

func formatTokens(tokens int) string {
	tokens = max(0, tokens)
	switch {
	case tokens >= 1_000_000:
		return formatTokenUnit(tokens, 1_000_000, "M")
	case tokens >= 1_000:
		return formatTokenUnit(tokens, 1_000, "K")
	default:
		return strconv.Itoa(tokens)
	}
}

func formatTokenUnit(tokens int, unit int, suffix string) string {
	whole := tokens / unit
	if whole >= 10 {
		return strconv.Itoa(whole) + suffix
	}
	tenths := (tokens % unit) * 10 / unit
	if tenths == 0 {
		return strconv.Itoa(whole) + suffix
	}
	return fmt.Sprintf("%d.%d%s", whole, tenths, suffix)
}
