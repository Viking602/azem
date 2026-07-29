package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/i18n"
)

func (m AppModel) renderBlock(block Block, index int, width int) []string {
	rendersDiff := block.Kind == BlockDiff || block.Kind == BlockTool && block.Title == "coding.git_diff"
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
		if block.State == "running" || block.State == "reviewing" {
			indicator = "◆"
			if !m.reducedMotion {
				frames := [...]string{"◇", "◈", "◆", "◈"}
				indicator = frames[m.animationFrame%len(frames)]
			}
		}
		header := m.renderBlockHeader(selector, indicator, m.tr("block.approval"), title, block.State, state, width, m.theme.ApprovalTag, m.theme.ApprovalAsk, selected)
		lines := []string{header}
		if block.Collapsed {
			return lines
		}
		return append(lines, m.renderApprovalContent(block.Content, width)...)
	case BlockHook:
		return m.renderHookPrompt(block, selected, width)
	case BlockTool:
		header := m.renderToolHeader(block, selector, width, selected)
		lines := []string{header}
		if block.Collapsed {
			return lines
		}
		if rendersDiff {
			return append(lines, m.renderDiffContent(block.Content, width+2)...)
		}
		return append(lines, m.renderToolContent(block, width)...)
	case BlockAgent, BlockDiff, BlockError:
		toggle := "▾"
		if block.Collapsed {
			toggle = "▸"
		}
		kind := m.tr("block." + string(block.Kind))
		tagStyle, accentStyle := m.blockKindStyles(block, rendersDiff)
		header := m.renderBlockHeader(selector, toggle, kind, title, block.State, state, width, tagStyle, accentStyle, selected)
		lines := []string{header}
		if block.Collapsed {
			return lines
		}
		if rendersDiff {
			return append(lines, m.renderDiffContent(block.Content, width+2)...)
		}
		return append(lines, m.renderBlockContent(block.Content, width, accentStyle)...)
	case BlockUser:
		return m.renderUserMessage(block.Content, width)
	case BlockThinking:
		return m.renderThinkingMessage(block, width)
	case BlockAssistant:
		if block.State == "streaming" && !strings.Contains(block.Content, "\n") {
			return renderProseBlock(m.theme.Assistant, "", block.Content, width)
		}
		return renderMarkdownBlock(m.theme.Assistant, "", block.Content, width)
	default:
		return renderProseBlock(m.theme.Assistant, "", block.Content, width)
	}
}

func (m AppModel) renderToolHeader(block Block, selector string, width int, selected bool) string {
	summary := m.toolDisplayName(block.Title)
	if detail := toolArgumentSummary(block); detail != "" {
		summary += " " + detail
	}
	if strings.ReplaceAll(block.Title, "_", ".") == "coding.search" {
		sections := parseSourceResult(block.Content, "")
		matches := 0
		for _, section := range sections {
			matches += len(section.lines)
		}
		if matches > 0 {
			summary += fmt.Sprintf(" (%d matches in %d files)", matches, len(sections))
		}
	}

	prefix := "  "
	if selected {
		prefix = "› "
	}
	line := m.theme.Muted.Render(prefix+"◆ ") + m.theme.Assistant.Render(summary)
	if block.State != "" && block.State != "completed" {
		line += m.theme.MetaDivider.Render("  ·  ") +
			m.stateStyle(block.State).Render(stateMark(block.State)+" "+m.displayState(block.State))
	}
	if len(block.Hooks) > 0 {
		line += m.theme.MetaDivider.Render(fmt.Sprintf("  [hooks: %d]", len(block.Hooks)))
	}
	line = padOrTrim(line, width)
	if selected {
		return m.theme.Selected.Render(line)
	}
	return line
}

