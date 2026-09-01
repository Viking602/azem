package agentruntime

import (
	"encoding/json"
	"time"
)

// Command is the public command contract accepted by Runner.ExecuteCommand.
type Command interface {
	CommandName() string
}

type RuntimeCommand = Command

type StartRunCommand struct {
	RunID        string
	RootTaskID   string
	Request      string
	AgentVersion string
	Metadata     map[string]string
}

type CreateTaskCommand struct {
	RunID              string
	TaskID             string
	ParentTaskID       string
	Type               TaskType
	Goal               string
	Input              json.RawMessage
	AssignedAgentID    string
	OwnerAgentID       string
	OwnerComponent     string
	AllowsAction       bool
	Tags               []string
	CompletionCriteria []string
	DependsOn          []string
	AwaitMode          AwaitMode
	AwaitQuorum        int
	OnDependencyFailed OnDependencyFailed
	ReadSelectors      []BlackboardSelector
	WriteTargets       []string
	RetryPolicy        RetryPolicy
	PolicyDecisions    []PolicyDecision
	// InputSchema / OutputSchema travel with the created task so the durable
	// worker path can rebuild the agent OutputPolicy from OutputSchema (the
	// store and contract already round-trip these on api.Task).
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	// Budget is the per-task loop budget. Like the schemas above it travels
	// with the created task so the durable worker path can enforce the
	// token, tool-call, and step ceilings the agent loop reads off the task.
	Budget         *TaskBudget
	ResourceClaims []ResourceClaimSpec
}

type TransitionRunCommand struct {
	RunID string
	To    RunStatus
}

type TransitionTaskCommand struct {
	RunID  string
	TaskID string
	To     TaskStatus
}

type AdvanceRunCommand struct {
	RunID string
}

type DispatchTaskCommand struct {
	RunID           string
	TaskID          string
	TargetAgentID   string
	TargetComponent string
	// godoc-allow-any: dispatch payloads are host-defined extension data.
	Payload map[string]any
}

type FanOutDispatchTaskCommand struct {
	RunID  string
	TaskID string
	To     Address
	// godoc-allow-any: fan-out payloads are host-defined extension data.
	Payload map[string]any
}

type AckEnvelopeCommand struct {
	EnvelopeID string
	HolderID   string
}

type DeadLetterCommand struct {
	EnvelopeID string
	Reason     string
}

type WriteBlackboardItemCommand struct {
	Item BlackboardItem
}

type AcquireTaskExecutionCommand struct {
	RunID      string
	TaskID     string
	EnvelopeID string
	HolderType HolderType
	HolderID   string
	TTL        time.Duration
}

type HeartbeatTaskExecutionCommand struct {
	LeaseID  string
	HolderID string
	TTL      time.Duration
}

type ReleaseTaskExecutionCommand struct {
	LeaseID  string
	HolderID string
}

type SubmitTypedReportCommand struct {
	RunID       string
	TaskID      string
	LeaseID     string
	HolderType  HolderType
	HolderID    string
	TaskVersion int
	Report      TypedReport
}

type SubmitUserInputCommand struct {
	RunID  string
	TaskID string
	Input  string
}

type ToolInvocation struct {
	RunID       string
	TaskID      string
	LeaseID     string
	HolderType  HolderType
	HolderID    string
	TaskVersion int
	ToolName    string
	// godoc-allow-any: tool input is defined by the selected tool schema.
	Input any
}

type ToolInvocationResult struct {
	ToolName string
	// godoc-allow-any: tool output is defined by the selected tool schema.
	Output   any
	Decision PolicyDecision
}

type HandoffCommand struct {
	RunID          string
	TaskID         string
	FromAgentID    string
	ToAgentID      string
	TaskVersion    int
	HandoffContext string
}

type SubmitResponseOutputCommand struct {
	RunID          string
	TaskID         string
	LeaseID        string
	HolderType     HolderType
	HolderID       string
	TaskVersion    int
	Type           UserMessageType
	Title          string
	Payload        string
	IdempotencyKey string
}

type PublishResponseCommand struct {
	RunID     string
	MessageID string
}

type RequestApprovalCommand struct {
	RunID            string
	TaskID           string
	ActionID         string
	RequesterAgentID string
	Reason           string
	RiskSummary      string
	RequestedAction  string
	Metadata         map[string]string
}

type DecideApprovalCommand struct {
	RunID      string
	ApprovalID string
	DecidedBy  string
	Decision   string
	Reason     string
}

type RecoverResumeTokenCommand struct {
	TokenID string
}

type StartActionAttemptCommand struct {
	AttemptID      string
	ActionID       string
	RunID          string
	TaskID         string
	LeaseID        string
	HolderType     HolderType
	HolderID       string
	TaskVersion    int
	ToolName       string
	IdempotencyKey string
	InputHash      string
}

type CompleteActionAttemptCommand struct {
	RunID             string
	TaskID            string
	LeaseID           string
	HolderType        HolderType
	HolderID          string
	TaskVersion       int
	AttemptID         string
	Status            ActionAttemptStatus
	ExternalRequestID string
	ExternalResultRef string
	ToolResult        json.RawMessage
	RequiresReconcile bool
	UsageRecord       *UsageRecord
}

// AppendTaskExecutionEventCommand appends an execution event only while the
// caller still owns the current lease and task version.
type AppendTaskExecutionEventCommand struct {
	RunID        string
	TaskID       string
	LeaseID      string
	HolderType   HolderType
	HolderID     string
	TaskVersion  int
	Event        Event
	UsageRecords []UsageRecord
}

// ResolveActionAttemptCommand records the host's reconciliation decision for an
// attempt whose outcome became unknown after an interruption.
type ResolveActionAttemptCommand struct {
	AttemptID         string
	Status            ActionAttemptStatus
	ExternalResultRef string
	ToolResult        json.RawMessage
}

type StartTraceSpanCommand struct {
	RunID     string
	TaskID    string
	TraceID   string
	ParentID  string
	Name      string
	Component string
	Metadata  map[string]string
}

type EndTraceSpanCommand struct {
	SpanID string
	Error  string
}
