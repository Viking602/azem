package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var commandPaletteOptions = []string{
	"login", "provider", "models", "model-routing", "skills", "reasoning", "sessions", "new", "recap", "status", "agents", "mcp", "cancel", "help", "quit",
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
	composer := m.renderComposer()
	attachments := m.renderPendingAttachments(width)
	attachmentRows := 0
	if attachments != "" {
		attachmentRows = 1
	}
	suggestions := m.visibleCommandSuggestions()
	recapStatus := m.visibleRecapStatus(width, height)
	recapRows := 0
	if recapStatus != "" {
		recapRows = 1
	}
	composerLines := strings.Count(composer, "\n") + 1 + attachmentRows
	layout := measureViewLayout(height, width, composerLines, len(suggestions), recapRows)
	sections := make([]string, 0, 10)
	if layout.showChrome {
		// Top chrome only — the rounded composer dock replaces the old bottom rule.
		sections = append(sections, header, m.theme.Border.Render(strings.Repeat("─", width)))
	}
	if layout.bodyHeight > 0 {
		sections = append(sections, m.renderBody(width, layout.bodyHeight))
	}
	if layout.suggestionHeight > 0 {
		sections = append(sections, m.renderCommandSuggestions(width, layout.suggestionHeight, suggestions))
	}
	if recapStatus != "" {
		sections = append(sections, recapStatus)
	}
	if attachments != "" {
		sections = append(sections, attachments)
	}
	// Keep one row of the measured composer block for the attachment strip.
	composerHeight := max(1, layout.composerHeight-attachmentRows)
	sections = append(sections, fitViewport(composer, width, composerHeight))
	if layout.footerHeight > 0 {
		sections = append(sections, m.renderDockFooter(width, layout.footerHeight))
	}
	view := tea.NewView(fitViewport(strings.Join(sections, "\n"), width, height))
	if cursor := m.composer.Cursor(); cursor != nil {
		cursor.Position.X += m.theme.PanelFocused.GetPaddingLeft() + m.theme.PanelFocused.GetBorderLeftSize()
		cursor.Position.Y += composerOffsetY(layout) + attachmentRows + m.theme.PanelFocused.GetPaddingTop() + m.theme.PanelFocused.GetBorderTopSize()
		view.Cursor = cursor
	}
	view.AltScreen = true
	view.ReportFocus = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "Azem"
	return view
}

func (m AppModel) renderComposer() string {
	style := m.theme.PanelBlurred
	if m.composer.Focused() {
		style = m.theme.PanelFocused
	}
	return style.Render(m.composer.View())
}

func (m AppModel) composerBlockLines() int {
	lines := strings.Count(m.renderComposer(), "\n") + 1
	if len(m.pendingImages) > 0 {
		lines++
	}
	return lines
}

func composerOffsetY(layout viewLayout) int {
	offset := layout.bodyHeight + layout.suggestionHeight + layout.recapRows
	if layout.showChrome {
		offset += 2 // header + top separator (composer dock owns the lower edge)
	}
	return offset
}

type viewLayout struct {
	bodyHeight       int
	composerHeight   int
	suggestionHeight int
	recapRows        int
	footerHeight     int
	showChrome       bool
	showModelStatus  bool
	showStatus       bool
}

