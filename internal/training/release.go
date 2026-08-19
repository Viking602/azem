package training

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Viking602/azem/internal/adapterdeployment"
	"github.com/Viking602/azem/internal/session"
)

type CostLineItemV1 struct {
	Category   string `json:"category"`
	CostMicros int64  `json:"cost_micros"`
}

type ReleaseSampleV1 struct {
	DatasetKind   string                 `json:"dataset_kind"`
	ContentSHA256 string                 `json:"content_sha256"`
	Outcome       session.RouteOutcomeV1 `json:"outcome"`
}

type ReleaseGateV1 struct {
	MinHeldOutTasks           int     `json:"min_held_out_tasks"`
	MinCorrectnessImprovement float64 `json:"min_correctness_improvement"`
	MaxSliceRegression        float64 `json:"max_slice_regression"`
	MaxServingCostIncrease    float64 `json:"max_serving_cost_increase"`
	MaxServingLatencyIncrease float64 `json:"max_serving_latency_increase"`
	MaxTrainServeCostMicros   int64   `json:"max_train_serve_cost_micros"`
}

type ReleaseEvaluationInputV1 struct {
	TrainWorkSpecIDs                   []string          `json:"train_work_spec_ids"`
	TrainRepositories                  []string          `json:"train_repositories"`
	TrainContentSHA256                 []string          `json:"train_content_sha256"`
	TrainingCosts                      []CostLineItemV1  `json:"training_costs"`
	DeclaredTrainingCostMicros         int64             `json:"declared_training_cost_micros"`
	Baseline                           []ReleaseSampleV1 `json:"baseline"`
	Candidate                          []ReleaseSampleV1 `json:"candidate"`
	DeclaredBaselineServingCostMicros  int64             `json:"declared_baseline_serving_cost_micros"`
	DeclaredCandidateServingCostMicros int64             `json:"declared_candidate_serving_cost_micros"`
}

type ReleaseSliceV1 struct {
	Arm                string  `json:"arm"`
	Dimension          string  `json:"dimension"`
	Value              string  `json:"value"`
	Count              int     `json:"count"`
	CorrectnessRate    float64 `json:"correctness_rate"`
	SafetyEventRate    float64 `json:"safety_event_rate"`
	RetentionEventRate float64 `json:"retention_event_rate"`
}

type HeldOutReleaseReportV1 struct {
	Version                     int              `json:"version"`
	Ship                        bool             `json:"ship"`
	Reasons                     []string         `json:"reasons,omitempty"`
	HeldOutTasks                int              `json:"held_out_tasks"`
	RealTasks                   int              `json:"real_tasks"`
	BaselineCorrectness         float64          `json:"baseline_correctness"`
	CandidateCorrectness        float64          `json:"candidate_correctness"`
	RealBaselineCorrectness     float64          `json:"real_baseline_correctness"`
	RealCandidateCorrectness    float64          `json:"real_candidate_correctness"`
	BaselineSafetyEventRate     float64          `json:"baseline_safety_event_rate"`
	CandidateSafetyEventRate    float64          `json:"candidate_safety_event_rate"`
	BaselineRetentionEventRate  float64          `json:"baseline_retention_event_rate"`
	CandidateRetentionEventRate float64          `json:"candidate_retention_event_rate"`
	BaselineFalsePassRate       float64          `json:"baseline_false_pass_rate"`
	CandidateFalsePassRate      float64          `json:"candidate_false_pass_rate"`
	BaselineMeanLatencyMS       float64          `json:"baseline_mean_latency_ms"`
	CandidateMeanLatencyMS      float64          `json:"candidate_mean_latency_ms"`
	TrainingCostMicros          int64            `json:"training_cost_micros"`
	BaselineServingCostMicros   int64            `json:"baseline_serving_cost_micros"`
	CandidateServingCostMicros  int64            `json:"candidate_serving_cost_micros"`
	TrainServeCostMicros        int64            `json:"train_serve_cost_micros"`
	ContaminationFound          bool             `json:"contamination_found"`
	CostAccountingComplete      bool             `json:"cost_accounting_complete"`
	Slices                      []ReleaseSliceV1 `json:"slices"`
	Digest                      string           `json:"digest"`
}

