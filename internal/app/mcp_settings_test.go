package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Viking602/venat/transport/mcpcontract"

	"github.com/Viking602/azem/internal/config"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/plugins"
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

func TestMCPSettingsDeletesPersistedServerFromRuntime(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "config.yaml")
	contents := "version: 1\nmcp:\n  servers:\n    docs:\n      enabled: false\n      transport: streamable_http\n      url: https://example.com/mcp\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, workspace)
	if err != nil {
		t.Fatal(err)
	}
	manager := mcpruntime.NewManager(cfg.MCP.Servers, "test", nil, mcpruntime.Options{})
	service := NewService(ctx, cfg)
	service.SetConfigPath(path)
	service.AttachAgentExtensions(manager, nil)
	t.Cleanup(func() { _ = manager.Close() })

	if err := service.ExecuteAction(ctx, Action{Kind: ActionDeleteMCPServer, Target: "docs"}); err != nil {
		t.Fatal(err)
	}
	persisted, err := config.Load(path, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := persisted.MCP.Servers["docs"]; ok {
		t.Fatalf("deleted MCP server persisted: %#v", persisted.MCP.Servers)
	}
	for _, snapshot := range manager.Servers() {
		if snapshot.Name == "docs" {
			t.Fatalf("deleted MCP server remained in runtime: %#v", manager.Servers())
		}
	}
}

func TestMCPSettingsDeletesManagedServerAndSuppressesRestart(t *testing.T) {
	cfg := config.Default()
	manager := mcpruntime.NewManager(cfg.MCP.Servers, "test", nil, mcpruntime.Options{})
	service := NewService(context.Background(), cfg)
	service.AttachAgentExtensions(manager, nil)
	defer func() { _ = manager.Close() }()
	if err := service.ExecuteAction(context.Background(), Action{Kind: ActionDeleteMCPServer, Target: "grep"}); err != nil {
		t.Fatal(err)
	}
	if _, exists := service.cfg.MCP.Servers["grep"]; exists || !slices.Contains(service.cfg.MCP.RemovedServers, "grep") || len(manager.Servers()) != 0 {
		t.Fatalf("managed MCP deletion state = %#v runtime=%#v", service.cfg.MCP, manager.Servers())
	}
}

func TestMergePluginsRestoresManagedOwnershipForPersistedOverride(t *testing.T) {
	assembly := bootstrapAssembly{
		cfg: config.Config{MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"review-tools": {Enabled: false, Transport: "stdio", Command: "review"},
		}}},
		pluginCatalog: plugins.Integration{MCPServers: map[string]config.MCPServerConfig{
			"review-tools": {Enabled: true, Transport: "stdio", Command: "review", Managed: true},
		}},
	}
	assembly.mergePlugins()
	server := assembly.cfg.MCP.Servers["review-tools"]
	if !server.Managed || server.Enabled {
		t.Fatalf("persisted plugin MCP ownership/override = %#v", server)
	}
}

func TestBootstrapRemovesLegacyCodexComputerUseReference(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\nmcp:\n  servers:\n    computer-use-computer-use:\n      enabled: false\n      transport: stdio\n      command: /Users/test/.codex/.tmp/plugins/computer-use/bin/computer-use-client-launcher\n      cwd: /Users/test/.codex/.tmp/plugins/computer-use\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	assembly := bootstrapAssembly{cfg: cfg, paths: config.Paths{ConfigFile: path}}
	if err := assembly.removeLegacyCodexComputerUseMCP(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := reloaded.MCP.Servers["computer-use-computer-use"]; exists || !slices.Contains(reloaded.MCP.RemovedServers, "computer-use-computer-use") {
		t.Fatalf("legacy computer-use MCP survived cleanup: %#v", reloaded.MCP)
	}
}
