package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Viking602/azem/internal/config"
)

const maxDiscoveryConfigBytes = 1 << 20

var discoveredServerNamePattern = regexp.MustCompile(`[^a-z0-9_-]+`)

type DiscoveryOptions struct {
	Workspace         string
	HomeDir           string
	NativeAgentDir    string
	DisabledProviders map[string]bool
}

type DiscoveryResult struct {
	Servers     map[string]config.MCPServerConfig
	Diagnostics []Diagnostic
}

type mcpSource struct {
	provider string
	path     string
	format   string
	cwd      string
	project  bool
}

func DiscoverConfig(_ context.Context, options DiscoveryOptions) DiscoveryResult {
	workspace, _ := filepath.Abs(options.Workspace)
	home := options.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	native := options.NativeAgentDir
	if native == "" {
		native = strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	}
	if native == "" && home != "" {
		native = filepath.Join(home, ".omp", "agent")
	}
	var sources []mcpSource
	add := func(provider, path, format string, project bool) {
		if path != "" && !options.DisabledProviders[provider] {
			sources = append(sources, mcpSource{provider: provider, path: path, format: format, project: project, cwd: workspace})
		}
	}
	// Provider priority order, with project sources before user sources.
	add("native", filepath.Join(workspace, ".omp", "mcp.json"), "json", true)
	add("native", filepath.Join(workspace, ".omp", ".mcp.json"), "json", true)
	add("native", filepath.Join(native, "mcp.json"), "json", false)
	add("native", filepath.Join(native, ".mcp.json"), "json", false)
	add("claude", filepath.Join(workspace, ".claude", "mcp.json"), "json", true)
	add("claude", filepath.Join(workspace, ".claude.json"), "claude", true)
	add("claude", filepath.Join(home, ".claude", "mcp.json"), "json", false)
	add("claude", filepath.Join(home, ".claude.json"), "claude", false)
	add("codex", filepath.Join(workspace, ".codex", "config.toml"), "toml", true)
	add("codex", filepath.Join(home, ".codex", "config.toml"), "toml", false)
	add("gemini", filepath.Join(workspace, ".gemini", "settings.json"), "json", true)
	add("gemini", filepath.Join(home, ".gemini", "settings.json"), "json", false)
	for _, path := range []string{
		filepath.Join(workspace, ".opencode", "opencode.json"), filepath.Join(workspace, ".opencode", "opencode.jsonc"),
		filepath.Join(workspace, "opencode.json"), filepath.Join(workspace, "opencode.jsonc"),
	} {
		add("opencode", path, "jsonc", true)
	}
	for _, path := range []string{
		filepath.Join(home, ".config", "opencode", "opencode.json"), filepath.Join(home, ".config", "opencode", "opencode.jsonc"),
		filepath.Join(home, ".config", "opencode", "config.json"),
	} {
		add("opencode", path, "jsonc", false)
	}
	add("cursor", filepath.Join(workspace, ".cursor", "mcp.json"), "json", true)
	add("cursor", filepath.Join(home, ".cursor", "mcp.json"), "json", false)
	add("windsurf", filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), "json", false)
	add("mcp-json", filepath.Join(workspace, "mcp.json"), "json", true)
	add("mcp-json", filepath.Join(workspace, ".mcp.json"), "json", true)
	add("vscode", filepath.Join(workspace, ".vscode", "mcp.json"), "json", true)

	result := DiscoveryResult{Servers: make(map[string]config.MCPServerConfig)}
	for _, source := range sources {
		payload, err := os.ReadFile(source.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Server: source.provider, Error: fmt.Sprintf("read %s: %v", source.path, err)})
			continue
		}
		if len(payload) > maxDiscoveryConfigBytes {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Server: source.provider, Error: fmt.Sprintf("skip oversized MCP config %s", source.path)})
			continue
		}
		servers, parseErr := parseMCPSource(source, payload, workspace)
		if parseErr != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Server: source.provider, Error: fmt.Sprintf("parse %s: %v", source.path, parseErr)})
			continue
		}
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, rawName := range names {
			name := normalizeDiscoveredServerName(rawName)
			if name == "" {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Server: rawName, Error: fmt.Sprintf("invalid server name in %s", source.path)})
				continue
			}
			if _, exists := result.Servers[name]; exists {
				continue
			}
			server, normalizeErr := normalizeDiscoveredServer(name, servers[rawName], source)
			if normalizeErr != nil {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Server: name, Error: normalizeErr.Error()})
				continue
			}
			result.Servers[name] = server
		}
	}
	return result
}

func parseMCPSource(source mcpSource, payload []byte, workspace string) (map[string]map[string]any, error) {
	if source.format == "toml" {
		var document struct {
			Servers map[string]map[string]any `toml:"mcp_servers"`
		}
		if _, err := toml.Decode(string(payload), &document); err != nil {
			return nil, err
		}
		return document.Servers, nil
	}
	if source.format == "jsonc" {
		payload = stripJSONComments(payload)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, err
	}
	if source.format == "claude" {
		if projects, ok := document["projects"].(map[string]any); ok {
			if project, ok := projects[workspace].(map[string]any); ok {
				if servers := objectMap(project["mcpServers"]); len(servers) > 0 {
					return servers, nil
				}
			}
		}
	}
	for _, key := range []string{"mcpServers", "servers", "mcp"} {
		if servers := objectMap(document[key]); len(servers) > 0 {
			return servers, nil
		}
	}
	return map[string]map[string]any{}, nil
}

