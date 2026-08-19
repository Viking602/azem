package eval

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/Viking602/azem/internal/session"
)

const TaskRegistryVersionV1 = 1
const maxFixtureDurationMS int64 = 24 * 60 * 60 * 1000

type RepositoryFixtureV1 struct {
	NoisePairID string `json:"noise_pair_id"`
	SHA256      string `json:"sha256"`
}

type CommandSpecV1 struct {
	Argv      []string `json:"argv"`
	TimeoutMS int64    `json:"timeout_ms"`
}

type ValidatorSpecV1 struct {
	ID          string            `json:"id"`
	Command     CommandSpecV1     `json:"command"`
	HiddenFiles map[string]string `json:"hidden_files,omitempty"`
}

type ResourceLimitsV1 struct {
	CPUs        float64 `json:"cpus"`
	MemoryMB    int64   `json:"memory_mb"`
	PIDs        int64   `json:"pids"`
	WallTimeMS  int64   `json:"wall_time_ms"`
	NetworkMode string  `json:"network_mode"`
}

type DecontaminationV1 struct {
	Source            string `json:"source"`
	License           string `json:"license"`
	ContaminationRisk string `json:"contamination_risk"`
	HeldOutSeparated  bool   `json:"held_out_separated"`
	Notes             string `json:"notes,omitempty"`
}

// TaskSpecV1 is the self-contained offline execution contract. The referenced
// noise pair owns repository bytes and the known-good solution; held-out files
// are materialized only after the candidate has finished.
type TaskSpecV1 struct {
	Version            int                   `json:"version"`
	ID                 string                `json:"id"`
	Prompt             string                `json:"prompt"`
	ContainerImage     string                `json:"container_image"`
	Repository         RepositoryFixtureV1   `json:"repository"`
	Setup              []CommandSpecV1       `json:"setup"`
	VisibleValidators  []ValidatorSpecV1     `json:"visible_validators"`
	HeldOutValidators  []ValidatorSpecV1     `json:"held_out_validators"`
	AllowedSideEffects []string              `json:"allowed_side_effects"`
	Resources          ResourceLimitsV1      `json:"resources"`
	NoiseFixtureID     string                `json:"noise_fixture_id"`
	ExpectedEvidence   []session.SourceRefV1 `json:"expected_evidence"`
	Decontamination    DecontaminationV1     `json:"decontamination"`
}

type TaskRegistryV1 struct {
	Version int          `json:"version"`
	Tasks   []TaskSpecV1 `json:"tasks"`
}