func toolArgumentSummary(block Block) string {
	var arguments map[string]any
	if json.Unmarshal([]byte(block.Arguments), &arguments) != nil {
		return ""
	}
	value := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := arguments[key]; ok {
				text := strings.TrimSpace(fmt.Sprint(raw))
				if text != "" {
					return text
				}
			}
		}
		return ""
	}

	name := strings.ReplaceAll(block.Title, "_", ".")
	switch name {
	case "coding.search":
		query := value("query", "pattern")
		scope := value("glob", "path")
		switch {
		case query != "" && scope != "":
			return fmt.Sprintf("%q in %s", query, scope)
		case query != "":
			return fmt.Sprintf("%q", query)
		}
	case "coding.read.file", "coding.list.files", "coding.write.file", "coding.edit.hashline", "coding.git.diff":
		return value("path", "file")
	case "coding.shell", "coding.go.test", "coding.gofmt":
		return value("command", "package", "path")
	}
	return value("path", "query", "command")
}

func (m AppModel) renderThinkingMessage(block Block, width int) []string {
	label := m.tr("block.thinking")
	header := m.theme.ThinkingTag.Render("  ◆ " + label)
	content := strings.ReplaceAll(block.Content, "****", "**\n\n**")
	body := renderMarkdownBlock(m.theme.Thinking, "", content, max(4, width-4))
	if len(body) == 0 {
		return []string{header}
	}
	lines := make([]string, 0, len(body)+1)
	lines = append(lines, header)
	for _, line := range body {
		lines = append(lines, m.theme.BlockRail.Render("  │ ")+line)
	}
	return lines
}

func (m AppModel) blockKindStyles(block Block, rendersDiff bool) (lipgloss.Style, lipgloss.Style) {
	if rendersDiff {
		return m.theme.AssistantTag, m.theme.Diff
	}
	switch block.Kind {
	case BlockTool:
		return m.toolStyles(block.Title)
	case BlockAgent:
		return m.theme.AgentTag, m.theme.ApprovalSmart
	case BlockError:
		return m.theme.ErrorTag, m.theme.Error
	default:
		return m.theme.AssistantTag, m.theme.Assistant
	}
}

func (m AppModel) toolStyles(name string) (lipgloss.Style, lipgloss.Style) {
	normalized := strings.ReplaceAll(name, "_", ".")
	switch normalized {
	case "coding.search":
		return m.theme.ThinkingTag, m.theme.ToolSearch
	case "coding.read.file", "coding.list.files":
		return m.theme.AgentTag, m.theme.ToolRead
	case "coding.edit.hashline", "coding.write.file", "coding.gofmt", "coding.git.diff":
		return m.theme.AssistantTag, m.theme.ToolWrite
	case "coding.shell", "coding.go.test":
		return m.theme.ToolTag, m.theme.ToolExecute
	case "todo", "hydaelyn.activate.skill", "hydaelyn.read.skill.resource", "subagent.spawn", "subagent.get.output", "subagent.kill":
		return m.theme.HookTag, m.theme.ToolAgent
	default:
		if strings.Contains(normalized, "memory") || strings.Contains(normalized, "recap") {
			return m.theme.AssistantTag, m.theme.ToolMemory
		}
		return m.theme.ToolTag, m.theme.Assistant
	}
}

func (m AppModel) renderBlockHeader(selector, mark, kind, title, rawState, stateLabel string, width int, tagStyle, accentStyle lipgloss.Style, selected bool) string {
	prefix := "  " + selector + " " + mark + " "
	header := m.theme.Muted.Render(prefix) + tagStyle.Render(kind) + m.theme.MetaDivider.Render(" · ") + accentStyle.Bold(true).Render(title)
	if stateLabel != "" {
		header += "  " + m.stateStyle(rawState).Bold(true).Render(stateMark(rawState)+" "+stateLabel)
	}
	header = padOrTrim(header, width+2)
	if selected {
		return m.theme.Selected.Render(header)
	}
	return header
}

func (m AppModel) renderBlockContent(content string, width int, style lipgloss.Style) []string {
	lines := make([]string, 0)
	for _, line := range wrapText(content, max(4, width-4)) {
		lines = append(lines, m.theme.BlockRail.Render("      │ ")+style.Render(line))
	}
	return lines
}

