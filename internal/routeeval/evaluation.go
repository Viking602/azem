package routeeval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Viking602/azem/internal/session"
)

type CalibrationSliceV1 struct {
	Dimension        string  `json:"dimension"`
	Value            string  `json:"value"`
	Samples          int     `json:"samples"`
	DefiniteSamples  int     `json:"definite_samples"`
	SuccessRate      float64 `json:"success_rate"`
	PredictedMean    float64 `json:"predicted_mean"`
	BrierScore       float64 `json:"brier_score"`
	CalibrationError float64 `json:"calibration_error"`
	FalsePassRate    float64 `json:"false_pass_rate"`
	FalseFailRate    float64 `json:"false_fail_rate"`
	SuccessLower95   float64 `json:"success_lower_95"`
	SuccessUpper95   float64 `json:"success_upper_95"`
}

type FairnessVisibilityV1 struct {
	Dimension       string  `json:"dimension"`
	Groups          int     `json:"groups"`
	MinimumSamples  int     `json:"minimum_samples"`
	MaxSuccessGap   float64 `json:"max_success_gap"`
	MaxFalsePassGap float64 `json:"max_false_pass_gap"`
}

type EvaluationReportV1 struct {
	DatasetID   string               `json:"dataset_id"`
	Seed        int64                `json:"seed"`
	SplitDigest string               `json:"split_digest"`
	Slices      []CalibrationSliceV1 `json:"slices"`
	Fairness    FairnessVisibilityV1 `json:"fairness"`
}

type PromotionDecisionV1 struct {
	Allowed bool     `json:"allowed"`
	Reasons []string `json:"reasons"`
}

func EvaluateCalibration(dataset DatasetV1, seed int64) (EvaluationReportV1, error) {
	if err := dataset.Validate(); err != nil {
		return EvaluationReportV1{}, err
	}
	if seed == 0 {
		return EvaluationReportV1{}, fmt.Errorf("routeeval: fixed non-zero seed is required")
	}
	type groupKey struct {
		dimension string
		value     string
	}
	groups := make(map[groupKey][]session.RouteOutcomeV1)
	heldOutTasks := make(map[string]struct{})
	var splitIdentity []string
	for _, outcome := range dataset.Outcomes {
		splitIdentity = append(splitIdentity, outcome.ID+"\x00"+outcome.TaskID+"\x00"+outcome.Split)
		if outcome.Split != "held_out" {
			continue
		}
		heldOutTasks[outcome.TaskID] = struct{}{}
		groups[groupKey{"task_class", outcome.TaskClass}] = append(groups[groupKey{"task_class", outcome.TaskClass}], outcome)
		providerModel := strings.Join([]string{outcome.Provider, outcome.Model}, "/")
		groups[groupKey{"provider_model", providerModel}] = append(groups[groupKey{"provider_model", providerModel}], outcome)
	}
	if len(groups) == 0 {
		return EvaluationReportV1{}, fmt.Errorf("routeeval: held_out split is empty")
	}
	if len(heldOutTasks) < minimumHeldOutTasksV1 {
		return EvaluationReportV1{}, fmt.Errorf("routeeval: at least %d held-out tasks are required", minimumHeldOutTasksV1)
	}
	sort.Strings(splitIdentity)
	payload, _ := json.Marshal(struct {
		Seed   int64    `json:"seed"`
		Splits []string `json:"splits"`
	}{seed, splitIdentity})
	digest := sha256.Sum256(payload)
	report := EvaluationReportV1{DatasetID: dataset.ID, Seed: seed, SplitDigest: hex.EncodeToString(digest[:])}
	keys := make([]groupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].dimension+"\x00"+keys[i].value < keys[j].dimension+"\x00"+keys[j].value
	})
	for _, key := range keys {
		report.Slices = append(report.Slices, calibrationSlice(key.dimension, key.value, groups[key]))
	}
	report.Fairness = fairnessVisibility(report.Slices)
	return report, nil
}

