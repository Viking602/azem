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
