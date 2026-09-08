package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

const (
	PromptQueueVersion     = 1
	MaxPromptQueueItems    = 64
	MaxPromptQueueDocument = 8 << 20
)

type QueuedPromptState string

const (
	QueuedPromptQueued      QueuedPromptState = "queued"
	QueuedPromptDispatching QueuedPromptState = "dispatching"
	QueuedPromptFailed      QueuedPromptState = "failed"
)

type PromptQueueState string

const (
	PromptQueueActive PromptQueueState = "active"
	PromptQueuePaused PromptQueueState = "paused"
)

type QueuedPromptV1 struct {
	ID          string            `json:"id"`
	Text        string            `json:"text"`
	Attachments []Attachment      `json:"attachments"`
	State       QueuedPromptState `json:"state"`
	RunID       string            `json:"runId,omitempty"`
	Error       string            `json:"error,omitempty"`
	Attempts    int               `json:"attempts"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type PromptQueueV1 struct {
	Version     int              `json:"version"`
	SessionID   string           `json:"sessionId"`
	Revision    int64            `json:"revision"`
	State       PromptQueueState `json:"state"`
	PauseReason string           `json:"pauseReason,omitempty"`
	Items       []QueuedPromptV1 `json:"items"`
	UpdatedAt   time.Time        `json:"updatedAt,omitempty"`
}

var ErrPromptQueueRevisionConflict = errors.New("prompt queue revision conflict")

type PromptQueueRevisionConflictError struct {
	Expected int64
	Current  PromptQueueV1
}

func (err *PromptQueueRevisionConflictError) Error() string {
	return fmt.Sprintf("prompt queue revision conflict: expected %d, current %d", err.Expected, err.Current.Revision)
}

func (err *PromptQueueRevisionConflictError) Unwrap() error {
	return ErrPromptQueueRevisionConflict
}

func (queue PromptQueueV1) Clone() PromptQueueV1 {
	cloned := queue
	cloned.Items = append([]QueuedPromptV1(nil), queue.Items...)
	for index := range cloned.Items {
		cloned.Items[index].Attachments = append([]Attachment(nil), queue.Items[index].Attachments...)
	}
	if cloned.Items == nil {
		cloned.Items = []QueuedPromptV1{}
	}
	return cloned
}

func (s *Service) LoadPromptQueue(ctx context.Context, sessionID string) (PromptQueueV1, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return PromptQueueV1{}, errors.New("prompt queue session is required")
	}
	row, err := dbgen.New(s.db).GetPromptQueue(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return emptyPromptQueue(sessionID), nil
	}
	if err != nil {
		return PromptQueueV1{}, fmt.Errorf("load prompt queue: %w", err)
	}
	return s.decodePromptQueueRow(ctx, sessionID, row)
}

func (s *Service) ListPromptQueues(ctx context.Context) ([]PromptQueueV1, error) {
	ids, err := dbgen.New(s.db).ListPromptQueueSessionIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list prompt queues: %w", err)
	}
	result := make([]PromptQueueV1, 0, len(ids))
	for _, sessionID := range ids {
		queue, err := s.LoadPromptQueue(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		result = append(result, queue)
	}
	if result == nil {
		result = []PromptQueueV1{}
	}
	return result, nil
}

func (s *Service) SavePromptQueueCAS(ctx context.Context, sessionID string, expectedRevision int64, queue PromptQueueV1) (result PromptQueueV1, err error) {
	return s.savePromptQueueCAS(ctx, sessionID, expectedRevision, queue, nil)
}

// SavePromptQueueWithGuidance consumes a queued item and records its user message
// in one transaction, so a crash cannot replay it as a second queued turn.
func (s *Service) SavePromptQueueWithGuidance(ctx context.Context, sessionID string, expectedRevision int64, queue PromptQueueV1, block Block) (PromptQueueV1, int64, error) {
	if block.Kind != "user" || block.State != "guidance" || block.RunID == "" {
		return PromptQueueV1{}, 0, errors.New("invalid queued guidance block")
	}
	saved, err := s.savePromptQueueCAS(ctx, sessionID, expectedRevision, queue, &block)
	return saved, block.Sequence, err
}

func (s *Service) savePromptQueueCAS(ctx context.Context, sessionID string, expectedRevision int64, queue PromptQueueV1, guidance *Block) (result PromptQueueV1, err error) {
	ctx, tracker, trackerOwner := beginBlobInstallTracking(ctx)
	defer s.finishBlobInstalls(tracker, trackerOwner, &err)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || expectedRevision < 0 {
		return PromptQueueV1{}, errors.New("prompt queue session and non-negative revision are required")
	}
	queue.Version = PromptQueueVersion
	queue.SessionID = sessionID
	queue.Revision = expectedRevision + 1
	queue.UpdatedAt = time.Now().UTC()
	if queue.State == "" {
		queue.State = PromptQueueActive
	}
	if queue.Items == nil {
		queue.Items = []QueuedPromptV1{}
	}
	if err := validatePromptQueue(queue); err != nil {
		return PromptQueueV1{}, err
	}
	encoded, err := json.Marshal(queue.Items)
	if err != nil {
		return PromptQueueV1{}, err
	}
	if len(encoded) > MaxPromptQueueDocument {
		return PromptQueueV1{}, fmt.Errorf("prompt queue document exceeds %d bytes", MaxPromptQueueDocument)
	}
	inline, digest, err := s.spillBytes(ctx, encoded)
	if err != nil {
		return PromptQueueV1{}, fmt.Errorf("store prompt queue document: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PromptQueueV1{}, err
	}
	defer tx.Rollback()
	queries := dbgen.New(tx)
	if expectedRevision == 0 {
		inserted, err := queries.InsertPromptQueue(ctx, dbgen.InsertPromptQueueParams{
			SessionID: sessionID, Revision: queue.Revision, State: string(queue.State), PauseReason: queue.PauseReason,
			ItemsInline: inline, ItemsDigest: digest, UpdatedAt: queue.UpdatedAt.UnixNano(),
		})
		if err != nil {
			return PromptQueueV1{}, fmt.Errorf("insert prompt queue: %w", err)
		}
		count, err := inserted.RowsAffected()
		if err != nil {
			return PromptQueueV1{}, err
		}
		if count == 0 {
			current, loadErr := s.loadPromptQueueWithQueries(ctx, queries, sessionID)
			if loadErr != nil {
				return PromptQueueV1{}, loadErr
			}
			return current, &PromptQueueRevisionConflictError{Expected: expectedRevision, Current: current}
		}
	} else {
		updated, err := queries.UpdatePromptQueueCAS(ctx, dbgen.UpdatePromptQueueCASParams{
			NextRevision: queue.Revision, State: string(queue.State), PauseReason: queue.PauseReason,
			ItemsInline: inline, ItemsDigest: digest, UpdatedAt: queue.UpdatedAt.UnixNano(),
			SessionID: sessionID, ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return PromptQueueV1{}, fmt.Errorf("update prompt queue: %w", err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return PromptQueueV1{}, err
		}
		if count == 0 {
			current, loadErr := s.loadPromptQueueWithQueries(ctx, queries, sessionID)
			if loadErr != nil {
				return PromptQueueV1{}, loadErr
			}
			return current, &PromptQueueRevisionConflictError{Expected: expectedRevision, Current: current}
		}
	}
	if guidance != nil {
		if err := lockBlobCatalog(ctx, tx); err != nil {
			return PromptQueueV1{}, err
		}
		sequence, _, err := s.appendSessionBlock(ctx, tx, sessionID, *guidance)
		if err != nil {
			return PromptQueueV1{}, err
		}
		guidance.Sequence = sequence
		if err := queries.UpdateProjectionRun(ctx, dbgen.UpdateProjectionRunParams{LastRunID: guidance.RunID, UpdatedAt: queue.UpdatedAt.UnixNano(), SessionID: sessionID}); err != nil {
			return PromptQueueV1{}, err
		}
		if err := queries.UpdateSessionTimestamp(ctx, dbgen.UpdateSessionTimestampParams{UpdatedAt: queue.UpdatedAt.UnixNano(), ID: sessionID}); err != nil {
			return PromptQueueV1{}, err
		}
	}
	if err := s.commitBlobTransaction(ctx, tx, tracker, trackerOwner); err != nil {
		return PromptQueueV1{}, err
	}
	return queue.Clone(), nil
}

func (s *Service) loadPromptQueueWithQueries(ctx context.Context, queries *dbgen.Queries, sessionID string) (PromptQueueV1, error) {
	row, err := queries.GetPromptQueue(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return emptyPromptQueue(sessionID), nil
	}
	if err != nil {
		return PromptQueueV1{}, err
	}
	return s.decodePromptQueueRow(ctx, sessionID, row)
}

func (s *Service) decodePromptQueueRow(ctx context.Context, sessionID string, row dbgen.GetPromptQueueRow) (PromptQueueV1, error) {
	inline := row.ItemsInline
	if row.ItemsDigest != "" && len(bytes.TrimSpace(inline)) > 0 && !bytes.Equal(bytes.TrimSpace(inline), []byte("[]")) {
		return PromptQueueV1{}, errors.New("prompt queue inline payload disagrees with stored digest")
	}
	payload, err := s.loadBytes(ctx, inline, row.ItemsDigest)
	if err != nil {
		return PromptQueueV1{}, fmt.Errorf("hydrate prompt queue document: %w", err)
	}
	if len(payload) > MaxPromptQueueDocument {
		return PromptQueueV1{}, fmt.Errorf("prompt queue document exceeds %d bytes", MaxPromptQueueDocument)
	}
	var items []QueuedPromptV1
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&items); err != nil {
		return PromptQueueV1{}, fmt.Errorf("decode prompt queue document: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return PromptQueueV1{}, errors.New("decode prompt queue document: trailing data")
	}
	queue := PromptQueueV1{
		Version: PromptQueueVersion, SessionID: sessionID, Revision: row.Revision,
		State: PromptQueueState(row.State), PauseReason: row.PauseReason,
		Items: items, UpdatedAt: time.Unix(0, row.UpdatedAt).UTC(),
	}
	if queue.Items == nil {
		queue.Items = []QueuedPromptV1{}
	}
	if err := validatePromptQueue(queue); err != nil {
		return PromptQueueV1{}, fmt.Errorf("validate stored prompt queue: %w", err)
	}
	return queue, nil
}

func emptyPromptQueue(sessionID string) PromptQueueV1 {
	return PromptQueueV1{Version: PromptQueueVersion, SessionID: sessionID, State: PromptQueueActive, Items: []QueuedPromptV1{}}
}

func validatePromptQueue(queue PromptQueueV1) error {
	if queue.Version != PromptQueueVersion || strings.TrimSpace(queue.SessionID) == "" || queue.Revision < 0 {
		return errors.New("prompt queue identity is invalid")
	}
	if queue.State != PromptQueueActive && queue.State != PromptQueuePaused {
		return fmt.Errorf("prompt queue state %q is invalid", queue.State)
	}
	if len(queue.Items) > MaxPromptQueueItems {
		return fmt.Errorf("prompt queue exceeds %d items", MaxPromptQueueItems)
	}
	ids := make(map[string]struct{}, len(queue.Items))
	for _, item := range queue.Items {
		item.ID = strings.TrimSpace(item.ID)
		if !validPromptQueueItemID(item.ID) || strings.TrimSpace(item.Text) == "" || item.Attempts < 0 || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
			return fmt.Errorf("prompt queue item %q is invalid", item.ID)
		}
		if _, duplicate := ids[item.ID]; duplicate {
			return fmt.Errorf("prompt queue item %q is duplicated", item.ID)
		}
		ids[item.ID] = struct{}{}
		switch item.State {
		case QueuedPromptQueued:
			if item.RunID != "" {
				return fmt.Errorf("queued prompt %q cannot have a run", item.ID)
			}
		case QueuedPromptDispatching:
		case QueuedPromptFailed:
			if strings.TrimSpace(item.Error) == "" {
				return fmt.Errorf("failed prompt %q requires an error", item.ID)
			}
		default:
			return fmt.Errorf("prompt queue item %q has invalid state %q", item.ID, item.State)
		}
		for _, attachment := range item.Attachments {
			if strings.TrimSpace(attachment.ID) == "" || strings.TrimSpace(attachment.Name) == "" || strings.TrimSpace(attachment.MIME) == "" || strings.TrimSpace(attachment.Path) == "" || attachment.Size <= 0 {
				return fmt.Errorf("prompt queue item %q has an invalid attachment", item.ID)
			}
		}
	}
	return nil
}

func validPromptQueueItemID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-_.:", character) {
			continue
		}
		return false
	}
	return true
}
