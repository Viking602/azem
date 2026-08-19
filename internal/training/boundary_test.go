package training

import (
	"fmt"
	"testing"

	"github.com/Viking602/azem/internal/session"
)

func TestBoundaryRerankingUsesOnlyValidatedSameRevisionTargets(t *testing.T) {
	t.Parallel()
	var observations []BoundaryObservationV1
	pairs := []struct{ boundary, left, right string }{
		{BoundaryActReread, "act", "re_read"},
		{BoundaryRetryEscalate, "retry", "escalate"},
		{BoundaryContinueStop, "continue", "stop"},
		{BoundaryRetrieveProceed, "retrieve", "proceed"},
	}
	for index, pair := range pairs {
		observations = append(observations,
			boundaryObservation(fmt.Sprintf("left-%d", index), pair.boundary, pair.left, map[string]float64{"left_signal": 2}),
			boundaryObservation(fmt.Sprintf("right-%d", index), pair.boundary, pair.right, map[string]float64{"right_signal": 2}),
		)
	}
	authorityMismatch := boundaryObservation("authority-mismatch", BoundaryActReread, "act", map[string]float64{"left_signal": 100})
	authorityMismatch.TargetAuthority = "user"
	stale := boundaryObservation("stale", BoundaryRetryEscalate, "retry", map[string]float64{"left_signal": 100})
	stale.TargetRevisionID = "revision-2"
	unvalidated := boundaryObservation("unvalidated", BoundaryContinueStop, "continue", map[string]float64{"left_signal": 100})
	unvalidated.TargetValidated = false
	observations = append(observations, authorityMismatch, stale, unvalidated)
	dataset, err := BuildBoundaryDataset(observations)
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.Examples) != 8 || len(dataset.Exclusions) != 3 {
		t.Fatalf("dataset = %+v", dataset)
	}
	observations[0].Features["left_signal"] = -100
	if dataset.Examples[0].Features["left_signal"] != 2 {
		t.Fatal("dataset retained mutable caller feature map")
	}
	model, err := TrainBoundaryReranker(dataset)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := EvaluateBoundaryReranker(model, dataset)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Accuracy != 1 {
		t.Fatalf("evaluation = %+v", evaluation)
	}
	for _, pair := range pairs {
		left, err := model.Predict(pair.boundary, map[string]float64{"left_signal": 1})
		if err != nil || left != pair.left {
			t.Fatalf("left prediction %s = %q err=%v", pair.boundary, left, err)
		}
		right, err := model.Predict(pair.boundary, map[string]float64{"right_signal": 1})
		if err != nil || right != pair.right {
			t.Fatalf("right prediction %s = %q err=%v", pair.boundary, right, err)
		}
	}
}

func TestBoundaryDatasetRejectsAllInvalidTargets(t *testing.T) {
	t.Parallel()
	observation := boundaryObservation("invalid", BoundaryActReread, "act", map[string]float64{"signal": 1})
	observation.TargetValidated = false
	if _, err := BuildBoundaryDataset([]BoundaryObservationV1{observation}); err == nil {
		t.Fatal("accepted dataset with no validator-backed target")
	}
}

func boundaryObservation(id, boundary, target string, features map[string]float64) BoundaryObservationV1 {
	return BoundaryObservationV1{
		ID: id, Boundary: boundary, Features: features, Target: target, TargetValidated: true,
		Authority: "workspace", TargetAuthority: "workspace", RevisionID: "revision-1", TargetRevisionID: "revision-1",
		Evidence: []session.SourceRefV1{{Kind: "validator", ID: "go-test"}},
	}
}
