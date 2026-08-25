package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/hook"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

func TestThinkingLoopGuardInterruptsAndContinues(t *testing.T) {
	repeated := strings.Repeat("concrete repeated reasoning segment with enough technical words to represent a stalled model loop and no actual progress. ", 8)
	repeated += repeated
	control := hyagent.NewControlQueue()
	guard := newModelLoopGuard(config.LoopGuardConfig{
		ThinkingEnabled: true, AssistantTextEnabled: true, ToolCallEnabled: true, ToolCallThreshold: 5,
	}, control, nil, "session", "run")
	engine := hyagent.Engine{
		Provider: &compactionTestDriver{streams: [][]hyprovider.Event{
			{{Kind: hyprovider.EventThinkingDelta, Thinking: repeated}},
			{{Kind: hyprovider.EventTextDelta, Text: "corrected answer"}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
		}},
		Hooks:   hook.NewChain(guard),
		Control: control,
	}
	output, err := engine.RunMessages(context.Background(), hyagent.LoopInput{
		Model: "model", Messages: []message.Message{message.NewText(message.RoleUser, "solve")}, MaxIterations: 3, Control: control,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(output.Messages)
	if strings.Contains(string(encoded), "stalled model loop") || !strings.Contains(string(encoded), "Host loop guard: thinking-loop") || !strings.Contains(string(encoded), "corrected answer") {
		t.Fatalf("thinking-loop recovery = %s", encoded)
	}
}

func TestToolCallLoopGuardCanonicalizesAndExemptsPolling(t *testing.T) {
	control := hyagent.NewControlQueue()
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
		if index == 2 {
			var interrupted *hyagent.StreamRuleInterruptError
			if !errors.As(err, &interrupted) {
				t.Fatalf("tool loop did not interrupt: %v", err)
			}
		}
	}
	controls, err := control.Drain(context.Background(), hyagent.TurnBoundaryBeforeModel)
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
