package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/catalog"
	llmuxdriver "github.com/Viking602/azem/internal/provider/llmux"
)

const (
	subscriptionQuotaRetryInitialDelay = 3 * time.Second
	subscriptionQuotaRetryMaxDelay     = time.Minute
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
		providerID := llmuxdriver.CanonicalProviderID(account.Provider)
		if account.Status == "active" {
			if providerID == "grok" {
				account = s.authentication.HydrateGrokAccount(ctx, account)
			}
			stored[providerID] = true
		}
	}
	for _, providerID := range []string{"chatgpt", "grok", "cursor"} {
		if account, ok := s.activeSubscriptionAccount(ctx, providerID); ok {
			activeAccounts[providerID] = subscriptionAccount{account.ID, firstNonEmpty(account.DisplayName, account.Email, account.ID)}
			stored[providerID] = true
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
		{"cursor", "Cursor 订阅", "cursor"},
	} {
		account := activeAccounts[subscription.id]
		source := "none"
		if account.id != "" {
			source = "stored"
		}
		models, warning, sourceName := s.cachedSubscriptionModels(ctx, subscription.id, account.id)
		entries = append(entries, ModelProviderEntry{
			ID: subscription.id, DisplayName: subscription.name, Backend: "subscription", Subscription: true,
			Enabled: account.id != "", CredentialConfigured: account.id != "", CredentialSource: source,
			AccountID: account.id, AccountLabel: account.label,
			ModelsDevID: subscription.logo, ModelsSource: sourceName, ModelsWarning: warning, Models: models,
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
			Models: s.llmuxConfiguredModels(ctx, profile.ID, provider.Models),
		})
	}
	return entries, nil
}

func (s *Service) discoverModelProvider(ctx context.Context, entry *ModelProviderEntry, secret string) error {
	return s.discoverModelProviderWith(ctx, entry, secret, llmuxdriver.DiscoverModels)
}

