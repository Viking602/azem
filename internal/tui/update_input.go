package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/i18n"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/session"
)

var writeClipboard = clipboard.WriteAll
var readClipboardText = clipboard.ReadAll

type clipboardWriteResultMsg struct{ err error }

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
		// The rounded panel is external to textarea; reserve its border and padding.
		m.composer.SetWidth(max(1, m.width-m.theme.PanelFocused.GetHorizontalFrameSize()))
		m.modelSearch.SetWidth(max(1, min(64, m.width-12)))
		m.transcriptTop = min(m.transcriptTop, m.transcriptMaxOffset())
		if m.overlay == OverlayRecap {
			m.overlayScroll = min(m.overlayScroll, m.recapScrollLimit())
		}
		return m, nil
	case tea.MouseClickMsg:
		return m.startTranscriptSelection(msg.Mouse())
	case tea.MouseMotionMsg:
		return m.extendTranscriptSelection(msg.Mouse())
	case tea.MouseReleaseMsg:
		return m.finishTranscriptSelection(msg.Mouse())
	case tea.MouseWheelMsg:
		m.transcriptSelection = nil
		if m.overlay != OverlayNone {
			switch msg.Button {
			case tea.MouseWheelUp:
				if m.overlay == OverlayRecap {
					m.scrollRecap(-3)
					return m, nil
				}
				return m.updateOverlayKey("up")
			case tea.MouseWheelDown:
				if m.overlay == OverlayRecap {
					m.scrollRecap(3)
					return m, nil
				}
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
	case clipboardWriteResultMsg:
		if msg.err != nil {
			m.errorBanner = m.tr("error.copy_selection", map[string]string{"detail": msg.err.Error()})
		}
		return m, nil
	case clipboardImageResultMsg:
		if msg.err != nil {
			m.errorBanner = m.tr("error.paste_image", map[string]string{"detail": msg.err.Error()})
			return m, nil
		}
		if msg.empty {
			// No image on clipboard; fall back to text paste into composer.
			if text, err := readClipboardText(); err == nil && text != "" {
				m.composer.InsertString(text)
				m.commandCursor = 0
			}
			return m, nil
		}
		if err := m.appendPendingImage(msg.attachment); err != nil {
			m.errorBanner = err.Error()
			return m, nil
		}
		m.errorBanner = ""
		return m, nil
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
		if (m.isRunning() || m.hasRunningHooks() || m.hasRunningAgents()) && !m.animationActive {
			m.animationActive = true
			commands = append(commands, nextRunFeedbackFrame(m.reducedMotion))
		}
		return m, tea.Batch(commands...)
	case animationTickMsg:
		if !m.isRunning() && !m.hasRunningHooks() && !m.hasRunningAgents() {
			m.animationActive = false
			m.animationFrame = 0
			return m, nil
		}
		if !m.reducedMotion {
			m.animationFrame++
		}
		return m, nextRunFeedbackFrame(m.reducedMotion)
	case startTurnResultMsg:
		if msg.Err != nil {
			m.status = "Ready"
			m.runID = ""
			m.errorBanner = msg.Err.Error()
			m.transcript = append(m.transcript, Block{Kind: BlockError, Title: m.tr("error.run_rejected"), Content: msg.Err.Error(), State: "failed"})
		} else if (m.status == "Starting" || m.status == "Running" || m.status == "Cancelling") && (m.runID == "" || m.runID == msg.RunID) {
			m.runID = msg.RunID
		}
		return m, nil
	case guidanceResultMsg:
		if msg.Err != nil {
			m.errorBanner = msg.Err.Error()
			m.transcript = append(m.transcript, Block{Kind: BlockError, Title: m.tr("error.guidance_rejected"), Content: msg.Err.Error(), State: "failed"})
			if m.composer.Value() == "" {
				m.composer.SetValue(msg.Text)
			}
		} else {
			m.transcript = append(m.transcript, Block{Kind: BlockUser, RunID: msg.RunID, Title: m.tr("block.guidance"), Content: msg.Text, State: "guidance"})
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
			if msg.Action.Kind == ActionSetModelRoute || msg.Action.Kind == ActionResetModelRoute {
				m.pendingModelRoute = nil
				if len(m.modelRoutes) > 0 {
					m.openOverlay(OverlayModelRoutes)
				}
			}
			if errors.Is(msg.Err, context.Canceled) {
				m.status = "Ready"
				m.errorBanner = m.tr("error.action_cancelled")
				return m, nil
			}
			if errors.Is(msg.Err, errActionUnsupported) {
				m.errorBanner = m.tr("error.action_unavailable")
				return m, nil
			}
			if msg.Action.Kind == ActionCompact {
				m.status = "Ready"
			}
			if errors.Is(msg.Err, app.ErrNothingToCompact) {
				m.errorBanner = m.tr("compact.nothing_to_compact")
			} else {
				m.errorBanner = msg.Err.Error()
			}
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

func nextRunFeedbackFrame(reducedMotion bool) tea.Cmd {
	if reducedMotion {
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return animationTickMsg{} })
	}
	return nextAnimationFrame()
}

func (m AppModel) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.transcriptSelection = nil
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
		if strings.TrimSpace(m.composer.Value()) == "" && m.dropLastPendingImage() {
			m.errorBanner = ""
			return m, nil
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
		if m.actionBusy {
			return m, nil
		}
		if m.isRunning() && !m.canGuideActiveRun() {
			return m, nil
		}
		return m.submit()
	case "ctrl+v", "super+v", "cmd+v", "meta+v":
		// Prefer clipboard image paste; text paste is the fallback when empty.
		return m, pasteClipboardImage(m.runtime, m.sessionID, m.tr("attachment.unavailable"))
	}

	previous := m.composer.Value()
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	if m.composer.Value() != previous {
		m.commandCursor = 0
	}
	return m, cmd
}

