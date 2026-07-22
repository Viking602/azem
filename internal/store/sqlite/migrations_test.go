package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationV3PreservesV2SubagentRuns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "azem.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 2; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			_ = db.Close()
			t.Fatalf("apply fixture migration %d: %v", version, err)
		}
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version = 2`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subagent_runs(
		id, session_id, parent_run_id, parent_agent_id, tool_call_id, role, state, summary, started_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-child", "session", "parent", "parent-agent", "spawn-call", "explore", "succeeded", "legacy answer", 100, 200,
	); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := provider.Close(ctx); err != nil {
			t.Error(err)
		}
	}()

	var version int
	if err := provider.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}

	var subagentType, state, summary, output string
	var transcript, toolsUsed []byte
	var background, completionDelivered int
	if err := provider.db.QueryRowContext(ctx, `SELECT
		subagent_type, state, summary, output, transcript, tools_used, background, completion_delivered
		FROM subagent_runs WHERE id = ?`, "legacy-child").Scan(
		&subagentType, &state, &summary, &output, &transcript, &toolsUsed, &background, &completionDelivered,
	); err != nil {
		t.Fatal(err)
	}
	if subagentType != "explore" || state != "completed" || summary != "legacy answer" {
		t.Fatalf("migrated identity/state = type:%q state:%q summary:%q", subagentType, state, summary)
	}
	if output != "" || len(transcript) != 0 || string(toolsUsed) != "[]" || background != 0 || completionDelivered != 0 {
		t.Fatalf("migrated defaults = output:%q transcript:%q tools:%q background:%d delivered:%d", output, transcript, toolsUsed, background, completionDelivered)
	}
}

func TestPhase3MigrationV11ArtifactForeignKeyCascade(t *testing.T) {
	ctx := context.Background()
	provider, err := Open(ctx, filepath.Join(t.TempDir(), "v11.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	if _, err := provider.db.ExecContext(ctx, `INSERT INTO sessions(id,title,created_at,updated_at) VALUES('s','S',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.db.ExecContext(ctx, `INSERT INTO context_artifacts(id,session_id,kind,sha256,payload,created_at) VALUES('a','s','tool_result','hash',X'01',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.db.ExecContext(ctx, `DELETE FROM sessions WHERE id='s'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := provider.db.QueryRowContext(ctx, `SELECT count(*) FROM context_artifacts WHERE id='a'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count=%d err=%v", count, err)
	}
}

func TestMigrationV5CreatesMemoryAndRecapStores(t *testing.T) {
	ctx := context.Background()
	provider, err := Open(ctx, filepath.Join(t.TempDir(), "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	for _, table := range []string{"memories", "memories_fts", "recaps"} {
		var found string
		if err := provider.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name=?`, table).Scan(&found); err != nil {
			t.Fatalf("missing migrated table %s: %v", table, err)
		}
	}
	if _, err := provider.db.ExecContext(ctx, `INSERT INTO memories(id,content,anchor,provenance,status,created_at,updated_at) VALUES('m','searchable evidence','/workspace','manual','active',1,1)`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := provider.db.QueryRowContext(ctx, `SELECT count(*) FROM memories_fts WHERE memories_fts MATCH 'searchable'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("FTS trigger count=%d err=%v", count, err)
	}
	if _, err := provider.db.ExecContext(ctx, `VACUUM`); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := provider.db.QueryRowContext(ctx, `SELECT m.id FROM memories_fts f JOIN memories m ON m.memory_rowid=f.rowid WHERE memories_fts MATCH 'searchable'`).Scan(&id); err != nil || id != "m" {
		t.Fatalf("FTS mapping after VACUUM id=%q err=%v", id, err)
	}
}

func TestMigrationV7MovesProjectionBlocksIntoAppendOnlyRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "azem.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 5; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("apply migration %d: %v", version, err)
		}
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(
		id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at
	) VALUES('session','Legacy','chatgpt','gpt-test','high','single',1,2)`); err != nil {
		t.Fatal(err)
	}
	const blocks = `[{"kind":"user","runId":"run-1","content":"legacy request"}]`
	if _, err := db.ExecContext(ctx, `INSERT INTO session_projections(
		session_id,last_run_id,blocks,updated_at
	) VALUES('session','run-1',?,2)`, blocks); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	var version int
	var gotBlocks, modelHistory string
	if err := provider.db.QueryRowContext(ctx, `SELECT blocks,model_history FROM session_projections WHERE session_id='session'`).Scan(
		&gotBlocks, &modelHistory,
	); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var kind, runID, data string
	if err := provider.db.QueryRowContext(ctx, `SELECT sequence,kind,run_id,data FROM session_blocks WHERE session_id='session'`).Scan(
		&sequence, &kind, &runID, &data,
	); err != nil {
		t.Fatal(err)
	}
	if err := provider.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion || gotBlocks != "[]" || modelHistory != "{}" || sequence != 0 || kind != "user" || runID != "run-1" || data != blocks[1:len(blocks)-1] {
		t.Fatalf("migration result version=%d blocks=%q model_history=%q row=%d/%q/%q/%q", version, gotBlocks, modelHistory, sequence, kind, runID, data)
	}
}

func TestPhase6MigrationV13BackfillsCanonicalBlocksAndArtifactPreviews(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history-backfill.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 12; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("apply migration %d: %v", version, err)
		}
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=12`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,created_at,updated_at) VALUES('s',1,1);
		INSERT INTO session_projections(session_id,updated_at) VALUES('s',1);
		INSERT INTO session_blocks(session_id,sequence,kind,data) VALUES
			('s',0,'user','{"kind":"user","content":"backfilluser"}'),
			('s',1,'assistant','{"kind":"assistant","content":"backfillassistant"}'),
			('s',2,'agent','{"kind":"agent","content":"backfillagent"}'),
			('s',3,'assistant','{"kind":"assistant","content":"backfillcancelled","state":"cancelled"}');
		INSERT INTO context_artifacts(id,session_id,kind,sha256,payload,preview,created_at)
			VALUES('a','s','tool','hash',X'01','backfillartifact',1);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	for query, want := range map[string]string{"backfilluser": "sequence:0", "backfillassistant": "sequence:1", "backfillartifact": "artifact:a"} {
		var source string
		if err := provider.db.QueryRowContext(ctx, `SELECT source_id FROM history_fts WHERE history_fts MATCH ? AND session_id='s'`, query).Scan(&source); err != nil || source != want {
			t.Fatalf("query %q source=%q err=%v", query, source, err)
		}
	}
	var count int
	if err := provider.db.QueryRowContext(ctx, `SELECT count(*) FROM history_fts WHERE history_fts MATCH 'backfillagent OR backfillcancelled'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("non-final backfill count=%d err=%v", count, err)
	}
}

func TestMigrationV8AddsSubagentProviderWithLegacyDefault(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "azem.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 7; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("apply migration %d: %v", version, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subagent_runs(id,session_id,parent_run_id,subagent_type,state,started_at) VALUES('legacy','session','parent','explore','completed',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	var got string
	if err := provider.db.QueryRowContext(ctx, `SELECT provider FROM subagent_runs WHERE id='legacy'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("legacy provider = %q, want empty inherit value", got)
	}
}
