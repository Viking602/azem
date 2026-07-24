package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ProviderRequestFact is the replaceable, per-provider-request source of truth
// for usage. A request ID identifies one actual call to Driver.Stream.
type ProviderRequestFact struct {
	RequestID, ProviderRequestID, SessionID, RunID, RequestKind string
	Provider, Model, Transport                                  string
	CacheEpoch, CheckpointGeneration                            int64
	InputTokens, CachedTokens, CacheWriteTokens                 int
	OutputTokens, ReasoningTokens, TotalTokens                  int
	CacheReported                                               bool
	Status                                                      string
	StartedAt, CompletedAt                                      time.Time
}

// UpsertProviderRequest replaces a fact rather than adding counters. This
// makes repeated terminal delivery for the same request exactly idempotent.
func (s *Service) UpsertProviderRequest(ctx context.Context, f ProviderRequestFact) error {
	if strings.TrimSpace(f.RequestID) == "" || strings.TrimSpace(f.SessionID) == "" || strings.TrimSpace(f.RequestKind) == "" {
		return fmt.Errorf("provider request id, session, and kind are required")
	}
	if f.StartedAt.IsZero() {
		f.StartedAt = time.Now().UTC()
	}
	completed := int64(0)
	if !f.CompletedAt.IsZero() {
		completed = f.CompletedAt.UnixNano()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_requests(
		request_id,provider_request_id,session_id,run_id,request_kind,provider,model,transport,cache_epoch,checkpoint_generation,
		input_tokens,cached_tokens,cache_write_tokens,output_tokens,reasoning_tokens,total_tokens,cache_reported,status,started_at,completed_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(request_id) DO UPDATE SET
		provider_request_id=excluded.provider_request_id,session_id=excluded.session_id,run_id=excluded.run_id,request_kind=excluded.request_kind,
		provider=excluded.provider,model=excluded.model,transport=excluded.transport,cache_epoch=excluded.cache_epoch,
		checkpoint_generation=excluded.checkpoint_generation,input_tokens=excluded.input_tokens,cached_tokens=excluded.cached_tokens,
		cache_write_tokens=excluded.cache_write_tokens,output_tokens=excluded.output_tokens,reasoning_tokens=excluded.reasoning_tokens,
		total_tokens=excluded.total_tokens,cache_reported=excluded.cache_reported,status=excluded.status,
		started_at=MIN(provider_requests.started_at,excluded.started_at),completed_at=excluded.completed_at`,
		f.RequestID, f.ProviderRequestID, f.SessionID, f.RunID, f.RequestKind, f.Provider, f.Model, f.Transport,
		f.CacheEpoch, f.CheckpointGeneration, f.InputTokens, f.CachedTokens, f.CacheWriteTokens, f.OutputTokens,
		f.ReasoningTokens, f.TotalTokens, f.CacheReported, f.Status, f.StartedAt.UnixNano(), completed)
	if err != nil {
		return fmt.Errorf("upsert provider request: %w", err)
	}
	return nil
}

type usageAggregate struct {
	rawInput, reportedInput, cached, write, output, reasoning, total, requests, reportedRequests int
	reported                                                                                     bool
}

func (s *Service) aggregateUsage(ctx context.Context, sessionID, clause string, args ...any) (usageAggregate, error) {
	values := append([]any{sessionID}, args...)
	var a usageAggregate
	var reported int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(input_tokens),0),COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0),COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0),COALESCE(SUM(cache_write_tokens),0),
		COALESCE(SUM(output_tokens),0),COALESCE(SUM(reasoning_tokens),0),COALESCE(SUM(total_tokens),0),COUNT(*),COALESCE(SUM(cache_reported),0),COALESCE(MAX(cache_reported),0)
		FROM provider_requests WHERE session_id=? AND status='completed' `+clause, values...).Scan(&a.rawInput, &a.reportedInput, &a.cached, &a.write, &a.output, &a.reasoning, &a.total, &a.requests, &a.reportedRequests, &reported)
	a.reported = reported != 0
	return a, err
}

func (s *Service) latestMainUsage(ctx context.Context, sessionID, runID string) (input, output int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT input_tokens,output_tokens FROM provider_requests
		WHERE session_id=? AND run_id=? AND request_kind='main' AND status='completed'
		ORDER BY completed_at DESC,started_at DESC,request_id DESC LIMIT 1`, sessionID, runID).Scan(&input, &output)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return input, output, err
}

// ProviderRunTotalTokens returns cumulative provider-reported usage for every
// main and compaction request in one logical run.
func (s *Service) ProviderRunTotalTokens(ctx context.Context, sessionID, runID string) (int64, error) {
	var total int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(total_tokens),0) FROM provider_requests
		WHERE session_id=? AND run_id=? AND status='completed'`, sessionID, runID).Scan(&total)
	return total, err
}

func (s *Service) ProviderRunHasUnknownRequest(ctx context.Context, sessionID, runID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_requests
		WHERE session_id=? AND run_id=? AND status='unknown'`, sessionID, runID).Scan(&count)
	return count > 0, err
}

func (s *Service) ProviderRunHasUncheckpointedCompletion(ctx context.Context, sessionID, runID string, checkpointGeneration int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_requests
		WHERE session_id=? AND run_id=? AND status='completed' AND checkpoint_generation>=?`, sessionID, runID, checkpointGeneration).Scan(&count)
	return count > 0, err
}

