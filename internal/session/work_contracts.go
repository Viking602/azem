package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
	"time"
)

const WorkContractVersionV1 = 1

var ErrInvalidWorkContract = errors.New("session: invalid work contract")

// SourceRefV1 points at durable evidence without copying its payload into every
// work record. Kind and ID are interpreted by the producer that owns the
// referenced store; SHA256 makes byte-bearing references independently
// verifiable.
type SourceRefV1 struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Range  string `json:"range,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}
type WorkRevisionFileV1 struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Observed bool   `json:"observed,omitempty"`
	Touched  bool   `json:"touched,omitempty"`
}

// WorkRevisionV1 binds asynchronous work to bounded durable state. Files are
// limited to paths already observed or touched by the run; callers must not
// scan the repository to populate this record.
type WorkRevisionV1 struct {
	Version               int                  `json:"version"`
	ID                    string               `json:"id"`
	SessionID             string               `json:"session_id"`
	CanonicalUserSequence int64                `json:"canonical_user_sequence"`
	TodoRevision          int64                `json:"todo_revision"`
	SemanticRevision      int64                `json:"semantic_revision"`
	ApprovedPlanID        string               `json:"approved_plan_id,omitempty"`
	ConstraintDigest      string               `json:"constraint_digest"`
	Constraints           []CriterionV1        `json:"constraints,omitempty"`
	GitHEAD               string               `json:"git_head,omitempty"`
	Files                 []WorkRevisionFileV1 `json:"files,omitempty"`
	SnapshotHash          string               `json:"snapshot_hash"`
	CreatedAt             time.Time            `json:"created_at"`
}

type CriterionV1 struct {
	ID       string        `json:"id"`
	Text     string        `json:"text"`
	Origin   string        `json:"origin"` // explicit or inferred
	Required bool          `json:"required"`
	Sources  []SourceRefV1 `json:"sources,omitempty"`
}

// WorkSpecV1 is the immutable, user-scoped statement of work used by
// verification and offline replay. Inferred criteria are recorded but cannot
// replace or weaken explicit criteria.
type WorkSpecV1 struct {
	Version            int           `json:"version"`
	ID                 string        `json:"id"`
	SessionID          string        `json:"session_id"`
	RunID              string        `json:"run_id,omitempty"`
	Goal               string        `json:"goal"`
	ApprovedPlanID     string        `json:"approved_plan_id,omitempty"`
	Criteria           []CriterionV1 `json:"criteria,omitempty"`
	Constraints        []CriterionV1 `json:"constraints,omitempty"`
	AllowedSideEffects []string      `json:"allowed_side_effects,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
}

// ActionIntentV1 records what an agent intended to execute before the result
// was known. ArgumentsHash identifies canonical arguments without duplicating
// secrets or large tool inputs.
type ActionIntentV1 struct {
	Version       int           `json:"version"`
	ID            string        `json:"id"`
	WorkSpecID    string        `json:"work_spec_id"`
	SessionID     string        `json:"session_id"`
	RunID         string        `json:"run_id"`
	AgentID       string        `json:"agent_id,omitempty"`
	ToolCallID    string        `json:"tool_call_id,omitempty"`
	Kind          string        `json:"kind"`
	Name          string        `json:"name"`
	ArgumentsHash string        `json:"arguments_hash,omitempty"`
	RevisionID    string        `json:"revision_id,omitempty"`
	SnapshotHash  string        `json:"snapshot_hash,omitempty"`
	Sources       []SourceRefV1 `json:"sources,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
}

type FileObservationV1 struct {
	Path         string `json:"path"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256,omitempty"`
	State        string `json:"state"` // observed, created, modified, deleted, or unknown
}

