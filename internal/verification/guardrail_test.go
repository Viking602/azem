package verification

import (
	"context"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

type memoryResultStore struct {
	result *session.VerificationResultV1
	saves  int
}

func (s *memoryResultStore) Latest(context.Context, string) (session.VerificationResultV1, error) {
	if s.result == nil {
		return session.VerificationResultV1{}, ErrNoVerificationResult
	}
	return *s.result, nil
}

func (s *memoryResultStore) Save(_ context.Context, result session.VerificationResultV1) error {
	s.saves++
	s.result = &result
	return nil
}

func TestFinalClaimGuardRetriesOnceThenPersistsUncertainty(t *testing.T) {
	t.Parallel()
	work, plan := guardrailWork(t)
	store := &memoryResultStore{}
	guard, err := NewFinalClaimGuard(store, func() time.Time { return time.Unix(3, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	first, err := guard.Check(t.Context(), true, work, plan, plan.SnapshotHash)
	if err != nil {
		t.Fatal(err)
	}
	if first.Action != "retry" || first.Status != "uncertain" || store.saves != 0 {
		t.Fatalf("first decision = %+v saves=%d", first, store.saves)
	}
	second, err := guard.Check(t.Context(), true, work, plan, plan.SnapshotHash)
	if err != nil {
		t.Fatal(err)
	}
	if second.Action != "surface" || second.Status != "uncertain" || second.Result == nil || store.saves != 1 {
		t.Fatalf("second decision = %+v saves=%d", second, store.saves)
	}
	if err := CompatibleResult(work, plan, *second.Result, plan.SnapshotHash); err != nil {
		t.Fatalf("persisted uncertainty is not compatible: %v", err)
	}
	for _, result := range second.Result.Criteria {
		if result.Status != "uncertain" || len(result.Evidence) == 0 {
			t.Fatalf("criterion uncertainty = %+v", result)
		}
	}
}

func TestFinalClaimGuardAllowsOnlyCompatiblePassAndSurfacesFailure(t *testing.T) {
	t.Parallel()
	work, plan := guardrailWork(t)
	pass := verificationResultFor(work, plan, "pass")
	store := &memoryResultStore{result: &pass}
	guard, err := NewFinalClaimGuard(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := guard.Check(t.Context(), true, work, plan, plan.SnapshotHash)
	if err != nil || decision.Action != "allow" || decision.Status != "pass" {
		t.Fatalf("pass decision = %+v, %v", decision, err)
	}
	failed := verificationResultFor(work, plan, "fail")
	store.result = &failed
	guard, _ = NewFinalClaimGuard(store, nil)
	decision, err = guard.Check(t.Context(), true, work, plan, plan.SnapshotHash)
	if err != nil || decision.Action != "surface" || decision.Status != "fail" {
		t.Fatalf("fail decision = %+v, %v", decision, err)
	}
}

func TestFinalClaimGuardIgnoresNonMutatingRunsAndRejectsStaleEvidence(t *testing.T) {
	t.Parallel()
	work, plan := guardrailWork(t)
	stale := verificationResultFor(work, plan, "pass")
	stale.SnapshotHash = "old"
	store := &memoryResultStore{result: &stale}
	guard, _ := NewFinalClaimGuard(store, nil)
	decision, err := guard.Check(t.Context(), false, work, plan, "current")
	if err != nil || decision.Action != "allow" {
		t.Fatalf("non-mutating decision = %+v, %v", decision, err)
	}
	decision, err = guard.Check(t.Context(), true, work, plan, "current")
	if err != nil || decision.Action != "retry" {
		t.Fatalf("stale decision = %+v, %v", decision, err)
	}
}

func TestCompatibleResultRejectsAggregateMismatch(t *testing.T) {
	t.Parallel()
	work, plan := guardrailWork(t)
	result := verificationResultFor(work, plan, "pass")
	result.Criteria[0].Status = "fail"
	if err := CompatibleResult(work, plan, result, plan.SnapshotHash); err == nil {
		t.Fatal("accepted aggregate pass with failed criterion")
	}
}

func guardrailWork(t *testing.T) (session.WorkSpecV1, session.VerificationPlanV1) {
	t.Helper()
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship", RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		UserCriteria: []CriterionInput{{ID: "required", Text: "works", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return work, plan
}

func verificationResultFor(work session.WorkSpecV1, plan session.VerificationPlanV1, status string) session.VerificationResultV1 {
	criterionStatus := status
	if status == "fail" {
		criterionStatus = "fail"
	}
	return session.VerificationResultV1{
		Version: 1, ID: "result", PlanID: plan.ID, WorkSpecID: work.ID, RevisionID: plan.RevisionID, SnapshotHash: plan.SnapshotHash,
		Status: status, Criteria: []session.CriterionResultV1{{CriterionID: "required", Status: criterionStatus, Evidence: []session.SourceRefV1{{Kind: "validator", ID: "test"}}}},
		CompletedAt: time.Unix(2, 0).UTC(),
	}
}
