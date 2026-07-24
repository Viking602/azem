package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	backgroundservice "github.com/Viking602/azem/internal/background"
	"github.com/Viking602/azem/internal/i18n"
	"github.com/Viking602/azem/internal/session"
)

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
		rows = append(rows, m.theme.Muted.Render(padOrTrim("   "+m.tr("overlay.suggestions.footer"), width)))
	}
	return strings.Join(rows, "\n")
}

func (m AppModel) renderOverlay(width int, height int) string {
	if width < 6 || height < 5 {
		title, _ := m.overlayHeading()
		if m.overlay == OverlayAgentDetail || m.overlay == OverlayMCPDetail || m.overlay == OverlayBackgroundDetail {
			title, _ = m.overlayHeading()
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
	if m.overlay == OverlayMCPDetail {
		return m.renderMCPDetailOverlay(width, height)
	}
	if m.overlay == OverlayBackgroundDetail {
		return m.renderBackgroundDetailOverlay(width, height)
	}
	maxBoxWidth := 82
	if m.overlay == OverlayAgentTypes || m.overlay == OverlayPersonas || m.overlay == OverlaySkills || m.overlay == OverlayMemory || m.overlay == OverlayRecap || m.overlay == OverlayModelRoutes || m.overlay == OverlayStatus || m.overlay == OverlayBackground {
		maxBoxWidth = 110
	}
	boxWidth := min(maxBoxWidth, max(3, width-2))
	innerWidth := max(1, boxWidth-2)
	maxInnerHeight := 20
	if m.overlay == OverlayStatus {
		maxInnerHeight = 28
	}
	innerHeight := max(1, min(height-2, maxInnerHeight))
	title, subtitle := m.overlayHeading()
	var description []string
	if m.overlay != OverlayRecap {
		description = m.overlayDescription()
	}
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
	diffDescription := false
	if m.overlay == OverlayDiff {
		if block, ok := m.selectedDiff(); ok {
			descriptionLines = m.renderDiffRows(block.Content, max(4, innerWidth-2), " ")
			diffDescription = true
		}
	}
	if m.overlay == OverlayRecap {
		descriptionLines = m.recapDescriptionLines(max(4, innerWidth-4))
	} else if !diffDescription {
		for _, line := range description {
			descriptionLines = append(descriptionLines, wrapText(line, max(4, innerWidth-4))...)
		}
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
	rows = append(rows, m.boxRow(" ◈ "+strings.ToUpper(title), innerWidth, m.theme.OverlayTitle, false))
	if subtitle != "" {
		rows = append(rows, m.boxRow(" "+subtitle, innerWidth, m.theme.Muted, false))
	}
	if m.overlay == OverlayModel {
		rows = append(rows, m.boxRow(" "+m.modelSearch.View(), innerWidth, m.theme.Assistant, false))
	}
	rows = append(rows, m.boxRow(" "+strings.Repeat("─", max(0, innerWidth-2)), innerWidth, m.theme.Border, false))
	for _, line := range descriptionLines {
		if diffDescription {
			rows = append(rows, m.boxRow(line, innerWidth, lipgloss.NewStyle(), false))
		} else {
			rows = append(rows, m.boxRow(" "+line, innerWidth, m.theme.Assistant, false))
		}
	}
	if stickyGroup != "" {
		rows = append(rows, m.boxRow(" "+strings.ToUpper(providerDisplayName(stickyGroup)), innerWidth, m.theme.OverlayGroup, false))
	}
	for _, renderRow := range renderRows[start:end] {
		if renderRow.OptionIndex < 0 {
			rows = append(rows, m.boxRow(" "+strings.ToUpper(providerDisplayName(renderRow.Group)), innerWidth, m.theme.OverlayGroup, false))
			continue
		}
		option := options[renderRow.OptionIndex]
		text := "   " + option.Label
		if option.State != "" {
			text += "  " + strings.ToUpper(m.displayState(option.State))
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
	rows = append(rows, m.boxRow(" "+footer, innerWidth, m.theme.OverlayFooter, false))
	if len(rows) > innerHeight {
		rows = append(rows[:innerHeight-1], m.boxRow(" "+footer, innerWidth, m.theme.OverlayFooter, false))
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

func (m AppModel) recapDescriptionLines(contentWidth int) []string {
	if m.recap == nil {
		return []string{m.tr("overlay.recap.empty")}
	}
	cache := m.recapLayout
	language := m.catalog.Language()
	if cache != nil && cache.initialized && cache.contentWidth == contentWidth && cache.language == language && cache.value == *m.recap {
		return cache.lines
	}
	description := []string{
		m.tr("overlay.recap.goal") + ": " + first(m.recap.Goal, "—"),
		m.tr("overlay.recap.summary") + ": " + first(m.recap.Summary, "—"),
		m.tr("overlay.recap.open_items") + ": " + first(m.recap.OpenItems, "—"),
		fmt.Sprintf("%s: %s · r%d", m.tr("overlay.recap.boundary"), first(m.recap.CoveredBoundary, "—"), m.recap.Revision),
	}
	lines := make([]string, 0, len(description))
	for _, line := range description {
		lines = append(lines, wrapText(line, contentWidth)...)
	}
	if cache != nil {
		cache.contentWidth = contentWidth
		cache.language = language
		cache.value = *m.recap
		cache.lines = lines
		cache.initialized = true
	}
	return lines
}

func (m AppModel) recapScrollLimit() int {
	if m.width < 6 || m.height < 5 {
		return 0
	}
	boxWidth := min(110, max(3, m.width-2))
	innerWidth := max(1, boxWidth-2)
	innerHeight := max(1, min(m.height-2, 20))
	contentRows := innerHeight - 4
	if _, subtitle := m.overlayHeading(); subtitle == "" {
		contentRows++
	}
	visibleRows := max(1, contentRows)
	lines := m.recapDescriptionLines(max(4, innerWidth-4))
	return max(0, len(lines)-visibleRows)
}

func (m *AppModel) scrollRecap(delta int) {
	m.overlayScroll = min(m.recapScrollLimit(), max(0, m.overlayScroll+delta))
}

func (m AppModel) renderAgentDetailOverlay(width, height int) string {
	boxWidth := min(96, max(3, width-2))
	innerWidth := max(1, boxWidth-2)
	innerHeight := max(1, min(height-2, 28))
	agent, found := m.agentDetail()
	content := make([]string, 0)
	if !found {
		content = append(content, m.tr("overlay.agent_detail.unavailable"))
	} else {
		content = append(content,
			fmt.Sprintf("%s · %s", first(agent.Role, m.tr("block.subagent")), strings.ToUpper(first(agent.State, m.tr("value.unknown")))),
			fmt.Sprintf("ID %s", agent.ID),
		)
		if agent.Description != "" {
			content = append(content, m.tr("agent.goal", map[string]string{"value": agent.Description}))
		}
		if agent.Activity != "" {
			content = append(content, m.tr("agent.activity", map[string]string{"value": agent.Activity}))
		}
		content = append(content,
			m.tr("agent.model", map[string]string{"model": first(agent.Model, m.tr("value.inherit")), "capability": first(agent.CapabilityMode, m.tr("value.inherit")), "background": fmt.Sprint(agent.Background)}),
			m.tr("agent.isolation", map[string]string{"value": first(agent.Isolation, m.tr("value.none")), "requested": first(agent.RequestedIsolation, m.tr("value.none"))}),
			m.tr("agent.stats", map[string]string{"tools": strconv.Itoa(agent.ToolCalls), "turns": strconv.Itoa(agent.Turns), "tokens": strconv.Itoa(agent.TokensUsed), "seconds": fmt.Sprintf("%.1f", float64(agent.ElapsedMS)/1000)}),
		)
		if agent.CWD != "" {
			content = append(content, "CWD: "+agent.CWD)
		}
		if agent.Warning != "" {
			content = append(content, m.tr("agent.warning", map[string]string{"value": agent.Warning}))
		}
		if agent.WorktreePath != "" {
			content = append(content, "Worktree: "+agent.WorktreePath)
		}
		content = append(content, strings.Repeat("─", max(0, innerWidth-4)))
		if len(agent.Blocks) == 0 {
			content = append(content, m.tr("overlay.agent_detail.empty"))
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
		m.boxRow(" ◈ "+strings.ToUpper(m.tr("overlay.agent_detail.title")), innerWidth, m.theme.OverlayTitle, false),
		m.boxRow(" "+first(agent.Description, agent.ID, m.tr("overlay.agent_detail.child_execution")), innerWidth, m.theme.Muted, false),
		m.boxRow(" "+strings.Repeat("─", max(0, innerWidth-2)), innerWidth, m.theme.Border, false),
	}
	for _, line := range content[start:end] {
		rows = append(rows, m.boxRow(" "+line, innerWidth, m.theme.Assistant, false))
	}
	for len(rows) < innerHeight-1 {
		rows = append(rows, m.boxRow("", innerWidth, m.theme.Assistant, false))
	}
	footer := m.boxRow(" "+m.overlayFooterForWidth(max(1, innerWidth-1)), innerWidth, m.theme.OverlayFooter, false)
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

func (m AppModel) renderMCPDetailOverlay(width, height int) string {
	boxWidth := min(96, max(3, width-2))
	innerWidth := max(1, boxWidth-2)
	innerHeight := max(1, min(height-2, 28))
	server, found := m.selectedMCPServer()
	content := make([]string, 0)
	subtitle := m.tr("overlay.mcp_detail.unavailable")
	if found {
		subtitle = server.Name + " · " + m.mcpConnectionState(server.State)
		content = append(content, m.tr("overlay.mcp_detail.summary", map[string]string{
			"state": m.mcpConnectionState(server.State), "count": strconv.Itoa(server.ToolCount),
		}))
		if server.Error != "" {
			content = append(content, m.tr("overlay.mcp_detail.error", map[string]string{"error": server.Error}))
		}
		content = append(content, strings.Repeat("─", max(0, innerWidth-4)))
		if len(server.Tools) == 0 {
			content = append(content, m.tr("overlay.mcp_detail.empty"))
		} else {
			for _, tool := range server.Tools {
				approval := m.tr("overlay.mcp_detail.no_approval")
				if tool.RequiresApproval {
					approval = m.tr("overlay.mcp_detail.approval")
				}
				content = append(content, "◆ "+tool.Name)
				content = append(content, "  "+m.tr("overlay.mcp_detail.governance", map[string]string{
					"effect": m.mcpToolEffect(tool.Effect), "approval": approval,
				}))
				for _, line := range wrapText(first(tool.Description, m.tr("overlay.mcp_detail.no_description")), max(4, innerWidth-6)) {
					content = append(content, "  "+line)
				}
			}
		}
	}
	rowsAvailable := max(1, innerHeight-4)
	start := min(max(0, m.overlayScroll), max(0, len(content)-rowsAvailable))
	end := min(len(content), start+rowsAvailable)
	rows := []string{
		m.boxRow(" ◈ "+strings.ToUpper(m.tr("overlay.mcp_detail.title")), innerWidth, m.theme.OverlayTitle, false),
		m.boxRow(" "+subtitle, innerWidth, m.theme.Muted, false),
		m.boxRow(" "+strings.Repeat("─", max(0, innerWidth-2)), innerWidth, m.theme.Border, false),
	}
	for _, line := range content[start:end] {
		rows = append(rows, m.boxRow(" "+line, innerWidth, m.theme.Assistant, false))
	}
	for len(rows) < innerHeight-1 {
		rows = append(rows, m.boxRow("", innerWidth, m.theme.Assistant, false))
	}
	footer := m.boxRow(" "+m.overlayFooterForWidth(max(1, innerWidth-1)), innerWidth, m.theme.OverlayFooter, false)
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

func (m AppModel) renderBackgroundDetailOverlay(width, height int) string {
	boxWidth := min(118, max(3, width-2))
	innerWidth := max(1, boxWidth-2)
	innerHeight := max(1, min(height-2, 32))
	process, found := m.selectedBackgroundProcess()
	subtitle := m.tr("overlay.background_detail.unavailable")
	content := make([]string, 0, 16)
	if found {
		subtitle = fmt.Sprintf("%s · %s · PID %d", process.Name, strings.ToUpper(m.displayState(process.State)), process.PID)
		content = append(content,
			m.tr("background.command", map[string]string{"value": process.Command}),
			"CWD: "+process.CWD,
			m.tr("background.runtime", map[string]string{"id": process.ID, "started": process.StartedAt.Local().Format("2006-01-02 15:04:05"), "bytes": formatByteCount(process.LogBytes)}),
		)
		if process.Error != "" {
			content = append(content, m.tr("background.error", map[string]string{"value": process.Error}))
		}
		content = append(content, strings.Repeat("─", max(0, innerWidth-4)))
	}
	if m.backgroundLogs == nil || m.backgroundLogs.Process.ID != m.detailBackgroundID {
		content = append(content, m.tr("overlay.background_detail.loading"))
	} else {
		if m.backgroundLogs.Truncated {
			content = append(content, m.tr("overlay.background_detail.truncated"))
		}
		if len(m.backgroundLogs.Lines) == 0 {
			content = append(content, m.tr("overlay.background_detail.empty"))
		} else {
			content = append(content, m.backgroundLogs.Lines...)
		}
	}
	rowsAvailable := max(1, innerHeight-4)
	limit := max(0, len(content)-rowsAvailable)
	start := min(max(0, m.overlayScroll), limit)
	end := min(len(content), start+rowsAvailable)
	rows := []string{
		m.boxRow(" ◈ "+strings.ToUpper(m.tr("overlay.background_detail.title")), innerWidth, m.theme.OverlayTitle, false),
		m.boxRow(" "+subtitle, innerWidth, m.theme.Muted, false),
		m.boxRow(" "+strings.Repeat("─", max(0, innerWidth-2)), innerWidth, m.theme.Border, false),
	}
	for _, line := range content[start:end] {
		rows = append(rows, m.boxRow(" "+line, innerWidth, m.theme.Assistant, false))
	}
	for len(rows) < innerHeight-1 {
		rows = append(rows, m.boxRow("", innerWidth, m.theme.Assistant, false))
	}
	footer := m.boxRow(" "+m.overlayFooterForWidth(max(1, innerWidth-1)), innerWidth, m.theme.OverlayFooter, false)
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

func (m AppModel) backgroundLogScrollLimit() int {
	if m.backgroundLogs == nil || m.backgroundLogs.Process.ID != m.detailBackgroundID {
		return 0
	}
	innerHeight := max(1, min(m.height-2, 32))
	metadata := 4
	if m.backgroundLogs.Truncated {
		metadata++
	}
	return max(0, metadata+len(m.backgroundLogs.Lines)-max(1, innerHeight-4))
}

func (m AppModel) selectedBackgroundProcess() (backgroundservice.Process, bool) {
	for _, process := range m.background {
		if process.ID == m.detailBackgroundID {
			return process, true
		}
	}
	if m.backgroundLogs != nil && m.backgroundLogs.Process.ID == m.detailBackgroundID {
		return m.backgroundLogs.Process, true
	}
	return backgroundservice.Process{}, false
}

func formatByteCount(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	if value < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(value)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(value)/(1024*1024))
}

func (m AppModel) mcpToolEffect(effect string) string {
	switch effect {
	case "read_only", "write", "external_side_effect":
		return m.tr("effect." + effect)
	default:
		return first(effect, m.tr("value.unknown"))
	}
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
		return m.tr("overlay.help.title"), m.tr("overlay.help.subtitle")
	case OverlayStatus:
		return m.tr("overlay.status.title"), m.tr("overlay.status.subtitle")
	case OverlayCommand:
		return m.tr("overlay.command.title"), m.tr("overlay.command.subtitle")
	case OverlayProvider:
		if m.overlayPurpose == "login" {
			return m.tr("overlay.signin.title"), m.tr("overlay.signin.subtitle")
		}
		return m.tr("overlay.provider.title"), m.tr("overlay.provider.subtitle")
	case OverlayModel:
		count := m.modelCatalogCount()
		if count == 1 {
			return m.tr("overlay.model.title"), m.tr("overlay.model.catalogs", map[string]string{"count": "1"})
		}
		return m.tr("overlay.model.title"), m.tr("overlay.model.catalogs", map[string]string{"count": strconv.Itoa(count)})
	case OverlayModelRoutes:
		return m.tr("overlay.model_routes.title"), m.tr("overlay.model_routes.subtitle")
	case OverlaySkills:
		return m.tr("overlay.skills.title"), m.tr("overlay.skills.subtitle")
	case OverlayLanguage:
		return m.tr("overlay.language.title"), m.tr("overlay.language.subtitle")
	case OverlayReasoning:
		if m.pendingModelRoute != nil {
			return m.tr("overlay.model_routes.reasoning_title"), m.pendingModelRoute.Provider + "/" + m.pendingModelRoute.Model
		}
		if m.pendingSessionModel != nil {
			return m.tr("overlay.reasoning.title"), m.pendingSessionModel.Provider + "/" + first(m.pendingSessionModel.Model, m.tr("value.no_model")) + " · " + m.tr("overlay.reasoning.choose_next")
		}
		return m.tr("overlay.reasoning.title"), m.provider + "/" + first(m.model, m.tr("value.no_model")) + " · " + m.tr("overlay.reasoning.applied_next")
	case OverlaySessions:
		return m.tr("overlay.sessions.title"), m.tr("overlay.sessions.subtitle")
	case OverlayApproval:
		return m.tr("overlay.approval.title"), m.tr("overlay.approval.subtitle")
	case OverlayCancel:
		return m.tr("overlay.cancel.title"), m.tr("overlay.cancel.subtitle")
	case OverlayDiff:
		return m.tr("overlay.diff.title"), m.tr("overlay.diff.subtitle")
	case OverlayAgents:
		return m.tr("overlay.agents.title"), m.tr("overlay.agents.mode", map[string]string{"mode": strings.ToUpper(m.agentMode)})
	case OverlayTodos:
		return m.tr("overlay.todos.title"), m.tr("overlay.todos.subtitle")
	case OverlayMemory:
		return m.tr("overlay.memory.title"), m.tr("overlay.memory.subtitle")
	case OverlayRecap:
		return m.tr("overlay.recap.title"), m.tr("overlay.recap.subtitle")
	case OverlayAgentDetail:
		return m.tr("overlay.agent_detail.title"), m.tr("overlay.agent_detail.subtitle")
	case OverlayAgentTypes:
		return m.tr("overlay.agent_types.title"), m.tr("overlay.agent_types.subtitle")
	case OverlayPersonas:
		return m.tr("overlay.personas.title"), m.tr("overlay.personas.subtitle")
	case OverlayMCP:
		return m.tr("overlay.mcp.title"), m.tr("overlay.mcp.subtitle")
	case OverlayMCPDetail:
		server, ok := m.selectedMCPServer()
		if !ok {
			return m.tr("overlay.mcp_detail.title"), m.tr("overlay.mcp_detail.unavailable")
		}
		return m.tr("overlay.mcp_detail.title"), server.Name + " · " + m.mcpConnectionState(server.State)
	case OverlayBackground:
		return m.tr("overlay.background.title"), m.tr("overlay.background.subtitle")
	case OverlayBackgroundDetail:
		process, ok := m.selectedBackgroundProcess()
		if !ok {
			return m.tr("overlay.background_detail.title"), m.tr("overlay.background_detail.unavailable")
		}
		return m.tr("overlay.background_detail.title"), process.Name + " · " + m.displayState(process.State)
	case OverlayRecovery:
		return m.tr("overlay.recovery.title"), m.tr("overlay.recovery.subtitle")
	case OverlayError:
		return m.tr("overlay.error.title"), m.tr("overlay.error.subtitle")
	default:
		return "Azem", ""
	}
}

func (m AppModel) overlayDescription() []string {
	switch m.overlay {
	case OverlayModelRoutes:
		return []string{m.tr("overlay.model_routes.description")}
	case OverlayTodos:
		if len(m.todo.Phases) == 0 {
			return []string{m.tr("overlay.todos.empty")}
		}
		if m.todo.Goal != "" {
			return []string{m.todo.Goal}
		}
	case OverlayMemory:
		if len(m.memories) == 0 {
			return []string{m.tr("overlay.memory.empty")}
		}
	case OverlayRecap:
		if m.recap == nil {
			return []string{m.tr("overlay.recap.empty")}
		}
		return []string{
			m.tr("overlay.recap.goal") + ": " + first(m.recap.Goal, "—"),
			m.tr("overlay.recap.summary") + ": " + first(m.recap.Summary, "—"),
			m.tr("overlay.recap.open_items") + ": " + first(m.recap.OpenItems, "—"),
			fmt.Sprintf("%s: %s · r%d", m.tr("overlay.recap.boundary"), first(m.recap.CoveredBoundary, "—"), m.recap.Revision),
		}
	case OverlayHelp:
		return []string{
			m.tr("overlay.help.line1"), m.tr("overlay.help.line2"), m.tr("overlay.help.line3"), m.tr("overlay.help.line4"),
			"/login /logout /provider /models /skills /skill /new /sessions /resume /compact",
			"/team /agents /mcp /status /reconcile /cancel /help /quit",
		}
	case OverlayStatus:
		return m.statusReportLines()
	case OverlayProvider:
		if m.overlayPurpose == "login" {
			return []string{
				m.tr("overlay.signin.chatgpt"), m.tr("overlay.signin.grok"), m.tr("overlay.signin.existing"),
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
			path := first(shortenPath(diagnostic.Path, 32), m.tr("skill.root"))
			warnings = append(warnings, m.tr("skill.warning", map[string]string{"path": path, "detail": diagnostic.Message}))
		}
		if remaining := len(m.skillDiagnostics) - len(warnings); remaining > 0 {
			warnings = append(warnings, m.tr("skill.more_warnings", map[string]string{"count": strconv.Itoa(remaining)}))
		}
		return warnings
	case OverlayReasoning:
		if len(m.reasoningLevels()) == 0 {
			return []string{m.tr("overlay.reasoning.unsupported")}
		}
	case OverlaySessions:
		if len(m.sessions) == 0 {
			return []string{m.tr("overlay.sessions.empty")}
		}
		return []string{m.tr("overlay.sessions.instructions")}
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
		return []string{m.tr("overlay.cancel.description")}
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
	case OverlayBackground:
		if len(m.background) == 0 {
			return []string{m.tr("overlay.background.empty")}
		}
	case OverlayRecovery:
		if len(m.recovery) == 0 {
			return []string{m.tr("overlay.recovery.empty")}
		}
		return []string{m.tr("overlay.recovery.description")}
	case OverlayError:
		return []string{first(m.errorBanner, m.tr("overlay.error.stream_stopped"))}
	}
	return nil
}

func (m AppModel) overlayOptions() []overlayOption {
	switch m.overlay {
	case OverlayMemory:
		options := make([]overlayOption, 0, len(m.memories))
		for _, item := range m.memories {
			options = append(options, overlayOption{
				Label:  compactAgentActivity(item.Content),
				Detail: item.ID + " · " + item.Provenance + " · " + item.UpdatedAt.Local().Format("2006-01-02 15:04"),
				State:  fmt.Sprintf("%d", item.Importance),
			})
		}
		return options
	case OverlayModelRoutes:
		options := make([]overlayOption, 0, len(m.modelRoutes))
		for _, entry := range m.modelRoutes {
			label := entry.Role
			inherit := m.tr("overlay.model_routes.inherit_parent")
			if entry.Scope == "compaction" {
				label = m.tr("overlay.model_routes.compaction")
				inherit = m.tr("overlay.model_routes.inherit_active")
			}
			detail := inherit
			if entry.Route.Provider != "" || entry.Route.Model != "" || entry.Route.Reasoning != "" {
				detail = strings.Trim(strings.Join([]string{entry.Route.Provider, entry.Route.Model, entry.Route.Reasoning}, "/"), "/")
			}
			if entry.Label != "" && entry.Label != label {
				detail += " · " + entry.Label
			}
			options = append(options, overlayOption{Label: label, Detail: detail})
		}
		return options
	case OverlayTodos:
		options := []overlayOption{}
		itemNumber := 0
		for _, phase := range m.todo.Phases {
			for _, item := range phase.Items {
				itemNumber++
				if m.todoHideCompleted && item.Status == session.TodoCompleted {
					continue
				}
				status := m.todoDisplayStatus(item)
				detail := m.displayState(string(item.Status)) + " · " + item.ID
				if item.SubagentRunID != "" {
					for _, agent := range m.agents {
						if agent.ID == item.SubagentRunID {
							detail += " · " + m.tr("block.agent") + " " + m.displayState(agent.State)
						}
					}
				}
				label := fmt.Sprintf("%s  %d. %s", todoMark(status, m.animationFrame), itemNumber, item.Content)
				options = append(options, overlayOption{Group: phase.Title, Label: label, Detail: detail, State: string(status)})
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
			detail := m.tr("provider.not_signed_in")
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
				capabilities = append(capabilities, m.tr("model.context", map[string]string{"count": formatTokens(model.ContextWindow)}))
			}
			if model.SupportsTools {
				capabilities = append(capabilities, m.tr("model.tools"))
			}
			if model.SupportsReasoning {
				capabilities = append(capabilities, m.tr("model.reasoning"))
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
			source := first(shortenPath(entry.SourcePath, 30), m.tr("skill.source.disk"))
			if entry.Bundled {
				source = m.tr("skill.source.bundled")
			}
			resourceLabel := m.tr("skill.resources", map[string]string{"count": strconv.Itoa(entry.ResourceCount)})
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
			options = append(options, overlayOption{Label: level, Detail: m.reasoningDescription(level), State: state})
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
			{Label: m.tr("overlay.cancel.parent"), Detail: m.tr("overlay.cancel.parent_detail")},
			{Label: m.tr("overlay.cancel.all"), Detail: m.tr("overlay.cancel.all_detail")},
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
			detail := m.tr("mcp.tools", map[string]string{"count": strconv.Itoa(server.ToolCount)})
			if server.Error != "" {
				detail = server.Error
			}
			options = append(options, overlayOption{Label: server.Name, Detail: detail, State: server.State})
		}
		return options
	case OverlayBackground:
		options := make([]overlayOption, 0, len(m.background))
		for _, process := range m.background {
			detail := fmt.Sprintf("PID %d · %s · %s", process.PID, formatByteCount(process.LogBytes), process.ID)
			if process.Command != "" {
				detail += " · " + compactAgentActivity(process.Command)
			}
			options = append(options, overlayOption{Label: first(process.Name, process.ID), Detail: detail, State: process.State})
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

func (m AppModel) reasoningDescription(level string) string {
	switch level {
	case "minimal":
		return m.tr("reasoning.minimal")
	case "low":
		return m.tr("reasoning.low")
	case "medium":
		return m.tr("reasoning.medium")
	case "high":
		return m.tr("reasoning.high")
	case "xhigh":
		return m.tr("reasoning.xhigh")
	default:
		return m.tr("reasoning.provider")
	}
}

func (m AppModel) overlayFooter() string {
	if m.actionBusy {
		return m.tr("overlay.footer.working")
	}
	switch m.overlay {
	case OverlayModelRoutes:
		return m.tr("overlay.model_routes.footer")
	case OverlayModel:
		return m.tr("overlay.footer.model")
	case OverlaySkills:
		return m.tr("overlay.footer.skills")
	case OverlayApproval:
		return m.tr("overlay.footer.approval")
	case OverlayCancel:
		return m.tr("overlay.footer.cancel")
	case OverlayAgents:
		return m.tr("overlay.footer.agents")
	case OverlayTodos:
		return m.tr("overlay.footer.todos")
	case OverlayMemory:
		return m.tr("overlay.memory.footer")
	case OverlayRecap:
		return m.tr("overlay.recap.footer")
	case OverlayAgentDetail:
		return m.tr("overlay.footer.agent_detail")
	case OverlayAgentTypes, OverlayPersonas:
		return m.tr("overlay.footer.browse")
	case OverlayMCP:
		return m.tr("overlay.footer.mcp")
	case OverlayMCPDetail:
		return m.tr("overlay.footer.mcp_detail")
	case OverlayBackground:
		return m.tr("overlay.footer.background")
	case OverlayBackgroundDetail:
		return m.tr("overlay.footer.background_detail")
	case OverlayRecovery:
		return m.tr("overlay.footer.recovery")
	case OverlayError:
		return m.tr("overlay.footer.error")
	case OverlayHelp, OverlayDiff, OverlayStatus:
		return m.tr("overlay.footer.scroll_close")
	default:
		return m.tr("overlay.footer.select")
	}
}

func (m AppModel) overlayFooterForWidth(width int) string {
	footer := m.overlayFooter()
	if ansi.StringWidth(footer) <= width {
		return footer
	}
	switch m.overlay {
	case OverlayModelRoutes:
		return m.tr("overlay.model_routes.footer_short")
	case OverlayApproval:
		return m.tr("overlay.footer.approval_short")
	case OverlayCancel:
		return m.tr("overlay.footer.cancel_short")
	case OverlayAgents:
		return m.tr("overlay.footer.agents_short")
	case OverlayAgentDetail:
		return m.tr("overlay.footer.scroll_back")
	case OverlayMCPDetail:
		return m.tr("overlay.footer.mcp_detail_short")
	case OverlayBackground:
		return m.tr("overlay.footer.background_short")
	case OverlayBackgroundDetail:
		return m.tr("overlay.footer.background_detail_short")
	case OverlayError:
		return m.tr("overlay.footer.error_short")
	case OverlayHelp, OverlayDiff, OverlayStatus:
		return m.tr("overlay.footer.scroll_close_short")
	default:
		return m.tr("overlay.footer.short")
	}
}

func (m AppModel) selectedMCPServer() (MCPView, bool) {
	for _, server := range m.mcpServers {
		if server.Name == m.detailMCPName {
			return server, true
		}
	}
	return MCPView{}, false
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
	case "reviewing", "reviewing approval":
		return m.theme.ApprovalSmart
	case "running", "starting", "streaming", "connecting", "queued", "cancelling", "awaiting approval", "blocked", "a", "shift+a", "d":
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

func (m AppModel) agentStateMark(state string) string {
	switch strings.ToLower(state) {
	case "initializing", "running", "cancelling":
		if m.reducedMotion {
			return "◆"
		}
		frames := [...]string{"◇", "◈", "◆", "◈"}
		return frames[m.animationFrame%len(frames)]
	default:
		return stateMark(state)
	}
}
