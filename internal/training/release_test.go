package training

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestHeldOutReleaseGateRequiresRealSafeNonContaminatedCostedGain(t *testing.T) {
	t.Parallel()
	input := validReleaseInput()
	gate := ReleaseGateV1{MinHeldOutTasks: 4, MinCorrectnessImprovement: 0.25, MaxSliceRegression: 0, MaxServingCostIncrease: 0.20, MaxTrainServeCostMicros: 1_000}
	report, err := EvaluateHeldOutRelease(input, gate)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ship || len(report.Reasons) != 0 || report.ContaminationFound || !report.CostAccountingComplete || report.RealTasks != 2 || len(report.Slices) != 16 {
		t.Fatalf("report = %+v", report)
	}
	if report.BaselineCorrectness != 0.25 || report.CandidateCorrectness != 1 || report.RealBaselineCorrectness != 0.5 || report.RealCandidateCorrectness != 1 || report.TrainServeCostMicros != 500 {
		t.Fatalf("metrics = %+v", report)
	}
	validation, err := AdapterValidationReport(report, []session.SourceRefV1{{Kind: "held_out_report", ID: "release-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if validation.Status != "pass" || validation.HeldOutTasks != 4 || validation.CorrectnessDelta != 0.75 || validation.TrainServeCostMicros != 500 || validation.Digest != report.Digest {
		t.Fatalf("validation = %+v", validation)
	}
	tampered := report
	tampered.CandidateCorrectness = 0
	if _, err := AdapterValidationReport(tampered, []session.SourceRefV1{{Kind: "report", ID: "tampered"}}); err == nil {
		t.Fatal("converted tampered release report")
	}
}

func TestHeldOutReleaseGateRejectsContaminationSyntheticOnlySafetyAndMissingCosts(t *testing.T) {
	t.Parallel()
	gate := ReleaseGateV1{MinHeldOutTasks: 4, MinCorrectnessImprovement: 0.25, MaxSliceRegression: 1, MaxServingCostIncrease: 1, MaxTrainServeCostMicros: 10_000}

	contaminated := validReleaseInput()
	contaminated.TrainWorkSpecIDs = []string{"work-1"}
	report, err := EvaluateHeldOutRelease(contaminated, gate)
	if err != nil || report.Ship || !report.ContaminationFound || !contains(report.Reasons, "train/held-out contamination detected") {
		t.Fatalf("contamination report = %+v err=%v", report, err)
	}

	syntheticOnly := validReleaseInput()
	for index := 0; index < 2; index++ {
		syntheticOnly.Candidate[index].Outcome.Correctness = syntheticOnly.Baseline[index].Outcome.Correctness
	}
	report, err = EvaluateHeldOutRelease(syntheticOnly, gate)
	if err != nil || report.Ship || !contains(report.Reasons, "improvement is synthetic-only") {
		t.Fatalf("synthetic report = %+v err=%v", report, err)
	}

	unsafe := validReleaseInput()
	unsafe.Candidate[0].Outcome.SafetyEvents = []string{"permission_bypass"}
	unsafe.Candidate[1].Outcome.SafetyEvents = []string{"permission_bypass"}
	report, err = EvaluateHeldOutRelease(unsafe, gate)
	if err != nil || report.Ship || !contains(report.Reasons, "safety regressed") {
		t.Fatalf("safety report = %+v err=%v", report, err)
	}

	uncosted := validReleaseInput()
	uncosted.Candidate[0].Outcome.InputTokens = 0
	report, err = EvaluateHeldOutRelease(uncosted, gate)
	if err != nil || report.Ship || report.CostAccountingComplete || !contains(report.Reasons, "train and serving cost accounting is incomplete") {
		t.Fatalf("cost report = %+v err=%v", report, err)
	}
}
func TestHeldOutReleaseRejectsIdentityMismatchOverflowDigestCaseAndNonFiniteGate(t *testing.T) {
	t.Parallel()
	gate := ReleaseGateV1{MinHeldOutTasks: 4, MinCorrectnessImprovement: 0, MaxSliceRegression: 1, MaxServingCostIncrease: 1, MaxTrainServeCostMicros: 10_000}

	mismatched := validReleaseInput()
	mismatched.Candidate[0].Outcome.TaskID = "different-task"
	if _, err := EvaluateHeldOutRelease(mismatched, gate); err == nil {
		t.Fatal("accepted mismatched task identity")
	}

	uppercase := validReleaseInput()
	uppercase.TrainContentSHA256[0] = strings.ToUpper(uppercase.Baseline[0].ContentSHA256)
	report, err := EvaluateHeldOutRelease(uppercase, gate)
	if err != nil || report.Ship || !report.ContaminationFound {
		t.Fatalf("accepted case-variant contamination: %+v err=%v", report, err)
	}
	overflow := validReleaseInput()
	overflow.DeclaredTrainingCostMicros = int64(^uint64(0) >> 1)
	overflow.TrainingCosts[0].CostMicros = int64(^uint64(0) >> 1)
	overflow.TrainingCosts[1].CostMicros = 1
	if _, err := EvaluateHeldOutRelease(overflow, gate); err != nil {
		t.Fatalf("overflow should produce a rejected report, got error: %v", err)
	} else if overflowReport, evalErr := EvaluateHeldOutRelease(overflow, gate); evalErr == nil && overflowReport.CostAccountingComplete {
		t.Fatalf("accepted overflowing cost accounting: %+v", overflowReport)
	}

	badGate := gate
	badGate.MaxServingCostIncrease = math.NaN()
	if _, err := EvaluateHeldOutRelease(validReleaseInput(), badGate); err == nil {
		t.Fatal("accepted non-finite release gate")
	}
}

func validReleaseInput() ReleaseEvaluationInputV1 {
	now := time.Unix(1_700_000_000, 0).UTC()
	input := ReleaseEvaluationInputV1{
		TrainWorkSpecIDs: []string{"train-work"}, TrainRepositories: []string{"train-project"}, TrainContentSHA256: []string{strings.Repeat("f", 64)},
		TrainingCosts: []CostLineItemV1{{Category: "data_preparation", CostMicros: 10}, {Category: "training_compute", CostMicros: 30}, {Category: "validation_compute", CostMicros: 20}}, DeclaredTrainingCostMicros: 60,
	}
	for index := 0; index < 4; index++ {
		datasetKind := "synthetic"
		if index < 2 {
			datasetKind = "real"
		}
		correctness := "fail"
		if index == 0 {
			correctness = "pass"
		}
		base := releaseOutcome(fmt.Sprintf("base-%d", index), fmt.Sprintf("work-%d", index), fmt.Sprintf("project-%d", index), "baseline-model", correctness, 100, now)
		if index == 1 {
			base.SafetyEvents = []string{"guardrail"}
			base.RetentionEvents = []string{"forgot_valid_memory"}
			base.FalsePass = true
		}
		candidate := releaseOutcome(fmt.Sprintf("candidate-%d", index), base.WorkSpecID, base.Repository, "adapted-model", "pass", 110, now)
		if index >= 2 {
			base.TaskClass, candidate.TaskClass = "typescript", "typescript"
		}
		hash := fmt.Sprintf("%064x", index+1)
		input.Baseline = append(input.Baseline, ReleaseSampleV1{DatasetKind: datasetKind, ContentSHA256: hash, Outcome: base})
		input.Candidate = append(input.Candidate, ReleaseSampleV1{DatasetKind: datasetKind, ContentSHA256: hash, Outcome: candidate})
		input.DeclaredBaselineServingCostMicros += base.CostMicros
		input.DeclaredCandidateServingCostMicros += candidate.CostMicros
	}
	return input
}

func releaseOutcome(id, workSpecID, repository, model, correctness string, cost int64, now time.Time) session.RouteOutcomeV1 {
	return session.RouteOutcomeV1{
		Version: 1, ID: id, WorkSpecID: workSpecID, TaskID: workSpecID, TaskClass: "go", Repository: repository, Toolchain: "go", PolicyVersion: "policy-v1",
		Provider: "openrouter", Model: model, Split: "held_out", Status: correctness, Correctness: correctness, CriterionCoverage: 1,
		InputTokens: 100, OutputTokens: 20, CostMicros: cost, LatencyMS: 50, Evidence: []session.SourceRefV1{{Kind: "validator", ID: "go-test"}}, CompletedAt: now,
	}
}
