package verification

import (
	"context"
	"fmt"
	"sort"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

const BuiltinReviewProfile = "review"

type SemanticReviewRequestV1 struct {
	Version      int                   `json:"version"`
	WorkSpecID   string                `json:"work_spec_id"`
	RevisionID   string                `json:"revision_id"`
	SnapshotHash string                `json:"snapshot_hash"`
	Criteria     []session.CriterionV1 `json:"criteria"`
	Evidence     []session.SourceRefV1 `json:"evidence"`
}

type SemanticReviewResponseV1 struct {
	Version int                         `json:"version"`
	Results []session.CriterionResultV1 `json:"results"`
}

// ReadOnlyReviewInvoker is an adapter over the existing Subagent/Team runtime.
// It exposes no edit, approval, admission, or retry operation.
type ReadOnlyReviewInvoker interface {
	InvokeReview(context.Context, string, SemanticReviewRequestV1) (SemanticReviewResponseV1, error)
}

type ProfiledSemanticReviewer struct {
	profileName string
	profile     config.SubagentRoleConfig
	invoker     ReadOnlyReviewInvoker
}

func NewProfiledSemanticReviewer(profileName string, profile config.SubagentRoleConfig, invoker ReadOnlyReviewInvoker) (*ProfiledSemanticReviewer, error) {
	if profileName == "" || invoker == nil {
		return nil, fmt.Errorf("verification: semantic reviewer requires profile and invoker")
	}
	if profile.CapabilityMode != "read-only" {
		return nil, fmt.Errorf("verification: reviewer profile %s must be read-only", profileName)
	}
	return &ProfiledSemanticReviewer{profileName: profileName, profile: profile, invoker: invoker}, nil
}

// ReviewAmbiguousCriteria delegates only unresolved criterion checks. The
// response is evidence, not authorization: every requested criterion must be
// returned as pass, fail, or uncertain with at least one evidence reference.
func (r *ProfiledSemanticReviewer) ReviewAmbiguousCriteria(ctx context.Context, work session.WorkSpecV1, plan session.VerificationPlanV1, evidence []session.SourceRefV1) ([]session.CriterionResultV1, error) {
	if r == nil || r.invoker == nil || plan.WorkSpecID != work.ID {
		return nil, fmt.Errorf("verification: invalid semantic review request")
	}
	authoritative := make(map[string]struct{}, len(evidence))
	for _, ref := range evidence {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		authoritative[semanticEvidenceKey(ref)] = struct{}{}
	}
	criterionByID := make(map[string]session.CriterionV1, len(work.Criteria))
	for _, criterion := range work.Criteria {
		criterionByID[criterion.ID] = criterion
	}
	requested := make(map[string]session.CriterionV1)
	for _, check := range plan.Checks {
		if check.Kind != "criterion" {
			continue
		}
		for _, criterionID := range check.CriterionIDs {
			criterion, exists := criterionByID[criterionID]
			if !exists {
				return nil, fmt.Errorf("verification: semantic check references unknown criterion %s", criterionID)
			}
			requested[criterionID] = criterion
		}
	}
	if len(requested) == 0 {
		return []session.CriterionResultV1{}, nil
	}
	ids := make([]string, 0, len(requested))
	for id := range requested {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	criteria := make([]session.CriterionV1, 0, len(ids))
	for _, id := range ids {
		criteria = append(criteria, requested[id])
	}
	response, err := r.invoker.InvokeReview(ctx, r.profileName, SemanticReviewRequestV1{
		Version: session.WorkContractVersionV1, WorkSpecID: work.ID, RevisionID: plan.RevisionID,
		SnapshotHash: plan.SnapshotHash, Criteria: criteria, Evidence: append([]session.SourceRefV1(nil), evidence...),
	})
	if err != nil {
		return nil, err
	}
	if response.Version != session.WorkContractVersionV1 {
		return nil, fmt.Errorf("verification: unsupported semantic review response")
	}
	byID := make(map[string]session.CriterionResultV1, len(response.Results))
	for _, result := range response.Results {
		for _, ref := range result.Evidence {
			if err := ref.Validate(); err != nil {
				return nil, err
			}
			if _, exists := authoritative[semanticEvidenceKey(ref)]; !exists {
				return nil, fmt.Errorf("verification: semantic result for %s cites unauthoritative evidence", result.CriterionID)
			}
		}
		if _, duplicate := byID[result.CriterionID]; duplicate {
			return nil, fmt.Errorf("verification: duplicate semantic result for %s", result.CriterionID)
		}
		byID[result.CriterionID] = result
	}
	results := make([]session.CriterionResultV1, 0, len(ids))
	for _, id := range ids {
		result, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf("verification: reviewer omitted criterion %s", id)
		}
		results = append(results, result)
	}
	return results, nil
}

func semanticEvidenceKey(ref session.SourceRefV1) string {
	return ref.Kind + "\x00" + ref.ID + "\x00" + ref.Range + "\x00" + ref.SHA256
}
