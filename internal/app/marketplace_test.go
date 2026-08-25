package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/plugins"
)

func TestMarketplaceActionsInstallReloadAndProjectCatalog(t *testing.T) {
	ctx := context.Background()
	dataDir, workspace, source := t.TempDir(), t.TempDir(), t.TempDir()
	writeAppMarketplaceFixture(t, source)
	cfg := config.Default()
	cfg.Plugins.ImportCodex = false
	cfg.Workspace.Root = workspace
	service := NewService(ctx, cfg)
	service.AttachPluginRuntime(plugins.Options{DataDir: dataDir, WorkspaceDir: workspace}, plugins.Integration{})
	manager, err := plugins.NewMarketplaceManager(plugins.MarketplaceManagerOptions{DataDir: dataDir, WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	service.marketplace = manager
	if err := service.ExecuteAction(ctx, Action{Kind: ActionMarketplaceAdd, Target: source}); err != nil {
		t.Fatal(err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionMarketplaceInstall, Target: "demo@action-market", Decision: "project"}); err != nil {
		t.Fatal(err)
	}
	if len(service.pluginCatalog) != 1 || service.pluginCatalog[0].Origin != "marketplace" || service.pluginCatalog[0].Scope != "project" ||
		!service.pluginCatalog[0].Enabled || service.pluginCatalog[0].ToolCount != 1 || service.pluginCatalog[0].CommandCount != 1 {
		t.Fatalf("plugin catalog = %#v", service.pluginCatalog)
	}
	if expanded, ok := service.commandCatalog.Expand("/hello world"); !ok || expanded != "Hello world" {
		t.Fatalf("marketplace command expansion = %q, %v", expanded, ok)
	}
	installed, err := manager.Installed()
	if err != nil || len(installed) != 1 || installed[0].Scope != plugins.MarketplaceScopeProject {
		t.Fatalf("installed = %#v, %v", installed, err)
	}
	snapshot, err := service.MarketplaceCatalogSnapshot()
	if err != nil || len(snapshot.Marketplaces) != 1 || len(snapshot.Available) != 1 || len(snapshot.Installed) != 1 ||
		snapshot.Available[0].ID != "demo@action-market" || snapshot.Installed[0].Scope != plugins.MarketplaceScopeProject {
		t.Fatalf("marketplace snapshot = %#v, %v", snapshot, err)
	}
	cloned := (Event{Kind: EventMarketplaceCatalog, MarketplaceCatalog: snapshot}).Clone()
	snapshot.Available[0].Keywords = append(snapshot.Available[0].Keywords, "mutated")
	if cloned.MarketplaceCatalog == nil || len(cloned.MarketplaceCatalog.Available[0].Keywords) != 0 {
		t.Fatalf("marketplace event clone aliases source: %#v", cloned.MarketplaceCatalog)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionMarketplaceDisable, Target: "demo@action-market", Decision: "project"}); err != nil {
		t.Fatal(err)
	}
	if len(service.pluginCatalog) != 1 || service.pluginCatalog[0].Enabled {
		t.Fatalf("disabled plugin catalog = %#v", service.pluginCatalog)
	}
	if _, ok := service.commandCatalog.Expand("/hello world"); ok {
		t.Fatal("disabled marketplace command remained active")
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionMarketplaceUninstall, Target: "demo@action-market", Decision: "project"}); err != nil {
		t.Fatal(err)
	}
	if len(service.pluginCatalog) != 0 {
		t.Fatalf("plugin survived uninstall: %#v", service.pluginCatalog)
	}
}

func writeAppMarketplaceFixture(t *testing.T, root string) {
	t.Helper()
	pluginRoot := filepath.Join(root, "demo")
	for _, directory := range []string{filepath.Join(root, ".omp-plugin"), filepath.Join(pluginRoot, ".claude-plugin"), filepath.Join(pluginRoot, "skills", "demo"), filepath.Join(pluginRoot, "tools"), filepath.Join(pluginRoot, "commands")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	catalog, _ := json.Marshal(map[string]any{
		"name": "action-market", "owner": map[string]any{"name": "Owner"},
		"plugins": []map[string]any{{"name": "demo", "version": "1.0.0", "source": "./demo"}},
	})
	manifest, _ := json.Marshal(map[string]any{"name": "demo", "version": "1.0.0", "description": "Demo", "skills": "./skills"})
	if err := os.WriteFile(filepath.Join(root, ".omp-plugin", "marketplace.json"), catalog, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "tools", "market.ts"), []byte(`export default () => ({name:"market",parameters:{type:"object"},execute(){return "ok"}})`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "commands", "hello.md"), []byte("Hello $ARGUMENTS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "skills", "demo", "SKILL.md"), []byte("---\nname: demo\ndescription: Demo\n---\nDemo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
