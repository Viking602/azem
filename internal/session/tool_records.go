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

func (s *Service) startToolRecord(ctx context.Context, sessionID string, record ToolRecord, anchorOverride *int64) (ToolRecord, error) {
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
	anchor := int64(-1)
	if anchorOverride != nil {
		anchor = *anchorOverride
	} else {
		var err error
		anchor, err = dbgen.New(s.db).CanonicalHighWater(ctx, sessionID)
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
	result, err := dbgen.New(s.db).InsertSessionToolRecord(ctx, dbgen.InsertSessionToolRecordParams{
		SessionID: sessionID, RunID: record.RunID, ToolCallID: record.ToolCallID,
		AnchorSequence: anchor, Name: record.Name, Arguments: record.Arguments, State: record.State,
		Content: record.Content, Structured: record.Structured, ArtifactID: record.ArtifactID,
		Observations: observations, StartedAt: record.StartedAt.UnixNano(),
	})
	if err != nil {
		return ToolRecord{}, fmt.Errorf("insert tool record: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ToolRecord{}, err
	}
	if changed == 0 {
		current, loadErr := s.loadToolRecord(ctx, sessionID, record.RunID, record.ToolCallID)
		if loadErr != nil {
			return ToolRecord{}, loadErr
		}
		if current.State != ToolInterrupted {
			return current, nil
		}
		record.AnchorSequence = current.AnchorSequence
		restarted, restartErr := dbgen.New(s.db).RestartInterruptedSessionToolRecordCAS(ctx, dbgen.RestartInterruptedSessionToolRecordCASParams{
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
			return record, nil
		}
		return s.loadToolRecord(ctx, sessionID, record.RunID, record.ToolCallID)
	}
	return record, nil
}

func (s *Service) FinishToolRecord(ctx context.Context, sessionID string, record ToolRecord) (ToolRecord, error) {
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
	result, err := dbgen.New(s.db).CompleteSessionToolRecordCAS(ctx, dbgen.CompleteSessionToolRecordCASParams{
		Name: record.Name, State: record.State, Content: record.Content, Structured: record.Structured,
		ArtifactID: record.ArtifactID, Observations: observations, CompletedAt: record.CompletedAt.UnixNano(),
		SessionID: sessionID, RunID: record.RunID, ToolCallID: record.ToolCallID,
	})
	if err != nil {
		return ToolRecord{}, fmt.Errorf("complete tool record: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ToolRecord{}, err
	}
	if changed != 1 {
		latest, loadErr := s.loadToolRecord(ctx, sessionID, record.RunID, record.ToolCallID)
		if loadErr == nil && sameTerminalToolRecord(latest, record) {
			return latest, nil
		}
		return ToolRecord{}, fmt.Errorf("complete tool record: call state changed concurrently")
	}
	return record, nil
}

func (s *Service) ListToolRecords(ctx context.Context, sessionID string) ([]ToolRecord, error) {
	rows, err := dbgen.New(s.db).ListSessionToolRecords(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tool records: %w", err)
	}
	result := make([]ToolRecord, 0, len(rows))
	for _, row := range rows {
		record, decodeErr := toolRecordFromRow(sessionID, row.RunID, row.ToolCallID, row.AnchorSequence, row.Name, row.Arguments, row.State, row.Content, row.Structured, row.ArtifactID, row.Observations, row.StartedAt, row.CompletedAt)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, record)
	}
	return result, nil
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

func (s *Service) SetWorkspaceSession(ctx context.Context, anchor, sessionID string) error {
	anchor, sessionID = strings.TrimSpace(anchor), strings.TrimSpace(sessionID)
	if anchor == "" || sessionID == "" {
		return fmt.Errorf("workspace anchor and session are required")
	}
	if err := dbgen.New(s.db).UpsertWorkspaceSession(ctx, dbgen.UpsertWorkspaceSessionParams{Anchor: anchor, SessionID: sessionID, UpdatedAt: time.Now().UTC().UnixNano()}); err != nil {
		return fmt.Errorf("set workspace session: %w", err)
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

func (s *Service) loadToolRecord(ctx context.Context, sessionID, runID, toolCallID string) (ToolRecord, error) {
	row, err := dbgen.New(s.db).GetSessionToolRecord(ctx, dbgen.GetSessionToolRecordParams{SessionID: sessionID, RunID: runID, ToolCallID: toolCallID})
	if err != nil {
		return ToolRecord{}, err
	}
	return toolRecordFromRow(sessionID, runID, toolCallID, row.AnchorSequence, row.Name, row.Arguments, row.State, row.Content, row.Structured, row.ArtifactID, row.Observations, row.StartedAt, row.CompletedAt)
}

func toolRecordFromRow(sessionID, runID, toolCallID string, anchor int64, name string, arguments []byte, state, content string, structured []byte, artifactID string, rawObservations []byte, startedAt, completedAt int64) (ToolRecord, error) {
	var observations []FileObservation
	if len(rawObservations) > 0 && json.Unmarshal(rawObservations, &observations) != nil {
		return ToolRecord{}, fmt.Errorf("decode tool observations for %s", toolCallID)
	}
	record := ToolRecord{
		SessionID: sessionID, RunID: runID, ToolCallID: toolCallID, AnchorSequence: anchor,
		Name: name, Arguments: append(json.RawMessage(nil), arguments...), State: state, Content: content,
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
