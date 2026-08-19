package evidence

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Viking602/azem/internal/session"
)

func TestIndexAndRetrieveRanksWorkspaceStructureBeforeHistory(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeFixture(t, workspace, "internal/pipeline.go", "package pipeline\n\nimport \"context\"\n\nfunc RunPipeline(ctx context.Context) error { return nil }\n")
	writeFixture(t, workspace, "internal/pipeline_test.go", "package pipeline\n\nfunc TestRunPipeline() {}\n")
	writeFixture(t, workspace, "Makefile", "test:\n\tgo test ./...\n")
	candidates, err := IndexWorkspace(workspace, []RepositorySignalV1{
		{Path: "internal/pipeline.go", Diagnostics: []string{"timeout in RunPipeline"}, Touched: true, Related: []string{"internal/pipeline_test.go"}},
		{Path: "internal/pipeline_test.go"},
		{Path: "Makefile"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 || candidates[0].Path != "Makefile" || candidates[1].Module != "pipeline" || candidates[1].Symbols[0] != "RunPipeline" || candidates[1].Imports[0] != "context" {
		t.Fatalf("indexed candidates = %+v", candidates)
	}
	historyCalled := false
	retriever := Retriever{History: func(_ context.Context, sessionID, query string, limit, tokenBudget, byteBudget int) ([]session.HistoryRecord, error) {
		historyCalled = sessionID == "session" && query == "RunPipeline timeout" && limit == 3 && tokenBudget > 0 && byteBudget == 256
		return []session.HistoryRecord{{SessionID: sessionID, SourceType: "sequence", SourceID: "sequence:1", Content: "RunPipeline timeout summary"}}, nil
	}}
	ranked, err := retriever.Retrieve(t.Context(), RetrieveInput{SessionID: "session", Query: "RunPipeline timeout", Limit: 3, ByteBudget: 256, Candidates: candidates})
	if err != nil {
		t.Fatal(err)
	}
	if !historyCalled || len(ranked) == 0 || ranked[0].Candidate.Path != "internal/pipeline.go" || ranked[0].Features["symbols"] == 0 || ranked[0].Features["diagnostics"] == 0 || ranked[0].Features["touched"] == 0 {
		t.Fatalf("ranked evidence = %+v historyCalled=%v", ranked, historyCalled)
	}
	if ranked[0].Candidate.Authority != "workspace" || ranked[0].Candidate.Ref.SHA256 == "" {
		t.Fatalf("workspace authority/provenance missing: %+v", ranked[0])
	}
}

func TestRetrieveHonorsByteAndResultBudgets(t *testing.T) {
	t.Parallel()
	candidates := []CandidateV1{
		{Ref: session.SourceRefV1{Kind: "workspace_file", ID: "a"}, Authority: "workspace", Path: "a.go", Content: "needle-aaaaaaaaaaaaaaaa"},
		{Ref: session.SourceRefV1{Kind: "workspace_file", ID: "b"}, Authority: "workspace", Path: "b.go", Content: "needle-bbbbbbbbbbbbbbbb"},
	}
	result, err := (Retriever{}).Retrieve(t.Context(), RetrieveInput{Query: "needle", Limit: 2, ByteBudget: 25, Candidates: candidates})
	if err != nil {
		t.Fatal(err)
	}
	used := 0
	for _, item := range result {
		used += len(item.Candidate.Content)
	}
	if len(result) != 2 || used > 25 {
		t.Fatalf("bounded result len=%d bytes=%d values=%+v", len(result), used, result)
	}
}

func TestIndexWorkspaceRejectsUnsafeAndUnboundedInputs(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	if _, err := IndexWorkspace(workspace, []RepositorySignalV1{{Path: "../outside"}}); err == nil {
		t.Fatal("accepted escaping path")
	}
	if _, err := IndexWorkspace(workspace, make([]RepositorySignalV1, MaxIndexPaths+1)); err == nil {
		t.Fatal("accepted unbounded path set")
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := IndexWorkspace(workspace, []RepositorySignalV1{{Path: "link.go"}}); err == nil {
		t.Fatal("accepted symlink workspace evidence")
	}
}

func writeFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
