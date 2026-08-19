package training

import (
	"fmt"
	"math"
	"sort"

	"github.com/Viking602/azem/internal/session"
)

const (
	BoundaryActReread       = "act_vs_reread"
	BoundaryRetryEscalate   = "retry_vs_escalate"
	BoundaryContinueStop    = "continue_vs_stop"
	BoundaryRetrieveProceed = "retrieve_vs_proceed"
)

type BoundaryObservationV1 struct {
	ID               string                `json:"id"`
	Boundary         string                `json:"boundary"`
	Features         map[string]float64    `json:"features"`
	Target           string                `json:"target"`
	TargetValidated  bool                  `json:"target_validated"`
	Authority        string                `json:"authority"`
	TargetAuthority  string                `json:"target_authority"`
	RevisionID       string                `json:"revision_id"`
	TargetRevisionID string                `json:"target_revision_id"`
	Evidence         []session.SourceRefV1 `json:"evidence"`
}

type BoundaryExclusionV1 struct {
	ObservationID string `json:"observation_id"`
	Reason        string `json:"reason"`
}

type BoundaryDatasetV1 struct {
	Version    int                     `json:"version"`
	Examples   []BoundaryObservationV1 `json:"examples"`
	Exclusions []BoundaryExclusionV1   `json:"exclusions"`
}

type BoundaryRerankerV1 struct {
	Version int                           `json:"version"`
	Weights map[string]map[string]float64 `json:"weights"`
	Targets map[string][]string           `json:"targets"`
}

type BoundaryEvaluationV1 struct {
	Examples int     `json:"examples"`
	Accuracy float64 `json:"accuracy"`
}

func BuildBoundaryDataset(observations []BoundaryObservationV1) (BoundaryDatasetV1, error) {
	if len(observations) == 0 {
		return BoundaryDatasetV1{}, fmt.Errorf("training: boundary observations are empty")
	}
	dataset := BoundaryDatasetV1{Version: 1}
	seen := make(map[string]struct{}, len(observations))
	for _, observation := range observations {
		if observation.ID == "" {
			return BoundaryDatasetV1{}, fmt.Errorf("training: boundary observation lacks id")
		}
		if _, exists := seen[observation.ID]; exists {
			return BoundaryDatasetV1{}, fmt.Errorf("training: duplicate boundary observation %q", observation.ID)
		}
		seen[observation.ID] = struct{}{}
		reason := boundaryExclusionReason(observation)
		if reason != "" {
			dataset.Exclusions = append(dataset.Exclusions, BoundaryExclusionV1{ObservationID: observation.ID, Reason: reason})
			continue
		}
		dataset.Examples = append(dataset.Examples, cloneBoundaryObservation(observation))
	}
	sort.Slice(dataset.Examples, func(i, j int) bool { return dataset.Examples[i].ID < dataset.Examples[j].ID })
	sort.Slice(dataset.Exclusions, func(i, j int) bool { return dataset.Exclusions[i].ObservationID < dataset.Exclusions[j].ObservationID })
	if len(dataset.Examples) == 0 {
		return BoundaryDatasetV1{}, fmt.Errorf("training: no valid boundary targets")
	}
	return dataset, nil
}

func TrainBoundaryReranker(dataset BoundaryDatasetV1) (BoundaryRerankerV1, error) {
	if dataset.Version != 1 || len(dataset.Examples) == 0 {
		return BoundaryRerankerV1{}, fmt.Errorf("training: invalid boundary dataset")
	}
	model := BoundaryRerankerV1{Version: 1, Weights: make(map[string]map[string]float64), Targets: make(map[string][]string)}
	for _, example := range dataset.Examples {
		if reason := boundaryExclusionReason(example); reason != "" {
			return BoundaryRerankerV1{}, fmt.Errorf("training: invalid target %q: %s", example.ID, reason)
		}
		left, right, _ := boundaryTargets(example.Boundary)
		model.Targets[example.Boundary] = []string{left, right}
		if model.Weights[example.Boundary] == nil {
			model.Weights[example.Boundary] = make(map[string]float64)
		}
		direction := 1.0
		if example.Target == right {
			direction = -1
		}
		for feature, value := range example.Features {
			model.Weights[example.Boundary][feature] += direction * value
		}
	}
	return model, nil
}

func (model BoundaryRerankerV1) Predict(boundary string, features map[string]float64) (string, error) {
	targets := model.Targets[boundary]
	if model.Version != 1 || len(targets) != 2 || len(features) == 0 {
		return "", fmt.Errorf("training: unsupported boundary prediction %q", boundary)
	}
	var score float64
	for feature, value := range features {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("training: invalid boundary feature")
		}
		score += model.Weights[boundary][feature] * value
	}
	if score >= 0 {
		return targets[0], nil
	}
	return targets[1], nil
}

func EvaluateBoundaryReranker(model BoundaryRerankerV1, dataset BoundaryDatasetV1) (BoundaryEvaluationV1, error) {
	result := BoundaryEvaluationV1{Examples: len(dataset.Examples)}
	if result.Examples == 0 {
		return result, fmt.Errorf("training: empty boundary evaluation")
	}
	var correct int
	for _, example := range dataset.Examples {
		prediction, err := model.Predict(example.Boundary, example.Features)
		if err != nil {
			return result, err
		}
		if prediction == example.Target {
			correct++
		}
	}
	result.Accuracy = float64(correct) / float64(result.Examples)
	return result, nil
}

func boundaryExclusionReason(observation BoundaryObservationV1) string {
	left, right, ok := boundaryTargets(observation.Boundary)
	if !ok || (observation.Target != left && observation.Target != right) || len(observation.Features) == 0 {
		return "unsupported boundary, target, or empty features"
	}
	for _, value := range observation.Features {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "non-finite feature"
		}
	}
	if !observation.TargetValidated || len(observation.Evidence) == 0 {
		return "downstream target is not validator-backed"
	}
	for _, ref := range observation.Evidence {
		if ref.Kind == "" || ref.ID == "" {
			return "invalid target evidence"
		}
	}
	if observation.Authority == "" || observation.Authority != observation.TargetAuthority {
		return "target authority changed"
	}
	if observation.RevisionID == "" || observation.RevisionID != observation.TargetRevisionID {
		return "target revision changed"
	}
	return ""
}

func boundaryTargets(boundary string) (string, string, bool) {
	switch boundary {
	case BoundaryActReread:
		return "act", "re_read", true
	case BoundaryRetryEscalate:
		return "retry", "escalate", true
	case BoundaryContinueStop:
		return "continue", "stop", true
	case BoundaryRetrieveProceed:
		return "retrieve", "proceed", true
	default:
		return "", "", false
	}
}

func cloneBoundaryObservation(observation BoundaryObservationV1) BoundaryObservationV1 {
	clone := observation
	clone.Features = make(map[string]float64, len(observation.Features))
	for key, value := range observation.Features {
		clone.Features[key] = value
	}
	clone.Evidence = append([]session.SourceRefV1(nil), observation.Evidence...)
	return clone
}
