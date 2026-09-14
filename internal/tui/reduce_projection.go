package tui

import (
	"strconv"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

func (m *AppModel) reduceProjectionEvent(event app.Event) bool {
	if m.reduceConnectionEvent(event) || m.reduceRunEvent(event) || m.reducePromptQueueEvent(event) || m.reduceSessionProjectionEvent(event) {
		return true
	}
	if event.Kind != app.EventProjectionResync || event.RuntimeProjection == nil {
		return false
	}
	projection := event.RuntimeProjection
	if event.SessionID != "" {
		m.sessionID = event.SessionID
	}
	m.runs = make(map[string]app.RunProjection, len(projection.Runs))
	for _, run := range projection.Runs {
		m.runs[runProjectionKey(run)] = run
	}
	m.promptQueues = make(map[string]session.PromptQueueV1, len(projection.PromptQueues))
	for _, queue := range projection.PromptQueues {
		m.promptQueues[queue.SessionID] = queue.Clone()
	}
	if projection.Session != nil && projection.Session.Session.ID == m.sessionID {
		m.applyTypedSessionProjection(*projection.Session, projection.LiveBlocks)
	}
	m.applyPendingControlProjection(projection.PendingControls, projection.Recovery)
	if run, exists := m.currentRunProjection(); exists {
		m.applyCurrentRunProjection(run)
	} else if m.connection.State == "connected" || m.connection.State == "" {
		m.runID = ""
		m.status = "Ready"
	}
	if raw := event.Data["wireSequence"]; raw != "" {
		if sequence, err := strconv.ParseUint(raw, 10, 64); err == nil {
			m.lastWireCursor = max(m.lastWireCursor, sequence)
		}
	}
	return true
}
