package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const DefaultModelsDevURL = "https://models.dev/api.json"

type ModelsDevProviderHint struct {
	ID, Name, API, EnvKey string
}

type ModelsDevCatalog struct {
	providers map[string]modelsDevProvider
	byID      map[string][]modelsDevMatch
}

type modelsDevProvider struct {
	Name   string                    `json:"name"`
	API    string                    `json:"api"`
	Env    []string                  `json:"env"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Attachment       bool   `json:"attachment"`
	Reasoning        bool   `json:"reasoning"`
	ToolCall         bool   `json:"tool_call"`
	StructuredOutput bool   `json:"structured_output"`
	ReasoningOptions []struct {
		Type   string   `json:"type"`
		Values []string `json:"values"`
	} `json:"reasoning_options"`
	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

type modelsDevMatch struct {
	provider string
	model    modelsDevModel
}

func FetchModelsDev(ctx context.Context, client *http.Client, endpoint string) (ModelsDevCatalog, error) {
	if endpoint = strings.TrimSpace(endpoint); endpoint == "" {
		endpoint = DefaultModelsDevURL
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalhost(parsed.Hostname()))) {
		return ModelsDevCatalog{}, fmt.Errorf("models.dev catalog URL must use https (http is allowed only for localhost)")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ModelsDevCatalog{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return ModelsDevCatalog{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return ModelsDevCatalog{}, fmt.Errorf("models.dev returned HTTP %d", response.StatusCode)
	}
	providers := map[string]modelsDevProvider{}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&providers); err != nil {
		return ModelsDevCatalog{}, err
	}
	catalog := ModelsDevCatalog{providers: providers, byID: make(map[string][]modelsDevMatch)}
	for providerID, provider := range providers {
		for key, model := range provider.Models {
			if model.ID == "" {
				model.ID = key
			}
			match := modelsDevMatch{provider: providerID, model: model}
			for _, candidate := range modelIDKeys(key, model.ID) {
				catalog.byID[candidate] = append(catalog.byID[candidate], match)
			}
		}
	}
	return catalog, nil
}

func (c ModelsDevCatalog) Enrich(hint ModelsDevProviderHint, models []Model) (string, int) {
	providerID := c.providerID(hint)
	matched := 0
	for index := range models {
		metadata, ok := c.match(providerID, models[index])
		if !ok {
			continue
		}
		enrichModelFromModelsDev(&models[index], metadata)
		matched++
	}
	return providerID, matched
}

func (c ModelsDevCatalog) Models(hint ModelsDevProviderHint) (string, []Model) {
	providerID := c.providerID(hint)
	provider, ok := c.providers[providerID]
	if !ok {
		return providerID, nil
	}
	keys := make([]string, 0, len(provider.Models))
	for key := range provider.Models {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	models := make([]Model, 0, len(keys))
	for _, key := range keys {
		metadata := provider.Models[key]
		model := Model{ID: first(metadata.ID, key)}
		enrichModelFromModelsDev(&model, metadata)
		models = append(models, model)
	}
	return providerID, models
}

func (c ModelsDevCatalog) providerID(hint ModelsDevProviderHint) string {
	preferred := ModelsDevProviderID(hint.ID)
	if _, ok := c.providers[preferred]; ok {
		return preferred
	}
	for id, provider := range c.providers {
		if strings.EqualFold(provider.Name, hint.Name) || sameCatalogEndpoint(provider.API, hint.API) || containsFold(provider.Env, hint.EnvKey) {
			return id
		}
	}
	return preferred
}

func (c ModelsDevCatalog) match(providerID string, model Model) (modelsDevModel, bool) {
	candidates := modelIDKeys(append([]string{model.ID}, model.Aliases...)...)
	lab := inferredLab(candidates)
	bestScore := -1
	var best modelsDevModel
	for _, candidate := range candidates {
		for _, match := range c.byID[candidate] {
			score := friendlyNameScore(match.model.Name, match.model.ID)
			if match.provider == providerID {
				score += 100
			}
			if lab != "" && match.provider == lab {
				score += 200
			}
			if score > bestScore {
				bestScore, best = score, match.model
			}
		}
	}
	return best, bestScore >= 0
}

func enrichModelFromModelsDev(model *Model, metadata modelsDevModel) {
	if metadata.Name != "" {
		model.Name = metadata.Name
	}
	if model.Description == "" && metadata.Description != "" {
		model.Description = metadata.Description
	}
	if model.ContextWindow == 0 && metadata.Limit.Context > 0 {
		model.ContextWindow = metadata.Limit.Context
	}
	if model.MaxOutputTokens == 0 && metadata.Limit.Output > 0 {
		model.MaxOutputTokens = metadata.Limit.Output
	}
	if metadata.ID != "" && !model.MatchesID(metadata.ID) {
		model.Aliases = appendUnique(model.Aliases, metadata.ID)
	}
	model.SupportsTools = model.SupportsTools || metadata.ToolCall
	model.SupportsReasoning = model.SupportsReasoning || metadata.Reasoning
	model.SupportsStructured = model.SupportsStructured || metadata.StructuredOutput
	if len(model.InputModalities) == 0 {
		model.InputModalities = appendUnique(model.InputModalities, metadata.Modalities.Input...)
		if metadata.Attachment {
			model.InputModalities = appendUnique(model.InputModalities, "attachment")
		}
	}
	if len(model.OutputModalities) == 0 {
		model.OutputModalities = appendUnique(model.OutputModalities, metadata.Modalities.Output...)
	}
	if len(model.ReasoningLevels) == 0 {
		for _, option := range metadata.ReasoningOptions {
			if option.Type == "effort" {
				model.ReasoningLevels = appendUnique(model.ReasoningLevels, option.Values...)
			}
		}
	}
	if model.DefaultReasoning == "" && len(model.ReasoningLevels) > 0 {
		model.DefaultReasoning = model.ReasoningLevels[min(2, len(model.ReasoningLevels)-1)]
	}
}

func ModelsDevProviderID(id string) string {
	id = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(id)), "_", "-")
	aliases := map[string]string{
		"chatgpt": "openai", "grok": "xai", "ai302": "302ai", "bigmodel": "zhipuai",
		"cloudflare": "cloudflare-workers-ai", "copilot": "github-copilot", "github": "github-copilot",
		"firepass": "fireworks-ai", "fireworks": "fireworks-ai", "gmi": "gmicloud", "gradient-ai": "digitalocean",
		"kimi": "moonshotai", "meta-llama": "llama", "nanogpt": "nano-gpt", "novita": "novita-ai",
		"nvidia-nim": "nvidia", "opencode-zen": "opencode", "scx-ai": "scx", "wafer": "wafer.ai",
		"xiaomimimo": "xiaomi", "zhipu-v4": "zhipuai", "ollama": "ollama-cloud",
	}
	return first(aliases[id], id)
}

func modelIDKeys(values ...string) []string {
	keys := make([]string, 0, len(values)*2)
	for _, value := range values {
		value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "models/")
		value = strings.TrimPrefix(value, "~")
		if value == "" {
			continue
		}
		keys = appendUnique(keys, value)
		if slash := strings.IndexByte(value, '/'); slash >= 0 && slash+1 < len(value) {
			keys = appendUnique(keys, value[slash+1:])
		}
	}
	return keys
}

func inferredLab(candidates []string) string {
	for _, candidate := range candidates {
		if slash := strings.IndexByte(candidate, '/'); slash > 0 {
			return ModelsDevProviderID(candidate[:slash])
		}
		for prefix, provider := range map[string]string{
			"gpt-": "openai", "chatgpt-": "openai", "o1-": "openai", "o3-": "openai", "o4-": "openai",
			"claude-": "anthropic", "grok-": "xai", "gemini-": "google", "deepseek-": "deepseek",
			"kimi-": "moonshotai", "qwen-": "alibaba", "qwq-": "alibaba", "glm-": "zhipuai",
			"llama-": "llama", "mistral-": "mistral", "codestral-": "mistral", "command-": "cohere",
			"phi-": "microsoft", "minimax-": "minimax", "ernie-": "baidu", "doubao-": "volcengine",
		} {
			if strings.HasPrefix(candidate, prefix) {
				return provider
			}
		}
	}
	return ""
}

func friendlyNameScore(name, id string) int {
	if name == "" {
		return 0
	}
	score := 1
	if !strings.EqualFold(name, id) {
		score += 10
	}
	if strings.Contains(name, " ") || name != strings.ToLower(name) {
		score += 5
	}
	return score
}

func sameCatalogEndpoint(left, right string) bool {
	leftURL, leftErr := url.Parse(strings.TrimRight(left, "/"))
	rightURL, rightErr := url.Parse(strings.TrimRight(right, "/"))
	return leftErr == nil && rightErr == nil && leftURL.Host != "" && leftURL.Host == rightURL.Host && leftURL.Path == rightURL.Path
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if wanted != "" && strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}

func isLocalhost(host string) bool {
	return strings.EqualFold(host, "localhost") || strings.EqualFold(host, "127.0.0.1") || strings.EqualFold(host, "::1")
}