func (s *Service) discoverModelProviderWith(
	ctx context.Context,
	entry *ModelProviderEntry,
	secret string,
	discover func(context.Context, llmuxdriver.DiscoveryConfig) ([]catalog.Model, string, string, error),
) error {
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
	apiKey, credentialSource, err := s.modelProviderAPIKey(ctx, profile, secret)
	if err != nil {
		return err
	}
	models, _, _, err := discover(ctx, llmuxdriver.DiscoveryConfig{
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
	if len(discovered) > 10 {
		for index := range discovered {
			discovered[index].Disabled = true
		}
	}
	entry.ID = id
	entry.Models = discovered
	entry.ModelsSource = "provider_api_preview"
	entry.CredentialConfigured = credentialSource != "none" || profile.AllowEmptyKey
	entry.CredentialSource = credentialSource
	entries, err := s.modelProviderEntries(ctx)
	if err != nil {
		return err
	}
	replaced := false
	for index := range entries {
		if entries[index].ID == id {
			entries[index] = *entry
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, *entry)
	}
	s.emit(ctx, Event{Kind: EventModelProviders, State: "discovered", ModelProviders: entries})
	encoded, _ := json.Marshal(discovered)
	s.emit(ctx, Event{Kind: EventModelCatalog, State: "discovered", Data: map[string]string{
		"provider": id, "models": string(encoded),
	}})
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

func (s *Service) refreshSubscriptionCatalog(ctx context.Context, providerID string) error {
	if s.catalog == nil || s.authentication == nil {
		return fmt.Errorf("subscription catalog is unavailable")
	}
	accounts, err := s.authentication.Accounts(ctx, providerID)
	if err != nil {
		return err
	}
	accountID := ""
	for _, account := range accounts {
		if account.Status == "active" {
			accountID = account.ID
			break
		}
	}
	if accountID == "" {
		return fmt.Errorf("%s is not signed in", providerID)
	}
	models, err := s.catalog.List(ctx, providerID, accountID, true)
	if err != nil {
		return err
	}
	models = s.catalog.EnrichWithModelsDev(ctx, models)
	models.Models = s.catalogModelsWithAvailability(providerID, models.Models)
	encoded, err := json.Marshal(models.Models)
	if err != nil {
		return err
	}
	state := "fresh"
	if models.Stale {
		state = "stale"
	}
	s.emit(ctx, Event{Kind: EventModelCatalog, State: state, Text: models.Warning, Data: map[string]string{
		"provider": providerID, "accountID": accountID, "models": string(encoded),
	}})
	return nil
}

func (s *Service) emitModelProviders(ctx context.Context, state string) error {
	if s.authentication == nil {
		return fmt.Errorf("authentication is unavailable")
	}
	entries, err := s.modelProviderEntries(ctx)
	if err != nil {
		return err
	}
	s.applySubscriptionQuotas(entries)
	s.emit(ctx, Event{Kind: EventModelProviders, State: state, ModelProviders: entries})
	for _, entry := range entries {
		if entry.Enabled && !entry.Subscription {
			s.emitConfiguredModelCatalog(ctx, entry.ID, entry.Models)
		}
		if entry.Subscription && entry.Enabled && len(entry.Models) > 0 {
			s.emitConfiguredModelCatalog(ctx, entry.ID, entry.Models)
		}
	}
	s.scheduleSubscriptionQuotaRefresh(entries)
	s.scheduleSubscriptionCatalogRefresh(entries)
	return nil
}

func (s *Service) activeSubscriptionAccount(ctx context.Context, providerID string) (authservice.Account, bool) {
	if s.authentication == nil {
		return authservice.Account{}, false
	}
	accounts, err := s.authentication.Accounts(ctx, providerID)
	if err != nil {
		return authservice.Account{}, false
	}
	active := make([]authservice.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Status == "active" {
			active = append(active, account)
		}
	}
	if len(active) == 0 {
		return authservice.Account{}, false
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].UpdatedAt.After(active[j].UpdatedAt) })
	if s.catalog != nil {
		for _, account := range active {
			cached, found, err := s.catalog.Cached(ctx, providerID, account.ID)
			if err == nil && found && len(cached.Models) > 0 {
				return account, true
			}
		}
	}
	return active[0], true
}

func (s *Service) cachedSubscriptionModels(ctx context.Context, providerID, accountID string) ([]config.LLMuxModelConfig, string, string) {
	if accountID == "" || s.catalog == nil {
		return []config.LLMuxModelConfig{}, "", "subscription"
	}
	cached, found, err := s.catalog.Cached(ctx, providerID, accountID)
	if err != nil {
		return []config.LLMuxModelConfig{}, err.Error(), "subscription"
	}
	if !found {
		return []config.LLMuxModelConfig{}, "", "subscription"
	}
	cached = s.catalog.EnrichWithModelsDev(ctx, cached)
	models := subscriptionModelsFromCatalog(s.catalogModelsWithAvailability(providerID, cached.Models))
	source := "subscription"
	if cached.Stale {
		source = "subscription_stale"
	}
	return models, cached.Warning, source
}

func (s *Service) scheduleSubscriptionCatalogRefresh(entries []ModelProviderEntry) {
	targets := make([]ModelProviderEntry, 0, 2)
	for _, entry := range entries {
		if entry.Subscription && entry.AccountID != "" {
			targets = append(targets, entry)
		}
	}
	if len(targets) == 0 || s.catalog == nil || s.authentication == nil {
		return
	}
	go s.refreshSubscriptionCatalogs(targets)
}

func (s *Service) refreshSubscriptionCatalogs(targets []ModelProviderEntry) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	changed := false
	for _, target := range targets {
		models, err := s.catalog.List(ctx, target.ID, target.AccountID, true)
		if err != nil {
			continue
		}
		models = s.catalog.EnrichWithModelsDev(ctx, models)
		models.Models = s.catalogModelsWithAvailability(target.ID, models.Models)
		encoded, encodeErr := json.Marshal(models.Models)
		if encodeErr != nil {
			continue
		}
		state := "fresh"
		if models.Stale {
			state = "stale"
		}
		s.emit(ctx, Event{Kind: EventModelCatalog, State: state, Text: models.Warning, Data: map[string]string{
			"provider": target.ID, "accountID": target.AccountID, "models": string(encoded),
		}})
		changed = true
	}
	if !changed {
		return
	}
	entries, err := s.modelProviderEntries(ctx)
	if err != nil {
		return
	}
	s.applySubscriptionQuotas(entries)
	s.emit(ctx, Event{Kind: EventModelProviders, State: "catalog_updated", ModelProviders: entries})
}

func (s *Service) scheduleSubscriptionQuotaRefresh(entries []ModelProviderEntry) {
	if len(entries) == 0 || s.authentication == nil {
		return
	}
	for _, entry := range entries {
		if !entry.Subscription || entry.AccountID == "" || !s.beginSubscriptionQuotaRefresh(entry.ID, entry.AccountID) {
			continue
		}
		target := entry
		go s.refreshSubscriptionQuota(target)
	}
}

