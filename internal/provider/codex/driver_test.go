package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestDriverRetriesConnectionResetFiveTimesThenSucceeds(t *testing.T) {
	var requests atomic.Int32
	var retries []hyprovider.RetryProgress
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) <= maxProviderStreamRetries {
			_, _ = writer.Write([]byte("data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"upstream connection reset\"}\n\n"))
			return
		}
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.retryDelay = func(attempt int) time.Duration { return time.Duration(attempt) * time.Millisecond }
	driver.SetRetryObserver(func(progress hyprovider.RetryProgress) error {
		retries = append(retries, progress)
		return nil
	})
	stream, err := driver.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventDone {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	if requests.Load() != 6 {
		t.Fatalf("requests=%d, want initial request plus five retries", requests.Load())
	}
	if len(retries) != maxProviderStreamRetries {
		t.Fatalf("retry progress events=%d, want %d", len(retries), maxProviderStreamRetries)
	}
	for index, progress := range retries {
		attempt := index + 1
		if progress.Attempt != attempt || progress.Max != maxProviderStreamRetries || progress.Delay != time.Duration(attempt)*time.Millisecond {
			t.Fatalf("retry %d progress=%#v", attempt, progress)
		}
		if progress.Cause == nil || !strings.Contains(progress.Cause.Error(), "connection reset") {
			t.Fatalf("retry %d cause=%v", attempt, progress.Cause)
		}
	}
}

func TestDriverFastModeSendsPriorityServiceTier(t *testing.T) {
	var serviceTier string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		serviceTier, _ = payload["service_tier"].(string)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.SetServiceTier(FastServiceTier)
	stream, err := driver.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if event, err := stream.Recv(); err != nil || event.Kind != hyprovider.EventDone {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	if serviceTier != "priority" {
		t.Fatalf("service_tier = %q", serviceTier)
	}
}

func TestProviderStreamRetryDelayCoversTransientOutage(t *testing.T) {
	want := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		8 * time.Second,
	}
	for index, expected := range want {
		if got := providerStreamRetryDelay(index + 1); got != expected {
			t.Fatalf("retry %d delay = %s, want %s", index+1, got, expected)
		}
	}
}

func TestDriverStopsAfterFiveConnectionResetRetries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"request failed; request ID req_server_123\"}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.retryDelay = func(int) time.Duration { return 0 }
	stream, err := driver.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventError || event.Err == nil {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	if !strings.Contains(event.Err.Error(), "after 5 retries") ||
		!strings.Contains(event.Err.Error(), "request ID req_server_123") {
		t.Fatalf("unexpected final error: %v", event.Err)
	}
	if requests.Load() != 6 {
		t.Fatalf("requests=%d, want initial request plus five retries", requests.Load())
	}
}

func TestDriverRetriesOverloadedRateLimitFiveTimesThenSucceeds(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		request := requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		if request <= 5 {
			_, _ = writer.Write([]byte("data: {\"type\":\"error\",\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}\n\n"))
			return
		}
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.retryDelay = func(int) time.Duration { return 0 }
	var progress []hyprovider.RetryProgress
	driver.SetRetryObserver(func(retry hyprovider.RetryProgress) error {
		progress = append(progress, retry)
		return nil
	})
	stream, err := driver.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventDone {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	if requests.Load() != 6 || len(progress) != 5 {
		t.Fatalf("requests=%d progress=%d, want initial request plus five reconnects", requests.Load(), len(progress))
	}
	for index, retry := range progress {
		if retry.Attempt != index+1 || retry.Max != 5 ||
			!strings.Contains(retry.Cause.Error(), "currently overloaded") {
			t.Fatalf("retry %d = %+v", index+1, retry)
		}
	}
}

func TestDriverStopsAfterFiveOverloadedRateLimitRetries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"error\",\"code\":\"server_is_overloaded\",\"message\":\"server overloaded; request ID req_overload_123\"}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.retryDelay = func(int) time.Duration { return 0 }
	stream, err := driver.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventError || event.Err == nil {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	if !strings.Contains(event.Err.Error(), "after 5 retries") ||
		!strings.Contains(event.Err.Error(), "request ID req_overload_123") {
		t.Fatalf("unexpected final error: %v", event.Err)
	}
	if requests.Load() != 6 {
		t.Fatalf("requests=%d, want initial request plus five reconnects", requests.Load())
	}
}

func TestDriverDoesNotRetryOtherProviderErrors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"error\",\"code\":\"context_length_exceeded\",\"message\":\"context too long\"}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.retryDelay = func(int) time.Duration { return 0 }
	stream, err := driver.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventError {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("non-reset error retried: requests=%d", requests.Load())
	}
}

