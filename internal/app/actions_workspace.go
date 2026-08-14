package app

import (
	"context"
	"fmt"

	backgroundservice "github.com/Viking602/azem/internal/background"
)

var workspaceActionHandlers = map[ActionKind]actionHandler{
	ActionListGitBranches: func(s *Service, ctx context.Context, action Action) error {
		return s.emitGitBranches(ctx, "listed")
	},
	ActionSwitchGitBranch: func(s *Service, ctx context.Context, action Action) error {
		return s.switchGitBranch(ctx, action.Target, action.Decision == "confirm_dirty")
	},
	ActionCreateGitBranch: func(s *Service, ctx context.Context, action Action) error {
		return s.createGitBranch(ctx, action.Target)
	},
	ActionListBackground: func(s *Service, ctx context.Context, action Action) error {
		return s.emitBackgroundSnapshot(ctx, "listed")
	},
	ActionStartBackground: func(s *Service, ctx context.Context, action Action) error {
		if s.background == nil {
			return fmt.Errorf("background runtime is unavailable")
		}
		if s.cfg.Workspace.ShellPolicy == "deny" {
			return fmt.Errorf("background commands are disabled by workspace.shell_policy")
		}
		_, err := s.background.Start(ctx, backgroundservice.StartRequest{Name: action.Name, Command: action.Target, CWD: action.CWD})
		if err != nil {
			return err
		}
		return s.emitBackgroundSnapshot(ctx, "started")
	},
	ActionStopBackground: func(s *Service, ctx context.Context, action Action) error {
		if s.background == nil {
			return fmt.Errorf("background runtime is unavailable")
		}
		if err := s.background.Stop(ctx, action.Target); err != nil {
			return err
		}
		return s.emitBackgroundSnapshot(ctx, "stopped")
	},
	ActionLogsBackground: func(s *Service, ctx context.Context, action Action) error {
		if s.background == nil {
			return fmt.Errorf("background runtime is unavailable")
		}
		snapshot, err := s.background.Logs(action.Target, action.Offset, action.Limit)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventBackgroundLogs, State: "loaded", BackgroundLogs: &snapshot})
		return nil
	},
}
