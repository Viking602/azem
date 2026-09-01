package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/hooks"
)

var sessionActionHandlers = map[ActionKind]actionHandler{
	ActionNewSession: func(s *Service, ctx context.Context, action Action) error {
		return s.createSession(ctx, action.Target)
	},
	ActionResumeSession: func(s *Service, ctx context.Context, action Action) error {
		if err := s.sessions.SetUIState(ctx, action.Target, "unread", false); err != nil {
			return err
		}
		if err := s.sessions.SetArchived(ctx, action.Target, false); err != nil {
			return err
		}
		if err := s.emitSession(ctx, action.Target); err != nil {
			return err
		}
		return s.emitSessionList(ctx)
	},
	ActionRefreshSession: func(s *Service, ctx context.Context, action Action) error {
		_, err := s.emitSessionProjection(ctx, action.Target, "refreshed", false)
		return err
	},
	ActionListSessions: func(s *Service, ctx context.Context, action Action) error {
		return s.emitSessionList(ctx)
	},
	ActionRemoveProject: func(s *Service, ctx context.Context, action Action) error {
		if err := s.sessions.HideProject(ctx, action.Target); err != nil {
			return err
		}
		return s.emitSessionList(ctx)
	},
	ActionListUsage: func(s *Service, ctx context.Context, action Action) error {
		return s.emitUsageReport(ctx, action.Target, "listed")
	},
	ActionRenameSession: func(s *Service, ctx context.Context, action Action) error {
		if err := s.sessions.Rename(ctx, action.Target, action.Name); err != nil {
			return err
		}
		return s.emitSessionList(ctx)
	},
	ActionPinSession: func(s *Service, ctx context.Context, action Action) error {
		enabled, err := strconv.ParseBool(action.Decision)
		if err != nil {
			return fmt.Errorf("pin state must be true or false")
		}
		if err := s.sessions.SetUIState(ctx, action.Target, "pinned", enabled); err != nil {
			return err
		}
		return s.emitSessionList(ctx)
	},
	ActionArchiveSession: func(s *Service, ctx context.Context, action Action) error {
		enabled := true
		if strings.TrimSpace(action.Decision) != "" {
			parsed, err := strconv.ParseBool(action.Decision)
			if err != nil {
				return fmt.Errorf("archive state must be true or false")
			}
			enabled = parsed
		}
		if err := s.sessions.SetArchived(ctx, action.Target, enabled); err != nil {
			return err
		}
		return s.emitSessionList(ctx)
	},
	ActionArchiveInactiveSessions: func(s *Service, ctx context.Context, action Action) error {
		days, err := archiveInactiveDays(action.Target)
		if err != nil {
			return err
		}
		if _, err := s.sessions.ArchiveInactive(ctx, time.Duration(days)*24*time.Hour, action.SessionID); err != nil {
			return err
		}
		return s.emitSessionList(ctx)
	},
	ActionMarkSessionUnread: func(s *Service, ctx context.Context, action Action) error {
		return s.markSessionUnread(ctx, action.Target)
	},
	ActionCompact: func(s *Service, ctx context.Context, action Action) error {
		return s.executeManualCompaction(ctx, action.Target)
	},
}

func (s *Service) executeManualCompaction(ctx context.Context, sessionID string) error {
	if s.sessions == nil {
		return fmt.Errorf("session store is unavailable")
	}
	if !s.cfg.Agents.Context.Enabled {
		return ErrContextArchivingDisabled
	}
	const compactReservation = "maintenance:compact"
	s.mu.Lock()
	if s.shuttingDown {
		s.mu.Unlock()
		return fmt.Errorf("application is shutting down")
	}
	if s.activeRun != "" {
		s.mu.Unlock()
		return ErrRunActive
	}
	s.activeRun = compactReservation
	s.activeSession = sessionID
	s.mu.Unlock()
	defer s.clearRun(compactReservation)
	projection, err := s.sessions.LoadProjection(ctx, sessionID)
	if err != nil {
		return err
	}
	if !manualCompactionEligible(projection.Blocks) {
		return ErrNothingToCompact
	}
	if s.providers == nil {
		return fmt.Errorf("context maintenance runtime is unavailable")
	}
	if err := s.dispatchLifecycle(ctx, hooks.PreCompact, s.hookMetadata(sessionID, ""), func(e *hooks.Envelope) { e.Trigger = "manual" }); err != nil {
		return err
	}
	plan, changed, prepareErr := s.providers.PrepareManualCompaction(ctx, projection)
	if prepareErr != nil {
		return prepareErr
	}
	if !changed {
		return ErrNothingToCompact
	}
	projection, err = s.sessions.ActivateArchiveCheckpoint(ctx, sessionID, plan)
	if err != nil {
		return err
	}
	// Compaction shrinks the live context. Clear stale main occupancy while
	// preserving the independently attributed compaction and cache totals.
	cleared, err := s.clearMainUsageOccupancy(ctx, sessionID, projection.Usage)
	if err != nil {
		return err
	}
	projection.Usage = cleared
	blocks, err := json.Marshal(projection.Blocks)
	if err != nil {
		return err
	}
	todo, err := s.sessions.LoadTodo(ctx, sessionID)
	if err != nil {
		return err
	}
	currentRecap, err := s.loadRecap(ctx, sessionID)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{
		Kind: EventSessionLoaded, SessionID: sessionID, State: "compacted",
		Data: sessionProjectionData(projection, string(blocks)), AgentSnapshots: s.subagentSnapshots(ctx, sessionID), Todo: &todo, Recap: currentRecap,
	})
	_ = s.emitContextProfile(ctx, sessionID)
	_ = s.dispatchLifecycle(ctx, hooks.PostCompact, s.hookMetadata(sessionID, ""), func(e *hooks.Envelope) {
		e.Trigger = "manual"
		e.CompactSummary = fmt.Sprintf("Session compacted to %d persisted blocks.", len(projection.Blocks))
	})
	return nil
}
