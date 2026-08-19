package training

import (
	"fmt"
	"github.com/Viking602/azem/internal/codingmemory"
	"github.com/Viking602/azem/internal/evidence"
	"math"
	"sort"
)

type MemoryLineageV1 struct {
	ID                      string                 `json:"id"`
	Ledger                  evidence.LedgerEntryV1 `json:"ledger"`
	Memory                  *codingmemory.MemoryV1 `json:"memory,omitempty"`
	ConsumedMemoryIDs       []string               `json:"consumed_memory_ids,omitempty"`
	ForgottenMemoryIDs      []string               `json:"forgotten_memory_ids,omitempty"`
	SemanticBytesBefore     int                    `json:"semantic_bytes_before"`
	SemanticProjectionBytes int                    `json:"semantic_projection_bytes"`
}

type MemoryPolicyV1 struct {
	Version                    int     `json:"version"`
	MinRetrievalScore          int     `json:"min_retrieval_score"`
	MinStoreConfidence         float64 `json:"min_store_confidence"`
	MaxSemanticProjectionBytes int     `json:"max_semantic_projection_bytes"`
	ForgetAfterFailedUse       bool    `json:"forget_after_failed_use"`
	TrainingExamples           int     `json:"training_examples"`
	ConsumedExamples           int     `json:"consumed_examples"`
}

type MemoryPolicyEvaluationV1 struct {
	Examples          int     `json:"examples"`
	DecisionRecall    float64 `json:"decision_recall"`
	UnsupportedStores int     `json:"unsupported_stores"`
}

func TrainMemoryPolicy(lineage []MemoryLineageV1) (MemoryPolicyV1, error) {
	if len(lineage) == 0 {
		return MemoryPolicyV1{}, fmt.Errorf("training: memory lineage is empty")
	}
	var retrievalScores []int
	var storeConfidences []float64
	var projectionSizes []int
	failed, failedForgotten := 0, 0
	consumed := 0
	for _, item := range lineage {
		if err := validateMemoryLineage(item); err != nil {
			return MemoryPolicyV1{}, err
		}
		pass := item.Ledger.Outcome != nil && item.Ledger.Outcome.Status == "pass"
		if pass && len(item.Ledger.Selected) > 0 {
			retrievalScores = append(retrievalScores, selectedAverageScore(item.Ledger))
		}
		if item.Memory != nil && contains(item.ConsumedMemoryIDs, item.Memory.ID) {
			consumed++
			if pass {
				storeConfidences = append(storeConfidences, item.Memory.Confidence)
			}
		}
		if item.SemanticProjectionBytes > 0 && item.SemanticProjectionBytes <= item.SemanticBytesBefore {
			projectionSizes = append(projectionSizes, item.SemanticProjectionBytes)
		}
		if item.Ledger.Outcome != nil && item.Ledger.Outcome.Status == "fail" {
			failed++
			if item.Memory != nil && contains(item.ForgottenMemoryIDs, item.Memory.ID) {
				failedForgotten++
			}
		}
	}
	if len(retrievalScores) == 0 || len(storeConfidences) == 0 || len(projectionSizes) == 0 {
		return MemoryPolicyV1{}, fmt.Errorf("training: memory lineage lacks consumed successful examples or compression bounds")
	}
	sort.Ints(retrievalScores)
	sort.Float64s(storeConfidences)
	sort.Ints(projectionSizes)
	return MemoryPolicyV1{
		Version:                    1,
		MinRetrievalScore:          retrievalScores[(len(retrievalScores)-1)/2],
		MinStoreConfidence:         storeConfidences[(len(storeConfidences)-1)/2],
		MaxSemanticProjectionBytes: projectionSizes[len(projectionSizes)-1],
		ForgetAfterFailedUse:       failed > 0 && failedForgotten*2 >= failed,
		TrainingExamples:           len(lineage), ConsumedExamples: consumed,
	}, nil
}

func (policy MemoryPolicyV1) ShouldRetrieve(selectedScore int) bool {
	return policy.Version == 1 && selectedScore >= policy.MinRetrievalScore
}

