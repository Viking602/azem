package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/tool"
)

// TestSpillAgentToolResultPersistsFullOutputWithLocator pins the spill
// contract: an oversized tool result keeps a bounded model-visible prefix, the
// complete output is durably stored as a session context artifact, and the
// replacement text carries the artifact locator plus context.read_artifact
// retrieval guidance so no output bytes are lost to truncation.
func TestSpillAgentToolResultPersistsFullOutputWithLocator(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err = sessions.Ensure(ctx, session.Session{ID: "spill-session", Title: "spill"}); err != nil {
		t.Fatal(err)
	}

	huge := strings.Repeat("line of oversized shell output\n", (maxAgentToolResultContentBytes/30)+64)
	result := spillAgentToolResult(ctx, sessions, "spill-session", "run-1", tool.Result{
		ToolCallID: "call-1", Name: "coding.shell", Content: huge,
	})

	if len(result.Content) > maxAgentToolResultContentBytes+1024 {
		t.Fatalf("spilled content still oversized: %d bytes", len(result.Content))
	}
	if !strings.Contains(result.Content, "full output: artifact:") {
		t.Fatalf("spilled content is missing the artifact locator: %q", result.Content[len(result.Content)-256:])
	}
	if !strings.Contains(result.Content, "context.read_artifact") {
		t.Fatal("spilled content is missing the retrieval guidance")
	}

	marker := "full output: artifact:"
	index := strings.Index(result.Content, marker)
	rest := result.Content[index+len(marker):]
	artifactID := rest[:strings.IndexAny(rest, " \n")]
	artifact, err := sessions.LoadArtifact(ctx, "spill-session", artifactID)
	if err != nil {
		t.Fatalf("load spilled artifact %q: %v", artifactID, err)
	}
	if string(artifact.Payload) != huge {
		t.Fatalf("spilled artifact payload differs: %d bytes, want %d", len(artifact.Payload), len(huge))
	}
	if artifact.Kind != spillArtifactKind {
		t.Fatalf("artifact kind = %q, want %q", artifact.Kind, spillArtifactKind)
	}
}

// TestSpillAgentToolResultSpillsOversizedStructuredPayload verifies the
// structured JSON path: the receipt carries the artifact ID for retrieval.
func TestSpillAgentToolResultSpillsOversizedStructuredPayload(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err = sessions.Ensure(ctx, session.Session{ID: "spill-structured", Title: "spill"}); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]string{"rows": strings.Repeat("x", maxAgentToolResultContentBytes+512)})
	result := spillAgentToolResult(ctx, sessions, "spill-structured", "run-1", tool.Result{
		ToolCallID: "call-1", Name: "mcp__db__query", Content: "ok", Structured: payload,
	})

	var receipt map[string]any
	if err = json.Unmarshal(result.Structured, &receipt); err != nil {
		t.Fatal(err)
	}
	artifactID, _ := receipt["artifact_id"].(string)
	if artifactID == "" {
		t.Fatalf("structured receipt is missing artifact_id: %v", receipt)
	}
	artifact, err := sessions.LoadArtifact(ctx, "spill-structured", artifactID)
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact.Payload) != string(payload) {
		t.Fatal("structured artifact payload differs from the original structured output")
	}
	if result.Content != "ok" {
		t.Fatalf("small content must stay exact, got %q", result.Content)
	}
}

// TestSpillAgentToolResultFallsBackWithoutStore keeps degraded runtimes on the
// existing lossy truncation instead of failing the tool call.
func TestSpillAgentToolResultFallsBackWithoutStore(t *testing.T) {
	huge := strings.Repeat("y", maxAgentToolResultContentBytes+512)
	result := spillAgentToolResult(context.Background(), nil, "s", "r", tool.Result{
		ToolCallID: "call-1", Name: "coding.search", Content: huge,
	})
	if !strings.Contains(result.Content, "[Tool result truncated by Azem") {
		t.Fatal("nil store must fall back to plain truncation")
	}
	if strings.Contains(result.Content, "artifact:") {
		t.Fatal("fallback truncation must not advertise an artifact locator")
	}
}

// TestSpillAgentToolResultKeepsSmallResultsExact pins the fast path.
func TestSpillAgentToolResultKeepsSmallResultsExact(t *testing.T) {
	original := tool.Result{ToolCallID: "call-1", Name: "coding.read_file", Content: "small body", Structured: json.RawMessage(`{"ok":true}`)}
	result := spillAgentToolResult(context.Background(), nil, "s", "r", original)
	if result.Content != original.Content || string(result.Structured) != string(original.Structured) {
		t.Fatalf("small result changed: %+v", result)
	}
}
