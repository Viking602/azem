package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	hyagent "github.com/Viking602/venat/agent"
)

const ExecutionManifestVersion = 2

type ExecutionBindingState string

const (
	ExecutionBindingPending           ExecutionBindingState = "pending"
	ExecutionBindingRunning           ExecutionBindingState = "running"
	ExecutionBindingSuspended         ExecutionBindingState = "suspended"
	ExecutionBindingReconcileRequired ExecutionBindingState = "reconcile_required"
	ExecutionBindingCompleted         ExecutionBindingState = "completed"
	ExecutionBindingFailed            ExecutionBindingState = "failed"
	ExecutionBindingCancelled         ExecutionBindingState = "cancelled"
)

// ExecutionManifest is the immutable Azem profile required to rebuild one
// direct Venat Engine execution segment.
type ExecutionManifest struct {
	Version               int                 `json:"version"`
	SessionID             string              `json:"sessionId,omitempty"`
	RunID                 string              `json:"runId"`
	AgentID               string              `json:"agentId"`
	StableID              string              `json:"stableId"`
	AgentVersion          string              `json:"agentVersion"`
	Kind                  string              `json:"kind"`
	Segment               int                 `json:"segment"`
	Prompt                string              `json:"prompt"`
	Provider              string              `json:"provider,omitempty"`
	AccountID             string              `json:"accountId,omitempty"`
	RawModel              string              `json:"rawModel,omitempty"`
	Model                 string              `json:"model,omitempty"`
	Reasoning             string              `json:"reasoning,omitempty"`
	ActiveSkills          []string            `json:"activeSkills"`
	ToolSetHash           string              `json:"toolSetHash,omitempty"`
	ToolProfileHash       string              `json:"toolProfileHash,omitempty"`
	PlanMode              bool                `json:"planMode,omitempty"`
	ApprovedPlanID        string              `json:"approvedPlanId,omitempty"`
	DisableSubagents      bool                `json:"disableSubagents,omitempty"`
	StaticIdentity        string              `json:"staticIdentity,omitempty"`
	WorkspaceAnchor       string              `json:"workspaceAnchor,omitempty"`
	PromptFingerprint     string              `json:"promptFingerprint,omitempty"`
	ToolSchemaFingerprint string              `json:"toolSchemaFingerprint,omitempty"`
	Budget                hyagent.Budget      `json:"budget,omitempty"`
	OutputSchema          json.RawMessage     `json:"outputSchema,omitempty"`
	Governance            GovernancePolicy    `json:"governance,omitempty"`
	RetryPolicy           RetryPolicy         `json:"retryPolicy,omitempty"`
	ResourceClaims        []ResourceClaimSpec `json:"resourceClaims,omitempty"`
	Metadata              map[string]string   `json:"metadata,omitempty"`
	Sealed                bool                `json:"sealed"`
	ProfileHash           string              `json:"profileHash"`
	StartedAt             time.Time           `json:"startedAt"`
}

// ExecutableProfile contains the route and provider-visible construction facts
// that are resolved after the app run exists but before its first durable
// effect. Sealing it makes resume fail closed when any executable dependency
// changes.
type ExecutableProfile struct {
	Provider              string
	AccountID             string
	RawModel              string
	Model                 string
	Reasoning             string
	ActiveSkills          []string
	ToolSetHash           string
	ToolProfileHash       string
	PlanMode              bool
	ApprovedPlanID        string
	DisableSubagents      bool
	StaticIdentity        string
	WorkspaceAnchor       string
	PromptFingerprint     string
	ToolSchemaFingerprint string
}

// ExecutionBinding keeps app run identity separate from versioned SDK
// execution segments.
type ExecutionBinding struct {
	ExecutionID string                `json:"executionId"`
	SessionID   string                `json:"sessionId,omitempty"`
	RunID       string                `json:"runId"`
	StableID    string                `json:"stableId"`
	AgentID     string                `json:"agentId"`
	Kind        string                `json:"kind"`
	Segment     int                   `json:"segment"`
	Manifest    ExecutionManifest     `json:"manifest"`
	ProfileHash string                `json:"profileHash"`
	State       ExecutionBindingState `json:"state"`
	Version     uint64                `json:"version"`
	UpdatedAt   time.Time             `json:"updatedAt"`
}

type ExecutionBindingRepository interface {
	SaveExecutionBinding(context.Context, ExecutionBinding, uint64) (ExecutionBinding, error)
	LoadExecutionBinding(context.Context, string) (ExecutionBinding, error)
	LoadLatestExecutionBinding(context.Context, string, string, string) (ExecutionBinding, error)
	ListExecutionBindings(context.Context, []ExecutionBindingState) ([]ExecutionBinding, error)
}
type RunExecutionRepository interface {
	CreateRunExecution(context.Context, Run, Task, TaskEnvelope, ExecutionBinding) error
}

func ExecutionID(kind, stableID string, segment int) (string, error) {
	kind = strings.TrimSpace(kind)
	stableID = strings.TrimSpace(stableID)
	if kind == "" || stableID == "" || segment < 0 || strings.ContainsAny(kind, ":/\\") || strings.ContainsAny(stableID, ":/\\") {
		return "", fmt.Errorf("invalid execution identity")
	}
	return fmt.Sprintf("azem:v1:%s:%s:%d", kind, stableID, segment), nil
}
