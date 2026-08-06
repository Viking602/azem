package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
	"github.com/Viking602/venat/message"
)

type Session struct {
	ID         string
	Workspace  string
	Title      string
	ProviderID string
	ModelID    string
	Reasoning  string
	AgentMode  string
	Pinned     bool
	Archived   bool
	Unread     bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Block struct {
	Sequence         int64             `json:"-"`
	Kind             string            `json:"kind"`
	RunID            string            `json:"runId,omitempty"`
	AgentID          string            `json:"agentId,omitempty"`
	ParentToolCallID string            `json:"parentToolCallId,omitempty"`
	Title            string            `json:"title,omitempty"`
	Content          string            `json:"content,omitempty"`
	TextPhase        string            `json:"textPhase,omitempty"`
	State            string            `json:"state,omitempty"`
	Collapsed        bool              `json:"collapsed,omitempty"`
	Attachments      []Attachment      `json:"attachments,omitempty"`
	Data             map[string]string `json:"data,omitempty"`
	Thinking         string            `json:"-"`
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
	ToolRecords          []ToolRecord
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
	// sqlc v1.30.0 treats the FTS5 table-name operand of MATCH as a column
	// reference ("column history_fts does not exist"), so this query stays raw.
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
	id := contextArtifactID(sessionID, kind, hash)
	now := time.Now().UTC()
	if err := dbgen.New(s.db).InsertContextArtifact(ctx, dbgen.InsertContextArtifactParams{ID: id, SessionID: sessionID, RunID: runID, Kind: kind, Sha256: hash, Payload: payload, Preview: preview, CreatedAt: now.UnixNano()}); err != nil {
		return ContextArtifact{}, fmt.Errorf("put context artifact: %w", err)
	}
	return s.LoadArtifact(ctx, sessionID, id)
}

func contextArtifactID(sessionID, kind, hash string) string {
	digest := sha256.Sum256([]byte(sessionID + "\x00" + kind + "\x00" + hash))
	return fmt.Sprintf("artifact_%x", digest[:16])
}

func (s *Service) LoadArtifact(ctx context.Context, sessionID, id string) (ContextArtifact, error) {
	row, err := dbgen.New(s.db).GetContextArtifact(ctx, dbgen.GetContextArtifactParams{ID: id, SessionID: sessionID})
	if errors.Is(err, sql.ErrNoRows) {
		return ContextArtifact{}, fmt.Errorf("context artifact %q not found in session %q", id, sessionID)
	}
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("load context artifact: %w", err)
	}
	value := ContextArtifact{ID: row.ID, SessionID: row.SessionID, RunID: row.RunID, Kind: row.Kind, SHA256: row.Sha256, Payload: append([]byte(nil), row.Payload...), Preview: row.Preview, CreatedAt: time.Unix(0, row.CreatedAt).UTC()}
	return value, nil
}

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func sessionFromDB(row dbgen.Session) Session {
	return Session{ID: row.ID, Title: row.Title, ProviderID: row.ProviderID, ModelID: row.ModelID, Reasoning: row.Reasoning, AgentMode: row.AgentMode, CreatedAt: time.Unix(0, row.CreatedAt).UTC(), UpdatedAt: time.Unix(0, row.UpdatedAt).UTC()}
}

func (s *Service) Ensure(ctx context.Context, value Session) (Session, error) {
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	queries := dbgen.New(s.db)
	err := queries.EnsureSession(ctx, dbgen.EnsureSessionParams{ID: value.ID, Title: value.Title, ProviderID: value.ProviderID, ModelID: value.ModelID, Reasoning: value.Reasoning, AgentMode: value.AgentMode, CreatedAt: value.CreatedAt.UnixNano(), UpdatedAt: value.UpdatedAt.UnixNano()})
	if err != nil {
		return Session{}, fmt.Errorf("ensure session: %w", err)
	}
	err = queries.EnsureSessionProjection(ctx, dbgen.EnsureSessionProjectionParams{SessionID: value.ID, UpdatedAt: now.UnixNano()})
	if err != nil {
		return Session{}, fmt.Errorf("ensure session projection: %w", err)
	}
	return s.LoadSession(ctx, value.ID)
}