func calibrationSlice(dimension, value string, outcomes []session.RouteOutcomeV1) CalibrationSliceV1 {
	slice := CalibrationSliceV1{Dimension: dimension, Value: value, Samples: len(outcomes)}
	var successes int
	var bins [10]struct {
		count             int
		predicted, actual float64
	}
	for _, outcome := range outcomes {
		if outcome.FalsePass {
			slice.FalsePassRate++
		}
		if outcome.FalseFail {
			slice.FalseFailRate++
		}
		if outcome.Correctness != "pass" && outcome.Correctness != "fail" {
			continue
		}
		actual := actualSuccess(outcome)
		slice.DefiniteSamples++
		successes += int(actual)
		slice.PredictedMean += outcome.PredictedSuccess
		delta := outcome.PredictedSuccess - actual
		slice.BrierScore += delta * delta
		bin := min(int(outcome.PredictedSuccess*10), 9)
		bins[bin].count++
		bins[bin].predicted += outcome.PredictedSuccess
		bins[bin].actual += actual
	}
	samples := float64(slice.Samples)
	slice.FalsePassRate /= samples
	slice.FalseFailRate /= samples
	if slice.DefiniteSamples == 0 {
		return slice
	}
	definite := float64(slice.DefiniteSamples)
	slice.SuccessRate = float64(successes) / definite
	slice.PredictedMean /= definite
	slice.BrierScore /= definite
	for _, bin := range bins {
		if bin.count == 0 {
			continue
		}
		count := float64(bin.count)
		slice.CalibrationError += count / definite * absolute(bin.predicted/count-bin.actual/count)
	}
	slice.SuccessLower95, slice.SuccessUpper95 = wilson95(successes, slice.DefiniteSamples)
	return slice
}

func fairnessVisibility(slices []CalibrationSliceV1) FairnessVisibilityV1 {
	visibility := FairnessVisibilityV1{Dimension: "provider_model"}
	minimum := int(^uint(0) >> 1)
	var minSuccess, maxSuccess, minFalsePass, maxFalsePass float64
	first := true
	for _, slice := range slices {
		if slice.Dimension != visibility.Dimension {
			continue
		}
		visibility.Groups++
		minimum = min(minimum, slice.Samples)
		if first {
			minSuccess, maxSuccess = slice.SuccessRate, slice.SuccessRate
			minFalsePass, maxFalsePass = slice.FalsePassRate, slice.FalsePassRate
			first = false
			continue
		}
		minSuccess, maxSuccess = min(minSuccess, slice.SuccessRate), max(maxSuccess, slice.SuccessRate)
		minFalsePass, maxFalsePass = min(minFalsePass, slice.FalsePassRate), max(maxFalsePass, slice.FalsePassRate)
	}
	if visibility.Groups > 0 {
		visibility.MinimumSamples = minimum
		visibility.MaxSuccessGap = maxSuccess - minSuccess
		visibility.MaxFalsePassGap = maxFalsePass - minFalsePass
	}
	return visibility
}

func ParetoFront(baselines []BaselineV1) []BaselineV1 {
	front := make([]BaselineV1, 0, len(baselines))
	for index, candidate := range baselines {
		if !finiteBaseline(candidate) {
			continue
		}
		dominated := false
		for otherIndex, other := range baselines {
			if index != otherIndex && finiteBaseline(other) && dominates(other, candidate) {
				dominated = true
				break
			}
		}
		if !dominated {
			front = append(front, candidate)
		}
	}
	sort.Slice(front, func(i, j int) bool { return routeKeyString(front[i].Route) < routeKeyString(front[j].Route) })
	return front
}

func finiteBaseline(baseline BaselineV1) bool {
	for _, value := range []float64{
		baseline.SuccessRate, baseline.CriterionCoverage, baseline.FalsePassRate, baseline.FalseFailRate,
		baseline.MeanLatencyMS, baseline.MeanInputTokens, baseline.MeanOutputTokens, baseline.MeanCacheReadTokens,
		baseline.MeanCostMicros, baseline.MeanRetries, baseline.CancellationRate, baseline.RecoveryRate,
		baseline.SafetyEventRate, baseline.RetentionEventRate, baseline.BrierScore, baseline.CalibrationError,
	} {
		if !finite(value) {
			return false
		}
	}
	return true
}

