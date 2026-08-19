// Package eval contains offline evaluation and replay helpers. It is imported
// by azem-eval only and is not part of the production agent runtime.
package eval

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/Viking602/azem/internal/blobstore"
)

const (
	TrajectoryVersionV1    = 1
	trajectoryPayloadLimit = 16 << 20
)

// StoredValueV1 preserves the SQLite storage class and exact bytes of one
// column. Text is base64-encoded too so malformed or non-normalized UTF-8 is
// never changed by a JSON round trip.
type StoredValueV1 struct {
	Kind    string   `json:"kind"`
	Integer *int64   `json:"integer,omitempty"`
	Real    *float64 `json:"real,omitempty"`
	Base64  string   `json:"base64,omitempty"`
}

type TrajectoryRowV1 struct {
	Values map[string]StoredValueV1 `json:"values"`
}

type TrajectoryTableV1 struct {
	Name    string            `json:"name"`
	Columns []string          `json:"columns"`
	Rows    []TrajectoryRowV1 `json:"rows"`
}

type TrajectoryBlobV1 struct {
	SHA256 string `json:"sha256"`
	Base64 string `json:"base64"`
}

// TrajectoryV1 is a deterministic, lossless export of the durable records that
// explain one session. It intentionally excludes credentials and mutable
// application configuration.
type TrajectoryV1 struct {
	Version   int                 `json:"version"`
	SessionID string              `json:"session_id"`
	Tables    []TrajectoryTableV1 `json:"tables"`
	Blobs     []TrajectoryBlobV1  `json:"blobs,omitempty"`
	Incidents []IncidentLabelV1   `json:"incidents,omitempty"`
}

type trajectoryTableSpec struct {
	name  string
	query string
	args  func(string) []any
}

var trajectoryTableSpecs = []trajectoryTableSpec{
	{name: "session_blocks", query: `SELECT * FROM session_blocks WHERE session_id = ? ORDER BY sequence`, args: sessionArg},
	{name: "session_tool_records", query: `SELECT * FROM session_tool_records WHERE session_id = ? ORDER BY started_at, run_id, tool_call_id`, args: sessionArg},
	{name: "provider_requests", query: `SELECT * FROM provider_requests WHERE session_id = ? ORDER BY started_at, request_id`, args: sessionArg},
	{name: "subagent_runs", query: `SELECT * FROM subagent_runs WHERE session_id = ? ORDER BY started_at, id`, args: sessionArg},
	{name: "session_todos", query: `SELECT * FROM session_todos WHERE session_id = ? ORDER BY session_id`, args: sessionArg},
	{name: "session_semantic_state", query: `SELECT * FROM session_semantic_state WHERE session_id = ? ORDER BY session_id`, args: sessionArg},
	{name: "session_semantic_state_events", query: `SELECT * FROM session_semantic_state_events WHERE session_id = ? ORDER BY revision`, args: sessionArg},
	{name: "context_manifests", query: `SELECT * FROM context_manifests WHERE session_id = ? ORDER BY created_at, id`, args: sessionArg},
	{name: "context_artifacts", query: `SELECT * FROM context_artifacts WHERE session_id = ? ORDER BY created_at, id`, args: sessionArg},
	{name: "events", query: `SELECT e.* FROM events e WHERE e.run_id <> '' AND e.run_id IN (` + sessionRunIDsSQL + `) ORDER BY e.run_id, e.sequence`, args: repeatedSessionArgs},
	{name: "records", query: `SELECT r.* FROM records r WHERE r.run_id <> '' AND r.run_id IN (` + sessionRunIDsSQL + `) ORDER BY r.run_id, r.created_at, r.kind, r.key1, r.key2`, args: repeatedSessionArgs},
}

