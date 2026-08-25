package benchmark

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeExecutor struct {
	active    atomic.Int32
	maxActive atomic.Int32
	mu        sync.Mutex
	coldDone  map[string]bool
	t         *testing.T
}

func (executor *fakeExecutor) Execute(_ context.Context, query Query) Observation {
	active := executor.active.Add(1)
	for {
		current := executor.maxActive.Load()
		if active <= current || executor.maxActive.CompareAndSwap(current, active) {
			break
		}
	}
	defer executor.active.Add(-1)
	if query.PairID > 0 {
		key := fmt.Sprintf("%s/%s/%d", query.Provider, query.Model, query.PairID)
		executor.mu.Lock()
		if query.Warm && !executor.coldDone[key] {
			executor.t.Errorf("warm cache query ran before cold query: %s", key)
		}
		if !query.Warm {
			executor.coldDone[key] = true
		}
		executor.mu.Unlock()
	}
	time.Sleep(5 * time.Millisecond)
	latency := 20 * time.Millisecond
	if query.Warm {
		latency = 10 * time.Millisecond
	}
	return Observation{Latency: latency, TimeToFirst: latency / 2, InputTokens: 100, OutputTokens: 20, CachedTokens: map[bool]int64{true: 80}[query.Warm], CacheReported: query.PairID > 0}
}

func TestManagerRunsProfilesInParallelAndSummarizes(t *testing.T) {
	executor := &fakeExecutor{coldDone: make(map[string]bool), t: t}
	manager, err := New(executor)
	if err != nil {
		t.Fatal(err)
	}
	report, err := manager.Run(context.Background(), Options{Models: []string{"openai/a", "anthropic/b"}, Profile: ProfileMix, Runs: 6, Parallel: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Observations) != 12 || len(report.Models) != 2 || executor.maxActive.Load() < 2 || report.Models[0].Succeeded != 6 || report.Models[0].Profiles[ProfileChat] != 2 || report.Models[0].Profiles[ProfilePrefill] != 2 || report.Models[0].Profiles[ProfileGeneration] != 2 {
		t.Fatalf("report=%#v max=%d", report, executor.maxActive.Load())
	}
	if report.Models[0].TokensPerSecond <= 0 || report.Models[0].LatencyP95 == 0 {
		t.Fatalf("model summary=%#v", report.Models[0])
	}
}

func TestCachePairsStaySequentialWhilePairsRunConcurrently(t *testing.T) {
	executor := &fakeExecutor{coldDone: make(map[string]bool), t: t}
	manager, _ := New(executor)
	report, err := manager.Run(context.Background(), Options{Models: []string{"openai/a"}, Cache: true, CachePairs: 4, CacheConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Observations) != 8 || executor.maxActive.Load() != 2 || report.Models[0].WarmSpeedup <= 1 || !report.Models[0].CacheReported {
		t.Fatalf("cache report=%#v max=%d", report, executor.maxActive.Load())
	}
}

func TestHTTPExecutorStreamsGatewayUsageAndCredentialHeaders(t *testing.T) {
	var authorization, provider string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization, provider = request.Header.Get("Authorization"), request.Header.Get("X-Azem-Provider")
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"prompt_tokens_details\":{\"cached_tokens\":80}}}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	executor, err := NewHTTPExecutor(HTTPExecutorOptions{GatewayURL: server.URL, Token: "gateway-token"})
	if err != nil {
		t.Fatal(err)
	}
	observation := executor.Execute(context.Background(), Query{Provider: "openai", Model: "gpt-test", Prompt: "hello", MaxTokens: 32})
	if observation.Error != "" || observation.InputTokens != 100 || observation.OutputTokens != 20 || observation.CachedTokens != 80 || !observation.CacheReported || observation.TimeToFirst <= 0 || authorization != "Bearer gateway-token" || provider != "openai" {
		t.Fatalf("observation=%#v auth=%q provider=%q", observation, authorization, provider)
	}
}