// ObservationEnvelopeV1 adds host-known reliability metadata around an
// unchanged raw result. Unknown metadata remains explicitly unknown.
type ObservationEnvelopeV1 struct {
	Version         int                 `json:"version"`
	ID              string              `json:"id"`
	IntentID        string              `json:"intent_id,omitempty"`
	SessionID       string              `json:"session_id"`
	RunID           string              `json:"run_id"`
	ToolCallID      string              `json:"tool_call_id,omitempty"`
	RevisionID      string              `json:"revision_id,omitempty"`
	SnapshotHash    string              `json:"snapshot_hash,omitempty"`
	Status          string              `json:"status"` // completed, failed, timeout, cancelled, or unknown
	ExitStatus      *int                `json:"exit_status,omitempty"`
	ContentPresence string              `json:"content_presence"` // present, empty, or missing
	Truncation      string              `json:"truncation"`       // none, truncated, spilled, or unknown
	SchemaValidity  string              `json:"schema_validity"`  // valid, invalid, or unknown
	Retryability    string              `json:"retryability"`     // retryable, terminal, or unknown
	FreshAt         time.Time           `json:"fresh_at,omitempty"`
	RawSHA256       string              `json:"raw_sha256,omitempty"`
	RawRef          *SourceRefV1        `json:"raw_ref,omitempty"`
	Files           []FileObservationV1 `json:"files,omitempty"`
	Sources         []SourceRefV1       `json:"sources,omitempty"`
}

type VerificationCheckV1 struct {
	ID           string            `json:"id"`
	CriterionIDs []string          `json:"criterion_ids"`
	Kind         string            `json:"kind"`
	Command      []string          `json:"command,omitempty"`
	CWD          string            `json:"cwd,omitempty"`
	Environment  map[string]string `json:"environment,omitempty"`
	ArtifactRef  string            `json:"artifact_ref,omitempty"`
	TimeoutMS    int64             `json:"timeout_ms,omitempty"`
}

// VerificationPlanV1 links every deterministic or semantic check to one or
// more acceptance criteria and the workspace snapshot it is meant to verify.
type VerificationPlanV1 struct {
	Version      int                   `json:"version"`
	ID           string                `json:"id"`
	WorkSpecID   string                `json:"work_spec_id"`
	RevisionID   string                `json:"revision_id"`
	SnapshotHash string                `json:"snapshot_hash"`
	Checks       []VerificationCheckV1 `json:"checks"`
	CreatedAt    time.Time             `json:"created_at"`
}

type CriterionResultV1 struct {
	CriterionID string        `json:"criterion_id"`
	Status      string        `json:"status"` // pass, fail, or uncertain
	Evidence    []SourceRefV1 `json:"evidence,omitempty"`
	Reason      string        `json:"reason,omitempty"`
}

// VerificationResultV1 is compatible with a final claim only while its
// RevisionID and SnapshotHash still identify the active workspace.
type VerificationResultV1 struct {
	Version      int                 `json:"version"`
	ID           string              `json:"id"`
	PlanID       string              `json:"plan_id"`
	WorkSpecID   string              `json:"work_spec_id"`
	RevisionID   string              `json:"revision_id"`
	SnapshotHash string              `json:"snapshot_hash"`
	Status       string              `json:"status"` // pass, fail, or uncertain
	Criteria     []CriterionResultV1 `json:"criteria"`
	StartedAt    time.Time           `json:"started_at,omitempty"`
	CompletedAt  time.Time           `json:"completed_at"`
}

// ExperienceCandidateV1 is evidence-backed proposed memory. Promotion is a
// separate, explicit action; Candidate records never become active memory by
// themselves.
type ExperienceCandidateV1 struct {
	Version    int           `json:"version"`
	ID         string        `json:"id"`
	Workspace  string        `json:"workspace"`
	Kind       string        `json:"kind"`
	Content    string        `json:"content"`
	Scope      string        `json:"scope"`
	Confidence string        `json:"confidence"`
	Status     string        `json:"status"`
	ExpiresAt  *time.Time    `json:"expires_at,omitempty"`
	Supersedes []string      `json:"supersedes,omitempty"`
	Evidence   []SourceRefV1 `json:"evidence"`
	CreatedAt  time.Time     `json:"created_at"`
}