func (s *Service) beginSubscriptionQuotaRefresh(providerID, accountID string) bool {
	if s == nil || providerID == "" || accountID == "" {
		return false
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.subscriptionQuotas == nil {
		s.subscriptionQuotas = map[string]subscriptionQuotaSnapshot{}
	}
	snapshot, ok := s.subscriptionQuotas[providerID]
	if ok && snapshot.AccountID == accountID && snapshot.Refreshing {
		return false
	}
	if !ok || snapshot.AccountID != accountID {
		snapshot = subscriptionQuotaSnapshot{AccountID: accountID}
	}
	snapshot.Refreshing = true
	snapshot.QuotaWarning = ""
	s.subscriptionQuotas[providerID] = snapshot
	return true
}

func (s *Service) refreshSubscriptionQuota(target ModelProviderEntry) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	defer s.finishSubscriptionQuotaRefresh(target.ID, target.AccountID)
	retryAttempt := 0
	for {
		if !s.markSubscriptionQuotaRefreshing(target.ID, target.AccountID) ||
			!s.emitSubscriptionQuotaState(ctx, target, "quota_refreshing") {
			return
		}
		quota, quotaErr := s.loadSubscriptionQuota(ctx, target.ID, target.AccountID)
		snapshot := s.rememberSubscriptionQuota(target.ID, target.AccountID, quota, quotaErr)
		if !s.emitSubscriptionQuotaState(ctx, target, "quota_updated") {
			return
		}
		if quotaErr == nil || !snapshot.Refreshing {
			return
		}
		retryAttempt++
		if !s.waitForSubscriptionQuotaRetry(ctx, retryAttempt) {
			return
		}
	}
}

func (s *Service) loadSubscriptionQuota(ctx context.Context, providerID, accountID string) (authservice.SubscriptionQuota, error) {
	if s.subscriptionQuotaLookup != nil {
		return s.subscriptionQuotaLookup(ctx, providerID, accountID)
	}
	return s.authentication.SubscriptionQuota(ctx, providerID, accountID)
}

