package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/venat/message"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type appFakeMCPClient struct {
	calls     atomic.Int32
	lastTool  string
	callErr   error
	resources []mcpruntime.Resource
	content   []mcpruntime.ResourceContent
	prompts   []mcpruntime.Prompt
	messages  []mcpruntime.PromptMessage
}

func (c *appFakeMCPClient) Initialize(context.Context, string, string) (mcpruntime.InitializeResult, error) {
	return mcpruntime.InitializeResult{ProtocolVersion: "2025-06-18"}, nil
}

func (c *appFakeMCPClient) ListTools(context.Context) ([]message.ToolDefinition, error) {
	return []message.ToolDefinition{{Name: "status", Description: "return status", InputSchema: message.JSONSchema{Type: "object"}}}, nil
}

func (c *appFakeMCPClient) CallTool(_ context.Context, name string, _ map[string]any) (mcpruntime.CallToolResult, error) {
	c.calls.Add(1)
	c.lastTool = name
	if c.callErr != nil {
		return mcpruntime.CallToolResult{}, c.callErr
	}
	return mcpruntime.CallToolResult{Content: []mcpruntime.ContentBlock{{Type: "text", Text: "remote ok"}}}, nil
}

func (c *appFakeMCPClient) ListResources(context.Context) ([]mcpruntime.Resource, error) {
	return append([]mcpruntime.Resource(nil), c.resources...), nil
}

func (c *appFakeMCPClient) ReadResource(context.Context, string) ([]mcpruntime.ResourceContent, error) {
	return append([]mcpruntime.ResourceContent(nil), c.content...), nil
}

func (c *appFakeMCPClient) ListPrompts(context.Context) ([]mcpruntime.Prompt, error) {
	return append([]mcpruntime.Prompt(nil), c.prompts...), nil
}

func (c *appFakeMCPClient) GetPrompt(context.Context, string, map[string]string) ([]mcpruntime.PromptMessage, error) {
	return append([]mcpruntime.PromptMessage(nil), c.messages...), nil
}
func (c *appFakeMCPClient) Close() error { return nil }

