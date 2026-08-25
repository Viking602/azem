package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/tool"
)

func TestAgentHubSendsListsWaitsAndConsumesPeerMessages(t *testing.T) {
	host := NewService(context.Background(), config.Default())
	host.mu.Lock()
	host.activeRun = "parent"
	host.activeSession = "session"
	host.guidanceOpen = true
	host.mu.Unlock()
	mainControl := host.TurnControl("parent")
	firstControl, secondControl := hyagent.NewControlQueue(), hyagent.NewControlQueue()
	runtime := &subagentRuntime{
		active: map[string]*activeSubagent{
			"child-a": {
				name: "ConfigAudit", control: firstControl,
				run: agentservice.SubagentRun{ID: "child-a", ChildRunID: "run-a", SessionID: "session", ParentRunID: "parent", Type: "explore", State: agentservice.SubagentRunning},
			},
			"child-b": {
				name: "UiAudit", control: secondControl,
				run: agentservice.SubagentRun{ID: "child-b", ChildRunID: "run-b", SessionID: "session", ParentRunID: "parent", Type: "review", State: agentservice.SubagentRunning},
			},
		},
		hosts: map[string]providerHost{"session": host}, peerMailboxes: make(map[string][]hubPeerMessage), peerChanged: make(chan struct{}),
	}
	callerA := tool.CallerInfo{AgentID: "subagent-explore", TeamRunID: "run-a"}
	callerB := tool.CallerInfo{AgentID: "subagent-review", TeamRunID: "run-b"}
	callerMain := tool.CallerInfo{AgentID: "azem-main", TeamRunID: "parent"}

	listed, err := runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "list", Caller: callerA, Params: map[string]any{}})
	if err != nil || !strings.Contains(listed.Content, "Main") || !strings.Contains(listed.Content, "ConfigAudit") || !strings.Contains(listed.Content, "UiAudit") {
		t.Fatalf("peer roster = %#v, %v", listed, err)
	}

	sent, err := runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "send", Caller: callerA, Params: map[string]any{"to": "UiAudit", "message": "Inspect the modal focus path"}})
	if err != nil || sent.IsError || !strings.Contains(sent.Content, "delivered") {
		t.Fatalf("peer send = %#v, %v", sent, err)
	}
	controls, err := secondControl.Drain(context.Background(), hyagent.TurnBoundaryBeforeModel)
	if err != nil || len(controls) != 1 || controls[0].Message.Visibility != "private" || !strings.Contains(controls[0].Message.Text, "Untrusted peer message from ConfigAudit") {
		t.Fatalf("peer control = %#v, %v", controls, err)
	}
	var receipts struct {
		Receipts []struct {
			ID string `json:"id"`
		} `json:"receipts"`
	}
	if err := json.Unmarshal(sent.Details, &receipts); err != nil || len(receipts.Receipts) != 1 {
		t.Fatalf("peer receipts = %#v, %v", receipts, err)
	}

	peeked, err := runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "inbox", Caller: callerB, Params: map[string]any{"peek": true}})
	if err != nil || !strings.Contains(peeked.Content, "Inspect the modal focus path") {
		t.Fatalf("peek inbox = %#v, %v", peeked, err)
	}
	consumed, err := runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "inbox", Caller: callerB, Params: map[string]any{}})
	if err != nil || !strings.Contains(consumed.Content, "Inspect the modal focus path") {
		t.Fatalf("consume inbox = %#v, %v", consumed, err)
	}
	empty, err := runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "inbox", Caller: callerB, Params: map[string]any{}})
	if err != nil || empty.Content != "No peer messages." {
		t.Fatalf("empty inbox = %#v, %v", empty, err)
	}

	_, err = runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "send", Caller: callerB, Params: map[string]any{
		"to": "ConfigAudit", "message": "Focus path is correct", "replyTo": receipts.Receipts[0].ID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	waited, err := runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "wait", Caller: callerA, Params: map[string]any{"from": "UiAudit", "timeoutMs": float64(100)}})
	if err != nil || !strings.Contains(waited.Content, "Focus path is correct") || !strings.Contains(waited.Content, "reply to "+receipts.Receipts[0].ID) {
		t.Fatalf("wait reply = %#v, %v", waited, err)
	}

	_, err = runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "send", Caller: callerB, Params: map[string]any{"to": "Main", "message": "Review complete"}})
	if err != nil {
		t.Fatal(err)
	}
	mainMessages, err := mainControl.Drain(context.Background(), hyagent.TurnBoundaryBeforeModel)
	if err != nil || len(mainMessages) != 1 || !strings.Contains(mainMessages[0].Message.Text, "Review complete") {
		t.Fatalf("child to main control = %#v, %v", mainMessages, err)
	}

	_, err = runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "send", Caller: callerMain, Params: map[string]any{"to": "UiAudit", "message": "Check one more state"}})
	if err != nil {
		t.Fatal(err)
	}
	mainToChild, err := secondControl.Drain(context.Background(), hyagent.TurnBoundaryAfterTools)
	if err != nil || len(mainToChild) != 1 || !strings.Contains(mainToChild[0].Message.Text, "Check one more state") {
		t.Fatalf("main to child control = %#v, %v", mainToChild, err)
	}
}

