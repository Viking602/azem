package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/plugins"
)

func pluginCatalogEntries(integration plugins.Integration) []PluginCatalogEntry {
	entries := make([]PluginCatalogEntry, len(integration.Entries))
	for index, entry := range integration.Entries {
		entries[index] = PluginCatalogEntry{
			ID: entry.ID, Name: entry.Name, DisplayName: entry.DisplayName, Version: entry.Version,
			Marketplace: entry.Marketplace, Origin: entry.Origin, Description: entry.Description, DeveloperName: entry.DeveloperName,
			Category: entry.Category, BrandColor: entry.BrandColor, LogoPath: entry.LogoPath,
			Enabled: entry.Enabled, SkillCount: entry.SkillCount, MCPServerCount: entry.MCPServerCount,
			IntegratedMCPCount: entry.IntegratedMCPCount, HookCount: entry.HookCount,
			HooksTrusted: entry.HooksTrusted, HasApp: entry.HasApp,
			Capabilities: append([]string(nil), entry.Capabilities...), Status: entry.Status, Warning: entry.Warning,
			Imported: entry.Imported,
		}
	}
	return entries
}

func (s *Service) setCodexPluginImported(ctx context.Context, pluginID string, imported bool) error {
	pluginID = strings.TrimSpace(pluginID)
	index := slices.IndexFunc(s.pluginCatalog, func(entry PluginCatalogEntry) bool { return entry.ID == pluginID })
	if index < 0 {
		return fmt.Errorf("plugin %q not found", pluginID)
	}
	entry := s.pluginCatalog[index]
	if entry.Origin != "codex" && entry.Origin != "codex_available" {
		return fmt.Errorf("plugin %q is not a Codex import", pluginID)
	}
	imports := append([]string(nil), s.cfg.Plugins.CodexImports...)
	imports = slices.DeleteFunc(imports, func(candidate string) bool { return candidate == pluginID })
	if imported {
		imports = append(imports, pluginID)
	}
	slices.Sort(imports)
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateCodexPluginImports(s.configPath, imports)
		}); err != nil {
			return err
		}
	}
	s.cfg.Plugins.CodexImports = imports
	entry.Enabled = false
	entry.Imported = imported
	entry.Status = "restart_required"
	if imported {
		entry.Origin = "codex"
		entry.Warning = "已选择导入；重启 Azem 后复制并启用"
	} else {
		entry.Origin = "codex_available"
		entry.Warning = "已取消导入；重启 Azem 后停止加载"
	}
	s.pluginCatalog[index] = entry
	s.emit(ctx, Event{Kind: EventPluginCatalog, State: "updated", PluginCatalog: s.pluginCatalog, PluginDiagnostics: s.pluginDiagnostics})
	return nil
}

func pluginDiagnostics(integration plugins.Integration) []PluginDiagnostic {
	diagnostics := make([]PluginDiagnostic, len(integration.Diagnostics))
	for index, diagnostic := range integration.Diagnostics {
		diagnostics[index] = PluginDiagnostic{PluginID: diagnostic.PluginID, Path: diagnostic.Path, Message: diagnostic.Message}
	}
	return diagnostics
}
