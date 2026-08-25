package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
)

func TestDiscoverImportsEnabledPluginCapabilities(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "azem-data")
	sourceRoot := filepath.Join(home, ".codex", "plugins", "cache", "market", "demo", "1.2.3")
	mustWrite(t, filepath.Join(sourceRoot, ".codex-plugin", "plugin.json"), `{
  "name":"demo","version":"1.2.3","description":"Demo plugin",
  "skills":"./skills/","mcpServers":"./.mcp.json","hooks":"./hooks.json","apps":"./.app.json","tools":"./tools","commands":"./commands",
  "interface":{"displayName":"Demo Plugin","developerName":"Azem","category":"Developer Tools","capabilities":["Read","Write"],"logo":"./assets/logo.svg"}
}`)
	mustWrite(t, filepath.Join(sourceRoot, "skills", "review", "SKILL.md"), "# Review\n")
	mustWrite(t, filepath.Join(sourceRoot, "assets", "logo.svg"), "<svg/>")
	mustWrite(t, filepath.Join(sourceRoot, "hooks.json"), `{"hooks":{}}`)
	mustWrite(t, filepath.Join(sourceRoot, ".app.json"), `{}`)
	mustWrite(t, filepath.Join(sourceRoot, "tools", "stats.ts"), `export default () => ({name:"stats",execute(){return "ok"}})`)
	mustWrite(t, filepath.Join(sourceRoot, "commands", "review.md"), "Review $ARGUMENTS")
	mustWrite(t, filepath.Join(sourceRoot, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviewer\n---\nReview code.\n")
	mustWrite(t, filepath.Join(sourceRoot, "themes", "demo.json"), `{"name":"demo","colors":{"accent":"#fff"}}`)
	mustWrite(t, filepath.Join(sourceRoot, "extensions", "register.ts"), `export default () => {};`)
	mustWrite(t, filepath.Join(sourceRoot, ".mcp.json"), `{"mcpServers":{
  "local":{"command":"tool","args":["serve"],"cwd":".","env":{"MODE":"plugin"}},
  "oauth":{"type":"http","url":"https://example.com/mcp"},
  "token":{"type":"http","url":"https://example.com/token","bearer_token_env_var":"DEMO_TOKEN"}
}}`)
	catalog := installedCatalog{Installed: []installedPlugin{{PluginID: "demo@market", Name: "demo", Marketplace: "market", Version: "1.2.3", Installed: true, Enabled: true}}}
	encoded, _ := json.Marshal(catalog)
	result := Discover(context.Background(), Options{HomeDir: home, DataDir: data, ImportCodex: true, CodexImports: []string{"demo@market"}, ListPlugins: func(context.Context) ([]byte, error) { return encoded, nil }})
	assertPluginEntry(t, result)
	copyRoot := filepath.Join(data, "plugin-packages", "codex", "market", "demo")
	assertLocalMCP(t, result.MCPServers["demo-local"], copyRoot)
	if result.Entries[0].Root != copyRoot || result.Entries[0].Origin != "codex" {
		t.Fatalf("plugin was not loaded from the Azem copy: %#v", result.Entries[0])
	}
	if !strings.HasPrefix(result.Entries[0].LogoPath, "data:image/svg+xml;base64,") {
		t.Fatalf("plugin icon was not projected as safe image data: %q", result.Entries[0].LogoPath)
	}
	if err := os.RemoveAll(sourceRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(copyRoot, "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("copied plugin content is unavailable after removing the Codex source: %v", err)
	}
	if result.MCPServers["demo-local"].Icon == "" || !strings.HasPrefix(result.MCPServers["demo-local"].Icon, "data:image/") {
		t.Fatalf("plugin MCP icon = %q", result.MCPServers["demo-local"].Icon)
	}
	if result.MCPServers["demo-oauth"].Enabled {
		t.Fatal("OAuth-only MCP must remain disabled until Azem has credentials")
	}
	assertTokenMCP(t, result.MCPServers["demo-token"])
}

func assertPluginEntry(t *testing.T, result Integration) {
	t.Helper()
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %#v", result.Entries)
	}
	entry := result.Entries[0]
	if entry.DisplayName != "Demo Plugin" {
		t.Fatalf("display name = %q", entry.DisplayName)
	}
	if entry.SkillCount != 1 || entry.MCPServerCount != 3 || entry.IntegratedMCPCount != 2 {
		t.Fatalf("model capability counts = %#v", entry)
	}
	if entry.HookCount != 1 || entry.HooksTrusted || !entry.HasApp || entry.ToolCount != 1 || entry.CommandCount != 1 ||
		entry.AgentCount != 1 || entry.ThemeCount != 1 || entry.ExtensionCount != 1 {
		t.Fatalf("extension capability counts = %#v", entry)
	}
	if len(result.SkillDirs) != 1 || len(result.HookSources) != 1 || len(result.ToolPaths) != 1 || len(result.CommandDirs) != 1 ||
		len(result.AgentDirs) != 1 || len(result.ThemeDirs) != 1 || len(result.ExtensionPaths) != 1 {
		t.Fatalf("integration = %#v", result)
	}
}

func assertLocalMCP(t *testing.T, server config.MCPServerConfig, root string) {
	t.Helper()
	if !server.Enabled || server.Transport != "stdio" || !server.Managed {
		t.Fatalf("local MCP state = %#v", server)
	}
	if server.CWD != root || server.RuntimeEnv["PLUGIN_ROOT"] != root {
		t.Fatalf("local MCP root = %#v", server)
	}
	if server.RuntimeEnv["MODE"] != "plugin" {
		t.Fatalf("local MCP environment = %#v", server.RuntimeEnv)
	}
}

func assertTokenMCP(t *testing.T, server config.MCPServerConfig) {
	t.Helper()
	if !server.Enabled || server.Headers["Authorization"] != "env:DEMO_TOKEN" {
		t.Fatalf("token MCP = %#v", server)
	}
}

func TestDiscoverTrustedHooksReceivePluginEnvironment(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	root := filepath.Join(data, "plugin-packages", "local", "demo")
	mustWrite(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"name":"demo","version":"1","description":"Demo","hooks":"./hooks.json"}`)
	mustWrite(t, filepath.Join(root, "hooks.json"), `{"hooks":{}}`)
	result := Discover(context.Background(), Options{HomeDir: home, DataDir: data, TrustHooks: true, ListPlugins: func(context.Context) ([]byte, error) {
		t.Fatal("Codex must not be queried when import is disabled")
		return nil, nil
	}})
	if len(result.HookSources) != 1 || result.HookSources[0].Environment["CLAUDE_PLUGIN_ROOT"] != root || !result.Entries[0].HooksTrusted {
		t.Fatalf("hook integration = %#v", result)
	}
	if result.Entries[0].Origin != "local" {
		t.Fatalf("local plugin origin = %#v", result.Entries[0])
	}
}

func TestDiscoverListsCodexPluginsWithoutImportingThem(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	sourceRoot := filepath.Join(home, "codex-plugin")
	mustWrite(t, filepath.Join(sourceRoot, ".codex-plugin", "plugin.json"), `{"name":"demo","version":"1","description":"Demo","interface":{"composerIcon":"./icon.png"}}`)
	mustWrite(t, filepath.Join(sourceRoot, "icon.png"), string([]byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}))
	catalog := installedCatalog{Installed: []installedPlugin{{PluginID: "demo@market", Name: "demo", Marketplace: "market", Version: "1", Installed: true, Enabled: true, Source: pluginSource{Path: sourceRoot}}}}
	encoded, _ := json.Marshal(catalog)
	result := Discover(context.Background(), Options{HomeDir: home, DataDir: data, ImportCodex: true, ListPlugins: func(context.Context) ([]byte, error) { return encoded, nil }})
	if len(result.Entries) != 1 || result.Entries[0].Origin != "codex_available" || result.Entries[0].Status != "available" || result.Entries[0].Imported {
		t.Fatalf("available Codex plugin = %#v", result.Entries)
	}
	if !strings.HasPrefix(result.Entries[0].LogoPath, "data:image/png;base64,") {
		t.Fatalf("available plugin icon = %q", result.Entries[0].LogoPath)
	}
	copyRoot := filepath.Join(data, "plugin-packages", "codex", "market", "demo")
	if _, err := os.Stat(copyRoot); !os.IsNotExist(err) {
		t.Fatalf("unselected Codex plugin was copied: %v", err)
	}
}

func TestDiscoverDoesNotRegisterCodexComputerUseMCP(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	sourceRoot := filepath.Join(home, "computer-use")
	mustWrite(t, filepath.Join(sourceRoot, ".codex-plugin", "plugin.json"), `{"name":"computer-use","version":"1","description":"Codex computer use","mcpServers":"./.mcp.json"}`)
	mustWrite(t, filepath.Join(sourceRoot, ".mcp.json"), `{"mcpServers":{"computer-use":{"command":"./launcher","args":["mcp"]}}}`)
	mustWrite(t, filepath.Join(sourceRoot, "launcher"), "launcher")
	catalog := installedCatalog{Installed: []installedPlugin{{PluginID: "computer-use@openai-bundled", Name: "computer-use", Marketplace: "openai-bundled", Version: "1", Installed: true, Enabled: true, Source: pluginSource{Path: sourceRoot}}}}
	encoded, _ := json.Marshal(catalog)
	result := Discover(context.Background(), Options{HomeDir: home, DataDir: data, ImportCodex: true, CodexImports: []string{"computer-use@openai-bundled"}, ListPlugins: func(context.Context) ([]byte, error) { return encoded, nil }})
	if len(result.MCPServers) != 0 || len(result.Entries) != 1 || !strings.Contains(result.Entries[0].Warning, "不兼容 Azem") {
		t.Fatalf("computer-use integration = %#v", result)
	}
}

func TestDiscoverRejectsManifestPathOutsidePlugin(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	root := filepath.Join(data, "plugin-packages", "local", "demo")
	mustWrite(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"name":"demo","version":"1","description":"Demo","skills":"../skills"}`)
	result := Discover(context.Background(), Options{HomeDir: home, DataDir: data})
	if len(result.Diagnostics) == 0 || len(result.SkillDirs) != 0 {
		t.Fatalf("expected rejected path, got %#v", result)
	}
}