// ProviderUsageSnapshot derives all cache KPIs from persisted facts.
func (s *Service) ProviderUsageSnapshot(ctx context.Context, sessionID, runID string) (Usage, error) {
	p, err := s.LoadProjection(ctx, sessionID)
	if err != nil {
		return Usage{}, err
	}
	epoch, err := s.aggregateUsage(ctx, sessionID, `AND request_kind='main' AND cache_epoch=?`, p.CacheEpoch)
	if err != nil {
		return Usage{}, err
	}
	turn, err := s.aggregateUsage(ctx, sessionID, `AND request_kind='main' AND run_id=?`, runID)
	if err != nil {
		return Usage{}, err
	}
	latestInput, latestOutput, err := s.latestMainUsage(ctx, sessionID, runID)
	if err != nil {
		return Usage{}, err
	}
	life, err := s.aggregateUsage(ctx, sessionID, `AND request_kind='main'`)
	if err != nil {
		return Usage{}, err
	}
	compact, err := s.aggregateUsage(ctx, sessionID, `AND request_kind='compaction'`)
	if err != nil {
		return Usage{}, err
	}
	team, err := s.aggregateUsage(ctx, sessionID, `AND request_kind='team'`)
	if err != nil {
		return Usage{}, err
	}
	sub, err := s.aggregateUsage(ctx, sessionID, `AND request_kind='subagent'`)
	if err != nil {
		return Usage{}, err
	}
	u := p.Usage
	u.CurrentCacheEpoch = p.CacheEpoch
	u.CurrentEpochMainInput, u.CurrentEpochMainReportedInput, u.CurrentEpochMainCached, u.CurrentEpochMainCacheWrite = epoch.rawInput, epoch.reportedInput, epoch.cached, epoch.write
	u.CurrentEpochMainReported, u.CurrentEpochMainRequests = epoch.reported, epoch.requests
	u.CurrentEpochMainReportedRequests = epoch.reportedRequests
	u.CurrentTurnMainInput, u.CurrentTurnMainReportedInput, u.CurrentTurnMainCached, u.CurrentTurnMainOutput, u.CurrentTurnMainRequests = turn.rawInput, turn.reportedInput, turn.cached, turn.output, turn.requests
	u.CurrentTurnMainReported = turn.reported
	u.LifetimeMainInput, u.LifetimeMainReportedInput, u.LifetimeMainCached, u.LifetimeMainOutput, u.LifetimeMainRequests = life.rawInput, life.reportedInput, life.cached, life.output, life.requests
	u.LifetimeMainReported = life.reported
	u.CompactionInput, u.CompactionReportedInput, u.CompactionCached, u.CompactionCacheWrite, u.CompactionOutput, u.CompactionReasoning, u.CompactionRequests, u.CompactionReportedRequests = compact.rawInput, compact.reportedInput, compact.cached, compact.write, compact.output, compact.reasoning, compact.requests, compact.reportedRequests
	u.CompactionCacheReported = compact.reported
	u.TeamInput, u.TeamReportedInput, u.TeamCached, u.TeamCacheWrite, u.TeamOutput, u.TeamReasoning, u.TeamRequests, u.TeamReportedRequests = team.rawInput, team.reportedInput, team.cached, team.write, team.output, team.reasoning, team.requests, team.reportedRequests
	u.TeamCacheReported = team.reported
	u.SubagentInput, u.SubagentReportedInput, u.SubagentCached, u.SubagentCacheWrite, u.SubagentOutput, u.SubagentReasoning, u.SubagentRequests, u.SubagentReportedRequests = sub.rawInput, sub.reportedInput, sub.cached, sub.write, sub.output, sub.reasoning, sub.requests, sub.reportedRequests
	u.SubagentCacheReported = sub.reported
	// Existing presentation fields now reflect the isolated current epoch.
	// Context occupancy is the latest request's full context, not the sum of
	// overlapping contexts sent by every model call in the agent loop.
	u.InputTokens, u.OutputTokens = latestInput, latestOutput
	u.MainCacheInput, u.MainCachedInput, u.MainCacheWrite, u.MainCacheReported = epoch.reportedInput, epoch.cached, epoch.write, epoch.reported
	u.CacheInputTokens, u.CachedInputTokens, u.CacheWriteTokens, u.CacheReported = epoch.reportedInput, epoch.cached, epoch.write, epoch.reported
	return u, nil
}

// EnsureCacheIdentity increments the epoch exactly once when main static wire
// identity changes. Concurrent callers observing the same hash converge.
func (s *Service) EnsureCacheIdentity(ctx context.Context, sessionID, identity string) (int64, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	var epoch, generation int64
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT cache_epoch,checkpoint_generation,cache_identity_hash FROM session_projections WHERE session_id=?`, sessionID).Scan(&epoch, &generation, &current); err != nil {
		return 0, 0, err
	}
	if current == "" {
		if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET cache_identity_hash=? WHERE session_id=?`, identity, sessionID); err != nil {
			return 0, 0, err
		}
	} else if current != identity {
		epoch++
		if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET cache_epoch=?,cache_identity_hash=? WHERE session_id=?`, epoch, identity, sessionID); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return epoch, generation, nil
}

// AdvanceCacheEpoch invalidates the active provider prefix with a CAS. The
// expected epoch makes activation idempotent when the same prepared generation
// is observed more than once.
func (s *Service) AdvanceCacheEpoch(ctx context.Context, sessionID string, expectedEpoch int64, identity string) (int64, bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE session_projections SET cache_epoch=cache_epoch+1,cache_identity_hash=? WHERE session_id=? AND cache_epoch=?`, identity, sessionID, expectedEpoch)
	if err != nil {
		return 0, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	var epoch int64
	if err := s.db.QueryRowContext(ctx, `SELECT cache_epoch FROM session_projections WHERE session_id=?`, sessionID).Scan(&epoch); err != nil {
		return 0, false, err
	}
	return epoch, changed == 1, nil
}
