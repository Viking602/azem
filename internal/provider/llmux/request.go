package llmuxdriver

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/Viking602/llmux"
	"github.com/Viking602/llmux/provider/openai/compat"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/provider/responses"
)

func convertRequest(request hyprovider.Request, defaultReasoningEffort, providerID string) (sdk.Request, *toolNames, error) {
	names := newToolNames(request.Tools)
	anthropicProtocol := providerID == "anthropic"
	developerMessages := providerID == "openai" || providerID == "xai"
	if profile, ok := compat.Lookup(providerID); ok {
		anthropicProtocol = profile.Protocol == compat.ProtocolAnthropic
		developerMessages = profile.Protocol == compat.ProtocolResponses
	}
	messages, instructions, err := convertMessages(request.Messages, stringExtra(request.ExtraBody, responses.AttachmentRootExtraKey), names, anthropicProtocol, developerMessages)
	if err != nil {
		return sdk.Request{}, nil, err
	}
	tools := make([]sdk.ToolDefinition, 0, len(request.Tools))
	for _, definition := range request.Tools {
		schema, err := json.Marshal(definition.InputSchema)
		if err != nil {
			return sdk.Request{}, nil, fmt.Errorf("encode tool %q schema: %w", definition.Name, err)
		}
		tools = append(tools, sdk.ToolDefinition{Name: names.Wire(definition.Name), Description: definition.Description, InputSchema: schema})
	}
	zeroRetries, parallel := 0, true
	if value, ok := boolExtra(request.ExtraBody, "parallel_tool_calls"); ok {
		parallel = value
	}
	options := sdk.CallOptions{StopSequences: request.StopSequences, Tools: tools, ParallelToolCalls: &parallel, MaxRetries: &zeroRetries}
	if len(tools) > 0 {
		options.ToolChoice = &sdk.ToolChoice{Mode: sdk.ToolChoiceAuto}
	}
	if maxOutput := intExtra(request.ExtraBody, "max_output_tokens"); maxOutput > 0 {
		options.MaxOutputTokens = &maxOutput
	} else if request.MaxTokens > 0 {
		// Venat ModelMaxTokens lands on Request.MaxTokens; honor it when the
		// host did not also put max_output_tokens in ExtraBody.
		maxOutput := request.MaxTokens
		options.MaxOutputTokens = &maxOutput
	}
	effort := firstNonempty(request.Metadata["reasoning_effort"], stringExtra(request.ExtraBody, "reasoning_effort"), defaultReasoningEffort)
	if effort != "" {
		options.Reasoning = &sdk.ReasoningOptions{Effort: effort, Summary: "auto"}
	}
	if request.ResponseFormat != nil {
		format := &sdk.ResponseFormat{Type: request.ResponseFormat.Type, Name: request.ResponseFormat.Name, Strict: request.ResponseFormat.Strict}
		if request.ResponseFormat.Schema != nil {
			format.Schema, err = json.Marshal(request.ResponseFormat.Schema)
			if err != nil {
				return sdk.Request{}, nil, fmt.Errorf("encode response schema: %w", err)
			}
		}
		options.ResponseFormat = format
	}
	if providerID == "xai" {
		options.ProviderOptions = map[string]json.RawMessage{"xai": json.RawMessage(`{"include":["reasoning.encrypted_content"]}`)}
	}
	return sdk.Request{Messages: messages, Instructions: instructions, Metadata: sanitizedMetadata(request.Metadata), Options: options}, names, nil
}

