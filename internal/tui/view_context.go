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
		m.theme.RailTitle.Render(padOrTrim("  RUN CONTEXT", width)),
		m.theme.RailTitle.Faint(true).Render(padOrTrim("  "+strings.Repeat("─", max(0, width-4)), width)),
	}
	todoHeader := "  TODO"
	if completed, total := todoProgress(m.todo); total > 0 {
		todoHeader = fmt.Sprintf("  TODO  %d/%d", completed, total)
	}
	rows = append(rows, m.theme.RailTodo.Render(padOrTrim(todoHeader, width)))
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
	rows = append(rows, m.theme.RailAgents.Render(padOrTrim(fmt.Sprintf("  AGENTS  %d", len(activeAgents)), width)))
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
	rows = append(rows, "", m.theme.RailMCP.Render(padOrTrim(fmt.Sprintf("  MCP  %d", len(m.mcpServers)), width)))
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

func (m AppModel) renderDockFooter(width int, lines int) string {
	if width <= 0 || lines <= 0 {
		return ""
	}
	rows := make([]string, 0, lines)
	switch {
	case lines >= 3:
		// Three clear bands: who/what is running, context health, then shortcuts.
		rows = append(rows,
			m.renderRuntimeStrip(width),
			m.renderContextStrip(width),
			m.renderHelpStrip(width),
		)
	case lines == 2:
		// Keep metrics alone on the second row — do not mix them with shortcut noise.
		rows = append(rows,
			m.renderRuntimeStrip(width),
			m.renderContextStrip(width),
		)
	default:
		rows = append(rows, m.renderStatus(width))
	}
	for len(rows) < lines {
		rows = append(rows, padStyledLine("", width))
	}
	return strings.Join(rows[:lines], "\n")
}

func (m AppModel) renderRuntimeStrip(width int) string {
	status := m.renderStatusCluster()
	model := m.renderModelCluster(width >= 56)
	// Status owns the left edge; model identity anchors the right with a clear gutter.
	if lipgloss.Width(status)+lipgloss.Width(model)+4 <= width {
		return padStyledLine(joinSides(status, model+" ", width), width)
	}
	return padStyledLine(joinSides(status, truncateStyledFallback(model, max(10, width/3))+" ", width), width)
}

func (m AppModel) renderContextStrip(width int) string {
	if width <= 0 {
		return ""
	}
	// Split primary occupancy from cache so the strip reads as two facts, not one blob.
	primary := m.renderContextPrimary(max(12, width*2/3))
	cache := m.renderCacheSummary(max(8, width/3))
	if cache == "" {
		return padStyledLine(padLeft(primary, 1), width)
	}
	return padStyledLine(joinSides(padLeft(primary, 1), cache+" ", width), width)
}

func (m AppModel) renderHelpStrip(width int) string {
	return padStyledLine(m.renderHelpStripContent(width), width)
}

func (m AppModel) renderHelpStripContent(width int) string {
	items := m.helpItems(width)
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, m.theme.HelpKey.Render(item.key)+m.theme.HelpDesc.Render(" "+item.desc))
	}
	// Leading gutter + wider separators keep shortcuts scannable under the meter.
	content := " " + strings.Join(parts, m.theme.MetaDivider.Render("   "))
	return truncateStyledFallback(content, width)
}

type helpItem struct {
	key  string
	desc string
}

func (m AppModel) helpItems(width int) []helpItem {
	all := []helpItem{
		{key: "Drag", desc: "copy"},
		{key: "Ctrl+V", desc: "image"},
		{key: "Shift+Tab", desc: strings.TrimSpace(strings.TrimPrefix(m.tr("footer.help.approval"), "Shift+Tab"))},
		{key: "Ctrl+R", desc: strings.TrimSpace(strings.TrimPrefix(m.tr("footer.help.reasoning"), "Ctrl+R"))},
		{key: "Ctrl+P", desc: strings.TrimSpace(strings.TrimPrefix(m.tr("footer.help.commands"), "Ctrl+P"))},
		{key: "?", desc: strings.TrimSpace(strings.TrimPrefix(m.tr("status.help"), "?"))},
	}
	// Normalize empty descriptions after trimming localized labels.
	for index := range all {
		if all[index].desc == "" {
			all[index].desc = strings.ToLower(all[index].key)
		}
	}
	switch {
	case width >= 112:
		return all
	case width >= 86:
		return all[:5]
	case width >= 64:
		return []helpItem{all[0], all[2], all[3], all[5]}
	default:
		return []helpItem{all[0]}
	}
}

