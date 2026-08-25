package session

import (
	"bytes"
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

	"github.com/Viking602/azem/internal/blobstore"
	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
	"github.com/Viking602/venat/message"
)

var ErrSessionNotFound = errors.New("session not found")

type Session struct {
	ID         string    `json:"id"`
	Workspace  string    `json:"workspace"`
	Title      string    `json:"title"`
	ProviderID string    `json:"providerId,omitempty"`
	ModelID    string    `json:"modelId,omitempty"`
	Reasoning  string    `json:"reasoning,omitempty"`
	AgentMode  string    `json:"agentMode,omitempty"`
	Pinned     bool      `json:"pinned,omitempty"`
	Archived   bool      `json:"archived,omitempty"`
	Unread     bool      `json:"unread,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
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
	ImportedMessage  json.RawMessage   `json:"importedMessage,omitempty"`
	Thinking         string            `json:"-"`
}

// ModelHistory is a replaceable provider-resume checkpoint, not the durable
// conversation record. CompleteTurn installs it atomically; independent
// transcript mutations invalidate it, while deterministic context archiving
// replaces the transcript-derived checkpoint in one transaction.
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
	ContextManifestHash    string            `json:"contextManifestHash,omitempty"`
	PolicyVersion          int               `json:"policyVersion,omitempty"`
}

const CurrentWireVersion = 3

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

type ArchivePlan struct {
	ModelHistory      ModelHistory
	ExpectedUpdatedAt time.Time
	ExpectedHighWater *int64
	Manifest          *ContextManifestRecord
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
	Manifest          *ContextManifestRecord
}

type Service struct {
	db    *sql.DB
	blobs blobstore.Store
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

const (
	maxContextArtifactPayloadBytes = 64 << 20
	// InternalArtifactKindPrefix marks durable control-plane artifacts that
	// must never enter ordinary conversation-history recall.
	InternalArtifactKindPrefix = "azem-internal/"
)

var ErrContextArtifactNotFound = errors.New("session: context artifact not found")

type limitedBlobStore interface {
	GetLimited(context.Context, string, int64) ([]byte, error)
}

type HistoryRecord struct {
	SessionID  string `json:"sessionId"`
	SourceType string `json:"sourceType"`
	SourceID   string `json:"sourceId"`
	Preview    string `json:"preview,omitempty"`
	Content    string `json:"content,omitempty"`
}

// SessionSearchResult is the bounded desktop projection for global search.
// Message results contain only an FTS-generated snippet and the durable block
// sequence required to focus the matching transcript entry.
type SessionSearchResult struct {
	SessionID string    `json:"sessionId"`
	Workspace string    `json:"workspace"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Preview   string    `json:"preview,omitempty"`
	Sequence  int64     `json:"sequence"`
	UpdatedAt time.Time `json:"updatedAt"`
}

const (
	defaultHistoryLimit = 8
	maxHistoryLimit     = 20
	defaultSearchLimit  = 20
	maxSearchLimit      = 30
	maxSearchQueryRunes = 200
)