func (m AppModel) renderApprovalContent(content string, width int) []string {
	const rail = "      │ "
	contentWidth := max(1, width-ansi.StringWidth(rail))
	lines := make([]string, 0)
	for _, source := range strings.Split(content, "\n") {
		label, value, found := strings.Cut(source, ":")
		if !found {
			label, value, found = strings.Cut(source, "：")
		}
		styled := m.theme.Assistant.Render(source)
		if found && strings.TrimSpace(label) != "" {
			separator := ":"
			if strings.Contains(source, "：") && !strings.Contains(source, ":") {
				separator = "："
			}
			styled = m.theme.ApprovalAsk.Bold(true).Render(strings.TrimSpace(label)+separator) + m.theme.Assistant.Render(strings.TrimSpace(value))
		}
		for _, line := range wrapText(styled, contentWidth) {
			lines = append(lines, m.theme.BlockRail.Render(rail)+line)
		}
	}
	return lines
}

type sourceResultSection struct {
	path  string
	lines []sourceResultLine
}

type sourceResultLine struct {
	number int
	code   string
}

func (m AppModel) renderToolContent(block Block, width int) []string {
	switch block.Title {
	case "coding.search", "coding.read_file":
		if rows, ok := m.renderSourceToolContent(block, width); ok {
			return rows
		}
	case "coding.list_files":
		return m.renderPathList(block.Content, width)
	}
	_, accent := m.toolStyles(block.Title)
	return m.renderBlockContent(block.Content, width, accent)
}

func (m AppModel) renderPathList(content string, width int) []string {
	const rail = "      │ "
	textWidth := max(1, width-ansi.StringWidth(rail))
	rows := make([]string, 0)
	for _, source := range strings.Split(content, "\n") {
		style := m.theme.ToolRead
		if strings.HasPrefix(strings.TrimSpace(source), "…") || strings.HasPrefix(strings.TrimSpace(source), "[") {
			style = m.theme.Muted
		}
		for _, line := range wrapText(source, textWidth) {
			rows = append(rows, m.theme.MetaDivider.Render(rail)+style.Render(line))
		}
	}
	return rows
}

func (m AppModel) renderSourceToolContent(block Block, width int) ([]string, bool) {
	sections := parseSourceResult(block.Content, sourcePathFromArguments(block.Arguments))
	if len(sections) == 0 {
		return nil, false
	}
	const rail = "      │ "
	available := max(1, width-ansi.StringWidth(rail))
	maxNumber := 1
	for _, section := range sections {
		for _, line := range section.lines {
			maxNumber = max(maxNumber, line.number)
		}
	}
	gutterWidth := len(strconv.Itoa(maxNumber))
	showNumbers := available >= 18
	rows := make([]string, 0)
	pathStyle := m.theme.ToolRead
	if block.Title == "coding.search" {
		pathStyle = m.theme.ToolSearch
	}
	for sectionIndex, section := range sections {
		if sectionIndex > 0 {
			rows = append(rows, m.theme.MetaDivider.Render(rail))
		}
		path := first(section.path, "source")
		pathLine := m.theme.MetaDivider.Render(rail) + pathStyle.Bold(true).Render("⌁ "+path)
		rows = append(rows, padOrTrim(pathLine, width))
		for _, source := range section.lines {
			gutter := ""
			if showNumbers {
				gutter = m.theme.Muted.Render(fmt.Sprintf("%*d │ ", gutterWidth, source.number))
			}
			codeWidth := max(1, available-ansi.StringWidth(gutter))
			highlighted := m.highlightSourceLine(path, source.code)
			wrapped := wrapText(highlighted, codeWidth)
			for index, line := range wrapped {
				lineGutter := gutter
				if index > 0 && showNumbers {
					lineGutter = m.theme.Muted.Render(strings.Repeat(" ", gutterWidth) + " · ")
				}
				rows = append(rows, m.theme.MetaDivider.Render(rail)+lineGutter+line)
			}
		}
	}
	return rows, true
}

