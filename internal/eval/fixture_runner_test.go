package eval

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPinnedContainerFixturesAreSolvable(t *testing.T) {
	if os.Getenv("AZEM_EVAL_CONTAINER_TEST") != "1" {
		t.Skip("set AZEM_EVAL_CONTAINER_TEST=1 to run pinned container fixtures")
	}
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
	pairByID := make(map[string]NoisePairV1, len(pairs))
	for _, pair := range pairs {
		pairByID[pair.ID] = pair
	}
	for _, task := range registry.Tasks {
		result, err := VerifyTaskFixture(t.Context(), task, pairByID[task.NoiseFixtureID])
		if err != nil {
			t.Fatalf("fixture %s: %v (%s)", task.ID, err, validatorSummary(result))
		}
		if !result.InitialFailureObserved || !result.CleanReplayPass || !result.NoisyReplayPass {
			t.Fatalf("fixture %s did not prove solvability: %+v", task.ID, result)
		}
		for _, run := range result.Validators {
			if run.Phase != "negative_control" && run.Status != "pass" {
				t.Fatalf("fixture %s validator failed: %+v", task.ID, run)
			}
		}
	}
}

func TestLimitedOutputBoundsCapturedValidatorBytes(t *testing.T) {
	t.Parallel()
	writer := &limitedOutput{limit: 4}
	payload := strings.Repeat("x", 10)
	written, err := writer.Write([]byte(payload))
	if err != nil || written != len(payload) {
		t.Fatalf("write = %d, %v", written, err)
	}
	if string(writer.Bytes()) != "xxxx" || !writer.Truncated() {
		t.Fatalf("output = %q truncated=%v", writer.Bytes(), writer.Truncated())
	}
}

func TestLimitedOutputSupportsConcurrentStreams(t *testing.T) {
	t.Parallel()
	writer := &limitedOutput{limit: 20_000}
	var wait sync.WaitGroup
	for _, payload := range []string{"stdout\n", "stderr\n"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 1_000 {
				if _, err := writer.Write([]byte(payload)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}()
	}
	wait.Wait()
	if len(writer.Bytes()) != 14_000 || writer.Truncated() {
		t.Fatalf("concurrent output bytes=%d truncated=%v", len(writer.Bytes()), writer.Truncated())
	}
}

func TestWriteFixtureFilesRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := writeFixtureFiles(root, map[string]string{"escape/secret": "leak"}); err == nil {
		t.Fatal("accepted fixture write through an escaping symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); !os.IsNotExist(err) {
		t.Fatalf("fixture write escaped root: %v", err)
	}
}

func TestFixtureSideEffectsRejectUndeclaredWrites(t *testing.T) {
	t.Parallel()
	before := map[string]fixtureFileState{"allowed.go": {kind: "file", digest: "before"}, "untouched.go": {kind: "file", digest: "same"}}
	after := map[string]fixtureFileState{"allowed.go": {kind: "file", digest: "after"}, "untouched.go": {kind: "file", digest: "same"}, "generated.txt": {kind: "file", digest: "new"}}
	if err := validateFixtureSideEffects(before, after, []string{"allowed.go"}); err == nil {
		t.Fatal("accepted undeclared fixture write")
	}
}