var trajectoryExpectedColumns = map[string][]string{
	"session_blocks": {
		"session_id", "sequence", "kind", "run_id", "agent_id", "data", "data_sha256",
	},
	"session_tool_records": {
		"session_id", "run_id", "tool_call_id", "anchor_sequence", "name", "arguments", "state",
		"content", "structured", "artifact_id", "observations", "started_at", "completed_at",
		"content_sha256", "structured_sha256",
	},
	"provider_requests": {
		"request_id", "provider_request_id", "session_id", "run_id", "request_kind", "provider",
		"model", "transport", "cache_epoch", "checkpoint_generation", "input_tokens", "cached_tokens",
		"cache_write_tokens", "output_tokens", "reasoning_tokens", "total_tokens", "cache_reported",
		"status", "started_at", "completed_at", "cache_write_reported",
	},
	"subagent_runs": {
		"id", "session_id", "parent_run_id", "parent_agent_id", "tool_call_id", "subagent_type",
		"state", "summary", "started_at", "finished_at", "child_run_id", "description",
		"model", "reasoning", "capability_mode", "requested_isolation", "isolation", "cwd",
		"background", "output", "error", "warning", "transcript", "tool_calls", "turns",
		"tokens_used", "tools_used", "worktree_path", "completion_delivered", "provider",
		"transcript_sha256", "output_sha256",
	},
	"session_todos": {
		"session_id", "goal", "revision", "phases", "updated_at",
	},
	"session_semantic_state": {
		"session_id", "revision", "checkpoint_id", "cursor", "state", "source_digest", "updated_at",
	},
	"session_semantic_state_events": {
		"session_id", "revision", "checkpoint_id", "base_revision", "cursor", "patch",
		"source_digest", "writer_run_id", "created_at",
	},
	"context_manifests": {
		"id", "session_id", "run_id", "canonical_high_water", "semantic_revision", "policy_version",
		"manifest_hash", "activated", "data", "created_at",
	},
	"context_artifacts": {
		"id", "session_id", "run_id", "kind", "sha256", "preview", "created_at",
	},
	"events": {
		"run_id", "sequence", "recorded_at", "data", "data_sha256",
	},
	"records": {
		"kind", "key1", "key2", "run_id", "task_id", "status", "created_at", "tool_name",
		"idempotency_key", "data", "data_sha256",
	},
}

const sessionRunIDsSQL = `
	SELECT run_id FROM session_blocks WHERE session_id = ? AND run_id <> ''
	UNION SELECT run_id FROM session_tool_records WHERE session_id = ? AND run_id <> ''
	UNION SELECT run_id FROM provider_requests WHERE session_id = ? AND run_id <> ''
	UNION SELECT parent_run_id FROM subagent_runs WHERE session_id = ? AND parent_run_id <> ''
	UNION SELECT child_run_id FROM subagent_runs WHERE session_id = ? AND child_run_id <> ''
	UNION SELECT run_id FROM context_manifests WHERE session_id = ? AND run_id <> ''
	UNION SELECT run_id FROM context_artifacts WHERE session_id = ? AND run_id <> ''`

func sessionArg(sessionID string) []any { return []any{sessionID} }

func repeatedSessionArgs(sessionID string) []any {
	return []any{sessionID, sessionID, sessionID, sessionID, sessionID, sessionID, sessionID}
}

// ExportTrajectory reads one consistent snapshot. Callers should use a
// read-only database or hold a read transaction when exporting a live store.
func ExportTrajectory(ctx context.Context, db *sql.DB, blobs blobstore.Store, sessionID string) (TrajectoryV1, error) {
	if db == nil || strings.TrimSpace(sessionID) == "" {
		return TrajectoryV1{}, fmt.Errorf("eval: trajectory export requires database and session id")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return TrajectoryV1{}, fmt.Errorf("eval: begin trajectory snapshot: %w", err)
	}
	defer tx.Rollback()

	export := TrajectoryV1{Version: TrajectoryVersionV1, SessionID: sessionID, Tables: make([]TrajectoryTableV1, 0, len(trajectoryTableSpecs))}
	for _, spec := range trajectoryTableSpecs {
		table, readErr := readTrajectoryTable(ctx, tx, spec, sessionID)
		if readErr != nil {
			return TrajectoryV1{}, readErr
		}
		export.Tables = append(export.Tables, table)
	}
	export.Blobs, err = collectTrajectoryBlobs(ctx, export.Tables, blobs)
	if err != nil {
		return TrajectoryV1{}, err
	}
	if err := tx.Commit(); err != nil {
		return TrajectoryV1{}, fmt.Errorf("eval: finish trajectory snapshot: %w", err)
	}
	return export, nil
}

