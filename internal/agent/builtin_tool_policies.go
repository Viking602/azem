package agent

import (
	"fmt"
	"runtime"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

func readOnlyPolicy(tags ...string) agentruntime.ToolPolicy {
	return agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly, RiskLevel: "low", PolicyTags: tags}
}

func workspaceWritePolicy(tags ...string) agentruntime.ToolPolicy {
	return agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectWrite, RequiresActionTask: true,
		RiskLevel: "medium", PolicyTags: tags,
	}
}

func approvedExternalPolicy(tags ...string) agentruntime.ToolPolicy {
	return agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
		RequiresActionTask: true, RiskLevel: "high", PolicyTags: tags,
	}
}

func (deleteFileDriver) ToolPolicy() agentruntime.ToolPolicy {
	return workspaceWritePolicy("coding", "delete", "workspace-write")
}

func (globDriver) ToolPolicy() agentruntime.ToolPolicy {
	return readOnlyPolicy("coding", "read")
}

func (*hashlineDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := workspaceWritePolicy("coding", "edit", "hashline", "workspace")
	policy.Concurrency = tool.ConcurrencyExclusive
	policy.ConcurrencyGroup = "workspace-files"
	return policy
}

func (driver *memoryToolDriver) ToolPolicy() agentruntime.ToolPolicy {
	if driver.operation == ToolRecall || driver.operation == ToolReflect {
		policy := readOnlyPolicy("memory", "read")
		policy.Concurrency = tool.ConcurrencyParallel
		return policy
	}
	policy := readOnlyPolicy("memory", "write")
	policy.Concurrency = tool.ConcurrencyExclusive
	policy.ConcurrencyGroup = "long-term-memory"
	return policy
}

func (*readDriver) ToolPolicy() agentruntime.ToolPolicy {
	return readOnlyPolicy("coding", "read")
}

func (replaceDriver) ToolPolicy() agentruntime.ToolPolicy {
	return workspaceWritePolicy("coding", "edit", "workspace-write")
}

func (reliableSearchDriver) ToolPolicy() agentruntime.ToolPolicy {
	return readOnlyPolicy("coding", "search")
}

func (driver *shellDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := approvedExternalPolicy("coding", "shell", "workspace")
	policy.RequiresApproval = driver.approval != "allow"
	policy.Metadata = map[string]string{
		"approval":               driver.approval,
		"network":                driver.allowNetwork,
		"platform":               runtime.GOOS,
		"max_wall_clock_seconds": fmt.Sprint(maxWallClockSeconds(driver.maxWallClock())),
	}
	return policy
}

func (*writeDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := workspaceWritePolicy("coding", "write", "workspace")
	policy.Concurrency = tool.ConcurrencyExclusive
	policy.ConcurrencyGroup = "workspace-files"
	return policy
}

func (listFilesDriver) ToolPolicy() agentruntime.ToolPolicy {
	return readOnlyPolicy("coding", "list", "workspace")
}

func (snapshotReadDriver) ToolPolicy() agentruntime.ToolPolicy {
	return readOnlyPolicy("coding", "read", "workspace")
}

func (gofmtDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := workspaceWritePolicy("coding", "format", "workspace")
	policy.Concurrency = tool.ConcurrencyExclusive
	policy.ConcurrencyGroup = "workspace-files"
	return policy
}

func (goTestDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectExternalSideEffect, RequiresActionTask: true,
		RiskLevel: "medium", PolicyTags: []string{"coding", "test", "execute"},
		Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "workspace-processes",
	}
	return policy
}

func (gitDiffDriver) ToolPolicy() agentruntime.ToolPolicy {
	return readOnlyPolicy("coding", "git", "diff")
}