func (policy MemoryPolicyV1) ShouldStore(memory codingmemory.MemoryV1, consumed bool, outcomeStatus string) bool {
	return policy.Version == 1 && !math.IsNaN(policy.MinStoreConfidence) && !math.IsInf(policy.MinStoreConfidence, 0) && consumed && outcomeStatus == "pass" && memory.Status == codingmemory.StatusActive && !math.IsNaN(memory.Confidence) && !math.IsInf(memory.Confidence, 0) && memory.Confidence >= policy.MinStoreConfidence
}

func (policy MemoryPolicyV1) ShouldForget(outcomeStatus string) bool {
	return policy.Version == 1 && policy.ForgetAfterFailedUse && outcomeStatus == "fail"
}

func EvaluateMemoryPolicy(policy MemoryPolicyV1, lineage []MemoryLineageV1) (MemoryPolicyEvaluationV1, error) {
	if policy.Version != 1 || len(lineage) == 0 {
		return MemoryPolicyEvaluationV1{}, fmt.Errorf("training: invalid memory policy evaluation input")
	}
	result := MemoryPolicyEvaluationV1{Examples: len(lineage)}
	var expected, matched int
	for _, item := range lineage {
		if err := validateMemoryLineage(item); err != nil {
			return MemoryPolicyEvaluationV1{}, err
		}
		status := ""
		if item.Ledger.Outcome != nil {
			status = item.Ledger.Outcome.Status
		}
		retrieveTarget := status == "pass" && len(item.Ledger.Selected) > 0
		expected++
		if policy.ShouldRetrieve(selectedAverageScore(item.Ledger)) == retrieveTarget {
			matched++
		}
		if item.Memory != nil {
			consumed := contains(item.ConsumedMemoryIDs, item.Memory.ID)
			storeTarget := consumed && status == "pass"
			expected++
			store := policy.ShouldStore(*item.Memory, consumed, status)
			if store == storeTarget {
				matched++
			}
			if store && !consumed {
				result.UnsupportedStores++
			}
			forgetTarget := status == "fail" && contains(item.ForgottenMemoryIDs, item.Memory.ID)
			expected++
			if policy.ShouldForget(status) == forgetTarget {
				matched++
			}
		}
	}
	result.DecisionRecall = float64(matched) / float64(expected)
	return result, nil
}

func validateMemoryLineage(item MemoryLineageV1) error {
	if item.ID == "" || item.SemanticBytesBefore <= 0 || item.SemanticProjectionBytes <= 0 || item.SemanticProjectionBytes > item.SemanticBytesBefore {
		return fmt.Errorf("training: invalid memory lineage %q", item.ID)
	}
	if err := item.Ledger.Validate(); err != nil {
		return err
	}
	if item.Memory != nil {
		if err := item.Memory.Validate(); err != nil {
			return err
		}
	}
	selectedMemoryIDs := make(map[string]struct{})
	for _, ref := range item.Ledger.Selected {
		if ref.Kind == "coding_memory" {
			selectedMemoryIDs[ref.ID] = struct{}{}
		}
	}
	if err := validateIDs(item.ConsumedMemoryIDs, "consumed"); err != nil {
		return err
	}
	for _, id := range item.ConsumedMemoryIDs {
		if _, selected := selectedMemoryIDs[id]; !selected {
			return fmt.Errorf("training: consumed memory %q was not selected by retrieval", id)
		}
	}
	if err := validateIDs(item.ForgottenMemoryIDs, "forgotten"); err != nil {
		return err
	}
	if item.Memory != nil && contains(item.ConsumedMemoryIDs, item.Memory.ID) {
		if _, selected := selectedMemoryIDs[item.Memory.ID]; !selected {
			return fmt.Errorf("training: consumed memory %q was not selected by retrieval", item.Memory.ID)
		}
	}
	return nil
}

func validateIDs(ids []string, label string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("training: empty %s memory id", label)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("training: duplicate %s memory id %q", label, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func selectedAverageScore(entry evidence.LedgerEntryV1) int {
	var total, count int
	for _, candidate := range entry.Candidates {
		if candidate.Selected {
			total += candidate.Score
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / count
}