// RouteOutcomeV1 is the versioned training/shadow-routing outcome. It records
// observed execution only; it grants no admission, retry, or policy authority.
type RouteOutcomeV1 struct {
	Version           int           `json:"version"`
	ID                string        `json:"id"`
	WorkSpecID        string        `json:"work_spec_id"`
	TaskID            string        `json:"task_id,omitempty"`
	TaskClass         string        `json:"task_class"`
	Repository        string        `json:"repository"`
	Toolchain         string        `json:"toolchain"`
	PolicyVersion     string        `json:"policy_version"`
	Provider          string        `json:"provider"`
	Model             string        `json:"model"`
	Budget            string        `json:"budget,omitempty"`
	Split             string        `json:"split,omitempty"`
	Status            string        `json:"status"`                // pass, fail, uncertain, or cancelled
	Correctness       string        `json:"correctness,omitempty"` // pass, fail, or uncertain
	CriterionCoverage float64       `json:"criterion_coverage,omitempty"`
	PredictedSuccess  float64       `json:"predicted_success,omitempty"`
	FalsePass         bool          `json:"false_pass,omitempty"`
	FalseFail         bool          `json:"false_fail,omitempty"`
	InputTokens       int64         `json:"input_tokens,omitempty"`
	OutputTokens      int64         `json:"output_tokens,omitempty"`
	CacheReadTokens   int64         `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens  int64         `json:"cache_write_tokens,omitempty"`
	CostMicros        int64         `json:"cost_micros,omitempty"`
	LatencyMS         int64         `json:"latency_ms,omitempty"`
	Retries           int           `json:"retries,omitempty"`
	Recovered         bool          `json:"recovered,omitempty"`
	SafetyEvents      []string      `json:"safety_events,omitempty"`
	RetentionEvents   []string      `json:"retention_events,omitempty"`
	Evidence          []SourceRefV1 `json:"evidence,omitempty"`
	CompletedAt       time.Time     `json:"completed_at"`
}

func (v SourceRefV1) Validate() error {
	if strings.TrimSpace(v.Kind) == "" || strings.TrimSpace(v.ID) == "" {
		return fmt.Errorf("%w: source reference requires kind and id", ErrInvalidWorkContract)
	}
	return nil
}

func (v WorkSpecV1) Validate() error {
	if err := requireV1(v.Version, v.ID, "work spec"); err != nil {
		return err
	}
	if v.ID != CanonicalWorkSpecID(v) {
		return fmt.Errorf("%w: work spec identity does not match canonical content", ErrInvalidWorkContract)
	}
	if v.SessionID == "" || strings.TrimSpace(v.Goal) == "" || v.CreatedAt.IsZero() || len(v.Criteria) == 0 {
		return fmt.Errorf("%w: work spec requires session_id, goal, created_at, and criteria", ErrInvalidWorkContract)
	}
	seen := make(map[string]struct{}, len(v.Criteria)+len(v.Constraints))
	validateOrdered := func(criteria []CriterionV1) error {
		for _, criterion := range criteria {
			if criterion.ID == "" || strings.TrimSpace(criterion.Text) == "" || (criterion.Origin != "explicit" && criterion.Origin != "inferred") {
				return fmt.Errorf("%w: invalid criterion", ErrInvalidWorkContract)
			}
			if _, exists := seen[criterion.ID]; exists {
				return fmt.Errorf("%w: duplicate criterion %q", ErrInvalidWorkContract, criterion.ID)
			}
			seen[criterion.ID] = struct{}{}
			for _, source := range criterion.Sources {
				if err := validateSourceDigest(source); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := validateOrdered(v.Criteria); err != nil {
		return err
	}
	if err := validateOrdered(v.Constraints); err != nil {
		return err
	}
	return nil
}

func validateSourceDigest(source SourceRefV1) error {
	if err := source.Validate(); err != nil {
		return err
	}
	return validateDigest(source.SHA256, "source")
}

func CanonicalWorkSpecID(v WorkSpecV1) string {
	payload := struct {
		Version            int           `json:"version"`
		SessionID          string        `json:"session_id"`
		RunID              string        `json:"run_id"`
		Goal               string        `json:"goal"`
		ApprovedPlanID     string        `json:"approved_plan_id"`
		Criteria           []CriterionV1 `json:"criteria"`
		Constraints        []CriterionV1 `json:"constraints"`
		AllowedSideEffects []string      `json:"allowed_side_effects"`
	}{
		Version: v.Version, SessionID: strings.TrimSpace(v.SessionID), RunID: strings.TrimSpace(v.RunID),
		Goal: strings.TrimSpace(v.Goal), ApprovedPlanID: strings.TrimSpace(v.ApprovedPlanID),
		Criteria: canonicalCriteria(v.Criteria), Constraints: canonicalCriteria(v.Constraints),
		AllowedSideEffects: append([]string(nil), v.AllowedSideEffects...),
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return "work:" + hex.EncodeToString(sum[:])[:16]
}

func CanonicalVerificationPlanID(v VerificationPlanV1) string {
	sum := sha256.Sum256([]byte(v.WorkSpecID + ":" + v.RevisionID + ":" + v.SnapshotHash))
	return "verification:" + hex.EncodeToString(sum[:])[:16]
}

func canonicalCriteria(criteria []CriterionV1) []CriterionV1 {
	result := append([]CriterionV1(nil), criteria...)
	for i := range result {
		result[i].Sources = append([]SourceRefV1(nil), result[i].Sources...)
		sort.Slice(result[i].Sources, func(a, b int) bool {
			return sourceKey(result[i].Sources[a]) < sourceKey(result[i].Sources[b])
		})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func sourceKey(source SourceRefV1) string {
	return source.Kind + "\x00" + source.ID + "\x00" + source.Range + "\x00" + source.SHA256
}

func validateCriterionSources(sources []SourceRefV1) error {
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if err := validateSourceDigest(source); err != nil {
			return err
		}
		key := sourceKey(source)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate source reference", ErrInvalidWorkContract)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validRelativePath(value string) bool {
	clean := path.Clean(strings.TrimSpace(value))
	return clean != "" && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && !strings.HasPrefix(clean, "/")
}

func validateDigest(value, label string) error {
	if value == "" {
		return nil
	}
	digest, err := hex.DecodeString(value)
	if err != nil || len(digest) != sha256.Size || value != strings.ToLower(value) {
		return fmt.Errorf("%w: invalid %s sha256", ErrInvalidWorkContract, label)
	}
	return nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func (v ActionIntentV1) Validate() error {
	if err := requireV1(v.Version, v.ID, "action intent"); err != nil {
		return err
	}
	if v.WorkSpecID == "" || v.SessionID == "" || v.RunID == "" || v.Kind == "" || v.Name == "" || v.CreatedAt.IsZero() {
		return fmt.Errorf("%w: incomplete action intent", ErrInvalidWorkContract)
	}
	if err := validateDigest(v.ArgumentsHash, "arguments"); err != nil {
		return err
	}
	return validateCriterionSources(v.Sources)
}

func (v ObservationEnvelopeV1) Validate() error {
	if err := requireV1(v.Version, v.ID, "observation"); err != nil {
		return err
	}
	if v.SessionID == "" || v.RunID == "" || !oneOf(v.Status, "completed", "failed", "timeout", "cancelled", "unknown") ||
		!oneOf(v.ContentPresence, "present", "empty", "missing") ||
		!oneOf(v.Truncation, "none", "truncated", "spilled", "unknown") ||
		!oneOf(v.SchemaValidity, "valid", "invalid", "unknown") ||
		!oneOf(v.Retryability, "retryable", "terminal", "unknown") {
		return fmt.Errorf("%w: invalid observation reliability metadata", ErrInvalidWorkContract)
	}
	if err := validateDigest(v.RawSHA256, "raw"); err != nil {
		return err
	}
	if v.RawRef != nil {
		if err := v.RawRef.Validate(); err != nil {
			return err
		}
	}
	if err := validateCriterionSources(v.Sources); err != nil {
		return err
	}
	for _, file := range v.Files {
		if !validRelativePath(file.Path) || !oneOf(file.State, "observed", "created", "modified", "deleted", "unknown") {
			return fmt.Errorf("%w: invalid file observation", ErrInvalidWorkContract)
		}
		if err := validateDigest(file.BeforeSHA256, "before file"); err != nil {
			return err
		}
		if err := validateDigest(file.AfterSHA256, "after file"); err != nil {
			return err
		}
	}
	return nil
}

func (v VerificationPlanV1) Validate() error {
	if err := requireV1(v.Version, v.ID, "verification plan"); err != nil {
		return err
	}
	if strings.HasPrefix(v.ID, "verification:") && v.ID != CanonicalVerificationPlanID(v) {
		return fmt.Errorf("%w: verification plan identity mismatch", ErrInvalidWorkContract)
	}
	if v.WorkSpecID == "" || v.RevisionID == "" || v.SnapshotHash == "" || len(v.Checks) == 0 || v.CreatedAt.IsZero() {
		return fmt.Errorf("%w: incomplete verification plan", ErrInvalidWorkContract)
	}
	seenChecks := make(map[string]struct{}, len(v.Checks))
	for _, check := range v.Checks {
		if check.ID == "" || check.Kind == "" || len(check.CriterionIDs) == 0 || check.TimeoutMS < 0 {
			return fmt.Errorf("%w: invalid verification check", ErrInvalidWorkContract)
		}
		if _, exists := seenChecks[check.ID]; exists {
			return fmt.Errorf("%w: duplicate verification check %q", ErrInvalidWorkContract, check.ID)
		}
		seenChecks[check.ID] = struct{}{}
		seenCriteria := make(map[string]struct{}, len(check.CriterionIDs))
		for _, criterionID := range check.CriterionIDs {
			if criterionID == "" {
				return fmt.Errorf("%w: empty criterion reference", ErrInvalidWorkContract)
			}
			if _, exists := seenCriteria[criterionID]; exists {
				return fmt.Errorf("%w: duplicate criterion reference %q", ErrInvalidWorkContract, criterionID)
			}
			seenCriteria[criterionID] = struct{}{}
		}
	}
	return nil
}

func (v VerificationResultV1) Validate() error {
	if err := requireV1(v.Version, v.ID, "verification result"); err != nil {
		return err
	}
	if v.PlanID == "" || v.WorkSpecID == "" || v.RevisionID == "" || v.SnapshotHash == "" ||
		!oneOf(v.Status, "pass", "fail", "uncertain") || len(v.Criteria) == 0 || v.CompletedAt.IsZero() {
		return fmt.Errorf("%w: incomplete verification result", ErrInvalidWorkContract)
	}
	seen := make(map[string]struct{}, len(v.Criteria))
	for _, result := range v.Criteria {
		if result.CriterionID == "" || !oneOf(result.Status, "pass", "fail", "uncertain") || len(result.Evidence) == 0 {
			return fmt.Errorf("%w: invalid criterion result", ErrInvalidWorkContract)
		}
		if _, exists := seen[result.CriterionID]; exists {
			return fmt.Errorf("%w: duplicate criterion result %q", ErrInvalidWorkContract, result.CriterionID)
		}
		seen[result.CriterionID] = struct{}{}
		if err := validateCriterionSources(result.Evidence); err != nil {
			return err
		}
	}
	return nil
}

func (v ExperienceCandidateV1) Validate() error {
	if err := requireV1(v.Version, v.ID, "experience candidate"); err != nil {
		return err
	}
	if v.Workspace == "" || v.Kind == "" || v.Content == "" || v.Scope == "" || v.Status != "candidate" || len(v.Evidence) == 0 || v.CreatedAt.IsZero() {
		return fmt.Errorf("%w: incomplete experience candidate", ErrInvalidWorkContract)
	}
	for _, source := range v.Evidence {
		if err := source.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (v RouteOutcomeV1) Validate() error {
	if err := requireV1(v.Version, v.ID, "route outcome"); err != nil {
		return err
	}
	if v.WorkSpecID == "" || v.Provider == "" || v.Model == "" || !oneOf(v.Status, "pass", "fail", "uncertain", "cancelled") || v.CompletedAt.IsZero() {
		return fmt.Errorf("%w: incomplete route outcome", ErrInvalidWorkContract)
	}
	if v.Correctness != "" && !oneOf(v.Correctness, "pass", "fail", "uncertain") {
		return fmt.Errorf("%w: invalid route correctness", ErrInvalidWorkContract)
	}
	if !finite(v.CriterionCoverage) || !finite(v.PredictedSuccess) ||
		v.CriterionCoverage < 0 || v.CriterionCoverage > 1 || v.PredictedSuccess < 0 || v.PredictedSuccess > 1 ||
		v.FalsePass && v.FalseFail || v.InputTokens < 0 || v.OutputTokens < 0 || v.CacheReadTokens < 0 ||
		v.CacheWriteTokens < 0 || v.CostMicros < 0 || v.LatencyMS < 0 || v.Retries < 0 {
		return fmt.Errorf("%w: invalid route metrics", ErrInvalidWorkContract)
	}
	if v.Status == "pass" && (v.InputTokens <= 0 || v.OutputTokens <= 0 || v.CostMicros <= 0 || v.LatencyMS <= 0) {
		return fmt.Errorf("%w: passing route outcome requires complete positive accounting", ErrInvalidWorkContract)
	}
	seen := make(map[string]struct{}, len(v.Evidence))
	for _, source := range v.Evidence {
		if err := source.Validate(); err != nil {
			return err
		}
		key := sourceKey(source)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate route evidence", ErrInvalidWorkContract)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (v WorkRevisionV1) Validate() error {
	if err := requireV1(v.Version, v.ID, "work revision"); err != nil {
		return err
	}
	if v.SessionID == "" || v.CanonicalUserSequence < 0 || v.TodoRevision < 0 || v.SemanticRevision < 0 ||
		v.ConstraintDigest == "" || v.SnapshotHash == "" || v.CreatedAt.IsZero() {
		return fmt.Errorf("%w: incomplete work revision", ErrInvalidWorkContract)
	}
	constraints, err := canonicalRevisionConstraints(v.Constraints)
	if err != nil {
		return err
	}
	if v.ConstraintDigest != digestJSON(constraints) {
		return fmt.Errorf("%w: work revision constraint digest mismatch", ErrInvalidWorkContract)
	}
	files, err := canonicalRevisionFiles(v.Files)
	if err != nil {
		return err
	}
	for i, file := range files {
		if i >= len(v.Files) || v.Files[i] != file {
			return fmt.Errorf("%w: work revision files are not canonical", ErrInvalidWorkContract)
		}
	}
	if v.SnapshotHash != digestJSON(struct {
		GitHEAD string               `json:"git_head"`
		Files   []WorkRevisionFileV1 `json:"files"`
	}{GitHEAD: strings.TrimSpace(v.GitHEAD), Files: files}) {
		return fmt.Errorf("%w: work revision snapshot hash mismatch", ErrInvalidWorkContract)
	}
	expected := "work-revision:" + digestJSON(struct {
		SessionID             string `json:"session_id"`
		CanonicalUserSequence int64  `json:"canonical_user_sequence"`
		TodoRevision          int64  `json:"todo_revision"`
		SemanticRevision      int64  `json:"semantic_revision"`
		ApprovedPlanID        string `json:"approved_plan_id"`
		ConstraintDigest      string `json:"constraint_digest"`
		SnapshotHash          string `json:"snapshot_hash"`
	}{v.SessionID, v.CanonicalUserSequence, v.TodoRevision, v.SemanticRevision, strings.TrimSpace(v.ApprovedPlanID), v.ConstraintDigest, v.SnapshotHash})[:24]
	if v.ID != expected {
		return fmt.Errorf("%w: work revision identity mismatch", ErrInvalidWorkContract)
	}
	return nil
}

func canonicalRevisionConstraints(criteria []CriterionV1) ([]CriterionV1, error) {
	result := make([]CriterionV1, 0, len(criteria))
	seen := make(map[string]struct{}, len(criteria))
	for _, criterion := range criteria {
		if criterion.Origin != "explicit" {
			continue
		}
		if criterion.ID == "" || strings.TrimSpace(criterion.Text) == "" {
			return nil, fmt.Errorf("%w: invalid revision constraint", ErrInvalidWorkContract)
		}
		if _, exists := seen[criterion.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate revision constraint %q", ErrInvalidWorkContract, criterion.ID)
		}
		seen[criterion.ID] = struct{}{}
		for _, source := range criterion.Sources {
			if err := source.Validate(); err != nil {
				return nil, err
			}
		}
		result = append(result, criterion)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func canonicalRevisionFiles(files []WorkRevisionFileV1) ([]WorkRevisionFileV1, error) {
	if len(files) > 4096 {
		return nil, fmt.Errorf("%w: too many work revision files", ErrInvalidWorkContract)
	}
	result := make([]WorkRevisionFileV1, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.SHA256) == "" || !validRelativePath(file.Path) || (!file.Observed && !file.Touched) || validateDigest(file.SHA256, "file") != nil {
			return nil, fmt.Errorf("%w: invalid work revision file %q", ErrInvalidWorkContract, file.Path)
		}
		file.Path = path.Clean(strings.TrimSpace(file.Path))
		file.SHA256 = strings.ToLower(file.SHA256)
		if _, exists := seen[file.Path]; exists {
			return nil, fmt.Errorf("%w: duplicate work revision file %q", ErrInvalidWorkContract, file.Path)
		}
		seen[file.Path] = struct{}{}
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func digestJSON(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func requireV1(version int, id, name string) error {
	if version != WorkContractVersionV1 || id == "" {
		return fmt.Errorf("%w: %s requires version %d and id", ErrInvalidWorkContract, name, WorkContractVersionV1)
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
