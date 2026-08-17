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

// TestCompactionPrunesStaleToolResultsWithoutModelCall pins the model-free
// pruning layer: when replacing stale oversized tool results with durable
// artifact locators alone reaches the compaction target, the semantic
// summarizer is never invoked, the pruned result is activated durably, and
// tool call/result pairing survives.
func TestCompactionPrunesStaleToolResultsWithoutModelCall(t *testing.T) {
	history := pruneTestHistory(64<<10, 2048)
	var storedPayload []byte
	activated := 0
	manager := turnContext{
		compactTargetTokens: 8_000, largeToolTokens: disableNormalizeThreshold,
		putArtifact: func(_ context.Context, kind string, payload []byte, _ string) (session.ContextArtifact, error) {
			if kind != "tool_result" {
				t.Fatalf("artifact kind = %q", kind)
			}
			storedPayload = append([]byte(nil), payload...)
			return session.ContextArtifact{ID: "pruned-1", SHA256: "digest"}, nil
		},
		summarize: func(context.Context, string) (string, error) {
			t.Fatal("semantic summarizer must not run when pruning already fits the target")
			return "", nil
		},
		activateCompaction: func(_ context.Context, result []message.Message, identity string) error {
			if identity == "" {
				t.Fatal("activation identity is empty")
			}
			activated++
			return nil
		},
	}
	result, err := manager.CompactTo(context.Background(), history, 16_000)
	if err != nil {
		t.Fatal(err)
	}
	if activated != 1 {
		t.Fatalf("pruned checkpoint activations = %d, want 1", activated)
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

// TestCompactionPruningFeedsSummarizerWhenTargetStillExceeded verifies the
// second stage: when pruning alone cannot fit the target, the semantic
// summarizer still runs and receives the pruned transcript.
func TestCompactionPruningFeedsSummarizerWhenTargetStillExceeded(t *testing.T) {
	history := pruneTestHistory(64<<10, 20<<10)
	summarized := false
	manager := turnContext{
		compactTargetTokens: 2_200, largeToolTokens: disableNormalizeThreshold,
		putArtifact: func(_ context.Context, _ string, payload []byte, _ string) (session.ContextArtifact, error) {
			return session.ContextArtifact{ID: "pruned-2", SHA256: "digest"}, nil
		},
		summarize: func(_ context.Context, transcript string) (string, error) {
			summarized = true
			if strings.Contains(transcript, strings.Repeat("m", 4096)) {
				t.Fatal("summarizer received the unpruned stale tool result body")
			}
			return semanticStateForTest("pruned summary"), nil
		},
	}
	result, err := manager.CompactTo(context.Background(), history, 16_000)
	if err != nil {
		t.Fatal(err)
	}
	if !summarized {
		t.Fatal("semantic summarizer did not run for an insufficient prune")
	}
	foundSummary := false
	for _, current := range result {
		if current.Kind == message.KindCompactionSummary {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Fatal("compacted history is missing the semantic checkpoint")
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