func EvaluateHeldOutRelease(input ReleaseEvaluationInputV1, gate ReleaseGateV1) (HeldOutReleaseReportV1, error) {
	if gate.MinHeldOutTasks < 2 || !finite(gate.MinCorrectnessImprovement) || !finite(gate.MaxSliceRegression) || !finite(gate.MaxServingCostIncrease) || !finite(gate.MaxServingLatencyIncrease) || gate.MinCorrectnessImprovement < 0 || gate.MaxSliceRegression < 0 || gate.MaxServingCostIncrease < 0 || gate.MaxServingLatencyIncrease < 0 || gate.MaxTrainServeCostMicros <= 0 {
		return HeldOutReleaseReportV1{}, fmt.Errorf("training: invalid held-out release gate")
	}
	baseline, err := indexReleaseSamples(input.Baseline)
	if err != nil {
		return HeldOutReleaseReportV1{}, err
	}
	candidate, err := indexReleaseSamples(input.Candidate)
	if err != nil {
		return HeldOutReleaseReportV1{}, err
	}
	if len(baseline) != len(candidate) || len(candidate) == 0 {
		return HeldOutReleaseReportV1{}, fmt.Errorf("training: held-out arms have unequal or empty tasks")
	}
	if !validTrainingIdentities(input.TrainWorkSpecIDs, input.TrainRepositories, input.TrainContentSHA256) {
		return HeldOutReleaseReportV1{}, fmt.Errorf("training: complete training identities are required for contamination checks")
	}
	report := HeldOutReleaseReportV1{Version: 1, HeldOutTasks: len(candidate)}
	trainTasks := stringSet(input.TrainWorkSpecIDs)
	trainRepositories := stringSet(input.TrainRepositories)
	trainContent := normalizedStringSet(input.TrainContentSHA256)
	for key, candidateSample := range candidate {
		baselineSample, exists := baseline[key]
		if !exists || baselineSample.DatasetKind != candidateSample.DatasetKind || !strings.EqualFold(baselineSample.ContentSHA256, candidateSample.ContentSHA256) {
			return HeldOutReleaseReportV1{}, fmt.Errorf("training: held-out task %q is not paired across arms", key)
		}
		if trainTasks[candidateSample.Outcome.WorkSpecID] || trainRepositories[candidateSample.Outcome.Repository] || trainContent[strings.ToLower(candidateSample.ContentSHA256)] {
			report.ContaminationFound = true
		}
	}
	report.BaselineCorrectness, report.BaselineSafetyEventRate, report.BaselineRetentionEventRate, report.BaselineFalsePassRate = aggregateRelease(input.Baseline, "")
	report.CandidateCorrectness, report.CandidateSafetyEventRate, report.CandidateRetentionEventRate, report.CandidateFalsePassRate = aggregateRelease(input.Candidate, "")
	report.RealBaselineCorrectness, _, _, _ = aggregateRelease(input.Baseline, "real")
	report.RealCandidateCorrectness, _, _, _ = aggregateRelease(input.Candidate, "real")
	report.BaselineMeanLatencyMS = aggregateLatency(input.Baseline, "")
	report.CandidateMeanLatencyMS = aggregateLatency(input.Candidate, "")
	for _, sample := range input.Candidate {
		if sample.DatasetKind == "real" {
			report.RealTasks++
		}
	}
	report.Slices = append(releaseSlices("baseline", input.Baseline), releaseSlices("candidate", input.Candidate)...)
	sort.Slice(report.Slices, func(i, j int) bool {
		if report.Slices[i].Arm != report.Slices[j].Arm {
			return report.Slices[i].Arm < report.Slices[j].Arm
		}
		if report.Slices[i].Dimension != report.Slices[j].Dimension {
			return report.Slices[i].Dimension < report.Slices[j].Dimension
		}
		return report.Slices[i].Value < report.Slices[j].Value
	})
	report.TrainingCostMicros, report.CostAccountingComplete = completeTrainingCosts(input.TrainingCosts, input.DeclaredTrainingCostMicros)
	report.BaselineServingCostMicros, report.CostAccountingComplete = completeServingCosts(input.Baseline, input.DeclaredBaselineServingCostMicros, report.CostAccountingComplete)
	report.CandidateServingCostMicros, report.CostAccountingComplete = completeServingCosts(input.Candidate, input.DeclaredCandidateServingCostMicros, report.CostAccountingComplete)
	if total, ok := safeAdd(report.TrainingCostMicros, report.CandidateServingCostMicros); ok {
		report.TrainServeCostMicros = total
	} else {
		report.CostAccountingComplete = false
	}
	if !finiteReleaseReport(report) {
		return HeldOutReleaseReportV1{}, fmt.Errorf("training: non-finite held-out report metric")
	}
	if report.HeldOutTasks < gate.MinHeldOutTasks {
		report.Reasons = append(report.Reasons, "insufficient held-out tasks")
	}
	if report.ContaminationFound {
		report.Reasons = append(report.Reasons, "train/held-out contamination detected")
	}
	if report.CandidateCorrectness < report.BaselineCorrectness+gate.MinCorrectnessImprovement {
		report.Reasons = append(report.Reasons, "held-out correctness improvement is below gate")
	}
	if report.RealTasks == 0 || report.RealCandidateCorrectness <= report.RealBaselineCorrectness {
		report.Reasons = append(report.Reasons, "improvement is synthetic-only")
	}
	if report.CandidateSafetyEventRate > report.BaselineSafetyEventRate {
		report.Reasons = append(report.Reasons, "safety regressed")
	}
	if report.CandidateRetentionEventRate > report.BaselineRetentionEventRate {
		report.Reasons = append(report.Reasons, "retention regressed")
	}
	if report.CandidateFalsePassRate > report.BaselineFalsePassRate {
		report.Reasons = append(report.Reasons, "false decisions increased")
	}
	for _, reason := range sliceRegressionReasons(input.Baseline, input.Candidate, gate.MaxSliceRegression) {
		report.Reasons = append(report.Reasons, reason)
	}
	if !report.CostAccountingComplete {
		report.Reasons = append(report.Reasons, "train and serving cost accounting is incomplete")
	}
	if report.TrainServeCostMicros > gate.MaxTrainServeCostMicros {
		report.Reasons = append(report.Reasons, "train and serving cost exceeds budget")
	}
	if report.BaselineServingCostMicros > 0 && float64(report.CandidateServingCostMicros)/float64(report.BaselineServingCostMicros)-1 > gate.MaxServingCostIncrease {
		report.Reasons = append(report.Reasons, "serving cost increase exceeds gate")
	}
	if report.BaselineMeanLatencyMS > 0 && float64(report.CandidateMeanLatencyMS)/report.BaselineMeanLatencyMS-1 > gate.MaxServingLatencyIncrease {
		report.Reasons = append(report.Reasons, "serving latency increase exceeds gate")
	}
	report.Reasons = uniqueSorted(report.Reasons)
	report.Ship = len(report.Reasons) == 0
	report.Digest = releaseReportDigest(report)
	return report, nil
}

