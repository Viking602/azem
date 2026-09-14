package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

func TestGeneratedLoopDetectsShortRepeatedSentences(t *testing.T) {
	var prefix strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&prefix, "Considering candidate %d with a different result and implementation detail. ", i)
	}
	for _, phrase := range []string{
		"Let me write it now. 1m23s is plenty for this. ",
		"Let me try to implement a 2x2 closed form case for n=2. ",
	} {
		if !detectGeneratedLoop(prefix.String() + strings.Repeat(phrase, 5)) {
			t.Errorf("missed short-sentence loop: %q", phrase)
		}
		if detectGeneratedLoop(prefix.String() + strings.Repeat(phrase, 3)) {
			t.Errorf("interrupted fewer than four repetitions: %q", phrase)
		}
	}
	if detectGeneratedLoop(prefix.String()) {
		t.Fatal("distinct candidate analysis was mistaken for a loop")
	}
}

func TestThinkingLoopGuardInterruptsAndContinues(t *testing.T) {
	repeated := strings.Repeat("concrete repeated reasoning segment with enough technical words to represent a stalled model loop and no actual progress. ", 8)
	repeated += repeated
	control := newTurnControlQueue()
	guard := newModelLoopGuard(config.LoopGuardConfig{
		ThinkingEnabled: true, AssistantTextEnabled: true, ToolCallEnabled: true, ToolCallThreshold: 5,
	}, control, nil, "session", "run")
	driver := &compactionTestDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventThinkingDelta, Thinking: repeated}},
		{{Kind: hyprovider.EventTextDelta, Text: "corrected answer"}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
	}}
	var stalled *loopGuardStalledStream
	engine := bindTurnControl(hyagent.Engine{
		Provider: driver,
		ModelInterceptor: hyprovider.StreamInterceptorFunc(func(ctx context.Context, next hyprovider.Driver, request hyprovider.Request) (hyprovider.Stream, error) {
			stream, err := next.Stream(ctx, request)
			if err == nil && len(driver.requests) == 1 {
				stalled = &loopGuardStalledStream{Stream: stream, ctx: ctx}
				return stalled, nil
			}
			return stream, err
		}),
		Model:      "model",
		LoopPolicy: hyagent.LoopPolicy{MaxIterations: 3},
	}, control, time.Time{})
	engine.Hooks = engine.Hooks.Prepend(guard)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := engine.Run(ctx, hyagent.Request{Prompt: "solve"}, hyagent.OutputPolicy{})
	if result.Failure != nil {
		t.Fatal(result.Failure)
	}
	encoded, _ := json.Marshal(result.Messages)
	if strings.Contains(string(encoded), "stalled model loop") || !strings.Contains(string(encoded), "Host loop guard: thinking-loop") || !strings.Contains(string(encoded), "corrected answer") {
		t.Fatalf("thinking-loop recovery = %s", encoded)
	}
	if !stalled.closed || ctx.Err() != nil || len(driver.requests) != 2 || result.Steps[0].ModelCall.StopReason != hyprovider.StopReasonAborted {
		t.Fatalf("stream was not interrupted and retried within the same live run: %+v", result)
	}
}

func TestProviderRuntimeLoopRecoveryKeepsEvidenceAndSettlesExhaustion(t *testing.T) {
	for _, test := range []struct {
		name, delta string
		loops       int
	}{
		{"thinking-recovery", "response.reasoning_summary_text.delta", 1},
		{"text-recovery", "response.output_text.delta", 1},
		{"exhaustion", "response.reasoning_summary_text.delta", 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			loops := test.loops
			done := make(chan struct{})
			defer close(done)
			phrase := "Looping candidate: create the branch, implement the parser, add tests, update documentation, and commit everything. "
			harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
				if call == 1 {
					writeProviderToolCall(writer, "loop-read", "read-1", "coding.read_file", `{"path":"evidence.txt"}`)
					return
				}
				if call > 2 {
					assertLoopRetryContext(t, body, phrase)
				}
				if call <= loops+1 {
					fmt.Fprintf(writer, "data: {\"type\":%q,\"delta\":%q}\n\n", test.delta, strings.Repeat(phrase, 20))
					writer.(http.Flusher).Flush()
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("host failed to interrupt repeated generation")
					}
					return
				}
				if loops == 4 || call != 3 {
					t.Errorf("unexpected provider call after retry limit: %d", call)
				}
				writeProviderText(writer, "loop-finish", "Finished using the observed evidence.")
			})
			if err := os.WriteFile(filepath.Join(harness.workspace, "evidence.txt"), []byte("stable-tool-evidence\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "loop-e2e", Prompt: "Read evidence.txt and explain it.", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
			if err != nil {
				t.Fatal(err)
			}
			terminal := waitForLoopTerminal(t, harness.service, runID)
			wantCalls, wantTerminal := int32(3), EventRunFinished
			if loops == 4 {
				wantCalls, wantTerminal = 5, EventRunFailed
				if !strings.Contains(terminal.Text, "persisted after three guarded retries") {
					t.Errorf("loop failure was obscured: %s", terminal.Text)
				}
			}
			if harness.calls.Load() != wantCalls || terminal.Kind != wantTerminal {
				t.Errorf("calls=%d terminal=%s; want %d %s", harness.calls.Load(), terminal.Kind, wantCalls, wantTerminal)
			}
			assertLoopAttemptEvidence(t, harness, phrase, loops)
		})
	}
}

