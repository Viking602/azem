package responses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
)

const maxSSEFrameBytes = 4 << 20

var errSSEFrameTooLarge = errors.New("provider SSE frame exceeds 4 MiB")

type Stream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	body      io.ReadCloser
	reader    *frameReader
	builders  map[string]*toolBuilder
	emitted   map[string]bool
	completed bool
	terminal  bool
	toolUse   bool
	closeOnce sync.Once
}

type toolBuilder struct {
	ID        string
	CallID    string
	Name      string
	Arguments strings.Builder
}

type streamEvent struct {
	Type        string          `json:"type"`
	Code        string          `json:"code"`
	Message     string          `json:"message"`
	Delta       string          `json:"delta"`
	Text        string          `json:"text"`
	Name        string          `json:"name"`
	Arguments   json.RawMessage `json:"arguments"`
	ItemID      string          `json:"item_id"`
	CallID      string          `json:"call_id"`
	OutputIndex *int            `json:"output_index"`
	Item        json.RawMessage `json:"item"`
	Response    json.RawMessage `json:"response"`
	Error       json.RawMessage `json:"error"`
}

type streamItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type completedResponse struct {
	Status string `json:"status"`
	Usage  struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		TotalTokens        int `json:"total_tokens"`
		InputTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

func NewStream(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser) *Stream {
	return &Stream{ctx: ctx, cancel: cancel, body: body, reader: newFrameReader(body), builders: make(map[string]*toolBuilder), emitted: make(map[string]bool)}
}

func (s *Stream) Recv() (hyprovider.Event, error) {
	if s.terminal {
		return hyprovider.Event{}, io.EOF
	}
	for {
		frame, err := s.reader.Next()
		if err != nil {
			if contextErr := s.ctx.Err(); contextErr != nil {
				s.terminal = true
				_ = s.Close()
				return hyprovider.Event{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonAborted}, nil
			}
			if errors.Is(err, io.EOF) && s.completed {
				s.terminal = true
				_ = s.Close()
				return hyprovider.Event{}, io.EOF
			}
			s.terminal = true
			_ = s.Close()
			return hyprovider.Event{Kind: hyprovider.EventError, Err: &APIError{Kind: ErrorStream, Message: err.Error()}}, nil
		}
		data := strings.TrimSpace(frame.Data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			if s.completed {
				s.terminal = true
				_ = s.Close()
				return hyprovider.Event{}, io.EOF
			}
			s.terminal = true
			_ = s.Close()
			return hyprovider.Event{Kind: hyprovider.EventError, Err: &APIError{Kind: ErrorStream, Message: "stream ended before response.completed"}}, nil
		}
		var event streamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			s.terminal = true
			_ = s.Close()
			return hyprovider.Event{Kind: hyprovider.EventError, Err: &APIError{Kind: ErrorStream, Message: "invalid JSON in provider stream"}}, nil
		}
		if event.Type == "" {
			event.Type = frame.Name
		}
		mapped, emit, terminal := s.mapEvent(event, []byte(data))
		if terminal || (emit && mapped.Kind == hyprovider.EventDone) {
			s.terminal = true
			_ = s.Close()
		}
		if emit {
			return mapped, nil
		}
	}
}

func (s *Stream) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		closeErr = s.body.Close()
	})
	return closeErr
}

