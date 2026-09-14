package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

func (m AppModel) updateQueueOverlayKey(key string) (tea.Model, tea.Cmd) {
	queue := m.currentPromptQueue()
	switch key {
	case "esc":
		return m, m.closeOverlay()
	case "up", "k":
		m.moveOverlayCursor(-1, len(queue.Items))
		return m, nil
	case "down", "j":
		m.moveOverlayCursor(1, len(queue.Items))
		return m, nil
	case "pgup":
		m.moveOverlayCursor(-max(1, m.height/3), len(queue.Items))
		return m, nil
	case "pgdown":
		m.moveOverlayCursor(max(1, m.height/3), len(queue.Items))
		return m, nil
	case "p":
		if !m.mutationsEnabled() {
			m.errorBanner = "Connection is not ready"
			return m, nil
		}
		operation, reason := app.PromptQueuePause, "paused by user"
		if queue.State == session.PromptQueuePaused {
			operation, reason = app.PromptQueueResume, ""
		}
		return m, mutatePromptQueue(m.runtime, app.PromptQueueMutation{
			Operation: operation, SessionID: m.sessionID, ExpectedRevision: queue.Revision, PauseReason: reason,
		}, "", nil, "")
	}
	if m.overlayCursor < 0 || m.overlayCursor >= len(queue.Items) {
		return m, nil
	}
	item := queue.Items[m.overlayCursor]
	if !m.mutationsEnabled() {
		m.errorBanner = "Connection is not ready"
		return m, nil
	}
	switch key {
	case "enter", "e":
		if item.State == session.QueuedPromptDispatching {
			m.errorBanner = "The dispatching item is immutable."
			return m, nil
		}
		m.queueEditItemID = item.ID
		m.composer.SetValue(item.Text)
		m.pendingImages = append([]session.Attachment(nil), item.Attachments...)
		return m, m.closeOverlay()
	case "d", "x":
		if item.State == session.QueuedPromptDispatching {
			m.errorBanner = "The dispatching item is immutable."
			return m, nil
		}
		return m, mutatePromptQueue(m.runtime, app.PromptQueueMutation{
			Operation: app.PromptQueueRemove, SessionID: m.sessionID, ExpectedRevision: queue.Revision, ItemID: item.ID,
		}, "", nil, "")
	case "r":
		if item.State != session.QueuedPromptFailed {
			m.errorBanner = "Only failed items can be retried."
			return m, nil
		}
		return m, mutatePromptQueue(m.runtime, app.PromptQueueMutation{
			Operation: app.PromptQueueRetry, SessionID: m.sessionID, ExpectedRevision: queue.Revision, ItemID: item.ID,
		}, "", nil, "")
	case "ctrl+up":
		if m.overlayCursor == 0 || item.State == session.QueuedPromptDispatching {
			return m, nil
		}
		return m, mutatePromptQueue(m.runtime, app.PromptQueueMutation{
			Operation: app.PromptQueueReorder, SessionID: m.sessionID, ExpectedRevision: queue.Revision,
			ItemID: item.ID, BeforeID: queue.Items[m.overlayCursor-1].ID,
		}, "", nil, "")
	case "ctrl+down":
		if m.overlayCursor >= len(queue.Items)-1 || item.State == session.QueuedPromptDispatching {
			return m, nil
		}
		beforeID := ""
		if m.overlayCursor+2 < len(queue.Items) {
			beforeID = queue.Items[m.overlayCursor+2].ID
		}
		return m, mutatePromptQueue(m.runtime, app.PromptQueueMutation{
			Operation: app.PromptQueueReorder, SessionID: m.sessionID, ExpectedRevision: queue.Revision,
			ItemID: item.ID, BeforeID: beforeID,
		}, "", nil, "")
	}
	return m, nil
}
