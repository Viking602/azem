package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/memory"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestCRUDSearchScopeAndLimits(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	one := memory.NewService(store.DB(), t.TempDir())
	two := memory.NewService(store.DB(), t.TempDir())
	item, err := one.Remember(ctx, "  durable alpha evidence  ", "session-1", "manual", 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = two.Remember(ctx, "alpha in another workspace", "session-2", "runtime", 1); err != nil {
		t.Fatal(err)
	}
	got, err := one.List(ctx, "alpha", 5)
	if err != nil || len(got) != 1 || got[0].ID != item.ID || got[0].Content != "durable alpha evidence" {
		t.Fatalf("search = %#v, %v", got, err)
	}
	if err := one.Forget(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	got, err = one.List(ctx, "", 20)
	if err != nil || len(got) != 0 {
		t.Fatalf("forgotten list = %#v, %v", got, err)
	}
	if _, err := one.Remember(ctx, strings.Repeat("x", memory.MaxContentRunes+1), "", "manual", 0); err == nil {
		t.Fatal("oversized memory accepted")
	}
	if _, err := one.Remember(ctx, "  ", "", "manual", 0); err == nil {
		t.Fatal("empty memory accepted")
	}
}

func TestInvalidFTSQueryFallsBackSafely(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := memory.NewService(store.DB(), t.TempDir())
	if _, err := svc.Remember(ctx, "literal unmatched bracket [", "s", "manual", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, "[", 5); err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
	if _, err := svc.Remember(ctx, `quoted "cache" policy`, "s", "manual", 0); err != nil {
		t.Fatal(err)
	}
	got, err := svc.List(ctx, `unrelated "cache"`, 5)
	if err != nil || len(got) != 1 || !strings.Contains(got[0].Content, "cache") {
		t.Fatalf("quoted multi-term search = %#v, %v", got, err)
	}
}

func TestWorkspaceAnchorResolvesSymlinks(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	root := t.TempDir()
	oneRoot, twoRoot := filepath.Join(root, "one"), filepath.Join(root, "two")
	if err := os.Mkdir(oneRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(twoRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "current")
	if err := os.Symlink(oneRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	one := memory.NewService(store.DB(), link)
	if _, err := one.Remember(ctx, "only repository one", "s", "manual", 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(twoRoot, link); err != nil {
		t.Fatal(err)
	}
	two := memory.NewService(store.DB(), link)
	items, err := two.List(ctx, "", 20)
	if err != nil || len(items) != 0 {
		t.Fatalf("retargeted symlink leaked memories: %#v, %v", items, err)
	}
}
