package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/blobstore"
)

func TestLargeEventsAndRecordsSpillOutOfSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.db")
	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	work, err := provider.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("event-body ", 2000)
	event := agentruntime.Event{RunID: "run-large", Sequence: 1, Type: agentruntime.EventTaskCompleted, RecordedAt: time.Now().UTC(), Payload: map[string]any{"text": payload}}
	if err := work.Events().AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := work.Runs().SaveRun(ctx, agentruntime.Run{ID: "run-large", Status: agentruntime.RunStatusCompleted, CreatedAt: time.Now().UTC(), Metadata: map[string]string{"note": payload}}); err != nil {
		t.Fatal(err)
	}
	if err := work.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var eventInline, recordInline int
	if err := provider.DB().QueryRowContext(ctx, `SELECT
		(SELECT length(data) FROM events WHERE run_id='run-large'),
		(SELECT length(data) FROM records WHERE kind='run' AND key1='run-large')`).Scan(&eventInline, &recordInline); err != nil {
		t.Fatal(err)
	}
	if eventInline > 8 || recordInline > 8 {
		t.Fatalf("large control-plane payloads stayed inline event=%d record=%d", eventInline, recordInline)
	}
	listed, err := provider.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	events, err := listed.Events().ListEvents(ctx, "run-large")
	if err != nil {
		t.Fatal(err)
	}
	if err := listed.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("listed events=%d", len(events))
	}
	text, _ := events[0].Payload["text"].(string)
	if text != payload {
		t.Fatalf("hydrated event payload len=%d", len(text))
	}
}

func TestSQLiteDSNWaitsForSerializedWriter(t *testing.T) {
	if got := sqliteDSN("azem.db", false); !strings.Contains(got, "busy_timeout(30000)") {
		t.Fatalf("sqlite DSN = %q, want 30 second writer wait", got)
	}
}

