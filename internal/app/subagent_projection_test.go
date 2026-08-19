package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/venat/message"
)

func TestTranscriptToAgentBlocksBoundsLargeToolProjection(t *testing.T) {
	large := strings.Repeat("x", maxAgentTranscriptToolBytes*4)
	encoded, err := json.Marshal([]message.Message{
		{
			Role:      message.RoleAssistant,
			ToolCalls: []message.ToolCall{{ID: "search", Name: "coding.search", Arguments: json.RawMessage(`{"query":"Todo"}`)}},
		},
		{
			Role:       message.RoleTool,
			ToolResult: &message.ToolResult{ToolCallID: "search", Name: "coding.search", Content: large},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := transcriptToAgentBlocks(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v", blocks)
	}
	block := blocks[0]
	if !block.ContentTruncated || block.ContentBytes <= maxAgentTranscriptToolBytes || len(block.Content) > maxAgentTranscriptToolBytes {
		t.Fatalf("bounded block = %#v", block)
	}
}

func TestLargeSubagentTranscriptProducesBoundedDesktopProjection(t *testing.T) {
	large := strings.Repeat("generated match line\n", 1_000_000)
	encoded, err := json.Marshal([]message.Message{
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "search", Name: "coding.search"}}},
		{Role: message.RoleTool, ToolResult: &message.ToolResult{ToolCallID: "search", Name: "coding.search", Content: large}},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	blocks, err := transcriptToAgentBlocks(encoded)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := json.Marshal(Event{Kind: EventAgentDetail, AgentBlocks: blocks})
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) > 64<<10 {
		t.Fatalf("desktop projection bytes = %d, want <= 64 KiB", len(projected))
	}
	t.Logf("projected %d transcript bytes into %d event bytes in %s", len(encoded), len(projected), time.Since(started))
}

func TestTranscriptToAgentBlocksHidesInternalContext(t *testing.T) {
	checkpoint := message.NewText(message.RoleUser, "archive carrier")
	checkpoint.Kind = message.KindCompactionSummary
	checkpoint.Visibility = message.VisibilityPrivate
	private := message.NewText(message.RoleAssistant, "trusted private context")
	private.Visibility = message.VisibilityPrivate
	legacyCheckpoint := message.NewText(message.RoleUser, "legacy archive carrier")
	legacyCheckpoint.Kind = message.KindCompactionSummary
	encoded, err := json.Marshal([]message.Message{
		checkpoint,
		private,
		legacyCheckpoint,
		{Role: message.RoleAssistant, Text: "public answer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := transcriptToAgentBlocks(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Content != "public answer" {
		t.Fatalf("internal context leaked into projection: %#v", blocks)
	}
}