func (m AppModel) renderStatusCluster() string {
	statusText := stateMark(m.status) + " " + m.displayState(m.status)
	status := m.stateStyle(m.status).Bold(true).Render(" " + statusText + " ")
	switch m.approvalMode {
	case ApprovalModeYolo:
		status += " " + m.theme.ChipDanger.Render("⚠ "+m.tr("status.approval.yolo"))
	case ApprovalModeAutoReview:
		status += " " + m.theme.ChipSmart.Render("⛨ "+m.tr("status.approval.auto"))
	default:
		status += " " + m.theme.ChipAsk.Render("☝︎ "+m.tr("status.approval.ask"))
	}
	if m.actionBusy {
		status += " " + m.theme.Chip.Render(m.tr("status.working"))
	} else if m.errorBanner != "" {
		errorInTranscript := false
		for index := len(m.transcript) - 1; index >= 0; index-- {
			if m.transcript[index].Kind == BlockError && m.transcript[index].Content == m.errorBanner {
				errorInTranscript = true
				break
			}
		}
		if errorInTranscript {
			status += " " + m.theme.Error.Render("DETAILS IN TRANSCRIPT")
		} else {
			status += " " + m.theme.Error.Render(m.errorBanner)
		}
	}
	return status
}

func (m AppModel) renderModelCluster(includeReasoning bool) string {
	model := m.provider + "/" + first(m.model, "no model")
	label := m.theme.MetaLabel.Render(m.tr("footer.model")) + " " + m.theme.MetaValue.Render(model)
	if includeReasoning && m.reasoning != "" {
		label += m.theme.MetaDivider.Render(" · ") +
			m.theme.MetaLabel.Render(m.tr("footer.reasoning")) + " " +
			m.theme.MetaValue.Render(m.reasoning)
	}
	return label
}

func (m AppModel) renderModelStatus(width int) string {
	left := m.renderModelCluster(width >= 64)
	if width < 32 {
		return padStyledLine(" "+left, width)
	}
	left = truncateStyledFallback(left, max(16, width/2))
	divider := m.theme.MetaDivider.Render("  │  ")
	contextWidth := max(0, width-lipgloss.Width(left)-lipgloss.Width(divider)-1)
	right := m.renderContextUsage(contextWidth)
	if right == "" {
		return padStyledLine(" "+left, width)
	}
	return padStyledLine(" "+left+divider+right, width)
}

func padLeft(value string, spaces int) string {
	if spaces <= 0 || value == "" {
		return value
	}
	return strings.Repeat(" ", spaces) + value
}

type contextMetrics struct {
	used          int
	limit         int
	percentage    float64
	contextLabel  string
	cacheLabel    string
	cache         string
	cacheRateOnly string
	compactCache  string
	detailSuffix  string
}

