package catalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Viking602/azem/internal/config"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestConfiguredModelsRoundTripAndDisable(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "llmux-models.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), nil)
	models := []config.LLMuxModelConfig{
		{ID: "stealth/ox-alpha", Name: "Ox Alpha", Disabled: true, ContextWindow: 128000, Capabilities: []string{"tools"}},
		{ID: "openai/gpt-test", Name: "GPT Test", ContextWindow: 200000},
	}
	if err := service.ReplaceConfiguredModels(ctx, "openrouter", models); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.ConfiguredModels(ctx, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0].ID != "openai/gpt-test" || loaded[1].ID != "stealth/ox-alpha" || !loaded[1].Disabled {
		t.Fatalf("loaded = %#v", loaded)
	}
	if err := service.SetConfiguredModelDisabled(ctx, "openrouter", "stealth/ox-alpha", false); err != nil {
		t.Fatal(err)
	}
	loaded, err = service.ConfiguredModels(ctx, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if loaded[1].Disabled {
		t.Fatal("enabled model stayed disabled")
	}
	if err := service.SetConfiguredModelDisabled(ctx, "openrouter", "missing", false); err == nil {
		t.Fatal("missing model was enabled")
	}
}
