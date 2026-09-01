package agentruntime

import "time"

// TaskBudget is the per-task budget consumed by agent.Engine and aggregated
// by Azem's Team scheduler for application-level observability.
//
// GovernancePolicy.Budget is the persisted outer admission envelope.
// TaskBudget is the direct Engine request budget for one execution.
//
// Spec anchor: docs/product-spec/v0.8.0/01-public-api.md §Change 4 and
// ADR-017.
type TaskBudget struct {
	MaxTokens    int64         `json:"maxTokens,omitempty"`
	MaxWallClock time.Duration `json:"maxWallClock,omitempty"`
	MaxToolCalls int           `json:"maxToolCalls,omitempty"`
	MaxSteps     int           `json:"maxSteps,omitempty"`
}
