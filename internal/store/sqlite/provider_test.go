package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/contract"
)

func TestStoreProviderContract(t *testing.T) {
	contract.RunStoreProviderContractTests(t, func(t *testing.T) (api.StoreProvider, func()) {
		t.Helper()
		provider, err := Open(context.Background(), ":memory:")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return provider, func() {
			if err := provider.Close(context.Background()); err != nil {
				t.Errorf("Close: %v", err)
			}
		}
	})
}

func TestDuplicateEnvelopeReturnsIdempotencyConflict(t *testing.T) {
	ctx := context.Background()
	provider, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)

	envelope := api.TaskEnvelope{
		ID:        "env-1",
		RunID:     "run-1",
		TaskID:    "task-1",
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
	}
	first, err := provider.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.MailboxOutbox().QueueEnvelope(ctx, envelope); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	second, err := provider.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback(ctx)
	err = second.MailboxOutbox().QueueEnvelope(ctx, envelope)
	if !errors.Is(err, api.ErrIdempotencyConflict) {
		t.Fatalf("duplicate envelope error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestOpenMigratesVersionOneCredentialSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	INSERT INTO schema_migrations(version) VALUES (1);
	PRAGMA user_version = 1;`); err != nil {
		db.Close()
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
	if err := provider.DB().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	var table string
	if err := provider.DB().QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='auth_credentials'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
}
