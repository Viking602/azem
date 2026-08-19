package verification

import (
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestCompileCriteriaPreservesExplicitScopeBeforeInference(t *testing.T) {
	t.Parallel()
	created := time.Unix(100, 0).UTC()
	input := CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship safely", ApprovedPlanID: "plan-1",
		RevisionID: "revision-1", SnapshotHash: "snapshot-1", CreatedAt: created,
		UserCriteria: []CriterionInput{{ID: "user-tests", Text: "All tests pass", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "10"}}}},
		PlanCriteria: []CriterionInput{{ID: "plan-tests", Text: "  all   TESTS pass ", Required: true, Sources: []session.SourceRefV1{{Kind: "plan", ID: "plan-1"}}}},
		Todo: session.TodoList{Revision: 4, Phases: []session.TodoPhase{{ID: "phase", Items: []session.TodoItem{
			{ID: "todo-live", Content: "Update the implementation", Status: session.TodoInProgress},
			{ID: "todo-cancelled", Content: "Do not include", Status: session.TodoCancelled},
		}}}},
		InferredCriteria: []CriterionInput{
			{ID: "inferred-tests", Text: "all tests pass", Required: false, Sources: []session.SourceRefV1{{Kind: "agent", ID: "guess"}}},
			{ID: "inferred-format", Text: "Formatting remains clean", Required: false, Sources: []session.SourceRefV1{{Kind: "workspace", ID: "go"}}},
		},
	}
	work, plan, err := CompileCriteria(input)
	if err != nil {
		t.Fatal(err)
	}
	if work.ApprovedPlanID != "plan-1" || len(work.Criteria) != 3 || len(plan.Checks) != 3 {
		t.Fatalf("compiled work = %+v plan=%+v", work, plan)
	}
	if work.Criteria[0].ID != "user-tests" || work.Criteria[0].Origin != "explicit" || !work.Criteria[0].Required || len(work.Criteria[0].Sources) != 2 {
		t.Fatalf("explicit criterion was not authoritative: %+v", work.Criteria[0])
	}
	if work.Criteria[1].ID != "todo:todo-live" || work.Criteria[1].Origin != "explicit" || work.Criteria[2].ID != "inferred-format" || work.Criteria[2].Origin != "inferred" {
		t.Fatalf("criterion order/origin = %+v", work.Criteria)
	}
	for index, check := range plan.Checks {
		if check.Kind != "criterion" || len(check.CriterionIDs) != 1 || check.CriterionIDs[0] != work.Criteria[index].ID {
			t.Fatalf("check %d = %+v", index, check)
		}
	}
	secondWork, secondPlan, err := CompileCriteria(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(secondWork, work) || !reflect.DeepEqual(secondPlan, plan) {
		t.Fatal("criteria compilation is not deterministic")
	}
}

func TestCompileCriteriaUsesGoalWhenNoExplicitCriteriaExist(t *testing.T) {
	t.Parallel()
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "fix the bug", GoalSource: session.SourceRefV1{Kind: "sequence", ID: "1"},
		RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		InferredCriteria: []CriterionInput{{ID: "inferred", Text: "add logging"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(work.Criteria) != 2 || work.Criteria[0].ID != "goal" || work.Criteria[0].Origin != "explicit" || work.Criteria[1].Origin != "inferred" || len(plan.Checks) != 2 {
		t.Fatalf("goal fallback = %+v plan=%+v", work, plan)
	}
}
