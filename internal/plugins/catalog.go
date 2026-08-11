package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/config"
)

const maxDescriptorBytes = 1 << 20

var serverNamePattern = regexp.MustCompile(`[^a-z0-9_-]+`)

type Entry struct {
	ID                 string
	Name               string
	DisplayName        string
	Version            string
	Marketplace        string
	Description        string
	DeveloperName      string
	Category           string
	BrandColor         string
	LogoPath           string
	Root               string
	Enabled            bool
	SkillCount         int
	MCPServerCount     int
	IntegratedMCPCount int
	HookCount          int
	HooksTrusted       bool
	HasApp             bool
	Capabilities       []string
	Status             string
	Warning            string
}

type Diagnostic struct {
	PluginID string
	Path     string
	Message  string
}

type HookSource struct {
	Path        string
	Environment map[string]string
}

type Integration struct {
	Entries     []Entry
	Diagnostics []Diagnostic
	SkillDirs   []string
	MCPServers  map[string]config.MCPServerConfig
	HookSources []HookSource
}

type Options struct {
	HomeDir     string
	DataDir     string
	TrustHooks  bool
	ListPlugins func(context.Context) ([]byte, error)
}

type installedCatalog struct {
	Installed []installedPlugin `json:"installed"`
}

type installedPlugin struct {
	PluginID    string       `json:"pluginId"`
	Name        string       `json:"name"`
	Marketplace string       `json:"marketplaceName"`
	Version     string       `json:"version"`
	Installed   bool         `json:"installed"`
	Enabled     bool         `json:"enabled"`
	Source      pluginSource `json:"source"`
}

type pluginSource struct {
	Path string `json:"path"`
}

type manifest struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Description string     `json:"description"`
	Skills      string     `json:"skills"`
	MCPServers  string     `json:"mcpServers"`
	Apps        string     `json:"apps"`
	Hooks       string     `json:"hooks"`
	Interface   manifestUI `json:"interface"`
}

type manifestUI struct {
	DisplayName   string   `json:"displayName"`
	DeveloperName string   `json:"developerName"`
	Category      string   `json:"category"`
	Capabilities  []string `json:"capabilities"`
	BrandColor    string   `json:"brandColor"`
	Logo          string   `json:"logo"`
}

type mcpDescriptor struct {
	Type              string            `json:"type"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	CWD               string            `json:"cwd"`
	Env               map[string]string `json:"env"`
	EnvVars           []string          `json:"env_vars"`
	URL               string            `json:"url"`
	Headers           map[string]string `json:"headers"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var"`
	StartupTimeoutSec float64           `json:"startup_timeout_sec"`
}

type manifestPaths struct {
	logo   string
	skills string
	mcp    string
	hooks  string
	apps   string
}

func Discover(ctx context.Context, options Options) Integration {
	result := Integration{MCPServers: map[string]config.MCPServerConfig{}}
	if strings.TrimSpace(options.HomeDir) == "" {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Message: "plugin discovery requires a home directory"})
		return result
	}
	list := options.ListPlugins
	if list == nil {
		list = listWithCodex
	}
	encoded, err := list(ctx)
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Message: fmt.Sprintf("codex plugin list failed: %v", err)})
		return result
	}
	var catalog installedCatalog
	if err := json.Unmarshal(encoded, &catalog); err != nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Message: fmt.Sprintf("decode codex plugin catalog: %v", err)})
		return result
	}
	for _, installed := range catalog.Installed {
		mergeInstalledPlugin(&result, options, installed)
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		return strings.ToLower(result.Entries[i].DisplayName) < strings.ToLower(result.Entries[j].DisplayName)
	})
	sort.Strings(result.SkillDirs)
	return result
}

func mergeInstalledPlugin(result *Integration, options Options, installed installedPlugin) {
	if !installed.Installed {
		return
	}
	entry, skillDir, servers, hookSource, diagnostics := inspectPlugin(options, installed)
	result.Entries = append(result.Entries, entry)
	result.Diagnostics = append(result.Diagnostics, diagnostics...)
	if !installed.Enabled || entry.Status == "invalid" {
		return
	}
	if skillDir != "" {
		result.SkillDirs = append(result.SkillDirs, skillDir)
	}
	mergeMCPServers(result.MCPServers, servers)
	if options.TrustHooks && hookSource.Path != "" {
		result.HookSources = append(result.HookSources, hookSource)
	}
}

