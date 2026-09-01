package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
)

type RunRecoverer interface {
	Recover(context.Context, string) (agentruntime.Projection, error)
}

type StorePreparer interface {
	PrepareRecovery(context.Context, time.Time) (expiredLeases int64, quarantinedAttempts int64, err error)
	ListReconcileAttempts(context.Context) ([]agentruntime.ActionAttempt, error)
}

type SubagentInterrupter interface {
	InterruptIncomplete(context.Context, time.Time) (int64, error)
}

type TeamResumer interface {
	ResumeTeam(context.Context, string) error
}

type RunResumer interface {
	ResumeRun(context.Context, string) error
}
type RunRecoveryClassifier interface {
	ClassifyRunRecovery(context.Context, string) (kind string, replay bool, err error)
}

type PendingApproval struct {
	Approval agentruntime.ApprovalRequest
	Token    agentruntime.ResumeToken
}

type RecoveredRun struct {
	Run        agentruntime.Run
	Projection agentruntime.Projection
	Team       bool
}

type Summary struct {
	ExpiredLeases        int64
	QuarantinedAttempts  int64
	InterruptedSubagents int64
	Runs                 []RecoveredRun
	Approvals            []PendingApproval
	ReconcileAttempts    []agentruntime.ActionAttempt
}

// Preparation records the exclusive crash-boundary cleanup completed before
// the durable execution runtime was constructed.
type Preparation struct {
	At                  time.Time
	ExpiredLeases       int64
	QuarantinedAttempts int64
}

type Service struct {
	store        agentruntime.StoreProvider
	preparer     StorePreparer
	runner       RunRecoverer
	subagents    SubagentInterrupter
	teams        TeamResumer
	runs         RunResumer
	now          func() time.Time
	beforeResume func(context.Context, []agentruntime.Run) error
}

func NewService(store agentruntime.StoreProvider, runner RunRecoverer, subagents SubagentInterrupter, teams TeamResumer, runs RunResumer) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("recovery store is nil")
	}
	if runner == nil {
		return nil, fmt.Errorf("recovery runner is nil")
	}
	preparer, ok := store.(StorePreparer)
	if !ok {
		return nil, fmt.Errorf("recovery store does not support crash-boundary preparation")
	}
	return &Service{store: store, preparer: preparer, runner: runner, subagents: subagents, teams: teams, runs: runs, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) SetBeforeResume(fn func(context.Context, []agentruntime.Run) error) {
	s.beforeResume = fn
}

func (s *Service) Recover(ctx context.Context) (Summary, error) {
	now := s.now().UTC()
	expired, quarantined, err := s.preparer.PrepareRecovery(ctx, now)
	if err != nil {
		return Summary{}, err
	}
	return s.RecoverPrepared(ctx, Preparation{
		At: now, ExpiredLeases: expired, QuarantinedAttempts: quarantined,
	})
}

