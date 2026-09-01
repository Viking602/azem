package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

func TestAgentRequestBudgetStopsBeforeOverspentToolDispatch(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventToolCall, ToolCall: &message.ToolCall{ID: "call-1", Name: "lookup", Arguments: []byte(`{}`)}},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonToolUse, Usage: hyprovider.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}},
		},
		{
			{Kind: hyprovider.EventToolCall, ToolCall: &message.ToolCall{ID: "call-2", Name: "lookup", Arguments: []byte(`{}`)}},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonToolUse, Usage: hyprovider.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}},
		},
	}}
	engine := hyagent.Engine{
		Provider: inner,
		Tools: tool.NewBus(planModeTestDriver{
			definition: tool.Definition{Name: "lookup", InputSchema: tool.Schema{Type: "object"}},
			policy:     agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly},
		}),
		Model:      "test",
		ToolMode:   tool.ModeParallel,
		LoopPolicy: hyagent.LoopPolicy{UnlimitedIterations: true},
	}
	result := engine.Run(context.Background(), hyagent.Request{
		Prompt: "use the tool until the budget stops",
		Budget: &hyagent.Budget{MaxTokens: 10},
	}, hyagent.OutputPolicy{})
	if result.Failure == nil || !errors.Is(result.Failure, hyagent.ErrBudgetExhausted) {
		t.Fatalf("failure=%v, want budget exhausted", result.Failure)
	}
	if len(inner.requests) != 2 || result.ToolCallsUsed != 1 || result.Usage.TotalTokens != 12 {
		t.Fatalf("provider requests=%d tool calls=%d usage=%+v", len(inner.requests), result.ToolCallsUsed, result.Usage)
	}
}
