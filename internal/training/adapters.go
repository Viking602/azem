package training

import (
	"fmt"
	"math"
	"sort"
)

const (
	MethodDeterministicBaseline = "deterministic_baseline"
	MethodPrefixSoftPrompt      = "prefix_soft_prompt"
	MethodReFT                  = "reft"
	MethodLoRA                  = "lora"
	MethodQLoRA                 = "qlora"
	MethodActivationSteering    = "activation_steering"
)

type AdapterMetricsV1 struct {
	Correctness       float64 `json:"correctness"`
	SafetyRetention   float64 `json:"safety_retention"`
	FalseDecisionRate float64 `json:"false_decision_rate"`
	LatencyMS         float64 `json:"latency_ms"`
	CostMicros        int64   `json:"cost_micros"`
}

type AdapterTrialV1 struct {
	Version          int              `json:"version"`
	ID               string           `json:"id"`
	Method           string           `json:"method"`
	BaseModelDigest  string           `json:"base_model_digest"`
	DatasetID        string           `json:"dataset_id"`
	HeldOutSplitHash string           `json:"held_out_split_hash"`
	Seed             int64            `json:"seed"`
	ParameterBytes   int64            `json:"parameter_bytes"`
	HeldOutTasks     int              `json:"held_out_tasks"`
	Metrics          AdapterMetricsV1 `json:"metrics"`
}

type AdapterGateV1 struct {
	MinHeldOutTasks           int     `json:"min_held_out_tasks"`
	MinCorrectnessImprovement float64 `json:"min_correctness_improvement"`
	MaxSafetyRegression       float64 `json:"max_safety_regression"`
	MaxFalseDecisionIncrease  float64 `json:"max_false_decision_increase"`
	MaxParameterBytes         int64   `json:"max_parameter_bytes"`
	AllowActivationSteering   bool    `json:"allow_activation_steering"`
}

type AdapterRejectionV1 struct {
	TrialID string `json:"trial_id"`
	Reason  string `json:"reason"`
}

type AdapterComparisonV1 struct {
	Version    int                  `json:"version"`
	BaselineID string               `json:"baseline_id"`
	Selected   *AdapterTrialV1      `json:"selected,omitempty"`
	Eligible   []string             `json:"eligible"`
	Rejections []AdapterRejectionV1 `json:"rejections"`
}

func CompareAdapterMethods(baseline AdapterTrialV1, candidates []AdapterTrialV1, gate AdapterGateV1) (AdapterComparisonV1, error) {
	if err := validateAdapterTrial(baseline); err != nil {
		return AdapterComparisonV1{}, err
	}
	if baseline.Method != MethodDeterministicBaseline || baseline.ParameterBytes != 0 || gate.MinHeldOutTasks < 2 || !finite(gate.MinCorrectnessImprovement) || !finite(gate.MaxSafetyRegression) || !finite(gate.MaxFalseDecisionIncrease) || gate.MinCorrectnessImprovement < 0 || gate.MaxSafetyRegression < 0 || gate.MaxFalseDecisionIncrease < 0 || gate.MaxParameterBytes <= 0 {
		return AdapterComparisonV1{}, fmt.Errorf("training: invalid adapter comparison baseline or gate")
	}
	present := make(map[string]bool)
	seenIDs := map[string]bool{baseline.ID: true}
	for _, candidate := range candidates {
		if candidate.ID != "" && seenIDs[candidate.ID] {
			return AdapterComparisonV1{}, fmt.Errorf("training: duplicate adapter trial id %q", candidate.ID)
		}
		if candidate.ID != "" {
			seenIDs[candidate.ID] = true
		}
		present[candidate.Method] = true
	}
	for _, method := range []string{MethodPrefixSoftPrompt, MethodReFT, MethodLoRA, MethodQLoRA} {
		if !present[method] {
			return AdapterComparisonV1{}, fmt.Errorf("training: adapter comparison omits %s", method)
		}
	}
	report := AdapterComparisonV1{Version: 1, BaselineID: baseline.ID}
	eligible := make([]AdapterTrialV1, 0, len(candidates))
	for _, candidate := range candidates {
		reason := adapterRejectionReason(baseline, candidate, gate, present)
		if reason != "" {
			report.Rejections = append(report.Rejections, AdapterRejectionV1{TrialID: candidate.ID, Reason: reason})
			continue
		}
		eligible = append(eligible, candidate)
		report.Eligible = append(report.Eligible, candidate.ID)
	}
	sort.Slice(report.Rejections, func(i, j int) bool { return report.Rejections[i].TrialID < report.Rejections[j].TrialID })
	sort.Strings(report.Eligible)
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].ParameterBytes != eligible[j].ParameterBytes {
			return eligible[i].ParameterBytes < eligible[j].ParameterBytes
		}
		if eligible[i].Metrics.CostMicros != eligible[j].Metrics.CostMicros {
			return eligible[i].Metrics.CostMicros < eligible[j].Metrics.CostMicros
		}
		return eligible[i].ID < eligible[j].ID
	})
	if len(eligible) > 0 {
		selected := eligible[0]
		report.Selected = &selected
	}
	return report, nil
}