func mergeMCPServers(target, source map[string]config.MCPServerConfig) {
	for name, server := range source {
		resolvedName := name
		for suffix := 2; ; suffix++ {
			if _, exists := target[resolvedName]; !exists {
				break
			}
			resolvedName = fmt.Sprintf("%s-%d", name, suffix)
		}
		target[resolvedName] = server
	}
}

func listWithCodex(parent context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "codex", "plugin", "list", "--json")
	command.Env = os.Environ()
	encoded, err := command.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("timed out after 3s")
		}
		return nil, err
	}
	return encoded, nil
}

func inspectPlugin(options Options, installed installedPlugin) (Entry, string, map[string]config.MCPServerConfig, HookSource, []Diagnostic) {
	entry := Entry{ID: installed.PluginID, Name: installed.Name, DisplayName: installed.Name, Version: installed.Version,
		Marketplace: installed.Marketplace, Enabled: installed.Enabled, Status: "ready"}
	servers := map[string]config.MCPServerConfig{}
	root, err := pluginRoot(options.HomeDir, installed)
	if err != nil {
		entry.Status, entry.Warning = "invalid", err.Error()
		return entry, "", servers, HookSource{}, []Diagnostic{{PluginID: entry.ID, Message: err.Error()}}
	}
	entry.Root = root
	var value manifest
	manifestPath := filepath.Join(root, ".codex-plugin", "plugin.json")
	if err := decodeJSONFile(manifestPath, &value); err != nil {
		entry.Status, entry.Warning = "invalid", err.Error()
		return entry, "", servers, HookSource{}, []Diagnostic{{PluginID: entry.ID, Path: manifestPath, Message: err.Error()}}
	}
	if value.Name == "" || value.Version == "" || value.Description == "" {
		err := errors.New("plugin manifest requires name, version, and description")
		entry.Status, entry.Warning = "invalid", err.Error()
		return entry, "", servers, HookSource{}, []Diagnostic{{PluginID: entry.ID, Path: manifestPath, Message: err.Error()}}
	}
	entry.Name, entry.Version, entry.Description = value.Name, value.Version, value.Description
	entry.DisplayName = firstNonEmpty(value.Interface.DisplayName, value.Name)
	entry.DeveloperName, entry.Category = value.Interface.DeveloperName, value.Interface.Category
	entry.BrandColor, entry.Capabilities = value.Interface.BrandColor, append([]string(nil), value.Interface.Capabilities...)
	paths, diagnostics := resolveManifestPaths(root, entry.ID, value)
	entry.LogoPath = paths.logo
	skillDir := paths.skills
	entry.SkillCount = countSkillDirectories(skillDir)
	mcpDiagnostics := integrateMCPServers(&entry, servers, root, pluginDataRoot(options.DataDir, installed), paths.mcp)
	diagnostics = append(diagnostics, mcpDiagnostics...)
	entry.HookCount = boolCount(paths.hooks != "")
	entry.HooksTrusted = options.TrustHooks && paths.hooks != ""
	if paths.hooks != "" && !options.TrustHooks {
		entry.Warning = appendWarning(entry.Warning, "Hooks 等待用户信任")
	}
	entry.HasApp = paths.apps != ""
	if paths.apps != "" {
		entry.Warning = appendWarning(entry.Warning, "App 连接需要单独授权")
	}
	if len(diagnostics) > 0 && entry.Status == "ready" {
		entry.Status = "degraded"
	}
	dataRoot := pluginDataRoot(options.DataDir, installed)
	hookSource := HookSource{Path: paths.hooks, Environment: map[string]string{
		"PLUGIN_ROOT": root, "CLAUDE_PLUGIN_ROOT": root, "PLUGIN_DATA": dataRoot,
	}}
	return entry, skillDir, servers, hookSource, diagnostics
}