func (m AppModel) startTranscriptSelection(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if mouse.Button != tea.MouseLeft || m.overlay != OverlayNone {
		m.transcriptSelection = nil
		return m, nil
	}
	left, top, width, height := m.composerBounds()
	if mouse.X >= left && mouse.X < left+width && mouse.Y >= top && mouse.Y < top+height {
		m.transcriptSelection = nil
		m.focus = focusComposer
		return m, m.composer.Focus()
	}
	_, top, width, height = m.transcriptBounds()
	if width <= 0 || height <= 0 || mouse.X < 0 || mouse.X >= width || mouse.Y < top || mouse.Y >= top+height {
		m.transcriptSelection = nil
		return m, nil
	}
	m.transcriptSelection = &transcriptSelection{startX: mouse.X, startY: mouse.Y - top, endX: mouse.X, endY: mouse.Y - top}
	return m, nil
}

func (m AppModel) extendTranscriptSelection(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if m.transcriptSelection == nil || mouse.Button != tea.MouseLeft {
		return m, nil
	}
	_, top, width, height := m.transcriptBounds()
	m.transcriptSelection.endX = min(max(0, mouse.X), max(0, width-1))
	m.transcriptSelection.endY = min(max(0, mouse.Y-top), max(0, height-1))
	return m, nil
}

func (m AppModel) finishTranscriptSelection(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if m.transcriptSelection == nil {
		return m, nil
	}
	_, command := m.extendTranscriptSelection(tea.Mouse{X: mouse.X, Y: mouse.Y, Button: tea.MouseLeft})
	selection := m.transcriptSelection
	if selection.startX == selection.endX && selection.startY == selection.endY {
		m.transcriptSelection = nil
		if m.toggleTranscriptBlockAt(selection.endX, selection.endY) {
			return m, nil
		}
	}
	text := m.selectedTranscriptText()
	if text == "" {
		return m, command
	}
	return m, func() tea.Msg { return clipboardWriteResultMsg{err: writeClipboard(text)} }
}

