package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/i18n"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/session"
)

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
		m.composer.SetWidth(max(1, m.width-4))
		m.modelSearch.SetWidth(max(1, min(64, m.width-12)))
		m.transcriptTop = min(m.transcriptTop, m.transcriptMaxOffset())
		return m, nil
	case tea.MouseWheelMsg:
		if m.overlay != OverlayNone {
			switch msg.Button {
			case tea.MouseWheelUp:
				return m.updateOverlayKey("up")
			case tea.MouseWheelDown:
				return m.updateOverlayKey("down")
			}
			return m, nil
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scrollTranscript(3)
		case tea.MouseWheelDown:
			m.scrollTranscript(-3)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	case appEventMsg:
		previousMaxOffset := 0
		if m.transcriptTop > 0 {
			previousMaxOffset = m.transcriptMaxOffset()
		}
		m.applyEvent(msg.Event)
		if m.transcriptTop > 0 {
			currentMaxOffset := m.transcriptMaxOffset()
			m.transcriptTop = min(currentMaxOffset, max(0, m.transcriptTop+currentMaxOffset-previousMaxOffset))
		}
		commands := []tea.Cmd{waitForAppEvent(m.runtime)}
		if (m.isRunning() || m.hasRunningHooks() || m.hasRunningAgents()) && !m.reducedMotion && !m.animationActive {
			m.animationActive = true
			commands = append(commands, nextAnimationFrame())
		}
		return m, tea.Batch(commands...)
	case animationTickMsg:
		if (!m.isRunning() && !m.hasRunningHooks() && !m.hasRunningAgents()) || m.reducedMotion {
			m.animationActive = false
			m.animationFrame = 0
			return m, nil
		}
		m.animationFrame++
		if m.transcriptLayout != nil {
			m.transcriptLayout.initialized = false
		}
		return m, nextAnimationFrame()
	case startTurnResultMsg:
		if msg.Err != nil {
			m.status = "Ready"
			m.runID = ""
			m.errorBanner = msg.Err.Error()
			m.transcript = append(m.transcript, Block{Kind: BlockError, Title: "Run rejected", Content: msg.Err.Error(), State: "failed"})
		} else if (m.status == "Starting" || m.status == "Running" || m.status == "Cancelling") && (m.runID == "" || m.runID == msg.RunID) {
			m.runID = msg.RunID
		}
		return m, nil
	case cancelResultMsg:
		if !msg.Cancelled && m.status == "Cancelling" {
			m.status = "Ready"
			m.runID = ""
		}
		return m, nil
	case actionResultMsg:
		if m.actionCancel != nil {
			m.actionCancel()
			m.actionCancel = nil
		}
		m.actionBusy = false
		if msg.Err != nil {
			if errors.Is(msg.Err, context.Canceled) {
				m.status = "Ready"
				m.errorBanner = "Action cancelled"
				return m, nil
			}
			m.errorBanner = msg.Err.Error()
			return m, nil
		}
		m.applyActionResult(msg.Action)
		return m, nil
	case shutdownResultMsg:
		if msg.Err != nil {
			m.errorBanner = msg.Err.Error()
		}
		return m, tea.Quit
	case appStreamClosedMsg:
		if !m.quitting {
			if msg.Err != nil {
				m.errorBanner = msg.Err.Error()
			}
			m.status = "Application stopped"
			m.openOverlay(OverlayError)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}

func nextAnimationFrame() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return animationTickMsg{} })
}

func (m AppModel) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		if m.cancelPendingAction() {
			return m, nil
		}
		if m.isRunning() {
			return m.requestTurnCancellation()
		}
		return m.beginShutdown()
	}
	if key == "shift+tab" {
		mode := ApprovalModePrompt
		if m.autoReviewAvailable {
			switch m.approvalMode {
			case ApprovalModePrompt:
				mode = ApprovalModeAutoReview
			case ApprovalModeAutoReview:
				mode = ApprovalModeYolo
			}
		} else if m.approvalMode == ApprovalModePrompt {
			mode = ApprovalModeYolo
		}
		return m.beginAction(Action{Kind: ActionSetApprovalMode, Target: string(mode)})
	}
	if m.overlay != OverlayNone {
		return m.updateOverlayKeyMsg(msg)
	}
	if m.focus == focusTranscript {
		return m.updateTranscriptKey(key)
	}

	suggestions := m.visibleCommandSuggestions()
	if len(suggestions) > 0 {
		switch key {
		case "up", "shift+tab":
			m.moveCommandCursor(-1, len(suggestions))
			return m, nil
		case "down":
			m.moveCommandCursor(1, len(suggestions))
			return m, nil
		case "tab":
			m.completeCommandSuggestion(suggestions)
			return m, nil
		case "enter":
			if !exactSlashCommand(m.composer.Value()) {
				m.completeCommandSuggestion(suggestions)
				return m, nil
			}
		}
	}

	switch key {
	case "esc":
		if m.cancelPendingAction() {
			return m, nil
		}
		if m.isRunning() {
			return m.requestTurnCancellation()
		}
	case "ctrl+j":
		m.composer.InsertString("\n")
		m.commandCursor = 0
		return m, nil
	case "ctrl+p":
		m.openOverlay(OverlayCommand)
		return m, nil
	case "ctrl+m":
		m.openOverlay(OverlayModel)
		return m, nil
	case "ctrl+r":
		m.openOverlay(OverlayReasoning)
		return m, nil
	case "ctrl+b":
		m.openOverlay(OverlayAgents)
		return m, nil
	case "pgup":
		m.scrollTranscript(max(1, m.height/2))
		return m, nil
	case "pgdown":
		m.scrollTranscript(-max(1, m.height/2))
		return m, nil
	case "ctrl+home":
		m.transcriptTop = m.transcriptMaxOffset()
		return m, nil
	case "ctrl+end":
		m.transcriptTop = 0
		return m, nil
	case "tab", "shift+tab":
		if m.selectTranscript() {
			return m, nil
		}
	case "?":
		if m.composer.Value() == "" {
			m.openOverlay(OverlayHelp)
			return m, nil
		}
	case "enter":
		if !m.isRunning() && !m.actionBusy {
			return m.submit()
		}
	}

	previous := m.composer.Value()
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	if m.composer.Value() != previous {
		m.commandCursor = 0
	}
	return m, cmd
}

func (m AppModel) visibleCommandSuggestions() []SlashCommand {
	if m.overlay != OverlayNone || m.focus != focusComposer || m.actionBusy || m.isRunning() {
		return nil
	}
	suggestions := commandSuggestions(m.composer.Value())
	for index := range suggestions {
		suggestions[index].Detail = m.tr("slash." + suggestions[index].Name + ".detail")
	}
	return suggestions
}

func (m *AppModel) moveCommandCursor(delta int, count int) {
	if count == 0 {
		m.commandCursor = 0
		return
	}
	m.commandCursor = (m.commandCursor + delta) % count
	if m.commandCursor < 0 {
		m.commandCursor += count
	}
}

func (m *AppModel) completeCommandSuggestion(suggestions []SlashCommand) {
	if len(suggestions) == 0 {
		return
	}
	index := min(max(0, m.commandCursor), len(suggestions)-1)
	command := suggestions[index]
	value := "/" + command.Name
	if strings.Contains(command.Usage, " ") {
		value += " "
	}
	m.composer.SetValue(value)
	m.composer.MoveToEnd()
	m.commandCursor = 0
}

func (m AppModel) updateTranscriptKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		if m.isRunning() {
			return m.requestTurnCancellation()
		}
		m.focus = focusComposer
		return m, m.composer.Focus()
	case "tab", "shift+tab":
		m.focus = focusComposer
		return m, m.composer.Focus()
	case "up", "k":
		m.moveTranscriptCursor(-1)
	case "down", "j":
		m.moveTranscriptCursor(1)
	case "enter", " ":
		if m.transcriptCursor >= 0 && m.transcriptCursor < len(m.transcript) {
			block := &m.transcript[m.transcriptCursor]
			block.Collapsed = !block.Collapsed
		}
	case "d":
		if m.transcriptCursor >= 0 && m.transcriptCursor < len(m.transcript) && m.transcript[m.transcriptCursor].Kind == BlockDiff {
			m.openOverlay(OverlayDiff)
		}
	case "pgup":
		m.scrollTranscript(max(1, m.height/2))
	case "pgdown":
		m.scrollTranscript(-max(1, m.height/2))
	case "ctrl+home":
		m.transcriptTop = m.transcriptMaxOffset()
	case "ctrl+end":
		m.transcriptTop = 0
	}
	return m, nil
}

func (m AppModel) updateOverlayKeyMsg(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.overlay != OverlayModel {
		return m.updateOverlayKey(msg.String())
	}
	key := msg.String()
	if key == "esc" && m.modelSearch.Value() != "" {
		m.modelSearch.Reset()
		m.overlayCursor = 0
		return m, nil
	}
	switch key {
	case "esc", "up", "down", "tab", "shift+tab", "pgup", "pgdown", "enter":
		return m.updateOverlayKey(key)
	}
	previous := m.modelSearch.Value()
	var cmd tea.Cmd
	m.modelSearch, cmd = m.modelSearch.Update(msg)
	if m.modelSearch.Value() != previous {
		m.overlayCursor = 0
	}
	return m, cmd
}

