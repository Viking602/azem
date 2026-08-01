package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Viking602/azem/internal/session"
	"github.com/charmbracelet/x/ansi"
)

var commandPaletteOptions = []string{
	"settings", "login", "provider", "models", "model-routing", "skills", "reasoning", "sessions", "new", "recap", "status", "context", "agents", "background", "mcp", "cancel", "help", "quit",
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

type uiClickTarget uint8

const (
	uiClickNone uiClickTarget = iota
	uiClickBranch
	uiClickWorkspace
	uiClickStatus
	uiClickContext
	uiClickTodos
	uiClickAgents
	uiClickModel
	uiClickReasoning
	uiClickApprovalMode
)

type uiSegment struct {
	target  uiClickTarget
	content string
}

func renderUISegments(segments []uiSegment) string {
	var rendered strings.Builder
	for _, segment := range segments {
		rendered.WriteString(segment.content)
	}
	return rendered.String()
}

func hitUISegments(segments []uiSegment, x, start, visibleWidth int) uiClickTarget {
	if x < start || x >= start+visibleWidth {
		return uiClickNone
	}
	offset := x - start
	cursor := 0
	for _, segment := range segments {
		width := lipgloss.Width(segment.content)
		if offset >= cursor && offset < cursor+width {
			return segment.target
		}
		cursor += width
		if cursor >= visibleWidth {
			break
		}
	}
	return uiClickNone
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

const (
	// Todo pane: tall enough for multi-phase plans without feeling cramped.
	// Still capped so a long list cannot crowd out the transcript.
	maxTodoPaneHeight  = 18
	maxTodoPanePercent = 30
)

type todoPaneLineKind uint8

const (
	todoLineItem todoPaneLineKind = iota
	todoLinePhase
	todoLineGap
)

type todoPaneLine struct {
	kind  todoPaneLineKind
	title string
	item  session.TodoItem
	// rail is a left grouping connector for consecutive items within a phase
	// (┌ / │ / └ / space), matching the grok-build accent column pattern.
	rail string
}

func (m AppModel) todoItemCount() int {
	total := 0
	for _, phase := range m.todo.Phases {
		total += len(phase.Items)
	}
	return total
}

func todoItemDone(status session.TodoStatus) bool {
	return status == session.TodoCompleted || status == session.TodoCancelled
}

func (m AppModel) phaseVisibleItems(phase session.TodoPhase) []session.TodoItem {
	items := make([]session.TodoItem, 0, len(phase.Items))
	for _, item := range phase.Items {
		if m.todoHideCompleted && todoItemDone(item.Status) {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (m AppModel) visibleTodoItemCount() int {
	total := 0
	for _, phase := range m.todo.Phases {
		total += len(m.phaseVisibleItems(phase))
	}
	return total
}

// todoPaneLines builds the flat render list with phase headers, gaps between
// phases, and left-rail grouping for consecutive same-status item runs.
func (m AppModel) todoPaneLines() []todoPaneLine {
	lines := make([]todoPaneLine, 0, m.visibleTodoItemCount()+len(m.todo.Phases)*2)
	for _, phase := range m.todo.Phases {
		items := m.phaseVisibleItems(phase)
		if len(items) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, todoPaneLine{kind: todoLineGap})
		}
		if title := strings.TrimSpace(phase.Title); title != "" {
			lines = append(lines, todoPaneLine{kind: todoLinePhase, title: title})
		}
		for index, item := range items {
			lines = append(lines, todoPaneLine{
				kind: todoLineItem,
				item: item,
				rail: todoItemRail(items, index, m),
			})
		}
	}
	return lines
}

func todoItemRail(items []session.TodoItem, index int, m AppModel) string {
	if len(items) <= 1 {
		return " "
	}
	status := m.todoDisplayStatus(items[index])
	// Only group completed/cancelled runs — active work stays unbracketed so
	// the eye lands on the current and pending items first.
	if status != session.TodoCompleted && status != session.TodoCancelled {
		return " "
	}
	prevSame := index > 0 && m.todoDisplayStatus(items[index-1]) == status
	nextSame := index+1 < len(items) && m.todoDisplayStatus(items[index+1]) == status
	switch {
	case !prevSame && nextSame:
		return "┌"
	case prevSame && nextSame:
		return "│"
	case prevSame && !nextSame:
		return "└"
	default:
		return " "
	}
}

func (m AppModel) visibleTodoRowCount() int {
	return len(m.todoPaneLines())
}

func (m AppModel) todoPaneDesiredHeight(viewHeight int) int {
	if !m.todoExpanded {
		return 0
	}
	count := m.visibleTodoRowCount()
	if count == 0 {
		return 1
	}
	fractionCap := max(1, viewHeight*maxTodoPanePercent/100)
	return min(count, min(maxTodoPaneHeight, fractionCap))
}

func (m AppModel) todoPaneScrollLimit(viewportHeight int) int {
	return max(0, m.visibleTodoRowCount()-max(1, viewportHeight))
}

func (m *AppModel) scrollTodoPane(delta, viewportHeight int) {
	limit := m.todoPaneScrollLimit(viewportHeight)
	m.todoScroll = min(limit, max(0, m.todoScroll+delta))
}

// cachedTodoPane reuses the last rendered todo pane when nothing todo-related
// changed — pure transcript scrolling must not re-paint the whole list.
func (m AppModel) cachedTodoPane(width, height int) string {
	p := m.paint
	focusTodo := m.focus == focusTodo
	if p != nil &&
		p.todoRender != "" &&
		p.todoWidth == width &&
		p.todoHeight == height &&
		p.todoScroll == m.todoScroll &&
		p.todoHide == m.todoHideCompleted &&
		p.todoFocus == focusTodo &&
		p.todoRevision == m.todo.Revision &&
		// In-progress marks animate; only bust cache when animation is live.
		(p.todoFrame == m.animationFrame || !m.todoHasInProgress()) {
		return p.todoRender
	}
	rendered := m.renderTodoPane(width, height)
	if p != nil {
		p.todoWidth = width
		p.todoHeight = height
		p.todoScroll = m.todoScroll
		p.todoHide = m.todoHideCompleted
		p.todoFocus = focusTodo
		p.todoRevision = m.todo.Revision
		p.todoFrame = m.animationFrame
		p.todoRender = rendered
	}
	return rendered
}

func (m AppModel) todoHasInProgress() bool {
	for _, phase := range m.todo.Phases {
		for _, item := range phase.Items {
			if m.todoDisplayStatus(item) == session.TodoInProgress {
				return true
			}
		}
	}
	return false
}

func (m AppModel) renderTodoPaneItem(item session.TodoItem, rail string, width int) string {
	status := m.todoDisplayStatus(item)
	markStyle := m.theme.Assistant
	textStyle := m.theme.Assistant
	mark := "□"
	switch status {
	case session.TodoInProgress:
		mark = "▶"
		markStyle = m.theme.Warning
		textStyle = textStyle.Bold(true)
	case session.TodoCompleted:
		mark = "✓"
		markStyle = m.theme.Success
		textStyle = m.theme.Muted
	case session.TodoCancelled:
		mark = "×"
		markStyle = m.theme.Error
		textStyle = m.theme.Muted.Strikethrough(true)
	}
	if rail == "" {
		rail = " "
	}
	prefix := m.theme.Muted.Render(rail) + " " + markStyle.Render(mark) + " "
	return truncateStyledFallback(prefix+textStyle.Render(item.Content), max(0, width))
}

func (m AppModel) renderTodoPanePhase(title string, width int) string {
	// Accent bar + phase title, matching the grok-build accent column and the
	// phase headers users expect for multi-step plans.
	accent := m.theme.Header.Render("▌")
	label := m.theme.Header.Render(" " + title)
	return truncateStyledFallback(accent+label, max(0, width))
}

func (m AppModel) renderTodoPane(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if width < 4 {
		return fitViewport(m.tr("overlay.todos.empty"), width, height)
	}

	contentWidth := width - 4
	contentRows := make([]string, 0, height)
	lines := m.todoPaneLines()
	visibleCount := len(lines)
	offset := min(m.todoPaneScrollLimit(height), max(0, m.todoScroll))
	if visibleCount == 0 {
		empty := "todo.all_done"
		if m.todoItemCount() == 0 {
			empty = "overlay.todos.empty"
		}
		contentRows = append(contentRows, m.theme.Muted.Render(m.tr(empty)))
	} else {
		for index := offset; index < visibleCount && len(contentRows) < height; index++ {
			line := lines[index]
			switch line.kind {
			case todoLinePhase:
				contentRows = append(contentRows, m.renderTodoPanePhase(line.title, contentWidth))
			case todoLineGap:
				contentRows = append(contentRows, "")
			default:
				contentRows = append(contentRows, m.renderTodoPaneItem(line.item, line.rail, contentWidth))
			}
		}
	}
	for len(contentRows) < height {
		contentRows = append(contentRows, "")
	}

	if visibleCount > height {
		thumbStart, thumbSize := transcriptScrollbarThumb(
			height,
			visibleCount,
			m.todoPaneScrollLimit(height),
			m.todoPaneScrollLimit(height)-offset,
		)
		column := max(0, contentWidth-1)
		for row := range height {
			contentRows[row] = ansi.Cut(padStyledLine(contentRows[row], contentWidth), 0, column) +
				m.renderScrollbarCell(row, thumbStart, thumbSize)
		}
	}

	borderStyle := m.theme.Border
	if m.focus == focusTodo {
		borderStyle = m.theme.Header
	}
	rows := make([]string, height)
	for row := range height {
		left, right := "│", "│"
		if row == 0 {
			left, right = "┌", "┐"
			if m.focus == focusTodo {
				right = "×"
			}
		}
		if row == height-1 && height > 1 {
			left, right = "└", "┘"
		}
		rows[row] = borderStyle.Render(left) + " " +
			padStyledLine(contentRows[row], contentWidth) + " " +
			borderStyle.Render(right)
	}
	return strings.Join(rows, "\n")
}

func (m AppModel) todoPaneBounds() (left, top, width, height int) {
	width = max(1, m.width)
	recapRows := 0
	if m.visibleRecapStatus(width, max(1, m.height)) != "" {
		recapRows = 1
	}
	layout := measureViewLayout(
		max(1, m.height),
		width,
		m.composerBlockLines(),
		len(m.visibleCommandSuggestions()),
		recapRows,
		m.todoPaneDesiredHeight(max(1, m.height)),
	)
	if layout.showChrome {
		top = 2
	}
	return 0, top, width, layout.todoHeight
}

func (m AppModel) View() tea.View {
	width := max(1, m.width)
	height := max(1, m.height)
	if m.overlay != OverlayNone {
		view := tea.NewView(m.renderOverlayView(width, height))
		if m.overlay == OverlayModel {
			if cursor := m.modelSearch.Cursor(); cursor != nil {
				if offsetX, offsetY, visible := m.modelSearchCursorOffset(width, height); visible {
					cursor.Position.X += offsetX
					cursor.Position.Y += offsetY
					view.Cursor = cursor
				}
			}
		} else if m.overlay == OverlaySettings {
			if cursor := m.settingsSearch.Cursor(); cursor != nil {
				if offsetX, offsetY, visible := m.settingsSearchCursorOffset(width, height); visible {
					cursor.Position.X += offsetX
					cursor.Position.Y += offsetY
					view.Cursor = cursor
				}
			}
		}
		view.AltScreen = true
		view.ReportFocus = true
		view.MouseMode = tea.MouseModeCellMotion
		view.WindowTitle = "Azem"
		return view
	}
	header := m.renderHeader(width)
	composer := m.cachedComposer(width)
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
	layout := measureViewLayout(height, width, composerLines, len(suggestions), recapRows, m.todoPaneDesiredHeight(height))
	// Sticky instruction chip (grok-build style): pin the current/scrolled-past
	// user prompt above the transcript so the active instruction stays visible.
	sticky := m.applyStickyLayout(width, &layout)
	// Publish scroll metrics for O(1) wheel clamping (pointer so value-receiver View persists).
	if p := m.paint; p != nil {
		p.width = width
		p.height = height
		p.bodyHeight = layout.bodyHeight
		p.contentWidth = transcriptContentWidth(width, layout.bodyHeight)
		// Warm line count once per frame; subsequent scroll events reuse it.
		p.lineCount = len(m.transcriptLines(p.contentWidth))
		if layout.stickyHeight == 0 {
			p.preBodyHeight = layout.bodyHeight
		}
	}
	sections := make([]string, 0, 12)
	if layout.showChrome {
		// Match the quiet Grok-style top chrome: one metadata row, then air.
		sections = append(sections, header, padStyledLine("", width))
	}
	if layout.todoHeight > 0 {
		sections = append(sections, m.cachedTodoPane(width, layout.todoHeight))
		if layout.todoGap > 0 {
			sections = append(sections, padStyledLine("", width))
		}
	}
	if layout.stickyHeight > 0 {
		sections = append(sections, sticky, padStyledLine("", width))
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
	view.MouseMode = tea.MouseModeAllMotion
	view.WindowTitle = "Azem"
	return view
}

func (m AppModel) renderOverlayView(width, height int) string {
	overlay := fitViewport(m.renderOverlay(width, height), width, height)
	x, y, boxWidth, boxHeight, fullscreen := m.overlayLayerBounds(width, height)
	if fullscreen {
		return overlay
	}
	base := m
	base.overlay = OverlayNone
	base.overlayPurpose = ""
	background := fitViewport(base.View().Content, width, height)
	box := styledViewportRect(overlay, x, y, boxWidth, boxHeight)
	return fitViewport(lipgloss.NewCompositor(
		lipgloss.NewLayer(background).Z(0),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render(), width, height)
}

func (m AppModel) overlayLayerBounds(width, height int) (x, y, boxWidth, boxHeight int, fullscreen bool) {
	if overlayUsesFullScreen(m.overlay, width, height) {
		return 0, 0, width, height, true
	}
	switch m.overlay {
	case OverlayMCPDetail:
		boxWidth = min(96, max(3, width-2))
		boxHeight = max(1, min(height-2, 28)) + 2
	case OverlayBackgroundDetail:
		boxWidth = min(118, max(3, width-2))
		boxHeight = max(1, min(height-2, 32)) + 2
	default:
		frame := m.genericOverlayFrame(width, height)
		return frame.leftPadding, frame.topPadding, frame.boxWidth, frame.innerHeight + 2, false
	}
	return max(0, (width-boxWidth)/2), max(0, (height-boxHeight)/2), boxWidth, boxHeight, false
}

func overlayUsesFullScreen(overlay Overlay, width, height int) bool {
	return width < 6 || height < 5 || overlay == OverlayAgentDetail
}

func styledViewportRect(viewport string, x, y, width, height int) string {
	lines := strings.Split(viewport, "\n")
	rows := make([]string, height)
	for row := range rows {
		line := ""
		if index := y + row; index >= 0 && index < len(lines) {
			line = ansi.Cut(lines[index], x, x+width)
		}
		rows[row] = padStyledLine(line, width)
	}
	return strings.Join(rows, "\n")
}

func (m AppModel) modelSearchCursorOffset(width, height int) (int, int, bool) {
	if width < 6 || height < 5 {
		return 0, 0, false
	}
	boxWidth := min(82, max(3, width-2))
	innerHeight := max(1, min(height-2, 20))
	_, subtitle := m.overlayHeading()
	searchRow := 1
	if subtitle != "" {
		searchRow++
	}
	if searchRow >= innerHeight-1 {
		return 0, 0, false
	}
	leftPadding := max(0, (width-boxWidth)/2)
	topPadding := max(0, (height-(innerHeight+2))/2)
	return leftPadding + 2, topPadding + 1 + searchRow, true
}

func (m AppModel) settingsSearchCursorOffset(width, height int) (int, int, bool) {
	if width < 6 || height < 5 {
		return 0, 0, false
	}
	frame := m.genericOverlayFrame(width, height)
	return frame.leftPadding + 2, frame.topPadding + 1, true
}

// cachedComposer reuses the docked input panel across pure scroll frames.
// Typing / focus / model / mode changes bust the cache.
func (m AppModel) cachedComposer(width int) string {
	p := m.paint
	value := m.composer.Value()
	height := m.composer.Height()
	focused := m.composer.Focused()
	mode := m.approvalModeLabel()
	if m.planMode {
		mode += "\x00plan"
	}
	if m.composerCacheMatches(p, width, value, height, focused, mode) {
		return p.composerRender
	}
	rendered := m.renderComposer()
	if p != nil {
		p.composerWidth = width
		p.composerValue = value
		p.composerHeight = height
		p.composerFocused = focused
		p.composerModel = m.model
		p.composerReason = m.reasoning
		p.composerMode = mode
		p.composerRender = rendered
	}
	return rendered
}

func (m AppModel) composerCacheMatches(p *paintCache, width int, value string, height int, focused bool, mode string) bool {
	return p != nil && p.composerRender != "" && p.composerWidth == width &&
		p.composerValue == value && p.composerHeight == height && p.composerFocused == focused &&
		p.composerModel == m.model && p.composerReason == m.reasoning && p.composerMode == mode
}

func (m AppModel) renderComposer() string {
	width := max(1, m.width)
	if width < 4 {
		return fitViewport(m.composer.View(), width, max(1, strings.Count(m.composer.View(), "\n")+1))
	}

	borderStyle := m.theme.Border
	if m.composer.Focused() {
		borderStyle = m.theme.Header
	}
	contentWidth := width - 4
	content := strings.Split(m.composer.View(), "\n")
	rows := make([]string, 0, len(content)+2)
	rows = append(rows, borderStyle.Render("╭"+strings.Repeat("─", width-2)+"╮"))
	for _, line := range content {
		rows = append(rows,
			borderStyle.Render("│ ")+padStyledLine(line, contentWidth)+borderStyle.Render(" │"),
		)
	}

	interiorWidth := width - 2
	caption := m.renderComposerCaption(max(0, interiorWidth-2))
	captionWidth := lipgloss.Width(caption)
	ruleWidth := max(0, interiorWidth-captionWidth)
	rows = append(rows,
		borderStyle.Render("╰"+strings.Repeat("─", ruleWidth))+caption+borderStyle.Render("╯"),
	)
	return strings.Join(rows, "\n")
}

func (m AppModel) renderComposerCaption(width int) string {
	if width <= 0 {
		return ""
	}
	return truncateStyledFallback(renderUISegments(m.composerCaptionSegments()), width)
}

func (m AppModel) composerCaptionSegments() []uiSegment {
	segments := []uiSegment{
		{content: " "},
		{target: uiClickModel, content: m.theme.MetaValue.Render(first(m.model, m.tr("value.no_model")))},
	}
	if m.reasoning != "" {
		segments = append(segments, uiSegment{
			target:  uiClickReasoning,
			content: m.theme.MetaValue.Render(" (" + m.reasoning + ")"),
		})
	}
	if m.planMode {
		segments = append(segments,
			uiSegment{content: m.theme.MetaDivider.Render(" · ")},
			uiSegment{content: m.theme.Warning.Bold(true).Render(m.tr("mode.plan"))},
		)
	}
	return append(segments,
		uiSegment{content: m.theme.MetaDivider.Render(" · ")},
		uiSegment{target: uiClickApprovalMode, content: m.theme.Muted.Render(m.approvalModeLabel())},
		uiSegment{content: " "},
	)
}

func (m AppModel) composerCaptionClickTarget(x, y int) uiClickTarget {
	if m.width < 4 {
		return uiClickNone
	}
	_, top, width, height := m.composerBounds()
	if height <= 0 || y != top+height-1 {
		return uiClickNone
	}
	maxCaptionWidth := max(0, width-4)
	caption := m.renderComposerCaption(maxCaptionWidth)
	captionWidth := lipgloss.Width(caption)
	start := width - 1 - captionWidth
	return hitUISegments(m.composerCaptionSegments(), x, start, captionWidth)
}

func (m AppModel) composerBlockLines() int {
	// Avoid renderComposer() on the hot scroll/layout path. The docked panel is
	// always: top border + textarea rows + bottom border (+ optional attach row).
	lines := max(1, m.composer.Height()) + 2
	if len(m.pendingImages) > 0 {
		lines++
	}
	return lines
}

func composerOffsetY(layout viewLayout) int {
	offset := layout.todoHeight + layout.todoGap + layout.stickyHeight + layout.bodyHeight + layout.suggestionHeight + layout.recapRows
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
	todoHeight       int
	todoGap          int
	stickyHeight     int // pinned instruction card + trailing gap
	footerHeight     int
	showChrome       bool
	showModelStatus  bool
	showStatus       bool
}

func measureViewLayout(height, width, composerHeight, suggestionCount, recapRows, todoHeight int) viewLayout {
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
	availableForTodo := max(1, height-fixedHeight)
	if todoHeight > 0 && availableForTodo >= 4 {
		layout.todoHeight = min(todoHeight, availableForTodo-3)
		layout.todoGap = 1
		fixedHeight += layout.todoHeight + layout.todoGap
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

// dockFooterLines keeps the bottom chrome to one discoverability row. Runtime
// activity, model, mode, and context live closer to the content they describe.
func dockFooterLines(height, width int) int {
	if height < 2 || width <= 0 {
		return 0
	}
	return 1
}

func (m AppModel) renderHeader(width int) string {
	left := m.renderHeaderLeft(width)
	right := renderUISegments(m.headerRightSegments())
	return renderSurface(m.theme.Chrome, joinSides(left, right, width))
}

func (m AppModel) headerLeftSegments(width int) []uiSegment {
	segments := []uiSegment{{content: m.theme.Header.Render("⌁")}}
	if branch := strings.TrimSpace(m.branch); branch != "" {
		segments = append(segments, uiSegment{content: " "}, uiSegment{target: uiClickBranch, content: m.theme.MetaValue.Render("⎇ " + branch)})
	}
	if width >= 32 {
		segments = append(segments, uiSegment{content: "  "}, uiSegment{target: uiClickWorkspace, content: m.theme.Muted.Render(shortenPath(m.workspace, max(12, width/2)))})
	}
	return segments
}

func (m AppModel) renderHeaderLeft(width int) string {
	return renderUISegments(m.headerLeftSegments(width))
}

func (m AppModel) headerRightSegments() []uiSegment {
	segments := make([]uiSegment, 0, 7)
	appendSegment := func(target uiClickTarget, content string) {
		if len(segments) > 0 {
			segments = append(segments, uiSegment{content: m.theme.MetaDivider.Render(" │ ")})
		}
		segments = append(segments, uiSegment{target: target, content: content})
	}
	appendSegment(uiClickAgents, m.renderHeaderAgents())
	if m.status != "" && m.status != "Ready" && !m.isRunning() {
		appendSegment(uiClickStatus, m.stateStyle(m.status).Render(
			stateMark(m.status)+" "+m.displayState(m.status),
		))
	}
	metrics := m.contextMetrics()
	if metrics.limit > 0 {
		appendSegment(uiClickContext, m.contextTone(metrics.percentage).Render(
			formatTokens(metrics.used)+" / "+formatTokens(metrics.limit),
		))
	}
	if completed, total := todoProgress(m.todo); total > 0 {
		appendSegment(uiClickTodos, m.theme.Muted.Render(fmt.Sprintf("%d/%d ✓", completed, total)))
	}
	return segments
}
func (m AppModel) renderHeaderAgents() string {
	mark := "○"
	style := m.theme.Muted
	active := m.activeAgents()
	switch {
	case m.hasRunningAgents():
		mark = m.agentStateMark("running")
		style = m.theme.RailAgents
	case len(active) > 0:
		mark = stateMark(active[0].State)
		style = m.theme.RailAgents
	case len(m.agents) > 0:
		mark = "✓"
	}
	return style.Render(fmt.Sprintf("%s %s %d", mark, m.tr("rail.agents"), len(m.agents)))
}

func (m AppModel) headerClickTarget(x, y int) uiClickTarget {
	width := max(1, m.width)
	if y != 0 || m.height < 6 || x < 0 || x >= width {
		return uiClickNone
	}
	segments := m.headerRightSegments()
	right := truncateStyledFallback(renderUISegments(segments), width)
	rightWidth := lipgloss.Width(right)
	rightStart := width - rightWidth
	if target := hitUISegments(segments, x, rightStart, rightWidth); target != uiClickNone {
		return target
	}

	gap := 0
	if rightWidth > 0 && width-rightWidth >= 2 {
		gap = 2
	}
	leftSegments := m.headerLeftSegments(width)
	left := truncateStyledFallback(renderUISegments(leftSegments), max(0, width-rightWidth-gap))
	return hitUISegments(leftSegments, x, 0, lipgloss.Width(left))
}

func (m AppModel) renderBody(width int, height int) string {
	transcriptWidth := bodyTranscriptWidth(width, height)
	transcript := m.renderTranscript(transcriptWidth, height)
	if transcriptWidth == width {
		return transcript
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, transcript, m.renderTranscriptScrollbar(height, transcriptWidth))
}

func bodyTranscriptWidth(width, height int) int {
	width = max(1, width)
	if width > 1 {
		return width - 1
	}
	return width
}

func transcriptContentWidth(width, height int) int {
	return max(1, bodyTranscriptWidth(width, height)-4)
}

func (m AppModel) renderTranscriptScrollbar(height, transcriptWidth int) string {
	height = max(1, height)
	lineCount := len(m.transcriptLines(max(1, transcriptWidth-4)))
	maxOffset := m.transcriptOffsetLimit(lineCount, height)
	if maxOffset == 0 {
		return strings.Repeat(" \n", height-1) + " "
	}
	offset := min(maxOffset, max(0, m.transcriptTop))
	thumbStart, thumbSize := transcriptScrollbarThumb(height, lineCount, maxOffset, offset)
	rows := make([]string, height)
	for row := range rows {
		rows[row] = m.renderScrollbarCell(row, thumbStart, thumbSize)
	}
	return strings.Join(rows, "\n")
}

const scrollbarSubcells = 8

var scrollbarLowerBlocks = [...]string{"", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

func transcriptScrollbarThumb(trackHeight, lineCount, maxOffset, offset int) (int, int) {
	if trackHeight <= 0 || lineCount <= 0 || maxOffset <= 0 {
		return 0, 0
	}
	// Block elements provide eight vertical positions per terminal row. This
	// keeps proportional thumb movement responsive without relying on terminal-
	// specific pixel graphics.
	trackSize := trackHeight * scrollbarSubcells
	contentHeight := max(1, lineCount-maxOffset)
	thumbSize := min(trackSize, max(scrollbarSubcells, (trackSize*contentHeight+lineCount-1)/lineCount))
	travel := trackSize - thumbSize
	if travel <= 0 {
		return 0, thumbSize
	}
	offset = min(maxOffset, max(0, offset))
	scrollFromOldest := maxOffset - offset
	thumbStart := (scrollFromOldest*travel + maxOffset/2) / maxOffset
	return min(travel, max(0, thumbStart)), thumbSize
}

func scrollbarThumbGlyph(row, thumbStart, thumbSize int) (glyph string, reverse bool) {
	if thumbSize <= 0 {
		return "│", false
	}
	cellStart := row * scrollbarSubcells
	cellEnd := cellStart + scrollbarSubcells
	fillStart := max(cellStart, thumbStart)
	fillEnd := min(cellEnd, thumbStart+thumbSize)
	if fillStart >= fillEnd {
		return "│", false
	}
	if fillStart == cellStart && fillEnd == cellEnd {
		return "█", false
	}
	filled := fillEnd - fillStart
	if fillEnd == cellEnd {
		return scrollbarLowerBlocks[filled], false
	}
	// Reverse a lower block to synthesize the corresponding upper fractional
	// block without relying on rarely-supported legacy Unicode glyphs.
	return scrollbarLowerBlocks[scrollbarSubcells-filled], true
}

func (m AppModel) renderScrollbarCell(row, thumbStart, thumbSize int) string {
	glyph, reverse := scrollbarThumbGlyph(row, thumbStart, thumbSize)
	if glyph == "│" {
		return m.theme.ScrollTrack.Render(glyph)
	}
	style := m.theme.ScrollThumb
	if reverse {
		style = style.Reverse(true)
	}
	return style.Render(glyph)
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
	layout := measureViewLayout(max(1, m.height), width, m.composerBlockLines(), len(m.visibleCommandSuggestions()), recapRows, m.todoPaneDesiredHeight(max(1, m.height)))
	// Prefer cached sticky decision when marks are warm — no full content lookup.
	if m.stickyVisibleWithCache(layout.bodyHeight) && layout.bodyHeight > stickySlotRows+1 {
		layout.stickyHeight = stickySlotRows
		layout.bodyHeight -= stickySlotRows
	} else if m.stickyInstructionHeight(width, layout.bodyHeight) > 0 && layout.bodyHeight > stickySlotRows+1 {
		layout.stickyHeight = stickySlotRows
		layout.bodyHeight -= stickySlotRows
	}
	if layout.showChrome {
		top = 2
	}
	top += layout.todoHeight + layout.todoGap + layout.stickyHeight
	// Prefer body height published by the last View frame when geometry matches.
	if p := m.paint; p != nil && p.bodyHeight > 0 && p.width == width && p.height == max(1, m.height) {
		layout.bodyHeight = p.bodyHeight
	}
	height = layout.bodyHeight
	width = bodyTranscriptWidth(width, height)
	return 0, top, width, height
}

func (m AppModel) composerBounds() (left int, top int, width int, height int) {
	width = max(1, m.width)
	recapRows := 0
	if m.visibleRecapStatus(width, max(1, m.height)) != "" {
		recapRows = 1
	}
	attachmentRows := 0
	if len(m.pendingImages) > 0 {
		attachmentRows = 1
	}
	layout := measureViewLayout(max(1, m.height), width, m.composerBlockLines(), len(m.visibleCommandSuggestions()), recapRows, m.todoPaneDesiredHeight(max(1, m.height)))
	top = composerOffsetY(layout) + attachmentRows
	height = max(1, layout.composerHeight-attachmentRows)
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
		cache.userMarks = nil
	}

	dirty := !cache.initialized || len(cache.blocks) != len(m.transcript)
	if len(cache.blocks) != len(m.transcript) {
		blocks := make([]transcriptBlockLayout, len(m.transcript))
		copy(blocks, cache.blocks)
		cache.blocks = blocks
		dirty = true
	}
	lineCount := 0
	for index, block := range m.transcript {
		selected := m.focus == focusTranscript && m.transcriptCursor == index
		hovered := block.Kind == BlockTool && m.transcriptHover == index
		layout := &cache.blocks[index]
		animationChanged := (index == m.activeThinkingBlockIndex() || transcriptBlockAnimated(block)) && layout.animationFrame != m.animationFrame
		if !cache.initialized || !sameTranscriptBlock(layout.block, block) || layout.selected != selected || layout.hovered != hovered || animationChanged {
			layout.block = block
			layout.selected = selected
			layout.hovered = hovered
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
	marks := make([]stickyUserMark, 0, 4)
	if len(cache.blocks) == 0 {
		lines = append(lines,
			m.theme.Muted.Render("  "+m.tr("empty.title")),
			m.theme.Assistant.Render("  "+m.tr("empty.body")),
			m.theme.Muted.Render("  "+m.tr("empty.help")),
		)
	}
	linePos := 0
	for index, block := range cache.blocks {
		if index > 0 && transcriptBlocksNeedSeparator(cache.blocks[index-1], block) {
			lines = append(lines, "")
			linePos++
		}
		if block.block.Kind == BlockUser {
			marks = append(marks, stickyUserMark{
				content: block.block.Content,
				endLine: linePos + len(block.lines),
				runID:   block.block.RunID,
			})
		}
		lines = append(lines, block.lines...)
		linePos += len(block.lines)
	}
	cache.lines = lines
	cache.userMarks = marks
	cache.initialized = true
	return cache.lines
}

func compactToolTranscriptBlock(block transcriptBlockLayout) bool {
	return len(block.lines) == 1 && (block.block.Kind == BlockTool || block.block.Kind == BlockDiff)
}

func transcriptBlocksNeedSeparator(previous, current transcriptBlockLayout) bool {
	if len(previous.lines) == 0 || previous.lines[len(previous.lines)-1] == "" {
		return false
	}
	if compactToolTranscriptBlock(previous) &&
		previous.block.RunID == current.block.RunID &&
		current.block.Kind != BlockUser {
		return false
	}
	return true
}

func (m *AppModel) positionTranscriptAfterToggle(index int, expanded bool) {
	_, _, width, height := m.transcriptBounds()
	lines := m.transcriptLines(max(1, width-4))
	maxOffset := m.transcriptOffsetLimit(len(lines), height)
	if !expanded {
		m.transcriptTop = min(m.transcriptTop, maxOffset)
		return
	}
	blocks := m.transcriptLayout.blocks
	if index < 0 || index >= len(blocks) {
		m.transcriptTop = min(m.transcriptTop, maxOffset)
		return
	}
	blockStart := 0
	for current := 0; current < index; current++ {
		blockStart += len(blocks[current].lines)
		if transcriptBlocksNeedSeparator(blocks[current], blocks[current+1]) {
			blockStart++
		}
	}
	targetTop := max(0, maxOffset-blockStart)
	for range 3 {
		adjusted := max(0, m.transcriptMaxOffsetForTop(targetTop)-blockStart)
		if adjusted == targetTop {
			break
		}
		targetTop = adjusted
	}
	m.transcriptTop = targetTop
}

// transcriptBlockHeaderAt maps a visible transcript row back to a block header.
// It mirrors renderTranscript's viewport and separator calculations.
func (m AppModel) transcriptBlockHeaderAt(row, width, height int) (int, bool) {
	contentWidth := max(1, width-4)
	lines := m.transcriptLines(contentWidth)
	maxOffset := m.transcriptOffsetLimit(len(lines), height)
	offset := min(maxOffset, max(0, m.transcriptTop))
	contentHeight := height
	if m.isRunning() || maxOffset > 0 {
		footerRows := 1
		if height >= 3 {
			footerRows++
		}
		contentHeight = max(1, height-footerRows)
	}
	if row < 0 || row >= contentHeight {
		return 0, false
	}
	end := len(lines) - offset
	start := max(0, end-contentHeight)
	line := start + row
	cursor := 0
	for index, layout := range m.transcriptLayout.blocks {
		if index > 0 && transcriptBlocksNeedSeparator(m.transcriptLayout.blocks[index-1], layout) {
			cursor++
		}
		if line == cursor && len(layout.lines) > 0 {
			return index, true
		}
		cursor += len(layout.lines)
	}
	return 0, false
}

func sameTranscriptBlock(left Block, right Block) bool {
	return left.ID == right.ID && left.Kind == right.Kind && left.RunID == right.RunID &&
		left.ToolCallID == right.ToolCallID && left.Title == right.Title && left.Arguments == right.Arguments &&
		left.Content == right.Content && left.Collapsed == right.Collapsed && left.State == right.State &&
		left.Orphaned == right.Orphaned && slices.Equal(left.Hooks, right.Hooks)
}

func transcriptBlockAnimated(block Block) bool {
	if (block.Kind == BlockTool || block.Kind == BlockDiff) && strings.EqualFold(block.State, "running") {
		return true
	}
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
		indicator := "◆"
		if !m.reducedMotion {
			frames := [...]string{"◇", "◈", "◆", "◈"}
			indicator = frames[m.animationFrame%len(frames)]
		}
		text := "  " + indicator + " " + label
		if m.status == "Running" {
			elapsed := "0s"
			if !m.runStartedAt.IsZero() {
				elapsed = formatActivityDuration(time.Since(m.runStartedAt))
			}
			text += "  " + elapsed
		}
		style := m.theme.Selected
		if m.status == "Reviewing approval" {
			style = style.Foreground(m.theme.ApprovalSmart.GetForeground())
		}
		return style.Render(padOrTrim(text, width))
	}
	if offset > 0 {
		return m.theme.Muted.Render(padOrTrim(
			"  "+m.tr("status.history_from_latest", map[string]string{"count": fmt.Sprint(offset)}),
			width,
		))
	}
	return m.theme.Muted.Render(padOrTrim(
		"  "+m.tr("status.history_all", map[string]string{"count": fmt.Sprint(maxOffset)}),
		width,
	))
}

func (m AppModel) runActivitySummary(now time.Time) string {
	activity := m.tr("status.detail.generating")
	switch m.runActivity {
	case "waiting_model":
		activity = m.tr("status.activity.waiting_model")
	case "thinking":
		activity = m.tr("status.activity.thinking")
	case "responding":
		activity = m.tr("status.activity.responding")
	case "retrying":
		activity = m.runActivityDetail
	case "tool":
		activity = m.tr("status.activity.tool", map[string]string{"name": first(m.runActivityDetail, m.tr("block.tool"))})
	case "waiting_after_tool":
		activity = m.tr("status.activity.waiting_after_tool", map[string]string{"name": first(m.runActivityDetail, m.tr("block.tool"))})
	case "hook":
		activity = m.tr("status.activity.hook", map[string]string{"name": first(m.runActivityDetail, m.tr("block.hook"))})
	case "approval":
		activity = m.tr("status.detail.approval")
	case "agents":
		activity = m.tr("status.activity.agents", map[string]string{"count": fmt.Sprint(len(m.activeAgents()))})
	}
	parts := []string{activity}
	if !m.runStartedAt.IsZero() {
		parts = append(parts, m.tr("status.activity.elapsed", map[string]string{"duration": formatActivityDuration(now.Sub(m.runStartedAt))}))
	}
	if m.status != "Awaiting approval" && m.status != "Reviewing approval" && m.status != "Cancelling" && !m.runActivityAt.IsZero() {
		idle := now.Sub(m.runActivityAt)
		if idle >= 5*time.Second {
			parts = append(parts, m.tr("status.activity.idle", map[string]string{"duration": formatActivityDuration(idle)}))
		}
	}
	return strings.Join(parts, "  · ")
}

func formatActivityDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	seconds := int(duration.Round(time.Second) / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	return fmt.Sprintf("%dh%02dm%02ds", minutes/60, minutes%60, seconds)
}

func (m AppModel) transcriptViewportSize() (int, int) {
	width := max(1, m.width)
	height := max(1, m.height)
	if p := m.paint; p != nil && p.bodyHeight > 0 && p.width == width && p.height == height {
		return bodyTranscriptWidth(width, p.bodyHeight), p.bodyHeight
	}
	suggestions := m.visibleCommandSuggestions()
	recapRows := 0
	if m.visibleRecapStatus(width, height) != "" {
		recapRows = 1
	}
	layout := measureViewLayout(height, width, m.composerBlockLines(), len(suggestions), recapRows, m.todoPaneDesiredHeight(height))
	if m.stickyInstructionHeight(width, layout.bodyHeight) > 0 && layout.bodyHeight > stickySlotRows+1 {
		layout.stickyHeight = stickySlotRows
		layout.bodyHeight -= stickySlotRows
	}
	return bodyTranscriptWidth(width, layout.bodyHeight), layout.bodyHeight
}

func (m AppModel) transcriptMaxOffset() int {
	// Hot path: use metrics last published by View via paint cache.
	if p := m.paint; p != nil && p.bodyHeight > 0 && p.lineCount >= 0 &&
		p.width == max(1, m.width) && p.height == max(1, m.height) {
		return m.transcriptOffsetLimit(p.lineCount, p.bodyHeight)
	}
	width, height := m.transcriptViewportSize()
	lineCount := len(m.transcriptLines(max(1, width-4)))
	return m.transcriptOffsetLimit(lineCount, height)
}

func (m *AppModel) scrollTranscript(delta int) {
	// Clamp against the layout implied by the destination offset. The cached
	// paint height describes the previous frame; when scrolling crosses a sticky
	// prompt boundary that height changes by stickySlotRows. Reusing it for one
	// more event can clamp the offset back across the boundary, making repeated
	// wheel-up events alternate between the sticky and non-sticky layouts.
	next := max(0, m.transcriptTop+delta)
	maxOffset := m.transcriptMaxOffsetForTop(next)
	clamped := min(maxOffset, next)
	if delta > 0 {
		clamped = max(m.transcriptTop, clamped)
	}
	m.transcriptTop = clamped
}

func (m AppModel) transcriptMaxOffsetForTop(top int) int {
	width := max(1, m.width)
	height := max(1, m.height)
	suggestions := m.visibleCommandSuggestions()
	recapRows := 0
	if m.visibleRecapStatus(width, height) != "" {
		recapRows = 1
	}
	layout := measureViewLayout(height, width, m.composerBlockLines(), len(suggestions), recapRows, m.todoPaneDesiredHeight(height))
	contentWidth := transcriptContentWidth(width, layout.bodyHeight)
	lines := m.transcriptLines(contentWidth)
	if m.stickyVisibleAtTop(layout.bodyHeight, top, lines, m.transcriptLayout.userMarks) {
		layout.bodyHeight -= stickySlotRows
	}
	return max(m.transcriptMaxOffset(), m.transcriptOffsetLimit(len(lines), max(1, layout.bodyHeight)))
}

// stickyVisibleWithCache decides sticky visibility using cached userMarks and
// paint metrics — no composer render, no full layout measure.
func (m AppModel) stickyVisibleWithCache(preBodyHeight int) bool {
	if preBodyHeight < stickySlotRows+2 {
		return false
	}
	cache := m.transcriptLayout
	if cache == nil || !cache.initialized || len(cache.userMarks) == 0 {
		return false
	}
	return m.stickyVisibleAtTop(preBodyHeight, m.transcriptTop, cache.lines, cache.userMarks)
}

func (m AppModel) stickyVisibleAtTop(preBodyHeight, top int, lines []string, marks []stickyUserMark) bool {
	totalLines := len(lines)
	if totalLines == 0 {
		return false
	}
	contentHeight := max(1, preBodyHeight-stickySlotRows)
	if m.isRunning() || m.transcriptOffsetLimit(totalLines, contentHeight) > 0 {
		footerGap := 0
		if contentHeight >= 3 {
			footerGap = 1
		}
		contentHeight = max(1, contentHeight-1-footerGap)
	}
	offset := min(m.transcriptOffsetLimit(totalLines, max(1, preBodyHeight-stickySlotRows)), max(0, top))
	end := totalLines - offset
	visibleStart := max(0, end-contentHeight)
	runID := m.runID
	running := m.isRunning()
	for index := len(marks) - 1; index >= 0; index-- {
		mark := marks[index]
		if mark.endLine > visibleStart {
			continue
		}
		if running && runID != "" && mark.runID != "" && mark.runID != runID {
			continue
		}
		if strings.TrimSpace(mark.content) == "" {
			continue
		}
		return true
	}
	return false
}
