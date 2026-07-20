package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Viking602/azem/internal/session"
)

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
			row := fmt.Sprintf("  %s %s", m.agentStateMark(agent.State), first(agent.Role, agent.ID))
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
			status := m.todoDisplayStatus(item)
			row := todoSummaryRow{text: item.Content, status: status}
			switch status {
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
	for index := range all {
		all[index].text = fmt.Sprintf("%s  %d. %s", todoMark(all[index].status, m.animationFrame), index+1, all[index].text)
	}
	if len(all) > limit {
		return all[:limit], len(all) - limit
	}
	return all, 0
}

func (m AppModel) todoDisplayStatus(item session.TodoItem) session.TodoStatus {
	if item.Status != session.TodoPending || item.SubagentRunID == "" {
		return item.Status
	}
	for _, agent := range m.agents {
		if agent.ID != item.SubagentRunID {
			continue
		}
		switch strings.ToLower(agent.State) {
		case "initializing", "running", "cancelling":
			return session.TodoInProgress
		}
		break
	}
	return item.Status
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

const (
	recapStatusMaxWords     = 40
	recapStatusMaxWidth     = 120
	recapStatusMaxSentences = 2
)

func (m AppModel) renderRecapStatus(width int) string {
	if width <= 0 || m.recap == nil || m.isRunning() {
		return ""
	}
	text := ""
	for _, candidate := range []string{m.recap.Summary, m.recap.Goal, m.recap.OpenItems} {
		text = recapStatusPreview(candidate)
		if text != "" {
			break
		}
	}
	if text == "" {
		return ""
	}
	prefix := m.tr("recap.status_prefix")
	suffix := m.tr("recap.status_hint")
	full := fmt.Sprintf("  ※ %s: %s  · %s", prefix, text, suffix)
	compact := fmt.Sprintf("  ※ %s: %s", prefix, text)
	if ansi.StringWidth(full) <= width {
		return m.theme.Muted.Italic(true).Render(padOrTrim(full, width))
	}
	return m.theme.Muted.Italic(true).Render(padOrTrim(compact, width))
}

func (m AppModel) visibleRecapStatus(width, height int) string {
	if height < 4 || strings.TrimSpace(m.composer.Value()) != "" {
		return ""
	}
	return m.renderRecapStatus(width)
}

func recapStatusPreview(value string) string {
	value = plainRecapText(value)
	if value == "" {
		return ""
	}
	value = firstRecapSentences(value, recapStatusMaxSentences)
	words := strings.Fields(value)
	if len(words) > recapStatusMaxWords {
		value = strings.Join(words[:recapStatusMaxWords], " ") + "…"
	}
	if ansi.StringWidth(value) > recapStatusMaxWidth {
		value = ansi.Truncate(value, recapStatusMaxWidth, "…")
	}
	return value
}

func plainRecapText(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	cleaned := make([]string, 0, len(lines))
	inCodeBlock := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if line == "" || inCodeBlock || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, ">"))
		for _, marker := range []string{"- ", "* ", "+ "} {
			line = strings.TrimPrefix(line, marker)
		}
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	value = strings.Join(cleaned, " ")
	value = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func firstRecapSentences(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	sentences := 0
	for index, current := range value {
		switch current {
		case '.', '!', '?', '。', '！', '？':
			sentences++
			if sentences == limit {
				return strings.TrimSpace(value[:index+len(string(current))])
			}
		}
	}
	return strings.TrimSpace(value)
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
	helpText := strings.Join([]string{"Drag copy", m.tr("footer.help.approval"), m.tr("footer.help.reasoning"), m.tr("footer.help.commands"), m.tr("status.help")}, "  ")
	if width < 112 {
		helpText = strings.Join([]string{"Drag copy", m.tr("footer.help.approval"), m.tr("footer.help.reasoning"), m.tr("status.help")}, "  ")
	}
	if width < 86 {
		helpText = strings.Join([]string{"Drag copy", m.tr("footer.help.approval"), m.tr("status.help")}, "  ")
	}
	if width < 64 {
		helpText = "Drag copy"
	}
	return joinSides(status, m.theme.Muted.Render(helpText+" "), width)
}
