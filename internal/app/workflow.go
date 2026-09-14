package app

import (
	"context"
	"fmt"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
)

// WorkflowMode is the configured strategy for new desktop turns, independent of
// the selected session's last run. Running and queued turns retain their mode.
func (s *Service) WorkflowMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Agents.Workflow
}

func (s *Service) setWorkflowMode(ctx context.Context, mode string) error {
	if mode != "vibe" && mode != "fusion" {
		return fmt.Errorf("workflow mode must be vibe or fusion")
	}
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	if mode == "fusion" {
		if err := s.validateFusion(TurnRequest{}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	currentSession := s.currentSession
	enabled := s.cfg.Agents.Subagents.Enabled && s.cfg.Agents.Subagents.MaxDepth != 0
	s.mu.Unlock()
	if !enabled {
		return fmt.Errorf("workflow mode requires subagents to be enabled")
	}
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateWorkflowMode(s.configPath, mode)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Agents.Workflow = mode
	s.mu.Unlock()
	s.emit(ctx, s.modelRoutesEvent("workflow_updated"))
	return nil
}
