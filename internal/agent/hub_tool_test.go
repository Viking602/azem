package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/Viking602/venat/tool"
)

func TestHubSupervisesLongRunningProcessLifecycle(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "hub-process", AgentID: "Main"})
	root := t.TempDir()
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newHubDriver(root, bridge)
	name := "echo-service"
	t.Cleanup(func() {
		_ = callHub(context.Background(), driver, map[string]any{"op": "stop", "name": name, "timeout": 2})
	})
	program := "import sys\nprint('READY', flush=True)\nfor line in sys.stdin:\n line=line.strip()\n print('ECHO:'+line, flush=True)\n if line=='quit': break\n"
	started := executeHub(t, ctx, driver, map[string]any{
		"op": "start", "name": name, "application": python, "args": []string{"-u", "-c", program}, "pty": true,
		"ready": map[string]any{"log": "READY", "timeout": 20}, "restart": "no",
	})
	if !strings.Contains(strings.ToLower(started.Content), "ready") {
		t.Fatalf("start = %s", started.Content)
	}
	listed := executeHub(t, ctx, driver, map[string]any{"op": "ps"})
	if !strings.Contains(listed.Content, name) {
		t.Fatalf("ps = %s", listed.Content)
	}
	secondBridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = secondBridge.Close(context.Background()) })
	secondHub := newHubDriver(root, secondBridge)
	shared := executeHub(t, ctx, secondHub, map[string]any{"op": "ps"})
	if !strings.Contains(shared.Content, name) {
		t.Fatalf("second client did not see shared process: %s", shared.Content)
	}
	executeHub(t, ctx, driver, map[string]any{"op": "send", "name": name, "text": "hello", "enter": true})
	waited := executeHub(t, ctx, driver, map[string]any{"op": "wait", "name": name, "pattern": "ECHO:hello", "timeout": 20})
	if !strings.Contains(waited.Content, "ECHO:hello") {
		t.Fatalf("wait = %s", waited.Content)
	}
	logs := executeHub(t, ctx, driver, map[string]any{"op": "logs", "name": name, "lines": 20})
	if !strings.Contains(logs.Content, "READY") || !strings.Contains(logs.Content, "ECHO:hello") {
		t.Fatalf("logs = %s", logs.Content)
	}
	described := executeHub(t, ctx, driver, map[string]any{"op": "describe", "name": name})
	if !strings.Contains(described.Content, name) {
		t.Fatalf("describe = %s", described.Content)
	}
	executeHub(t, ctx, driver, map[string]any{"op": "stop", "name": name, "timeout": 5})
	restarted := executeHub(t, ctx, driver, map[string]any{"op": "restart", "name": name})
	if !strings.Contains(restarted.Content, "Restarted") {
		t.Fatalf("restart = %s", restarted.Content)
	}
	executeHub(t, ctx, driver, map[string]any{"op": "wait", "name": name, "for": "ready", "timeout": 20})
	executeHub(t, ctx, driver, map[string]any{"op": "stop", "name": name, "timeout": 5})
}