func TestDiscoverImportsFromMarketplaceCheckoutWhenCodexListFails(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	sourceRoot := filepath.Join(home, ".codex", ".tmp", "marketplaces", "kami", "plugins", "kami")
	mustWrite(t, filepath.Join(sourceRoot, ".codex-plugin", "plugin.json"), `{"name":"kami","version":"1.12.0","description":"Typeset documents","skills":"./skills/"}`)
	mustWrite(t, filepath.Join(sourceRoot, "skills", "kami", "SKILL.md"), "---\nname: kami\ndescription: Typeset documents\n---\n")
	result := Discover(context.Background(), Options{
		HomeDir: home, DataDir: data, ImportCodex: true,
		CodexImports: []string{"kami@kami"},
		ListPlugins:  func(context.Context) ([]byte, error) { return nil, os.ErrNotExist },
	})
	copyRoot := filepath.Join(data, "plugin-packages", "codex", "kami", "kami")
	if _, err := os.Stat(filepath.Join(copyRoot, "skills", "kami", "SKILL.md")); err != nil {
		t.Fatalf("marketplace fallback copy: %v diagnostics=%#v", err, result.Diagnostics)
	}
	if len(result.Entries) != 1 || result.Entries[0].Origin != "codex" || result.Entries[0].ID != "kami@kami" {
		t.Fatalf("imported catalog = %#v", result.Entries)
	}
}

