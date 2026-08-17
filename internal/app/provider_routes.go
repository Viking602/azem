package app

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/provider/xai"
	hyprovider "github.com/Viking602/venat/provider"
)

func (r *ProviderRuntime) modelRouteSnapshot() (config.ModelRouteConfig, *subagentRuntime) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.Agents.Compaction, r.subagents
}

func (r *ProviderRuntime) titleModelRouteSnapshot() config.ModelRouteConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.Agents.Title
}

func (r *ProviderRuntime) recapModelRouteSnapshot() config.ModelRouteConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.Agents.Recap
}

func (r *ProviderRuntime) routeTurn(request TurnRequest) TurnRequest {
	if !request.PlanMode {
		return request
	}
	r.mu.RLock()
	route := r.cfg.Agents.Plan
	r.mu.RUnlock()
	if route.Provider == "" || route.Model == "" {
		return request
	}
	request.Provider, request.Model = route.Provider, route.Model
	if route.Reasoning != "" {
		request.Reasoning = route.Reasoning
	}
	return request
}

// UpdateModelRoute updates only the live routing snapshot. Existing runs keep
// the route captured when their engine or spawn profile was created.
func (r *ProviderRuntime) UpdateModelRoute(scope, role string, route config.ModelRouteConfig) {
	r.mu.Lock()
	if scope == "main" {
		r.cfg.Defaults.Provider, r.cfg.Defaults.Model, r.cfg.Defaults.Reasoning = route.Provider, route.Model, route.Reasoning
	}
	routeTargets := map[string]*config.ModelRouteConfig{
		"title":      &r.cfg.Agents.Title,
		"plan":       &r.cfg.Agents.Plan,
		"approval":   &r.cfg.Agents.Approval,
		"vision":     &r.cfg.Agents.Vision,
		"compaction": &r.cfg.Agents.Compaction,
		"recap":      &r.cfg.Agents.Recap,
	}
	if target := routeTargets[scope]; target != nil {
		*target = route
	}
	subagents := r.subagents
	if scope == "subagent" {
		current := r.cfg.Agents.Subagents.Roles[role]
		current.Provider, current.Model, current.Reasoning = route.Provider, route.Model, route.Reasoning
		r.cfg.Agents.Subagents.Roles[role] = current
		delete(r.cfg.Agents.Subagents.Models, role)
		if route == (config.ModelRouteConfig{}) {
			delete(r.cfg.Agents.Subagents.Routes, role)
		} else {
			r.cfg.Agents.Subagents.Routes[role] = route
		}
	}
	r.mu.Unlock()
	if scope == "subagent" && subagents != nil {
		subagents.updateModelRoute(role, route)
	}
}

func (r *ProviderRuntime) UpdateLLMuxProvider(id string, provider config.LLMuxProviderConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg.Providers.LLMux == nil {
		r.cfg.Providers.LLMux = map[string]config.LLMuxProviderConfig{}
	}
	provider.Models = cloneLLMuxModels(provider.Models)
	r.cfg.Providers.LLMux[id] = provider
}

func (r *ProviderRuntime) UpdateSubagentMaxConcurrency(maxConcurrency int) {
	r.mu.Lock()
	r.cfg.Agents.Subagents.MaxConcurrency = maxConcurrency
	subagents := r.subagents
	r.mu.Unlock()
	if subagents != nil {
		subagents.updateMaxConcurrency(maxConcurrency)
	}
}

func (r *ProviderRuntime) UpdateSubagentMaxDepth(maxDepth int) {
	r.mu.Lock()
	r.cfg.Agents.Subagents.MaxDepth = maxDepth
	subagents := r.subagents
	r.mu.Unlock()
	if subagents != nil {
		subagents.updateMaxDepth(maxDepth)
	}
}

func (r *ProviderRuntime) UpdateSubagentAwaitTimeout(timeout time.Duration) {
	r.mu.Lock()
	r.cfg.Agents.Subagents.AwaitTimeout = timeout.String()
	r.cfg.Agents.Subagents.AwaitDuration = timeout
	subagents := r.subagents
	r.mu.Unlock()
	if subagents != nil {
		subagents.updateAwaitTimeout(timeout)
	}
}

func (r *ProviderRuntime) UpdateSubagentIdleTimeout(timeout time.Duration) {
	r.mu.Lock()
	r.cfg.Agents.Subagents.IdleTimeout = timeout.String()
	r.cfg.Agents.Subagents.IdleDuration = timeout
	subagents := r.subagents
	r.mu.Unlock()
	if subagents != nil {
		subagents.updateIdleTimeout(timeout)
	}
}

func (r *ProviderRuntime) UpdateChatGPTFastMode(enabled bool) {
	r.mu.Lock()
	r.cfg.Providers.ChatGPT.FastMode = enabled
	r.mu.Unlock()
}