func TestAgentHubSendRevivesParkedAgentWithStableName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runtime, provider, coding, store := newGatedForegroundHarness(t, ctx, 0)
	defer runtime.Shutdown(ctx)
	defer coding.Close(ctx)
	firstParent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "parent-one", ProviderID: "test", ModelID: "model", Reasoning: "high",
		Driver: provider, Coding: coding, WorkspaceRoot: t.TempDir(),
	}
	if _, err := runtime.Drivers(firstParent); err != nil {
		t.Fatal(err)
	}
	spawn := &subagentSpawnDriver{runtime: runtime, parent: firstParent}
	returned := make(chan tool.Result, 1)
	go func() {
		result, _ := spawn.Execute(ctx, tool.Call{ID: "spawn", Name: subagentSpawnTool, Arguments: json.RawMessage(`{
			"name":"Researcher","prompt":"inspect the first state","description":"inspect state","subagent_type":"explore"
		}`)}, nil)
		returned <- result
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("initial named peer did not start")
	}
	provider.release <- struct{}{}
	select {
	case result := <-returned:
		if result.IsError {
			t.Fatalf("initial named peer = %#v", result)
		}
	case <-ctx.Done():
		t.Fatal("initial named peer did not park")
	}
	roster, err := runtime.ExecuteHubPeer(ctx, agentservice.HubPeerRequest{Operation: "list", Caller: tool.CallerInfo{TeamRunID: "parent-one"}, Params: map[string]any{}})
	if err != nil || !strings.Contains(roster.Content, "Researcher [explore] — parked") {
		t.Fatalf("parked roster = %#v, %v", roster, err)
	}

	secondParent := firstParent
	secondParent.ParentRunID = "parent-two"
	if _, err := runtime.Drivers(secondParent); err != nil {
		t.Fatal(err)
	}
	sent, err := runtime.ExecuteHubPeer(ctx, agentservice.HubPeerRequest{Operation: "send", Caller: tool.CallerInfo{AgentID: "azem-main", TeamRunID: "parent-two"}, Params: map[string]any{
		"to": "Researcher", "message": "Inspect the new evidence",
	}})
	if err != nil || sent.IsError {
		t.Fatalf("revive send = %#v, %v", sent, err)
	}
	select {
	case goal := <-provider.started:
		if !strings.Contains(goal, "Inspect the new evidence") || !strings.Contains(goal, "Untrusted peer message from Main") {
			t.Fatalf("revived peer goal = %q", goal)
		}
	case <-ctx.Done():
		t.Fatal("parked peer was not revived")
	}
	runtime.mu.Lock()
	var revivedID string
	for id, active := range runtime.active {
		if active.name == "Researcher" {
			revivedID = id
		}
	}
	runtime.mu.Unlock()
	if revivedID == "" {
		t.Fatal("revived peer lost its stable name")
	}
	provider.release <- struct{}{}
	snapshots := runtime.Query(ctx, "session", []string{revivedID}, 2*time.Second)
	if len(snapshots) != 1 || !snapshots[0].Found || snapshots[0].Run.State != agentservice.SubagentCompleted {
		t.Fatalf("revived peer completion = %#v", snapshots)
	}
	runs, err := store.List(ctx, "session")
	if err != nil || len(runs) != 2 {
		t.Fatalf("revived durable runs = %#v, %v", runs, err)
	}
	roster, err = runtime.ExecuteHubPeer(ctx, agentservice.HubPeerRequest{Operation: "list", Caller: tool.CallerInfo{TeamRunID: "parent-two"}, Params: map[string]any{}})
	if err != nil || !strings.Contains(roster.Content, "Researcher [explore] — parked") {
		t.Fatalf("reparked roster = %#v, %v", roster, err)
	}
}