func (m *AppModel) toggleTranscriptBlockAt(x, row int) bool {
	_, _, width, height := m.transcriptBounds()
	if x < 0 || x >= width {
		return false
	}
	index, ok := m.transcriptBlockHeaderAt(row, width, height)
	if !ok || index < 0 || index >= len(m.transcript) {
		return false
	}
	block := &m.transcript[index]
	if block.Kind != BlockTool && block.Kind != BlockDiff && block.Kind != BlockError {
		return false
	}
	block.Collapsed = !block.Collapsed
	m.focus = focusTranscript
	m.transcriptCursor = index
	m.composer.Blur()
	return true
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
		if (m.overlay == OverlayModel || m.overlay == OverlayReasoning) && m.pendingModelRoute != nil {
			m.modelSearch.Reset()
			m.modelSearch.Blur()
			m.pendingModelRoute = nil
			m.openOverlay(OverlayModelRoutes)
			return m, nil
		}
		if m.overlay == OverlayCancel {
			m.status = "Running"
		}
		m.cancelPendingAction()
		return m, m.closeOverlay()
	}
	if m.actionBusy {
		return m, nil
	}
	if m.overlay == OverlayRecap {
		switch key {
		case "up", "k":
			m.scrollRecap(-1)
			return m, nil
		case "down", "j":
			m.scrollRecap(1)
			return m, nil
		case "pgup":
			m.scrollRecap(-max(1, m.height/3))
			return m, nil
		case "pgdown":
			m.scrollRecap(max(1, m.height/3))
			return m, nil
		case "home":
			m.overlayScroll = 0
			return m, nil
		case "end":
			m.overlayScroll = m.recapScrollLimit()
			return m, nil
		}
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
		if m.overlay == OverlayModelRoutes && m.overlayCursor < len(m.modelRoutes) {
			entry := m.modelRoutes[m.overlayCursor]
			return m.beginAction(Action{Kind: ActionResetModelRoute, Route: &entry})
		}
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
	case OverlayModelRoutes:
		return len(m.modelRoutes)
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
		if m.pendingModelRoute != nil {
			entries := m.modelPickerEntries()
			if m.overlayCursor >= len(entries) {
				return m, nil
			}
			entry := entries[m.overlayCursor]
			m.pendingModelRoute.Provider = entry.Provider
			m.pendingModelRoute.Model = entry.Model.ID
			levels := catalog.AvailableReasoningLevels(entry.Provider, entry.Model)
			if len(levels) == 0 {
				return m.savePendingModelRoute("")
			}
			if !contains(levels, m.pendingModelRoute.Reasoning) {
				m.pendingModelRoute.Reasoning = catalog.PreferredReasoningLevel(entry.Provider, entry.Model)
			}
			m.openOverlay(OverlayReasoning)
			return m, nil
		}
		if m.isRunning() {
			m.errorBanner = m.tr("error.model_idle")
			return m, nil
		}
		entries := m.modelPickerEntries()
		if m.overlayCursor >= len(entries) {
			return m, nil
		}
		entry := entries[m.overlayCursor]
		levels := catalog.AvailableReasoningLevels(entry.Provider, entry.Model)
		if len(levels) == 0 {
			m.switchProvider(entry.Provider)
			m.selectModel(entry.Model.ID)
			return m, m.closeOverlay()
		}
		m.pendingSessionModel = &pendingSessionModel{Provider: entry.Provider, Model: entry.Model.ID}
		m.openOverlay(OverlayReasoning)
		return m, nil
	case OverlayLanguage:
		languages := i18n.Languages()
		if m.overlayCursor >= 0 && m.overlayCursor < len(languages) {
			language := languages[m.overlayCursor]
			_ = m.closeOverlay()
			return m.beginAction(Action{Kind: ActionSetLanguage, Target: language})
		}
	case OverlayReasoning:
		if m.pendingModelRoute != nil {
			levels := m.reasoningLevels()
			if m.overlayCursor < len(levels) {
				return m.savePendingModelRoute(levels[m.overlayCursor])
			}
			return m, nil
		}
		if pending := m.pendingSessionModel; pending != nil {
			if m.isRunning() {
				m.errorBanner = m.tr("error.model_idle")
				return m, nil
			}
			levels := m.reasoningLevels()
			if m.overlayCursor >= len(levels) {
				return m, nil
			}
			reasoning := levels[m.overlayCursor]
			m.pendingSessionModel = nil
			m.switchProvider(pending.Provider)
			m.selectModel(pending.Model)
			m.reasoning = reasoning
			return m, m.closeOverlay()
		}
		if m.isRunning() {
			m.errorBanner = m.tr("error.reasoning_idle")
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
			m.approval = &ApprovalView{ToolCallID: item.ID, Tool: m.tr("approval.recovered"), Target: item.TaskID, Risk: item.Detail, Effect: m.tr("approval.effect"), Action: item.Detail}
			m.openOverlay(OverlayApproval)
			return m, nil
		}
		m.errorBanner = m.tr("recovery.reconcile_instruction", map[string]string{"id": item.ID})
		return m, nil
	case OverlayDiff:
		return m, m.closeOverlay()
	case OverlayModelRoutes:
		if m.overlayCursor < len(m.modelRoutes) {
			entry := m.modelRoutes[m.overlayCursor]
			m.pendingModelRoute = &pendingModelRoute{Entry: entry, Provider: entry.Route.Provider, Model: entry.Route.Model, Reasoning: entry.Route.Reasoning}
			m.openOverlay(OverlayModel)
		}
	}
	return m, nil
}

