package session

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/go-hydaelyn/message"
)

func TestPhase4ProviderFactsAreIdempotentIsolatedAndDurable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facts.db")
	store, err := sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store.DB())
	if _, err = svc.Ensure(ctx, Session{ID: "s", Title: "facts"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	started := ProviderRequestFact{RequestID: "main-1", SessionID: "s", RunID: "run", RequestKind: "main", Status: "started", StartedAt: now}
	if err = svc.UpsertProviderRequest(ctx, started); err != nil {
		t.Fatal(err)
	}
	completed := started
	completed.Status, completed.CompletedAt = "completed", now.Add(time.Second)
	completed.InputTokens, completed.CachedTokens, completed.OutputTokens, completed.CacheReported = 10, 4, 2, true
	if err = svc.UpsertProviderRequest(ctx, completed); err != nil {
		t.Fatal(err)
	}
	first, err := svc.ProviderUsageSnapshot(ctx, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.UpsertProviderRequest(ctx, completed); err != nil {
		t.Fatal(err)
	}
	duplicate, err := svc.ProviderUsageSnapshot(ctx, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, duplicate) {
		t.Fatalf("duplicate changed snapshot: %#v != %#v", duplicate, first)
	}
	var rows int
	if err = store.DB().QueryRow(`SELECT count(*) FROM provider_requests`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows=%d err=%v", rows, err)
	}

	facts := []ProviderRequestFact{
		{RequestID: "main-2", SessionID: "s", RunID: "run", RequestKind: "main", Status: "completed", StartedAt: now, CompletedAt: now, InputTokens: 20},
		{RequestID: "compact", SessionID: "s", RunID: "run", RequestKind: "compaction", Status: "completed", StartedAt: now, CompletedAt: now, InputTokens: 30, CachedTokens: 3, CacheReported: true},
		{RequestID: "team", SessionID: "s", RunID: "run", RequestKind: "team", Status: "completed", StartedAt: now, CompletedAt: now, InputTokens: 40},
		{RequestID: "sub", SessionID: "s", RunID: "run", RequestKind: "subagent", Status: "completed", StartedAt: now, CompletedAt: now, InputTokens: 50, CachedTokens: 5, CacheReported: true},
	}
	for _, fact := range facts {
		if err = svc.UpsertProviderRequest(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := svc.ProviderUsageSnapshot(ctx, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if snap.CurrentTurnMainRequests != 2 || snap.CurrentTurnMainInput != 30 || snap.CompactionInput != 30 || snap.TeamInput != 40 || snap.SubagentInput != 50 {
		t.Fatalf("kind/retry isolation failed: %#v", snap)
	}
	if snap.TeamReportedInput != 0 || snap.TeamCacheReported || snap.CompactionReportedInput != 30 || snap.CompactionCached != 3 {
		t.Fatalf("reported denominators failed: %#v", snap)
	}
	if err = store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	store, err = sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	reopened, err := NewService(store.DB()).ProviderUsageSnapshot(ctx, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap, reopened) {
		t.Fatalf("reopened snapshot differs: %#v != %#v", reopened, snap)
	}
}

func TestPhase4EpochIsolationMutationAndManualActivationBinding(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "epoch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := NewService(store.DB())
	if _, err = svc.Ensure(ctx, Session{ID: "s", Title: "epoch"}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AppendBlock(ctx, "s", Block{Kind: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.CompleteTurn(ctx, "s", Block{Kind: "assistant", Content: "hi"}, ModelHistory{}); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.LoadProjection(ctx, "s")
	if p.CacheEpoch != 0 {
		t.Fatalf("ordinary mutations changed epoch to %d", p.CacheEpoch)
	}
	now := time.Now().UTC()
	for _, f := range []ProviderRequestFact{
		{RequestID: "old", SessionID: "s", RunID: "r", RequestKind: "main", CacheEpoch: 0, Status: "completed", StartedAt: now, CompletedAt: now, InputTokens: 100, CachedTokens: 90, CacheReported: true},
	} {
		if err = svc.UpsertProviderRequest(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	p, err = svc.CompactWithSummary(ctx, "s", CompactionPlan{Summary: "summary", ModelHistory: ModelHistory{StaticPrefixHash: "partial"}, ExpectedUpdatedAt: p.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	if p.CacheEpoch != 1 || p.CacheIdentityHash != "" {
		t.Fatalf("activation=%#v", p)
	}
	epoch, _, err := svc.EnsureCacheIdentity(ctx, "s", "full-hash")
	if err != nil || epoch != 1 {
		t.Fatalf("first bind epoch=%d err=%v", epoch, err)
	}
	epoch, _, _ = svc.EnsureCacheIdentity(ctx, "s", "full-hash")
	if epoch != 1 {
		t.Fatalf("same identity epoch=%d", epoch)
	}
	epoch, _, _ = svc.EnsureCacheIdentity(ctx, "s", "different")
	if epoch != 2 {
		t.Fatalf("different identity epoch=%d", epoch)
	}
	zero := ProviderRequestFact{RequestID: "zero", SessionID: "s", RunID: "r", RequestKind: "main", CacheEpoch: 2, Status: "completed", StartedAt: now, CompletedAt: now, InputTokens: 25, CachedTokens: 0, CacheReported: true}
	if err = svc.UpsertProviderRequest(ctx, zero); err != nil {
		t.Fatal(err)
	}
	snap, err := svc.ProviderUsageSnapshot(ctx, "s", "r")
	if err != nil {
		t.Fatal(err)
	}
	if snap.CurrentEpochMainInput != 25 || snap.CurrentEpochMainReportedInput != 25 || snap.CurrentEpochMainCached != 0 || !snap.CurrentEpochMainReported {
		t.Fatalf("epoch exclusion/explicit zero failed: %#v", snap)
	}
}

func TestPhase5AdvanceCacheEpochCAS(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "epoch-cas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := NewService(store.DB())
	if _, err = svc.Ensure(ctx, Session{ID: "s", Title: "cas"}); err != nil {
		t.Fatal(err)
	}
	const workers = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	changed := 0
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, didChange, advanceErr := svc.AdvanceCacheEpoch(ctx, "s", 0, "compacted")
			if advanceErr != nil {
				t.Errorf("AdvanceCacheEpoch: %v", advanceErr)
				return
			}
			if didChange {
				mu.Lock()
				changed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	projection, err := svc.LoadProjection(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 || projection.CacheEpoch != 1 {
		t.Fatalf("changed=%d epoch=%d", changed, projection.CacheEpoch)
	}
}

func TestAutomaticCompactionCrashSeparatesRestoredPrefixEpoch(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "epoch-crash.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := NewService(store.DB())
	if _, err := svc.Ensure(ctx, Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	if epoch, _, err := svc.EnsureCacheIdentity(ctx, "s", "old-prefix"); err != nil || epoch != 0 {
		t.Fatalf("initial epoch=%d err=%v", epoch, err)
	}
	if epoch, changed, err := svc.AdvanceCacheEpoch(ctx, "s", 0, "compacted-prefix"); err != nil || !changed || epoch != 1 {
		t.Fatalf("compacted epoch=%d changed=%v err=%v", epoch, changed, err)
	}
	if epoch, _, err := svc.EnsureCacheIdentity(ctx, "s", "old-prefix"); err != nil || epoch != 2 {
		t.Fatalf("restored old prefix epoch=%d err=%v", epoch, err)
	}
}

func TestPhase5CompleteTurnPersistsAutomaticSummaryHash(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "summary-hash.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	svc := NewService(store.DB())
	if _, err = svc.Ensure(ctx, Session{ID: "s", Title: "summary"}); err != nil {
		t.Fatal(err)
	}
	summary := message.NewText(message.RoleAssistant, "automatic checkpoint")
	summary.Kind = message.KindCompactionSummary
	history := ModelHistory{InstructionFingerprint: "instructions", Messages: []message.Message{summary}}
	if err = svc.CompleteTurn(ctx, "s", Block{Kind: "assistant", Content: "done"}, history); err != nil {
		t.Fatal(err)
	}
	projection, err := svc.LoadProjection(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if projection.ModelHistory.SummaryHash == "" || projection.ModelHistory.WireVersion != CurrentWireVersion {
		t.Fatalf("model history=%#v", projection.ModelHistory)
	}
}
