package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

const (
	goalToolName     = "goal"
	goalArtifactKind = session.InternalArtifactKindPrefix + "goal-state-v1"
)

type GoalStatus string

const (
	GoalActive        GoalStatus = "active"
	GoalPaused        GoalStatus = "paused"
	GoalBudgetLimited GoalStatus = "budget-limited"
	GoalComplete      GoalStatus = "complete"
	GoalDropped       GoalStatus = "dropped"
)

type Goal struct {
	ID              string     `json:"id"`
	Objective       string     `json:"objective"`
	Status          GoalStatus `json:"status"`
	TokenBudget     *int       `json:"tokenBudget,omitempty"`
	TokensUsed      int        `json:"tokensUsed"`
	TimeUsedSeconds int        `json:"timeUsedSeconds"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type goalModeState struct {
	Version            int       `json:"version"`
	Present            bool      `json:"present"`
	Enabled            bool      `json:"enabled"`
	Goal               Goal      `json:"goal"`
	AccountedRunID     string    `json:"accountedRunId,omitempty"`
	AccountedRunTokens int       `json:"accountedRunTokens,omitempty"`
	LastAccountedAt    time.Time `json:"lastAccountedAt,omitempty"`
	SavedAt            time.Time `json:"savedAt"`
}

type goalDriver struct {
	sessionID string
	runID     string
	store     *session.Service
}

var goalLocks sync.Map

func sessionGoalLock(sessionID string) *sync.Mutex {
	value, _ := goalLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (driver *goalDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        goalToolName,
		Description: "Manage autonomous Goal mode. create starts one durable objective; get reads it; resume reactivates a paused or budget-limited goal; complete is allowed only after the objective is actually finished and verified; drop abandons it. An active goal prevents the agent from ending and continues work until complete, dropped, cancelled, or budget-limited.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"op"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
			"op":           {Type: "string", Enum: []string{"create", "get", "complete", "resume", "drop"}},
			"objective":    {Type: "string", Description: "Required only for create."},
			"token_budget": {Type: "integer", Description: "Optional positive autonomous-work token budget."},
		}},
		EffectType: tool.EffectReadOnly, RiskLevel: "low", PolicyTags: []string{"goal", "control"}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "session-goal",
	}
}

func (driver *goalDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	if driver == nil || driver.store == nil || strings.TrimSpace(driver.sessionID) == "" {
		return goalToolError(call, errors.New("Goal mode is unavailable")), nil
	}
	var input struct {
		Operation   string `json:"op"`
		Objective   string `json:"objective"`
		TokenBudget *int   `json:"token_budget,omitempty"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return goalToolError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	input.Operation = strings.TrimSpace(input.Operation)
	input.Objective = strings.TrimSpace(input.Objective)
	lock := sessionGoalLock(driver.sessionID)
	lock.Lock()
	defer lock.Unlock()
	state, err := loadGoalState(ctx, driver.store, driver.sessionID)
	if err != nil {
		return goalToolError(call, err), nil
	}
	now := time.Now().UTC()
	if state != nil && state.Present {
		if changed, accountErr := accountGoalState(ctx, driver.store, driver.sessionID, driver.runID, state, now); accountErr != nil {
			return goalToolError(call, accountErr), nil
		} else if changed && input.Operation == "get" {
			if err := saveGoalState(ctx, driver.store, driver.sessionID, driver.runID, state); err != nil {
				return goalToolError(call, err), nil
			}
		}
	}
	var response *Goal
	completionReport := ""
	if len([]rune(input.Objective)) > 20_000 {
		return goalToolError(call, errors.New("objective exceeds 20000 characters")), nil
	}
	switch input.Operation {
	case "create":
		if input.Objective == "" {
			return goalToolError(call, errors.New("objective is required when op=create")), nil
		}
		if input.TokenBudget != nil && *input.TokenBudget <= 0 {
			return goalToolError(call, errors.New("token_budget must be a positive integer when provided")), nil
		}
		if state != nil && state.Present && state.Goal.Status != GoalComplete && state.Goal.Status != GoalDropped {
			return goalToolError(call, errors.New("cannot create a new goal because this session already has a goal")), nil
		}
		id, idErr := randomID("goal")
		if idErr != nil {
			return goalToolError(call, idErr), nil
		}
		baseline, baselineErr := driver.store.GoalTokenUsageSnapshot(ctx, driver.sessionID, driver.runID)
		if baselineErr != nil {
			return goalToolError(call, baselineErr), nil
		}
		state = &goalModeState{Version: 1, Present: true, Enabled: true, Goal: Goal{
			ID: id, Objective: input.Objective, Status: GoalActive, TokenBudget: cloneInt(input.TokenBudget), CreatedAt: now, UpdatedAt: now,
		}, AccountedRunID: driver.runID, AccountedRunTokens: baseline, LastAccountedAt: now}
		response = &state.Goal
	case "get":
		if state != nil && state.Present {
			response = &state.Goal
		}
	case "resume":
		if state == nil || !state.Present {
			return goalToolError(call, errors.New("No paused goal")), nil
		}
		if state.Goal.Status == GoalComplete {
			return goalToolError(call, errors.New("Goal is already complete")), nil
		}
		if state.Goal.Status == GoalDropped {
			return goalToolError(call, errors.New("cannot resume a dropped goal")), nil
		}
		baseline, baselineErr := driver.store.GoalTokenUsageSnapshot(ctx, driver.sessionID, driver.runID)
		if baselineErr != nil {
			return goalToolError(call, baselineErr), nil
		}
		state.Enabled, state.Goal.Status, state.Goal.UpdatedAt = true, GoalActive, now
		state.AccountedRunID, state.AccountedRunTokens, state.LastAccountedAt = driver.runID, baseline, now
		response = &state.Goal
	case "complete":
		if state == nil || !state.Present {
			return goalToolError(call, errors.New("cannot complete goal because no goal is active")), nil
		}
		if state.Goal.Status == GoalComplete {
			return goalToolError(call, errors.New("goal is already complete")), nil
		}
		if state.Goal.Status == GoalDropped {
			return goalToolError(call, errors.New("cannot complete a dropped goal")), nil
		}
		state.Enabled, state.Goal.Status, state.Goal.UpdatedAt = false, GoalComplete, now
		response = &state.Goal
		completionReport = goalCompletionReport(state.Goal)
	case "drop":
		if state != nil && state.Present {
			state.Enabled, state.Goal.Status, state.Goal.UpdatedAt = false, GoalDropped, now
			dropped := state.Goal
			response = &dropped
			state.Present = false
		} else {
			state = &goalModeState{Version: 1, Present: false, SavedAt: now}
		}
	default:
		return goalToolError(call, fmt.Errorf("unsupported goal operation %q", input.Operation)), nil
	}
	if input.Operation != "get" {
		if err := saveGoalState(ctx, driver.store, driver.sessionID, driver.runID, state); err != nil {
			return goalToolError(call, err), nil
		}
	}
	return goalToolResult(call, input.Operation, response, completionReport), nil
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func loadGoalState(ctx context.Context, store *session.Service, sessionID string) (*goalModeState, error) {
	artifact, err := store.LoadLatestArtifactByKind(ctx, sessionID, goalArtifactKind)
	if errors.Is(err, session.ErrContextArtifactNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load Goal mode state: %w", err)
	}
	var state goalModeState
	if err := json.Unmarshal(artifact.Payload, &state); err != nil {
		return nil, fmt.Errorf("decode Goal mode state: %w", err)
	}
	if state.Version != 1 {
		return nil, fmt.Errorf("Goal mode state version %d is unsupported", state.Version)
	}
	return &state, nil
}

func saveGoalState(ctx context.Context, store *session.Service, sessionID, runID string, state *goalModeState) error {
	if state == nil {
		return errors.New("Goal mode state is nil")
	}
	state.Version = 1
	state.SavedAt = time.Now().UTC()
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode Goal mode state: %w", err)
	}
	if _, err := store.PutArtifact(ctx, sessionID, runID, goalArtifactKind, payload, ""); err != nil {
		return fmt.Errorf("persist Goal mode state: %w", err)
	}
	return nil
}