func (m AppModel) updateOverlayKey(key string) (tea.Model, tea.Cmd) {
	if m.overlay == OverlayTodos && key == "h" {
		m.todoHideCompleted = !m.todoHideCompleted
		m.overlayCursor = 0
		return m, nil
	}
	if m.overlay == OverlayAgentDetail {
		switch key {
		case "esc":
			m.overlay = OverlayAgents
			m.overlayCursor = 0
			m.overlayScroll = 0
		case "up", "k":
			m.overlayScroll = max(0, m.overlayScroll-1)
		case "down", "j":
			m.overlayScroll++
		case "pgup":
			m.overlayScroll = max(0, m.overlayScroll-max(1, m.height/3))
		case "pgdown":
			m.overlayScroll += max(1, m.height/3)
		case "home":
			m.overlayScroll = 0
		}
		return m, nil
	}
	if key == "esc" {
		if m.overlay == OverlayCancel {
			m.status = "Running"
		}
		m.cancelPendingAction()
		return m, m.closeOverlay()
	}
	if m.actionBusy {
		return m, nil
	}
	count := m.overlayOptionCount()
	if count == 0 {
		switch key {
		case "up", "k":
			m.overlayScroll = max(0, m.overlayScroll-1)
			return m, nil
		case "down", "j":
			m.overlayScroll++
			return m, nil
		case "pgup":
			m.overlayScroll = max(0, m.overlayScroll-max(1, m.height/3))
			return m, nil
		case "pgdown":
			m.overlayScroll += max(1, m.height/3)
			return m, nil
		case "home":
			m.overlayScroll = 0
			return m, nil
		}
	}
	switch key {
	case "up", "k", "shift+tab":
		m.moveOverlayCursor(-1, count)
		return m, nil
	case "down", "j", "tab":
		m.moveOverlayCursor(1, count)
		return m, nil
	case "home":
		m.overlayCursor = 0
		return m, nil
	case "end":
		m.overlayCursor = max(0, count-1)
		return m, nil
	case "pgup":
		m.moveOverlayCursor(-max(1, m.height/3), count)
		return m, nil
	case "pgdown":
		m.moveOverlayCursor(max(1, m.height/3), count)
		return m, nil
	case "enter":
		return m.activateOverlayOption()
	case "a":
		if m.overlay == OverlayApproval {
			return m.beginAction(Action{Kind: ActionResolveApproval, Target: m.approvalID(), Decision: "once"})
		}
	case "A":
		if m.overlay == OverlayApproval {
			return m.beginAction(Action{Kind: ActionResolveApproval, Target: m.approvalID(), Decision: "session"})
		}
	case "d":
		if m.overlay == OverlayApproval {
			return m.beginAction(Action{Kind: ActionResolveApproval, Target: m.approvalID(), Decision: "deny"})
		}
	case "x":
		if m.overlay == OverlayAgents && m.overlayCursor < len(m.agents) {
			return m.beginAction(Action{Kind: ActionCancelAgent, Target: m.agents[m.overlayCursor].ID})
		}
	case "r":
		if m.overlay == OverlaySkills {
			return m.beginAction(Action{Kind: ActionReloadSkills})
		}
		if m.overlay == OverlayMCP && m.overlayCursor < len(m.mcpServers) {
			return m.beginAction(Action{Kind: ActionRefreshMCP, Target: m.mcpServers[m.overlayCursor].Name})
		}
	case "R":
		if m.overlay == OverlayMCP && m.overlayCursor < len(m.mcpServers) {
			return m.beginAction(Action{Kind: ActionReconnectMCP, Target: m.mcpServers[m.overlayCursor].Name})
		}
	case "q":
		if m.overlay == OverlayError {
			return m.beginShutdown()
		}
	}
	return m, nil
}

func (m *AppModel) moveOverlayCursor(delta int, count int) {
	if count <= 0 {
		m.overlayCursor = 0
		return
	}
	m.overlayCursor = (m.overlayCursor + delta) % count
	if m.overlayCursor < 0 {
		m.overlayCursor += count
	}
}

func (m AppModel) overlayOptionCount() int {
	switch m.overlay {
	case OverlayCommand:
		return len(commandPaletteOptions)
	case OverlayProvider:
		return 2
	case OverlayModel:
		return len(m.modelPickerEntries())
	case OverlaySkills:
		return len(m.skills)
	case OverlayLanguage:
		return len(i18n.Languages())
	case OverlayTodos:
		return len(m.overlayOptions())
	case OverlayMemory:
		return len(m.memories)
	case OverlayReasoning:
		return len(m.reasoningLevels())
	case OverlaySessions:
		return len(m.sessions)
	case OverlayApproval:
		return 3
	case OverlayCancel:
		return 2
	case OverlayAgents:
		return len(m.agents)
	case OverlayAgentTypes:
		return len(m.agentTypes)
	case OverlayPersonas:
		return len(m.personas)
	case OverlayMCP:
		return len(m.mcpServers)
	case OverlayRecovery:
		return len(m.recovery)
	default:
		return 0
	}
}

func (m AppModel) activateOverlayOption() (tea.Model, tea.Cmd) {
	switch m.overlay {
	case OverlayCommand:
		return m.activatePaletteOption()
	case OverlayProvider:
		providers := []string{"chatgpt", "grok"}
		if m.overlayCursor >= len(providers) {
			return m, nil
		}
		provider := providers[m.overlayCursor]
		if m.overlayPurpose == "login" {
			return m.beginAction(Action{Kind: ActionLogin, Target: provider})
		}
		m.switchProvider(provider)
		return m, m.closeOverlay()
	case OverlayModel:
		if m.isRunning() {
			m.errorBanner = "model can only change while idle"
			return m, nil
		}
		entries := m.modelPickerEntries()
		if m.overlayCursor < len(entries) {
			entry := entries[m.overlayCursor]
			m.switchProvider(entry.Provider)
			m.selectModel(entry.Model.ID)
			return m, m.closeOverlay()
		}
	case OverlayLanguage:
		languages := i18n.Languages()
		if m.overlayCursor >= 0 && m.overlayCursor < len(languages) {
			language := languages[m.overlayCursor]
			_ = m.closeOverlay()
			return m.beginAction(Action{Kind: ActionSetLanguage, Target: language})
		}
	case OverlayReasoning:
		if m.isRunning() {
			m.errorBanner = "reasoning can only change while idle"
			return m, nil
		}
		levels := m.reasoningLevels()
		if m.overlayCursor < len(levels) {
			m.reasoning = levels[m.overlayCursor]
			return m, m.closeOverlay()
		}
	case OverlaySessions:
		if m.overlayCursor < len(m.sessions) {
			return m.beginAction(Action{Kind: ActionResumeSession, Target: m.sessions[m.overlayCursor].ID})
		}
	case OverlayApproval:
		decisions := []string{"once", "session", "deny"}
		if m.overlayCursor < len(decisions) {
			return m.beginAction(Action{Kind: ActionResolveApproval, Target: m.approvalID(), Decision: decisions[m.overlayCursor]})
		}
	case OverlayCancel:
		if m.overlayCursor < 0 || m.overlayCursor > 1 {
			return m, nil
		}
		children := m.overlayCursor == 1
		m.overlay = OverlayNone
		m.status = "Cancelling"
		return m, cancelTurn(m.runtime, children)
	case OverlayAgents:
		if m.overlayCursor >= 0 && m.overlayCursor < len(m.agents) {
			return m.beginAction(Action{Kind: ActionInspectAgent, Target: m.agents[m.overlayCursor].ID})
		}
		return m, nil
	case OverlayRecovery:
		if m.overlayCursor >= len(m.recovery) {
			return m, nil
		}
		item := m.recovery[m.overlayCursor]
		if item.Kind == "approval" {
			m.approval = &ApprovalView{ToolCallID: item.ID, Tool: "Recovered approval", Target: item.TaskID, Risk: item.Detail, Effect: "approval", Action: item.Detail}
			m.openOverlay(OverlayApproval)
			return m, nil
		}
		m.errorBanner = "Use /reconcile " + item.ID + " succeeded|failed|cancelled after checking the external result."
		return m, nil
	case OverlayDiff:
		return m, m.closeOverlay()
	}
	return m, nil
}

func (m AppModel) activatePaletteOption() (tea.Model, tea.Cmd) {
	if m.overlayCursor < 0 || m.overlayCursor >= len(commandPaletteOptions) {
		return m, nil
	}
	switch commandPaletteOptions[m.overlayCursor] {
	case "login":
		m.openOverlay(OverlayProvider)
		m.overlayPurpose = "login"
	case "provider":
		m.openOverlay(OverlayProvider)
	case "models":
		m.openOverlay(OverlayModel)
	case "skills":
		return m.beginAction(Action{Kind: ActionListSkills})
	case "reasoning":
		m.openOverlay(OverlayReasoning)
	case "sessions":
		return m.beginAction(Action{Kind: ActionListSessions})
	case "new":
		return m.beginAction(Action{Kind: ActionNewSession})
	case "agents":
		m.openOverlay(OverlayAgents)
	case "agent-types":
		return m.beginAction(Action{Kind: ActionListAgentTypes})
	case "personas":
		return m.beginAction(Action{Kind: ActionListPersonas})
	case "mcp":
		m.openOverlay(OverlayMCP)
	case "cancel":
		if m.isRunning() {
			return m.requestTurnCancellation()
		}
	case "help":
		m.openOverlay(OverlayHelp)
	case "quit":
		return m.beginShutdown()
	}
	return m, nil
}

func (m AppModel) requestTurnCancellation() (tea.Model, tea.Cmd) {
	m.overlay = OverlayNone
	if hasActiveForegroundChildren(m.runtime) {
		m.status = "Choose cancellation scope"
		m.openOverlay(OverlayCancel)
		return m, nil
	}
	m.status = "Cancelling"
	return m, cancelTurn(m.runtime, false)
}

func (m AppModel) beginShutdown() (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, nil
	}
	m.quitting = true
	m.status = "Shutting down"
	m.actionBusy = true
	return m, shutdownApplication(m.runtime)
}

func (m *AppModel) cancelPendingAction() bool {
	if !m.actionBusy || m.actionCancel == nil {
		return false
	}
	m.actionCancel()
	m.status = "Cancelling action"
	return true
}

