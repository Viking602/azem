package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	llmuxdriver "github.com/Viking602/azem/internal/provider/llmux"
	hyprovider "github.com/Viking602/venat/provider"
)

func (r *ProviderRuntime) resolveLLMuxDriverForAccount(ctx context.Context, providerID, modelID, requestedReasoning, accountID string) (auth.Account, string, int, hyprovider.Driver, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	profile, ok := llmuxdriver.LookupProfile(providerID)
	if !ok {
		return auth.Account{}, "", 0, nil, fmt.Errorf("unsupported provider %q", providerID)
	}
	r.mu.RLock()
	provider, configured := r.cfg.Providers.LLMux[providerID]
	r.mu.RUnlock()
	if !configured || !provider.Enabled {
		return auth.Account{}, "", 0, nil, fmt.Errorf("enable %s in model settings before starting a turn", providerID)
	}
	selected, err := configuredModel(providerID, provider.Models, modelID)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	reasoning, err := catalog.ResolveReasoningEffort(providerID, selected, requestedReasoning)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	account, apiKey, err := r.llmuxCredential(ctx, profile, accountID)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" {
		baseURL = profile.BaseURL
	}
	driver, err := llmuxdriver.New(llmuxdriver.Config{
		ProviderID: providerID, APIKey: apiKey, BaseURL: baseURL,
		Models: []string{selected.ID}, ReasoningEffort: reasoning,
	})
	return account, selected.ID, selected.ContextWindow, driver, err
}

func (r *ProviderRuntime) llmuxCredential(ctx context.Context, profile llmuxdriver.Profile, accountID string) (auth.Account, string, error) {
	accounts, err := r.auth.Accounts(ctx, profile.ID)
	if err != nil {
		return auth.Account{}, "", err
	}
	for _, account := range accounts {
		if account.Status != "active" || (accountID != "" && account.ID != accountID) {
			continue
		}
		credential, err := r.auth.Credential(ctx, profile.ID, account.ID)
		if err != nil {
			return auth.Account{}, "", err
		}
		return account, credential.AccessToken, nil
	}
	envID := "env:" + profile.EnvKey
	if accountID == "" || accountID == envID {
		if apiKey := strings.TrimSpace(os.Getenv(profile.EnvKey)); apiKey != "" {
			return auth.Account{ID: envID, Provider: profile.ID, DisplayName: profile.EnvKey, Status: "active"}, apiKey, nil
		}
	}
	if profile.AllowEmptyKey && (accountID == "" || accountID == "anonymous") {
		return auth.Account{ID: "anonymous", Provider: profile.ID, DisplayName: profile.DisplayName, Status: "active"}, "", nil
	}
	if accountID != "" {
		return auth.Account{}, "", fmt.Errorf("%s account %s is unavailable; refusing to resume with a different account", profile.ID, accountID)
	}
	return auth.Account{}, "", fmt.Errorf("configure an API key for %s in model settings or set %s", profile.DisplayName, profile.EnvKey)
}

func (r *ProviderRuntime) resolvedLLMuxReasoningEffort(providerID, modelID, requested string) (string, error) {
	r.mu.RLock()
	provider, ok := r.cfg.Providers.LLMux[providerID]
	r.mu.RUnlock()
	if !ok || !provider.Enabled {
		return "", fmt.Errorf("provider %q is not enabled", providerID)
	}
	model, err := configuredModel(providerID, provider.Models, modelID)
	if err != nil {
		return "", err
	}
	return catalog.ResolveReasoningEffort(providerID, model, requested)
}

func configuredModel(providerID string, models []config.LLMuxModelConfig, modelID string) (catalog.Model, error) {
	if modelID == "" && len(models) > 0 {
		modelID = models[0].ID
	}
	for _, model := range configuredCatalogModels(models) {
		if model.ID == modelID {
			return model, nil
		}
	}
	return catalog.Model{}, fmt.Errorf("model %q is not configured for %s", modelID, providerID)
}
