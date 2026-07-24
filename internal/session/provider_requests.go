package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
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
	err := dbgen.New(s.db).UpsertProviderRequest(ctx, dbgen.UpsertProviderRequestParams{RequestID: f.RequestID, ProviderRequestID: f.ProviderRequestID, SessionID: f.SessionID, RunID: f.RunID, RequestKind: f.RequestKind, Provider: f.Provider, Model: f.Model, Transport: f.Transport, CacheEpoch: f.CacheEpoch, CheckpointGeneration: f.CheckpointGeneration, InputTokens: int64(f.InputTokens), CachedTokens: int64(f.CachedTokens), CacheWriteTokens: int64(f.CacheWriteTokens), OutputTokens: int64(f.OutputTokens), ReasoningTokens: int64(f.ReasoningTokens), TotalTokens: int64(f.TotalTokens), CacheReported: requestBoolInt(f.CacheReported), Status: f.Status, StartedAt: f.StartedAt.UnixNano(), CompletedAt: completed})
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
	q := dbgen.New(s.db)
	var values [10]int64
	var err error
	switch clause {
	case `AND request_kind='main' AND cache_epoch=?`:
		row, e := q.AggregateMainUsageByEpoch(ctx, dbgen.AggregateMainUsageByEpochParams{SessionID: sessionID, CacheEpoch: args[0].(int64)})
		err = e
		values = aggregateValues(row.RawInput, row.ReportedInput, row.Cached, row.CacheWrite, row.Output, row.Reasoning, row.Total, row.Requests, row.ReportedRequests, row.Reported)
	case `AND request_kind='main' AND run_id=?`:
		row, e := q.AggregateMainUsageByRun(ctx, dbgen.AggregateMainUsageByRunParams{SessionID: sessionID, RunID: args[0].(string)})
		err = e
		values = aggregateValues(row.RawInput, row.ReportedInput, row.Cached, row.CacheWrite, row.Output, row.Reasoning, row.Total, row.Requests, row.ReportedRequests, row.Reported)
	case `AND request_kind='main'`:
		row, e := q.AggregateMainUsage(ctx, sessionID)
		err = e
		values = aggregateValues(row.RawInput, row.ReportedInput, row.Cached, row.CacheWrite, row.Output, row.Reasoning, row.Total, row.Requests, row.ReportedRequests, row.Reported)
	case `AND request_kind='compaction'`:
		row, e := q.AggregateCompactionUsage(ctx, sessionID)
		err = e
		values = aggregateValues(row.RawInput, row.ReportedInput, row.Cached, row.CacheWrite, row.Output, row.Reasoning, row.Total, row.Requests, row.ReportedRequests, row.Reported)
	case `AND request_kind='team'`:
		row, e := q.AggregateTeamUsage(ctx, sessionID)
		err = e
		values = aggregateValues(row.RawInput, row.ReportedInput, row.Cached, row.CacheWrite, row.Output, row.Reasoning, row.Total, row.Requests, row.ReportedRequests, row.Reported)
	case `AND request_kind='subagent'`:
		row, e := q.AggregateSubagentUsage(ctx, sessionID)
		err = e
		values = aggregateValues(row.RawInput, row.ReportedInput, row.Cached, row.CacheWrite, row.Output, row.Reasoning, row.Total, row.Requests, row.ReportedRequests, row.Reported)
	default:
		return usageAggregate{}, fmt.Errorf("unsupported usage aggregate %q", clause)
	}
	return usageAggregate{rawInput: int(values[0]), reportedInput: int(values[1]), cached: int(values[2]), write: int(values[3]), output: int(values[4]), reasoning: int(values[5]), total: int(values[6]), requests: int(values[7]), reportedRequests: int(values[8]), reported: values[9] != 0}, err
}

func aggregateValues(rawInput, reportedInput, cached, cacheWrite, output, reasoning, total, requests, reportedRequests, reported int64) [10]int64 {
	return [10]int64{rawInput, reportedInput, cached, cacheWrite, output, reasoning, total, requests, reportedRequests, reported}
}

func (s *Service) latestMainUsage(ctx context.Context, sessionID, runID string) (input, output int, err error) {
	row, err := dbgen.New(s.db).LatestMainUsage(ctx, dbgen.LatestMainUsageParams{SessionID: sessionID, RunID: runID})
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return int(row.InputTokens), int(row.OutputTokens), err
}

// ProviderRunTotalTokens returns cumulative provider-reported usage for every
// main and compaction request in one logical run.
func (s *Service) ProviderRunTotalTokens(ctx context.Context, sessionID, runID string) (int64, error) {
	return dbgen.New(s.db).ProviderRunTotalTokens(ctx, dbgen.ProviderRunTotalTokensParams{SessionID: sessionID, RunID: runID})
}

func (s *Service) ProviderRunHasUnknownRequest(ctx context.Context, sessionID, runID string) (bool, error) {
	count, err := dbgen.New(s.db).CountUnknownProviderRequests(ctx, dbgen.CountUnknownProviderRequestsParams{SessionID: sessionID, RunID: runID})
	return count > 0, err
}

func (s *Service) ProviderRunHasUncheckpointedCompletion(ctx context.Context, sessionID, runID string, checkpointGeneration int64) (bool, error) {
	count, err := dbgen.New(s.db).CountUncheckpointedCompletions(ctx, dbgen.CountUncheckpointedCompletionsParams{SessionID: sessionID, RunID: runID, CheckpointGeneration: checkpointGeneration})
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
	q := dbgen.New(tx)
	projection, err := q.GetCacheProjection(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}
	epoch, generation, current := projection.CacheEpoch, projection.CheckpointGeneration, projection.CacheIdentityHash
	if current == "" {
		if err := q.InitializeCacheIdentity(ctx, dbgen.InitializeCacheIdentityParams{CacheIdentityHash: identity, SessionID: sessionID}); err != nil {
			return 0, 0, err
		}
	} else if current != identity {
		epoch++
		if err := q.ChangeCacheIdentity(ctx, dbgen.ChangeCacheIdentityParams{CacheEpoch: epoch, CacheIdentityHash: identity, SessionID: sessionID}); err != nil {
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
	q := dbgen.New(s.db)
	result, err := q.AdvanceCacheEpochCAS(ctx, dbgen.AdvanceCacheEpochCASParams{CacheIdentityHash: identity, SessionID: sessionID, CacheEpoch: expectedEpoch})
	if err != nil {
		return 0, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	epoch, err := q.GetCacheEpoch(ctx, sessionID)
	if err != nil {
		return 0, false, err
	}
	return epoch, changed == 1, nil
}

func requestBoolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