// SearchSessions searches session titles and durable canonical conversation
// blocks across every project. Conversation content stays in SQLite: only a
// short FTS snippet is returned to the desktop renderer.
func (s *Service) SearchSessions(ctx context.Context, query string, limit int) ([]SessionSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	query = truncateRunes(query, maxSearchQueryRunes)
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	results := make([]SessionSearchResult, 0, limit)
	titleRows, err := s.db.QueryContext(ctx, `SELECT s.id,COALESCE(sw.workspace,''),s.title,s.updated_at
		FROM sessions s
		LEFT JOIN session_workspaces sw ON sw.session_id=s.id
		WHERE instr(lower(s.title),lower(?))>0
		ORDER BY CASE
			WHEN lower(s.title)=lower(?) THEN 0
			WHEN instr(lower(s.title),lower(?))=1 THEN 1
			ELSE 2 END,
			s.updated_at DESC
		LIMIT ?`, query, query, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search session titles: %w", err)
	}
	for titleRows.Next() {
		var result SessionSearchResult
		var updatedAt int64
		if err := titleRows.Scan(&result.SessionID, &result.Workspace, &result.Title, &updatedAt); err != nil {
			titleRows.Close()
			return nil, err
		}
		result.Kind = "title"
		result.UpdatedAt = time.Unix(0, updatedAt).UTC()
		results = append(results, result)
	}
	if err := titleRows.Err(); err != nil {
		titleRows.Close()
		return nil, err
	}
	if err := titleRows.Close(); err != nil {
		return nil, err
	}
	if len(results) >= limit {
		return results[:limit], nil
	}

	match := safeSessionSearchMatch(query)
	if match == "" {
		return results, nil
	}
	remaining := limit - len(results)
	rows, err := s.db.QueryContext(ctx, `SELECT f.session_id,COALESCE(sw.workspace,''),s.title,
		b.kind,b.sequence,snippet(history_fts,0,'','',' … ',24),s.updated_at
		FROM history_fts f
		JOIN sessions s ON s.id=f.session_id
		JOIN session_blocks b ON b.session_id=f.session_id AND f.source_type='sequence' AND 'sequence:'||b.sequence=f.source_id
		LEFT JOIN session_workspaces sw ON sw.session_id=f.session_id
		WHERE history_fts MATCH ? AND
			(b.kind='user' OR (b.kind='assistant' AND COALESCE(json_extract(b.data,'$.state'),'') IN ('','completed')))
		ORDER BY bm25(history_fts),s.updated_at DESC
		LIMIT ?`, match, remaining)
	if err != nil {
		return nil, fmt.Errorf("search session messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var result SessionSearchResult
		var updatedAt int64
		if err := rows.Scan(&result.SessionID, &result.Workspace, &result.Title, &result.Kind, &result.Sequence, &result.Preview, &updatedAt); err != nil {
			return nil, err
		}
		result.Preview = strings.TrimSpace(result.Preview)
		result.UpdatedAt = time.Unix(0, updatedAt).UTC()
		results = append(results, result)
	}
	return results, rows.Err()
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

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
			(f.source_type='artifact' AND EXISTS(SELECT 1 FROM context_artifacts a WHERE a.session_id=f.session_id
				AND a.kind NOT LIKE ? AND 'artifact:'||a.id=f.source_id)))
		ORDER BY bm25(history_fts) LIMIT ?`, match, sessionID, InternalArtifactKindPrefix+"%", limit)
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
	return safeFTSMatch(query, " OR ")
}

func safeSessionSearchMatch(query string) string {
	return safeFTSMatch(query, " AND ")
}

func safeFTSMatch(query, operator string) string {
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
	return strings.Join(quoted, operator)
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
func (s *Service) PutArtifact(ctx context.Context, sessionID, runID, kind string, payload []byte, preview string) (artifact ContextArtifact, err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(kind) == "" {
		return ContextArtifact{}, fmt.Errorf("artifact session and kind are required")
	}
	if len(payload) > maxContextArtifactPayloadBytes {
		return ContextArtifact{}, fmt.Errorf("context artifact exceeds %d-byte limit", maxContextArtifactPayloadBytes)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ContextArtifact{}, err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return ContextArtifact{}, fmt.Errorf("lock context artifact catalog: %w", err)
	}
	hash, err := s.installTrackedBlob(ctx, payload)
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("put context artifact blob: %w", err)
	}
	preview = BuildArtifactPreviewV2(kind, payload, hash, preview)
	id := contextArtifactID(sessionID, kind, hash)
	now := time.Now().UTC()
	if err := dbgen.New(tx).InsertContextArtifact(ctx, dbgen.InsertContextArtifactParams{ID: id, SessionID: sessionID, RunID: runID, Kind: kind, Sha256: hash, Preview: preview, CreatedAt: now.UnixNano()}); err != nil {
		return ContextArtifact{}, fmt.Errorf("put context artifact: %w", err)
	}
	if err := s.commitBlobTransaction(ctx, tx, tracker, trackerOwner); err != nil {
		return ContextArtifact{}, fmt.Errorf("commit context artifact: %w", err)
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
		return ContextArtifact{}, fmt.Errorf("%w: %q in session %q", ErrContextArtifactNotFound, id, sessionID)
	}
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("load context artifact: %w", err)
	}
	return s.loadArtifactRow(ctx, row)
}

func (s *Service) LoadLatestArtifactByKind(ctx context.Context, sessionID, kind string) (ContextArtifact, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(kind) == "" {
		return ContextArtifact{}, errors.New("session id and artifact kind are required")
	}
	row, err := dbgen.New(s.db).GetLatestContextArtifactByKind(ctx, dbgen.GetLatestContextArtifactByKindParams{SessionID: sessionID, Kind: kind})
	if errors.Is(err, sql.ErrNoRows) {
		return ContextArtifact{}, fmt.Errorf("%w: kind %q in session %q", ErrContextArtifactNotFound, kind, sessionID)
	}
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("load latest context artifact: %w", err)
	}
	return s.loadArtifactRow(ctx, row)
}

func (s *Service) LoadLatestArtifactByKindPrefix(ctx context.Context, sessionID, kindPrefix string) (ContextArtifact, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(kindPrefix) == "" {
		return ContextArtifact{}, errors.New("session id and artifact kind prefix are required")
	}
	row, err := dbgen.New(s.db).GetLatestContextArtifactByKindPrefix(ctx, dbgen.GetLatestContextArtifactByKindPrefixParams{
		SessionID: sessionID, KindPrefix: sql.NullString{String: kindPrefix, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ContextArtifact{}, fmt.Errorf("%w: kind prefix %q in session %q", ErrContextArtifactNotFound, kindPrefix, sessionID)
	}
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("load latest context artifact by kind prefix: %w", err)
	}
	return s.loadArtifactRow(ctx, row)
}

func (s *Service) loadArtifactRow(ctx context.Context, row dbgen.ContextArtifact) (ContextArtifact, error) {
	if strings.HasPrefix(row.ID, "artifact_") {
		if expectedID := contextArtifactID(row.SessionID, row.Kind, row.Sha256); row.ID != expectedID {
			return ContextArtifact{}, fmt.Errorf("context artifact %q failed identity check", row.ID)
		}
	}
	var (
		payload []byte
		err     error
	)
	if limited, ok := s.blobs.(limitedBlobStore); ok {
		payload, err = limited.GetLimited(ctx, row.Sha256, maxContextArtifactPayloadBytes)
	} else {
		payload, err = s.blobs.Get(ctx, row.Sha256)
	}
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("load context artifact payload: %w", err)
	}
	if actual := blobstore.Sum(payload); actual != row.Sha256 {
		return ContextArtifact{}, fmt.Errorf("context artifact %q failed integrity check: got sha256 %s", row.ID, actual)
	}
	return ContextArtifact{
		ID: row.ID, SessionID: row.SessionID, RunID: row.RunID, Kind: row.Kind, SHA256: row.Sha256,
		Payload: payload, Preview: row.Preview, CreatedAt: time.Unix(0, row.CreatedAt).UTC(),
	}, nil
}

// UpdateLatestBlockState updates the newest matching durable UI block without
// appending a second transcript entry. It is used for interactive lifecycle
// records such as questions and plan proposals, whose content is immutable but
// whose review state must survive an application restart.
func (s *Service) UpdateLatestBlockState(ctx context.Context, sessionID, kind, dataKey, dataValue, expectedState, nextState string, data map[string]string) (blockResult Block, err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Block{}, err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return Block{}, err
	}
	rows, err := dbgen.New(tx).ListSessionBlocks(ctx, sessionID)
	if err != nil {
		return Block{}, err
	}
	for index := len(rows) - 1; index >= 0; index-- {
		encoded, err := s.decodeBlockJSON(ctx, rows[index].Data, rows[index].DataSha256)
		if err != nil {
			return Block{}, err
		}
		block, matches, err := matchingLifecycleBlock(encoded, kind, dataKey, dataValue, expectedState)
		if err != nil {
			return Block{}, err
		}
		if !matches {
			continue
		}
		setLifecycleBlockState(&block, nextState, data)
		encoded, err = json.Marshal(block)
		if err != nil {
			return Block{}, err
		}
		inline, digest, err := s.encodeBlockData(ctx, block, encoded)
		if err != nil {
			return Block{}, err
		}
		queries := dbgen.New(tx)
		if err := queries.UpdateSessionBlockData(ctx, dbgen.UpdateSessionBlockDataParams{Data: inline, DataSha256: digest, SessionID: sessionID, Sequence: rows[index].Sequence}); err != nil {
			return Block{}, err
		}
		now := time.Now().UTC().UnixNano()
		if err := queries.TouchProjection(ctx, dbgen.TouchProjectionParams{UpdatedAt: now, SessionID: sessionID}); err != nil {
			return Block{}, err
		}
		if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: now, ID: sessionID}); err != nil {
			return Block{}, err
		}
		block.Sequence = rows[index].Sequence
		return block, s.commitBlobTransaction(ctx, tx, tracker, trackerOwner)
	}
	return Block{}, fmt.Errorf("matching %s block was not found", kind)
}

func matchingLifecycleBlock(encoded []byte, kind, dataKey, dataValue, expectedState string) (Block, bool, error) {
	var block Block
	if err := json.Unmarshal(encoded, &block); err != nil {
		return Block{}, false, fmt.Errorf("decode session block: %w", err)
	}
	if block.Kind != kind || (expectedState != "" && block.State != expectedState) {
		return block, false, nil
	}
	if dataKey != "" && (block.Data == nil || block.Data[dataKey] != dataValue) {
		return block, false, nil
	}
	return block, true, nil
}

func setLifecycleBlockState(block *Block, state string, data map[string]string) {
	block.State = state
	if block.Data == nil {
		block.Data = map[string]string{}
	}
	for key, value := range data {
		block.Data[key] = value
	}
}

func NewService(db *sql.DB, blobs blobstore.Store) *Service {
	if blobs == nil {
		blobs = blobstore.NewMemory()
	}
	return &Service{db: db, blobs: blobs}
}

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
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
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

const archivedSessionListCap = 500

// SetArchived toggles the archived flag. Restoring a previously archived
// session refreshes its updated timestamp so it returns to the active list.
func (s *Service) SetArchived(ctx context.Context, id string, archived bool) error {
	if _, err := s.LoadSession(ctx, id); err != nil {
		return err
	}
	var current int
	err := s.db.QueryRowContext(ctx, `SELECT archived FROM session_ui_state WHERE session_id=?`, id).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read session archive state: %w", err)
	}
	if err := s.SetUIState(ctx, id, "archived", archived); err != nil {
		return err
	}
	if archived || current == 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, time.Now().UTC().UnixNano(), id); err != nil {
		return fmt.Errorf("touch restored session: %w", err)
	}
	return nil
}

// ArchiveInactive archives unpinned, unarchived sessions whose last update is
// older than olderThan. skipIDs are left in the active list, typically the
// currently visible session.
func (s *Service) ArchiveInactive(ctx context.Context, olderThan time.Duration, skipIDs ...string) (int, error) {
	if olderThan <= 0 {
		return 0, fmt.Errorf("archive inactivity threshold must be positive")
	}
	sessions, err := s.List(ctx, 0)
	if err != nil {
		return 0, err
	}
	skip := make(map[string]struct{}, len(skipIDs))
	for _, id := range skipIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			skip[id] = struct{}{}
		}
	}
	cutoff := time.Now().UTC().Add(-olderThan)
	archived := 0
	for _, item := range sessions {
		if item.Archived || item.Pinned {
			continue
		}
		if _, found := skip[item.ID]; found {
			continue
		}
		if !item.UpdatedAt.Before(cutoff) {
			continue
		}
		if err := s.SetUIState(ctx, item.ID, "archived", true); err != nil {
			return archived, err
		}
		archived++
	}
	return archived, nil
}

func (s *Service) Fork(ctx context.Context, sourceID, targetID string) error {
	return s.fork(ctx, sourceID, targetID, "", false)
}

// ForkAt creates an independent session containing only the root-to-entry path.
// An empty entry starts the fork at the graph root with no transcript blocks.
func (s *Service) ForkAt(ctx context.Context, sourceID, targetID, entryID string) error {
	return s.fork(ctx, sourceID, targetID, entryID, true)
}

func (s *Service) fork(ctx context.Context, sourceID, targetID, entryID string, pathOnly bool) (err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	if err := validateSessionForkIDs(sourceID, targetID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return err
	}
	if pathOnly {
		if err := requireGraphEntry(ctx, tx, sourceID, entryID); err != nil {
			return err
		}
	}
	now := time.Now().UTC().UnixNano()
	result, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at)
		SELECT ?,title,provider_id,model_id,reasoning,agent_mode,?,? FROM sessions WHERE id=?`, targetID, now, now, sourceID)
	if err != nil {
		return fmt.Errorf("fork session: %w", err)
	}
	if err := requireOneSession(result, sourceID); err != nil {
		return err
	}
	blockCopy := `INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data,data_sha256)
		SELECT ?,sequence,kind,run_id,agent_id,data,data_sha256 FROM session_blocks WHERE session_id=? ORDER BY sequence`
	blockArgs := []any{targetID, sourceID}
	toolCopy := `INSERT INTO session_tool_records(session_id,run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at,content_sha256,structured_sha256)
		SELECT ?,run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at,content_sha256,structured_sha256
		FROM session_tool_records WHERE session_id=? AND state<>'running'`
	toolArgs := []any{targetID, sourceID}
	if pathOnly {
		blockCopy = `WITH RECURSIVE branch(entry_id,parent_entry_id,block_sequence) AS (
				SELECT entry_id,parent_entry_id,block_sequence FROM session_graph_entries WHERE session_id=? AND entry_id=?
				UNION ALL
				SELECT entry.entry_id,entry.parent_entry_id,entry.block_sequence
				FROM session_graph_entries entry JOIN branch ON entry.entry_id=branch.parent_entry_id WHERE entry.session_id=?
			)
			INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data,data_sha256)
			SELECT ?,block.sequence,block.kind,block.run_id,block.agent_id,block.data,block.data_sha256
			FROM session_blocks block JOIN branch ON branch.block_sequence=block.sequence
			WHERE block.session_id=? ORDER BY block.sequence`
		blockArgs = []any{sourceID, entryID, sourceID, targetID, sourceID}
		toolCopy = `WITH RECURSIVE branch(entry_id,parent_entry_id,block_sequence) AS (
				SELECT entry_id,parent_entry_id,block_sequence FROM session_graph_entries WHERE session_id=? AND entry_id=?
				UNION ALL
				SELECT entry.entry_id,entry.parent_entry_id,entry.block_sequence
				FROM session_graph_entries entry JOIN branch ON entry.entry_id=branch.parent_entry_id WHERE entry.session_id=?
			)
			INSERT INTO session_tool_records(session_id,run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at,content_sha256,structured_sha256)
			SELECT ?,record.run_id,record.tool_call_id,record.anchor_sequence,record.name,record.arguments,record.state,record.content,record.structured,record.artifact_id,record.observations,record.started_at,record.completed_at,record.content_sha256,record.structured_sha256
			FROM session_tool_records record JOIN branch ON branch.block_sequence=record.anchor_sequence
			WHERE record.session_id=? AND record.state<>'running'`
		toolArgs = []any{sourceID, entryID, sourceID, targetID, sourceID}
	}
	copies := []struct {
		name  string
		query string
		args  []any
	}{
		{"projection", `INSERT INTO session_projections(session_id,last_run_id,updated_at,model_history,usage,checkpoint_generation,cache_epoch,cache_identity_hash,model_history_sha256)
			SELECT ?,'',?,'{}','{}',0,0,'','' FROM session_projections WHERE session_id=?`, []any{targetID, now, sourceID}},
		{"blocks", blockCopy, blockArgs},
		{"tools", toolCopy, toolArgs},
		{"project", `INSERT INTO session_workspaces(session_id,workspace,assigned_at)
			SELECT ?,workspace,? FROM session_workspaces WHERE session_id=?`, []any{targetID, now, sourceID}},
	}
	if !pathOnly {
		copies = append(copies,
			struct {
				name  string
				query string
				args  []any
			}{"todo", `INSERT INTO session_todos(session_id,goal,revision,phases,updated_at)
				SELECT ?,goal,revision,phases,? FROM session_todos WHERE session_id=?`, []any{targetID, now, sourceID}},
			struct {
				name  string
				query string
				args  []any
			}{"recap", `INSERT INTO recaps(session_id,anchor,covered_boundary,revision,goal,summary,open_items,updated_at)
				SELECT ?,anchor,covered_boundary,revision,goal,summary,open_items,? FROM recaps WHERE session_id=?`, []any{targetID, now, sourceID}},
		)
	}
	for _, copy := range copies {
		if _, err := tx.ExecContext(ctx, copy.query, copy.args...); err != nil {
			return fmt.Errorf("fork session %s: %w", copy.name, err)
		}
	}
	if err := configureForkGraph(ctx, tx, sourceID, targetID, entryID, pathOnly, now); err != nil {
		return err
	}
	artifactIDs, err := s.cloneForkArtifacts(ctx, tx, sourceID, targetID)
	if err != nil {
		return err
	}
	if err := s.remapForkArtifactReferences(ctx, tx, targetID, artifactIDs); err != nil {
		return err
	}
	return s.commitBlobTransaction(ctx, tx, tracker, trackerOwner)
}

