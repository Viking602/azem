package devin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/provider/toolnames"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/google/uuid"
)

type turnState struct {
	Provider        string             `json:"provider"`
	Model           string             `json:"model"`
	MessageID       string             `json:"messageId,omitempty"`
	SignatureType   string             `json:"signatureType,omitempty"`
	CascadeID       string             `json:"cascadeId"`
	OutputID        string             `json:"outputId,omitempty"`
	ThinkingID      string             `json:"thinkingId,omitempty"`
	GeminiSignature []byte             `json:"geminiSignature,omitempty"`
	Phase           string             `json:"phase,omitempty"`
	ToolCalls       []message.ToolCall `json:"toolCalls,omitempty"`
}

func conversationID(account string, request hyprovider.Request) string {
	key := request.PromptCacheKey
	if key == "" {
		key = request.Metadata["session_id"]
	}
	if key == "" {
		return uuid.NewString()
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(account+"\x00"+key)).String()
}

func buildPrompts(ctx context.Context, request hyprovider.Request, cascade string, names *toolnames.Names) (string, []proto, error) {
	var system []string
	var prompts []proto
	leading := true
	for index, current := range request.Messages {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		if leading && current.Role == message.RoleSystem {
			system = append(system, current.TextContent())
			continue
		}
		leading = false
		var p proto
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s\x00%d\x00%s", cascade, index, current.Role))).String()
		text := current.TextContent()
		source := uint64(1)
		switch current.Role {
		case message.RoleSystem:
			// SWE rejects SYSTEM_PROMPT inside conversation prompts with HTTP
			// 502. Match the existing Anthropic host-context envelope and retain
			// its tail position so subsequent turns keep the same cached prefix.
			text = "[Trusted host context]\n" + text
		case message.RoleUser:
		case message.RoleAssistant:
			source = 2
			var state turnState
			native := json.Unmarshal(current.ProviderState, &state) == nil && state.Provider == "devin" && state.Model == request.Model && state.CascadeID == cascade
			if native {
				if state.MessageID != "" {
					id = state.MessageID
				}
				p.text(11, current.ReasoningContent())
				for _, part := range current.CanonicalContent() {
					if part.Kind == message.ContentReasoning {
						p.text(12, part.Signature)
					}
				}
				p.text(18, state.SignatureType)
				p.text(15, state.OutputID)
				p.text(16, state.ThinkingID)
				p.data(17, state.GeminiSignature)
				p.text(19, state.Phase)
			}
			for _, call := range current.ToolCalls {
				wireName, arguments := names.Wire(call.Name), wireToolArguments(call, names)
				if native {
					for _, saved := range state.ToolCalls {
						if saved.ID == call.ID && saved.Name == wireName && json.Valid(saved.Arguments) {
							arguments = saved.Arguments
							break
						}
					}
				}
				var tool proto
				tool.text(1, call.ID)
				tool.text(2, wireName)
				tool.data(3, arguments)
				p.data(6, tool)
			}
		case message.RoleTool:
			source = 4
			if current.ToolResult == nil {
				return "", nil, fmt.Errorf("Devin tool message has no result")
			}
			text = current.ToolResult.TextContent()
			p.text(7, current.ToolResult.ToolCallID)
			if current.ToolResult.IsError {
				p.number(9, 1)
			}
		default:
			return "", nil, fmt.Errorf("Devin does not support message role %q", current.Role)
		}
		p.text(1, id)
		p.number(2, source)
		p.text(3, text)
		if current.Role == message.RoleUser || current.Role == message.RoleTool {
			images, err := responses.LoadImageAttachments(current.Metadata, responses.AttachmentRootFromContext(ctx))
			if err != nil {
				return "", nil, err
			}
			for _, image := range images {
				appendImage(&p, image.Data, image.MediaType)
			}
			parts := current.CanonicalContent()
			if current.ToolResult != nil {
				parts = current.ToolResult.CanonicalContent()
			}
			for _, part := range parts {
				if part.Kind != message.ContentImage {
					continue
				}
				if len(part.Data) == 0 || !strings.HasPrefix(http.DetectContentType(part.Data), "image/") {
					return "", nil, fmt.Errorf("Devin image requires validated image bytes")
				}
				appendImage(&p, part.Data, http.DetectContentType(part.Data))
			}
		}
		prompts = append(prompts, p)
	}
	return strings.Join(system, "\n\n"), prompts, nil
}

func appendImage(p *proto, data []byte, mime string) {
	var image proto
	image.text(1, base64.StdEncoding.EncodeToString(data))
	image.text(2, mime)
	p.data(10, image)
}

func buildRequest(request hyprovider.Request, token, jwt, cascade, system string, prompts []proto, model, assignment string, maxOutput int, supportsParallel bool, names *toolnames.Names) (proto, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("Devin model is empty")
	}
	if request.ResponseFormat != nil {
		return nil, fmt.Errorf("Devin does not expose a structured response format")
	}
	var p proto
	p.data(1, metadata(token, jwt, false))
	p.text(2, system)
	for _, prompt := range prompts {
		p.data(3, prompt)
	}
	p.text(21, model)
	p.number(7, 5)
	p.number(20, 1)
	p.text(16, cascade)
	p.text(22, uuid.NewString())
	p.text(26, assignment)
	var config proto
	output := request.MaxTokens
	if output <= 0 {
		output = maxOutput
	}
	if output <= 0 {
		output = 64000
	}
	config.number(1, 1)
	config.number(2, uint64(output))
	config.number(3, 200)
	temperature := request.Temperature
	if temperature == 0 {
		temperature = 0.01
	}
	config.double(5, temperature)
	config.double(6, temperature)
	config.number(7, 50)
	topP := request.TopP
	if topP == 0 {
		topP = 1
	}
	config.double(8, topP)
	config.double(11, 1)
	for _, stop := range append([]string{"<|user|>", "<|bot|>", "<|context_request|>", "<|endoftext|>", "<|end_of_turn|>"}, request.StopSequences...) {
		config.text(9, stop)
	}
	p.data(8, config)
	for _, definition := range request.Tools {
		schema, err := json.Marshal(definition.InputSchema)
		if err != nil {
			return nil, err
		}
		var tool proto
		tool.text(1, names.Wire(definition.Name))
		tool.text(2, definition.Description)
		tool.data(3, schema)
		p.data(10, tool)
	}
	parallel := supportsParallel && (request.ParallelToolCalls == nil || *request.ParallelToolCalls)
	if !parallel {
		p.number(11, 1)
	}
	var choice, cache proto
	choice.text(1, "auto")
	cache.number(1, 1)
	p.data(12, choice)
	p.data(13, cache)
	return p, nil
}