func (m AppModel) beginAction(action Action) (tea.Model, tea.Cmd) {
	if m.actionBusy {
		return m, nil
	}
	if action.Kind == ActionResolveApproval && action.Target == "" {
		m.errorBanner = "approval request is no longer available"
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.actionBusy = true
	m.actionCancel = cancel
	action.SessionID = first(action.SessionID, m.sessionID)
	m.errorBanner = ""
	return m, executeAction(ctx, m.runtime, action)
}

func (m *AppModel) applyActionResult(action Action) {
	switch action.Kind {
	case ActionSetApprovalMode:
		m.approvalMode = ApprovalMode(action.Target)
	case ActionSetLanguage:
		if err := m.SetLanguage(action.Target); err != nil {
			m.errorBanner = err.Error()
		}
	case ActionResolveApproval:
		recovered := m.runID == ""
		m.approval = nil
		for index := range m.recovery {
			if m.recovery[index].ID == action.Target {
				m.recovery = append(m.recovery[:index], m.recovery[index+1:]...)
				break
			}
		}
		_ = m.closeOverlay()
		if !recovered {
			m.status = "Running"
			break
		}
		if len(m.recovery) > 0 {
			m.status = "Recovery attention"
			break
		}
		m.status = "Ready"
		if m.overlay == OverlayRecovery {
			_ = m.closeOverlay()
		}
	case ActionLogin:
		provider, _, _ := strings.Cut(action.Target, ":")
		m.switchProvider(provider)
		m.status = "Ready"
		_ = m.closeOverlay()
	case ActionNewSession, ActionResumeSession:
		m.status = "Ready"
		_ = m.closeOverlay()
	case ActionCancelAgent:
		m.errorBanner = "cancellation requested for " + action.Target
	case ActionRefreshMCP, ActionReconnectMCP:
		m.errorBanner = "MCP update requested for " + action.Target
	case ActionReconcileAttempt:
		for index := range m.recovery {
			if m.recovery[index].ID == action.Target {
				m.recovery = append(m.recovery[:index], m.recovery[index+1:]...)
				break
			}
		}
		m.status = "Reconciled"
		if len(m.recovery) == 0 {
			_ = m.closeOverlay()
		}
	}
}

func (m AppModel) approvalID() string {
	if m.approval == nil {
		return ""
	}
	return first(m.approval.ApprovalID, m.approval.ToolCallID)
}

func (m *AppModel) queueApproval(event app.Event) {
	approval := ApprovalView{
		ApprovalID: first(event.ApprovalID, event.ToolCallID), AgentID: event.AgentID,
		ToolCallID: event.ToolCallID, Tool: first(event.Data["tool"], event.Data["name"]),
		Target: event.Data["target"], Risk: event.Data["risk"], Effect: event.Data["effect"],
		Action: first(event.Data["action"], event.Text), Diff: event.Data["diff"],
	}
	if event.State == "reviewing" {
		id := "approval:" + approval.ApprovalID
		title := m.tr("approval.reviewing")
		content := m.approvalActionSummary(approval.Tool, approval.Target)
		for index := range m.transcript {
			if m.transcript[index].ID == id {
				m.transcript[index].Kind = BlockApproval
				m.transcript[index].Title = title
				m.transcript[index].Content = content
				m.transcript[index].State = "reviewing"
				m.status = "Reviewing approval"
				return
			}
		}
		m.transcript = append(m.transcript, Block{
			ID: id, Kind: BlockApproval, RunID: event.RunID, ToolCallID: event.ToolCallID,
			Title: title, Content: content, State: "reviewing",
		})
		m.status = "Reviewing approval"
		return
	}
	for index := range m.pendingApprovals {
		if m.pendingApprovals[index].ApprovalID == approval.ApprovalID {
			m.pendingApprovals[index] = approval
			if m.approval != nil && m.approval.ApprovalID == approval.ApprovalID {
				current := approval
				m.approval = &current
			}
			return
		}
	}
	m.pendingApprovals = append(m.pendingApprovals, approval)
	if m.approval == nil {
		current := m.pendingApprovals[0]
		m.approval = &current
	}
	m.status = "Awaiting approval"
	m.openOverlay(OverlayApproval)
}

func (m *AppModel) resolveApproval(event app.Event) {
	if strings.HasPrefix(event.State, "auto_") {
		m.resolveAutomaticApproval(event)
		if len(m.pendingApprovals) > 0 {
			m.status = "Awaiting approval"
			return
		}
		if m.runID != "" {
			m.status = "Running"
		} else {
			m.status = "Ready"
		}
		return
	}
	id := first(event.ApprovalID, event.ToolCallID, event.Text)
	pending := m.pendingApprovals[:0]
	for _, approval := range m.pendingApprovals {
		if approval.ApprovalID != id {
			pending = append(pending, approval)
		}
	}
	m.pendingApprovals = pending
	if m.approval != nil && m.approval.ApprovalID == id {
		m.approval = nil
	}
	if m.approval == nil && len(m.pendingApprovals) > 0 {
		current := m.pendingApprovals[0]
		m.approval = &current
		m.status = "Awaiting approval"
		m.openOverlay(OverlayApproval)
		return
	}
	if len(m.pendingApprovals) == 0 {
		if m.overlay == OverlayApproval {
			_ = m.closeOverlay()
		}
		if m.runID != "" {
			m.status = "Running"
		} else {
			m.status = "Ready"
		}
	}
}

func (m *AppModel) resolveAutomaticApproval(event app.Event) {
	id := "approval:" + first(event.ApprovalID, event.ToolCallID)
	label := m.tr("approval.failed")
	state := "failed"
	switch event.State {
	case "auto_approved":
		label = m.tr("approval.allowed")
		state = "completed"
	case "auto_denied":
		label = m.tr("approval.denied")
		state = "denied"
	case "auto_timed_out":
		label = m.tr("approval.timed_out")
	}
	content := m.approvalActionSummary(event.Data["tool"], event.Data["target"])
	if risk := event.Data["risk"]; risk != "" {
		content = joinToolSummary(content, m.tr("approval.risk", map[string]string{"risk": risk}))
	}
	if rationale := strings.TrimSpace(event.Data["rationale"]); rationale != "" {
		content = joinToolSummary(content, m.tr("approval.rationale", map[string]string{"rationale": rationale}))
	} else if event.State == "auto_failed" || event.State == "auto_timed_out" {
		if detail := strings.TrimSpace(event.Text); detail != "" {
			content = joinToolSummary(content, m.tr("approval.failure", map[string]string{"failure": detail}))
		}
	}
	if content == "" {
		content = event.Text
	}
	for index := range m.transcript {
		if m.transcript[index].ID != id {
			continue
		}
		m.transcript[index].Kind = BlockApproval
		m.transcript[index].Title = label
		m.transcript[index].Content = content
		m.transcript[index].State = state
		return
	}
	m.transcript = append(m.transcript, Block{
		ID: id, Kind: BlockApproval, RunID: event.RunID, ToolCallID: event.ToolCallID,
		Title: label, Content: content, State: state,
	})
}

func (m AppModel) approvalActionSummary(toolName, target string) string {
	action := m.toolDisplayName(toolName)
	if action == "" {
		action = m.tr("approval.requested_action")
	}
	if target != "" && target != "workspace" {
		return action + " · " + target
	}
	return action
}

func (m AppModel) submit() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.composer.Value())
	if input == "" {
		return m, nil
	}
	if command, ok, err := ParseCommand(input); ok {
		m.composer.Reset()
		m.commandCursor = 0
		if err != nil {
			m.errorBanner = err.Error()
			return m, nil
		}
		return m.executeCommand(command)
	}
	m.transcript = append(m.transcript, Block{Kind: BlockUser, Title: "You", Content: input})
	m.composer.Reset()
	m.commandCursor = 0
	m.status = "Starting"
	m.errorBanner = ""
	m.runID = ""
	m.resetTurnUsage()
	m.transcriptTop = 0
	return m, startTurn(m.runtime, app.TurnRequest{SessionID: m.sessionID, Prompt: input, Provider: m.provider, Model: m.model, Reasoning: m.reasoning, AgentMode: m.agentMode})
}

