package securityscan_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/securityscan"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func promptOccurrenceID(prompt string) string {
	start := strings.Index(prompt, "{")
	if start < 0 {
		return ""
	}
	var payload map[string]any
	if json.NewDecoder(strings.NewReader(prompt[start:])).Decode(&payload) != nil {
		return ""
	}
	if occurrenceID, ok := payload["occurrenceId"].(string); ok {
		return occurrenceID
	}
	if finding, ok := payload["finding"].(map[string]any); ok {
		occurrenceID, _ := finding["occurrenceId"].(string)
		return occurrenceID
	}
	return ""
}

type nativeFakeExecutor struct {
	mu                   sync.Mutex
	service              *securityscan.Service
	runs                 int
	skipProgress         bool
	fixerExtraFile       bool
	skipVerifierEvidence bool
}

func (e *nativeFakeExecutor) Execute(ctx context.Context, request securityscan.ExecutionRequest) (securityscan.ExecutionResult, error) {
	e.mu.Lock()
	e.runs++
	run := e.runs
	e.mu.Unlock()
	if request.Worker.Kind == securityscan.WorkerReducer {
		inputs, err := e.service.ReducerInputs(request.Scan.ID, request.Worker.ID)
		if err != nil {
			return securityscan.ExecutionResult{}, err
		}
		draft := inputs[len(inputs)-1]
		if len(inputs) > 1 {
			draft.Findings = inputs[0].Findings
		}
		if err := e.service.SubmitDraft(ctx, request.Scan.ID, request.Worker.ID, draft); err != nil {
			return securityscan.ExecutionResult{}, err
		}
		return securityscan.ExecutionResult{RunID: "run_reducer", InputTokens: 10, OutputTokens: 2}, nil
	}
	if request.Worker.Kind == securityscan.WorkerFixer {
		path := filepath.Join(request.WorkspaceRoot, "main.go")
		if err := os.WriteFile(path, []byte("package main\nconst fixed = true\n"), 0o600); err != nil {
			return securityscan.ExecutionResult{}, err
		}
		files := []string{"main.go"}
		if e.fixerExtraFile {
			if err := os.WriteFile(filepath.Join(request.WorkspaceRoot, "unrelated.go"), []byte("package main\nconst unrelated = true\n"), 0o600); err != nil {
				return securityscan.ExecutionResult{}, err
			}
			files = append(files, "unrelated.go")
		}
		occurrenceID := promptOccurrenceID(request.Prompt)
		if err := e.service.SubmitPatchResult(request.Scan.ID, request.Worker.ID, securityscan.PatchResult{
			OccurrenceID: occurrenceID, Status: "generated", Files: files,
		}); err != nil {
			return securityscan.ExecutionResult{}, err
		}
		return securityscan.ExecutionResult{RunID: "run_fixer"}, nil
	}
	if request.Worker.Kind == securityscan.WorkerVerifier {
		if !e.skipVerifierEvidence {
			e.service.ObservePath(request.Scan.ID, request.Worker.ID, "main.go")
			e.service.ObserveTool(request.Scan.ID, request.Worker.ID, "coding.go_test")
		}
		occurrenceID := promptOccurrenceID(strings.TrimPrefix(request.Prompt, "Verify this proposed security fix:\n"))
		if err := e.service.SubmitPatchResult(request.Scan.ID, request.Worker.ID, securityscan.PatchResult{
			OccurrenceID: occurrenceID, Status: "verified", Files: []string{"main.go"}, Verification: "synthetic regression check passed",
		}); err != nil {
			return securityscan.ExecutionResult{}, err
		}
		return securityscan.ExecutionResult{RunID: "run_verifier"}, nil
	}
	if !e.skipProgress {
		for _, path := range request.Scan.Target.Inventory {
			e.service.ObservePath(request.Scan.ID, request.Worker.ID, path)
		}
		if err := e.service.RecordProgress(ctx, request.Scan.ID, request.Worker.ID, securityscan.PhaseDiscovery, request.Scan.Target.Inventory, "review complete"); err != nil {
			return securityscan.ExecutionResult{}, err
		}
	}
	draft := securityscan.Draft{
		ScanID: request.Scan.ID,
		Findings: []securityscan.Finding{{
			RuleID: "test.unsafe-input", Identity: securityscan.FindingIdentity{Anchor: "unsafe-input"},
			Title: "Unsafe input reaches a sensitive operation", Summary: "A synthetic attacker-controlled value reaches the test sink.",
			Severity: securityscan.Severity{Level: "high"}, Confidence: securityscan.Confidence{Level: "high", Rationale: "Synthetic source trace."},
			Taxonomy:    securityscan.Taxonomy{Category: "input-validation", CWE: []string{"CWE-20"}},
			Locations:   []securityscan.FindingLocation{{Path: request.Scan.Target.Inventory[0], StartLine: 1, Role: "sink"}},
			Remediation: "Validate the value before use.", Validation: nil, AttackPath: nil,
			RemediationTests: []string{"Reject invalid input."}, Preventive: []string{"Centralize validation."},
			Provenance: map[string]any{"source": "azem_runtime"}, Extensions: map[string]any{},
		}},
		Coverage: securityscan.Coverage{
			Completeness:       securityscan.CompletenessComplete,
			Surfaces:           []securityscan.CoverageSurface{{ID: "surface_test", Label: "Synthetic input", Disposition: "reported", ReceiptRefs: []string{}}},
			ExplicitExclusions: []securityscan.CoverageExclusion{}, Deferred: []securityscan.DeferredWork{},
		},
	}
	if err := e.service.SubmitDraft(ctx, request.Scan.ID, request.Worker.ID, draft); err != nil {
		return securityscan.ExecutionResult{}, err
	}
	return securityscan.ExecutionResult{RunID: "run_audit", InputTokens: int64(run * 100), CachedInputTokens: 20, OutputTokens: 10}, nil
}