func (s *Service) LoadSession(ctx context.Context, id string) (Session, error) {
	row, err := dbgen.New(s.db).GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("session %q not found", id)
	}
	if err != nil {
		return Session{}, fmt.Errorf("load session: %w", err)
	}
	value := sessionFromDB(row)
	return value, nil
}

func (s *Service) UpdatePreferences(ctx context.Context, id, providerID, modelID, reasoning, agentMode string) error {
	result, err := dbgen.New(s.db).UpdateSessionPreferences(ctx, dbgen.UpdateSessionPreferencesParams{ProviderID: providerID, ModelID: modelID, Reasoning: reasoning, AgentMode: agentMode, UpdatedAt: time.Now().UTC().UnixNano(), ID: id})
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

func (s *Service) Rename(ctx context.Context, id, title string) error {
	title, err := normalizeSessionTitle(title)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET title=? WHERE id=?`, title, id)
	if err != nil {
		return fmt.Errorf("rename session: %w", err)
	}
	return requireOneSession(result, id)
}

// RenameIfTitle updates a generated title only while the caller's observed
// placeholder is still current, so a concurrent manual rename always wins.
func (s *Service) RenameIfTitle(ctx context.Context, id, currentTitle, nextTitle string) (bool, error) {
	nextTitle, err := normalizeSessionTitle(nextTitle)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET title=? WHERE id=? AND title=?`, nextTitle, id, currentTitle)
	if err != nil {
		return false, fmt.Errorf("conditionally rename session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return changed == 1, nil
}

func normalizeSessionTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("session title is required")
	}
	if len([]rune(title)) > 200 {
		return "", fmt.Errorf("session title is too long")
	}
	return title, nil
}

func (s *Service) SetUIState(ctx context.Context, id, field string, enabled bool) error {
	column := ""
	switch field {
	case "pinned", "archived", "unread":
		column = field
	default:
		return fmt.Errorf("unsupported session UI field %q", field)
	}
	if _, err := s.LoadSession(ctx, id); err != nil {
		return err
	}
	value := 0
	if enabled {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO session_ui_state(session_id,`+column+`) VALUES(?,?)
		ON CONFLICT(session_id) DO UPDATE SET `+column+`=excluded.`+column, id, value)
	if err != nil {
		return fmt.Errorf("update session %s: %w", field, err)
	}
	return nil
}

func (s *Service) Fork(ctx context.Context, sourceID, targetID string) error {
	if err := validateSessionForkIDs(sourceID, targetID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixNano()
	result, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at)
		SELECT ?,title,provider_id,model_id,reasoning,agent_mode,?,? FROM sessions WHERE id=?`, targetID, now, now, sourceID)
	if err != nil {
		return fmt.Errorf("fork session: %w", err)
	}
	if err := requireOneSession(result, sourceID); err != nil {
		return err
	}
	copies := []struct {
		name  string
		query string
		args  []any
	}{
		{"projection", `INSERT INTO session_projections(session_id,last_run_id,blocks,updated_at,model_history,usage,checkpoint_generation,cache_epoch,cache_identity_hash)
			SELECT ?,'',blocks,?,model_history,'{}',checkpoint_generation,0,'' FROM session_projections WHERE session_id=?`, []any{targetID, now, sourceID}},
		{"blocks", `INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data)
			SELECT ?,sequence,kind,run_id,agent_id,data FROM session_blocks WHERE session_id=?`, []any{targetID, sourceID}},
		{"todo", `INSERT INTO session_todos(session_id,goal,revision,phases,updated_at)
			SELECT ?,goal,revision,phases,? FROM session_todos WHERE session_id=?`, []any{targetID, now, sourceID}},
		{"recap", `INSERT INTO recaps(session_id,anchor,covered_boundary,revision,goal,summary,open_items,updated_at)
			SELECT ?,anchor,covered_boundary,revision,goal,summary,open_items,? FROM recaps WHERE session_id=?`, []any{targetID, now, sourceID}},
		{"tools", `INSERT INTO session_tool_records(session_id,run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at)
			SELECT ?,run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at
			FROM session_tool_records WHERE session_id=? AND state<>'running'`, []any{targetID, sourceID}},
		{"project", `INSERT INTO session_workspaces(session_id,workspace,assigned_at)
			SELECT ?,workspace,? FROM session_workspaces WHERE session_id=?`, []any{targetID, now, sourceID}},
	}
	for _, copy := range copies {
		if _, err := tx.ExecContext(ctx, copy.query, copy.args...); err != nil {
			return fmt.Errorf("fork session %s: %w", copy.name, err)
		}
	}
	artifactIDs, err := cloneForkArtifacts(ctx, tx, sourceID, targetID)
	if err != nil {
		return err
	}
	if err := remapForkArtifactReferences(ctx, tx, targetID, artifactIDs); err != nil {
		return err
	}
	return tx.Commit()
}

