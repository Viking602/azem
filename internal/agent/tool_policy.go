package agent

import (
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

type ToolPolicyResolver interface {
	PolicyForCall(tool.Call) agentruntime.ToolPolicy
}

type StaticToolPolicyProvider interface {
	ToolPolicy() agentruntime.ToolPolicy
}

// ToolDescriptor keeps provider-visible schema separate from Azem governance.
type ToolDescriptor struct {
	WireDefinition tool.Definition
	PolicyResolver func(tool.Call) agentruntime.ToolPolicy
}

func (descriptor ToolDescriptor) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	if descriptor.PolicyResolver == nil {
		return conservativeToolPolicy(descriptor.WireDefinition.Name)
	}
	return descriptor.PolicyResolver(call).Clone()
}

func DescribeTool(driver tool.Driver) (ToolDescriptor, error) {
	if driver == nil {
		return ToolDescriptor{}, fmt.Errorf("tool descriptor: nil driver")
	}
	definition := driver.Definition()
	if strings.TrimSpace(definition.Name) == "" {
		return ToolDescriptor{}, fmt.Errorf("tool descriptor: empty tool name")
	}
	descriptor := ToolDescriptor{WireDefinition: definition}
	switch resolved := driver.(type) {
	case ToolPolicyResolver:
		descriptor.PolicyResolver = resolved.PolicyForCall
	case StaticToolPolicyProvider:
		policy := resolved.ToolPolicy().Clone()
		descriptor.PolicyResolver = func(tool.Call) agentruntime.ToolPolicy { return policy.Clone() }
	default:
		policy := conservativeToolPolicy(definition.Name)
		descriptor.PolicyResolver = func(tool.Call) agentruntime.ToolPolicy { return policy.Clone() }
	}
	return descriptor, nil
}

func conservativeToolPolicy(name string) agentruntime.ToolPolicy {
	return agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
		RequiresActionTask: true, RiskLevel: "high", Origin: "azem:" + strings.TrimSpace(name),
	}
}
