package app

import "github.com/Viking602/azem/internal/plugins"

func pluginCatalogEntries(integration plugins.Integration) []PluginCatalogEntry {
	entries := make([]PluginCatalogEntry, len(integration.Entries))
	for index, entry := range integration.Entries {
		entries[index] = PluginCatalogEntry{
			ID: entry.ID, Name: entry.Name, DisplayName: entry.DisplayName, Version: entry.Version,
			Marketplace: entry.Marketplace, Description: entry.Description, DeveloperName: entry.DeveloperName,
			Category: entry.Category, BrandColor: entry.BrandColor, LogoPath: entry.LogoPath,
			Enabled: entry.Enabled, SkillCount: entry.SkillCount, MCPServerCount: entry.MCPServerCount,
			IntegratedMCPCount: entry.IntegratedMCPCount, HookCount: entry.HookCount,
			HooksTrusted: entry.HooksTrusted, HasApp: entry.HasApp,
			Capabilities: append([]string(nil), entry.Capabilities...), Status: entry.Status, Warning: entry.Warning,
		}
	}
	return entries
}

func pluginDiagnostics(integration plugins.Integration) []PluginDiagnostic {
	diagnostics := make([]PluginDiagnostic, len(integration.Diagnostics))
	for index, diagnostic := range integration.Diagnostics {
		diagnostics[index] = PluginDiagnostic{PluginID: diagnostic.PluginID, Path: diagnostic.Path, Message: diagnostic.Message}
	}
	return diagnostics
}