func AdapterValidationReport(report HeldOutReleaseReportV1, evidence []session.SourceRefV1) (adapterdeployment.ValidationReportV1, error) {
	if !finiteReleaseReport(report) || !report.Ship || report.Digest == "" || report.Digest != releaseReportDigest(report) || len(evidence) == 0 {
		return adapterdeployment.ValidationReportV1{}, fmt.Errorf("training: held-out report is not deployable")
	}
	for _, ref := range evidence {
		if ref.Kind == "" || ref.ID == "" {
			return adapterdeployment.ValidationReportV1{}, fmt.Errorf("training: invalid held-out report evidence")
		}
	}
	return adapterdeployment.ValidationReportV1{
		Status: "pass", HeldOutTasks: report.HeldOutTasks,
		CorrectnessDelta:     report.CandidateCorrectness - report.BaselineCorrectness,
		SafetyRetention:      1 - report.CandidateSafetyEventRate,
		FalseDecisionDelta:   report.CandidateFalsePassRate - report.BaselineFalsePassRate,
		TrainServeCostMicros: report.TrainServeCostMicros, Digest: report.Digest,
		Evidence: append([]session.SourceRefV1(nil), evidence...),
	}, nil
}

func indexReleaseSamples(samples []ReleaseSampleV1) (map[string]ReleaseSampleV1, error) {
	result := make(map[string]ReleaseSampleV1, len(samples))
	seenWorkSpecs := make(map[string]bool, len(samples))
	for _, sample := range samples {
		if sample.DatasetKind != "real" && sample.DatasetKind != "synthetic" {
			return nil, fmt.Errorf("training: invalid dataset kind")
		}
		if !validSHA256(sample.ContentSHA256) || !finite(sample.Outcome.CriterionCoverage) || !finite(sample.Outcome.PredictedSuccess) || sample.Outcome.Split != "held_out" || sample.Outcome.WorkSpecID == "" || sample.Outcome.TaskID == "" || sample.Outcome.TaskClass == "" || sample.Outcome.Repository == "" || sample.Outcome.Correctness == "" || sample.Outcome.Status == "cancelled" {
			return nil, fmt.Errorf("training: incomplete held-out sample")
		}
		for _, ref := range sample.Outcome.Evidence {
			if ref.Kind == "" || ref.ID == "" {
				return nil, fmt.Errorf("training: held-out sample has invalid evidence")
			}
		}
		if len(sample.Outcome.Evidence) == 0 {
			return nil, fmt.Errorf("training: held-out sample lacks evidence")
		}
		if seenWorkSpecs[sample.Outcome.WorkSpecID] {
			return nil, fmt.Errorf("training: duplicate held-out work spec %q", sample.Outcome.WorkSpecID)
		}
		seenWorkSpecs[sample.Outcome.WorkSpecID] = true
		key := releaseSampleKey(sample)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("training: duplicate held-out task %q", key)
		}
		result[key] = sample
	}
	return result, nil
}

