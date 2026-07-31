package tui

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

func (m AppModel) renderContextRail(width int, height int) string {
	activeAgents := m.activeAgents()
	rows := []string{
		m.theme.RailTitle.Render(padOrTrim("  "+m.tr("rail.run_context"), width)),
		m.theme.RailTitle.Faint(true).Render(padOrTrim("  "+strings.Repeat("─", max(0, width-4)), width)),
	}
	todoHeader := "  " + m.tr("rail.todos")
	if completed, total := todoProgress(m.todo); total > 0 {
		todoHeader = fmt.Sprintf("  %s  %d/%d", m.tr("rail.todos"), completed, total)
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
		rows = append(rows, m.theme.Muted.Render(padOrTrim("  "+m.tr("rail.more", map[string]string{"count": fmt.Sprint(more)}), width)))
	}
	rows = append(rows, "")
	rows = append(rows, m.theme.RailAgents.Render(padOrTrim(fmt.Sprintf("  %s  %d", m.tr("rail.agents"), len(activeAgents)), width)))
	if len(activeAgents) == 0 {
		rows = append(rows, m.theme.Muted.Render(padOrTrim("  "+m.tr("rail.no_agents"), width)))
	} else {
		for index, agent := range activeAgents {
			if index == 4 {
				rows = append(rows, m.theme.Muted.Render(padOrTrim("  "+m.tr("rail.more", map[string]string{"count": fmt.Sprint(len(activeAgents) - index)}), width)))
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
				rows = append(rows, m.theme.Muted.Render(padOrTrim("  "+m.tr("rail.more", map[string]string{"count": fmt.Sprint(len(m.mcpServers) - index)}), width)))
				break
			}
			row := fmt.Sprintf("  %s %s · %s", stateMark(server.State), server.Name, m.mcpConnectionState(server.State))
			rows = append(rows, m.stateStyle(server.State).Render(padOrTrim(row, width)))
		}
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return strings.Join(rows[:height], "\n")
}

func (m AppModel) mcpConnectionState(state string) string {
	switch strings.ToLower(state) {
	case "ready":
		return m.tr("mcp.status.connected")
	case "connecting":
		return m.tr("mcp.status.connecting")
	case "degraded":
		return m.tr("mcp.status.degraded")
	case "disabled":
		return m.tr("mcp.status.disabled")
	case "stopped":
		return m.tr("mcp.status.disconnected")
	default:
		return m.displayState(state)
	}
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
	rows := []string{m.renderHelpStrip(width)}
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
		return renderSurface(m.theme.RuntimeStrip, padStyledLine(joinSides(status, model+" ", width), width))
	}
	return renderSurface(m.theme.RuntimeStrip, padStyledLine(joinSides(status, truncateStyledFallback(model, max(10, width/3))+" ", width), width))
}

func (m AppModel) renderContextStrip(width int) string {
	if width <= 0 {
		return ""
	}
	// Split primary occupancy from cache so the strip reads as two facts, not one blob.
	primary := m.renderContextPrimary(max(12, width*2/3))
	cache := m.renderCacheSummary(max(8, width/3))
	if cache == "" {
		return renderSurface(m.theme.ContextStrip, padStyledLine(padLeft(primary, 1), width))
	}
	return renderSurface(m.theme.ContextStrip, padStyledLine(joinSides(padLeft(primary, 1), cache+" ", width), width))
}

func (m AppModel) renderHelpStrip(width int) string {
	return renderSurface(m.theme.HelpStrip, padStyledLine(m.renderHelpStripContent(width), width))
}

func (m AppModel) renderHelpStripContent(width int) string {
	items := m.helpItems(width)
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, m.theme.HelpKey.Render(item.key)+m.theme.HelpDesc.Render(":"+item.desc))
	}
	content := " " + strings.Join(parts, m.theme.MetaDivider.Render("  │  "))
	return truncateStyledFallback(content, width)
}

type helpItem struct {
	key  string
	desc string
}

