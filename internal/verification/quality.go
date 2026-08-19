package verification

import (
	"fmt"
	"math"
)

type QualitySampleV1 struct {
	ID                        string
	ExpectedStatus            string
	RequiredCriteria          int
	BaselineVerifiedCriteria  int
	CandidateVerifiedCriteria int
	BaselineStatus            string
	CandidateStatus           string
	BaselineToolCalls         int
	CandidateToolCalls        int
	CandidateReviewerTokens   int
}

type QualityMetricsV1 struct {
	Samples            int
	CriterionCoverage  float64
	FalsePassRate      float64
	FalseFailRate      float64
	MeanToolCalls      float64
	MeanReviewerTokens float64
	UncertainRate      float64
}

type QualityReportV1 struct {
	Baseline  QualityMetricsV1
	Candidate QualityMetricsV1
	Passed    bool
	Failures  []string
}

type QualityGateV1 struct {
	MaxExtraToolCalls     float64
	MaxMeanReviewerTokens float64
}

func EvaluateQuality(samples []QualitySampleV1, gate QualityGateV1) (QualityReportV1, error) {
	if len(samples) == 0 || gate.MaxExtraToolCalls < 0 || gate.MaxMeanReviewerTokens < 0 {
		return QualityReportV1{}, fmt.Errorf("verification: invalid quality evaluation input")
	}
	for _, sample := range samples {
		if sample.ID == "" || sample.RequiredCriteria <= 0 || sample.BaselineVerifiedCriteria < 0 || sample.CandidateVerifiedCriteria < 0 ||
			sample.BaselineVerifiedCriteria > sample.RequiredCriteria || sample.CandidateVerifiedCriteria > sample.RequiredCriteria ||
			sample.BaselineToolCalls < 0 || sample.CandidateToolCalls < 0 || sample.CandidateReviewerTokens < 0 ||
			!qualityStatus(sample.ExpectedStatus, "pass", "fail") || !qualityStatus(sample.BaselineStatus, "pass", "fail", "uncertain") ||
			!qualityStatus(sample.CandidateStatus, "pass", "fail", "uncertain") {
			return QualityReportV1{}, fmt.Errorf("verification: invalid quality sample %q", sample.ID)
		}
	}
	baseline := aggregateQuality(samples, false)
	candidate := aggregateQuality(samples, true)
	failures := make([]string, 0, 5)
	if candidate.CriterionCoverage < baseline.CriterionCoverage {
		failures = append(failures, "criterion coverage regressed")
	}
	if candidate.FalsePassRate > baseline.FalsePassRate {
		failures = append(failures, "false-pass rate regressed")
	}
	if candidate.FalseFailRate > baseline.FalseFailRate {
		failures = append(failures, "false-fail rate regressed")
	}
	if candidate.MeanToolCalls-baseline.MeanToolCalls > gate.MaxExtraToolCalls {
		failures = append(failures, "extra tool-call cost exceeded")
	}
	if candidate.MeanReviewerTokens > gate.MaxMeanReviewerTokens {
		failures = append(failures, "reviewer-token cost exceeded")
	}
	return QualityReportV1{Baseline: baseline, Candidate: candidate, Passed: len(failures) == 0, Failures: failures}, nil
}

func aggregateQuality(samples []QualitySampleV1, candidate bool) QualityMetricsV1 {
	var required, verified, falsePasses, falseFails, passExpected, failExpected, toolCalls, reviewerTokens, uncertain int
	for _, sample := range samples {
		required += sample.RequiredCriteria
		status := sample.BaselineStatus
		verified += sample.BaselineVerifiedCriteria
		calls := sample.BaselineToolCalls
		if candidate {
			status = sample.CandidateStatus
			verified += sample.CandidateVerifiedCriteria - sample.BaselineVerifiedCriteria
			calls = sample.CandidateToolCalls
			reviewerTokens += sample.CandidateReviewerTokens
		}
		toolCalls += calls
		if sample.ExpectedStatus == "pass" {
			passExpected++
			if status == "fail" {
				falseFails++
			}
		} else {
			failExpected++
			if status == "pass" {
				falsePasses++
			}
		}
		if status == "uncertain" {
			uncertain++
		}
	}
	return QualityMetricsV1{
		Samples: len(samples), CriterionCoverage: ratio(verified, required), FalsePassRate: ratio(falsePasses, failExpected),
		FalseFailRate: ratio(falseFails, passExpected), MeanToolCalls: ratio(toolCalls, len(samples)),
		MeanReviewerTokens: ratio(reviewerTokens, len(samples)), UncertainRate: ratio(uncertain, len(samples)),
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return math.Round((float64(numerator)/float64(denominator))*1e6) / 1e6
}

func qualityStatus(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