func TestRefreshMCPActionWithoutTargetRefreshesConnectedServers(t *testing.T) {
	ctx := context.Background()
	client := &appFakeMCPClient{}
	manager := mcpruntime.NewManager(map[string]config.MCPServerConfig{
		"demo": {Enabled: true, Transport: "stdio", Command: "fake", ConnectTimeout: "1s", CallTimeout: "1s", MaxConcurrency: 1},
	}, "test", nil, mcpruntime.Options{
		Dial: func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpruntime.Client, error) {
			return client, nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()

	service := NewService(ctx, config.Default())
	service.AttachAgentExtensions(manager, nil)
	if err := service.ExecuteAction(ctx, Action{Kind: ActionRefreshMCP}); err != nil {
		t.Fatalf("empty-target MCP refresh error = %v", err)
	}
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventMCPState || event.State != "snapshot" {
		t.Fatalf("MCP refresh event = %#v", event)
	}
}

func TestMCPResourcesAndPromptsReachResourceRouterAndActions(t *testing.T) {
	ctx := context.Background()
	client := &appFakeMCPClient{
		resources: []mcpruntime.Resource{{URI: "file:///guide.md", Name: "Guide", MimeType: "text/markdown"}},
		content:   []mcpruntime.ResourceContent{{URI: "file:///guide.md", MimeType: "text/markdown", Text: "# Guide"}},
		prompts:   []mcpruntime.Prompt{{Name: "summarize", Description: "Summarize"}},
		messages:  []mcpruntime.PromptMessage{{Role: "user", Content: mcpruntime.ContentBlock{Type: "text", Text: "Summarize this"}}},
	}
	manager := mcpruntime.NewManager(map[string]config.MCPServerConfig{
		"demo": {Enabled: true, Transport: "stdio", Command: "fake", ConnectTimeout: "1s", CallTimeout: "1s", MaxConcurrency: 1},
	}, "test", nil, mcpruntime.Options{Dial: func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpruntime.Client, error) {
		return client, nil
	}})
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	router, err := buildResourceRouter(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Register("mcp", mcpResourceHandler{manager: manager}); err != nil {
		t.Fatal(err)
	}
	resourceResult, err := router.Read(ctx, "mcp://demo/file:///guide.md", "", resource.Scope{})
	if err != nil || string(resourceResult.Data) != "# Guide" || resourceResult.MediaType != "text/markdown" {
		t.Fatalf("MCP resource = %#v, %v", resourceResult, err)
	}
	service := NewService(ctx, config.Default())
	service.AttachAgentExtensions(manager, nil)
	payload, _ := json.Marshal(map[string]any{"server": "demo", "name": "summarize"})
	if err := service.ExecuteAction(ctx, Action{Kind: ActionGetMCPPrompt, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(ctx)
	if err != nil || event.State != "prompt" || !strings.Contains(event.Data["messages"], "Summarize this") {
		t.Fatalf("prompt event = %#v, %v", event, err)
	}
}

func TestConfiguredTurnSnapshotsAndGovernsMCPTool(t *testing.T) {
	testConfiguredTurnMCP(t, nil, "Remote status checked.", "remote ok")
}

func TestConfiguredTurnSurvivesMCPTransportRejection(t *testing.T) {
	testConfiguredTurnMCP(t, &jsonrpc.Error{Code: -32005, Message: "rejected by transport"}, "Remote status unavailable; the run continued.", "jsonrpc error -32005: rejected by transport")
}

func testConfiguredTurnMCP(t *testing.T, callErr error, expectedAnswer, expectedToolResult string) {
	t.Helper()
	var responseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" || request.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Errorf("provider auth headers missing")
		}
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-mcp","title":"GPT MCP","context_window":128000,"supports_tools":true}]}`))
		case "/responses":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read provider request: %v", err)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			if responseCalls.Add(1) == 1 {
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"mcp-1\",\"name\":\"mcp__demo__status\",\"arguments\":\"{}\"}}\n\n")
			} else {
				if !strings.Contains(string(body), expectedToolResult) {
					t.Errorf("follow-up provider request omitted MCP tool result %q: %s", expectedToolResult, body)
				}
				_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", expectedAnswer)
			}
			_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-%d\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n", responseCalls.Load())
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	importPath := filepath.Join(t.TempDir(), "codex.json")
	if err := os.WriteFile(importPath, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authentication.ImportChatGPT(ctx, importPath); err != nil {
		t.Fatal(err)
	}
	modelCatalog := catalog.NewService(store.DB(), authentication)
	modelCatalog.Endpoints["chatgpt"] = server.URL + "/models"
	modelCatalog.AdditionalEndpoints["chatgpt"] = nil
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	providerRuntime, err := NewProviderRuntime(cfg, authentication, modelCatalog, coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	providerRuntime.ChatGPTEndpoint = server.URL + "/responses"
	client := &appFakeMCPClient{callErr: callErr}
	manager := mcpruntime.NewManager(map[string]config.MCPServerConfig{
		"demo": {Enabled: true, Transport: "stdio", Command: "fake", ConnectTimeout: "1s", CallTimeout: "1s", MaxConcurrency: 1, Approval: "always"},
	}, "test", nil, mcpruntime.Options{
		Dial: func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpruntime.Client, error) {
			return client, nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "default", Title: "Test", ProviderID: "chatgpt", ModelID: "gpt-mcp", Reasoning: "minimal", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, cfg)
	service.AttachDurable(sessions, coding)
	service.AttachAuth(authentication, modelCatalog)
	service.AttachAgentExtensions(manager, nil)
	if err := service.emitMCPSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	snapshotEvent, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []struct {
		Name  string `json:"name"`
		Tools []struct {
			Name             string `json:"name"`
			Description      string `json:"description"`
			Effect           string `json:"effect"`
			RequiresApproval bool   `json:"requiresApproval"`
		} `json:"tools"`
	}
	if snapshotEvent.Kind != EventMCPState || json.Unmarshal([]byte(snapshotEvent.Data["servers"]), &snapshots) != nil {
		t.Fatalf("MCP snapshot event = %#v", snapshotEvent)
	}
	if len(snapshots) != 1 || snapshots[0].Name != "demo" || len(snapshots[0].Tools) != 1 {
		t.Fatalf("MCP snapshot = %#v", snapshots)
	}
	toolSnapshot := snapshots[0].Tools[0]
	if toolSnapshot.Name != "status" || toolSnapshot.Description != "return status" || toolSnapshot.Effect != "external_side_effect" || !toolSnapshot.RequiresApproval {
		t.Fatalf("MCP tool snapshot = %#v", toolSnapshot)
	}
	service.AttachProviderRuntime(providerRuntime)

	runID, err := service.StartConfiguredTurn(TurnRequest{SessionID: "default", Prompt: "check remote status", Provider: "chatgpt", Model: "gpt-mcp", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	approved := false
	var answer string
	deadline, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(deadline)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventApprovalRequested:
			if event.Data["tool"] != "mcp__demo__status" {
				t.Fatalf("approval event=%+v", event)
			}
			if err := service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
				t.Fatal(err)
			}
			approved = true
		case EventTextDelta:
			if event.TextPhase != "commentary" {
				answer += event.Text
			}
		case EventRunFailed:
			t.Fatalf("run failed: %s", event.Text)
		case EventRunFinished:
			goto finished
		}
	}

finished:
	if !approved || answer != expectedAnswer || client.calls.Load() != 1 || client.lastTool != "status" {
		t.Fatalf("MCP approved=%v answer=%q calls=%d tool=%q", approved, answer, client.calls.Load(), client.lastTool)
	}
	if servers := manager.Servers(); len(servers) != 1 || servers[0].State != mcpruntime.StateReady {
		t.Fatalf("MCP call broke reusable connection: %#v", servers)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}
