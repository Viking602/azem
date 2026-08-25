package securityscan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ManifestDocument struct {
	DocumentType  string       `json:"documentType"`
	SchemaVersion string       `json:"schemaVersion"`
	Scan          ManifestScan `json:"scan"`
}

type ManifestScan struct {
	ID          string             `json:"id"`
	Producer    ManifestProducer   `json:"producer"`
	Status      string             `json:"status"`
	StartedAt   string             `json:"startedAt"`
	CompletedAt string             `json:"completedAt"`
	SealedAt    string             `json:"sealedAt"`
	Target      ManifestTarget     `json:"target"`
	Scope       ManifestScope      `json:"scope"`
	ThreatModel any                `json:"threatModel,omitempty"`
	CoverageRef string             `json:"coverageRef"`
	FindingsRef string             `json:"findingsRef"`
	Artifacts   []ManifestArtifact `json:"artifacts"`
	Warnings    []string           `json:"warnings,omitempty"`
}

type ManifestProducer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ManifestTarget struct {
	Kind           string `json:"kind"`
	TargetID       string `json:"targetId"`
	DisplayName    string `json:"displayName"`
	Remote         string `json:"remote,omitempty"`
	Revision       string `json:"revision,omitempty"`
	BaseRevision   string `json:"baseRevision,omitempty"`
	HeadRevision   string `json:"headRevision,omitempty"`
	SnapshotDigest string `json:"snapshotDigest,omitempty"`
}

type ManifestScope struct {
	IncludePaths []string `json:"includePaths"`
	ExcludePaths []string `json:"excludePaths"`
	Summary      string   `json:"summary,omitempty"`
	Context      string   `json:"context,omitempty"`
	Limitations  []string `json:"limitations,omitempty"`
}