func (m AppModel) executeCommand(command Command) (tea.Model, tea.Cmd) {
	switch command.Name {
	case "language":
		if len(command.Args) == 0 {
			m.openOverlay(OverlayLanguage)
			break
		}
		if len(command.Args) != 1 {
			m.errorBanner = m.tr("language.usage")
			break
		}
		language := strings.ToLower(command.Args[0])
		valid := true
		switch language {
		case "en":
			language = "en"
		case "zh-cn", "zh_cn", "zh", "cn":
			language = "zh-CN"
		default:
			m.errorBanner = m.tr("language.unsupported")
			valid = false
		}
		if !valid {
			break
		}
		return m.beginAction(Action{Kind: ActionSetLanguage, Target: language})
	case "help":
		m.openOverlay(OverlayHelp)
	case "quit":
		return m.beginShutdown()
	case "cancel":
		if m.isRunning() {
			return m.requestTurnCancellation()
		}
	case "team":
		if m.isRunning() {
			m.errorBanner = "agent mode can only change while idle"
			break
		}
		if len(command.Args) != 1 || (command.Args[0] != "on" && command.Args[0] != "off") {
			m.errorBanner = "usage: /team on|off"
			break
		}
		if command.Args[0] == "on" {
			m.agentMode = "team"
		} else {
			m.agentMode = "single"
		}
	case "provider":
		if m.isRunning() {
			m.errorBanner = "provider can only change while idle"
			break
		}
		if len(command.Args) == 0 {
			m.openOverlay(OverlayProvider)
			break
		}
		if len(command.Args) != 1 {
			m.errorBanner = "usage: /provider [chatgpt|grok]"
			break
		}
		provider := strings.ToLower(command.Args[0])
		if provider != "chatgpt" && provider != "grok" {
			m.errorBanner = "provider must be chatgpt or grok"
			break
		}
		m.switchProvider(provider)
	case "login":
		if len(command.Args) > 2 {
			m.errorBanner = "usage: /login [chatgpt [--import-codex]|grok [--import]]"
			break
		}
		if len(command.Args) >= 1 {
			provider := strings.ToLower(command.Args[0])
			if provider != "chatgpt" && provider != "grok" {
				m.errorBanner = "provider must be chatgpt or grok"
				break
			}
			target := provider
			if len(command.Args) == 2 {
				if (provider == "chatgpt" && command.Args[1] != "--import-codex") || (provider == "grok" && command.Args[1] != "--import") {
					m.errorBanner = "usage: /login chatgpt --import-codex | /login grok --import"
					break
				}
				target += ":import"
			}
			return m.beginAction(Action{Kind: ActionLogin, Target: target})
		}
		m.openOverlay(OverlayProvider)
		m.overlayPurpose = "login"
	case "logout":
		target := m.provider
		if len(command.Args) == 1 {
			target = strings.ToLower(command.Args[0])
		} else if len(command.Args) > 1 {
			m.errorBanner = "usage: /logout [chatgpt|grok]"
			break
		}
		return m.beginAction(Action{Kind: ActionLogout, Target: target})
	case "skills":
		if len(command.Args) == 0 {
			return m.beginAction(Action{Kind: ActionListSkills})
		}
		if len(command.Args) == 1 && strings.ToLower(command.Args[0]) == "reload" {
			return m.beginAction(Action{Kind: ActionReloadSkills})
		}
		m.errorBanner = "usage: /skills [reload]"
	case "skill":
		if len(command.Args) == 0 {
			m.errorBanner = "usage: /skill <name> [instruction]"
			break
		}
		if m.isRunning() {
			m.errorBanner = "skill invocation can only start while idle"
			break
		}
		if m.agentMode == "team" {
			m.errorBanner = "skill invocation requires single-agent mode; use /team off"
			break
		}
		name := strings.ToLower(command.Args[0])
		prompt := `Apply the "` + name + `" skill to the current workspace and report the result.`
		if len(command.Args) > 1 {
			prompt = strings.Join(command.Args[1:], " ")
		}
		m.transcript = append(m.transcript, Block{Kind: BlockUser, Title: "You", Content: prompt})
		m.status = "Starting"
		m.errorBanner = ""
		m.runID = ""
		m.resetTurnUsage()
		m.transcriptTop = 0
		return m, startTurn(m.runtime, app.TurnRequest{
			SessionID: m.sessionID, Prompt: prompt, Provider: m.provider, Model: m.model,
			Reasoning: m.reasoning, AgentMode: m.agentMode, ActiveSkills: []string{name},
		})
	case "models":
		if len(command.Args) != 0 {
			m.errorBanner = "usage: /models"
			break
		}
		m.openOverlay(OverlayModel)
	case "reasoning":
		if len(command.Args) == 0 {
			m.openOverlay(OverlayReasoning)
			break
		}
		levels := m.reasoningLevels()
		if len(levels) == 0 {
			m.errorBanner = "the selected model does not support adjustable reasoning"
			break
		}
		if len(command.Args) != 1 || !contains(levels, command.Args[0]) {
			m.errorBanner = "usage: /reasoning " + strings.Join(levels, "|")
			break
		}
		if m.isRunning() {
			m.errorBanner = "reasoning can only change while idle"
			break
		}
		m.reasoning = command.Args[0]
	case "new":
		return m.beginAction(Action{Kind: ActionNewSession})
	case "sessions":
		return m.beginAction(Action{Kind: ActionListSessions})
	case "resume":
		if len(command.Args) != 0 {
			m.errorBanner = "usage: /resume"
			break
		}
		return m.beginAction(Action{Kind: ActionListSessions})
	case "compact":
		return m.beginAction(Action{Kind: ActionCompact, Target: m.sessionID})
	case "memory":
		return m.beginAction(Action{Kind: ActionListMemories, Target: strings.Join(command.Args, " ")})
	case "remember":
		content := strings.TrimSpace(strings.Join(command.Args, " "))
		if content == "" {
			m.errorBanner = m.tr("memory.remember_usage")
			break
		}
		return m.beginAction(Action{Kind: ActionRemember, Target: content})
	case "forget":
		if len(command.Args) != 1 {
			m.errorBanner = m.tr("memory.forget_usage")
			break
		}
		return m.beginAction(Action{Kind: ActionForgetMemory, Target: command.Args[0]})
	case "recap":
		if len(command.Args) != 0 {
			m.errorBanner = m.tr("recap.usage")
			break
		}
		return m.beginAction(Action{Kind: ActionShowRecap})
	case "agents":
		if len(command.Args) == 0 {
			m.openOverlay(OverlayAgents)
			break
		}
		if len(command.Args) != 2 || command.Args[0] != "cancel" {
			m.errorBanner = "usage: /agents [cancel <id>]"
			break
		}
		return m.beginAction(Action{Kind: ActionCancelAgent, Target: command.Args[1]})
	case "todo", "todos":
		if len(command.Args) != 0 {
			m.errorBanner = "usage: /todos"
			break
		}
		m.openOverlay(OverlayTodos)
	case "agent-types":
		if len(command.Args) != 0 {
			m.errorBanner = "usage: /agent-types"
			break
		}
		return m.beginAction(Action{Kind: ActionListAgentTypes})
	case "personas":
		if len(command.Args) != 0 {
			m.errorBanner = "usage: /personas"
			break
		}
		return m.beginAction(Action{Kind: ActionListPersonas})
	case "mcp":
		if len(command.Args) == 0 {
			m.openOverlay(OverlayMCP)
			break
		}
		if len(command.Args) != 2 || (command.Args[0] != "refresh" && command.Args[0] != "reconnect") {
			m.errorBanner = "usage: /mcp [refresh|reconnect <server>]"
			break
		}
		kind := ActionRefreshMCP
		if command.Args[0] == "reconnect" {
			kind = ActionReconnectMCP
		}
		return m.beginAction(Action{Kind: kind, Target: command.Args[1]})
	case "reconcile":
		if len(command.Args) != 2 {
			m.errorBanner = "usage: /reconcile <attempt-id> succeeded|failed|cancelled"
			break
		}
		return m.beginAction(Action{Kind: ActionReconcileAttempt, Target: command.Args[0], Decision: command.Args[1]})
	default:
		m.errorBanner = fmt.Sprintf("unknown command /%s", command.Name)
	}
	return m, nil
}

func (m *AppModel) applyEvent(event app.Event) {
	if event.Kind != app.EventSessionLoaded && event.SessionID != "" && event.SessionID != m.sessionID {
		return
	}
	if event.AgentID != "" && event.AgentID != "main" {
		switch event.Kind {
		case app.EventThinkingDelta, app.EventTextDelta, app.EventToolStarted, app.EventToolUpdate, app.EventToolFinished,
			app.EventHookStarted, app.EventHookFinished, app.EventHookDiagnostic:
			m.updateAgentStream(event)
			return
		}
	}
	if !m.acceptRunEvent(event) {
		return
	}
	m.updateUsage(event.Data)

	switch event.Kind {
	case app.EventBootstrapDone:
		m.workspace = event.Text
		m.status = "Ready"
	case app.EventSessionLoaded:
		m.loadSessionEvent(event)
	case app.EventTodoUpdated:
		if event.Todo != nil && event.Todo.Revision >= m.todo.Revision {
			m.todo = event.Todo.Clone()
		}
	case app.EventRunStarted:
		m.resetTurnUsage()
		m.runID = event.RunID
		if m.status != "Cancelling" {
			m.status = "Running"
		}
	case app.EventContextUsage:
	case app.EventAgentState:
		m.updateAgent(event)
	case app.EventAgentDetail:
		m.updateAgentDetail(event)
	case app.EventThinkingDelta:
		m.appendDelta(BlockThinking, event.RunID, "Thinking", event.Text)
	case app.EventTextDelta:
		m.appendDelta(BlockAssistant, event.RunID, "Azem", event.Text)
	case app.EventToolStarted, app.EventToolUpdate, app.EventToolFinished:
		m.updateTool(event)
	case app.EventHookStarted, app.EventHookFinished, app.EventHookDiagnostic:
		m.updateHooks(&m.transcript, event)
	case app.EventDiffReady:
		block := Block{ID: event.ToolCallID, Kind: BlockDiff, RunID: event.RunID, Title: first(event.Data["path"], "Diff"), Content: event.Text, State: first(event.State, "ready")}
		m.transcript = append(m.transcript, block)
	case app.EventApprovalRequested:
		m.queueApproval(event)
	case app.EventApprovalResolved:
		m.resolveApproval(event)
	case app.EventApprovalMode:
		m.approvalMode = ApprovalMode(event.State)
		m.autoReviewAvailable, _ = strconv.ParseBool(event.Data["auto_review_available"])
		if !m.autoReviewAvailable && m.approvalMode == ApprovalModeAutoReview {
			m.approvalMode = ApprovalModePrompt
		}
	case app.EventModelCatalog:
		m.loadModels(event)
	case app.EventSkillCatalog:
		m.skills = append([]SkillCatalogView(nil), event.SkillCatalog...)
		m.skillDiagnostics = append([]app.SkillDiagnostic(nil), event.SkillDiagnostics...)
		m.openOverlay(OverlaySkills)
	case app.EventMemoryState:
		switch event.State {
		case "forgotten":
			for index := range m.memories {
				if m.memories[index].ID == event.Text {
					m.memories = append(m.memories[:index], m.memories[index+1:]...)
					break
				}
			}
		default:
			m.memories = append([]memory.Memory(nil), event.Memories...)
		}
		m.openOverlay(OverlayMemory)
	case app.EventRecapState:
		if event.State == "failed" {
			m.errorBanner = m.tr("recap.persist_failed") + ": " + event.Text
			break
		}
		m.recap = nil
		if event.Recap != nil {
			value := *event.Recap
			m.recap = &value
		}
		m.openOverlay(OverlayRecap)
	case app.EventAuthState:
		m.updateAuth(event)
	case app.EventMCPState:
		m.updateMCP(event)
	case app.EventRecoveryState:
		m.loadRecoveryEvent(event)
	case app.EventRunFinished:
		m.finishRun(event.RunID, "Ready")
	case app.EventRunCancelled:
		m.finishRun(event.RunID, "Cancelled")
	case app.EventRunFailed:
		m.errorBanner = event.Text
		m.transcript = append(m.transcript, Block{Kind: BlockError, RunID: event.RunID, Title: "Run failed", Content: event.Text, State: "failed"})
		if event.RunID != "" {
			m.finishRun(event.RunID, "Failed")
		} else {
			m.status = "Failed"
		}
		if event.State == "fatal" {
			m.openOverlay(OverlayError)
		}
	}
}

