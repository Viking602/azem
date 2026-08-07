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
	type subscriptionAccount struct{ id, label string }
	activeAccounts := make(map[string]subscriptionAccount, 2)
	for _, account := range accounts {
		if account.Status == "active" {
			providerID := llmuxdriver.CanonicalProviderID(account.Provider)
			stored[providerID] = true
			activeAccounts[providerID] = subscriptionAccount{account.ID, firstNonEmpty(account.DisplayName, account.Email, account.ID)}
		}
	}
	s.mu.Lock()
	configured := make(map[string]config.LLMuxProviderConfig, len(s.cfg.Providers.LLMux))
	for id, provider := range s.cfg.Providers.LLMux {
		configured[llmuxdriver.CanonicalProviderID(id)] = provider
	}
	s.mu.Unlock()
	profiles := llmuxdriver.Profiles()
	entries := make([]ModelProviderEntry, 0, len(profiles)+2)
	// Subscription quota is loaded asynchronously after the catalog is emitted so
	// list_model_providers never blocks the settings UI on external HTTP calls.
	for _, subscription := range []struct{ id, name, logo string }{
		{"chatgpt", "OpenAI / ChatGPT 订阅", "openai"},
		{"grok", "Grok 订阅", "xai"},
	} {
		account := activeAccounts[subscription.id]
		source := "none"
		if account.id != "" {
			source = "stored"
		}
		entries = append(entries, ModelProviderEntry{
			ID: subscription.id, DisplayName: subscription.name, Backend: "subscription", Subscription: true,
			Enabled: account.id != "", CredentialConfigured: account.id != "", CredentialSource: source,
			AccountID: account.id, AccountLabel: account.label,
			ModelsDevID: subscription.logo, ModelsSource: "subscription", Models: []config.LLMuxModelConfig{},
		})
	}
	for _, profile := range profiles {
		provider := configured[profile.ID]
		source := "none"
		if stored[profile.ID] {
			source = "stored"
		} else if profile.EnvKey != "" && strings.TrimSpace(os.Getenv(profile.EnvKey)) != "" {
			source = "environment"
		}
		baseURL := provider.BaseURL
		if profile.BaseURL != "" {
			baseURL = profile.BaseURL
		}
		entries = append(entries, ModelProviderEntry{
			ID: profile.ID, DisplayName: profile.DisplayName, Backend: profile.Backend,
			DefaultBaseURL: profile.BaseURL, BaseURL: baseURL, EnvKey: profile.EnvKey,
			Enabled: provider.Enabled, CredentialConfigured: source != "none" || profile.AllowEmptyKey,
			CredentialSource: source, ModelsDevID: llmuxdriver.ModelsDevID(profile.ID), ModelsSource: "configured",
			Models: cloneLLMuxModels(provider.Models),
		})
	}
	return entries, nil
}

func (s *Service) discoverModelProvider(ctx context.Context, entry *ModelProviderEntry, secret string) error {
	if entry == nil {
		return fmt.Errorf("model provider is required")
	}
	id := llmuxdriver.CanonicalProviderID(entry.ID)
	profile, ok := llmuxdriver.LookupProfile(id)
	if !ok {
		return fmt.Errorf("unsupported llmux provider %q", id)
	}
	baseURL := strings.TrimSpace(entry.BaseURL)
	if profile.BaseURL != "" {
		baseURL = ""
	} else if baseURL == "" {
		return fmt.Errorf("%s requires a custom API base URL", profile.DisplayName)
	}
	probe := config.LLMuxProviderConfig{BaseURL: baseURL}
	if err := config.ValidateLLMuxProvider(id, probe); err != nil {
		return err
	}
	apiKey, source, err := s.modelProviderAPIKey(ctx, profile, secret)
	if err != nil {
		return err
	}
	models, modelsDevID, warning, err := llmuxdriver.DiscoverModels(ctx, llmuxdriver.DiscoveryConfig{
		Profile: profile, BaseURL: probe.BaseURL, APIKey: apiKey,
	})
	if err != nil {
		return err
	}
	discovered := make([]config.LLMuxModelConfig, 0, len(models))
	for _, model := range models {
		capabilities := make([]string, 0, 4)
		if model.SupportsTools {
			capabilities = append(capabilities, "tools")
		}
		if model.SupportsParallel {
			capabilities = append(capabilities, "parallel-tools")
		}
		if model.SupportsReasoning {
			capabilities = append(capabilities, "reasoning")
		}
		if model.SupportsStructured {
			capabilities = append(capabilities, "structured-output")
		}
		discovered = append(discovered, config.LLMuxModelConfig{
			ID: model.ID, Name: model.Name, Aliases: append([]string(nil), model.Aliases...), Description: model.Description,
			ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
			ReasoningLevels: append([]string(nil), model.ReasoningLevels...), DefaultReasoning: model.DefaultReasoning,
			Capabilities: capabilities, InputModalities: append([]string(nil), model.InputModalities...), OutputModalities: append([]string(nil), model.OutputModalities...),
		})
	}
	entries, err := s.modelProviderEntries(ctx)
	if err != nil {
		return err
	}
	for index := range entries {
		if entries[index].ID != id {
			continue
		}
		entries[index].Enabled = entry.Enabled
		entries[index].BaseURL = entry.BaseURL
		entries[index].Models = discovered
		entries[index].ModelsSource = "provider_api"
		if warning == "" && modelsDevID != "" {
			entries[index].ModelsSource += "+models.dev"
		}
		entries[index].ModelsWarning = warning
		entries[index].ModelsDevID = firstNonEmpty(modelsDevID, entries[index].ModelsDevID)
		entries[index].CredentialConfigured = apiKey != "" || profile.AllowEmptyKey
		entries[index].CredentialSource = source
		break
	}
	s.emit(ctx, Event{Kind: EventModelProviders, State: "discovered", Data: map[string]string{"provider": id}, ModelProviders: entries})
	return nil
}

