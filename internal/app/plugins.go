package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Viking602/azem/internal/commands"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/plugins"
)

func pluginCatalogEntries(integration plugins.Integration) []PluginCatalogEntry {
	entries := make([]PluginCatalogEntry, len(integration.Entries))
	for index, entry := range integration.Entries {
		entries[index] = PluginCatalogEntry{
			ID: entry.ID, Name: entry.Name, DisplayName: entry.DisplayName, Version: entry.Version,
			Marketplace: entry.Marketplace, Origin: entry.Origin, Scope: entry.Scope, Description: entry.Description, DeveloperName: entry.DeveloperName,
			Category: entry.Category, BrandColor: entry.BrandColor, LogoPath: entry.LogoPath,
			Enabled: entry.Enabled, SkillCount: entry.SkillCount, MCPServerCount: entry.MCPServerCount,
			IntegratedMCPCount: entry.IntegratedMCPCount, HookCount: entry.HookCount,
			ToolCount: entry.ToolCount, CommandCount: entry.CommandCount,
			AgentCount: entry.AgentCount, ThemeCount: entry.ThemeCount, ExtensionCount: entry.ExtensionCount,
			HooksTrusted: entry.HooksTrusted, HasApp: entry.HasApp,
			Capabilities: append([]string(nil), entry.Capabilities...), Status: entry.Status, Warning: entry.Warning,
			Imported: entry.Imported,
		}
	}
	return entries
}

func (s *Service) AttachPluginRuntime(options plugins.Options, integration plugins.Integration) {
	s.pluginOptions = options
	s.rememberPluginRuntime(integration)
}

func (s *Service) rememberPluginRuntime(integration plugins.Integration) {
	s.pluginSkillDirs = append([]string(nil), integration.SkillDirs...)
	s.pluginHookSources = append([]plugins.HookSource(nil), integration.HookSources...)
	s.pluginMCPNames = s.pluginMCPNames[:0]
	for name := range integration.MCPServers {
		s.pluginMCPNames = append(s.pluginMCPNames, name)
	}
	slices.Sort(s.pluginMCPNames)
}

func (s *Service) setCodexPluginImported(ctx context.Context, pluginID string, imported bool) error {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return fmt.Errorf("plugin id is required")
	}
	index := slices.IndexFunc(s.pluginCatalog, func(entry PluginCatalogEntry) bool {
		return pluginCatalogIdentity(entry) == pluginID || entry.ID == pluginID
	})
	if index < 0 {
		return fmt.Errorf("plugin %q not found", pluginID)
	}
	pluginID = pluginCatalogIdentity(s.pluginCatalog[index])
	entry := s.pluginCatalog[index]
	if entry.Origin != "codex" && entry.Origin != "codex_available" {
		return fmt.Errorf("plugin %q is not a Codex import", pluginID)
	}
	previous := append([]string(nil), s.cfg.Plugins.CodexImports...)
	imports := slices.DeleteFunc(append([]string(nil), previous...), func(candidate string) bool { return candidate == pluginID })
	if imported {
		imports = append(imports, pluginID)
	}
	slices.Sort(imports)
	if err := s.persistCodexImports(imports); err != nil {
		return err
	}
	s.cfg.Plugins.CodexImports = imports
	if err := s.reloadPluginRuntime(ctx); err != nil {
		s.restoreCodexImports(previous)
		return err
	}
	if imported && !pluginCatalogHasLoaded(s.pluginCatalog, pluginID) {
		s.restoreCodexImports(previous)
		_ = s.reloadPluginRuntime(ctx)
		return fmt.Errorf("import plugin %q failed: %s", pluginID, pluginImportFailure(s.pluginDiagnostics, pluginID))
	}
	return nil
}

func (s *Service) persistCodexImports(imports []string) error {
	if s.configPath == "" {
		return nil
	}
	return s.ensureHookWatcher().writeConfig(s.configPath, func() error {
		return config.UpdateCodexPluginImports(s.configPath, imports)
	})
}

func (s *Service) restoreCodexImports(imports []string) {
	s.cfg.Plugins.CodexImports = append([]string(nil), imports...)
	_ = s.persistCodexImports(imports)
}

