package llmuxdriver

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/Viking602/llmux"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

var openAIToolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func TestSanitizeToolNameMatchesOpenAIDeepSeekPattern(t *testing.T) {
	cases := map[string]string{
		"coding.read_file":        "coding_read_file",
		"coding.edit_hashline":    "coding_edit_hashline",
		"mcp.server/tool.name":    "mcp_server_tool_name",
		"already_safe-name":       "already_safe-name",
		"todo":                    "todo",
		"hydaelyn_activate_skill": "hydaelyn_activate_skill",
		"...weird...":             "weird",
	}
	for input, want := range cases {
		got := sanitizeToolName(input)
		if got != want {
			t.Fatalf("sanitizeToolName(%q) = %q, want %q", input, got, want)
		}
		if got != "" && !openAIToolNamePattern.MatchString(got) {
			t.Fatalf("sanitizeToolName(%q) = %q does not match provider pattern", input, got)
		}
	}
}

func TestToolNamesRoundTripAndCollision(t *testing.T) {
	names := newToolNames([]message.ToolDefinition{
		{Name: "coding.read_file"},
		{Name: "coding_read_file"},
		{Name: "coding.shell"},
	})
	if names.Wire("coding.read_file") != "coding_read_file" {
		t.Fatalf("wire dotted = %q", names.Wire("coding.read_file"))
	}
	if names.Wire("coding_read_file") != "coding_read_file_2" {
		t.Fatalf("wire collision = %q", names.Wire("coding_read_file"))
	}
	if names.Local("coding_read_file") != "coding.read_file" {
		t.Fatalf("local dotted = %q", names.Local("coding_read_file"))
	}
	if names.Local("coding_read_file_2") != "coding_read_file" {
		t.Fatalf("local collision = %q", names.Local("coding_read_file_2"))
	}
	if names.Wire("coding.shell") != "coding_shell" || names.Local("coding_shell") != "coding.shell" {
		t.Fatalf("shell mapping wire=%q local=%q", names.Wire("coding.shell"), names.Local("coding_shell"))
	}
}

