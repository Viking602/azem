package toollab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestGeneratedToolStaysUninstallableUntilLabAndHumanPromotion(t *testing.T) {
	t.Parallel()
	input := validToolInput()
	candidate, err := GenerateCandidate(input)
	if err != nil {
		t.Fatal(err)
	}
	input.SourceFiles["main.go"] = "tampered"
	input.Gap.Evidence[0].ID = "tampered"
	if candidate.SourceFiles["main.go"] == "tampered" || candidate.Gap.Evidence[0].ID == "tampered" || candidate.Installable {
		t.Fatal("candidate retained caller state or became installable")
	}
	policy := DefaultLabPolicy()
	var checks []string
	runner := func(_ context.Context, _ CandidateV1, check string, _ LabCommandV1) CheckResultV1 {
		checks = append(checks, check)
		return CheckResultV1{Check: check, Status: "pass"}
	}
	report, err := Evaluate(context.Background(), candidate, policy, runner)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "pass" || report.Installable || report.Digest == "" || report.PolicyDigest == "" || len(checks) != 4 {
		t.Fatalf("report = %+v checks=%v", report, checks)
	}
	if _, err := Promote(candidate, policy, report, HumanReviewV1{}); err == nil {
		t.Fatal("promoted laboratory result without human review")
	}
	review := HumanReviewV1{
		Reviewer: "maintainer@example.com", Decision: "approved", ReviewedAt: time.Unix(1_700_000_000, 0).UTC(),
		Evidence: []session.SourceRefV1{{Kind: "review", ID: "review-1"}},
	}
	promoted, err := Promote(candidate, policy, report, review)
	if err != nil {
		t.Fatal(err)
	}
	if !promoted.Installable || promoted.CandidateID != candidate.ID || promoted.LabDigest != report.Digest {
		t.Fatalf("promoted = %+v", promoted)
	}
	if _, err := Promote(candidate, policy, report, HumanReviewV1{
		Reviewer: report.TesterID, Decision: "approved", ReviewedAt: time.Now(),
		Evidence: []session.SourceRefV1{{Kind: "review", ID: "review-same-identity"}},
	}); err == nil {
		t.Fatal("laboratory tester self-approved candidate")
	}
	tampered := report
	tampered.Results = append([]CheckResultV1(nil), report.Results...)
	tampered.Results[0].Output = "changed after evaluation"
	if _, err := Promote(candidate, policy, tampered, review); err == nil {
		t.Fatal("promoted report whose digest no longer matched")
	}
}

func TestCandidateChecksAreIgnoredAndHostPolicyOwnsAllGates(t *testing.T) {
	t.Parallel()
	input := validToolInput()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["checks"] = json.RawMessage(`{"fuzz":{"argv":["sh","-c","true"],"timeout_ms":1}}`)
	raw, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var decoded GeneratedToolInputV1
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	candidate, err := GenerateCandidate(decoded)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultLabPolicy()
	var received []LabCommandV1
	report, err := Evaluate(context.Background(), candidate, policy, func(_ context.Context, _ CandidateV1, check string, command LabCommandV1) CheckResultV1 {
		received = append(received, command)
		return CheckResultV1{Check: check, Status: "pass"}
	})
	if err != nil || report.Status != "pass" || len(received) != len(requiredChecks()) {
		t.Fatalf("report=%+v received=%d err=%v", report, len(received), err)
	}
	for _, command := range received {
		if len(command.Argv) != 3 || command.Argv[0] != "go" || command.Argv[1] != "test" {
			t.Fatalf("candidate-controlled command reached runner: %+v", command)
		}
	}
	missing := DefaultLabPolicy()
	delete(missing.Checks, CheckFuzz)
	runnerCalled := false
	if _, err := Evaluate(context.Background(), candidate, missing, func(context.Context, CandidateV1, string, LabCommandV1) CheckResultV1 {
		runnerCalled = true
		return CheckResultV1{Status: "pass"}
	}); err == nil || runnerCalled {
		t.Fatalf("missing host gate was not rejected before execution: err=%v called=%v", err, runnerCalled)
	}
}

func TestGeneratedToolRequiresClosedSchemaHistoricalEvidenceAndSafePermissions(t *testing.T) {
	t.Parallel()
	input := validToolInput()
	input.Permissions = append(input.Permissions, "network")
	if _, err := GenerateCandidate(input); err == nil {
		t.Fatal("accepted network permission")
	}
	input = validToolInput()
	input.InputSchema = []byte(`{"type":"object","properties":{},"additionalProperties":true}`)
	if _, err := GenerateCandidate(input); err == nil {
		t.Fatal("accepted open input schema")
	}
	input = validToolInput()
	input.Gap.Evidence = nil
	if _, err := GenerateCandidate(input); err == nil {
		t.Fatal("accepted candidate without historical gap evidence")
	}
}