func parseSourceResult(content, fallbackPath string) []sourceResultSection {
	sections := make([]sourceResultSection, 0)
	current := -1
	ensureSection := func(path string) int {
		sections = append(sections, sourceResultSection{path: path})
		return len(sections) - 1
	}
	for _, source := range strings.Split(strings.TrimSpace(content), "\n") {
		if strings.HasPrefix(source, "¶") {
			path := strings.TrimPrefix(strings.SplitN(source, "#", 2)[0], "¶")
			current = ensureSection(first(path, fallbackPath))
			continue
		}
		prefix, code, found := strings.Cut(source, ":")
		number, err := strconv.Atoi(strings.TrimSpace(prefix))
		if !found || err != nil || number < 1 {
			continue
		}
		if current < 0 {
			current = ensureSection(fallbackPath)
		}
		sections[current].lines = append(sections[current].lines, sourceResultLine{number: number, code: strings.TrimPrefix(code, " ")})
	}
	result := sections[:0]
	for _, section := range sections {
		if len(section.lines) > 0 {
			result = append(result, section)
		}
	}
	return result
}

func sourcePathFromArguments(arguments string) string {
	var input struct {
		Path string `json:"path"`
		Glob string `json:"glob"`
	}
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return ""
	}
	return first(input.Path, input.Glob)
}

func (m AppModel) highlightSourceLine(path, source string) string {
	lexer := lexers.Match(filepath.Base(path))
	if lexer == nil || lexer == lexers.Fallback {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		return m.theme.Assistant.Render(source)
	}
	tokens, err := chroma.Tokenise(lexer, nil, source)
	if err != nil {
		return m.theme.Assistant.Render(source)
	}
	var rendered strings.Builder
	for _, token := range tokens {
		value := strings.TrimSuffix(token.Value, "\n")
		style := m.theme.Assistant
		switch {
		case token.Type.InCategory(chroma.Keyword):
			style = m.theme.CodeKeyword
		case token.Type.InCategory(chroma.LiteralString):
			style = m.theme.CodeString
		case token.Type.InCategory(chroma.LiteralNumber):
			style = m.theme.CodeNumber
		case token.Type.InCategory(chroma.Comment):
			style = m.theme.CodeComment
		case token.Type.InCategory(chroma.Name):
			style = m.theme.CodeName
		case token.Type.InCategory(chroma.Operator), token.Type.InCategory(chroma.Punctuation):
			style = m.theme.CodeOperator
		}
		rendered.WriteString(style.Render(value))
	}
	return rendered.String()
}

// maxStickyInstructionBody is the collapsed body height for the pinned prompt
// chip (mirrors grok-build sticky user prompts: short, always-scannable).
const maxStickyInstructionBody = 1

// stickyCardRows is fixed: top border + body + bottom border. A fixed slot
// prevents layout thrashing (and scroll jank) when sticky toggles or wraps.
const stickyCardRows = maxStickyInstructionBody + 2

// stickySlotRows is the layout cost of an active sticky chip (card + gap).
const stickySlotRows = stickyCardRows + 1

// renderUserMessage paints the in-transcript prompt as a floating dialog card
// (rounded frame + elevated band), matching the grok-build user-prompt look.
func (m AppModel) renderUserMessage(content string, width int) []string {
	return m.renderUserDialog(content, width, 0)
}