func (m AppModel) acceptRunEvent(event app.Event) bool {
	if event.Kind == app.EventHookStarted || event.Kind == app.EventHookFinished || event.Kind == app.EventHookDiagnostic {
		return true
	}
	if event.AgentID != "" {
		switch event.Kind {
		case app.EventAgentState, app.EventAgentDetail, app.EventApprovalRequested, app.EventApprovalResolved:
			return true
		}
	}
	if event.RunID == "" {
		return true
	}
	if event.Kind == app.EventRunStarted {
		return m.isRunning() && (m.runID == "" || m.runID == event.RunID)
	}
	if (event.Kind == app.EventToolFinished || event.Kind == app.EventHookFinished) && event.RunID == m.lastRunID {
		if event.Kind == app.EventHookFinished {
			return true
		}
		return m.hasOrphanedTool(event.RunID, event.ToolCallID)
	}
	return m.runID != "" && event.RunID == m.runID
}

func (m *AppModel) finishRun(runID string, status string) {
	fallbackState := "failed"
	orphaned := false
	fallbackMessage := "parent run failed before tool completed"
	switch status {
	case "Cancelled":
		fallbackState = "cancelled"
		fallbackMessage = "tool cancelled with parent run"
	case "Ready":
		orphaned = true
		fallbackMessage = "orphaned: missing tool result"
	}
	for index := range m.transcript {
		block := &m.transcript[index]
		if (block.Kind != BlockTool && block.Kind != BlockApproval) || block.RunID != runID || toolStateTerminal(block.State) {
			continue
		}
		block.State = fallbackState
		block.Orphaned = orphaned
		appendBlockContent(block, fallbackMessage)
	}
	m.lastRunID = runID
	m.runID = ""
	m.status = status
	m.approval = nil
	if m.overlay == OverlayApproval {
		_ = m.closeOverlay()
	}
}

func (m AppModel) hasAgent(id string) bool {
	for _, agent := range m.agents {
		if agent.ID == id {
			return true
		}
	}
	return false
}

func (m AppModel) hasOrphanedTool(runID string, toolCallID string) bool {
	for _, block := range m.transcript {
		if block.Kind == BlockTool && block.RunID == runID && block.ID == toolCallID && block.Orphaned {
			return true
		}
	}
	return false
}

func toolStateTerminal(state string) bool {
	switch state {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func terminalToolState(state string) string {
	switch state {
	case "failed", "cancelled":
		return state
	default:
		return "completed"
	}
}

func appendBlockContent(block *Block, content string) {
	if content == "" {
		return
	}
	if block.Content != "" && !strings.HasSuffix(block.Content, "\n") {
		block.Content += "\n"
	}
	block.Content += content
}

func (m *AppModel) loadSessionEvent(event app.Event) {
	if event.State == "list" {
		if err := json.Unmarshal([]byte(event.Data["sessions"]), &m.sessions); err != nil {
			m.errorBanner = "list sessions: " + err.Error()
			return
		}
		m.status = "Ready"
		m.openOverlay(OverlaySessions)
		return
	}
	var recovered []struct {
		ID               string `json:"id"`
		Kind             string `json:"kind"`
		RunID            string `json:"runId"`
		AgentID          string `json:"agentId"`
		ParentToolCallID string `json:"parentToolCallId"`
		Title            string `json:"title"`
		Content          string `json:"content"`
		State            string `json:"state"`
		Collapsed        bool   `json:"collapsed"`
	}
	if err := json.Unmarshal([]byte(event.Data["blocks"]), &recovered); err != nil {
		m.errorBanner = "recover session: " + err.Error()
		return
	}
	if event.SessionID != "" {
		m.sessionID = event.SessionID
	}
	if event.Todo != nil {
		m.todo = event.Todo.Clone()
	} else {
		m.todo = TodoView{}
	}
	m.transcript = make([]Block, 0, len(recovered))
	m.transcriptTop = 0
	for _, block := range recovered {
		m.transcript = append(m.transcript, Block{
			ID: first(block.AgentID, block.ID), Kind: BlockKind(block.Kind), RunID: block.RunID,
			ToolCallID: block.ParentToolCallID, Title: block.Title, Content: block.Content,
			State: block.State, Collapsed: block.Collapsed,
		})
	}
	m.runID = ""
	m.lastRunID = event.Data["lastRunID"]
	m.approval = nil
	m.pendingApprovals = nil
	m.agents = make([]AgentView, 0, len(event.AgentSnapshots))
	for _, snapshot := range event.AgentSnapshots {
		m.agents = append(m.agents, agentViewFromPayload(snapshot.ID, snapshot.State, snapshot.Summary, &snapshot.Agent))
	}
	m.detailAgentID = ""
	m.usage = UsageView{}
	m.status = "Ready"
	provider := first(event.Data["provider"], m.provider)
	m.switchProvider(provider)
	m.selectModel(first(event.Data["model"], m.model))
	m.reasoning = first(event.Data["reasoning"], m.reasoning)
	m.syncReasoningForModel()
	m.agentMode = first(event.Data["agentMode"], m.agentMode)
	if sessions := event.Data["sessions"]; sessions != "" {
		_ = json.Unmarshal([]byte(sessions), &m.sessions)
	}
}

func (m *AppModel) loadRecoveryEvent(event app.Event) {
	if event.State == "reconciled" {
		for index := range m.recovery {
			if m.recovery[index].ID == event.Text {
				m.recovery = append(m.recovery[:index], m.recovery[index+1:]...)
				break
			}
		}
		return
	}
	var recovered []RecoveryView
	if err := json.Unmarshal([]byte(event.Data["items"]), &recovered); err != nil {
		m.errorBanner = "load recovery state: " + err.Error()
		return
	}
	m.recovery = recovered
	if len(recovered) > 0 {
		m.status = "Recovery attention"
		m.openOverlay(OverlayRecovery)
		return
	}
}

func (m *AppModel) appendDelta(kind BlockKind, runID string, title string, text string) {
	if text == "" {
		return
	}
	if len(m.transcript) > 0 {
		last := &m.transcript[len(m.transcript)-1]
		if last.Kind == kind && last.RunID == runID {
			last.Content = appendStreamContent(last.Content, text, kind)
			return
		}
	}
	m.transcript = append(m.transcript, Block{Kind: kind, RunID: runID, Title: title, Content: text, State: "streaming"})
}

func appendStreamContent(existing, incoming string, kind BlockKind) string {
	// Responses reasoning summaries arrive as individually complete Markdown
	// segments. Their stream events do not carry a block separator, so direct
	// concatenation turns adjacent bold headings into an unreadable "****".
	// Do not add whitespace to ordinary token deltas.
	if kind == BlockThinking &&
		strings.HasSuffix(strings.TrimSpace(existing), "**") &&
		strings.HasPrefix(strings.TrimSpace(incoming), "**") {
		return strings.TrimRight(existing, "\n") + "\n\n" + strings.TrimLeft(incoming, "\n")
	}
	return existing + incoming
}

func (m *AppModel) updateTool(event app.Event) {
	id := event.ToolCallID
	if id == "" {
		id = first(event.Data["id"], event.Data["name"])
	}
	index := -1
	for candidate := len(m.transcript) - 1; candidate >= 0; candidate-- {
		if (m.transcript[candidate].Kind == BlockTool || m.transcript[candidate].Kind == BlockDiff) && m.transcript[candidate].ID == id {
			index = candidate
			break
		}
	}
	switch event.Kind {
	case app.EventToolStarted:
		if index == -1 {
			arguments := event.Data["arguments"]
			name := first(event.Data["name"], event.Data["tool"], "Tool")
			if name == "todo" {
				arguments = ""
			}
			m.transcript = append(m.transcript, Block{
				ID: id, Kind: BlockTool, RunID: event.RunID, ToolCallID: id, Title: name,
				Arguments: arguments, Content: first(event.Text, map[bool]string{true: "Todo / progress", false: summarizeToolArguments(name, arguments)}[name == "todo"]), State: "running",
			})
			return
		}
		block := &m.transcript[index]
		if toolStateTerminal(block.State) {
			return
		}
		block.State = "running"
		appendBlockContent(block, first(event.Text, summarizeToolArguments(block.Title, event.Data["arguments"])))
	case app.EventToolUpdate:
		if index == -1 {
			return
		}
		block := &m.transcript[index]
		if toolStateTerminal(block.State) {
			return
		}
		appendBlockContent(block, event.Text)
	case app.EventToolFinished:
		state := terminalToolState(event.State)
		if index == -1 {
			content := event.Text
			kind := BlockTool
			title := first(event.Data["name"], event.Data["tool"], "Tool")
			if state == "completed" {
				if diffTitle, diff, ok := summarizeFileChange(title, event.Data["arguments"], event.Data["structured"], event.Text); ok {
					kind, title, content = BlockDiff, diffTitle, diff
				} else {
					content = summarizeToolResult(title, event.Data["arguments"], event.Text, m.catalog)
				}
			}
			m.transcript = append(m.transcript, Block{
				ID: id, Kind: kind, RunID: event.RunID, ToolCallID: id, Title: title,
				Arguments: event.Data["arguments"], Content: content, State: state,
			})
			return
		}
		block := &m.transcript[index]
		if toolStateTerminal(block.State) && !block.Orphaned {
			return
		}
		if block.Orphaned {
			block.Content = strings.TrimSuffix(block.Content, "\norphaned: missing tool result")
			if block.Content == "orphaned: missing tool result" {
				block.Content = ""
			}
		}
		block.State = state
		block.Orphaned = false
		if state == "completed" {
			if title, diff, ok := summarizeFileChange(block.Title, block.Arguments, event.Data["structured"], event.Text); ok {
				block.Kind, block.Title, block.Content = BlockDiff, title, diff
			} else {
				block.Content = summarizeToolResult(block.Title, block.Arguments, event.Text, m.catalog)
			}
		} else {
			block.Content = joinToolSummary(summarizeToolArguments(block.Title, block.Arguments), summarizeToolFailure(block.Title, event.Text))
		}
	}
}

type fileChangeSection struct {
	Path             string `json:"path"`
	FirstChangedLine int    `json:"firstChangedLine"`
	Diff             string `json:"diff"`
}

func summarizeFileChange(name, arguments, structured, output string) (string, string, bool) {
	type editResult struct {
		Sections []fileChangeSection `json:"sections"`
	}

	var sections []fileChangeSection
	switch name {
	case "coding.edit_hashline":
		var result editResult
		if json.Unmarshal([]byte(structured), &result) == nil {
			sections = result.Sections
		}
		if len(sections) == 0 {
			sections = parseCompactEditOutput(output)
		}
	case "coding.write_file":
		var input struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(arguments), &input) != nil || input.Path == "" {
			return "", "", false
		}
		lines := strings.Split(input.Content, "\n")
		if input.Content == "" {
			lines = nil
		} else if strings.HasSuffix(input.Content, "\n") {
			lines = lines[:len(lines)-1]
		}
		for index := range lines {
			lines[index] = "+" + lines[index]
		}
		sections = []fileChangeSection{{Path: input.Path, FirstChangedLine: 1, Diff: strings.Join(lines, "\n")}}
	default:
		return "", "", false
	}
	if len(sections) == 0 {
		return "", "", false
	}

	added, deleted := 0, 0
	chunks := make([]string, 0, len(sections))
	for _, section := range sections {
		if section.Path == "" {
			continue
		}
		for _, line := range strings.Split(section.Diff, "\n") {
			if strings.HasPrefix(line, "+") {
				added++
			} else if strings.HasPrefix(line, "-") {
				deleted++
			}
		}
		header := "@@ " + section.Path
		if section.FirstChangedLine > 0 {
			header += fmt.Sprintf(":%d", section.FirstChangedLine)
		}
		header += " @@"
		body := section.Diff
		if body == "" {
			body = "(empty file)"
		}
		chunks = append(chunks, header+"\n"+body)
	}
	if len(chunks) == 0 {
		return "", "", false
	}
	title := sections[0].Path
	if len(sections) > 1 {
		title = fmt.Sprintf("%d files", len(sections))
	}
	title += fmt.Sprintf("  +%d/-%d", added, deleted)
	return title, strings.Join(chunks, "\n\n"), true
}

func parseCompactEditOutput(output string) []fileChangeSection {
	var result []fileChangeSection
	var current *fileChangeSection
	var diffLines []string
	inDiff := false
	flush := func() {
		if current == nil {
			return
		}
		current.Diff = strings.Trim(strings.Join(diffLines, "\n"), "\n")
		if current.Path != "" && current.Diff != "" {
			result = append(result, *current)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "¶") {
			flush()
			path := strings.TrimPrefix(strings.SplitN(line, "#", 2)[0], "¶")
			current = &fileChangeSection{Path: path}
			diffLines = nil
			inDiff = false
			continue
		}
		if current == nil {
			continue
		}
		if value, ok := strings.CutPrefix(line, "firstChangedLine: "); ok {
			current.FirstChangedLine, _ = strconv.Atoi(strings.TrimSpace(value))
			continue
		}
		if line == "--- compact diff ---" {
			inDiff = true
			continue
		}
		if inDiff {
			diffLines = append(diffLines, line)
		}
	}
	flush()
	return result
}

func summarizeToolArguments(name, arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return ""
	}
	var fields map[string]any
	if json.Unmarshal([]byte(arguments), &fields) != nil {
		return compactAgentActivity(arguments)
	}
	stringField := func(key string) string {
		value, _ := fields[key].(string)
		return strings.TrimSpace(value)
	}
	intField := func(key string) int {
		value, _ := fields[key].(float64)
		return int(value)
	}
	switch name {
	case "coding.edit_hashline":
		return summarizeEditArguments(stringField("input"), fields["dryRun"] == true)
	case "coding.write_file":
		if path := stringField("path"); path != "" {
			content, _ := fields["content"].(string)
			lines := 0
			if content != "" {
				lines = strings.Count(content, "\n") + 1
				if strings.HasSuffix(content, "\n") {
					lines--
				}
			}
			return fmt.Sprintf("Create %s · %d lines", path, lines)
		}
	case "coding.read_file":
		path := stringField("path")
		start, end := intField("startLine"), intField("endLine")
		if start == 0 && end > 0 {
			start = 1
		}
		if path != "" && start > 0 && end >= start {
			return fmt.Sprintf("Read %s · lines %d-%d", path, start, end)
		}
		if path != "" {
			return "Read " + path
		}
	case "coding.go_test":
		if pkg := stringField("package"); pkg != "" {
			return "Test package " + pkg
		}
	case "coding.shell":
		if command := stringField("command"); command != "" {
			return "$ " + compactAgentActivity(command)
		}
	case "coding.list_files":
		if pattern := first(stringField("pattern"), stringField("path")); pattern != "" {
			return "List " + pattern
		}
	}
	if summary, ok := summarizeJSONOutput(arguments); ok {
		lines := strings.Split(summary, "\n")
		return strings.Join(lines[:min(4, len(lines))], "\n")
	}
	return compactAgentActivity(arguments)
}