func (s *Service) reloadPluginRuntime(ctx context.Context) error {
	options := s.pluginOptions
	if strings.TrimSpace(options.DataDir) == "" && strings.TrimSpace(options.HomeDir) == "" {
		return fmt.Errorf("plugin runtime is not attached")
	}
	options.CodexImports = append([]string(nil), s.cfg.Plugins.CodexImports...)
	options.ImportCodex = s.cfg.Plugins.ImportCodex || len(options.CodexImports) > 0
	options.TrustHooks = s.cfg.Plugins.TrustHooks
	options.FallbackCatalog = s.codexListingJSON()
	integration := plugins.Discover(ctx, options)
	if err := s.applyPluginSkills(integration.SkillDirs); err != nil {
		return err
	}
	if err := s.applyPluginMCP(ctx, integration.MCPServers); err != nil {
		return err
	}
	s.applyPluginHooks(integration.HookSources)
	s.rememberPluginRuntime(integration)
	if s.cfg.Extensions.Enabled {
		disabledProviders := make(map[string]bool, len(s.cfg.Discovery.DisabledProviders))
		for _, provider := range s.cfg.Discovery.DisabledProviders {
			disabledProviders[strings.ToLower(strings.TrimSpace(provider))] = true
		}
		commandDirs := append(append([]string(nil), s.cfg.Extensions.AdditionalCommandDirs...), integration.CommandDirs...)
		catalog, diagnostics, commandErr := commands.Discover(commands.Options{
			Workspace: options.WorkspaceDir, HomeDir: options.HomeDir,
			DisabledProviders: disabledProviders, AdditionalDirs: commandDirs,
		})
		if commandErr != nil {
			return commandErr
		}
		s.AttachCommands(catalog, diagnostics)
	}
	s.pluginCatalog = pluginCatalogEntries(integration)
	s.pluginDiagnostics = pluginDiagnostics(integration)
	s.emit(ctx, Event{Kind: EventPluginCatalog, State: "updated", PluginCatalog: s.pluginCatalog, PluginDiagnostics: s.pluginDiagnostics})
	_ = s.emitHookCatalog(ctx, "updated")
	if s.skillCatalog != nil {
		if err := s.emitSkillCatalog(ctx, "reloaded"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyPluginSkills(nextDirs []string) error {
	s.cfg.Skills.AdditionalDirs = mergeSkillDirs(s.cfg.Skills.AdditionalDirs, s.pluginSkillDirs, nextDirs)
	if s.skillCatalog == nil {
		return nil
	}
	return s.skillCatalog.UpdateConfig(cloneSkillsConfig(s.cfg.Skills), nil)
}

func (s *Service) applyPluginMCP(ctx context.Context, next map[string]config.MCPServerConfig) error {
	removed := make(map[string]struct{}, len(s.cfg.MCP.RemovedServers))
	for _, name := range s.cfg.MCP.RemovedServers {
		removed[name] = struct{}{}
	}
	previous := make(map[string]struct{}, len(s.pluginMCPNames))
	for _, name := range s.pluginMCPNames {
		previous[name] = struct{}{}
	}
	for name, server := range next {
		if _, suppressed := removed[name]; suppressed {
			continue
		}
		if current, exists := s.cfg.MCP.Servers[name]; exists {
			if _, owned := previous[name]; !owned {
				current.Managed = true
				if server.Icon != "" {
					current.Icon = server.Icon
				}
				s.cfg.MCP.Servers[name] = current
				continue
			}
		}
		server.Managed = true
		icon := server.Icon
		if s.mcp != nil {
			normalized, err := config.NormalizeMCPServer(name, server)
			if err != nil {
				return err
			}
			normalized, err = s.mcp.Configure(name, normalized)
			if err != nil {
				return err
			}
			server = normalized
		}
		if icon != "" {
			server.Icon = icon
		}
		if s.cfg.MCP.Servers == nil {
			s.cfg.MCP.Servers = map[string]config.MCPServerConfig{}
		}
		s.cfg.MCP.Servers[name] = server
		if s.mcp != nil && server.Enabled {
			s.startMCPReconnect(name)
		}
	}
	for name := range previous {
		if _, keep := next[name]; keep {
			continue
		}
		if s.mcp != nil {
			if err := s.mcp.Remove(name); err != nil {
				return err
			}
		}
		delete(s.cfg.MCP.Servers, name)
	}
	if s.mcp != nil {
		return s.emitMCPSnapshot(ctx)
	}
	return nil
}

func (s *Service) applyPluginHooks(sources []plugins.HookSource) {
	if s.hooks.Registry == nil {
		return
	}
	kept := make([]hooks.Source, 0, len(s.hookOptions.Sources))
	for _, source := range s.hookOptions.Sources {
		if source.Environment["PLUGIN_ROOT"] != "" {
			continue
		}
		kept = append(kept, source)
	}
	if s.cfg.Plugins.TrustHooks {
		for _, source := range sources {
			if dataDir := source.Environment["PLUGIN_DATA"]; dataDir != "" {
				_ = os.MkdirAll(dataDir, 0o700)
			}
			kept = append(kept, hooks.Source{Path: source.Path, Trusted: true, Environment: source.Environment})
		}
	}
	s.hookOptions.Sources = kept
	s.hookOptions.Disabled = append([]string(nil), s.cfg.Hooks.Disabled...)
	s.hooks.Registry.Replace(hooks.Discover(s.hookOptions))
}

func mergeSkillDirs(current, previousPlugin, nextPlugin []string) []string {
	drop := make(map[string]struct{}, len(previousPlugin))
	for _, path := range previousPlugin {
		drop[filepath.Clean(path)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(current)+len(nextPlugin))
	result := make([]string, 0, len(current)+len(nextPlugin))
	appendUnique := func(path string, skipDropped bool) {
		clean := filepath.Clean(path)
		if clean == "." || path == "" {
			return
		}
		if skipDropped {
			if _, dropped := drop[clean]; dropped {
				return
			}
		}
		if _, exists := seen[clean]; exists {
			return
		}
		seen[clean] = struct{}{}
		result = append(result, path)
	}
	for _, path := range current {
		appendUnique(path, true)
	}
	for _, path := range nextPlugin {
		appendUnique(path, false)
	}
	return result
}

func (s *Service) codexListingJSON() []byte {
	type item struct {
		PluginID    string `json:"pluginId"`
		Name        string `json:"name"`
		Marketplace string `json:"marketplaceName"`
		Version     string `json:"version"`
		Installed   bool   `json:"installed"`
		Enabled     bool   `json:"enabled"`
	}
	installed := make([]item, 0, len(s.pluginCatalog))
	for _, entry := range s.pluginCatalog {
		if entry.Origin != "codex" && entry.Origin != "codex_available" {
			continue
		}
		id := pluginCatalogIdentity(entry)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = strings.TrimSpace(entry.DisplayName)
		}
		installed = append(installed, item{
			PluginID: id, Name: name, Marketplace: entry.Marketplace, Version: entry.Version,
			Installed: true, Enabled: true,
		})
	}
	if len(installed) == 0 {
		return nil
	}
	encoded, err := json.Marshal(map[string]any{"installed": installed})
	if err != nil {
		return nil
	}
	return encoded
}

func pluginCatalogIdentity(entry PluginCatalogEntry) string {
	if id := strings.TrimSpace(entry.ID); id != "" {
		return id
	}
	name := strings.TrimSpace(entry.Name)
	marketplace := strings.TrimSpace(entry.Marketplace)
	if name != "" && marketplace != "" {
		return name + "@" + marketplace
	}
	return name
}

func pluginCatalogHasLoaded(entries []PluginCatalogEntry, pluginID string) bool {
	for _, entry := range entries {
		if pluginCatalogIdentity(entry) == pluginID && entry.Origin == "codex" {
			return true
		}
	}
	return false
}

func pluginImportFailure(diagnostics []PluginDiagnostic, pluginID string) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.PluginID == pluginID && strings.TrimSpace(diagnostic.Message) != "" {
			return diagnostic.Message
		}
	}
	for _, diagnostic := range diagnostics {
		if strings.TrimSpace(diagnostic.Message) != "" {
			return diagnostic.Message
		}
	}
	return "Codex package could not be copied into Azem"
}

func pluginDiagnostics(integration plugins.Integration) []PluginDiagnostic {
	diagnostics := make([]PluginDiagnostic, len(integration.Diagnostics))
	for index, diagnostic := range integration.Diagnostics {
		diagnostics[index] = PluginDiagnostic{PluginID: diagnostic.PluginID, Path: diagnostic.Path, Message: diagnostic.Message}
	}
	return diagnostics
}
