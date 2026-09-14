package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

func TestFusionProjectionKeepsFailuresControlsAndIndependentTools(t *testing.T) {
	ctx := context.Background()
	s := NewService(ctx, config.Config{})
	defer s.cancel()
	host := fusionHost{providerHost: s, parentRunID: "root", model: "actual-model"}
	for _, event := range []Event{
		{Kind: EventToolStarted, RunID: "root", ToolCallID: "handoff", Data: map[string]string{"name": "sidekick", "arguments": "private brief"}},
		{Kind: EventToolUpdate, RunID: "root", ToolCallID: "handoff", Text: "private delta"},
		{Kind: EventToolFinished, RunID: "root", ToolCallID: "handoff", State: session.ToolFailed, Text: `{"error":"worker timeout","output":"private report"}`, Data: map[string]string{"name": "sidekick"}},
		{Kind: EventToolStarted, RunID: "root", ToolCallID: "ordinary", Data: map[string]string{"name": "vibe_spawn"}},
		{Kind: EventAgentState, RunID: "root", AgentID: "ordinary-worker"},
	} {
		event.SessionID = "session"
		if !s.EmitEvent(ctx, event) {
			t.Fatal("event delivery failed")
		}
	}
	for _, child := range []string{"child-a", "child-b"} {
		for _, kind := range []EventKind{EventTextDelta, EventThinkingDelta, EventAgentState, EventToolStarted, EventToolFinished, EventApprovalRequested} {
			if !host.EmitEvent(ctx, Event{Kind: kind, SessionID: "session", RunID: child, AgentID: "worker", ToolCallID: "same-call",
				ApprovalID: "approve-" + child, State: session.ToolFailed, Text: "actual tool failure", Data: map[string]string{"name": "coding.shell"}}) {
				t.Fatal("Fusion event delivery failed")
			}
		}
	}
	s.events.Close()
	events := drainBrokerEvents(t, s.events)
	if len(events) != 15 {
		t.Fatalf("visible events = %d, want handoffs, Vibe and both model/tool/control sequences", len(events))
	}
	if events[0].Data["name"] != "sidekick" || events[0].Data["arguments"] != "private brief" || events[3].Data["name"] != "vibe_spawn" || events[4].AgentID != "ordinary-worker" {
		t.Fatal("handoff or ordinary subagent projection changed")
	}
	for index, child := range []string{"child-a", "child-b"} {
		for _, event := range events[5+index*5 : 9+index*5] {
			if event.RunID != "root" || event.AgentID != "" || event.Data["executionRunId"] != child || event.Data["sourceLabel"] != "Sidekick · actual-model" {
				t.Fatal("child source identity was lost")
			}
			if event.Kind == EventTextDelta || event.Kind == EventThinkingDelta {
				if event.TextPhase != "commentary" {
					t.Fatal("child report impersonated the lead final")
				}
			} else if event.ToolCallID != fusionToolID(child, "same-call") {
				t.Fatal("child tool identity collided")
			}
		}
		control := events[9+index*5]
		if control.ApprovalID != "approve-"+child || control.RunID != child || control.AgentID != "worker" || control.ToolCallID != "same-call" {
			t.Fatal("approval routing identity changed")
		}
	}
}

func TestFusionHandoffsRestoreTheirPromptAndFailure(t *testing.T) {
	ctx := context.Background()
	s := NewService(ctx, config.Config{})
	defer s.cancel()
	// Every handoff remains reviewable, including failures and its actual brief.
	for _, state := range []string{session.ToolCompleted, session.ToolFailed, session.ToolInterrupted, session.ToolReconcileRequired} {
		projection := session.Projection{ToolRecords: []session.ToolRecord{{RunID: "root", ToolCallID: "handoff", Name: "sidekick", State: state,
			Arguments: json.RawMessage(`{"prompt":"private brief"}`), Content: `{"error":"worker timeout","output":"private report"}`}}}
		if _, err := s.projectFusionSession(ctx, &projection); err != nil || len(projection.ToolRecords) != 1 {
			t.Fatalf("restore failed handoff: %v", err)
		}
		record := projection.ToolRecords[0]
		if record.Name != "sidekick" || record.State != state || record.Content != `{"error":"worker timeout","output":"private report"}` || len(record.Arguments) == 0 {
			t.Fatal("restored handoff lost its brief or result")
		}
	}
}

func TestFusionConversationRestoresOnlyCurrentHandoffProse(t *testing.T) {
	run := agentservice.SubagentRun{ChildRunID: "child", Model: "actual-model"}
	handoff := session.ToolRecord{RunID: "lead", ToolCallID: "handoff", AnchorSequence: 17, Arguments: json.RawMessage(`{"prompt":"same brief"}`)}
	blocks := []AgentTranscriptBlock{
		{Kind: "user", Content: "same brief"}, {Kind: "assistant", Content: "old report"},
		{Kind: "user", Content: "same brief"}, {ID: "thought", Kind: "thinking", Content: "reason"},
		{ID: "progress", Kind: "commentary", Content: "progress"}, {Kind: "tool", ToolCallID: "read"},
		{ID: "report", Kind: "assistant", Content: "report"},
	}
	restored := fusionConversationBlocks(run, handoff, blocks)
	active := fusionConversationBlocks(run, handoff, blocks[3:])
	if !reflect.DeepEqual(restored, active) || len(restored) != 3 {
		t.Fatal("inherited context repeated or active prose lost")
	}
	for i, block := range restored {
		if block.RunID != "lead" || block.Sequence != 17 || block.TextPhase != "commentary" || block.Data["sourceLabel"] != "Sidekick · actual-model" {
			t.Fatal("display projection changed durable identity or final ownership")
		}
		anchor := "handoff"
		if i == 2 {
			anchor = fusionToolID("child", "read")
		}
		if block.Data["fusionAfterToolCallId"] != anchor {
			t.Fatal("prose lost its tool boundary")
		}
	}
}