func convertMessages(input []message.Message, attachmentRoot string, names *toolNames, anthropicProtocol, developerMessages bool) ([]sdk.Message, string, error) {
	messages := make([]sdk.Message, 0, len(input))
	instructions := make([]string, 0, 2)
	for _, current := range input {
		switch current.Role {
		case message.RoleSystem:
			if current.Text == "" {
				continue
			}
			if current.Visibility == message.VisibilityPrivate {
				if developerMessages {
					messages = append(messages, sdk.TextMessage(sdk.RoleDeveloper, current.Text))
				} else {
					instructions = append(instructions, current.Text)
				}
			} else {
				instructions = append(instructions, current.Text)
			}
		case message.RoleUser, message.RoleCustom:
			parts, err := userContentParts(current, attachmentRoot)
			if err != nil {
				return nil, "", err
			}
			if len(parts) > 0 {
				messages = append(messages, sdk.Message{Role: sdk.RoleUser, Content: parts})
			}
		case message.RoleAssistant:
			converted, err := assistantMessage(current, names)
			if err != nil {
				return nil, "", err
			}
			messages = append(messages, converted)
		case message.RoleTool:
			if current.ToolResult == nil {
				return nil, "", fmt.Errorf("tool message %q has no result", current.ID)
			}
			result := current.ToolResult
			part := sdk.ContentPart{
				Kind: sdk.ContentToolResult, ToolResult: &sdk.ToolResult{
					ToolCallID: result.ToolCallID, Name: names.Wire(result.Name), Content: result.Content,
					Structured: append(json.RawMessage(nil), result.Structured...), IsError: result.IsError,
				},
			}
			if anthropicProtocol && len(messages) > 0 && messages[len(messages)-1].Role == sdk.RoleTool {
				messages[len(messages)-1].Content = append(messages[len(messages)-1].Content, part)
			} else {
				messages = append(messages, sdk.Message{Role: sdk.RoleTool, Content: []sdk.ContentPart{part}})
			}
		default:
			return nil, "", fmt.Errorf("unsupported message role %q", current.Role)
		}
	}
	return messages, strings.Join(instructions, "\n\n"), nil
}

func userContentParts(current message.Message, attachmentRoot string) ([]sdk.ContentPart, error) {
	parts := make([]sdk.ContentPart, 0, 2)
	if text := strings.TrimSpace(current.Text); text != "" {
		parts = append(parts, sdk.ContentPart{Kind: sdk.ContentText, Text: text})
	}
	images, err := responses.LoadImageAttachments(current.Metadata, attachmentRoot)
	if err != nil {
		return nil, err
	}
	for _, image := range images {
		parts = append(parts, sdk.ContentPart{Kind: sdk.ContentImage, Data: image.Data, MediaType: image.MediaType})
	}
	return parts, nil
}

func assistantMessage(current message.Message, names *toolNames) (sdk.Message, error) {
	if len(current.ProviderState) > 0 {
		return sdk.Message{Role: sdk.RoleAssistant, ProviderState: append(json.RawMessage(nil), current.ProviderState...)}, nil
	}
	parts := make([]sdk.ContentPart, 0, 1+len(current.ToolCalls))
	if current.Text != "" {
		parts = append(parts, sdk.ContentPart{Kind: sdk.ContentText, Text: current.Text})
	}
	for _, call := range current.ToolCalls {
		arguments := append(json.RawMessage(nil), call.Arguments...)
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		if !json.Valid(arguments) {
			return sdk.Message{}, fmt.Errorf("assistant tool call %q has invalid JSON arguments", call.ID)
		}
		parts = append(parts, sdk.ContentPart{Kind: sdk.ContentToolCall, ToolCall: &sdk.ToolCall{ID: call.ID, Name: names.Wire(call.Name), Arguments: arguments}})
	}
	return sdk.Message{Role: sdk.RoleAssistant, Content: parts}, nil
}

func sanitizedMetadata(metadata map[string]string) map[string]string {
	result := make(map[string]string, 3)
	for _, key := range []string{"run_id", "session_id", "agent_id"} {
		if value := metadata[key]; value != "" {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func stringExtra(extra map[string]any, key string) string {
	value, _ := extra[key].(string)
	return value
}

func boolExtra(extra map[string]any, key string) (bool, bool) {
	value, ok := extra[key]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		if strings.EqualFold(typed, "true") {
			return true, true
		}
		if strings.EqualFold(typed, "false") {
			return false, true
		}
	}
	return false, false
}

func intExtra(extra map[string]any, key string) int {
	switch value := extra[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
