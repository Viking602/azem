package verification

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Viking602/azem/internal/session"
)

var ErrNoVerificationResult = errors.New("verification: no result")

type ResultStore interface {
	Latest(context.Context, string) (session.VerificationResultV1, error)
	Save(context.Context, session.VerificationResultV1) error
}

type ClaimDecisionV1 struct {
	Action string // allow, retry, or surface
	Status string // pass, fail, or uncertain
	Reason string
	Result *session.VerificationResultV1
}

type FinalClaimGuard struct {
	store   ResultStore
	now     func() time.Time
	retried atomic.Bool
}

func NewFinalClaimGuard(store ResultStore, now func() time.Time) (*FinalClaimGuard, error) {
	if store == nil {
		return nil, fmt.Errorf("verification: final claim guard requires result store")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &FinalClaimGuard{store: store, now: now}, nil
}

// Check allows non-mutating runs immediately. A mutating run gets one retry to
// produce current evidence. If evidence remains absent or stale, the guard
// persists an explicit uncertain result and requires the caller to surface it;
// it never manufactures a pass.
func (g *FinalClaimGuard) Check(ctx context.Context, mutating bool, work session.WorkSpecV1, plan session.VerificationPlanV1, currentSnapshot string) (ClaimDecisionV1, error) {
	if g == nil || g.store == nil || currentSnapshot == "" || len(work.Criteria) == 0 || plan.WorkSpecID != work.ID {
		return ClaimDecisionV1{}, fmt.Errorf("verification: invalid final claim guard input")
	}
	if !mutating {
		return ClaimDecisionV1{Action: "allow", Status: "pass"}, nil
	}
	if err := work.Validate(); err != nil {
		return ClaimDecisionV1{}, err
	}
	if err := plan.Validate(); err != nil {
		return ClaimDecisionV1{}, err
	}
	known := make(map[string]struct{}, len(work.Criteria))
	for _, criterion := range work.Criteria {
		known[criterion.ID] = struct{}{}
	}
	for _, check := range plan.Checks {
		for _, criterionID := range check.CriterionIDs {
			if _, exists := known[criterionID]; !exists {
				return ClaimDecisionV1{}, fmt.Errorf("verification: plan references unknown criterion %s", criterionID)
			}
		}
	}
	result, err := g.store.Latest(ctx, work.ID)
	if err == nil {
		if compatibilityErr := CompatibleResult(work, plan, result, currentSnapshot); compatibilityErr == nil {
			decision := ClaimDecisionV1{Action: "allow", Status: result.Status, Result: &result}
			if result.Status != "pass" {
				decision.Action = "surface"
				decision.Reason = "verification result is " + result.Status
			}
			return decision, nil
		}
	} else if !errors.Is(err, ErrNoVerificationResult) {
		return ClaimDecisionV1{}, err
	}
	if g.retried.CompareAndSwap(false, true) {
		return ClaimDecisionV1{
			Action: "retry", Status: "uncertain",
			Reason: "Run the selected verification checks for the current workspace snapshot, persist their criterion-linked result, then answer again.",
		}, nil
	}
	uncertain := uncertainResult(work, plan, currentSnapshot, g.now())
	if err := g.store.Save(ctx, uncertain); err != nil {
		return ClaimDecisionV1{}, fmt.Errorf("verification: persist uncertain result: %w", err)
	}
	return ClaimDecisionV1{
		Action: "surface", Status: "uncertain",
		Reason: "Verification evidence is missing or stale for the current workspace snapshot.", Result: &uncertain,
	}, nil
}

func CompatibleResult(work session.WorkSpecV1, plan session.VerificationPlanV1, result session.VerificationResultV1, currentSnapshot string) error {
	if err := work.Validate(); err != nil {
		return err
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.PlanID != plan.ID || result.WorkSpecID != work.ID || result.RevisionID != plan.RevisionID || result.SnapshotHash != plan.SnapshotHash || result.SnapshotHash != currentSnapshot {
		return fmt.Errorf("verification: result is stale or belongs to another plan")
	}
	if result.CompletedAt.Before(plan.CreatedAt) {
		return fmt.Errorf("verification: result predates plan")
	}
	known := make(map[string]struct{}, len(work.Criteria))
	for _, criterion := range work.Criteria {
		known[criterion.ID] = struct{}{}
	}
	statuses := make(map[string]string, len(result.Criteria))
	for _, criterion := range result.Criteria {
		if _, known := known[criterion.CriterionID]; !known {
			return fmt.Errorf("verification: result references unknown criterion %s", criterion.CriterionID)
		}
		if _, duplicate := statuses[criterion.CriterionID]; duplicate {
			return fmt.Errorf("verification: duplicate result for criterion %s", criterion.CriterionID)
		}
		statuses[criterion.CriterionID] = criterion.Status
	}
	aggregate := "pass"
	for _, criterion := range work.Criteria {
		status, exists := statuses[criterion.ID]
		if criterion.Required && !exists {
			return fmt.Errorf("verification: required criterion %s has no result", criterion.ID)
		}
		if !exists {
			continue
		}
		if status == "fail" {
			aggregate = "fail"
		} else if status == "uncertain" && aggregate == "pass" {
			aggregate = "uncertain"
		}
	}
	if result.Status != aggregate {
		return fmt.Errorf("verification: aggregate status %s does not match criteria %s", result.Status, aggregate)
	}
	return nil
}

func uncertainResult(work session.WorkSpecV1, plan session.VerificationPlanV1, snapshot string, completedAt time.Time) session.VerificationResultV1 {
	evidence := []session.SourceRefV1{{Kind: "guardrail", ID: "missing_or_stale_verification"}}
	criteria := make([]session.CriterionResultV1, 0, len(work.Criteria))
	for _, criterion := range work.Criteria {
		criteria = append(criteria, session.CriterionResultV1{
			CriterionID: criterion.ID, Status: "uncertain", Evidence: evidence,
			Reason: "no compatible verification evidence was recorded after one retry",
		})
	}
	return session.VerificationResultV1{
		Version: session.WorkContractVersionV1,
		ID:      "verification-result:" + shortHash(work.ID+":"+plan.ID+":"+snapshot+":uncertain"),
		PlanID:  plan.ID, WorkSpecID: work.ID, RevisionID: plan.RevisionID, SnapshotHash: snapshot,
		Status: "uncertain", Criteria: criteria, CompletedAt: completedAt,
	}
}
