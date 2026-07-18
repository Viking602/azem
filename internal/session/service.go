package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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

type Projection struct {
	Session   Session
	LastRunID string
	Blocks    []Block
	UpdatedAt time.Time
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
	var data []byte
	var runID string
	var updated int64
	err = s.db.QueryRowContext(ctx, `SELECT last_run_id,blocks,updated_at FROM session_projections WHERE session_id=?`, id).Scan(&runID, &data, &updated)
	if err != nil {
		return Projection{}, fmt.Errorf("load projection: %w", err)
	}
	var blocks []Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return Projection{}, fmt.Errorf("decode projection: %w", err)
	}
	return Projection{Session: value, LastRunID: runID, Blocks: blocks, UpdatedAt: time.Unix(0, updated).UTC()}, nil
}

func (s *Service) AppendBlock(ctx context.Context, sessionID string, block Block) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT blocks FROM session_projections WHERE session_id=?`, sessionID).Scan(&data); err != nil {
		return fmt.Errorf("load projection for append: %w", err)
	}
	var blocks []Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	if len(blocks) > 0 && block.Kind == "assistant" {
		last := &blocks[len(blocks)-1]
		if last.Kind == block.Kind && last.RunID == block.RunID {
			last.Content += block.Content
		} else {
			blocks = append(blocks, block)
		}
	} else {
		blocks = append(blocks, block)
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET last_run_id=?,blocks=?,updated_at=? WHERE session_id=?`, block.RunID, encoded, now, sessionID); err != nil {
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
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT blocks FROM session_projections WHERE session_id=?`, sessionID).Scan(&data); err != nil {
		return fmt.Errorf("load projection for agent upsert: %w", err)
	}
	var blocks []Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return fmt.Errorf("decode projection for agent upsert: %w", err)
	}
	replaced := false
	for index := range blocks {
		if blocks[index].Kind == "agent" && blocks[index].AgentID == agentID {
			blocks[index] = block
			replaced = true
			break
		}
	}
	if !replaced {
		blocks = append(blocks, block)
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET blocks=?,updated_at=? WHERE session_id=?`, encoded, now, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Compact(ctx context.Context, sessionID string) (Projection, error) {
	projection, err := s.LoadProjection(ctx, sessionID)
	if err != nil {
		return Projection{}, err
	}
	const keepRecent = 4
	if len(projection.Blocks) <= keepRecent+1 {
		return projection, nil
	}
	older := projection.Blocks[:len(projection.Blocks)-keepRecent]
	var summary strings.Builder
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
	compacted := []Block{{Kind: "assistant", Title: "Compacted history", Content: summary.String(), State: "compacted", Collapsed: true}}
	compacted = append(compacted, projection.Blocks[len(projection.Blocks)-keepRecent:]...)
	encoded, err := json.Marshal(compacted)
	if err != nil {
		return Projection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Projection{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET blocks=?,updated_at=? WHERE session_id=?`, encoded, now, sessionID); err != nil {
		return Projection{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return Projection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Projection{}, err
	}
	projection.Blocks = compacted
	projection.UpdatedAt = time.Unix(0, now).UTC()
	return projection, nil
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
		WHERE p.last_run_id<>'' OR CAST(p.blocks AS TEXT)<>'[]'
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
