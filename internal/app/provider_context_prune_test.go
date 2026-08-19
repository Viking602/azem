package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
)

// pruneTestHistory builds one stale turn with an oversized tool result
// followed by three small recent user turns, so the stale result sits before
// the preserved recent-user boundary and is the only prune candidate.
func pruneTestHistory(staleBytes, recentBytes int) []message.Message {
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, "stale question"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "stale-1", Name: "coding.search"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "stale-1", Name: "coding.search", Content: strings.Repeat("m", staleBytes)}),
	}
	for _, prompt := range []string{"recent one", "recent two", "recent three"} {
		history = append(history,
			message.NewText(message.RoleUser, prompt),
			message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "call-" + prompt, Name: "coding.read_file"}}},
			message.NewToolResult(message.ToolResult{ToolCallID: "call-" + prompt, Name: "coding.read_file", Content: strings.Repeat("r", recentBytes)}),
			message.NewText(message.RoleAssistant, "done"),
		)
	}
	return history
}

// disableNormalizeThreshold keeps the pre-existing 12k-token result
// externalization out of the way so these tests exercise the pruning layer.
const disableNormalizeThreshold = 1_000_000

// TestArchivePrunesStaleToolResultsWithoutModelCall pins the model-free
// pruning layer. Replacing a stale oversized result writes an exact durable
// payload locator while preserving tool pairing and message order.
func TestArchivePrunesStaleToolResultsWithoutModelCall(t *testing.T) {
	history := pruneTestHistory(64<<10, 2048)
	var storedPayload []byte
	manager := turnContext{
		largeToolTokens: disableNormalizeThreshold,
		putArtifact: func(_ context.Context, kind string, payload []byte, _ string) (session.ContextArtifact, error) {
			if kind != "tool_result" {
				t.Fatalf("artifact kind = %q", kind)
			}
			storedPayload = append([]byte(nil), payload...)
			return session.ContextArtifact{ID: "pruned-1", SHA256: "digest"}, nil
		},
	}
	result, changed, err := manager.pruneStaleToolResults(context.Background(), history, 16_000)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("stale tool result was not pruned")
	}
	if len(storedPayload) != 64<<10 {
		t.Fatalf("stored artifact payload = %d bytes, want %d", len(storedPayload), 64<<10)
	}
	if err := message.ValidateCompleteTurns(result); err != nil {
		t.Fatalf("pruned history broke turn pairing: %v", err)
	}
	if len(result) != len(history) {
		t.Fatalf("pruning must not drop messages: %d != %d", len(result), len(history))
	}
	staleResult := result[3].ToolResult
	if staleResult == nil || !strings.Contains(staleResult.Content, `"artifact_ref":"pruned-1"`) || !strings.Contains(staleResult.Content, `"pruned":true`) {
		t.Fatalf("stale tool result was not replaced with a pruned locator: %#v", staleResult)
	}
	for index := 4; index < len(result); index++ {
		if current := result[index].ToolResult; current != nil && strings.Contains(current.Content, "context_artifact") {
			t.Fatalf("recent tool result at %d was pruned: %#v", index, current)
		}
	}
}

// TestPruneStaleToolResultsSkipsSmallAndRecentResults pins the guardrails:
// results at or below the floor, results inside the recent-user window, and
// histories already under target are untouched.
func TestPruneStaleToolResultsSkipsSmallAndRecentResults(t *testing.T) {
	history := pruneTestHistory(512, 2048)
	manager := turnContext{
		putArtifact: func(context.Context, string, []byte, string) (session.ContextArtifact, error) {
			t.Fatal("no artifact should be written for small results")
			return session.ContextArtifact{}, nil
		},
	}
	pruned, changed, err := manager.pruneStaleToolResults(context.Background(), history, 1)
	if err != nil || changed {
		t.Fatalf("small stale result was pruned: changed=%v err=%v", changed, err)
	}
	if len(pruned) != len(history) {
		t.Fatal("history length changed")
	}

	underTarget := pruneTestHistory(64<<10, 2048)
	if _, changed, err = manager.pruneStaleToolResults(context.Background(), underTarget, 1_000_000); err != nil || changed {
		t.Fatalf("under-target history was pruned: changed=%v err=%v", changed, err)
	}
}
