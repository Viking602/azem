package app

import (
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

func appReadOnlyPolicy(tags ...string) agentruntime.ToolPolicy {
	return agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly, RiskLevel: "low", PolicyTags: tags}
}

func appInternalWritePolicy(tags ...string) agentruntime.ToolPolicy {
	return agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectWrite, RiskLevel: "low", PolicyTags: tags}
}

func (driver *checkpointDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appReadOnlyPolicy("checkpoint", "context")
	policy.Concurrency = tool.ConcurrencyExclusive
	policy.ConcurrencyGroup = "context-checkpoint"
	return policy
}

func (*contextArtifactDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appReadOnlyPolicy("session", "context", "read-only")
	policy.Metadata = map[string]string{"approval": "allow"}
	return policy
}

func (*goalDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appReadOnlyPolicy("goal", "control")
	policy.Concurrency = tool.ConcurrencyExclusive
	policy.ConcurrencyGroup = "session-goal"
	return policy
}

func (*askDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appReadOnlyPolicy("session", "planning", "interactive")
	policy.Metadata = map[string]string{"approval": "allow", "interactive": "true", "exclusive": "true"}
	return policy
}

func (*submitPlanDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appInternalWritePolicy("session", "planning")
	policy.Metadata = map[string]string{"approval": "allow", "terminal": "true"}
	return policy
}

func (*securityProgressDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appReadOnlyPolicy("security", "progress")
	policy.Metadata = map[string]string{"approval": "allow", "security_scan": "progress"}
	return policy
}

func (*securitySubmitDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appInternalWritePolicy("security", "state")
	policy.Metadata = map[string]string{"approval": "allow", "security_scan": "draft"}
	return policy
}

func (*securityReducerInputsDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appReadOnlyPolicy("security", "reducer")
	policy.Metadata = map[string]string{"approval": "allow"}
	return policy
}

func (*securitySubmitMatchesDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appInternalWritePolicy("security", "matching")
	policy.Metadata = map[string]string{"approval": "allow"}
	return policy
}

func (*securitySubmitPatchDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appInternalWritePolicy("security", "remediation")
	policy.Metadata = map[string]string{"approval": "allow"}
	return policy
}

func (*subagentSpawnDriver) ToolPolicy() agentruntime.ToolPolicy {
	return appReadOnlyPolicy("subagent", "spawn")
}

func (*subagentGetOutputDriver) ToolPolicy() agentruntime.ToolPolicy {
	return appReadOnlyPolicy("subagent", "query")
}

func (*subagentKillDriver) ToolPolicy() agentruntime.ToolPolicy {
	return appReadOnlyPolicy("subagent", "cancel")
}

func (*todoDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appInternalWritePolicy("session", "todo")
	policy.Metadata = map[string]string{"approval": "allow"}
	return policy
}

func (*vibeDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := appReadOnlyPolicy("vibe", "subagent")
	policy.Concurrency = tool.ConcurrencyParallel
	return policy
}
