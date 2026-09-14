package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	catalogsvc "github.com/Viking602/azem/internal/provider/catalog"
	llmuxdriver "github.com/Viking602/azem/internal/provider/llmux"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestChatGPTExtendedContextUpdatesRoutingAndRestoresCatalogDefaults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	root := t.TempDir()
	credentials, err := authservice.NewFileStore(filepath.Join(root, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	now := time.Now().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "context-account", "chatgpt", "file:chatgpt:context-account", "active", now, now); err != nil {
		t.Fatal(err)
	}
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer metadata.Close()
	models := catalogsvc.NewService(store.DB(), authentication)
	models.ModelsDevURL = metadata.URL
	defaults := []catalogsvc.Model{
		{ID: "gpt-5.6-sol", ContextWindow: 272000, Aliases: []string{"sol-alias"}, SupportsTools: true, SupportsParallel: true, ServiceTiers: []catalogsvc.ServiceTier{{ID: "priority"}}},
		{ID: "gpt-6-astra", ContextWindow: 272000, SupportsTools: true},
		{ID: "gpt-5.6-luna", ContextWindow: 272000, SupportsTools: true},
	}
	var refreshes atomic.Int32
	models.Fetchers["chatgpt"] = func(context.Context, string) ([]catalogsvc.Model, error) {
		refreshes.Add(1)
		return append([]catalogsvc.Model(nil), defaults...), nil
	}
	if _, err := models.List(ctx, "chatgpt", "context-account", true); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	service := NewService(ctx, cfg)
	service.configPath = filepath.Join(root, "config.yaml")
	service.AttachAuth(authentication, models)
	service.providers = &ProviderRuntime{cfg: cfg, auth: authentication, catalog: models}
	service.subscriptionQuotaLookup = func(context.Context, string, string) (authservice.SubscriptionQuota, error) {
		refreshes.Add(1)
		return authservice.SubscriptionQuota{}, nil
	}
	check := func(extended []string) {
		t.Helper()
		loaded, err := config.Load(service.configPath, root)
		if err != nil || !slices.Equal(loaded.Providers.ChatGPT.ExtendedContextModels, extended) {
			t.Fatalf("persisted choice = %v, err = %v", loaded.Providers.ChatGPT.ExtendedContextModels, err)
		}
		restarted := &ProviderRuntime{cfg: loaded, auth: authentication, catalog: models}
		cached, _, err := models.Cached(ctx, "chatgpt", "context-account")
		if err != nil {
			t.Fatal(err)
		}
		projected := service.catalogModelsWithAvailability("chatgpt", cached.Models)
		settings := subscriptionModelsFromCatalog(projected)
		for i, model := range cached.Models {
			enabled := slices.Contains(extended, model.ID)
			want := model.ContextWindow
			if enabled {
				want = 1_050_000
			}
			if model.ExtendedContext || model.ContextWindow != 272000 && model.ContextWindow != 300000 {
				t.Fatalf("override contaminated source catalog: %+v", model)
			}
			if projected[i].ContextWindow != want || settings[i].ExtendedContext != enabled || configuredCatalogModels(settings)[i].ExtendedContext != enabled {
				t.Fatalf("projection %s = %+v", model.ID, settings[i])
			}
			for _, runtime := range []*ProviderRuntime{service.providers, restarted} {
				requested := model.ID
				if len(model.Aliases) > 0 {
					requested = model.Aliases[0]
				}
				_, resolved, window, _, err := runtime.resolveDriver(ctx, "chatgpt", requested, "")
				if err != nil || resolved != model.ID || window != want {
					t.Fatalf("runtime %s = %s/%d, err = %v", requested, resolved, window, err)
				}
				manual, err := runtime.cachedManualCompactionModel(ctx, "chatgpt", requested)
				if err != nil || manual.ContextWindow != want {
					t.Fatalf("manual compaction %s = %+v, err = %v", requested, manual, err)
				}
				budget, err := calculateContextBudget(resolved, window, 1000, cfg.Agents.Context)
				if err != nil || budget.ContextWindow != want || enabled && budget.Trigger != 891500 {
					t.Fatalf("budget = %+v, err = %v", budget, err)
				}
			}
		}
		for _, model := range projected {
			if model.ID == "gpt-5.6-sol" && (!model.SupportsParallel || !model.SupportsServiceTier("priority")) {
				t.Fatal("context toggle lost capabilities")
			}
		}
		other := service.catalogModelsWithAvailability("cursor", defaults)
		if other[0].ContextWindow != defaults[0].ContextWindow || other[0].ExtendedContext {
			t.Fatal("ChatGPT setting crossed provider boundary")
		}
	}
	for _, step := range []struct {
		model   string
		enabled bool
		want    []string
	}{
		{"gpt-5.6-sol", true, []string{"gpt-5.6-sol"}},
		{"gpt-6-astra", true, []string{"gpt-5.6-sol", "gpt-6-astra"}},
		{"gpt-5.6-sol", false, []string{"gpt-6-astra"}},
		{"gpt-6-astra", false, nil},
	} {
		if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelExtendedContext, Target: "chatgpt", Name: step.model, Decision: fmt.Sprint(step.enabled)}); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []EventKind{EventAuthState, EventModelCatalog, EventModelProviders} {
			event, err := service.NextEvent(ctx)
			if err != nil || event.Kind != kind {
				t.Fatalf("toggle event = %s, want %s, err = %v", event.Kind, kind, err)
			}
			if kind == EventModelProviders && event.State != "model_context_updated" {
				t.Fatalf("provider event state = %s", event.State)
			}
		}
		check(step.want)
		quiet, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		_, err := service.NextEvent(quiet)
		stop()
		if err == nil || refreshes.Load() != 1 {
			t.Fatal("context toggle triggered a catalog/quota refresh or extra event")
		}
	}
	for _, action := range []Action{
		{Target: "cursor", Name: "gpt-6-astra", Decision: "true"},
		{Target: "chatgpt", Name: "gpt-5.6-luna", Decision: "true"},
		{Target: "chatgpt", Name: "gpt-6-astra", Decision: "sometimes"},
	} {
		action.Kind = ActionSetModelExtendedContext
		if err := service.ExecuteAction(ctx, action); err == nil {
			t.Fatalf("invalid setting accepted: %+v", action)
		}
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelExtendedContext, Target: "chatgpt", Name: "gpt-5.6-sol", Decision: "true"}); err != nil {
		t.Fatal(err)
	}
	defaults[0].ContextWindow = 300000
	if _, err := models.List(ctx, "chatgpt", "context-account", true); err != nil {
		t.Fatal(err)
	}
	check([]string{"gpt-5.6-sol"})
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelExtendedContext, Target: "chatgpt", Name: "gpt-5.6-sol", Decision: "false"}); err != nil {
		t.Fatal(err)
	}
	check(nil)
	service.configPath = root // A failed write must leave both live snapshots unchanged.
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelExtendedContext, Target: "chatgpt", Name: "gpt-6-astra", Decision: "true"}); err == nil {
		t.Fatal("failed persistence was accepted")
	}
	if len(service.cfg.Providers.ChatGPT.ExtendedContextModels) != 0 || len(service.providers.cfg.Providers.ChatGPT.ExtendedContextModels) != 0 {
		t.Fatal("failed persistence changed live context settings")
	}
}

