package routeeval

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestDatasetAggregatesCorrectnessCalibrationCostAndSafety(t *testing.T) {
	t.Parallel()
	now := time.Unix(10, 0).UTC()
	outcomes := []session.RouteOutcomeV1{
		routeOutcome("pass", "task-a", "pass", "pass", 0.8, 1, 100, 100, 20, 50, 10, 0, false, false, false, nil, now),
		routeOutcome("fail", "task-b", "fail", "fail", 0.6, 0.5, 200, 200, 40, 0, 30, 1, true, false, false, []string{"unsafe_tool_request"}, now),
		routeOutcome("cancel", "task-c", "cancelled", "uncertain", 0.4, 0, 50, 0, 0, 0, 0, 0, false, false, true, nil, now),
	}
	dataset, err := NewDataset([]session.RouteOutcomeV1{outcomes[2], outcomes[0], outcomes[1]}, now)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Outcomes[0].ID != "cancel" || dataset.ID == "" {
		t.Fatalf("dataset order/identity = %+v", dataset)
	}
	baselines, err := Aggregate(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if len(baselines) != 1 {
		t.Fatalf("baselines = %+v", baselines)
	}
	baseline := baselines[0]
	assertNear(t, baseline.SuccessRate, 0.5)
	assertNear(t, baseline.CriterionCoverage, 0.5)
	assertNear(t, baseline.FalsePassRate, 1.0/3)
	assertNear(t, baseline.FalseFailRate, 0)
	assertNear(t, baseline.MeanLatencyMS, 350.0/3)
	assertNear(t, baseline.MeanInputTokens, 100)
	assertNear(t, baseline.MeanOutputTokens, 20)
	assertNear(t, baseline.MeanCacheReadTokens, 50.0/3)
	assertNear(t, baseline.MeanCostMicros, 40.0/3)
	assertNear(t, baseline.MeanRetries, 1.0/3)
	assertNear(t, baseline.CancellationRate, 1.0/3)
	assertNear(t, baseline.RecoveryRate, 1.0/3)
	assertNear(t, baseline.SafetyEventRate, 1.0/3)
	assertNear(t, baseline.BrierScore, 0.2)
	assertNear(t, baseline.CalibrationError, 0.4)
	if baseline.P50LatencyMS != 100 || baseline.DefiniteSamples != 2 {
		t.Fatalf("baseline = %+v", baseline)
	}
	payload, err := EncodeDataset(dataset)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDataset(payload)
	if err != nil || decoded.ID != dataset.ID || len(decoded.Outcomes) != 3 {
		t.Fatalf("decoded = %+v err=%v", decoded, err)
	}
	if _, err := DecodeDataset(bytes.Replace(payload, []byte(`"version": 1`), []byte(`"version": 1, "unknown": true`), 1)); err == nil {
		t.Fatal("accepted unknown dataset field")
	}
}

func TestDatasetRejectsMissingCalibrationIdentity(t *testing.T) {
	t.Parallel()
	outcome := routeOutcome("id", "", "pass", "pass", 0.5, 1, 1, 1, 1, 0, 1, 0, false, false, false, nil, time.Unix(1, 0).UTC())
	if _, err := NewDataset([]session.RouteOutcomeV1{outcome}, time.Unix(1, 0).UTC()); err == nil {
		t.Fatal("accepted outcome without task id")
	}
}

func routeOutcome(id, taskID, status, correctness string, predicted, coverage float64, latency, input, output, cache, cost int64, retries int, falsePass, falseFail, recovered bool, safety []string, now time.Time) session.RouteOutcomeV1 {
	return session.RouteOutcomeV1{
		Version: 1, ID: id, WorkSpecID: "work-" + id, TaskID: taskID, TaskClass: "go", Repository: "azem", Toolchain: "go1.25", PolicyVersion: "policy-v1",
		Provider: "provider", Model: "model", Budget: "standard", Split: "held_out", Status: status, Correctness: correctness, PredictedSuccess: predicted,
		CriterionCoverage: coverage, FalsePass: falsePass, FalseFail: falseFail, LatencyMS: latency, InputTokens: input, OutputTokens: output, CacheReadTokens: cache,
		CostMicros: cost, Retries: retries, Recovered: recovered, SafetyEvents: safety, Evidence: []session.SourceRefV1{{Kind: "verification_result", ID: "result-" + id}}, CompletedAt: now,
	}
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %v want %v", got, want)
	}
}
func TestDatasetRejectsIdentityTamperingAndNonFiniteMetrics(t *testing.T) {
	t.Parallel()
	now := time.Unix(1, 0).UTC()
	dataset, err := NewDataset([]session.RouteOutcomeV1{routeOutcome("id", "task", "pass", "pass", 0.5, 1, 1, 1, 1, 0, 1, 0, false, false, false, nil, now)}, now)
	if err != nil {
		t.Fatal(err)
	}
	dataset.Outcomes[0].CriterionCoverage = 0.5
	if err := dataset.Validate(); err == nil {
		t.Fatal("accepted dataset identity tampering")
	}
	dataset.Outcomes[0].CriterionCoverage = math.NaN()
	if err := dataset.Validate(); err == nil {
		t.Fatal("accepted non-finite dataset metric")
	}
}
