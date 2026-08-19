package workrevision

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestRevisionGateRejectsDelayedChangedRequirementAndFileResults(t *testing.T) {
	t.Parallel()
	source := mustRevision(t, "original requirement", "a.go", "a", 1)
	scenarios := []struct {
		name    string
		current session.WorkRevisionV1
	}{
		{name: "delayed-result-after-changed-requirement", current: mustRevision(t, "replacement requirement", "a.go", "a", 2)},
		{name: "changed-observed-file", current: mustRevision(t, "original requirement", "a.go", "b", 2)},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			disposition, err := Disposition(CompatibilityInput{IntentID: "old-intent", Source: source, Current: scenario.current, CompletedAt: time.Unix(3, 0).UTC()})
			if err != nil {
				t.Fatal(err)
			}
			if disposition.Status != DispositionQuarantinedStale || disposition.CanSatisfyDependency() || disposition.CanSatisfyVerification() || ProjectionStatus(&disposition, nil) != EvidenceStale {
				t.Fatalf("stale work became acceptable: %+v", disposition)
			}
		})
	}
}

func TestRevisionGateAcceptsRetryOnlyAgainstCurrentRevision(t *testing.T) {
	t.Parallel()
	source := mustRevision(t, "old requirement", "a.go", "a", 1)
	current := mustRevision(t, "new requirement", "a.go", "a", 2)
	oldIntent := boundGuidanceIntent(t, source, "old-intent")
	cancelled := false
	decisions, err := HandleGuidance(t.Context(), current, []ActiveIntentV1{{Intent: oldIntent, SourceRevision: source, CancelSafe: true}}, func(context.Context, string) error {
		cancelled = true
		return nil
	}, time.Unix(3, 0).UTC())
	if err != nil || !cancelled || decisions[0].Action != GuidanceCancel {
		t.Fatalf("cancel decision = %+v cancelled=%v err=%v", decisions, cancelled, err)
	}
	oldResult, err := Disposition(CompatibilityInput{IntentID: oldIntent.ID, Source: source, Current: current, CompletedAt: time.Unix(4, 0).UTC()})
	if err != nil || oldResult.Accepted() {
		t.Fatalf("old result = %+v err=%v", oldResult, err)
	}
	retry := boundGuidanceIntent(t, current, "retry-intent")
	retryResult, err := Disposition(CompatibilityInput{IntentID: retry.ID, Source: current, Current: current, CompletedAt: time.Unix(5, 0).UTC()})
	if err != nil || !retryResult.Accepted() {
		t.Fatalf("retry result = %+v err=%v", retryResult, err)
	}
}

func TestRevisionGateSurvivesProcessRestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "restart.db")
	provider, err := sqlitestore.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "test"}); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(sessions, "session", "run")
	source := mustRevision(t, "before restart", "a.go", "a", 1)
	current := mustRevision(t, "after restart", "a.go", "a", 2)
	if err := store.SaveRevision(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRevision(ctx, current); err != nil {
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
	store, _ = NewStore(session.NewService(provider.DB(), provider.Blobs()), "session", "resumed")
	reloadedSource, err := store.Revision(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	reloadedCurrent, err := store.LatestRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	disposition, err := Disposition(CompatibilityInput{IntentID: "delayed", Source: reloadedSource, Current: reloadedCurrent, CompletedAt: time.Unix(3, 0).UTC()})
	if err != nil || disposition.Accepted() || disposition.Status != DispositionQuarantinedStale {
		t.Fatalf("restart disposition = %+v err=%v", disposition, err)
	}
}
