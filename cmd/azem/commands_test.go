package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/operator"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestOperatorRegistryContainsExecutableProtocolSessionAndAuthCommands(t *testing.T) {
	registry, err := buildOperatorRegistry(func([]string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]bool)
	for _, command := range registry.Commands() {
		registered[filepath.Join(command.Path...)] = command.Handler != nil
	}
	for _, path := range []string{
		"help", "version", "completion", "setup", "update", "gc", "usage", "bench", "webhook/serve", "rpc", "rpc-ui", "acp",
		"session/list", "session/export", "session/import", "session/share", "session/fork", "session/tree", "session/label",
		"auth-broker/serve", "auth-broker/token", "auth-broker/status", "auth-gateway/serve", "auth-gateway/token", "auth-gateway/status",
	} {
		if !registered[path] {
			t.Fatalf("command %q is missing or has no handler: %#v", path, registered)
		}
	}
}

func TestSessionListExportAndTokenCommandsRunThroughRegistry(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("AZEM_HOME", home)
	store, err := sqlitestore.Open(ctx, filepath.Join(home, "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Command session"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "session", session.Block{Kind: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	store.Close(ctx)
	registry, err := buildOperatorRegistry(func([]string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	streams := operator.IO{In: &bytes.Buffer{}, Out: &output, Err: &diagnostics}
	matched, err := registry.Run(ctx, []string{"session", "list", "--json"}, streams)
	if err != nil || !matched {
		t.Fatalf("list matched=%v error=%v diagnostics=%s", matched, err, diagnostics.String())
	}
	var listed []session.Session
	if err := json.Unmarshal(output.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].ID != "session" {
		t.Fatalf("listed=%#v error=%v output=%s", listed, err, output.String())
	}
	output.Reset()
	exportPath := filepath.Join(t.TempDir(), "session.json")
	matched, err = registry.Run(ctx, []string{"session", "export", "--session", "session", "--output", exportPath, "--format", "json"}, streams)
	if err != nil || !matched {
		t.Fatalf("export matched=%v error=%v", matched, err)
	}
	if payload, err := os.ReadFile(exportPath); err != nil || !bytes.Contains(payload, []byte("Command session")) {
		t.Fatalf("export payload=%q error=%v", payload, err)
	}
	output.Reset()
	tokenPath := filepath.Join(t.TempDir(), "broker.token")
	matched, err = registry.Run(ctx, []string{"auth-broker", "token", "--path", tokenPath, "--json"}, streams)
	if err != nil || !matched {
		t.Fatalf("token matched=%v error=%v", matched, err)
	}
	var token map[string]string
	if err := json.Unmarshal(output.Bytes(), &token); err != nil || len(token["token"]) < 32 || token["path"] != tokenPath {
		t.Fatalf("token=%#v error=%v", token, err)
	}
	if info, err := os.Stat(tokenPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode=%v error=%v", info.Mode(), err)
	}
	output.Reset()
	matched, err = registry.Run(ctx, []string{"completion", "bash"}, streams)
	if err != nil || !matched || !strings.Contains(output.String(), "auth-broker") || !strings.Contains(output.String(), "session") {
		t.Fatalf("completion matched=%v error=%v output=%s", matched, err, output.String())
	}
	output.Reset()
	matched, err = registry.Run(ctx, []string{"gc", "--json"}, streams)
	if err != nil || !matched || !strings.Contains(output.String(), `"apply":false`) {
		t.Fatalf("gc matched=%v error=%v output=%s", matched, err, output.String())
	}
	output.Reset()
	matched, err = registry.Run(ctx, []string{"usage", "--scope", "all", "--json"}, streams)
	if err != nil || !matched || !strings.Contains(output.String(), `"scope": "all"`) {
		t.Fatalf("usage matched=%v error=%v output=%s", matched, err, output.String())
	}
}

func TestBenchCommandRunsGatewayWorkflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\\n\\n"))
		_, _ = writer.Write([]byte("data: [DONE]\\n\\n"))
	}))
	defer server.Close()
	registry, err := buildOperatorRegistry(func([]string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	matched, err := registry.Run(context.Background(), []string{"bench", "--gateway", server.URL, "--runs", "1", "--parallel", "1", "--json", "openai/test-model"}, operator.IO{In: &bytes.Buffer{}, Out: &output, Err: &bytes.Buffer{}})
	if err != nil || !matched || !strings.Contains(output.String(), `"model": "test-model"`) {
		t.Fatalf("bench matched=%v error=%v output=%s", matched, err, output.String())
	}
}
