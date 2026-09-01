package agent

import (
	"errors"
	"fmt"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
)

var ErrTaskExecutionUnavailable = errors.New("task execution unavailable")

type TaskExecutionUnavailableError struct {
	TaskID         string
	ResourceClaims agentruntime.ResourceClaimDecision
}

func (failure *TaskExecutionUnavailableError) Error() string {
	if failure.ResourceClaims.Reason != "" {
		return fmt.Sprintf("%v: task %s resource claims denied: %s", ErrTaskExecutionUnavailable, failure.TaskID, failure.ResourceClaims.Reason)
	}
	return fmt.Sprintf("%v: task %s already has an active lease", ErrTaskExecutionUnavailable, failure.TaskID)
}

func (failure *TaskExecutionUnavailableError) Unwrap() error { return ErrTaskExecutionUnavailable }

type ExecutionState string

const (
	ExecutionCompleted ExecutionState = "completed"
	ExecutionFailed    ExecutionState = "failed"
	ExecutionSuspended ExecutionState = "suspended"
	ExecutionCancelled ExecutionState = "cancelled"
)

type SuspensionKind string

const (
	SuspensionApproval       SuspensionKind = "approval"
	SuspensionReconciliation SuspensionKind = "reconciliation"
	SuspensionUserInput      SuspensionKind = "user_input"
	SuspensionRequested      SuspensionKind = "requested"
)

type Suspension struct {
	Kind   SuspensionKind `json:"kind"`
	Reason string         `json:"reason,omitempty"`
}

type ExecutionOutcome struct {
	State      ExecutionState        `json:"state"`
	RunID      string                `json:"runId,omitempty"`
	TaskID     string                `json:"taskId,omitempty"`
	LeaseID    string                `json:"leaseId,omitempty"`
	Result     hyagent.Result        `json:"result,omitempty"`
	Failure    *hyagent.AgentFailure `json:"failure,omitempty"`
	Suspension *Suspension           `json:"suspension,omitempty"`
}
