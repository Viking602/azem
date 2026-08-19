package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/blobstore"
)

const (
	inlinePayloadLimit = 4096
	extractBatchSize   = 16
)

func extractLargePayloads(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	if blobs == nil {
		return fmt.Errorf("blob store is required")
	}
	if err := extractArtifactPayloads(ctx, tx, blobs); err != nil {
		return err
	}
	if err := extractToolContents(ctx, tx, blobs); err != nil {
		return err
	}
	if err := extractSubagentPayloads(ctx, tx, blobs); err != nil {
		return err
	}
	if err := extractSessionBlocks(ctx, tx, blobs); err != nil {
		return err
	}
	if err := extractModelHistories(ctx, tx, blobs); err != nil {
		return err
	}
	if err := extractEventPayloads(ctx, tx, blobs); err != nil {
		return err
	}
	if err := extractRecordPayloads(ctx, tx, blobs); err != nil {
		return err
	}
	return extractToolStructured(ctx, tx, blobs)
}

func extractArtifactPayloads(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, sha256, payload FROM context_artifacts`)
	if err != nil {
		return fmt.Errorf("list artifact payloads: %w", err)
	}
	type row struct {
		id, sha, digest string
		payload         []byte
	}
	var items []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.sha, &item.payload); err != nil {
			_ = rows.Close()
			return err
		}
		item.digest = blobstore.Sum(item.payload)
		if blobstore.ValidDigest(item.sha) {
			item.digest = item.sha
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
		if err := blobs.PutAt(ctx, item.digest, item.payload); err != nil {
			return fmt.Errorf("store artifact %s: %w", item.id, err)
		}
		if item.sha == item.digest {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE context_artifacts SET sha256=? WHERE id=?`, item.digest, item.id); err != nil {
			return fmt.Errorf("normalize artifact %s digest: %w", item.id, err)
		}
	}
	return nil
}