type forkArtifact struct {
	id, runID, kind, hash, preview string
	payload                        []byte
	createdAt                      int64
}

func cloneForkArtifacts(ctx context.Context, tx *sql.Tx, sourceID, targetID string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,run_id,kind,sha256,payload,preview,created_at
		FROM context_artifacts WHERE session_id=? ORDER BY created_at,id`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("fork session artifacts: %w", err)
	}
	artifacts := []forkArtifact{}
	for rows.Next() {
		var artifact forkArtifact
		if err := rows.Scan(&artifact.id, &artifact.runID, &artifact.kind, &artifact.hash, &artifact.payload, &artifact.preview, &artifact.createdAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read fork session artifact: %w", err)
		}
		artifact.payload = append([]byte(nil), artifact.payload...)
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close fork session artifacts: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read fork session artifacts: %w", err)
	}
	ids := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		targetArtifactID := contextArtifactID(targetID, artifact.kind, artifact.hash)
		if _, err := tx.ExecContext(ctx, `INSERT INTO context_artifacts(id,session_id,run_id,kind,sha256,payload,preview,created_at)
			VALUES(?,?,?,?,?,?,?,?)`, targetArtifactID, targetID, artifact.runID, artifact.kind, artifact.hash, artifact.payload, artifact.preview, artifact.createdAt); err != nil {
			return nil, fmt.Errorf("clone fork session artifact %s: %w", artifact.id, err)
		}
		ids[artifact.id] = targetArtifactID
	}
	return ids, nil
}

func remapForkArtifactReferences(ctx context.Context, tx *sql.Tx, targetID string, ids map[string]string) error {
	for sourceArtifactID, targetArtifactID := range ids {
		updates := []struct {
			name  string
			query string
			args  []any
		}{
			{"projection", `UPDATE session_projections
				SET blocks=replace(CAST(blocks AS TEXT),?,?),model_history=replace(CAST(model_history AS TEXT),?,?)
				WHERE session_id=?`, []any{sourceArtifactID, targetArtifactID, sourceArtifactID, targetArtifactID, targetID}},
			{"blocks", `UPDATE session_blocks SET data=replace(CAST(data AS TEXT),?,?) WHERE session_id=?`, []any{sourceArtifactID, targetArtifactID, targetID}},
			{"todo", `UPDATE session_todos SET goal=replace(goal,?,?),phases=replace(CAST(phases AS TEXT),?,?) WHERE session_id=?`, []any{sourceArtifactID, targetArtifactID, sourceArtifactID, targetArtifactID, targetID}},
			{"recap", `UPDATE recaps SET goal=replace(goal,?,?),summary=replace(summary,?,?),open_items=replace(open_items,?,?) WHERE session_id=?`, []any{sourceArtifactID, targetArtifactID, sourceArtifactID, targetArtifactID, sourceArtifactID, targetArtifactID, targetID}},
			{"tools", `UPDATE session_tool_records
				SET arguments=replace(CAST(arguments AS TEXT),?,?),content=replace(content,?,?),
					structured=replace(CAST(structured AS TEXT),?,?),artifact_id=CASE WHEN artifact_id=? THEN ? ELSE artifact_id END
				WHERE session_id=?`, []any{sourceArtifactID, targetArtifactID, sourceArtifactID, targetArtifactID, sourceArtifactID, targetArtifactID, sourceArtifactID, targetArtifactID, targetID}},
		}
		for _, update := range updates {
			if _, err := tx.ExecContext(ctx, update.query, update.args...); err != nil {
				return fmt.Errorf("remap fork session %s artifact: %w", update.name, err)
			}
		}
	}
	return nil
}

func validateSessionForkIDs(sourceID, targetID string) error {
	if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(targetID) == "" || sourceID == targetID {
		return fmt.Errorf("source and target session IDs are required and must differ")
	}
	return nil
}

func requireOneSession(result sql.Result, id string) error {
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
	row, err := dbgen.New(s.db).GetSessionProjection(ctx, id)
	if err != nil {
		return Projection{}, fmt.Errorf("load projection: %w", err)
	}
	var blocks []Block
	if err := json.Unmarshal(row.Blocks, &blocks); err != nil {
		return Projection{}, fmt.Errorf("decode projection: %w", err)
	}
	var history ModelHistory
	if err := json.Unmarshal(row.ModelHistory, &history); err != nil {
		return Projection{}, fmt.Errorf("decode model history: %w", err)
	}
	usage, err := DecodeUsage(row.Usage)
	if err != nil {
		return Projection{}, err
	}
	blocks, err = loadSessionBlocks(ctx, s.db, id)
	if err != nil {
		return Projection{}, err
	}
	if len(blocks) == 0 && string(row.Blocks) != "[]" {
		if err := json.Unmarshal(row.Blocks, &blocks); err != nil {
			return Projection{}, fmt.Errorf("decode legacy projection: %w", err)
		}
	}
	tools, err := s.ListToolRecords(ctx, id)
	if err != nil {
		return Projection{}, err
	}
	return Projection{
		Session: value, LastRunID: row.LastRunID, Blocks: blocks, ToolRecords: tools, ModelHistory: history, Usage: usage,
		UpdatedAt:            time.Unix(0, row.UpdatedAt).UTC(),
		CheckpointGeneration: row.CheckpointGeneration, CacheEpoch: row.CacheEpoch, CacheIdentityHash: row.CacheIdentityHash,
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
	queries := dbgen.New(tx)
	if mutated && block.Kind == "assistant" {
		if err := queries.UpdateProjectionRunAfterAssistantMutation(ctx, dbgen.UpdateProjectionRunAfterAssistantMutationParams{LastRunID: block.RunID, HistorySequence: sequence, GenerationSequence: sequence, UpdatedAt: now, SessionID: sessionID}); err != nil {
			return 0, err
		}
	} else if err := queries.UpdateProjectionRun(ctx, dbgen.UpdateProjectionRunParams{LastRunID: block.RunID, UpdatedAt: now, SessionID: sessionID}); err != nil {
		return 0, err
	}
	if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: now, ID: sessionID}); err != nil {
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
	queries := dbgen.New(tx)
	checkpoint, err := queries.GetProjectionCheckpoint(ctx, sessionID)
	if err != nil {
		return err
	}
	activeRunID, currentGeneration := checkpoint.LastRunID, checkpoint.CheckpointGeneration
	if block.RunID != "" && activeRunID != "" && activeRunID != block.RunID {
		return fmt.Errorf("complete turn: active run changed from %q to %q", block.RunID, activeRunID)
	}
	thinking := block.Thinking
	block.Thinking = ""
	if strings.TrimSpace(thinking) != "" {
		thought := block
		thought.Kind = "thinking"
		thought.Title = "thinking"
		thought.Content = thinking
		if thought.State == "" {
			thought.State = "completed"
		}
		if _, _, err := appendSessionBlock(ctx, tx, sessionID, thought); err != nil {
			return err
		}
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
	result, err := queries.CompleteProjectionCAS(ctx, dbgen.CompleteProjectionCASParams{LastRunID: block.RunID, ModelHistory: encodedHistory, CheckpointGeneration: generation, UpdatedAt: now, SessionID: sessionID, LastRunID_2: activeRunID, CheckpointGeneration_2: currentGeneration})
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
	if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: now, ID: sessionID}); err != nil {
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
	queries := dbgen.New(tx)
	state, err := queries.GetRunCheckpointState(ctx, sessionID)
	if err != nil {
		return err
	}
	lastRunID, generation, cacheEpoch, currentIdentity := state.LastRunID, state.CheckpointGeneration, state.CacheEpoch, state.CacheIdentityHash
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
		encoded, err := queries.GetProjectionHistory(ctx, sessionID)
		if err != nil {
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
	result, err := queries.SaveRunCheckpointCAS(ctx, dbgen.SaveRunCheckpointCASParams{ModelHistory: encoded, CheckpointGeneration: generation + 1, CacheEpoch: nextCacheEpoch, CacheIdentityHash: checkpoint.CacheIdentity, UpdatedAt: now, SessionID: sessionID, LastRunID: checkpoint.RunID, CheckpointGeneration_2: generation})
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
	if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: now, ID: sessionID}); err != nil {
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
	queries := dbgen.New(tx)
	result, err := queries.UpdateAgentBlock(ctx, dbgen.UpdateAgentBlockParams{RunID: block.RunID, Data: encoded, SessionID: sessionID, AgentID: agentID})
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
	if err := queries.TouchProjection(ctx, dbgen.TouchProjectionParams{UpdatedAt: now, SessionID: sessionID}); err != nil {
		return err
	}
	if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: now, ID: sessionID}); err != nil {
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
	queries := dbgen.New(tx)
	state, err := queries.GetCompactionState(ctx, sessionID)
	if err != nil {
		return Projection{}, err
	}
	historyData, projectionUpdated, generation, cacheEpoch := state.ModelHistory, state.UpdatedAt, state.CheckpointGeneration, state.CacheEpoch
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
	if err := queries.SaveCompaction(ctx, dbgen.SaveCompactionParams{ModelHistory: encodedHistory, CheckpointGeneration: generation + 1, CacheEpoch: cacheEpoch + 1, UpdatedAt: now, SessionID: sessionID}); err != nil {
		return Projection{}, err
	}
	if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: now, ID: sessionID}); err != nil {
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

func loadSessionBlocks(ctx context.Context, queryer dbgen.DBTX, sessionID string) ([]Block, error) {
	rows, err := dbgen.New(queryer).ListSessionBlocks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session blocks: %w", err)
	}
	blocks := make([]Block, 0, len(rows))
	for _, row := range rows {
		var block Block
		if err := json.Unmarshal(row.Data, &block); err != nil {
			return nil, fmt.Errorf("decode session block: %w", err)
		}
		block.Sequence = row.Sequence
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func appendSessionBlock(ctx context.Context, tx *sql.Tx, sessionID string, block Block) (int64, bool, error) {
	row, err := dbgen.New(tx).GetLatestSessionBlock(ctx, sessionID)
	sequence, data := row.Sequence, row.Data
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
			err = dbgen.New(tx).UpdateSessionBlockData(ctx, dbgen.UpdateSessionBlockDataParams{Data: encoded, SessionID: sessionID, Sequence: sequence})
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

func canonicalHighWater(ctx context.Context, queryer dbgen.DBTX, sessionID string) (*int64, error) {
	value, err := dbgen.New(queryer).CanonicalHighWater(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func insertSessionBlock(ctx context.Context, tx *sql.Tx, sessionID string, block Block, encoded []byte) error {
	return dbgen.New(tx).InsertSessionBlock(ctx, dbgen.InsertSessionBlockParams{SessionID: sessionID, Kind: block.Kind, RunID: block.RunID, AgentID: block.AgentID, Data: encoded, SessionID_2: sessionID})
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
	queries := dbgen.New(s.db)
	// ponytail: session history is user-local; move pin ordering into SQL if lists grow into thousands.
	rows, err := queries.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]Session, 0, len(rows))
	for _, row := range rows {
		values = append(values, sessionFromDB(row))
	}
	states, err := s.sessionUIStates(ctx)
	if err != nil {
		return nil, err
	}
	for index := range values {
		state := states[values[index].ID]
		values[index].Pinned, values[index].Archived, values[index].Unread = state[0], state[1], state[2]
	}
	workspaces, err := s.sessionWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	for index := range values {
		values[index].Workspace = workspaces[values[index].ID]
	}
	sort.SliceStable(values, func(left, right int) bool { return values[left].Pinned && !values[right].Pinned })
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func (s *Service) sessionUIStates(ctx context.Context) (map[string][3]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id,pinned,archived,unread FROM session_ui_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[string][3]bool)
	for rows.Next() {
		var id string
		var pinned, archived, unread int
		if err := rows.Scan(&id, &pinned, &archived, &unread); err != nil {
			return nil, err
		}
		states[id] = [3]bool{pinned != 0, archived != 0, unread != 0}
	}
	return states, rows.Err()
}
