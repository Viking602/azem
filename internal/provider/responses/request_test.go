package responses

import (
	"encoding/json"
	"reflect"
	"strings"
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
		ExtraBody:      map[string]any{"max_output_tokens": 2048, "parallel_tool_calls": false, "prompt_cache_key": "session-1"},
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
	if payload["model"] != "gpt-test" || payload["prompt_cache_key"] != "session-1" ||
		payload["instructions"] != "system rules" || payload["store"] != false || payload["stream"] != true {
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

func TestBuildKeepsPrivateSystemMessagesInConversationPosition(t *testing.T) {
	private := message.NewText(message.RoleSystem, "current trusted context")
	private.Visibility = message.VisibilityPrivate
	data, err := Build(hyprovider.Request{
		Model: "gpt-test",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "stable rules"),
			message.NewText(message.RoleUser, "old request"),
			private,
			message.NewText(message.RoleUser, "new request"),
		},
	}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Instructions string            `json:"instructions"`
		Input        []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Instructions != "stable rules" || len(payload.Input) != 3 {
		t.Fatalf("payload = %s", data)
	}
	var roles []string
	for _, raw := range payload.Input {
		var item struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, item.Role)
		if item.Role == "developer" && (len(item.Content) != 1 || item.Content[0].Type != "input_text" || item.Content[0].Text != private.Text) {
			t.Fatalf("private developer item = %s", raw)
		}
	}
	if got := strings.Join(roles, ","); got != "user,developer,user" {
		t.Fatalf("input roles = %s; payload=%s", got, data)
	}
}

func TestBuildPrivateTodoUpdatePreservesExactWirePrefix(t *testing.T) {
	initial := message.NewText(message.RoleSystem, "[Session Todo private reminder] revision=1")
	initial.Visibility = message.VisibilityPrivate
	history := []message.Message{
		message.NewText(message.RoleSystem, "stable rules"),
		initial,
		message.NewText(message.RoleUser, "continue"),
	}
	request := hyprovider.Request{
		Model: "gpt-test", Messages: history,
		ExtraBody: map[string]any{"prompt_cache_key": "session-1"},
	}
	firstData, err := Build(request, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	update := message.NewText(message.RoleSystem, "[Session Todo private reminder] revision=2")
	update.Visibility = message.VisibilityPrivate
	request.Messages = append(append([]message.Message(nil), history...), update)
	secondData, err := Build(request, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var first, second struct {
		Instructions   string            `json:"instructions"`
		PromptCacheKey string            `json:"prompt_cache_key"`
		Tools          []json.RawMessage `json:"tools"`
		Input          []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(firstData, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(secondData, &second); err != nil {
		t.Fatal(err)
	}
	if first.Instructions != second.Instructions || first.PromptCacheKey != second.PromptCacheKey || !reflect.DeepEqual(first.Tools, second.Tools) {
		t.Fatalf("stable request fields changed: first=%s second=%s", firstData, secondData)
	}
	if len(second.Input) != len(first.Input)+1 {
		t.Fatalf("input lengths first=%d second=%d", len(first.Input), len(second.Input))
	}
	for index := range first.Input {
		if string(first.Input[index]) != string(second.Input[index]) {
			t.Fatalf("wire prefix changed at input %d: first=%s second=%s", index, first.Input[index], second.Input[index])
		}
	}
}

func TestBuildReplaysAssistantProviderStateWithoutSyntheticDuplicates(t *testing.T) {
	state := json.RawMessage(`[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"},{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"raw answer"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"}]`)
	data, err := Build(hyprovider.Request{
		Model: "gpt-test",
		Messages: []message.Message{
			message.NewText(message.RoleUser, "inspect"),
			{
				Role: message.RoleAssistant, Text: "normalized text must not be serialized",
				ProviderState: state,
				ToolCalls: []message.ToolCall{{
					ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"normalized"}`),
				}},
			},
			message.NewToolResult(message.ToolResult{ToolCallID: "call_1", Name: "lookup", Content: "result"}),
		},
	}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	var want []json.RawMessage
	if err := json.Unmarshal(state, &want); err != nil {
		t.Fatal(err)
	}
	if len(payload.Input) != len(want)+2 {
		t.Fatalf("input count = %d; payload=%s", len(payload.Input), data)
	}
	for index := range want {
		if string(payload.Input[index+1]) != string(want[index]) {
			t.Fatalf("raw item %d = %s, want %s", index, payload.Input[index+1], want[index])
		}
	}
	if strings.Contains(string(data), "normalized text must not be serialized") || strings.Contains(string(data), "normalized") {
		t.Fatalf("synthetic assistant data duplicated provider state: %s", data)
	}
}

func TestBuildRejectsMalformedAssistantProviderState(t *testing.T) {
	_, err := Build(hyprovider.Request{
		Model: "gpt-test",
		Messages: []message.Message{{
			Role: message.RoleAssistant, ProviderState: json.RawMessage(`{"type":"reasoning"}`),
		}},
	}, BuildOptions{})
	if err == nil || !strings.Contains(err.Error(), "must be a JSON array") {
		t.Fatalf("malformed provider state error = %v", err)
	}
}

func TestBuildOmitsSyntheticFunctionItemIDWithoutProviderMapping(t *testing.T) {
	data, err := Build(hyprovider.Request{
		Model: "gpt-test",
		Messages: []message.Message{{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{{
				ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`),
			}},
		}},
	}, BuildOptions{ToolCallItemID: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Input) != 1 || payload.Input[0]["call_id"] != "call_1" || payload.Input[0]["id"] != nil {
		t.Fatalf("synthetic function input = %s", data)
	}
}
