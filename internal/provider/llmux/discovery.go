package llmuxdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/Viking602/llmux"
	"github.com/Viking602/llmux/provider/openai"

	"github.com/Viking602/azem/internal/provider/catalog"
)

const modelsDevAPI = "https://models.dev/api.json"

type DiscoveryConfig struct {
	Profile      Profile
	BaseURL      string
	APIKey       string
	ModelsDevURL string
	Client       *http.Client
}

func DiscoverModels(ctx context.Context, config DiscoveryConfig) ([]catalog.Model, string, string, error) {
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	models, providerErr := fetchProviderModels(ctx, client, config)
	modelsDevURL := strings.TrimSpace(config.ModelsDevURL)
	if modelsDevURL == "" {
		modelsDevURL = modelsDevAPI
	}
	metadata, err := catalog.FetchModelsDev(ctx, client, modelsDevURL)
	warning := ""
	providerID := ""
	if err != nil {
		if providerErr != nil {
			return nil, "", "", fmt.Errorf("%w (models.dev: %v)", providerErr, err)
		}
		warning = "models.dev metadata unavailable: " + err.Error()
	} else if providerErr != nil {
		providerID, models = metadata.Models(catalog.ModelsDevProviderHint{
			ID: config.Profile.ID, Name: config.Profile.DisplayName, API: config.Profile.BaseURL, EnvKey: config.Profile.EnvKey,
		})
		if len(models) == 0 {
			return nil, providerID, "", providerErr
		}
		warning = "provider API unavailable: " + providerErr.Error() + "; using models.dev catalog"
	} else {
		var matched int
		providerID, matched = metadata.Enrich(catalog.ModelsDevProviderHint{
			ID: config.Profile.ID, Name: config.Profile.DisplayName, API: config.Profile.BaseURL, EnvKey: config.Profile.EnvKey,
		}, models)
		if matched < len(models) {
			warning = fmt.Sprintf("models.dev metadata did not match %d model(s)", len(models)-matched)
		}
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, providerID, warning, nil
}

func fetchProviderModels(ctx context.Context, client *http.Client, config DiscoveryConfig) ([]catalog.Model, error) {
	provider, err := newDiscoveryProvider(config, client)
	if err != nil {
		return nil, err
	}
	items, err := sdk.ListModels(ctx, provider)
	if err != nil {
		return nil, err
	}
	models := make([]catalog.Model, 0, len(items))
	for _, item := range items {
		if model := modelFromInfo(item); model.ID != "" {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("%s model catalog returned no models", config.Profile.ID)
	}
	return dedupeModels(models), nil
}

func newDiscoveryProvider(config DiscoveryConfig, client *http.Client) (sdk.Provider, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = config.Profile.BaseURL
	}
	if config.Profile.ID == "deepseek" {
		baseURL = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/anthropic")
		return openai.New(openai.Config{
			APIKey: config.APIKey, BaseURL: baseURL, Client: client,
			Retry: sdk.RetryPolicy{MaxAttempts: 1}, ProviderName: config.Profile.ID,
			APIKeyHeader: "Authorization", APIKeyPrefix: "Bearer ",
			ListModelsURL: baseURL + "/models",
		})
	}
	return newProvider(Config{
		ProviderID: config.Profile.ID, APIKey: config.APIKey, BaseURL: baseURL, Client: client,
	})
}

func modelFromInfo(info sdk.ModelInfo) catalog.Model {
	item := make(map[string]any)
	if len(info.Raw) > 0 {
		_ = json.Unmarshal(info.Raw, &item)
	}
	name := strings.TrimSpace(info.DisplayName)
	if name == "" {
		name = firstString(item, "display_name", "displayName", "name")
	}
	model := catalog.Model{
		ID: strings.TrimSpace(info.ID), Name: name, Description: stringValue(item, "description"),
		Aliases:         append(stringSlice(item["aliases"]), firstString(item, "slug", "model")),
		ContextWindow:   firstInt(item, "context_window", "context_length", "max_context_length", "inputTokenLimit"),
		MaxOutputTokens: firstInt(item, "max_output_tokens", "outputTokenLimit"),
		InputModalities: stringSlice(item["input_modalities"]), OutputModalities: stringSlice(item["output_modalities"]),
		ReasoningLevels: stringSlice(firstValue(item, "reasoning_levels", "supported_reasoning_levels")),
	}
	capabilities := append(stringSlice(item["capabilities"]), stringSlice(item["supported_parameters"])...)
	for _, capability := range capabilities {
		switch capability {
		case "tools", "tool_call", "tool_use", "tool_choice":
			model.SupportsTools = true
		case "parallel_tool_calls", "parallel_tools":
			model.SupportsParallel = true
		case "reasoning", "include_reasoning":
			model.SupportsReasoning = true
		case "structured_output", "response_format":
			model.SupportsStructured = true
		}
	}
	model.SupportsReasoning = model.SupportsReasoning || len(model.ReasoningLevels) > 0 || boolValue(item, "reasoning")
	return model
}

func ModelsDevID(id string) string {
	return catalog.ModelsDevProviderID(CanonicalProviderID(id))
}

func dedupeModels(models []catalog.Model) []catalog.Model {
	seen := make(map[string]bool, len(models))
	result := models[:0]
	for _, model := range models {
		if !seen[model.ID] {
			seen[model.ID] = true
			result = append(result, model)
		}
	}
	return result
}

func stringValue(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return value
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(item, key); value != "" {
			return value
		}
	}
	return ""
}

func firstValue(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if item[key] != nil {
			return item[key]
		}
	}
	return nil
}

func firstInt(item map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := item[key].(type) {
		case float64:
			if value > 0 {
				return int(value)
			}
		case string:
			if parsed, _ := strconv.Atoi(value); parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func boolValue(item map[string]any, key string) bool {
	value, _ := item[key].(bool)
	return value
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}