func (m AppModel) contextMetrics() contextMetrics {
	used := max(0, m.usage.InputTokens+m.usage.OutputTokens)
	limit := m.usage.ContextLimit
	metrics := contextMetrics{
		used:         used,
		limit:        limit,
		contextLabel: m.tr("footer.context"),
		cacheLabel:   m.tr("footer.cache"),
	}
	if limit > 0 {
		metrics.percentage = float64(used) * 100 / float64(limit)
	}
	cache := metrics.cacheLabel + " --"
	if m.usage.CurrentCacheEpoch > 0 {
		cache = fmt.Sprintf("%s E%d pending", metrics.cacheLabel, m.usage.CurrentCacheEpoch)
	}
	cacheRate := 0.0
	mainInput := m.usage.CurrentEpochMainReportedInput
	mainCached := min(max(0, m.usage.CurrentEpochMainCached), mainInput)
	mainReported := m.usage.MainCacheReported
	legacy := m.usage.CurrentEpochMainRequests == 0 && m.usage.MainCacheInput > 0
	if legacy {
		mainInput = m.usage.MainCacheInput
		mainCached = min(max(0, m.usage.MainCachedInput), mainInput)
	}
	if m.usage.CurrentEpochMainRequests > 0 && m.usage.CurrentEpochMainReportedRequests == 0 {
		cache = fmt.Sprintf("%s E%d N/A", metrics.cacheLabel, m.usage.CurrentCacheEpoch)
	}
	if !mainReported && m.usage.CacheReported && m.usage.TeamInput == 0 && m.usage.CompactionInput == 0 {
		mainInput = m.usage.CacheInputTokens
		mainCached = min(max(0, m.usage.CachedInputTokens), mainInput)
		mainReported = true
	}
	if mainReported && mainInput > 0 {
		cacheRate = float64(mainCached) * 100 / float64(mainInput)
		if legacy {
			cache = fmt.Sprintf("%s %s/%s · %.1f%%", metrics.cacheLabel, formatTokens(mainCached), formatTokens(mainInput), cacheRate)
		} else {
			cache = fmt.Sprintf("%s E%d %.1f%% · %d req", metrics.cacheLabel, m.usage.CurrentCacheEpoch, cacheRate, m.usage.CurrentEpochMainRequests)
		}
	} else if m.usage.CurrentEpochMainReportedRequests > 0 {
		cache = fmt.Sprintf("%s E%d 0.0%% · %d req", metrics.cacheLabel, m.usage.CurrentCacheEpoch, m.usage.CurrentEpochMainRequests)
	}
	allInput := m.usage.CacheInputTokens
	allCached := min(max(0, m.usage.CachedInputTokens), allInput)
	allRate := 0.0
	if m.usage.CacheReported && allInput > 0 {
		allRate = float64(allCached) * 100 / float64(allInput)
	}
	hasChildUsage := allInput > mainInput
	if hasChildUsage && mainReported && mainInput > 0 {
		cache = fmt.Sprintf("%s %s %s/%s · %.1f%% · %s %.1f%%", metrics.cacheLabel, m.tr("footer.main"), formatTokens(mainCached), formatTokens(mainInput), cacheRate, m.tr("footer.all"), allRate)
	}
	compactCache := "--"
	if mainReported && mainInput > 0 {
		compactCache = fmt.Sprintf("%.0f%%", cacheRate)
	}
	cacheRateOnly := metrics.cacheLabel + " --"
	if mainReported && mainInput > 0 {
		cacheRateOnly = fmt.Sprintf("%s %.1f%%", metrics.cacheLabel, cacheRate)
	}
	if hasChildUsage && mainReported && mainInput > 0 {
		cacheRateOnly = fmt.Sprintf("%s M%.1f%% A%.1f%%", metrics.cacheLabel, cacheRate, allRate)
		compactCache = fmt.Sprintf("M%.0f/A%.0f%%", cacheRate, allRate)
	}
	details := make([]string, 0, 4)
	if m.usage.UncachedInputTokens > 0 {
		details = append(details, "U "+formatTokens(m.usage.UncachedInputTokens))
	}
	if m.usage.CacheWriteTokens > 0 {
		write := "W " + formatTokens(m.usage.CacheWriteTokens)
		if m.usage.MainCacheWrite != m.usage.CacheWriteTokens {
			write = "W M" + formatTokens(m.usage.MainCacheWrite) + "/A" + formatTokens(m.usage.CacheWriteTokens)
		}
		details = append(details, write)
	}
	if m.usage.ReasoningTokens > 0 {
		details = append(details, "R "+formatTokens(m.usage.ReasoningTokens))
	}
	if m.usage.CompactionInput > 0 || m.usage.CompactionOutput > 0 {
		compaction := "CMP " + formatTokens(m.usage.CompactionInput) + "/" + formatTokens(m.usage.CompactionOutput)
		if m.usage.CompactionInput > 0 {
			compaction += fmt.Sprintf(" C%.0f%%", float64(min(m.usage.CompactionCached, m.usage.CompactionInput))*100/float64(m.usage.CompactionInput))
		}
		if m.usage.CompactionUncached > 0 {
			compaction += " U" + formatTokens(m.usage.CompactionUncached)
		}
		if m.usage.CompactionCacheWrite > 0 {
			compaction += " W" + formatTokens(m.usage.CompactionCacheWrite)
		}
		if m.usage.CompactionReasoning > 0 {
			compaction += " R" + formatTokens(m.usage.CompactionReasoning)
		}
		details = append(details, compaction)
	}
	if m.usage.TeamInput > 0 || m.usage.TeamOutput > 0 {
		team := "TEAM " + formatTokens(m.usage.TeamInput) + "/" + formatTokens(m.usage.TeamOutput)
		if m.usage.TeamInput > 0 {
			team += fmt.Sprintf(" C%.0f%%", float64(min(m.usage.TeamCached, m.usage.TeamInput))*100/float64(m.usage.TeamInput))
		}
		if m.usage.TeamUncached > 0 {
			team += " U" + formatTokens(m.usage.TeamUncached)
		}
		if m.usage.TeamCacheWrite > 0 {
			team += " W" + formatTokens(m.usage.TeamCacheWrite)
		}
		if m.usage.TeamReasoning > 0 {
			team += " R" + formatTokens(m.usage.TeamReasoning)
		}
		details = append(details, team)
	}
	if m.usage.LastRequestKind != "" {
		details = append(details, m.usage.LastRequestKind)
	}
	if m.usage.LastTransport != "" {
		details = append(details, m.usage.LastTransport)
	}
	metrics.cache = cache
	metrics.cacheRateOnly = cacheRateOnly
	metrics.compactCache = compactCache
	if len(details) > 0 {
		metrics.detailSuffix = " · " + strings.Join(details, " · ")
	}
	return metrics
}

