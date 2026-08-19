// Package verification compiles work criteria, selects checks, and evaluates
// evidence without granting tool execution or approval authority.
package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

type CriterionInput struct {
	ID       string
	Text     string
	Required bool
	Sources  []session.SourceRefV1
}

type CompileInput struct {
	SessionID        string
	RunID            string
	Goal             string
	GoalSource       session.SourceRefV1
	ApprovedPlanID   string
	UserCriteria     []CriterionInput
	PlanCriteria     []CriterionInput
	InferredCriteria []CriterionInput
	Todo             session.TodoList
	RevisionID       string
	SnapshotHash     string
	CreatedAt        time.Time
}

// CompileCriteria preserves explicit user/plan/Todo criteria before adding
// inferred criteria. Text-equivalent inferred criteria are discarded; they can
// never replace, weaken, or rename explicit scope.
func CompileCriteria(input CompileInput) (session.WorkSpecV1, session.VerificationPlanV1, error) {
	input.Goal = strings.TrimSpace(input.Goal)
	input.ApprovedPlanID = strings.TrimSpace(input.ApprovedPlanID)
	if input.SessionID == "" || input.RunID == "" || input.Goal == "" || input.RevisionID == "" || input.SnapshotHash == "" || input.CreatedAt.IsZero() {
		return session.WorkSpecV1{}, session.VerificationPlanV1{}, fmt.Errorf("verification: incomplete criteria compiler input")
	}
	explicit := make([]CriterionInput, 0, len(input.UserCriteria)+len(input.PlanCriteria)+len(input.Todo.Phases)+1)
	explicit = append(explicit, input.UserCriteria...)
	explicit = append(explicit, input.PlanCriteria...)
	for _, phase := range input.Todo.Phases {
		for _, item := range phase.Items {
			if item.Status == session.TodoCancelled || strings.TrimSpace(item.Content) == "" {
				continue
			}
			explicit = append(explicit, CriterionInput{
				ID: "todo:" + item.ID, Text: item.Content, Required: true,
				Sources: []session.SourceRefV1{{Kind: "todo", ID: fmt.Sprintf("%d:%s", input.Todo.Revision, item.ID)}},
			})
		}
	}
	if len(explicit) == 0 {
		explicit = append(explicit, CriterionInput{ID: "goal", Text: input.Goal, Required: true, Sources: []session.SourceRefV1{input.GoalSource}})
	}
	criteria := make([]session.CriterionV1, 0, len(explicit)+len(input.InferredCriteria))
	byText := make(map[string]int)
	appendCriterion := func(candidate CriterionInput, origin string) error {
		candidate.Text = strings.TrimSpace(candidate.Text)
		if candidate.Text == "" {
			return fmt.Errorf("verification: empty %s criterion", origin)
		}
		normalized := normalizeCriterionText(candidate.Text)
		if index, exists := byText[normalized]; exists {
			if origin == "inferred" {
				return nil
			}
			criteria[index].Sources = mergeSources(criteria[index].Sources, candidate.Sources)
			criteria[index].Required = criteria[index].Required || candidate.Required
			return nil
		}
		id := strings.TrimSpace(candidate.ID)
		if id == "" {
			id = origin + ":" + shortHash(candidate.Text)
		}
		criterion := session.CriterionV1{ID: id, Text: candidate.Text, Origin: origin, Required: candidate.Required, Sources: mergeSources(nil, candidate.Sources)}
		criteria = append(criteria, criterion)
		byText[normalized] = len(criteria) - 1
		return nil
	}
	for _, candidate := range explicit {
		if err := appendCriterion(candidate, "explicit"); err != nil {
			return session.WorkSpecV1{}, session.VerificationPlanV1{}, err
		}
	}
	for _, candidate := range input.InferredCriteria {
		if err := appendCriterion(candidate, "inferred"); err != nil {
			return session.WorkSpecV1{}, session.VerificationPlanV1{}, err
		}
	}
	work := session.WorkSpecV1{
		Version: session.WorkContractVersionV1, SessionID: input.SessionID, RunID: input.RunID,
		Goal: input.Goal, ApprovedPlanID: input.ApprovedPlanID, Criteria: criteria, CreatedAt: input.CreatedAt.UTC(),
	}
	work.ID = session.CanonicalWorkSpecID(work)
	checks := make([]session.VerificationCheckV1, 0, len(criteria))
	for _, criterion := range criteria {
		checks = append(checks, session.VerificationCheckV1{ID: "criterion:" + criterion.ID, CriterionIDs: []string{criterion.ID}, Kind: "criterion"})
	}
	plan := session.VerificationPlanV1{
		Version: session.WorkContractVersionV1, ID: "verification:" + shortHash(work.ID+":"+input.RevisionID+":"+input.SnapshotHash),
		WorkSpecID: work.ID, RevisionID: input.RevisionID, SnapshotHash: input.SnapshotHash, Checks: checks, CreatedAt: input.CreatedAt.UTC(),
	}
	if err := work.Validate(); err != nil {
		return session.WorkSpecV1{}, session.VerificationPlanV1{}, err
	}
	if err := plan.Validate(); err != nil {
		return session.WorkSpecV1{}, session.VerificationPlanV1{}, err
	}
	return work, plan, nil
}

func normalizeCriterionText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func mergeSources(existing, additions []session.SourceRefV1) []session.SourceRefV1 {
	result := append([]session.SourceRefV1(nil), existing...)
	seen := make(map[string]struct{}, len(result)+len(additions))
	for _, source := range result {
		seen[source.Kind+"\x00"+source.ID+"\x00"+source.Range+"\x00"+source.SHA256] = struct{}{}
	}
	for _, source := range additions {
		key := source.Kind + "\x00" + source.ID + "\x00" + source.Range + "\x00" + source.SHA256
		if source.Kind == "" || source.ID == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		return left.Kind+"\x00"+left.ID+"\x00"+left.Range+"\x00"+left.SHA256 < right.Kind+"\x00"+right.ID+"\x00"+right.Range+"\x00"+right.SHA256
	})
	return result
}

func shortHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:8])
}
