package tui

import (
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/plugins"
)

func TestMarketplaceCommandsDispatchScopedActions(t *testing.T) {
	runtime := &recordedRuntime{}
	model := NewModel(runtime, t.TempDir(), "chatgpt", "model", "low", "single")
	updated, command := model.executeCommand(Command{Name: "marketplace", Args: []string{"install", "review@official", "project"}})
	model = updated.(AppModel)
	if command == nil {
		t.Fatalf("marketplace install returned no action: %s", model.errorBanner)
	}
	_ = command()
	if len(runtime.actions) != 1 || runtime.actions[0].Kind != ActionMarketplaceInstall || runtime.actions[0].Target != "review@official" || runtime.actions[0].Decision != "project" {
		t.Fatalf("marketplace install action = %#v", runtime.actions)
	}
	updated, command = model.executeCommand(Command{Name: "marketplace", Args: []string{"uninstall", "review@official"}})
	model = updated.(AppModel)
	if command != nil || model.errorBanner == "" {
		t.Fatalf("unscoped uninstall command=%v error=%q", command != nil, model.errorBanner)
	}
}

func TestMarketplaceEventRendersPublicInventoryWithoutCachePaths(t *testing.T) {
	model := NewModel(&recordedRuntime{}, t.TempDir(), "chatgpt", "model", "low", "single")
	model.applyEvent(app.Event{Kind: app.EventMarketplaceCatalog, MarketplaceCatalog: &app.MarketplaceCatalogPayload{
		Marketplaces: []plugins.MarketplaceRecord{{Name: "official", Source: "owner/repo", Type: "github", CachePath: "/private/cache/path"}},
		Available:    []plugins.MarketplacePluginView{{ID: "review@official", Name: "review", Marketplace: "official", Version: "1.2.0"}},
	}})
	if len(model.transcript) != 1 || model.transcript[0].Title != "Marketplace" || !strings.Contains(model.transcript[0].Content, "owner/repo") || strings.Contains(model.transcript[0].Content, "/private/cache/path") {
		t.Fatalf("marketplace transcript = %#v", model.transcript)
	}
}
