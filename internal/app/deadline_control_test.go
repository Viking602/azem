package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

func TestDeadlineWrapUpTimingAndSinglePrivateTail(t *testing.T) {
	for _, budget := range []time.Duration{20 * time.Second, 869 * time.Second} {
		queue := newTurnControlQueue()
		deadline := time.Now().Add(budget)
		engine := bindTurnControl(hyagent.Engine{}, queue, deadline)
		runtime := engine.OutputGuardrails[0].(*turnControlRuntime)
		wantReserve := 4 * time.Second
		if budget > time.Minute {
			wantReserve = 90 * time.Second
		}
		reserve := deadline.Sub(runtime.wrapUpAt)
		if reserve > wantReserve || reserve < wantReserve-time.Millisecond {
			t.Fatalf("budget %s reserved %s, want %s", budget, reserve, wantReserve)
		}
		prefix := []message.Message{message.NewText(message.RoleSystem, "stable rules"), message.NewText(message.RoleUser, "save the result")}
		request := hyprovider.Request{Messages: append([]message.Message(nil), prefix...)}
		if err := runtime.BeforeModelCall(t.Context(), &request); err != nil || len(request.Messages) != len(prefix) {
			t.Fatalf("early reminder: messages=%v error=%v", request.Messages, err)
		}
		runtime.wrapUpAt = time.Now().Add(-time.Second)
		for _, kind := range []hyprovider.EventKind{hyprovider.EventTextDelta, hyprovider.EventToolCallDelta, hyprovider.EventDone, hyprovider.EventError} {
			if err := runtime.OnEvent(t.Context(), hyprovider.Event{Kind: kind}); err != nil || len(queue.pending) != 0 {
				t.Fatalf("interrupted %s: pending=%v error=%v", kind, queue.pending, err)
			}
		}
		for range 2 {
			if err := runtime.OnEvent(t.Context(), hyprovider.Event{Kind: hyprovider.EventThinkingDelta}); err != nil {
				t.Fatal(err)
			}
		}
		if len(queue.pending) != 1 || !queue.pending[0].InterruptStream {
			t.Fatalf("pending controls=%v", queue.pending)
		}
		if err := runtime.BeforeModelCall(t.Context(), &request); err != nil || len(request.Messages) != 3 {
			t.Fatalf("deadline tail: messages=%v error=%v", request.Messages, err)
		}
		for i, before := range prefix {
			if request.Messages[i].Text != before.Text || request.Messages[i].Role != before.Role {
				t.Fatal("deadline control rewrote the existing cache prefix")
			}
		}
		if agentruntime.MessageVisibilityOf(request.Messages[2]) != agentruntime.MessageVisibilityPrivate {
			t.Fatal("deadline reminder must remain private")
		}
		if err := queue.Acknowledge(t.Context(), []string{queue.pending[0].ID}); err != nil {
			t.Fatal(err)
		}
		if err := runtime.BeforeModelCall(t.Context(), &request); err != nil || len(request.Messages) != 3 {
			t.Fatalf("repeated reminder: messages=%v error=%v", request.Messages, err)
		}
	}
}

func TestDeadlineWrapUpSkipsUnboundedExpiredAndCancelledRuns(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		ctx      context.Context
		deadline time.Time
	}{
		{t.Context(), time.Time{}},
		{t.Context(), time.Now().Add(-time.Second)},
		{cancelled, time.Now().Add(time.Minute)},
	} {
		runtime := &turnControlRuntime{queue: newTurnControlQueue(), deadlineAt: test.deadline, wrapUpAt: time.Now().Add(-time.Second)}
		if err := runtime.OnEvent(test.ctx, hyprovider.Event{Kind: hyprovider.EventThinkingDelta}); err != nil || len(runtime.queue.pending) != 0 {
			t.Fatalf("unexpected deadline control: pending=%v error=%v", runtime.queue.pending, err)
		}
	}
}

func TestProviderRuntimeDeadlineInterruptsThinkingAndSavesDeliverable(t *testing.T) {
	continued := make(chan struct{})
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			// Keep generating distinct reasoning until the host closes this stream.
			// A provider completion here would mask the missing deadline control.
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			limit := time.NewTimer(25 * time.Second)
			defer limit.Stop()
			for step := 0; ; step++ {
				select {
				case <-continued:
					return
				case <-limit.C:
					return
				case <-ticker.C:
					fmt.Fprintf(writer, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":%q}\n\n", fmt.Sprintf("Considering optimization candidate %d. ", step))
					writer.(http.Flusher).Flush()
				}
			}
		case 2:
			close(continued)
			if strings.Count(body, "[Host deadline: wrap up]") != 1 || !strings.Contains(body, "Save the current best deliverable") {
				t.Error("retry did not receive exactly one deadline reminder")
			}
			writeProviderToolCall(writer, "deadline-write", "write-1", "coding.write_file", `{"path":"answer.txt","content":"candidate\n"}`)
		case 3:
			// Verification must still reject a premature final answer after mutation.
			writeProviderText(writer, "deadline-premature", "Saved and verified.")
		case 4:
			if !strings.Contains(body, "git diff --check") {
				t.Error("deadline reminder bypassed verification")
			}
			fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"deadline-read-item\",\"call_id\":\"read-1\",\"name\":\"coding.read_file\",\"arguments\":\"{\\\"path\\\":\\\"answer.txt\\\"}\"}}\n\n")
			writeProviderToolCall(writer, "deadline-check", "check-1", "coding.shell", `{"command":"git diff --check -- answer.txt","wall_clock_seconds":1}`)
		case 5:
			if strings.Count(body, "[Host deadline: wrap up]") != 1 {
				t.Error("deadline reminder was duplicated across model calls")
			}
			writeProviderText(writer, "deadline-finish", "Saved and verified.")
		default:
			t.Errorf("unexpected provider call %d", call)
			writeProviderText(writer, "deadline-extra", "Unexpected call.")
		}
	})
	if output, err := exec.Command("git", "init", "-q", harness.workspace).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	harness.service.providers.cfg.Agents.Main.MaxWallClockDuration = 20 * time.Second
	if err := harness.service.ExecuteAction(t.Context(), Action{Kind: ActionSetApprovalMode, Target: string(ApprovalModeYolo)}); err != nil {
		t.Fatal(err)
	}
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "deadline-e2e", Prompt: "Save a candidate in answer.txt and verify the file.",
		Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if content, err := os.ReadFile(filepath.Join(harness.workspace, "answer.txt")); err != nil || string(content) != "candidate\n" {
		t.Fatalf("deliverable=%q error=%v", content, err)
	}
	var total, unknown, completed int
	err = harness.store.DB().QueryRowContext(context.Background(), `SELECT count(*), sum(status='unknown'), sum(status='completed') FROM provider_requests WHERE run_id=?`, runID).Scan(&total, &unknown, &completed)
	if err != nil || total != 5 || unknown != 1 || completed != 4 {
		t.Fatalf("request evidence: total=%d unknown=%d completed=%d error=%v", total, unknown, completed, err)
	}
}