func (m AppModel) savePendingModelRoute(reasoning string) (tea.Model, tea.Cmd) {
	pending := m.pendingModelRoute
	if pending == nil {
		return m, nil
	}
	pending.Reasoning = reasoning
	entry := pending.Entry
	entry.Route.Provider, entry.Route.Model, entry.Route.Reasoning = pending.Provider, pending.Model, reasoning
	return m.beginAction(Action{Kind: ActionSetModelRoute, Route: &entry})
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
	case "model-routing":
		return m.beginAction(Action{Kind: ActionListModelRoutes})
	case "skills":
		return m.beginAction(Action{Kind: ActionListSkills})
	case "reasoning":
		m.openOverlay(OverlayReasoning)
	case "sessions":
		return m.beginAction(Action{Kind: ActionListSessions})
	case "new":
		return m.beginAction(Action{Kind: ActionNewSession})
	case "recap":
		return m.beginAction(Action{Kind: ActionShowRecap})
	case "status":
		m.openOverlay(OverlayStatus)
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
	if hasActiveChildren(m.runtime) {
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
		m.errorBanner = m.tr("error.approval_unavailable")
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.actionBusy = true
	m.actionCancel = cancel
	action.SessionID = first(action.SessionID, m.sessionID)
	m.errorBanner = ""
	if action.Kind == ActionCompact {
		m.status = "Compacting"
	}
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
	case ActionCompact:
		m.status = "Ready"
	case ActionNewSession, ActionResumeSession:
		m.status = "Ready"
		_ = m.closeOverlay()
	case ActionCancelAgent:
		m.errorBanner = m.tr("action.cancellation_requested", map[string]string{"target": action.Target})
	case ActionRefreshMCP, ActionReconnectMCP:
		m.errorBanner = m.tr("action.mcp_requested", map[string]string{"target": action.Target})
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
	images := append([]session.Attachment(nil), m.pendingImages...)
	if input == "" && len(images) == 0 {
		return m, nil
	}
	if command, ok, err := ParseCommand(input); ok {
		m.composer.Reset()
		m.commandCursor = 0
		if err != nil {
			m.errorBanner = m.tr("error.empty_command")
			return m, nil
		}
		return m.executeCommand(command)
	}
	if m.canGuideActiveRun() {
		if len(images) > 0 {
			m.errorBanner = m.tr("error.guidance_images")
			return m, nil
		}
		m.composer.Reset()
		m.commandCursor = 0
		m.errorBanner = ""
		return m, guideActiveTurn(m.runtime, m.sessionID, m.runID, input)
	}
	if m.isRunning() {
		return m, nil
	}
	m.transcript = append(m.transcript, Block{Kind: BlockUser, Title: m.tr("block.you"), Content: formatUserContent(input, images), Attachments: images})
	m.composer.Reset()
	m.commandCursor = 0
	m.clearPendingImages()
	m.status = "Starting"
	m.errorBanner = ""
	m.runID = ""
	m.resetTurnUsage()
	m.transcriptTop = 0
	m.beginRunActivity()
	return m, startTurn(m.runtime, app.TurnRequest{SessionID: m.sessionID, Prompt: input, Provider: m.provider, Model: m.model, Reasoning: m.reasoning, AgentMode: m.agentMode, Images: images})
}

type guidanceResultMsg struct {
	RunID string
	Text  string
	Err   error
}

func guideActiveTurn(runtime Runtime, sessionID, runID, text string) tea.Cmd {
	return func() tea.Msg {
		guided, ok := runtime.(interface {
			GuideActiveTurn(string, string, string) error
		})
		if !ok {
			return guidanceResultMsg{RunID: runID, Text: text, Err: fmt.Errorf("active-run guidance is unsupported")}
		}
		return guidanceResultMsg{RunID: runID, Text: text, Err: guided.GuideActiveTurn(sessionID, runID, text)}
	}
}

func (m AppModel) canGuideActiveRun() bool {
	if m.agentMode != "single" || m.runID == "" {
		return false
	}
	return m.status == "Running" || m.status == "Awaiting approval" || m.status == "Reviewing approval"
}

func (m AppModel) executeCommand(command Command) (tea.Model, tea.Cmd) {
	switch command.Name {
	case "model-routing":
		return m.beginAction(Action{Kind: ActionListModelRoutes})
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
	case "status":
		if len(command.Args) != 0 {
			m.errorBanner = m.tr("status.usage")
			break
		}
		m.openOverlay(OverlayStatus)
	case "quit":
		return m.beginShutdown()
	case "cancel":
		if m.isRunning() {
			return m.requestTurnCancellation()
		}
	case "team":
		if m.isRunning() {
			m.errorBanner = m.tr("error.agent_idle")
			break
		}
		if len(command.Args) != 1 || (command.Args[0] != "on" && command.Args[0] != "off") {
			m.errorBanner = m.tr("command.usage.team")
			break
		}
		if command.Args[0] == "on" {
			m.agentMode = "team"
		} else {
			m.agentMode = "single"
		}
	case "provider":
		if m.isRunning() {
			m.errorBanner = m.tr("error.provider_idle")
			break
		}
		if len(command.Args) == 0 {
			m.openOverlay(OverlayProvider)
			break
		}
		if len(command.Args) != 1 {
			m.errorBanner = m.tr("command.usage.provider")
			break
		}
		provider := strings.ToLower(command.Args[0])
		if provider != "chatgpt" && provider != "grok" {
			m.errorBanner = m.tr("provider.invalid")
			break
		}
		m.switchProvider(provider)
	case "login":
		if len(command.Args) > 2 {
			m.errorBanner = m.tr("command.usage.login")
			break
		}
		if len(command.Args) >= 1 {
			provider := strings.ToLower(command.Args[0])
			if provider != "chatgpt" && provider != "grok" {
				m.errorBanner = m.tr("provider.invalid")
				break
			}
			target := provider
			if len(command.Args) == 2 {
				if (provider == "chatgpt" && command.Args[1] != "--import-codex") || (provider == "grok" && command.Args[1] != "--import") {
					m.errorBanner = m.tr("command.usage.login_import")
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
			m.errorBanner = m.tr("command.usage.logout")
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
		m.errorBanner = m.tr("command.usage.skills")
	case "skill":
		if len(command.Args) == 0 {
			m.errorBanner = m.tr("command.usage.skill")
			break
		}
		if m.isRunning() {
			m.errorBanner = m.tr("error.skill_idle")
			break
		}
		if m.agentMode == "team" {
			m.errorBanner = m.tr("error.skill_single_mode")
			break
		}
		name := strings.ToLower(command.Args[0])
		prompt := fmt.Sprintf("Apply the %q skill to the current workspace and report the result.", name)
		displayPrompt := m.tr("skill.invoke_prompt", map[string]string{"name": name})
		if len(command.Args) > 1 {
			prompt = strings.Join(command.Args[1:], " ")
			displayPrompt = prompt
		}
		m.transcript = append(m.transcript, Block{Kind: BlockUser, Title: m.tr("block.you"), Content: displayPrompt})
		m.status = "Starting"
		m.errorBanner = ""
		m.runID = ""
		m.resetTurnUsage()
		m.transcriptTop = 0
		m.beginRunActivity()
		return m, startTurn(m.runtime, app.TurnRequest{
			SessionID: m.sessionID, Prompt: prompt, Provider: m.provider, Model: m.model,
			Reasoning: m.reasoning, AgentMode: m.agentMode, ActiveSkills: []string{name},
		})
	case "models":
		if len(command.Args) != 0 {
			m.errorBanner = m.tr("command.usage.models")
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
			m.errorBanner = m.tr("error.reasoning_unsupported")
			break
		}
		if len(command.Args) != 1 || !contains(levels, command.Args[0]) {
			m.errorBanner = m.tr("command.usage.reasoning", map[string]string{"levels": strings.Join(levels, "|")})
			break
		}
		if m.isRunning() {
			m.errorBanner = m.tr("error.reasoning_idle")
			break
		}
		m.reasoning = command.Args[0]
	case "new":
		return m.beginAction(Action{Kind: ActionNewSession})
	case "sessions":
		return m.beginAction(Action{Kind: ActionListSessions})
	case "resume":
		if len(command.Args) != 0 {
			m.errorBanner = m.tr("command.usage.resume")
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
			m.errorBanner = m.tr("command.usage.agents")
			break
		}
		return m.beginAction(Action{Kind: ActionCancelAgent, Target: command.Args[1]})
	case "todo", "todos":
		if len(command.Args) != 0 {
			m.errorBanner = m.tr("command.usage.todos")
			break
		}
		m.openOverlay(OverlayTodos)
	case "agent-types":
		if len(command.Args) != 0 {
			m.errorBanner = m.tr("command.usage.agent_types")
			break
		}
		return m.beginAction(Action{Kind: ActionListAgentTypes})
	case "personas":
		if len(command.Args) != 0 {
			m.errorBanner = m.tr("command.usage.personas")
			break
		}
		return m.beginAction(Action{Kind: ActionListPersonas})
	case "mcp":
		if len(command.Args) == 0 {
			m.openOverlay(OverlayMCP)
			break
		}
		if len(command.Args) != 2 || (command.Args[0] != "refresh" && command.Args[0] != "reconnect") {
			m.errorBanner = m.tr("command.usage.mcp")
			break
		}
		kind := ActionRefreshMCP
		if command.Args[0] == "reconnect" {
			kind = ActionReconnectMCP
		}
		return m.beginAction(Action{Kind: kind, Target: command.Args[1]})
	case "reconcile":
		if len(command.Args) != 2 {
			m.errorBanner = m.tr("command.usage.reconcile")
			break
		}
		return m.beginAction(Action{Kind: ActionReconcileAttempt, Target: command.Args[0], Decision: command.Args[1]})
	default:
		m.errorBanner = m.tr("command.unknown", map[string]string{"name": command.Name})
	}
	return m, nil
}