type forkArtifact struct {
	id, runID, kind, hash, preview string
	createdAt                      int64
}

func (s *Service) cloneForkArtifacts(ctx context.Context, tx *sql.Tx, sourceID, targetID string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,run_id,kind,sha256,preview,created_at
		FROM context_artifacts WHERE session_id=? AND kind<>'context_archive' ORDER BY created_at,id`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("fork session artifacts: %w", err)
	}
	artifacts := []forkArtifact{}
	for rows.Next() {
		var artifact forkArtifact
		if err := rows.Scan(&artifact.id, &artifact.runID, &artifact.kind, &artifact.hash, &artifact.preview, &artifact.createdAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read fork session artifact: %w", err)
		}
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO context_artifacts(id,session_id,run_id,kind,sha256,preview,created_at)
			VALUES(?,?,?,?,?,?,?)`, targetArtifactID, targetID, artifact.runID, artifact.kind, artifact.hash, artifact.preview, artifact.createdAt); err != nil {
			return nil, fmt.Errorf("clone fork session artifact %s: %w", artifact.id, err)
		}
		ids[artifact.id] = targetArtifactID
	}
	return ids, nil
}

func (s *Service) remapForkArtifactReferences(ctx context.Context, tx *sql.Tx, targetID string, ids map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := remapForkInlineArtifactReferences(ctx, tx, targetID, ids); err != nil {
		return err
	}
	if err := s.remapForkBlockPayloads(ctx, tx, targetID, ids); err != nil {
		return err
	}
	if err := s.remapForkToolContents(ctx, tx, targetID, ids); err != nil {
		return err
	}
	return nil
}

