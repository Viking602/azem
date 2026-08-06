package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

func (c *Config) validateLLMuxProviders() error {
	if c.Providers.LLMux == nil {
		c.Providers.LLMux = map[string]LLMuxProviderConfig{}
	}
	if len(c.Providers.LLMux) > 128 {
		return fmt.Errorf("providers.llmux must contain at most 128 providers")
	}
	for id, provider := range c.Providers.LLMux {
		if err := validateLLMuxProvider(id, provider); err != nil {
			return err
		}
	}
	return nil
}

func validateLLMuxProvider(id string, provider LLMuxProviderConfig) error {
	if !mcpServerNamePattern.MatchString(id) {
		return fmt.Errorf("providers.llmux provider %q must match [a-z0-9_-]+", id)
	}
	if len(provider.Models) > 256 {
		return fmt.Errorf("providers.llmux.%s.models must contain at most 256 models", id)
	}
	if provider.Enabled && len(provider.Models) == 0 {
		return fmt.Errorf("providers.llmux.%s must configure at least one model when enabled", id)
	}
	if provider.BaseURL != "" {
		endpoint, err := url.Parse(provider.BaseURL)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && isLoopbackHost(endpoint.Hostname()))) {
			return fmt.Errorf("providers.llmux.%s.base_url must use https (http is allowed only for localhost)", id)
		}
	}
	seen := make(map[string]struct{}, len(provider.Models))
	for _, model := range provider.Models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" || modelID != model.ID || len(modelID) > 256 {
			return fmt.Errorf("providers.llmux.%s contains an empty or oversized model ID", id)
		}
		if _, exists := seen[modelID]; exists {
			return fmt.Errorf("providers.llmux.%s repeats model %q", id, modelID)
		}
		seen[modelID] = struct{}{}
		if model.ContextWindow < 1024 || model.ContextWindow > 10_000_000 {
			return fmt.Errorf("providers.llmux.%s model %q context_window must be 1024..10000000", id, modelID)
		}
		if model.DefaultReasoning != "" && !slices.Contains(model.ReasoningLevels, model.DefaultReasoning) {
			return fmt.Errorf("providers.llmux.%s model %q default_reasoning must be listed in reasoning_levels", id, modelID)
		}
		levels := make(map[string]bool, len(model.ReasoningLevels))
		for index, level := range model.ReasoningLevels {
			level = strings.TrimSpace(level)
			if level == "" || level != model.ReasoningLevels[index] || len(level) > 64 || levels[level] {
				return fmt.Errorf("providers.llmux.%s model %q has an empty, oversized, or repeated reasoning level", id, modelID)
			}
			levels[level] = true
		}
	}
	return nil
}

func ValidateLLMuxProvider(id string, provider LLMuxProviderConfig) error {
	return validateLLMuxProvider(strings.ToLower(strings.TrimSpace(id)), provider)
}
