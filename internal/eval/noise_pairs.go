package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const NoisePairVersionV1 = 1

var pinnedContainerPattern = regexp.MustCompile(`^[^@]+@sha256:[0-9a-f]{64}$`)

type NoisePairV1 struct {
	Version         int               `json:"version"`
	ID              string            `json:"id"`
	TaskPrompt      string            `json:"task_prompt"`
	ContainerImage  string            `json:"container_image"`
	RepositoryFiles map[string]string `json:"repository_files"`
	SolutionFiles   map[string]string `json:"solution_files"`
	Validator       []string          `json:"validator"`
	KnownValidPath  []string          `json:"known_valid_path"`
	Clean           ReplayFixtureV1   `json:"clean"`
	Noisy           ReplayFixtureV1   `json:"noisy"`
	NoiseLabels     []IncidentLabelV1 `json:"noise_labels"`
}

func ReadNoisePairs(r io.Reader) ([]NoisePairV1, error) {
	if r == nil {
		return nil, fmt.Errorf("eval: noise pair reader is nil")
	}
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var pairs []NoisePairV1
	if err := decoder.Decode(&pairs); err != nil {
		return nil, fmt.Errorf("eval: decode noise pairs: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, fmt.Errorf("eval: decode noise pairs: %w", err)
	}
	seen := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		if err := pair.Validate(); err != nil {
			return nil, err
		}
		if _, exists := seen[pair.ID]; exists {
			return nil, fmt.Errorf("eval: duplicate noise pair %q", pair.ID)
		}
		seen[pair.ID] = struct{}{}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].ID < pairs[j].ID })
	return pairs, nil
}

func (p NoisePairV1) Validate() error {
	if p.Version != NoisePairVersionV1 || p.ID == "" || p.TaskPrompt == "" || !pinnedContainerPattern.MatchString(p.ContainerImage) {
		return fmt.Errorf("eval: invalid noise pair identity %q", p.ID)
	}
	if len(p.RepositoryFiles) == 0 || len(p.SolutionFiles) == 0 || len(p.Validator) == 0 || len(p.KnownValidPath) == 0 {
		return fmt.Errorf("eval: noise pair %s lacks repository, solution, validator, or valid path", p.ID)
	}
	for path := range p.RepositoryFiles {
		if err := validateFixturePath(path); err != nil {
			return fmt.Errorf("eval: noise pair %s repository: %w", p.ID, err)
		}
	}
	for path := range p.SolutionFiles {
		if err := validateFixturePath(path); err != nil {
			return fmt.Errorf("eval: noise pair %s solution: %w", p.ID, err)
		}
		if _, exists := p.RepositoryFiles[path]; !exists {
			return fmt.Errorf("eval: noise pair %s solution creates undeclared path %s", p.ID, path)
		}
	}
	if err := p.Clean.Validate(); err != nil {
		return fmt.Errorf("eval: noise pair %s clean fixture: %w", p.ID, err)
	}
	if err := p.Noisy.Validate(); err != nil {
		return fmt.Errorf("eval: noise pair %s noisy fixture: %w", p.ID, err)
	}
	clean, err := Replay(p.Clean)
	if err != nil || clean.RunState != "completed" {
		return fmt.Errorf("eval: noise pair %s clean path is not solvable", p.ID)
	}
	noisy, err := Replay(p.Noisy)
	if err != nil || noisy.RunState != "completed" {
		return fmt.Errorf("eval: noise pair %s noisy path is not recoverable", p.ID)
	}
	differences := replayDifferenceIDs(p.Clean, p.Noisy)
	if len(differences) == 0 {
		return fmt.Errorf("eval: noise pair %s clean and noisy fixtures are identical", p.ID)
	}
	if len(p.NoiseLabels) == 0 {
		return fmt.Errorf("eval: noise pair %s has no perturbation labels", p.ID)
	}
	seenLabels := make(map[string]struct{}, len(p.NoiseLabels))
	for _, label := range p.NoiseLabels {
		if label.Version != 1 || label.ID == "" || label.Category == "" || len(label.Evidence) == 0 {
			return fmt.Errorf("eval: noise pair %s has invalid incident label", p.ID)
		}
		if _, exists := seenLabels[label.ID]; exists {
			return fmt.Errorf("eval: noise pair %s repeats incident label %s", p.ID, label.ID)
		}
		seenLabels[label.ID] = struct{}{}
		corresponds := false
		for _, evidence := range label.Evidence {
			if evidence.Kind == "fixture_step" {
				if _, exists := differences[evidence.ID]; exists {
					corresponds = true
				}
			}
		}
		if !corresponds {
			return fmt.Errorf("eval: noise pair %s label %s has no evidence of clean/noisy difference", p.ID, label.ID)
		}
	}
	return nil
}
func replayDifferenceIDs(clean, noisy ReplayFixtureV1) map[string]struct{} {
	cleanByID := make(map[string][]byte, len(clean.Steps))
	for _, step := range clean.Steps {
		encoded, _ := json.Marshal(step)
		cleanByID[step.ID] = encoded
	}
	differences := make(map[string]struct{})
	for _, step := range noisy.Steps {
		encoded, _ := json.Marshal(step)
		if prior, exists := cleanByID[step.ID]; !exists || string(prior) != string(encoded) {
			differences[step.ID] = struct{}{}
		}
		delete(cleanByID, step.ID)
	}
	for id := range cleanByID {
		differences[id] = struct{}{}
	}
	return differences
}

func validateFixturePath(path string) error {
	path = strings.TrimSpace(path)
	clean := filepath.Clean(path)
	if path == "" || clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe fixture path %q", path)
	}
	return nil
}
