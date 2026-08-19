package codingmemory

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestCodingMemoryScopesProvenanceSupersessionAndForgetting(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "memory.db")
	provider, err := sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "memory"}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(sessions, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1, 0).UTC()
	policy := DefaultPolicy("user")
	policy.LocalLearningEnabled = true
	strategy := testMemory("strategy-1", KindStrategy, ScopeV1{Kind: ScopeRepository, ID: "azem"}, OriginDerived, AttributionV1{Ref: session.SourceRefV1{Kind: "workspace_file", ID: "internal/a.go", SHA256: "abc"}, Authority: "workspace"}, now)
	if _, err := store.Add(ctx, strategy, policy, strategy.Scope, false, now); err != nil {
		t.Fatal(err)
	}
	fabricated := testMemory("fabricated", KindStrategy, strategy.Scope, OriginDerived, AttributionV1{Ref: session.SourceRefV1{Kind: "evidence_ledger", ID: "missing-ledger"}, Authority: "history"}, now)
	if _, err := store.Add(ctx, fabricated, policy, fabricated.Scope, false, now); err == nil {
		t.Fatal("stored memory with fabricated ledger provenance")
	}
	if _, err := store.Add(ctx, testMemory("wrong-scope", KindStrategy, strategy.Scope, OriginDerived, strategy.Sources[0], now), policy, ScopeV1{Kind: ScopeProject, ID: "repo"}, false, now); err == nil {
		t.Fatal("stored memory outside explicitly authorized scope")
	}
	guidance := testMemory("guidance-1", KindToolLesson, ScopeV1{Kind: ScopeProject, ID: "/repo"}, OriginGuidance, AttributionV1{Ref: session.SourceRefV1{Kind: "user_turn", ID: "42"}, Authority: "user"}, now)
	if _, err := store.Add(ctx, guidance, policy, guidance.Scope, false, now); err == nil {
		t.Fatal("stored guidance without explicit user action")
	}
	if _, err := store.Add(ctx, guidance, policy, guidance.Scope, true, now); err != nil {
		t.Fatal(err)
	}
	asset := testMemory("asset-1", KindAssetRef, ScopeV1{Kind: ScopeUser, ID: "user"}, OriginDerived, AttributionV1{Ref: session.SourceRefV1{Kind: "asset", ID: "adapter:v1"}, Authority: "validator"}, now)
	if _, err := store.Add(ctx, asset, policy, asset.Scope, false, now); err != nil {
		t.Fatal(err)
	}

	active, err := store.Active(ctx, []ScopeV1{{Kind: ScopeRepository, ID: "azem"}, {Kind: ScopeProject, ID: "/repo"}, {Kind: ScopeUser, ID: "user"}}, now)
	if err != nil || len(active) != 3 {
		t.Fatalf("active = %+v err=%v", active, err)
	}
	replacement := testMemory("strategy-2", KindStrategy, strategy.Scope, OriginDerived, strategy.Sources[0], time.Unix(2, 0).UTC())
	replacement.Content = "Prefer the verified retry boundary."
	replacement.Supersedes = []string{strategy.ID}
	if _, err := store.Supersede(ctx, strategy.ID, replacement, false, time.Unix(2, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	guidanceReplacement := testMemory("guidance-2", KindToolLesson, guidance.Scope, OriginDerived, guidance.Sources[0], time.Unix(2, 0).UTC())
	guidanceReplacement.Supersedes = []string{guidance.ID}
	if _, err := store.Supersede(ctx, guidance.ID, guidanceReplacement, false, time.Unix(2, 0).UTC()); err == nil {
		t.Fatal("silently superseded direct user guidance")
	}
	if _, err := store.Forget(ctx, asset.ID, "retention expired", false, time.Unix(3, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Forget(ctx, guidance.ID, "automatic cleanup", false, time.Unix(3, 0).UTC()); err == nil {
		t.Fatal("silently forgot direct user guidance")
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
	catalog, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Revision != 5 || len(catalog.Memories) != 3 || len(catalog.Deletions) != 1 || catalog.Deletions[0].ID != asset.ID || memoryIndex(catalog.Memories, asset.ID) >= 0 {
		t.Fatalf("reopened catalog = %+v", catalog)
	}
	if catalog.Memories[memoryIndex(catalog.Memories, strategy.ID)].Status != StatusSuperseded || catalog.Memories[memoryIndex(catalog.Memories, replacement.ID)].Status != StatusActive {
		t.Fatalf("supersession lost: %+v", catalog.Memories)
	}
}

func TestCodingMemoryRejectsGuidanceWithoutUserProvenance(t *testing.T) {
	t.Parallel()
	now := time.Unix(1, 0).UTC()
	memory := testMemory("bad", KindStrategy, ScopeV1{Kind: ScopeUser, ID: "user"}, OriginGuidance, AttributionV1{Ref: session.SourceRefV1{Kind: "workspace_file", ID: "a"}, Authority: "workspace"}, now)
	if err := memory.Validate(); err == nil {
		t.Fatal("accepted direct guidance without user provenance")
	}
}

func TestCodingMemoryAddRequiresPolicyScopeAndFiniteConfidence(t *testing.T) {
	t.Parallel()
	now := time.Unix(1, 0).UTC()
	memory := testMemory("derived", KindStrategy, ScopeV1{Kind: ScopeRepository, ID: "repo"}, OriginDerived, AttributionV1{Ref: session.SourceRefV1{Kind: "validator", ID: "test"}, Authority: "validator"}, now)
	policy := DefaultPolicy("user")
	if err := memory.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(nil, "", ""); err == nil {
		t.Fatal("constructed store without session")
	}
	memory.Confidence = math.NaN()
	if err := memory.Validate(); err == nil {
		t.Fatal("accepted NaN confidence")
	}
	memory.Confidence = 0.8
	if policy.LocalLearningEnabled {
		t.Fatal("default policy unexpectedly enabled local learning")
	}
}

func TestCodingMemoryStoresAreSerializedAcrossInstances(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "memory"}); err != nil {
		t.Fatal(err)
	}
	first, _ := NewStore(sessions, "session", "run")
	second, _ := NewStore(sessions, "session", "other-run")
	policy := DefaultPolicy("user")
	policy.LocalLearningEnabled = true
	now := time.Unix(1, 0).UTC()
	errs := make(chan error, 8)
	for index := 0; index < 8; index++ {
		store := first
		if index%2 == 1 {
			store = second
		}
		go func(index int, store *Store) {
			memory := testMemory(fmt.Sprintf("memory-%d", index), KindStrategy, ScopeV1{Kind: ScopeRepository, ID: "repo"}, OriginDerived, AttributionV1{Ref: session.SourceRefV1{Kind: "validator", ID: "test"}, Authority: "validator"}, now)
			_, addErr := store.Add(ctx, memory, policy, memory.Scope, false, now)
			errs <- addErr
		}(index, store)
	}
	for index := 0; index < 8; index++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := first.Load(ctx)
	if err != nil || len(catalog.Memories) != 8 {
		t.Fatalf("serialized catalog = %+v err=%v", catalog, err)
	}
}

func testMemory(id, kind string, scope ScopeV1, origin string, source AttributionV1, now time.Time) MemoryV1 {
	return MemoryV1{Version: 1, ID: id, Kind: kind, Scope: scope, Content: "Use the narrow verified path.", Confidence: 0.8, Status: StatusActive, Origin: origin, Sources: []AttributionV1{source}, CreatedAt: now, UpdatedAt: now}
}
