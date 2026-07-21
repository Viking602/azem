package session

import (
	"context"
	"path/filepath"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestUsageApplyAndPersistAcrossReload(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)

	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Usage", ProviderID: "chatgpt", ModelID: "gpt-main"}); err != nil {
		t.Fatal(err)
	}

	var usage Usage
	usage.Apply(map[string]string{
		"inputTokens": "68000", "cachedInputTokens": "34000", "outputTokens": "4000",
		"uncachedInputTokens": "34000", "requestKind": "main",
		"cacheStatus": "reported", "contextLimit": "272000", "provider": "grok", "model": "grok-4.5", "transport": "xai-responses",
	})
	usage.Apply(map[string]string{"reasoningTokens": "1200", "requestKind": "main", "aggregateOnly": "true"})
	usage.Apply(map[string]string{
		"inputTokens": "50", "cachedInputTokens": "40", "outputTokens": "5",
		"uncachedInputTokens": "10", "reasoningTokens": "3", "requestKind": "compaction",
		"cacheStatus": "reported", "aggregateOnly": "true",
	})
	if usage.InputTokens != 68000 || usage.OutputTokens != 4000 {
		t.Fatalf("main occupancy = %+v", usage)
	}
	if usage.MainCacheInput != 68000 || usage.MainCachedInput != 34000 {
		t.Fatalf("main cache = %+v", usage)
	}
	if usage.CacheInputTokens != 68050 || usage.CachedInputTokens != 34040 {
		t.Fatalf("aggregate cache = %+v", usage)
	}
	if usage.UncachedInputTokens != 34000 || usage.ReasoningTokens != 1200 || usage.CompactionInput != 50 || usage.CompactionCached != 40 || usage.CompactionUncached != 10 || usage.CompactionOutput != 5 || usage.CompactionReasoning != 3 {
		t.Fatalf("detailed usage = %#v", usage)
	}
	if usage.LastRequestKind != "compaction" || usage.LastProvider != "grok" || usage.LastModel != "grok-4.5" || usage.LastTransport != "xai-responses" {
		t.Fatalf("usage attribution = %#v", usage)
	}
	if err := service.UpdateUsage(ctx, "session", usage); err != nil {
		t.Fatal(err)
	}

	loaded, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Usage != usage {
		t.Fatalf("reloaded usage = %+v, want %+v", loaded.Usage, usage)
	}
}

func TestUsageResetPreservesContextLimit(t *testing.T) {
	usage := Usage{InputTokens: 10, OutputTokens: 2, ContextLimit: 1000, CacheReported: true}
	usage.Reset()
	if usage != (Usage{ContextLimit: 1000}) {
		t.Fatalf("reset usage = %+v", usage)
	}
}
