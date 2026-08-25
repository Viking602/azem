package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	hyagent "github.com/Viking602/venat/agent"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

func TestPrewalkSwitchesOnceAfterTodoAndFirstMutation(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "prewalk", Title: "Prewalk"}); err != nil {
		t.Fatal(err)
	}
	initial := &compactionTestDriver{streams: [][]hyprovider.Event{{{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}}}}
	target := &compactionTestDriver{streams: [][]hyprovider.Event{{{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}}}}
	switcher := &prewalkSwitchDriver{initial: initial, target: target, targetProvider: "chatgpt", targetModel: "fast-model", targetReasoning: "low"}
	control := hyagent.NewControlQueue()
	hook := &prewalkHook{driver: switcher, control: control, store: sessions, sessionID: "prewalk", runID: "run"}

	stream, err := switcher.Stream(ctx, hyprovider.Request{Model: "large-model"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()
	_ = stream.Close()
	if err := hook.AfterToolCall(ctx, &tool.Result{Name: "coding.read_file", Content: "read"}); err != nil || switcher.isSwitched() {
		t.Fatalf("read-only prewalk changed model: switched=%v err=%v", switcher.isSwitched(), err)
	}
	if err := hook.AfterToolCall(ctx, &tool.Result{Name: "coding.write_file", Content: "written"}); err != nil || switcher.isSwitched() {
		t.Fatalf("prewalk switched before todo: switched=%v err=%v", switcher.isSwitched(), err)
	}
	planNudge, err := control.Drain(ctx, hyagent.TurnBoundaryBeforeModel)
	if err != nil || len(planNudge) != 1 || !strings.Contains(planNudge[0].Message.Text, "durable implementation plan") {
		t.Fatalf("prewalk plan nudge = %#v, %v", planNudge, err)
	}

	todo := &todoDriver{sessionID: "prewalk", store: sessions}
	result, err := todo.Execute(ctx, tool.Call{ID: "todo", Name: "todo", Arguments: json.RawMessage(`{
		"op":"init","goal":"implement safely","phases":[{"title":"Build","items":[{"content":"Implement change"},{"content":"Verify change"}]}]
	}`)}, nil)
	if err != nil || result.IsError {
		t.Fatalf("todo init = %#v, %v", result, err)
	}
	if err := hook.AfterToolCall(ctx, &tool.Result{Name: "coding.write_file", Content: "written"}); err != nil || !switcher.isSwitched() {
		t.Fatalf("prewalk did not switch after mutation: switched=%v err=%v", switcher.isSwitched(), err)
	}
	checklist, err := control.Drain(ctx, hyagent.TurnBoundaryAfterTools)
	if err != nil || len(checklist) != 1 || !strings.Contains(checklist[0].Message.Text, "consistency/scope/verification") && !strings.Contains(checklist[0].Message.Text, "every matching callsite") {
		t.Fatalf("prewalk checklist = %#v, %v", checklist, err)
	}
	stream, err = switcher.Stream(ctx, hyprovider.Request{Model: "large-model", Metadata: map[string]string{"reasoning_effort": "high"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()
	_ = stream.Close()
	if len(initial.requests) != 1 || len(target.requests) != 1 || target.requests[0].Model != "fast-model" || target.requests[0].Metadata["reasoning_effort"] != "low" {
		t.Fatalf("prewalk requests initial=%#v target=%#v", initial.requests, target.requests)
	}
	artifact, err := sessions.LoadLatestArtifactByKind(ctx, "prewalk", prewalkArtifactKind)
	if err != nil || !strings.Contains(string(artifact.Payload), "fast-model") {
		t.Fatalf("prewalk handoff artifact = %#v, %v", artifact, err)
	}
}

func TestPlanYoloAutomaticallyStartsImplementationRun(t *testing.T) {
	ctx := context.Background()
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			if !strings.Contains(body, "Plan-yolo mode") || !strings.Contains(body, "submit_plan") {
				t.Errorf("plan-yolo planning context missing: %s", body)
			}
			writeProviderToolCall(writer, "plan-yolo-1", "submit-plan", submitPlanToolName, `{"title":"Automatic plan","plan":"1. Inspect the target.\n2. Implement the change.\n3. Run the behavioral check."}`)
		case 2:
			if !strings.Contains(body, "Automatic plan") || !strings.Contains(body, "Implement the approved plan") && !strings.Contains(body, "implement it exactly") {
				t.Errorf("plan-yolo implementation handoff missing: %s", body)
			}
			writeProviderText(writer, "plan-yolo-2", "Automatic implementation complete.")
		default:
			t.Errorf("unexpected plan-yolo provider call %d", call)
			writeProviderText(writer, "plan-yolo-extra", "unexpected")
		}
	})
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "plan-yolo", Prompt: "Plan and implement automatically", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single",
		PlanYolo: &config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	deadline := time.Now().Add(3 * time.Second)
	for harness.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if harness.calls.Load() != 2 {
		t.Fatalf("plan-yolo calls = %d, want 2", harness.calls.Load())
	}
	for time.Now().Before(deadline) {
		harness.service.mu.Lock()
		active := harness.service.activeRun
		harness.service.mu.Unlock()
		if active == "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	projection, err := harness.service.sessions.LoadProjection(ctx, "plan-yolo")
	if err != nil {
		t.Fatal(err)
	}
	var approved bool
	for _, block := range projection.Blocks {
		if block.Kind == "plan" && block.State == "approved" && block.Data["planYolo"] == "true" {
			approved = true
		}
	}
	if !approved || len(projection.Blocks) == 0 || projection.Blocks[len(projection.Blocks)-1].Content != "Automatic implementation complete." {
		t.Fatalf("plan-yolo projection = %#v", projection.Blocks)
	}
}

func TestPrewalkAndPlanYoloRequestValidation(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	for name, request := range map[string]TurnRequest{
		"combined":              {SessionID: "s", Prompt: "x", Prewalk: &config.ModelRouteConfig{Provider: "p", Model: "m"}, PlanYolo: &config.ModelRouteConfig{Provider: "p", Model: "m"}},
		"missing prewalk model": {SessionID: "s", Prompt: "x", Prewalk: &config.ModelRouteConfig{Provider: "p"}},
		"missing plan provider": {SessionID: "s", Prompt: "x", PlanYolo: &config.ModelRouteConfig{Model: "m"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.StartConfiguredTurn(request); err == nil {
				t.Fatal("invalid prewalk request accepted")
			}
		})
	}
}
