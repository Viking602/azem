package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	if events[len(events)-1].State != StateStopped {
		t.Fatalf("close did not emit stopped event: %#v", events)
	}
}

func TestManagerClosesClientsConcurrentlyAndHonorsContext(t *testing.T) {
	releaseBoth := make(chan struct{})
	startedBoth := make(chan struct{}, 2)
	one := &fakeClient{closeBlock: releaseBoth, closeStarted: startedBoth}
	two := &fakeClient{closeBlock: releaseBoth, closeStarted: startedBoth}
	manager := NewManager(nil, "test", nil, Options{})
	manager.servers = map[string]*server{
		"one": {client: one, connection: newConnectionAttempt(one)},
		"two": {client: two, connection: newConnectionAttempt(two)},
	}
	closed := make(chan error, 1)
	go func() { closed <- manager.CloseContext(context.Background()) }()
	<-startedBoth
	<-startedBoth
	close(releaseBoth)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	closeStarted := make(chan struct{}, 1)
	blocked := &fakeClient{closeBlock: release, closeStarted: closeStarted}
	manager = NewManager(nil, "test", nil, Options{})
	manager.servers = map[string]*server{"blocked": {client: blocked, connection: newConnectionAttempt(blocked)}}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- manager.CloseContext(ctx) }()
	<-closeStarted
	cancel()
	select {
	case err := <-result:
		t.Fatalf("close abandoned an existing client after cancellation: %v", err)
	default:
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("released close: %v", err)
	}
}

func TestManagerShutdownRejectsInflightPublishWithoutWaitingForClose(t *testing.T) {
	initialize := make(chan struct{})
	initializeStarted := make(chan struct{}, 1)
	releaseClose := make(chan struct{})
	client := &fakeClient{initializeBlock: initialize, initializeStarted: initializeStarted, closeBlock: releaseClose}
	manager := managerWithClient(client)
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	<-initializeStarted
	ctx, cancel := context.WithCancel(context.Background())
	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.CloseContext(ctx) }()
	for !manager.isClosed() {
		runtime.Gosched()
	}
	cancel()
	select {
	case err := <-closeDone:
		t.Fatalf("close abandoned initialized client: %v", err)
	default:
	}
	close(initialize)
	if err := <-startDone; !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("start error = %v", err)
	}
	if servers := manager.Servers(); servers[0].State != StateStopped || servers[0].ToolCount != 0 || len(manager.Snapshot()) != 0 {
		t.Fatalf("manager republished after close: %#v", servers)
	}
	close(releaseClose)
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagerCloseAdmitsDialBeforeClientExists(t *testing.T) {
	dialStarted := make(chan struct{}, 1)
	releaseDial := make(chan struct{})
	closeStarted := make(chan struct{}, 1)
	releaseClose := make(chan struct{})
	client := &fakeClient{closeStarted: closeStarted, closeBlock: releaseClose}
	manager := NewManager(map[string]config.MCPServerConfig{
		"local": {Enabled: true, ConnectDuration: time.Second},
	}, "test", nil, Options{
		Dial: func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpcontract.Client, error) {
			dialStarted <- struct{}{}
			<-releaseDial
			return client, nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	<-dialStarted

	closeCtx, cancelClose := context.WithCancel(context.Background())
	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.CloseContext(closeCtx) }()
	for !manager.isClosed() {
		runtime.Gosched()
	}
	select {
	case err := <-closeDone:
		t.Fatalf("close returned before admitted dial completed: %v", err)
	default:
	}
	cancelClose()
	if err := <-closeDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled close error = %v", err)
	}

	close(releaseDial)
	<-closeStarted
	close(releaseClose)
	if err := <-startDone; !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("start error = %v", err)
	}
	if err := manager.CloseContext(context.Background()); err != nil {
		t.Fatalf("wait for dialed client close: %v", err)
	}
}

