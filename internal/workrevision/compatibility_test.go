package workrevision

import (
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestDispositionAcceptsExactAndCompatibleRevisions(t *testing.T) {
	t.Parallel()
	source := mustRevision(t, "keep API", "a.go", "a", 1)
	exact, err := Disposition(CompatibilityInput{IntentID: "intent", Source: source, Current: source, CompletedAt: time.Unix(3, 0).UTC()})
	if err != nil || !exact.Accepted() || !exact.CanSatisfyDependency() || !exact.CanSatisfyVerification() {
		t.Fatalf("exact disposition = %+v, %v", exact, err)
	}
	current, err := Derive(DeriveInput{
		SessionID: "session", CanonicalUserSequence: 2,
		ExplicitConstraints: []session.CriterionV1{{ID: "constraint", Text: "keep API", Origin: "explicit"}},
		GitHEAD:             "head", Files: []session.WorkRevisionFileV1{
			{Path: "a.go", SHA256: strings.Repeat("a", 64), Observed: true},
			{Path: "unrelated.go", SHA256: strings.Repeat("b", 64), Observed: true},
		}, CreatedAt: time.Unix(2, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	compatible, err := Disposition(CompatibilityInput{IntentID: "intent", Source: source, Current: current, CompletedAt: time.Unix(3, 0).UTC()})
	if err != nil || !compatible.Accepted() {
		t.Fatalf("compatible disposition = %+v, %v", compatible, err)
	}
}

func TestDispositionQuarantinesChangedConstraintOrObservedFile(t *testing.T) {
	t.Parallel()
	source := mustRevision(t, "keep API", "a.go", "a", 1)
	changedConstraint := mustRevision(t, "change API", "a.go", "a", 2)
	stale, err := Disposition(CompatibilityInput{IntentID: "intent", Source: source, Current: changedConstraint, CompletedAt: time.Unix(3, 0).UTC()})
	if err != nil || stale.Status != DispositionQuarantinedStale || stale.CanSatisfyDependency() || stale.CanSatisfyVerification() {
		t.Fatalf("constraint disposition = %+v, %v", stale, err)
	}
	changedFile := mustRevision(t, "keep API", "a.go", "b", 2)
	stale, err = Disposition(CompatibilityInput{IntentID: "intent", Source: source, Current: changedFile, CompletedAt: time.Unix(3, 0).UTC()})
	if err != nil || stale.Status != DispositionQuarantinedStale {
		t.Fatalf("file disposition = %+v, %v", stale, err)
	}
}

func TestDispositionAcceptsFileChangeExplainedByTerminalEvidence(t *testing.T) {
	t.Parallel()
	source := mustRevision(t, "keep API", "a.go", "a", 1)
	current := mustRevision(t, "keep API", "a.go", "b", 2)
	disposition, err := Disposition(CompatibilityInput{
		IntentID: "intent", Source: source, Current: current, CompletedAt: time.Unix(3, 0).UTC(),
		ResultFiles: []session.FileObservationV1{{Path: "a.go", BeforeSHA256: strings.Repeat("a", 64), AfterSHA256: strings.Repeat("b", 64), State: "modified"}},
	})
	if err != nil || !disposition.Accepted() {
		t.Fatalf("explained disposition = %+v, %v", disposition, err)
	}
}

func mustRevision(t *testing.T, constraint, path, hashByte string, sequence int64) session.WorkRevisionV1 {
	t.Helper()
	revision, err := Derive(DeriveInput{
		SessionID: "session", CanonicalUserSequence: sequence,
		ExplicitConstraints: []session.CriterionV1{{ID: "constraint", Text: constraint, Origin: "explicit"}},
		GitHEAD:             "head", Files: []session.WorkRevisionFileV1{{Path: path, SHA256: strings.Repeat(hashByte, 64), Observed: true}}, CreatedAt: time.Unix(sequence, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
