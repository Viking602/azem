package routeeval

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestCalibrationReportUsesFixedSplitAndShowsProviderModelGaps(t *testing.T) {
	t.Parallel()
	now := time.Unix(30, 0).UTC()
	train := baselineOutcome("train", "train", "train", "model-a", "pass", now)
	heldA1 := baselineOutcome("held-a-pass", "task-1", "held_out", "model-a", "pass", now)
	heldA1.PredictedSuccess = 0.8
	heldA2 := baselineOutcome("held-a-fail", "task-2", "held_out", "model-a", "fail", now)
	heldA2.PredictedSuccess = 0.7
	heldA2.FalsePass = true
	heldB1 := baselineOutcome("held-b-pass", "task-1", "held_out", "model-b", "pass", now)
	heldB1.PredictedSuccess = 0.6
	heldB2 := baselineOutcome("held-b-pass-2", "task-2", "held_out", "model-b", "pass", now)
	heldB2.PredictedSuccess = 0.7
	dataset, err := NewDataset([]session.RouteOutcomeV1{train, heldA1, heldA2, heldB1, heldB2}, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := EvaluateCalibration(dataset, 602)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EvaluateCalibration(dataset, 602)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic report: %+v %+v err=%v", first, second, err)
	}
	if first.Seed != 602 || first.SplitDigest == "" || first.Fairness.Groups != 2 || first.Fairness.MinimumSamples != 2 || first.Fairness.MaxSuccessGap != 0.5 || first.Fairness.MaxFalsePassGap != 0.5 {
		t.Fatalf("report = %+v", first)
	}
	if _, err := EvaluateCalibration(dataset, 0); err == nil {
		t.Fatal("accepted implicit evaluation seed")
	}
}

func TestParetoFrontAndPromotionGateRejectRegressions(t *testing.T) {
	t.Parallel()
	context := RouteKeyV1{TaskClass: "go", Repository: "azem", Toolchain: "go1.25", Provider: "provider", PolicyVersion: "policy"}
	quality := BaselineV1{Route: context, Samples: 20, SuccessRate: 0.9, CriterionCoverage: 1, MeanLatencyMS: 200, MeanInputTokens: 100, MeanOutputTokens: 20, MeanCacheReadTokens: 40, MeanCostMicros: 100, FalsePassRate: 0.05, BrierScore: 0.1, CalibrationError: 0.05}
	quality.Route.Model = "quality"
	cheap := quality
	cheap.Route.Model = "cheap"
	cheap.SuccessRate = 0.8
	cheap.MeanLatencyMS = 100
	cheap.MeanCostMicros = 50
	cheap.MeanInputTokens = 70
	cheap.MeanCacheReadTokens = 50
	dominated := quality
	dominated.Route.Model = "dominated"
	dominated.SuccessRate = 0.7
	dominated.MeanLatencyMS = 300
	dominated.MeanCostMicros = 150
	dominated.MeanInputTokens = 150
	dominated.MeanCacheReadTokens = 10
	front := ParetoFront([]BaselineV1{dominated, cheap, quality})
	if len(front) != 2 || front[0].Route.Model != "cheap" || front[1].Route.Model != "quality" {
		t.Fatalf("pareto front = %+v", front)
	}
	candidate := quality
	candidate.Route.Model = "candidate"
	candidate.MeanCostMicros = 90
	if decision := CheckPromotion(quality, candidate); !decision.Allowed {
		t.Fatalf("rejected cost improvement: %+v", decision)
	}
	candidate.RetentionEventRate = 0.1
	if decision := CheckPromotion(quality, candidate); decision.Allowed || !containsReason(decision.Reasons, "retention_regression") {
		t.Fatalf("accepted retention regression: %+v", decision)
	}
	candidate = quality
	candidate.Route.Model = "candidate"
	if decision := CheckPromotion(quality, candidate); decision.Allowed || !containsReason(decision.Reasons, "no_false_decision_or_cost_improvement") {
		t.Fatalf("accepted no-op candidate: %+v", decision)
	}
}

func containsReason(reasons []string, wanted string) bool {
	for _, reason := range reasons {
		if reason == wanted {
			return true
		}
	}
	return false
}
func TestPromotionRejectsLatencyCostAndNonFiniteRegressions(t *testing.T) {
	t.Parallel()
	route := RouteKeyV1{TaskClass: "go", Repository: "azem", Toolchain: "go", Provider: "provider", Model: "model", Budget: "standard", PolicyVersion: "policy"}
	incumbent := BaselineV1{Route: route, Samples: 20, SuccessRate: 0.8, CriterionCoverage: 1, MeanLatencyMS: 100, MeanCostMicros: 100}
	candidate := incumbent
	candidate.MeanLatencyMS = 101
	candidate.MeanCostMicros = 101
	decision := CheckPromotion(incumbent, candidate)
	if decision.Allowed || !containsReason(decision.Reasons, "latency_regression") || !containsReason(decision.Reasons, "cost_regression") {
		t.Fatalf("accepted latency/cost regression: %+v", decision)
	}
	candidate = incumbent
	candidate.MeanCostMicros = math.NaN()
	if decision := CheckPromotion(incumbent, candidate); decision.Allowed {
		t.Fatalf("accepted non-finite promotion metrics: %+v", decision)
	}
}
