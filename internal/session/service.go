package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	Sequence         int64        `json:"-"`
	Kind             string       `json:"kind"`
	RunID            string       `json:"runId,omitempty"`
	AgentID          string       `json:"agentId,omitempty"`
	ParentToolCallID string       `json:"parentToolCallId,omitempty"`
	Title            string       `json:"title,omitempty"`
	Content          string       `json:"content,omitempty"`
	State            string       `json:"state,omitempty"`
	Collapsed        bool         `json:"collapsed,omitempty"`
	Attachments      []Attachment `json:"attachments,omitempty"`
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
	CoveredThroughSequence *int64            `json:"coveredThroughSequence,omitempty"`
	Generation             int64             `json:"generation,omitempty"`
	SummaryHash            string            `json:"summaryHash,omitempty"`
	StaticPrefixHash       string            `json:"staticPrefixHash,omitempty"`
	WireVersion            int               `json:"wireVersion,omitempty"`
}

const CurrentWireVersion = 1

var ErrRunCheckpointStale = errors.New("session: run checkpoint source is stale")

// ModelCheckpointHash identifies every private message that constitutes a
// compacted provider checkpoint. Execution facts are included with the model
// summary so cache identity cannot outlive the workspace/Todo evidence it was
// paired with.
func ModelCheckpointHash(messages []message.Message) string {
	var checkpoint []message.Message
	for _, current := range messages {
		if current.Kind != message.KindCompactionSummary && current.Metadata["azem.context.execution_checkpoint"] == "" {
			continue
		}
		current.CreatedAt = time.Time{}
		checkpoint = append(checkpoint, current)
	}
	if len(checkpoint) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(checkpoint)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

type Projection struct {
	Session              Session
	LastRunID            string
	Blocks               []Block
	ModelHistory         ModelHistory
	Usage                Usage
	UpdatedAt            time.Time
	CheckpointGeneration int64
	CacheEpoch           int64
	CacheIdentityHash    string
}

type CompactionPlan struct {
	Summary           string
	ModelHistory      ModelHistory
	ExpectedUpdatedAt time.Time
	TailStart         int
	ExpectedHighWater *int64
}

// RunCheckpoint installs provider-resumable history for an active run without
// appending a canonical assistant block. Cache identity changes atomically with
// the history so a resumed request can never pair a new checkpoint with the
// previous provider cache generation.
type RunCheckpoint struct {
	RunID             string
	ModelHistory      ModelHistory
	CacheIdentity     string
	ExpectedHighWater *int64
}

type Service struct {
	db *sql.DB
}

type ContextArtifact struct {
	ID        string
	SessionID string
	RunID     string
	Kind      string
	SHA256    string
	Payload   []byte
	Preview   string
	CreatedAt time.Time
}

type HistoryRecord struct {
	SessionID  string `json:"sessionId"`
	SourceType string `json:"sourceType"`
	SourceID   string `json:"sourceId"`
	Preview    string `json:"preview,omitempty"`
	Content    string `json:"content,omitempty"`
}

const (
	defaultHistoryLimit = 8
	maxHistoryLimit     = 20
)

// SearchHistory searches only durable, canonical sources in one session. The
// payload budget is approximate (four UTF-8 bytes per token) and is also
// capped by byteBudget. Artifact payloads are never loaded by this method.
func (s *Service) SearchHistory(ctx context.Context, sessionID, query string, limit, tokenBudget, byteBudget int) ([]HistoryRecord, error) {
	match := safeHistoryMatch(query)
	if match == "" || strings.TrimSpace(sessionID) == "" || tokenBudget <= 0 || byteBudget <= 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	if limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}
	budget := byteBudget
	if tokenBytes := tokenBudget * 4; tokenBytes < budget {
		budget = tokenBytes
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.session_id,f.source_type,f.source_id,f.content
		FROM history_fts f
		WHERE history_fts MATCH ? AND f.session_id=? AND (
			(f.source_type='sequence' AND EXISTS(SELECT 1 FROM session_blocks b WHERE b.session_id=f.session_id
				AND (b.kind='user' OR (b.kind='assistant' AND COALESCE(json_extract(b.data,'$.state'),'') IN ('','completed')))
				AND 'sequence:'||b.sequence=f.source_id)) OR
			(f.source_type='artifact' AND EXISTS(SELECT 1 FROM context_artifacts a WHERE a.session_id=f.session_id AND 'artifact:'||a.id=f.source_id)))
		ORDER BY bm25(history_fts) LIMIT ?`, match, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("search session history: %w", err)
	}
	defer rows.Close()
	result := make([]HistoryRecord, 0, limit)
	used := 0
	for rows.Next() {
		var record HistoryRecord
		var text string
		if err := rows.Scan(&record.SessionID, &record.SourceType, &record.SourceID, &text); err != nil {
			return nil, err
		}
		remaining := budget - used
		if remaining <= 0 {
			break
		}
		text = truncateUTF8Bytes(text, remaining)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if record.SourceType == "artifact" {
			record.Preview = text
		} else {
			record.Content = text
		}
		used += len(text)
		result = append(result, record)
	}
	return result, rows.Err()
}

func safeHistoryMatch(query string) string {
	words := strings.FieldsFunc(query, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80)
	})
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word != "" {
			quoted = append(quoted, `"`+strings.ReplaceAll(word, `"`, `""`)+`"`)
		}
	}
	return strings.Join(quoted, " OR ")
}