func (e *nativeFakeExecutor) Cancel(context.Context, string) error { return nil }

func nativeService(t *testing.T) (*securityscan.Service, *nativeFakeExecutor, func()) {
	t.Helper()
	root := t.TempDir()
	provider, err := sqlitestore.Open(context.Background(), filepath.Join(root, "azem.db"), sqlitestore.WithBlobRoot(filepath.Join(root, "blobs")))
	if err != nil {
		t.Fatal(err)
	}
	store, err := securityscan.NewSQLStore(provider.DB())
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err := securityscan.NewFinalizer()
	if err != nil {
		t.Fatal(err)
	}
	executor := &nativeFakeExecutor{}
	service, err := securityscan.NewService(securityscan.ServiceOptions{
		Store: store, Executor: executor, Snapshotter: securityscan.Snapshotter{DataRoot: root}, Finalizer: finalizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor.service = service
	return service, executor, func() { _ = provider.Close(context.Background()) }
}

func waitForTerminalScan(t *testing.T, service *securityscan.Service, scanID string) securityscan.Projection {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		projection, err := service.Scan(context.Background(), scanID)
		if err == nil && projection.Scan.Status.Terminal() {
			return projection
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("scan %s did not become terminal", scanID)
	return securityscan.Projection{}
}

func testScanRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestNativeStandardScanOwnsFinalization(t *testing.T) {
	service, _, cleanup := nativeService(t)
	defer cleanup()
	scan, err := service.Start(context.Background(), securityscan.StartRequest{
		Repository: testScanRepository(t), TargetKind: securityscan.TargetRepository, Mode: securityscan.ModeStandard,
		Route: securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"}, Deep: securityscan.DefaultDeepOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := waitForTerminalScan(t, service, scan.ID)
	if projection.Scan.Status != securityscan.StatusComplete || len(projection.Findings) != 1 || projection.Progress.FilesCompleted != 1 {
		t.Fatalf("projection = %+v", projection)
	}
	if _, err := os.Stat(filepath.Join(projection.Scan.OutputDirectory, "scan-manifest.json")); err != nil {
		t.Fatal(err)
	}
	occurrenceID := projection.Findings[0].OccurrenceID
	if err := service.SaveTriage(context.Background(), securityscan.Triage{OccurrenceID: occurrenceID, Status: "closed", CloseReason: "false_positive"}); err != nil {
		t.Fatal(err)
	}
	triaged, err := service.Scan(context.Background(), scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if triaged.Triage[occurrenceID].CloseReason != "false_positive" {
		t.Fatalf("triage projection = %#v", triaged.Triage)
	}
}

func TestNativeDeepScanRunsIndependentAuditsAndReducer(t *testing.T) {
	service, executor, cleanup := nativeService(t)
	defer cleanup()
	deep := securityscan.DefaultDeepOptions()
	deep.Workers, deep.MaxDiscoveryRuns, deep.StopAfterNoNew = 1, 2, 1
	scan, err := service.Start(context.Background(), securityscan.StartRequest{
		Repository: testScanRepository(t), TargetKind: securityscan.TargetRepository, Mode: securityscan.ModeDeep,
		Route: securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"}, Deep: deep,
		Budget: securityscan.Budget{MaxTimeHours: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := waitForTerminalScan(t, service, scan.ID)
	if projection.Scan.Status != securityscan.StatusComplete || len(projection.Findings) != 1 {
		t.Fatalf("projection = %+v", projection)
	}
	executor.mu.Lock()
	runs := executor.runs
	executor.mu.Unlock()
	if runs < 3 {
		t.Fatalf("model runs = %d, want audits plus reducer", runs)
	}
}

func TestCompleteClaimWithoutReadReceiptsBecomesPartial(t *testing.T) {
	service, executor, cleanup := nativeService(t)
	defer cleanup()
	executor.skipProgress = true
	scan, err := service.Start(context.Background(), securityscan.StartRequest{
		Repository: testScanRepository(t), TargetKind: securityscan.TargetRepository, Mode: securityscan.ModeStandard,
		Route: securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"}, Deep: securityscan.DefaultDeepOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := waitForTerminalScan(t, service, scan.ID)
	if projection.Scan.Status != securityscan.StatusComplete || projection.Scan.Completeness != securityscan.CompletenessPartial {
		t.Fatalf("scan status=%s completeness=%s", projection.Scan.Status, projection.Scan.Completeness)
	}
}

func TestPublicationClaimIsExclusive(t *testing.T) {
	service, _, cleanup := nativeService(t)
	defer cleanup()
	scan, err := service.Start(context.Background(), securityscan.StartRequest{
		Repository: testScanRepository(t), TargetKind: securityscan.TargetRepository, Mode: securityscan.ModeStandard,
		Route: securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"}, Deep: securityscan.DefaultDeepOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := waitForTerminalScan(t, service, scan.ID)
	publication := securityscan.Publication{
		ScanID: scan.ID, OccurrenceID: projection.Findings[0].OccurrenceID, Destination: "mcp__linear__create_issue",
	}
	if err := service.ClaimPublication(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	if err := service.ClaimPublication(context.Background(), publication); !errors.Is(err, securityscan.ErrPublicationExists) {
		t.Fatalf("second claim error = %v", err)
	}
	if err := service.ReconcilePublication(context.Background(), publication.ScanID, publication.OccurrenceID, publication.Destination, "retry"); err != nil {
		t.Fatal(err)
	}
	if err := service.ClaimPublication(context.Background(), publication); err != nil {
		t.Fatalf("claim after explicit retry reconciliation: %v", err)
	}
	if err := service.ReconcilePublication(context.Background(), publication.ScanID, publication.OccurrenceID, publication.Destination, "published"); err != nil {
		t.Fatal(err)
	}
	issues, err := service.PublicationIssues(context.Background(), publication.ScanID, publication.Destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("published occurrence remained eligible: %#v", issues)
	}
}

func TestPatchRejectsFilesOutsideFindingLocations(t *testing.T) {
	service, executor, cleanup := nativeService(t)
	defer cleanup()
	repository := remediationRepository(t)
	scan, err := service.Start(context.Background(), securityscan.StartRequest{
		Repository: repository, TargetKind: securityscan.TargetRepository, Mode: securityscan.ModeStandard,
		Route: securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"}, Deep: securityscan.DefaultDeepOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := waitForTerminalScan(t, service, scan.ID)
	executor.fixerExtraFile = true
	results, err := service.Patch(context.Background(), securityscan.PatchRequest{
		OccurrenceIDs: []string{projection.Findings[0].OccurrenceID},
		Route:         securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "failed" || !strings.Contains(results[0].Reason, "host-authorized") {
		t.Fatalf("patch results = %#v", results)
	}
	updated, err := service.Scan(context.Background(), scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, worker := range updated.Workers {
		if worker.Kind == securityscan.WorkerFixer && worker.Status == securityscan.WorkerRunning {
			t.Fatalf("fixer remained running: %+v", worker)
		}
	}
}

func TestPatchVerifierCannotSelfAttestWithoutReceipts(t *testing.T) {
	service, executor, cleanup := nativeService(t)
	defer cleanup()
	repository := remediationRepository(t)
	scan, err := service.Start(context.Background(), securityscan.StartRequest{
		Repository: repository, TargetKind: securityscan.TargetRepository, Mode: securityscan.ModeStandard,
		Route: securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"}, Deep: securityscan.DefaultDeepOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := waitForTerminalScan(t, service, scan.ID)
	executor.skipVerifierEvidence = true
	_, err = service.Patch(context.Background(), securityscan.PatchRequest{
		OccurrenceIDs: []string{projection.Findings[0].OccurrenceID},
		Route:         securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"},
	})
	if err == nil || !strings.Contains(err.Error(), "did not read every changed file") {
		t.Fatalf("verification error = %v", err)
	}
}