func assertLoopRetryContext(t *testing.T, body, phrase string) {
	t.Helper()
	if strings.Contains(body, phrase) || !strings.Contains(body, "stable-tool-evidence") || !strings.Contains(body, "Host loop guard") {
		t.Error("retry retained rejected repetition or lost prior tool evidence/control")
	}
}

func waitForLoopTerminal(t *testing.T, service *Service, runID string) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var terminal Event
	for {
		event, err := service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID == runID && (event.Kind == EventRunFinished || event.Kind == EventRunFailed || event.Kind == EventRecoveryState) {
			terminal = event
			break
		}
	}
	return terminal
}

func assertLoopAttemptEvidence(t *testing.T, harness skillRuntimeHarness, phrase string, loops int) {
	t.Helper()
	var unknownEffects, unknownUsage int
	if err := harness.store.DB().QueryRowContext(t.Context(), `SELECT count(*) FROM agent_effect_attempts WHERE status IN ('unknown','running')`).Scan(&unknownEffects); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.DB().QueryRowContext(t.Context(), `SELECT count(*) FROM provider_requests WHERE status='unknown'`).Scan(&unknownUsage); err != nil {
		t.Fatal(err)
	}
	if unknownEffects != 0 || unknownUsage != loops {
		t.Errorf("durable unknown effects=%d, unknown physical usage=%d; want 0 and %d", unknownEffects, unknownUsage, loops)
	}
	if retained := retainedLoopAttempts(t, harness, phrase); retained != loops {
		t.Fatalf("original looping attempt evidence: retained=%d want=%d", retained, loops)
	}
}

func retainedLoopAttempts(t *testing.T, harness skillRuntimeHarness, phrase string) int {
	t.Helper()
	rows, err := harness.store.DB().QueryContext(t.Context(), `SELECT attempt_inline,attempt_digest FROM agent_effect_attempts WHERE kind='model'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	retained := 0
	for rows.Next() {
		var data []byte
		var digest string
		if err := rows.Scan(&data, &digest); err != nil {
			t.Fatal(err)
		}
		if digest != "" {
			data, err = harness.store.Blobs().Get(t.Context(), digest)
			if err != nil {
				t.Fatal(err)
			}
		}
		var stored struct{ Attempt struct{ Payload []byte } }
		if err := json.Unmarshal(data, &stored); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(stored.Attempt.Payload), phrase) {
			retained++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return retained
}

// The original regression test ended the stream itself, masking the missing
// host interruption. This provider never finishes after its first delta.
type loopGuardStalledStream struct {
	hyprovider.Stream
	ctx          context.Context
	read, closed bool
}

func (s *loopGuardStalledStream) Recv() (hyprovider.Event, error) {
	if s.read {
		<-s.ctx.Done()
		return hyprovider.Event{}, s.ctx.Err()
	}
	s.read = true
	return s.Stream.Recv()
}

func (s *loopGuardStalledStream) Close() error {
	s.closed = true
	return s.Stream.Close()
}

func TestStreamControlPreservesToolAndTerminalBoundaries(t *testing.T) {
	for _, first := range []hyprovider.EventKind{hyprovider.EventThinkingDelta, hyprovider.EventTextDelta, hyprovider.EventToolCallDelta, hyprovider.EventToolCall, hyprovider.EventDone, hyprovider.EventError} {
		for _, interrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/interrupt=%v", first, interrupt), func(t *testing.T) {
				queue := newTurnControlQueue()
				stream := &turnControlStream{ctx: t.Context(), queue: queue, Stream: hyprovider.NewSliceStream([]hyprovider.Event{
					{Kind: first}, {Kind: hyprovider.EventTextDelta, Text: "original next event"},
				})}
				if _, err := stream.Recv(); err != nil {
					t.Fatal(err)
				}
				if err := queue.Enqueue(turnControlMessage{Kind: turnControlSteer, Message: message.NewText(message.RoleSystem, "continue"), InterruptStream: interrupt}); err != nil {
					t.Fatal(err)
				}
				event, err := stream.Recv()
				wantAbort := interrupt && (first == hyprovider.EventThinkingDelta || first == hyprovider.EventTextDelta)
				if err != nil || (event.StopReason == hyprovider.StopReasonAborted) != wantAbort {
					t.Fatalf("next event=%+v, err=%v, wantAbort=%v", event, err, wantAbort)
				}
				if !wantAbort && event.Text != "original next event" {
					t.Fatalf("provider event was changed: %+v", event)
				}
			})
		}
	}
}

func TestToolCallLoopGuardCanonicalizesAndExemptsPolling(t *testing.T) {
	control := newTurnControlQueue()
	guard := newModelLoopGuard(config.LoopGuardConfig{
		ToolCallEnabled: true, ToolCallThreshold: 3, ToolCallExemptTools: []string{"hub"},
	}, control, nil, "session", "run")
	calls := []message.ToolCall{
		{ID: "one", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"a.go","intent":"first"}`)},
		{ID: "two", Name: "coding.read_file", Arguments: json.RawMessage(`{"intent":"second","path":"a.go"}`)},
		{ID: "three", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
	}
	for index, call := range calls {
		err := guard.OnEvent(context.Background(), hyprovider.Event{Kind: hyprovider.EventToolCall, ToolCall: &call})
		if index < 2 && err != nil {
			t.Fatalf("tool loop tripped early at %d: %v", index, err)
		}
		if index == 2 && err != nil {
			t.Fatalf("tool loop queue failed: %v", err)
		}
	}
	controls, err := control.Drain(context.Background(), turnControlBeforeModel)
	if err != nil || len(controls) != 1 || !strings.Contains(controls[0].Message.Text, "identical coding.read_file call") {
		t.Fatalf("tool loop control = %#v, %v", controls, err)
	}
	for range 10 {
		call := message.ToolCall{ID: "poll", Name: "hub", Arguments: json.RawMessage(`{"op":"wait"}`)}
		if err := guard.OnEvent(context.Background(), hyprovider.Event{Kind: hyprovider.EventToolCall, ToolCall: &call}); err != nil {
			t.Fatalf("exempt polling tool tripped loop guard: %v", err)
		}
	}
}

