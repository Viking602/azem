package eval

import (
	"os"
	"testing"

	"github.com/Viking602/azem/internal/session"
)

func TestTaskRegistryResolvesPinnedFixturesAndHeldOutChecks(t *testing.T) {
	t.Parallel()
	pairsFile, err := os.Open("testdata/noise/paired_tasks_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := ReadNoisePairs(pairsFile)
	pairsFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	registryFile, err := os.Open("testdata/tasks/task_registry_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := ReadTaskRegistry(registryFile, pairs)
	registryFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if registry.Version != 1 || len(registry.Tasks) != len(pairs) {
		t.Fatalf("registry = %+v", registry)
	}
	pairByID := make(map[string]NoisePairV1, len(pairs))
	for _, pair := range pairs {
		pairByID[pair.ID] = pair
	}
	for _, task := range registry.Tasks {
		pair := pairByID[task.NoiseFixtureID]
		if task.Repository.SHA256 != fixtureFilesHash(pair.RepositoryFiles) || task.ContainerImage != pair.ContainerImage {
			t.Fatalf("task %s fixture identity diverged", task.ID)
		}
		for _, validator := range task.HeldOutValidators {
			if len(validator.HiddenFiles) == 0 {
				t.Fatalf("task %s held-out validator has no hidden files", task.ID)
			}
			for path := range validator.HiddenFiles {
				if _, visible := pair.RepositoryFiles[path]; visible {
					t.Fatalf("task %s exposes held-out file %s", task.ID, path)
				}
			}
		}
	}
}

func TestTaskRegistryRejectsRepositoryDigestDrift(t *testing.T) {
	t.Parallel()
	pair := NoisePairV1{
		Version: 1, ID: "pair", TaskPrompt: "task",
		ContainerImage:  "example.invalid/go@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RepositoryFiles: map[string]string{"go.mod": "module example\n"}, SolutionFiles: map[string]string{"go.mod": "module fixed\n"},
		Validator: []string{"go", "test"}, KnownValidPath: []string{"fix"},
		Clean: completedReplayFixture("clean"), Noisy: completedReplayFixture("noisy"),
		NoiseLabels: []IncidentLabelV1{{Version: 1, ID: "noise", Category: IncidentToolFailure, Actor: "tool", SubjectID: "call", Evidence: []session.SourceRefV1{{Kind: "fixture", ID: "noise"}}}},
	}
	task := TaskSpecV1{
		Version: 1, ID: "task", Prompt: "task", ContainerImage: pair.ContainerImage,
		Repository: RepositoryFixtureV1{NoisePairID: pair.ID, SHA256: "wrong"}, NoiseFixtureID: pair.ID,
		VisibleValidators:  []ValidatorSpecV1{{ID: "visible", Command: CommandSpecV1{Argv: []string{"go", "test"}, TimeoutMS: 1}}},
		HeldOutValidators:  []ValidatorSpecV1{{ID: "held", Command: CommandSpecV1{Argv: []string{"go", "test"}, TimeoutMS: 1}, HiddenFiles: map[string]string{"hidden_test.go": "package x"}}},
		AllowedSideEffects: []string{"workspace_files"}, Resources: ResourceLimitsV1{CPUs: 1, MemoryMB: 1, PIDs: 1, WallTimeMS: 1, NetworkMode: "none"},
		ExpectedEvidence: []session.SourceRefV1{{Kind: "validator", ID: "visible"}}, Decontamination: DecontaminationV1{Source: "test", License: "test", ContaminationRisk: "low", HeldOutSeparated: true},
	}
	if err := task.Validate(pair); err == nil {
		t.Fatal("accepted repository digest drift")
	}
}
func TestTaskRejectsOverflowingWallTime(t *testing.T) {
	t.Parallel()
	pair := NoisePairV1{
		Version: 1, ID: "pair", TaskPrompt: "task",
		ContainerImage:  "example.invalid/go@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RepositoryFiles: map[string]string{"go.mod": "module example\n"}, SolutionFiles: map[string]string{"go.mod": "module fixed\n"},
		Clean: completedReplayFixture("clean"), Noisy: completedReplayFixture("noisy"),
		NoiseLabels: []IncidentLabelV1{{Version: 1, ID: "noise", Category: IncidentToolFailure, Evidence: []session.SourceRefV1{{Kind: "fixture_step", ID: "complete"}}}},
	}
	task := TaskSpecV1{
		Version: 1, ID: "task", Prompt: "task", ContainerImage: pair.ContainerImage,
		Repository:         RepositoryFixtureV1{NoisePairID: pair.ID, SHA256: fixtureFilesHash(pair.RepositoryFiles)},
		VisibleValidators:  []ValidatorSpecV1{{ID: "visible", Command: CommandSpecV1{Argv: []string{"go", "test"}, TimeoutMS: maxFixtureDurationMS + 1}}},
		HeldOutValidators:  []ValidatorSpecV1{{ID: "held", Command: CommandSpecV1{Argv: []string{"go", "test"}, TimeoutMS: 1}, HiddenFiles: map[string]string{"hidden_test.go": "package x"}}},
		AllowedSideEffects: []string{"workspace_files"}, Resources: ResourceLimitsV1{CPUs: 1, MemoryMB: 1, PIDs: 1, WallTimeMS: maxFixtureDurationMS + 1, NetworkMode: "none"},
		ExpectedEvidence: []session.SourceRefV1{{Kind: "validator", ID: "visible"}}, Decontamination: DecontaminationV1{Source: "test", License: "test", ContaminationRisk: "low", HeldOutSeparated: true},
	}
	if err := task.Validate(pair); err == nil {
		t.Fatal("accepted overflowing fixture duration")
	}
}

func completedReplayFixture(name string) ReplayFixtureV1 {
	return ReplayFixtureV1{
		Version: 1, Name: name, Scenario: name,
		Initial:  ReplayStateV1{},
		Steps:    []ReplayStepV1{{Sequence: 1, ID: "start", Kind: "run", State: "running"}, {Sequence: 2, ID: "complete", Kind: "run", State: "completed"}},
		Expected: ReplayStateV1{OrderedStepIDs: []string{"start", "complete"}, TextPhases: []string{}, ToolStates: map[string]string{}, ApprovalStates: map[string]string{}, ArtifactHashes: map[string]string{}, SubagentStates: map[string]string{}, RunState: "completed", StaleGuidance: []string{}, Evidence: []ReplayEvidenceRefV1{}},
	}
}
