package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Viking602/azem/internal/session"
)

type MutationCandidateV1 struct {
	SourceKind        string
	SourceID          string
	License           string
	Prompt            string
	ContainerImage    string
	CorrectFiles      map[string]string
	TargetPath        string
	Find              string
	Replace           string
	VisibleValidators []ValidatorSpecV1
	HeldOutValidators []ValidatorSpecV1
	Clean             ReplayFixtureV1
	Noisy             ReplayFixtureV1
	NoiseLabels       []IncidentLabelV1
}

type SynthesisRejectionV1 struct {
	SourceID string `json:"source_id"`
	Reason   string `json:"reason"`
}

type SynthesizedTaskV1 struct {
	Task         TaskSpecV1            `json:"task"`
	Pair         NoisePairV1           `json:"pair"`
	Verification FixtureVerificationV1 `json:"verification"`
}

type SynthesisReportV1 struct {
	Version    int                    `json:"version"`
	Accepted   []SynthesizedTaskV1    `json:"accepted"`
	Rejections []SynthesisRejectionV1 `json:"rejections"`
}

type FixtureVerifier func(context.Context, TaskSpecV1, NoisePairV1) (FixtureVerificationV1, error)

// SynthesizeValidatedTasks accepts exact, reviewable mutations from repository
// history, bug-fix diffs, test failures, or structural templates. Every task is
// discarded unless its negative control and known solution pass isolated checks.
func SynthesizeValidatedTasks(ctx context.Context, candidates []MutationCandidateV1, verifier FixtureVerifier) SynthesisReportV1 {
	if verifier == nil {
		verifier = VerifyTaskFixture
	}
	report := SynthesisReportV1{Version: 1}
	for _, candidate := range candidates {
		task, pair, err := synthesizeCandidate(candidate)
		if err != nil {
			report.Rejections = append(report.Rejections, SynthesisRejectionV1{SourceID: candidate.SourceID, Reason: err.Error()})
			continue
		}
		verification, err := verifier(ctx, task, pair)
		if err != nil || !verificationAcceptable(task, verification) {
			reason := "fixture verification failed"
			if err != nil {
				reason = err.Error()
			}
			report.Rejections = append(report.Rejections, SynthesisRejectionV1{SourceID: candidate.SourceID, Reason: reason})
			continue
		}
		report.Accepted = append(report.Accepted, SynthesizedTaskV1{Task: task, Pair: pair, Verification: verification})
	}
	sort.Slice(report.Accepted, func(i, j int) bool { return report.Accepted[i].Task.ID < report.Accepted[j].Task.ID })
	sort.Slice(report.Rejections, func(i, j int) bool { return report.Rejections[i].SourceID < report.Rejections[j].SourceID })
	return report
}

func verificationAcceptable(task TaskSpecV1, verification FixtureVerificationV1) bool {
	if verification.TaskID != task.ID || !verification.InitialFailureObserved || !verification.CleanReplayPass || !verification.NoisyReplayPass {
		return false
	}
	passed := make(map[string]struct{}, len(verification.Validators))
	for _, run := range verification.Validators {
		if run.Status == "pass" && (run.Phase == "visible" || run.Phase == "held_out") {
			passed[run.Phase+"\x00"+run.ID] = struct{}{}
		}
	}
	for _, validator := range task.VisibleValidators {
		if _, exists := passed["visible\x00"+validator.ID]; !exists {
			return false
		}
	}
	for _, validator := range task.HeldOutValidators {
		if _, exists := passed["held_out\x00"+validator.ID]; !exists {
			return false
		}
	}
	return true
}

