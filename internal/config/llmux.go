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
	normalized := make(map[string]LLMuxProviderConfig, len(c.Providers.LLMux))
	for id, provider := range c.Providers.LLMux {
		canonical := canonicalLLMuxProviderID(id)
		if _, duplicate := normalized[canonical]; duplicate {
			return fmt.Errorf("providers.llmux repeats provider %q using legacy and canonical IDs", canonical)
		}
		if err := validateLLMuxProvider(canonical, provider); err != nil {
			return err
		}
		normalized[canonical] = provider
	}
	c.Providers.LLMux = normalized
	return nil
}

func validateLLMuxProvider(id string, provider LLMuxProviderConfig) error {
	if !mcpServerNamePattern.MatchString(id) {
		return fmt.Errorf("providers.llmux provider %q must match [a-z0-9_-]+", id)
	}
	if len(provider.Models) > 2048 {
		return fmt.Errorf("providers.llmux.%s.models must contain at most 2048 models", id)
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
		if model.ContextWindow != 0 && (model.ContextWindow < 1024 || model.ContextWindow > 10_000_000) {
			return fmt.Errorf("providers.llmux.%s model %q context_window must be 1024..10000000", id, modelID)
		}
		if model.MaxOutputTokens < 0 || model.MaxOutputTokens > 10_000_000 {
			return fmt.Errorf("providers.llmux.%s model %q max_output_tokens must be 0..10000000", id, modelID)
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
		for name, values := range map[string][]string{"capabilities": model.Capabilities, "input_modalities": model.InputModalities, "output_modalities": model.OutputModalities} {
			seenValues := make(map[string]bool, len(values))
			for index, value := range values {
				value = strings.TrimSpace(value)
				if value == "" || value != values[index] || len(value) > 64 || seenValues[value] {
					return fmt.Errorf("providers.llmux.%s model %q has an empty, oversized, or repeated %s value", id, modelID, name)
				}
				seenValues[value] = true
			}
		}
	}
	return nil
}

func ValidateLLMuxProvider(id string, provider LLMuxProviderConfig) error {
	return validateLLMuxProvider(canonicalLLMuxProviderID(id), provider)
}

func canonicalLLMuxProviderID(id string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(id)), "_", "-")
}
