package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
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
	Provider            string
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
	return dbgen.New(s.db).CreateSubagentRun(ctx, createSubagentParams(run, toolsUsed))
}

func (s *SQLSubagentRunStore) Save(ctx context.Context, run SubagentRun) error {
	if run.ToolsUsed == nil {
		run.ToolsUsed = []string{}
	}
	toolsUsed, err := json.Marshal(run.ToolsUsed)
	if err != nil {
		return fmt.Errorf("encode subagent tools: %w", err)
	}
	result, err := dbgen.New(s.db).SaveSubagentRun(ctx, saveSubagentParams(run, toolsUsed))
	if err != nil {
		return err
	}
	return requireOneSubagentRow(result)
}

func (s *SQLSubagentRunStore) Get(ctx context.Context, id string) (SubagentRun, error) {
	row, err := dbgen.New(s.db).GetSubagentRun(ctx, id)
	if err == sql.ErrNoRows {
		return SubagentRun{}, api.ErrNotFound
	}
	if err != nil {
		return SubagentRun{}, err
	}
	return subagentRunFromDB(row)
}

func (s *SQLSubagentRunStore) List(ctx context.Context, sessionID string) ([]SubagentRun, error) {
	q := dbgen.New(s.db)
	var rows []dbgen.SubagentRun
	var err error
	if sessionID != "" {
		rows, err = q.ListSubagentRunsBySession(ctx, sessionID)
	} else {
		rows, err = q.ListSubagentRuns(ctx)
	}
	if err != nil {
		return nil, err
	}
	runs := make([]SubagentRun, 0, len(rows))
	for _, row := range rows {
		run, scanErr := subagentRunFromDB(row)
		if scanErr != nil {
			return nil, scanErr
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *SQLSubagentRunStore) SetCompletionDelivered(ctx context.Context, id string, delivered bool) error {
	result, err := dbgen.New(s.db).SetSubagentCompletionDelivered(ctx, dbgen.SetSubagentCompletionDeliveredParams{CompletionDelivered: int64(boolInt(delivered)), ID: id})
	if err != nil {
		return err
	}
	return requireOneSubagentRow(result)
}

func (s *SQLSubagentRunStore) InterruptIncomplete(ctx context.Context, at time.Time) (int64, error) {
	result, err := dbgen.New(s.db).InterruptIncompleteSubagents(ctx, unixNano(at))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func createSubagentParams(run SubagentRun, toolsUsed []byte) dbgen.CreateSubagentRunParams {
	transcript := []byte(run.Transcript)
	if len(transcript) == 0 {
		transcript = []byte("[]")
	}
	return dbgen.CreateSubagentRunParams{ID: run.ID, SessionID: run.SessionID, ParentRunID: run.ParentRunID, ParentAgentID: run.ParentAgentID, ToolCallID: run.ParentToolCallID, ChildRunID: run.ChildRunID, Description: run.Description, SubagentType: run.Type, State: string(run.State), Summary: run.Summary, Provider: run.Provider, Model: run.Model, Reasoning: run.Reasoning, CapabilityMode: run.CapabilityMode, RequestedIsolation: run.RequestedIsolation, Isolation: run.Isolation, Cwd: run.CWD, Background: int64(boolInt(run.Background)), Output: run.Output, Error: run.Error, Warning: run.Warning, Transcript: transcript, ToolCalls: int64(run.ToolCalls), Turns: int64(run.Turns), TokensUsed: int64(run.TokensUsed), ToolsUsed: toolsUsed, WorktreePath: run.WorktreePath, CompletionDelivered: int64(boolInt(run.CompletionDelivered)), StartedAt: unixNano(run.StartedAt), FinishedAt: unixNano(run.FinishedAt)}
}

func saveSubagentParams(run SubagentRun, toolsUsed []byte) dbgen.SaveSubagentRunParams {
	p := createSubagentParams(run, toolsUsed)
	return dbgen.SaveSubagentRunParams{ID: p.ID, SessionID: p.SessionID, ParentRunID: p.ParentRunID, ParentAgentID: p.ParentAgentID, ToolCallID: p.ToolCallID, ChildRunID: p.ChildRunID, Description: p.Description, SubagentType: p.SubagentType, State: p.State, Summary: p.Summary, Provider: p.Provider, Model: p.Model, Reasoning: p.Reasoning, CapabilityMode: p.CapabilityMode, RequestedIsolation: p.RequestedIsolation, Isolation: p.Isolation, Cwd: p.Cwd, Background: p.Background, Output: p.Output, Error: p.Error, Warning: p.Warning, Transcript: p.Transcript, ToolCalls: p.ToolCalls, Turns: p.Turns, TokensUsed: p.TokensUsed, ToolsUsed: p.ToolsUsed, WorktreePath: p.WorktreePath, CompletionDelivered: p.CompletionDelivered, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt}
}

func subagentRunFromDB(row dbgen.SubagentRun) (SubagentRun, error) {
	run := SubagentRun{ID: row.ID, SessionID: row.SessionID, ParentRunID: row.ParentRunID, ParentAgentID: row.ParentAgentID, ParentToolCallID: row.ToolCallID, ChildRunID: row.ChildRunID, Description: row.Description, Type: row.SubagentType, State: SubagentState(row.State), Summary: row.Summary, Provider: row.Provider, Model: row.Model, Reasoning: row.Reasoning, CapabilityMode: row.CapabilityMode, RequestedIsolation: row.RequestedIsolation, Isolation: row.Isolation, CWD: row.Cwd, Background: row.Background != 0, Output: row.Output, Error: row.Error, Warning: row.Warning, Transcript: append(json.RawMessage(nil), row.Transcript...), ToolCalls: int(row.ToolCalls), Turns: int(row.Turns), TokensUsed: int(row.TokensUsed), WorktreePath: row.WorktreePath, CompletionDelivered: row.CompletionDelivered != 0, StartedAt: timeFromUnixNano(row.StartedAt), FinishedAt: timeFromUnixNano(row.FinishedAt)}
	if len(row.ToolsUsed) > 0 {
		if err := json.Unmarshal(row.ToolsUsed, &run.ToolsUsed); err != nil {
			return SubagentRun{}, fmt.Errorf("decode subagent tools for %s: %w", run.ID, err)
		}
	}
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
