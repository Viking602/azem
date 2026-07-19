package recap_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/recap"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestUpsertLoadAndRevision(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	now := time.Now().UnixMilli()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO sessions(id,created_at,updated_at) VALUES('s',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := recap.NewService(store.DB(), t.TempDir())
	first, err := svc.Upsert(ctx, recap.Recap{SessionID: "s", CoveredBoundary: "run-1", Goal: "goal", Summary: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Upsert(ctx, recap.Recap{SessionID: "s", CoveredBoundary: "run-2", Goal: "goal", Summary: "second", OpenItems: "open"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := svc.Load(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || second.Revision != 2 || loaded.Revision != 2 || loaded.Summary != "second" || loaded.CoveredBoundary != "run-2" {
		t.Fatalf("revisions/loaded = %#v %#v %#v", first, second, loaded)
	}
}

func TestRecapCannotMoveBetweenWorkspaceAnchors(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	now := time.Now().UnixMilli()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO sessions(id,created_at,updated_at) VALUES('s',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	one := recap.NewService(store.DB(), t.TempDir())
	two := recap.NewService(store.DB(), t.TempDir())
	if _, err := one.Upsert(ctx, recap.Recap{SessionID: "s", Summary: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := two.Upsert(ctx, recap.Recap{SessionID: "s", Summary: "two"}); err == nil {
		t.Fatal("cross-workspace recap update unexpectedly succeeded")
	}
	loaded, err := one.Load(ctx, "s")
	if err != nil || loaded.Summary != "one" || loaded.Revision != 1 {
		t.Fatalf("original recap moved or changed: %#v, %v", loaded, err)
	}
}

func TestRecapRedactsCommonSecretsAndBoundsFields(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	now := time.Now().UnixMilli()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO sessions(id,created_at,updated_at) VALUES('s',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	svc := recap.NewService(store.DB(), t.TempDir())
	value, err := svc.Upsert(ctx, recap.Recap{
		SessionID: "s",
		Goal:      "API_KEY=super-secret-value",
		Summary:   "Authorization: Bearer token-value and ghp_1234567890abcdefghij",
		OpenItems: strings.Repeat("x", 2000),
	})
	if err != nil {
		t.Fatal(err)
	}
	combined := value.Goal + value.Summary
	if strings.Contains(combined, "super-secret-value") || strings.Contains(combined, "token-value") || strings.Contains(combined, "ghp_") || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("recap secret redaction failed: %q", combined)
	}
	if len([]rune(value.OpenItems)) != 1600 {
		t.Fatalf("open items length=%d, want 1600", len([]rune(value.OpenItems)))
	}
}