func (s *Service) emitSubscriptionQuotaState(ctx context.Context, target ModelProviderEntry, state string) bool {
	entries, err := s.modelProviderEntries(ctx)
	if err != nil {
		return false
	}
	found := false
	for index := range entries {
		if entries[index].ID == target.ID && entries[index].Subscription && entries[index].AccountID == target.AccountID {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	s.applySubscriptionQuotas(entries)
	s.emit(ctx, Event{Kind: EventModelProviders, State: state, ModelProviders: entries})
	return true
}

func (s *Service) markSubscriptionQuotaRefreshing(providerID, accountID string) bool {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	snapshot, ok := s.subscriptionQuotas[providerID]
	if !ok || snapshot.AccountID != accountID || !snapshot.Refreshing {
		return false
	}
	snapshot.QuotaWarning = ""
	s.subscriptionQuotas[providerID] = snapshot
	return true
}

func (s *Service) finishSubscriptionQuotaRefresh(providerID, accountID string) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	snapshot, ok := s.subscriptionQuotas[providerID]
	if !ok || snapshot.AccountID != accountID {
		return
	}
	snapshot.Refreshing = false
	s.subscriptionQuotas[providerID] = snapshot
}

func (s *Service) waitForSubscriptionQuotaRetry(ctx context.Context, attempt int) bool {
	delay := defaultSubscriptionQuotaRetryDelay(attempt)
	if s.subscriptionQuotaRetryDelay != nil {
		delay = s.subscriptionQuotaRetryDelay(attempt)
	}
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func defaultSubscriptionQuotaRetryDelay(attempt int) time.Duration {
	delay := subscriptionQuotaRetryInitialDelay
	for step := 1; step < attempt && delay < subscriptionQuotaRetryMaxDelay; step++ {
		if delay >= subscriptionQuotaRetryMaxDelay/2 {
			return subscriptionQuotaRetryMaxDelay
		}
		delay *= 2
	}
	if delay > subscriptionQuotaRetryMaxDelay {
		return subscriptionQuotaRetryMaxDelay
	}
	return delay
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
	if s.catalog != nil {
		if err := s.catalog.ReplaceConfiguredModels(ctx, id, provider.Models); err != nil {
			return err
		}
	}
	if s.configPath != "" {
		yamlProvider := provider
		if s.catalog != nil {
			yamlProvider.Models = nil
		}
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateLLMuxProvider(s.configPath, id, yamlProvider)
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

func (s *Service) setModelsEnabled(ctx context.Context, providerID string, modelIDs []string, enabled bool) error {
	providerID = llmuxdriver.CanonicalProviderID(providerID)
	if len(modelIDs) == 0 || len(modelIDs) > 2048 {
		return fmt.Errorf("model availability batch must contain between 1 and 2048 models")
	}
	seen := make(map[string]struct{}, len(modelIDs))
	cleaned := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || len(modelID) > 256 {
			return fmt.Errorf("model ID is required and must not exceed 256 characters")
		}
		if _, exists := seen[modelID]; exists {
			continue
		}
		seen[modelID] = struct{}{}
		cleaned = append(cleaned, modelID)
	}
	if !config.IsSubscriptionProvider(providerID) {
		if len(cleaned) != 1 {
			return fmt.Errorf("batch model availability is supported only for subscription providers")
		}
		return s.setLLMuxModelEnabled(ctx, providerID, cleaned[0], enabled)
	}
	return s.setSubscriptionModelsEnabled(ctx, providerID, cleaned, enabled)
}

func (s *Service) setLLMuxModelEnabled(ctx context.Context, providerID, modelID string, enabled bool) error {
	entries, err := s.modelProviderEntries(ctx)
	if err != nil {
		return err
	}
	for index := range entries {
		if entries[index].ID != providerID {
			continue
		}
		for modelIndex := range entries[index].Models {
			if entries[index].Models[modelIndex].ID != modelID {
				continue
			}
			entries[index].Models[modelIndex].Disabled = !enabled
			if s.catalog != nil {
				if err := s.catalog.SetConfiguredModelDisabled(ctx, providerID, modelID, !enabled); err != nil {
					return err
				}
				s.mu.Lock()
				if s.cfg.Providers.LLMux == nil {
					s.cfg.Providers.LLMux = map[string]config.LLMuxProviderConfig{}
				}
				provider := s.cfg.Providers.LLMux[providerID]
				provider.Enabled = entries[index].Enabled
				provider.BaseURL = entries[index].BaseURL
				provider.Models = cloneLLMuxModels(entries[index].Models)
				s.cfg.Providers.LLMux[providerID] = provider
				s.mu.Unlock()
				if s.providers != nil {
					s.providers.UpdateLLMuxProvider(providerID, provider)
				}
				return s.emitModelProviders(ctx, "model_availability_updated")
			}
			return s.updateModelProvider(ctx, &entries[index], "")
		}
		return fmt.Errorf("model %q is not configured for %s", modelID, providerID)
	}
	return fmt.Errorf("unsupported model provider %q", providerID)
}

func (s *Service) setSubscriptionModelsEnabled(ctx context.Context, providerID string, modelIDs []string, enabled bool) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.mu.Lock()
	currentSession := s.currentSession
	var disabled []string
	if providerID == "chatgpt" {
		disabled = append([]string(nil), s.cfg.Providers.ChatGPT.DisabledModels...)
	} else if providerID == "grok" {
		disabled = append([]string(nil), s.cfg.Providers.Grok.DisabledModels...)
	} else {
		disabled = append([]string(nil), s.cfg.Providers.Cursor.DisabledModels...)
	}
	s.mu.Unlock()
	disabled = setModelsDisabled(disabled, modelIDs, !enabled)
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateSubscriptionDisabledModels(s.configPath, providerID, disabled)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	if providerID == "chatgpt" {
		s.cfg.Providers.ChatGPT.DisabledModels = append([]string(nil), disabled...)
	} else if providerID == "grok" {
		s.cfg.Providers.Grok.DisabledModels = append([]string(nil), disabled...)
	} else {
		s.cfg.Providers.Cursor.DisabledModels = append([]string(nil), disabled...)
	}
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateSubscriptionDisabledModels(providerID, disabled)
	}
	s.emitAuthCatalog(ctx)
	return s.emitModelProviders(ctx, "model_availability_updated")
}

func setModelsDisabled(models, modelIDs []string, disabled bool) []string {
	targets := make(map[string]struct{}, len(modelIDs))
	for _, modelID := range modelIDs {
		targets[modelID] = struct{}{}
	}
	result := make([]string, 0, len(models)+len(modelIDs))
	for _, existing := range models {
		if _, changed := targets[existing]; !changed {
			result = append(result, existing)
		}
	}
	if disabled {
		result = append(result, modelIDs...)
		sort.Strings(result)
	}
	return result
}

func (s *Service) catalogModelsWithAvailability(provider string, models []catalog.Model) []catalog.Model {
	s.mu.Lock()
	var disabled []string
	if provider == "chatgpt" {
		disabled = append([]string(nil), s.cfg.Providers.ChatGPT.DisabledModels...)
	} else if provider == "grok" {
		disabled = append([]string(nil), s.cfg.Providers.Grok.DisabledModels...)
	} else if provider == "cursor" {
		disabled = append([]string(nil), s.cfg.Providers.Cursor.DisabledModels...)
	}
	s.mu.Unlock()
	result := append([]catalog.Model(nil), models...)
	for index := range result {
		result[index].Disabled = slices.Contains(disabled, result[index].ID)
	}
	return result
}

func (s *Service) emitConfiguredModelCatalog(ctx context.Context, provider string, models []config.LLMuxModelConfig) {
	encoded, err := json.Marshal(configuredCatalogModels(models))
	if err == nil {
		s.emit(ctx, Event{Kind: EventModelCatalog, State: "configured", Data: map[string]string{"provider": provider, "models": string(encoded)}})
	}
}

func subscriptionModelsFromCatalog(models []catalog.Model) []config.LLMuxModelConfig {
	result := make([]config.LLMuxModelConfig, 0, len(models))
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
		result = append(result, config.LLMuxModelConfig{
			ID: model.ID, Disabled: model.Disabled, Name: model.Name, Aliases: append([]string(nil), model.Aliases...),
			Description: model.Description, ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
			ReasoningLevels: append([]string(nil), model.ReasoningLevels...), DefaultReasoning: model.DefaultReasoning,
			Capabilities: capabilities, InputModalities: append([]string(nil), model.InputModalities...),
			OutputModalities: append([]string(nil), model.OutputModalities...),
		})
	}
	return result
}

