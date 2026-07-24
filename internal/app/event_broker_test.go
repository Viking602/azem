package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/multiagent"
	"github.com/Viking602/go-hydaelyn/stream"
	"github.com/Viking602/go-hydaelyn/tool"
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
	if err != nil || first.AgentID != "first" || first.Text != "a" {
		t.Fatalf("first stream=%#v error=%v", first, err)
	}
	second, err := broker.Next(ctx)
	if err != nil || second.AgentID != "second" || second.Text != "b" {
		t.Fatalf("second stream=%#v error=%v", second, err)
	}
	third, err := broker.Next(ctx)
	if err != nil || third.AgentID != "first" || third.Text != "c" {
		t.Fatalf("third stream=%#v error=%v", third, err)
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

func TestEventBrokerOverloadPreservesAcceptedPrefixAndTerminalEvent(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.maxBytes = 32
	status := broker.Publish(Event{Kind: EventTextDelta, RunID: "run", Text: strings.Repeat("x", 64)})
	if status != eventPublishOverloaded {
		t.Fatalf("overload status=%v", status)
	}
	if status := broker.Publish(Event{Kind: EventTextDelta, RunID: "run", Text: "not accepted"}); status != eventPublishOverloaded {
		t.Fatalf("post-overload delta status=%v", status)
	}
	if status := broker.Publish(Event{Kind: EventRunFailed, RunID: "run", Text: errEventBrokerOverloaded.Error()}); status != eventPublishAccepted {
		t.Fatalf("terminal status=%v", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	delta, err := broker.Next(ctx)
	if err != nil || delta.Text != strings.Repeat("x", 64) {
		t.Fatalf("accepted prefix=%#v error=%v", delta, err)
	}
	terminal, err := broker.Next(ctx)
	if err != nil || terminal.Kind != EventRunFailed || terminal.Text != errEventBrokerOverloaded.Error() {
		t.Fatalf("terminal=%#v error=%v", terminal, err)
	}
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

func TestProviderStreamStopsExplicitlyAfterEventBacklogOverload(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.events.maxBytes = 32
	sink := service.providerStreamSink("session", "run", "grok", "model", "high", "responses")
	err := sink.Emit(context.Background(), stream.Frame{Kind: stream.FrameText, Text: strings.Repeat("x", 64)})
	if !errors.Is(err, errEventBrokerOverloaded) {
		t.Fatalf("provider sink error=%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := service.NextEvent(ctx)
	if err != nil || event.Kind != EventTextDelta || event.Text != strings.Repeat("x", 64) {
		t.Fatalf("accepted provider prefix=%#v error=%v", event, err)
	}
}

func TestEventBrokerAccountsTinyCoalescedFragmentsByRetainedCapacity(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.maxBytes = 128
	acceptedBytes := 0
	for {
		status := broker.Publish(Event{Kind: EventTextDelta, RunID: "run", Text: "x"})
		acceptedBytes++
		if status == eventPublishOverloaded {
			break
		}
		if acceptedBytes > 1024 {
			t.Fatal("tiny fragments bypassed byte high-water mark")
		}
	}
	broker.Publish(Event{Kind: EventRunFailed, RunID: "run", Text: errEventBrokerOverloaded.Error()})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	delta, err := broker.Next(ctx)
	if err != nil || delta.Text != strings.Repeat("x", acceptedBytes) {
		t.Fatalf("accepted fragment prefix bytes=%d event=%#v error=%v", acceptedBytes, delta, err)
	}
}

func TestEventBrokerAccountsStructuredPayloads(t *testing.T) {
	broker := newEventBroker(time.Hour)
	broker.maxBytes = 256
	status := broker.Publish(Event{
		Kind: EventAgentDetail, AgentID: "agent",
		AgentBlocks: []AgentTranscriptBlock{{Content: strings.Repeat("x", 1024)}},
	})
	if status != eventPublishOverloaded {
		t.Fatalf("structured payload status=%v", status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := broker.Next(ctx)
	if err != nil || len(event.AgentBlocks) != 1 || len(event.AgentBlocks[0].Content) != 1024 {
		t.Fatalf("structured event=%#v error=%v", event, err)
	}
}

func TestTeamAnswerBacklogFailureCannotReportSuccess(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.events.maxBytes = 32
	if service.emit(context.Background(), Event{Kind: EventTextDelta, RunID: "run", Text: strings.Repeat("x", 64)}) {
		t.Fatal("backlog setup did not overload broker")
	}
	execution := agentservice.TeamExecution{Result: multiagent.DriveResult{State: multiagent.TeamState{Tasks: []api.Task{{
		Result: &api.TypedReport{Structured: map[string]any{"answer": "team answer"}},
	}}}}}
	service.finishProviderTeam(context.Background(), "session", "run", "goal", session.TodoList{}, execution, nil)
	service.events.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := false
	failed := false
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
		failed = failed || event.Kind == EventRunFailed && event.Text == errEventBrokerOverloaded.Error()
	}
	if finished || !failed {
		t.Fatalf("finished=%t failed=%t", finished, failed)
	}
}

func TestApprovalWaitFailsImmediatelyWhenRequestCannotBeDelivered(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.events.maxBytes = 32
	if service.emit(context.Background(), Event{Kind: EventTextDelta, RunID: "run", Text: strings.Repeat("x", 64)}) {
		t.Fatal("backlog setup did not overload broker")
	}
	started := time.Now()
	_, err := service.awaitTeamApproval(
		context.Background(), "session", "run", "goal",
		tool.Call{ID: "call", Name: "coding.write_file"},
		tool.Definition{Name: "coding.write_file", EffectType: tool.EffectWrite},
	)
	if !errors.Is(err, errEventBrokerOverloaded) {
		t.Fatalf("approval error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("approval waited despite invisible request: %s", elapsed)
	}
}
