package verification

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestArtifactResultStoreSurvivesReopenAndReturnsLatest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "verification.db")
	provider, err := sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "test"}); err != nil {
		t.Fatal(err)
	}
	store, err := NewArtifactResultStore(sessions, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	work, plan := guardrailWork(t)
	first := verificationResultFor(work, plan, "pass")
	first.ID = "result-1"
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := verificationResultFor(work, plan, "fail")
	second.ID = "result-2"
	second.CompletedAt = first.CompletedAt.Add(time.Second)
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	store, err = NewArtifactResultStore(session.NewService(provider.DB(), provider.Blobs()), "session", "resumed-run")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Latest(ctx, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != second.ID || loaded.Status != "fail" {
		t.Fatalf("latest result = %+v", loaded)
	}
}

func TestArtifactResultStoreFailsClosedForMissingOrInvalidLatestArtifact(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "verification.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "test"}); err != nil {
		t.Fatal(err)
	}
	store, err := NewArtifactResultStore(sessions, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Latest(ctx, "missing"); !errors.Is(err, ErrNoVerificationResult) {
		t.Fatalf("missing result error = %v", err)
	}
	kind, err := verificationResultArtifactKind("work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.PutArtifact(ctx, "session", "run", kind, []byte(`{"version":1,"unknown":true}`), "invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Latest(ctx, "work"); err == nil {
		t.Fatal("accepted invalid verification artifact")
	}
}