func ReadTaskRegistry(r io.Reader, pairs []NoisePairV1) (TaskRegistryV1, error) {
	if r == nil {
		return TaskRegistryV1{}, fmt.Errorf("eval: task registry reader is nil")
	}
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var registry TaskRegistryV1
	if err := decoder.Decode(&registry); err != nil {
		return TaskRegistryV1{}, fmt.Errorf("eval: decode task registry: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return TaskRegistryV1{}, fmt.Errorf("eval: decode task registry: %w", err)
	}
	if registry.Version != TaskRegistryVersionV1 || len(registry.Tasks) == 0 {
		return TaskRegistryV1{}, fmt.Errorf("eval: invalid task registry identity")
	}
	pairByID := make(map[string]NoisePairV1, len(pairs))
	for _, pair := range pairs {
		if err := pair.Validate(); err != nil {
			return TaskRegistryV1{}, err
		}
		pairByID[pair.ID] = pair
	}
	seen := make(map[string]struct{}, len(registry.Tasks))
	for _, task := range registry.Tasks {
		pair, exists := pairByID[task.Repository.NoisePairID]
		if !exists {
			return TaskRegistryV1{}, fmt.Errorf("eval: task %s references unknown noise pair %s", task.ID, task.Repository.NoisePairID)
		}
		if err := task.Validate(pair); err != nil {
			return TaskRegistryV1{}, err
		}
		if _, exists := seen[task.ID]; exists {
			return TaskRegistryV1{}, fmt.Errorf("eval: duplicate task %q", task.ID)
		}
		seen[task.ID] = struct{}{}
	}
	sort.Slice(registry.Tasks, func(i, j int) bool { return registry.Tasks[i].ID < registry.Tasks[j].ID })
	return registry, nil
}

func (t TaskSpecV1) Validate(pair NoisePairV1) error {
	if t.Version != TaskRegistryVersionV1 || t.ID == "" || t.Prompt == "" || !pinnedContainerPattern.MatchString(t.ContainerImage) {
		return fmt.Errorf("eval: invalid task identity %q", t.ID)
	}
	if t.ContainerImage != pair.ContainerImage || t.NoiseFixtureID != pair.ID || t.Repository.NoisePairID != pair.ID {
		return fmt.Errorf("eval: task %s route or fixture identity diverges from noise pair", t.ID)
	}
	if digest := fixtureFilesHash(pair.RepositoryFiles); t.Repository.SHA256 != digest {
		return fmt.Errorf("eval: task %s repository digest = %s, want %s", t.ID, t.Repository.SHA256, digest)
	}
	if len(t.VisibleValidators) == 0 || len(t.HeldOutValidators) == 0 || len(t.AllowedSideEffects) == 0 || len(t.ExpectedEvidence) == 0 {
		return fmt.Errorf("eval: task %s lacks validators, side-effect policy, or evidence", t.ID)
	}
	if t.Resources.CPUs <= 0 || t.Resources.MemoryMB <= 0 || t.Resources.PIDs <= 0 || t.Resources.WallTimeMS <= 0 || t.Resources.WallTimeMS > maxFixtureDurationMS || t.Resources.NetworkMode != "none" {
		return fmt.Errorf("eval: task %s has unsafe resource limits", t.ID)
	}
	for _, effect := range t.AllowedSideEffects {
		if effect != "workspace_files" {
			if err := validateFixturePath(effect); err != nil {
				return fmt.Errorf("eval: task %s side-effect policy: %w", t.ID, err)
			}
		}
	}
	validatorIDs := make(map[string]struct{})
	for _, validators := range [][]ValidatorSpecV1{t.VisibleValidators, t.HeldOutValidators} {
		for _, validator := range validators {
			if validator.ID == "" || len(validator.Command.Argv) == 0 || validator.Command.TimeoutMS <= 0 || validator.Command.TimeoutMS > maxFixtureDurationMS {
				return fmt.Errorf("eval: task %s has invalid validator", t.ID)
			}
			if _, exists := validatorIDs[validator.ID]; exists {
				return fmt.Errorf("eval: task %s repeats validator %s", t.ID, validator.ID)
			}
			validatorIDs[validator.ID] = struct{}{}
			for path := range validator.HiddenFiles {
				if err := validateFixturePath(path); err != nil {
					return fmt.Errorf("eval: task %s hidden validator file: %w", t.ID, err)
				}
				if _, visible := pair.RepositoryFiles[path]; visible {
					return fmt.Errorf("eval: task %s hidden validator overwrites visible file %s", t.ID, path)
				}
			}
		}
	}
	if t.Decontamination.Source == "" || t.Decontamination.License == "" || t.Decontamination.ContaminationRisk == "" || !t.Decontamination.HeldOutSeparated {
		return fmt.Errorf("eval: task %s lacks decontamination metadata", t.ID)
	}
	return nil
}

func fixtureFilesHash(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	var size [8]byte
	for _, path := range paths {
		binary.BigEndian.PutUint64(size[:], uint64(len(path)))
		hash.Write(size[:])
		hash.Write([]byte(path))
		content := files[path]
		binary.BigEndian.PutUint64(size[:], uint64(len(content)))
		hash.Write(size[:])
		hash.Write([]byte(content))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