func TestSubscriptionModelProjectionPreservesFastCapability(t *testing.T) {
	models := []catalogsvc.Model{
		{ID: "priority-model", ServiceTiers: []catalogsvc.ServiceTier{{ID: "priority"}}, SupportsTools: true},
		{ID: "speed-tier-model", AdditionalSpeedTiers: []string{"fast"}},
		{ID: "standard-model", ServiceTiers: []catalogsvc.ServiceTier{{ID: "default"}}},
		{ID: "gpt-without-fast"},
	}
	projected := subscriptionModelsFromCatalog(models)
	for index, model := range projected {
		if got, want := slices.Contains(model.Capabilities, "fast"), models[index].SupportsServiceTier("priority"); got != want {
			t.Errorf("model %s: fast capability = %v, want %v", model.ID, got, want)
		}
	}
	if !slices.Contains(projected[0].Capabilities, "tools") {
		t.Fatal("Fast projection dropped existing capabilities")
	}
	for index, model := range configuredCatalogModels(projected) {
		if got, want := model.SupportsServiceTier("priority"), models[index].SupportsServiceTier("priority"); got != want {
			t.Errorf("refresh round trip changed %s fast support: got %v, want %v", model.ID, got, want)
		}
	}
}

func TestModelProviderCatalogMergesConfigAndCredentialState(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	if _, err := authentication.SetAPIKey(ctx, "openrouter", "secret-value"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Providers.LLMux["openrouter"] = config.LLMuxProviderConfig{Enabled: true, Models: []config.LLMuxModelConfig{{ID: "openai/gpt-test", Name: "GPT Test", ContextWindow: 128000}}}
	service := NewService(ctx, cfg)
	service.AttachAuth(authentication, nil)
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListModelProviders}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventModelProviders {
		t.Fatalf("event kind = %q", event.Kind)
	}
	found := map[string]bool{"subscription": false, "opencode": false, "openrouter": false, "cursor": false, "devin": false}
	for _, provider := range event.ModelProviders {
		if provider.Models == nil {
			t.Fatalf("provider %q emitted nil models", provider.ID)
		}
		if provider.ID == "chatgpt" {
			found["subscription"] = provider.Subscription && provider.Backend == "subscription" && provider.ModelsDevID == "openai"
		}
		if provider.ID == "opencode-zen" {
			found["opencode"] = provider.DefaultBaseURL == "https://opencode.ai/zen/v1" && provider.BaseURL == provider.DefaultBaseURL
		}
		if provider.ID == "cursor" {
			found["cursor"] = provider.Subscription && provider.Backend == "subscription" && provider.ModelsDevID == "cursor" && provider.EnvKey == ""
		}
		if provider.ID == "devin" {
			found["devin"] = provider.Subscription && provider.Backend == "subscription" && provider.ModelsDevID == "devin" && provider.EnvKey == ""
		}
		if provider.ID != "openrouter" {
			continue
		}
		found["openrouter"] = true
		if !provider.Enabled || !provider.CredentialConfigured || provider.CredentialSource != "stored" || provider.Models[0].ID != "openai/gpt-test" {
			t.Fatalf("provider = %+v", provider)
		}
		clone := event.Clone()
		for i := range clone.ModelProviders {
			if clone.ModelProviders[i].ID == "openrouter" {
				clone.ModelProviders[i].Models[0].ID = "changed"
			}
		}
		if provider.Models[0].ID != "openai/gpt-test" {
			t.Fatal("event clone mutated source provider models")
		}
	}
	for name, ok := range found {
		if !ok {
			t.Fatalf("provider %s was not emitted correctly", name)
		}
	}
}