func TestDriverCancelsConnectionResetBackoff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"upstream connection reset\"}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.retryDelay = func(int) time.Duration { return time.Minute }
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := driver.Stream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	result := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if err != context.Canceled {
			t.Fatalf("error=%v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt retry backoff")
	}
}

func TestDriverDoesNotReplayResetAfterPartialOutput(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))
		_, _ = writer.Write([]byte("data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"upstream connection reset\"}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.retryDelay = func(int) time.Duration { return 0 }
	stream, err := driver.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if event, err := stream.Recv(); err != nil || event.Kind != hyprovider.EventTextDelta {
		t.Fatalf("partial event=%#v error=%v", event, err)
	}
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventError || !strings.Contains(event.Err.Error(), "refusing unsafe replay") {
		t.Fatalf("reset event=%#v error=%v", event, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("partially emitted stream replayed: requests=%d", requests.Load())
	}
}

func testRequest() hyprovider.Request {
	return hyprovider.Request{Model: "gpt-test", Messages: []message.Message{message.NewText(message.RoleUser, "test")}}
}

func TestDriverDoesNotReplayInterruptedToolStream(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer access" || request.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Errorf("auth headers missing")
		}
		if request.Header.Get("originator") != "codex_cli_rs" || request.Header.Get("OpenAI-Beta") == "" {
			t.Errorf("Codex compatibility headers missing")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "gpt-test" || body["stream"] != true {
			t.Errorf("request body=%v", body)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item\",\"call_id\":\"call\",\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"a.go\\\"}\"}}\n\n"))
	}))
	defer server.Close()
	driver := newTestDriver(t, server.URL)
	stream, err := driver.Stream(context.Background(), hyprovider.Request{Model: "gpt-test", Messages: []message.Message{message.NewText(message.RoleUser, "inspect")}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Recv()
	if err != nil || first.Kind != hyprovider.EventToolCall {
		t.Fatalf("first=%#v error=%v", first, err)
	}
	second, err := stream.Recv()
	if err != nil || second.Kind != hyprovider.EventError {
		t.Fatalf("second=%#v error=%v", second, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("interrupted tool stream replayed %d requests", requests.Load())
	}
}

func TestDriverMapsNamesOutsideCodexToolPattern(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Name != "coding_read_file" {
			t.Errorf("mapped tools=%+v", body.Tools)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item\",\"call_id\":\"call\",\"name\":\"coding_read_file\",\"arguments\":\"{\\\"path\\\":\\\"a.go\\\"}\"}}\n\n"))
	}))
	defer server.Close()
	driver := newTestDriver(t, server.URL)
	stream, err := driver.Stream(context.Background(), hyprovider.Request{
		Model: "gpt-test", Messages: []message.Message{message.NewText(message.RoleUser, "inspect")},
		Tools: []message.ToolDefinition{{Name: "coding.read_file", Description: "read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.ToolCall == nil || event.ToolCall.Name != "coding.read_file" {
		t.Fatalf("tool event=%#v", event)
	}
}

func TestDriverMapsHistoricalToolNamesThatAreNoLongerExposed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Input) != 3 || body.Input[1]["name"] != "coding_edit_hashline" {
			t.Errorf("mapped input=%+v", body.Input)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(`data: {"type":"response.completed","response":{"status":"completed","output":[]}}` + "\n\n"))
	}))
	defer server.Close()
	driver := newTestDriver(t, server.URL)
	stream, err := driver.Stream(context.Background(), hyprovider.Request{
		Model: "gpt-test",
		Messages: []message.Message{
			message.NewText(message.RoleUser, "edit"),
			{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "call_1", Name: "coding.edit_hashline", Arguments: json.RawMessage(`{"input":"patch"}`)}}},
			message.NewToolResult(message.ToolResult{ToolCallID: "call_1", Name: "coding.edit_hashline", Content: "rejected"}),
		},
		Tools: []message.ToolDefinition{{Name: "coding.read_file", Description: "read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
}

func TestDriverRoundTripsDistinctFunctionItemID(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			_, _ = writer.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_item\",\"call_id\":\"call_1\",\"name\":\"coding_read_file\",\"arguments\":\"{\\\"path\\\":\\\"a.go\\\"}\"}}\n\n"))
			return
		}
		if len(body.Input) != 3 || body.Input[1]["id"] != "fc_item" || body.Input[1]["call_id"] != "call_1" || body.Input[2]["call_id"] != "call_1" {
			t.Errorf("second request input=%+v", body.Input)
		}
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	tools := []message.ToolDefinition{{Name: "coding.read_file", Description: "read"}}
	first, err := driver.Stream(context.Background(), hyprovider.Request{
		Model: "gpt-test", Messages: []message.Message{message.NewText(message.RoleUser, "inspect")}, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := first.Recv()
	_ = first.Close()
	if err != nil || event.ToolCall == nil || event.ToolCall.ID != "call_1" {
		t.Fatalf("first event=%#v error=%v", event, err)
	}
	second, err := driver.Stream(context.Background(), hyprovider.Request{
		Model: "gpt-test",
		Messages: []message.Message{
			message.NewText(message.RoleUser, "inspect"),
			{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "call_1", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}}},
			message.NewToolResult(message.ToolResult{ToolCallID: "call_1", Name: "coding.read_file", Content: "package a"}),
		},
		Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
	if requests.Load() != 2 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestDriverAlignsPromptCacheBodyHeadersAndPrefersProviderState(t *testing.T) {
	const state = `[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"},{"type":"function_call","id":"provider_item","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"}]`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			PromptCacheKey string            `json:"prompt_cache_key"`
			Input          []json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.PromptCacheKey != "session-cache-key" ||
			request.Header.Get("conversation_id") != body.PromptCacheKey ||
			request.Header.Get("session_id") != body.PromptCacheKey {
			t.Errorf("cache routing body=%q conversation=%q session=%q", body.PromptCacheKey,
				request.Header.Get("conversation_id"), request.Header.Get("session_id"))
		}
		if len(body.Input) != 4 || !strings.Contains(string(body.Input[1]), `"id":"rs_1"`) ||
			!strings.Contains(string(body.Input[2]), `"id":"provider_item"`) ||
			strings.Contains(string(body.Input[2]), "stale_item") {
			t.Errorf("provider state replay input=%s", body.Input)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(`data: {"type":"response.completed","response":{"status":"completed","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	driver := newTestDriver(t, server.URL)
	driver.recordToolItemID("call_1", "stale_item")
	stream, err := driver.Stream(context.Background(), hyprovider.Request{
		Model: "gpt-test",
		Messages: []message.Message{
			message.NewText(message.RoleUser, "inspect"),
			{
				Role: message.RoleAssistant, Text: "normalized",
				ProviderState: json.RawMessage(state),
				ToolCalls: []message.ToolCall{{
					ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"normalized"}`),
				}},
			},
			message.NewToolResult(message.ToolResult{ToolCallID: "call_1", Name: "lookup", Content: "result"}),
		},
		ExtraBody: map[string]any{"prompt_cache_key": " session-cache-key "},
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	_ = stream.Close()
	if err != nil || event.Kind != hyprovider.EventDone {
		t.Fatalf("terminal event=%#v error=%v", event, err)
	}
}

func TestDriverCancellationAbortsSSERead(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()
	driver := newTestDriver(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := driver.Stream(ctx, hyprovider.Request{Model: "gpt-test", Messages: []message.Message{message.NewText(message.RoleUser, "wait")}})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	result := make(chan hyprovider.Event, 1)
	go func() { event, _ := stream.Recv(); result <- event }()
	cancel()
	select {
	case event := <-result:
		if event.Kind != hyprovider.EventDone || event.StopReason != hyprovider.StopReasonAborted {
			t.Fatalf("cancel event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt SSE read")
	}
}

func newTestDriver(t *testing.T, endpoint string) *Driver {
	t.Helper()
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close(context.Background()) })
	secrets, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Put(ctx, auth.Credential{Provider: "chatgpt", AccountID: "acct", AccessToken: "access"}); err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(provider.DB(), secrets, chatgpt.NewClient(), nil)
	driver, err := New(authentication, "acct", endpoint, []string{"gpt-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return driver
}
