package session

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestHistorySearchLongSessionUsesBoundedLookup(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := NewService(store.DB(), store.Blobs())
	if _, err := svc.Ensure(ctx, Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 5000; i++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO context_artifacts(id,session_id,kind,sha256,preview,created_at) VALUES(?,'s','tool_result',?,'needle',0)`, fmt.Sprint(i), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	records, err := svc.SearchHistory(searchCtx, "s", "needle", 8, 4096, 16384)
	if err != nil || len(records) != 8 {
		t.Fatalf("long-session search: count=%d err=%v", len(records), err)
	}
}
