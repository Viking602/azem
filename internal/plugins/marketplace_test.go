package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMarketplaceLocalInstallScopeShadowEnableUpgradeAndUninstall(t *testing.T) {
	ctx := context.Background()
	dataDir, workspace, source := t.TempDir(), t.TempDir(), t.TempDir()
	writeMarketplaceFixture(t, source, "1.0.0", "USER_V1")
	manager, err := NewMarketplaceManager(MarketplaceManagerOptions{DataDir: dataDir, WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Add(ctx, source)
	if err != nil || record.Name != "demo-market" || record.Type != "local" {
		t.Fatalf("add = %#v, %v", record, err)
	}
	available, err := manager.Discover("")
	if err != nil || len(available) != 1 || available[0].ID != "demo@demo-market" {
		t.Fatalf("discover = %#v, %v", available, err)
	}
	user, err := manager.Install(ctx, "demo@demo-market", MarketplaceScopeUser, false)
	if err != nil || user.Scope != MarketplaceScopeUser || user.Version != "1.0.0" {
		t.Fatalf("user install = %#v, %v", user, err)
	}
	writeMarketplaceFixture(t, source, "2.0.0", "PROJECT_V2")
	project, err := manager.Install(ctx, "demo@demo-market", MarketplaceScopeProject, false)
	if err != nil || project.Scope != MarketplaceScopeProject || project.Version != "2.0.0" {
		t.Fatalf("project install = %#v, %v", project, err)
	}
	integration := Discover(ctx, Options{DataDir: dataDir, WorkspaceDir: workspace})
	if len(integration.SkillDirs) != 1 || !strings.Contains(integration.SkillDirs[0], filepath.Join(".azem", "plugin-packages")) || len(integration.Entries) != 2 {
		t.Fatalf("project shadow integration = %#v", integration)
	}
	if err := manager.SetEnabled("demo@demo-market", MarketplaceScopeProject, false); err != nil {
		t.Fatal(err)
	}
	integration = Discover(ctx, Options{DataDir: dataDir, WorkspaceDir: workspace})
	if len(integration.SkillDirs) != 1 || strings.Contains(integration.SkillDirs[0], filepath.Join(".azem", "plugin-packages")) {
		t.Fatalf("disabled project did not reveal user plugin: %#v", integration.SkillDirs)
	}
	writeMarketplaceFixture(t, source, "3.0.0", "UPGRADED_V3")
	upgrades, err := manager.AvailableUpgrades()
	if err != nil || len(upgrades) != 2 {
		t.Fatalf("upgrades = %#v, %v", upgrades, err)
	}
	upgraded, err := manager.Upgrade(ctx, "demo@demo-market", "")
	if err != nil || len(upgraded) != 2 || upgraded[0].Version != "3.0.0" || upgraded[1].Version != "3.0.0" {
		t.Fatalf("upgrade = %#v, %v", upgraded, err)
	}
	if err := manager.Uninstall("demo@demo-market", MarketplaceScopeProject); err != nil {
		t.Fatal(err)
	}
	installed, err := manager.Installed()
	if err != nil || len(installed) != 1 || installed[0].Scope != MarketplaceScopeUser {
		t.Fatalf("installed after uninstall = %#v, %v", installed, err)
	}
	if err := manager.Remove("demo-market"); err != nil {
		t.Fatal(err)
	}
	if records, err := manager.List(); err != nil || len(records) != 0 {
		t.Fatalf("marketplaces after remove = %#v, %v", records, err)
	}
}

func TestMarketplaceDirectUpdateFailureKeepsLastValidCatalog(t *testing.T) {
	var invalid atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		owner := map[string]any{"name": "Owner"}
		if invalid.Load() {
			owner = map[string]any{}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"name": "direct-market", "owner": owner,
			"plugins": []map[string]any{{"name": "remote", "version": "1.0.0", "source": map[string]any{"source": "github", "repo": "example/remote"}}},
		})
	}))
	defer server.Close()
	manager, err := NewMarketplaceManager(MarketplaceManagerOptions{DataDir: t.TempDir(), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), server.URL+"/marketplace.json"); err != nil {
		t.Fatal(err)
	}
	invalid.Store(true)
	if _, err := manager.Update(context.Background(), "direct-market"); err == nil {
		t.Fatal("invalid marketplace update succeeded")
	}
	available, err := manager.Discover("direct-market")
	if err != nil || len(available) != 1 || available[0].Version != "1.0.0" {
		t.Fatalf("cached catalog after failed update = %#v, %v", available, err)
	}
}

