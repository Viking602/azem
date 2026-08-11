package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/venat/transport/mcpcontract"

	"github.com/Viking602/azem/internal/config"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
)

func TestMCPSettingsActionsPersistAndReconfigureLiveManager(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"demo": {Enabled: false, Transport: "stdio", Command: "demo", ConnectTimeout: "1s", CallTimeout: "1s", MaxConcurrency: 1, Approval: "always"},
	}
	manager := mcpruntime.NewManager(cfg.MCP.Servers, "test", nil, mcpruntime.Options{
		Dial: func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpcontract.Client, error) {
			return &appFakeMCPClient{}, nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	service := NewService(ctx, cfg)
	service.SetConfigPath(path)
	service.AttachAgentExtensions(manager, nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
	})

	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetMCPEnabled, Target: "demo", Decision: "true"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for manager.Servers()[0].State != mcpruntime.StateReady && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if snapshot := manager.Servers()[0]; snapshot.State != mcpruntime.StateReady || snapshot.ToolCount != 1 {
		t.Fatalf("enabled MCP snapshot = %#v", snapshot)
	}

	payload, err := json.Marshal(mcpServerMutation{
		Name: "docs", Enabled: false, Transport: "streamable_http", URL: "https://example.com/mcp", Approval: "never",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionUpsertMCPServer, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	persisted, err := config.Load(path, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.MCP.Servers["demo"].Enabled || persisted.MCP.Servers["docs"].Enabled || persisted.MCP.Servers["docs"].URL != "https://example.com/mcp" {
		t.Fatalf("persisted MCP configuration = %#v", persisted.MCP.Servers)
	}
}
