package session

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

var ErrSemanticStateStale = errors.New("session: semantic state source is stale")

type WriterCursorV1 struct {
	CanonicalSequence    int64  `json:"canonical_sequence"`
	TodoRevision         int64  `json:"todo_revision"`
	ToolCompletedAtNS    int64  `json:"tool_completed_at_ns"`
	ToolRunID            string `json:"tool_run_id,omitempty"`
	ToolCallID           string `json:"tool_call_id,omitempty"`
	SubagentFinishedAtNS int64  `json:"subagent_finished_at_ns"`
	SubagentID           string `json:"subagent_id,omitempty"`
}

type SemanticCheckpointV1 struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	Revision     int64           `json:"revision"`
	Cursor       WriterCursorV1  `json:"cursor"`
	State        json.RawMessage `json:"state"`
	SourceDigest string          `json:"source_digest"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type SemanticCommit struct {
	CheckpointID string
	BaseRevision int64
	Cursor       WriterCursorV1
	State        json.RawMessage
	Patch        json.RawMessage
	SourceDigest string
}

type SemanticStateEvent struct {
	Revision     int64
	CheckpointID string
	BaseRevision int64
	Cursor       WriterCursorV1
	Patch        json.RawMessage
	SourceDigest string
	WriterRunID  string
	CreatedAt    time.Time
}

type ContextManifestRecord struct {
	ID                 string
	RunID              string
	CanonicalHighWater *int64
	SemanticRevision   int64
	PolicyVersion      int
	ManifestHash       string
	Data               json.RawMessage
	CreatedAt          time.Time
}

func (s *Service) LoadSemanticCheckpoint(ctx context.Context, sessionID string) (SemanticCheckpointV1, error) {
	row, err := dbgen.New(s.db).GetSemanticState(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return SemanticCheckpointV1{SessionID: sessionID, Cursor: WriterCursorV1{CanonicalSequence: -1}, State: json.RawMessage(`{"version":1}`)}, nil
	}
	if err != nil {
		return SemanticCheckpointV1{}, err
	}
	var cursor WriterCursorV1
	if err := json.Unmarshal(row.Cursor, &cursor); err != nil {
		return SemanticCheckpointV1{}, fmt.Errorf("decode semantic cursor: %w", err)
	}
	if !json.Valid(row.State) {
		return SemanticCheckpointV1{}, fmt.Errorf("decode semantic state: invalid JSON")
	}
	return SemanticCheckpointV1{
		ID: row.CheckpointID, SessionID: sessionID, Revision: row.Revision, Cursor: cursor,
		State: append(json.RawMessage(nil), row.State...), SourceDigest: row.SourceDigest,
		UpdatedAt: time.Unix(0, row.UpdatedAt).UTC(),
	}, nil
}

func (s *Service) LoadActiveContextManifest(ctx context.Context, sessionID string) (ContextManifestRecord, error) {
	row, err := dbgen.New(s.db).GetActiveContextManifest(ctx, sessionID)
	if err != nil {
		return ContextManifestRecord{}, err
	}
	return contextManifestFromRow(row), nil
}

func (s *Service) ListSemanticStateEvents(ctx context.Context, sessionID string) ([]SemanticStateEvent, error) {
	rows, err := dbgen.New(s.db).ListSemanticStateEvents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]SemanticStateEvent, 0, len(rows))
	for _, row := range rows {
		var cursor WriterCursorV1
		if err := json.Unmarshal(row.Cursor, &cursor); err != nil {
			return nil, fmt.Errorf("decode semantic event %d cursor: %w", row.Revision, err)
		}
		result = append(result, SemanticStateEvent{
			Revision: row.Revision, CheckpointID: row.CheckpointID, BaseRevision: row.BaseRevision,
			Cursor: cursor, Patch: append(json.RawMessage(nil), row.Patch...), SourceDigest: row.SourceDigest,
			WriterRunID: row.WriterRunID, CreatedAt: time.Unix(0, row.CreatedAt).UTC(),
		})
	}
	return result, nil
}

func commitSemanticState(ctx context.Context, queries *dbgen.Queries, sessionID, writerRunID string, commit *SemanticCommit, now int64) (int64, error) {
	if commit == nil {
		return 0, nil
	}
	if err := validateSemanticCommit(*commit); err != nil {
		return 0, err
	}
	base, err := loadSemanticCommitBase(ctx, queries, sessionID)
	if err != nil {
		return 0, err
	}
	if base.revision == commit.BaseRevision+1 && base.digest == commit.SourceDigest {
		return base.revision, nil
	}
	if base.revision != commit.BaseRevision {
		return 0, fmt.Errorf("%w: expected revision %d, current revision %d", ErrSemanticStateStale, commit.BaseRevision, base.revision)
	}
	if !cursorAtOrAfter(commit.Cursor, base.cursor) {
		return 0, fmt.Errorf("semantic cursor moved backwards")
	}
	nextRevision := base.revision + 1
	cursor, _ := json.Marshal(commit.Cursor)
	if err := appendSemanticStateEvent(ctx, queries, sessionID, writerRunID, *commit, nextRevision, cursor, now); err != nil {
		return 0, err
	}
	if err := persistSemanticSnapshot(ctx, queries, sessionID, *commit, base, nextRevision, cursor, now); err != nil {
		return 0, err
	}
	return nextRevision, nil
}

type semanticCommitBase struct {
	revision int64
	cursor   WriterCursorV1
	digest   string
	exists   bool
}

func loadSemanticCommitBase(ctx context.Context, queries *dbgen.Queries, sessionID string) (semanticCommitBase, error) {
	base := semanticCommitBase{cursor: WriterCursorV1{CanonicalSequence: -1}}
	row, err := queries.GetSemanticState(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return base, nil
	}
	if err != nil {
		return base, err
	}
	if err := json.Unmarshal(row.Cursor, &base.cursor); err != nil {
		return base, fmt.Errorf("decode current semantic cursor: %w", err)
	}
	base.revision, base.digest, base.exists = row.Revision, row.SourceDigest, true
	return base, nil
}

func appendSemanticStateEvent(ctx context.Context, queries *dbgen.Queries, sessionID, writerRunID string, commit SemanticCommit, revision int64, cursor []byte, now int64) error {
	inserted, err := queries.InsertSemanticStateEvent(ctx, dbgen.InsertSemanticStateEventParams{
		SessionID: sessionID, Revision: revision, CheckpointID: commit.CheckpointID,
		BaseRevision: commit.BaseRevision, Cursor: cursor, Patch: commit.Patch,
		SourceDigest: commit.SourceDigest, WriterRunID: writerRunID, CreatedAt: now,
	})
	if err != nil {
		return err
	}
	changed, err := inserted.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("%w: semantic event already exists", ErrSemanticStateStale)
	}
	return nil
}

func persistSemanticSnapshot(ctx context.Context, queries *dbgen.Queries, sessionID string, commit SemanticCommit, base semanticCommitBase, revision int64, cursor []byte, now int64) error {
	params := dbgen.InsertSemanticStateParams{
		SessionID: sessionID, Revision: revision, CheckpointID: commit.CheckpointID,
		Cursor: cursor, State: commit.State, SourceDigest: commit.SourceDigest, UpdatedAt: now,
	}
	var changed int64
	if !base.exists {
		result, err := queries.InsertSemanticState(ctx, params)
		if err != nil {
			return err
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return err
		}
	} else {
		result, updateErr := queries.UpdateSemanticStateCAS(ctx, dbgen.UpdateSemanticStateCASParams{
			Revision: revision, CheckpointID: commit.CheckpointID, Cursor: cursor, State: commit.State,
			SourceDigest: commit.SourceDigest, UpdatedAt: now, SessionID: sessionID, Revision_2: base.revision,
		})
		if updateErr != nil {
			return updateErr
		}
		var err error
		changed, err = result.RowsAffected()
		if err != nil {
			return err
		}
	}
	if changed != 1 {
		return fmt.Errorf("%w: semantic state changed during commit", ErrSemanticStateStale)
	}
	return nil
}

func persistContextManifest(ctx context.Context, queries *dbgen.Queries, sessionID string, manifest *ContextManifestRecord, semanticRevision int64, now int64) error {
	if manifest == nil {
		return nil
	}
	if err := validateContextManifest(*manifest); err != nil {
		return err
	}
	highWater := int64(-1)
	if manifest.CanonicalHighWater != nil {
		highWater = *manifest.CanonicalHighWater
	}
	if semanticRevision == 0 {
		semanticRevision = manifest.SemanticRevision
	}
	if err := queries.DeactivateContextManifests(ctx, sessionID); err != nil {
		return err
	}
	return queries.UpsertContextManifest(ctx, dbgen.UpsertContextManifestParams{
		ID: manifest.ID, SessionID: sessionID, RunID: manifest.RunID, CanonicalHighWater: highWater,
		SemanticRevision: semanticRevision, PolicyVersion: int64(manifest.PolicyVersion), ManifestHash: manifest.ManifestHash,
		Activated: 1, Data: manifest.Data, CreatedAt: now,
	})
}

func validateContextManifest(manifest ContextManifestRecord) error {
	if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.ManifestHash) == "" || manifest.PolicyVersion <= 0 || !json.Valid(manifest.Data) {
		return fmt.Errorf("context manifest is incomplete")
	}
	var data struct {
		ID               string `json:"id"`
		SemanticRevision int64  `json:"semantic_revision"`
		PolicyVersion    int    `json:"policy_version"`
		ManifestHash     string `json:"manifest_hash"`
	}
	if json.Unmarshal(manifest.Data, &data) != nil || data.ID != manifest.ID || data.ManifestHash != manifest.ManifestHash || data.PolicyVersion != manifest.PolicyVersion || data.SemanticRevision != manifest.SemanticRevision {
		return fmt.Errorf("context manifest metadata does not match its payload")
	}
	return nil
}

func contextManifestFromRow(row dbgen.GetActiveContextManifestRow) ContextManifestRecord {
	var highWater *int64
	if row.CanonicalHighWater >= 0 {
		value := row.CanonicalHighWater
		highWater = &value
	}
	return ContextManifestRecord{
		ID: row.ID, RunID: row.RunID, CanonicalHighWater: highWater, SemanticRevision: row.SemanticRevision,
		PolicyVersion: int(row.PolicyVersion), ManifestHash: row.ManifestHash, Data: append(json.RawMessage(nil), row.Data...),
		CreatedAt: time.Unix(0, row.CreatedAt).UTC(),
	}
}

func validateSemanticCommit(commit SemanticCommit) error {
	if commit.BaseRevision < 0 || strings.TrimSpace(commit.CheckpointID) == "" || len(commit.SourceDigest) != 64 || !json.Valid(commit.State) || !json.Valid(commit.Patch) {
		return fmt.Errorf("semantic commit is incomplete")
	}
	if _, err := hex.DecodeString(commit.SourceDigest); err != nil {
		return fmt.Errorf("semantic source digest is invalid")
	}
	if err := validateSemanticStatePayload(commit.State); err != nil {
		return err
	}
	return validateSemanticPatchPayload(commit)
}

func validateSemanticStatePayload(payload json.RawMessage) error {
	var state struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(payload, &state) != nil || state.Version != 1 {
		return fmt.Errorf("semantic state version is invalid")
	}
	return nil
}

func validateSemanticPatchPayload(commit SemanticCommit) error {
	var patch struct {
		Version      int               `json:"version"`
		BaseRevision int64             `json:"base_revision"`
		Through      WriterCursorV1    `json:"through"`
		SourceDigest string            `json:"source_digest"`
		Operations   []json.RawMessage `json:"operations"`
	}
	if json.Unmarshal(commit.Patch, &patch) != nil || patch.Version != 1 || patch.BaseRevision != commit.BaseRevision || patch.Through != commit.Cursor || patch.SourceDigest != commit.SourceDigest || patch.Operations == nil {
		return fmt.Errorf("semantic patch metadata does not match its commit")
	}
	return nil
}

func cursorAtOrAfter(next, current WriterCursorV1) bool {
	return next.CanonicalSequence >= current.CanonicalSequence &&
		next.TodoRevision >= current.TodoRevision &&
		cursorTupleAtOrAfter(next.ToolCompletedAtNS, next.ToolRunID+"\x00"+next.ToolCallID, current.ToolCompletedAtNS, current.ToolRunID+"\x00"+current.ToolCallID) &&
		cursorTupleAtOrAfter(next.SubagentFinishedAtNS, next.SubagentID, current.SubagentFinishedAtNS, current.SubagentID)
}

func cursorTupleAtOrAfter(nextTime int64, nextID string, currentTime int64, currentID string) bool {
	return nextTime > currentTime || nextTime == currentTime && nextID >= currentID
}