func extractToolContents(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	rows, err := tx.QueryContext(ctx, `SELECT session_id, run_id, tool_call_id, content FROM session_tool_records WHERE length(content) > ?`, inlinePayloadLimit)
	if err != nil {
		return fmt.Errorf("list tool contents: %w", err)
	}
	type row struct {
		sessionID, runID, callID, digest string
	}
	var updates []row
	for rows.Next() {
		var item row
		var content string
		if err := rows.Scan(&item.sessionID, &item.runID, &item.callID, &content); err != nil {
			_ = rows.Close()
			return err
		}
		digest, err := blobs.Put(ctx, []byte(content))
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("store tool content %s: %w", item.callID, err)
		}
		item.digest = digest
		updates = append(updates, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE session_tool_records SET content='', content_sha256=? WHERE session_id=? AND run_id=? AND tool_call_id=?`,
			item.digest, item.sessionID, item.runID, item.callID); err != nil {
			return err
		}
	}
	return nil
}

func extractSubagentPayloads(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, output, transcript FROM subagent_runs`)
	if err != nil {
		return fmt.Errorf("list subagent payloads: %w", err)
	}
	type row struct {
		id, outputDigest, transcriptDigest string
		clearOutput, clearTranscript       bool
	}
	var updates []row
	for rows.Next() {
		var item row
		var output string
		var transcript []byte
		if err := rows.Scan(&item.id, &output, &transcript); err != nil {
			_ = rows.Close()
			return err
		}
		if len(output) > inlinePayloadLimit {
			digest, err := blobs.Put(ctx, []byte(output))
			if err != nil {
				_ = rows.Close()
				return err
			}
			item.outputDigest = digest
			item.clearOutput = true
		}
		if len(transcript) > inlinePayloadLimit {
			digest, err := blobs.Put(ctx, transcript)
			if err != nil {
				_ = rows.Close()
				return err
			}
			item.transcriptDigest = digest
			item.clearTranscript = true
		}
		if item.clearOutput || item.clearTranscript {
			updates = append(updates, item)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range updates {
		if item.clearOutput {
			if _, err := tx.ExecContext(ctx, `UPDATE subagent_runs SET output='', output_sha256=? WHERE id=?`, item.outputDigest, item.id); err != nil {
				return err
			}
		}
		if item.clearTranscript {
			if _, err := tx.ExecContext(ctx, `UPDATE subagent_runs SET transcript=X'', transcript_sha256=? WHERE id=?`, item.transcriptDigest, item.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractSessionBlocks(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	rows, err := tx.QueryContext(ctx, `SELECT session_id, sequence, kind, data FROM session_blocks WHERE length(data) > ?`, inlinePayloadLimit)
	if err != nil {
		return fmt.Errorf("list session blocks: %w", err)
	}
	type row struct {
		sessionID, digest string
		sequence          int64
	}
	var updates []row
	for rows.Next() {
		var item row
		var kind string
		var data []byte
		if err := rows.Scan(&item.sessionID, &item.sequence, &kind, &data); err != nil {
			_ = rows.Close()
			return err
		}
		if !shouldSpillBlockKind(kind, data) {
			continue
		}
		digest, err := blobs.Put(ctx, data)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("store session block %s/%d: %w", item.sessionID, item.sequence, err)
		}
		item.digest = digest
		updates = append(updates, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE session_blocks SET data='{}', data_sha256=? WHERE session_id=? AND sequence=?`,
			item.digest, item.sessionID, item.sequence); err != nil {
			return err
		}
	}
	return nil
}

func shouldSpillBlockKind(kind string, data []byte) bool {
	if kind == "user" {
		return false
	}
	if kind != "assistant" {
		return true
	}
	var block struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(data, &block)
	return block.State != "" && block.State != "completed"
}

func extractEventPayloads(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	if !hasTable(ctx, tx, "events") {
		return nil
	}
	for {
		rows, err := tx.QueryContext(ctx, `SELECT run_id, sequence, data FROM events WHERE length(data) > ? AND data_sha256='' LIMIT ?`, inlinePayloadLimit, extractBatchSize)
		if err != nil {
			return fmt.Errorf("list event payloads: %w", err)
		}
		type row struct {
			runID  string
			seq    int64
			digest string
		}
		var updates []row
		for rows.Next() {
			var item row
			var data []byte
			if err := rows.Scan(&item.runID, &item.seq, &data); err != nil {
				_ = rows.Close()
				return err
			}
			digest, err := blobs.Put(ctx, data)
			if err != nil {
				_ = rows.Close()
				return fmt.Errorf("store event %s/%d: %w", item.runID, item.seq, err)
			}
			item.digest = digest
			updates = append(updates, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(updates) == 0 {
			return nil
		}
		for _, item := range updates {
			if _, err := tx.ExecContext(ctx, `UPDATE events SET data='{}', data_sha256=? WHERE run_id=? AND sequence=?`,
				item.digest, item.runID, item.seq); err != nil {
				return err
			}
		}
	}
}

func extractRecordPayloads(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	if !hasTable(ctx, tx, "records") {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind, key1, key2, data FROM records WHERE length(data) > ?`, inlinePayloadLimit)
	if err != nil {
		return fmt.Errorf("list record payloads: %w", err)
	}
	type row struct {
		kind, key1, key2, digest string
	}
	var updates []row
	for rows.Next() {
		var item row
		var data []byte
		if err := rows.Scan(&item.kind, &item.key1, &item.key2, &data); err != nil {
			_ = rows.Close()
			return err
		}
		digest, err := blobs.Put(ctx, data)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("store record %s/%s: %w", item.kind, item.key1, err)
		}
		item.digest = digest
		updates = append(updates, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE records SET data='{}', data_sha256=? WHERE kind=? AND key1=? AND key2=?`,
			item.digest, item.kind, item.key1, item.key2); err != nil {
			return err
		}
	}
	return nil
}

func extractToolStructured(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	rows, err := tx.QueryContext(ctx, `SELECT session_id, run_id, tool_call_id, structured FROM session_tool_records WHERE length(structured) > ?`, inlinePayloadLimit)
	if err != nil {
		return fmt.Errorf("list tool structured payloads: %w", err)
	}
	type row struct {
		sessionID, runID, callID, digest string
	}
	var updates []row
	for rows.Next() {
		var item row
		var structured []byte
		if err := rows.Scan(&item.sessionID, &item.runID, &item.callID, &structured); err != nil {
			_ = rows.Close()
			return err
		}
		digest, err := blobs.Put(ctx, structured)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("store tool structured %s: %w", item.callID, err)
		}
		item.digest = digest
		updates = append(updates, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE session_tool_records SET structured='null', structured_sha256=? WHERE session_id=? AND run_id=? AND tool_call_id=?`,
			item.digest, item.sessionID, item.runID, item.callID); err != nil {
			return err
		}
	}
	return nil
}

func hasTable(ctx context.Context, tx *sql.Tx, name string) bool {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	return err == nil && found == name
}

func isMissingRelation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") || strings.Contains(message, "no such column")
}

func extractModelHistories(ctx context.Context, tx *sql.Tx, blobs blobstore.Store) error {
	rows, err := tx.QueryContext(ctx, `SELECT session_id, model_history FROM session_projections WHERE length(model_history) > ?`, inlinePayloadLimit)
	if err != nil {
		return fmt.Errorf("list model histories: %w", err)
	}
	type row struct {
		sessionID, digest string
		stub              []byte
	}
	var updates []row
	for rows.Next() {
		var item row
		var history []byte
		if err := rows.Scan(&item.sessionID, &history); err != nil {
			_ = rows.Close()
			return err
		}
		digest, err := blobs.Put(ctx, history)
		if err != nil {
			_ = rows.Close()
			return err
		}
		item.digest = digest
		item.stub, err = modelHistoryStub(history, digest)
		if err != nil {
			_ = rows.Close()
			return err
		}
		updates = append(updates, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE session_projections SET model_history=?, model_history_sha256=? WHERE session_id=?`,
			item.stub, item.digest, item.sessionID); err != nil {
			return err
		}
	}
	return nil
}

func modelHistoryStub(history []byte, digest string) ([]byte, error) {
	var meta struct {
		CoveredThroughSequence *int64 `json:"coveredThroughSequence"`
		Generation             int64  `json:"generation"`
	}
	_ = json.Unmarshal(history, &meta)
	return json.Marshal(map[string]any{
		"blob":                   digest,
		"coveredThroughSequence": meta.CoveredThroughSequence,
		"generation":             meta.Generation,
	})
}
