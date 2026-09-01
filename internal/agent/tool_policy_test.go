package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

type descriptorTestDriver struct {
	policy agentruntime.ToolPolicy
}

func (driver descriptorTestDriver) Definition() tool.Definition {
	return tool.Definition{
		Name: "descriptor.test", Description: "provider-visible description",
		InputSchema: tool.Schema{Type: "object"}, Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver descriptorTestDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	policy := driver.policy.Clone()
	var input struct {
		Write bool `json:"write"`
	}
	_ = json.Unmarshal(call.Arguments, &input)
	if input.Write {
		policy.Effect = agentruntime.ToolEffectWrite
		policy.RequiresActionTask = true
	}
	return policy
}

func (descriptorTestDriver) Execute(context.Context, tool.Call, tool.UpdateSink) (tool.Result, error) {
	return tool.Result{}, nil
}

type unclassifiedDescriptorTestDriver struct{}

func (unclassifiedDescriptorTestDriver) Definition() tool.Definition {
	return tool.Definition{Name: "descriptor.unknown", InputSchema: tool.Schema{Type: "object"}}
}

func (unclassifiedDescriptorTestDriver) Execute(context.Context, tool.Call, tool.UpdateSink) (tool.Result, error) {
	return tool.Result{}, nil
}

func TestToolDescriptorSeparatesWireSchemaFromDynamicPolicy(t *testing.T) {
	driver := descriptorTestDriver{policy: agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectReadOnly, RiskLevel: "low", PolicyTags: []string{"descriptor"},
		Metadata: map[string]string{"approval": "allow"}, Concurrency: tool.ConcurrencyExclusive,
	}}
	descriptor, err := DescribeTool(driver)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.WireDefinition.Name != "descriptor.test" || descriptor.WireDefinition.Description != "provider-visible description" || descriptor.WireDefinition.Concurrency != tool.ConcurrencyParallel {
		t.Fatalf("wire definition = %#v", descriptor.WireDefinition)
	}
	read := descriptor.PolicyForCall(tool.Call{Name: "descriptor.test", Arguments: json.RawMessage(`{"write":false}`)})
	write := descriptor.PolicyForCall(tool.Call{Name: "descriptor.test", Arguments: json.RawMessage(`{"write":true}`)})
	if read.Effect != agentruntime.ToolEffectReadOnly || read.Concurrency != tool.ConcurrencyExclusive {
		t.Fatalf("read policy = %#v", read)
	}
	if write.Effect != agentruntime.ToolEffectWrite || !write.RequiresActionTask {
		t.Fatalf("write policy = %#v", write)
	}
	read.PolicyTags[0] = "mutated"
	read.Metadata["approval"] = "deny"
	again := descriptor.PolicyForCall(tool.Call{Name: "descriptor.test"})
	if again.PolicyTags[0] != "descriptor" || again.Metadata["approval"] != "allow" {
		t.Fatalf("descriptor leaked mutable policy state = %#v", again)
	}
}

func TestUnclassifiedToolDescriptorFailsClosed(t *testing.T) {
	descriptor, err := DescribeTool(unclassifiedDescriptorTestDriver{})
	if err != nil {
		t.Fatal(err)
	}
	policy := descriptor.PolicyForCall(tool.Call{Name: "descriptor.unknown"})
	if policy.Effect != agentruntime.ToolEffectExternalSideEffect || !policy.RequiresApproval || !policy.RequiresActionTask || policy.RiskLevel != "high" {
		t.Fatalf("fallback policy = %#v", policy)
	}
}
