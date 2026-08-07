package llmuxdriver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverModelsUsesProviderAPIAndModelsDevCapabilities(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request = %s authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		fmt.Fprint(response, `{"data":[{"id":"openai/gpt-test","name":"API name"}]}`)
	}))
	defer api.Close()
	metadata := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `{"openrouter":{"name":"OpenRouter","api":"https://openrouter.ai/api/v1","env":["OPENROUTER_API_KEY"],"models":{"openai/gpt-test":{"name":"GPT Test","description":"Test model","attachment":true,"reasoning":true,"reasoning_options":[{"type":"effort","values":["low","medium","high"]}],"tool_call":true,"structured_output":true,"modalities":{"input":["text","image"],"output":["text"]},"limit":{"context":400000,"output":128000}}}}}`)
	}))
	defer metadata.Close()

	models, providerID, warning, err := DiscoverModels(context.Background(), DiscoveryConfig{
		Profile: Profile{ID: "openrouter", DisplayName: "OpenRouter", Backend: "openai-compatible", BaseURL: "https://openrouter.ai/api/v1", EnvKey: "OPENROUTER_API_KEY"},
		BaseURL: api.URL + "/v1", APIKey: "secret", ModelsDevURL: metadata.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if providerID != "openrouter" || warning != "" || len(models) != 1 {
		t.Fatalf("provider=%q warning=%q models=%+v", providerID, warning, models)
	}
	model := models[0]
	if model.ID != "openai/gpt-test" || model.Name != "GPT Test" || model.ContextWindow != 400000 || model.MaxOutputTokens != 128000 || !model.SupportsTools || !model.SupportsReasoning || !model.SupportsStructured {
		t.Fatalf("model = %+v", model)
	}
	if !reflect.DeepEqual(model.ReasoningLevels, []string{"low", "medium", "high"}) || model.DefaultReasoning != "high" {
		t.Fatalf("reasoning levels=%v default=%q", model.ReasoningLevels, model.DefaultReasoning)
	}
	if !reflect.DeepEqual(model.InputModalities, []string{"text", "image", "attachment"}) || !reflect.DeepEqual(model.OutputModalities, []string{"text"}) {
		t.Fatalf("modalities input=%v output=%v", model.InputModalities, model.OutputModalities)
	}
}

func TestModelsDevIDUsesModelsDevProviderKeys(t *testing.T) {
	for input, want := range map[string]string{"alibaba_coding_plan": "alibaba-coding-plan", "ai302": "302ai", "chatgpt": "openai"} {
		if got := ModelsDevID(input); got != want {
			t.Fatalf("ModelsDevID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDiscoverModelsUsesDeepSeekOpenAIModelEndpoint(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/models" || request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("x-api-key") != "" {
			http.Error(response, "wrong DeepSeek discovery request", http.StatusNotFound)
			return
		}
		fmt.Fprint(response, `{"data":[{"id":"deepseek-v4-flash"},{"id":"deepseek-v4-pro"}]}`)
	}))
	defer api.Close()
	metadata := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `{"deepseek":{"name":"DeepSeek","api":"https://api.deepseek.com","models":{"deepseek-v4-flash":{"name":"DeepSeek V4 Flash"},"deepseek-v4-pro":{"name":"DeepSeek V4 Pro"}}}}`)
	}))
	defer metadata.Close()

	models, providerID, warning, err := DiscoverModels(context.Background(), DiscoveryConfig{
		Profile: Profile{ID: "deepseek", DisplayName: "DeepSeek", Backend: "anthropic", BaseURL: api.URL + "/anthropic", EnvKey: "DEEPSEEK_API_KEY"},
		APIKey:  "secret", ModelsDevURL: metadata.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if providerID != "deepseek" || warning != "" || len(models) != 2 || models[0].Name != "DeepSeek V4 Flash" || models[1].Name != "DeepSeek V4 Pro" {
		t.Fatalf("provider=%q warning=%q models=%+v", providerID, warning, models)
	}
}

func TestDiscoverModelsFallsBackToModelsDevWhenProviderCatalogFails(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "catalog unavailable", http.StatusServiceUnavailable)
	}))
	defer api.Close()
	metadata := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `{"deepseek":{"name":"DeepSeek","api":"https://api.deepseek.com","models":{"deepseek-v4-flash":{"name":"DeepSeek V4 Flash","reasoning":true,"tool_call":true,"limit":{"context":1000000}},"deepseek-v4-pro":{"name":"DeepSeek V4 Pro","reasoning":true,"tool_call":true,"limit":{"context":1000000}}}}}`)
	}))
	defer metadata.Close()

	models, providerID, warning, err := DiscoverModels(context.Background(), DiscoveryConfig{
		Profile: Profile{ID: "deepseek", DisplayName: "DeepSeek", Backend: "anthropic", BaseURL: api.URL + "/anthropic", EnvKey: "DEEPSEEK_API_KEY"},
		APIKey:  "secret", ModelsDevURL: metadata.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if providerID != "deepseek" || len(models) != 2 || !strings.Contains(warning, "using models.dev") {
		t.Fatalf("provider=%q warning=%q models=%+v", providerID, warning, models)
	}
}
