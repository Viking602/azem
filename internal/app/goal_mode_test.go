package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

func TestGoalToolPersistsLifecycleAcrossServiceInstances(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "goal-session", Title: "Goal"}); err != nil {
		t.Fatal(err)
	}
	driver := &goalDriver{sessionID: "goal-session", runID: "run-one", store: sessions}
	created := executeGoal(t, ctx, driver, `{"op":"create","objective":"Ship <verified> Goal mode","token_budget":100}`)
	if created.IsError || !strings.Contains(created.Content, "Status: active") || !strings.Contains(created.Content, "100 budget") {
		t.Fatalf("create goal = %#v", created)
	}
	duplicate := executeGoal(t, ctx, driver, `{"op":"create","objective":"replace active"}`)
	if !duplicate.IsError || !strings.Contains(duplicate.Content, "already has a goal") {
		t.Fatalf("duplicate goal = %#v", duplicate)
	}

	restarted := &goalDriver{sessionID: "goal-session", runID: "run-two", store: session.NewService(provider.DB(), provider.Blobs())}
	loaded := executeGoal(t, ctx, restarted, `{"op":"get"}`)
	if loaded.IsError || !strings.Contains(loaded.Content, "Ship <verified> Goal mode") {
		t.Fatalf("restored goal = %#v", loaded)
	}
	contextText, err := activeGoalContext(ctx, sessions, "goal-session")
	if err != nil || !strings.Contains(contextText, "Ship &lt;verified&gt; Goal mode") || !strings.Contains(contextText, "Do not end the run") {
		t.Fatalf("active Goal context = %q, %v", contextText, err)
	}
	if err := pauseSessionGoal(ctx, sessions, "goal-session", "run-two"); err != nil {
		t.Fatal(err)
	}
	paused := executeGoal(t, ctx, restarted, `{"op":"get"}`)
	if paused.IsError || !strings.Contains(paused.Content, "Status: paused") {
		t.Fatalf("paused goal = %#v", paused)
	}
	resumed := executeGoal(t, ctx, restarted, `{"op":"resume"}`)
	if resumed.IsError || !strings.Contains(resumed.Content, "Status: active") {
		t.Fatalf("resumed goal = %#v", resumed)
	}
	completed := executeGoal(t, ctx, restarted, `{"op":"complete"}`)
	if completed.IsError || !strings.Contains(completed.Content, "Status: complete") || !strings.Contains(completed.Content, "tokens used:") {
		t.Fatalf("completed goal = %#v", completed)
	}
	if next := executeGoal(t, ctx, restarted, `{"op":"create","objective":"Second goal"}`); next.IsError {
		t.Fatalf("new goal after completion = %#v", next)
	}
	dropped := executeGoal(t, ctx, restarted, `{"op":"drop"}`)
	if dropped.IsError || !strings.Contains(dropped.Content, "Status: dropped") {
		t.Fatalf("dropped goal = %#v", dropped)
	}
	if empty := executeGoal(t, ctx, restarted, `{"op":"get"}`); empty.IsError || empty.Content != "No active goal." {
		t.Fatalf("goal remained after drop = %#v", empty)
	}

	history, err := sessions.SearchHistory(ctx, "goal-session", "verified Goal mode", 10, 1000, 4000)
	if err != nil || len(history) != 0 {
		t.Fatalf("private Goal artifacts entered history recall: %#v, %v", history, err)
	}
}