func releaseSampleKey(sample ReleaseSampleV1) string {
	return strings.Join([]string{sample.Outcome.WorkSpecID, sample.Outcome.TaskID, sample.Outcome.Repository, sample.Outcome.TaskClass}, "\x00")
}

func aggregateRelease(samples []ReleaseSampleV1, datasetKind string) (correctness, safety, retention, falsePass float64) {
	var count, correct, safetyEvents, retentionEvents, falsePasses int
	for _, sample := range samples {
		if datasetKind != "" && sample.DatasetKind != datasetKind {
			continue
		}
		count++
		if sample.Outcome.Correctness == "pass" {
			correct++
		}
		if len(sample.Outcome.SafetyEvents) > 0 {
			safetyEvents++
		}
		if len(sample.Outcome.RetentionEvents) > 0 {
			retentionEvents++
		}
		if sample.Outcome.FalsePass {
			falsePasses++
		}
	}
	if count == 0 {
		return 0, 0, 0, 0
	}
	return float64(correct) / float64(count), float64(safetyEvents) / float64(count), float64(retentionEvents) / float64(count), float64(falsePasses) / float64(count)
}
func aggregateLatency(samples []ReleaseSampleV1, datasetKind string) float64 {
	var total int64
	var count int
	for _, sample := range samples {
		if datasetKind != "" && sample.DatasetKind != datasetKind {
			continue
		}
		if sample.Outcome.LatencyMS < 0 {
			return math.NaN()
		}
		if total > int64(^uint64(0)>>1)-sample.Outcome.LatencyMS {
			return math.Inf(1)
		}
		total += sample.Outcome.LatencyMS
		count++
	}
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}

func releaseSlices(arm string, samples []ReleaseSampleV1) []ReleaseSliceV1 {
	type group struct{ samples []ReleaseSampleV1 }
	groups := make(map[string]*group)
	for _, sample := range samples {
		values := map[string]string{
			"task": sample.Outcome.TaskClass, "project": sample.Outcome.Repository,
			"model": sample.Outcome.Model, "provider": sample.Outcome.Provider,
		}
		for dimension, value := range values {
			key := dimension + "\x00" + value
			if groups[key] == nil {
				groups[key] = &group{}
			}
			groups[key].samples = append(groups[key].samples, sample)
		}
	}
	result := make([]ReleaseSliceV1, 0, len(groups))
	for key, grouped := range groups {
		separator := 0
		for separator < len(key) && key[separator] != 0 {
			separator++
		}
		correctness, safety, retention, _ := aggregateRelease(grouped.samples, "")
		result = append(result, ReleaseSliceV1{Arm: arm, Dimension: key[:separator], Value: key[separator+1:], Count: len(grouped.samples), CorrectnessRate: correctness, SafetyEventRate: safety, RetentionEventRate: retention})
	}
	return result
}