func (m AppModel) contextTone(percentage float64) lipgloss.Style {
	switch {
	case percentage >= 90:
		return m.theme.Error
	case percentage >= 70:
		return m.theme.Warning
	default:
		return m.theme.Muted
	}
}

func (m AppModel) renderContextPrimary(width int) string {
	if width <= 0 {
		return ""
	}
	metrics := m.contextMetrics()
	if metrics.limit <= 0 {
		return truncateStyledFallback(m.theme.Muted.Render(metrics.contextLabel+" "+m.tr("footer.unavailable")), width)
	}
	barWidth := 10
	if width >= 40 {
		barWidth = 12
	}
	if width < 28 {
		barWidth = 6
	}
	barPlain := contextProgressBar(metrics.used, metrics.limit, barWidth)
	candidates := []struct {
		text     string
		barPlain string
		barWidth int
	}{
		// Occupancy only — cache lives on the right of the strip.
		{fmt.Sprintf("%s %s  %s / %s  ·  %.1f%%", metrics.contextLabel, barPlain, formatTokens(metrics.used), formatTokens(metrics.limit), metrics.percentage), barPlain, barWidth},
		{fmt.Sprintf("%s %s %s/%s %.0f%%", metrics.contextLabel, contextProgressBar(metrics.used, metrics.limit, 8), formatTokens(metrics.used), formatTokens(metrics.limit), metrics.percentage), contextProgressBar(metrics.used, metrics.limit, 8), 8},
		{fmt.Sprintf("%s %s/%s %.0f%%", metrics.contextLabel, formatTokens(metrics.used), formatTokens(metrics.limit), metrics.percentage), "", 0},
		{fmt.Sprintf("%s %.0f%%", metrics.contextLabel, metrics.percentage), "", 0},
	}
	return m.renderContextCandidate(width, metrics, candidates)
}