func synthesizeCandidate(candidate MutationCandidateV1) (TaskSpecV1, NoisePairV1, error) {
	if !oneOfString(candidate.SourceKind, "repository_history", "bug_fix_diff", "test_failure", "structural_mutation") || candidate.SourceID == "" || candidate.License == "" || candidate.Prompt == "" || !pinnedContainerPattern.MatchString(candidate.ContainerImage) || len(candidate.CorrectFiles) == 0 || len(candidate.VisibleValidators) == 0 || len(candidate.HeldOutValidators) == 0 {
		return TaskSpecV1{}, NoisePairV1{}, fmt.Errorf("eval: incomplete synthesis candidate %q", candidate.SourceID)
	}
	if err := validateFixturePath(candidate.TargetPath); err != nil {
		return TaskSpecV1{}, NoisePairV1{}, err
	}
	correct, exists := candidate.CorrectFiles[candidate.TargetPath]
	if !exists {
		return TaskSpecV1{}, NoisePairV1{}, fmt.Errorf("eval: target %s is absent", candidate.TargetPath)
	}
	if candidate.Find == "" || candidate.Find == candidate.Replace || strings.Count(correct, candidate.Find) != 1 {
		return TaskSpecV1{}, NoisePairV1{}, fmt.Errorf("eval: mutation must replace one exact occurrence")
	}
	brokenFiles := cloneFiles(candidate.CorrectFiles)
	brokenFiles[candidate.TargetPath] = strings.Replace(correct, candidate.Find, candidate.Replace, 1)
	identity, _ := json.Marshal(struct {
		SourceID string            `json:"source_id"`
		Files    map[string]string `json:"files"`
		Target   string            `json:"target"`
		Find     string            `json:"find"`
		Replace  string            `json:"replace"`
	}{candidate.SourceID, candidate.CorrectFiles, candidate.TargetPath, candidate.Find, candidate.Replace})
	digest := sha256.Sum256(identity)
	id := hex.EncodeToString(digest[:12])
	pairID := "synthetic-pair:" + id
	noisy := candidate.Noisy
	noisy.Steps = append([]ReplayStepV1(nil), candidate.Noisy.Steps...)
	noiseLabels := append([]IncidentLabelV1(nil), candidate.NoiseLabels...)
	if len(replayDifferenceIDs(candidate.Clean, noisy)) == 0 {
		if len(noisy.Steps) == 0 {
			return TaskSpecV1{}, NoisePairV1{}, fmt.Errorf("eval: noisy replay fixture has no steps")
		}
		const startID = "synthetic-noise-start"
		const failureID = "synthetic-noise-failure"
		nextSequence := noisy.Steps[len(noisy.Steps)-1].Sequence + 1
		noisy.Steps = append(noisy.Steps,
			ReplayStepV1{Sequence: nextSequence, ID: startID, Kind: "tool", TargetID: "synthetic-noise", State: "running"},
			ReplayStepV1{Sequence: nextSequence + 1, ID: failureID, Kind: "tool", TargetID: "synthetic-noise", State: "failed"},
		)
		noisy.Expected.OrderedStepIDs = append(append([]string(nil), noisy.Expected.OrderedStepIDs...), startID, failureID)
		noisy.Expected.ToolStates = cloneStringMap(noisy.Expected.ToolStates)
		noisy.Expected.ToolStates["synthetic-noise"] = "failed"
		noiseLabels = append(noiseLabels, IncidentLabelV1{
			Version: 1, ID: "tool_execution_failure:synthetic-noise",
			Category: IncidentToolFailure, Actor: "tool", SubjectID: "synthetic-noise",
			Reason:   "synthesized recoverable noise",
			Evidence: []session.SourceRefV1{{Kind: "fixture_step", ID: failureID}},
		})
	}
	pair := NoisePairV1{
		Version: 1, ID: pairID, TaskPrompt: candidate.Prompt, ContainerImage: candidate.ContainerImage,
		RepositoryFiles: brokenFiles, SolutionFiles: map[string]string{candidate.TargetPath: correct},
		Validator: append([]string(nil), candidate.VisibleValidators[0].Command.Argv...), KnownValidPath: []string{candidate.TargetPath},
		Clean: candidate.Clean, Noisy: noisy, NoiseLabels: noiseLabels,
	}
	if err := pair.Validate(); err != nil {
		return TaskSpecV1{}, NoisePairV1{}, err
	}
	task := TaskSpecV1{
		Version: 1, ID: "synthetic-task:" + id, Prompt: candidate.Prompt, ContainerImage: candidate.ContainerImage,
		Repository:        RepositoryFixtureV1{NoisePairID: pair.ID, SHA256: fixtureFilesHash(pair.RepositoryFiles)},
		VisibleValidators: append([]ValidatorSpecV1(nil), candidate.VisibleValidators...), HeldOutValidators: append([]ValidatorSpecV1(nil), candidate.HeldOutValidators...),
		AllowedSideEffects: []string{candidate.TargetPath}, Resources: ResourceLimitsV1{CPUs: 1, MemoryMB: 512, PIDs: 128, WallTimeMS: 120_000, NetworkMode: "none"},
		NoiseFixtureID: pair.ID, ExpectedEvidence: []session.SourceRefV1{{Kind: candidate.SourceKind, ID: candidate.SourceID, SHA256: fixtureFilesHash(candidate.CorrectFiles)}},
		Decontamination: DecontaminationV1{Source: candidate.SourceKind + ":" + candidate.SourceID, License: candidate.License, ContaminationRisk: "synthetic_from_local_source", HeldOutSeparated: true},
	}
	if err := task.Validate(pair); err != nil {
		return TaskSpecV1{}, NoisePairV1{}, err
	}
	return task, pair, nil
}

func cloneFiles(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for path, content := range source {
		clone[path] = content
	}
	return clone
}

func oneOfString(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