func TestToolLabRejectsOversizedPayloadsBeforeExecution(t *testing.T) {
	t.Parallel()
	input := validToolInput()
	input.SourceFiles["large.go"] = strings.Repeat("x", maxSourceFileBytes+1)
	if _, err := GenerateCandidate(input); err == nil {
		t.Fatal("accepted oversized source file")
	}
	input = validToolInput()
	for index := 0; index < maxSourceFiles+1; index++ {
		input.SourceFiles["extra"+string(rune('a'+index%26))+strings.Repeat("x", index/26)+".go"] = "x"
	}
	if _, err := GenerateCandidate(input); err == nil {
		t.Fatal("accepted too many source files")
	}
	input = validToolInput()
	input.InputSchema = []byte(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":[],"additionalProperties":false}`)
	candidate, err := GenerateCandidate(input)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultLabPolicy()
	policy.MaxSchemaProperties = 1
	called := false
	if _, err := Evaluate(context.Background(), candidate, policy, func(context.Context, CandidateV1, string, LabCommandV1) CheckResultV1 {
		called = true
		return CheckResultV1{Status: "pass"}
	}); err == nil || called {
		t.Fatalf("oversized schema reached execution: err=%v called=%v", err, called)
	}
	policy = DefaultLabPolicy()
	policy.MaxSourceBytes = 1
	called = false
	if _, err := Evaluate(context.Background(), candidate, policy, func(context.Context, CandidateV1, string, LabCommandV1) CheckResultV1 {
		called = true
		return CheckResultV1{Status: "pass"}
	}); err == nil || called {
		t.Fatalf("oversized source payload reached execution: err=%v called=%v", err, called)
	}
	policy = DefaultLabPolicy()
	policy.MaxDescriptionBytes = 1
	called = false
	if _, err := Evaluate(context.Background(), candidate, policy, func(context.Context, CandidateV1, string, LabCommandV1) CheckResultV1 {
		called = true
		return CheckResultV1{Status: "pass"}
	}); err == nil || called {
		t.Fatalf("oversized description reached execution: err=%v called=%v", err, called)
	}
	policy = DefaultLabPolicy()
	policy.Checks[CheckFuzz] = LabCommandV1{Argv: []string{"go"}, TimeoutMS: maxCommandTimeoutMS + 1}
	called = false
	if _, err := Evaluate(context.Background(), candidate, policy, func(context.Context, CandidateV1, string, LabCommandV1) CheckResultV1 {
		called = true
		return CheckResultV1{Status: "pass"}
	}); err == nil || called {
		t.Fatalf("unbounded check timeout reached execution: err=%v called=%v", err, called)
	}
	policy = DefaultLabPolicy()
	policy.Checks[CheckFuzz] = LabCommandV1{Argv: []string{strings.Repeat("x", maxCommandArgBytes+1)}, TimeoutMS: 1}
	called = false
	if _, err := Evaluate(context.Background(), candidate, policy, func(context.Context, CandidateV1, string, LabCommandV1) CheckResultV1 {
		called = true
		return CheckResultV1{Status: "pass"}
	}); err == nil || called {
		t.Fatalf("oversized check reached execution: err=%v called=%v", err, called)
	}
}

func TestToolLabBoundsRunnerOutputAndBindsPolicyDigest(t *testing.T) {
	t.Parallel()
	candidate, err := GenerateCandidate(validToolInput())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultLabPolicy()
	policy.MaxCommandOutputBytes = 8
	report, err := Evaluate(context.Background(), candidate, policy, func(context.Context, CandidateV1, string, LabCommandV1) CheckResultV1 {
		return CheckResultV1{Status: "pass", Output: strings.Repeat("o", maxCommandOutputBytes)}
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Results {
		if len(result.Output) != 8 || !result.Truncated {
			t.Fatalf("unbounded output result: %+v", result)
		}
	}
	tamperedOutput := report
	tamperedOutput.Results = append([]CheckResultV1(nil), report.Results...)
	tamperedOutput.Results[0].Output = strings.Repeat("x", 9)
	tamperedOutput.Digest = reportIdentity(tamperedOutput.CandidateID, tamperedOutput.PolicyDigest, tamperedOutput.TesterID, tamperedOutput.Status, tamperedOutput.Results)
	review := HumanReviewV1{Reviewer: "maintainer", Decision: "approved", ReviewedAt: time.Now(), Evidence: []session.SourceRefV1{{Kind: "review", ID: "review"}}}
	if _, err := Promote(candidate, policy, tamperedOutput, review); err == nil {
		t.Fatal("promoted oversized laboratory output")
	}
	changed := policy
	changed.Checks[CheckFuzz] = LabCommandV1{Argv: []string{"go", "test", "./...", "-run", "Changed"}, TimeoutMS: 30_000}
	if _, err := Promote(candidate, changed, report, review); err == nil {
		t.Fatal("promoted report under a different host policy")
	}
}

func TestFailedGeneratedToolCannotBePromoted(t *testing.T) {
	t.Parallel()
	candidate, err := GenerateCandidate(validToolInput())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultLabPolicy()
	report, err := Evaluate(context.Background(), candidate, policy, func(_ context.Context, _ CandidateV1, check string, _ LabCommandV1) CheckResultV1 {
		status := "pass"
		if check == CheckStaticAnalysis {
			status = "fail"
		}
		return CheckResultV1{Check: check, Status: status}
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "fail" || report.Installable {
		t.Fatalf("report = %+v", report)
	}
	review := HumanReviewV1{Reviewer: "maintainer", Decision: "approved", ReviewedAt: time.Now(), Evidence: []session.SourceRefV1{{Kind: "review", ID: "review"}}}
	if _, err := Promote(candidate, policy, report, review); err == nil {
		t.Fatal("promoted failed tool")
	}
}

func validToolInput() GeneratedToolInputV1 {
	return GeneratedToolInputV1{
		Name: "structural_lookup", Description: "Resolve one validated historical lookup gap.",
		InputSchema:    []byte(`{"type":"object","properties":{"symbol":{"type":"string"}},"required":["symbol"],"additionalProperties":false}`),
		Gap:            HistoricalGapV1{ID: "gap-1", Summary: "structural lookups lacked one bounded query", Evidence: []session.SourceRefV1{{Kind: "incident", ID: "incident-1"}}},
		ContainerImage: "docker.io/library/golang:1.25.6@sha256:06d1251c59a75761ce4ebc8b299030576233d7437c886a68b43464bad62d4bb1",
		SourceFiles:    map[string]string{"go.mod": "module fixture.local/tool\n\ngo 1.25\n", "main.go": "package tool\n"},
		Permissions:    []string{"workspace_read"},
	}
}