func TestMarketplaceRejectsEscapingRelativePluginSource(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, "market")
	if err := os.MkdirAll(filepath.Join(catalogDir, ".omp-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := map[string]any{
		"name": "escape-market", "owner": map[string]any{"name": "Owner"},
		"plugins": []map[string]any{{"name": "escape", "source": "./../outside"}},
	}
	encoded, _ := json.Marshal(catalog)
	if err := os.WriteFile(filepath.Join(catalogDir, ".omp-plugin", "marketplace.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "outside"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager, _ := NewMarketplaceManager(MarketplaceManagerOptions{DataDir: t.TempDir()})
	if _, err := manager.Add(context.Background(), catalogDir); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), "escape@escape-market", MarketplaceScopeUser, false); err == nil {
		t.Fatal("escaping marketplace plugin source installed")
	}
}

func TestMarketplaceGitSubdirSourceVerifiesPinnedSHA(t *testing.T) {
	repository := t.TempDir()
	pluginRoot := filepath.Join(repository, "plugins", "pinned")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"name": "pinned", "version": "1.0.0", "description": "Pinned"})
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Azem Test", "-c", "user.email=azem@example.test", "commit", "-qm", "fixture"}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git fixture unavailable: %v: %s", err, output)
		}
	}
	shaOutput, err := exec.Command("git", "-C", repository, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	source, _ := json.Marshal(map[string]any{"source": "git-subdir", "url": repository, "path": "plugins/pinned", "sha": strings.TrimSpace(string(shaOutput))})
	manager, _ := NewMarketplaceManager(MarketplaceManagerOptions{DataDir: t.TempDir()})
	root, cleanup, err := manager.resolvePluginSource(context.Background(), MarketplaceRecord{}, MarketplaceCatalog{}, MarketplacePlugin{Source: source})
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	badSource, _ := json.Marshal(map[string]any{"source": "git-subdir", "url": repository, "path": "plugins/pinned", "sha": strings.Repeat("0", 40)})
	_, badCleanup, badErr := manager.resolvePluginSource(context.Background(), MarketplaceRecord{}, MarketplaceCatalog{}, MarketplacePlugin{Source: badSource})
	if badCleanup != nil {
		badCleanup()
	}
	if badErr == nil {
		t.Fatal("mismatched plugin SHA succeeded")
	}
}

func TestMarketplaceVersionUpgradeHonorsSemverDirection(t *testing.T) {
	if !marketplaceVersionUpgrade("1.2.3", "1.3.0") || marketplaceVersionUpgrade("2.0.0", "1.9.0") ||
		!marketplaceVersionUpgrade("build-a", "build-b") || marketplaceVersionUpgrade("same", "same") {
		t.Fatal("marketplace version upgrade comparison is incorrect")
	}
}

func writeMarketplaceFixture(t *testing.T, root, version, body string) {
	t.Helper()
	pluginRoot := filepath.Join(root, "plugins", "demo")
	for _, directory := range []string{filepath.Join(root, ".omp-plugin"), filepath.Join(pluginRoot, ".claude-plugin"), filepath.Join(pluginRoot, "skills", "demo-skill")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	catalog := map[string]any{
		"name": "demo-market", "owner": map[string]any{"name": "Owner"}, "metadata": map[string]any{"pluginRoot": "plugins"},
		"plugins": []map[string]any{{"name": "demo", "version": version, "description": "Demo plugin", "source": "./demo"}},
	}
	manifest := map[string]any{"name": "demo", "version": version, "description": "Demo plugin", "skills": "./skills"}
	catalogJSON, _ := json.Marshal(catalog)
	manifestJSON, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, ".omp-plugin", "marketplace.json"), catalogJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), manifestJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: demo-skill\ndescription: Marketplace skill\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(pluginRoot, "skills", "demo-skill", "SKILL.md"), []byte(skill), 0o600); err != nil {
		t.Fatal(err)
	}
}
