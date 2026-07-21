package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/multiagent"
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
	Prompt  string
	Classes map[string]multiagent.AgentClass
}

func (s CodingScheduler) Next(ctx context.Context, state multiagent.TeamState) ([]multiagent.Dispatch, error) {
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

func (s CodingScheduler) dispatch(state multiagent.TeamState, className string, from *multiagent.AgentInstance, input any) ([]multiagent.Dispatch, error) {
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
	instanceID := multiagent.ComputeInstanceID(className, state.RunID, taskID, strconv.Itoa(len(state.Instances)))
	dispatch := multiagent.Dispatch{
		To:             instanceID,
		ClassName:      className,
		AgentClassName: className,
		Task: api.Task{
			ID:           taskID,
			RunID:        state.RunID,
			Type:         api.TaskTypeWorker,
			AllowsAction: className == ImplementerClass || className == ReviewerClass,
			Goal:         class.Instructions,
			Input:        raw,
			Status:       api.TaskStatusCreated,
			InputSchema:  class.InputSchema,
			OutputSchema: class.OutputSchema,
			Budget:       &api.TaskBudget{},
		},
		Input: raw,
		OutputPolicy: hyagent.OutputPolicy{
			Schema:   class.OutputSchema,
			Validate: len(class.OutputSchema) > 0,
		},
	}
	if from != nil {
		dispatch.Handoff = &multiagent.Handoff{
			RunID:                state.RunID,
			From:                 from.ID,
			To:                   instanceID,
			Reason:               from.ClassName + " completed",
			Payload:              raw,
			RequiredOutputSchema: class.OutputSchema,
		}
	}
	return []multiagent.Dispatch{dispatch}, nil
}

func activeOrFailed(state multiagent.TeamState) bool {
	for _, instance := range state.Instances {
		switch instance.State {
		case multiagent.InstanceStatePending, multiagent.InstanceStateRunning, multiagent.InstanceStateFailed:
			return true
		}
	}
	return false
}

func finishedForClass(state multiagent.TeamState, className string) []multiagent.AgentInstance {
	instances := make([]multiagent.AgentInstance, 0, 2)
	for _, instance := range state.Instances {
		if instance.ClassName == className && instance.State == multiagent.InstanceStateFinished {
			instances = append(instances, instance)
		}
	}
	return instances
}

func classAttemptCount(state multiagent.TeamState, className string) int {
	count := 0
	for _, instance := range state.Instances {
		if instance.ClassName == className {
			count++
		}
	}
	return count
}

func reportForTask(state multiagent.TeamState, taskID string) *api.TypedReport {
	for index := len(state.Tasks) - 1; index >= 0; index-- {
		if state.Tasks[index].ID == taskID {
			return state.Tasks[index].Result
		}
	}
	return nil
}

func reviewVerdict(report *api.TypedReport) string {
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
