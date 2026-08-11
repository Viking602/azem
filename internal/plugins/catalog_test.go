package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Viking602/azem/internal/config"
)

func TestDiscoverImportsEnabledPluginCapabilities(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "azem-data")
	root := filepath.Join(home, ".codex", "plugins", "cache", "market", "demo", "1.2.3")
	mustWrite(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{
  "name":"demo","version":"1.2.3","description":"Demo plugin",
  "skills":"./skills/","mcpServers":"./.mcp.json","hooks":"./hooks.json","apps":"./.app.json",
  "interface":{"displayName":"Demo Plugin","developerName":"Azem","category":"Developer Tools","capabilities":["Read","Write"],"logo":"./assets/logo.svg"}
}`)
	mustWrite(t, filepath.Join(root, "skills", "review", "SKILL.md"), "# Review\n")
	mustWrite(t, filepath.Join(root, "assets", "logo.svg"), "<svg/>")
	mustWrite(t, filepath.Join(root, "hooks.json"), `{"hooks":{}}`)
	mustWrite(t, filepath.Join(root, ".app.json"), `{}`)
	mustWrite(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{
  "local":{"command":"tool","args":["serve"],"cwd":".","env":{"MODE":"plugin"}},
  "oauth":{"type":"http","url":"https://example.com/mcp"},
  "token":{"type":"http","url":"https://example.com/token","bearer_token_env_var":"DEMO_TOKEN"}
}}`)
	catalog := installedCatalog{Installed: []installedPlugin{{PluginID: "demo@market", Name: "demo", Marketplace: "market", Version: "1.2.3", Installed: true, Enabled: true}}}
	encoded, _ := json.Marshal(catalog)
	result := Discover(context.Background(), Options{HomeDir: home, DataDir: data, ListPlugins: func(context.Context) ([]byte, error) { return encoded, nil }})
	assertPluginEntry(t, result)
	assertLocalMCP(t, result.MCPServers["demo-local"], root)
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
	if entry.HookCount != 1 || entry.HooksTrusted || !entry.HasApp {
		t.Fatalf("extension capability counts = %#v", entry)
	}
	if len(result.SkillDirs) != 1 || len(result.HookSources) != 0 {
		t.Fatalf("integration = %#v", result)
	}
}

func assertLocalMCP(t *testing.T, server config.MCPServerConfig, root string) {
	t.Helper()
	if !server.Enabled || server.Transport != "stdio" {
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
	root := filepath.Join(home, "plugin")
	mustWrite(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"name":"demo","version":"1","description":"Demo","hooks":"./hooks.json"}`)
	mustWrite(t, filepath.Join(root, "hooks.json"), `{"hooks":{}}`)
	catalog := installedCatalog{Installed: []installedPlugin{{PluginID: "demo@local", Name: "demo", Marketplace: "local", Version: "1", Installed: true, Enabled: true, Source: pluginSource{Path: root}}}}
	encoded, _ := json.Marshal(catalog)
	result := Discover(context.Background(), Options{HomeDir: home, DataDir: filepath.Join(home, "data"), TrustHooks: true, ListPlugins: func(context.Context) ([]byte, error) { return encoded, nil }})
	if len(result.HookSources) != 1 || result.HookSources[0].Environment["CLAUDE_PLUGIN_ROOT"] != root || !result.Entries[0].HooksTrusted {
		t.Fatalf("hook integration = %#v", result)
	}
}

func TestDiscoverRejectsManifestPathOutsidePlugin(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "plugin")
	mustWrite(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"name":"demo","version":"1","description":"Demo","skills":"../skills"}`)
	catalog := installedCatalog{Installed: []installedPlugin{{PluginID: "demo@local", Name: "demo", Marketplace: "local", Version: "1", Installed: true, Enabled: true, Source: pluginSource{Path: root}}}}
	encoded, _ := json.Marshal(catalog)
	result := Discover(context.Background(), Options{HomeDir: home, ListPlugins: func(context.Context) ([]byte, error) { return encoded, nil }})
	if len(result.Diagnostics) == 0 || len(result.SkillDirs) != 0 {
		t.Fatalf("expected rejected path, got %#v", result)
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
