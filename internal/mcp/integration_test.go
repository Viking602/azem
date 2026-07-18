package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/tool"
	"github.com/Viking602/go-hydaelyn/worker"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

const (
	mcpHelperMode   = "AZEM_MCP_HELPER_MODE"
	mcpHelperToken  = "AZEM_MCP_HELPER_TOKEN"
	mcpHelperClosed = "AZEM_MCP_HELPER_CLOSED"
	mcpParentSecret = "AZEM_MCP_PARENT_SECRET"
)

func TestMain(m *testing.M) {
	if os.Getenv(mcpHelperMode) == "stdio" {
		runStdioHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestManagerCallsStdioMCPWithoutInheritingParentEnvironment(t *testing.T) {
	t.Setenv(mcpParentSecret, "must-not-leak")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	closedFile := filepath.Join(t.TempDir(), "closed")
	values := map[string]string{
		"env:HELPER_MODE":  "stdio",
		"env:HELPER_TOKEN": "configured",
		"env:CLOSED_FILE":  closedFile,
	}
	manager := NewManager(map[string]config.MCPServerConfig{
		"stdio": {
			Enabled: true, Transport: "stdio", Command: executable, InheritEnv: false,
			Env:            map[string]string{mcpHelperMode: "env:HELPER_MODE", mcpHelperToken: "env:HELPER_TOKEN", mcpHelperClosed: "env:CLOSED_FILE"},
			ConnectTimeout: "5s", CallTimeout: "5s", MaxConcurrency: 1,
			ToolOverrides: map[string]config.ToolOverride{"environment": {Effect: "read_only", Approval: "never"}},
		},
	}, "test", func(_ context.Context, reference string) (string, error) { return values[reference], nil }, Options{})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	drivers := manager.Snapshot()
	if len(drivers) != 1 || drivers[0].Definition().Name != "mcp__stdio__environment" {
		t.Fatalf("drivers=%v", definitionNames(drivers))
	}
	result, err := drivers[0].Execute(context.Background(), tool.Call{ID: "stdio-call", Name: drivers[0].Definition().Name}, nil)
	if err != nil || result.Content != "configured|" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(closedFile); err != nil {
		t.Fatalf("stdio child was not reaped before close returned: %v", err)
	}
}

func TestManagerCallsStreamableHTTPWithResolvedHeader(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "http-server", Version: "1"}, nil)
	server.AddTool(&sdkmcp.Tool{Name: "status", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "http-ok"}}}, nil
	})
	var authenticated atomic.Bool
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, &sdkmcp.StreamableHTTPOptions{JSONResponse: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") == "Bearer resolved" {
			authenticated.Store(true)
		}
		handler.ServeHTTP(writer, request)
	}))
	defer httpServer.Close()

	manager := NewManager(map[string]config.MCPServerConfig{
		"remote": {
			Enabled: true, Transport: "streamable_http", URL: httpServer.URL,
			Headers:        map[string]string{"Authorization": "env:MCP_BEARER"},
			ConnectTimeout: "5s", CallTimeout: "5s", MaxConcurrency: 1,
			ToolOverrides: map[string]config.ToolOverride{"status": {Effect: "read_only", Approval: "never"}},
		},
	}, "test", func(context.Context, string) (string, error) { return "Bearer resolved", nil }, Options{})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()
	drivers := manager.Snapshot()
	if len(drivers) != 1 {
		t.Fatalf("drivers=%v", definitionNames(drivers))
	}
	result, err := drivers[0].Execute(context.Background(), tool.Call{ID: "http-call", Name: drivers[0].Definition().Name}, nil)
	if err != nil || result.Content != "http-ok" || !authenticated.Load() {
		t.Fatalf("result=%#v authenticated=%v error=%v", result, authenticated.Load(), err)
	}
}

func TestMCPDefaultDriverRequiresRunnerApprovalBeforeRemoteCall(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{tools: []tool.Definition{{Name: "deploy", InputSchema: tool.Schema{Type: "object"}}}}
	manager := managerWithClient(client)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()

	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "governance.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(ctx) }()
	run, err := service.StartRun(ctx, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	driver := manager.Snapshot()[0]
	governed := worker.GovernedToolBus{
		Runner: service.Runner(), Bus: tool.NewBus(driver), RunID: run.RunID, TaskID: run.TaskID,
		LeaseID: run.LeaseID, HolderType: api.HolderAgent, HolderID: run.HolderID, TaskVersion: run.TaskVersion,
	}
	_, err = governed.Execute(ctx, tool.Call{ID: "deploy-call", Name: driver.Definition().Name}, nil)
	if !errors.Is(err, hydaelyn.ErrPolicyDenied) {
		t.Fatalf("governed MCP error=%v", err)
	}
	if client.callCount != 0 {
		t.Fatalf("remote call bypassed approval: calls=%d", client.callCount)
	}
}

func runStdioHelper() {
	defer func() {
		if path := os.Getenv(mcpHelperClosed); path != "" {
			_ = os.WriteFile(path, []byte("closed"), 0o600)
		}
	}()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "stdio-server", Version: "1"}, nil)
	server.AddTool(&sdkmcp.Tool{Name: "environment", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		text := os.Getenv(mcpHelperToken) + "|" + os.Getenv(mcpParentSecret)
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}}}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = server.Run(ctx, &sdkmcp.StdioTransport{})
}
