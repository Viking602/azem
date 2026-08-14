package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var extensionActionHandlers = map[ActionKind]actionHandler{
	ActionListSkills: func(s *Service, ctx context.Context, action Action) error {
		return s.emitSkillCatalog(ctx, "listed")
	},
	ActionListPlugins: func(s *Service, ctx context.Context, action Action) error {
		s.emit(ctx, Event{Kind: EventPluginCatalog, State: "listed", PluginCatalog: s.pluginCatalog, PluginDiagnostics: s.pluginDiagnostics})
		return nil
	},
	ActionListHooks: func(s *Service, ctx context.Context, action Action) error {
		return s.emitHookCatalog(ctx, "listed")
	},
	ActionSetPluginHooksTrusted: func(s *Service, ctx context.Context, action Action) error {
		trusted, err := strconv.ParseBool(strings.TrimSpace(action.Decision))
		if err != nil {
			return fmt.Errorf("plugin hook trust decision must be true or false")
		}
		return s.setPluginHooksTrusted(ctx, trusted)
	},
	ActionSetPluginImported: func(s *Service, ctx context.Context, action Action) error {
		imported, err := strconv.ParseBool(strings.TrimSpace(action.Decision))
		if err != nil {
			return fmt.Errorf("plugin imported decision must be true or false: %w", err)
		}
		return s.setCodexPluginImported(ctx, action.Target, imported)
	},
	ActionReloadSkills: func(s *Service, ctx context.Context, action Action) error {
		if s.skillCatalog == nil {
			return fmt.Errorf("skills are unavailable")
		}
		if err := s.skillCatalog.Reload(); err != nil {
			return err
		}
		if err := s.emitSkillCatalog(ctx, "reloaded"); err != nil {
			return err
		}
		_ = s.emitContextProfile(ctx, action.SessionID)
		return nil
	},
	ActionSetSkillEnabled: func(s *Service, ctx context.Context, action Action) error {
		enabled, err := strconv.ParseBool(strings.TrimSpace(action.Decision))
		if err != nil {
			return fmt.Errorf("skill enabled state must be true or false")
		}
		return s.setSkillEnabled(ctx, action.Target, enabled, action.SessionID)
	},
	ActionRefreshMCP: func(s *Service, ctx context.Context, action Action) error {
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		refreshErr := s.mcp.Refresh(ctx, action.Target)
		snapshotErr := s.emitMCPSnapshot(ctx)
		return errors.Join(refreshErr, snapshotErr)
	},
	ActionReconnectMCP: func(s *Service, ctx context.Context, action Action) error {
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		reconnectErr := s.mcp.Reconnect(ctx, action.Target)
		snapshotErr := s.emitMCPSnapshot(ctx)
		return errors.Join(reconnectErr, snapshotErr)
	},
	ActionSetMCPEnabled: func(s *Service, ctx context.Context, action Action) error {
		enabled, err := strconv.ParseBool(action.Decision)
		if err != nil {
			return fmt.Errorf("invalid MCP enabled state %q", action.Decision)
		}
		return s.setMCPServerEnabled(ctx, action.Target, enabled)
	},
	ActionUpsertMCPServer: func(s *Service, ctx context.Context, action Action) error {
		return s.upsertMCPServer(ctx, action.Payload)
	},
	ActionDeleteMCPServer: func(s *Service, ctx context.Context, action Action) error {
		return s.deleteMCPServer(ctx, action.Target)
	},
}
