package tui

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/x/ansi"
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
		if rendersDiff {
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
		if rendersDiff {
			lines = append(lines, m.renderDiffContent(block.Content, width+2)...)
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
	files, ok := parseDiffView(content)
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

func parseDiffView(content string) ([]diffViewFile, bool) {
	sourceLines := strings.Split(content, "\n")
	files := make([]diffViewFile, 0)
	var file *diffViewFile
	var hunk *diffViewHunk
	oldLine, newLine := 1, 1
	recognized := false
	startFile := func(path string) {
		path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "a/"), "b/")
		if path == "" || path == "/dev/null" {
			path = "workspace change"
		}
		files = append(files, diffViewFile{Path: path})
		file = &files[len(files)-1]
		hunk = nil
	}
	startHunk := func(header string, oldStart, newStart int) {
		if file == nil {
			startFile("workspace change")
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
				startHunk(fmt.Sprintf("@@ line %d @@", line), line, line)
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
			startHunk("@@ change @@", 1, 1)
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
		style := styles.LightStyleConfig
		if key.dark {
			style = styles.DarkStyleConfig
		}
		style.H2.Prefix = "▌ "
		style.H3.Prefix = "│ "
		style.H4.Prefix = "· "
		style.H5.Prefix = ""
		style.H6.Prefix = ""
		style.HorizontalRule.Format = "\n──────\n"
		var err error
		renderer, err = glamour.NewTermRenderer(
			glamour.WithStyles(style),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return "", err
		}
		markdownRendererCache.renderers[key] = renderer
	}
	return renderer.Render(content)
}