func measureViewLayout(height, width, composerHeight, suggestionCount, recapRows int) viewLayout {
	height = max(1, height)
	width = max(1, width)
	footerHeight := dockFooterLines(height, width)
	layout := viewLayout{
		showChrome:      height >= 6,
		footerHeight:    footerHeight,
		showModelStatus: footerHeight >= 2,
		showStatus:      footerHeight >= 1,
	}
	fixedHeight := 0
	if layout.showChrome {
		fixedHeight += 2 // header + top separator
	}
	fixedHeight += layout.footerHeight
	layout.recapRows = min(max(0, recapRows), 1)
	fixedHeight += layout.recapRows
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

// dockFooterLines chooses how many meta rows fit under the composer dock.
// Spacious terminals get de-aggregated runtime / context / help strips.
func dockFooterLines(height, width int) int {
	switch {
	case height >= 14 && width >= 72:
		return 3
	case height >= 10:
		return 2
	case height >= 2:
		return 1
	default:
		return 0
	}
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
		if height >= 3 {
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
	visible = m.highlightTranscriptSelection(visible[:height], width)
	return strings.Join(visible[:height], "\n")
}

func (m AppModel) transcriptBounds() (left int, top int, width int, height int) {
	width = max(1, m.width)
	recapRows := 0
	if m.visibleRecapStatus(width, max(1, m.height)) != "" {
		recapRows = 1
	}
	layout := measureViewLayout(max(1, m.height), width, m.composerBlockLines(), len(m.visibleCommandSuggestions()), recapRows)
	if layout.showChrome {
		top = 2
	}
	height = layout.bodyHeight
	if width >= 104 && height >= 16 {
		width -= min(31, width/3) + 1
	}
	return 0, top, width, height
}

func (m AppModel) highlightTranscriptSelection(lines []string, width int) []string {
	selection := m.transcriptSelection
	if selection == nil {
		return lines
	}
	startX, startY, endX, endY := selection.startX, selection.startY, selection.endX, selection.endY
	if startY > endY || startY == endY && startX > endX {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	for row := max(0, startY); row <= min(len(lines)-1, endY); row++ {
		left, right := 0, width
		if row == startY {
			left = startX
		}
		if row == endY {
			right = endX + 1
		}
		if right <= left {
			continue
		}
		line := lines[row]
		selected := ansi.Strip(ansi.Cut(line, left, right))
		lines[row] = ansi.Cut(line, 0, left) + m.theme.Selected.Render(selected) + ansi.Cut(line, right, width)
	}
	return lines
}

func (m AppModel) selectedTranscriptText() string {
	selection := m.transcriptSelection
	if selection == nil {
		return ""
	}
	width, height := m.transcriptViewportSize()
	lines := strings.Split(m.renderTranscript(width, height), "\n")
	startX, startY, endX, endY := selection.startX, selection.startY, selection.endX, selection.endY
	if startY > endY || startY == endY && startX > endX {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	selected := make([]string, 0, endY-startY+1)
	for row := max(0, startY); row <= min(len(lines)-1, endY); row++ {
		line := strings.TrimRight(ansi.Strip(lines[row]), " ")
		left, right := 0, ansi.StringWidth(line)
		if row == startY {
			left = min(startX, right)
		}
		if row == endY {
			right = min(endX+1, right)
		}
		selected = append(selected, strings.TrimRight(ansi.Cut(line, left, right), " "))
	}
	return strings.TrimRight(strings.Join(selected, "\n"), "\n")
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
		animationChanged := transcriptBlockAnimated(block) && layout.animationFrame != m.animationFrame
		if !cache.initialized || !sameTranscriptBlock(layout.block, block) || layout.selected != selected || animationChanged {
			layout.block = block
			layout.selected = selected
			layout.animationFrame = m.animationFrame
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

func sameTranscriptBlock(left Block, right Block) bool {
	return left.ID == right.ID && left.Kind == right.Kind && left.RunID == right.RunID &&
		left.ToolCallID == right.ToolCallID && left.Title == right.Title && left.Arguments == right.Arguments &&
		left.Content == right.Content && left.Collapsed == right.Collapsed && left.State == right.State &&
		left.Orphaned == right.Orphaned && slices.Equal(left.Hooks, right.Hooks)
}

func transcriptBlockAnimated(block Block) bool {
	if block.Kind == BlockApproval && (block.State == "running" || block.State == "reviewing") {
		return true
	}
	for _, hook := range block.Hooks {
		if hook.State == "running" {
			return true
		}
	}
	return false
}

func (m AppModel) transcriptOffsetLimit(lineCount int, height int) int {
	contentHeight := max(1, height)
	if m.isRunning() || lineCount > contentHeight {
		footerRows := 1
		if height >= 3 {
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
		case "Compacting":
			detail = "Waiting for the compaction model"
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
		style := m.theme.Selected
		if m.status == "Reviewing approval" {
			style = style.Foreground(m.theme.ApprovalSmart.GetForeground())
		}
		return style.Render(padOrTrim(text, width))
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
	suggestions := m.visibleCommandSuggestions()
	recapRows := 0
	if m.visibleRecapStatus(width, height) != "" {
		recapRows = 1
	}
	layout := measureViewLayout(height, width, m.composerBlockLines(), len(suggestions), recapRows)
	bodyHeight := layout.bodyHeight
	if width >= 104 && bodyHeight >= 16 {
		width -= min(31, width/3) + 1
	}
	return width, bodyHeight
}

func (m AppModel) transcriptMaxOffset() int {
	width, height := m.transcriptViewportSize()
	lineCount := len(m.transcriptLines(max(1, width-4)))
	return m.transcriptOffsetLimit(lineCount, height)
}

func (m *AppModel) scrollTranscript(delta int) {
	m.transcriptTop = min(m.transcriptMaxOffset(), max(0, m.transcriptTop+delta))
}
