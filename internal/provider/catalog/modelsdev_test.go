package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestModelsDevEnrichmentResolvesProviderAliasesAndModelAliases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `{
			"openai":{"name":"OpenAI","models":{"gpt-test":{"id":"gpt-test","name":"GPT Test","description":"Canonical","reasoning":true,"tool_call":true,"limit":{"context":400000}}}},
			"openrouter":{"name":"OpenRouter","models":{"openai/gpt-test":{"id":"openai/gpt-test","name":"OpenAI GPT Test"}}}
		}`)
	}))
	defer server.Close()

	metadata, err := FetchModelsDev(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	models := []Model{{ID: "provider-specific-gpt-test", Aliases: []string{"openai/gpt-test"}}}
	provider, matched := metadata.Enrich(ModelsDevProviderHint{ID: "openrouter"}, models)
	if provider != "openrouter" || matched != 1 {
		t.Fatalf("provider=%q matched=%d", provider, matched)
	}
	if models[0].Name != "GPT Test" || models[0].Description != "Canonical" || models[0].ContextWindow != 400000 || !models[0].SupportsTools || !models[0].SupportsReasoning {
		t.Fatalf("model=%+v", models[0])
	}
	if !models[0].MatchesID("provider-specific-gpt-test") || !models[0].MatchesID("openai/gpt-test") || !models[0].MatchesID("gpt-test") {
		t.Fatalf("aliases=%v", models[0].Aliases)
	}
	if got := ModelsDevProviderID("opencode_zen"); got != "opencode" {
		t.Fatalf("ModelsDevProviderID(opencode_zen)=%q", got)
	}
}

func TestServiceCachesModelsDevAndEnrichesSubscriptionCatalog(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(response, `{"openai":{"models":{"gpt-5.6-sol":{"id":"gpt-5.6-sol","name":"GPT-5.6 Sol","limit":{"context":1050000}}}}}`)
	}))
	defer server.Close()
	service := NewService(nil, nil)
	service.ModelsDevURL, service.ModelsDevClient = server.URL, server.Client()
	for range 2 {
		result := service.EnrichWithModelsDev(context.Background(), Result{Provider: "chatgpt", Models: []Model{{ID: "gpt-5.6-sol", Name: "gpt-5.6-sol", ContextWindow: 272000}}})
		if result.Models[0].Name != "GPT-5.6 Sol" || result.Models[0].ContextWindow != 272000 || result.Warning != "" {
			t.Fatalf("result=%+v", result)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("models.dev calls=%d", calls.Load())
	}
}

func TestModelsDevOnlyFillsMissingProviderMetadata(t *testing.T) {
	metadata := modelsDevModel{ID: "gpt-test", Name: "Friendly Name", Description: "models.dev description", Attachment: true}
	metadata.Limit.Context, metadata.Limit.Output = 400000, 128000
	metadata.Modalities.Input, metadata.Modalities.Output = []string{"text", "image"}, []string{"text", "audio"}
	metadata.ReasoningOptions = append(metadata.ReasoningOptions, struct {
		Type   string   `json:"type"`
		Values []string `json:"values"`
	}{Type: "effort", Values: []string{"low", "high"}})
	model := Model{
		ID: "provider/gpt-test", Name: "Provider ID", Description: "provider description",
		ContextWindow: 128000, MaxOutputTokens: 32000, ReasoningLevels: []string{"medium"},
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
	}

	enrichModelFromModelsDev(&model, metadata)
	if model.Name != "Friendly Name" || model.Description != "provider description" || model.ContextWindow != 128000 || model.MaxOutputTokens != 32000 {
		t.Fatalf("scalar metadata=%+v", model)
	}
	if fmt.Sprint(model.ReasoningLevels) != "[medium]" || fmt.Sprint(model.InputModalities) != "[text]" || fmt.Sprint(model.OutputModalities) != "[text]" {
		t.Fatalf("provider lists were overwritten: %+v", model)
	}
}
