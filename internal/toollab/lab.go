// Package toollab evaluates generated coding tools without exposing them to the
// production tool registry.
package toollab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const (
	CheckFuzz              = "fuzz"
	CheckStaticAnalysis    = "static_analysis"
	CheckPermissionAudit   = "permission_audit"
	CheckIsolatedExecution = "isolated_execution"

	// These are hard upper bounds even for a host policy. They keep malformed
	// artifacts from consuming unbounded memory before the lab starts.
	maxSourceFiles        = 64
	maxSourceFileBytes    = 1 << 20
	maxSourceBytes        = 8 << 20
	maxSchemaBytes        = 64 << 10
	maxSchemaProperties   = 128
	maxSchemaRequired     = 128
	maxDescriptionBytes   = 16 << 10
	maxPermissions        = 2
	maxCommandArgs        = 64
	maxCommandArgBytes    = 4 << 10
	maxCommandTimeoutMS   = 15 * 60 * 1000
	maxCommandOutputBytes = 32 << 10
)

var pinnedImagePattern = regexp.MustCompile(`^[^@]+@sha256:[0-9a-f]{64}$`)

type LabPolicyV1 struct {
	Version               int                     `json:"version"`
	TesterID              string                  `json:"tester_id"`
	Checks                map[string]LabCommandV1 `json:"checks"`
	MaxSourceFiles        int                     `json:"max_source_files"`
	MaxSourceFileBytes    int64                   `json:"max_source_file_bytes"`
	MaxSourceBytes        int64                   `json:"max_source_bytes"`
	MaxSchemaBytes        int64                   `json:"max_schema_bytes"`
	MaxSchemaProperties   int                     `json:"max_schema_properties"`
	MaxSchemaRequired     int                     `json:"max_schema_required"`
	MaxDescriptionBytes   int64                   `json:"max_description_bytes"`
	MaxPermissions        int                     `json:"max_permissions"`
	MaxCommandOutputBytes int64                   `json:"max_command_output_bytes"`
}

type HistoricalGapV1 struct {
	ID       string                `json:"id"`
	Summary  string                `json:"summary"`
	Evidence []session.SourceRefV1 `json:"evidence"`
}

type LabCommandV1 struct {
	Argv      []string `json:"argv"`
	TimeoutMS int64    `json:"timeout_ms"`
}

func DefaultLabPolicy() LabPolicyV1 {
	checks := make(map[string]LabCommandV1, 4)
	for _, check := range requiredChecks() {
		checks[check] = LabCommandV1{Argv: []string{"go", "test", "./..."}, TimeoutMS: 30_000}
	}
	return LabPolicyV1{
		Version: 1, Checks: checks,
		TesterID:       "azem-tool-lab",
		MaxSourceFiles: maxSourceFiles, MaxSourceFileBytes: maxSourceFileBytes, MaxSourceBytes: maxSourceBytes,
		MaxSchemaBytes: maxSchemaBytes, MaxSchemaProperties: maxSchemaProperties, MaxSchemaRequired: maxSchemaRequired,
		MaxDescriptionBytes: maxDescriptionBytes, MaxPermissions: maxPermissions,
		MaxCommandOutputBytes: maxCommandOutputBytes,
	}
}

type GeneratedToolInputV1 struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	InputSchema    json.RawMessage   `json:"input_schema"`
	Gap            HistoricalGapV1   `json:"gap"`
	ContainerImage string            `json:"container_image"`
	SourceFiles    map[string]string `json:"source_files"`
	Permissions    []string          `json:"permissions"`
}

