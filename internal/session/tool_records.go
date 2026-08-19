package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

const (
	ToolRunning           = "running"
	ToolCompleted         = "completed"
	ToolFailed            = "failed"
	ToolInterrupted       = "interrupted"
	ToolReconcileRequired = "reconcile_required"
)

type FileObservation struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	SHA256    string `json:"sha256,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type ToolRecord struct {
	SessionID      string            `json:"sessionId"`
	RunID          string            `json:"runId"`
	ToolCallID     string            `json:"toolCallId"`
	AnchorSequence int64             `json:"anchorSequence"`
	Name           string            `json:"name"`
	Arguments      json.RawMessage   `json:"arguments,omitempty"`
	State          string            `json:"state"`
	Content        string            `json:"content,omitempty"`
	Structured     json.RawMessage   `json:"structured,omitempty"`
	ArtifactID     string            `json:"artifactId,omitempty"`
	Observations   []FileObservation `json:"observations,omitempty"`
	StartedAt      time.Time         `json:"startedAt"`
	CompletedAt    time.Time         `json:"completedAt,omitempty"`
}

func (s *Service) StartToolRecord(ctx context.Context, sessionID string, record ToolRecord) (ToolRecord, error) {
	return s.startToolRecord(ctx, sessionID, record, nil)
}

// StartToolRecordAt anchors a tool after an explicit durable transcript block.
// Commentary uses this path so restored process trails preserve commentary → tool order.
func (s *Service) StartToolRecordAt(ctx context.Context, sessionID string, record ToolRecord, anchorSequence int64) (ToolRecord, error) {
	if anchorSequence < -1 {
		return ToolRecord{}, fmt.Errorf("start tool record: invalid anchor sequence %d", anchorSequence)
	}
	return s.startToolRecord(ctx, sessionID, record, &anchorSequence)
}

func (s *Service) startToolRecord(ctx context.Context, sessionID string, record ToolRecord, anchorOverride *int64) (recordResult ToolRecord, err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	sessionID = strings.TrimSpace(sessionID)
	record.SessionID = sessionID
	if sessionID == "" || strings.TrimSpace(record.RunID) == "" || strings.TrimSpace(record.ToolCallID) == "" || strings.TrimSpace(record.Name) == "" {
		return ToolRecord{}, fmt.Errorf("start tool record: session, run, call, and name are required")
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now().UTC()
	}
	record.State = ToolRunning
	record.CompletedAt = time.Time{}
	if len(record.Arguments) == 0 {
		record.Arguments = json.RawMessage(`{}`)
	}
	if len(record.Structured) == 0 {
		record.Structured = json.RawMessage(`null`)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ToolRecord{}, err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return ToolRecord{}, err
	}
	queries := dbgen.New(tx)
	anchor := int64(-1)
	if anchorOverride != nil {
		anchor = *anchorOverride
	} else {
		anchor, err = queries.CanonicalHighWater(ctx, sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			anchor = -1
		} else if err != nil {
			return ToolRecord{}, fmt.Errorf("start tool record high-water: %w", err)
		}
	}
	record.AnchorSequence = anchor
	observations, err := json.Marshal(record.Observations)
	if err != nil {
		return ToolRecord{}, fmt.Errorf("encode tool observations: %w", err)
	}
	content, contentDigest, err := s.spillText(ctx, record.Content)
	if err != nil {
		return ToolRecord{}, fmt.Errorf("store tool content: %w", err)
	}
	structured, structuredDigest, err := s.spillBytes(ctx, record.Structured)
	if err != nil {
		return ToolRecord{}, fmt.Errorf("store tool structured: %w", err)
	}
	if structured == nil {
		structured = []byte("null")
	}
	result, err := queries.InsertSessionToolRecord(ctx, dbgen.InsertSessionToolRecordParams{
		SessionID: sessionID, RunID: record.RunID, ToolCallID: record.ToolCallID,
		AnchorSequence: anchor, Name: record.Name, Arguments: record.Arguments, State: record.State,
		Content: content, Structured: structured, ArtifactID: record.ArtifactID,
		Observations: observations, StartedAt: record.StartedAt.UnixNano(), ContentSha256: contentDigest,
		StructuredSha256: structuredDigest,
	})
	if err != nil {
		return ToolRecord{}, fmt.Errorf("insert tool record: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ToolRecord{}, err
	}
	if changed == 0 {
		current, loadErr := s.loadToolRecordFrom(ctx, tx, sessionID, record.RunID, record.ToolCallID)
		if loadErr != nil {
			return ToolRecord{}, loadErr
		}
		if current.State != ToolInterrupted {
			return s.commitToolRecord(ctx, tx, tracker, trackerOwner, current)
		}
		record.AnchorSequence = current.AnchorSequence
		restarted, restartErr := queries.RestartInterruptedSessionToolRecordCAS(ctx, dbgen.RestartInterruptedSessionToolRecordCASParams{
			Name: record.Name, Arguments: record.Arguments, StartedAt: record.StartedAt.UnixNano(),
			SessionID: sessionID, RunID: record.RunID, ToolCallID: record.ToolCallID,
		})
		if restartErr != nil {
			return ToolRecord{}, fmt.Errorf("restart interrupted tool record: %w", restartErr)
		}
		restartedRows, rowsErr := restarted.RowsAffected()
		if rowsErr != nil {
			return ToolRecord{}, rowsErr
		}
		if restartedRows == 1 {
			return s.commitToolRecord(ctx, tx, tracker, trackerOwner, record)
		}
		latest, err := s.loadToolRecordFrom(ctx, tx, sessionID, record.RunID, record.ToolCallID)
		if err != nil {
			return ToolRecord{}, err
		}
		return s.commitToolRecord(ctx, tx, tracker, trackerOwner, latest)
	}
	return s.commitToolRecord(ctx, tx, tracker, trackerOwner, record)
}

func (s *Service) FinishToolRecord(ctx context.Context, sessionID string, record ToolRecord) (recordResult ToolRecord, err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	sessionID = strings.TrimSpace(sessionID)
	record.SessionID = sessionID
	if sessionID == "" || strings.TrimSpace(record.RunID) == "" || strings.TrimSpace(record.ToolCallID) == "" {
		return ToolRecord{}, fmt.Errorf("finish tool record: session, run, and call are required")
	}
	if record.State != ToolCompleted && record.State != ToolFailed && record.State != ToolInterrupted && record.State != ToolReconcileRequired {
		return ToolRecord{}, fmt.Errorf("finish tool record: invalid terminal state %q", record.State)
	}
	current, err := s.loadToolRecord(ctx, sessionID, record.RunID, record.ToolCallID)
	if errors.Is(err, sql.ErrNoRows) {
		if strings.TrimSpace(record.Name) == "" {
			return ToolRecord{}, fmt.Errorf("finish tool record without start requires a name")
		}
		started, startErr := s.StartToolRecord(ctx, sessionID, ToolRecord{
			RunID: record.RunID, ToolCallID: record.ToolCallID, Name: record.Name,
			Arguments: record.Arguments, StartedAt: record.StartedAt,
		})
		if startErr != nil {
			return ToolRecord{}, startErr
		}
		current = started
	} else if err != nil {
		return ToolRecord{}, err
	}
	if strings.TrimSpace(record.Name) == "" {
		record.Name = current.Name
	}
	if len(record.Structured) == 0 {
		record.Structured = json.RawMessage(`null`)
	}
	if current.State != ToolRunning {
		if sameTerminalToolRecord(current, record) {
			return current, nil
		}
		return ToolRecord{}, fmt.Errorf("finish tool record: call %s is already %s", record.ToolCallID, current.State)
	}
	record.Arguments = current.Arguments
	record.AnchorSequence = current.AnchorSequence
	record.StartedAt = current.StartedAt
	if record.CompletedAt.IsZero() {
		record.CompletedAt = time.Now().UTC()
	}
	observations, err := json.Marshal(record.Observations)
	if err != nil {
		return ToolRecord{}, fmt.Errorf("encode tool observations: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ToolRecord{}, err
	}
	defer tx.Rollback()
	if err := lockBlobCatalog(ctx, tx); err != nil {
		return ToolRecord{}, err
	}
	content, contentDigest, err := s.spillText(ctx, record.Content)
	if err != nil {
		return ToolRecord{}, fmt.Errorf("store tool content: %w", err)
	}
	structured, structuredDigest, err := s.spillBytes(ctx, record.Structured)
	if err != nil {
		return ToolRecord{}, fmt.Errorf("store tool structured: %w", err)
	}
	if structured == nil {
		structured = []byte("null")
	}
	result, err := dbgen.New(tx).CompleteSessionToolRecordCAS(ctx, dbgen.CompleteSessionToolRecordCASParams{
		Name: record.Name, State: record.State, Content: content, Structured: structured,
		ArtifactID: record.ArtifactID, Observations: observations, CompletedAt: record.CompletedAt.UnixNano(),
		ContentSha256: contentDigest, StructuredSha256: structuredDigest, SessionID: sessionID, RunID: record.RunID, ToolCallID: record.ToolCallID,
	})
	if err != nil {
		return ToolRecord{}, fmt.Errorf("complete tool record: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ToolRecord{}, err
	}
	if changed != 1 {
		latest, loadErr := s.loadToolRecordFrom(ctx, tx, sessionID, record.RunID, record.ToolCallID)
		if loadErr == nil && sameTerminalToolRecord(latest, record) {
			return s.commitToolRecord(ctx, tx, tracker, trackerOwner, latest)
		}
		return ToolRecord{}, fmt.Errorf("complete tool record: call state changed concurrently")
	}
	return s.commitToolRecord(ctx, tx, tracker, trackerOwner, record)
}

func (s *Service) ListToolRecords(ctx context.Context, sessionID string) ([]ToolRecord, error) {
	rows, err := dbgen.New(s.db).ListSessionToolRecords(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tool records: %w", err)
	}
	result := make([]ToolRecord, 0, len(rows))
	for _, row := range rows {
		record, decodeErr := s.toolRecordFromRow(ctx, sessionID, row.RunID, row.ToolCallID, row.AnchorSequence, row.Name, row.Arguments, row.State, row.Content, row.ContentSha256, row.Structured, row.StructuredSha256, row.ArtifactID, row.Observations, row.StartedAt, row.CompletedAt)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, record)
	}
	return result, nil
}

func (s *Service) commitToolRecord(ctx context.Context, tx *sql.Tx, tracker *blobInstallTracker, trackerOwner bool, record ToolRecord) (ToolRecord, error) {
	if err := s.commitBlobTransaction(ctx, tx, tracker, trackerOwner); err != nil {
		return ToolRecord{}, err
	}
	return record, nil
}

func (s *Service) loadToolRecord(ctx context.Context, sessionID, runID, toolCallID string) (ToolRecord, error) {
	return s.loadToolRecordFrom(ctx, s.db, sessionID, runID, toolCallID)
}

func (s *Service) loadToolRecordFrom(ctx context.Context, queryer dbgen.DBTX, sessionID, runID, toolCallID string) (ToolRecord, error) {
	row, err := dbgen.New(queryer).GetSessionToolRecord(ctx, dbgen.GetSessionToolRecordParams{SessionID: sessionID, RunID: runID, ToolCallID: toolCallID})
	if err != nil {
		return ToolRecord{}, err
	}
	return s.toolRecordFromRow(ctx, sessionID, runID, toolCallID, row.AnchorSequence, row.Name, row.Arguments, row.State, row.Content, row.ContentSha256, row.Structured, row.StructuredSha256, row.ArtifactID, row.Observations, row.StartedAt, row.CompletedAt)
}

func (s *Service) InterruptRunningToolRecordsForRun(ctx context.Context, runID string, at time.Time) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("interrupt running tool records: run is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := dbgen.New(s.db).InterruptRunningSessionToolRecordsByRun(ctx, dbgen.InterruptRunningSessionToolRecordsByRunParams{
		CompletedAt: at.UnixNano(), RunID: runID,
	}); err != nil {
		return fmt.Errorf("interrupt running tool records for run %s: %w", runID, err)
	}
	return nil
}

func (s *Service) WorkspaceSession(ctx context.Context, anchor string) (string, error) {
	id, err := dbgen.New(s.db).GetWorkspaceSession(ctx, strings.TrimSpace(anchor))
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) toolRecordFromRow(ctx context.Context, sessionID, runID, toolCallID string, anchor int64, name string, arguments []byte, state, content, contentDigest string, structured []byte, structuredDigest, artifactID string, rawObservations []byte, startedAt, completedAt int64) (ToolRecord, error) {
	var observations []FileObservation
	if len(rawObservations) > 0 && json.Unmarshal(rawObservations, &observations) != nil {
		return ToolRecord{}, fmt.Errorf("decode tool observations for %s", toolCallID)
	}
	text, err := s.loadText(ctx, content, contentDigest)
	if err != nil {
		return ToolRecord{}, fmt.Errorf("load tool content for %s: %w", toolCallID, err)
	}
	structured, err = s.loadBytes(ctx, structured, structuredDigest)
	if err != nil {
		return ToolRecord{}, fmt.Errorf("load tool structured for %s: %w", toolCallID, err)
	}
	record := ToolRecord{
		SessionID: sessionID, RunID: runID, ToolCallID: toolCallID, AnchorSequence: anchor,
		Name: name, Arguments: append(json.RawMessage(nil), arguments...), State: state, Content: text,
		Structured: append(json.RawMessage(nil), structured...), ArtifactID: artifactID, Observations: observations,
		StartedAt: time.Unix(0, startedAt).UTC(),
	}
	if completedAt > 0 {
		record.CompletedAt = time.Unix(0, completedAt).UTC()
	}
	return record, nil
}

func sameTerminalToolRecord(left, right ToolRecord) bool {
	return left.State == right.State && left.Name == right.Name && left.Content == right.Content && left.ArtifactID == right.ArtifactID &&
		bytes.Equal(left.Structured, right.Structured)
}