func remapForkInlineArtifactReferences(ctx context.Context, tx *sql.Tx, targetID string, ids map[string]string) error {
	for sourceArtifactID, targetArtifactID := range ids {
		updates := []struct {
			name  string
			query string
			args  []any
		}{
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

func (s *Service) remapForkBlockPayloads(ctx context.Context, tx *sql.Tx, targetID string, ids map[string]string) error {
	rows, err := tx.QueryContext(ctx, `SELECT sequence,data,data_sha256 FROM session_blocks WHERE session_id=?`, targetID)
	if err != nil {
		return fmt.Errorf("list fork session blocks: %w", err)
	}
	type row struct {
		sequence int64
		data     []byte
		digest   string
	}
	var items []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.sequence, &item.data, &item.digest); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		payload, err := s.decodeBlockJSON(ctx, item.data, item.digest)
		if err != nil {
			return fmt.Errorf("load fork session block %d: %w", item.sequence, err)
		}
		next := replaceArtifactIDs(payload, ids)
		if string(next) == string(payload) {
			continue
		}
		var block Block
		if err := json.Unmarshal(next, &block); err != nil {
			return fmt.Errorf("decode remapped session block %d: %w", item.sequence, err)
		}
		inline, digest, err := s.encodeBlockData(ctx, block, next)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE session_blocks SET data=?, data_sha256=? WHERE session_id=? AND sequence=?`,
			inline, digest, targetID, item.sequence); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) remapForkToolContents(ctx context.Context, tx *sql.Tx, targetID string, ids map[string]string) error {
	rows, err := tx.QueryContext(ctx, `SELECT run_id,tool_call_id,content,content_sha256,structured,structured_sha256
		FROM session_tool_records WHERE session_id=? AND (content_sha256<>'' OR structured_sha256<>'')`, targetID)
	if err != nil {
		return fmt.Errorf("list fork spilled tool payloads: %w", err)
	}
	type row struct {
		runID, callID, content, contentDigest, structuredDigest string
		structured                                              []byte
	}
	var items []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.runID, &item.callID, &item.content, &item.contentDigest, &item.structured, &item.structuredDigest); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		content, err := s.loadText(ctx, item.content, item.contentDigest)
		if err != nil {
			return fmt.Errorf("load fork tool content %s: %w", item.callID, err)
		}
		structured, err := s.loadBytes(ctx, item.structured, item.structuredDigest)
		if err != nil {
			return fmt.Errorf("load fork tool structured payload %s: %w", item.callID, err)
		}
		nextContent := string(replaceArtifactIDs([]byte(content), ids))
		nextStructured := replaceArtifactIDs(structured, ids)
		if nextContent == content && bytes.Equal(nextStructured, structured) {
			continue
		}
		contentInline, contentDigest, err := s.spillText(ctx, nextContent)
		if err != nil {
			return err
		}
		structuredInline, structuredDigest, err := s.spillBytes(ctx, nextStructured)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE session_tool_records
			SET content=?,content_sha256=?,structured=?,structured_sha256=?
			WHERE session_id=? AND run_id=? AND tool_call_id=?`,
			contentInline, contentDigest, structuredInline, structuredDigest, targetID, item.runID, item.callID); err != nil {
			return err
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
	history, err := s.decodeModelHistory(ctx, row.ModelHistory)
	if err != nil {
		return Projection{}, fmt.Errorf("decode model history: %w", err)
	}
	usage, err := DecodeUsage(row.Usage)
	if err != nil {
		return Projection{}, err
	}
	blocks, err := s.loadSessionBlocks(ctx, s.db, id)
	if err != nil {
		return Projection{}, err
	}
	blocks, err = filterBlocksToActiveSessionBranch(ctx, s.db, id, blocks)
	if err != nil {
		return Projection{}, err
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

func (s *Service) AppendBlock(ctx context.Context, sessionID string, block Block) (sequenceResult int64, err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return 0, err
	}
	sequence, mutated, err := s.appendSessionBlock(ctx, tx, sessionID, block)
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
	return sequence, s.commitBlobTransaction(ctx, tx, tracker, trackerOwner)
}

func (s *Service) CompleteTurn(ctx context.Context, sessionID string, block Block, history ModelHistory) (err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return err
	}
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
		if _, _, err := s.appendSessionBlock(ctx, tx, sessionID, thought); err != nil {
			return err
		}
	}
	if strings.TrimSpace(block.Content) != "" {
		if _, _, err := s.appendSessionBlock(ctx, tx, sessionID, block); err != nil {
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
	boundary, err := canonicalHighWater(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	history.CoveredThroughSequence = boundary
	generation := currentGeneration + 1
	history.Generation = generation
	encodedHistory, err := s.encodeModelHistory(ctx, history)
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
	return s.commitBlobTransaction(ctx, tx, tracker, trackerOwner)
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
func (s *Service) SaveRunCheckpoint(ctx context.Context, sessionID string, checkpoint RunCheckpoint) (err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	if strings.TrimSpace(checkpoint.RunID) == "" || len(checkpoint.ModelHistory.Messages) == 0 || strings.TrimSpace(checkpoint.CacheIdentity) == "" {
		return fmt.Errorf("save run checkpoint: run, history, and cache identity are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return err
	}
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
	if checkpoint.Manifest != nil {
		history.WireVersion = CurrentWireVersion
	}
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
	if checkpoint.ExpectedHighWater != nil && !sameSequence(boundary, checkpoint.ExpectedHighWater) {
		return fmt.Errorf("%w: canonical transcript changed while checkpoint was prepared", ErrRunCheckpointStale)
	}
	now := time.Now().UTC().UnixNano()
	if checkpoint.Manifest != nil {
		history.ContextManifestHash = checkpoint.Manifest.ManifestHash
		history.PolicyVersion = checkpoint.Manifest.PolicyVersion
	}
	if currentIdentity == checkpoint.CacheIdentity && checkpoint.Manifest == nil {
		encoded, err := queries.GetProjectionHistory(ctx, sessionID)
		if err != nil {
			return err
		}
		current, decodeErr := s.decodeModelHistory(ctx, encoded)
		if decodeErr == nil && reflect.DeepEqual(normalizeMessageTimes(current.Messages), normalizeMessageTimes(history.Messages)) {
			return s.commitBlobTransaction(ctx, tx, tracker, trackerOwner)
		}
	}
	history.CoveredThroughSequence = checkpoint.ExpectedHighWater
	history.Generation = generation + 1
	encoded, err := s.encodeModelHistory(ctx, history)
	if err != nil {
		return fmt.Errorf("encode run checkpoint: %w", err)
	}
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
	if err := persistContextManifest(ctx, queries, sessionID, checkpoint.Manifest, now); err != nil {
		return err
	}
	if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: now, ID: sessionID}); err != nil {
		return err
	}
	return s.commitBlobTransaction(ctx, tx, tracker, trackerOwner)
}

func normalizeMessageTimes(messages []message.Message) []message.Message {
	result := append([]message.Message(nil), messages...)
	for index := range result {
		result[index].CreatedAt = time.Time{}
	}
	return result
}

func (s *Service) UpsertAgentBlock(ctx context.Context, sessionID, agentID string, block Block) (err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
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
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return err
	}
	encoded, err := json.Marshal(block)
	if err != nil {
		return err
	}
	inline, digest, err := s.encodeBlockData(ctx, block, encoded)
	if err != nil {
		return err
	}
	queries := dbgen.New(tx)
	result, err := queries.UpdateAgentBlock(ctx, dbgen.UpdateAgentBlockParams{RunID: block.RunID, Data: inline, DataSha256: digest, SessionID: sessionID, AgentID: agentID})
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		if err := s.insertSessionBlock(ctx, tx, sessionID, block, inline, digest); err != nil {
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
	return s.commitBlobTransaction(ctx, tx, tracker, trackerOwner)
}

// ActivateArchiveCheckpoint installs a manually prepared archive checkpoint
// without changing the canonical transcript.
func (s *Service) ActivateArchiveCheckpoint(ctx context.Context, sessionID string, plan ArchivePlan) (projectionResult Projection, err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	if len(plan.ModelHistory.Messages) == 0 || plan.Manifest == nil {
		return Projection{}, fmt.Errorf("activate archive checkpoint: history and manifest are required")
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
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return Projection{}, err
	}
	queries := dbgen.New(tx)
	state, err := queries.GetCompactionState(ctx, sessionID)
	if err != nil {
		return Projection{}, err
	}
	historyData, projectionUpdated, generation, cacheEpoch := state.ModelHistory, state.UpdatedAt, state.CheckpointGeneration, state.CacheEpoch
	if !plan.ExpectedUpdatedAt.IsZero() && projectionUpdated != plan.ExpectedUpdatedAt.UnixNano() {
		return Projection{}, fmt.Errorf("compact session: projection changed while summary was generated")
	}
	decodedHistory, err := s.decodeModelHistory(ctx, historyData)
	if err != nil {
		return Projection{}, fmt.Errorf("decode model history for compaction: %w", err)
	}
	projection.ModelHistory = decodedHistory
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
	if plan.Manifest != nil {
		plan.ModelHistory.ContextManifestHash = plan.Manifest.ManifestHash
		plan.ModelHistory.PolicyVersion = plan.Manifest.PolicyVersion
	}
	encodedHistory, err := s.encodeModelHistory(ctx, plan.ModelHistory)
	if err != nil {
		return Projection{}, fmt.Errorf("encode compacted model history: %w", err)
	}
	if err := queries.SaveCompaction(ctx, dbgen.SaveCompactionParams{ModelHistory: encodedHistory, CheckpointGeneration: generation + 1, CacheEpoch: cacheEpoch + 1, UpdatedAt: now, SessionID: sessionID}); err != nil {
		return Projection{}, err
	}
	if err := persistContextManifest(ctx, queries, sessionID, plan.Manifest, now); err != nil {
		return Projection{}, err
	}
	if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: now, ID: sessionID}); err != nil {
		return Projection{}, err
	}
	if err := s.commitBlobTransaction(ctx, tx, tracker, trackerOwner); err != nil {
		return Projection{}, err
	}
	projection.ModelHistory = plan.ModelHistory
	projection.UpdatedAt = time.Unix(0, now).UTC()
	projection.CheckpointGeneration = generation + 1
	projection.CacheEpoch = cacheEpoch + 1
	projection.CacheIdentityHash = ""
	return projection, nil
}

func (s *Service) loadSessionBlocks(ctx context.Context, queryer dbgen.DBTX, sessionID string) ([]Block, error) {
	rows, err := dbgen.New(queryer).ListSessionBlocks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session blocks: %w", err)
	}
	blocks := make([]Block, 0, len(rows))
	for _, row := range rows {
		payload, err := s.decodeBlockJSON(ctx, row.Data, row.DataSha256)
		if err != nil {
			return nil, fmt.Errorf("load session block %d: %w", row.Sequence, err)
		}
		var block Block
		if err := json.Unmarshal(payload, &block); err != nil {
			return nil, fmt.Errorf("decode session block: %w", err)
		}
		block.Sequence = row.Sequence
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func (s *Service) appendSessionBlock(ctx context.Context, tx *sql.Tx, sessionID string, block Block) (int64, bool, error) {
	row, err := dbgen.New(tx).GetLatestSessionBlock(ctx, sessionID)
	sequence := row.Sequence
	empty := errors.Is(err, sql.ErrNoRows)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("load latest session block: %w", err)
	}
	coalesceAssistant := false
	if err == nil && block.Kind == "assistant" {
		var activeLeafSequence int64
		activeErr := tx.QueryRowContext(ctx, `
			SELECT entry.block_sequence
			FROM session_graphs graph
			JOIN session_graph_entries entry
				ON entry.session_id=graph.session_id AND entry.entry_id=graph.active_leaf_entry_id
			WHERE graph.session_id=?
		`, sessionID).Scan(&activeLeafSequence)
		if activeErr != nil && !errors.Is(activeErr, sql.ErrNoRows) {
			return 0, false, fmt.Errorf("load active session leaf: %w", activeErr)
		}
		coalesceAssistant = activeErr == nil && activeLeafSequence == row.Sequence
	}
	if coalesceAssistant {
		payload, loadErr := s.decodeBlockJSON(ctx, row.Data, row.DataSha256)
		if loadErr != nil {
			return 0, false, fmt.Errorf("load latest session block: %w", loadErr)
		}
		var previous Block
		if err := json.Unmarshal(payload, &previous); err != nil {
			return 0, false, fmt.Errorf("decode latest session block: %w", err)
		}
		if previous.Kind == block.Kind && previous.RunID == block.RunID {
			previous.Content += block.Content
			encoded, err := json.Marshal(previous)
			if err != nil {
				return 0, false, err
			}
			inline, digest, err := s.encodeBlockData(ctx, previous, encoded)
			if err != nil {
				return 0, false, err
			}
			err = dbgen.New(tx).UpdateSessionBlockData(ctx, dbgen.UpdateSessionBlockDataParams{Data: inline, DataSha256: digest, SessionID: sessionID, Sequence: sequence})
			return sequence, true, err
		}
	}
	encoded, err := json.Marshal(block)
	if err != nil {
		return 0, false, err
	}
	inline, digest, err := s.encodeBlockData(ctx, block, encoded)
	if err != nil {
		return 0, false, err
	}
	sequence++
	if empty {
		sequence = 0
	}
	return sequence, false, s.insertSessionBlock(ctx, tx, sessionID, block, inline, digest)
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

func (s *Service) insertSessionBlock(ctx context.Context, tx *sql.Tx, sessionID string, block Block, encoded []byte, digest string) error {
	return dbgen.New(tx).InsertSessionBlock(ctx, dbgen.InsertSessionBlockParams{SessionID: sessionID, Kind: block.Kind, RunID: block.RunID, AgentID: block.AgentID, Data: encoded, DataSha256: digest, SessionID_2: sessionID})
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
	if limit <= 0 {
		return values, nil
	}
	active := make([]Session, 0, limit)
	archived := make([]Session, 0)
	for _, item := range values {
		if item.Archived {
			archived = append(archived, item)
			continue
		}
		if len(active) < limit {
			active = append(active, item)
		}
	}
	if len(archived) > archivedSessionListCap {
		archived = archived[:archivedSessionListCap]
	}
	return append(active, archived...), nil
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
