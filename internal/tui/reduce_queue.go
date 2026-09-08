package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/session"
)

type promptQueueMutationResultMsg struct {
	Queue       session.PromptQueueV1
	Text        string
	Attachments []session.Attachment
	EditItemID  string
	Err         error
}

func (m *AppModel) reducePromptQueueEvent(event app.Event) bool {
	if event.Kind != app.EventPromptQueueState || event.PromptQueue == nil {
		return false
	}
	if m.promptQueues == nil {
		m.promptQueues = make(map[string]session.PromptQueueV1)
	}
	queue := event.PromptQueue.Clone()
	current := m.promptQueues[queue.SessionID]
	if queue.Revision >= current.Revision {
		m.promptQueues[queue.SessionID] = queue
	}
	return true
}

func (m AppModel) currentPromptQueue() session.PromptQueueV1 {
	if queue, exists := m.promptQueues[m.sessionID]; exists {
		return queue.Clone()
	}
	return session.PromptQueueV1{Version: session.PromptQueueVersion, SessionID: m.sessionID, State: session.PromptQueueActive, Items: []session.QueuedPromptV1{}}
}

func mutatePromptQueue(runtime Runtime, mutation app.PromptQueueMutation, text string, attachments []session.Attachment, editItemID string) tea.Cmd {
	return func() tea.Msg {
		mutation.MutationID = uuid.NewString()
		var queue session.PromptQueueV1
		err := runtime.Request(context.Background(), desktopipc.MethodMutatePromptQueue, mutation, &queue)
		var protocolErr *desktopipc.ProtocolRequestError
		if errors.As(err, &protocolErr) && (protocolErr.Code == "revision_conflict" || protocolErr.Code == "resync_required") {
			_ = runtime.Request(context.Background(), desktopipc.MethodPromptQueue, map[string]string{"sessionId": mutation.SessionID}, &queue)
		}
		return promptQueueMutationResultMsg{Queue: queue, Text: text, Attachments: attachments, EditItemID: editItemID, Err: err}
	}
}

func enqueuePrompt(runtime Runtime, queue session.PromptQueueV1, text string, attachments []session.Attachment) tea.Cmd {
	now := time.Now().UTC()
	return mutatePromptQueue(runtime, app.PromptQueueMutation{
		Operation: app.PromptQueueEnqueue, SessionID: queue.SessionID, ExpectedRevision: queue.Revision,
		Item: session.QueuedPromptV1{ID: uuid.NewString(), Text: text, Attachments: append([]session.Attachment(nil), attachments...), State: session.QueuedPromptQueued, CreatedAt: now, UpdatedAt: now},
	}, text, attachments, "")
}
