package eval

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSynthesizeValidatedTasksKeepsOnlySolvableIsolatedMutations(t *testing.T) {
	t.Parallel()
	valid := synthesisCandidate("bug-42")
	invalidMutation := synthesisCandidate("bad-mutation")
	invalidMutation.Find = "missing exact text"
	verificationFailure := synthesisCandidate("verification-failure")
	report := SynthesizeValidatedTasks(t.Context(), []MutationCandidateV1{verificationFailure, invalidMutation, valid}, func(_ context.Context, task TaskSpecV1, _ NoisePairV1) (FixtureVerificationV1, error) {
		if task.ExpectedEvidence[0].ID == "verification-failure" {
			return FixtureVerificationV1{}, errors.New("known solution failed hidden check")
		}
		return FixtureVerificationV1{
			Version: 1, TaskID: task.ID, InitialFailureObserved: true, CleanReplayPass: true, NoisyReplayPass: true,
			Validators: []ValidatorRunV1{{ID: "visible", Phase: "visible", Status: "pass"}, {ID: "hidden", Phase: "held_out", Status: "pass"}},
		}, nil
	})
	if report.Version != 1 || len(report.Accepted) != 1 || len(report.Rejections) != 2 {
		t.Fatalf("report = %+v", report)
	}
	accepted := report.Accepted[0]
	if !strings.Contains(accepted.Pair.RepositoryFiles["main.go"], "return 3") || !strings.Contains(accepted.Pair.SolutionFiles["main.go"], "return 2") {
		t.Fatalf("mutation/solution = %+v %+v", accepted.Pair.RepositoryFiles, accepted.Pair.SolutionFiles)
	}
	if accepted.Task.Resources.NetworkMode != "none" || accepted.Task.Repository.SHA256 != fixtureFilesHash(accepted.Pair.RepositoryFiles) || accepted.Task.Decontamination.Source != "bug_fix_diff:bug-42" || !accepted.Task.Decontamination.HeldOutSeparated || accepted.Task.ExpectedEvidence[0].SHA256 == "" {
		t.Fatalf("task contract = %+v", accepted.Task)
	}
}

func TestSynthesisRejectsIncompleteVerificationEvidence(t *testing.T) {
	t.Parallel()
	report := SynthesizeValidatedTasks(t.Context(), []MutationCandidateV1{synthesisCandidate("incomplete")}, func(_ context.Context, task TaskSpecV1, _ NoisePairV1) (FixtureVerificationV1, error) {
		return FixtureVerificationV1{Version: 1, TaskID: task.ID, InitialFailureObserved: true, CleanReplayPass: true, NoisyReplayPass: true}, nil
	})
	if len(report.Accepted) != 0 || len(report.Rejections) != 1 {
		t.Fatalf("accepted incomplete validator evidence: %+v", report)
	}
}

func TestSynthesisRejectsEmptyNoisyReplayWithoutPanicking(t *testing.T) {
	t.Parallel()
	candidate := synthesisCandidate("empty-noisy")
	candidate.Clean = ReplayFixtureV1{}
	candidate.Noisy = ReplayFixtureV1{}

	report := SynthesizeValidatedTasks(t.Context(), []MutationCandidateV1{candidate}, nil)
	if len(report.Accepted) != 0 || len(report.Rejections) != 1 || !strings.Contains(report.Rejections[0].Reason, "no steps") {
		t.Fatalf("empty replay report = %+v", report)
	}
}

func TestSynthesisKeepsSourceProvenanceSeparateFromSyntheticNoise(t *testing.T) {
	t.Parallel()
	task, pair, err := synthesizeCandidate(synthesisCandidate("separate-noise"))
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" || len(pair.NoiseLabels) != 1 {
		t.Fatalf("synthesized pair = %+v", pair)
	}
	label := pair.NoiseLabels[0]
	if label.Category != IncidentToolFailure || len(label.Evidence) != 1 || label.Evidence[0].ID != "synthetic-noise-failure" {
		t.Fatalf("synthetic incident = %+v", label)
	}
	if len(task.ExpectedEvidence) != 1 || task.ExpectedEvidence[0].Kind != "bug_fix_diff" || task.ExpectedEvidence[0].ID != "separate-noise" {
		t.Fatalf("source provenance = %+v", task.ExpectedEvidence)
	}
}

func synthesisCandidate(sourceID string) MutationCandidateV1 {
	container := "docker.io/library/golang:1.25.6@sha256:06d1251c59a75761ce4ebc8b299030576233d7437c886a68b43464bad62d4bb1"
	return MutationCandidateV1{
		SourceKind: "bug_fix_diff", SourceID: sourceID, License: "repository-license", Prompt: "Restore Add behavior without changing its API.", ContainerImage: container,
		CorrectFiles: map[string]string{"go.mod": "module fixture\n\ngo 1.25\n", "main.go": "package fixture\n\nfunc Add() int { return 2 }\n"},
		TargetPath:   "main.go", Find: "return 2", Replace: "return 3",
		VisibleValidators: []ValidatorSpecV1{{ID: "visible", Command: CommandSpecV1{Argv: []string{"go", "test", "./..."}, TimeoutMS: 30_000}}},
		HeldOutValidators: []ValidatorSpecV1{{ID: "hidden", Command: CommandSpecV1{Argv: []string{"go", "test", "./..."}, TimeoutMS: 30_000}, HiddenFiles: map[string]string{"main_test.go": "package fixture\n"}}},
		Clean:             completedReplayFixture("synthesis-clean-" + sourceID), Noisy: completedReplayFixture("synthesis-noisy-" + sourceID),
	}
}