func TestManagerCloseWaitsForSupersededDial(t *testing.T) {
	dialStarted := make(chan int, 2)
	releaseDial := []chan struct{}{make(chan struct{}), make(chan struct{})}
	closeStarted := make(chan int, 2)
	releaseClose := []chan struct{}{make(chan struct{}), make(chan struct{})}
	clientCloseStarted := []chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)}
	clients := []*fakeClient{
		{closeStarted: clientCloseStarted[0], closeBlock: releaseClose[0]},
		{closeStarted: clientCloseStarted[1], closeBlock: releaseClose[1]},
	}
	for index, started := range clientCloseStarted {
		index, started := index, started
		go func() {
			<-started
			closeStarted <- index
		}()
	}
	var dialCount atomic.Int32
	manager := NewManager(map[string]config.MCPServerConfig{
		"local": {Enabled: true, ConnectDuration: time.Second},
	}, "test", nil, Options{
		Dial: func(context.Context, string, config.MCPServerConfig, map[string]string, http.Header) (mcpcontract.Client, error) {
			index := int(dialCount.Add(1) - 1)
			dialStarted <- index
			<-releaseDial[index]
			return clients[index], nil
		},
	})
	connectDone := make(chan error, 2)
	go func() { connectDone <- manager.connectOnce(context.Background(), "local") }()
	if index := <-dialStarted; index != 0 {
		t.Fatalf("first dial index = %d", index)
	}
	go func() { connectDone <- manager.connectOnce(context.Background(), "local") }()
	if index := <-dialStarted; index != 1 {
		t.Fatalf("second dial index = %d", index)
	}

	closed := make(chan error, 1)
	go func() { closed <- manager.CloseContext(context.Background()) }()
	for !manager.isClosed() {
		runtime.Gosched()
	}
	close(releaseDial[1])
	if index := <-closeStarted; index != 1 {
		t.Fatalf("first closed client index = %d", index)
	}
	close(releaseClose[1])
	select {
	case err := <-closed:
		t.Fatalf("close returned before superseded dial completed: %v", err)
	default:
	}
	close(releaseDial[0])
	if index := <-closeStarted; index != 0 {
		t.Fatalf("superseded closed client index = %d", index)
	}
	close(releaseClose[0])
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	for range clients {
		if err := <-connectDone; !errors.Is(err, ErrManagerClosed) {
			t.Fatalf("connect error = %v", err)
		}
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

func TestRemoteToolBusinessErrorKeepsServerReady(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{
		tools: []message.ToolDefinition{{Name: "lookup", InputSchema: message.JSONSchema{Type: "object"}}},
		callResult: &mcpcontract.CallToolResult{
			Content: []mcpcontract.ContentBlock{{Type: "text", Text: "object not found"}}, IsError: true,
		},
	}
	manager := managerWithClient(client)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	driver := manager.Snapshot()[0]
	result, err := driver.Execute(ctx, tool.Call{ID: "call-1", Name: driver.Definition().Name}, nil)
	if err != nil || !result.IsError || result.Content != "object not found" {
		t.Fatalf("business error result=%#v error=%v", result, err)
	}
	if manager.Servers()[0].State != StateReady || len(manager.Snapshot()) != 1 {
		t.Fatalf("business error degraded server: servers=%#v snapshot=%v", manager.Servers(), manager.Snapshot())
	}
	second := manager.Snapshot()[0]
	if _, err := second.Execute(ctx, tool.Call{ID: "call-2", Name: second.Definition().Name}, nil); err != nil {
		t.Fatalf("second business-error call failed at transport level: %v", err)
	}
	if client.callCount != 2 {
		t.Fatalf("call count=%d, want 2", client.callCount)
	}
}

func TestRemoteToolPreservesBoundedTextAndStructuredResult(t *testing.T) {
	ctx := context.Background()
	structured := map[string]any{"status": "ready", "count": float64(2)}
	client := &fakeClient{
		tools: []message.ToolDefinition{{Name: "status", InputSchema: message.JSONSchema{Type: "object"}}},
		callResult: &mcpcontract.CallToolResult{
			Content:           []mcpcontract.ContentBlock{{Type: "text", Text: "ready"}},
			StructuredContent: structured,
		},
	}
	manager := managerWithClient(client)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	driver := manager.Snapshot()[0]
	result, err := driver.Execute(ctx, tool.Call{ID: "call", Name: driver.Definition().Name}, nil)
	if err != nil || result.IsError || result.Content != "ready" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(result.Structured, &decoded); err != nil || !reflect.DeepEqual(decoded, structured) {
		t.Fatalf("structured=%s decoded=%v error=%v", result.Structured, decoded, err)
	}
}

func TestRemoteToolOversizedTextReturnsBoundedBusinessError(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{
		tools: []message.ToolDefinition{{Name: "search", InputSchema: message.JSONSchema{Type: "object"}}},
		callResult: &mcpcontract.CallToolResult{
			Content: []mcpcontract.ContentBlock{{Type: "text", Text: strings.Repeat("x", maxMCPModelOutputBytes+1)}},
		},
	}
	manager := managerWithClient(client)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	driver := manager.Snapshot()[0]
	result, err := driver.Execute(ctx, tool.Call{ID: "large", Name: driver.Definition().Name}, nil)
	assertMCPOutputLimitError(t, result, err)
	if manager.Servers()[0].State != StateReady || len(manager.Snapshot()) != 1 {
		t.Fatalf("output limit degraded server: servers=%#v snapshot=%v", manager.Servers(), manager.Snapshot())
	}
	client.setCallResult(nil)
	result, err = manager.Snapshot()[0].Execute(ctx, tool.Call{ID: "small", Name: driver.Definition().Name}, nil)
	if err != nil || result.IsError || result.Content != "ok" {
		t.Fatalf("call after oversized output result=%#v error=%v", result, err)
	}
}

func TestRemoteToolOversizedStructuredResultIsNotExposed(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{
		tools: []message.ToolDefinition{{Name: "query", InputSchema: message.JSONSchema{Type: "object"}}},
		callResult: &mcpcontract.CallToolResult{
			Content:           []mcpcontract.ContentBlock{{Type: "text", Text: "small"}},
			StructuredContent: map[string]any{"data": strings.Repeat("x", maxMCPModelOutputBytes)},
		},
	}
	manager := managerWithClient(client)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	driver := manager.Snapshot()[0]
	result, err := driver.Execute(ctx, tool.Call{ID: "call", Name: driver.Definition().Name}, nil)
	assertMCPOutputLimitError(t, result, err)
	if len(result.Structured) != 0 {
		t.Fatalf("oversized structured result leaked %d bytes", len(result.Structured))
	}
}

func TestRemoteToolCombinedContentBlocksRespectAggregateLimit(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{
		tools: []message.ToolDefinition{{Name: "read", InputSchema: message.JSONSchema{Type: "object"}}},
		callResult: &mcpcontract.CallToolResult{Content: []mcpcontract.ContentBlock{
			{Type: "text", Text: strings.Repeat("a", maxMCPModelOutputBytes/2)},
			{Type: "text", Text: strings.Repeat("b", maxMCPModelOutputBytes/2)},
		}},
	}
	manager := managerWithClient(client)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	driver := manager.Snapshot()[0]
	result, err := driver.Execute(ctx, tool.Call{ID: "call", Name: driver.Definition().Name}, nil)
	assertMCPOutputLimitError(t, result, err)
}

func TestBoundMCPModelOutputSerializedBoundary(t *testing.T) {
	base := tool.Result{ToolCallID: "call", Name: "mcp__local__read", Content: "x"}
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Content = strings.Repeat("x", maxMCPModelOutputBytes-len(encoded)+1)
	encoded, err = json.Marshal(base)
	if err != nil || len(encoded) != maxMCPModelOutputBytes {
		t.Fatalf("boundary result size=%d error=%v", len(encoded), err)
	}
	bounded := boundMCPModelOutput(base)
	if bounded.IsError || bounded.Content != base.Content {
		t.Fatalf("exact-limit result was rejected: %#v", bounded)
	}
	base.Content += "x"
	assertMCPOutputLimitError(t, boundMCPModelOutput(base), nil)
}

func assertMCPOutputLimitError(t *testing.T, result tool.Result, err error) {
	t.Helper()
	if err != nil || !result.IsError {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if !strings.Contains(result.Content, "model-context output limit") ||
		!strings.Contains(result.Content, "Narrow the query") {
		t.Fatalf("unexpected output-limit message %q", result.Content)
	}
	if len(result.Content)+len(result.Structured) >= 1024 {
		t.Fatalf("output-limit error is not bounded: content=%d structured=%d", len(result.Content), len(result.Structured))
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
	callResult         *mcpcontract.CallToolResult
	closeDelay         time.Duration
	closeBlock         <-chan struct{}
	closeStarted       chan<- struct{}
	initializeBlock    <-chan struct{}
	initializeStarted  chan<- struct{}
	closed             bool
}

func (c *fakeClient) Initialize(_ context.Context, name, version string) (mcpcontract.InitializeResult, error) {
	if c.initializeStarted != nil {
		c.initializeStarted <- struct{}{}
	}
	if c.initializeBlock != nil {
		<-c.initializeBlock
	}
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
	if c.callResult != nil {
		return *c.callResult, nil
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
	if c.closeStarted != nil {
		c.closeStarted <- struct{}{}
	}
	if c.closeDelay > 0 {
		time.Sleep(c.closeDelay)
	}
	if c.closeBlock != nil {
		<-c.closeBlock
	}
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

func (c *fakeClient) setCallResult(result *mcpcontract.CallToolResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callResult = result
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
