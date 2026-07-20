package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/go-hydaelyn/message"
)

type Session struct {
	ID         string
	Title      string
	ProviderID string
	ModelID    string
	Reasoning  string
	AgentMode  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Block struct {
	Kind             string `json:"kind"`
	RunID            string `json:"runId,omitempty"`
	AgentID          string `json:"agentId,omitempty"`
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
	Title            string `json:"title,omitempty"`
	Content          string `json:"content,omitempty"`
	State            string `json:"state,omitempty"`
	Collapsed        bool   `json:"collapsed,omitempty"`
}

// ModelHistory is a replaceable provider-resume checkpoint, not the durable
// conversation record. CompleteTurn installs it atomically; independent
// transcript mutations invalidate it, while provider-generated compaction
// replaces the transcript and checkpoint in one transaction.
type ModelHistory struct {
	ProviderID             string            `json:"providerId,omitempty"`
	ModelID                string            `json:"modelId,omitempty"`
	InstructionFingerprint string            `json:"instructionFingerprint,omitempty"`
	Messages               []message.Message `json:"messages,omitempty"`
}

type Projection struct {
	Session      Session
	LastRunID    string
	Blocks       []Block
	ModelHistory ModelHistory
	UpdatedAt    time.Time
}

type CompactionPlan struct {
	Summary           string
	ModelHistory      ModelHistory
	ExpectedUpdatedAt time.Time
	TailStart         int
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) Ensure(ctx context.Context, value Session) (Session, error) {
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, value.ID, value.Title, value.ProviderID, value.ModelID, value.Reasoning, value.AgentMode, value.CreatedAt.UnixNano(), value.UpdatedAt.UnixNano())
	if err != nil {
		return Session{}, fmt.Errorf("ensure session: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO session_projections(session_id,updated_at) VALUES(?,?) ON CONFLICT(session_id) DO NOTHING`, value.ID, now.UnixNano())
	if err != nil {
		return Session{}, fmt.Errorf("ensure session projection: %w", err)
	}
	return s.LoadSession(ctx, value.ID)
}

func (s *Service) LoadSession(ctx context.Context, id string) (Session, error) {
	var value Session
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at FROM sessions WHERE id=?`, id).Scan(
		&value.ID, &value.Title, &value.ProviderID, &value.ModelID, &value.Reasoning, &value.AgentMode, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("session %q not found", id)
	}
	if err != nil {
		return Session{}, fmt.Errorf("load session: %w", err)
	}
	value.CreatedAt = time.Unix(0, created).UTC()
	value.UpdatedAt = time.Unix(0, updated).UTC()
	return value, nil
}

func (s *Service) UpdatePreferences(ctx context.Context, id, providerID, modelID, reasoning, agentMode string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET provider_id=?,model_id=?,reasoning=?,agent_mode=?,updated_at=? WHERE id=?`,
		providerID, modelID, reasoning, agentMode, time.Now().UTC().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("update session preferences: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("session %q not found", id)
	}
	return nil
}

func (s *Service) LoadProjection(ctx context.Context, id string) (Projection, error) {
	value, err := s.LoadSession(ctx, id)
	if err != nil {
		return Projection{}, err
	}
	var blocksData, historyData []byte
	var runID string
	var updated int64
	err = s.db.QueryRowContext(ctx, `SELECT last_run_id,blocks,model_history,updated_at FROM session_projections WHERE session_id=?`, id).Scan(
		&runID, &blocksData, &historyData, &updated,
	)
	if err != nil {
		return Projection{}, fmt.Errorf("load projection: %w", err)
	}
	var blocks []Block
	if err := json.Unmarshal(blocksData, &blocks); err != nil {
		return Projection{}, fmt.Errorf("decode projection: %w", err)
	}
	var history ModelHistory
	if err := json.Unmarshal(historyData, &history); err != nil {
		return Projection{}, fmt.Errorf("decode model history: %w", err)
	}
	blocks, err = loadSessionBlocks(ctx, s.db, id)
	if err != nil {
		return Projection{}, err
	}
	if len(blocks) == 0 && string(blocksData) != "[]" {
		if err := json.Unmarshal(blocksData, &blocks); err != nil {
			return Projection{}, fmt.Errorf("decode legacy projection: %w", err)
		}
	}
	return Projection{
		Session: value, LastRunID: runID, Blocks: blocks, ModelHistory: history,
		UpdatedAt: time.Unix(0, updated).UTC(),
	}, nil
}

func (s *Service) AppendBlock(ctx context.Context, sessionID string, block Block) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendSessionBlock(ctx, tx, sessionID, block); err != nil {
		return err
	}
	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET last_run_id=?,model_history='{}',updated_at=? WHERE session_id=?`, block.RunID, now, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) CompleteTurn(ctx context.Context, sessionID string, block Block, history ModelHistory) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if strings.TrimSpace(block.Content) != "" {
		if err := appendSessionBlock(ctx, tx, sessionID, block); err != nil {
			return err
		}
	}
	encodedHistory, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("encode model history: %w", err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET last_run_id=?,model_history=?,updated_at=? WHERE session_id=?`,
		block.RunID, encodedHistory, now, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) UpsertAgentBlock(ctx context.Context, sessionID, agentID string, block Block) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("agent ID is required")
	}
	block.Kind = "agent"
	block.AgentID = agentID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	encoded, err := json.Marshal(block)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE session_blocks SET run_id=?,data=? WHERE session_id=? AND kind='agent' AND agent_id=?`,
		block.RunID, encoded, sessionID, agentID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		if err := insertSessionBlock(ctx, tx, sessionID, block, encoded); err != nil {
			return err
		}
	}
	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET model_history='{}',updated_at=? WHERE session_id=?`, now, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Compact(ctx context.Context, sessionID string) (Projection, error) {
	return s.compact(ctx, sessionID, CompactionPlan{})
}

