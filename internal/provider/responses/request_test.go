package responses

import (
	"encoding/json"
	"testing"

	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
)

func TestBuildMapsHistoryToolsReasoningAndFormat(t *testing.T) {
	additional := false
	request := hyprovider.Request{
		Model: "gpt-test",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "system rules"),
			message.NewText(message.RoleUser, "inspect"),
			{Role: message.RoleAssistant, Text: "calling", ToolCalls: []message.ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}}},
			message.NewToolResult(message.ToolResult{ToolCallID: "call-1", Name: "read_file", Content: "package a"}),
		},
		Tools:          []message.ToolDefinition{{Name: "read_file", Description: "read", InputSchema: message.JSONSchema{Type: "object", Properties: map[string]message.JSONSchema{"path": {Type: "string"}}, Required: []string{"path"}, AdditionalProperties: &additional}}},
		Metadata:       map[string]string{"reasoning_effort": "high", "run_id": "run-1", "secret": "omit"},
		ExtraBody:      map[string]any{"max_output_tokens": 2048, "parallel_tool_calls": false},
		ResponseFormat: &hyprovider.ResponseFormat{Type: "json_schema", Name: "result", Strict: true, Schema: &message.JSONSchema{Type: "object"}},
	}
	data, err := Build(request, BuildOptions{IncludeEncryptedReasoning: true, DefaultParallelTools: true})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "gpt-test" || payload["instructions"] != "system rules" || payload["store"] != false || payload["stream"] != true {
		t.Fatalf("base payload=%s", data)
	}
	if payload["parallel_tool_calls"] != false || payload["max_output_tokens"] != float64(2048) {
		t.Fatalf("options payload=%s", data)
	}
	reasoning := payload["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning=%v", reasoning)
	}
	metadata := payload["metadata"].(map[string]any)
	if metadata["run_id"] != "run-1" || metadata["secret"] != nil {
		t.Fatalf("metadata=%v", metadata)
	}
	input := payload["input"].([]any)
	if len(input) != 4 || input[2].(map[string]any)["type"] != "function_call" || input[3].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("input=%v", input)
	}
	tools := payload["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "read_file" {
		t.Fatalf("tools=%v", tools)
	}
}

func TestBuildRejectsMalformedToolHistory(t *testing.T) {
	_, err := Build(hyprovider.Request{Model: "gpt", Messages: []message.Message{{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "call", Name: "bad", Arguments: json.RawMessage(`{"x":`)}}}}}, BuildOptions{})
	if err == nil {
		t.Fatal("malformed historical tool call accepted")
	}
}