func summarizeEditArguments(input string, dryRun bool) string {
	paths := make([]string, 0, 2)
	seen := make(map[string]bool)
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "¶") {
			continue
		}
		path := strings.TrimPrefix(strings.SplitN(line, "#", 2)[0], "¶")
		if path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	action := "Edit"
	if dryRun {
		action = "Preview"
	}
	switch len(paths) {
	case 0:
		return action + " file changes"
	case 1:
		return action + " " + paths[0]
	default:
		return fmt.Sprintf("%s %d files · %s", action, len(paths), strings.Join(paths[:min(3, len(paths))], ", "))
	}
}

func summarizeToolFailure(name, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "Tool failed"
	}
	if summary, ok := summarizeJSONOutput(output); ok {
		output = summary
	}
	for _, prefix := range []string{name + " failed:", name + " rejected:"} {
		if detail, found := strings.CutPrefix(output, prefix); found {
			output = strings.TrimSpace(detail)
			break
		}
	}
	runes := []rune(output)
	if len(runes) > 600 {
		output = string(runes[:599]) + "…"
	}
	return output
}

func joinToolSummary(summary, detail string) string {
	if summary == "" {
		return detail
	}
	if detail == "" || detail == summary {
		return summary
	}
	return summary + "\n" + detail
}

func summarizeToolResult(name, arguments, output string, catalogs ...i18n.Catalog) string {
	switch name {
	case "todo":
		var todo session.TodoList
		if json.Unmarshal([]byte(output), &todo) == nil {
			total, completed, current := 0, 0, ""
			for _, phase := range todo.Phases {
				for _, item := range phase.Items {
					total++
					if item.Status == session.TodoCompleted {
						completed++
					}
					if item.Status == session.TodoInProgress {
						current = item.Content
					}
				}
			}
			summary := fmt.Sprintf("Todo updated · %d/%d", completed, total)
			if current != "" {
				summary += "\nCurrent: " + current
			}
			return summary
		}
		return "Todo updated"
	case "coding.read_file":
		return summarizeReadFile(arguments, output)
	case "coding.list_files":
		return summarizeListOutput(output, 8)
	case "hydaelyn_activate_skill":
		catalog := i18n.Must(i18n.DefaultLanguage)
		if len(catalogs) > 0 {
			catalog = catalogs[0]
		}
		var input struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(arguments), &input)
		if input.Name == "" {
			for _, line := range strings.Split(output, "\n") {
				if value, found := strings.CutPrefix(strings.TrimSpace(line), "--- skill: "); found {
					input.Name = strings.TrimSpace(strings.TrimSuffix(value, "---"))
					break
				}
			}
		}
		if input.Name == "" {
			input.Name = catalog.T("skill.unknown")
		}
		status := "skill.status.loaded"
		if strings.Contains(strings.ToLower(output), "already active") {
			status = "skill.status.active"
		}
		return catalog.T("skill.name", map[string]string{"name": input.Name}) + "\n" + catalog.T(status)
	default:
		if summary, ok := summarizeJSONOutput(output); ok {
			return summary
		}
		return output
	}
}

func summarizeListOutput(output string, limit int) string {
	lines := make([]string, 0)
	truncation := ""
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[truncated;") {
			truncation = line
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) <= limit && truncation == "" {
		return strings.Join(lines, "\n")
	}
	shown := min(limit, len(lines))
	result := append([]string(nil), lines[:shown]...)
	if remaining := len(lines) - shown; remaining > 0 {
		result = append(result, fmt.Sprintf("… %d more entries (%d total)", remaining, len(lines)))
	}
	if truncation != "" {
		result = append(result, truncation)
	}
	return strings.Join(result, "\n")
}

func summarizeJSONOutput(output string) (string, bool) {
	var value any
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &value) != nil {
		return "", false
	}
	lines := make([]string, 0, 8)
	appendJSONSummary(&lines, "", value, 8)
	if len(lines) == 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func appendJSONSummary(lines *[]string, key string, value any, limit int) {
	if len(*lines) >= limit {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		for _, childKey := range keys {
			childValue := typed[childKey]
			if text, ok := childValue.(string); ok && strings.TrimSpace(text) == "" {
				continue
			}
			label := childKey
			if key != "" {
				label = key + "." + childKey
			}
			appendJSONSummary(lines, label, childValue, limit)
			if len(*lines) >= limit {
				break
			}
		}
	case []any:
		label := key
		if label == "" {
			label = "items"
		}
		*lines = append(*lines, fmt.Sprintf("%s: %d items", label, len(typed)))
		shown := min(5, len(typed))
		for index := 0; index < shown && len(*lines) < limit; index++ {
			appendJSONSummary(lines, fmt.Sprintf("  [%d]", index+1), typed[index], limit)
		}
		if len(typed) > shown && len(*lines) < limit {
			*lines = append(*lines, fmt.Sprintf("  … %d more items", len(typed)-shown))
		}
	case nil:
		if key != "" {
			*lines = append(*lines, key+": null")
		}
	case string:
		text := strings.Join(strings.Fields(typed), " ")
		if key == "" {
			*lines = append(*lines, text)
		} else {
			*lines = append(*lines, key+": "+text)
		}
	default:
		text := fmt.Sprint(typed)
		if key == "" {
			*lines = append(*lines, text)
		} else {
			*lines = append(*lines, key+": "+text)
		}
	}
}

