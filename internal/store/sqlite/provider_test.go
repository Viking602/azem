package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
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

func TestBackupDatabaseIncludesUncheckpointedWAL(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wal.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA wal_autocheckpoint=0`,
		`CREATE TABLE values_test(value TEXT NOT NULL)`,
		`INSERT INTO values_test(value) VALUES('committed-in-wal')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := backupDatabase(ctx, db, path); err != nil {
		t.Fatal(err)
	}
	backup, err := sql.Open("sqlite", sqliteDSN(path+".bak", false))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var value string
	if err := backup.QueryRowContext(ctx, `SELECT value FROM values_test`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "committed-in-wal" {
		t.Fatalf("backup value = %q", value)
	}
}

func TestOpenBacksUpOnlyWhenSchemaUpgradeIsRequired(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "current.db")
	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current schema created backup: %v", err)
	}
}

func TestConcurrentAgentsCanCreateOpenAndUpgradeSharedDatabase(t *testing.T) {
	for _, fixture := range []string{"create", "current", "upgrade"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "shared.db")
			switch fixture {
			case "current":
				provider, err := Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				if err := provider.Close(ctx); err != nil {
					t.Fatal(err)
				}
			case "upgrade":
				db, err := sql.Open("sqlite", sqliteDSN(path, false))
				if err != nil {
					t.Fatal(err)
				}
				for version := 1; version < schemaVersion; version++ {
					if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
						t.Fatalf("apply fixture migration %d: %v", version, err)
					}
				}
				if _, err := db.ExecContext(ctx, `PRAGMA user_version = 6`); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}

			const agents = 8
			providers := make([]*Provider, agents)
			errorsByAgent := make([]error, agents)
			var wait sync.WaitGroup
			start := make(chan struct{})
			for index := range agents {
				wait.Add(1)
				go func() {
					defer wait.Done()
					<-start
					providers[index], errorsByAgent[index] = Open(ctx, path)
				}()
			}
			close(start)
			wait.Wait()
			for index, err := range errorsByAgent {
				if err != nil {
					t.Fatalf("agent %d open: %v", index, err)
				}
				defer providers[index].Close(ctx)
			}
			for index, provider := range providers {
				if _, err := provider.db.ExecContext(ctx, `INSERT INTO sessions(id,created_at,updated_at) VALUES(?,?,?)`, index, index, index); err != nil {
					t.Fatalf("agent %d write: %v", index, err)
				}
			}
			var version, sessions int
			if err := providers[0].db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := providers[0].db.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if version != schemaVersion || sessions != agents {
				t.Fatalf("shared database version=%d sessions=%d", version, sessions)
			}
			_, backupErr := os.Stat(path + ".bak")
			if fixture == "upgrade" && backupErr != nil {
				t.Fatalf("upgrade backup: %v", backupErr)
			}
			if fixture != "upgrade" && !errors.Is(backupErr, os.ErrNotExist) {
				t.Fatalf("%s unexpectedly created backup: %v", fixture, backupErr)
			}
		})
	}
}
