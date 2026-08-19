package eval

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const fixtureOutputLimit = 1 << 20

type ValidatorRunV1 struct {
	ID           string `json:"id"`
	Phase        string `json:"phase"`
	Status       string `json:"status"` // pass, fail, or timeout
	ExitStatus   *int   `json:"exit_status,omitempty"`
	OutputSHA256 string `json:"output_sha256"`
	Truncated    bool   `json:"truncated"`
}

type FixtureVerificationV1 struct {
	Version                int              `json:"version"`
	TaskID                 string           `json:"task_id"`
	InitialFailureObserved bool             `json:"initial_failure_observed"`
	CleanReplayPass        bool             `json:"clean_replay_pass"`
	NoisyReplayPass        bool             `json:"noisy_replay_pass"`
	Validators             []ValidatorRunV1 `json:"validators"`
}

// VerifyTaskFixture proves that the starting repository fails, the known-good
// solution passes visible and held-out validators, and clean/noisy replays both
// converge. Noise remains fixture metadata and is never injected into a live
// user session.
func VerifyTaskFixture(ctx context.Context, task TaskSpecV1, pair NoisePairV1) (FixtureVerificationV1, error) {
	if err := task.Validate(pair); err != nil {
		return FixtureVerificationV1{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(task.Resources.WallTimeMS)*time.Millisecond)
	defer cancel()
	result := FixtureVerificationV1{Version: 1, TaskID: task.ID, Validators: []ValidatorRunV1{}}
	clean, cleanErr := Replay(pair.Clean)
	noisy, noisyErr := Replay(pair.Noisy)
	result.CleanReplayPass = cleanErr == nil && clean.RunState == "completed"
	result.NoisyReplayPass = noisyErr == nil && noisy.RunState == "completed"
	if !result.CleanReplayPass || !result.NoisyReplayPass {
		return result, fmt.Errorf("eval: task %s replay did not converge", task.ID)
	}
	workspace, err := os.MkdirTemp("", "azem-eval-fixture-*")
	if err != nil {
		return result, fmt.Errorf("eval: create fixture workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	appendRun := func(id, phase string, spec CommandSpecV1) (ValidatorRunV1, error) {
		run, runErr := runFixtureCommand(runCtx, task, workspace, id, phase, spec)
		result.Validators = append(result.Validators, run)
		return run, runErr
	}
	resetWorkspace := func(solutionFiles, hiddenFiles map[string]string, recordSetup bool) error {
		if err := os.RemoveAll(workspace); err != nil {
			return fmt.Errorf("eval: reset fixture workspace: %w", err)
		}
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return fmt.Errorf("eval: recreate fixture workspace: %w", err)
		}
		if err := writeFixtureFiles(workspace, pair.RepositoryFiles); err != nil {
			return err
		}
		if err := writeFixtureFiles(workspace, solutionFiles); err != nil {
			return err
		}
		if err := writeFixtureFiles(workspace, hiddenFiles); err != nil {
			return err
		}
		for index, setup := range task.Setup {
			id := fmt.Sprintf("setup-%d", index+1)
			run, runErr := runFixtureCommand(runCtx, task, workspace, id, "setup", setup)
			if recordSetup {
				result.Validators = append(result.Validators, run)
			}
			if runErr != nil || run.Status != "pass" {
				return fmt.Errorf("eval: task %s setup failed", task.ID)
			}
		}
		return nil
	}
	if err := resetWorkspace(nil, nil, true); err != nil {
		return result, err
	}
	for index, validator := range task.VisibleValidators {
		if index > 0 {
			if err := resetWorkspace(nil, nil, false); err != nil {
				return result, err
			}
		}
		run, runErr := appendRun(validator.ID, "negative_control", validator.Command)
		if runErr != nil {
			return result, fmt.Errorf("eval: task %s negative control violated side-effect policy", task.ID)
		}
		if run.Status != "pass" {
			result.InitialFailureObserved = true
		}
	}
	if !result.InitialFailureObserved {
		return result, fmt.Errorf("eval: task %s starting repository already passes visible validators", task.ID)
	}
	for _, validator := range task.VisibleValidators {
		if err := resetWorkspace(pair.SolutionFiles, nil, false); err != nil {
			return result, err
		}
		run, runErr := appendRun(validator.ID, "visible", validator.Command)
		if runErr != nil || run.Status != "pass" {
			return result, fmt.Errorf("eval: task %s known solution failed visible validator %s", task.ID, validator.ID)
		}
	}
	for _, validator := range task.HeldOutValidators {
		if err := resetWorkspace(pair.SolutionFiles, validator.HiddenFiles, false); err != nil {
			return result, err
		}
		run, runErr := appendRun(validator.ID, "held_out", validator.Command)
		if runErr != nil || run.Status != "pass" {
			return result, fmt.Errorf("eval: task %s known solution failed held-out validator %s", task.ID, validator.ID)
		}
	}
	if runCtx.Err() != nil {
		return result, fmt.Errorf("eval: task %s exceeded wall time", task.ID)
	}
	return result, nil
}

func writeFixtureFiles(root string, files map[string]string) error {
	if len(files) == 0 {
		return nil
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("eval: open fixture root: %w", err)
	}
	defer rootFS.Close()
	paths := make([]string, 0, len(files))
	for relative := range files {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		if err := validateFixturePath(relative); err != nil {
			return err
		}
		clean := filepath.Clean(relative)
		if parent := filepath.Dir(clean); parent != "." {
			if err := rootFS.MkdirAll(parent, 0o755); err != nil {
				return fmt.Errorf("eval: create fixture directory: %w", err)
			}
		}
		file, err := rootFS.OpenFile(clean, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("eval: open fixture file %s: %w", relative, err)
		}
		if _, err := io.WriteString(file, files[relative]); err != nil {
			_ = file.Close()
			return fmt.Errorf("eval: write fixture file %s: %w", relative, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("eval: close fixture file %s: %w", relative, err)
		}
	}
	return nil
}

func runFixtureCommand(ctx context.Context, task TaskSpecV1, workspace, id, phase string, spec CommandSpecV1) (ValidatorRunV1, error) {
	timeout := time.Duration(spec.TimeoutMS) * time.Millisecond
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	before, err := snapshotFixtureWorkspace(workspace)
	if err != nil {
		return ValidatorRunV1{}, err
	}
	containerName := "azem-eval-" + sumHex([]byte(task.ID + "\x00" + id + "\x00" + phase + "\x00" + strconv.FormatInt(time.Now().UnixNano(), 10)))[:24]
	args := []string{
		"run", "--rm", "--name", containerName, "--network=none", "--read-only",
		"--cpus=" + strconv.FormatFloat(task.Resources.CPUs, 'f', -1, 64),
		"--memory=" + strconv.FormatInt(task.Resources.MemoryMB, 10) + "m",
		"--pids-limit=" + strconv.FormatInt(task.Resources.PIDs, 10),
		"--tmpfs", "/tmp:rw,exec,nosuid,size=256m",
		"--mount", "type=bind,source=" + workspace + ",target=/workspace",
		"--workdir", "/workspace",
		"--env", "GOCACHE=/tmp/go-cache",
		"--env", "GOMODCACHE=/tmp/go-mod-cache",
		task.ContainerImage,
	}
	args = append(args, spec.Argv...)
	command := exec.CommandContext(commandCtx, "docker", args...)
	output := &limitedOutput{limit: fixtureOutputLimit}
	command.Stdout, command.Stderr = output, output
	err = command.Run()
	run := ValidatorRunV1{ID: id, Phase: phase, Status: "pass", OutputSHA256: sumHex(output.Bytes()), Truncated: output.Truncated()}
	if err == nil {
		zero := 0
		run.ExitStatus = &zero
	} else if commandCtx.Err() != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = exec.CommandContext(cleanupCtx, "docker", "rm", "-f", containerName).Run()
		cleanupCancel()
		run.Status = "timeout"
	} else {
		run.Status = "fail"
		if exit, ok := err.(*exec.ExitError); ok {
			code := exit.ExitCode()
			run.ExitStatus = &code
		}
	}
	after, snapshotErr := snapshotFixtureWorkspace(workspace)
	if snapshotErr != nil {
		return run, snapshotErr
	}
	if sideEffectErr := validateFixtureSideEffects(before, after, task.AllowedSideEffects); sideEffectErr != nil {
		return run, sideEffectErr
	}
	return run, nil
}

type fixtureFileState struct {
	kind   string
	digest string
	mode   os.FileMode
}

func snapshotFixtureWorkspace(root string) (map[string]fixtureFileState, error) {
	result := make(map[string]fixtureFileState)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := fixtureFileState{mode: info.Mode()}
		switch {
		case info.Mode().IsRegular():
			payload, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state.kind, state.digest = "file", sumHex(payload)
		case info.IsDir():
			state.kind = "dir"
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			state.kind, state.digest = "symlink", target
		default:
			state.kind = "other"
		}
		result[filepath.ToSlash(relative)] = state
		return nil
	})
	return result, err
}

