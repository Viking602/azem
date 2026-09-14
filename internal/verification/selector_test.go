package verification

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestSelectDeterministicChecksUsesTouchedSurfaceBeforeSemantics(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeSelectorFile(t, workspace, "internal/demo/demo.go", "package demo\n")
	writeSelectorFile(t, workspace, "internal/demo/testdata/case.json", "{}\n")
	writeSelectorFile(t, workspace, "gpui/crates/example/src/lib.rs", "pub fn example() {}\n")
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
			{Path: "gpui/crates/example/src/lib.rs", Change: "modified"},
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
	wantPrefix := []string{"gofmt", "go-test", "gpui-tests", "artifact:internal/demo/generated.gen.go"}
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
	gpui := selected.Checks[2]
	if gpui.CWD != "gpui" || !reflect.DeepEqual(gpui.Command, []string{"cargo", "test", "--workspace", "--all-targets"}) {
		t.Fatalf("GPUI test selection = %+v", gpui)
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
	for _, id := range []string{"go-test", "sqlite-tests", "contracts-check", "python-compile"} {
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

func TestSelectDeterministicChecksSeparatesFrontendFromGoEmbedPackage(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeSelectorFile(t, workspace, "frontend/embed.go", "package frontend\n")
	writeSelectorFile(t, workspace, "frontend/package.json", `{"packageManager":"bun@1.3.14","scripts":{"build":"tsc -b && vite build","test":"vitest run --environment jsdom","typecheck":"tsc -b --pretty false"}}`)
	writeSelectorFile(t, workspace, "frontend/src/components/Timeline.test.ts", "test()\n")
	writeSelectorFile(t, workspace, "frontend/src/styles.css", "body {}\n")
	work, plan := selectorTestWork(t)
	selected, err := SelectDeterministicChecks(SelectInput{
		Workspace: workspace, Work: work, Plan: plan,
		Touched: []TouchedFileV1{{Path: "frontend/src/components/Timeline.test.ts"}, {Path: "frontend/src/styles.css"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(selected.Checks))
	for _, check := range selected.Checks {
		ids = append(ids, check.ID)
	}
	if len(ids) < 2 || ids[0] != "frontend-build" || ids[1] != "frontend-test" {
		t.Fatalf("frontend checks = %v", ids)
	}
	for _, id := range ids {
		if id == "gofmt" || id == "go-test" {
			t.Fatalf("frontend source selected Go wrapper check %q: %v", id, ids)
		}
	}
}

func TestNearestGoPackageKeepsEmbeddedNonJavaScriptAssets(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeSelectorFile(t, workspace, "frontend/embed.go", "package frontend\n")
	writeSelectorFile(t, workspace, "frontend/package.json", `{"packageManager":"bun@1.3.14","scripts":{"build":"vite build"}}`)
	writeSelectorFile(t, workspace, "frontend/tools/package.json", `{"packageManager":"bun@1.3.14","scripts":{"test":"vitest run"}}`)
	writeSelectorFile(t, workspace, "frontend/dist/favicon.png", "not really an image\n")
	if got := nearestGoPackage(workspace, "frontend/dist/favicon.png"); got != "./frontend" {
		t.Fatalf("embedded asset owner = %q", got)
	}
	if got := nearestGoPackage(workspace, "frontend/tools/dist/icon.svg"); got != "./frontend" {
		t.Fatalf("nested embedded asset owner = %q", got)
	}
	if got := nearestGoPackage(workspace, "frontend/tools/src/view.ts"); got != "" {
		t.Fatalf("nested JavaScript package owner = %q", got)
	}
	if got := nearestGoPackage(workspace, "frontend/src/view.ts"); got != "" {
		t.Fatalf("JavaScript package source owner = %q", got)
	}
}

func selectorTestWork(t *testing.T) (session.WorkSpecV1, session.VerificationPlanV1) {
	t.Helper()
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship", RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		UserCriteria: []CriterionInput{{ID: "criterion", Text: "works", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return work, plan
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

func TestPythonChecksUseCurrentProjectFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test_expense_summary.py"), []byte("import unittest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	work, plan, err := CompileCriteria(CompileInput{SessionID: "s", RunID: "r", Goal: "test", RevisionID: "rev", SnapshotHash: "snap", CreatedAt: time.Unix(1, 0).UTC(), UserCriteria: []CriterionInput{{ID: "c", Text: "works", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectDeterministicChecks(SelectInput{Workspace: root, Work: work, Plan: plan, Touched: []TouchedFileV1{{Path: "expense_summary.py"}, {Path: "test_expense_summary.py"}, {Path: "old.py", Change: "deleted"}}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, check := range selected.Checks {
		if strings.Contains(strings.Join(check.Command, " "), "eval/harbor") {
			t.Fatalf("foreign project check: %+v", check)
		}
		if check.ID == "python-tests" {
			found = true
			if !reflect.DeepEqual(check.Command, []string{"python3", "-m", "unittest", "test_expense_summary.py"}) {
				t.Fatalf("wrong command: %+v", check)
			}
		}
	}
	if !found {
		t.Fatal("unittest check missing")
	}
}