func TestDiscoverKeepsAzemCopiesWhenCodexIsUnavailable(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	sourceRoot := filepath.Join(home, "codex-plugin")
	mustWrite(t, filepath.Join(sourceRoot, ".codex-plugin", "plugin.json"), `{"name":"demo","version":"1","description":"Demo"}`)
	catalog := installedCatalog{Installed: []installedPlugin{{PluginID: "demo@market", Name: "demo", Marketplace: "market", Version: "1", Installed: true, Enabled: true, Source: pluginSource{Path: sourceRoot}}}}
	encoded, _ := json.Marshal(catalog)
	first := Discover(context.Background(), Options{HomeDir: home, DataDir: data, ImportCodex: true, CodexImports: []string{"demo@market"}, ListPlugins: func(context.Context) ([]byte, error) { return encoded, nil }})
	if len(first.Entries) != 1 {
		t.Fatalf("first import = %#v", first)
	}
	second := Discover(context.Background(), Options{HomeDir: home, DataDir: data, ImportCodex: true, CodexImports: []string{"demo@market"}, ListPlugins: func(context.Context) ([]byte, error) { return nil, os.ErrNotExist }})
	if len(second.Entries) != 1 || second.Entries[0].Origin != "codex" || len(second.Diagnostics) == 0 {
		t.Fatalf("Azem copy was not retained independently: %#v", second)
	}
}

func TestDiscoverLiveCodexImportUsesOnlyAzemCopies(t *testing.T) {
	if os.Getenv("AZEM_LIVE_CODEX_PLUGIN_TEST") != "1" {
		t.Skip("set AZEM_LIVE_CODEX_PLUGIN_TEST=1 to exercise the installed Codex catalog")
	}
	data := t.TempDir()
	encoded, err := listWithCodex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var catalog installedCatalog
	if err := json.Unmarshal(encoded, &catalog); err != nil {
		t.Fatal(err)
	}
	selected := make([]string, 0, len(catalog.Installed))
	for _, entry := range catalog.Installed {
		if entry.Installed {
			selected = append(selected, entry.PluginID)
		}
	}
	result := Discover(context.Background(), Options{DataDir: data, ImportCodex: true, CodexImports: selected, ListPlugins: func(context.Context) ([]byte, error) { return encoded, nil }})
	if len(result.Entries) == 0 {
		t.Fatalf("live Codex import returned no catalog entries: %#v", result.Diagnostics)
	}
	copyRoot := filepath.Join(data, "plugin-packages", "codex")
	for _, entry := range result.Entries {
		if entry.Origin != "codex" || !pathWithinRoot(copyRoot, entry.Root) {
			t.Fatalf("live plugin did not execute from an Azem copy: %#v", entry)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
