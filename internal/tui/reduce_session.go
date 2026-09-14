package tui

import (
	"sort"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

func (m *AppModel) reduceSessionProjectionEvent(event app.Event) bool {
	if event.Kind != app.EventSessionProjection || event.SessionProjection == nil {
		return false
	}
	if event.SessionID != "" && event.SessionID != m.sessionID {
		return true
	}
	m.applyTypedSessionProjection(*event.SessionProjection, nil)
	return true
}

func (m *AppModel) applyTypedSessionProjection(projection app.SessionProjection, live []app.LiveBlockProjection) {
	if projection.Session.ID == "" {
		return
	}
	m.sessionID = projection.Session.ID
	m.lastRunID = projection.LastRunID
	m.todo = projection.Todo.Clone()
	m.recap = projection.Recap
	m.agents = make([]AgentView, 0, len(projection.AgentSnapshots))
	for _, snapshot := range projection.AgentSnapshots {
		m.agents = append(m.agents, agentViewFromPayload(snapshot.ID, snapshot.State, snapshot.Summary, &snapshot.Agent))
	}
	m.planReview = nil
	m.planningInput = nil
	m.planningOther = false
	m.planMode = false
	toolRecords := append([]session.ToolRecord(nil), projection.ToolRecords...)
	sort.Slice(toolRecords, func(left, right int) bool {
		if toolRecords[left].AnchorSequence == toolRecords[right].AnchorSequence {
			if toolRecords[left].StartedAt.Equal(toolRecords[right].StartedAt) {
				return toolRecords[left].ToolCallID < toolRecords[right].ToolCallID
			}
			return toolRecords[left].StartedAt.Before(toolRecords[right].StartedAt)
		}
		return toolRecords[left].AnchorSequence < toolRecords[right].AnchorSequence
	})
	m.transcript = make([]Block, 0, len(projection.Blocks)+len(toolRecords)+len(live))
	toolIndex := 0
	for _, block := range projection.Blocks {
		kind := BlockKind(block.Kind)
		content := block.Content
		if kind == BlockUser {
			content = formatUserContent(block.Content, block.Attachments)
		}
		m.transcript = append(m.transcript, Block{
			ID: block.ID, Sequence: block.Sequence, AgentID: block.AgentID, Kind: kind, RunID: block.RunID,
			TextPhase: block.TextPhase, Data: cloneBlockData(block.Data), ToolCallID: block.ToolCallID,
			UserInputID: block.Data["userInputId"], PlanID: block.Data["planId"], Title: block.Title,
			Content: content, State: block.State, Collapsed: block.Collapsed || defaultToolCollapsed(kind, block.State),
			Attachments: append([]session.Attachment(nil), block.Attachments...),
		})
		if kind == BlockPlan && block.State == "proposed" {
			m.planMode = true
			m.planReview = &PlanReviewView{ID: block.Data["planId"], Title: block.Title, Body: block.Content, Version: first(block.Data["version"], "1"), State: block.State}
		}
		for toolIndex < len(toolRecords) && toolRecords[toolIndex].AnchorSequence <= block.Sequence {
			m.transcript = append(m.transcript, m.recoveredToolBlock(toolRecords[toolIndex]))
			toolIndex++
		}
	}
	for ; toolIndex < len(toolRecords); toolIndex++ {
		m.transcript = append(m.transcript, m.recoveredToolBlock(toolRecords[toolIndex]))
	}
	m.mergeLiveBlockProjections(live)
	m.settleUnownedLiveBlocks()
	m.switchProvider(first(projection.Session.ProviderID, m.provider))
	m.selectModel(first(projection.Session.ModelID, m.model))
	m.reasoning = first(projection.Session.Reasoning, m.reasoning)
	m.agentMode = first(projection.Session.AgentMode, m.agentMode)
	m.syncReasoningForModel()
	m.usage = UsageView{}
	m.restoreUsageSnapshot(projection.Usage)
	if m.paint != nil {
		m.paint.todoRender = ""
	}
	m.invalidateTranscriptLayout()
	m.transcriptTop = 0
}

func (m *AppModel) mergeLiveBlockProjections(live []app.LiveBlockProjection) {
	byID := make(map[string]int, len(m.transcript))
	byTool := make(map[string]int)
	for index := range m.transcript {
		byID[m.transcript[index].ID] = index
		if m.transcript[index].ToolCallID != "" {
			byTool[m.transcript[index].ToolCallID] = index
		}
	}
	for _, projected := range live {
		if projected.SessionID != m.sessionID || projected.AgentID != "" {
			continue
		}
		if index, exists := byID[projected.ID]; exists {
			m.transcript[index].Content = projected.Content
			m.transcript[index].State = projected.State
			continue
		}
		if projected.ToolCallID != "" {
			if index, exists := byTool[projected.ToolCallID]; exists {
				m.transcript[index].State = projected.State
				if projected.Content != "" {
					m.transcript[index].Content = projected.Content
				}
				continue
			}
		}
		kind := BlockAssistant
		title := "Azem"
		switch projected.Kind {
		case "text":
			if projected.TextPhase == "commentary" {
				kind, title = BlockAssistant, "Progress"
			}
		case "thinking":
			kind, title = BlockThinking, "Thinking"
		case "tool":
			kind, title = BlockTool, first(projected.Data["name"], "Tool")
		}
		m.transcript = append(m.transcript, Block{
			ID: projected.ID, Kind: kind, RunID: projected.RunID, AgentID: projected.AgentID,
			ToolCallID: projected.ToolCallID, TextPhase: projected.TextPhase, Data: cloneBlockData(projected.Data),
			Title: title, Content: projected.Content, State: projected.State,
		})
	}
}

func (m *AppModel) settleUnownedLiveBlocks() {
	for index := range m.transcript {
		block := &m.transcript[index]
		switch block.State {
		case "streaming", "running", "started", "progress":
		default:
			continue
		}
		run, exists := m.runs[block.RunID]
		if exists && !terminalProjectedRun(run) {
			continue
		}
		switch run.State {
		case "completed", "failed", "cancelled":
			block.State = run.State
		default:
			block.State = "interrupted"
		}
	}
}

func cloneBlockData(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