func TestConvertRequestRewritesDottedToolNames(t *testing.T) {
	request := hyprovider.Request{
		Model: "deepseek-v4-flash",
		Messages: []message.Message{
			message.NewText(message.RoleUser, "edit the file"),
			{
				Role: message.RoleAssistant,
				ToolCalls: []message.ToolCall{{
					ID: "call_1", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"main.go"}`),
				}},
			},
			{
				Role: message.RoleTool,
				ToolResult: &message.ToolResult{
					ToolCallID: "call_1", Name: "coding.read_file", Content: "package main",
				},
			},
		},
		Tools: []message.ToolDefinition{{
			Name: "coding.read_file", Description: "read a file",
			InputSchema: message.JSONSchema{Type: "object"},
		}, {
			Name: "coding.edit_hashline", Description: "edit",
			InputSchema: message.JSONSchema{Type: "object"},
		}},
	}
	converted, names, err := convertRequest(request, "", "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Options.Tools) != 2 {
		t.Fatalf("tools=%d", len(converted.Options.Tools))
	}
	for _, tool := range converted.Options.Tools {
		if !openAIToolNamePattern.MatchString(tool.Name) {
			t.Fatalf("tool wire name %q rejected by provider pattern", tool.Name)
		}
	}
	if converted.Options.Tools[0].Name != "coding_read_file" || converted.Options.Tools[1].Name != "coding_edit_hashline" {
		t.Fatalf("tools=%+v", converted.Options.Tools)
	}
	var sawCall, sawResult bool
	for _, msg := range converted.Messages {
		for _, part := range msg.Content {
			if part.ToolCall != nil {
				sawCall = true
				if part.ToolCall.Name != "coding_read_file" {
					t.Fatalf("history tool call name = %q", part.ToolCall.Name)
				}
			}
			if part.ToolResult != nil {
				sawResult = true
				if part.ToolResult.Name != "coding_read_file" {
					t.Fatalf("history tool result name = %q", part.ToolResult.Name)
				}
			}
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("history rewrite incomplete call=%v result=%v", sawCall, sawResult)
	}
	if names.Local("coding_read_file") != "coding.read_file" {
		t.Fatalf("stream reverse map = %q", names.Local("coding_read_file"))
	}
}

func TestConvertRequestGroupsParallelToolResultsForAnthropicProtocol(t *testing.T) {
	request := hyprovider.Request{
		Model: "deepseek-v4-flash",
		Messages: []message.Message{
			message.NewText(message.RoleUser, "inspect the repository"),
			{
				Role: message.RoleAssistant,
				ToolCalls: []message.ToolCall{
					{ID: "call_0", Name: "coding.git_diff", Arguments: json.RawMessage(`{}`)},
					{ID: "call_1", Name: "coding.list_files", Arguments: json.RawMessage(`{}`)},
				},
			},
			message.NewToolResult(message.ToolResult{ToolCallID: "call_0", Name: "coding.git_diff", Content: "diff"}),
			message.NewToolResult(message.ToolResult{ToolCallID: "call_1", Name: "coding.list_files", Content: "files"}),
		},
		Tools: []message.ToolDefinition{
			{Name: "coding.git_diff", InputSchema: message.JSONSchema{Type: "object"}},
			{Name: "coding.list_files", InputSchema: message.JSONSchema{Type: "object"}},
		},
	}

	for _, test := range []struct {
		providerID   string
		wantMessages int
		wantResults  int
	}{
		{providerID: "deepseek", wantMessages: 3, wantResults: 2},
		{providerID: "openai", wantMessages: 4, wantResults: 1},
	} {
		t.Run(test.providerID, func(t *testing.T) {
			converted, _, err := convertRequest(request, "", test.providerID)
			if err != nil {
				t.Fatal(err)
			}
			if len(converted.Messages) != test.wantMessages {
				t.Fatalf("messages=%d, want %d", len(converted.Messages), test.wantMessages)
			}
			results := converted.Messages[len(converted.Messages)-1]
			if results.Role != sdk.RoleTool || len(results.Content) != test.wantResults {
				t.Fatalf("last message=%+v, want %d tool results", results, test.wantResults)
			}
			if test.providerID == "deepseek" && (results.Content[0].ToolResult.ToolCallID != "call_0" || results.Content[1].ToolResult.ToolCallID != "call_1") {
				t.Fatalf("tool result order=%+v", results.Content)
			}
		})
	}
}

func TestConvertRequestUsesInstructionsForPrivateSystemOnAnthropicFollowup(t *testing.T) {
	private := message.NewText(message.RoleSystem, "trusted follow-up context")
	private.Visibility = message.VisibilityPrivate
	request := hyprovider.Request{Messages: []message.Message{
		message.NewText(message.RoleSystem, "base instructions"),
		message.NewText(message.RoleUser, "first question"),
		message.NewText(message.RoleAssistant, "first answer"),
		private,
		message.NewText(message.RoleUser, "follow-up question"),
	}}

	for _, providerID := range []string{"anthropic", "deepseek", "google", "mistral", "cohere"} {
		converted, _, err := convertRequest(request, "", providerID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(converted.Instructions, "base instructions") || !strings.Contains(converted.Instructions, "trusted follow-up context") {
			t.Fatalf("%s instructions = %q", providerID, converted.Instructions)
		}
		for _, current := range converted.Messages {
			if current.Role == sdk.RoleDeveloper {
				t.Fatalf("%s follow-up leaked unsupported developer role: %+v", providerID, current)
			}
		}
	}

	converted, _, err := convertRequest(request, "", "openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Messages) != 4 || converted.Messages[2].Role != sdk.RoleDeveloper {
		t.Fatalf("openai private system role was not preserved: %+v", converted.Messages)
	}
}

func TestStreamAdapterRestoresCanonicalToolNames(t *testing.T) {
	names := newToolNames([]message.ToolDefinition{{Name: "coding.read_file"}})
	stream := &streamAdapter{inner: &sliceStream{parts: []sdk.Part{{
		Kind:     sdk.PartToolCall,
		ToolCall: &sdk.ToolCall{ID: "call_1", Name: "coding_read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
	}}}, names: names}
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.ToolCall == nil || event.ToolCall.Name != "coding.read_file" || event.ToolCall.ID != "call_1" {
		t.Fatalf("stream tool call = %+v", event.ToolCall)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}
