package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
)

const (
	PlannerClass     = "planner"
	ImplementerClass = "implementer"
	ReviewerClass    = "reviewer"
	ReporterClass    = "reporter"
)

// CodingScheduler is a replay-safe scheduler for the built-in coding team.
// Prompt is immutable run input; every scheduling decision and retry count is
// otherwise derived from the supplied TeamState snapshot.
type CodingScheduler struct {
	Prompt         string
	Classes        map[string]agentruntime.TeamAgentClass
	RetryPolicy    agentruntime.RetryPolicy
	ResourceClaims []agentruntime.ResourceClaimSpec
}

func (s CodingScheduler) Next(ctx context.Context, state agentruntime.TeamState) ([]agentruntime.TeamDispatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if activeOrFailed(state) {
		return nil, nil
	}

	planner := finishedForClass(state, PlannerClass)
	if len(planner) == 0 {
		return s.dispatch(state, PlannerClass, nil, map[string]any{"request": s.Prompt})
	}
	implementers := finishedForClass(state, ImplementerClass)
	if len(implementers) == 0 {
		return s.dispatch(state, ImplementerClass, &planner[len(planner)-1], map[string]any{
			"request":  s.Prompt,
			"previous": reportForTask(state, planner[len(planner)-1].TaskID),
		})
	}
	reviewers := finishedForClass(state, ReviewerClass)
	if len(reviewers) == 0 {
		return s.dispatch(state, ReviewerClass, &implementers[len(implementers)-1], map[string]any{
			"request":  s.Prompt,
			"previous": reportForTask(state, implementers[len(implementers)-1].TaskID),
		})
	}

	latestReview := reportForTask(state, reviewers[len(reviewers)-1].TaskID)
	if reviewVerdict(latestReview) == "revise" && len(implementers) == 1 {
		return s.dispatch(state, ImplementerClass, &reviewers[len(reviewers)-1], map[string]any{
			"request":  s.Prompt,
			"previous": latestReview,
			"revision": 1,
		})
	}
	if len(implementers) == 2 && len(reviewers) == 1 {
		return s.dispatch(state, ReviewerClass, &implementers[len(implementers)-1], map[string]any{
			"request":  s.Prompt,
			"previous": reportForTask(state, implementers[len(implementers)-1].TaskID),
			"revision": 1,
		})
	}

	reporters := finishedForClass(state, ReporterClass)
	if len(reporters) > 0 {
		return nil, nil
	}
	input := map[string]any{"request": s.Prompt, "previous": latestReview}
	if reviewVerdict(latestReview) == "revise" {
		input["revision_limit_reached"] = true
	}
	return s.dispatch(state, ReporterClass, &reviewers[len(reviewers)-1], input)
}

func (s CodingScheduler) dispatch(state agentruntime.TeamState, className string, from *agentruntime.TeamInstance, input any) ([]agentruntime.TeamDispatch, error) {
	class, ok := s.Classes[className]
	if !ok {
		return nil, fmt.Errorf("coding scheduler: class %q is not configured", className)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("coding scheduler: encode %s input: %w", className, err)
	}
	attempt := classAttemptCount(state, className) + 1
	taskID := state.RunID + "-" + className
	if attempt > 1 {
		taskID += "-attempt-" + strconv.Itoa(attempt)
	}
	instanceID := agentruntime.ComputeTeamInstanceID(className, state.RunID, taskID, strconv.Itoa(len(state.Instances)))
	dispatch := agentruntime.TeamDispatch{
		To:             instanceID,
		ClassName:      className,
		AgentClassName: className,
		Task: agentruntime.Task{
			ID:           taskID,
			RunID:        state.RunID,
			Type:         agentruntime.TaskTypeWorker,
			AllowsAction: className == ImplementerClass || className == ReviewerClass,
			Goal:         codingTaskGoal(className),
			Input:        raw,
			Status:       agentruntime.TaskStatusCreated,
			InputSchema:  class.InputSchema,
			OutputSchema: class.OutputSchema,
			Budget:       &agentruntime.TaskBudget{},
			RetryPolicy:  s.RetryPolicy,
		},
		Input: raw,
		OutputPolicy: hyagent.OutputPolicy{
			Schema:   class.OutputSchema,
			Validate: len(class.OutputSchema) > 0,
		},
	}
	if codingClassMayMutateWorkspace(class) {
		dispatch.Task.ResourceClaims = slices.Clone(s.ResourceClaims)
	}
	if from != nil {
		dispatch.Handoff = &agentruntime.TeamHandoff{
			RunID:                state.RunID,
			From:                 from.ID,
			To:                   instanceID,
			Reason:               from.ClassName + " completed",
			Payload:              raw,
			RequiredOutputSchema: class.OutputSchema,
		}
	}
	return []agentruntime.TeamDispatch{dispatch}, nil
}

func codingClassMayMutateWorkspace(class agentruntime.TeamAgentClass) bool {
	for _, name := range class.Tools {
		switch name {
		case ToolEditHashline, ToolWriteFile, ToolGofmt, ToolShell:
			return true
		}
	}
	return false
}

func codingTaskGoal(className string) string {
	switch className {
	case PlannerClass:
		return "Plan the requested workspace change."
	case ImplementerClass:
		return "Implement the approved workspace change."
	case ReviewerClass:
		return "Review the implementation against the request."
	case ReporterClass:
		return "Report the verified result to the user."
	default:
		return "Complete the assigned team task."
	}
}

func activeOrFailed(state agentruntime.TeamState) bool {
	for _, instance := range state.Instances {
		switch instance.State {
		case agentruntime.TeamInstancePending, agentruntime.TeamInstanceRunning, agentruntime.TeamInstanceFailed:
			return true
		}
	}
	return false
}

func finishedForClass(state agentruntime.TeamState, className string) []agentruntime.TeamInstance {
	instances := make([]agentruntime.TeamInstance, 0, 2)
	for _, instance := range state.Instances {
		if instance.ClassName == className && instance.State == agentruntime.TeamInstanceFinished {
			instances = append(instances, instance)
		}
	}
	return instances
}

func classAttemptCount(state agentruntime.TeamState, className string) int {
	count := 0
	for _, instance := range state.Instances {
		if instance.ClassName == className {
			count++
		}
	}
	return count
}

func reportForTask(state agentruntime.TeamState, taskID string) *agentruntime.TypedReport {
	for index := len(state.Tasks) - 1; index >= 0; index-- {
		if state.Tasks[index].ID == taskID {
			return state.Tasks[index].Result
		}
	}
	return nil
}

func reviewVerdict(report *agentruntime.TypedReport) string {
	if report == nil || report.Structured == nil {
		return "revise"
	}
	verdict, _ := report.Structured["verdict"].(string)
	verdict = strings.ToLower(strings.TrimSpace(verdict))
	if verdict == "accept" {
		return verdict
	}
	return "revise"
}