func TestGoalGuardrailAccountsUsageContinuesAndHonorsBudget(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	for _, id := range []string{"active-goal", "budget-goal"} {
		if _, err := sessions.Ensure(ctx, session.Session{ID: id, Title: id}); err != nil {
			t.Fatal(err)
		}
	}

	activeDriver := &goalDriver{sessionID: "active-goal", runID: "run-active", store: sessions}
	if result := executeGoal(t, ctx, activeDriver, `{"op":"create","objective":"Finish all checks","token_budget":100}`); result.IsError {
		t.Fatal(result.Content)
	}
	guard := goalOutputGuardrail(sessions, "active-goal", "run-active")
	usage := hyprovider.Usage{InputTokens: 100, CachedInputTokens: 60, CachedInputTokensReported: true, CacheWriteInputTokens: 5, OutputTokens: 10}
	continued, err := guard.Check(ctx, hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "intermediate"), Usage: usage})
	if err != nil || continued.Action != hyagent.OutputGuardrailActionRetry || len(continued.RetryMessages) != 1 || !strings.Contains(continued.RetryMessages[0].Text, "not complete") {
		t.Fatalf("active Goal guard = %#v, %v", continued, err)
	}
	state, err := loadGoalState(ctx, sessions, "active-goal")
	if err != nil || state.Goal.TokensUsed != 55 {
		t.Fatalf("Goal usage = %#v, %v", state, err)
	}
	if result := executeGoal(t, ctx, activeDriver, `{"op":"complete"}`); result.IsError {
		t.Fatal(result.Content)
	}
	allowed, err := guard.Check(ctx, hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "final"), Usage: usage})
	if err != nil || allowed.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("completed Goal guard = %#v, %v", allowed, err)
	}

	budgetDriver := &goalDriver{sessionID: "budget-goal", runID: "run-budget", store: sessions}
	if result := executeGoal(t, ctx, budgetDriver, `{"op":"create","objective":"Use bounded work","token_budget":20}`); result.IsError {
		t.Fatal(result.Content)
	}
	budgetGuard := goalOutputGuardrail(sessions, "budget-goal", "run-budget")
	limited, err := budgetGuard.Check(ctx, hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "partial"), Usage: hyprovider.Usage{InputTokens: 25}})
	if err != nil || limited.Action != hyagent.OutputGuardrailActionRetry || !strings.Contains(limited.RetryMessages[0].Text, "budget is exhausted") {
		t.Fatalf("budget Goal guard = %#v, %v", limited, err)
	}
	budgetState, err := loadGoalState(ctx, sessions, "budget-goal")
	if err != nil || budgetState.Goal.Status != GoalBudgetLimited || budgetState.Goal.TokensUsed != 25 {
		t.Fatalf("budget Goal state = %#v, %v", budgetState, err)
	}
	final, err := budgetGuard.Check(ctx, hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "bounded report"), Usage: hyprovider.Usage{InputTokens: 25}})
	if err != nil || final.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("budget-limited final = %#v, %v", final, err)
	}
}

func TestProviderRuntimeContinuesActiveGoalUntilGoalToolCompletes(t *testing.T) {
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			if !strings.Contains(body, `"name":"goal"`) {
				t.Errorf("Goal tool was not advertised: %s", body)
			}
			writeProviderToolCall(writer, "goal-response-1", "goal-create", goalToolName, `{"op":"create","objective":"Finish the autonomous fixture","token_budget":10000}`)
		case 2:
			if !strings.Contains(body, "Finish the autonomous fixture") {
				t.Errorf("Goal create result missing from continuation request: %s", body)
			}
			writeProviderText(writer, "goal-response-2", "Intermediate answer must not finish.")
		case 3:
			if !strings.Contains(body, "active Goal is not complete") {
				t.Errorf("Goal continuation guard was not injected: %s", body)
			}
			writeProviderToolCall(writer, "goal-response-3", "goal-complete", goalToolName, `{"op":"complete"}`)
		case 4:
			if !strings.Contains(body, "Goal achieved") {
				t.Errorf("Goal completion report missing from final request: %s", body)
			}
			writeProviderText(writer, "goal-response-4", "Autonomous fixture complete.")
		default:
			t.Errorf("unexpected Goal provider call %d", call)
			writeProviderText(writer, "goal-response-extra", "unexpected")
		}
	})
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "goal-e2e", Prompt: "Run the autonomous fixture", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if harness.calls.Load() != 4 {
		t.Fatalf("Goal provider calls = %d, want 4", harness.calls.Load())
	}
	state, err := loadGoalState(context.Background(), harness.service.sessions, "goal-e2e")
	if err != nil || state == nil || state.Goal.Status != GoalComplete {
		t.Fatalf("completed Goal state = %#v, %v", state, err)
	}
	projection, err := harness.service.sessions.LoadProjection(context.Background(), "goal-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) == 0 || projection.Blocks[len(projection.Blocks)-1].Content != "Autonomous fixture complete." {
		t.Fatalf("Goal final projection = %#v", projection.Blocks)
	}
}

func TestGoalToolValidatesOperationContracts(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "goal-validation", Title: "Goal"}); err != nil {
		t.Fatal(err)
	}
	driver := &goalDriver{sessionID: "goal-validation", runID: "run", store: sessions}
	for name, input := range map[string]string{
		"missing objective": `{"op":"create"}`,
		"invalid budget":    `{"op":"create","objective":"x","token_budget":0}`,
		"missing resume":    `{"op":"resume"}`,
		"missing complete":  `{"op":"complete"}`,
		"unknown op":        `{"op":"replace"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if result := executeGoal(t, ctx, driver, input); !result.IsError {
				t.Fatalf("invalid Goal operation accepted: %#v", result)
			}
		})
	}
}

func executeGoal(t *testing.T, ctx context.Context, driver tool.Driver, input string) tool.Result {
	t.Helper()
	result, err := driver.Execute(ctx, tool.Call{ID: "goal", Name: goalToolName, Arguments: json.RawMessage(input)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
