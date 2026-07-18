package responses

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	hyprovider "github.com/Viking602/go-hydaelyn/provider"
)

type chunkBody struct {
	chunks [][]byte
	index  int
	offset int
	closed bool
}

func (b *chunkBody) Read(target []byte) (int, error) {
	if b.index >= len(b.chunks) {
		return 0, io.EOF
	}
	chunk := b.chunks[b.index]
	count := copy(target, chunk[b.offset:])
	b.offset += count
	if b.offset == len(chunk) {
		b.index++
		b.offset = 0
	}
	return count, nil
}
func (b *chunkBody) Close() error { b.closed = true; return nil }

func TestStreamAssemblesCrossChunkToolCallAndUsage(t *testing.T) {
	frames := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello "}`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"checking"}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read_file"}}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"item-1","delta":"{\"path\":\""}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"item-1","delta":"a.go\"}"}`,
		`data: {"type":"response.function_call_arguments.done","item_id":"item-1"}`,
		`data: {"type":"response.completed","response":{"id":"response-1","status":"completed","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14,"input_tokens_details":{"cached_tokens":6}}}}`,
	}, "\n\n") + "\n\n"
	body := &chunkBody{chunks: [][]byte{[]byte(frames[:17]), []byte(frames[17:93]), []byte(frames[93:211]), []byte(frames[211:])}}
	ctx, cancel := context.WithCancel(context.Background())
	stream := NewStream(ctx, cancel, body)
	var events []hyprovider.Event
	for {
		event, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
		if event.Kind == hyprovider.EventDone || event.Kind == hyprovider.EventError {
			break
		}
	}
	if len(events) != 4 {
		t.Fatalf("events=%#v", events)
	}
	if events[0].Text != "hello " || events[1].Thinking != "checking" {
		t.Fatalf("deltas=%#v", events[:2])
	}
	if events[2].Kind != hyprovider.EventToolCall || events[2].ToolCall.ID != "call-1" || events[2].ToolCall.Name != "read_file" || string(events[2].ToolCall.Arguments) != `{"path":"a.go"}` {
		t.Fatalf("tool event=%#v", events[2])
	}
	if events[3].StopReason != hyprovider.StopReasonToolUse || events[3].Usage.TotalTokens != 14 || events[3].Usage.CachedInputTokens != 6 {
		t.Fatalf("done=%#v", events[3])
	}
	if !body.closed {
		t.Fatal("response body was not closed on completion")
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal recv error=%v", err)
	}
}

func TestStreamRejectsIncompleteToolJSONAndTruncatedFrames(t *testing.T) {
	invalid := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item\",\"call_id\":\"call\",\"name\":\"tool\",\"arguments\":\"{bad\"}}\n\n"
	body := &chunkBody{chunks: [][]byte{[]byte(invalid)}}
	ctx, cancel := context.WithCancel(context.Background())
	event, err := NewStream(ctx, cancel, body).Recv()
	if err != nil || event.Kind != hyprovider.EventError {
		t.Fatalf("invalid event=%#v error=%v", event, err)
	}

	truncated := &chunkBody{chunks: [][]byte{[]byte(`data: {"type":"response.output_text.delta","delta":"partial"}`)}}
	ctx2, cancel2 := context.WithCancel(context.Background())
	event, err = NewStream(ctx2, cancel2, truncated).Recv()
	if err != nil || event.Kind != hyprovider.EventError {
		t.Fatalf("truncated event=%#v error=%v", event, err)
	}
}

func TestStreamMapsProviderFailure(t *testing.T) {
	payload := "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"context_length_exceeded\",\"message\":\"too long\"}}}\n\n"
	body := &chunkBody{chunks: [][]byte{[]byte(payload)}}
	ctx, cancel := context.WithCancel(context.Background())
	event, err := NewStream(ctx, cancel, body).Recv()
	if err != nil {
		t.Fatal(err)
	}
	var apiError *APIError
	if event.Kind != hyprovider.EventError || !errors.As(event.Err, &apiError) || apiError.Kind != ErrorContextLimit {
		t.Fatalf("event=%#v error=%v", event, event.Err)
	}
}

func TestStreamMapsTopLevelProviderErrorDetails(t *testing.T) {
	payload := "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"upstream connection reset\"}\n\n"
	body := &chunkBody{chunks: [][]byte{[]byte(payload)}}
	ctx, cancel := context.WithCancel(context.Background())
	event, err := NewStream(ctx, cancel, body).Recv()
	if err != nil {
		t.Fatal(err)
	}
	var apiError *APIError
	if event.Kind != hyprovider.EventError || !errors.As(event.Err, &apiError) {
		t.Fatalf("event=%#v error=%v", event, event.Err)
	}
	if apiError.Kind != ErrorServer || apiError.Code != "server_error" || apiError.Message != "upstream connection reset" {
		t.Fatalf("top-level provider error=%+v", apiError)
	}
	if got := apiError.Error(); !strings.Contains(got, "upstream connection reset") {
		t.Fatalf("top-level provider diagnostic was lost: %q", got)
	}
}