func (m AppModel) renderCacheSummary(width int) string {
	if width <= 0 {
		return ""
	}
	metrics := m.contextMetrics()
	if metrics.limit <= 0 {
		return ""
	}
	candidates := []string{
		metrics.cache,
		metrics.cacheRateOnly,
		metrics.cacheLabel + " " + metrics.compactCache,
	}
	text := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if ansi.StringWidth(candidate) <= width {
			text = candidate
			break
		}
	}
	return m.contextTone(metrics.percentage).Render(ansi.Truncate(text, width, "…"))
}

func (m AppModel) renderContextUsage(width int) string {
	if width <= 0 {
		return ""
	}
	metrics := m.contextMetrics()
	if metrics.limit <= 0 {
		return truncateStyledFallback(m.theme.Muted.Render(metrics.contextLabel+" "+m.tr("footer.unavailable")), width)
	}
	barWidth := 12
	if width < 48 {
		barWidth = 8
	}
	barPlainWide := contextProgressBar(metrics.used, metrics.limit, barWidth)
	barPlainMid := contextProgressBar(metrics.used, metrics.limit, 10)
	// Keep the dock strip readable: occupancy + cache only. Technical extras live in /status.
	candidates := []struct {
		text     string
		barPlain string
		barWidth int
	}{
		{fmt.Sprintf("%s %s  %s / %s  ·  %.1f%%  ·  %s", metrics.contextLabel, barPlainWide, formatTokens(metrics.used), formatTokens(metrics.limit), metrics.percentage, metrics.cache), barPlainWide, barWidth},
		{fmt.Sprintf("%s %s %s / %s · %.1f%% · %s", metrics.contextLabel, barPlainMid, formatTokens(metrics.used), formatTokens(metrics.limit), metrics.percentage, metrics.cache), barPlainMid, 10},
		{fmt.Sprintf("%s %s/%s · %.1f%% · %s", metrics.contextLabel, formatTokens(metrics.used), formatTokens(metrics.limit), metrics.percentage, metrics.cacheRateOnly), "", 0},
		{fmt.Sprintf("%s %s/%s %.0f%% C%s", metrics.contextLabel, formatTokens(metrics.used), formatTokens(metrics.limit), metrics.percentage, metrics.compactCache), "", 0},
		{fmt.Sprintf("%s %.0f%%", metrics.contextLabel, metrics.percentage), "", 0},
	}
	return m.renderContextCandidate(width, metrics, candidates)
}