func truncateUTF8Bytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && (value[limit]&0xc0) == 0x80 {
		limit--
	}
	return value[:limit]
}

// PutArtifact durably stores a payload and returns the existing row when the
// same session, kind, and content are seen again.
func (s *Service) PutArtifact(ctx context.Context, sessionID, runID, kind string, payload []byte, preview string) (ContextArtifact, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(kind) == "" {
		return ContextArtifact{}, fmt.Errorf("artifact session and kind are required")
	}
	digest := sha256.Sum256(payload)
	hash := fmt.Sprintf("%x", digest[:])
	idDigest := sha256.Sum256([]byte(sessionID + "\x00" + kind + "\x00" + hash))
	id := fmt.Sprintf("artifact_%x", idDigest[:16])
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO context_artifacts(id,session_id,run_id,kind,sha256,payload,preview,created_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(session_id,kind,sha256) DO NOTHING`, id, sessionID, runID, kind, hash, payload, preview, now.UnixNano()); err != nil {
		return ContextArtifact{}, fmt.Errorf("put context artifact: %w", err)
	}
	return s.LoadArtifact(ctx, sessionID, id)
}

func (s *Service) LoadArtifact(ctx context.Context, sessionID, id string) (ContextArtifact, error) {
	var value ContextArtifact
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id,session_id,run_id,kind,sha256,payload,preview,created_at
		FROM context_artifacts WHERE id=? AND session_id=?`, id, sessionID).Scan(&value.ID, &value.SessionID, &value.RunID, &value.Kind, &value.SHA256, &value.Payload, &value.Preview, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextArtifact{}, fmt.Errorf("context artifact %q not found in session %q", id, sessionID)
	}
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("load context artifact: %w", err)
	}
	value.Payload = append([]byte(nil), value.Payload...)
	value.CreatedAt = time.Unix(0, created).UTC()
	return value, nil
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
	var blocksData, historyData, usageData []byte
	var runID string
	var updated, generation, cacheEpoch int64
	var cacheIdentity string
	err = s.db.QueryRowContext(ctx, `SELECT last_run_id,blocks,model_history,usage,updated_at,checkpoint_generation,cache_epoch,cache_identity_hash FROM session_projections WHERE session_id=?`, id).Scan(
		&runID, &blocksData, &historyData, &usageData, &updated, &generation, &cacheEpoch, &cacheIdentity,
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
	usage, err := DecodeUsage(usageData)
	if err != nil {
		return Projection{}, err
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
		Session: value, LastRunID: runID, Blocks: blocks, ModelHistory: history, Usage: usage,
		UpdatedAt:            time.Unix(0, updated).UTC(),
		CheckpointGeneration: generation, CacheEpoch: cacheEpoch, CacheIdentityHash: cacheIdentity,
	}, nil
}

