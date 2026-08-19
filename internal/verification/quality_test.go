package verification

import (
	"slices"
	"testing"
)

func TestEvaluateQualityAcceptsSaferCoverageWithinCostBudget(t *testing.T) {
	t.Parallel()
	samples := []QualitySampleV1{
		{ID: "pass", ExpectedStatus: "pass", RequiredCriteria: 2, BaselineVerifiedCriteria: 1, CandidateVerifiedCriteria: 2, BaselineStatus: "uncertain", CandidateStatus: "pass", BaselineToolCalls: 1, CandidateToolCalls: 2, CandidateReviewerTokens: 80},
		{ID: "fail", ExpectedStatus: "fail", RequiredCriteria: 2, BaselineVerifiedCriteria: 1, CandidateVerifiedCriteria: 2, BaselineStatus: "pass", CandidateStatus: "fail", BaselineToolCalls: 1, CandidateToolCalls: 2, CandidateReviewerTokens: 120},
	}
	report, err := EvaluateQuality(samples, QualityGateV1{MaxExtraToolCalls: 1, MaxMeanReviewerTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.Baseline.CriterionCoverage != 0.5 || report.Candidate.CriterionCoverage != 1 || report.Baseline.FalsePassRate != 1 || report.Candidate.FalsePassRate != 0 {
		t.Fatalf("metrics = %+v", report)
	}
}

func TestEvaluateQualityRejectsSafetyAndCostRegressions(t *testing.T) {
	t.Parallel()
	samples := []QualitySampleV1{
		{ID: "pass", ExpectedStatus: "pass", RequiredCriteria: 2, BaselineVerifiedCriteria: 2, CandidateVerifiedCriteria: 1, BaselineStatus: "pass", CandidateStatus: "fail", BaselineToolCalls: 1, CandidateToolCalls: 4, CandidateReviewerTokens: 300},
		{ID: "fail", ExpectedStatus: "fail", RequiredCriteria: 1, BaselineVerifiedCriteria: 1, CandidateVerifiedCriteria: 1, BaselineStatus: "fail", CandidateStatus: "pass", BaselineToolCalls: 1, CandidateToolCalls: 4, CandidateReviewerTokens: 300},
	}
	report, err := EvaluateQuality(samples, QualityGateV1{MaxExtraToolCalls: 1, MaxMeanReviewerTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"criterion coverage regressed",
		"false-pass rate regressed",
		"false-fail rate regressed",
		"extra tool-call cost exceeded",
		"reviewer-token cost exceeded",
	}
	if report.Passed || !slices.Equal(report.Failures, want) {
		t.Fatalf("failures = %v, want %v", report.Failures, want)
	}
}

func TestEvaluateQualityTreatsUncertainAsNeitherFalsePassNorFalseFail(t *testing.T) {
	t.Parallel()
	report, err := EvaluateQuality([]QualitySampleV1{{
		ID: "unknown", ExpectedStatus: "fail", RequiredCriteria: 1, BaselineVerifiedCriteria: 0, CandidateVerifiedCriteria: 0,
		BaselineStatus: "uncertain", CandidateStatus: "uncertain",
	}}, QualityGateV1{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Candidate.UncertainRate != 1 || report.Candidate.FalsePassRate != 0 {
		t.Fatalf("uncertain report = %+v", report)
	}
}
