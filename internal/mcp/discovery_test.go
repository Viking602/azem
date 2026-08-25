package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverConfigLoadsCrossHarnessSourcesWithProviderPrecedence(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("MCP_TOKEN", "secret-token")
	mustWriteDiscovery(t, filepath.Join(workspace, ".omp", "mcp.json"), `{"mcpServers":{"shared":{"command":"native-server","args":["--native"]}}}`)
	mustWriteDiscovery(t, filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers":{"shared":{"command":"cursor-server"},"cursor-http":{"url":"https://mcp.example.test","headers":{"Authorization":"Bearer $MCP_TOKEN"},"auth":{"type":"oauth","tokenUrl":"https://auth.example.test/token","clientId":"client","clientSecret":"$MCP_TOKEN"},"oauth":{"authorizationUrl":"https://auth.example.test/authorize","scopes":["read"]}}}}`)
	mustWriteDiscovery(t, filepath.Join(workspace, ".codex", "config.toml"), "[mcp_servers.codex]\ncommand = \"codex-server\"\nargs = [\"--serve\"]\n[mcp_servers.codex.env]\nTOKEN = \"$MCP_TOKEN\"\n")
	mustWriteDiscovery(t, filepath.Join(home, ".config", "opencode", "opencode.jsonc"), `{
		// JSONC and command arrays are accepted.
		"mcp": {"Open Code": {"type":"local", "command":["opencode-server", "--stdio"], "environment":{"TOKEN":"${MCP_TOKEN}"}}}
	}`)
	mustWriteDiscovery(t, filepath.Join(workspace, ".vscode", "mcp.json"), `{"servers":{"vscode":{"command":"vscode-server"}}}`)

	result := DiscoverConfig(context.Background(), DiscoveryOptions{Workspace: workspace, HomeDir: home, NativeAgentDir: filepath.Join(home, ".omp", "agent")})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	if len(result.Servers) != 5 {
		t.Fatalf("servers = %#v", result.Servers)
	}
	if result.Servers["shared"].Command != "native-server" || result.Servers["shared"].Args[0] != "--native" {
		t.Fatalf("provider precedence = %#v", result.Servers["shared"])
	}
	if result.Servers["cursor-http"].Transport != "streamable_http" || result.Servers["cursor-http"].RuntimeHeaders["Authorization"] != "Bearer secret-token" ||
		result.Servers["cursor-http"].Auth == nil || result.Servers["cursor-http"].Auth.Type != "oauth" ||
		result.Servers["cursor-http"].RuntimeAuthClientSecret != "secret-token" || result.Servers["cursor-http"].OAuth == nil || len(result.Servers["cursor-http"].OAuth.Scopes) != 1 {
		t.Fatalf("HTTP/OAuth discovery = %#v", result.Servers["cursor-http"])
	}
	if result.Servers["codex"].RuntimeEnv["TOKEN"] != "secret-token" || result.Servers["codex"].Command != "codex-server" {
		t.Fatalf("Codex discovery = %#v", result.Servers["codex"])
	}
	if result.Servers["open_code"].Command != "opencode-server" || result.Servers["open_code"].Args[0] != "--stdio" || result.Servers["open_code"].RuntimeEnv["TOKEN"] != "secret-token" {
		t.Fatalf("OpenCode discovery = %#v", result.Servers["open_code"])
	}
}

func TestDiscoverConfigHonorsDisabledProvider(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	mustWriteDiscovery(t, filepath.Join(workspace, ".cursor", "mcp.json"), `{"mcpServers":{"cursor":{"command":"cursor-server"}}}`)
	result := DiscoverConfig(context.Background(), DiscoveryOptions{Workspace: workspace, HomeDir: home, DisabledProviders: map[string]bool{"cursor": true}})
	if len(result.Servers) != 0 {
		t.Fatalf("disabled provider servers = %#v", result.Servers)
	}
}

func mustWriteDiscovery(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