func (s *Service) AppendBlock(ctx context.Context, sessionID string, block Block) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	sequence, mutated, err := appendSessionBlock(ctx, tx, sessionID, block)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().UnixNano()
	// Appending a canonical user tail and UI-only lifecycle updates do not make
	// an earlier checkpoint stale. Coalescing can rewrite a covered assistant.
	query := `UPDATE session_projections SET last_run_id=?,updated_at=? WHERE session_id=?`
	if mutated && block.Kind == "assistant" {
		query = `UPDATE session_projections SET last_run_id=?,
			model_history=CASE WHEN CAST(json_extract(model_history,'$.coveredThroughSequence') AS INTEGER)>=` + fmt.Sprint(sequence) + ` THEN '{}' ELSE model_history END,
			checkpoint_generation=checkpoint_generation+CASE WHEN CAST(json_extract(model_history,'$.coveredThroughSequence') AS INTEGER)>=` + fmt.Sprint(sequence) + ` THEN 1 ELSE 0 END,
			updated_at=? WHERE session_id=?`
	}
	if _, err := tx.ExecContext(ctx, query, block.RunID, now, sessionID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return 0, err
	}
	return sequence, tx.Commit()
}

func (s *Service) CompleteTurn(ctx context.Context, sessionID string, block Block, history ModelHistory) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var activeRunID string
	var currentGeneration int64
	if err := tx.QueryRowContext(ctx, `SELECT last_run_id,checkpoint_generation FROM session_projections WHERE session_id=?`, sessionID).
		Scan(&activeRunID, &currentGeneration); err != nil {
		return err
	}
	if block.RunID != "" && activeRunID != "" && activeRunID != block.RunID {
		return fmt.Errorf("complete turn: active run changed from %q to %q", block.RunID, activeRunID)
	}
	if strings.TrimSpace(block.Content) != "" {
		if _, _, err := appendSessionBlock(ctx, tx, sessionID, block); err != nil {
			return err
		}
	}
	// Derive checkpoint identity from the actual provider history. Automatic
	// compaction must never rely on an empty caller-supplied SummaryHash.
	if hash := ModelCheckpointHash(history.Messages); hash != "" {
		history.SummaryHash = hash
		history.WireVersion = CurrentWireVersion
		if history.StaticPrefixHash == "" {
			history.StaticPrefixHash = history.InstructionFingerprint
		}
	}
	encodedHistory, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("encode model history: %w", err)
	}
	boundary, err := canonicalHighWater(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	history.CoveredThroughSequence = boundary
	generation := currentGeneration + 1
	history.Generation = generation
	encodedHistory, err = json.Marshal(history)
	if err != nil {
		return fmt.Errorf("encode model history: %w", err)
	}
	now := time.Now().UTC().UnixNano()
	result, err := tx.ExecContext(ctx, `UPDATE session_projections SET last_run_id=?,model_history=?,checkpoint_generation=?,updated_at=?
		WHERE session_id=? AND last_run_id=? AND checkpoint_generation=?`, block.RunID, encodedHistory, generation, now,
		sessionID, activeRunID, currentGeneration)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("complete turn: active run or checkpoint changed while completion was prepared")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func sameSequence(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// SaveRunCheckpoint durably advances an active run's replaceable provider
// history while leaving the canonical transcript unchanged. It is safe to call
// repeatedly with the same checkpoint identity and rejects a stale run after a
// newer user turn has taken ownership of the session.
func (s *Service) SaveRunCheckpoint(ctx context.Context, sessionID string, checkpoint RunCheckpoint) error {
	if strings.TrimSpace(checkpoint.RunID) == "" || len(checkpoint.ModelHistory.Messages) == 0 || strings.TrimSpace(checkpoint.CacheIdentity) == "" {
		return fmt.Errorf("save run checkpoint: run, history, and cache identity are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lastRunID, currentIdentity string
	var generation, cacheEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT last_run_id,checkpoint_generation,cache_epoch,cache_identity_hash FROM session_projections WHERE session_id=?`, sessionID).
		Scan(&lastRunID, &generation, &cacheEpoch, &currentIdentity); err != nil {
		return err
	}
	if lastRunID != checkpoint.RunID {
		return fmt.Errorf("save run checkpoint: active run changed from %q to %q", checkpoint.RunID, lastRunID)
	}
	history := checkpoint.ModelHistory
	if hash := ModelCheckpointHash(history.Messages); hash != "" {
		history.SummaryHash = hash
		history.WireVersion = CurrentWireVersion
		if history.StaticPrefixHash == "" {
			history.StaticPrefixHash = history.InstructionFingerprint
		}
	}
	boundary, err := canonicalHighWater(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	if checkpoint.ExpectedHighWater != nil && (boundary == nil || *boundary < *checkpoint.ExpectedHighWater) {
		return fmt.Errorf("%w: canonical transcript changed while checkpoint was prepared", ErrRunCheckpointStale)
	}
	if currentIdentity == checkpoint.CacheIdentity {
		var current ModelHistory
		var encoded []byte
		if err := tx.QueryRowContext(ctx, `SELECT model_history FROM session_projections WHERE session_id=?`, sessionID).Scan(&encoded); err != nil {
			return err
		}
		if json.Unmarshal(encoded, &current) == nil && reflect.DeepEqual(normalizeMessageTimes(current.Messages), normalizeMessageTimes(history.Messages)) {
			return tx.Commit()
		}
	}
	history.CoveredThroughSequence = checkpoint.ExpectedHighWater
	history.Generation = generation + 1
	encoded, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("encode run checkpoint: %w", err)
	}
	now := time.Now().UTC().UnixNano()
	nextCacheEpoch := cacheEpoch
	if currentIdentity != checkpoint.CacheIdentity {
		nextCacheEpoch++
	}
	result, err := tx.ExecContext(ctx, `UPDATE session_projections SET model_history=?,checkpoint_generation=?,cache_epoch=?,cache_identity_hash=?,updated_at=?
		WHERE session_id=? AND last_run_id=? AND checkpoint_generation=?`, encoded, generation+1, nextCacheEpoch, checkpoint.CacheIdentity, now,
		sessionID, checkpoint.RunID, generation)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("save run checkpoint: projection changed while checkpoint was prepared")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeMessageTimes(messages []message.Message) []message.Message {
	result := append([]message.Message(nil), messages...)
	for index := range result {
		result[index].CreatedAt = time.Time{}
	}
	return result
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
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET updated_at=? WHERE session_id=?`, now, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// CompactWithSummary activates a provider checkpoint without changing the
// canonical transcript. The name is retained for API compatibility.
func (s *Service) CompactWithSummary(ctx context.Context, sessionID string, plan CompactionPlan) (Projection, error) {
	if strings.TrimSpace(plan.Summary) == "" {
		return Projection{}, fmt.Errorf("compact session: summary is empty")
	}
	projection, err := s.LoadProjection(ctx, sessionID)
	if err != nil {
		return Projection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Projection{}, err
	}
	defer tx.Rollback()
	var historyData []byte
	var projectionUpdated, generation, cacheEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT model_history,updated_at,checkpoint_generation,cache_epoch FROM session_projections WHERE session_id=?`, sessionID).Scan(&historyData, &projectionUpdated, &generation, &cacheEpoch); err != nil {
		return Projection{}, err
	}
	if !plan.ExpectedUpdatedAt.IsZero() && projectionUpdated != plan.ExpectedUpdatedAt.UnixNano() {
		return Projection{}, fmt.Errorf("compact session: projection changed while summary was generated")
	}
	if err := json.Unmarshal(historyData, &projection.ModelHistory); err != nil {
		return Projection{}, fmt.Errorf("decode model history for compaction: %w", err)
	}
	boundary, err := canonicalHighWater(ctx, tx, sessionID)
	if err != nil {
		return Projection{}, err
	}
	if plan.ExpectedHighWater != nil && (boundary == nil || *boundary != *plan.ExpectedHighWater) {
		return Projection{}, fmt.Errorf("compact session: projection changed while summary was generated")
	}
	plan.ModelHistory.CoveredThroughSequence = boundary
	plan.ModelHistory.Generation = generation + 1
	now := time.Now().UTC().UnixNano()
	encodedHistory, err := json.Marshal(plan.ModelHistory)
	if err != nil {
		return Projection{}, fmt.Errorf("encode compacted model history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET model_history=?,checkpoint_generation=?,cache_epoch=?,cache_identity_hash='',updated_at=? WHERE session_id=?`, encodedHistory, generation+1, cacheEpoch+1, now, sessionID); err != nil {
		return Projection{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return Projection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Projection{}, err
	}
	projection.ModelHistory = plan.ModelHistory
	projection.UpdatedAt = time.Unix(0, now).UTC()
	projection.CheckpointGeneration = generation + 1
	projection.CacheEpoch = cacheEpoch + 1
	projection.CacheIdentityHash = ""
	return projection, nil
}

type sessionBlockQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadSessionBlocks(ctx context.Context, queryer sessionBlockQueryer, sessionID string) ([]Block, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT sequence,data FROM session_blocks WHERE session_id=? ORDER BY sequence`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session blocks: %w", err)
	}
	defer rows.Close()
	blocks := make([]Block, 0)
	for rows.Next() {
		var data []byte
		var sequence int64
		if err := rows.Scan(&sequence, &data); err != nil {
			return nil, err
		}
		var block Block
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, fmt.Errorf("decode session block: %w", err)
		}
		block.Sequence = sequence
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}

func appendSessionBlock(ctx context.Context, tx *sql.Tx, sessionID string, block Block) (int64, bool, error) {
	var sequence int64
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT sequence,data FROM session_blocks WHERE session_id=? ORDER BY sequence DESC LIMIT 1`, sessionID).Scan(&sequence, &data)
	empty := errors.Is(err, sql.ErrNoRows)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("load latest session block: %w", err)
	}
	if err == nil && block.Kind == "assistant" {
		var previous Block
		if err := json.Unmarshal(data, &previous); err != nil {
			return 0, false, fmt.Errorf("decode latest session block: %w", err)
		}
		if previous.Kind == block.Kind && previous.RunID == block.RunID {
			previous.Content += block.Content
			encoded, err := json.Marshal(previous)
			if err != nil {
				return 0, false, err
			}
			_, err = tx.ExecContext(ctx, `UPDATE session_blocks SET data=? WHERE session_id=? AND sequence=?`, encoded, sessionID, sequence)
			return sequence, true, err
		}
	}
	encoded, err := json.Marshal(block)
	if err != nil {
		return 0, false, err
	}
	sequence++
	if empty {
		sequence = 0
	}
	return sequence, false, insertSessionBlock(ctx, tx, sessionID, block, encoded)
}

func canonicalHighWater(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sessionID string) (*int64, error) {
	var value sql.NullInt64
	if err := queryer.QueryRowContext(ctx, `SELECT MAX(sequence) FROM session_blocks WHERE session_id=? AND kind IN ('user','assistant')`, sessionID).Scan(&value); err != nil {
		return nil, err
	}
	if !value.Valid {
		return nil, nil
	}
	return &value.Int64, nil
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