func (m AppModel) helpItems(width int) []helpItem {
	if m.focus == focusTodo {
		action := m.tr("todo.footer.hide")
		if m.todoHideCompleted {
			action = m.tr("todo.footer.show")
		}
		items := []helpItem{
			{key: "h", desc: action},
			{key: "?", desc: m.tr("footer.desc.help")},
		}
		if width < 36 {
			return items[:1]
		}
		return items
	}
	firstItem := helpItem{key: m.tr("footer.key.drag"), desc: m.tr("footer.desc.copy")}
	if m.isRunning() {
		firstItem = helpItem{key: "Ctrl+C", desc: m.tr("footer.desc.cancel")}
	}
	all := []helpItem{
		firstItem,
		{key: "Shift+Tab", desc: m.tr("footer.desc.approval")},
		{key: "Ctrl+P", desc: m.tr("footer.desc.commands")},
		{key: "?", desc: m.tr("footer.desc.help")},
	}
	switch {
	case width >= 88:
		return all
	case width >= 62:
		return all[:3]
	case width >= 36:
		return all[:2]
	default:
		return all[:1]
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
			status += " " + m.theme.Error.Render(m.tr("status.transcript_details"))
		} else {
			status += " " + m.theme.Error.Render(m.errorBanner)
		}
	}
	return status
}

func (m AppModel) renderModelCluster(includeReasoning bool) string {
	model := m.provider + "/" + first(m.model, m.tr("value.no_model"))
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
	used           int
	limit          int
	percentage     float64
	contextLabel   string
	cacheLabel     string
	cache          string
	cacheRateOnly  string
	compactCache   string
	reported       bool
	calibrated     bool
	detailSuffix   string
	estimated      bool
	categoryTokens map[app.ContextCategory]int
}

func saturatingTokenSum(left, right int) int {
	left, right = max(0, left), max(0, right)
	if left > int(^uint(0)>>1)-right {
		return int(^uint(0) >> 1)
	}
	return left + right
}