func (s *Service) modelProviderAPIKey(ctx context.Context, profile llmuxdriver.Profile, secret string) (string, string, error) {
	if secret = strings.TrimSpace(secret); secret != "" {
		return secret, "pending", nil
	}
	if s.authentication != nil {
		accounts, err := s.authentication.Accounts(ctx, profile.ID)
		if err != nil {
			return "", "none", err
		}
		for _, account := range accounts {
			if account.Status != "active" {
				continue
			}
			credential, err := s.authentication.Credential(ctx, profile.ID, account.ID)
			if err != nil {
				return "", "none", err
			}
			return credential.AccessToken, "stored", nil
		}
	}
	if profile.EnvKey != "" {
		if value := strings.TrimSpace(os.Getenv(profile.EnvKey)); value != "" {
			return value, "environment", nil
		}
	}
	if profile.AllowEmptyKey {
		return "", "none", nil
	}
	return "", "none", fmt.Errorf("%s requires an API key before models can be fetched", profile.DisplayName)
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
		if entry.Enabled && !entry.Subscription {
			s.emitConfiguredModelCatalog(ctx, entry.ID, entry.Models)
		}
	}
	s.scheduleSubscriptionQuotaRefresh(entries)
	return nil
}

func (s *Service) scheduleSubscriptionQuotaRefresh(entries []ModelProviderEntry) {
	targets := make([]ModelProviderEntry, 0, 2)
	for _, entry := range entries {
		if entry.Subscription && entry.AccountID != "" {
			targets = append(targets, entry)
		}
	}
	if len(targets) == 0 || s.authentication == nil {
		return
	}
	go s.refreshSubscriptionQuotas(targets)
}

func (s *Service) refreshSubscriptionQuotas(targets []ModelProviderEntry) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// Rebuild the full catalog so a late quota update cannot drop concurrent provider edits.
	entries, err := s.modelProviderEntries(ctx)
	if err != nil {
		return
	}
	changed := false
	for _, target := range targets {
		for index := range entries {
			if entries[index].ID != target.ID || !entries[index].Subscription || entries[index].AccountID == "" {
				continue
			}
			quota, quotaErr := s.authentication.SubscriptionQuota(ctx, entries[index].ID, entries[index].AccountID)
			if quotaErr != nil {
				entries[index].QuotaWarning = quotaErr.Error()
				entries[index].QuotaAvailable = false
				entries[index].QuotaUsedPercent = 0
				entries[index].QuotaResetsAt = 0
				entries[index].QuotaBalance = ""
				entries[index].QuotaUnlimited = false
			} else {
				entries[index].QuotaAvailable = true
				entries[index].QuotaUsedPercent = quota.UsedPercent
				entries[index].QuotaResetsAt = quota.ResetsAt
				entries[index].QuotaBalance = quota.Balance
				entries[index].AccountPlan = quota.Plan
				entries[index].QuotaUnlimited = quota.Unlimited
				entries[index].QuotaWarning = ""
			}
			changed = true
			break
		}
	}
	if !changed {
		return
	}
	s.emit(ctx, Event{Kind: EventModelProviders, State: "quota_updated", ModelProviders: entries})
}

func (s *Service) updateModelProvider(ctx context.Context, entry *ModelProviderEntry, secret string) error {
	if entry == nil {
		return fmt.Errorf("model provider is required")
	}
	id := llmuxdriver.CanonicalProviderID(entry.ID)
	profile, ok := llmuxdriver.LookupProfile(id)
	if !ok {
		return fmt.Errorf("unsupported llmux provider %q", id)
	}
	baseURL := strings.TrimSpace(entry.BaseURL)
	if profile.BaseURL != "" {
		baseURL = ""
	} else if entry.Enabled && baseURL == "" {
		return fmt.Errorf("%s requires a custom API base URL", profile.DisplayName)
	}
	provider := config.LLMuxProviderConfig{Enabled: entry.Enabled, BaseURL: baseURL, Models: cloneLLMuxModels(entry.Models)}
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
			ID: model.ID, Name: model.Name, Aliases: append([]string(nil), model.Aliases...), Description: model.Description, ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
			ReasoningLevels: append([]string(nil), model.ReasoningLevels...), DefaultReasoning: model.DefaultReasoning,
			SupportsTools: hasCapability(model.Capabilities, "tools"), SupportsParallel: hasCapability(model.Capabilities, "parallel-tools"),
			SupportsReasoning:  hasCapability(model.Capabilities, "reasoning") || len(model.ReasoningLevels) > 0,
			SupportsStructured: hasCapability(model.Capabilities, "structured-output"),
			InputModalities:    append([]string(nil), model.InputModalities...), OutputModalities: append([]string(nil), model.OutputModalities...),
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
		cloned[i].Aliases = append([]string(nil), models[i].Aliases...)
		cloned[i].ReasoningLevels = append([]string(nil), models[i].ReasoningLevels...)
		cloned[i].Capabilities = append([]string(nil), models[i].Capabilities...)
		cloned[i].InputModalities = append([]string(nil), models[i].InputModalities...)
		cloned[i].OutputModalities = append([]string(nil), models[i].OutputModalities...)
	}
	return cloned
}

func hasCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneLLMuxProviders(providers map[string]config.LLMuxProviderConfig) map[string]config.LLMuxProviderConfig {
	cloned := make(map[string]config.LLMuxProviderConfig, len(providers))
	for id, provider := range providers {
		provider.Models = cloneLLMuxModels(provider.Models)
		cloned[id] = provider
	}
	return cloned
}
