package workrevision

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestStorePersistsRevisionBoundIntentObservationAndVerification(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "revision.db")
	provider, err := sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "test"}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(sessions, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := Derive(DeriveInput{
		SessionID: "session", CanonicalUserSequence: 4, TodoRevision: 2, SemanticRevision: 1, GitHEAD: "head",
		Files: []session.WorkRevisionFileV1{{Path: "a.go", SHA256: strings.Repeat("a", 64), Touched: true}}, CreatedAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := BindIntent(revision, session.ActionIntentV1{
		Version: 1, ID: "intent", WorkSpecID: "work", SessionID: "session", RunID: "run", Kind: "subagent", Name: "review", CreatedAt: time.Unix(2, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := BindObservation(revision, intent, session.ObservationEnvelopeV1{
		Version: 1, ID: "observation", IntentID: intent.ID, SessionID: "session", RunID: "run", Status: "completed",
		ContentPresence: "present", Truncation: "none", SchemaValidity: "valid", Retryability: "terminal",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BindVerificationPlan(revision, session.VerificationPlanV1{
		Version: 1, ID: "plan", WorkSpecID: "work", Checks: []session.VerificationCheckV1{{ID: "check", CriterionIDs: []string{"criterion"}, Kind: "command"}}, CreatedAt: time.Unix(2, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := BindVerificationResult(revision, plan, session.VerificationResultV1{
		Version: 1, ID: "result", PlanID: "plan", WorkSpecID: "work", Status: "pass",
		Criteria: []session.CriterionResultV1{{CriterionID: "criterion", Status: "pass", Evidence: []session.SourceRefV1{{Kind: "tool_record", ID: "call"}}}}, CompletedAt: time.Unix(3, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	disposition, err := Disposition(CompatibilityInput{IntentID: intent.ID, Source: revision, Current: revision, CompletedAt: time.Unix(3, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	guidance, err := HandleGuidance(ctx, revision, []ActiveIntentV1{{Intent: intent, SourceRevision: revision}}, nil, time.Unix(3, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	saves := []struct {
		name string
		save func() error
	}{
		{"revision", func() error { return store.SaveRevision(ctx, revision) }},
		{"intent", func() error { return store.SaveIntent(ctx, intent) }},
		{"observation", func() error { return store.SaveObservation(ctx, observation) }},
		{"disposition", func() error { return store.SaveDisposition(ctx, disposition) }},
		{"guidance", func() error { return store.SaveGuidanceDecision(ctx, guidance[0]) }},
		{"plan", func() error { return store.SaveVerificationPlan(ctx, plan) }},
		{"result", func() error { return store.SaveVerificationResult(ctx, result) }},
	}
	for _, item := range saves {
		if err := item.save(); err != nil {
			t.Fatalf("save %s: %v", item.name, err)
		}
	}
	var count int
	if err := provider.DB().QueryRowContext(ctx, `SELECT count(*) FROM context_artifacts WHERE session_id='session'`).Scan(&count); err != nil || count != 7 {
		t.Fatalf("artifact count=%d err=%v", count, err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	store, _ = NewStore(session.NewService(provider.DB(), provider.Blobs()), "session", "resumed")
	loaded, err := store.LatestRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != revision.ID || loaded.SnapshotHash != revision.SnapshotHash {
		t.Fatalf("loaded revision = %+v", loaded)
	}
}

func TestBindObservationRejectsAnotherRevisionIntent(t *testing.T) {
	t.Parallel()
	revision, err := Derive(DeriveInput{SessionID: "session", CreatedAt: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	intent := session.ActionIntentV1{ID: "intent", SessionID: "session", RunID: "run", RevisionID: "other", SnapshotHash: revision.SnapshotHash}
	observation := session.ObservationEnvelopeV1{IntentID: "intent", SessionID: "session", RunID: "run"}
	if _, err := BindObservation(revision, intent, observation); err == nil {
		t.Fatal("accepted observation from another revision")
	}
}