// renderUserDialog draws a rounded instruction card. maxBodyLines caps the
// body (0 = unlimited); sticky pins use a small cap so the chip stays compact.
func (m AppModel) renderUserDialog(content string, width int, maxBodyLines int) []string {
	if width < 6 {
		return []string{m.theme.UserSurface.Render(padOrTrim(" › "+strings.TrimSpace(content), width))}
	}
	innerWidth := max(1, width-4) // between "│ " and " │"
	textWidth := max(1, innerWidth-2)
	body := wrapText(strings.TrimSpace(content), textWidth)
	if len(body) == 0 {
		body = []string{""}
	}
	if maxBodyLines > 0 && len(body) > maxBodyLines {
		body = append([]string(nil), body[:maxBodyLines]...)
		last := body[len(body)-1]
		if ansi.StringWidth(last) >= textWidth {
			body[len(body)-1] = ansi.Truncate(last, textWidth, "…")
		} else if !strings.HasSuffix(last, "…") {
			body[len(body)-1] = last + "…"
		}
	}
	border := m.theme.Border
	lines := make([]string, 0, len(body)+2)
	lines = append(lines, border.Render("╭"+strings.Repeat("─", width-2)+"╮"))
	for index, line := range body {
		prefix := "  "
		if index == 0 {
			prefix = "› "
		}
		inner := m.theme.UserSurface.Render(padOrTrim(prefix+line, innerWidth))
		lines = append(lines, border.Render("│ ")+inner+border.Render(" │"))
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return lines
}

type stickyUserMark struct {
	content string
	endLine int
	runID   string
}

// stickyInstructionContent returns the latest user prompt that has scrolled
// above the transcript viewport (iOS/grok sticky section header).
//
// bodyHeight must be the pre-sticky body height. Decision uses a fixed sticky
// slot so showing the chip cannot feed back into a different decision and
// thrash scroll position.
func (m AppModel) stickyInstructionContent(contentWidth, bodyHeight int) (string, bool) {
	if bodyHeight < stickySlotRows+2 {
		return "", false
	}
	// Ensure marks are warm; transcriptLines is cached after the first call.
	_ = m.transcriptLines(contentWidth)
	cache := m.transcriptLayout
	if cache == nil || len(cache.userMarks) == 0 {
		return "", false
	}
	totalLines := len(cache.lines)
	if totalLines == 0 {
		return "", false
	}

	contentHeight := max(1, bodyHeight-stickySlotRows)
	if m.isRunning() || m.transcriptOffsetLimit(totalLines, contentHeight) > 0 {
		footerGap := 0
		if contentHeight >= 3 {
			footerGap = 1
		}
		contentHeight = max(1, contentHeight-1-footerGap)
	}
	offset := min(m.transcriptOffsetLimit(totalLines, max(1, bodyHeight-stickySlotRows)), max(0, m.transcriptTop))
	end := totalLines - offset
	visibleStart := max(0, end-contentHeight)

	for index := len(cache.userMarks) - 1; index >= 0; index-- {
		mark := cache.userMarks[index]
		if mark.endLine > visibleStart {
			continue
		}
		if m.isRunning() && m.runID != "" && mark.runID != "" && mark.runID != m.runID {
			continue
		}
		content := strings.TrimSpace(mark.content)
		if content == "" {
			continue
		}
		return content, true
	}
	return "", false
}

// stickyInstructionHeight returns the fixed layout cost of the sticky chip
// (0 when hidden). Height is constant to avoid scroll jank from wrap changes.
func (m AppModel) stickyInstructionHeight(width, bodyHeight int) int {
	if m.stickyVisibleWithCache(bodyHeight) {
		return stickyCardRows
	}
	// Cold cache (marks not built yet): fall back to full content lookup once.
	if _, ok := m.stickyInstructionContent(transcriptContentWidth(width, bodyHeight), bodyHeight); ok {
		return stickyCardRows
	}
	return 0
}

func (m AppModel) renderStickyInstruction(width, bodyHeight int) string {
	content, ok := m.stickyInstructionContent(transcriptContentWidth(width, bodyHeight), bodyHeight)
	if !ok || width <= 0 {
		return ""
	}
	lines := m.renderUserDialog(content, width, maxStickyInstructionBody)
	for len(lines) < stickyCardRows {
		lines = append(lines, padStyledLine("", width))
	}
	if len(lines) > stickyCardRows {
		lines = lines[:stickyCardRows]
	}
	for index, line := range lines {
		lines[index] = padStyledLine(line, width)
	}
	return strings.Join(lines, "\n")
}

// applyStickyLayout reserves a fixed sticky slot on layout when the current
// instruction has scrolled out of view. Returns the rendered chip (may be empty).
// Uses paint cache so pure scroll frames reuse the previous chip string.
func (m AppModel) applyStickyLayout(width int, layout *viewLayout) string {
	if layout == nil || layout.bodyHeight < stickySlotRows+2 {
		return ""
	}
	preBody := layout.bodyHeight
	content, ok := m.stickyInstructionContent(transcriptContentWidth(width, preBody), preBody)
	if !ok {
		if p := m.paint; p != nil {
			p.stickyHeight = 0
			p.stickyText = ""
			p.stickyRender = ""
			p.preBodyHeight = preBody
		}
		return ""
	}
	layout.stickyHeight = stickySlotRows
	layout.bodyHeight -= stickySlotRows

	if p := m.paint; p != nil &&
		p.stickyText == content &&
		p.stickyHeight == stickySlotRows &&
		p.width == width &&
		p.stickyRender != "" {
		return p.stickyRender
	}

	lines := m.renderUserDialog(content, width, maxStickyInstructionBody)
	for len(lines) < stickyCardRows {
		lines = append(lines, padStyledLine("", width))
	}
	if len(lines) > stickyCardRows {
		lines = lines[:stickyCardRows]
	}
	for index, line := range lines {
		lines[index] = padStyledLine(line, width)
	}
	rendered := strings.Join(lines, "\n")
	if p := m.paint; p != nil {
		p.stickyHeight = stickySlotRows
		p.stickyText = content
		p.stickyRender = rendered
		p.stickyTop = m.transcriptTop
		p.stickyRunID = m.runID
		p.stickyRun = m.isRunning()
		p.preBodyHeight = preBody
		p.width = width
	}
	return rendered
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
		labelText := fmt.Sprintf("%s %s · %s%s", mark, m.tr("hook.label"), hook.Event, name)
		label := padOrTrim("    "+m.theme.HookTag.Render(labelText), width+2)
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
	return m.renderDiffRows(content, width, "      │ ")
}

type diffLineKind uint8

const (
	diffContext diffLineKind = iota
	diffAdded
	diffDeleted
	diffMeta
)

type diffViewLine struct {
	Kind    diffLineKind
	Text    string
	OldLine int
	NewLine int
}

type diffViewHunk struct {
	Header string
	Lines  []diffViewLine
}

type diffViewFile struct {
	Path    string
	Added   int
	Deleted int
	Hunks   []diffViewHunk
}

func (m AppModel) renderDiffRows(content string, width int, prefix string) []string {
	files, ok := parseDiffView(content, m.catalog)
	if !ok {
		rows := make([]string, 0)
		for _, source := range strings.Split(content, "\n") {
			for _, line := range wrapText(source, max(1, width-ansi.StringWidth(prefix))) {
				rows = append(rows, m.theme.Assistant.Render(prefix+line))
			}
		}
		return rows
	}

	rows := make([]string, 0)
	for fileIndex, file := range files {
		if fileIndex > 0 {
			rows = append(rows, prefix)
		}
		status := "M"
		if file.Added > 0 && file.Deleted == 0 {
			status = "A"
		} else if file.Deleted > 0 && file.Added == 0 {
			status = "D"
		}
		headerPrefix := fmt.Sprintf("%s%s %s  ", prefix, status, file.Path)
		header := m.theme.Diff.Bold(true).Render(headerPrefix) +
			m.theme.Success.Bold(true).Render(fmt.Sprintf("+%d", file.Added)) +
			m.theme.Error.Render(fmt.Sprintf(" -%d", file.Deleted))
		rows = append(rows, padOrTrim(header, width))
		rows = append(rows, prefix)
		for _, hunk := range file.Hunks {
			rows = append(rows, m.theme.DiffHunk.Render(padOrTrim(prefix+hunk.Header, width)))
			maxLine := 0
			for _, line := range hunk.Lines {
				maxLine = max(maxLine, line.OldLine, line.NewLine)
			}
			gutterWidth := len(strconv.Itoa(max(1, maxLine)))
			showNumbers := width-ansi.StringWidth(prefix) >= 30
			for _, line := range hunk.Lines {
				mark := " "
				style := m.theme.Assistant
				switch line.Kind {
				case diffAdded:
					mark, style = "+", m.theme.DiffAdd
				case diffDeleted:
					mark, style = "-", m.theme.DiffDel
				case diffMeta:
					mark, style = "·", m.theme.Muted
				}
				gutter := mark + " "
				if showNumbers {
					oldNumber, newNumber := "", ""
					if line.OldLine > 0 {
						oldNumber = strconv.Itoa(line.OldLine)
					}
					if line.NewLine > 0 {
						newNumber = strconv.Itoa(line.NewLine)
					}
					gutter = fmt.Sprintf("%*s %*s %s ", gutterWidth, oldNumber, gutterWidth, newNumber, mark)
				}
				displayText := strings.ReplaceAll(line.Text, "\t", "    ")
				textWidth := max(1, width-ansi.StringWidth(prefix)-ansi.StringWidth(gutter))
				wrapped := wrapText(displayText, textWidth)
				for index, text := range wrapped {
					lineGutter := gutter
					if index > 0 {
						lineGutter = strings.Repeat(" ", ansi.StringWidth(gutter)-2) + "· "
					}
					row := prefix + lineGutter + text
					if line.Kind == diffAdded || line.Kind == diffDeleted {
						row = padOrTrim(row, width)
					}
					rows = append(rows, style.Render(row))
				}
			}
		}
	}
	return rows
}

func parseDiffView(content string, catalogs ...i18n.Catalog) ([]diffViewFile, bool) {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	sourceLines := strings.Split(content, "\n")
	files := make([]diffViewFile, 0)
	var file *diffViewFile
	var hunk *diffViewHunk
	oldLine, newLine := 1, 1
	recognized := false
	startFile := func(path string) {
		path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "a/"), "b/")
		if path == "" || path == "/dev/null" {
			path = catalog.T("diff.workspace_change")
		}
		files = append(files, diffViewFile{Path: path})
		file = &files[len(files)-1]
		hunk = nil
	}
	startHunk := func(header string, oldStart, newStart int) {
		if file == nil {
			startFile(catalog.T("diff.workspace_change"))
		}
		file.Hunks = append(file.Hunks, diffViewHunk{Header: header})
		hunk = &file.Hunks[len(file.Hunks)-1]
		oldLine, newLine = max(1, oldStart), max(1, newStart)
	}

	for index, source := range sourceLines {
		if strings.HasPrefix(source, "diff --git ") {
			fields := strings.Fields(source)
			if len(fields) >= 4 {
				startFile(fields[3])
				recognized = true
			}
			continue
		}
		if strings.HasPrefix(source, "+++ ") {
			if file == nil {
				startFile(strings.TrimSpace(strings.TrimPrefix(source, "+++ ")))
			}
			recognized = true
			continue
		}
		if strings.HasPrefix(source, "--- ") || strings.HasPrefix(source, "index ") || strings.HasPrefix(source, "new file mode ") || strings.HasPrefix(source, "deleted file mode ") {
			recognized = true
			continue
		}
		if strings.HasPrefix(source, "@@ ") {
			if oldStart, newStart, ok := unifiedHunkStarts(source); ok {
				startHunk(source, oldStart, newStart)
				recognized = true
				continue
			}
			if path, line, ok := compactDiffHeader(source); ok {
				startFile(path)
				startHunk(catalog.T("diff.line_header", map[string]string{"line": strconv.Itoa(line)}), line, line)
				recognized = true
				continue
			}
		}
		if !recognized {
			continue
		}
		if source == "" && (index == len(sourceLines)-1 || index+1 < len(sourceLines) && strings.HasPrefix(sourceLines[index+1], "@@ ")) {
			continue
		}
		if hunk == nil {
			startHunk(catalog.T("diff.change_header"), 1, 1)
		}
		line := diffViewLine{Kind: diffContext, Text: source, OldLine: oldLine, NewLine: newLine}
		switch {
		case strings.HasPrefix(source, "+"):
			line.Kind, line.Text, line.OldLine = diffAdded, strings.TrimPrefix(source, "+"), 0
			file.Added++
			newLine++
		case strings.HasPrefix(source, "-"):
			line.Kind, line.Text, line.NewLine = diffDeleted, strings.TrimPrefix(source, "-"), 0
			file.Deleted++
			oldLine++
		case strings.HasPrefix(source, "\\ No newline"):
			line.Kind, line.Text, line.OldLine, line.NewLine = diffMeta, source, 0, 0
		default:
			line.Text = strings.TrimPrefix(source, " ")
			oldLine++
			newLine++
		}
		hunk.Lines = append(hunk.Lines, line)
	}
	return files, recognized && len(files) > 0
}

