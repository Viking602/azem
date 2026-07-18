package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Viking602/go-hydaelyn/api"
)

type SubagentState string

const (
	SubagentInitializing SubagentState = "initializing"
	SubagentQueued       SubagentState = "queued"
	SubagentRunning      SubagentState = "running"
	SubagentCancelling   SubagentState = "cancelling"
	SubagentCompleted    SubagentState = "completed"
	SubagentFailed       SubagentState = "failed"
	SubagentCancelled    SubagentState = "cancelled"
	SubagentInterrupted  SubagentState = "interrupted"
)

type SubagentRun struct {
	ID                  string
	SessionID           string
	ParentRunID         string
	ParentAgentID       string
	ParentToolCallID    string
	ChildRunID          string
	Description         string
	Type                string
	State               SubagentState
	Summary             string
	Model               string
	Reasoning           string
	CapabilityMode      string
	RequestedIsolation  string
	Isolation           string
	CWD                 string
	Background          bool
	Output              string
	Error               string
	Warning             string
	Transcript          json.RawMessage
	ToolCalls           int
	Turns               int
	TokensUsed          int
	ToolsUsed           []string
	WorktreePath        string
	CompletionDelivered bool
	StartedAt           time.Time
	FinishedAt          time.Time
}

type SubagentSnapshot struct {
	Run     SubagentRun
	Elapsed time.Duration
	Found   bool
}

type SubagentCancelOutcome struct {
	Outcome  string
	Snapshot SubagentSnapshot
}

type SubagentRunStore interface {
	Create(context.Context, SubagentRun) error
	Save(context.Context, SubagentRun) error
	Get(context.Context, string) (SubagentRun, error)
	List(context.Context, string) ([]SubagentRun, error)
	SetCompletionDelivered(context.Context, string, bool) error
	InterruptIncomplete(context.Context, time.Time) (int64, error)
}

type SQLSubagentRunStore struct {
	db *sql.DB
}

const subagentRunColumns = `id, session_id, parent_run_id, parent_agent_id, tool_call_id, child_run_id,
	description, subagent_type, state, summary, model, reasoning, capability_mode,
	requested_isolation, isolation, cwd, background, output, error, warning, transcript,
	tool_calls, turns, tokens_used, tools_used, worktree_path, completion_delivered,
	started_at, finished_at`

func NewSQLSubagentRunStore(db *sql.DB) (*SQLSubagentRunStore, error) {
	if db == nil {
		return nil, fmt.Errorf("subagent store: database is nil")
	}
	return &SQLSubagentRunStore{db: db}, nil
}