func configuredCatalogModels(models []config.LLMuxModelConfig) []catalog.Model {
	result := make([]catalog.Model, 0, len(models))
	for _, model := range models {
		result = append(result, catalog.Model{
			ID: model.ID, Disabled: model.Disabled, Name: model.Name, Aliases: append([]string(nil), model.Aliases...), Description: model.Description, ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
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

func (s *Service) llmuxConfiguredModels(ctx context.Context, providerID string, fallback []config.LLMuxModelConfig) []config.LLMuxModelConfig {
	if s.catalog == nil {
		return cloneLLMuxModels(fallback)
	}
	models, err := s.catalog.ConfiguredModels(ctx, providerID)
	if err != nil || len(models) == 0 {
		return cloneLLMuxModels(fallback)
	}
	return cloneLLMuxModels(models)
}

func (s *Service) hydrateLLMuxModels(ctx context.Context) {
	if s.catalog == nil {
		return
	}
	s.mu.Lock()
	providers := cloneLLMuxProviders(s.cfg.Providers.LLMux)
	s.mu.Unlock()
	for id, provider := range providers {
		stored, err := s.catalog.ConfiguredModels(ctx, id)
		if err != nil {
			continue
		}
		legacy := len(provider.Models) > 0
		if len(stored) == 0 && legacy {
			if err := s.catalog.ReplaceConfiguredModels(ctx, id, provider.Models); err != nil {
				continue
			}
			stored = cloneLLMuxModels(provider.Models)
		}
		if len(stored) > 0 {
			provider.Models = cloneLLMuxModels(stored)
			s.mu.Lock()
			if s.cfg.Providers.LLMux == nil {
				s.cfg.Providers.LLMux = map[string]config.LLMuxProviderConfig{}
			}
			s.cfg.Providers.LLMux[id] = provider
			s.mu.Unlock()
			if s.providers != nil {
				s.providers.UpdateLLMuxProvider(id, provider)
			}
		}
		if !legacy || s.configPath == "" {
			continue
		}
		stripped := provider
		stripped.Models = nil
		_ = config.UpdateLLMuxProvider(s.configPath, id, stripped)
	}
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

type subscriptionQuotaSnapshot struct {
	AccountID        string
	AccountLabel     string
	AccountPlan      string
	QuotaAvailable   bool
	QuotaPeriod      string
	QuotaStartedAt   int64
	QuotaUsedPercent float64
	QuotaBreakdown   []ModelProviderQuotaBreakdown
	QuotaResetsAt    int64
	QuotaUpdatedAt   string
	QuotaBalance     string
	QuotaUnlimited   bool
	QuotaWarning     string
	Refreshing       bool
}

func (s *Service) rememberSubscriptionQuota(providerID, accountID string, quota authservice.SubscriptionQuota, quotaErr error) subscriptionQuotaSnapshot {
	if s == nil || providerID == "" || accountID == "" {
		return subscriptionQuotaSnapshot{}
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.subscriptionQuotas == nil {
		s.subscriptionQuotas = map[string]subscriptionQuotaSnapshot{}
	}
	snapshot, ok := s.subscriptionQuotas[providerID]
	if !ok || snapshot.AccountID != accountID {
		snapshot = subscriptionQuotaSnapshot{AccountID: accountID}
	}
	snapshot.AccountID = accountID
	snapshot.QuotaUpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if label := firstNonEmpty(quota.DisplayName, quota.Email); label != "" {
		snapshot.AccountLabel = label
	}
	if quota.Plan != "" {
		snapshot.AccountPlan = quota.Plan
	}
	if quotaErr != nil {
		snapshot.QuotaWarning = quotaErr.Error()
		snapshot.Refreshing = authservice.IsRetryableSubscriptionQuotaError(quotaErr)
	} else {
		snapshot.QuotaAvailable = true
		snapshot.QuotaPeriod = quota.Period
		snapshot.QuotaStartedAt = quota.StartsAt
		snapshot.QuotaUsedPercent = quota.UsedPercent
		snapshot.QuotaBreakdown = quotaBreakdownEntries(quota.Breakdown)
		snapshot.QuotaResetsAt = quota.ResetsAt
		snapshot.QuotaBalance = quota.Balance
		snapshot.QuotaUnlimited = quota.Unlimited
		snapshot.QuotaWarning = ""
		snapshot.Refreshing = false
	}
	s.subscriptionQuotas[providerID] = snapshot
	return snapshot
}

func quotaBreakdownEntries(breakdown []authservice.SubscriptionQuotaBreakdown) []ModelProviderQuotaBreakdown {
	if len(breakdown) == 0 {
		return nil
	}
	entries := make([]ModelProviderQuotaBreakdown, len(breakdown))
	for index, item := range breakdown {
		entries[index] = ModelProviderQuotaBreakdown{ID: item.ID, UsedPercent: item.UsedPercent}
	}
	return entries
}

func (s *Service) forgetSubscriptionQuota(providerID, accountID string) {
	if s == nil || providerID == "" {
		return
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	current, ok := s.subscriptionQuotas[providerID]
	if !ok {
		return
	}
	if accountID != "" && current.AccountID != accountID {
		return
	}
	delete(s.subscriptionQuotas, providerID)
}

func (s *Service) applySubscriptionQuotas(entries []ModelProviderEntry) {
	if s == nil || len(entries) == 0 {
		return
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	for index := range entries {
		snapshot, ok := s.subscriptionQuotas[entries[index].ID]
		if !ok || !entries[index].Subscription || entries[index].AccountID == "" || entries[index].AccountID != snapshot.AccountID {
			continue
		}
		applySubscriptionQuotaSnapshot(&entries[index], snapshot)
	}
}

func applySubscriptionQuotaSnapshot(entry *ModelProviderEntry, snapshot subscriptionQuotaSnapshot) {
	if entry == nil {
		return
	}
	if snapshot.AccountLabel != "" {
		entry.AccountLabel = snapshot.AccountLabel
	}
	if snapshot.AccountPlan != "" {
		entry.AccountPlan = snapshot.AccountPlan
	}
	entry.QuotaAvailable = snapshot.QuotaAvailable
	entry.QuotaPeriod = snapshot.QuotaPeriod
	entry.QuotaStartedAt = snapshot.QuotaStartedAt
	entry.QuotaUsedPercent = snapshot.QuotaUsedPercent
	entry.QuotaBreakdown = append([]ModelProviderQuotaBreakdown(nil), snapshot.QuotaBreakdown...)
	entry.QuotaResetsAt = snapshot.QuotaResetsAt
	entry.QuotaUpdatedAt = snapshot.QuotaUpdatedAt
	entry.QuotaBalance = snapshot.QuotaBalance
	entry.QuotaUnlimited = snapshot.QuotaUnlimited
	entry.QuotaWarning = snapshot.QuotaWarning
}

func cloneLLMuxProviders(providers map[string]config.LLMuxProviderConfig) map[string]config.LLMuxProviderConfig {
	cloned := make(map[string]config.LLMuxProviderConfig, len(providers))
	for id, provider := range providers {
		provider.Models = cloneLLMuxModels(provider.Models)
		cloned[id] = provider
	}
	return cloned
}