func TestUnexpectedStopGuardRetriesThenFailsClosed(t *testing.T) {
	guard := newUnexpectedStopGuard(config.LoopGuardConfig{UnexpectedStop: "mechanical", UnexpectedStopRetries: 2})
	for attempt := 0; attempt < 2; attempt++ {
		result, err := guard.Check(context.Background(), hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "I will")})
		if err != nil || result.Action != hyagent.OutputGuardrailActionRetry || len(result.RetryMessages) != 1 {
			t.Fatalf("unexpected-stop retry %d = %#v, %v", attempt, result, err)
		}
	}
	blocked, err := guard.Check(context.Background(), hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "I will")})
	if err != nil || blocked.Action != hyagent.OutputGuardrailActionBlock {
		t.Fatalf("unexpected-stop exhaustion = %#v, %v", blocked, err)
	}
	complete := newUnexpectedStopGuard(config.LoopGuardConfig{UnexpectedStop: "mechanical", UnexpectedStopRetries: 2})
	allowed, err := complete.Check(context.Background(), hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "Completed with verified evidence.")})
	if err != nil || allowed.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("complete answer blocked = %#v, %v", allowed, err)
	}
	if !isUnexpectedStopCandidate(message.Message{Role: message.RoleAssistant, Thinking: "unfinished private reasoning"}) ||
		!isUnexpectedStopCandidate(message.NewText(message.RoleAssistant, "```go\nfunc incomplete()")) {
		t.Fatal("thinking-only or unclosed-code stop was not detected")
	}
}

func TestProviderRuntimeContinuesUnexpectedStop(t *testing.T) {
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			writeProviderText(writer, "unexpected-1", "I will")
		case 2:
			if !strings.Contains(body, "provider stopped unexpectedly") {
				t.Errorf("unexpected-stop continuation missing: %s", body)
			}
			writeProviderText(writer, "unexpected-2", "Completed after provider-neutral continuation.")
		default:
			t.Errorf("unexpected provider call %d", call)
			writeProviderText(writer, "unexpected-extra", "unexpected")
		}
	})
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "unexpected-stop", Prompt: "Finish the answer", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if harness.calls.Load() != 2 {
		t.Fatalf("unexpected-stop calls = %d, want 2", harness.calls.Load())
	}
	projection, err := harness.service.sessions.LoadProjection(context.Background(), "unexpected-stop")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) == 0 || projection.Blocks[len(projection.Blocks)-1].Content != "Completed after provider-neutral continuation." {
		t.Fatalf("unexpected-stop projection = %#v", projection.Blocks)
	}
}