func (s *Stream) mapEvent(event streamEvent, raw []byte) (hyprovider.Event, bool, bool) {
	switch event.Type {
	case "response.output_text.delta":
		if event.Delta != "" {
			return hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: event.Delta}, true, false
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if event.Delta != "" {
			return hyprovider.Event{Kind: hyprovider.EventThinkingDelta, Thinking: event.Delta}, true, false
		}
	case "response.output_item.added":
		var item streamItem
		if json.Unmarshal(event.Item, &item) == nil && isToolItem(item.Type) {
			s.updateBuilder(item, event.OutputIndex, false)
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		builder := s.builder(event.ItemID, event.CallID, event.OutputIndex)
		if event.Name != "" {
			builder.Name = event.Name
		}
		builder.Arguments.WriteString(event.Delta)
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		builder := s.builder(event.ItemID, event.CallID, event.OutputIndex)
		if event.Name != "" {
			builder.Name = event.Name
		}
		if len(event.Arguments) > 0 {
			arguments, err := argumentBytes(event.Arguments)
			if err != nil {
				return s.errorEvent(err), true, true
			}
			builder.Arguments.Reset()
			builder.Arguments.Write(arguments)
		}
		return s.finishTool(builder)
	case "response.output_item.done":
		var item streamItem
		if err := json.Unmarshal(event.Item, &item); err == nil && isToolItem(item.Type) {
			builder, err := s.updateBuilder(item, event.OutputIndex, true)
			if err != nil {
				return s.errorEvent(err), true, true
			}
			return s.finishTool(builder)
		}
	case "response.completed":
		var response completedResponse
		if err := json.Unmarshal(event.Response, &response); err != nil {
			return s.errorEvent(&APIError{Kind: ErrorStream, Message: "invalid response.completed payload"}), true, true
		}
		if response.Status != "" && response.Status != "completed" {
			return s.errorEvent(streamError(raw)), true, true
		}
		if s.hasIncompleteTool() {
			return s.errorEvent(&APIError{Kind: ErrorStream, Message: "response completed with an unfinished tool call"}), true, true
		}
		s.completed = true
		reason := hyprovider.StopReasonComplete
		if s.toolUse {
			reason = hyprovider.StopReasonToolUse
		}
		usage := hyprovider.Usage{
			InputTokens: response.Usage.InputTokens, CachedInputTokens: response.Usage.InputTokensDetails.CachedTokens,
			OutputTokens: response.Usage.OutputTokens, TotalTokens: response.Usage.TotalTokens,
		}
		return hyprovider.Event{Kind: hyprovider.EventDone, StopReason: reason, Usage: usage}, true, false
	case "response.failed", "response.incomplete", "error":
		return s.errorEvent(streamError(raw)), true, true
	}
	return hyprovider.Event{}, false, false
}

func (s *Stream) hasIncompleteTool() bool {
	seen := make(map[*toolBuilder]bool)
	for _, builder := range s.builders {
		if seen[builder] {
			continue
		}
		seen[builder] = true
		id := firstString(builder.CallID, builder.ID)
		if !s.emitted[id] && (id != "" || builder.Name != "" || builder.Arguments.Len() > 0) {
			return true
		}
	}
	return false
}

func (s *Stream) updateBuilder(item streamItem, index *int, replaceArguments bool) (*toolBuilder, error) {
	builder := s.builder(item.ID, item.CallID, index)
	if item.ID != "" {
		builder.ID = item.ID
		s.builders["id:"+item.ID] = builder
	}
	if item.CallID != "" {
		builder.CallID = item.CallID
		s.builders["call:"+item.CallID] = builder
	}
	if item.Name != "" {
		builder.Name = item.Name
	}
	if len(item.Arguments) > 0 {
		arguments, err := argumentBytes(item.Arguments)
		if err != nil {
			return builder, err
		}
		if replaceArguments || builder.Arguments.Len() == 0 {
			builder.Arguments.Reset()
			builder.Arguments.Write(arguments)
		}
	}
	return builder, nil
}

func (s *Stream) builder(itemID string, callID string, index *int) *toolBuilder {
	keys := builderKeys(itemID, callID, index)
	for _, key := range keys {
		if builder := s.builders[key]; builder != nil {
			for _, alias := range keys {
				s.builders[alias] = builder
			}
			return builder
		}
	}
	builder := &toolBuilder{ID: itemID, CallID: callID}
	for _, key := range keys {
		s.builders[key] = builder
	}
	return builder
}

// ToolItemID returns the provider's response item ID for a completed function call.
func (s *Stream) ToolItemID(callID string) string {
	builder := s.builders["call:"+callID]
	if builder == nil {
		return ""
	}
	return builder.ID
}

func (s *Stream) finishTool(builder *toolBuilder) (hyprovider.Event, bool, bool) {
	id := firstString(builder.CallID, builder.ID)
	if id == "" || builder.Name == "" {
		return s.errorEvent(&APIError{Kind: ErrorStream, Message: "completed tool call is missing id or name"}), true, true
	}
	if s.emitted[id] {
		return hyprovider.Event{}, false, false
	}
	arguments := []byte(builder.Arguments.String())
	if len(arguments) == 0 {
		arguments = []byte(`{}`)
	}
	if !json.Valid(arguments) {
		return s.errorEvent(&APIError{Kind: ErrorStream, Message: "completed tool call arguments are not valid JSON"}), true, true
	}
	s.emitted[id] = true
	s.toolUse = true
	return hyprovider.Event{Kind: hyprovider.EventToolCall, ToolCall: &message.ToolCall{ID: id, Name: builder.Name, Arguments: json.RawMessage(bytes.Clone(arguments))}}, true, false
}

func (s *Stream) errorEvent(err error) hyprovider.Event {
	return hyprovider.Event{Kind: hyprovider.EventError, Err: err}
}

func isToolItem(itemType string) bool {
	return itemType == "function_call" || itemType == "custom_tool_call"
}

func argumentBytes(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return []byte(value), nil
	}
	return bytes.Clone(raw), nil
}

func builderKeys(itemID string, callID string, index *int) []string {
	keys := make([]string, 0, 3)
	if itemID != "" {
		keys = append(keys, "id:"+itemID)
	}
	if callID != "" {
		keys = append(keys, "call:"+callID)
	}
	if index != nil {
		keys = append(keys, fmt.Sprintf("index:%d", *index))
	}
	if len(keys) == 0 {
		keys = append(keys, "anonymous")
	}
	return keys
}

type sseFrame struct {
	Name string
	Data string
}

type frameReader struct{ reader *bufio.Reader }

func newFrameReader(reader io.Reader) *frameReader {
	return &frameReader{reader: bufio.NewReaderSize(reader, 64<<10)}
}

func (r *frameReader) Next() (sseFrame, error) {
	var name string
	var data []string
	total := 0
	for {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) && len(data) == 0 && name == "" {
				return sseFrame{}, io.EOF
			}
			return sseFrame{}, err
		}
		total += len(line)
		if total > maxSSEFrameBytes {
			return sseFrame{}, errSSEFrameTooLarge
		}
		if len(line) == 0 {
			if len(data) == 0 && name == "" {
				continue
			}
			return sseFrame{Name: name, Data: strings.Join(data, "\n")}, nil
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := strings.Cut(string(line), ":")
		if !found {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			data = append(data, value)
		}
	}
}

func (r *frameReader) readLine() ([]byte, error) {
	var line []byte
	for {
		part, err := r.reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > maxSSEFrameBytes {
			return nil, errSSEFrameTooLarge
		}
		if err == nil {
			return bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r")), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, io.EOF
		}
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
}

var _ hyprovider.Stream = (*Stream)(nil)
