package training

import (
	"math"
	"testing"
)

func TestCompareAdapterMethodsPrefersSmallestHeldOutSafeMethod(t *testing.T) {
	t.Parallel()
	baseline := adapterTrial("baseline", MethodDeterministicBaseline, 0, AdapterMetricsV1{Correctness: 0.70, SafetyRetention: 1, FalseDecisionRate: 0.05, LatencyMS: 100, CostMicros: 100})
	candidates := []AdapterTrialV1{
		adapterTrial("prefix", MethodPrefixSoftPrompt, 100, AdapterMetricsV1{Correctness: 0.76, SafetyRetention: 1, FalseDecisionRate: 0.05, LatencyMS: 101, CostMicros: 102}),
		adapterTrial("reft", MethodReFT, 200, AdapterMetricsV1{Correctness: 0.74, SafetyRetention: 1, FalseDecisionRate: 0.05, LatencyMS: 101, CostMicros: 101}),
		adapterTrial("lora", MethodLoRA, 1_000, AdapterMetricsV1{Correctness: 0.82, SafetyRetention: 1, FalseDecisionRate: 0.05, LatencyMS: 110, CostMicros: 120}),
		adapterTrial("qlora", MethodQLoRA, 500, AdapterMetricsV1{Correctness: 0.82, SafetyRetention: 0.95, FalseDecisionRate: 0.05, LatencyMS: 108, CostMicros: 115}),
		adapterTrial("steering", MethodActivationSteering, 50, AdapterMetricsV1{Correctness: 0.90, SafetyRetention: 1, FalseDecisionRate: 0.05, LatencyMS: 100, CostMicros: 100}),
	}
	gate := AdapterGateV1{MinHeldOutTasks: 100, MinCorrectnessImprovement: 0.05, MaxSafetyRegression: 0.01, MaxFalseDecisionIncrease: 0.01, MaxParameterBytes: 2_000}
	report, err := CompareAdapterMethods(baseline, candidates, gate)
	if err != nil {
		t.Fatal(err)
	}
	if report.Selected == nil || report.Selected.ID != "prefix" {
		t.Fatalf("selected = %+v", report.Selected)
	}
	if len(report.Eligible) != 2 || len(report.Rejections) != 3 {
		t.Fatalf("report = %+v", report)
	}
	gate.AllowActivationSteering = true
	report, err = CompareAdapterMethods(baseline, candidates, gate)
	if err != nil {
		t.Fatal(err)
	}
	if report.Selected == nil || report.Selected.ID != "steering" {
		t.Fatalf("opt-in selected = %+v", report.Selected)
	}
}

func TestCompareAdapterMethodsRequiresAllStandardMethodsAndSameSplit(t *testing.T) {
	t.Parallel()
	baseline := adapterTrial("baseline", MethodDeterministicBaseline, 0, AdapterMetricsV1{Correctness: 0.7, SafetyRetention: 1})
	gate := AdapterGateV1{MinHeldOutTasks: 100, MaxParameterBytes: 2_000}
	if _, err := CompareAdapterMethods(baseline, []AdapterTrialV1{adapterTrial("prefix", MethodPrefixSoftPrompt, 1, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1})}, gate); err == nil {
		t.Fatal("accepted comparison that omitted standard methods")
	}
	candidates := []AdapterTrialV1{
		adapterTrial("prefix", MethodPrefixSoftPrompt, 100, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
		adapterTrial("reft", MethodReFT, 100, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
		adapterTrial("lora", MethodLoRA, 100, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
		adapterTrial("qlora", MethodQLoRA, 100, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
	}
	candidates[0].HeldOutSplitHash = "different"
	report, err := CompareAdapterMethods(baseline, candidates, gate)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rejections) != 1 || report.Rejections[0].TrialID != "prefix" {
		t.Fatalf("report = %+v", report)
	}
}

func adapterTrial(id, method string, parameterBytes int64, metrics AdapterMetricsV1) AdapterTrialV1 {
	return AdapterTrialV1{
		Version: 1, ID: id, Method: method, BaseModelDigest: "sha256:model", DatasetID: "dataset", HeldOutSplitHash: "sha256:split", Seed: 602,
		ParameterBytes: parameterBytes, HeldOutTasks: 100, Metrics: metrics,
	}
}
func TestCompareAdapterMethodsRejectsDuplicateIDsAndNonFiniteGates(t *testing.T) {
	t.Parallel()
	baseline := adapterTrial("baseline", MethodDeterministicBaseline, 0, AdapterMetricsV1{Correctness: 0.7, SafetyRetention: 1})
	candidates := []AdapterTrialV1{
		adapterTrial("duplicate", MethodPrefixSoftPrompt, 1, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
		adapterTrial("duplicate", MethodReFT, 1, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
	}
	gate := AdapterGateV1{MinHeldOutTasks: 100, MaxParameterBytes: 2_000}
	if _, err := CompareAdapterMethods(baseline, candidates, gate); err == nil {
		t.Fatal("accepted duplicate adapter trial IDs")
	}
	gate.MinCorrectnessImprovement = math.NaN()
	all := []AdapterTrialV1{
		adapterTrial("prefix", MethodPrefixSoftPrompt, 1, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
		adapterTrial("reft", MethodReFT, 1, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
		adapterTrial("lora", MethodLoRA, 1, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
		adapterTrial("qlora", MethodQLoRA, 1, AdapterMetricsV1{Correctness: 0.8, SafetyRetention: 1}),
	}
	if _, err := CompareAdapterMethods(baseline, all, gate); err == nil {
		t.Fatal("accepted non-finite adapter gate")
	}
}