func summarizeReadFile(arguments, output string) string {
	var input struct {
		Path      string `json:"path"`
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
	}
	_ = json.Unmarshal([]byte(arguments), &input)
	path := input.Path
	start, end := 0, 0
	for _, line := range strings.Split(output, "\n") {
		if path == "" && strings.HasPrefix(line, "¶") {
			path = strings.TrimPrefix(strings.SplitN(line, "#", 2)[0], "¶")
		}
		prefix, _, found := strings.Cut(line, ":")
		lineNumber, err := strconv.Atoi(strings.TrimSpace(prefix))
		if !found || err != nil || lineNumber < 1 {
			continue
		}
		if start == 0 || lineNumber < start {
			start = lineNumber
		}
		if lineNumber > end {
			end = lineNumber
		}
	}
	if start == 0 {
		start = input.StartLine
		end = input.EndLine
	}
	if path == "" {
		return output
	}
	if start > 0 && end >= start {
		return fmt.Sprintf("Read %s · lines %d-%d", path, start, end)
	}
	return "Read " + path
}

func (m *AppModel) updateAgent(event app.Event) {
	if event.AgentID == "" || event.Agent == nil {
		return
	}
	value := agentViewFromPayload(event.AgentID, event.State, event.Text, event.Agent)
	for index := range m.agents {
		if m.agents[index].ID == event.AgentID {
			value.Blocks = m.agents[index].Blocks
			m.agents[index] = value
			m.updateAgentBlock(event, value)
			return
		}
	}
	m.agents = append(m.agents, value)
	m.updateAgentBlock(event, value)
}

func (m *AppModel) updateAgentBlock(event app.Event, agent AgentView) {
	content := first(agent.Activity, agent.Summary, agent.Description)
	for index := range m.transcript {
		block := &m.transcript[index]
		if block.Kind == BlockAgent && block.ID == agent.ID {
			block.Title = first(agent.Role, "Subagent")
			block.Content = content
			block.State = agent.State
			return
		}
	}
	m.transcript = append(m.transcript, Block{
		ID: agent.ID, Kind: BlockAgent, RunID: first(agent.ParentRunID, event.RunID),
		ToolCallID: agent.ParentToolCallID, Title: first(agent.Role, "Subagent"),
		Content: content, State: agent.State,
	})
}

func agentViewFromPayload(id, state, summary string, payload *app.AgentStatePayload) AgentView {
	if payload == nil {
		return AgentView{ID: id, State: state, Summary: summary}
	}
	return AgentView{
		ID: id, Role: payload.Type, State: state, Summary: summary, Description: payload.Description,
		Model: payload.Model, Background: payload.Background, CapabilityMode: payload.CapabilityMode,
		RequestedIsolation: payload.RequestedIsolation, Isolation: payload.Isolation, CWD: payload.CWD,
		ParentRunID: payload.ParentRunID, ParentToolCallID: payload.ParentToolCallID,
		ChildRunID: payload.ChildRunID, Activity: payload.Activity, Warning: payload.Warning,
		WorktreePath: payload.WorktreePath, ToolCalls: payload.ToolCalls, Turns: payload.Turns,
		TokensUsed: payload.TokensUsed, ElapsedMS: payload.ElapsedMS,
	}
}

func (m *AppModel) updateAgentStream(event app.Event) {
	index := -1
	for candidate := range m.agents {
		if m.agents[candidate].ID == event.AgentID {
			index = candidate
			break
		}
	}
	if index < 0 {
		m.agents = append(m.agents, AgentView{ID: event.AgentID, Role: "Subagent", State: "running"})
		index = len(m.agents) - 1
	}
	agent := &m.agents[index]
	switch event.Kind {
	case app.EventThinkingDelta:
		agent.Activity = first(compactAgentActivity(event.Text), "thinking")
		appendAgentViewDelta(&agent.Blocks, BlockThinking, event.RunID, "Thinking", event.Text)
	case app.EventTextDelta:
		agent.Activity = "responding"
		appendAgentViewDelta(&agent.Blocks, BlockAssistant, event.RunID, "Assistant", event.Text)
	case app.EventToolStarted:
		agent.Activity = first(event.Data["name"], event.Text, "tool")
		upsertAgentTool(&agent.Blocks, event, "running", m.catalog)
	case app.EventToolUpdate:
		agent.Activity = first(event.Data["name"], event.Text, "tool update")
		upsertAgentTool(&agent.Blocks, event, "running", m.catalog)
	case app.EventToolFinished:
		agent.Activity = first(event.Data["name"], "tool finished")
		upsertAgentTool(&agent.Blocks, event, terminalToolState(event.State), m.catalog)
	case app.EventHookStarted, app.EventHookFinished, app.EventHookDiagnostic:
		m.updateHooks(&agent.Blocks, event)

	}
}

func (m *AppModel) hasRunningHooks() bool {
	for _, block := range m.transcript {
		for _, hook := range block.Hooks {
			if hook.State == "running" {
				return true
			}
		}
	}
	for _, agent := range m.agents {
		for _, block := range agent.Blocks {
			for _, hook := range block.Hooks {
				if hook.State == "running" {
					return true
				}
			}
		}
	}
	return false
}

func (m AppModel) hasRunningAgents() bool {
	for _, agent := range m.agents {
		switch strings.ToLower(agent.State) {
		case "initializing", "running", "cancelling":
			return true
		}
	}
	return false
}

func (m *AppModel) updateHooks(blocks *[]Block, event app.Event) {
	hook := hookRunFromEvent(event)
	if event.Kind == app.EventHookDiagnostic {
		*blocks = append(*blocks, Block{ID: event.ToolCallID, Kind: BlockHook, RunID: event.RunID, Title: hook.Event, State: "failed", Hooks: []HookRunView{hook}})
	} else if event.Kind == app.EventHookFinished {
		for i := len(*blocks) - 1; i >= 0; i-- {
			if (*blocks)[i].Kind == BlockHook && upsertMatchingHook(&(*blocks)[i].Hooks, hook) {
				if hook.State == "completed" {
					*blocks = append((*blocks)[:i], (*blocks)[i+1:]...)
					m.invalidateTranscriptLayout()
					return
				}
				(*blocks)[i].State = hook.State
				m.invalidateTranscriptLayout()
				return
			}
		}
		if hook.State != "completed" {
			*blocks = append(*blocks, Block{Kind: BlockHook, RunID: event.RunID, Title: hook.Event, State: hook.State, Hooks: []HookRunView{hook}})
		}
	} else {
		*blocks = append(*blocks, Block{Kind: BlockHook, RunID: event.RunID, Title: hook.Event, State: "running", Hooks: []HookRunView{hook}})
	}
	m.invalidateTranscriptLayout()
}

func (m *AppModel) invalidateTranscriptLayout() {
	if m.transcriptLayout != nil {
		m.transcriptLayout.initialized = false
	}
}

func hookRunFromEvent(event app.Event) HookRunView {
	d := event.Data
	h := HookRunView{Event: d["event"], Name: first(d["name"], "hook"), Source: filepath.Base(d["source"]), State: "running"}
	h.Key = h.Event + "\x00" + h.Name + "\x00" + h.Source
	h.DurationMS, _ = strconv.ParseInt(d["durationMS"], 10, 64)
	h.Truncated, _ = strconv.ParseBool(d["stdoutTruncated"])
	stderrTruncated, _ := strconv.ParseBool(d["stderrTruncated"])
	h.Truncated = h.Truncated || stderrTruncated
	if event.Kind == app.EventHookDiagnostic {
		h.State = "failed"
		h.Reason = compactHookText(first(d["reason"], event.Text), 3, 2048)
		return h
	}
	if event.Kind == app.EventHookFinished {
		h.State = first(event.State, d["state"], "completed")
		if h.State != "blocked" && h.State != "failed" && d["exitCode"] != "" && d["exitCode"] != "0" {
			h.State = "failed"
		}
		h.Reason = compactHookText(d["reason"], 3, 2048)
		stdout := strings.TrimSpace(d["stdout"])
		if strings.HasPrefix(stdout, "{") {
			stdout = ""
		}
		if h.State == "failed" || h.State == "blocked" {
			h.Output = compactHookText(first(h.Reason, d["stderr"], stdout), 3, 2048)
		} else {
			h.Output = compactHookText(stdout, 3, 2048)
		}
	}
	return h
}

func compactHookText(value string, lines, bytes int) string {
	parts := strings.Split(strings.TrimSpace(value), "\n")
	if len(parts) > lines {
		parts = parts[:lines]
	}
	value = strings.Join(parts, "\n")
	if len(value) > bytes {
		value = string([]byte(value)[:bytes])
		value = strings.ToValidUTF8(value, "") + "…"
	}
	return value
}

func upsertMatchingHook(hooks *[]HookRunView, hook HookRunView) bool {
	for i := len(*hooks) - 1; i >= 0; i-- {
		if (*hooks)[i].Key == hook.Key && (*hooks)[i].State == "running" {
			(*hooks)[i] = hook
			return true
		}
	}
	return false
}

func compactAgentActivity(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 80 {
		value = string(runes[:79]) + "…"
	}
	return value
}

func appendAgentViewDelta(blocks *[]Block, kind BlockKind, runID, title, content string) {
	if content == "" {
		return
	}
	if len(*blocks) > 0 {
		last := &(*blocks)[len(*blocks)-1]
		if last.Kind == kind && last.RunID == runID && !toolStateTerminal(last.State) {
			last.Content = appendStreamContent(last.Content, content, kind)
			return
		}
	}
	*blocks = append(*blocks, Block{
		ID: fmt.Sprintf("live-%s-%d", kind, len(*blocks)), Kind: kind, RunID: runID,
		Title: title, Content: content, State: "streaming",
	})
}

