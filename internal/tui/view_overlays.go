package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	if m.overlay == OverlayAgentTypes || m.overlay == OverlayPersonas || m.overlay == OverlaySkills || m.overlay == OverlayMemory || m.overlay == OverlayRecap || m.overlay == OverlayModelRoutes || m.overlay == OverlayStatus {
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
	rows = append(rows, m.boxRow(" "+strings.ToUpper(title), innerWidth, m.theme.Header, false))
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
	case OverlayStatus:
		return m.tr("overlay.status.title"), m.tr("overlay.status.subtitle")
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
	case OverlayModelRoutes:
		return m.tr("overlay.model_routes.title"), m.tr("overlay.model_routes.subtitle")
	case OverlaySkills:
		return "Skills", "Reload affects new turns only"
	case OverlayLanguage:
		return m.tr("overlay.language.title"), m.tr("overlay.language.subtitle")
	case OverlayReasoning:
		if m.pendingModelRoute != nil {
			return m.tr("overlay.model_routes.reasoning_title"), m.pendingModelRoute.Provider + "/" + m.pendingModelRoute.Model
		}
		if m.pendingSessionModel != nil {
			return "Thinking level", m.pendingSessionModel.Provider + "/" + first(m.pendingSessionModel.Model, "no model") + " · choose for the next turn"
		}
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
	case OverlayMemory:
		return m.tr("overlay.memory.title"), m.tr("overlay.memory.subtitle")
	case OverlayRecap:
		return m.tr("overlay.recap.title"), m.tr("overlay.recap.subtitle")
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
			"Enter submit · Ctrl+J newline · Esc close/cancel/remove last image",
			"Ctrl+V paste image from clipboard · text paste if no image",
			"Ctrl+P commands · Ctrl+M model · Ctrl+R reasoning · Shift+Tab approval",
			"Ctrl+B agents · PageUp/PageDown transcript · Tab cards",
			"/login /logout /provider /models /skills /skill /new /sessions /resume /compact",
			"/team /agents /mcp /status /reconcile /cancel /help /quit",
		}
	case OverlayStatus:
		return m.statusReportLines()
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
				detail := string(item.Status) + " · " + item.ID
				if item.SubagentRunID != "" {
					for _, agent := range m.agents {
						if agent.ID == item.SubagentRunID {
							detail += " · agent " + agent.State
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
	case OverlayModelRoutes:
		return m.tr("overlay.model_routes.footer")
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
	case OverlayMemory:
		return m.tr("overlay.memory.footer")
	case OverlayRecap:
		return m.tr("overlay.recap.footer")
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
	case OverlayHelp, OverlayDiff, OverlayStatus:
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
	case OverlayModelRoutes:
		return m.tr("overlay.model_routes.footer_short")
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
	case OverlayHelp, OverlayDiff, OverlayStatus:
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
