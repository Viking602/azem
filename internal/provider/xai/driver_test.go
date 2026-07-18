package xai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/grok"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestStandardTransportUsesOnlyXAIHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" {
			t.Errorf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("originator") != "" || request.Header.Get("OpenAI-Beta") != "" {
			t.Errorf("Codex headers leaked: %v", request.Header)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response\",\"status\":\"completed\"}}\n\n"))
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
	stream, err := driver.Stream(ctx, hyprovider.Request{Model: "grok-test", Messages: []message.Message{message.NewText(message.RoleUser, "hello")}})
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

func TestCLIProxyTransportKeepsFingerprintIsolated(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
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
	})}
	transport := &CLIProxyTransport{Token: func(context.Context) (string, error) { return "proxy-token", nil }, Headers: map[string]string{"X-Stainless-Lang": "js", "Authorization": "must-not-win"}, HTTP: client}
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