func (s *SQLSubagentRunStore) Create(ctx context.Context, run SubagentRun) error {
	if run.ToolsUsed == nil {
		run.ToolsUsed = []string{}
	}
	toolsUsed, err := json.Marshal(run.ToolsUsed)
	if err != nil {
		return fmt.Errorf("encode subagent tools: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO subagent_runs(`+subagentRunColumns+`) VALUES(
		?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
	)`, subagentRunValues(run, toolsUsed)...)
	return err
}

func (s *SQLSubagentRunStore) Save(ctx context.Context, run SubagentRun) error {
	if run.ToolsUsed == nil {
		run.ToolsUsed = []string{}
	}
	toolsUsed, err := json.Marshal(run.ToolsUsed)
	if err != nil {
		return fmt.Errorf("encode subagent tools: %w", err)
	}
	values := subagentRunValues(run, toolsUsed)
	result, err := s.db.ExecContext(ctx, `UPDATE subagent_runs SET
		session_id=?, parent_run_id=?, parent_agent_id=?, tool_call_id=?, child_run_id=?,
		description=?, subagent_type=?, state=?, summary=?, model=?, reasoning=?, capability_mode=?,
		requested_isolation=?, isolation=?, cwd=?, background=?, output=?, error=?, warning=?, transcript=?,
		tool_calls=?, turns=?, tokens_used=?, tools_used=?, worktree_path=?, completion_delivered=?,
		started_at=?, finished_at=? WHERE id=?`, append(values[1:], values[0])...)
	if err != nil {
		return err
	}
	return requireOneSubagentRow(result)
}

func (s *SQLSubagentRunStore) Get(ctx context.Context, id string) (SubagentRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+subagentRunColumns+` FROM subagent_runs WHERE id=?`, id)
	run, err := scanSubagentRun(row.Scan)
	if err == sql.ErrNoRows {
		return SubagentRun{}, api.ErrNotFound
	}
	return run, err
}

func (s *SQLSubagentRunStore) List(ctx context.Context, sessionID string) ([]SubagentRun, error) {
	query := `SELECT ` + subagentRunColumns + ` FROM subagent_runs`
	args := []any{}
	if sessionID != "" {
		query += ` WHERE session_id=?`
		args = append(args, sessionID)
	}
	query += ` ORDER BY started_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]SubagentRun, 0)
	for rows.Next() {
		run, scanErr := scanSubagentRun(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *SQLSubagentRunStore) SetCompletionDelivered(ctx context.Context, id string, delivered bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE subagent_runs SET completion_delivered=? WHERE id=?`, boolInt(delivered), id)
	if err != nil {
		return err
	}
	return requireOneSubagentRow(result)
}

func (s *SQLSubagentRunStore) InterruptIncomplete(ctx context.Context, at time.Time) (int64, error) {
	const reason = "interrupted by process restart"
	result, err := s.db.ExecContext(ctx, `UPDATE subagent_runs
		SET state=?, summary=?, error=?, finished_at=?
		WHERE state IN (?, ?, ?, ?)`,
		SubagentInterrupted, reason, reason, unixNano(at),
		SubagentInitializing, SubagentQueued, SubagentRunning, SubagentCancelling)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func subagentRunValues(run SubagentRun, toolsUsed []byte) []any {
	transcript := []byte(run.Transcript)
	if len(transcript) == 0 {
		transcript = []byte("[]")
	}
	return []any{
		run.ID, run.SessionID, run.ParentRunID, run.ParentAgentID, run.ParentToolCallID, run.ChildRunID,
		run.Description, run.Type, run.State, run.Summary, run.Model, run.Reasoning, run.CapabilityMode,
		run.RequestedIsolation, run.Isolation, run.CWD, boolInt(run.Background), run.Output, run.Error,
		run.Warning, transcript, run.ToolCalls, run.Turns, run.TokensUsed, toolsUsed,
		run.WorktreePath, boolInt(run.CompletionDelivered), unixNano(run.StartedAt), unixNano(run.FinishedAt),
	}
}

type subagentScanner func(...any) error

func scanSubagentRun(scan subagentScanner) (SubagentRun, error) {
	var run SubagentRun
	var transcript, toolsUsed []byte
	var background, delivered int64
	var started, finished int64
	err := scan(
		&run.ID, &run.SessionID, &run.ParentRunID, &run.ParentAgentID, &run.ParentToolCallID, &run.ChildRunID,
		&run.Description, &run.Type, &run.State, &run.Summary, &run.Model, &run.Reasoning, &run.CapabilityMode,
		&run.RequestedIsolation, &run.Isolation, &run.CWD, &background, &run.Output, &run.Error, &run.Warning,
		&transcript, &run.ToolCalls, &run.Turns, &run.TokensUsed, &toolsUsed, &run.WorktreePath, &delivered,
		&started, &finished,
	)
	if err != nil {
		return SubagentRun{}, err
	}
	if len(toolsUsed) > 0 {
		if err := json.Unmarshal(toolsUsed, &run.ToolsUsed); err != nil {
			return SubagentRun{}, fmt.Errorf("decode subagent tools for %s: %w", run.ID, err)
		}
	}
	run.Transcript = append(json.RawMessage(nil), transcript...)
	run.Background = background != 0
	run.CompletionDelivered = delivered != 0
	run.StartedAt = timeFromUnixNano(started)
	run.FinishedAt = timeFromUnixNano(finished)
	return run, nil
}

func requireOneSubagentRow(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return api.ErrNotFound
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func unixNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func timeFromUnixNano(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}
