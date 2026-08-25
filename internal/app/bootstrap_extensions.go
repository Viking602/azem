package app

import (
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/commands"
	"github.com/Viking602/azem/internal/customtools"
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
	toolPaths := append(append([]string(nil), b.cfg.Extensions.AdditionalToolPaths...), b.pluginCatalog.ToolPaths...)
	modules, diagnostics, err := customtools.Discover(customtools.DiscoveryOptions{
		Workspace: b.paths.Workspace, HomeDir: b.homeDir, TrustProject: b.cfg.Extensions.TrustProjectCode,
		DisabledProviders: disabledProviders, AdditionalPaths: toolPaths,
	})
	if err != nil {
		return fmt.Errorf("discover custom tools: %w", err)
	}
	b.customDiagnostics = append([]string(nil), diagnostics...)
	extensionPaths := append(append([]string(nil), b.cfg.Extensions.AdditionalExtensionPaths...), b.pluginCatalog.ExtensionPaths...)
	extensionModules, extensionDiagnostics, err := customtools.DiscoverExtensions(customtools.DiscoveryOptions{
		Workspace: b.paths.Workspace, HomeDir: b.homeDir, TrustProject: b.cfg.Extensions.TrustProjectCode,
		DisabledProviders: disabledProviders, AdditionalPaths: extensionPaths,
	})
	if err != nil {
		return fmt.Errorf("discover extensions: %w", err)
	}
	b.extensionDiagnostics = append(b.extensionDiagnostics, extensionDiagnostics...)
	if len(modules) > 0 || len(extensionModules) > 0 {
		b.customTools, err = customtools.NewWithExtensions(b.ctx, b.paths.Workspace, modules, extensionModules)
		if err != nil {
			return fmt.Errorf("load custom tools and extensions: %w", err)
		}
		b.customDiagnostics = append(b.customDiagnostics, b.customTools.Diagnostics()...)
		b.extensionDiagnostics = append(b.extensionDiagnostics, applyExtensionProviders(&b.cfg, b.customTools.ExtensionProviders())...)
	}
	agentDirs := append(append([]string(nil), b.cfg.Extensions.AdditionalAgentDirs...), b.pluginCatalog.AgentDirs...)
	themeDirs := append(append([]string(nil), b.cfg.Extensions.AdditionalThemeDirs...), b.pluginCatalog.ThemeDirs...)
	if b.customTools != nil {
		themeDirs = append(themeDirs, b.customTools.ThemePaths()...)
	}
	agents, themes, catalogDiagnostics, err := extensions.Discover(extensions.DiscoveryOptions{
		Workspace: b.paths.Workspace, HomeDir: b.homeDir, AgentDirs: agentDirs, ThemeDirs: themeDirs,
	})
	if err != nil {
		return fmt.Errorf("discover extension agents and themes: %w", err)
	}
	b.extensionThemes = themes
	b.extensionDiagnostics = append(b.extensionDiagnostics, catalogDiagnostics...)
	var registeredAgents []customtools.ExtensionAgent
	if b.customTools != nil {
		registeredAgents = b.customTools.ExtensionAgents()
	}
	b.extensionDiagnostics = append(b.extensionDiagnostics, mergeExtensionAgents(&b.cfg, agents, registeredAgents)...)
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
