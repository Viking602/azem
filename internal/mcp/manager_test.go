package mcp

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/tool"
	"github.com/Viking602/go-hydaelyn/transport/mcpcontract"

	"github.com/Viking602/azem/internal/config"
)

func TestManagerNamespacesIsolatesAndGovernsTools(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{tools: []message.ToolDefinition{
		{Name: "read.file", Description: "read", InputSchema: message.JSONSchema{Type: "object"}},
		{Name: "read@file", Description: "collision", InputSchema: message.JSONSchema{Type: "object"}},
		{Name: "bad", InputSchema: message.JSONSchema{Type: "array"}},
		{Name: "safe", InputSchema: message.JSONSchema{Type: "object"}},
	}}
	var gotEnv map[string]string
	var gotHeaders http.Header
	events := make([]Event, 0)
	manager := NewManager(map[string]config.MCPServerConfig{
		"local": {
			Enabled: true, Transport: "stdio", Command: "fake", ConnectTimeout: "1s", CallTimeout: "1s", MaxConcurrency: 1,
			Approval: "always", Env: map[string]string{"TOKEN": "env:TOKEN"}, Headers: map[string]string{"Authorization": "keyring:MCP"},
			ToolOverrides: map[string]config.ToolOverride{"safe": {Effect: "read_only", Approval: "never"}},
		},
	}, "test-version", func(_ context.Context, reference string) (string, error) {
		return "resolved:" + reference, nil
	}, Options{
		Dial: func(_ context.Context, _ string, _ config.MCPServerConfig, environment map[string]string, headers http.Header) (mcpcontract.Client, error) {
			gotEnv, gotHeaders = environment, headers
			return client, nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
		Sink:  func(event Event) { events = append(events, event) },
	})
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if client.initializedName != "azem" || client.initializedVersion != "test-version" {
		t.Fatalf("initialize = %q %q", client.initializedName, client.initializedVersion)
	}
	if gotEnv["TOKEN"] != "resolved:env:TOKEN" || gotHeaders.Get("Authorization") != "resolved:keyring:MCP" {
		t.Fatalf("environment=%v headers=%v", gotEnv, gotHeaders)
	}
	drivers := manager.Snapshot()
	if len(drivers) != 2 {
		t.Fatalf("drivers=%v", definitionNames(drivers))
	}
	definitions := map[string]tool.Definition{}
	for _, driver := range drivers {
		definitions[driver.Definition().Name] = driver.Definition()
	}
	remote := definitions["mcp__local__read_file"]
	if remote.EffectType != tool.EffectExternalSideEffect || !remote.RequiresApproval || !remote.RequiresActionTask {
		t.Fatalf("default MCP governance=%#v", remote)
	}
	safe := definitions["mcp__local__safe"]
	if safe.EffectType != tool.EffectReadOnly || safe.RequiresApproval || safe.RequiresActionTask {
		t.Fatalf("safe override=%#v", safe)
	}
	servers := manager.Servers()
	if len(servers) != 1 || servers[0].State != StateReady || len(servers[0].Diagnostics) != 2 {
		t.Fatalf("servers=%#v", servers)
	}
	if len(events) < 2 || events[0].State != StateConnecting || events[len(events)-1].State != StateReady {
		t.Fatalf("events=%#v", events)
	}

	result, err := driverWithName(t, drivers, "mcp__local__read_file").Execute(ctx, tool.Call{ID: "call", Name: "mcp__local__read_file"}, nil)
	if err != nil || result.Name != "mcp__local__read_file" || result.Content != "ok" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if client.lastTool != "read.file" {
		t.Fatalf("remote tool name=%q", client.lastTool)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if !client.closed || manager.Servers()[0].State != StateStopped {
		t.Fatalf("client closed=%v servers=%#v", client.closed, manager.Servers())
	}
}

func TestManagerRefreshUsesImmutableTurnSnapshot(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{tools: []message.ToolDefinition{{Name: "first", InputSchema: message.JSONSchema{Type: "object"}}}}
	manager := managerWithClient(client)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	turn := manager.Snapshot()
	client.setTools([]message.ToolDefinition{{Name: "second", InputSchema: message.JSONSchema{Type: "object"}}})
	if err := manager.Refresh(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if got := definitionNames(turn); !reflect.DeepEqual(got, []string{"mcp__local__first"}) {
		t.Fatalf("existing turn catalog mutated: %v", got)
	}
	if got := definitionNames(manager.Snapshot()); !reflect.DeepEqual(got, []string{"mcp__local__second"}) {
		t.Fatalf("refreshed catalog=%v", got)
	}
}

func TestManagerRetriesConnectionWithBoundedBackoff(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{}
	attempts := 0
	delays := make([]time.Duration, 0)
	manager := NewManager(map[string]config.MCPServerConfig{
		"local": {Enabled: true, Transport: "stdio", Command: "fake", ConnectTimeout: "1s", CallTimeout: "1s", MaxConcurrency: 1},
	}, "test", nil, Options{
		Dial: func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpcontract.Client, error) {
			attempts++
			if attempts < 4 {
				return nil, errors.New("offline")
			}
			return client, nil
		},
		Sleep: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	})
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || !reflect.DeepEqual(delays, []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}) {
		t.Fatalf("attempts=%d delays=%v", attempts, delays)
	}
}

func TestRemoteToolFailureDegradesWithoutReplay(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{tools: []message.ToolDefinition{{Name: "fail", InputSchema: message.JSONSchema{Type: "object"}}}, callErr: errors.New("remote failed")}
	manager := managerWithClient(client)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	driver := manager.Snapshot()[0]
	if _, err := driver.Execute(ctx, tool.Call{ID: "call", Name: driver.Definition().Name}, nil); err == nil {
		t.Fatal("remote failure was hidden")
	}
	if client.callCount != 1 {
		t.Fatalf("call count=%d", client.callCount)
	}
	if manager.Servers()[0].State != StateDegraded || len(manager.Snapshot()) != 0 {
		t.Fatalf("servers=%#v snapshot=%v", manager.Servers(), manager.Snapshot())
	}
}

func managerWithClient(client *fakeClient) *Manager {
	return NewManager(map[string]config.MCPServerConfig{
		"local": {Enabled: true, Transport: "stdio", Command: "fake", ConnectTimeout: "1s", CallTimeout: "1s", MaxConcurrency: 1},
	}, "test", nil, Options{
		Dial: func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpcontract.Client, error) {
			return client, nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
}

type fakeClient struct {
	mu                 sync.Mutex
	tools              []message.ToolDefinition
	initializedName    string
	initializedVersion string
	lastTool           string
	callCount          int
	callErr            error
	closed             bool
}

func (c *fakeClient) Initialize(_ context.Context, name, version string) (mcpcontract.InitializeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initializedName, c.initializedVersion = name, version
	return mcpcontract.InitializeResult{ServerInfo: mcpcontract.ServerInfo{Name: "fake", Version: "1"}}, nil
}
func (c *fakeClient) ListTools(context.Context) ([]message.ToolDefinition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]message.ToolDefinition(nil), c.tools...), nil
}
func (c *fakeClient) CallTool(_ context.Context, name string, _ map[string]any) (mcpcontract.CallToolResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callCount++
	c.lastTool = name
	if c.callErr != nil {
		return mcpcontract.CallToolResult{}, c.callErr
	}
	return mcpcontract.CallToolResult{Content: []mcpcontract.ContentBlock{{Type: "text", Text: "ok"}}, StructuredContent: map[string]any{"ok": true}}, nil
}
func (*fakeClient) ListResources(context.Context) ([]mcpcontract.Resource, error) { return nil, nil }
func (*fakeClient) ReadResource(context.Context, string) ([]mcpcontract.ResourceContent, error) {
	return nil, nil
}
func (*fakeClient) ListPrompts(context.Context) ([]mcpcontract.Prompt, error) { return nil, nil }
func (*fakeClient) GetPrompt(context.Context, string, map[string]string) ([]mcpcontract.PromptMessage, error) {
	return nil, nil
}
func (c *fakeClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}
func (c *fakeClient) setTools(tools []message.ToolDefinition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = append([]message.ToolDefinition(nil), tools...)
}

func definitionNames(drivers []tool.Driver) []string {
	names := make([]string, 0, len(drivers))
	for _, driver := range drivers {
		names = append(names, driver.Definition().Name)
	}
	return names
}

func driverWithName(t *testing.T, drivers []tool.Driver, name string) tool.Driver {
	t.Helper()
	for _, driver := range drivers {
		if driver.Definition().Name == name {
			return driver
		}
	}
	t.Fatalf("driver %q not found", name)
	return nil
}