func validateFixtureSideEffects(before, after map[string]fixtureFileState, allowed []string) error {
	allowedAll := false
	allowedPaths := make([]string, 0, len(allowed))
	for _, path := range allowed {
		if path == "workspace_files" {
			allowedAll = true
			continue
		}
		if err := validateFixturePath(path); err != nil {
			return err
		}
		allowedPaths = append(allowedPaths, filepath.ToSlash(filepath.Clean(path)))
	}
	if allowedAll {
		return nil
	}
	isAllowed := func(path string) bool {
		for _, allowedPath := range allowedPaths {
			if path == allowedPath || strings.HasPrefix(path, allowedPath+"/") {
				return true
			}
		}
		return false
	}
	for path, state := range after {
		if prior, exists := before[path]; exists && prior == state {
			continue
		}
		if !isAllowed(path) {
			return fmt.Errorf("eval: fixture command wrote undeclared path %s", path)
		}
	}
	for path := range before {
		if _, exists := after[path]; !exists && !isAllowed(path) {
			return fmt.Errorf("eval: fixture command removed undeclared path %s", path)
		}
	}
	return nil
}

type limitedOutput struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (w *limitedOutput) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(payload)
	remaining := w.limit - len(w.data)
	if remaining > 0 {
		if len(payload) > remaining {
			payload = payload[:remaining]
		}
		w.data = append(w.data, payload...)
	}
	if original > remaining {
		w.truncated = true
	}
	return original, nil
}

func (w *limitedOutput) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.data...)
}

func (w *limitedOutput) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

var _ io.Writer = (*limitedOutput)(nil)

func validatorSummary(result FixtureVerificationV1) string {
	parts := make([]string, 0, len(result.Validators))
	for _, run := range result.Validators {
		parts = append(parts, run.Phase+":"+run.ID+":"+run.Status)
	}
	return strings.Join(parts, ",")
}
