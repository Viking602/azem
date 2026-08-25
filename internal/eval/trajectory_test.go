package eval

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestExportTrajectoryPreservesDurableRowsAndExternalPayloads(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	provider, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	db := provider.DB()
	mustExec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement, args...); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	mustExec(`INSERT INTO sessions(id, title, created_at, updated_at) VALUES('session-1', 'one', 1, 1), ('session-2', 'two', 1, 1)`)
	block := []byte(`{"kind":"assistant","text_phase":"final_answer"}`)
	mustExec(`INSERT INTO session_blocks(session_id, sequence, kind, run_id, data, data_sha256) VALUES(?, 1, 'assistant', 'run-1', ?, ?)`, "session-1", block, sumHex(block))
	mustExec(`INSERT INTO session_blocks(session_id, sequence, kind, run_id, data, data_sha256) VALUES('session-2', 1, 'assistant', 'other-run', '{}', '')`)
	mustExec(`INSERT INTO session_tool_records(session_id, run_id, tool_call_id, name, arguments, state, content, structured, observations, started_at) VALUES('session-1', 'run-1', 'call-1', 'read', '{}', 'completed', '', 'null', '[]', 2)`)
	mustExec(`INSERT INTO provider_requests(request_id, session_id, run_id, request_kind, status, started_at) VALUES('request-1', 'session-1', 'run-1', 'main', 'completed', 3)`)
	mustExec(`INSERT INTO subagent_runs(id, session_id, parent_run_id, child_run_id, subagent_type, state, started_at) VALUES('child-1', 'session-1', 'run-1', 'child-run-1', 'review', 'completed', 4)`)
	mustExec(`INSERT INTO session_todos(session_id, goal, revision, phases, updated_at) VALUES('session-1', 'goal', 2, '[]', 5)`)
	mustExec(`INSERT INTO session_semantic_state(session_id, revision, checkpoint_id, cursor, state, source_digest, updated_at) VALUES('session-1', 1, 'checkpoint-1', '{}', '{"version":1}', 'source', 6)`)
	mustExec(`INSERT INTO session_semantic_state_events(session_id, revision, checkpoint_id, base_revision, cursor, patch, source_digest, writer_run_id, created_at) VALUES('session-1', 1, 'checkpoint-1', 0, '{}', '{}', 'source', 'run-1', 6)`)
	mustExec(`INSERT INTO context_manifests(id, session_id, run_id, policy_version, manifest_hash, data, created_at) VALUES('manifest-1', 'session-1', 'archive-run', 1, 'manifest-hash', '{}', 7)`)

	external := []byte("raw external payload\x00with bytes")
	externalDigest, err := provider.Blobs().Put(ctx, external)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO context_artifacts(id, session_id, run_id, kind, sha256, preview, created_at) VALUES('artifact-1', 'session-1', 'archive-run', 'tool_result', ?, '{}', 8)`, externalDigest)
	mustExec(`INSERT INTO events(run_id, sequence, recorded_at, data, data_sha256) VALUES('run-1', 1, 9, '{}', ?), ('archive-run', 1, 9, '{}', ''), ('other-run', 1, 9, '{}', '')`, externalDigest)
	mustExec(`INSERT INTO records(kind, key1, run_id, created_at, data, data_sha256) VALUES('run', 'record-1', 'run-1', 10, ?, ?), ('run', 'archive-record', 'archive-run', 10, '{}', ''), ('run', 'record-2', 'other-run', 10, '{}', '')`, external, externalDigest)

	trajectory, err := ExportTrajectory(ctx, db, provider.Blobs(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if trajectory.Version != 1 || trajectory.SessionID != "session-1" || len(trajectory.Tables) != len(trajectoryTableSpecs) {
		t.Fatalf("trajectory identity = %+v", trajectory)
	}
	for _, table := range trajectory.Tables {
		wantRows := 1
		if table.Name == "events" || table.Name == "records" {
			wantRows = 2
		}
		if len(table.Rows) != wantRows {
			t.Fatalf("table %s rows = %d, want %d", table.Name, len(table.Rows), wantRows)
		}
	}
	if len(trajectory.Blobs) != 1 || trajectory.Blobs[0].SHA256 != externalDigest {
		t.Fatalf("blobs = %+v", trajectory.Blobs)
	}
	decoded, err := base64.StdEncoding.DecodeString(trajectory.Blobs[0].Base64)
	if err != nil || !bytes.Equal(decoded, external) {
		t.Fatalf("external payload = %q, %v", decoded, err)
	}

	var first, second bytes.Buffer
	if err := WriteTrajectory(&first, trajectory); err != nil {
		t.Fatal(err)
	}
	if err := WriteTrajectory(&second, trajectory); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("trajectory encoding is not deterministic")
	}
	roundTrip, err := ReadTrajectory(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, trajectory) {
		t.Fatalf("round trip changed trajectory\n got: %#v\nwant: %#v", roundTrip, trajectory)
	}
	missingBlobs := trajectory
	missingBlobs.Blobs = nil
	if err := validateTrajectoryPayloads(missingBlobs); err == nil {
		t.Fatal("accepted trajectory with a missing referenced blob")
	}

	truncated := trajectory
	truncated.Tables = append([]TrajectoryTableV1(nil), trajectory.Tables...)
	truncated.Tables[0].Rows = append([]TrajectoryRowV1(nil), trajectory.Tables[0].Rows...)
	truncated.Tables[0].Rows[0].Values = cloneStoredValues(trajectory.Tables[0].Rows[0].Values)
	delete(truncated.Tables[0].Rows[0].Values, truncated.Tables[0].Columns[0])
	if err := validateTrajectoryPayloads(truncated); err == nil {
		t.Fatal("accepted trajectory row with a missing declared column")
	}
}

func TestReadTrajectoryRejectsBlobDigestMismatch(t *testing.T) {
	t.Parallel()
	trajectory := emptyValidTrajectory()
	trajectory.Blobs = []TrajectoryBlobV1{{
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Base64: "eA==",
	}}
	raw, err := json.Marshal(trajectory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrajectory(bytes.NewReader(raw)); err == nil {
		t.Fatal("accepted trajectory with mismatched blob digest")
	}
}

func TestExportRejectsInlinePayloadDigestMismatchEvenWhenBlobExists(t *testing.T) {
	t.Parallel()
	payload := []byte("inline")
	trajectory := emptyValidTrajectory()
	values := make(map[string]StoredValueV1, len(trajectory.Tables[0].Columns))
	for _, column := range trajectory.Tables[0].Columns {
		values[column] = StoredValueV1{Kind: "null"}
	}
	values["data"] = StoredValueV1{Kind: "blob", Base64: base64.StdEncoding.EncodeToString(payload)}
	values["data_sha256"] = StoredValueV1{Kind: "text", Base64: base64.StdEncoding.EncodeToString([]byte(sumHex([]byte("different"))))}
	trajectory.Tables[0].Rows = []TrajectoryRowV1{{Values: values}}
	trajectory.Blobs = []TrajectoryBlobV1{{SHA256: sumHex([]byte("different")), Base64: base64.StdEncoding.EncodeToString([]byte("different"))}}
	if err := validateTrajectoryPayloads(trajectory); err == nil {
		t.Fatal("accepted inline payload with mismatched stored digest")
	}
}

func TestTrajectoryRejectsJSONSentinelForTextPayload(t *testing.T) {
	trajectory := emptyValidTrajectory()
	payload := []byte("external text payload")
	digest := sumHex(payload)
	for index := range trajectory.Tables {
		if trajectory.Tables[index].Name != "session_tool_records" {
			continue
		}
		values := make(map[string]StoredValueV1, len(trajectory.Tables[index].Columns))
		for _, column := range trajectory.Tables[index].Columns {
			values[column] = StoredValueV1{Kind: "null"}
		}
		values["content"] = StoredValueV1{Kind: "text", Base64: base64.StdEncoding.EncodeToString([]byte("{}"))}
		values["content_sha256"] = StoredValueV1{Kind: "text", Base64: base64.StdEncoding.EncodeToString([]byte(digest))}
		trajectory.Tables[index].Rows = []TrajectoryRowV1{{Values: values}}
		trajectory.Blobs = []TrajectoryBlobV1{{SHA256: digest, Base64: base64.StdEncoding.EncodeToString(payload)}}
		if err := validateTrajectoryPayloads(trajectory); err == nil {
			t.Fatal("accepted JSON sentinel for text payload")
		}
		return
	}
	t.Fatal("session_tool_records table is unavailable")
}

func emptyValidTrajectory() TrajectoryV1 {
	trajectory := TrajectoryV1{Version: 1, SessionID: "session", Tables: make([]TrajectoryTableV1, 0, len(trajectoryTableSpecs))}
	for _, spec := range trajectoryTableSpecs {
		trajectory.Tables = append(trajectory.Tables, TrajectoryTableV1{
			Name: spec.name, Columns: append([]string(nil), trajectoryExpectedColumns[spec.name]...), Rows: []TrajectoryRowV1{},
		})
	}
	return trajectory
}

func cloneStoredValues(source map[string]StoredValueV1) map[string]StoredValueV1 {
	cloned := make(map[string]StoredValueV1, len(source))
	for column, value := range source {
		cloned[column] = value
	}
	return cloned
}
