package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Viking602/go-hydaelyn/api"
)

type RunRecoverer interface {
	Recover(context.Context, string) (api.Projection, error)
}

type StorePreparer interface {
	PrepareRecovery(context.Context, time.Time) (expiredLeases int64, quarantinedAttempts int64, err error)
	ListReconcileAttempts(context.Context) ([]api.ActionAttempt, error)
}

type SubagentInterrupter interface {
	InterruptIncomplete(context.Context, time.Time) (int64, error)
}

type TeamResumer interface {
	ResumeTeam(context.Context, string) error
}

type PendingApproval struct {
	Approval api.ApprovalRequest
	Token    api.ResumeToken
}

type RecoveredRun struct {
	Run        api.Run
	Projection api.Projection
	Team       bool
}

type Summary struct {
	ExpiredLeases        int64
	QuarantinedAttempts  int64
	InterruptedSubagents int64
	Runs                 []RecoveredRun
	Approvals            []PendingApproval
	ReconcileAttempts    []api.ActionAttempt
}

type Service struct {
	store     api.StoreProvider
	preparer  StorePreparer
	runner    RunRecoverer
	subagents SubagentInterrupter
	teams     TeamResumer
	now       func() time.Time
}

func NewService(store api.StoreProvider, runner RunRecoverer, subagents SubagentInterrupter, teams TeamResumer) (*Service, error) {
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
	return &Service{store: store, preparer: preparer, runner: runner, subagents: subagents, teams: teams, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Recover(ctx context.Context) (Summary, error) {
	now := s.now().UTC()
	expired, quarantined, err := s.preparer.PrepareRecovery(ctx, now)
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{ExpiredLeases: expired, QuarantinedAttempts: quarantined}
	if s.subagents != nil {
		interrupted, err := s.subagents.InterruptIncomplete(ctx, now)
		if err != nil {
			return Summary{}, fmt.Errorf("interrupt incomplete subagents: %w", err)
		}
		summary.InterruptedSubagents = interrupted
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
	runs, err := uow.Runs().ListRuns(ctx, api.RunSelector{})
	if err != nil {
		return Summary{}, fmt.Errorf("list recoverable runs: %w", err)
	}
	type runCandidate struct {
		run  api.Run
		team bool
	}
	candidates := make([]runCandidate, 0, len(runs))
	for _, run := range runs {
		if terminalRunStatus(run.Status) {
			continue
		}
		_, teamErr := uow.TeamStates().LoadTeamState(ctx, run.ID)
		team := teamErr == nil
		if teamErr != nil && !errors.Is(teamErr, api.ErrNotFound) {
			return Summary{}, fmt.Errorf("load team state for %s: %w", run.ID, teamErr)
		}
		candidates = append(candidates, runCandidate{run: run, team: team})
		tokens, err := uow.ResumeTokens().ListPending(ctx, api.ResumeTokenSelector{RunID: run.ID})
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

	for _, candidate := range candidates {
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
		}
		summary.Runs = append(summary.Runs, RecoveredRun{Run: candidate.run, Projection: projection, Team: candidate.team})
	}
	attempts, err := s.preparer.ListReconcileAttempts(ctx)
	if err != nil {
		return Summary{}, err
	}
	summary.ReconcileAttempts = attempts
	return summary, nil
}

func terminalRunStatus(status api.RunStatus) bool {
	switch status {
	case api.RunStatusCompleted, api.RunStatusFailed, api.RunStatusBlocked, api.RunStatusCancelled:
		return true
	default:
		return false
	}
}
