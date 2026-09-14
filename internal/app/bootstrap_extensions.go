package app

import (
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/commands"
	"github.com/Viking602/azem/internal/extensions"
)

func (b *bootstrapAssembly) loadExtensions() error {
	if !b.cfg.Extensions.Enabled {
		return nil
	}
	disabledProviders := make(map[string]bool, len(b.cfg.Discovery.DisabledProviders))
	for _, provider := range b.cfg.Discovery.DisabledProviders {
		disabledProviders[strings.ToLower(strings.TrimSpace(provider))] = true
	}
	agentDirs := append(append([]string(nil), b.cfg.Extensions.AdditionalAgentDirs...), b.pluginCatalog.AgentDirs...)
	themeDirs := append(append([]string(nil), b.cfg.Extensions.AdditionalThemeDirs...), b.pluginCatalog.ThemeDirs...)
	agents, themes, catalogDiagnostics, err := extensions.Discover(extensions.DiscoveryOptions{
		Workspace: b.paths.Workspace, HomeDir: b.homeDir, AgentDirs: agentDirs, ThemeDirs: themeDirs,
	})
	if err != nil {
		return fmt.Errorf("discover extension agents and themes: %w", err)
	}
	b.extensionThemes = themes
	b.extensionDiagnostics = append(b.extensionDiagnostics, catalogDiagnostics...)
	b.extensionDiagnostics = append(b.extensionDiagnostics, mergeExtensionAgents(&b.cfg, agents)...)
	commandDirs := append(append([]string(nil), b.cfg.Extensions.AdditionalCommandDirs...), b.pluginCatalog.CommandDirs...)
	b.commandCatalog, b.commandDiagnostics, err = commands.Discover(commands.Options{
		Workspace: b.paths.Workspace, HomeDir: b.homeDir, DisabledProviders: disabledProviders, AdditionalDirs: commandDirs,
	})
	if err != nil {
		return fmt.Errorf("discover custom commands: %w", err)
	}
	if err := b.cfg.Validate(); err != nil {
		return fmt.Errorf("validate extensions: %w", err)
	}
	return nil
}
