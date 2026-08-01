package xai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"resty.dev/v3"

	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/provider/responses"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func restyStreamResponse(body string) *resty.Response {
	raw := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	return &resty.Response{RawResponse: raw, Body: raw.Body}
}

type retryTransport struct{ requests int }

func (t *retryTransport) Name() string { return "retry-test" }

func (t *retryTransport) Post(context.Context, []byte) (*resty.Response, error) {
	t.requests++
	body := `data: {"type":"error","code":"server_is_overloaded","message":"server overloaded; request ID req_server_456"}` + "\n\n"
	if t.requests == 3 {
		body = `data: {"type":"response.completed","response":{"status":"completed"}}` + "\n\n"
	}
	return restyStreamResponse(body), nil
}

func TestDriverReportsRateLimitRetriesThroughGenericObserver(t *testing.T) {
	transport := &retryTransport{}
	driver, err := New(transport, []string{"grok-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	driver.retryDelay = func(int) time.Duration { return 0 }
	var progress []hyprovider.RetryProgress
	driver.SetRetryObserver(func(retry hyprovider.RetryProgress) error {
		progress = append(progress, retry)
		return nil
	})
	stream, err := driver.Stream(context.Background(), hyprovider.Request{
		Model: "grok-test", Messages: []message.Message{message.NewText(message.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventDone {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	if transport.requests != 3 || len(progress) != 2 {
		t.Fatalf("requests=%d progress=%d, want 3 requests and 2 retry events", transport.requests, len(progress))
	}
	for index, retry := range progress {
		if retry.Attempt != index+1 || retry.Max != hyprovider.DefaultMaxStreamRetries || retry.Cause == nil {
			t.Fatalf("retry %d=%#v", index+1, retry)
		}
		if !strings.Contains(retry.Cause.Error(), "request ID req_server_456") {
			t.Fatalf("retry %d lost request ID: %v", index+1, retry.Cause)
		}
	}
}

func TestDriverNormalizesAutomaticCacheUsage(t *testing.T) {
	transport := &cacheUsageTransport{body: `data: {"type":"response.completed","response":{"id":"response-1","status":"completed","usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24,"input_tokens_details":{"cached_tokens":12,"cache_write_tokens":8},"output_tokens_details":{"reasoning_tokens":2}}}}` + "\n\n"}
	driver, err := New(transport, []string{"grok-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var details responses.UsageDetails
	stream, err := driver.Stream(context.Background(), hyprovider.Request{
		Model: "grok-test", Messages: []message.Message{message.NewText(message.RoleUser, "hello")},
		ExtraBody: map[string]any{responses.UsageReporterExtraKey: responses.UsageReporter(func(d responses.UsageDetails) { details = d })},
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventDone {
		t.Fatalf("event=%#v err=%v", event, err)
	}
	if details.CacheModel != responses.CacheModelAutomatic || details.CacheWriteTokens != 0 || details.CachedTokens != 12 || !details.CacheReported {
		t.Fatalf("xAI automatic cache details=%+v", details)
	}
	if event.Usage.CachedInputTokens != 12 {
		t.Fatalf("stream cached tokens=%d", event.Usage.CachedInputTokens)
	}
}

type cacheUsageTransport struct{ body string }

func (t *cacheUsageTransport) Name() string { return "cache-usage-test" }

func (t *cacheUsageTransport) Post(context.Context, []byte) (*resty.Response, error) {
	return restyStreamResponse(t.body), nil
}

func TestStandardTransportUsesOnlyXAIHeaders(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		if request.Header.Get("Authorization") != "Bearer access" {
			t.Errorf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("originator") != "" || request.Header.Get("OpenAI-Beta") != "" {
			t.Errorf("Codex headers leaked: %v", request.Header)
		}
		var payload struct {
			PromptCacheKey string   `json:"prompt_cache_key"`
			Include        []string `json:"include"`
			Store          bool     `json:"store"`
			Input          []any    `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode xAI request: %v", err)
		}
		if payload.Store || payload.PromptCacheKey != "session-1" || len(payload.Include) != 1 || payload.Include[0] != "reasoning.encrypted_content" {
			t.Errorf("xAI cache request=%+v", payload)
		}
		if requestCount == 2 {
			encoded, _ := json.Marshal(payload.Input)
			wire := string(encoded)
			reasoning := strings.Index(wire, `"encrypted_content":"opaque"`)
			messageItem := strings.Index(wire, `"id":"msg_1"`)
			latestUser := strings.LastIndex(wire, "next")
			if reasoning < 0 || messageItem < reasoning || latestUser < messageItem {
				t.Errorf("provider state replay order=%s", wire)
			}
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		if requestCount == 1 {
			_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response\",\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"id\":\"rs_1\",\"encrypted_content\":\"opaque\"},{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n"))
		} else {
			_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-2\",\"status\":\"completed\"}}\n\n"))
		}
	}))
	defer server.Close()
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	secrets, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Put(ctx, auth.Credential{Provider: "grok", AccountID: "acct", AccessToken: "access"}); err != nil {
		t.Fatal(err)
	}
	grokClient := grok.NewClient()
	grokClient.AllowInsecure = true
	authentication := auth.NewService(provider.DB(), secrets, nil, grokClient)
	driver, err := New(&StandardTransport{Auth: authentication, AccountID: "acct", Endpoint: server.URL}, []string{"grok-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := driver.Stream(ctx, hyprovider.Request{
		Model: "grok-test", Messages: []message.Message{message.NewText(message.RoleUser, "hello")},
		ExtraBody: map[string]any{"prompt_cache_key": "session-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != hyprovider.EventDone {
		t.Fatalf("event=%#v", event)
	}
	assistant := message.NewText(message.RoleAssistant, "done")
	assistant.ProviderState = event.ProviderState
	second, err := driver.Stream(ctx, hyprovider.Request{
		Model: "grok-test", Messages: []message.Message{message.NewText(message.RoleUser, "hello"), assistant, message.NewText(message.RoleUser, "next")},
		ExtraBody: map[string]any{"prompt_cache_key": "session-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event, err := second.Recv(); err != nil || event.Kind != hyprovider.EventDone {
		t.Fatalf("second event=%#v error=%v", event, err)
	}
	if requestCount != 2 {
		t.Fatalf("xAI requests=%d", requestCount)
	}
}

func TestCLIProxyTransportKeepsFingerprintIsolated(t *testing.T) {
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() != "cli-chat-proxy.grok.com" {
			t.Fatalf("host=%q", request.URL.Hostname())
		}
		if request.Header.Get("Authorization") != "Bearer proxy-token" || request.Header.Get("X-Stainless-Lang") != "js" {
			t.Fatalf("proxy headers=%v", request.Header)
		}
		if request.Header.Get("OpenAI-Beta") != "" || request.Header.Get("ChatGPT-Account-ID") != "" {
			t.Fatalf("standard headers leaked=%v", request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response\",\"status\":\"completed\"}}\n\n")), Request: request}, nil
	})})
	defer client.Close()
	transport := &CLIProxyTransport{Token: func(context.Context) (string, error) { return "proxy-token", nil }, Headers: map[string]string{"X-Stainless-Lang": "js", "Authorization": "must-not-win"}, Client: client}
	driver, err := New(transport, []string{"grok-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := driver.Stream(context.Background(), hyprovider.Request{Model: "grok-test", Messages: []message.Message{message.NewText(message.RoleUser, "hello")}})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != hyprovider.EventDone {
		t.Fatalf("event=%#v", event)
	}
}
