package app

import (
	"context"
	"errors"
	"testing"

	hyagent "github.com/Viking602/venat/agent"
	hyprovider "github.com/Viking602/venat/provider"
)

func TestProviderUsageBudgetStopsNewRequestAfterReportedUsage(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}}},
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}}},
	}}
	budget := &providerUsageBudget{maxTokens: 10}
	driver := &budgetedProviderDriver{inner: inner, budget: budget}
	for range 2 {
		stream, err := driver.Stream(context.Background(), hyprovider.Request{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := driver.Stream(context.Background(), hyprovider.Request{}); !errors.Is(err, hyagent.ErrBudgetExhausted) {
		t.Fatalf("third request error=%v", err)
	}
	if budget.used != 12 {
		t.Fatalf("reported usage=%d, want 12", budget.used)
	}
}
