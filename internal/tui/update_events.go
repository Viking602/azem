package tui

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/session"
)

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
		if event.State != "list" {
			m.recap = cloneRecap(event.Recap)
		}
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
		m.appendDelta(BlockThinking, event.RunID, m.tr("block.thinking_title"), event.Text)
	case app.EventTextDelta:
		m.appendDelta(BlockAssistant, event.RunID, "Azem", event.Text)
	case app.EventToolStarted, app.EventToolUpdate, app.EventToolFinished:
		m.updateTool(event)
	case app.EventHookStarted, app.EventHookFinished, app.EventHookDiagnostic:
		m.updateHooks(&m.transcript, event)
	case app.EventDiffReady:
		block := Block{ID: event.ToolCallID, Kind: BlockDiff, RunID: event.RunID, Title: first(event.Data["path"], m.tr("block.diff_title")), Content: event.Text, State: first(event.State, "ready")}
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
	case app.EventModelRoutes:
		cursorScope, cursorRole := "", ""
		if m.pendingModelRoute != nil {
			cursorScope, cursorRole = m.pendingModelRoute.Entry.Scope, m.pendingModelRoute.Entry.Role
		} else if m.overlay == OverlayModelRoutes && m.overlayCursor < len(m.modelRoutes) {
			cursorScope, cursorRole = m.modelRoutes[m.overlayCursor].Scope, m.modelRoutes[m.overlayCursor].Role
		}
		m.modelRoutes = append([]app.ModelRouteEntry(nil), event.ModelRoutes...)
		m.pendingModelRoute = nil
		m.openOverlay(OverlayModelRoutes)
		for index, entry := range m.modelRoutes {
			if entry.Scope == cursorScope && entry.Role == cursorRole {
				m.overlayCursor = index
				break
			}
		}
	case app.EventSkillCatalog:
		m.skills = append([]SkillCatalogView(nil), event.SkillCatalog...)
		m.skillDiagnostics = append([]app.SkillDiagnostic(nil), event.SkillDiagnostics...)
		m.openOverlay(OverlaySkills)
	case app.EventMemoryState:
		switch event.State {
		case "recalled":
			count, _ := strconv.Atoi(event.Data["count"])
			if count > 0 {
				m.transcript = append(m.transcript, Block{
					ID: "memory-recall:" + first(event.RunID, event.SessionID), Kind: BlockHook, State: "completed",
					Hooks: []HookRunView{{Event: m.tr("memory.recalled", map[string]string{"count": strconv.Itoa(count)}), State: "completed"}},
				})
			}
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
		if event.State != "recalled" {
			m.openOverlay(OverlayMemory)
		}
	case app.EventRecapState:
		if event.State == "failed" {
			m.errorBanner = m.tr("recap.persist_failed") + ": " + event.Text
			break
		}
		m.recap = cloneRecap(event.Recap)
		if event.State != "updated" {
			m.openOverlay(OverlayRecap)
		}
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
		m.transcript = append(m.transcript, Block{Kind: BlockError, RunID: event.RunID, Title: m.tr("error.run_failed"), Content: event.Text, State: "failed"})
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

func cloneRecap(value *recap.Recap) *recap.Recap {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (m AppModel) acceptRunEvent(event app.Event) bool {
	if event.Kind == app.EventRecapState {
		return true
	}
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
	fallbackMessage := m.tr("tool.parent_failed")
	switch status {
	case "Cancelled":
		fallbackState = "cancelled"
		fallbackMessage = m.tr("tool.parent_cancelled")
	case "Ready":
		orphaned = true
		fallbackMessage = m.tr("tool.orphaned")
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
			m.errorBanner = m.tr("error.list_sessions") + ": " + err.Error()
			return
		}
		m.status = "Ready"
		m.openOverlay(OverlaySessions)
		return
	}
	var recovered []struct {
		ID               string               `json:"id"`
		Kind             string               `json:"kind"`
		RunID            string               `json:"runId"`
		AgentID          string               `json:"agentId"`
		ParentToolCallID string               `json:"parentToolCallId"`
		Title            string               `json:"title"`
		Content          string               `json:"content"`
		State            string               `json:"state"`
		Collapsed        bool                 `json:"collapsed"`
		Attachments      []session.Attachment `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(event.Data["blocks"]), &recovered); err != nil {
		m.errorBanner = m.tr("error.recover_session") + ": " + err.Error()
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
		content := block.Content
		if BlockKind(block.Kind) == BlockUser {
			content = formatUserContent(block.Content, block.Attachments)
		}
		m.transcript = append(m.transcript, Block{
			ID: first(block.AgentID, block.ID), Kind: BlockKind(block.Kind), RunID: block.RunID,
			ToolCallID: block.ParentToolCallID, Title: block.Title, Content: content,
			State: block.State, Collapsed: block.Collapsed, Attachments: block.Attachments,
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
	m.clearPendingImages()
	m.status = "Ready"
	provider := first(event.Data["provider"], m.provider)
	m.switchProvider(provider)
	m.selectModel(first(event.Data["model"], m.model))
	m.reasoning = first(event.Data["reasoning"], m.reasoning)
	m.syncReasoningForModel()
	m.agentMode = first(event.Data["agentMode"], m.agentMode)
	m.restoreUsage(event.Data["usage"])
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
		m.errorBanner = m.tr("error.load_recovery") + ": " + err.Error()
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
			name := first(event.Data["name"], event.Data["tool"], m.tr("block.tool_title"))
			if name == "todo" {
				arguments = ""
			}
			m.transcript = append(m.transcript, Block{
				ID: id, Kind: BlockTool, RunID: event.RunID, ToolCallID: id, Title: name,
				Arguments: arguments, Content: first(event.Text, map[bool]string{true: m.tr("tool.todo_progress"), false: summarizeToolArguments(name, arguments, m.catalog)}[name == "todo"]), State: "running",
			})
			return
		}
		block := &m.transcript[index]
		if toolStateTerminal(block.State) {
			return
		}
		block.State = "running"
		appendBlockContent(block, first(event.Text, summarizeToolArguments(block.Title, event.Data["arguments"], m.catalog)))
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
			title := first(event.Data["name"], event.Data["tool"], m.tr("block.tool_title"))
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
			block.Content = strings.TrimSuffix(block.Content, "\n"+m.tr("tool.orphaned"))
			if block.Content == m.tr("tool.orphaned") {
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
			block.Content = joinToolSummary(summarizeToolArguments(block.Title, block.Arguments, m.catalog), summarizeToolFailure(block.Title, event.Text, m.catalog))
		}
	}
}