func accountGoalState(ctx context.Context, store *session.Service, sessionID, runID string, state *goalModeState, now time.Time) (bool, error) {
	if state == nil || !state.Present || !state.Enabled || state.Goal.Status != GoalActive {
		return false, nil
	}
	usage, err := store.GoalTokenUsageSnapshot(ctx, sessionID, runID)
	if err != nil {
		return false, err
	}
	if state.AccountedRunID != runID {
		state.AccountedRunID, state.AccountedRunTokens = runID, 0
		state.LastAccountedAt = now
	}
	delta := max(0, usage-state.AccountedRunTokens)
	elapsed := 0
	if !state.LastAccountedAt.IsZero() {
		elapsed = max(0, int(now.Sub(state.LastAccountedAt)/time.Second))
	}
	if delta == 0 && elapsed == 0 {
		return false, nil
	}
	state.Goal.TokensUsed += delta
	state.Goal.TimeUsedSeconds += elapsed
	state.Goal.UpdatedAt = now
	state.AccountedRunTokens = usage
	state.LastAccountedAt = now
	if state.Goal.TokenBudget != nil && state.Goal.TokensUsed >= *state.Goal.TokenBudget {
		state.Goal.Status = GoalBudgetLimited
	}
	return true, nil
}

func activeGoalContext(ctx context.Context, store *session.Service, sessionID string) (string, error) {
	lock := sessionGoalLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	state, err := loadGoalState(ctx, store, sessionID)
	if err != nil || state == nil || !state.Present || !state.Enabled || state.Goal.Status != GoalActive {
		return "", err
	}
	return renderGoalModeMessage("active", state.Goal), nil
}

