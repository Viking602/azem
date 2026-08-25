package securityscan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func exampleFinding() Finding {
	return Finding{
		RuleID:           "path-traversal.archive-extraction",
		Identity:         FindingIdentity{Anchor: "archive-entry-write-without-containment"},
		Title:            "Unsafe archive extraction can escape the output directory",
		Summary:          "An attacker-controlled path reaches a filesystem write without containment validation.",
		Severity:         Severity{Level: "high", Score: 8.1, ScoringSystem: "CVSS:3.1"},
		Confidence:       Confidence{Level: "high", Rationale: "Direct source trace reaches the filesystem write without a containment check."},
		Taxonomy:         Taxonomy{Category: "path-traversal", CWE: []string{"CWE-22"}},
		Locations:        []FindingLocation{{Path: "src/extract.py", StartLine: 41, EndLine: 44, Role: "sink"}},
		Remediation:      "Normalize destinations and reject entries that escape the extraction root.",
		Validation:       nil,
		AttackPath:       nil,
		RemediationTests: []string{"Assert traversal is rejected."},
		Preventive:       []string{"Use one checked extraction helper."},
		Provenance:       map[string]any{"source": "azem_runtime"},
		Extensions:       map[string]any{},
	}
}

func TestFindingIdentityMatchesCodexSecurityV1Fixture(t *testing.T) {
	fingerprints, findingID, occurrenceID, err := FindingIdentityFor("scan_example_001", "target_sha256_example", exampleFinding())
	if err != nil {
		t.Fatal(err)
	}
	if fingerprints.Primary != "codex-security/v1:sha256:990a4a6a2ec18440dd47eac4d7256c0ee2c02db1b43104720cab3cbe9db706ca" {
		t.Fatalf("fingerprint = %q", fingerprints.Primary)
	}
	if findingID != "csf_852f90d6e1177502ff113d4a" || occurrenceID != "occ_e79cb19591e696572a1c22be" {
		t.Fatalf("identity = %s %s", findingID, occurrenceID)
	}
}

func TestFinalizerWritesAndRevalidatesSealedArtifacts(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	output := filepath.Join(root, "results")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	scan := Scan{
		ID: "scan_example_001", ProjectID: repository, Mode: ModeStandard, Status: StatusRunning, Phase: PhaseReporting,
		Target: Target{
			Kind: TargetRepository, Repository: repository, TargetID: "target_sha256_example", DisplayName: "example/repo",
			Revision: "deadbeef", SnapshotDigest: SnapshotAlgorithm + ":sha256:ed88f96a4c1a06603a41b3f261f59c3de2555c367ef6ad3bb8b9e483495d34eb",
			IncludePaths: []string{"."}, ExcludePaths: []string{}, Inventory: []string{"src/extract.py"},
		},
		Route:           Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"},
		WorkflowVersion: WorkflowVersion, ContractVersion: ContractVersion, OutputDirectory: output,
		CreatedAt: fixed.Add(-time.Minute), StartedAt: fixed.Add(-time.Minute), UpdatedAt: fixed,
	}
	draft := Draft{
		ScanID:   scan.ID,
		Findings: []Finding{exampleFinding()},
		Coverage: Coverage{
			Completeness:       CompletenessComplete,
			Surfaces:           []CoverageSurface{{ID: "surface_archive", Label: "Archive extraction", Disposition: "reported", ReceiptRefs: []string{}}},
			ExplicitExclusions: []CoverageExclusion{}, Deferred: []DeferredWork{},
		},
	}
	finalizer, err := NewFinalizer()
	if err != nil {
		t.Fatal(err)
	}
	finalizer.Now = func() time.Time { return fixed }
	completion, err := finalizer.Finalize(context.Background(), scan, draft)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Scan.Status != StatusComplete || completion.Scan.Completeness != CompletenessComplete {
		t.Fatalf("completion scan = %+v", completion.Scan)
	}
	for _, relative := range []string{"scan-manifest.json", "findings.json", "coverage.json", "report.md", "exports/results.sarif"} {
		metadata, statErr := os.Lstat(filepath.Join(output, filepath.FromSlash(relative)))
		if statErr != nil || !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("artifact %s: %v %#v", relative, statErr, metadata)
		}
	}
	manifestBytes, err := os.ReadFile(filepath.Join(output, "scan-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest ManifestDocument
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Scan.Producer.Name != ProducerName || manifest.Scan.SealedAt != manifest.Scan.CompletedAt {
		t.Fatalf("manifest = %+v", manifest.Scan)
	}
}

func TestSafeRelativePathRejectsEscapes(t *testing.T) {
	for _, value := range []string{"../escape", "/escape", "C:/escape", `nested\\escape`, "nested/../escape", "nested\x00escape"} {
		if _, err := SafeRelativePath(value, false); err == nil {
			t.Fatalf("unsafe path accepted: %q", value)
		}
	}
	if value, err := SafeRelativePath("src/main.go", false); err != nil || value != "src/main.go" {
		t.Fatalf("safe path = %q, %v", value, err)
	}
}

func TestDeepExecutionBudgetPartitionsAggregateEnvelope(t *testing.T) {
	scan := Scan{
		Mode:   ModeDeep,
		Deep:   DeepOptions{StopAfterConsecutiveErrors: 3, MaxDiscoveryRuns: 40},
		Budget: Budget{MaxTokens: 10_000_000, MaxToolCalls: 20_000},
	}
	budget := scanExecutionBudget(scan)
	if budget.MaxTokens != 62_500 || budget.MaxToolCalls != 125 {
		t.Fatalf("partitioned budget = %+v", budget)
	}
}
