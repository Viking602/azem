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
	if version != 7 || gotBlocks != "[]" || modelHistory != "{}" || sequence != 0 || kind != "user" || runID != "run-1" || data != blocks[1:len(blocks)-1] {
		t.Fatalf("migration result version=%d blocks=%q model_history=%q row=%d/%q/%q/%q", version, gotBlocks, modelHistory, sequence, kind, runID, data)
	}
}