func resolveManifestPaths(root, pluginID string, value manifest) (manifestPaths, []Diagnostic) {
	paths := manifestPaths{}
	diagnostics := []Diagnostic{}
	references := []struct {
		field     string
		reference string
		target    *string
	}{
		{field: "interface.logo", reference: value.Interface.Logo, target: &paths.logo},
		{field: "skills", reference: value.Skills, target: &paths.skills},
		{field: "mcpServers", reference: value.MCPServers, target: &paths.mcp},
		{field: "hooks", reference: value.Hooks, target: &paths.hooks},
		{field: "apps", reference: value.Apps, target: &paths.apps},
	}
	for _, item := range references {
		if item.reference == "" {
			continue
		}
		resolved, err := resolvePluginPath(root, item.reference)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{PluginID: pluginID, Path: item.reference, Message: item.field + ": " + err.Error()})
			continue
		}
		*item.target = resolved
	}
	return paths, diagnostics
}

func integrateMCPServers(entry *Entry, servers map[string]config.MCPServerConfig, root, dataRoot, descriptorPath string) []Diagnostic {
	if descriptorPath == "" {
		return nil
	}
	descriptors, err := decodeMCPDescriptors(descriptorPath)
	if err != nil {
		return []Diagnostic{{PluginID: entry.ID, Path: descriptorPath, Message: err.Error()}}
	}
	entry.MCPServerCount = len(descriptors)
	var diagnostics []Diagnostic
	for name, descriptor := range descriptors {
		serverName := uniqueServerName(entry.Name, name, servers)
		server, warning, buildErr := buildMCPServer(root, dataRoot, descriptor)
		if buildErr != nil {
			diagnostics = append(diagnostics, Diagnostic{PluginID: entry.ID, Path: descriptorPath, Message: name + ": " + buildErr.Error()})
			continue
		}
		entry.Warning = appendWarning(entry.Warning, warning)
		servers[serverName] = server
		entry.IntegratedMCPCount += boolCount(server.Enabled)
	}
	return diagnostics
}

func pluginRoot(home string, installed installedPlugin) (string, error) {
	candidates := []string{installed.Source.Path}
	if installed.Marketplace != "" && installed.Name != "" && installed.Version != "" {
		candidates = append(candidates, filepath.Join(home, ".codex", "plugins", "cache", installed.Marketplace, installed.Name, installed.Version))
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(absolute, ".codex-plugin", "plugin.json")); err == nil && info.Mode().IsRegular() {
			return absolute, nil
		}
	}
	return "", fmt.Errorf("installed plugin root is unavailable for %s", installed.PluginID)
}

func resolvePluginPath(root, reference string) (string, error) {
	if !strings.HasPrefix(reference, "./") {
		return "", fmt.Errorf("path %q must begin with ./", reference)
	}
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(reference, "./")))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootEval, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the plugin root", reference)
	}
	return resolved, nil
}

func decodeJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxDescriptorBytes {
		return fmt.Errorf("descriptor must be a regular file no larger than 1 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxDescriptorBytes+1))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("descriptor must contain one JSON document")
	}
	return nil
}

func decodeMCPDescriptors(path string) (map[string]mcpDescriptor, error) {
	var raw map[string]json.RawMessage
	if err := decodeJSONFile(path, &raw); err != nil {
		return nil, err
	}
	for _, key := range []string{"mcpServers", "mcp_servers"} {
		if value, ok := raw[key]; ok {
			var wrapped map[string]mcpDescriptor
			if err := json.Unmarshal(value, &wrapped); err != nil {
				return nil, err
			}
			return wrapped, nil
		}
	}
	encoded, _ := json.Marshal(raw)
	var direct map[string]mcpDescriptor
	if err := json.Unmarshal(encoded, &direct); err != nil {
		return nil, err
	}
	return direct, nil
}