func TestHubTracksWaitsAndCancelsBackgroundShellJobs(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := newBackgroundJobManager(base)
	t.Cleanup(func() { _ = jobs.shutdown(context.Background()) })
	shellRuntime := newShellRuntime(base, defaultShellOptions())
	shell := newRuntimeShellDriver(t.TempDir(), "allow", "deny", shellRuntime, jobs)
	hub := newHubDriver(t.TempDir(), newLSPBridgeRuntime(), jobs)
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{AgentID: "Main"})

	arguments, _ := json.Marshal(shellInput{Command: "sleep 0.1; printf 'job-done\\n'", Async: true, WallClockSeconds: 5})
	started, err := shell.Execute(ctx, tool.Call{ID: "shell-job", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil || started.IsError {
		t.Fatalf("start background shell = %#v, %v", started, err)
	}
	var snapshot backgroundJobSnapshot
	if json.Unmarshal(started.Structured, &snapshot) != nil || snapshot.ID == "" {
		t.Fatalf("background snapshot = %#v", started)
	}
	waited := executeHub(t, ctx, hub, map[string]any{"op": "wait", "ids": []string{snapshot.ID}, "timeoutMs": 5000})
	if !strings.Contains(waited.Content, "completed") || !strings.Contains(waited.Content, "job-done") {
		t.Fatalf("background wait = %s", waited.Content)
	}

	arguments, _ = json.Marshal(shellInput{Command: "sleep 30", Async: true, WallClockSeconds: 40})
	started, err = shell.Execute(ctx, tool.Call{ID: "shell-cancel", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil || json.Unmarshal(started.Structured, &snapshot) != nil {
		t.Fatalf("start cancellable shell = %#v, %v", started, err)
	}
	cancelled := executeHub(t, ctx, hub, map[string]any{"op": "cancel", "ids": []string{snapshot.ID}})
	if !strings.Contains(cancelled.Content, "cancelled") {
		t.Fatalf("background cancel = %s", cancelled.Content)
	}
}

func TestHubUsesDynamicApprovalAndValidatesArgv(t *testing.T) {
	driver := newHubDriver(t.TempDir(), newLSPBridgeRuntime())
	readArgs := json.RawMessage(`{"op":"logs","name":"service"}`)
	read := driver.DefinitionForCall(tool.Call{Name: ToolHub, Arguments: readArgs})
	if read.EffectType != tool.EffectReadOnly || read.RequiresApproval {
		t.Fatalf("logs governance = %#v", read)
	}
	startArgs := json.RawMessage(`{"op":"start","name":"service","application":"python3"}`)
	start := driver.DefinitionForCall(tool.Call{Name: ToolHub, Arguments: startArgs})
	if start.EffectType != tool.EffectExternalSideEffect || !start.RequiresApproval || !start.RequiresActionTask {
		t.Fatalf("start governance = %#v", start)
	}
	invalid := callHub(context.Background(), driver, map[string]any{"op": "start", "name": "bad name", "application": "python3"})
	if !invalid.IsError || !strings.Contains(invalid.Content, "process name") {
		t.Fatalf("invalid start = %#v", invalid)
	}
}

type fakeHubPeerBroker struct {
	requests []HubPeerRequest
}

func (broker *fakeHubPeerBroker) ExecuteHubPeer(_ context.Context, request HubPeerRequest) (HubPeerResponse, error) {
	broker.requests = append(broker.requests, request)
	details, _ := json.Marshal(map[string]any{"op": request.Operation})
	return HubPeerResponse{Content: "peer-" + request.Operation, Details: details}, nil
}

func TestHubRoutesPeerMessagingWithoutProcessApproval(t *testing.T) {
	broker := &fakeHubPeerBroker{}
	ref := &hubPeerBrokerRef{}
	ref.set(broker)
	driver := newHubDriver(t.TempDir(), newLSPBridgeRuntime())
	driver.peers = ref
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{AgentID: "Main", TeamRunID: "parent"})

	sent := callHub(ctx, driver, map[string]any{"op": "send", "to": "Reviewer", "message": "check the change", "replyTo": "peer-0"})
	if sent.IsError || sent.Content != "peer-send" || len(broker.requests) != 1 ||
		broker.requests[0].Caller.AgentID != "Main" || broker.requests[0].Params["to"] != "Reviewer" {
		t.Fatalf("peer send = %#v requests=%#v", sent, broker.requests)
	}
	definition := driver.DefinitionForCall(tool.Call{Name: ToolHub, Arguments: json.RawMessage(`{"op":"send","to":"Reviewer","message":"check"}`)})
	if definition.EffectType != tool.EffectReadOnly || definition.RequiresApproval {
		t.Fatalf("peer send governance = %#v", definition)
	}
	listed := callHub(ctx, driver, map[string]any{"op": "list"})
	if listed.IsError || listed.Content != "peer-list" || len(broker.requests) != 2 {
		t.Fatalf("peer list = %#v requests=%#v", listed, broker.requests)
	}
	invalid := callHub(ctx, driver, map[string]any{"op": "send", "to": "Reviewer", "message": ""})
	if !invalid.IsError || !strings.Contains(invalid.Content, "message is required") {
		t.Fatalf("invalid peer send = %#v", invalid)
	}
}

func executeHub(t *testing.T, ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	t.Helper()
	if concrete, ok := driver.(*hubDriver); ok {
		if err := concrete.bridge.resolveAssets(); err != nil {
			t.Skip(err)
		}
	}
	result := callHub(ctx, driver, input)
	if result.IsError {
		t.Fatalf("hub call failed: %s", result.Content)
	}
	return result
}

func callHub(ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	arguments, _ := json.Marshal(input)
	result, err := driver.Execute(ctx, tool.Call{ID: "hub", Name: ToolHub, Arguments: arguments}, nil)
	if err != nil {
		return tool.Result{ToolCallID: "hub", Name: ToolHub, Content: err.Error(), IsError: true}
	}
	return result
}
