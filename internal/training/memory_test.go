package training

import (
	"testing"
	"time"

	"github.com/Viking602/azem/internal/codingmemory"
	"github.com/Viking602/azem/internal/evidence"
	"github.com/Viking602/azem/internal/session"
)

func TestTrainMemoryPolicyUsesOnlyConsumedSuccessfulLineage(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	successMemory := memoryForTraining("memory-success", 0.8, now)
	failedMemory := memoryForTraining("memory-failed", 0.2, now)
	unconsumedMemory := memoryForTraining("memory-unconsumed", 0.99, now)
	lineage := []MemoryLineageV1{
		memoryLineage("success", 20, "pass", &successMemory, []string{successMemory.ID}, nil, 4_000, 1_000, now),
		memoryLineage("failed", 2, "fail", &failedMemory, []string{failedMemory.ID}, []string{failedMemory.ID}, 4_000, 800, now),
		memoryLineage("unconsumed", 30, "pass", &unconsumedMemory, nil, nil, 4_000, 900, now),
	}
	policy, err := TrainMemoryPolicy(lineage)
	if err != nil {
		t.Fatal(err)
	}
	if policy.MinRetrievalScore != 20 || policy.MinStoreConfidence != 0.8 || policy.MaxSemanticProjectionBytes != 1_000 || !policy.ForgetAfterFailedUse || policy.ConsumedExamples != 2 {
		t.Fatalf("policy = %+v", policy)
	}
	if !policy.ShouldStore(successMemory, true, "pass") || policy.ShouldStore(unconsumedMemory, false, "pass") || policy.ShouldStore(failedMemory, true, "fail") {
		t.Fatal("store policy ignored consumption or outcome lineage")
	}
	if !policy.ShouldRetrieve(20) || policy.ShouldRetrieve(2) || !policy.ShouldForget("fail") {
		t.Fatal("retrieval or forgetting policy did not preserve learned boundary")
	}
	evaluation, err := EvaluateMemoryPolicy(policy, lineage)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.DecisionRecall != 1 || evaluation.UnsupportedStores != 0 {
		t.Fatalf("evaluation = %+v", evaluation)
	}
}

func TestTrainMemoryPolicyRejectsLineageWithoutConsumedSuccess(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	memory := memoryForTraining("unused", 0.9, now)
	lineage := []MemoryLineageV1{memoryLineage("unused", 20, "pass", &memory, nil, nil, 100, 50, now)}
	if _, err := TrainMemoryPolicy(lineage); err == nil {
		t.Fatal("accepted memory training without actual consumption")
	}
}
func TestTrainMemoryPolicyRejectsUnselectedConsumedMemory(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	memory := memoryForTraining("unselected", 20, now)
	lineage := memoryLineage("unselected", 20, "pass", &memory, []string{memory.ID}, nil, 100, 50, now)
	lineage.Ledger.Candidates = lineage.Ledger.Candidates[:1]
	lineage.Ledger.Selected = lineage.Ledger.Selected[:1]
	if _, err := TrainMemoryPolicy([]MemoryLineageV1{lineage}); err == nil {
		t.Fatal("accepted positive example for unselected memory")
	}
}

func memoryLineage(id string, score int, status string, memory *codingmemory.MemoryV1, consumed, forgotten []string, before, after int, now time.Time) MemoryLineageV1 {
	ref := session.SourceRefV1{Kind: "workspace_file", ID: "internal/example.go", SHA256: "abc"}
	memoryRef := session.SourceRefV1{Kind: "coding_memory", ID: memory.ID}
	outcome := &evidence.ConsumerOutcomeV1{Action: "implement", Status: status, CompletedAt: now.Add(time.Minute)}
	if status == "pass" {
		outcome.Evidence = []session.SourceRefV1{ref}
	}
	return MemoryLineageV1{
		ID: id,
		Ledger: evidence.LedgerEntryV1{
			Version: 1, ID: "ledger-" + id, SessionID: "session", RunID: "run", Query: "find evidence", Subgoal: "implement safely",
			Candidates: []evidence.CandidateRecordV1{
				{Ref: ref, Authority: "workspace", Rank: 1, Score: score, Features: map[string]int{"exact": score}, Selected: true},
				{Ref: memoryRef, Authority: "history", Rank: 2, Score: score, Features: map[string]int{"memory": score}, Selected: true},
			},
			Selected: []session.SourceRefV1{ref, memoryRef}, Outcome: outcome, CreatedAt: now,
		},
		Memory: memory, ConsumedMemoryIDs: consumed, ForgottenMemoryIDs: forgotten,
		SemanticBytesBefore: before, SemanticProjectionBytes: after,
	}
}

func memoryForTraining(id string, confidence float64, now time.Time) codingmemory.MemoryV1 {
	return codingmemory.MemoryV1{
		Version: 1, ID: id, Kind: codingmemory.KindStrategy, Scope: codingmemory.ScopeV1{Kind: codingmemory.ScopeRepository, ID: "repo"},
		Content: "validated strategy", Confidence: confidence, Status: codingmemory.StatusActive, Origin: codingmemory.OriginDerived,
		Sources:   []codingmemory.AttributionV1{{Ref: session.SourceRefV1{Kind: "validator", ID: "go-test"}, Authority: "validator"}},
		CreatedAt: now, UpdatedAt: now,
	}
}
