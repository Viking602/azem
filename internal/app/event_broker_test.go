package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/tool"
)

func TestEventBrokerCoalescesDeltasBeforeLifecycleBarrier(t *testing.T) {
	broker := newEventBroker(time.Hour)
	if broker.Publish(Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: "hello"}) != eventPublishAccepted ||
		broker.Publish(Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: " world"}) != eventPublishAccepted ||
		broker.Publish(Event{Kind: EventRunFinished, SessionID: "session", RunID: "run"}) != eventPublishAccepted {
		t.Fatal("broker rejected event before close")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	delta, err := broker.Next(ctx)
	if err != nil || delta.Kind != EventTextDelta || delta.Text != "hello world" {
		t.Fatalf("coalesced delta=%#v error=%v", delta, err)
	}
	finished, err := broker.Next(ctx)
	if err != nil || finished.Kind != EventRunFinished {
		t.Fatalf("lifecycle event=%#v error=%v", finished, err)
	}
}

func TestEventBrokerKeepsTextPhasesSeparate(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.Publish(Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: "working", TextPhase: "commentary"})
	broker.Publish(Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: "answer", TextPhase: "final"})
	broker.Publish(Event{Kind: EventRunFinished, SessionID: "session", RunID: "run"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	commentary, err := broker.Next(ctx)
	if err != nil || commentary.Text != "working" || commentary.TextPhase != "commentary" {
		t.Fatalf("commentary delta=%#v error=%v", commentary, err)
	}
	final, err := broker.Next(ctx)
	if err != nil || final.Text != "answer" || final.TextPhase != "final" {
		t.Fatalf("final delta=%#v error=%v", final, err)
	}
}

func TestEventBrokerReplacesLiveAgentStateByStreamIdentity(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.Publish(Event{
		Kind: EventAgentState, SessionID: "session", RunID: "run", AgentID: "child",
		State: "running", Agent: &AgentStatePayload{Activity: "step-1", ToolCalls: 1},
	})
	broker.Publish(Event{
		Kind: EventAgentState, SessionID: "session", RunID: "run", AgentID: "child",
		State: "running", Agent: &AgentStatePayload{Activity: "step-2", ToolCalls: 2},
	})
	broker.Publish(Event{
		Kind: EventAgentState, SessionID: "session", RunID: "run", AgentID: "other",
		State: "running", Agent: &AgentStatePayload{Activity: "independent"},
	})
	broker.Publish(Event{Kind: EventRunFinished, SessionID: "session", RunID: "run"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, err := broker.Next(ctx)
	if err != nil || first.Kind != EventAgentState || first.AgentID != "child" {
		t.Fatalf("first event=%#v error=%v", first, err)
	}
	// Streaming roster updates for one child collapse to the newest snapshot.
	if first.Agent == nil || first.Agent.Activity != "step-2" || first.Agent.ToolCalls != 2 {
		t.Fatalf("replaced agent payload=%#v", first.Agent)
	}
	second, err := broker.Next(ctx)
	if err != nil || second.AgentID != "other" || second.Agent == nil || second.Agent.Activity != "independent" {
		t.Fatalf("independent agent event=%#v error=%v", second, err)
	}
	finished, err := broker.Next(ctx)
	if err != nil || finished.Kind != EventRunFinished {
		t.Fatalf("lifecycle event=%#v error=%v", finished, err)
	}
}

func TestEventBrokerKeepsDistinctAgentStateTransitionsOrdered(t *testing.T) {
	broker := newEventBroker(time.Hour)
	for _, state := range []string{"initializing", "queued", "running", "running", "running"} {
		broker.Publish(Event{
			Kind: EventAgentState, SessionID: "session", RunID: "run", AgentID: "child",
			State: state, Agent: &AgentStatePayload{Activity: state},
		})
	}
	broker.Publish(Event{Kind: EventRunFinished, SessionID: "session", RunID: "run"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var states []string
	for {
		event, err := broker.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == EventRunFinished {
			break
		}
		states = append(states, event.State)
	}
	// Lifecycle transitions all arrive; only the repeated running snapshots
	// collapse into the newest one.
	want := []string{"initializing", "queued", "running"}
	if len(states) != len(want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
	for index, state := range want {
		if states[index] != state {
			t.Fatalf("states = %v, want %v", states, want)
		}
	}
}

func TestEventBrokerKeepsTerminalAgentStatesOrdered(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.Publish(Event{
		Kind: EventAgentState, SessionID: "session", RunID: "run", AgentID: "child",
		State: "running", Agent: &AgentStatePayload{Activity: "reviewing"},
	})
	broker.Publish(Event{
		Kind: EventAgentState, SessionID: "session", RunID: "run", AgentID: "child",
		State: "completed", Agent: &AgentStatePayload{Activity: "done"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	running, err := broker.Next(ctx)
	if err != nil || running.State != "running" {
		t.Fatalf("running state=%#v error=%v", running, err)
	}
	completed, err := broker.Next(ctx)
	if err != nil || completed.State != "completed" {
		t.Fatalf("terminal state must not be replaced or dropped: %#v error=%v", completed, err)
	}
}

func TestTerminalEventReleasesRunAdmissionBeforeDelivery(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.mu.Lock()
	service.activeRun = "run"
	service.activeSession = "session"
	service.activeEnd = func() {}
	service.mu.Unlock()

	if !service.emitTerminal(context.Background(), Event{
		Kind: EventRunFailed, SessionID: "session", RunID: "run", State: "failed", Text: "stream interrupted",
	}) {
		t.Fatal("terminal event was not published")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventRunFailed {
		t.Fatalf("event kind = %s, want %s", event.Kind, EventRunFailed)
	}
	service.mu.Lock()
	activeRun, activeSession := service.activeRun, service.activeSession
	service.mu.Unlock()
	if activeRun != "" || activeSession != "" {
		t.Fatalf("terminal event was observable before admission release: run=%q session=%q", activeRun, activeSession)
	}
}

func TestEventBrokerDoesNotBlockProducerWhenConsumerIsIdle(t *testing.T) {
	broker := newEventBroker(time.Hour)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; index < 100_000; index++ {
			if broker.Publish(Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: "x"}) != eventPublishAccepted {
				return
			}
		}
		broker.Publish(Event{Kind: EventRunFinished, SessionID: "session", RunID: "run"})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("idle consumer backpressured event producer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	delta, err := broker.Next(ctx)
	if err != nil || delta.Kind != EventTextDelta || len(delta.Text) != 100_000 {
		t.Fatalf("delta kind=%s bytes=%d error=%v", delta.Kind, len(delta.Text), err)
	}
	finished, err := broker.Next(ctx)
	if err != nil || finished.Kind != EventRunFinished {
		t.Fatalf("finished=%#v error=%v", finished, err)
	}
}

func TestEventBrokerKeepsIndependentStreamsSeparate(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.Publish(Event{Kind: EventTextDelta, RunID: "run", AgentID: "first", Text: "a"})
	broker.Publish(Event{Kind: EventTextDelta, RunID: "run", AgentID: "second", Text: "b"})
	broker.Publish(Event{Kind: EventTextDelta, RunID: "run", AgentID: "first", Text: "c"})
	broker.Publish(Event{Kind: EventRunFinished, RunID: "run"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, err := broker.Next(ctx)
	if err != nil || first.AgentID != "first" || first.Text != "ac" {
		t.Fatalf("first stream=%#v error=%v", first, err)
	}
	second, err := broker.Next(ctx)
	if err != nil || second.AgentID != "second" || second.Text != "b" {
		t.Fatalf("second stream=%#v error=%v", second, err)
	}
	finished, err := broker.Next(ctx)
	if err != nil || finished.Kind != EventRunFinished {
		t.Fatalf("finished=%#v error=%v", finished, err)
	}
}

func TestEventBrokerCoalescesInterleavedStreamsBeforeHighWater(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.maxEvents = 2
	for _, event := range []Event{
		{Kind: EventTextDelta, SessionID: "session", RunID: "run", AgentID: "first", Text: "a"},
		{Kind: EventTextDelta, SessionID: "session", RunID: "run", AgentID: "second", Text: "b"},
		{Kind: EventTextDelta, SessionID: "session", RunID: "run", AgentID: "first", Text: "c"},
	} {
		if status := broker.Publish(event); status != eventPublishAccepted {
			t.Fatalf("publish status=%v", status)
		}
	}
	broker.mu.Lock()
	queued := len(broker.queue) - broker.head
	degraded := len(broker.degraded)
	broker.mu.Unlock()
	if queued != 2 || degraded != 0 {
		t.Fatalf("queued=%d degraded=%d, want two coalesced streams", queued, degraded)
	}
}

func TestEventBrokerMergesToolUpdatesWithoutLosingMessages(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.Publish(Event{Kind: EventToolUpdate, RunID: "run", ToolCallID: "tool", Text: "first", Data: map[string]string{"progress": "1"}})
	broker.Publish(Event{Kind: EventToolUpdate, RunID: "run", ToolCallID: "tool", Text: "second", State: "running", Data: map[string]string{"progress": "2"}})
	broker.Publish(Event{Kind: EventToolFinished, RunID: "run", ToolCallID: "tool"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	update, err := broker.Next(ctx)
	if err != nil || update.Text != "first\nsecond" || update.State != "running" || update.Data["progress"] != "2" {
		t.Fatalf("tool update=%#v error=%v", update, err)
	}
}

func TestEventBrokerCloseDrainsQueuedEventsThenReturnsEOF(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.Publish(Event{Kind: EventThinkingDelta, RunID: "run", Text: "thinking"})
	broker.Publish(Event{Kind: EventApprovalRequested, RunID: "run", ApprovalID: "approval"})
	broker.Close()
	if broker.Publish(Event{Kind: EventRunFinished, RunID: "run"}) != eventPublishClosed {
		t.Fatal("publish succeeded after close")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	thinking, err := broker.Next(ctx)
	if err != nil || thinking.Kind != EventThinkingDelta || thinking.Text != "thinking" {
		t.Fatalf("thinking=%#v error=%v", thinking, err)
	}
	approval, err := broker.Next(ctx)
	if err != nil || approval.Kind != EventApprovalRequested {
		t.Fatalf("approval=%#v error=%v", approval, err)
	}
	_, err = broker.Next(ctx)
	var eof ioEOF
	if !errors.As(err, &eof) {
		t.Fatalf("close error=%T %v", err, err)
	}
}

func TestEventBrokerCoalescingWindowBatchesFastDeltas(t *testing.T) {
	broker := newEventBroker(20 * time.Millisecond)
	broker.Publish(Event{Kind: EventTextDelta, RunID: "run", Text: "one"})
	broker.Publish(Event{Kind: EventTextDelta, RunID: "run", Text: "two"})
	started := time.Now()
	event, err := broker.Next(context.Background())
	if err != nil || event.Text != "onetwo" {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	if elapsed := time.Since(started); elapsed < 10*time.Millisecond || elapsed > time.Second {
		t.Fatalf("coalescing delay=%s", elapsed)
	}
}

func TestEventBrokerNextHonorsContextCancellation(t *testing.T) {
	broker := newEventBroker(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := broker.Next(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error=%v", err)
	}
	if strings.Contains(err.Error(), "event stream closed") {
		t.Fatalf("cancellation reported as close: %v", err)
	}
}

func TestEventBrokerBacklogCompactsProjectionAndRequestsFinalResync(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.maxBytes = 32
	status := broker.Publish(Event{Kind: EventTextDelta, RunID: "run", Text: strings.Repeat("x", 64)})
	if status != eventPublishAccepted {
		t.Fatalf("publish status=%v", status)
	}
	if status := broker.Publish(Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: strings.Repeat("y", 64)}); status != eventPublishAccepted {
		t.Fatalf("backlogged delta status=%v", status)
	}
	if status := broker.Publish(Event{Kind: EventRunFinished, SessionID: "session", RunID: "run"}); status != eventPublishAccepted {
		t.Fatalf("terminal status=%v", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	degraded := nextBrokerEvent(t, broker, ctx)
	if degraded.Kind != EventProjectionResync || degraded.State != "degraded" {
		t.Fatalf("degraded resync=%#v", degraded)
	}
	terminal := nextBrokerEvent(t, broker, ctx)
	if terminal.Kind != EventRunFinished {
		t.Fatalf("terminal=%#v", terminal)
	}
	final := nextBrokerEvent(t, broker, ctx)
	if final.Kind != EventProjectionResync || final.State != "final" {
		t.Fatalf("final resync=%#v", final)
	}
}

func nextBrokerEvent(t *testing.T, broker *eventBroker, ctx context.Context) Event {
	t.Helper()
	event, err := broker.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestEventBrokerBroadcastWakesConcurrentConsumers(t *testing.T) {
	broker := newEventBroker(time.Hour)
	results := make(chan Event, 2)
	errors := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for range 2 {
		go func() {
			event, err := broker.Next(ctx)
			if err != nil {
				errors <- err
				return
			}
			results <- event
		}()
	}
	time.Sleep(10 * time.Millisecond)
	broker.Publish(Event{Kind: EventRunStarted, RunID: "first"})
	broker.Publish(Event{Kind: EventRunStarted, RunID: "second"})
	seen := map[string]bool{}
	for range 2 {
		select {
		case err := <-errors:
			t.Fatal(err)
		case event := <-results:
			seen[event.RunID] = true
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("events=%v", seen)
	}
}

func TestEventBrokerResyncPreservesSubagentID(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.maxBytes = 32
	if status := broker.Publish(Event{
		Kind: EventThinkingDelta, SessionID: "session", RunID: "child-run", AgentID: "subagent_1",
		Text: strings.Repeat("x", 64),
	}); status != eventPublishAccepted {
		t.Fatalf("publish status=%v", status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event := nextBrokerEvent(t, broker, ctx)
	if event.Kind != EventProjectionResync || event.State != "degraded" ||
		event.AgentID != "subagent_1" || event.RunID != "child-run" {
		t.Fatalf("subagent resync=%#v", event)
	}
}

func TestProviderStreamContinuesAfterEventBacklogCompaction(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.events.maxBytes = 32
	sink := service.providerStreamSink("session", "run", "grok", "model", "high", "responses")
	err := sink.Emit(context.Background(), hyagent.Frame{Kind: hyagent.FrameText, Text: strings.Repeat("x", 64)})
	if err != nil {
		t.Fatalf("provider sink stopped on UI backlog: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := service.NextEvent(ctx)
	if err != nil || event.Kind != EventProjectionResync || event.State != "degraded" {
		t.Fatalf("projection resync=%#v error=%v", event, err)
	}
}

func TestProviderToolResultUsesBoundedUIProjection(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	sink := service.providerStreamSink("session", "run", "grok", "model", "high", "responses")
	result := tool.Result{
		ToolCallID: "tool", Name: "coding.read_file",
		Content:    strings.Repeat("x", maxToolRecordPreviewBytes*2),
		Structured: json.RawMessage(strings.Repeat("y", maxInlineToolRecordBytes*2)),
	}
	if err := sink.Emit(context.Background(), hyagent.Frame{Kind: hyagent.FrameToolResult, ToolResult: &result}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventToolFinished || len(event.Text) > maxToolRecordPreviewBytes+64 || event.Data["structured"] != "" || event.Data["projection_truncated"] != "true" {
		t.Fatalf("bounded tool projection=%#v", event)
	}
}

func TestProviderToolResultPublishesAfterRunCancellation(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	sink := service.providerStreamSink("session", "run", "grok", "model", "high", "responses")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := tool.Result{ToolCallID: "tool", Name: "coding.read_file", Content: "done"}
	if err := sink.Emit(ctx, hyagent.Frame{Kind: hyagent.FrameToolResult, ToolResult: &result}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(context.Background())
	if err != nil || event.Kind != EventToolFinished || event.State != "completed" {
		t.Fatalf("tool result after cancellation=%#v error=%v", event, err)
	}
}

func TestEventBrokerAccountsTinyCoalescedFragmentsByRetainedCapacity(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.maxBytes = 128
	for index := 0; index < 1024; index++ {
		if status := broker.Publish(Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: "x"}); status != eventPublishAccepted {
			t.Fatalf("fragment %d status=%v", index, status)
		}
	}
	broker.mu.Lock()
	queued := len(broker.queue) - broker.head
	broker.mu.Unlock()
	if queued > 2 {
		t.Fatalf("replaceable projection grew without bound: %d queued events", queued)
	}
	broker.Publish(Event{Kind: EventRunFinished, SessionID: "session", RunID: "run"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := broker.Next(ctx)
	if err != nil || event.Kind != EventProjectionResync {
		t.Fatalf("projection resync=%#v error=%v", event, err)
	}
}

func TestEventBrokerPreservesOversizedLifecycleSnapshot(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.maxBytes = 256
	status := broker.Publish(Event{
		Kind: EventAgentDetail, AgentID: "agent",
		AgentBlocks: []AgentTranscriptBlock{{Content: strings.Repeat("x", 1024)}},
	})
	if status != eventPublishAccepted {
		t.Fatalf("structured payload status=%v", status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := broker.Next(ctx)
	if err != nil || len(event.AgentBlocks) != 1 || len(event.AgentBlocks[0].Content) != 1024 {
		t.Fatalf("structured event=%#v error=%v", event, err)
	}
}

func TestTeamAnswerBacklogDoesNotChangeSuccessfulOutcome(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.events.maxBytes = 32
	if !service.emit(context.Background(), Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: strings.Repeat("x", 64)}) {
		t.Fatal("UI backlog rejected replaceable projection")
	}
	execution := agentservice.TeamExecution{Result: agentruntime.TeamExecutionResult{State: agentruntime.TeamState{Tasks: []agentruntime.Task{{
		Result: &agentruntime.TypedReport{Structured: map[string]any{"answer": "team answer"}},
	}}}}}
	service.finishProviderTeam(context.Background(), "session", "run", "goal", session.TodoList{}, execution, nil)
	service.events.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := false
	resynced := false
	for {
		event, err := service.NextEvent(ctx)
		if err != nil {
			var eof ioEOF
			if !errors.As(err, &eof) {
				t.Fatal(err)
			}
			break
		}
		finished = finished || event.Kind == EventRunFinished
		resynced = resynced || event.Kind == EventProjectionResync && event.State == "final"
	}
	if !finished || !resynced {
		t.Fatalf("finished=%t resynced=%t", finished, resynced)
	}
}

func TestApprovalRequestRemainsDeliverableAfterProjectionCompaction(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.events.maxBytes = 32
	if !service.emit(context.Background(), Event{Kind: EventTextDelta, SessionID: "session", RunID: "run", Text: strings.Repeat("x", 64)}) {
		t.Fatal("UI backlog rejected replaceable projection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := service.awaitTeamApproval(
		ctx, "session", "run", "goal",
		tool.Call{ID: "call", Name: "coding.write_file"},
		agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectWrite, RequiresApproval: true},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("approval error=%v", err)
	}
	service.events.Close()
	for _, event := range drainBrokerEvents(t, service.events) {
		if event.Kind == EventApprovalRequested && event.ApprovalID != "" {
			return
		}
	}
	t.Fatal("approval request was lost behind replaceable projection")
}

func drainBrokerEvents(t *testing.T, broker *eventBroker) []Event {
	t.Helper()
	var events []Event
	for {
		event, err := broker.Next(context.Background())
		if err == nil {
			events = append(events, event)
			continue
		}
		var eof ioEOF
		if !errors.As(err, &eof) {
			t.Fatal(err)
		}
		return events
	}
}