func CheckPromotion(incumbent, candidate BaselineV1) PromotionDecisionV1 {
	decision := PromotionDecisionV1{}
	if !finiteBaseline(incumbent) || !finiteBaseline(candidate) {
		decision.Reasons = append(decision.Reasons, "non_finite_metrics")
		decision.Allowed = false
		return decision
	}
	if incumbent.Route.TaskClass != candidate.Route.TaskClass || incumbent.Route.Repository != candidate.Route.Repository || incumbent.Route.Toolchain != candidate.Route.Toolchain || incumbent.Route.PolicyVersion != candidate.Route.PolicyVersion {
		decision.Reasons = append(decision.Reasons, "comparison_context_mismatch")
	}
	if candidate.SuccessRate < incumbent.SuccessRate {
		decision.Reasons = append(decision.Reasons, "correctness_regression")
	}
	if candidate.CriterionCoverage < incumbent.CriterionCoverage {
		decision.Reasons = append(decision.Reasons, "criterion_coverage_regression")
	}
	if candidate.SafetyEventRate > incumbent.SafetyEventRate {
		decision.Reasons = append(decision.Reasons, "safety_regression")
	}
	if candidate.RetentionEventRate > incumbent.RetentionEventRate {
		decision.Reasons = append(decision.Reasons, "retention_regression")
	}
	if candidate.CancellationRate > incumbent.CancellationRate {
		decision.Reasons = append(decision.Reasons, "cancellation_regression")
	}
	if candidate.BrierScore > incumbent.BrierScore || candidate.CalibrationError > incumbent.CalibrationError {
		decision.Reasons = append(decision.Reasons, "calibration_regression")
	}
	if candidate.MeanLatencyMS > incumbent.MeanLatencyMS {
		decision.Reasons = append(decision.Reasons, "latency_regression")
	}
	if candidate.MeanCostMicros > incumbent.MeanCostMicros {
		decision.Reasons = append(decision.Reasons, "cost_regression")
	}
	incumbentFalse := incumbent.FalsePassRate + incumbent.FalseFailRate
	candidateFalse := candidate.FalsePassRate + candidate.FalseFailRate
	if candidateFalse > incumbentFalse {
		decision.Reasons = append(decision.Reasons, "false_decision_regression")
	}
	if candidateFalse >= incumbentFalse && candidate.MeanCostMicros >= incumbent.MeanCostMicros && candidate.MeanLatencyMS >= incumbent.MeanLatencyMS {
		decision.Reasons = append(decision.Reasons, "no_false_decision_or_cost_improvement")
	}
	decision.Allowed = len(decision.Reasons) == 0
	return decision
}

func dominates(left, right BaselineV1) bool {
	leftTokens := max(0, left.MeanInputTokens-left.MeanCacheReadTokens) + left.MeanOutputTokens
	rightTokens := max(0, right.MeanInputTokens-right.MeanCacheReadTokens) + right.MeanOutputTokens
	noWorse := left.SuccessRate >= right.SuccessRate && left.CriterionCoverage >= right.CriterionCoverage &&
		left.FalsePassRate+left.FalseFailRate <= right.FalsePassRate+right.FalseFailRate && left.MeanLatencyMS <= right.MeanLatencyMS &&
		left.MeanCostMicros <= right.MeanCostMicros && leftTokens <= rightTokens && left.MeanCacheReadTokens >= right.MeanCacheReadTokens &&
		left.SafetyEventRate <= right.SafetyEventRate && left.RetentionEventRate <= right.RetentionEventRate
	if !noWorse {
		return false
	}
	return left.SuccessRate > right.SuccessRate || left.CriterionCoverage > right.CriterionCoverage ||
		left.FalsePassRate+left.FalseFailRate < right.FalsePassRate+right.FalseFailRate || left.MeanLatencyMS < right.MeanLatencyMS ||
		left.MeanCostMicros < right.MeanCostMicros || leftTokens < rightTokens || left.MeanCacheReadTokens > right.MeanCacheReadTokens ||
		left.SafetyEventRate < right.SafetyEventRate || left.RetentionEventRate < right.RetentionEventRate
}

func wilson95(successes, total int) (float64, float64) {
	if total == 0 {
		return 0, 0
	}
	const z = 1.959963984540054
	n := float64(total)
	p := float64(successes) / n
	denominator := 1 + z*z/n
	center := (p + z*z/(2*n)) / denominator
	margin := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / denominator
	return max(0, center-margin), min(1, center+margin)
}
