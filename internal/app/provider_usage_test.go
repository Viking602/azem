package app

import (
	"context"
	"errors"
	"testing"

	hyagent "github.com/Viking602/venat/agent"
	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/config"
)

func TestCompactionUsageIsReportedSeparatelyFromMainProviderTurn(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}}},
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}},
	}}
	var compactUsage hyprovider.Usage
	driver := &compactionUsageDriver{inner: inner, report: func(usage hyprovider.Usage) { compactUsage = usage }}
	compactStream, err := driver.Stream(context.Background(), hyprovider.Request{Metadata: map[string]string{compactionRequestMetadataKey: "true"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compactStream.Recv(); err != nil {
		t.Fatal(err)
	}
	mainStream, err := driver.Stream(context.Background(), hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	done, err := mainStream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if compactUsage.InputTokens != 7 || compactUsage.OutputTokens != 3 || compactUsage.TotalTokens != 10 {
		t.Fatalf("compaction usage = %#v", compactUsage)
	}
	if done.Usage.InputTokens != 15 || done.Usage.OutputTokens != 5 || done.Usage.TotalTokens != 20 {
		t.Fatalf("main usage was contaminated = %#v", done.Usage)
	}
}

func TestProviderUsageBudgetIncludesCompactionWithoutMergingUsage(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{TotalTokens: 6}}},
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{TotalTokens: 6}}},
	}}
	driver := &budgetedProviderDriver{inner: inner, budget: &providerUsageBudget{maxTokens: 10}}
	for index := 0; index < 2; index++ {
		stream, err := driver.Stream(context.Background(), hyprovider.Request{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := driver.Stream(context.Background(), hyprovider.Request{}); !errors.Is(err, hyagent.ErrBudgetExhausted) {
		t.Fatalf("combined usage budget error=%v", err)
	}
	if len(inner.requests) != 2 {
		t.Fatalf("provider received %d requests after budget exhaustion", len(inner.requests))
	}
}

func TestLazyCompactionRouteUsesIndependentDriverCacheKeyAndUsage(t *testing.T) {
	compact := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "summary"},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete, Usage: hyprovider.Usage{TotalTokens: 10}},
	}}}
	resolveCalls := 0
	var reported hyprovider.Usage
	summarize := lazyCompactionSummarizer(func(_ context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
		resolveCalls++
		if provider != "grok" || model != "summary-model" || reasoning != "high" {
			t.Fatalf("resolved route = %s/%s/%s", provider, model, reasoning)
		}
		return model, 8_000, compact, nil
	}, config.ModelRouteConfig{Provider: "grok", Model: "summary-model", Reasoning: "high"}, "chatgpt", "main-model", "low", "session-1:compaction", nil, func(_, _, _, _ string, usage hyprovider.Usage, _, _ int) { reported = usage })
	if resolveCalls != 0 {
		t.Fatal("compaction driver resolved eagerly")
	}
	if _, err := summarize(context.Background(), "history"); err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 1 || len(compact.requests) != 1 || compact.requests[0].Model != "summary-model" {
		t.Fatalf("resolve calls=%d requests=%#v", resolveCalls, compact.requests)
	}
	request := compact.requests[0]
	if request.ExtraBody["prompt_cache_key"] != "session-1:compaction" || request.Metadata["reasoning_effort"] != "high" {
		t.Fatalf("compaction request metadata=%#v extra=%#v", request.Metadata, request.ExtraBody)
	}
	if reported.TotalTokens != 10 {
		t.Fatalf("reported compaction usage = %#v", reported)
	}
}

func TestModelContextTokenTargetRequiresCatalogMetadataAndAvoidsOverflow(t *testing.T) {
	if _, err := modelContextTokenTarget("grok", "missing", 0, 0); err == nil {
		t.Fatal("missing context window was accepted")
	}
	maxInt := int(^uint(0) >> 1)
	target, err := modelContextTokenTarget("chatgpt", "large", maxInt, 0)
	if err != nil {
		t.Fatal(err)
	}
	if target <= 0 || target > maxInt {
		t.Fatalf("large context target = %d", target)
	}
	if target, err := modelContextTokenTarget("grok", "grok-4.5", 500_000, 2_000); err != nil || target != 169_808 {
		t.Fatalf("xAI long-context target = %d, error=%v", target, err)
	}
	if target, err := modelContextTokenTarget("grok", "grok-build", 256_000, 0); err != nil || target != 171_808 {
		t.Fatalf("xAI build target = %d, error=%v", target, err)
	}
	if target, err := modelContextTokenTarget("chatgpt", "gpt", 500_000, 2_000); err != nil || target != 364_808 {
		t.Fatalf("standard target = %d, error=%v", target, err)
	}
	if target, err := modelContextTokenTarget("chatgpt", "gpt-5.6-sol", 1_050_000, 2_000); err != nil || target != 239_808 {
		t.Fatalf("GPT-5.6 pricing-aware target = %d, error=%v", target, err)
	}
}
