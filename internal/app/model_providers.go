package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/catalog"
	llmuxdriver "github.com/Viking602/azem/internal/provider/llmux"
)

func (s *Service) modelProviderEntries(ctx context.Context) ([]ModelProviderEntry, error) {
	accounts, err := s.authentication.Accounts(ctx, "")
	if err != nil {
		return nil, err
	}
	stored := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		if account.Status == "active" {
			stored[account.Provider] = true
		}
	}
	s.mu.Lock()
	configured := make(map[string]config.LLMuxProviderConfig, len(s.cfg.Providers.LLMux))
	for id, provider := range s.cfg.Providers.LLMux {
		configured[id] = provider
	}
	s.mu.Unlock()
	profiles := llmuxdriver.Profiles()
	entries := make([]ModelProviderEntry, 0, len(profiles))
	for _, profile := range profiles {
		provider := configured[profile.ID]
		source := "none"
		if stored[profile.ID] {
			source = "stored"
		} else if profile.EnvKey != "" && strings.TrimSpace(os.Getenv(profile.EnvKey)) != "" {
			source = "environment"
		}
		entries = append(entries, ModelProviderEntry{
			ID: profile.ID, DisplayName: profile.DisplayName, Backend: profile.Backend,
			DefaultBaseURL: profile.BaseURL, BaseURL: provider.BaseURL, EnvKey: profile.EnvKey,
			Enabled: provider.Enabled, CredentialConfigured: source != "none" || profile.AllowEmptyKey,
			CredentialSource: source, Models: cloneLLMuxModels(provider.Models),
		})
	}
	return entries, nil
}

func (s *Service) emitModelProviders(ctx context.Context, state string) error {
	if s.authentication == nil {
		return fmt.Errorf("authentication is unavailable")
	}
	entries, err := s.modelProviderEntries(ctx)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventModelProviders, State: state, ModelProviders: entries})
	for _, entry := range entries {
		if entry.Enabled {
			s.emitConfiguredModelCatalog(ctx, entry.ID, entry.Models)
		}
	}
	return nil
}

func (s *Service) updateModelProvider(ctx context.Context, entry *ModelProviderEntry, secret string) error {
	if entry == nil {
		return fmt.Errorf("model provider is required")
	}
	id := strings.ToLower(strings.TrimSpace(entry.ID))
	if _, ok := llmuxdriver.LookupProfile(id); !ok {
		return fmt.Errorf("unsupported llmux provider %q", id)
	}
	provider := config.LLMuxProviderConfig{Enabled: entry.Enabled, BaseURL: strings.TrimSpace(entry.BaseURL), Models: cloneLLMuxModels(entry.Models)}
	if err := config.ValidateLLMuxProvider(id, provider); err != nil {
		return err
	}
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.mu.Lock()
	currentSession := s.currentSession
	s.mu.Unlock()
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if strings.TrimSpace(secret) != "" {
		if s.authentication == nil {
			return fmt.Errorf("authentication is unavailable")
		}
		if _, err := s.authentication.SetAPIKey(ctx, id, secret); err != nil {
			return err
		}
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateLLMuxProvider(s.configPath, id, provider)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	if s.cfg.Providers.LLMux == nil {
		s.cfg.Providers.LLMux = map[string]config.LLMuxProviderConfig{}
	}
	s.cfg.Providers.LLMux[id] = provider
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateLLMuxProvider(id, provider)
	}
	return s.emitModelProviders(ctx, "updated")
}

func (s *Service) emitConfiguredModelCatalog(ctx context.Context, provider string, models []config.LLMuxModelConfig) {
	encoded, err := json.Marshal(configuredCatalogModels(models))
	if err == nil {
		s.emit(ctx, Event{Kind: EventModelCatalog, State: "configured", Data: map[string]string{"provider": provider, "models": string(encoded)}})
	}
}

func configuredCatalogModels(models []config.LLMuxModelConfig) []catalog.Model {
	result := make([]catalog.Model, 0, len(models))
	for _, model := range models {
		result = append(result, catalog.Model{
			ID: model.ID, Name: model.Name, ContextWindow: model.ContextWindow,
			ReasoningLevels: append([]string(nil), model.ReasoningLevels...), DefaultReasoning: model.DefaultReasoning,
			SupportsTools: true, SupportsParallel: true, SupportsReasoning: len(model.ReasoningLevels) > 0,
		})
	}
	return result
}

func cloneLLMuxModels(models []config.LLMuxModelConfig) []config.LLMuxModelConfig {
	if models == nil {
		return []config.LLMuxModelConfig{}
	}
	cloned := append([]config.LLMuxModelConfig(nil), models...)
	for i := range cloned {
		cloned[i].ReasoningLevels = append([]string(nil), models[i].ReasoningLevels...)
	}
	return cloned
}

func cloneLLMuxProviders(providers map[string]config.LLMuxProviderConfig) map[string]config.LLMuxProviderConfig {
	cloned := make(map[string]config.LLMuxProviderConfig, len(providers))
	for id, provider := range providers {
		provider.Models = cloneLLMuxModels(provider.Models)
		cloned[id] = provider
	}
	return cloned
}
