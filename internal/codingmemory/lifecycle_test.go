package codingmemory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/evidence"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestRetrievedEvidenceMemoryRetainsProvenanceAndForgetStopsRecall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pipeline.go"), []byte("package pipeline\n\nfunc RetryBoundary() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates, err := evidence.IndexWorkspace(root, []evidence.RepositorySignalV1{{Path: "pipeline.go", Diagnostics: []string{"RetryBoundary failed"}, Touched: true}})
	if err != nil {
		t.Fatal(err)
	}
	ranked, err := (evidence.Retriever{}).Retrieve(ctx, evidence.RetrieveInput{Query: "RetryBoundary failed", Limit: 2, ByteBudget: 1024, Candidates: candidates})
	if err != nil || len(ranked) != 1 {
		t.Fatalf("ranked = %+v err=%v", ranked, err)
	}
	selected := []session.SourceRefV1{ranked[0].Candidate.Ref}
	ledger, err := evidence.NewLedgerEntry("session", "run", "RetryBoundary failed", "locate failure", ranked, selected, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	ledger, err = evidence.CompleteLedgerEntry(ledger, "inspect failure source", "pass", []session.SourceRefV1{{Kind: "validator", ID: "go-test"}}, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	databasePath := filepath.Join(t.TempDir(), "lifecycle.db")
	provider, err := sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "memory"}); err != nil {
		t.Fatal(err)
	}
	ledgerStore, _ := evidence.NewLedgerStore(sessions, "session", "run")
	if err := ledgerStore.Save(ctx, ledger); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(sessions, "session", "run")
	memory := MemoryV1{
		Version: 1, ID: "retry-strategy", Kind: KindStrategy, Scope: ScopeV1{Kind: ScopeRepository, ID: "repo"}, Content: "Inspect RetryBoundary before changing retry policy.",
		Confidence: 1, Status: StatusActive, Origin: OriginDerived,
		Sources:   []AttributionV1{{Ref: ranked[0].Candidate.Ref, Authority: "workspace"}, {Ref: session.SourceRefV1{Kind: "evidence_ledger", ID: ledger.ID}, Authority: "history"}},
		CreatedAt: time.Unix(3, 0).UTC(), UpdatedAt: time.Unix(3, 0).UTC(),
	}
	policy := DefaultPolicy("user")
	policy.LocalLearningEnabled = true
	if _, err := store.Add(ctx, memory, policy, memory.Scope, false, time.Unix(3, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	ref := SalienceRefV1{Version: 1, Kind: "coding_memory", ID: memory.ID, Scope: memory.Scope, Salience: 1, Reason: "same failure boundary", Sources: []session.SourceRefV1{{Kind: "evidence_ledger", ID: ledger.ID}}}
	policy = DefaultPolicy("user")
	if recalled, err := store.Recall(ctx, []SalienceRefV1{ref}, policy, time.Unix(4, 0).UTC()); err != nil || len(recalled) != 0 {
		t.Fatalf("opt-out recall = %+v err=%v", recalled, err)
	}
	policy.ExperiencePromotionEnabled = true
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions = session.NewService(provider.DB(), provider.Blobs())
	store, _ = NewStore(sessions, "session", "run")
	recalled, err := store.Recall(ctx, []SalienceRefV1{ref}, policy, time.Unix(4, 0).UTC())
	if err != nil || len(recalled) != 1 || recalled[0].Sources[0].Ref.SHA256 != ranked[0].Candidate.Ref.SHA256 || recalled[0].Sources[1].Ref.ID != ledger.ID {
		t.Fatalf("recalled = %+v err=%v", recalled, err)
	}
	if _, err := store.Forget(ctx, memory.ID, "user deleted memory", true, time.Unix(5, 0).UTC()); err != nil {
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
	store, _ = NewStore(session.NewService(provider.DB(), provider.Blobs()), "session", "run")
	recalled, err = store.Recall(ctx, []SalienceRefV1{ref}, policy, time.Unix(6, 0).UTC())
	if err != nil || len(recalled) != 0 {
		t.Fatalf("forgotten memory resolved from stale salience ref: %+v err=%v", recalled, err)
	}
	catalog, err := store.Load(ctx)
	if err != nil || memoryIndex(catalog.Memories, memory.ID) >= 0 || len(catalog.Deletions) != 1 {
		t.Fatalf("forgotten catalog = %+v err=%v", catalog, err)
	}
}

func TestRetentionPrunesDerivedMemoryButPreservesGuidance(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "memory"}); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(sessions, "session", "run")
	old := time.Unix(1, 0).UTC()
	derived := testMemory("derived", KindStrategy, ScopeV1{Kind: ScopeUser, ID: "user"}, OriginDerived, AttributionV1{Ref: session.SourceRefV1{Kind: "validator", ID: "result"}, Authority: "validator"}, old)
	guidance := testMemory("guidance", KindStrategy, ScopeV1{Kind: ScopeUser, ID: "user"}, OriginGuidance, AttributionV1{Ref: session.SourceRefV1{Kind: "user_turn", ID: "turn"}, Authority: "user"}, old)
	policy := DefaultPolicy("user")
	policy.LocalLearningEnabled = true
	if _, err := store.Add(ctx, derived, policy, derived.Scope, false, old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(ctx, guidance, policy, guidance.Scope, true, old); err != nil {
		t.Fatal(err)
	}
	policy.RetentionDays = 1
	catalog, err := store.PruneExpired(ctx, policy, old.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if memoryIndex(catalog.Memories, derived.ID) >= 0 || memoryIndex(catalog.Memories, guidance.ID) < 0 {
		t.Fatalf("retention catalog = %+v", catalog)
	}
}
