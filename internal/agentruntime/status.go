package agentruntime

// RunStatus describes the runtime lifecycle state for a run.
type RunStatus string

const (
	RunStatusCreated           RunStatus = "created"
	RunStatusPlanning          RunStatus = "planning"
	RunStatusValidating        RunStatus = "validating"
	RunStatusRouting           RunStatus = "routing"
	RunStatusDispatching       RunStatus = "dispatching"
	RunStatusRunning           RunStatus = "running"
	RunStatusWaitingUserInput  RunStatus = "waiting_user_input"
	RunStatusWaitingApproval   RunStatus = "waiting_approval"
	RunStatusExecuting         RunStatus = "executing"
	RunStatusReconcileRequired RunStatus = "reconcile_required"
	RunStatusComposingResponse RunStatus = "composing_response"
	RunStatusCompleted         RunStatus = "completed"
	RunStatusFailed            RunStatus = "failed"
	RunStatusBlocked           RunStatus = "blocked"
	RunStatusCancelled         RunStatus = "cancelled"
)

// TaskType is a free-form label set by the developer when creating a task.
// The framework only reserves TaskTypeWorker and TaskTypeResponse.
type TaskType string

const (
	TaskTypeWorker   TaskType = "worker"
	TaskTypeResponse TaskType = "response"
)

// AwaitMode controls how a task's DependsOn list is evaluated.
type AwaitMode string

const (
	AwaitModeAll    AwaitMode = ""
	AwaitModeAny    AwaitMode = "any"
	AwaitModeQuorum AwaitMode = "quorum"
)

// OnDependencyFailed governs what happens when a dependency reaches a terminal
// failure state.
type OnDependencyFailed string

const (
	OnDependencyFailedContinue OnDependencyFailed = ""
	OnDependencyFailedSkip     OnDependencyFailed = "skip"
	OnDependencyFailedFail     OnDependencyFailed = "fail"
)

type TaskStatus string

const (
	TaskStatusCreated           TaskStatus = "created"
	TaskStatusPlanned           TaskStatus = "planned"
	TaskStatusValidated         TaskStatus = "validated"
	TaskStatusRouted            TaskStatus = "routed"
	TaskStatusWaitingDependency TaskStatus = "waiting_dependency"
	TaskStatusDispatched        TaskStatus = "dispatched"
	TaskStatusRunning           TaskStatus = "running"
	TaskStatusPaused            TaskStatus = "paused"
	TaskStatusWaitingUserInput  TaskStatus = "waiting_user_input"
	TaskStatusReconcileRequired TaskStatus = "reconcile_required"
	TaskStatusBlocked           TaskStatus = "blocked"
	TaskStatusCompleted         TaskStatus = "completed"
	TaskStatusFailed            TaskStatus = "failed"
	TaskStatusCancelled         TaskStatus = "cancelled"
)

type HolderType string

const (
	HolderAgent     HolderType = "agent"
	HolderComponent HolderType = "component"
)

type LeaseStatus string

const (
	LeaseStatusActive   LeaseStatus = "active"
	LeaseStatusReleased LeaseStatus = "released"
	LeaseStatusExpired  LeaseStatus = "expired"
)

type ReportStatus string

const (
	ReportStatusSuccess            ReportStatus = "success"
	ReportStatusPartialSuccess     ReportStatus = "partial_success"
	ReportStatusFailed             ReportStatus = "failed"
	ReportStatusBlocked            ReportStatus = "blocked"
	ReportStatusNeedsHandoff       ReportStatus = "needs_handoff"
	ReportStatusNeedsApproval      ReportStatus = "needs_approval"
	ReportStatusNeedsClarification ReportStatus = "needs_clarification"
)

type ActionAttemptStatus string

const (
	ActionAttemptCreated   ActionAttemptStatus = "created"
	ActionAttemptRunning   ActionAttemptStatus = "running"
	ActionAttemptSucceeded ActionAttemptStatus = "succeeded"
	ActionAttemptFailed    ActionAttemptStatus = "failed"
	ActionAttemptTimeout   ActionAttemptStatus = "timeout"
	ActionAttemptUnknown   ActionAttemptStatus = "unknown"
	ActionAttemptCancelled ActionAttemptStatus = "cancelled"
	ActionAttemptRetry     ActionAttemptStatus = "retry"
)

// TaskEnvelope.Status values the framework itself writes. The field stays
// a plain string for compatibility (hosts may add their own states); these
// constants document the built-in lifecycle: dispatch creates a pending
// envelope, AckEnvelope marks it acked, and DeadLetter either re-queues it
// as pending (retry decision) or parks it as dead.
const (
	EnvelopeStatusPending = "pending"
	EnvelopeStatusAcked   = "acked"
	EnvelopeStatusDead    = "dead"
)
