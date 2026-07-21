package responses

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

const (
	UsageReporterExtraKey  = "azem_usage_reporter"
	AttachmentRootExtraKey = "azem_attachment_root"
	maxWireImages          = 6
	maxWireImageBytes      = 8 << 20
)

type UsageDetails struct {
	InputTokens      int
	CachedTokens     int
	CacheWriteTokens int
	OutputTokens     int
	ReasoningTokens  int
	TotalTokens      int
}

type UsageReporter func(UsageDetails)

func RequestUsageReporter(request hyprovider.Request) UsageReporter {
	reporter, _ := request.ExtraBody[UsageReporterExtraKey].(UsageReporter)
	return reporter
}

type wireRequest struct {
	Model             string            `json:"model"`
	PromptCacheKey    string            `json:"prompt_cache_key,omitempty"`
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
	instructions, input, err := buildInput(request.Messages, options.ToolCallItemID, strings.TrimSpace(stringExtra(request, AttachmentRootExtraKey)))
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
		Model: request.Model, PromptCacheKey: strings.TrimSpace(stringExtra(request, "prompt_cache_key")),
		Instructions: instructions, Input: input, Tools: tools,
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

func buildInput(messages []message.Message, toolCallItemID func(string) string, attachmentRoot string) (string, []any, error) {
	instructions := make([]string, 0, 2)
	input := make([]any, 0, len(messages))
	for _, current := range messages {
		switch current.Role {
		case message.RoleSystem:
			if current.Text == "" {
				continue
			}
			if current.Visibility == message.VisibilityPrivate {
				input = append(input, wireMessage("developer", "input_text", current.Text))
			} else {
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
			if len(current.ProviderState) > 0 {
				items, err := decodeProviderState(current.ProviderState)
				if err != nil {
					return "", nil, err
				}
				for _, item := range items {
					input = append(input, item)
				}
				continue
			}
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
				item := map[string]any{
					"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(arguments),
				}
				if toolCallItemID != nil {
					if resolved := toolCallItemID(call.ID); resolved != "" {
						item["id"] = resolved
					}
				}
				input = append(input, item)
			}
		case message.RoleUser, message.RoleCustom:
			item, ok, err := wireUserMessage(current, attachmentRoot)
			if err != nil {
				return "", nil, err
			}
			if ok {
				input = append(input, item)
			}
		default:
			return "", nil, fmt.Errorf("unsupported message role %q", current.Role)
		}
	}
	return strings.Join(instructions, "\n\n"), input, nil
}

func decodeProviderState(state json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(state)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return nil, fmt.Errorf("responses provider state must be a JSON array")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("decode responses provider state: %w", err)
	}
	return items, nil
}

func wireMessage(role string, contentType string, text string) map[string]any {
	return map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": contentType, "text": text}}}
}

type wireAttachment struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	MIME string `json:"mime,omitempty"`
	Path string `json:"path,omitempty"`
	Size int64  `json:"size,omitempty"`
}

func wireUserMessage(current message.Message, attachmentRoot string) (map[string]any, bool, error) {
	content := make([]any, 0, 4)
	if text := strings.TrimSpace(current.Text); text != "" {
		content = append(content, map[string]any{"type": "input_text", "text": text})
	}
	attachments, err := decodeWireAttachments(current.Metadata)
	if err != nil {
		return nil, false, err
	}
	if len(attachments) > maxWireImages {
		return nil, false, fmt.Errorf("responses request has %d images; maximum is %d", len(attachments), maxWireImages)
	}
	for _, att := range attachments {
		part, err := wireInputImage(att, attachmentRoot)
		if err != nil {
			return nil, false, err
		}
		content = append(content, part)
	}
	if len(content) == 0 {
		return nil, false, nil
	}
	return map[string]any{"type": "message", "role": "user", "content": content}, true, nil
}

func decodeWireAttachments(meta map[string]string) ([]wireAttachment, error) {
	if meta == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(meta["azem.attachments"])
	if raw == "" || raw == "[]" || raw == "null" {
		return nil, nil
	}
	var atts []wireAttachment
	if err := json.Unmarshal([]byte(raw), &atts); err != nil {
		return nil, fmt.Errorf("decode responses image attachments: %w", err)
	}
	return atts, nil
}

func wireInputImage(att wireAttachment, attachmentRoot string) (map[string]any, error) {
	path := strings.TrimSpace(att.Path)
	if path == "" {
		return nil, fmt.Errorf("image attachment %q is missing a path", att.Name)
	}
	if attachmentRoot == "" {
		return nil, fmt.Errorf("image attachment %q has no trusted attachment root", att.Name)
	}
	root, err := filepath.EvalSymlinks(attachmentRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve attachment root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve image %q: %w", firstNonEmpty(att.Name, path), err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("image attachment %q is outside the trusted attachment root", firstNonEmpty(att.Name, path))
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("read image %q: %w", firstNonEmpty(att.Name, path), err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxWireImageBytes {
		return nil, fmt.Errorf("image %q is not a regular file within the %d MiB limit", firstNonEmpty(att.Name, path), maxWireImageBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWireImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image %q: %w", firstNonEmpty(att.Name, path), err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("image %q is empty", firstNonEmpty(att.Name, path))
	}
	if len(data) > maxWireImageBytes {
		return nil, fmt.Errorf("image %q exceeds the %d MiB limit", firstNonEmpty(att.Name, path), maxWireImageBytes>>20)
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(data[:min(len(data), 512)]), ";", 2)[0]))
	if mimeType == "image/jpg" {
		mimeType = "image/jpeg"
	}
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return nil, fmt.Errorf("image %q has unsupported detected type %q", firstNonEmpty(att.Name, path), mimeType)
	}
	url := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	return map[string]any{
		"type":      "input_image",
		"image_url": url,
		"detail":    "auto",
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
