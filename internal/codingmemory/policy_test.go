package codingmemory

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestExperiencePromotionAndLearningAreDisabledUntilExplicitOptIn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "policy.db")
	provider, err := sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "policy"}); err != nil {
		t.Fatal(err)
	}
	store, err := NewPolicyStore(sessions, "session", "run", "user")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	candidate := session.ExperienceCandidateV1{
		Version: 1, ID: "candidate", Workspace: "/repo", Kind: KindStrategy, Content: "Run the focused validator first.",
		Scope: ScopeRepository, Confidence: "verified", Status: "candidate", Evidence: []session.SourceRefV1{{Kind: "validator_result", ID: "go-test"}}, CreatedAt: time.Unix(1, 0).UTC(),
	}
	if _, err := PromoteExperience(candidate, ScopeV1{Kind: ScopeRepository, ID: "repo"}, policy, time.Unix(2, 0).UTC()); err == nil {
		t.Fatal("promoted experience under default policy")
	}
	if err := AllowsLocalLearning(policy); err == nil {
		t.Fatal("allowed local learning under default policy")
	}
	policy.ExperiencePromotionEnabled = true
	policy.LocalLearningEnabled = true
	if _, err := store.Update(ctx, policy, false, time.Unix(2, 0).UTC()); err == nil {
		t.Fatal("enabled policy without explicit user action")
	}
	policy, err = store.Update(ctx, policy, true, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := AllowsLocalLearning(policy); err != nil {
		t.Fatal(err)
	}
	memory, err := PromoteExperience(candidate, ScopeV1{Kind: ScopeRepository, ID: "repo"}, policy, time.Unix(3, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if memory.ID != "experience:candidate" || memory.Confidence != 1 || memory.Sources[0].Authority != "validator" {
		t.Fatalf("promoted memory = %+v", memory)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	store, _ = NewPolicyStore(session.NewService(provider.DB(), provider.Blobs()), "session", "run", "user")
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.ExperiencePromotionEnabled || !loaded.LocalLearningEnabled || loaded.Revision != 1 {
		t.Fatalf("reopened policy = %+v", loaded)
	}
}

func TestExperiencePromotionRejectsExpiredCandidate(t *testing.T) {
	t.Parallel()
	expires := time.Unix(2, 0).UTC()
	candidate := session.ExperienceCandidateV1{Version: 1, ID: "candidate", Workspace: "/repo", Kind: KindStrategy, Content: "content", Scope: ScopeRepository, Confidence: "high", Status: "candidate", Evidence: []session.SourceRefV1{{Kind: "workspace_file", ID: "a.go"}}, ExpiresAt: &expires, CreatedAt: time.Unix(1, 0).UTC()}
	policy := DefaultPolicy("user")
	policy.ExperiencePromotionEnabled = true
	if _, err := PromoteExperience(candidate, ScopeV1{Kind: ScopeRepository, ID: "repo"}, policy, expires); err == nil {
		t.Fatal("promoted expired candidate")
	}
}
