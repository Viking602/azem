package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/customtools"
	"github.com/Viking602/azem/internal/extensions"
	llmuxdriver "github.com/Viking602/azem/internal/provider/llmux"
)

var extensionEnvKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,127}$`)

type extensionProviderConfig struct {
	BaseURL    string            `json:"baseUrl"`
	APIKey     string            `json:"apiKey"`
	API        string            `json:"api"`
	Headers    map[string]string `json:"headers"`
	AuthHeader bool              `json:"authHeader"`
	Models     []struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		API           string   `json:"api"`
		Reasoning     bool     `json:"reasoning"`
		Input         []string `json:"input"`
		ContextWindow int      `json:"contextWindow"`
		MaxTokens     int      `json:"maxTokens"`
	} `json:"models"`
}

type extensionAgentConfig struct {
	Description  string   `json:"description"`
	SystemPrompt string   `json:"systemPrompt"`
	Instructions string   `json:"instructions"`
	Model        any      `json:"model"`
	Thinking     string   `json:"thinkingLevel"`
	Tools        []string `json:"tools"`
	Isolation    string   `json:"isolation"`
}

func applyExtensionProviders(cfg *config.Config, providers []customtools.ExtensionProvider) []string {
	if cfg.Providers.LLMux == nil {
		cfg.Providers.LLMux = make(map[string]config.LLMuxProviderConfig)
	}
	var diagnostics []string
	for _, registered := range providers {
		id := llmuxdriver.CanonicalProviderID(registered.Name)
		if _, exists := cfg.Providers.LLMux[id]; exists {
			continue
		}
		var value extensionProviderConfig
		if err := json.Unmarshal(registered.Config, &value); err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("extension provider %s: %v", id, err))
			continue
		}
		backend := extensionProviderBackend(value.API)
		provider := config.LLMuxProviderConfig{
			Enabled: true, BaseURL: strings.TrimSpace(value.BaseURL), DisplayName: registered.Name,
			Backend: backend, RuntimeHeaders: cloneExtensionHeaders(value.Headers),
		}
		apiKey := strings.TrimSpace(value.APIKey)
		if extensionEnvKeyPattern.MatchString(apiKey) {
			provider.EnvKey = apiKey
		} else if strings.HasPrefix(apiKey, "env:") {
			provider.EnvKey = strings.TrimPrefix(apiKey, "env:")
		} else if apiKey != "" {
			provider.RuntimeAPIKey = apiKey
		} else {
			provider.AllowEmptyKey = true
		}
		for _, model := range value.Models {
			levels := []string{}
			defaultReasoning := ""
			if model.Reasoning {
				levels, defaultReasoning = []string{"low", "medium", "high"}, "medium"
			}
			inputs := append([]string(nil), model.Input...)
			if len(inputs) == 0 {
				inputs = []string{"text"}
			}
			capabilities := []string{"tools"}
			if slices.Contains(inputs, "image") {
				capabilities = append(capabilities, "vision")
			}
			provider.Models = append(provider.Models, config.LLMuxModelConfig{
				ID: strings.TrimSpace(model.ID), Name: firstNonEmpty(model.Name, model.ID),
				ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxTokens,
				ReasoningLevels: levels, DefaultReasoning: defaultReasoning, Capabilities: capabilities,
				InputModalities: inputs, OutputModalities: []string{"text"},
			})
			if provider.Backend == "" {
				provider.Backend = extensionProviderBackend(model.API)
			}
		}
		if provider.Backend == "" || provider.BaseURL == "" || len(provider.Models) == 0 {
			diagnostics = append(diagnostics, fmt.Sprintf("extension provider %s requires api, baseUrl, and models", id))
			continue
		}
		if err := config.ValidateLLMuxProvider(id, provider); err != nil {
			diagnostics = append(diagnostics, err.Error())
			continue
		}
		cfg.Providers.LLMux[id] = provider
	}
	return diagnostics
}

func mergeExtensionAgents(cfg *config.Config, agents []extensions.Agent, registered []customtools.ExtensionAgent) []string {
	all := append([]extensions.Agent(nil), agents...)
	var diagnostics []string
	for _, current := range registered {
		var value extensionAgentConfig
		if err := json.Unmarshal(current.Config, &value); err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("extension agent %s: %v", current.Name, err))
			continue
		}
		all = append(all, extensions.Agent{
			Name: current.Name, Description: value.Description,
			SystemPrompt: firstNonEmpty(value.SystemPrompt, value.Instructions), Models: extensionStringList(value.Model),
			Thinking: value.Thinking, Tools: append([]string(nil), value.Tools...), Isolation: value.Isolation, Source: current.ModulePath,
		})
	}
	for _, agent := range all {
		if existing, exists := cfg.Agents.Subagents.Roles[agent.Name]; exists && existing.Source != "builtin" {
			continue
		}
		tools := normalizeExtensionAgentTools(agent.Tools)
		role := config.SubagentRoleConfig{
			Description: agent.Description, Instructions: agent.SystemPrompt, Reasoning: agent.Thinking,
			CapabilityMode: agentCapabilityMode(tools), Isolation: normalizeAgentIsolation(agent.Isolation),
			Tools: tools, Source: agent.Source,
		}
		if len(agent.Models) > 0 {
			applyAgentModel(cfg, &role, agent.Models[0])
		}
		cfg.Agents.Subagents.Roles[agent.Name] = role
	}
	return diagnostics
}

func extensionProviderBackend(api string) string {
	api = strings.ToLower(strings.TrimSpace(api))
	switch {
	case strings.Contains(api, "anthropic"):
		return "anthropic"
	case strings.Contains(api, "google"), strings.Contains(api, "gemini"):
		return "google"
	case strings.Contains(api, "mistral"):
		return "mistral"
	case strings.Contains(api, "cohere"):
		return "cohere"
	case strings.Contains(api, "xai"):
		return "xai"
	case strings.Contains(api, "openai"):
		return "openai-compatible"
	default:
		return ""
	}
}

func cloneExtensionHeaders(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		if key = strings.TrimSpace(key); key != "" && !strings.ContainsAny(key, "\r\n") && !strings.ContainsAny(value, "\r\n") {
			result[key] = value
		}
	}
	return result
}

func extensionStringList(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var result []string
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func agentCapabilityMode(tools []string) string {
	if len(tools) == 0 {
		return "read-only"
	}
	for _, name := range tools {
		if strings.Contains(name, "write") || strings.Contains(name, "edit") || strings.Contains(name, "delete") || name == "coding.shell" || name == "bash" {
			return "all"
		}
	}
	return "read-only"
}

func normalizeAgentIsolation(value string) string {
	if value == "worktree" {
		return "worktree"
	}
	return "none"
}

func normalizeExtensionAgentTools(tools []string) []string {
	aliases := map[string]string{
		"read": "coding.read_file", "grep": "coding.search", "glob": "coding.glob",
		"edit": "coding.edit_hashline", "write": "coding.write_file", "bash": "coding.shell",
		"task": "subagent.spawn",
	}
	result := make([]string, 0, len(tools))
	seen := make(map[string]bool)
	for _, name := range tools {
		name = strings.TrimSpace(name)
		if alias := aliases[name]; alias != "" {
			name = alias
		}
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

func applyAgentModel(cfg *config.Config, role *config.SubagentRoleConfig, selector string) {
	selector = strings.TrimSpace(selector)
	if strings.HasPrefix(selector, "@") {
		if route, ok := cfg.Agents.Subagents.Routes[strings.TrimPrefix(selector, "@")]; ok {
			role.Provider, role.Model = route.Provider, route.Model
			if role.Reasoning == "" {
				role.Reasoning = route.Reasoning
			}
		}
		return
	}
	if provider, model, found := strings.Cut(selector, "/"); found {
		role.Provider, role.Model = provider, model
	} else {
		role.Model = selector
	}
}

func (s *Service) AttachThemes(themes []extensions.Theme, diagnostics []string) {
	s.extensionThemes = append([]extensions.Theme(nil), themes...)
	s.extensionDiagnostics = append([]string(nil), diagnostics...)
}

func (s *Service) emitThemeCatalog(ctx context.Context, state string) error {
	encoded, err := json.Marshal(s.extensionThemes)
	if err != nil {
		return err
	}
	diagnostics, err := json.Marshal(s.extensionDiagnostics)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventThemeCatalog, State: state, Data: map[string]string{"themes": string(encoded), "diagnostics": string(diagnostics)}})
	return nil
}
