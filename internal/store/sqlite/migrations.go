package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

const schemaVersion = 4

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS records (
		kind TEXT NOT NULL,
		key1 TEXT NOT NULL,
		key2 TEXT NOT NULL DEFAULT '',
		run_id TEXT NOT NULL DEFAULT '',
		task_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL DEFAULT 0,
		tool_name TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL DEFAULT '',
		data BLOB NOT NULL,
		PRIMARY KEY (kind, key1, key2)
	);
	CREATE INDEX IF NOT EXISTS records_kind_run ON records(kind, run_id, created_at, key1);
	CREATE INDEX IF NOT EXISTS records_kind_task ON records(kind, task_id, created_at, key1);
	CREATE UNIQUE INDEX IF NOT EXISTS action_attempt_idempotency
		ON records(kind, run_id, task_id, tool_name, idempotency_key)
		WHERE kind = 'action_attempt' AND idempotency_key <> '';
	CREATE TABLE IF NOT EXISTS events (
		run_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		recorded_at INTEGER NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY (run_id, sequence)
	);
	CREATE TABLE IF NOT EXISTS leases (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		holder_id TEXT NOT NULL,
		status TEXT NOT NULL,
		expires_at INTEGER NOT NULL,
		version INTEGER NOT NULL,
		data BLOB NOT NULL
	);
	CREATE INDEX IF NOT EXISTS leases_task_version ON leases(run_id, task_id, version DESC);
	CREATE UNIQUE INDEX IF NOT EXISTS leases_active_slot ON leases(run_id, task_id)
		WHERE status = 'active';
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		provider_id TEXT NOT NULL DEFAULT '',
		model_id TEXT NOT NULL DEFAULT '',
		reasoning TEXT NOT NULL DEFAULT '',
		agent_mode TEXT NOT NULL DEFAULT 'single',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS session_projections (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		last_run_id TEXT NOT NULL DEFAULT '',
		blocks BLOB NOT NULL DEFAULT '[]',
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS accounts (
		id TEXT NOT NULL,
		provider_id TEXT NOT NULL,
		email TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL DEFAULT '',
		plan TEXT NOT NULL DEFAULT '',
		credential_ref TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(provider_id, id)
	);
	CREATE TABLE IF NOT EXISTS model_catalog (
		provider_id TEXT NOT NULL,
		account_id TEXT NOT NULL,
		model_id TEXT NOT NULL,
		etag TEXT NOT NULL DEFAULT '',
		fetched_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY(provider_id, account_id, model_id)
	);
	CREATE TABLE IF NOT EXISTS subagent_runs (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		parent_run_id TEXT NOT NULL,
		parent_agent_id TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL,
		state TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL,
		finished_at INTEGER NOT NULL DEFAULT 0
	);`,
	`CREATE TABLE IF NOT EXISTS auth_credentials (
		provider_id TEXT NOT NULL,
		account_id TEXT NOT NULL,
		data TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(provider_id, account_id)
	);`,
	`CREATE TABLE IF NOT EXISTS subagent_runs (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		parent_run_id TEXT NOT NULL,
		parent_agent_id TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL,
		state TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL,
		finished_at INTEGER NOT NULL DEFAULT 0
	);
	ALTER TABLE subagent_runs RENAME COLUMN role TO subagent_type;
	ALTER TABLE subagent_runs ADD COLUMN child_run_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN description TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN model TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN reasoning TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN capability_mode TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN requested_isolation TEXT NOT NULL DEFAULT 'none';
	ALTER TABLE subagent_runs ADD COLUMN isolation TEXT NOT NULL DEFAULT 'none';
	ALTER TABLE subagent_runs ADD COLUMN cwd TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN background INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE subagent_runs ADD COLUMN output TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN error TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN warning TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN transcript BLOB NOT NULL DEFAULT X'';
	ALTER TABLE subagent_runs ADD COLUMN tool_calls INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE subagent_runs ADD COLUMN turns INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE subagent_runs ADD COLUMN tokens_used INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE subagent_runs ADD COLUMN tools_used BLOB NOT NULL DEFAULT '[]';
	ALTER TABLE subagent_runs ADD COLUMN worktree_path TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN completion_delivered INTEGER NOT NULL DEFAULT 0;
	UPDATE subagent_runs SET state='completed' WHERE state='succeeded';
	CREATE INDEX IF NOT EXISTS subagent_runs_session_started ON subagent_runs(session_id, started_at);
	CREATE INDEX IF NOT EXISTS subagent_runs_parent_state ON subagent_runs(parent_run_id, state);`,
	`CREATE TABLE session_todos (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		goal TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 0,
		phases BLOB NOT NULL DEFAULT '[]',
		updated_at INTEGER NOT NULL
	);`,
}

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema %d is newer than supported schema %d", version, schemaVersion)
	}
	for version < schemaVersion {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version+1, err)
		}
		next := version + 1
		if _, err := tx.ExecContext(ctx, migrations[next-1]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", next, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, next); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", next, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, next)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set schema version %d: %w", next, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", next, err)
		}
		version = next
	}
	return nil
}
