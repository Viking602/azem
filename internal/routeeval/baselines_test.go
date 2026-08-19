package routeeval

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestTransparentBaselinesPreferHeldOutSuccessfulRoute(t *testing.T) {
	t.Parallel()
	now := time.Unix(20, 0).UTC()
	var outcomes []session.RouteOutcomeV1
	for index := range 3 {
		outcomes = append(outcomes,
			baselineOutcome(fmt.Sprintf("train-a-%d", index), fmt.Sprintf("train-a-%d", index), "train", "model-a", "pass", now),
			baselineOutcome(fmt.Sprintf("train-b-%d", index), fmt.Sprintf("train-b-%d", index), "train", "model-b", "fail", now),
		)
	}
	for index := range 2 {
		taskID := fmt.Sprintf("held-%d", index)
		outcomes = append(outcomes,
			baselineOutcome(fmt.Sprintf("held-a-%d", index), taskID, "held_out", "model-a", "pass", now),
			baselineOutcome(fmt.Sprintf("held-b-%d", index), taskID, "held_out", "model-b", "fail", now),
		)
	}
	dataset, err := NewDataset(outcomes, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := CompareTransparentBaselines(dataset)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompareTransparentBaselines(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("comparison is not deterministic:\n%+v\n%+v", first, second)
	}
	wantNames := []string{"empirical_rules", "logistic_regression", "beta_ucb"}
	if len(first) != len(wantNames) {
		t.Fatalf("comparisons = %+v", first)
	}
	for index, comparison := range first {
		if comparison.Name != wantNames[index] || comparison.HeldOutTasks != 2 || comparison.SuccessRate != 1 || comparison.MeanSelectionRegret != 0 || comparison.BrierScore < 0 || comparison.BrierScore > 1 {
			t.Fatalf("comparison[%d] = %+v", index, comparison)
		}
	}
}

func TestTransparentBaselinesRequirePairedHeldOutRoutes(t *testing.T) {
	t.Parallel()
	now := time.Unix(20, 0).UTC()
	dataset, err := NewDataset([]session.RouteOutcomeV1{
		baselineOutcome("train", "train", "train", "model-a", "pass", now),
		baselineOutcome("held", "held", "held_out", "model-a", "pass", now),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompareTransparentBaselines(dataset); err == nil {
		t.Fatal("accepted held-out task without paired routes")
	}
}

func baselineOutcome(id, taskID, split, model, correctness string, now time.Time) session.RouteOutcomeV1 {
	outcome := routeOutcome(id, taskID, correctness, correctness, 0.5, 1, 100, 100, 20, 0, 10, 0, false, false, false, nil, now)
	outcome.Split = split
	outcome.Model = model
	return outcome
}
