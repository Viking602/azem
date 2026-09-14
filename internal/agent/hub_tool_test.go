package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

func TestHubTracksWaitsAndCancelsBackgroundShellJobs(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := newBackgroundJobManager(base)
	t.Cleanup(func() { _ = jobs.shutdown(context.Background()) })
	shellRuntime := newShellRuntime(base, defaultShellOptions())
	shell := newRuntimeShellDriver(t.TempDir(), "allow", "deny", shellRuntime, jobs)
	hub := newHubDriver(t.TempDir(), jobs)
	ctx := WithInvocation(context.Background(), Invocation{AgentID: "Main"})

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

func TestHubReportsMissingNativeProcessHost(t *testing.T) {
	driver := newHubDriver(t.TempDir())
	for _, op := range []string{"start", "ps", "logs", "stop", "restart", "describe"} {
		result := callHub(context.Background(), driver, map[string]any{"op": op})
		if !result.IsError || !strings.Contains(result.Content, "native process host is unavailable") {
			t.Fatalf("op %q = %#v", op, result)
		}
	}
	read := driver.PolicyForCall(tool.Call{Name: ToolHub, Arguments: json.RawMessage(`{"op":"jobs"}`)})
	if read.Effect != agentruntime.ToolEffectReadOnly || read.RequiresApproval {
		t.Fatalf("jobs governance = %#v", read)
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
	driver := newHubDriver(t.TempDir())
	driver.peers = ref
	ctx := WithInvocation(context.Background(), Invocation{AgentID: "Main", TeamRunID: "parent"})

	sent := callHub(ctx, driver, map[string]any{"op": "send", "to": "Reviewer", "message": "check the change", "replyTo": "peer-0"})
	if sent.IsError || sent.Content != "peer-send" || len(broker.requests) != 1 ||
		broker.requests[0].Caller.AgentID != "Main" || broker.requests[0].Params["to"] != "Reviewer" {
		t.Fatalf("peer send = %#v requests=%#v", sent, broker.requests)
	}
	policy := driver.PolicyForCall(tool.Call{Name: ToolHub, Arguments: json.RawMessage(`{"op":"send","to":"Reviewer","message":"check"}`)})
	if policy.Effect != agentruntime.ToolEffectReadOnly || policy.RequiresApproval {
		t.Fatalf("peer send governance = %#v", policy)
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

func TestHubPeerOpsRequireBroker(t *testing.T) {
	driver := newHubDriver(t.TempDir())
	result := callHub(context.Background(), driver, map[string]any{"op": "list"})
	if !result.IsError || !strings.Contains(result.Content, "peer operations are unavailable") {
		t.Fatalf("list without broker = %#v", result)
	}
}

func executeHub(t *testing.T, ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	t.Helper()
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
