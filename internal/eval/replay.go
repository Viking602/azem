package eval

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

const ReplayFixtureVersionV1 = 1

type ReplayEvidenceRefV1 struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256,omitempty"`
}

type ReplayStepV1 struct {
	Sequence int64                `json:"sequence"`
	ID       string               `json:"id"`
	Kind     string               `json:"kind"`
	TargetID string               `json:"target_id,omitempty"`
	State    string               `json:"state,omitempty"`
	Phase    string               `json:"phase,omitempty"`
	SHA256   string               `json:"sha256,omitempty"`
	Revision int64                `json:"revision,omitempty"`
	Evidence *ReplayEvidenceRefV1 `json:"evidence,omitempty"`
}

type ReplayStateV1 struct {
	OrderedStepIDs   []string              `json:"ordered_step_ids"`
	TextPhases       []string              `json:"text_phases"`
	RunState         string                `json:"run_state,omitempty"`
	ToolStates       map[string]string     `json:"tool_states"`
	ApprovalStates   map[string]string     `json:"approval_states"`
	ArtifactHashes   map[string]string     `json:"artifact_hashes"`
	SubagentStates   map[string]string     `json:"subagent_states"`
	SemanticRevision int64                 `json:"semantic_revision"`
	GuidanceRevision int64                 `json:"guidance_revision"`
	StaleGuidance    []string              `json:"stale_guidance"`
	Evidence         []ReplayEvidenceRefV1 `json:"evidence"`
}

type ReplayFixtureV1 struct {
	Version  int            `json:"version"`
	Name     string         `json:"name"`
	Scenario string         `json:"scenario"`
	Initial  ReplayStateV1  `json:"initial"`
	Steps    []ReplayStepV1 `json:"steps"`
	Expected ReplayStateV1  `json:"expected"`
}

func ReadReplayFixtures(r io.Reader) ([]ReplayFixtureV1, error) {
	if r == nil {
		return nil, fmt.Errorf("eval: replay fixture reader is nil")
	}
	decoder := json.NewDecoder(r)
	var fixtures []ReplayFixtureV1
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixtures); err != nil {
		return nil, fmt.Errorf("eval: decode replay fixtures: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, fmt.Errorf("eval: decode replay fixtures: %w", err)
	}
	seen := make(map[string]struct{}, len(fixtures))
	for _, fixture := range fixtures {
		if err := fixture.Validate(); err != nil {
			return nil, err
		}
		actual, err := Replay(fixture)
		if err != nil {
			return nil, err
		}
		if err := validateReplayExpectation(fixture, actual); err != nil {
			return nil, err
		}
		if _, exists := seen[fixture.Name]; exists {
			return nil, fmt.Errorf("eval: duplicate replay fixture %q", fixture.Name)
		}
		seen[fixture.Name] = struct{}{}
	}
	return fixtures, nil
}