func TestProviderQueuesImmediateTransactionsOnOneConnection(t *testing.T) {
	provider, err := Open(t.Context(), filepath.Join(t.TempDir(), "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(t.Context())
	if got := provider.DB().Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("max open connections = %d, want 1", got)
	}
}

func TestDuplicateEnvelopeReturnsIdempotencyConflict(t *testing.T) {
	ctx := context.Background()
	provider, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)

	envelope := agentruntime.TaskEnvelope{
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
	duplicate := envelope
	duplicate.Payload = map[string]any{"body": strings.Repeat("duplicate-payload ", 1_000)}
	duplicateData, err := marshalJSON(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	duplicateDigest := blobstore.Sum(duplicateData)
	err = second.MailboxOutbox().QueueEnvelope(ctx, duplicate)
	if !errors.Is(err, agentruntime.ErrIdempotencyConflict) {
		t.Fatalf("duplicate envelope error = %v, want ErrIdempotencyConflict", err)
	}
	if _, err := provider.Blobs().Get(ctx, duplicateDigest); err == nil {
		t.Fatal("failed duplicate envelope installed an unreferenced blob")
	}
	if err := second.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	rolledBack := agentruntime.TaskEnvelope{
		ID: "env-rollback", RunID: "run-1", TaskID: "task-1", Status: "pending", CreatedAt: time.Now().UTC(),
		Payload: map[string]any{"body": strings.Repeat("rollback-payload ", 1_000)},
	}
	rolledBackData, err := marshalJSON(rolledBack)
	if err != nil {
		t.Fatal(err)
	}
	rolledBackDigest := blobstore.Sum(rolledBackData)
	third, err := provider.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.MailboxOutbox().QueueEnvelope(ctx, rolledBack); err != nil {
		t.Fatal(err)
	}
	loaded, err := third.MailboxOutbox().LoadEnvelope(ctx, rolledBack.ID)
	if err != nil || loaded.Payload["body"] != rolledBack.Payload["body"] {
		t.Fatalf("staged envelope = %#v, error=%v", loaded, err)
	}
	if _, err := provider.Blobs().Get(ctx, rolledBackDigest); err == nil {
		t.Fatal("uncommitted envelope installed its blob before commit")
	}
	if err := third.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Blobs().Get(ctx, rolledBackDigest); err == nil {
		t.Fatal("rolled-back envelope left an unreferenced blob")
	}
}

func TestUnitOfWorkDoesNotInstallSupersededLargePayload(t *testing.T) {
	ctx := t.Context()
	provider, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	work, err := provider.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	large := agentruntime.Run{
		ID: "superseded", Status: agentruntime.RunStatusRunning, CreatedAt: time.Now().UTC(),
		Metadata: map[string]string{"body": strings.Repeat("superseded-payload ", 1_000)},
	}
	largeData, err := marshalJSON(large)
	if err != nil {
		t.Fatal(err)
	}
	digest := blobstore.Sum(largeData)
	if err := work.Runs().SaveRun(ctx, large); err != nil {
		t.Fatal(err)
	}
	large.Metadata = map[string]string{"body": "current"}
	if err := work.Runs().SaveRun(ctx, large); err != nil {
		t.Fatal(err)
	}
	if err := work.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Blobs().Get(ctx, digest); err == nil {
		t.Fatal("superseded payload installed an unreferenced blob")
	}
}

type failSecondPutBlobStore struct {
	blobstore.Store
	puts int
}

type failDeleteBlobStore struct {
	blobstore.Store
}

func (s *failDeleteBlobStore) Delete(context.Context, string) error {
	return errors.New("injected blob delete failure")
}

type countingGetBlobStore struct {
	blobstore.Store
	gets int
}

func (s *countingGetBlobStore) Get(ctx context.Context, digest string) ([]byte, error) {
	s.gets++
	return s.Store.Get(ctx, digest)
}

func TestListRunsFiltersStatusBeforeHydratingPayloads(t *testing.T) {
	ctx := t.Context()
	provider := openMemoryTestProvider(t)
	blobs := &countingGetBlobStore{Store: blobstore.NewMemory()}
	provider.blobs = blobs
	work := beginTestUnitOfWork(t, provider)
	createdAt := time.Now().UTC()
	for _, run := range []agentruntime.Run{
		{ID: "completed", Status: agentruntime.RunStatusCompleted, CreatedAt: createdAt, Metadata: map[string]string{"body": strings.Repeat("completed ", 2_000)}},
		{ID: "running", Status: agentruntime.RunStatusRunning, CreatedAt: createdAt, Metadata: map[string]string{"body": strings.Repeat("running ", 2_000)}},
	} {
		mustSaveRun(t, ctx, work, run)
	}
	if err := work.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	blobs.gets = 0
	read := beginTestUnitOfWork(t, provider)
	runs, err := read.Runs().ListRuns(ctx, agentruntime.RunSelector{Statuses: []agentruntime.RunStatus{agentruntime.RunStatusRunning}})
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != "running" {
		t.Fatalf("runs = %+v, want running", runs)
	}
	if blobs.gets != 1 {
		t.Fatalf("blob reads = %d, want 1", blobs.gets)
	}
}

func (s *failSecondPutBlobStore) InstallAt(ctx context.Context, digest string, payload []byte) (bool, error) {
	s.puts++
	if s.puts == 2 {
		return false, errors.New("injected blob write failure")
	}
	return s.Store.InstallAt(ctx, digest, payload)
}

func TestUnitOfWorkRemovesInstalledBlobsWhenLaterPutFails(t *testing.T) {
	ctx := t.Context()
	provider := openMemoryTestProvider(t)
	memory := blobstore.NewMemory()
	provider.blobs = &failSecondPutBlobStore{Store: memory}
	work := beginTestUnitOfWork(t, provider)
	payload := strings.Repeat("partial-commit ", 1_000)
	run := agentruntime.Run{
		ID: "partial-run", Status: agentruntime.RunStatusRunning, CreatedAt: time.Now().UTC(),
		Metadata: map[string]string{"body": payload},
	}
	event := agentruntime.Event{
		RunID: "partial-run", Sequence: 1, Type: agentruntime.EventTaskCompleted, RecordedAt: time.Now().UTC(),
		Payload: map[string]any{"body": payload + "event"},
	}
	mustSaveRun(t, ctx, work, run)
	mustAppendEvent(t, ctx, work, event)
	assertCommitFailsWith(t, ctx, work, "injected blob write failure")
	assertBlobsMissing(t, ctx, memory, digestOf(t, run), digestOf(t, event))
}

func TestUnitOfWorkDoesNotFailCommittedMutationWhenObsoleteBlobCleanupFails(t *testing.T) {
	ctx := t.Context()
	provider := openMemoryTestProvider(t)
	memory := blobstore.NewMemory()
	provider.blobs = &failDeleteBlobStore{Store: memory}
	run := agentruntime.Run{
		ID: "cleanup-failure", Status: agentruntime.RunStatusRunning, CreatedAt: time.Now().UTC(),
		Metadata: map[string]string{"body": strings.Repeat("obsolete ", 2_000)},
	}
	first := beginTestUnitOfWork(t, provider)
	mustSaveRun(t, ctx, first, run)
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	obsoleteDigest := digestOf(t, run)

	run.Metadata = map[string]string{"body": strings.Repeat("current ", 2_000)}
	second := beginTestUnitOfWork(t, provider)
	mustSaveRun(t, ctx, second, run)
	if err := second.Commit(ctx); err != nil {
		t.Fatalf("committed mutation reported cleanup failure: %v", err)
	}
	read := beginTestUnitOfWork(t, provider)
	runs, err := read.Runs().ListRuns(ctx, agentruntime.RunSelector{})
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Metadata["body"] != run.Metadata["body"] {
		t.Fatalf("committed run = %+v, want current payload", runs)
	}
	if exists, err := memory.Exists(ctx, obsoleteDigest); err != nil || !exists {
		t.Fatalf("best-effort cleanup retained old blob=%v error=%v", exists, err)
	}
}

func TestUnitOfWorkRemovesInstalledBlobWhenSQLCommitFails(t *testing.T) {
	ctx := t.Context()
	provider := openMemoryTestProvider(t)
	work := beginTestUnitOfWork(t, provider).(*unitOfWork)
	deferForeignKeyCommitFailure(t, ctx, work)
	run := agentruntime.Run{
		ID: "failed-sql-commit", Status: agentruntime.RunStatusRunning, CreatedAt: time.Now().UTC(),
		Metadata: map[string]string{"body": strings.Repeat("failed-sql-commit ", 1_000)},
	}
	mustSaveRun(t, ctx, work, run)
	assertCommitFailsWith(t, ctx, work, "FOREIGN KEY constraint failed")
	assertBlobsMissing(t, ctx, provider.Blobs(), digestOf(t, run))
}

func openMemoryTestProvider(t *testing.T) *Provider {
	t.Helper()
	provider, err := Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return provider
}

func beginTestUnitOfWork(t *testing.T, provider *Provider) agentruntime.UnitOfWork {
	t.Helper()
	work, err := provider.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func digestOf(t *testing.T, value any) string {
	t.Helper()
	data, err := marshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return blobstore.Sum(data)
}

func mustSaveRun(t *testing.T, ctx context.Context, work agentruntime.UnitOfWork, run agentruntime.Run) {
	t.Helper()
	if err := work.Runs().SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
}

func mustAppendEvent(t *testing.T, ctx context.Context, work agentruntime.UnitOfWork, event agentruntime.Event) {
	t.Helper()
	if err := work.Events().AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
}

func assertCommitFailsWith(t *testing.T, ctx context.Context, work agentruntime.UnitOfWork, expected string) {
	t.Helper()
	if err := work.Commit(ctx); err == nil || !strings.Contains(err.Error(), expected) {
		t.Fatalf("commit error = %v", err)
	}
}

func assertBlobsMissing(t *testing.T, ctx context.Context, store blobstore.Store, digests ...string) {
	t.Helper()
	for _, digest := range digests {
		if exists, err := store.Exists(ctx, digest); err != nil {
			t.Fatal(err)
		} else if exists {
			t.Fatalf("failed commit left blob %s", digest)
		}
	}
}

func deferForeignKeyCommitFailure(t *testing.T, ctx context.Context, work *unitOfWork) {
	t.Helper()
	if _, err := work.tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := work.tx.ExecContext(ctx, `INSERT INTO session_blocks (
		session_id, sequence, kind, data
	) VALUES ('missing-session', 1, 'user', '{}')`); err != nil {
		t.Fatal(err)
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
				for version := 1; version <= 6; version++ {
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