func TestAgentHubRestoresParkedRosterAfterRuntimeRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, provider, coding, store := newGatedForegroundHarness(t, ctx, 0)
	defer coding.Close(ctx)
	parent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "parent-one", ProviderID: "test", ModelID: "model", Reasoning: "high",
		Driver: provider, Coding: coding, WorkspaceRoot: t.TempDir(),
	}
	if _, err := first.Drivers(parent); err != nil {
		t.Fatal(err)
	}
	spawn := &subagentSpawnDriver{runtime: first, parent: parent}
	returned := make(chan tool.Result, 1)
	go func() {
		result, _ := spawn.Execute(ctx, tool.Call{ID: "spawn", Name: subagentSpawnTool, Arguments: json.RawMessage(`{
			"name":"DurableResearcher","prompt":"inspect durable state","description":"inspect durable state","subagent_type":"explore"
		}`)}, nil)
		returned <- result
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("durable peer did not start")
	}
	provider.release <- struct{}{}
	select {
	case <-returned:
	case <-ctx.Done():
		t.Fatal("durable peer did not complete")
	}
	first.Shutdown(ctx)

	cfg := config.Default().Agents.Subagents
	restarted, err := newSubagentRuntime(ctx, cfg, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(ctx)
	parent.ParentRunID = "parent-two"
	if _, err := restarted.Drivers(parent); err != nil {
		t.Fatal(err)
	}
	roster, err := restarted.ExecuteHubPeer(ctx, agentservice.HubPeerRequest{Operation: "list", Caller: tool.CallerInfo{TeamRunID: "parent-two"}, Params: map[string]any{}})
	if err != nil || !strings.Contains(roster.Content, "DurableResearcher [explore] — parked") {
		t.Fatalf("restored parked roster = %#v, %v", roster, err)
	}
	sent, err := restarted.ExecuteHubPeer(ctx, agentservice.HubPeerRequest{Operation: "send", Caller: tool.CallerInfo{TeamRunID: "parent-two"}, Params: map[string]any{
		"to": "DurableResearcher", "message": "Continue after restart",
	}})
	if err != nil || sent.IsError {
		t.Fatalf("restored peer send = %#v, %v", sent, err)
	}
	select {
	case goal := <-provider.started:
		if !strings.Contains(goal, "Continue after restart") {
			t.Fatalf("restored peer goal = %q", goal)
		}
	case <-ctx.Done():
		t.Fatal("restored parked peer did not revive")
	}
	provider.release <- struct{}{}
}

func TestAgentHubRejectsUnknownSelfAndEmptyBroadcasts(t *testing.T) {
	runtime := &subagentRuntime{
		active: map[string]*activeSubagent{"child": {
			name: "OnlyPeer", control: hyagent.NewControlQueue(),
			run: agentservice.SubagentRun{ID: "child", ChildRunID: "run-child", SessionID: "session", ParentRunID: "parent", State: agentservice.SubagentRunning},
		}},
		peerMailboxes: make(map[string][]hubPeerMessage), peerChanged: make(chan struct{}),
	}
	caller := tool.CallerInfo{TeamRunID: "run-child"}
	for name, to := range map[string]string{"self": "OnlyPeer", "unknown": "Missing", "empty broadcast": "all"} {
		t.Run(name, func(t *testing.T) {
			if _, err := runtime.ExecuteHubPeer(context.Background(), agentservice.HubPeerRequest{Operation: "send", Caller: caller, Params: map[string]any{"to": to, "message": "hello"}}); err == nil {
				t.Fatal("invalid peer send accepted")
			}
		})
	}
}
