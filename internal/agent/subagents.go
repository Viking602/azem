package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/blobstore"
	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

const inlinePayloadLimit = 4096

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
	AccountID           string
	Model               string
	Reasoning           string
	CapabilityMode      string
	RequestedIsolation  string
	Isolation           string
	CWD                 string
	Background          bool
	Output              string
	Error               string
	StructuredOutput    json.RawMessage
	StructuredSource    string
	StructuredMode      string
	StructuredStatus    string
	StructuredError     string
	Warning             string
	EvidenceStatus      string
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
	db    *sql.DB
	blobs blobstore.Store
}

func NewSQLSubagentRunStore(db *sql.DB, blobs blobstore.Store) (*SQLSubagentRunStore, error) {
	if db == nil {
		return nil, fmt.Errorf("subagent store: database is nil")
	}
	if blobs == nil {
		blobs = blobstore.NewMemory()
	}
	return &SQLSubagentRunStore{db: db, blobs: blobs}, nil
}

func (s *SQLSubagentRunStore) Create(ctx context.Context, run SubagentRun) (err error) {
	created := make(map[string]struct{})
	defer s.finishBlobInstalls(created, &err)
	if run.ToolsUsed == nil {
		run.ToolsUsed = []string{}
	}
	toolsUsed, err := json.Marshal(run.ToolsUsed)
	if err != nil {
		return fmt.Errorf("encode subagent tools: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE subagent_runs SET id = id WHERE id = ''`); err != nil {
		return err
	}
	params, err := s.encodeRun(ctx, run, toolsUsed, created)
	if err != nil {
		return err
	}
	if err := dbgen.New(tx).CreateSubagentRun(ctx, params); err != nil {
		return err
	}
	return s.commitBlobInstalls(ctx, tx, created)
}

func (s *SQLSubagentRunStore) Save(ctx context.Context, run SubagentRun) (err error) {
	created := make(map[string]struct{})
	defer s.finishBlobInstalls(created, &err)
	if run.ToolsUsed == nil {
		run.ToolsUsed = []string{}
	}
	toolsUsed, err := json.Marshal(run.ToolsUsed)
	if err != nil {
		return fmt.Errorf("encode subagent tools: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE subagent_runs SET id = id WHERE id = ''`); err != nil {
		return err
	}
	params, err := s.encodeRun(ctx, run, toolsUsed, created)
	if err != nil {
		return err
	}
	result, err := dbgen.New(tx).SaveSubagentRun(ctx, saveSubagentParams(params))
	if err != nil {
		return err
	}
	if err := requireOneSubagentRow(result); err != nil {
		return err
	}
	return s.commitBlobInstalls(ctx, tx, created)
}

func (s *SQLSubagentRunStore) Get(ctx context.Context, id string) (SubagentRun, error) {
	row, err := dbgen.New(s.db).GetSubagentRun(ctx, id)
	if err == sql.ErrNoRows {
		return SubagentRun{}, agentruntime.ErrNotFound
	}
	if err != nil {
		return SubagentRun{}, err
	}
	return s.decodeRun(ctx, row)
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
		run, scanErr := s.decodeRun(ctx, row)
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

func (s *SQLSubagentRunStore) encodeRun(ctx context.Context, run SubagentRun, toolsUsed []byte, created map[string]struct{}) (dbgen.CreateSubagentRunParams, error) {
	transcript := []byte(run.Transcript)
	if len(transcript) == 0 {
		transcript = []byte("[]")
	}
	output, outputDigest, err := spillText(ctx, s.blobs, run.Output, created)
	if err != nil {
		return dbgen.CreateSubagentRunParams{}, err
	}
	storedTranscript, transcriptDigest, err := spillBytes(ctx, s.blobs, transcript, created)
	if err != nil {
		return dbgen.CreateSubagentRunParams{}, err
	}
	if storedTranscript == nil {
		storedTranscript = []byte{}
	}
	return dbgen.CreateSubagentRunParams{ID: run.ID, SessionID: run.SessionID, ParentRunID: run.ParentRunID, ParentAgentID: run.ParentAgentID, ToolCallID: run.ParentToolCallID, ChildRunID: run.ChildRunID, Description: run.Description, SubagentType: run.Type, State: string(run.State), Summary: run.Summary, Provider: run.Provider, Model: run.Model, Reasoning: run.Reasoning, CapabilityMode: run.CapabilityMode, RequestedIsolation: run.RequestedIsolation, Isolation: run.Isolation, Cwd: run.CWD, Background: int64(boolInt(run.Background)), Output: output, Error: run.Error, Warning: run.Warning, Transcript: storedTranscript, ToolCalls: int64(run.ToolCalls), Turns: int64(run.Turns), TokensUsed: int64(run.TokensUsed), ToolsUsed: toolsUsed, WorktreePath: run.WorktreePath, CompletionDelivered: int64(boolInt(run.CompletionDelivered)), StartedAt: unixNano(run.StartedAt), FinishedAt: unixNano(run.FinishedAt), TranscriptSha256: transcriptDigest, OutputSha256: outputDigest}, nil
}

func saveSubagentParams(p dbgen.CreateSubagentRunParams) dbgen.SaveSubagentRunParams {
	return dbgen.SaveSubagentRunParams{ID: p.ID, SessionID: p.SessionID, ParentRunID: p.ParentRunID, ParentAgentID: p.ParentAgentID, ToolCallID: p.ToolCallID, ChildRunID: p.ChildRunID, Description: p.Description, SubagentType: p.SubagentType, State: p.State, Summary: p.Summary, Provider: p.Provider, Model: p.Model, Reasoning: p.Reasoning, CapabilityMode: p.CapabilityMode, RequestedIsolation: p.RequestedIsolation, Isolation: p.Isolation, Cwd: p.Cwd, Background: p.Background, Output: p.Output, Error: p.Error, Warning: p.Warning, Transcript: p.Transcript, ToolCalls: p.ToolCalls, Turns: p.Turns, TokensUsed: p.TokensUsed, ToolsUsed: p.ToolsUsed, WorktreePath: p.WorktreePath, CompletionDelivered: p.CompletionDelivered, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt, TranscriptSha256: p.TranscriptSha256, OutputSha256: p.OutputSha256}
}

func (s *SQLSubagentRunStore) decodeRun(ctx context.Context, row dbgen.SubagentRun) (SubagentRun, error) {
	output, err := loadText(ctx, s.blobs, row.Output, row.OutputSha256)
	if err != nil {
		return SubagentRun{}, err
	}
	transcript, err := loadBytes(ctx, s.blobs, row.Transcript, row.TranscriptSha256)
	if err != nil {
		return SubagentRun{}, err
	}
	run := SubagentRun{ID: row.ID, SessionID: row.SessionID, ParentRunID: row.ParentRunID, ParentAgentID: row.ParentAgentID, ParentToolCallID: row.ToolCallID, ChildRunID: row.ChildRunID, Description: row.Description, Type: row.SubagentType, State: SubagentState(row.State), Summary: row.Summary, Provider: row.Provider, Model: row.Model, Reasoning: row.Reasoning, CapabilityMode: row.CapabilityMode, RequestedIsolation: row.RequestedIsolation, Isolation: row.Isolation, CWD: row.Cwd, Background: row.Background != 0, Output: output, Error: row.Error, Warning: row.Warning, Transcript: append(json.RawMessage(nil), transcript...), ToolCalls: int(row.ToolCalls), Turns: int(row.Turns), TokensUsed: int(row.TokensUsed), WorktreePath: row.WorktreePath, CompletionDelivered: row.CompletionDelivered != 0, StartedAt: timeFromUnixNano(row.StartedAt), FinishedAt: timeFromUnixNano(row.FinishedAt)}
	if len(row.ToolsUsed) > 0 {
		if err := json.Unmarshal(row.ToolsUsed, &run.ToolsUsed); err != nil {
			return SubagentRun{}, fmt.Errorf("decode subagent tools for %s: %w", run.ID, err)
		}
	}
	return run, nil
}

func spillText(ctx context.Context, blobs blobstore.Store, text string, created map[string]struct{}) (string, string, error) {
	if len(text) <= inlinePayloadLimit {
		return text, "", nil
	}
	digest := blobstore.Sum([]byte(text))
	installed, err := blobs.InstallAt(ctx, digest, []byte(text))
	if err != nil {
		return "", "", err
	}
	if installed {
		created[digest] = struct{}{}
	}
	return "", digest, nil
}

func spillBytes(ctx context.Context, blobs blobstore.Store, payload []byte, created map[string]struct{}) ([]byte, string, error) {
	if len(payload) <= inlinePayloadLimit {
		return payload, "", nil
	}
	digest := blobstore.Sum(payload)
	installed, err := blobs.InstallAt(ctx, digest, payload)
	if err != nil {
		return nil, "", err
	}
	if installed {
		created[digest] = struct{}{}
	}
	return []byte{}, digest, nil
}

func (s *SQLSubagentRunStore) finishBlobInstalls(created map[string]struct{}, operationErr *error) {
	if operationErr == nil || *operationErr == nil || len(created) == 0 {
		return
	}
	*operationErr = errors.Join(*operationErr, s.cleanupBlobInstalls(created))
}

func (s *SQLSubagentRunStore) commitBlobInstalls(ctx context.Context, tx *sql.Tx, created map[string]struct{}) error {
	for digest := range created {
		if err := s.cleanupBlobInstall(ctx, tx, digest); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLSubagentRunStore) cleanupBlobInstalls(created map[string]struct{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open subagent blob cleanup connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate subagent blob cleanup: %w", err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	var cleanupErrors []error
	for digest := range created {
		if err := s.cleanupBlobInstall(ctx, conn, digest); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("commit subagent blob cleanup: %w", err))
	}
	return errors.Join(cleanupErrors...)
}

func (s *SQLSubagentRunStore) cleanupBlobInstall(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, digest string,
) error {
	var referenced bool
	err := queryer.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM context_artifacts WHERE sha256 = ?
		UNION ALL SELECT 1 FROM session_blocks WHERE data_sha256 = ?
		UNION ALL SELECT 1 FROM session_tool_records WHERE content_sha256 = ? OR structured_sha256 = ?
		UNION ALL SELECT 1 FROM session_projections
			WHERE model_history_sha256 = ? OR json_extract(CAST(model_history AS TEXT), '$.blob') = ?
		UNION ALL SELECT 1 FROM subagent_runs WHERE transcript_sha256 = ? OR output_sha256 = ?
		UNION ALL SELECT 1 FROM events WHERE data_sha256 = ?
		UNION ALL SELECT 1 FROM records WHERE data_sha256 = ?
	)`, digest, digest, digest, digest, digest, digest, digest, digest, digest, digest).Scan(&referenced)
	if err != nil {
		return fmt.Errorf("check subagent blob %s: %w", digest, err)
	}
	if referenced {
		return nil
	}
	return s.blobs.Delete(ctx, digest)
}

func loadText(ctx context.Context, blobs blobstore.Store, inline, digest string) (string, error) {
	if digest == "" {
		return inline, nil
	}
	payload, err := blobs.Get(ctx, digest)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func loadBytes(ctx context.Context, blobs blobstore.Store, inline []byte, digest string) ([]byte, error) {
	if digest == "" {
		return inline, nil
	}
	return blobs.Get(ctx, digest)
}

func requireOneSubagentRow(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return agentruntime.ErrNotFound
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
