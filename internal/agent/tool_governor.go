package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

type toolPolicyLimiter struct {
	capacity int
	permits  chan struct{}
}

type toolPolicyGovernor struct {
	mu       sync.Mutex
	limiters map[string]*toolPolicyLimiter
}

func (governor *toolPolicyGovernor) acquire(ctx context.Context, toolName string, policy agentruntime.ToolPolicy) (func(), error) {
	key, capacity, err := toolPolicyConcurrencySpec(toolName, policy)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return func() {}, nil
	}

	governor.mu.Lock()
	if governor.limiters == nil {
		governor.limiters = make(map[string]*toolPolicyLimiter)
	}
	limiter := governor.limiters[key]
	if limiter == nil {
		limiter = &toolPolicyLimiter{capacity: capacity, permits: make(chan struct{}, capacity)}
		governor.limiters[key] = limiter
	} else if limiter.capacity != capacity {
		governor.mu.Unlock()
		return nil, fmt.Errorf("tool policy concurrency group %q has limits %d and %d", key, limiter.capacity, capacity)
	}
	governor.mu.Unlock()

	select {
	case limiter.permits <- struct{}{}:
		return func() { <-limiter.permits }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func toolPolicyConcurrencySpec(toolName string, policy agentruntime.ToolPolicy) (string, int, error) {
	mode := policy.Concurrency
	if mode == "" {
		mode = tool.ConcurrencyParallel
	}
	capacity := policy.MaxConcurrency
	if capacity < 0 {
		return "", 0, fmt.Errorf("tool policy %q has negative max concurrency", toolName)
	}
	switch mode {
	case tool.ConcurrencyParallel:
	case tool.ConcurrencySequential, tool.ConcurrencyExclusive:
		if capacity > 1 {
			return "", 0, fmt.Errorf("tool policy %q mode %s requires max concurrency 0 or 1", toolName, mode)
		}
		capacity = 1
	default:
		return "", 0, fmt.Errorf("tool policy %q has concurrency mode %q", toolName, mode)
	}
	if capacity == 0 {
		return "", 0, nil
	}
	group := strings.TrimSpace(policy.ConcurrencyGroup)
	if group == "" {
		group = strings.TrimSpace(toolName)
	}
	if group == "" {
		return "", 0, errors.New("tool policy concurrency requires a tool name or group")
	}
	return group, capacity, nil
}

// ExecutePolicyCall applies call-specific descriptor concurrency before invoking
// a driver. Approval and durable action policy remain owned by the caller.
func (service *Service) ExecutePolicyCall(ctx context.Context, driver tool.Driver, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	if service == nil {
		return tool.Result{}, errors.Join(tool.ErrNotExecuted, errors.New("agent service is nil"))
	}
	descriptor, err := DescribeTool(driver)
	if err != nil {
		return tool.Result{}, errors.Join(tool.ErrNotExecuted, err)
	}
	if call.Name != descriptor.WireDefinition.Name {
		return tool.Result{}, errors.Join(tool.ErrNotExecuted, fmt.Errorf("tool call %q does not match driver %q", call.Name, descriptor.WireDefinition.Name))
	}
	release, err := service.toolGovernor.acquire(ctx, call.Name, descriptor.PolicyForCall(call))
	if err != nil {
		return tool.Result{}, errors.Join(tool.ErrNotExecuted, err)
	}
	defer release()
	return driver.Execute(ctx, call, sink)
}