type CandidateV1 struct {
	Version        int               `json:"version"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	InputSchema    json.RawMessage   `json:"input_schema"`
	Gap            HistoricalGapV1   `json:"gap"`
	ContainerImage string            `json:"container_image"`
	SourceFiles    map[string]string `json:"source_files"`
	Permissions    []string          `json:"permissions"`
	Installable    bool              `json:"installable"`
}

type CheckResultV1 struct {
	Check     string `json:"check"`
	Status    string `json:"status"`
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type LabReportV1 struct {
	Version      int             `json:"version"`
	CandidateID  string          `json:"candidate_id"`
	TesterID     string          `json:"tester_id"`
	PolicyDigest string          `json:"policy_digest"`
	Status       string          `json:"status"`
	Results      []CheckResultV1 `json:"results"`
	Installable  bool            `json:"installable"`
	Digest       string          `json:"digest"`
}

type HumanReviewV1 struct {
	Reviewer   string                `json:"reviewer"`
	Decision   string                `json:"decision"`
	Evidence   []session.SourceRefV1 `json:"evidence"`
	ReviewedAt time.Time             `json:"reviewed_at"`
}

type PromotedToolV1 struct {
	CandidateID string `json:"candidate_id"`
	LabDigest   string `json:"lab_digest"`
	Reviewer    string `json:"reviewer"`
	Installable bool   `json:"installable"`
}

type CheckRunner func(context.Context, CandidateV1, string, LabCommandV1) CheckResultV1

func GenerateCandidate(input GeneratedToolInputV1) (CandidateV1, error) {
	candidate := CandidateV1{
		Version: 1, Name: input.Name, Description: input.Description, InputSchema: append(json.RawMessage(nil), input.InputSchema...),
		Gap:            HistoricalGapV1{ID: input.Gap.ID, Summary: input.Gap.Summary, Evidence: append([]session.SourceRefV1(nil), input.Gap.Evidence...)},
		ContainerImage: input.ContainerImage, SourceFiles: cloneStrings(input.SourceFiles),
		Permissions: append([]string(nil), input.Permissions...), Installable: false,
	}
	candidate.ID = candidateIdentity(candidate)
	if err := candidate.Validate(); err != nil {
		return CandidateV1{}, err
	}
	return candidate, nil
}

func (candidate CandidateV1) Validate() error {
	if candidate.Version != 1 || candidate.ID == "" || candidate.ID != candidateIdentity(candidate) || !validToolName(candidate.Name) || strings.TrimSpace(candidate.Description) == "" || len(candidate.Description) > maxDescriptionBytes || candidate.Installable || !pinnedImagePattern.MatchString(candidate.ContainerImage) || len(candidate.SourceFiles) == 0 {
		return fmt.Errorf("tool lab: invalid candidate")
	}
	if candidate.Gap.ID == "" || strings.TrimSpace(candidate.Gap.Summary) == "" || len(candidate.Gap.Evidence) == 0 {
		return fmt.Errorf("tool lab: candidate lacks historical gap evidence")
	}
	for _, ref := range candidate.Gap.Evidence {
		if ref.Kind == "" || ref.ID == "" {
			return fmt.Errorf("tool lab: invalid historical gap evidence")
		}
	}
	if err := validateInputSchema(candidate.InputSchema, DefaultLabPolicy()); err != nil {
		return err
	}
	if err := validateCandidateLimits(candidate, DefaultLabPolicy()); err != nil {
		return err
	}
	permissions := make(map[string]struct{}, len(candidate.Permissions))
	for _, permission := range candidate.Permissions {
		if permission != "workspace_read" && permission != "workspace_write" {
			return fmt.Errorf("tool lab: forbidden permission %q", permission)
		}
		if _, duplicate := permissions[permission]; duplicate {
			return fmt.Errorf("tool lab: duplicate permission %q", permission)
		}
		permissions[permission] = struct{}{}
	}
	return nil
}

func (policy LabPolicyV1) Validate() error {
	if policy.Version != 1 || strings.TrimSpace(policy.TesterID) == "" || len(policy.Checks) != len(requiredChecks()) {
		return fmt.Errorf("tool lab: invalid host lab policy")
	}
	if policy.MaxSourceFiles <= 0 || policy.MaxSourceFiles > maxSourceFiles ||
		policy.MaxSourceFileBytes <= 0 || policy.MaxSourceFileBytes > maxSourceFileBytes ||
		policy.MaxSourceBytes <= 0 || policy.MaxSourceBytes > maxSourceBytes ||
		policy.MaxSchemaBytes <= 0 || policy.MaxSchemaBytes > maxSchemaBytes ||
		policy.MaxSchemaProperties <= 0 || policy.MaxSchemaProperties > maxSchemaProperties ||
		policy.MaxSchemaRequired <= 0 || policy.MaxSchemaRequired > maxSchemaRequired ||
		policy.MaxDescriptionBytes <= 0 || policy.MaxDescriptionBytes > maxDescriptionBytes ||
		policy.MaxPermissions < 0 || policy.MaxPermissions > maxPermissions ||
		policy.MaxCommandOutputBytes <= 0 || policy.MaxCommandOutputBytes > maxCommandOutputBytes {
		return fmt.Errorf("tool lab: host lab policy exceeds safety bounds")
	}
	for _, check := range requiredChecks() {
		command, exists := policy.Checks[check]
		if !exists || len(command.Argv) == 0 || len(command.Argv) > maxCommandArgs || command.TimeoutMS <= 0 || command.TimeoutMS > maxCommandTimeoutMS {
			return fmt.Errorf("tool lab: invalid %s host check", check)
		}
		for _, arg := range command.Argv {
			if arg == "" || len(arg) > maxCommandArgBytes {
				return fmt.Errorf("tool lab: invalid %s host command argument", check)
			}
		}
	}
	return nil
}

func Evaluate(ctx context.Context, candidate CandidateV1, policy LabPolicyV1, runner CheckRunner) (LabReportV1, error) {
	if err := candidate.Validate(); err != nil {
		return LabReportV1{}, err
	}
	if err := policy.Validate(); err != nil {
		return LabReportV1{}, err
	}
	if err := validateInputSchema(candidate.InputSchema, policy); err != nil {
		return LabReportV1{}, err
	}
	if err := validateCandidateLimits(candidate, policy); err != nil {
		return LabReportV1{}, err
	}
	if runner == nil {
		runner = func(ctx context.Context, candidate CandidateV1, check string, spec LabCommandV1) CheckResultV1 {
			return runIsolatedCheck(ctx, candidate, check, spec, policy.MaxCommandOutputBytes)
		}
	}
	policyDigest := policyIdentity(policy)
	report := LabReportV1{Version: 1, CandidateID: candidate.ID, PolicyDigest: policyDigest, TesterID: policy.TesterID, Status: "pass", Installable: false}
	for _, check := range requiredChecks() {
		result := runner(ctx, candidate, check, policy.Checks[check])
		result.Check = check
		result.Output, result.Truncated = boundOutput(result.Output, policy.MaxCommandOutputBytes, result.Truncated)
		if result.Status != "pass" {
			report.Status = "fail"
		}
		report.Results = append(report.Results, result)
	}
	report.Digest = reportIdentity(report.CandidateID, report.PolicyDigest, report.TesterID, report.Status, report.Results)
	return report, nil
}

func Promote(candidate CandidateV1, policy LabPolicyV1, report LabReportV1, review HumanReviewV1) (PromotedToolV1, error) {
	if err := candidate.Validate(); err != nil {
		return PromotedToolV1{}, err
	}
	if err := policy.Validate(); err != nil {
		return PromotedToolV1{}, err
	}
	if err := validateInputSchema(candidate.InputSchema, policy); err != nil {
		return PromotedToolV1{}, err
	}
	if err := validateCandidateLimits(candidate, policy); err != nil {
		return PromotedToolV1{}, err
	}
	policyDigest := policyIdentity(policy)
	if report.Version != 1 || report.CandidateID != candidate.ID || report.PolicyDigest != policyDigest || report.TesterID != policy.TesterID || report.Status != "pass" || report.Installable || report.Digest != reportIdentity(report.CandidateID, report.PolicyDigest, report.TesterID, report.Status, report.Results) || len(report.Results) != len(requiredChecks()) {
		return PromotedToolV1{}, fmt.Errorf("tool lab: candidate has not passed the complete laboratory")
	}
	seenChecks := make(map[string]struct{}, len(report.Results))
	for _, result := range report.Results {
		if int64(len(result.Output)) > policy.MaxCommandOutputBytes {
			return PromotedToolV1{}, fmt.Errorf("tool lab: laboratory output is oversized")
		}
		if result.Status != "pass" || !requiredCheck(result.Check) {
			return PromotedToolV1{}, fmt.Errorf("tool lab: laboratory result is not passing")
		}
		if _, duplicate := seenChecks[result.Check]; duplicate {
			return PromotedToolV1{}, fmt.Errorf("tool lab: duplicate laboratory result")
		}
		seenChecks[result.Check] = struct{}{}
	}
	if len(seenChecks) != len(requiredChecks()) {
		return PromotedToolV1{}, fmt.Errorf("tool lab: laboratory result is incomplete")
	}
	if strings.TrimSpace(review.Reviewer) == "" || review.Reviewer == report.TesterID || review.Decision != "approved" || review.ReviewedAt.IsZero() || len(review.Evidence) == 0 {
		return PromotedToolV1{}, fmt.Errorf("tool lab: human approval is required")
	}
	for _, ref := range review.Evidence {
		if ref.Kind == "" || ref.ID == "" {
			return PromotedToolV1{}, fmt.Errorf("tool lab: invalid review evidence")
		}
	}
	return PromotedToolV1{CandidateID: candidate.ID, LabDigest: report.Digest, Reviewer: review.Reviewer, Installable: true}, nil
}

func runIsolatedCheck(ctx context.Context, candidate CandidateV1, check string, spec LabCommandV1, outputLimit int64) CheckResultV1 {
	workspace, err := os.MkdirTemp("", "azem-tool-lab-")
	if err != nil {
		return CheckResultV1{Check: check, Status: "fail", ExitCode: -1, Output: err.Error()}
	}
	defer os.RemoveAll(workspace)
	for path, content := range candidate.SourceFiles {
		target := filepath.Join(workspace, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return CheckResultV1{Check: check, Status: "fail", ExitCode: -1, Output: err.Error()}
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return CheckResultV1{Check: check, Status: "fail", ExitCode: -1, Output: err.Error()}
		}
	}
	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutMS)*time.Millisecond)
	defer cancel()
	args := []string{
		"run", "--rm", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--cpus=1", "--memory=512m", "--pids-limit=128", "--tmpfs", "/tmp:rw,exec,nosuid,size=256m",
		"--mount", "type=bind,source=" + workspace + ",target=/workspace,readonly", "--workdir", "/workspace",
		"--env", "GOCACHE=/tmp/go-cache", "--env", "GOMODCACHE=/tmp/go-mod-cache", candidate.ContainerImage,
	}
	args = append(args, spec.Argv...)
	command := exec.CommandContext(commandCtx, "docker", args...)
	output := &boundedBuffer{limit: int(outputLimit)}
	command.Stdout, command.Stderr = output, output
	err = command.Run()
	result := CheckResultV1{Check: check, Status: "pass", Output: output.String(), Truncated: output.truncated}
	if err != nil {
		result.Status = "fail"
		result.ExitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		if commandCtx.Err() != nil {
			result.Output = strings.TrimSpace(result.Output + "\n" + commandCtx.Err().Error())
		}
	}
	return result
}

func validateInputSchema(raw json.RawMessage, policy LabPolicyV1) error {
	if len(raw) == 0 || int64(len(raw)) > policy.MaxSchemaBytes {
		return fmt.Errorf("tool lab: input schema is oversized")
	}
	var schema struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schema); err != nil || schema.Type != "object" || schema.Properties == nil || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		return fmt.Errorf("tool lab: input schema must be a closed JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("tool lab: input schema contains multiple JSON values")
		}
		return fmt.Errorf("tool lab: input schema contains trailing data")
	}
	if len(schema.Properties) > policy.MaxSchemaProperties || len(schema.Required) > policy.MaxSchemaRequired {
		return fmt.Errorf("tool lab: input schema contains too many properties")
	}
	for _, required := range schema.Required {
		if _, exists := schema.Properties[required]; !exists {
			return fmt.Errorf("tool lab: required property %q is undefined", required)
		}
	}
	return nil
}

func validateCandidateLimits(candidate CandidateV1, policy LabPolicyV1) error {
	if len(candidate.Description) > int(policy.MaxDescriptionBytes) {
		return fmt.Errorf("tool lab: description is oversized")
	}
	if len(candidate.SourceFiles) > policy.MaxSourceFiles {
		return fmt.Errorf("tool lab: too many source files")
	}
	var total int64
	for path, content := range candidate.SourceFiles {
		if err := validateRelativePath(path); err != nil {
			return err
		}
		if int64(len(content)) > policy.MaxSourceFileBytes {
			return fmt.Errorf("tool lab: source file %q is oversized", path)
		}
		total += int64(len(content))
		if total > policy.MaxSourceBytes {
			return fmt.Errorf("tool lab: source payload is oversized")
		}
	}
	if len(candidate.Permissions) > policy.MaxPermissions {
		return fmt.Errorf("tool lab: too many permissions")
	}
	return nil
}

func validToolName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, character := range name {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validateRelativePath(path string) error {
	clean := filepath.ToSlash(filepath.Clean(path))
	if path == "" || filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != filepath.ToSlash(path) {
		return fmt.Errorf("tool lab: unsafe source path %q", path)
	}
	return nil
}

func candidateIdentity(candidate CandidateV1) string {
	candidate.ID = ""
	identity, _ := json.Marshal(candidate)
	digest := sha256.Sum256(identity)
	return "tool-candidate:" + hex.EncodeToString(digest[:12])
}

func policyIdentity(policy LabPolicyV1) string {
	identity, _ := json.Marshal(policy)
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:])
}

func reportIdentity(candidateID, policyDigest, testerID, status string, results []CheckResultV1) string {
	identity, _ := json.Marshal(struct {
		CandidateID  string          `json:"candidate_id"`
		PolicyDigest string          `json:"policy_digest"`
		TesterID     string          `json:"tester_id"`
		Status       string          `json:"status"`
		Results      []CheckResultV1 `json:"results"`
	}{candidateID, policyDigest, testerID, status, results})
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:])
}

func requiredChecks() []string {
	return []string{CheckFuzz, CheckStaticAnalysis, CheckPermissionAudit, CheckIsolatedExecution}
}

func requiredCheck(check string) bool {
	switch check {
	case CheckFuzz, CheckStaticAnalysis, CheckPermissionAudit, CheckIsolatedExecution:
		return true
	default:
		return false
	}
}

func boundOutput(output string, limit int64, truncated bool) (string, bool) {
	if int64(len(output)) <= limit {
		return output, truncated
	}
	return output[:limit], true
}

func cloneStrings(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		clone[key] = source[key]
	}
	return clone
}

type boundedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (buffer *boundedBuffer) Write(payload []byte) (int, error) {
	original := len(payload)
	remaining := buffer.limit - len(buffer.data)
	if remaining > 0 {
		if len(payload) > remaining {
			payload = payload[:remaining]
		}
		buffer.data = append(buffer.data, payload...)
	}
	if original > remaining {
		buffer.truncated = true
	}
	return original, nil
}

func (buffer *boundedBuffer) String() string { return string(buffer.data) }
