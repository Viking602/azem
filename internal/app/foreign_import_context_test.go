package app

import (
	"encoding/json"
	"testing"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
)

func TestBlockMessageReplaysImportedToolContracts(t *testing.T) {
	foreign := message.Message{
		Role:      message.RoleAssistant,
		ToolCalls: []message.ToolCall{{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{"path":"a.txt"}`)}},
	}
	encoded, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := blockMessage(session.Block{Sequence: 7, Kind: "assistant", Content: "Tool call: read", ImportedMessage: encoded})
	if !ok || len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call-1" || got.Metadata[sourceSequenceMetadataKey] != "7" {
		t.Fatalf("imported block message=%#v ok=%v", got, ok)
	}
}