func readTrajectoryTable(ctx context.Context, tx *sql.Tx, spec trajectoryTableSpec, sessionID string) (TrajectoryTableV1, error) {
	rows, err := tx.QueryContext(ctx, spec.query, spec.args(sessionID)...)
	if err != nil {
		return TrajectoryTableV1{}, fmt.Errorf("eval: export %s: %w", spec.name, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return TrajectoryTableV1{}, fmt.Errorf("eval: columns for %s: %w", spec.name, err)
	}
	expectedColumns := trajectoryExpectedColumns[spec.name]
	if !slices.Equal(columns, expectedColumns) {
		return TrajectoryTableV1{}, fmt.Errorf("eval: columns for %s do not match trajectory schema: got %v, want %v", spec.name, columns, expectedColumns)
	}
	table := TrajectoryTableV1{Name: spec.name, Columns: append([]string(nil), columns...), Rows: []TrajectoryRowV1{}}
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return TrajectoryTableV1{}, fmt.Errorf("eval: scan %s: %w", spec.name, err)
		}
		encoded := make(map[string]StoredValueV1, len(columns))
		for i, column := range columns {
			encoded[column], err = encodeStoredValue(values[i])
			if err != nil {
				return TrajectoryTableV1{}, fmt.Errorf("eval: encode %s.%s: %w", spec.name, column, err)
			}
		}
		table.Rows = append(table.Rows, TrajectoryRowV1{Values: encoded})
	}
	if err := rows.Err(); err != nil {
		return TrajectoryTableV1{}, fmt.Errorf("eval: iterate %s: %w", spec.name, err)
	}
	return table, nil
}

func encodeStoredValue(value any) (StoredValueV1, error) {
	switch typed := value.(type) {
	case nil:
		return StoredValueV1{Kind: "null"}, nil
	case int64:
		return StoredValueV1{Kind: "integer", Integer: &typed}, nil
	case float64:
		return StoredValueV1{Kind: "real", Real: &typed}, nil
	case bool:
		integer := int64(0)
		if typed {
			integer = 1
		}
		return StoredValueV1{Kind: "integer", Integer: &integer}, nil
	case string:
		if len(typed) > trajectoryPayloadLimit {
			return StoredValueV1{}, fmt.Errorf("text payload exceeds %d bytes", trajectoryPayloadLimit)
		}
		return StoredValueV1{Kind: "text", Base64: base64.StdEncoding.EncodeToString([]byte(typed))}, nil
	case []byte:
		if len(typed) > trajectoryPayloadLimit {
			return StoredValueV1{}, fmt.Errorf("blob payload exceeds %d bytes", trajectoryPayloadLimit)
		}
		return StoredValueV1{Kind: "blob", Base64: base64.StdEncoding.EncodeToString(typed)}, nil
	default:
		return StoredValueV1{}, fmt.Errorf("unsupported SQLite value %T", value)
	}
}

func (v StoredValueV1) Bytes() ([]byte, bool, error) {
	if v.Kind != "text" && v.Kind != "blob" {
		return nil, false, nil
	}
	if base64.StdEncoding.DecodedLen(len(v.Base64)) > trajectoryPayloadLimit {
		return nil, true, fmt.Errorf("%s value exceeds %d bytes", v.Kind, trajectoryPayloadLimit)
	}
	decoded, err := base64.StdEncoding.DecodeString(v.Base64)
	if err != nil {
		return nil, true, fmt.Errorf("decode %s value: %w", v.Kind, err)
	}
	if len(decoded) > trajectoryPayloadLimit {
		return nil, true, fmt.Errorf("%s value exceeds %d bytes", v.Kind, trajectoryPayloadLimit)
	}
	return decoded, true, nil
}

type payloadDigestMapping struct {
	digestColumn string
	inlineColumn string
	requiredBlob bool
}

var payloadDigestMappings = map[string][]payloadDigestMapping{
	"context_artifacts":    {{digestColumn: "sha256", requiredBlob: true}},
	"session_blocks":       {{digestColumn: "data_sha256", inlineColumn: "data"}},
	"session_tool_records": {{digestColumn: "content_sha256", inlineColumn: "content"}, {digestColumn: "structured_sha256", inlineColumn: "structured"}},
	"subagent_runs":        {{digestColumn: "transcript_sha256", inlineColumn: "transcript"}, {digestColumn: "output_sha256", inlineColumn: "output"}},
	"events":               {{digestColumn: "data_sha256", inlineColumn: "data"}},
	"records":              {{digestColumn: "data_sha256", inlineColumn: "data"}},
}