func (r *ProviderRuntime) UpdateSubscriptionDisabledModels(provider string, models []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if provider == "chatgpt" {
		r.cfg.Providers.ChatGPT.DisabledModels = append([]string(nil), models...)
	} else if provider == "grok" {
		r.cfg.Providers.Grok.DisabledModels = append([]string(nil), models...)
	}
}

func (r *ProviderRuntime) resolveDriver(ctx context.Context, providerID, modelID, requestedReasoning string) (auth.Account, string, int, hyprovider.Driver, error) {
	return r.resolveDriverForAccount(ctx, providerID, modelID, requestedReasoning, "")
}

func (r *ProviderRuntime) resolveDriverForAccount(ctx context.Context, providerID, modelID, requestedReasoning, accountID string) (auth.Account, string, int, hyprovider.Driver, error) {
	if providerID != "chatgpt" && providerID != "grok" {
		return r.resolveLLMuxDriverForAccount(ctx, providerID, modelID, requestedReasoning, accountID)
	}
	accounts, err := r.auth.Accounts(ctx, providerID)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	var account auth.Account
	for _, candidate := range accounts {
		if candidate.Status == "active" && (accountID == "" || candidate.ID == accountID) {
			account = candidate
			break
		}
	}
	if account.ID == "" {
		if accountID != "" {
			return auth.Account{}, "", 0, nil, fmt.Errorf("%s account %s is unavailable; refusing to resume with a different account", providerID, accountID)
		}
		return auth.Account{}, "", 0, nil, fmt.Errorf("sign in to %s before starting a turn", providerID)
	}
	models, err := r.catalog.List(ctx, providerID, account.ID, false)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	r.mu.RLock()
	var disabledModels []string
	if providerID == "chatgpt" {
		disabledModels = append([]string(nil), r.cfg.Providers.ChatGPT.DisabledModels...)
	} else {
		disabledModels = append([]string(nil), r.cfg.Providers.Grok.DisabledModels...)
	}
	r.mu.RUnlock()
	if modelID == "" {
		for _, model := range models.Models {
			if !slices.Contains(disabledModels, model.ID) {
				modelID = model.ID
				break
			}
		}
	}
	var selectedModel catalog.Model
	for _, model := range models.Models {
		if model.MatchesID(modelID) {
			if slices.Contains(disabledModels, model.ID) {
				return auth.Account{}, "", 0, nil, fmt.Errorf("model %q is disabled for %s", model.ID, providerID)
			}
			selectedModel = model
			break
		}
	}
	if selectedModel.ID == "" {
		return auth.Account{}, "", 0, nil, fmt.Errorf("model %q is not available for %s account %s", modelID, providerID, account.ID)
	}
	reasoningEffort, err := catalog.ResolveReasoningEffort(providerID, selectedModel, requestedReasoning)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	modelID = selectedModel.ID
	modelIDs := []string{modelID}
	switch providerID {
	case "chatgpt":
		driver, err := codex.New(r.auth, account.ID, r.ChatGPTEndpoint, modelIDs, reasoningEffort)
		r.mu.RLock()
		fastMode := r.cfg.Providers.ChatGPT.FastMode
		r.mu.RUnlock()
		if err == nil && fastMode && selectedModel.SupportsServiceTier(codex.FastServiceTier) {
			driver.SetServiceTier(codex.FastServiceTier)
		}
		return account, modelID, selectedModel.ContextWindow, driver, err
	case "grok":
		var transport xai.Transport
		switch r.cfg.Providers.Grok.Transport {
		case "", "api":
			transport = &xai.StandardTransport{Auth: r.auth, AccountID: account.ID, Endpoint: r.GrokEndpoint}
		case "cli_proxy":
			transport = &xai.CLIProxyTransport{Token: func(ctx context.Context) (string, error) {
				credential, err := r.auth.Credential(ctx, "grok", account.ID)
				return credential.AccessToken, err
			}}
		default:
			return auth.Account{}, "", 0, nil, fmt.Errorf("unsupported Grok transport %q", r.cfg.Providers.Grok.Transport)
		}
		driver, err := xai.New(transport, modelIDs, reasoningEffort)
		return account, modelID, selectedModel.ContextWindow, driver, err
	default:
		return auth.Account{}, "", 0, nil, fmt.Errorf("unsupported provider %q", providerID)
	}
}

func (r *ProviderRuntime) resolvedReasoningEffort(ctx context.Context, providerID, accountID, modelID, requested string) (string, error) {
	if providerID != "chatgpt" && providerID != "grok" {
		return r.resolvedLLMuxReasoningEffort(providerID, modelID, requested)
	}
	models, err := r.catalog.List(ctx, providerID, accountID, false)
	if err != nil {
		return "", err
	}
	for _, model := range models.Models {
		if model.MatchesID(modelID) {
			return catalog.ResolveReasoningEffort(providerID, model, requested)
		}
	}
	return "", fmt.Errorf("model %q is not available for %s account %s", modelID, providerID, accountID)
}
