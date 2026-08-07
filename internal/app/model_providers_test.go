package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

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
	foundSubscription, foundOpenCode := false, false
	for _, provider := range event.ModelProviders {
		if provider.Models == nil {
			t.Fatalf("provider %q emitted nil models", provider.ID)
		}
		if provider.ID == "chatgpt" {
			foundSubscription = provider.Subscription && provider.Backend == "subscription" && provider.ModelsDevID == "openai"
		}
		if provider.ID == "opencode" {
			foundOpenCode = provider.DefaultBaseURL == "https://opencode.ai/zen/v1" && provider.BaseURL == provider.DefaultBaseURL
		}
		if provider.ID != "openrouter" {
			continue
		}
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
		if !foundSubscription || !foundOpenCode {
			t.Fatalf("subscription=%v opencode=%v", foundSubscription, foundOpenCode)
		}
		return
	}
	t.Fatal("openrouter profile was not emitted")
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