func pauseSessionGoal(ctx context.Context, store *session.Service, sessionID, runID string) error {
	if store == nil {
		return nil
	}
	lock := sessionGoalLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	state, err := loadGoalState(ctx, store, sessionID)
	if err != nil || state == nil || !state.Present || !state.Enabled || state.Goal.Status != GoalActive {
		return err
	}
	_, _ = accountGoalState(ctx, store, sessionID, runID, state, time.Now().UTC())
	state.Enabled, state.Goal.Status, state.Goal.UpdatedAt = false, GoalPaused, time.Now().UTC()
	return saveGoalState(ctx, store, sessionID, runID, state)
}

func goalOutputGuardrail(store *session.Service, sessionID, runID string) hyagent.OutputGuardrail {
	if store == nil {
		return nil
	}
	budgetSteered := false
	return hyagent.NewOutputGuardrail("active-goal", func(ctx context.Context, input hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
		lock := sessionGoalLock(sessionID)
		lock.Lock()
		defer lock.Unlock()
		state, err := loadGoalState(ctx, store, sessionID)
		if err != nil {
			return hyagent.OutputGuardrailResult{}, err
		}
		if state == nil || !state.Present || !state.Enabled || state.Goal.Status == GoalComplete || state.Goal.Status == GoalDropped || state.Goal.Status == GoalPaused {
			return hyagent.AllowOutput(), nil
		}
		current := goalProviderUsageTokens(input.Usage)
		if state.AccountedRunID != runID {
			state.AccountedRunID, state.AccountedRunTokens, state.LastAccountedAt = runID, 0, time.Now().UTC()
		}
		delta := max(0, current-state.AccountedRunTokens)
		now := time.Now().UTC()
		elapsed := max(0, int(now.Sub(state.LastAccountedAt)/time.Second))
		changed := delta > 0 || elapsed > 0
		if changed {
			state.Goal.TokensUsed += delta
			state.Goal.TimeUsedSeconds += elapsed
			state.Goal.UpdatedAt = now
			state.AccountedRunTokens, state.LastAccountedAt = current, now
			if state.Goal.TokenBudget != nil && state.Goal.TokensUsed >= *state.Goal.TokenBudget {
				state.Goal.Status = GoalBudgetLimited
			}
			if err := saveGoalState(ctx, store, sessionID, runID, state); err != nil {
				return hyagent.OutputGuardrailResult{}, err
			}
		}
		if state.Goal.Status == GoalBudgetLimited {
			if !budgetSteered {
				budgetSteered = true
				value := message.NewText(message.RoleSystem, renderGoalModeMessage("budget-limit", state.Goal))
				value.Visibility = message.VisibilityPrivate
				return hyagent.RetryOutput(value), nil
			}
			return hyagent.AllowOutput(), nil
		}
		value := message.NewText(message.RoleSystem, renderGoalModeMessage("continuation", state.Goal))
		value.Visibility = message.VisibilityPrivate
		return hyagent.RetryOutput(value), nil
	})
}