func (m AppModel) statusReportLines() []string {
	metrics := m.contextMetrics()
	// Lead with the dense counters users open /status to read, then identity/context.
	lines := []string{m.tr("overlay.status.section.diagnostics")}
	if metrics.detailSuffix == "" && m.usage.UncachedInputTokens == 0 && m.usage.ReasoningTokens == 0 &&
		m.usage.CacheWriteTokens == 0 && m.usage.CompactionInput == 0 && m.usage.TeamInput == 0 &&
		m.usage.LastRequestKind == "" && m.usage.LastTransport == "" {
		lines = append(lines, "  "+m.tr("overlay.status.empty_diagnostics"))
	} else {
		if m.usage.UncachedInputTokens > 0 {
			lines = append(lines, fmt.Sprintf("  %s (U): %s", m.tr("overlay.status.field.uncached"), formatTokens(m.usage.UncachedInputTokens)))
		}
		if m.usage.CacheWriteTokens > 0 {
			write := formatTokens(m.usage.CacheWriteTokens)
			if m.usage.MainCacheWrite != m.usage.CacheWriteTokens {
				write = fmt.Sprintf("%s main / %s all", formatTokens(m.usage.MainCacheWrite), formatTokens(m.usage.CacheWriteTokens))
			}
			lines = append(lines, fmt.Sprintf("  %s (W): %s", m.tr("overlay.status.field.cache_write"), write))
		}
		if m.usage.ReasoningTokens > 0 {
			lines = append(lines, fmt.Sprintf("  %s (R): %s", m.tr("overlay.status.field.reasoning_tokens"), formatTokens(m.usage.ReasoningTokens)))
		}
		if m.usage.CompactionInput > 0 || m.usage.CompactionOutput > 0 {
			line := fmt.Sprintf("  %s (CMP): %s in / %s out", m.tr("overlay.status.field.compaction"), formatTokens(m.usage.CompactionInput), formatTokens(m.usage.CompactionOutput))
			if m.usage.CompactionCacheReported && m.usage.CompactionReportedInput > 0 {
				line += fmt.Sprintf(" · cache %.0f%%", float64(min(m.usage.CompactionCached, m.usage.CompactionReportedInput))*100/float64(m.usage.CompactionReportedInput))
			} else {
				line += " · cache N/A"
			}
			if m.usage.CompactionUncached > 0 {
				line += " · U " + formatTokens(m.usage.CompactionUncached)
			}
			if m.usage.CompactionCacheWrite > 0 {
				line += " · W " + formatTokens(m.usage.CompactionCacheWrite)
			}
			if m.usage.CompactionReasoning > 0 {
				line += " · R " + formatTokens(m.usage.CompactionReasoning)
			}
			lines = append(lines, line)
		}
		if m.usage.LifetimeMainRequests > 0 {
			lines = append(lines, fmt.Sprintf("  MAIN lifetime: %s in / %s out · %d req", formatTokens(m.usage.LifetimeMainInput), formatTokens(m.usage.LifetimeMainOutput), m.usage.LifetimeMainRequests))
		}
		if m.usage.TeamInput > 0 || m.usage.TeamOutput > 0 {
			line := fmt.Sprintf("  %s (TEAM): %s in / %s out", m.tr("overlay.status.field.team"), formatTokens(m.usage.TeamInput), formatTokens(m.usage.TeamOutput))
			if m.usage.TeamInput > 0 {
				line += fmt.Sprintf(" · cache %.0f%%", float64(min(m.usage.TeamCached, m.usage.TeamInput))*100/float64(m.usage.TeamInput))
			}
			if m.usage.TeamUncached > 0 {
				line += " · U " + formatTokens(m.usage.TeamUncached)
			}
			if m.usage.TeamCacheWrite > 0 {
				line += " · W " + formatTokens(m.usage.TeamCacheWrite)
			}
			if m.usage.TeamReasoning > 0 {
				line += " · R " + formatTokens(m.usage.TeamReasoning)
			}
			lines = append(lines, line)
		}
		if m.usage.LastRequestKind != "" {
			lines = append(lines, "  "+m.tr("overlay.status.field.request_kind")+": "+m.usage.LastRequestKind)
		}
		if m.usage.LastTransport != "" {
			lines = append(lines, "  "+m.tr("overlay.status.field.transport")+": "+m.usage.LastTransport)
		}
		if m.usage.LastProvider != "" || m.usage.LastModel != "" {
			lines = append(lines, "  "+m.tr("overlay.status.field.last_model")+": "+first(m.usage.LastProvider, "—")+"/"+first(m.usage.LastModel, "—"))
		}
	}
	lines = append(lines,
		m.tr("overlay.status.section.session"),
		"  "+m.tr("overlay.status.field.status")+": "+m.displayState(m.status),
		"  "+m.tr("overlay.status.field.mode")+": "+first(m.agentMode, "single"),
		"  "+m.tr("overlay.status.field.approval")+": "+m.approvalModeLabel(),
		"  "+m.tr("overlay.status.field.session")+": "+first(m.sessionID, "—"),
		m.tr("overlay.status.section.model"),
		"  "+m.tr("overlay.status.field.provider")+": "+first(m.provider, "—"),
		"  "+m.tr("overlay.status.field.model")+": "+first(m.model, "—"),
		"  "+m.tr("overlay.status.field.reasoning")+": "+first(m.reasoning, "—"),
		m.tr("overlay.status.section.context"),
	)
	if metrics.limit <= 0 {
		lines = append(lines, "  "+m.tr("footer.unavailable"))
	} else {
		lines = append(lines,
			fmt.Sprintf("  %s: %s / %s (%.1f%%)", m.tr("overlay.status.field.occupancy"), formatTokens(metrics.used), formatTokens(metrics.limit), metrics.percentage),
			"  "+m.tr("overlay.status.field.cache")+": "+metrics.cache,
		)
	}
	return lines
}