func buildMCPServer(root, dataRoot string, descriptor mcpDescriptor) (config.MCPServerConfig, string, error) {
	server := config.MCPServerConfig{Enabled: true, InheritEnv: true, ConnectTimeout: "30s", CallTimeout: "60s",
		ConnectDuration: 30 * time.Second, CallDuration: 60 * time.Second, MaxConcurrency: 2, Approval: "always",
		RuntimeEnv: map[string]string{"PLUGIN_ROOT": root, "PLUGIN_DATA": dataRoot}}
	if descriptor.StartupTimeoutSec > 0 {
		server.ConnectDuration = time.Duration(descriptor.StartupTimeoutSec * float64(time.Second))
		server.ConnectTimeout = server.ConnectDuration.String()
	}
	for key, value := range descriptor.Env {
		server.RuntimeEnv[key] = value
	}
	for _, key := range descriptor.EnvVars {
		if value, ok := os.LookupEnv(key); ok {
			server.RuntimeEnv[key] = value
		}
	}
	transport := strings.ToLower(strings.TrimSpace(descriptor.Type))
	if transport == "http" || transport == "streamable_http" || descriptor.URL != "" {
		return buildHTTPMCPServer(server, descriptor)
	}
	return buildStdioMCPServer(server, root, descriptor)
}

func buildHTTPMCPServer(server config.MCPServerConfig, descriptor mcpDescriptor) (config.MCPServerConfig, string, error) {
	if strings.TrimSpace(descriptor.URL) == "" {
		return server, "", errors.New("HTTP MCP server requires url")
	}
	endpoint, err := url.Parse(descriptor.URL)
	if err != nil || endpoint.Hostname() == "" {
		return server, "", errors.New("HTTP MCP server has an invalid url")
	}
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && isLoopbackHost(endpoint.Hostname())) {
		return server, "", errors.New("HTTP MCP server requires HTTPS except on loopback")
	}
	server.Transport, server.URL = "streamable_http", descriptor.URL
	server.Headers = map[string]string{}
	server.RuntimeHeaders = cloneMap(descriptor.Headers)
	if descriptor.BearerTokenEnvVar != "" {
		server.Headers["Authorization"] = "env:" + descriptor.BearerTokenEnvVar
		return server, "", nil
	}
	server.Enabled = false
	return server, "远程 MCP 需要 OAuth 或显式凭据，当前未自动连接", nil
}

func buildStdioMCPServer(server config.MCPServerConfig, root string, descriptor mcpDescriptor) (config.MCPServerConfig, string, error) {
	if strings.TrimSpace(descriptor.Command) == "" {
		return server, "", errors.New("stdio MCP server requires command")
	}
	server.Transport, server.Command, server.Args = "stdio", descriptor.Command, append([]string(nil), descriptor.Args...)
	if strings.HasPrefix(server.Command, "./") {
		resolved, err := resolvePluginPath(root, server.Command)
		if err != nil {
			return server, "", err
		}
		server.Command = resolved
	}
	server.CWD = root
	if cwd := strings.TrimSpace(descriptor.CWD); cwd != "" && cwd != "." {
		if !strings.HasPrefix(cwd, "./") {
			cwd = "./" + cwd
		}
		resolved, err := resolvePluginPath(root, cwd)
		if err != nil {
			return server, "", err
		}
		server.CWD = resolved
	}
	return server, "", nil
}

func pluginDataRoot(dataDir string, installed installedPlugin) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = filepath.Join(filepath.Dir(filepath.Dir(installed.Source.Path)), "plugin-data")
	}
	return filepath.Join(dataDir, "plugins", sanitizeName(installed.Marketplace), sanitizeName(installed.Name))
}

func countSkillDirectories(root string) int {
	if root == "" {
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, entry.Name(), "SKILL.md")); err == nil && info.Mode().IsRegular() {
			count++
		}
	}
	return count
}

func uniqueServerName(pluginName, serverName string, existing map[string]config.MCPServerConfig) string {
	base := sanitizeName(pluginName + "-" + serverName)
	if base == "" {
		base = "plugin-mcp"
	}
	name := base
	for index := 2; ; index++ {
		if _, exists := existing[name]; !exists {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, index)
	}
}

func sanitizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(serverNamePattern.ReplaceAllString(value, "-"), "-_")
	return value
}

func cloneMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func appendWarning(current, next string) string {
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	return current + "；" + next
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
