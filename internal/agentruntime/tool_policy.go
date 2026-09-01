package agentruntime

import (
	"maps"
	"slices"

	"github.com/Viking602/venat/tool"
)

// ToolPolicy is Azem's non-wire governance decision for one concrete tool
// invocation. It never enters provider-visible tool schemas.
type ToolPolicy struct {
	Effect              ToolEffectType
	RequiresApproval    bool
	RequiresActionTask  bool
	RiskLevel           string
	Tags                []string
	PolicyTags          []string
	Origin              string
	Idempotent          bool
	RequiredPermissions []string
	Metadata            map[string]string
	Concurrency         tool.ConcurrencyMode
	ConcurrencyGroup    string
	MaxConcurrency      int
}

func (policy ToolPolicy) Clone() ToolPolicy {
	policy.Tags = slices.Clone(policy.Tags)
	policy.PolicyTags = slices.Clone(policy.PolicyTags)
	policy.RequiredPermissions = slices.Clone(policy.RequiredPermissions)
	policy.Metadata = maps.Clone(policy.Metadata)
	return policy
}

func (policy ToolPolicy) IsReadOnly() bool { return policy.Effect == ToolEffectReadOnly }