type ManifestArtifact struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"mediaType"`
}

type FindingsDocument struct {
	DocumentType  string    `json:"documentType"`
	SchemaVersion string    `json:"schemaVersion"`
	ScanID        string    `json:"scanId"`
	Findings      []Finding `json:"findings"`
}

type CoverageDocument struct {
	DocumentType  string `json:"documentType"`
	SchemaVersion string `json:"schemaVersion"`
	ScanID        string `json:"scanId"`
	Coverage
}

type Completion struct {
	Scan      Scan
	Manifest  ManifestDocument
	Findings  FindingsDocument
	Coverage  CoverageDocument
	Artifacts []Artifact
}

type Finalizer struct {
	Validator *ContractValidator
	Now       func() time.Time
}

func NewFinalizer() (*Finalizer, error) {
	validator, err := DefaultValidator()
	if err != nil {
		return nil, err
	}
	return &Finalizer{Validator: validator, Now: func() time.Time { return time.Now().UTC() }}, nil
}

func (f *Finalizer) Finalize(ctx context.Context, scan Scan, draft Draft) (Completion, error) {
	if err := ctx.Err(); err != nil {
		return Completion{}, err
	}
	if f == nil || f.Validator == nil {
		return Completion{}, fmt.Errorf("security scan: finalizer validator is unavailable")
	}
	if draft.ScanID != scan.ID {
		return Completion{}, fmt.Errorf("security scan: draft belongs to %q, expected %q", draft.ScanID, scan.ID)
	}
	if err := draft.Validate(); err != nil {
		return Completion{}, err
	}
	if err := scan.Target.Validate(); err != nil {
		return Completion{}, err
	}
	if err := requireOutputOutsideTarget(scan.Target.Repository, scan.OutputDirectory); err != nil {
		return Completion{}, err
	}
	if err := ensurePrivateDirectory(scan.OutputDirectory); err != nil {
		return Completion{}, err
	}

	findings, err := normalizeFindings(scan, draft.Findings)
	if err != nil {
		return Completion{}, err
	}
	coverage := normalizeCoverage(scan, draft.Coverage)
	findingsDocument := FindingsDocument{DocumentType: FindingsDocumentType, SchemaVersion: ContractVersion, ScanID: scan.ID, Findings: findings}
	coverageDocument := CoverageDocument{DocumentType: CoverageDocumentType, SchemaVersion: ContractVersion, ScanID: scan.ID, Coverage: coverage}
	if err := f.Validator.ValidateFindings(findingsDocument); err != nil {
		return Completion{}, err
	}
	if err := f.Validator.ValidateCoverage(coverageDocument); err != nil {
		return Completion{}, err
	}

	findingsBytes, err := canonicalJSON(findingsDocument)
	if err != nil {
		return Completion{}, err
	}
	coverageBytes, err := canonicalJSON(coverageDocument)
	if err != nil {
		return Completion{}, err
	}
	reportBytes := BuildReport(scan, findings, coverage)
	sarifBytes, err := BuildSARIF(scan, findings)
	if err != nil {
		return Completion{}, fmt.Errorf("security scan: build SARIF: %w", err)
	}
	sarifBytes = append(sarifBytes, '\n')

	completedAt := f.Now
	if completedAt == nil {
		completedAt = func() time.Time { return time.Now().UTC() }
	}
	completed := completedAt().UTC()
	if scan.StartedAt.IsZero() {
		scan.StartedAt = scan.CreatedAt
	}
	if scan.StartedAt.IsZero() {
		scan.StartedAt = completed
	}

	payloads := []struct {
		kind      string
		path      string
		mediaType string
		contents  []byte
	}{
		{kind: "findings", path: "findings.json", mediaType: "application/json", contents: findingsBytes},
		{kind: "coverage", path: "coverage.json", mediaType: "application/json", contents: coverageBytes},
		{kind: "report", path: "report.md", mediaType: "text/markdown", contents: reportBytes},
		{kind: "sarif", path: "exports/results.sarif", mediaType: "application/sarif+json", contents: sarifBytes},
	}
	if scan.Target.DiffArtifact != "" {
		diffEvidence, readErr := os.ReadFile(filepath.Join(scan.Target.SnapshotRoot, filepath.FromSlash(scan.Target.DiffArtifact)))
		if readErr != nil {
			return Completion{}, fmt.Errorf("security scan: read immutable diff evidence: %w", readErr)
		}
		diffSum := sha256.Sum256(diffEvidence)
		if digest := "sha256:" + hex.EncodeToString(diffSum[:]); digest != scan.Target.DiffDigest {
			return Completion{}, fmt.Errorf("security scan: immutable diff evidence digest changed")
		}
		payloads = append(payloads, struct {
			kind      string
			path      string
			mediaType string
			contents  []byte
		}{kind: "diff_evidence", path: "artifacts/diff.patch", mediaType: "text/x-diff", contents: diffEvidence})
	}
	artifacts := make([]Artifact, 0, len(payloads)+1)
	manifestArtifacts := make([]ManifestArtifact, 0, len(payloads))
	for _, payload := range payloads {
		digest := sha256.Sum256(payload.contents)
		hexDigest := hex.EncodeToString(digest[:])
		artifacts = append(artifacts, Artifact{ScanID: scan.ID, Kind: payload.kind, Path: payload.path, MediaType: payload.mediaType, SHA256: hexDigest, Bytes: int64(len(payload.contents)), CreatedAt: completed})
		manifestArtifacts = append(manifestArtifacts, ManifestArtifact{Path: payload.path, SHA256: hexDigest, MediaType: payload.mediaType})
	}

	threatModel, err := decodeOptionalObject(draft.ThreatModel)
	if err != nil {
		return Completion{}, fmt.Errorf("security scan: threat model: %w", err)
	}
	manifest := ManifestDocument{
		DocumentType:  ManifestDocumentType,
		SchemaVersion: ContractVersion,
		Scan: ManifestScan{
			ID:          scan.ID,
			Producer:    ManifestProducer{Name: ProducerName, Version: ProducerVersion},
			Status:      "completed",
			StartedAt:   scan.StartedAt.UTC().Format(time.RFC3339Nano),
			CompletedAt: completed.Format(time.RFC3339Nano),
			SealedAt:    completed.Format(time.RFC3339Nano),
			Target:      manifestTarget(scan.Target),
			Scope:       ManifestScope{IncludePaths: nonNilStrings(scan.Target.IncludePaths), ExcludePaths: nonNilStrings(scan.Target.ExcludePaths), Context: strings.TrimSpace(scan.UserContext)},
			ThreatModel: threatModel,
			CoverageRef: "coverage.json",
			FindingsRef: "findings.json",
			Artifacts:   manifestArtifacts,
		},
	}
	if scan.Warning != "" {
		manifest.Scan.Warnings = []string{scan.Warning}
	}
	if err := f.Validator.ValidateManifest(manifest); err != nil {
		return Completion{}, err
	}
	manifestBytes, err := canonicalJSON(manifest)
	if err != nil {
		return Completion{}, err
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	artifacts = append(artifacts, Artifact{ScanID: scan.ID, Kind: "manifest", Path: "scan-manifest.json", MediaType: "application/json", SHA256: hex.EncodeToString(manifestDigest[:]), Bytes: int64(len(manifestBytes)), CreatedAt: completed})

	for _, payload := range payloads {
		if err := atomicWriteScanFile(ctx, scan.OutputDirectory, payload.path, payload.contents); err != nil {
			return Completion{}, err
		}
	}
	if err := atomicWriteScanFile(ctx, scan.OutputDirectory, "scan-manifest.json", manifestBytes); err != nil {
		return Completion{}, err
	}
	if err := verifyArtifacts(ctx, scan.OutputDirectory, artifacts); err != nil {
		return Completion{}, err
	}

	scan.Status = StatusComplete
	scan.Phase = PhaseReporting
	scan.Completeness = coverage.Completeness
	scan.CompletedAt = completed
	scan.UpdatedAt = completed
	return Completion{Scan: scan, Manifest: manifest, Findings: findingsDocument, Coverage: coverageDocument, Artifacts: artifacts}, nil
}

func normalizeFindings(scan Scan, values []Finding) ([]Finding, error) {
	findings := make([]Finding, len(values))
	for index, finding := range values {
		for locationIndex := range finding.Locations {
			normalized, err := SafeRelativePath(finding.Locations[locationIndex].Path, false)
			if err != nil {
				return nil, fmt.Errorf("security scan: finding %d location %d: %w", index, locationIndex, err)
			}
			finding.Locations[locationIndex].Path = normalized
			if finding.Locations[locationIndex].StartLine < 1 {
				return nil, fmt.Errorf("security scan: finding %d location %d has invalid start line", index, locationIndex)
			}
			if finding.Locations[locationIndex].EndLine == 0 {
				finding.Locations[locationIndex].EndLine = finding.Locations[locationIndex].StartLine
			}
			if finding.Locations[locationIndex].EndLine < finding.Locations[locationIndex].StartLine {
				return nil, fmt.Errorf("security scan: finding %d location %d has invalid line range", index, locationIndex)
			}
		}
		if finding.Taxonomy.CWE == nil {
			finding.Taxonomy.CWE = []string{}
		}
		if finding.RemediationTests == nil {
			finding.RemediationTests = []string{}
		}
		if finding.Preventive == nil {
			finding.Preventive = []string{}
		}
		if finding.Provenance == nil {
			finding.Provenance = map[string]any{"source": "azem_runtime"}
		}
		if source, ok := finding.Provenance["source"].(string); !ok || strings.TrimSpace(source) == "" {
			finding.Provenance["source"] = "azem_runtime"
		}
		if finding.Extensions == nil {
			finding.Extensions = map[string]any{}
		}
		findings[index] = finding
	}
	return BindFindingIdentities(scan.ID, scan.Target.TargetID, findings)
}

func normalizeCoverage(scan Scan, coverage Coverage) Coverage {
	coverage.Mode = coverageMode(scan)
	coverage.InventoryStrategy = inventoryStrategy(scan.Target)
	coverage.IncludePaths = append([]string(nil), scan.Target.IncludePaths...)
	coverage.ExcludePaths = append([]string(nil), scan.Target.ExcludePaths...)
	if coverage.IncludePaths == nil {
		coverage.IncludePaths = []string{"."}
	}
	if coverage.ExcludePaths == nil {
		coverage.ExcludePaths = []string{}
	}
	if coverage.Surfaces == nil {
		coverage.Surfaces = []CoverageSurface{}
	}
	for index := range coverage.Surfaces {
		if coverage.Surfaces[index].ReceiptRefs == nil {
			coverage.Surfaces[index].ReceiptRefs = []string{}
		}
	}
	if coverage.ExplicitExclusions == nil {
		coverage.ExplicitExclusions = []CoverageExclusion{}
	}
	if coverage.Deferred == nil {
		coverage.Deferred = []DeferredWork{}
	}
	for index := range coverage.Deferred {
		if coverage.Deferred[index].ID == "" {
			coverage.Deferred[index].ID = fmt.Sprintf("deferred_%03d", index+1)
		}
	}
	return coverage
}

func coverageMode(scan Scan) string {
	if scan.Mode == ModeDeep && scan.Target.Kind == TargetRepository {
		return "deep_repository"
	}
	switch scan.Target.Kind {
	case TargetPaths:
		return "scoped_path"
	case TargetGitRefs:
		return "branch_diff"
	case TargetWorkingTree:
		return "working_tree"
	default:
		return "repository"
	}
}

func inventoryStrategy(target Target) string {
	switch target.Kind {
	case TargetPaths:
		return "scoped_path"
	case TargetGitRefs, TargetWorkingTree:
		return "diff"
	default:
		return "repository"
	}
}

func nonNilStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func manifestTarget(target Target) ManifestTarget {
	kind := "git_worktree"
	if target.Kind == TargetGitRefs || target.Kind == TargetWorkingTree {
		kind = "git_diff"
	} else if target.Revision == "" {
		kind = "directory_snapshot"
	}
	return ManifestTarget{
		Kind: kind, TargetID: target.TargetID, DisplayName: target.DisplayName, Remote: target.Remote,
		Revision: target.Revision, BaseRevision: target.BaseRevision, HeadRevision: target.HeadRevision,
		SnapshotDigest: target.SnapshotDigest,
	}
}

func decodeOptionalObject(payload json.RawMessage) (any, error) {
	if len(payload) == 0 || string(payload) == "null" {
		return nil, nil
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func canonicalJSON(value any) ([]byte, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("security scan: encode canonical JSON: %w", err)
	}
	return append(payload, '\n'), nil
}

func requireOutputOutsideTarget(repository, output string) error {
	repository, err := filepath.Abs(repository)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(repository, output)
	if err != nil {
		return err
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)) {
		return fmt.Errorf("security scan: output directory must be outside the repository")
	}
	return nil
}

func ensurePrivateDirectory(directory string) error {
	metadata, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("security scan: create output directory: %w", err)
		}
		metadata, err = os.Lstat(directory)
	}
	if err != nil {
		return fmt.Errorf("security scan: inspect output directory: %w", err)
	}
	if !metadata.IsDir() || metadata.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("security scan: output directory must be a non-symlink directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("security scan: protect output directory: %w", err)
	}
	return nil
}

func atomicWriteScanFile(ctx context.Context, root, relativePath string, contents []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := SafeRelativePath(relativePath, false)
	if err != nil {
		return err
	}
	destination := filepath.Join(root, filepath.FromSlash(normalized))
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("security scan: create artifact directory: %w", err)
	}
	if metadata, err := os.Lstat(destination); err == nil && metadata.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("security scan: artifact destination is a symlink: %s", normalized)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".azem-security-*")
	if err != nil {
		return fmt.Errorf("security scan: create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("security scan: write artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("security scan: sync artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("security scan: close artifact: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("security scan: publish artifact: %w", err)
	}
	return nil
}

func verifyArtifacts(ctx context.Context, root string, artifacts []Artifact) error {
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		normalized, err := SafeRelativePath(artifact.Path, false)
		if err != nil {
			return err
		}
		path := filepath.Join(root, filepath.FromSlash(normalized))
		metadata, err := os.Lstat(path)
		if err != nil || !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("security scan: sealed artifact is missing or unsafe: %s", artifact.Path)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("security scan: read sealed artifact: %w", err)
		}
		digest := sha256.Sum256(payload)
		if hex.EncodeToString(digest[:]) != artifact.SHA256 {
			return fmt.Errorf("security scan: sealed artifact changed: %s", artifact.Path)
		}
	}
	return nil
}
