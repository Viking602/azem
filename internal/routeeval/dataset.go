// Package routeeval evaluates route choices offline. It has no live routing API.
package routeeval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const DatasetVersionV1 = 1

type DatasetV1 struct {
	Version   int                      `json:"version"`
	ID        string                   `json:"id"`
	Outcomes  []session.RouteOutcomeV1 `json:"outcomes"`
	CreatedAt time.Time                `json:"created_at"`
}

type RouteKeyV1 struct {
	TaskClass     string `json:"task_class"`
	Repository    string `json:"repository"`
	Toolchain     string `json:"toolchain"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Budget        string `json:"budget"`
	PolicyVersion string `json:"policy_version"`
}

type BaselineV1 struct {
	Route               RouteKeyV1 `json:"route"`
	Samples             int        `json:"samples"`
	DefiniteSamples     int        `json:"definite_samples"`
	SuccessRate         float64    `json:"success_rate"`
	CriterionCoverage   float64    `json:"criterion_coverage"`
	FalsePassRate       float64    `json:"false_pass_rate"`
	FalseFailRate       float64    `json:"false_fail_rate"`
	MeanLatencyMS       float64    `json:"mean_latency_ms"`
	P50LatencyMS        int64      `json:"p50_latency_ms"`
	MeanInputTokens     float64    `json:"mean_input_tokens"`
	MeanOutputTokens    float64    `json:"mean_output_tokens"`
	MeanCacheReadTokens float64    `json:"mean_cache_read_tokens"`
	MeanCostMicros      float64    `json:"mean_cost_micros"`
	MeanRetries         float64    `json:"mean_retries"`
	CancellationRate    float64    `json:"cancellation_rate"`
	RecoveryRate        float64    `json:"recovery_rate"`
	SafetyEventRate     float64    `json:"safety_event_rate"`
	RetentionEventRate  float64    `json:"retention_event_rate"`
	BrierScore          float64    `json:"brier_score"`
	CalibrationError    float64    `json:"calibration_error"`
}

func NewDataset(outcomes []session.RouteOutcomeV1, createdAt time.Time) (DatasetV1, error) {
	copyOutcomes := append([]session.RouteOutcomeV1(nil), outcomes...)
	sort.Slice(copyOutcomes, func(i, j int) bool { return copyOutcomes[i].ID < copyOutcomes[j].ID })
	dataset := DatasetV1{Version: DatasetVersionV1, Outcomes: copyOutcomes, CreatedAt: createdAt.UTC()}
	dataset.ID = datasetIdentity(copyOutcomes)
	return dataset, dataset.Validate()
}

func (dataset DatasetV1) Validate() error {
	if dataset.Version != DatasetVersionV1 || dataset.ID == "" || len(dataset.Outcomes) == 0 || dataset.CreatedAt.IsZero() {
		return fmt.Errorf("routeeval: invalid dataset")
	}
	if expected := datasetIdentity(dataset.Outcomes); expected != dataset.ID {
		return fmt.Errorf("routeeval: dataset identity mismatch")
	}
	seen := make(map[string]struct{}, len(dataset.Outcomes))
	for _, outcome := range dataset.Outcomes {
		if err := outcome.Validate(); err != nil {
			return err
		}
		if !finite(outcome.CriterionCoverage) || !finite(outcome.PredictedSuccess) {
			return fmt.Errorf("routeeval: outcome %q has non-finite metrics", outcome.ID)
		}
		if outcome.Status == "pass" && (outcome.InputTokens <= 0 || outcome.OutputTokens <= 0 || outcome.CostMicros <= 0 || outcome.LatencyMS <= 0) {
			return fmt.Errorf("routeeval: outcome %q lacks positive pass accounting", outcome.ID)
		}
		for _, ref := range outcome.Evidence {
			if ref.Kind == "" || ref.ID == "" {
				return fmt.Errorf("routeeval: outcome %q has invalid evidence", outcome.ID)
			}
		}
		if outcome.TaskID == "" || outcome.TaskClass == "" || outcome.Repository == "" || outcome.Toolchain == "" || outcome.PolicyVersion == "" || outcome.Budget == "" || outcome.Split == "" || outcome.Correctness == "" || len(outcome.Evidence) == 0 || outcome.CompletedAt.IsZero() {
			return fmt.Errorf("routeeval: outcome %q lacks calibration identity or evidence", outcome.ID)
		}
		if _, exists := seen[outcome.ID]; exists {
			return fmt.Errorf("routeeval: duplicate outcome %q", outcome.ID)
		}
		seen[outcome.ID] = struct{}{}
	}
	return nil
}

func Aggregate(dataset DatasetV1) ([]BaselineV1, error) {
	if err := dataset.Validate(); err != nil {
		return nil, err
	}
	type accumulator struct {
		baseline        BaselineV1
		latencies       []int64
		successes       int
		falsePasses     int
		falseFails      int
		cancelled       int
		recovered       int
		safetyEvents    int
		retentionEvents int
		brier           float64
		calibrationBin  [10]struct {
			count              int
			prediction, actual float64
		}
	}
	groups := make(map[RouteKeyV1]*accumulator)
	for _, outcome := range dataset.Outcomes {
		key := RouteKeyV1{TaskClass: outcome.TaskClass, Repository: outcome.Repository, Toolchain: outcome.Toolchain, Provider: outcome.Provider, Model: outcome.Model, Budget: outcome.Budget, PolicyVersion: outcome.PolicyVersion}
		group := groups[key]
		if group == nil {
			group = &accumulator{baseline: BaselineV1{Route: key}}
			groups[key] = group
		}
		baseline := &group.baseline
		baseline.Samples++
		baseline.CriterionCoverage += outcome.CriterionCoverage
		baseline.MeanLatencyMS += float64(outcome.LatencyMS)
		baseline.MeanInputTokens += float64(outcome.InputTokens)
		baseline.MeanOutputTokens += float64(outcome.OutputTokens)
		baseline.MeanCacheReadTokens += float64(outcome.CacheReadTokens)
		baseline.MeanCostMicros += float64(outcome.CostMicros)
		baseline.MeanRetries += float64(outcome.Retries)
		group.latencies = append(group.latencies, outcome.LatencyMS)
		if outcome.FalsePass {
			group.falsePasses++
		}
		if outcome.FalseFail {
			group.falseFails++
		}
		if outcome.Status == "cancelled" {
			group.cancelled++
		}
		if outcome.Recovered {
			group.recovered++
		}
		if len(outcome.SafetyEvents) > 0 {
			group.safetyEvents++
		}
		if len(outcome.RetentionEvents) > 0 {
			group.retentionEvents++
		}
		if outcome.Correctness == "pass" || outcome.Correctness == "fail" {
			actual := 0.0
			if outcome.Correctness == "pass" {
				actual = 1
				group.successes++
			}
			baseline.DefiniteSamples++
			delta := outcome.PredictedSuccess - actual
			group.brier += delta * delta
			bin := min(int(outcome.PredictedSuccess*10), 9)
			group.calibrationBin[bin].count++
			group.calibrationBin[bin].prediction += outcome.PredictedSuccess
			group.calibrationBin[bin].actual += actual
		}
	}
	result := make([]BaselineV1, 0, len(groups))
	for _, group := range groups {
		baseline := group.baseline
		samples := float64(baseline.Samples)
		baseline.CriterionCoverage /= samples
		baseline.FalsePassRate = float64(group.falsePasses) / samples
		baseline.FalseFailRate = float64(group.falseFails) / samples
		baseline.MeanLatencyMS /= samples
		baseline.MeanInputTokens /= samples
		baseline.MeanOutputTokens /= samples
		baseline.MeanCacheReadTokens /= samples
		baseline.MeanCostMicros /= samples
		baseline.MeanRetries /= samples
		baseline.CancellationRate = float64(group.cancelled) / samples
		baseline.RecoveryRate = float64(group.recovered) / samples
		baseline.SafetyEventRate = float64(group.safetyEvents) / samples
		baseline.RetentionEventRate = float64(group.retentionEvents) / samples
		sort.Slice(group.latencies, func(i, j int) bool { return group.latencies[i] < group.latencies[j] })
		baseline.P50LatencyMS = group.latencies[(len(group.latencies)-1)/2]
		if baseline.DefiniteSamples > 0 {
			definite := float64(baseline.DefiniteSamples)
			baseline.SuccessRate = float64(group.successes) / definite
			baseline.BrierScore = group.brier / definite
			for _, bin := range group.calibrationBin {
				if bin.count == 0 {
					continue
				}
				count := float64(bin.count)
				baseline.CalibrationError += count / definite * absolute(bin.prediction/count-bin.actual/count)
			}
		}
		result = append(result, baseline)
	}
	sort.Slice(result, func(i, j int) bool { return routeKeyString(result[i].Route) < routeKeyString(result[j].Route) })
	return result, nil
}

func EncodeDataset(dataset DatasetV1) ([]byte, error) {
	if err := dataset.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(dataset, "", "  ")
}

func DecodeDataset(payload []byte) (DatasetV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var dataset DatasetV1
	if err := decoder.Decode(&dataset); err != nil {
		return DatasetV1{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return DatasetV1{}, fmt.Errorf("routeeval: trailing JSON")
	}
	return dataset, dataset.Validate()
}

func datasetIdentity(outcomes []session.RouteOutcomeV1) string {
	copyOutcomes := append([]session.RouteOutcomeV1(nil), outcomes...)
	sort.Slice(copyOutcomes, func(i, j int) bool { return copyOutcomes[i].ID < copyOutcomes[j].ID })
	identity, _ := json.Marshal(copyOutcomes)
	digest := sha256.Sum256(identity)
	return "route-outcomes:" + hex.EncodeToString(digest[:12])
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func routeKeyString(key RouteKeyV1) string {
	payload, _ := json.Marshal(key)
	return string(payload)
}

func absolute(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
