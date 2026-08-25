package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/plugins"
)

func (b *bootstrapAssembly) loadPlugins(desktopMode bool) error {
	if !desktopMode || os.Getenv("AZEM_FAKE_PROVIDER") == "1" || !b.cfg.Plugins.Enabled {
		return nil
	}
	if err := b.removeLegacyCodexComputerUseMCP(); err != nil {
		return err
	}
	b.pluginCatalog = plugins.Discover(b.ctx, plugins.Options{
		HomeDir: b.homeDir, DataDir: b.paths.DataDir, WorkspaceDir: b.paths.Workspace,
		ImportCodex: b.cfg.Plugins.ImportCodex, CodexImports: b.cfg.Plugins.CodexImports, TrustHooks: b.cfg.Plugins.TrustHooks,
	})
	b.mergePlugins()
	return nil
}

func (b *bootstrapAssembly) mergePlugins() {
	seenSkills := make(map[string]bool, len(b.cfg.Skills.AdditionalDirs)+len(b.pluginCatalog.SkillDirs))
	mergedSkills := make([]string, 0, len(b.cfg.Skills.AdditionalDirs)+len(b.pluginCatalog.SkillDirs))
	for _, path := range append(append([]string(nil), b.cfg.Skills.AdditionalDirs...), b.pluginCatalog.SkillDirs...) {
		clean := filepath.Clean(path)
		if clean == "." || seenSkills[clean] {
			continue
		}
		seenSkills[clean] = true
		mergedSkills = append(mergedSkills, path)
	}
	b.cfg.Skills.AdditionalDirs = mergedSkills
	if b.cfg.MCP.Servers == nil {
		b.cfg.MCP.Servers = map[string]config.MCPServerConfig{}
	}
	removed := make(map[string]struct{}, len(b.cfg.MCP.RemovedServers))
	for _, name := range b.cfg.MCP.RemovedServers {
		removed[name] = struct{}{}
	}
	for name, server := range b.pluginCatalog.MCPServers {
		if _, suppressed := removed[name]; suppressed {
			continue
		}
		if configuredServer, configured := b.cfg.MCP.Servers[name]; configured {
			configuredServer.Managed = true
			if server.Icon != "" {
				configuredServer.Icon = server.Icon
			}
			b.cfg.MCP.Servers[name] = configuredServer
			continue
		}
		b.cfg.MCP.Servers[name] = server
	}
}

func mergeDiscoveredMCP(cfg *config.Config, discovered map[string]config.MCPServerConfig) {
	if cfg == nil {
		return
	}
	if cfg.MCP.Servers == nil {
		cfg.MCP.Servers = make(map[string]config.MCPServerConfig)
	}
	removed := make(map[string]bool, len(cfg.MCP.RemovedServers))
	for _, name := range cfg.MCP.RemovedServers {
		removed[name] = true
	}
	for name, server := range discovered {
		if _, exists := cfg.MCP.Servers[name]; !exists && !removed[name] {
			cfg.MCP.Servers[name] = server
		}
	}
}

func (b *bootstrapAssembly) removeLegacyCodexComputerUseMCP() error {
	const serverName = "computer-use-computer-use"
	server, exists := b.cfg.MCP.Servers[serverName]
	if !exists || filepath.Base(server.Command) != "computer-use-client-launcher" || !strings.Contains(filepath.ToSlash(server.Command), "/.codex/") {
		return nil
	}
	if b.paths.ConfigFile != "" {
		if err := config.DeleteMCPServer(b.paths.ConfigFile, serverName); err != nil {
			return fmt.Errorf("remove legacy Codex computer-use MCP reference: %w", err)
		}
	}
	delete(b.cfg.MCP.Servers, serverName)
	if !slices.Contains(b.cfg.MCP.RemovedServers, serverName) {
		b.cfg.MCP.RemovedServers = append(b.cfg.MCP.RemovedServers, serverName)
		slices.Sort(b.cfg.MCP.RemovedServers)
	}
	return nil
}