func compactDiffHeader(source string) (string, int, bool) {
	value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(source, "@@ "), "@@"))
	separator := strings.LastIndex(value, ":")
	if separator < 1 {
		return "", 0, false
	}
	line, err := strconv.Atoi(value[separator+1:])
	return value[:separator], line, err == nil && line > 0
}

func unifiedHunkStarts(source string) (int, int, bool) {
	fields := strings.Fields(source)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, false
	}
	parseStart := func(value string) (int, error) {
		value = strings.TrimLeft(value, "+-")
		value, _, _ = strings.Cut(value, ",")
		return strconv.Atoi(value)
	}
	oldStart, oldErr := parseStart(fields[1])
	newStart, newErr := parseStart(fields[2])
	return oldStart, newStart, oldErr == nil && newErr == nil
}

func toolDisplayName(name string, catalogs ...i18n.Catalog) string {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	keys := map[string]string{
		"coding.read_file": "tool.read_file", "coding.write_file": "tool.write_file", "coding.edit_hashline": "tool.edit_file",
		"coding.search": "tool.search", "coding.list_files": "tool.list_files", "coding.shell": "tool.shell",
		"coding.go_test": "tool.go_test", "coding.gofmt": "tool.gofmt", "coding.git_diff": "tool.git_diff",
		"hydaelyn_activate_skill": "tool.activate_skill", "subagent.spawn": "tool.spawn",
	}
	if key := keys[name]; key != "" {
		return catalog.T(key)
	}
	return name
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
	return toolDisplayName(name, m.catalog)
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
		var err error
		renderer, err = newTerminalMarkdownRenderer(key)
		if err != nil {
			return "", err
		}
		markdownRendererCache.renderers[key] = renderer
	}
	return renderer.Render(content)
}

func newTerminalMarkdownRenderer(key markdownRendererKey) (*glamour.TermRenderer, error) {
	style := styles.LightStyleConfig
	if key.dark {
		style = styles.DarkStyleConfig
	}
	// Glamour's built-in themes add backgrounds to headings, inline code,
	// and fenced code. Keep transcript content transparent so it inherits
	// the terminal background like the rest of the UI.
	style.H1.BackgroundColor = nil
	style.Code.BackgroundColor = nil
	if style.CodeBlock.Chroma != nil {
		chromaStyle := *style.CodeBlock.Chroma
		chromaStyle.Error.BackgroundColor = nil
		chromaStyle.Background.BackgroundColor = nil
		style.CodeBlock.Chroma = &chromaStyle
	}
	style.H2.Prefix = "▌ "
	style.H3.Prefix = "│ "
	style.H4.Prefix = "· "
	style.H5.Prefix = ""
	style.H6.Prefix = ""
	style.HorizontalRule.Format = "\n──────\n"
	return glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(key.width),
	)
}
