package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/blobstore"
)

const inlinePayloadLimit = 4096

func (s *Service) spillText(ctx context.Context, text string) (inline, digest string, err error) {
	if len(text) <= inlinePayloadLimit {
		return text, "", nil
	}
	digest, err = s.installTrackedBlob(ctx, []byte(text))
	if err != nil {
		return "", "", err
	}
	return "", digest, nil
}

func (s *Service) loadText(ctx context.Context, inline, digest string) (string, error) {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return inline, nil
	}
	payload, err := s.blobs.Get(ctx, digest)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *Service) spillBytes(ctx context.Context, payload []byte) (inline []byte, digest string, err error) {
	if len(payload) <= inlinePayloadLimit {
		return payload, "", nil
	}
	digest, err = s.installTrackedBlob(ctx, payload)
	if err != nil {
		return nil, "", err
	}
	return []byte{}, digest, nil
}

func (s *Service) loadBytes(ctx context.Context, inline []byte, digest string) ([]byte, error) {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return inline, nil
	}
	return s.blobs.Get(ctx, digest)
}

func (s *Service) encodeBlockData(ctx context.Context, block Block, encoded []byte) (inline []byte, digest string, err error) {
	if !shouldSpillBlock(block, encoded) {
		return encoded, "", nil
	}
	_, digest, err = s.spillBytes(ctx, encoded)
	if err != nil {
		return nil, "", err
	}
	return []byte("{}"), digest, nil
}

func (s *Service) decodeBlockJSON(ctx context.Context, inline []byte, digest string) ([]byte, error) {
	return s.loadBytes(ctx, inline, digest)
}

func shouldSpillBlock(block Block, encoded []byte) bool {
	if len(encoded) <= inlinePayloadLimit {
		return false
	}
	if block.Kind == "user" {
		return false
	}
	if block.Kind == "assistant" && (block.State == "" || block.State == "completed") {
		return false
	}
	return true
}

func (s *Service) encodeModelHistory(ctx context.Context, history ModelHistory) ([]byte, error) {
	encoded, err := json.Marshal(history)
	if err != nil {
		return nil, err
	}
	if len(encoded) <= inlinePayloadLimit {
		return encoded, nil
	}
	digest, err := s.installTrackedBlob(ctx, encoded)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"blob":                   digest,
		"coveredThroughSequence": history.CoveredThroughSequence,
		"generation":             history.Generation,
	})
}

func (s *Service) decodeModelHistory(ctx context.Context, encoded []byte) (ModelHistory, error) {
	var history ModelHistory
	if len(encoded) == 0 || string(encoded) == "{}" {
		return history, nil
	}
	var stub struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(encoded, &stub); err != nil {
		return ModelHistory{}, err
	}
	if stub.Blob != "" {
		payload, err := s.blobs.Get(ctx, stub.Blob)
		if err != nil {
			return ModelHistory{}, fmt.Errorf("load model history blob: %w", err)
		}
		encoded = payload
	}
	if err := json.Unmarshal(encoded, &history); err != nil {
		return ModelHistory{}, err
	}
	return history, nil
}

type blobInstallTrackerKey struct{}

type blobInstallTracker struct {
	digests map[string]struct{}
}

func beginBlobInstallTracking(ctx context.Context) (context.Context, *blobInstallTracker, bool) {
	if tracker, ok := ctx.Value(blobInstallTrackerKey{}).(*blobInstallTracker); ok {
		return ctx, tracker, false
	}
	tracker := &blobInstallTracker{digests: make(map[string]struct{})}
	return context.WithValue(ctx, blobInstallTrackerKey{}, tracker), tracker, true
}

func (s *Service) installTrackedBlob(ctx context.Context, payload []byte) (string, error) {
	tracker, ok := ctx.Value(blobInstallTrackerKey{}).(*blobInstallTracker)
	if !ok {
		return "", errors.New("session blob install requires an operation tracker")
	}
	digest := blobstore.Sum(payload)
	created, err := s.blobs.InstallAt(ctx, digest, payload)
	if err != nil {
		return "", err
	}
	if created {
		tracker.digests[digest] = struct{}{}
	}
	return digest, nil
}

func (s *Service) finishBlobInstalls(tracker *blobInstallTracker, owner bool, operationErr *error) {
	if !owner || operationErr == nil || *operationErr == nil || len(tracker.digests) == 0 {
		return
	}
	*operationErr = errors.Join(*operationErr, s.cleanupBlobInstalls(tracker))
}

func (s *Service) commitBlobTransaction(ctx context.Context, tx *sql.Tx, tracker *blobInstallTracker, owner bool) error {
	if owner {
		for digest := range tracker.digests {
			if err := s.cleanupBlobInstall(ctx, tx, digest); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Service) cleanupBlobInstalls(tracker *blobInstallTracker) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open session blob cleanup connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate session blob cleanup: %w", err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	var cleanupErrors []error
	for digest := range tracker.digests {
		if err := s.cleanupBlobInstall(ctx, conn, digest); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("commit session blob cleanup: %w", err))
	}
	return errors.Join(cleanupErrors...)
}

type blobReferenceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) cleanupBlobInstall(ctx context.Context, queryer blobReferenceQueryer, digest string) error {
	referenced, err := sessionBlobReferenced(ctx, queryer, digest)
	if err != nil {
		return fmt.Errorf("check session blob %s: %w", digest, err)
	}
	if referenced {
		return nil
	}
	return s.blobs.Delete(ctx, digest)
}

func sessionBlobReferenced(ctx context.Context, queryer blobReferenceQueryer, digest string) (bool, error) {
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
	return referenced, err
}

type blobWriteLocker interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func lockBlobCatalog(ctx context.Context, locker blobWriteLocker) error {
	_, err := locker.ExecContext(ctx, `UPDATE sessions SET id = id WHERE id = ''`)
	return err
}

func replaceArtifactIDs(payload []byte, ids map[string]string) []byte {
	if len(payload) == 0 || len(ids) == 0 {
		return payload
	}
	text := string(payload)
	replaced := text
	for source, target := range ids {
		replaced = strings.ReplaceAll(replaced, source, target)
	}
	if replaced == text {
		return payload
	}
	return []byte(replaced)
}