func collectTrajectoryBlobs(ctx context.Context, tables []TrajectoryTableV1, blobs blobstore.Store) ([]TrajectoryBlobV1, error) {
	payloads := make(map[string][]byte)
	for _, table := range tables {
		for _, mapping := range payloadDigestMappings[table.Name] {
			for _, row := range table.Rows {
				digest, err := storedString(row.Values[mapping.digestColumn])
				if err != nil {
					return nil, fmt.Errorf("eval: decode %s.%s: %w", table.Name, mapping.digestColumn, err)
				}
				if digest == "" {
					continue
				}
				if !blobstore.ValidDigest(digest) {
					return nil, fmt.Errorf("eval: invalid payload digest %q in %s.%s", digest, table.Name, mapping.digestColumn)
				}
				if mapping.inlineColumn != "" {
					inline, present, decodeErr := row.Values[mapping.inlineColumn].Bytes()
					if decodeErr != nil {
						return nil, fmt.Errorf("eval: decode %s.%s: %w", table.Name, mapping.inlineColumn, decodeErr)
					}
					if present {
						inlineDigest := sumHex(inline)
						if len(inline) == 0 && inlineDigest != digest {
							// Schema 21 leaves an empty sentinel after moving the payload to BlobStore.
						} else if inlineDigest != digest {
							return nil, fmt.Errorf("eval: inline payload %s.%s disagrees with stored digest", table.Name, mapping.inlineColumn)
						} else {
							continue
						}
					}
				}
				if _, exists := payloads[digest]; exists {
					continue
				}
				if blobs == nil {
					return nil, fmt.Errorf("eval: payload %s from %s requires blob store", digest, table.Name)
				}
				payload, getErr := blobs.Get(ctx, digest)
				if getErr != nil {
					return nil, fmt.Errorf("eval: recover payload %s from %s: %w", digest, table.Name, getErr)
				}
				if len(payload) > trajectoryPayloadLimit {
					return nil, fmt.Errorf("eval: recovered payload %s exceeds %d bytes", digest, trajectoryPayloadLimit)
				}
				if sumHex(payload) != digest {
					return nil, fmt.Errorf("eval: recovered payload %s failed digest validation", digest)
				}
				payloads[digest] = payload
			}
		}
	}
	digests := make([]string, 0, len(payloads))
	for digest := range payloads {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	result := make([]TrajectoryBlobV1, 0, len(digests))
	for _, digest := range digests {
		result = append(result, TrajectoryBlobV1{SHA256: digest, Base64: base64.StdEncoding.EncodeToString(payloads[digest])})
	}
	return result, nil
}

func storedString(value StoredValueV1) (string, error) {
	bytes, present, err := value.Bytes()
	if err != nil || !present {
		return "", err
	}
	return string(bytes), nil
}

func WriteTrajectory(w io.Writer, trajectory TrajectoryV1) error {
	if w == nil {
		return fmt.Errorf("eval: trajectory writer is nil")
	}
	if trajectory.Version != TrajectoryVersionV1 || trajectory.SessionID == "" {
		return fmt.Errorf("eval: invalid trajectory identity")
	}
	if err := validateTrajectoryPayloads(trajectory); err != nil {
		return err
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(trajectory); err != nil {
		return fmt.Errorf("eval: encode trajectory: %w", err)
	}
	return nil
}

func ReadTrajectory(r io.Reader) (TrajectoryV1, error) {
	if r == nil {
		return TrajectoryV1{}, fmt.Errorf("eval: trajectory reader is nil")
	}
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var trajectory TrajectoryV1
	if err := decoder.Decode(&trajectory); err != nil {
		return TrajectoryV1{}, fmt.Errorf("eval: decode trajectory: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return TrajectoryV1{}, fmt.Errorf("eval: decode trajectory: %w", err)
	}
	if trajectory.Version != TrajectoryVersionV1 || trajectory.SessionID == "" {
		return TrajectoryV1{}, fmt.Errorf("eval: unsupported trajectory identity")
	}
	if err := validateTrajectoryPayloads(trajectory); err != nil {
		return TrajectoryV1{}, err
	}
	return trajectory, nil
}

func validateTrajectoryPayloads(trajectory TrajectoryV1) error {
	if len(trajectory.Tables) != len(trajectoryTableSpecs) {
		return fmt.Errorf("eval: trajectory table set is incomplete")
	}
	blobPayloads := make(map[string][]byte, len(trajectory.Blobs))
	for _, blob := range trajectory.Blobs {
		if !blobstore.ValidDigest(blob.SHA256) || base64.StdEncoding.DecodedLen(len(blob.Base64)) > trajectoryPayloadLimit {
			return fmt.Errorf("eval: invalid trajectory blob %s", blob.SHA256)
		}
		if _, exists := blobPayloads[blob.SHA256]; exists {
			return fmt.Errorf("eval: duplicate trajectory blob %s", blob.SHA256)
		}
		payload, err := base64.StdEncoding.DecodeString(blob.Base64)
		if err != nil || len(payload) > trajectoryPayloadLimit || sumHex(payload) != blob.SHA256 {
			return fmt.Errorf("eval: invalid trajectory blob %s", blob.SHA256)
		}
		blobPayloads[blob.SHA256] = payload
	}
	for index, spec := range trajectoryTableSpecs {
		table := trajectory.Tables[index]
		expectedColumns := trajectoryExpectedColumns[spec.name]
		if table.Name != spec.name || !slices.Equal(table.Columns, expectedColumns) {
			return fmt.Errorf("eval: trajectory table %d does not match %s schema", index, spec.name)
		}
		for rowIndex, row := range table.Rows {
			if len(row.Values) != len(expectedColumns) {
				return fmt.Errorf("eval: %s row %d has incomplete columns", table.Name, rowIndex)
			}
			for _, column := range expectedColumns {
				value, exists := row.Values[column]
				if !exists {
					return fmt.Errorf("eval: %s row %d is missing column %s", table.Name, rowIndex, column)
				}
				if err := validateStoredValue(value); err != nil {
					return fmt.Errorf("eval: invalid %s.%s value: %w", table.Name, column, err)
				}
			}
			for _, mapping := range payloadDigestMappings[table.Name] {
				digest, err := storedString(row.Values[mapping.digestColumn])
				if err != nil {
					return fmt.Errorf("eval: invalid %s.%s digest: %w", table.Name, mapping.digestColumn, err)
				}
				if digest == "" {
					if mapping.requiredBlob {
						return fmt.Errorf("eval: %s.%s requires a payload digest", table.Name, mapping.digestColumn)
					}
					continue
				}
				if !blobstore.ValidDigest(digest) {
					return fmt.Errorf("eval: invalid %s.%s digest %q", table.Name, mapping.digestColumn, digest)
				}
				requiresBlob := mapping.requiredBlob
				if mapping.inlineColumn != "" {
					inline, present, err := row.Values[mapping.inlineColumn].Bytes()
					if err != nil {
						return fmt.Errorf("eval: invalid %s.%s payload: %w", table.Name, mapping.inlineColumn, err)
					}
					if present {
						inlineDigest := sumHex(inline)
						if len(inline) == 0 && inlineDigest != digest {
							requiresBlob = true
						} else if inlineDigest != digest {
							return fmt.Errorf("eval: inline payload %s.%s disagrees with stored digest", table.Name, mapping.inlineColumn)
						} else {
							requiresBlob = false
						}
					} else {
						requiresBlob = true
					}
				}
				if requiresBlob {
					if _, exists := blobPayloads[digest]; !exists {
						return fmt.Errorf("eval: payload %s from %s is missing from trajectory blobs", digest, table.Name)
					}
				}
			}
		}
	}
	return nil
}

func validateStoredValue(value StoredValueV1) error {
	switch value.Kind {
	case "null":
		if value.Integer != nil || value.Real != nil || value.Base64 != "" {
			return fmt.Errorf("null value carries data")
		}
	case "integer":
		if value.Integer == nil || value.Real != nil || value.Base64 != "" {
			return fmt.Errorf("integer value has invalid representation")
		}
	case "real":
		if value.Integer != nil || value.Real == nil || value.Base64 != "" {
			return fmt.Errorf("real value has invalid representation")
		}
	case "text", "blob":
		if value.Integer != nil || value.Real != nil {
			return fmt.Errorf("%s value has invalid representation", value.Kind)
		}
		if _, _, err := value.Bytes(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported storage kind %q", value.Kind)
	}
	return nil
}

func sumHex(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