func WriteReplayFixtures(w io.Writer, fixtures []ReplayFixtureV1) error {
	if w == nil || len(fixtures) == 0 {
		return fmt.Errorf("eval: replay fixture writer requires fixtures")
	}
	seen := make(map[string]struct{}, len(fixtures))
	for _, fixture := range fixtures {
		if err := fixture.Validate(); err != nil {
			return err
		}
		if _, exists := seen[fixture.Name]; exists {
			return fmt.Errorf("eval: duplicate replay fixture %q", fixture.Name)
		}
		seen[fixture.Name] = struct{}{}
		actual, err := Replay(fixture)
		if err != nil {
			return err
		}
		if err := validateReplayExpectation(fixture, actual); err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(fixtures); err != nil {
		return fmt.Errorf("eval: encode replay fixtures: %w", err)
	}
	return nil
}

func (f ReplayFixtureV1) Validate() error {
	if f.Version != ReplayFixtureVersionV1 || f.Name == "" || f.Scenario == "" || len(f.Steps) == 0 {
		return fmt.Errorf("eval: invalid replay fixture identity")
	}
	if err := validateReplayStateHashes(f.Initial); err != nil {
		return fmt.Errorf("eval: fixture %s initial state: %w", f.Name, err)
	}
	if err := validateReplayStateHashes(f.Expected); err != nil {
		return fmt.Errorf("eval: fixture %s expected state: %w", f.Name, err)
	}
	if f.Expected.RunState != "completed" && f.Expected.RunState != "failed" && f.Expected.RunState != "cancelled" {
		return fmt.Errorf("eval: fixture %s expected state is not terminal", f.Name)
	}
	var sequence int64
	seen := make(map[string]struct{}, len(f.Steps))
	for _, step := range f.Steps {
		if step.Sequence <= sequence || step.ID == "" || step.Kind == "" {
			return fmt.Errorf("eval: fixture %s has invalid step order", f.Name)
		}
		if _, exists := seen[step.ID]; exists {
			return fmt.Errorf("eval: fixture %s repeats step %s", f.Name, step.ID)
		}
		if step.Kind == "artifact" && !validReplayDigest(step.SHA256) {
			return fmt.Errorf("eval: fixture %s artifact %s has invalid hash", f.Name, step.ID)
		}
		if step.Evidence != nil && step.Evidence.SHA256 != "" && !validReplayDigest(step.Evidence.SHA256) {
			return fmt.Errorf("eval: fixture %s evidence %s has invalid hash", f.Name, step.ID)
		}
		sequence = step.Sequence
		seen[step.ID] = struct{}{}
	}
	return nil
}

func Replay(fixture ReplayFixtureV1) (ReplayStateV1, error) {
	if err := fixture.Validate(); err != nil {
		return ReplayStateV1{}, err
	}
	state := cloneReplayState(fixture.Initial)
	for _, step := range fixture.Steps {
		state.OrderedStepIDs = append(state.OrderedStepIDs, step.ID)
		var err error
		switch step.Kind {
		case "run":
			err = applyRunStep(&state, step)
		case "text":
			err = applyTextStep(&state, step)
		case "tool":
			err = applyTransition(state.ToolStates, step.TargetID, step.State, toolTransitionAllowed)
		case "approval":
			err = applyTransition(state.ApprovalStates, step.TargetID, step.State, approvalTransitionAllowed)
		case "artifact":
			if step.TargetID == "" || !validReplayDigest(step.SHA256) {
				err = fmt.Errorf("artifact step requires target and valid hash")
			} else {
				state.ArtifactHashes[step.TargetID] = step.SHA256
			}
		case "subagent":
			err = applyTransition(state.SubagentStates, step.TargetID, step.State, subagentTransitionAllowed)
		case "compaction":
			if step.Revision <= state.SemanticRevision {
				err = fmt.Errorf("semantic revision did not advance")
			} else {
				state.SemanticRevision = step.Revision
			}
		case "guidance":
			err = applyGuidanceStep(&state, step)
		case "evidence":
			if step.Evidence == nil || step.Evidence.Kind == "" || step.Evidence.ID == "" {
				err = fmt.Errorf("evidence step is incomplete")
			} else if step.Evidence.Kind == "artifact" && (step.Evidence.SHA256 == "" || state.ArtifactHashes[step.Evidence.ID] != step.Evidence.SHA256) {
				err = fmt.Errorf("artifact evidence does not match stored hash")
			} else {
				state.Evidence = append(state.Evidence, *step.Evidence)
			}
		default:
			err = fmt.Errorf("unknown step kind %q", step.Kind)
		}
		if err != nil {
			return ReplayStateV1{}, fmt.Errorf("eval: replay %s step %s: %w", fixture.Name, step.ID, err)
		}
	}
	if err := validateReplayExpectation(fixture, state); err != nil {
		return ReplayStateV1{}, err
	}
	return state, nil
}

func validateReplayExpectation(fixture ReplayFixtureV1, actual ReplayStateV1) error {
	if fixture.Expected.RunState != "completed" && fixture.Expected.RunState != "failed" && fixture.Expected.RunState != "cancelled" {
		return fmt.Errorf("eval: fixture %s expected state is not terminal", fixture.Name)
	}
	if !reflect.DeepEqual(actual, fixture.Expected) {
		return fmt.Errorf("eval: replay fixture %s does not match expected state", fixture.Name)
	}
	return nil
}

func validateReplayStateHashes(state ReplayStateV1) error {
	for id, digest := range state.ArtifactHashes {
		if id == "" || !validReplayDigest(digest) {
			return fmt.Errorf("artifact %s has invalid hash", id)
		}
	}
	for _, evidence := range state.Evidence {
		if evidence.Kind == "" || evidence.ID == "" {
			return fmt.Errorf("evidence reference is incomplete")
		}
		if evidence.SHA256 != "" && !validReplayDigest(evidence.SHA256) {
			return fmt.Errorf("evidence %s has invalid hash", evidence.ID)
		}
		if evidence.Kind == "artifact" && (evidence.SHA256 == "" || state.ArtifactHashes[evidence.ID] != evidence.SHA256) {
			return fmt.Errorf("artifact evidence %s does not match stored hash", evidence.ID)
		}
	}
	return nil
}

func validReplayDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func cloneReplayState(source ReplayStateV1) ReplayStateV1 {
	return ReplayStateV1{
		OrderedStepIDs:   append([]string{}, source.OrderedStepIDs...),
		TextPhases:       append([]string{}, source.TextPhases...),
		RunState:         source.RunState,
		ToolStates:       cloneStringMap(source.ToolStates),
		ApprovalStates:   cloneStringMap(source.ApprovalStates),
		ArtifactHashes:   cloneStringMap(source.ArtifactHashes),
		SubagentStates:   cloneStringMap(source.SubagentStates),
		SemanticRevision: source.SemanticRevision,
		GuidanceRevision: source.GuidanceRevision,
		StaleGuidance:    append([]string{}, source.StaleGuidance...),
		Evidence:         append([]ReplayEvidenceRefV1{}, source.Evidence...),
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func applyRunStep(state *ReplayStateV1, step ReplayStepV1) error {
	if !runTransitionAllowed(state.RunState, step.State) {
		return fmt.Errorf("invalid run transition %q -> %q", state.RunState, step.State)
	}
	state.RunState = step.State
	return nil
}

func applyTextStep(state *ReplayStateV1, step ReplayStepV1) error {
	if step.TargetID == "" || (step.Phase != "commentary" && step.Phase != "final_answer") {
		return fmt.Errorf("invalid text phase")
	}
	state.TextPhases = append(state.TextPhases, step.TargetID+":"+step.Phase)
	return nil
}

func applyTransition(states map[string]string, target, next string, allowed func(string, string) bool) error {
	if target == "" || !allowed(states[target], next) {
		return fmt.Errorf("invalid %s transition %q -> %q", target, states[target], next)
	}
	states[target] = next
	return nil
}

func applyGuidanceStep(state *ReplayStateV1, step ReplayStepV1) error {
	if step.TargetID == "" || step.Revision <= 0 || (step.State != "active" && step.State != "stale") {
		return fmt.Errorf("invalid guidance step")
	}
	if step.State == "stale" {
		if step.Revision >= state.GuidanceRevision {
			return fmt.Errorf("stale guidance revision %d is not older than active revision %d", step.Revision, state.GuidanceRevision)
		}
		state.StaleGuidance = append(state.StaleGuidance, step.TargetID)
		return nil
	}
	if step.Revision <= state.GuidanceRevision {
		return fmt.Errorf("active guidance revision did not advance")
	}
	state.GuidanceRevision = step.Revision
	return nil
}

func runTransitionAllowed(current, next string) bool {
	switch current {
	case "":
		return next == "running" || next == "recovered"
	case "running":
		return next == "retrying" || next == "completed" || next == "failed" || next == "cancelled"
	case "retrying", "recovered":
		return next == "running" || next == "completed" || next == "failed" || next == "cancelled"
	default:
		return false
	}
}

func toolTransitionAllowed(current, next string) bool {
	switch current {
	case "":
		return next == "queued" || next == "running"
	case "queued":
		return next == "awaiting_approval" || next == "running" || next == "cancelled"
	case "awaiting_approval":
		return next == "running" || next == "cancelled"
	case "running":
		return next == "completed" || next == "failed" || next == "timeout" || next == "cancelled"
	default:
		return false
	}
}

func approvalTransitionAllowed(current, next string) bool {
	switch current {
	case "":
		return next == "requested"
	case "requested":
		return next == "approved" || next == "denied" || next == "cancelled"
	default:
		return false
	}
}

func subagentTransitionAllowed(current, next string) bool {
	switch current {
	case "":
		return next == "running"
	case "running":
		return next == "completed" || next == "failed" || next == "cancelled"
	default:
		return false
	}
}
