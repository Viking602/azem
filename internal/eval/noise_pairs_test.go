package eval

import (
	"os"
	"testing"
)

func TestPairedCodingFixturesKeepCleanAndNoisyPathsSolvable(t *testing.T) {
	t.Parallel()
	file, err := os.Open("testdata/noise/paired_tasks_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	pairs, err := ReadNoisePairs(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 {
		t.Fatalf("pair count = %d, want 2", len(pairs))
	}
	for _, pair := range pairs {
		clean, err := Replay(pair.Clean)
		if err != nil {
			t.Fatalf("clean replay %s: %v", pair.ID, err)
		}
		noisy, err := Replay(pair.Noisy)
		if err != nil {
			t.Fatalf("noisy replay %s: %v", pair.ID, err)
		}
		if clean.RunState != "completed" || noisy.RunState != "completed" || len(clean.Evidence) == 0 || len(noisy.Evidence) == 0 {
			t.Fatalf("pair %s is not solvable: clean=%+v noisy=%+v", pair.ID, clean, noisy)
		}
		if clean.TextPhases[len(clean.TextPhases)-1] != noisy.TextPhases[len(noisy.TextPhases)-1] {
			t.Fatalf("pair %s changed final text phase", pair.ID)
		}
		if len(pair.NoiseLabels) == 0 {
			t.Fatalf("pair %s has no explicit offline perturbations", pair.ID)
		}
	}
}

func TestNoisePairRejectsMutableContainerAndEscapingPath(t *testing.T) {
	t.Parallel()
	pair := NoisePairV1{
		Version: 1, ID: "unsafe", TaskPrompt: "task", ContainerImage: "golang:latest",
		RepositoryFiles: map[string]string{"../escape": "x"}, SolutionFiles: map[string]string{"../escape": "y"},
		Validator: []string{"go", "test"}, KnownValidPath: []string{"edit"},
		Clean:       ReplayFixtureV1{Version: 1, Name: "clean", Scenario: "clean", Steps: []ReplayStepV1{{Sequence: 1, ID: "start", Kind: "run", State: "running"}}},
		Noisy:       ReplayFixtureV1{Version: 1, Name: "noisy", Scenario: "noisy", Steps: []ReplayStepV1{{Sequence: 1, ID: "start", Kind: "run", State: "running"}}},
		NoiseLabels: []IncidentLabelV1{{Version: 1, ID: "noise", Category: IncidentToolFailure}},
	}
	if err := pair.Validate(); err == nil {
		t.Fatal("accepted mutable container and escaping fixture path")
	}
}