func adapterRejectionReason(baseline, candidate AdapterTrialV1, gate AdapterGateV1, present map[string]bool) string {
	if err := validateAdapterTrial(candidate); err != nil {
		return err.Error()
	}
	if candidate.Method == MethodDeterministicBaseline {
		return "candidate repeats deterministic baseline"
	}
	if candidate.BaseModelDigest != baseline.BaseModelDigest || candidate.DatasetID != baseline.DatasetID || candidate.HeldOutSplitHash != baseline.HeldOutSplitHash || candidate.Seed != baseline.Seed {
		return "identity, split, or seed differs from baseline"
	}
	if candidate.HeldOutTasks < gate.MinHeldOutTasks || candidate.HeldOutTasks != baseline.HeldOutTasks {
		return "held-out coverage is insufficient or unequal"
	}
	if candidate.ParameterBytes > gate.MaxParameterBytes {
		return "adapter exceeds parameter budget"
	}
	if candidate.Method == MethodActivationSteering {
		if !gate.AllowActivationSteering {
			return "activation steering is disabled"
		}
		for _, prerequisite := range []string{MethodPrefixSoftPrompt, MethodReFT, MethodLoRA, MethodQLoRA} {
			if !present[prerequisite] {
				return "activation steering evaluated before standard adapter methods"
			}
		}
	}
	if candidate.Metrics.Correctness < baseline.Metrics.Correctness+gate.MinCorrectnessImprovement {
		return "held-out correctness improvement is below gate"
	}
	if candidate.Metrics.SafetyRetention < baseline.Metrics.SafetyRetention-gate.MaxSafetyRegression {
		return "safety retention regressed"
	}
	if candidate.Metrics.FalseDecisionRate > baseline.Metrics.FalseDecisionRate+gate.MaxFalseDecisionIncrease {
		return "false decisions increased"
	}
	return ""
}

func validateAdapterTrial(trial AdapterTrialV1) error {
	if trial.Version != 1 || trial.ID == "" || !adapterMethod(trial.Method) || trial.BaseModelDigest == "" || trial.DatasetID == "" || trial.HeldOutSplitHash == "" || trial.Seed == 0 || trial.ParameterBytes < 0 || trial.HeldOutTasks <= 0 || trial.Metrics.CostMicros < 0 {
		return fmt.Errorf("training: invalid adapter trial %q", trial.ID)
	}
	for _, value := range []float64{trial.Metrics.Correctness, trial.Metrics.SafetyRetention, trial.Metrics.FalseDecisionRate} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("training: invalid adapter metrics on %q", trial.ID)
		}
	}
	if math.IsNaN(trial.Metrics.LatencyMS) || math.IsInf(trial.Metrics.LatencyMS, 0) || trial.Metrics.LatencyMS < 0 {
		return fmt.Errorf("training: invalid adapter latency on %q", trial.ID)
	}
	return nil
}
func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func adapterMethod(method string) bool {
	switch method {
	case MethodDeterministicBaseline, MethodPrefixSoftPrompt, MethodReFT, MethodLoRA, MethodQLoRA, MethodActivationSteering:
		return true
	default:
		return false
	}
}
