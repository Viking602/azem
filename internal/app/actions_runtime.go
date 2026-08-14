package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
)

var runtimeActionHandlers = map[ActionKind]actionHandler{
	ActionSetApprovalMode: func(s *Service, ctx context.Context, action Action) error {
		return s.setApprovalMode(ctx, ApprovalMode(action.Target))
	},
	ActionSetLanguage: func(s *Service, ctx context.Context, action Action) error {
		if action.Target != "en" && action.Target != "zh-CN" {
			return fmt.Errorf("invalid language %q", action.Target)
		}
		return s.updateDefaultPreference(ctx, "language", action.Target, func(cfg *config.Config) {
			cfg.Defaults.Language = action.Target
		})
	},
	ActionSetQueueMode: func(s *Service, ctx context.Context, action Action) error {
		if action.Target != "queue" && action.Target != "guide" {
			return fmt.Errorf("invalid queue mode %q", action.Target)
		}
		return s.updateDefaultPreference(ctx, "queue_mode", action.Target, func(cfg *config.Config) {
			cfg.Defaults.QueueMode = action.Target
		})
	},
	ActionResolveApproval: func(s *Service, ctx context.Context, action Action) error {
		if s.coding == nil {
			return fmt.Errorf("coding runtime is unavailable")
		}
		if resolved, err := s.resolveLiveApproval(ctx, action.Target, action.Decision, "user"); resolved {
			return err
		}
		for index, pending := range s.recovery.Approvals {
			if pending.Approval.ApprovalID != action.Target {
				continue
			}
			if err := s.coding.ResolveRecoveredApproval(ctx, pending.Approval, pending.Token.TokenID, action.Decision); err != nil {
				return err
			}
			if s.providers != nil {
				if err := s.providers.ResumeRecoveredRun(ctx, pending.Approval.RunID); err != nil {
					return fmt.Errorf("resume approved run %s: %w", pending.Approval.RunID, err)
				}
			}
			s.recovery.Approvals = slices.Delete(s.recovery.Approvals, index, index+1)
			s.emit(ctx, Event{Kind: EventApprovalResolved, RunID: pending.Approval.RunID, State: action.Decision, Text: pending.Approval.ApprovalID})
			return nil
		}
		return fmt.Errorf("approval %q is not pending", action.Target)
	},
	ActionResolveUserInput: func(s *Service, ctx context.Context, action Action) error {
		return s.resolveUserInput(ctx, action.SessionID, action.Target, action.Payload)
	},
	ActionResolvePlan: func(s *Service, ctx context.Context, action Action) error {
		return s.resolvePlan(ctx, action.SessionID, action.Target, action.Decision)
	},
	ActionReconcileAttempt: func(s *Service, ctx context.Context, action Action) error {
		if s.reconciler == nil {
			return fmt.Errorf("action reconciliation is unavailable")
		}
		status, err := reconciledStatus(action.Decision)
		if err != nil {
			return err
		}
		attemptIndex := -1
		runID := ""
		for index, attempt := range s.recovery.ReconcileAttempts {
			if attempt.AttemptID == action.Target {
				attemptIndex = index
				runID = attempt.RunID
				break
			}
		}
		if runID == "" {
			return fmt.Errorf("reconciliation attempt %q is not pending", action.Target)
		}
		if err := s.reconciler.ResolveReconcileAttempt(ctx, action.Target, status, "user-confirmed"); err != nil {
			return err
		}
		if s.providers != nil {
			if err := s.providers.ResumeRecoveredRun(ctx, runID); err != nil {
				return fmt.Errorf("resume reconciled run %s: %w", runID, err)
			}
		}
		s.recovery.ReconcileAttempts = slices.Delete(s.recovery.ReconcileAttempts, attemptIndex, attemptIndex+1)
		s.emit(ctx, Event{Kind: EventRecoveryState, RunID: runID, State: "reconciled", Text: action.Target, Data: map[string]string{"decision": string(status)}})
		return nil
	},
	ActionLogin: func(s *Service, ctx context.Context, action Action) error {
		return s.login(ctx, action.Target)
	},
	ActionLogout: func(s *Service, ctx context.Context, action Action) error {
		if s.authentication == nil {
			return fmt.Errorf("authentication is unavailable")
		}
		if action.Target == "" {
			return fmt.Errorf("logout requires a provider or provider/account id")
		}
		provider, accountID, _ := strings.Cut(action.Target, "/")
		if accountID == "" {
			accounts, err := s.authentication.Accounts(ctx, provider)
			if err != nil {
				return err
			}
			for _, account := range accounts {
				if account.Status == "active" {
					accountID = account.ID
					break
				}
			}
		}
		if accountID == "" {
			return fmt.Errorf("no active %s account is available", provider)
		}
		if err := s.authentication.Logout(ctx, provider, accountID); err != nil {
			return err
		}
		return s.emitModelProviders(ctx, "auth_updated")
	},
}

// updateDefaultPreference persists one defaults.* configuration key through
// the hook-guarded config writer and mirrors it into the in-memory config.
func (s *Service) updateDefaultPreference(ctx context.Context, key, value string, apply func(*config.Config)) error {
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(s.currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateDefault(s.configPath, key, value)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	apply(&s.cfg)
	s.mu.Unlock()
	return nil
}
