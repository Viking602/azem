package responses

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
)

type BuildOptions struct {
	IncludeEncryptedReasoning bool
	DefaultParallelTools      bool
	DefaultReasoningEffort    string
	ToolCallItemID            func(string) string
}

type wireRequest struct {
	Model             string            `json:"model"`
	Instructions      string            `json:"instructions,omitempty"`
	Input             []any             `json:"input"`
	Tools             []any             `json:"tools,omitempty"`
	ToolChoice        string            `json:"tool_choice,omitempty"`
	ParallelToolCalls bool              `json:"parallel_tool_calls"`
	Reasoning         map[string]any    `json:"reasoning,omitempty"`
	MaxOutputTokens   int               `json:"max_output_tokens,omitempty"`
	Text              map[string]any    `json:"text,omitempty"`
	Store             bool              `json:"store"`
	Stream            bool              `json:"stream"`
	Include           []string          `json:"include,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

func Build(request hyprovider.Request, options BuildOptions) ([]byte, error) {
	if strings.TrimSpace(request.Model) == "" {
		return nil, fmt.Errorf("responses request model is empty")
	}
	instructions, input, err := buildInput(request.Messages, options.ToolCallItemID)
	if err != nil {
		return nil, err
	}
	tools := make([]any, 0, len(request.Tools))
	for _, definition := range request.Tools {
		tools = append(tools, map[string]any{
			"type": "function", "name": definition.Name, "description": definition.Description,
			"parameters": definition.InputSchema, "strict": false,
		})
	}
	parallel := options.DefaultParallelTools
	if value, ok := boolExtra(request, "parallel_tool_calls"); ok {
		parallel = value
	}
	effort := firstString(request.Metadata["reasoning_effort"], stringExtra(request, "reasoning_effort"), options.DefaultReasoningEffort)
	var reasoning map[string]any
	if effort != "" {
		reasoning = map[string]any{"effort": effort, "summary": "auto"}
	}
	maxOutput := intExtra(request, "max_output_tokens")
	wire := wireRequest{
		Model: request.Model, Instructions: instructions, Input: input, Tools: tools,
		ParallelToolCalls: parallel, Reasoning: reasoning, MaxOutputTokens: maxOutput,
		Store: false, Stream: true, Metadata: sanitizedMetadata(request.Metadata),
	}
	if len(tools) > 0 {
		wire.ToolChoice = "auto"
	}
	if options.IncludeEncryptedReasoning {
		wire.Include = []string{"reasoning.encrypted_content"}
	}
	if request.ResponseFormat != nil {
		format := map[string]any{"type": request.ResponseFormat.Type}
		if request.ResponseFormat.Name != "" {
			format["name"] = request.ResponseFormat.Name
		}
		if request.ResponseFormat.Schema != nil {
			format["schema"] = request.ResponseFormat.Schema
		}
		format["strict"] = request.ResponseFormat.Strict
		wire.Text = map[string]any{"format": format}
	}
	return json.Marshal(wire)
}

func buildInput(messages []message.Message, toolCallItemID func(string) string) (string, []any, error) {
	instructions := make([]string, 0, 2)
	input := make([]any, 0, len(messages))
	for _, current := range messages {
		switch current.Role {
		case message.RoleSystem:
			if current.Text != "" {
				instructions = append(instructions, current.Text)
			}
		case message.RoleTool:
			if current.ToolResult == nil {
				return "", nil, fmt.Errorf("tool message %q has no result", current.ID)
			}
			output := current.ToolResult.Content
			if output == "" && len(current.ToolResult.Structured) > 0 {
				output = string(current.ToolResult.Structured)
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": current.ToolResult.ToolCallID, "output": output})
		case message.RoleAssistant:
			if current.Text != "" {
				input = append(input, wireMessage("assistant", "output_text", current.Text))
			}
			for _, call := range current.ToolCalls {
				arguments := call.Arguments
				if len(arguments) == 0 {
					arguments = json.RawMessage(`{}`)
				}
				if !json.Valid(arguments) {
					return "", nil, fmt.Errorf("assistant tool call %q has invalid JSON arguments", call.ID)
				}
				itemID := call.ID
				if toolCallItemID != nil {
					if resolved := toolCallItemID(call.ID); resolved != "" {
						itemID = resolved
					}
				}
				input = append(input, map[string]any{"type": "function_call", "id": itemID, "call_id": call.ID, "name": call.Name, "arguments": string(arguments)})
			}
		case message.RoleUser, message.RoleCustom:
			if current.Text != "" {
				input = append(input, wireMessage("user", "input_text", current.Text))
			}
		default:
			return "", nil, fmt.Errorf("unsupported message role %q", current.Role)
		}
	}
	return strings.Join(instructions, "\n\n"), input, nil
}

func wireMessage(role string, contentType string, text string) map[string]any {
	return map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": contentType, "text": text}}}
}

func boolExtra(request hyprovider.Request, key string) (bool, bool) {
	value, ok := request.ExtraBody[key]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(typed)
		return parsed, err == nil
	default:
		return false, false
	}
}

func intExtra(request hyprovider.Request, key string) int {
	value := request.ExtraBody[key]
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func stringExtra(request hyprovider.Request, key string) string {
	value, _ := request.ExtraBody[key].(string)
	return value
}

func sanitizedMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	allowed := make(map[string]string)
	for _, key := range []string{"run_id", "session_id", "agent_id"} {
		if value := metadata[key]; value != "" {
			allowed[key] = value
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
