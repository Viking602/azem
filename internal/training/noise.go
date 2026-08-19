// Package training contains offline-only dataset and model evaluation helpers.
package training

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	evalpkg "github.com/Viking602/azem/internal/eval"
)

const NoiseDatasetVersionV1 = 1

const (
	ActionRequireValidator = "require_validator_before_success"
	ActionRetryRecover     = "retry_or_recover_observation"
	ActionEscalate         = "escalate_ambiguity"
	ActionDeduplicate      = "deduplicate_evidence"
)

type NoiseExampleV1 struct {
	PairID           string                    `json:"pair_id"`
	RawClean         json.RawMessage           `json:"raw_clean"`
	RawNoisy         json.RawMessage           `json:"raw_noisy"`
	NormalizedLabels []evalpkg.IncidentLabelV1 `json:"normalized_labels"`
	TargetActions    []string                  `json:"target_actions"`
}

type NoiseDatasetV1 struct {
	Version  int              `json:"version"`
	ID       string           `json:"id"`
	Examples []NoiseExampleV1 `json:"examples"`
}

type NoiseRobustnessModelV1 struct {
	Version           int                 `json:"version"`
	ID                string              `json:"id"`
	DatasetID         string              `json:"dataset_id"`
	Seed              int64               `json:"seed"`
	ActionsByCategory map[string][]string `json:"actions_by_category"`
	DefaultActions    []string            `json:"default_actions"`
}

type NoiseEvaluationV1 struct {
	Examples             int     `json:"examples"`
	ExpectedActionRecall float64 `json:"expected_action_recall"`
	FalseSuccessRate     float64 `json:"false_success_rate"`
}

func BuildNoiseDataset(pairs []evalpkg.NoisePairV1) (NoiseDatasetV1, error) {
	if len(pairs) == 0 {
		return NoiseDatasetV1{}, fmt.Errorf("training: noise dataset requires pairs")
	}
	examples := make([]NoiseExampleV1, 0, len(pairs))
	for _, pair := range pairs {
		if err := pair.Validate(); err != nil {
			return NoiseDatasetV1{}, err
		}
		clean, err := json.Marshal(pair.Clean)
		if err != nil {
			return NoiseDatasetV1{}, err
		}
		noisy, err := json.Marshal(pair.Noisy)
		if err != nil {
			return NoiseDatasetV1{}, err
		}
		labels := append([]evalpkg.IncidentLabelV1(nil), pair.NoiseLabels...)
		sort.Slice(labels, func(i, j int) bool { return labels[i].ID < labels[j].ID })
		examples = append(examples, NoiseExampleV1{
			PairID: pair.ID, RawClean: append(json.RawMessage(nil), clean...), RawNoisy: append(json.RawMessage(nil), noisy...),
			NormalizedLabels: labels, TargetActions: targetActions(labels),
		})
	}
	sort.Slice(examples, func(i, j int) bool { return examples[i].PairID < examples[j].PairID })
	identity, _ := json.Marshal(examples)
	digest := sha256.Sum256(identity)
	return NoiseDatasetV1{Version: NoiseDatasetVersionV1, ID: "noise-dataset:" + hex.EncodeToString(digest[:12]), Examples: examples}, nil
}

func TrainNoiseRobustness(dataset NoiseDatasetV1, seed int64) (NoiseRobustnessModelV1, error) {
	if dataset.Version != NoiseDatasetVersionV1 || dataset.ID == "" || len(dataset.Examples) == 0 || seed == 0 {
		return NoiseRobustnessModelV1{}, fmt.Errorf("training: invalid noise training input")
	}
	counts := make(map[string]map[string]int)
	for _, example := range dataset.Examples {
		for _, label := range example.NormalizedLabels {
			if counts[label.Category] == nil {
				counts[label.Category] = make(map[string]int)
			}
			for _, action := range targetActions([]evalpkg.IncidentLabelV1{label}) {
				counts[label.Category][action]++
			}
		}
	}
	actionsByCategory := make(map[string][]string, len(counts))
	for category, actionCounts := range counts {
		type counted struct {
			action string
			count  int
		}
		ordered := make([]counted, 0, len(actionCounts))
		for action, count := range actionCounts {
			ordered = append(ordered, counted{action, count})
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].count != ordered[j].count {
				return ordered[i].count > ordered[j].count
			}
			return ordered[i].action < ordered[j].action
		})
		for _, item := range ordered {
			actionsByCategory[category] = append(actionsByCategory[category], item.action)
		}
	}
	identity, _ := json.Marshal(struct {
		Dataset string              `json:"dataset"`
		Seed    int64               `json:"seed"`
		Actions map[string][]string `json:"actions"`
	}{dataset.ID, seed, actionsByCategory})
	digest := sha256.Sum256(identity)
	return NoiseRobustnessModelV1{
		Version: 1, ID: "noise-model:" + hex.EncodeToString(digest[:12]), DatasetID: dataset.ID, Seed: seed,
		ActionsByCategory: actionsByCategory, DefaultActions: []string{ActionRequireValidator},
	}, nil
}

func (model NoiseRobustnessModelV1) Predict(labels []evalpkg.IncidentLabelV1) []string {
	actions := append([]string(nil), model.DefaultActions...)
	for _, label := range labels {
		actions = append(actions, model.ActionsByCategory[label.Category]...)
	}
	return uniqueSorted(actions)
}

func EvaluateNoiseRobustness(model NoiseRobustnessModelV1, dataset NoiseDatasetV1) (NoiseEvaluationV1, error) {
	if model.Version != 1 || model.DatasetID == "" || dataset.Version != NoiseDatasetVersionV1 || len(dataset.Examples) == 0 {
		return NoiseEvaluationV1{}, fmt.Errorf("training: invalid noise evaluation input")
	}
	evaluation := NoiseEvaluationV1{Examples: len(dataset.Examples)}
	var expected, recovered int
	var falseSuccess int
	for _, example := range dataset.Examples {
		predicted := model.Predict(example.NormalizedLabels)
		for _, action := range example.TargetActions {
			expected++
			if contains(predicted, action) {
				recovered++
			}
		}
		if !contains(predicted, ActionRequireValidator) {
			falseSuccess++
		}
	}
	if expected > 0 {
		evaluation.ExpectedActionRecall = float64(recovered) / float64(expected)
	}
	evaluation.FalseSuccessRate = float64(falseSuccess) / float64(evaluation.Examples)
	return evaluation, nil
}

func targetActions(labels []evalpkg.IncidentLabelV1) []string {
	actions := []string{ActionRequireValidator}
	for _, label := range labels {
		switch label.Category {
		case evalpkg.IncidentToolFailure, evalpkg.IncidentToolIncomplete, evalpkg.IncidentToolErroneous, evalpkg.IncidentToolMisleading:
			actions = append(actions, ActionRetryRecover)
		case evalpkg.IncidentUserAmbiguity, evalpkg.IncidentUserInconsistency, evalpkg.IncidentUserDrift, evalpkg.IncidentUserBoundaryProbe:
			actions = append(actions, ActionEscalate)
		case evalpkg.IncidentUserRedundancy, evalpkg.IncidentToolRedundant:
			actions = append(actions, ActionDeduplicate)
		}
	}
	return uniqueSorted(actions)
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