func sliceRegressionReasons(baseline, candidate []ReleaseSampleV1, maxRegression float64) []string {
	baselineRates := comparableSliceRates(baseline)
	candidateRates := comparableSliceRates(candidate)
	var reasons []string
	for key, baselineRate := range baselineRates {
		candidateRate, exists := candidateRates[key]
		if exists && candidateRate+maxRegression < baselineRate {
			reasons = append(reasons, "correctness regressed for "+key)
		}
	}
	return reasons
}

func comparableSliceRates(samples []ReleaseSampleV1) map[string]float64 {
	groups := make(map[string][]ReleaseSampleV1)
	for _, sample := range samples {
		for dimension, value := range map[string]string{"task": sample.Outcome.TaskClass, "project": sample.Outcome.Repository} {
			key := dimension + ":" + value
			groups[key] = append(groups[key], sample)
		}
	}
	result := make(map[string]float64, len(groups))
	for key, grouped := range groups {
		result[key], _, _, _ = aggregateRelease(grouped, "")
	}
	return result
}

func completeTrainingCosts(costs []CostLineItemV1, declared int64) (int64, bool) {
	if len(costs) == 0 || declared <= 0 {
		return 0, false
	}
	var total int64
	seen := make(map[string]bool)
	for _, cost := range costs {
		if cost.Category == "" || cost.CostMicros <= 0 || seen[cost.Category] {
			return total, false
		}
		next, ok := safeAdd(total, cost.CostMicros)
		if !ok {
			return total, false
		}
		seen[cost.Category] = true
		total = next
	}
	for _, required := range []string{"data_preparation", "training_compute", "validation_compute"} {
		if !seen[required] {
			return total, false
		}
	}
	return total, total == declared
}

func completeServingCosts(samples []ReleaseSampleV1, declared int64, complete bool) (int64, bool) {
	if !complete || declared <= 0 || len(samples) == 0 {
		return 0, false
	}
	var total int64
	for _, sample := range samples {
		if sample.Outcome.InputTokens <= 0 || sample.Outcome.OutputTokens <= 0 || sample.Outcome.CostMicros <= 0 {
			return total, false
		}
		next, ok := safeAdd(total, sample.Outcome.CostMicros)
		if !ok {
			return total, false
		}
		total = next
	}
	return total, total == declared
}

func validTrainingIdentities(workSpecs, repositories, digests []string) bool {
	if len(workSpecs) == 0 || len(repositories) == 0 || len(digests) == 0 {
		return false
	}
	seenWorkSpecs := make(map[string]bool, len(workSpecs))
	for _, value := range workSpecs {
		if value == "" || seenWorkSpecs[value] {
			return false
		}
		seenWorkSpecs[value] = true
	}
	for _, repository := range repositories {
		if repository == "" {
			return false
		}
	}
	for _, value := range digests {
		if !validSHA256(strings.ToLower(value)) {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			result[value] = true
		}
	}
	return result
}
func normalizedStringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			result[strings.ToLower(value)] = true
		}
	}
	return result
}

func finiteReleaseReport(report HeldOutReleaseReportV1) bool {
	for _, value := range []float64{
		report.BaselineCorrectness, report.CandidateCorrectness, report.RealBaselineCorrectness,
		report.RealCandidateCorrectness, report.BaselineSafetyEventRate, report.CandidateSafetyEventRate,
		report.BaselineRetentionEventRate, report.CandidateRetentionEventRate, report.BaselineFalsePassRate,
		report.CandidateFalsePassRate, report.BaselineMeanLatencyMS, report.CandidateMeanLatencyMS,
	} {
		if !finite(value) {
			return false
		}
	}
	for _, slice := range report.Slices {
		if !finite(slice.CorrectnessRate) || !finite(slice.SafetyEventRate) || !finite(slice.RetentionEventRate) {
			return false
		}
	}
	return true
}

func safeAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > int64(^uint64(0)>>1)-right {
		return 0, false
	}
	return left + right, true
}

func releaseReportDigest(report HeldOutReleaseReportV1) string {
	report.Digest = ""
	payload, _ := json.Marshal(report)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