// CompactWithSummary replaces older transcript blocks with a provider-generated
// handoff and installs the matching provider-resume checkpoint atomically.
func (s *Service) CompactWithSummary(ctx context.Context, sessionID string, plan CompactionPlan) (Projection, error) {
	if strings.TrimSpace(plan.Summary) == "" {
		return Projection{}, fmt.Errorf("compact session: summary is empty")
	}
	return s.compact(ctx, sessionID, plan)
}

func (s *Service) compact(ctx context.Context, sessionID string, plan CompactionPlan) (Projection, error) {
	projection, err := s.LoadProjection(ctx, sessionID)
	if err != nil {
		return Projection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Projection{}, err
	}
	defer tx.Rollback()
	projection.Blocks, err = loadSessionBlocks(ctx, tx, sessionID)
	if err != nil {
		return Projection{}, err
	}
	var historyData []byte
	var projectionUpdated int64
	if err := tx.QueryRowContext(ctx, `SELECT model_history,updated_at FROM session_projections WHERE session_id=?`, sessionID).Scan(&historyData, &projectionUpdated); err != nil {
		return Projection{}, err
	}
	if !plan.ExpectedUpdatedAt.IsZero() && projectionUpdated != plan.ExpectedUpdatedAt.UnixNano() {
		return Projection{}, fmt.Errorf("compact session: projection changed while summary was generated")
	}
	if err := json.Unmarshal(historyData, &projection.ModelHistory); err != nil {
		return Projection{}, fmt.Errorf("decode model history for compaction: %w", err)
	}
	const keepRecent = 4
	if len(projection.Blocks) <= keepRecent+1 {
		return projection, nil
	}
	tailStart := len(projection.Blocks) - keepRecent
	if plan.Summary != "" {
		tailStart = plan.TailStart
		if tailStart <= 0 || tailStart >= len(projection.Blocks) {
			return Projection{}, fmt.Errorf("compact session: invalid retained tail start %d", tailStart)
		}
	}
	older := projection.Blocks[:tailStart]
	var summary strings.Builder
	if plan.Summary != "" {
		summary.WriteString(strings.TrimSpace(plan.Summary))
	} else {
		summary.WriteString("Earlier conversation compacted locally:\n")
		for _, block := range older {
			text := []rune(strings.TrimSpace(block.Content))
			if len(text) > 320 {
				text = append(text[:320], '…')
			}
			fmt.Fprintf(&summary, "- %s: %s\n", firstSessionValue(block.Title, block.Kind), string(text))
			if summary.Len() >= 4_000 {
				break
			}
		}
	}
	compacted := []Block{{Kind: "assistant", Title: "Compacted history", Content: summary.String(), State: "compacted", Collapsed: true}}
	compacted = append(compacted, projection.Blocks[tailStart:]...)
	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_blocks WHERE session_id=?`, sessionID); err != nil {
		return Projection{}, err
	}
	for _, block := range compacted {
		encoded, err := json.Marshal(block)
		if err != nil {
			return Projection{}, err
		}
		if err := insertSessionBlock(ctx, tx, sessionID, block, encoded); err != nil {
			return Projection{}, err
		}
	}
	encodedHistory := []byte(`{}`)
	if plan.Summary != "" {
		encodedHistory, err = json.Marshal(plan.ModelHistory)
		if err != nil {
			return Projection{}, fmt.Errorf("encode compacted model history: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET model_history=?,updated_at=? WHERE session_id=?`, encodedHistory, now, sessionID); err != nil {
		return Projection{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return Projection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Projection{}, err
	}
	projection.Blocks = compacted
	projection.ModelHistory = plan.ModelHistory
	projection.UpdatedAt = time.Unix(0, now).UTC()
	return projection, nil
}

type sessionBlockQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadSessionBlocks(ctx context.Context, queryer sessionBlockQueryer, sessionID string) ([]Block, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT data FROM session_blocks WHERE session_id=? ORDER BY sequence`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session blocks: %w", err)
	}
	defer rows.Close()
	blocks := make([]Block, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var block Block
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, fmt.Errorf("decode session block: %w", err)
		}
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}

func appendSessionBlock(ctx context.Context, tx *sql.Tx, sessionID string, block Block) error {
	var sequence int
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT sequence,data FROM session_blocks WHERE session_id=? ORDER BY sequence DESC LIMIT 1`, sessionID).Scan(&sequence, &data)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load latest session block: %w", err)
	}
	if err == nil && block.Kind == "assistant" {
		var previous Block
		if err := json.Unmarshal(data, &previous); err != nil {
			return fmt.Errorf("decode latest session block: %w", err)
		}
		if previous.Kind == block.Kind && previous.RunID == block.RunID {
			previous.Content += block.Content
			encoded, err := json.Marshal(previous)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE session_blocks SET data=? WHERE session_id=? AND sequence=?`, encoded, sessionID, sequence)
			return err
		}
	}
	encoded, err := json.Marshal(block)
	if err != nil {
		return err
	}
	return insertSessionBlock(ctx, tx, sessionID, block, encoded)
}

func insertSessionBlock(ctx context.Context, tx *sql.Tx, sessionID string, block Block, encoded []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data)
		SELECT ?,COALESCE(MAX(sequence)+1,0),?,?,?,? FROM session_blocks WHERE session_id=?`,
		sessionID, block.Kind, block.RunID, block.AgentID, encoded, sessionID)
	return err
}

func firstSessionValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "message"
}

func (s *Service) List(ctx context.Context, limit int) ([]Session, error) {
	query := `SELECT s.id,s.title,s.provider_id,s.model_id,s.reasoning,s.agent_mode,s.created_at,s.updated_at
		FROM sessions s JOIN session_projections p ON p.session_id=s.id
		WHERE p.last_run_id<>'' OR EXISTS(SELECT 1 FROM session_blocks b WHERE b.session_id=s.id) OR CAST(p.blocks AS TEXT)<>'[]'
		ORDER BY s.updated_at DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []Session
	for rows.Next() {
		var value Session
		var created, updated int64
		if err := rows.Scan(&value.ID, &value.Title, &value.ProviderID, &value.ModelID, &value.Reasoning, &value.AgentMode, &created, &updated); err != nil {
			return nil, err
		}
		value.CreatedAt = time.Unix(0, created).UTC()
		value.UpdatedAt = time.Unix(0, updated).UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}
