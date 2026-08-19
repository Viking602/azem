package verification

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestSelectDeterministicChecksUsesTouchedSurfaceBeforeSemantics(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeSelectorFile(t, workspace, "internal/demo/demo.go", "package demo\n")
	writeSelectorFile(t, workspace, "internal/demo/testdata/case.json", "{}\n")
	writeSelectorFile(t, workspace, "frontend/src/App.test.tsx", "export {}\n")
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship", RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		UserCriteria: []CriterionInput{{ID: "behavior", Text: "behavior works", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}, {ID: "quality", Text: "quality holds", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectDeterministicChecks(SelectInput{
		Workspace: workspace, Work: work, Plan: plan,
		Touched: []TouchedFileV1{
			{Path: "internal/demo/demo.go", Change: "modified"},
			{Path: "internal/demo/testdata/case.json", Change: "modified"},
			{Path: "frontend/src/App.test.tsx", Change: "modified"},
			{Path: "internal/demo/generated.gen.go", Change: "modified", Generated: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(selected.Checks))
	for _, check := range selected.Checks {
		ids = append(ids, check.ID)
		if len(check.CriterionIDs) == 0 {
			t.Fatalf("check %s has no criterion links", check.ID)
		}
	}
	wantPrefix := []string{"gofmt", "go-test", "frontend-typecheck", "frontend-tests", "artifact:internal/demo/generated.gen.go"}
	if len(ids) != len(wantPrefix)+2 || !reflect.DeepEqual(ids[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("check order = %v", ids)
	}
	if selected.Checks[len(selected.Checks)-2].Kind != "criterion" || selected.Checks[len(selected.Checks)-1].Kind != "criterion" {
		t.Fatalf("semantic checks were not last: %+v", selected.Checks)
	}
	goTest := selected.Checks[1]
	if goTest.Environment["GOWORK"] != "off" || !reflect.DeepEqual(goTest.Command, []string{"go", "test", "./internal/demo"}) {
		t.Fatalf("go test selection = %+v", goTest)
	}
	frontend := selected.Checks[3]
	if frontend.CWD != "frontend" || !reflect.DeepEqual(frontend.Command, []string{"bun", "run", "test", "--", "src/App.test.tsx"}) {
		t.Fatalf("frontend test selection = %+v", frontend)
	}
}

func TestSelectDeterministicChecksAddsSchemaAndContractGuards(t *testing.T) {
	t.Parallel()
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship", RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		UserCriteria: []CriterionInput{{ID: "criterion", Text: "works", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectDeterministicChecks(SelectInput{Workspace: t.TempDir(), Work: work, Plan: plan, Touched: []TouchedFileV1{
		{Path: "internal/store/sqlite/dbgen/schema.sql"}, {Path: "internal/app/contracts.go"}, {Path: "eval/harbor/timeout_test.py"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, check := range selected.Checks {
		seen[check.ID] = true
	}
	for _, id := range []string{"go-test", "sqlite-tests", "contracts-check", "python-tests"} {
		if !seen[id] {
			t.Fatalf("missing %s in %+v", id, selected.Checks)
		}
	}
}

func TestSelectDeterministicChecksRejectsEscapingPath(t *testing.T) {
	t.Parallel()
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship", RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		UserCriteria: []CriterionInput{{ID: "criterion", Text: "works", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SelectDeterministicChecks(SelectInput{Workspace: t.TempDir(), Work: work, Plan: plan, Touched: []TouchedFileV1{{Path: "../outside.go"}}}); err == nil {
		t.Fatal("accepted escaping touched path")
	}
}

func writeSelectorFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
