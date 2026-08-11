package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Viking602/venat/tool"
)

func TestBoundAgentToolResultKeepsSmallResultExact(t *testing.T) {
	want := tool.Result{ToolCallID: "call-1", Name: "coding.search", Content: "small", Structured: json.RawMessage(`{"ok":true}`)}
	got := boundAgentToolResult(want)
	if got.Content != want.Content || !bytes.Equal(got.Structured, want.Structured) {
		t.Fatalf("small result changed: got=%+v want=%+v", got, want)
	}
}

func TestBoundAgentToolResultCapsContentAndStructuredCheckpointPayload(t *testing.T) {
	large := strings.Repeat("界", maxAgentToolResultContentBytes)
	got := boundAgentToolResult(tool.Result{
		ToolCallID: "call-1",
		Name:       "coding.search",
		Content:    large,
		Structured: json.RawMessage(`{"matches":"` + strings.Repeat("x", maxAgentToolResultContentBytes*2) + `"}`),
	})
	if len(got.Content) > maxAgentToolResultContentBytes || !strings.Contains(got.Content, "Tool result truncated by Azem") {
		t.Fatalf("bounded content bytes=%d content suffix missing=%v", len(got.Content), !strings.Contains(got.Content, "Tool result truncated by Azem"))
	}
	if !utf8.ValidString(got.Content) {
		t.Fatal("bounded content split a UTF-8 sequence")
	}
	var receipt map[string]any
	if err := json.Unmarshal(got.Structured, &receipt); err != nil {
		t.Fatalf("structured receipt is invalid JSON: %v", err)
	}
	if receipt["truncated"] != true {
		t.Fatalf("structured receipt = %v", receipt)
	}
}
