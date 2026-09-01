package app

import (
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/message"
)

func markPrivateMessage(value *message.Message) {
	agentruntime.SetMessageVisibility(value, agentruntime.MessageVisibilityPrivate)
}

func isPrivateMessage(value message.Message) bool {
	return agentruntime.MessageVisibilityOf(value) == agentruntime.MessageVisibilityPrivate
}

func setMessageCreatedAt(value *message.Message, createdAt time.Time) {
	agentruntime.SetMessageCreatedAt(value, createdAt)
}

func messageCreatedAt(value message.Message) time.Time {
	return agentruntime.MessageCreatedAt(value)
}