func TestDiscoverModelProviderPreviewDoesNotPersist(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	modelCatalog := catalogsvc.NewService(store.DB(), nil)
	cfg := config.Default()
	cfg.Providers.LLMux["openrouter"] = config.LLMuxProviderConfig{
		Models: []config.LLMuxModelConfig{{ID: "existing/model", Disabled: true}},
	}
	service := NewService(ctx, cfg)
	service.AttachAuth(authentication, modelCatalog)
	storedBefore, err := modelCatalog.ConfiguredModels(ctx, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if len(storedBefore) != 1 || storedBefore[0].ID != "existing/model" || !storedBefore[0].Disabled {
		t.Fatalf("initial model catalog = %+v", storedBefore)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configBytes := []byte("version: 1\n")
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	service.configPath = configPath

	entry := &ModelProviderEntry{
		ID: "openrouter", DisplayName: "OpenRouter", Backend: "openai_compat",
		BaseURL: "https://openrouter.ai/api/v1", Enabled: true,
	}
	err = service.discoverModelProviderWith(ctx, entry, "pending-secret", func(_ context.Context, request llmuxdriver.DiscoveryConfig) ([]catalogsvc.Model, string, string, error) {
		if request.Profile.ID != "openrouter" || request.APIKey != "pending-secret" {
			t.Fatalf("discovery request = %+v", request)
		}
		return []catalogsvc.Model{{ID: "preview/model", Name: "Preview", SupportsTools: true}}, "openrouter", "", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	configured := service.cfg.Providers.LLMux["openrouter"]
	if configured.Enabled || len(configured.Models) != 1 || configured.Models[0].ID != "existing/model" || !configured.Models[0].Disabled {
		t.Fatalf("discovery mutated runtime config: %+v", configured)
	}
	storedModels, err := modelCatalog.ConfiguredModels(ctx, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if len(storedModels) != 1 || storedModels[0].ID != "existing/model" || !storedModels[0].Disabled {
		t.Fatalf("discovery mutated model catalog: %+v", storedModels)
	}
	accounts, err := authentication.Accounts(ctx, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("discovery persisted credentials: %+v", accounts)
	}
	persisted, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(persisted) != string(configBytes) {
		t.Fatalf("discovery rewrote config: %q", persisted)
	}

	eventCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	providersEvent, err := service.NextEvent(eventCtx)
	if err != nil {
		t.Fatal(err)
	}
	if providersEvent.Kind != EventModelProviders || providersEvent.State != "discovered" {
		t.Fatalf("providers event = %+v", providersEvent)
	}
	var preview *ModelProviderEntry
	for index := range providersEvent.ModelProviders {
		if providersEvent.ModelProviders[index].ID == "openrouter" {
			preview = &providersEvent.ModelProviders[index]
			break
		}
	}
	if preview == nil || preview.ModelsSource != "provider_api_preview" || preview.CredentialSource != "pending" ||
		len(preview.Models) != 1 || preview.Models[0].ID != "preview/model" || !slices.Contains(preview.Models[0].Capabilities, "tools") {
		t.Fatalf("preview provider = %+v", preview)
	}
	catalogEvent, err := service.NextEvent(eventCtx)
	if err != nil {
		t.Fatal(err)
	}
	if catalogEvent.Kind != EventModelCatalog || catalogEvent.State != "discovered" || catalogEvent.Data["provider"] != "openrouter" {
		t.Fatalf("catalog event = %+v", catalogEvent)
	}
}

func TestDiscoverModelProviderFiveModelDefaultPreservesChoices(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	cfg := config.Default()
	cfg.Providers.LLMux["openrouter"] = config.LLMuxProviderConfig{Models: []config.LLMuxModelConfig{
		{ID: "model-0"}, {ID: "model-1", Disabled: true},
	}}
	service := NewService(ctx, cfg)
	service.AttachAuth(authservice.NewService(db.DB(), authservice.NewSQLiteStore(db.DB()), nil, nil), catalogsvc.NewService(db.DB(), nil))
	for _, count := range []int{5, 6} {
		entry := &ModelProviderEntry{ID: "openrouter"}
		err := service.discoverModelProviderWith(ctx, entry, "pending-secret", func(context.Context, llmuxdriver.DiscoveryConfig) ([]catalogsvc.Model, string, string, error) {
			models := make([]catalogsvc.Model, count)
			for i := range models {
				models[i].ID = fmt.Sprintf("model-%d", i)
			}
			return models, "openrouter", "", nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for i, model := range entry.Models {
			want := i == 1 || (i > 1 && count > 5)
			if model.Disabled != want {
				t.Fatalf("count %d: %s disabled = %v, want %v", count, model.ID, model.Disabled, want)
			}
		}
	}
}

func TestConfiguredModelResolvesAliasToProviderModelID(t *testing.T) {
	model, err := configuredModel("openrouter", []config.LLMuxModelConfig{{ID: "openai/gpt-5.6-sol", Aliases: []string{"gpt-latest"}, Name: "GPT-5.6 Sol"}}, "gpt-latest")
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "openai/gpt-5.6-sol" || model.Name != "GPT-5.6 Sol" {
		t.Fatalf("model=%+v", model)
	}
}

func TestConfiguredModelRejectsDisabledModel(t *testing.T) {
	_, err := configuredModel("openrouter", []config.LLMuxModelConfig{{ID: "openai/gpt-test", Disabled: true}}, "openai/gpt-test")
	if err == nil {
		t.Fatal("disabled model was accepted")
	}
}

func TestSetModelEnabledUpdatesConfiguredProvider(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Providers.LLMux["openrouter"] = config.LLMuxProviderConfig{Enabled: true, Models: []config.LLMuxModelConfig{{ID: "openai/gpt-test"}}}
	service := NewService(ctx, cfg)
	service.AttachAuth(authservice.NewService(store.DB(), credentials, nil, nil), nil)
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelEnabled, Target: "openrouter", Name: "openai/gpt-test", Decision: "false"}); err != nil {
		t.Fatal(err)
	}
	if !service.cfg.Providers.LLMux["openrouter"].Models[0].Disabled {
		t.Fatal("disabled state was not applied to the configured provider")
	}
}

func TestSetModelEnabledFindsDiscoveredOpenRouterModel(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Providers.LLMux["openrouter"] = config.LLMuxProviderConfig{Enabled: true}
	service := NewService(ctx, cfg)
	service.AttachAuth(authservice.NewService(store.DB(), credentials, nil, nil), nil)
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelEnabled, Target: "openrouter", Name: "stealth/ox-alpha", Decision: "true"}); err == nil {
		t.Fatal("unconfigured model was enabled")
	}
	if err := service.updateModelProvider(ctx, &ModelProviderEntry{
		ID: "openrouter", Enabled: true, DisplayName: "OpenRouter",
		Models: []config.LLMuxModelConfig{{ID: "stealth/ox-alpha", Name: "Ox Alpha", Disabled: true, ContextWindow: 128000}},
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelEnabled, Target: "openrouter", Name: "stealth/ox-alpha", Decision: "true"}); err != nil {
		t.Fatal(err)
	}
	if service.cfg.Providers.LLMux["openrouter"].Models[0].Disabled {
		t.Fatal("discovered model stayed disabled")
	}
}

func TestLLMuxModelsPersistInSQLiteNotYAML(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\nproviders:\n  llmux:\n    openrouter:\n      enabled: true\n      models:\n        - id: stealth/ox-alpha\n          name: Ox Alpha\n          disabled: true\n          context_window: 128000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, cfg)
	service.SetConfigPath(configPath)
	models := catalogsvc.NewService(store.DB(), nil)
	service.AttachAuth(authservice.NewService(store.DB(), credentials, nil, nil), models)
	loaded, err := config.Load(configPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Providers.LLMux["openrouter"]; !got.Enabled || len(got.Models) != 0 {
		t.Fatalf("yaml provider = %#v", got)
	}
	stored, err := models.ConfiguredModels(ctx, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ID != "stealth/ox-alpha" || !stored[0].Disabled {
		t.Fatalf("sqlite models = %#v", stored)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelEnabled, Target: "openrouter", Name: "stealth/ox-alpha", Decision: "true"}); err != nil {
		t.Fatal(err)
	}
	stored, err = models.ConfiguredModels(ctx, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if stored[0].Disabled {
		t.Fatal("sqlite model stayed disabled")
	}
	reloaded, err := config.Load(configPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Providers.LLMux["openrouter"].Models) != 0 {
		t.Fatalf("enable rewrote models into yaml: %#v", reloaded.Providers.LLMux["openrouter"].Models)
	}
}

func TestAttachProviderRuntimeReceivesHydratedLLMuxModels(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	models := catalogsvc.NewService(store.DB(), nil)
	if err := models.ReplaceConfiguredModels(ctx, "openrouter", []config.LLMuxModelConfig{{
		ID: "stealth/ox-alpha", Name: "Ox Alpha", ContextWindow: 128000,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Providers.LLMux["openrouter"] = config.LLMuxProviderConfig{Enabled: true}
	service := NewService(ctx, cfg)
	service.AttachAuth(nil, models)
	runtime := &ProviderRuntime{cfg: cfg}
	service.AttachProviderRuntime(runtime)
	if _, err := runtime.resolvedLLMuxReasoningEffort("openrouter", "stealth/ox-alpha", ""); err != nil {
		t.Fatalf("hydrated model was unavailable to runtime: %v", err)
	}
}

func TestSetCursorModelFamilyEnabledUpdatesEveryVariantAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authservice.NewService(store.DB(), credentials, nil, nil), nil)
	payload, err := json.Marshal(map[string]any{"modelIds": []string{
		"claude-4-sonnet-thinking", "claude-4-sonnet",
	}})
	if err != nil {
		t.Fatal(err)
	}
	action := Action{Kind: ActionSetModelEnabled, Target: "cursor", Decision: "false", Payload: payload}
	if err := service.ExecuteAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(service.cfg.Providers.Cursor.DisabledModels, ","); got != "claude-4-sonnet,claude-4-sonnet-thinking" {
		t.Fatalf("disabled Cursor family = %q", got)
	}
	action.Decision = "true"
	if err := service.ExecuteAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	if len(service.cfg.Providers.Cursor.DisabledModels) != 0 {
		t.Fatalf("enabled Cursor family retained disabled variants: %v", service.cfg.Providers.Cursor.DisabledModels)
	}
}

func TestListModelProvidersAttachesCachedGrokModels(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	now := time.Now().UTC().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"grok-acct", "grok", "user@example.com", "user@example.com", "SuperGrok", "file:grok:grok-acct", "active", now-2, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"grok-empty", "grok", "user@example.com", "user@example.com", "SuperGrok", "file:grok:grok-empty", "active", now-1, now-1); err != nil {
		t.Fatal(err)
	}
	modelCatalog := catalogsvc.NewService(store.DB(), authentication)
	payload := `{"id":"grok-4.6","name":"Grok 4.6","contextWindow":500000,"supportsTools":true}`
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO model_catalog(provider_id,account_id,model_id,etag,fetched_at,expires_at,data) VALUES(?,?,?,?,?,?,?)`,
		"grok", "grok-acct", "grok-4.6", "", now, now+int64(time.Hour), []byte(payload)); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authentication, modelCatalog)
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListModelProviders}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventModelProviders {
		t.Fatalf("event kind = %q", event.Kind)
	}
	var grok ModelProviderEntry
	for _, provider := range event.ModelProviders {
		if provider.ID == "grok" {
			grok = provider
			break
		}
	}
	if grok.AccountID != "grok-acct" || len(grok.Models) != 1 {
		t.Fatalf("grok provider = %+v", grok)
	}
	var grok46 config.LLMuxModelConfig
	for _, model := range grok.Models {
		if model.ID == "grok-4.6" {
			grok46 = model
			break
		}
	}
	if strings.Join(grok46.ReasoningLevels, ",") != "low,medium,high,xhigh" || grok46.DefaultReasoning != "high" {
		t.Fatalf("cold-start Grok reasoning = %+v", grok46)
	}
}

func TestDiscoverSubscriptionModelsRefreshesProviderCatalog(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	now := time.Now().UTC().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"chatgpt-acct", "chatgpt", "user@example.com", "user@example.com", "pro", "file:chatgpt:chatgpt-acct", "active", now, now); err != nil {
		t.Fatal(err)
	}
	modelCatalog := catalogsvc.NewService(store.DB(), authentication)
	oldPayload := `{"id":"gpt-old","name":"GPT Old","supportsTools":true}`
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO model_catalog(provider_id,account_id,model_id,etag,fetched_at,expires_at,data) VALUES(?,?,?,?,?,?,?)`,
		"chatgpt", "chatgpt-acct", "gpt-old", `"stale-etag"`, now, now+int64(time.Hour), []byte(oldPayload)); err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	modelCatalog.Fetchers["chatgpt"] = func(context.Context, string) ([]catalogsvc.Model, error) {
		fetches.Add(1)
		return []catalogsvc.Model{{ID: "gpt-new", Name: "GPT New", SupportsTools: true}}, nil
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authentication, modelCatalog)
	if err := service.ExecuteAction(ctx, Action{Kind: ActionDiscoverProviderModels, Target: "chatgpt"}); err != nil {
		t.Fatal(err)
	}
	if fetches.Load() != 1 {
		t.Fatalf("forced catalog fetch count = %d", fetches.Load())
	}
	catalogEvent, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if catalogEvent.Kind != EventModelCatalog || catalogEvent.Data["provider"] != "chatgpt" || catalogEvent.Data["accountID"] != "chatgpt-acct" {
		t.Fatalf("catalog event = %+v", catalogEvent)
	}
	if !strings.Contains(catalogEvent.Data["models"], `"id":"gpt-new"`) || strings.Contains(catalogEvent.Data["models"], "gpt-old") {
		t.Fatalf("catalog models = %s", catalogEvent.Data["models"])
	}
	providersEvent, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if providersEvent.Kind != EventModelProviders || providersEvent.State != "catalog_updated" {
		t.Fatalf("providers event = %+v", providersEvent)
	}
	var chatgpt ModelProviderEntry
	for _, provider := range providersEvent.ModelProviders {
		if provider.ID == "chatgpt" {
			chatgpt = provider
			break
		}
	}
	if chatgpt.AccountID != "chatgpt-acct" || len(chatgpt.Models) != 1 || chatgpt.Models[0].ID != "gpt-new" || chatgpt.Models[0].Name != "GPT New" {
		t.Fatalf("chatgpt provider = %+v", chatgpt)
	}
}

func TestDiscoverSubscriptionModelsFailsWhenForcedFetchFails(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	now := time.Now().UTC().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"chatgpt-acct", "chatgpt", "user@example.com", "user@example.com", "pro", "file:chatgpt:chatgpt-acct", "active", now, now); err != nil {
		t.Fatal(err)
	}
	modelCatalog := catalogsvc.NewService(store.DB(), authentication)
	oldPayload := `{"id":"gpt-old","name":"GPT Old","supportsTools":true}`
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO model_catalog(provider_id,account_id,model_id,etag,fetched_at,expires_at,data) VALUES(?,?,?,?,?,?,?)`,
		"chatgpt", "chatgpt-acct", "gpt-old", `"stale-etag"`, now, now+int64(time.Hour), []byte(oldPayload)); err != nil {
		t.Fatal(err)
	}
	modelCatalog.Fetchers["chatgpt"] = func(context.Context, string) ([]catalogsvc.Model, error) {
		return nil, fmt.Errorf("chatgpt catalog returned HTTP 500")
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authentication, modelCatalog)
	err = service.ExecuteAction(ctx, Action{Kind: ActionDiscoverProviderModels, Target: "chatgpt"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("forced fetch error = %v", err)
	}
}

func TestListModelProvidersDoesNotBlockOnSubscriptionQuota(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	// Seed an active ChatGPT account so a blocked hot path would attempt a live quota fetch.
	now := time.Now().UTC().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		"acct-list-providers", "chatgpt", "file:chatgpt:acct-list-providers", "active", now, now); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authentication, nil)
	started := time.Now()
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListModelProviders}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("list_model_providers blocked for %s; subscription quota must stay off the hot path", elapsed)
	}
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventModelProviders {
		t.Fatalf("event kind = %q", event.Kind)
	}
	var chatgpt ModelProviderEntry
	for _, provider := range event.ModelProviders {
		if provider.ID == "chatgpt" {
			chatgpt = provider
			break
		}
	}
	if chatgpt.ID == "" || !chatgpt.Subscription || !chatgpt.Enabled || chatgpt.AccountID != "acct-list-providers" {
		t.Fatalf("chatgpt subscription provider = %+v", chatgpt)
	}
	if chatgpt.QuotaAvailable {
		t.Fatal("initial catalog must not wait for live subscription quota")
	}
}

func TestSubscriptionQuotaTimeoutRefreshesUntilSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	now := time.Now().UTC().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		"grok-timeout", "grok", "owner@example.com", "file:grok:grok-timeout", "active", now, now); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authentication, nil)
	service.subscriptionQuotaRetryDelay = func(int) time.Duration { return time.Millisecond }
	var calls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	service.subscriptionQuotaLookup = func(context.Context, string, string) (authservice.SubscriptionQuota, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return authservice.SubscriptionQuota{}, subscriptionQuotaTimeoutError{}
		}
		return authservice.SubscriptionQuota{
			Plan: "SuperGrok", Period: "weekly", UsedPercent: 17.5, ResetsAt: 1786500000, Email: "owner@example.com",
		}, nil
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListModelProviders}); err != nil {
		t.Fatal(err)
	}
	initial, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Kind != EventModelProviders || initial.State != "listed" {
		t.Fatalf("initial event = %s/%s", initial.Kind, initial.State)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListModelProviders}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first quota refresh did not start")
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent catalog refresh started %d quota lookups", calls.Load())
	}
	close(releaseFirst)

	eventCtx, eventCancel := context.WithTimeout(ctx, 2*time.Second)
	defer eventCancel()
	sawTimeout := false
	sawRetrying := false
	for {
		event, err := service.NextEvent(eventCtx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind != EventModelProviders {
			continue
		}
		var grok ModelProviderEntry
		for _, provider := range event.ModelProviders {
			if provider.ID == "grok" {
				grok = provider
				break
			}
		}
		if event.State == "quota_updated" && grok.QuotaWarning != "" {
			sawTimeout = strings.Contains(grok.QuotaWarning, "TLS handshake timeout")
			continue
		}
		if sawTimeout && event.State == "quota_refreshing" {
			sawRetrying = grok.QuotaWarning == ""
			continue
		}
		if grok.QuotaAvailable {
			if !sawTimeout || !sawRetrying {
				t.Fatalf("quota recovered without visible timeout/retrying states: timeout=%t retrying=%t", sawTimeout, sawRetrying)
			}
			if event.State != "quota_updated" || grok.QuotaWarning != "" || grok.QuotaUsedPercent != 17.5 {
				t.Fatalf("recovered quota = %+v state=%q", grok, event.State)
			}
			break
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("quota lookup calls = %d, want one timeout and one refresh", calls.Load())
	}
	if got := defaultSubscriptionQuotaRetryDelay(100); got != subscriptionQuotaRetryMaxDelay {
		t.Fatalf("retry delay is not capped: %s", got)
	}
}

func TestQuotaRefreshReusesListedProviderSnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	now := time.Now().UTC().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		"grok-reuse", "grok", "owner@example.com", "file:grok:grok-reuse", "active", now, now); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authentication, nil)
	service.subscriptionQuotaLookup = func(context.Context, string, string) (authservice.SubscriptionQuota, error) {
		return authservice.SubscriptionQuota{Plan: "SuperGrok", Period: "weekly", UsedPercent: 4}, nil
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListModelProviders}); err != nil {
		t.Fatal(err)
	}
	listed, err := service.NextEvent(ctx)
	if err != nil || listed.Kind != EventModelProviders {
		t.Fatalf("listed = %#v, %v", listed, err)
	}
	service.authentication = nil
	target := ModelProviderEntry{ID: "grok", Subscription: true, AccountID: "grok-reuse"}
	if !service.emitSubscriptionQuotaState(ctx, target, "quota_updated") {
		t.Fatal("quota emit rebuilt provider catalogs after the initial listing")
	}
}

func TestCatalogRefreshKeepsLiveSubscriptionQuota(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	now := time.Now().UTC().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"acct-quota-keep", "chatgpt", "user@example.com", "", "pro", "file:chatgpt:acct-quota-keep", "active", now, now); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authentication, nil)
	service.rememberSubscriptionQuota("chatgpt", "acct-quota-keep", authservice.SubscriptionQuota{
		Plan: "pro", Period: "weekly", StartsAt: 100, UsedPercent: 61.5, ResetsAt: 200, Balance: "12.50", Email: "user@example.com",
		Breakdown: []authservice.SubscriptionQuotaBreakdown{{ID: "cursor", UsedPercent: 16}},
	}, nil)
	entries, err := service.modelProviderEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var before ModelProviderEntry
	for _, entry := range entries {
		if entry.ID == "chatgpt" {
			before = entry
			break
		}
	}
	if before.QuotaAvailable || before.QuotaUsedPercent != 0 {
		t.Fatalf("catalog rebuild must not fetch quota: %+v", before)
	}
	service.applySubscriptionQuotas(entries)
	var after ModelProviderEntry
	for _, entry := range entries {
		if entry.ID == "chatgpt" {
			after = entry
			break
		}
	}
	if !after.QuotaAvailable || after.QuotaPeriod != "weekly" || after.QuotaStartedAt != 100 || after.QuotaUsedPercent != 61.5 ||
		after.QuotaResetsAt != 200 || after.QuotaBalance != "12.50" || after.AccountPlan != "pro" || after.QuotaUpdatedAt == "" ||
		len(after.QuotaBreakdown) != 1 || after.QuotaBreakdown[0] != (ModelProviderQuotaBreakdown{ID: "cursor", UsedPercent: 16}) {
		t.Fatalf("catalog rebuild dropped live quota: %+v", after)
	}
	service.rememberSubscriptionQuota("chatgpt", "acct-other", authservice.SubscriptionQuota{UsedPercent: 10}, nil)
	fresh, err := service.modelProviderEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service.applySubscriptionQuotas(fresh)
	for _, entry := range fresh {
		if entry.ID == "chatgpt" && (entry.QuotaAvailable || entry.QuotaUsedPercent != 0) {
			t.Fatalf("quota from another account leaked: %+v", entry)
		}
	}
	service.forgetSubscriptionQuota("chatgpt", "acct-other")
	if _, ok := service.subscriptionQuotas["chatgpt"]; ok {
		t.Fatal("logout must drop the remembered quota")
	}
}

func TestListModelProvidersHydratesGrokAnonymousLabelFromJWT(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authservice.NewService(store.DB(), credentials, nil, nil)
	authentication.GrokUserURL = "http://127.0.0.1:1/user"
	authentication.GrokQuotaURL = "http://127.0.0.1:1/billing"
	idToken := providerTestJWT(map[string]any{"sub": "jwt-user", "email": "owner@example.com"})
	if _, err := credentials.Put(ctx, authservice.Credential{Provider: "grok", AccountID: "anonymous-be73a171915548ed", AccessToken: "access", IDToken: idToken}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"anonymous-be73a171915548ed", "grok", "", "", "", "file:grok:anonymous-be73a171915548ed", "active", now, now); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachAuth(authentication, nil)
	started := time.Now()
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListModelProviders}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("list_model_providers blocked for %s while hydrating Grok identity", elapsed)
	}
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var grokProvider ModelProviderEntry
	for _, provider := range event.ModelProviders {
		if provider.ID == "grok" {
			grokProvider = provider
			break
		}
	}
	if grokProvider.AccountID != "anonymous-be73a171915548ed" || grokProvider.AccountLabel != "owner@example.com" {
		t.Fatalf("grok provider = %+v", grokProvider)
	}
}

func providerTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

type subscriptionQuotaTimeoutError struct{}

func (subscriptionQuotaTimeoutError) Error() string   { return "net/http: TLS handshake timeout" }
func (subscriptionQuotaTimeoutError) Timeout() bool   { return true }
func (subscriptionQuotaTimeoutError) Temporary() bool { return true }

func TestModelAvailabilityDoesNotRefreshSubscriptionCatalogOrQuota(t *testing.T) {
	for _, providerID := range config.SubscriptionProviderIDs() {
		t.Run(providerID, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store, err := sqlitestore.Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close(ctx)
			credentials, err := authservice.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
			if err != nil {
				t.Fatal(err)
			}
			authentication := authservice.NewService(store.DB(), credentials, nil, nil)
			now := time.Now().UnixNano()
			if _, err := store.DB().ExecContext(ctx, `INSERT INTO accounts(id,provider_id,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "toggle-account", providerID, "file:chatgpt:toggle-account", "active", now, now); err != nil {
				t.Fatal(err)
			}
			models := catalogsvc.NewService(store.DB(), authentication)
			if _, err := store.DB().ExecContext(ctx, `INSERT INTO model_catalog(provider_id,account_id,model_id,etag,fetched_at,expires_at,data) VALUES(?,?,?,?,?,?,?)`, providerID, "toggle-account", "gpt-test", "", now, now+int64(time.Hour), []byte(`{"id":"gpt-test","name":"Test","supportsTools":true,"supportsParallel":true,"serviceTiers":[{"id":"priority"}]}`)); err != nil {
				t.Fatal(err)
			}
			refreshes := make(chan string, 16)
			models.Fetchers[providerID] = func(context.Context, string) ([]catalogsvc.Model, error) {
				refreshes <- "catalog"
				return []catalogsvc.Model{{ID: "gpt-test"}}, nil
			}
			service := NewService(ctx, config.Default())
			service.AttachAuth(authentication, models)
			service.subscriptionQuotaLookup = func(context.Context, string, string) (authservice.SubscriptionQuota, error) {
				refreshes <- "quota"
				return authservice.SubscriptionQuota{}, nil
			}
			for _, enabled := range []bool{false, true} {
				if err := service.ExecuteAction(ctx, Action{Kind: ActionSetModelEnabled, Target: providerID, Name: "gpt-test", Decision: fmt.Sprint(enabled)}); err != nil {
					t.Fatal(err)
				}
				select {
				case kind := <-refreshes:
					t.Fatalf("model toggle unexpectedly refreshed %s", kind)
				case <-time.After(100 * time.Millisecond):
				}
				event, err := service.NextEvent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind != EventAuthState {
					t.Fatalf("auth event = %s", event.Kind)
				}
				event, err = service.NextEvent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind != EventModelCatalog || event.Data["provider"] != providerID {
					t.Fatalf("routing catalog event = %s", event.Kind)
				}
				var projected []catalogsvc.Model
				if err := json.Unmarshal([]byte(event.Data["models"]), &projected); err != nil {
					t.Fatal(err)
				}
				if len(projected) != 1 || projected[0].Disabled == enabled {
					t.Fatal("routing availability was not updated")
				}
				if !projected[0].SupportsParallel || !projected[0].SupportsServiceTier("priority") {
					t.Fatal("toggle dropped subscription capabilities")
				}
				event, err = service.NextEvent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind != EventModelProviders || event.State != "model_availability_updated" {
					t.Fatalf("availability event = %s/%s", event.Kind, event.State)
				}
				found := false
				for _, provider := range event.ModelProviders {
					if provider.ID == providerID && len(provider.Models) == 1 {
						found = true
						if provider.Models[0].Disabled == enabled {
							t.Fatal("provider availability was not updated")
						}
					}
				}
				if !found {
					t.Fatal("updated model missing")
				}
				quiet, stop := context.WithTimeout(ctx, 100*time.Millisecond)
				_, err = service.NextEvent(quiet)
				stop()
				if err == nil {
					t.Fatal("toggle emitted an extra catalog replacement")
				}
			}
		})
	}
}
