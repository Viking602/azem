package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceChangesListsTrackedDeletedAndUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	runWorkspaceGit(t, root, "init", "--initial-branch=main")
	runWorkspaceGit(t, root, "config", "user.name", "Azem Test")
	runWorkspaceGit(t, root, "config", "user.email", "azem@example.com")
	mustWriteWorkspaceFile(t, filepath.Join(root, "src", "main.go"), "one\ntwo\n")
	mustWriteWorkspaceFile(t, filepath.Join(root, "docs", "old.md"), "gone\n")
	runWorkspaceGit(t, root, "add", ".")
	runWorkspaceGit(t, root, "commit", "-m", "initial")

	mustWriteWorkspaceFile(t, filepath.Join(root, "src", "main.go"), "one\nthree\n")
	if err := os.Remove(filepath.Join(root, "docs", "old.md")); err != nil {
		t.Fatal(err)
	}
	mustWriteWorkspaceFile(t, filepath.Join(root, "frontend", "new.ts"), "first\nsecond\n")
	bridge := &Bridge{workspace: root}

	changes, err := bridge.WorkspaceChanges()
	if err != nil {
		t.Fatal(err)
	}
	if !changes.Repository || changes.Branch != "main" || changes.Base != "HEAD" {
		t.Fatalf("change metadata = %#v", changes)
	}
	if changes.Additions != 3 || changes.Deletions != 2 || len(changes.Files) != 3 {
		t.Fatalf("change totals = %#v", changes)
	}
	byPath := make(map[string]WorkspaceChangeFile)
	for _, file := range changes.Files {
		byPath[file.Path] = file
	}
	if byPath["src/main.go"].Status != "modified" || byPath["src/main.go"].Additions != 1 || byPath["src/main.go"].Deletions != 1 {
		t.Fatalf("modified file = %#v", byPath["src/main.go"])
	}
	if byPath["docs/old.md"].Status != "deleted" || byPath["docs/old.md"].Deletions != 1 {
		t.Fatalf("deleted file = %#v", byPath["docs/old.md"])
	}
	if byPath["frontend/new.ts"].Status != "untracked" || byPath["frontend/new.ts"].Additions != 2 {
		t.Fatalf("untracked file = %#v", byPath["frontend/new.ts"])
	}
}

func TestWorkspaceChangeReturnsBoundedUnifiedPatches(t *testing.T) {
	root := t.TempDir()
	runWorkspaceGit(t, root, "init", "--initial-branch=main")
	runWorkspaceGit(t, root, "config", "user.name", "Azem Test")
	runWorkspaceGit(t, root, "config", "user.email", "azem@example.com")
	mustWriteWorkspaceFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc value() string { return \"old\" }\n")
	runWorkspaceGit(t, root, "add", ".")
	runWorkspaceGit(t, root, "commit", "-m", "initial")
	mustWriteWorkspaceFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc value() string { return \"new\" }\n")
	mustWriteWorkspaceFile(t, filepath.Join(root, "new.txt"), "alpha\nbeta\n")
	bridge := &Bridge{workspace: root}

	tracked, err := bridge.WorkspaceChange("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if tracked.Status != "modified" || tracked.Additions != 1 || tracked.Deletions != 1 || !strings.Contains(tracked.Patch, "@@") || !strings.Contains(tracked.Patch, "+func value() string { return \"new\" }") {
		t.Fatalf("tracked patch = %#v", tracked)
	}
	untracked, err := bridge.WorkspaceChange("new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if untracked.Status != "untracked" || untracked.Additions != 2 || !strings.Contains(untracked.Patch, "--- /dev/null") || !strings.Contains(untracked.Patch, "+alpha") {
		t.Fatalf("untracked patch = %#v", untracked)
	}
	if _, err := bridge.WorkspaceChange("../outside.txt"); err == nil {
		t.Fatal("workspace traversal was accepted")
	}
	if _, err := bridge.WorkspaceChange("unchanged.txt"); err == nil {
		t.Fatal("unchanged path was accepted")
	}
}

func TestWorkspaceChangesReturnsNonRepositoryState(t *testing.T) {
	changes, err := (&Bridge{workspace: t.TempDir()}).WorkspaceChanges()
	if err != nil {
		t.Fatal(err)
	}
	if changes.Repository || len(changes.Files) != 0 {
		t.Fatalf("non-repository changes = %#v", changes)
	}
}

func runWorkspaceGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