func objectMap(value any) map[string]map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]map[string]any)
	for name, value := range object {
		if item, ok := value.(map[string]any); ok {
			result[name] = item
		}
	}
	return result
}

func normalizeDiscoveredServer(name string, raw map[string]any, source mcpSource) (config.MCPServerConfig, error) {
	server := config.MCPServerConfig{Enabled: true, Managed: true, Approval: "always"}
	if enabled, ok := raw["enabled"].(bool); ok {
		server.Enabled = enabled
	}
	if disabled, ok := raw["disabled"].(bool); ok && disabled {
		server.Enabled = false
	}
	server.Command, _ = raw["command"].(string)
	if commandParts := anyStrings(raw["command"]); len(commandParts) > 0 {
		server.Command = commandParts[0]
		server.Args = append(server.Args, commandParts[1:]...)
	}
	if len(server.Args) == 0 {
		server.Args = anyStrings(raw["args"])
	}
	server.CWD, _ = raw["cwd"].(string)
	if server.CWD == "" && source.project && server.Command != "" {
		server.CWD = source.cwd
	}
	server.URL, _ = raw["url"].(string)
	if server.URL == "" {
		server.URL, _ = raw["serverUrl"].(string)
	}
	server.URL = os.ExpandEnv(server.URL)
	server.RuntimeEnv = expandedStringMap(firstValue(raw, "env", "environment"))
	server.RuntimeHeaders = expandedStringMap(firstValue(raw, "headers", "http_headers", "httpHeaders"))
	if auth := anyMap(raw["auth"]); auth != nil {
		server.Auth = &config.MCPAuthConfig{
			Type: stringValue(auth, "type"), CredentialID: stringValue(auth, "credentialId", "credential_id"),
			TokenURL: os.ExpandEnv(stringValue(auth, "tokenUrl", "token_url")), ClientID: os.ExpandEnv(stringValue(auth, "clientId", "client_id")),
			Resource: os.ExpandEnv(stringValue(auth, "resource")),
		}
		server.RuntimeAuthClientSecret = os.ExpandEnv(stringValue(auth, "clientSecret", "client_secret"))
	}
	if oauth := anyMap(raw["oauth"]); oauth != nil {
		server.OAuth = &config.MCPOAuthConfig{
			AuthorizationURL: os.ExpandEnv(stringValue(oauth, "authorizationUrl", "authorization_url")),
			TokenURL:         os.ExpandEnv(stringValue(oauth, "tokenUrl", "token_url")),
			RegistrationURL:  os.ExpandEnv(stringValue(oauth, "registrationUrl", "registration_url")),
			ClientID:         os.ExpandEnv(stringValue(oauth, "clientId", "client_id")),
			Scopes:           anyStrings(oauth["scopes"]), RedirectURI: os.ExpandEnv(stringValue(oauth, "redirectUri", "redirect_uri")),
			CallbackPort: intValue(oauth, "callbackPort", "callback_port"),
			CallbackPath: stringValue(oauth, "callbackPath", "callback_path"), Prompt: stringValue(oauth, "prompt"),
		}
		server.RuntimeOAuthClientSecret = os.ExpandEnv(stringValue(oauth, "clientSecret", "client_secret"))
	}
	if server.Command != "" {
		server.Transport = "stdio"
	} else if server.URL != "" {
		server.Transport = "streamable_http"
	} else {
		return config.MCPServerConfig{}, fmt.Errorf("MCP server %q from %s has neither command nor URL", name, source.path)
	}
	return config.NormalizeMCPServer(name, server)
}

func normalizeDiscoveredServerName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Trim(discoveredServerNamePattern.ReplaceAllString(name, "_"), "_")
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "_")
	}
	return name
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value := values[key]; value != nil {
			return value
		}
	}
	return nil
}

func anyStrings(value any) []string {
	switch typed := value.(type) {
	case []any:
		var result []string
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, os.ExpandEnv(text))
			}
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	default:
		return nil
	}
}

func anyMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func stringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}

func intValue(values map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := values[key].(type) {
		case float64:
			return int(value)
		case int64:
			return int(value)
		case int:
			return value
		}
	}
	return 0
}

func expandedStringMap(value any) map[string]string {
	object, ok := value.(map[string]any)
	if !ok {
		if typed, ok := value.(map[string]string); ok {
			result := make(map[string]string, len(typed))
			for key, item := range typed {
				result[key] = os.ExpandEnv(item)
			}
			return result
		}
		return nil
	}
	result := make(map[string]string, len(object))
	for key, item := range object {
		if text, ok := item.(string); ok {
			result[key] = os.ExpandEnv(text)
		}
	}
	return result
}

func stripJSONComments(input []byte) []byte {
	output := make([]byte, 0, len(input))
	inString, escaped := false, false
	for index := 0; index < len(input); {
		current := input[index]
		if inString {
			output = append(output, current)
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			index++
			continue
		}
		if current == '"' {
			inString = true
			output = append(output, current)
			index++
			continue
		}
		if current == '/' && index+1 < len(input) && input[index+1] == '/' {
			for index < len(input) && input[index] != '\n' {
				index++
			}
			continue
		}
		if current == '/' && index+1 < len(input) && input[index+1] == '*' {
			index += 2
			for index+1 < len(input) && !(input[index] == '*' && input[index+1] == '/') {
				index++
			}
			if index+1 < len(input) {
				index += 2
			}
			continue
		}
		output = append(output, current)
		index++
	}
	return output
}
