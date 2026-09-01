package agentruntime

import "context"

// PipelineComponents are optional AdvanceRun customization hooks. They
// compile intent into tasks through the existing Runner; they are not a
// second durable runtime (ADR-008 Principle 2, ADR-027).

type IntentAnalyzer interface {
	AnalyzeIntent(context.Context, Run) (Intent, error)
}

type Planner interface {
	CreatePlan(context.Context, Intent) (TodoPlan, error)
}

type PlanValidator interface {
	ValidatePlan(context.Context, TodoPlan) error
}

type TaskRouter interface {
	RouteTasks(context.Context, TodoPlan) (RoutingPlan, error)
}

type Dispatcher interface {
	Dispatch(context.Context, RoutingPlan) ([]TaskEnvelope, error)
}

type TaskMonitor interface {
	Advance(context.Context, Run) error
	DecideDeadLetter(context.Context, TaskEnvelope, string) (TaskMonitorDecision, error)
}

type PipelineComponents struct {
	IntentAnalyzer IntentAnalyzer
	Planner        Planner
	Validator      PlanValidator
	Router         TaskRouter
	Dispatcher     Dispatcher
	TaskMonitor    TaskMonitor
}
