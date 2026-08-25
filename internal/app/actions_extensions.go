package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var extensionActionHandlers = map[ActionKind]actionHandler{
	ActionListCustomCommands: func(s *Service, ctx context.Context, _ Action) error {
		return s.emitCommandCatalog(ctx, "listed")
	},
	ActionListThemes: func(s *Service, ctx context.Context, _ Action) error {
		return s.emitThemeCatalog(ctx, "listed")
	},
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
	ActionSetHookEnabled: func(s *Service, ctx context.Context, action Action) error {
		enabled, err := strconv.ParseBool(strings.TrimSpace(action.Decision))
		if err != nil {
			return fmt.Errorf("hook enabled state must be true or false")
		}
		return s.setHookEnabled(ctx, action.Target, enabled)
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
	ActionGetMCPPrompt: func(s *Service, ctx context.Context, action Action) error {
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		var request struct {
			Server    string            `json:"server"`
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments,omitempty"`
		}
		if err := json.Unmarshal(action.Payload, &request); err != nil {
			return fmt.Errorf("decode MCP prompt request: %w", err)
		}
		messages, err := s.mcp.GetPrompt(ctx, request.Server, request.Name, request.Arguments)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(messages)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventMCPState, State: "prompt", Data: map[string]string{
			"server": request.Server, "prompt": request.Name, "messages": string(encoded),
		}})
		return nil
	},
	ActionSubscribeMCPResource: func(s *Service, ctx context.Context, action Action) error {
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		return s.mcp.SubscribeResource(ctx, action.Target, action.Decision)
	},
	ActionUnsubscribeMCPResource: func(s *Service, ctx context.Context, action Action) error {
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		return s.mcp.UnsubscribeResource(ctx, action.Target, action.Decision)
	},
	ActionAuthenticateMCPServer: func(s *Service, ctx context.Context, action Action) error {
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		authErr := s.mcp.Authenticate(ctx, action.Target, openBrowserURL)
		return errors.Join(authErr, s.emitMCPSnapshot(ctx))
	},
	ActionUnauthenticateMCPServer: func(s *Service, ctx context.Context, action Action) error {
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		authErr := s.mcp.Unauthenticate(ctx, action.Target)
		return errors.Join(authErr, s.emitMCPSnapshot(ctx))
	},
	ActionMarketplaceAdd: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceRemove: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceUpdate: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceList: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceDiscover: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceInstall: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceUninstall: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceInstalled: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceUpgrade: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceEnable: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
	ActionMarketplaceDisable: func(s *Service, ctx context.Context, action Action) error {
		return s.executeMarketplaceAction(ctx, action)
	},
}