func goalProviderUsageTokens(usage hyprovider.Usage) int {
	input := usage.InputTokens
	if usage.CachedInputTokensReported {
		input = max(0, input-usage.CachedInputTokens)
	}
	return input + usage.CacheWriteInputTokens + usage.OutputTokens
}

func renderGoalModeMessage(kind string, goal Goal) string {
	remaining := "unbounded"
	budget := "none"
	if goal.TokenBudget != nil {
		budget = fmt.Sprint(*goal.TokenBudget)
		remaining = fmt.Sprint(max(0, *goal.TokenBudget-goal.TokensUsed))
	}
	prefix := "Goal mode is active. Continue autonomous work toward the objective. Do not end the run while it remains active. Call `goal` with op=complete only after the objective is actually done and verified; use drop only to abandon it."
	if kind == "continuation" {
		prefix = "The active Goal is not complete. Continue working now; do not restate an intermediate answer as completion."
	} else if kind == "budget-limit" {
		prefix = "The Goal token budget is exhausted. Stop autonomous work, report the verified partial state and remaining blocker, and leave the Goal budget-limited until the user resumes or drops it."
	}
	return fmt.Sprintf("%s\n<goal-objective>\n%s\n</goal-objective>\nTokens used: %d · budget: %s · remaining: %s · elapsed: %ds", prefix, html.EscapeString(goal.Objective), goal.TokensUsed, budget, remaining, goal.TimeUsedSeconds)
}

func goalCompletionReport(goal Goal) string {
	parts := make([]string, 0, 2)
	if goal.TokenBudget != nil {
		parts = append(parts, fmt.Sprintf("tokens used: %d of %d", goal.TokensUsed, *goal.TokenBudget))
	}
	if goal.TimeUsedSeconds > 0 {
		parts = append(parts, fmt.Sprintf("time used: %d seconds", goal.TimeUsedSeconds))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Goal achieved. Report final budget usage to the user: " + strings.Join(parts, "; ") + "."
}

func goalToolResult(call tool.Call, operation string, goal *Goal, completionReport string) tool.Result {
	remaining := any(nil)
	if goal != nil && goal.TokenBudget != nil {
		remaining = max(0, *goal.TokenBudget-goal.TokensUsed)
	}
	details := map[string]any{"op": operation, "goal": goal, "remainingTokens": remaining, "completionBudgetReport": nil}
	if completionReport != "" {
		details["completionBudgetReport"] = completionReport
	}
	structured, _ := json.Marshal(details)
	if goal == nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "No active goal.", Structured: structured}
	}
	text := fmt.Sprintf("Goal: %s\nStatus: %s\nTokens: %d used", goal.Objective, goal.Status, goal.TokensUsed)
	if goal.TokenBudget != nil {
		text += fmt.Sprintf(" / %d budget\nRemaining tokens: %d", *goal.TokenBudget, max(0, *goal.TokenBudget-goal.TokensUsed))
	}
	if completionReport != "" {
		text += "\n\n" + completionReport
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: text, Structured: structured}
}

func goalToolError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "goal failed: " + err.Error(), IsError: true}
}