func upsertAgentTool(blocks *[]Block, event app.Event, state string, catalog i18n.Catalog) {
	for index := len(*blocks) - 1; index >= 0; index-- {
		block := &(*blocks)[index]
		if (block.Kind != BlockTool && block.Kind != BlockDiff) || block.ToolCallID != event.ToolCallID {
			continue
		}
		if event.Kind == app.EventToolFinished || !toolStateTerminal(block.State) {
			block.State = state
			if event.Kind == app.EventToolFinished && state == "completed" {
				if title, diff, ok := summarizeFileChange(block.Title, block.Arguments, event.Data["structured"], event.Text); ok {
					block.Kind, block.Title, block.Content = BlockDiff, title, diff
				} else {
					block.Content = summarizeToolResult(block.Title, block.Arguments, event.Text, catalog)
				}
			} else if event.Kind == app.EventToolFinished {
				block.Content = joinToolSummary(summarizeToolArguments(block.Title, block.Arguments), summarizeToolFailure(block.Title, event.Text))
			} else {
				appendBlockContent(block, event.Text)
			}
		}
		return
	}
	name := first(event.Data["name"], "Tool")
	content := first(event.Data["arguments"], event.Text)
	kind := BlockTool
	title := name
	if event.Kind == app.EventToolFinished && state == "completed" {
		if diffTitle, diff, ok := summarizeFileChange(name, event.Data["arguments"], event.Data["structured"], event.Text); ok {
			kind, title, content = BlockDiff, diffTitle, diff
		}
	}
	*blocks = append(*blocks, Block{
		ID: event.ToolCallID, Kind: kind, RunID: event.RunID, ToolCallID: event.ToolCallID,
		Title: title, Arguments: event.Data["arguments"], Content: content, State: state,
	})
}

func (m *AppModel) updateAgentDetail(event app.Event) {
	switch event.State {
	case "detail":
		for index := range m.agents {
			if m.agents[index].ID == event.AgentID {
				m.agents[index].Blocks = agentTranscriptBlocks(event.AgentBlocks, m.catalog)
				m.detailAgentID = event.AgentID
				m.openOverlay(OverlayAgentDetail)
				return
			}
		}
	case "agent_types":
		m.agentTypes = agentCatalogViews(event.AgentCatalog)
		m.openOverlay(OverlayAgentTypes)
	case "personas":
		m.personas = agentCatalogViews(event.AgentCatalog)
		m.openOverlay(OverlayPersonas)
	case "cancel_requested", "already_finished":
		m.errorBanner = event.Text
	}
}

func agentTranscriptBlocks(blocks []app.AgentTranscriptBlock, catalogs ...i18n.Catalog) []Block {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	result := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		kind := BlockKind(block.Kind)
		content, arguments := block.Content, ""
		if block.Kind == string(BlockTool) {
			arguments, content = splitAgentToolContent(block.Content)
			switch block.State {
			case "completed":
				if title, diff, ok := summarizeFileChange(block.Title, arguments, "", content); ok {
					kind, block.Title, content = BlockDiff, title, diff
				} else {
					content = summarizeToolResult(block.Title, arguments, content, catalog)
				}
			case "failed", "cancelled":
				content = joinToolSummary(summarizeToolArguments(block.Title, arguments), summarizeToolFailure(block.Title, content))
			default:
				content = summarizeToolArguments(block.Title, arguments)
			}
		}
		result = append(result, Block{
			ID: block.ID, Kind: kind, RunID: block.RunID, ToolCallID: block.ToolCallID,
			Title: block.Title, Arguments: arguments, Content: content, State: block.State,
		})
	}
	return result
}

func splitAgentToolContent(content string) (string, string) {
	arguments, rest, found := strings.Cut(content, "\n")
	if !found || !json.Valid([]byte(arguments)) {
		return "", content
	}
	var object map[string]any
	if json.Unmarshal([]byte(arguments), &object) != nil {
		return "", content
	}
	return arguments, rest
}

func agentCatalogViews(entries []app.AgentCatalogEntry) []AgentCatalogView {
	result := make([]AgentCatalogView, 0, len(entries))
	for _, entry := range entries {
		result = append(result, AgentCatalogView{
			Name: entry.Name, Description: entry.Description, Persona: entry.Persona, Model: entry.Model,
			Reasoning: entry.Reasoning, CapabilityMode: entry.CapabilityMode, Isolation: entry.Isolation,
			Source: entry.Source, Enabled: entry.Enabled,
		})
	}
	return result
}

func (m *AppModel) updateMCP(event app.Event) {
	if encoded := event.Data["servers"]; encoded != "" {
		if json.Unmarshal([]byte(encoded), &m.mcpServers) == nil {
			sort.Slice(m.mcpServers, func(i, j int) bool { return m.mcpServers[i].Name < m.mcpServers[j].Name })
		}
		return
	}
	name := first(event.Data["server"], event.AgentID)
	if name == "" {
		return
	}
	toolCount, _ := strconv.Atoi(event.Data["toolCount"])
	value := MCPView{Name: name, State: first(event.State, event.Data["state"]), ToolCount: toolCount, Error: first(event.Text, event.Data["error"])}
	for index := range m.mcpServers {
		if m.mcpServers[index].Name == name {
			m.mcpServers[index] = value
			return
		}
	}
	m.mcpServers = append(m.mcpServers, value)
	sort.Slice(m.mcpServers, func(i, j int) bool { return m.mcpServers[i].Name < m.mcpServers[j].Name })
}

func (m *AppModel) updateAuth(event app.Event) {
	provider := first(event.Data["provider"], event.AgentID)
	if provider == "" {
		return
	}
	m.auth[provider] = AuthView{
		Provider: provider, AccountID: event.Data["accountID"], Email: event.Data["email"],
		DisplayName: event.Data["displayName"], Plan: event.Data["plan"], State: first(event.State, event.Data["state"]),
	}
}

func (m *AppModel) loadModels(event app.Event) {
	provider := first(event.Data["provider"], m.provider)
	var choices []ModelChoice
	if err := json.Unmarshal([]byte(event.Data["models"]), &choices); err != nil {
		m.errorBanner = "decode model catalog: " + err.Error()
		return
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].ID < choices[j].ID })
	if m.modelsByProvider == nil {
		m.modelsByProvider = make(map[string][]ModelChoice)
	}
	m.modelsByProvider[provider] = choices
	if provider == m.provider {
		m.selectModels(choices)
	}
}

func (m *AppModel) switchProvider(provider string) {
	if provider == "" {
		return
	}
	if provider != m.provider {
		m.usage.InputTokens = 0
		m.usage.CacheInputTokens = 0
		m.usage.CachedInputTokens = 0
		m.usage.MainCacheInput = 0
		m.usage.MainCachedInput = 0
		m.usage.OutputTokens = 0
		m.usage.CacheReported = false
		m.usage.MainCacheReported = false
	}
	m.provider = provider
	m.selectModels(m.modelsByProvider[provider])
}

func (m *AppModel) selectModels(choices []ModelChoice) {
	if m.modelsByProvider == nil {
		m.modelsByProvider = make(map[string][]ModelChoice)
	}
	m.modelsByProvider[m.provider] = choices
	m.models = choices
	for _, choice := range choices {
		if choice.ID == m.model {
			m.selectModel(m.model)
			return
		}
	}
	if len(choices) == 0 {
		m.selectModel("")
		return
	}
	m.selectModel(choices[0].ID)
}

func (m *AppModel) selectModel(modelID string) {
	if modelID != m.model {
		m.usage.InputTokens = 0
		m.usage.CacheInputTokens = 0
		m.usage.CachedInputTokens = 0
		m.usage.MainCacheInput = 0
		m.usage.MainCachedInput = 0
		m.usage.OutputTokens = 0
		m.usage.CacheReported = false
		m.usage.MainCacheReported = false
	}
	m.model = modelID
	m.usage.ContextLimit = 0
	for _, choice := range m.models {
		if choice.ID == modelID {
			m.usage.ContextLimit = choice.ContextWindow
			break
		}
	}
	m.syncReasoningForModel()
}

func (m *AppModel) updateUsage(data map[string]string) {
	if data == nil {
		return
	}
	inputTokens, inputErr := strconv.Atoi(data["inputTokens"])
	if inputErr == nil && data["inputTokens"] != "" {
		if data["aggregateOnly"] != "true" {
			m.usage.InputTokens = inputTokens
			if data["cacheStatus"] == "reported" {
				m.usage.MainCacheInput += inputTokens
			}
		}
		if data["cacheStatus"] == "reported" {
			m.usage.CacheInputTokens += inputTokens
		}
	}
	if value, err := strconv.Atoi(data["cachedInputTokens"]); err == nil && data["cachedInputTokens"] != "" {
		m.usage.CachedInputTokens += value
		m.usage.CacheReported = true
		if data["aggregateOnly"] != "true" {
			m.usage.MainCachedInput += value
			m.usage.MainCacheReported = true
		}
	}
	if value, err := strconv.Atoi(data["outputTokens"]); err == nil && data["outputTokens"] != "" {
		if data["aggregateOnly"] != "true" {
			m.usage.OutputTokens = value
		}
	}
	if value, err := strconv.Atoi(data["contextLimit"]); err == nil && data["contextLimit"] != "" {
		m.usage.ContextLimit = value
	}
}

func (m *AppModel) resetTurnUsage() {
	m.usage.InputTokens = 0
	m.usage.CacheInputTokens = 0
	m.usage.CachedInputTokens = 0
	m.usage.MainCacheInput = 0
	m.usage.MainCachedInput = 0
	m.usage.OutputTokens = 0
	m.usage.CacheReported = false
	m.usage.MainCacheReported = false
}

func (m AppModel) isRunning() bool {
	return m.status == "Starting" || m.status == "Running" || m.status == "Awaiting approval" || m.status == "Reviewing approval" || m.status == "Cancelling"
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