func (m AppModel) contextMetrics() contextMetrics {
	categoryTokens := make(map[app.ContextCategory]int, 6)
	for _, contribution := range m.contextProfile.Contributions {
		categoryTokens[contribution.Category] = saturatingTokenSum(categoryTokens[contribution.Category], contribution.Tokens)
	}
	outputTokens := m.usage.OutputTokens
	profileReported := m.contextProfile.ReportedInputTokens > 0
	if m.contextProfile.Source == "team_request" {
		outputTokens = m.contextProfile.ReportedOutputTokens
	}
	categoryTokens[app.ContextCategoryConversation] = saturatingTokenSum(categoryTokens[app.ContextCategoryConversation], outputTokens)
	usageKnown := m.contextProfile.Source != "team_request" && m.usage.InputTokens > 0
	reported := profileReported || (usageKnown && m.usage.ContextReported)
	used := saturatingTokenSum(m.contextProfile.TotalTokens(), outputTokens)
	if profileReported {
		used = saturatingTokenSum(m.contextProfile.ReportedInputTokens, m.contextProfile.ReportedOutputTokens)
	} else if usageKnown {
		used = saturatingTokenSum(m.usage.InputTokens, m.usage.OutputTokens)
	}
	limit := m.usage.ContextLimit
	metrics := contextMetrics{
		used:           used,
		limit:          limit,
		estimated:      !reported && (m.contextProfile.Estimated || usageKnown),
		categoryTokens: categoryTokens,
		reported:       reported,
		calibrated:     profileReported || usageKnown,
		contextLabel:   m.tr("footer.context"),
		cacheLabel:     m.tr("footer.cache"),
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
	if m.showsCacheWrite() {
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
		if !m.usesAutomaticPromptCache() && m.usage.CompactionCacheWrite > 0 {
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
		if !m.usesAutomaticPromptCache() && m.usage.TeamCacheWrite > 0 {
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
	barWidth := 12
	if width >= 44 {
		barWidth = 16
	}
	if width < 28 {
		barWidth = 8
	}
	estimateMark := ""
	if metrics.estimated {
		estimateMark = "~"
	}
	barPlain := contextProgressBar(metrics.used, metrics.limit, barWidth)
	compactBar := contextProgressBar(metrics.used, metrics.limit, 8)
	candidates := []struct {
		text     string
		barPlain string
		barWidth int
	}{
		// Occupancy only — cache lives on the right of the strip.
		{fmt.Sprintf("%s %s  %s / %s  ·  %s%.1f%%", metrics.contextLabel, barPlain, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage), barPlain, barWidth},
		{fmt.Sprintf("%s %s %s/%s %s%.0f%%", metrics.contextLabel, compactBar, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage), compactBar, 8},
		{fmt.Sprintf("%s %s/%s %s%.0f%%", metrics.contextLabel, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage), "", 0},
		{fmt.Sprintf("%s %s%.0f%%", metrics.contextLabel, estimateMark, metrics.percentage), "", 0},
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
	barWidth := 16
	if width < 52 {
		barWidth = 8
	}
	estimateMark := ""
	if metrics.estimated {
		estimateMark = "~"
	}
	barPlainWide := contextProgressBar(metrics.used, metrics.limit, barWidth)
	barPlainMid := contextProgressBar(metrics.used, metrics.limit, 10)
	// Keep the dock strip readable: occupancy + cache only. Technical extras live in /status.
	candidates := []struct {
		text     string
		barPlain string
		barWidth int
	}{
		{fmt.Sprintf("%s %s  %s / %s  ·  %s%.1f%%  ·  %s", metrics.contextLabel, barPlainWide, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage, metrics.cache), barPlainWide, barWidth},
		{fmt.Sprintf("%s %s %s / %s · %s%.1f%% · %s", metrics.contextLabel, barPlainMid, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage, metrics.cache), barPlainMid, 10},
		{fmt.Sprintf("%s %s/%s · %s%.1f%% · %s", metrics.contextLabel, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage, metrics.cacheRateOnly), "", 0},
		{fmt.Sprintf("%s %s/%s %s%.0f%% C%s", metrics.contextLabel, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage, metrics.compactCache), "", 0},
		{fmt.Sprintf("%s %s%.0f%%", metrics.contextLabel, estimateMark, metrics.percentage), "", 0},
	}
	return m.renderContextCandidate(width, metrics, candidates)
}

func (m AppModel) statusReportLines() []string {
	metrics := m.contextMetrics()
	// Lead with the dense counters users open /status to read, then identity/context.
	lines := []string{m.tr("overlay.status.section.diagnostics")}
	if metrics.detailSuffix == "" && m.usage.UncachedInputTokens == 0 && m.usage.ReasoningTokens == 0 &&
		!m.showsCacheWrite() && m.usage.CompactionInput == 0 && m.usage.TeamInput == 0 &&
		m.usage.LastRequestKind == "" && m.usage.LastTransport == "" &&
		!m.usesAutomaticPromptCache() && !m.usage.CachePrefixDegraded {
		lines = append(lines, "  "+m.tr("overlay.status.empty_diagnostics"))
	} else {
		if m.usesAutomaticPromptCache() {
			lines = append(lines, "  "+m.tr("overlay.status.field.cache_model")+": "+m.tr("overlay.status.cache_model.automatic"))
		} else if m.usage.CacheModel == "write-tokens" || m.provider == "chatgpt" {
			lines = append(lines, "  "+m.tr("overlay.status.field.cache_model")+": "+m.tr("overlay.status.cache_model.write_tokens"))
		}
		if m.usage.CachePrefixDegraded {
			reason := m.usage.CachePrefixReason
			if reason == "" {
				reason = m.tr("overlay.status.cache_prefix.degraded_default")
			}
			lines = append(lines, "  "+m.tr("overlay.status.field.cache_prefix")+": "+reason)
		}
		if m.usage.UncachedInputTokens > 0 {
			lines = append(lines, fmt.Sprintf("  %s (U): %s", m.tr("overlay.status.field.uncached"), formatTokens(m.usage.UncachedInputTokens)))
		}
		if m.showsCacheWrite() {
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
			if !m.usesAutomaticPromptCache() && m.usage.CompactionCacheWrite > 0 {
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
			if !m.usesAutomaticPromptCache() && m.usage.TeamCacheWrite > 0 {
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
		estimateMark := ""
		if metrics.estimated {
			estimateMark = "~"
		}
		lines = append(lines,
			fmt.Sprintf("  %s: %s%s / %s (%s%.1f%%)", m.tr("overlay.status.field.occupancy"), estimateMark, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage),
			"  "+m.tr("overlay.status.field.cache")+": "+metrics.cache,
		)
	}
	lines = append(lines, "Shell processes")
	shells := activeShellExecutions(m.runtime)
	if len(shells) == 0 {
		lines = append(lines, "  none")
	} else {
		for _, shell := range shells {
			owner := first(shell.AgentID, "unknown")
			process := fmt.Sprintf("pid %d", shell.PID)
			if shell.PGID != 0 {
				process += fmt.Sprintf(" / pgid %d", shell.PGID)
			} else if shell.JobID != "" {
				process += " / job " + shell.JobID
			}
			deadline := "none"
			if !shell.Deadline.IsZero() {
				deadline = shell.Deadline.Format("15:04:05")
			}
			lines = append(lines, fmt.Sprintf("  %s · run %s · call %s · %s · %s · deadline %s · %d bytes · %s",
				owner, first(shell.RunID, "unknown"), shell.ToolCallID, process, first(shell.State, "running"), deadline,
				shell.OutputBytes, shell.CommandHash[:min(12, len(shell.CommandHash))]))
		}
	}
	return lines
}

var contextCategoryOrder = [...]app.ContextCategory{
	app.ContextCategoryCore,
	app.ContextCategorySkills,
	app.ContextCategoryBuiltinTools,
	app.ContextCategoryMCP,
	app.ContextCategoryConversation,
	app.ContextCategoryOther,
}

func allocateProportional(values []int, target int) []int {
	allocated := make([]int, len(values))
	if target <= 0 || len(values) == 0 {
		return allocated
	}
	total := 0
	for _, value := range values {
		total = saturatingTokenSum(total, value)
	}
	if total <= 0 {
		allocated[len(allocated)-1] = target
		return allocated
	}
	remainders := make([]int64, len(values))
	assigned := 0
	for index, value := range values {
		high, low := bits.Mul64(uint64(max(0, value)), uint64(target))
		quotient, remainder := bits.Div64(high, low, uint64(total))
		allocated[index] = int(quotient)
		remainders[index] = int64(remainder)
		assigned = saturatingTokenSum(assigned, allocated[index])
	}
	for assigned < target {
		best := 0
		for index := 1; index < len(remainders); index++ {
			if remainders[index] > remainders[best] {
				best = index
			}
		}
		allocated[best]++
		remainders[best] = -1
		assigned++
	}
	return allocated
}

func normalizedContextCategoryTotals(totals map[app.ContextCategory]int, target int) map[app.ContextCategory]int {
	values := make([]int, len(contextCategoryOrder))
	for index, category := range contextCategoryOrder {
		values[index] = totals[category]
	}
	allocated := allocateProportional(values, target)
	normalized := make(map[app.ContextCategory]int, len(contextCategoryOrder))
	for index, category := range contextCategoryOrder {
		normalized[category] = allocated[index]
	}
	return normalized
}

func normalizedContextContributions(contributions []app.ContextContribution, totals map[app.ContextCategory]int) []app.ContextContribution {
	normalized := append([]app.ContextContribution(nil), contributions...)
	for _, category := range contextCategoryOrder {
		indices := make([]int, 0)
		values := make([]int, 0)
		for index, contribution := range normalized {
			if contribution.Category == category {
				indices = append(indices, index)
				values = append(values, contribution.Tokens)
			}
		}
		allocated := allocateProportional(values, totals[category])
		for index, contributionIndex := range indices {
			normalized[contributionIndex].Tokens = allocated[index]
		}
	}
	return normalized
}

func contextProfileContributionHash(profile app.ContextProfile) uint64 {
	const (
		offset64 = uint64(14695981039346656037)
		prime64  = uint64(1099511628211)
	)
	hash := offset64
	mixString := func(value string) {
		for index := range len(value) {
			hash ^= uint64(value[index])
			hash *= prime64
		}
		hash ^= 0xff
		hash *= prime64
	}
	mixInt := func(value int) {
		number := uint64(value)
		for range 8 {
			hash ^= number & 0xff
			hash *= prime64
			number >>= 8
		}
	}
	for _, contribution := range profile.Contributions {
		mixString(string(contribution.Category))
		mixString(contribution.Name)
		mixInt(contribution.Tokens)
	}
	return hash
}

func (m AppModel) contextReportLines() []string {
	cache := m.contextReportCache
	hash := contextProfileContributionHash(m.contextProfile)
	language := m.catalog.Language()
	if cache != nil &&
		cache.valid &&
		cache.usage == m.usage &&
		cache.language == language &&
		cache.profileSource == m.contextProfile.Source &&
		cache.profileEstimated == m.contextProfile.Estimated &&
		cache.profileReportedInput == m.contextProfile.ReportedInputTokens &&
		cache.profileReportedOutput == m.contextProfile.ReportedOutputTokens &&
		cache.profileContributionCount == len(m.contextProfile.Contributions) &&
		cache.profileContributionHash == hash &&
		cache.profileError == m.contextProfileError {
		return cache.lines
	}

	lines := m.buildContextReportLines()
	if cache != nil {
		cache.valid = true
		cache.usage = m.usage
		cache.language = language
		cache.profileSource = m.contextProfile.Source
		cache.profileEstimated = m.contextProfile.Estimated
		cache.profileReportedInput = m.contextProfile.ReportedInputTokens
		cache.profileReportedOutput = m.contextProfile.ReportedOutputTokens
		cache.profileContributionCount = len(m.contextProfile.Contributions)
		cache.profileContributionHash = hash
		cache.profileError = m.contextProfileError
		cache.lines = lines
		cache.wrappedWidth = 0
		cache.wrappedLines = nil
	}
	return lines
}

func (m AppModel) contextOverlayDescriptionLines(width int) []string {
	width = max(1, width)
	lines := m.contextReportLines()
	cache := m.contextReportCache
	if cache != nil && cache.wrappedWidth == width && cache.wrappedLines != nil {
		return cache.wrappedLines
	}
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapText(line, width)...)
	}
	if cache != nil {
		cache.wrappedWidth = width
		cache.wrappedLines = wrapped
	}
	return wrapped
}

func (m AppModel) buildContextReportLines() []string {
	metrics := m.contextMetrics()
	estimateMark := ""
	if metrics.estimated {
		estimateMark = "~"
	}
	lines := make([]string, 0, len(m.contextProfile.Contributions)+16)
	if metrics.limit > 0 {
		lines = append(lines,
			m.styledContextProgressBar(metrics, 40),
			fmt.Sprintf("%s%s / %s · %s%.1f%%", estimateMark, formatTokens(metrics.used), formatTokens(metrics.limit), estimateMark, metrics.percentage),
		)
	} else {
		lines = append(lines, fmt.Sprintf("%s%s · %s", estimateMark, formatTokens(metrics.used), m.tr("footer.unavailable")))
	}
	if m.contextProfileError != "" {
		lines = append(lines, m.theme.Error.Render(m.tr("overlay.context.profile_error", map[string]string{"error": m.contextProfileError})))
	}
	sourceKey := "overlay.context.source.estimated"
	if metrics.reported {
		sourceKey = "overlay.context.source.reported"
	}
	lines = append(lines, m.theme.Muted.Render(m.tr(sourceKey)), "", m.tr("overlay.context.section.categories"))

	totals := make(map[app.ContextCategory]int, len(metrics.categoryTokens))
	totalEstimated := 0
	for category, tokens := range metrics.categoryTokens {
		totals[category] = max(0, tokens)
		totalEstimated = saturatingTokenSum(totalEstimated, tokens)
	}
	if totalEstimated < metrics.used {
		totals[app.ContextCategoryOther] += metrics.used - totalEstimated
	}
	if metrics.calibrated && totalEstimated > metrics.used {
		totals = normalizedContextCategoryTotals(totals, metrics.used)
	}
	for _, category := range contextCategoryOrder {
		tokens := totals[category]
		if tokens == 0 {
			continue
		}
		percentage := ""
		if metrics.limit > 0 {
			percentage = fmt.Sprintf(" · ~%.1f%%", float64(tokens)*100/float64(metrics.limit))
		}
		marker := m.contextCategoryStyle(category).Render("■")
		lines = append(lines, fmt.Sprintf("%s %s  ~%s%s", marker, m.contextCategoryLabel(category), formatTokens(tokens), percentage))
	}

	contributions := append([]app.ContextContribution(nil), m.contextProfile.Contributions...)
	if m.contextProfile.Source == "team_request" && m.contextProfile.ReportedOutputTokens > 0 {
		contributions = append(contributions, app.ContextContribution{
			Category: app.ContextCategoryConversation,
			Name:     "current_output",
			Tokens:   m.contextProfile.ReportedOutputTokens,
		})
	} else if m.contextProfile.Source != "team_request" && m.usage.OutputTokens > 0 {
		contributions = append(contributions, app.ContextContribution{
			Category: app.ContextCategoryConversation,
			Name:     "current_output",
			Tokens:   m.usage.OutputTokens,
		})
	}
	if totalEstimated < metrics.used {
		contributions = append(contributions, app.ContextContribution{
			Category: app.ContextCategoryOther,
			Name:     "provider_framing",
			Tokens:   metrics.used - totalEstimated,
		})
	}
	if metrics.calibrated && totalEstimated > metrics.used {
		contributions = normalizedContextContributions(contributions, totals)
	}
	categoryRank := make(map[app.ContextCategory]int, len(contextCategoryOrder))
	for index, category := range contextCategoryOrder {
		categoryRank[category] = index
	}
	sort.SliceStable(contributions, func(i, j int) bool {
		if contributions[i].Category != contributions[j].Category {
			return categoryRank[contributions[i].Category] < categoryRank[contributions[j].Category]
		}
		if contributions[i].Tokens != contributions[j].Tokens {
			return contributions[i].Tokens > contributions[j].Tokens
		}
		return contributions[i].Name < contributions[j].Name
	})
	if len(contributions) > 0 {
		lines = append(lines, "", m.tr("overlay.context.section.details"))
		currentCategory := app.ContextCategory("")
		for _, contribution := range contributions {
			if contribution.Tokens <= 0 {
				continue
			}
			if contribution.Category != currentCategory {
				currentCategory = contribution.Category
				lines = append(lines, m.contextCategoryStyle(currentCategory).Bold(true).Render(m.contextCategoryLabel(currentCategory)))
			}
			lines = append(lines, fmt.Sprintf("  %s · ~%s", m.contextContributionLabel(contribution), formatTokens(contribution.Tokens)))
		}
	}
	return lines
}

func (m AppModel) contextCategoryLabel(category app.ContextCategory) string {
	return m.tr("overlay.context.category." + string(category))
}

func sanitizeContextLabel(value string) string {
	value = ansi.Strip(value)
	value = strings.Map(func(current rune) rune {
		if unicode.IsControl(current) {
			return -1
		}
		return current
	}, value)
	value = strings.TrimSpace(value)
	if value == "" {
		return "—"
	}
	return ansi.Truncate(value, 96, "…")
}

func (m AppModel) contextContributionLabel(contribution app.ContextContribution) string {
	return sanitizeContextLabel(m.rawContextContributionLabel(contribution))
}

func (m AppModel) rawContextContributionLabel(contribution app.ContextContribution) string {
	switch contribution.Name {
	case "azem.core_instructions":
		return m.tr("overlay.context.item.core")
	case "runtime.overhead":
		return m.tr("overlay.context.item.skill_runtime")
	case "catalog.overhead":
		return m.tr("overlay.context.item.skill_catalog")
	case "current_output":
		return m.tr("overlay.context.item.current_output")
	case "provider_framing":
		return m.tr("overlay.context.item.provider_framing")
	case app.ContextContributionRemainingItems:
		return m.tr("overlay.context.item.remaining")
	}
	if strings.HasPrefix(contribution.Name, "mcp__") {
		parts := strings.Split(contribution.Name, "__")
		if len(parts) >= 3 {
			return parts[1] + " / " + strings.Join(parts[2:], "__")
		}
	}
	if strings.HasPrefix(contribution.Name, "message:") {
		parts := strings.Split(contribution.Name, ":")
		if len(parts) == 3 {
			return m.tr("overlay.context.item.message", map[string]string{
				"role":  m.tr("overlay.context.role." + parts[1]),
				"count": parts[2],
			})
		}
	}
	if strings.HasPrefix(contribution.Name, "tool_result:") {
		return m.tr("overlay.context.item.tool_result", map[string]string{"name": strings.TrimPrefix(contribution.Name, "tool_result:")})
	}
	if strings.HasPrefix(contribution.Name, "compaction:") {
		return m.tr("overlay.context.item.compaction")
	}
	if strings.HasPrefix(contribution.Name, "system:") {
		return m.tr("overlay.context.item.system", map[string]string{"count": strings.TrimPrefix(contribution.Name, "system:")})
	}
	return contribution.Name
}

func activeShellExecutions(runtime Runtime) []agentservice.ShellExecutionSnapshot {
	provider, ok := runtime.(interface {
		ActiveShellExecutions() []agentservice.ShellExecutionSnapshot
	})
	if !ok {
		return nil
	}
	return provider.ActiveShellExecutions()
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
},
) string {
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
	return tone.Render(parts[0]) + m.styledContextProgressBar(metrics, chosen.barWidth) + tone.Render(parts[1])
}

func contextProgressBar(used int, limit int, width int) string {
	if limit <= 0 || width <= 0 {
		return ""
	}
	clamped := min(max(0, used), limit)
	filled := int((int64(clamped)*int64(width) + int64(limit)/2) / int64(limit))
	if clamped > 0 && filled == 0 {
		filled = 1
	}
	return "[" + strings.Repeat("■", filled) + strings.Repeat("·", width-filled) + "]"
}

func (m AppModel) styledContextProgressBar(metrics contextMetrics, width int) string {
	plain := contextProgressBar(metrics.used, metrics.limit, width)
	if plain == "" || width <= 0 {
		return plain
	}
	clamped := min(max(0, metrics.used), metrics.limit)
	filled := int((int64(clamped)*int64(width) + int64(metrics.limit)/2) / int64(metrics.limit))
	if clamped > 0 && filled == 0 {
		filled = 1
	}
	categories := contextCategoryOrder
	tokens := make([]int, len(categories))
	total := 0
	for index, category := range categories {
		tokens[index] = max(0, metrics.categoryTokens[category])
		total = saturatingTokenSum(total, tokens[index])
	}
	if total < metrics.used {
		tokens[len(tokens)-1] += metrics.used - total
		total = metrics.used
	}
	if total == 0 && filled > 0 {
		tokens[len(tokens)-1] = metrics.used
		total = metrics.used
	}
	cells := allocateProportional(tokens, filled)
	var bar strings.Builder
	bar.WriteString(m.theme.MetaDivider.Render("["))
	for index, count := range cells {
		if count > 0 {
			bar.WriteString(m.contextCategoryStyle(categories[index]).Render(strings.Repeat("■", count)))
		}
	}
	bar.WriteString(m.theme.BarEmpty.Render(strings.Repeat("·", width-filled)))
	bar.WriteString(m.theme.MetaDivider.Render("]"))
	return bar.String()
}

func (m AppModel) contextCategoryStyle(category app.ContextCategory) lipgloss.Style {
	switch category {
	case app.ContextCategoryCore:
		return m.theme.BarCore
	case app.ContextCategorySkills:
		return m.theme.BarSkills
	case app.ContextCategoryBuiltinTools:
		return m.theme.BarBuiltin
	case app.ContextCategoryMCP:
		return m.theme.BarMCP
	case app.ContextCategoryConversation:
		return m.theme.BarChat
	default:
		return m.theme.BarOther
	}
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
	helpText := strings.Join([]string{m.tr("footer.drag_copy"), m.tr("footer.paste_image"), m.tr("footer.help.approval"), m.tr("footer.help.reasoning"), m.tr("footer.help.commands"), m.tr("status.help")}, "  ")
	if width < 112 {
		helpText = strings.Join([]string{m.tr("footer.drag_copy"), m.tr("footer.paste_image"), m.tr("footer.help.approval"), m.tr("footer.help.reasoning"), m.tr("status.help")}, "  ")
	}
	if width < 86 {
		helpText = strings.Join([]string{m.tr("footer.drag_copy"), m.tr("footer.help.approval"), m.tr("status.help")}, "  ")
	}
	if width < 64 {
		helpText = m.tr("footer.drag_copy")
	}
	return helpText
}
