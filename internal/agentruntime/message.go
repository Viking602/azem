package agentruntime

import (
	"encoding/json"
	"maps"
	"time"

	"github.com/Viking602/venat/message"
)

// MessageVisibility is Azem's application-level projection scope. Venat owns
// only provider-facing message content; Azem owns visibility and durable
// conversation identity.
type MessageVisibility string

const (
	MessageVisibilityShared  MessageVisibility = "shared"
	MessageVisibilityPrivate MessageVisibility = "private"
)

const (
	messageVisibilityKey  = "azem.message.visibility"
	messageTeamIDKey      = "azem.message.team_id"
	messageAgentIDKey     = "azem.message.agent_id"
	messageRunIDKey       = "azem.message.run_id"
	messageParentRunIDKey = "azem.message.parent_run_id"
	messageCreatedAtKey   = "azem.message.created_at"
)

func MessageVisibilityOf(value message.Message) MessageVisibility {
	if value.Metadata != nil && value.Metadata[messageVisibilityKey] == string(MessageVisibilityPrivate) {
		return MessageVisibilityPrivate
	}
	return MessageVisibilityShared
}

func SetMessageVisibility(value *message.Message, visibility MessageVisibility) {
	if value == nil {
		return
	}
	setMessageMetadata(value, messageVisibilityKey, string(visibility), visibility == MessageVisibilityShared || visibility == "")
}

func MessageTeamID(value message.Message) string  { return messageMetadata(value, messageTeamIDKey) }
func MessageAgentID(value message.Message) string { return messageMetadata(value, messageAgentIDKey) }
func MessageRunID(value message.Message) string   { return messageMetadata(value, messageRunIDKey) }
func MessageParentRunID(value message.Message) string {
	return messageMetadata(value, messageParentRunIDKey)
}

func SetMessageIdentity(value *message.Message, teamID, agentID, runID, parentRunID string) {
	if value == nil {
		return
	}
	setMessageMetadata(value, messageTeamIDKey, teamID, teamID == "")
	setMessageMetadata(value, messageAgentIDKey, agentID, agentID == "")
	setMessageMetadata(value, messageRunIDKey, runID, runID == "")
	setMessageMetadata(value, messageParentRunIDKey, parentRunID, parentRunID == "")
}

func MessageCreatedAt(value message.Message) time.Time {
	text := messageMetadata(value, messageCreatedAtKey)
	if text == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func SetMessageCreatedAt(value *message.Message, createdAt time.Time) {
	if value == nil {
		return
	}
	if createdAt.IsZero() {
		setMessageMetadata(value, messageCreatedAtKey, "", true)
		return
	}
	setMessageMetadata(value, messageCreatedAtKey, createdAt.UTC().Format(time.RFC3339Nano), false)
}

func messageMetadata(value message.Message, key string) string {
	if value.Metadata == nil {
		return ""
	}
	return value.Metadata[key]
}

func setMessageMetadata(value *message.Message, key, content string, remove bool) {
	if remove {
		if value.Metadata != nil {
			delete(value.Metadata, key)
			if len(value.Metadata) == 0 {
				value.Metadata = nil
			}
		}
		return
	}
	if value.Metadata == nil {
		value.Metadata = make(map[string]string)
	}
	value.Metadata[key] = content
}

// PersistedMessage preserves Azem's v3 model-history JSON fields while the
// in-memory provider message remains the v0.16 SDK type.
type PersistedMessage struct {
	message.Message
	TeamID      string            `json:"teamId,omitempty"`
	AgentID     string            `json:"agentId,omitempty"`
	RunID       string            `json:"runId,omitempty"`
	ParentRunID string            `json:"parentRunId,omitempty"`
	Visibility  MessageVisibility `json:"visibility,omitempty"`
	CreatedAt   time.Time         `json:"createdAt,omitempty"`
}

func PersistMessage(value message.Message) PersistedMessage {
	wire := message.Clone(value)
	wire.Metadata = maps.Clone(wire.Metadata)
	if wire.Metadata != nil {
		for _, key := range []string{messageVisibilityKey, messageTeamIDKey, messageAgentIDKey, messageRunIDKey, messageParentRunIDKey, messageCreatedAtKey} {
			delete(wire.Metadata, key)
		}
		if len(wire.Metadata) == 0 {
			wire.Metadata = nil
		}
	}
	return PersistedMessage{
		Message: wire, TeamID: MessageTeamID(value), AgentID: MessageAgentID(value),
		RunID: MessageRunID(value), ParentRunID: MessageParentRunID(value),
		Visibility: MessageVisibilityOf(value), CreatedAt: MessageCreatedAt(value),
	}
}

func (value PersistedMessage) RuntimeMessage() message.Message {
	result := message.Clone(value.Message)
	if value.TeamID != "" || value.AgentID != "" || value.RunID != "" || value.ParentRunID != "" {
		SetMessageIdentity(&result, value.TeamID, value.AgentID, value.RunID, value.ParentRunID)
	}
	if value.Visibility != "" {
		SetMessageVisibility(&result, value.Visibility)
	}
	if !value.CreatedAt.IsZero() {
		SetMessageCreatedAt(&result, value.CreatedAt)
	}
	return result
}

func PersistMessages(values []message.Message) []PersistedMessage {
	if values == nil {
		return nil
	}
	result := make([]PersistedMessage, len(values))
	for index := range values {
		result[index] = PersistMessage(values[index])
	}
	return result
}

func RuntimeMessages(values []PersistedMessage) []message.Message {
	if values == nil {
		return nil
	}
	result := make([]message.Message, len(values))
	for index := range values {
		result[index] = values[index].RuntimeMessage()
	}
	return result
}

func MarshalMessages(values []message.Message) ([]byte, error) {
	return json.Marshal(PersistMessages(values))
}

func UnmarshalMessages(encoded []byte) ([]message.Message, error) {
	var values []PersistedMessage
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, err
	}
	return RuntimeMessages(values), nil
}
