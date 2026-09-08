package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationV29AddsPromptQueuesWithoutChangingSessionState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "queue-v28.db")
	database, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	for version := range 28 {
		if _, err := database.ExecContext(ctx, migrations[version]); err != nil {
			t.Fatalf("apply migration %d: %v", version+1, err)
		}
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO sessions(id,title,created_at,updated_at) VALUES('session','Retained',1,2);
		INSERT INTO session_projections(session_id,last_run_id,model_history,usage,updated_at)
			VALUES('session','run-1','{}','{}',2);
		INSERT INTO session_todos(session_id,goal,revision,phases,updated_at)
			VALUES('session','Ship',3,'[]',2);
		PRAGMA user_version=28;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var version, queues, todos int
	if err := provider.DB().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := provider.DB().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='session_prompt_queues'`).Scan(&queues); err != nil {
		t.Fatal(err)
	}
	if err := provider.DB().QueryRowContext(ctx, `SELECT count(*) FROM session_todos WHERE session_id='session' AND goal='Ship' AND revision=3`).Scan(&todos); err != nil {
		t.Fatal(err)
	}
	if version != 29 || queues != 1 || todos != 1 {
		t.Fatalf("migration version=%d queues=%d retained todos=%d", version, queues, todos)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	var title, lastRun string
	if err := reopened.DB().QueryRowContext(ctx, `SELECT s.title,p.last_run_id FROM sessions s JOIN session_projections p ON p.session_id=s.id WHERE s.id='session'`).Scan(&title, &lastRun); err != nil {
		t.Fatal(err)
	}
	if title != "Retained" || lastRun != "run-1" {
		t.Fatalf("reopened session title=%q lastRun=%q", title, lastRun)
	}
}