func (m AppModel) approvalModeLabel() string {
	switch m.approvalMode {
	case ApprovalModeYolo:
		return m.tr("status.approval.yolo")
	case ApprovalModeAutoReview:
		return m.tr("status.approval.auto")
	default:
		return m.tr("status.approval.ask")
	}
}

func (m AppModel) renderContextCandidate(width int, metrics contextMetrics, candidates []struct {
	text     string
	barPlain string
	barWidth int
}) string {
	chosen := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if ansi.StringWidth(candidate.text) <= width {
			chosen = candidate
			break
		}
	}
	text := ansi.Truncate(chosen.text, width, "…")
	tone := m.contextTone(metrics.percentage)
	if chosen.barPlain == "" || !strings.Contains(text, chosen.barPlain) {
		return tone.Render(text)
	}
	parts := strings.SplitN(text, chosen.barPlain, 2)
	return tone.Render(parts[0]) + m.styledContextProgressBar(metrics.used, metrics.limit, chosen.barWidth) + tone.Render(parts[1])
}

func contextProgressBar(used int, limit int, width int) string {
	if limit <= 0 || width <= 0 {
		return ""
	}
	clamped := min(max(0, used), limit)
	filled := int((int64(clamped)*int64(width) + int64(limit)/2) / int64(limit))
	return "[" + strings.Repeat("■", filled) + strings.Repeat("·", width-filled) + "]"
}

func (m AppModel) styledContextProgressBar(used int, limit int, width int) string {
	plain := contextProgressBar(used, limit, width)
	if plain == "" || width <= 0 {
		return plain
	}
	clamped := min(max(0, used), limit)
	filled := int((int64(clamped)*int64(width) + int64(limit)/2) / int64(limit))
	return m.theme.MetaDivider.Render("[") +
		m.theme.BarFilled.Render(strings.Repeat("■", filled)) +
		m.theme.BarEmpty.Render(strings.Repeat("·", width-filled)) +
		m.theme.MetaDivider.Render("]")
}

func (m AppModel) renderStatus(width int) string {
	status := m.renderStatusCluster()
	// Failure details take the whole strip — never compete with shortcut noise.
	if m.errorBanner != "" {
		errorInTranscript := false
		for index := len(m.transcript) - 1; index >= 0; index-- {
			if m.transcript[index].Kind == BlockError && m.transcript[index].Content == m.errorBanner {
				errorInTranscript = true
				break
			}
		}
		if errorInTranscript || m.actionBusy {
			return padStyledLine(status, width)
		}
	}
	// Keep plain "Drag copy" / localized shortcut phrases discoverable for tests and narrow widths.
	return joinSides(status, m.theme.Muted.Render(m.plainHelpText(width)+" "), width)
}

func (m AppModel) plainHelpText(width int) string {
	helpText := strings.Join([]string{"Drag copy", "Ctrl+V image", m.tr("footer.help.approval"), m.tr("footer.help.reasoning"), m.tr("footer.help.commands"), m.tr("status.help")}, "  ")
	if width < 112 {
		helpText = strings.Join([]string{"Drag copy", "Ctrl+V image", m.tr("footer.help.approval"), m.tr("footer.help.reasoning"), m.tr("status.help")}, "  ")
	}
	if width < 86 {
		helpText = strings.Join([]string{"Drag copy", m.tr("footer.help.approval"), m.tr("status.help")}, "  ")
	}
	if width < 64 {
		helpText = "Drag copy"
	}
	return helpText
}