// RecoverPrepared resumes application projections after the exclusive startup
// boundary has already quarantined legacy in-flight effects.
func (s *Service) RecoverPrepared(ctx context.Context, prepared Preparation) (Summary, error) {
	now := prepared.At.UTC()
	if now.IsZero() {
		return Summary{}, fmt.Errorf("recovery preparation time is required")
	}
	summary := Summary{
		ExpiredLeases:       prepared.ExpiredLeases,
		QuarantinedAttempts: prepared.QuarantinedAttempts,
	}

	uow, err := s.store.Begin(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("begin recovery scan: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = uow.Rollback(context.WithoutCancel(ctx))
		}
	}()
	runs, err := uow.Runs().ListRuns(ctx, agentruntime.RunSelector{Statuses: []agentruntime.RunStatus{
		agentruntime.RunStatusCreated,
		agentruntime.RunStatusPlanning,
		agentruntime.RunStatusValidating,
		agentruntime.RunStatusRouting,
		agentruntime.RunStatusDispatching,
		agentruntime.RunStatusRunning,
		agentruntime.RunStatusWaitingUserInput,
		agentruntime.RunStatusWaitingApproval,
		agentruntime.RunStatusExecuting,
		agentruntime.RunStatusReconcileRequired,
		agentruntime.RunStatusComposingResponse,
	}})
	if err != nil {
		return Summary{}, fmt.Errorf("list recoverable runs: %w", err)
	}
	type runCandidate struct {
		run        agentruntime.Run
		team       bool
		replay     bool
		kind       string
		classified bool
	}
	candidates := make([]runCandidate, 0, len(runs))
	for _, run := range runs {
		if strings.HasPrefix(run.Metadata["automation_kind"], "security_") {
			continue
		}
		if terminalRunStatus(run.Status) {
			continue
		}
		_, teamErr := uow.TeamStates().LoadTeamState(ctx, run.ID)
		team := teamErr == nil
		if teamErr != nil && !errors.Is(teamErr, agentruntime.ErrNotFound) {
			return Summary{}, fmt.Errorf("load team state for %s: %w", run.ID, teamErr)
		}
		kind := "main"
		if team {
			kind = "team"
		}
		candidates = append(candidates, runCandidate{
			run: run, team: team, replay: run.Status != agentruntime.RunStatusReconcileRequired, kind: kind,
		})
		tokens, err := uow.ResumeTokens().ListPending(ctx, agentruntime.ResumeTokenSelector{RunID: run.ID})
		if err != nil {
			return Summary{}, fmt.Errorf("list pending resume tokens for %s: %w", run.ID, err)
		}
		for _, token := range tokens {
			if token.ApprovalID == "" {
				continue
			}
			approval, err := uow.Approvals().LoadApproval(ctx, token.ApprovalID)
			if err != nil {
				return Summary{}, fmt.Errorf("load pending approval %s: %w", token.ApprovalID, err)
			}
			if approval.Status == "pending" && (approval.ExpiresAt.IsZero() || approval.ExpiresAt.After(now)) {
				summary.Approvals = append(summary.Approvals, PendingApproval{Approval: approval, Token: token})
			}
		}
	}
	if err := uow.Commit(ctx); err != nil {
		return Summary{}, fmt.Errorf("commit recovery scan: %w", err)
	}
	closed = true

	if classifier, ok := s.runner.(RunRecoveryClassifier); ok {
		for index := range candidates {
			candidate := &candidates[index]
			if candidate.team || !candidate.replay {
				continue
			}
			kind, replay, err := classifier.ClassifyRunRecovery(ctx, candidate.run.ID)
			if err != nil {
				return Summary{}, fmt.Errorf("classify run %s recovery: %w", candidate.run.ID, err)
			}
			candidate.kind = kind
			candidate.replay = replay
			candidate.classified = true
		}
	}
	if len(summary.Approvals) > 0 {
		replayable := make(map[string]bool, len(candidates))
		for _, candidate := range candidates {
			replayable[candidate.run.ID] = candidate.replay
		}
		approvals := summary.Approvals[:0]
		for _, pending := range summary.Approvals {
			if replayable[pending.Approval.RunID] {
				approvals = append(approvals, pending)
			}
		}
		summary.Approvals = approvals
	}
	if s.subagents != nil {
		interrupted, err := s.subagents.InterruptIncomplete(ctx, now)
		if err != nil {
			return Summary{}, fmt.Errorf("interrupt incomplete subagents: %w", err)
		}
		summary.InterruptedSubagents = interrupted
	}

	if s.beforeResume != nil {
		discovered := make([]agentruntime.Run, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.replay {
				discovered = append(discovered, candidate.run)
			}
		}
		if err := s.beforeResume(ctx, discovered); err != nil {
			return Summary{}, fmt.Errorf("prepare recovered run presentation: %w", err)
		}
	}

	for pass := 0; pass < 2; pass++ {
		for _, candidate := range candidates {
			isSubagent := candidate.kind == "subagent"
			if (pass == 0 && isSubagent) || (pass == 1 && !isSubagent) {
				continue
			}
			if !candidate.replay {
				projection := agentruntime.Projection{Run: candidate.run}
				if candidate.classified {
					var err error
					projection, err = s.runner.Recover(ctx, candidate.run.ID)
					if err != nil {
						return Summary{}, fmt.Errorf("recover reconciled run %s: %w", candidate.run.ID, err)
					}
				}
				summary.Runs = append(summary.Runs, RecoveredRun{Run: projection.Run, Projection: projection, Team: candidate.team})
				continue
			}
			projection, err := s.runner.Recover(ctx, candidate.run.ID)
			if err != nil {
				return Summary{}, fmt.Errorf("recover run %s: %w", candidate.run.ID, err)
			}
			if candidate.team {
				if s.teams == nil {
					return Summary{}, fmt.Errorf("recover team run %s: team resumer is unavailable", candidate.run.ID)
				}
				if err := s.teams.ResumeTeam(ctx, candidate.run.ID); err != nil {
					return Summary{}, fmt.Errorf("resume team run %s: %w", candidate.run.ID, err)
				}
			} else if !isSubagent && s.runs != nil && projection.Run.Status != agentruntime.RunStatusReconcileRequired {
				if err := s.runs.ResumeRun(ctx, candidate.run.ID); err != nil {
					return Summary{}, fmt.Errorf("resume run %s: %w", candidate.run.ID, err)
				}
			}
			summary.Runs = append(summary.Runs, RecoveredRun{Run: projection.Run, Projection: projection, Team: candidate.team})
		}
	}
	attempts, err := s.preparer.ListReconcileAttempts(ctx)
	if err != nil {
		return Summary{}, err
	}
	summary.ReconcileAttempts = attempts
	return summary, nil
}

func terminalRunStatus(status agentruntime.RunStatus) bool {
	switch status {
	case agentruntime.RunStatusCompleted, agentruntime.RunStatusFailed, agentruntime.RunStatusBlocked, agentruntime.RunStatusCancelled:
		return true
	default:
		return false
	}
}
