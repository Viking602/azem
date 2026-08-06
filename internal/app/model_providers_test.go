package app

import (
	"context"
	"path/filepath"
	"testing"

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
	for _, provider := range event.ModelProviders {
		if provider.Models == nil {
			t.Fatalf("provider %q emitted nil models", provider.ID)
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
		return
	}
	t.Fatal("openrouter profile was not emitted")
}
