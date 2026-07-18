package codex

import (
	"crypto/sha256"
	"fmt"

	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
)

const maxCodexToolNameLength = 64

func mapToolNames(request hyprovider.Request) (hyprovider.Request, map[string]string) {
	if len(request.Tools) == 0 {
		return request, nil
	}
	mapped := request
	mapped.Tools = append([]message.ToolDefinition(nil), request.Tools...)
	forward := make(map[string]string, len(request.Tools))
	reverse := make(map[string]string, len(request.Tools))
	for index := range mapped.Tools {
		original := mapped.Tools[index].Name
		name := codexToolName(original)
		if collision, exists := reverse[name]; exists && collision != original {
			digest := sha256.Sum256([]byte(original))
			suffix := fmt.Sprintf("_%x", digest[:4])
			name = name[:min(len(name), maxCodexToolNameLength-len(suffix))] + suffix
		}
		mapped.Tools[index].Name = name
		forward[original] = name
		reverse[name] = original
	}
	mapped.Messages = append([]message.Message(nil), request.Messages...)
	for index := range mapped.Messages {
		if len(mapped.Messages[index].ToolCalls) == 0 {
			continue
		}
		mapped.Messages[index].ToolCalls = append([]message.ToolCall(nil), mapped.Messages[index].ToolCalls...)
		for callIndex := range mapped.Messages[index].ToolCalls {
			if name := forward[mapped.Messages[index].ToolCalls[callIndex].Name]; name != "" {
				mapped.Messages[index].ToolCalls[callIndex].Name = name
			}
		}
	}
	return mapped, reverse
}

func codexToolName(name string) string {
	buffer := make([]byte, 0, min(len(name), maxCodexToolNameLength))
	for index := 0; index < len(name) && len(buffer) < maxCodexToolNameLength; index++ {
		character := name[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			buffer = append(buffer, character)
		} else {
			buffer = append(buffer, '_')
		}
	}
	if len(buffer) == 0 {
		return "tool"
	}
	return string(buffer)
}

type toolNameStream struct {
	inner        hyprovider.Stream
	reverse      map[string]string
	recordItemID func(string, string)
}

func (s *toolNameStream) Recv() (hyprovider.Event, error) {
	event, err := s.inner.Recv()
	if event.ToolCall != nil && s.recordItemID != nil {
		if source, ok := s.inner.(interface{ ToolItemID(string) string }); ok {
			if itemID := source.ToolItemID(event.ToolCall.ID); itemID != "" {
				s.recordItemID(event.ToolCall.ID, itemID)
			}
		}
	}
	if event.ToolCall != nil {
		if original := s.reverse[event.ToolCall.Name]; original != "" {
			call := *event.ToolCall
			call.Name = original
			event.ToolCall = &call
		}
	}
	if event.ToolCallDelta != nil {
		if original := s.reverse[event.ToolCallDelta.Name]; original != "" {
			delta := *event.ToolCallDelta
			delta.Name = original
			event.ToolCallDelta = &delta
		}
	}
	return event, err
}

func (s *toolNameStream) Close() error {
	return s.inner.Close()
}
