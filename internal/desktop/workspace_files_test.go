package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceEntriesAndTextPreview(t *testing.T) {
	root := t.TempDir()
	mustWriteWorkspaceFile(t, filepath.Join(root, "zeta.txt"), "one\ntwo\n")
	mustWriteWorkspaceFile(t, filepath.Join(root, "src", "main.go"), "package main\n")
	mustWriteWorkspaceFile(t, filepath.Join(root, ".hidden"), "hidden")
	mustWriteWorkspaceFile(t, filepath.Join(root, ".git", "config"), "ignored")
	bridge := &Bridge{workspace: root}

	directory, err := bridge.WorkspaceEntries("")
	if err != nil {
		t.Fatal(err)
	}
	if len(directory.Entries) != 3 {
		t.Fatalf("entries = %#v", directory.Entries)
	}
	if directory.Entries[0].Path != "src" || !directory.Entries[0].Directory {
		t.Fatalf("directory was not sorted first: %#v", directory.Entries)
	}
	if !directory.Entries[1].Hidden || directory.Entries[1].Name != ".hidden" {
		t.Fatalf("hidden file metadata = %#v", directory.Entries[1])
	}

	file, err := bridge.WorkspaceFile("src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != "text" || file.Language != "go" || file.Content != "package main\n" || file.LineCount != 1 {
		t.Fatalf("file = %#v", file)
	}
}

func TestWorkspaceEntriesExcludeGitIgnoredBuildOutputs(t *testing.T) {
	root := t.TempDir()
	mustWriteWorkspaceFile(t, filepath.Join(root, ".gitignore"), "/dist/\n/app\n*.test\n")
	mustWriteWorkspaceFile(t, filepath.Join(root, "main.go"), "package main\n")
	mustWriteWorkspaceFile(t, filepath.Join(root, "dist", "Azem"), "generated")
	mustWriteWorkspaceFile(t, filepath.Join(root, "app"), "generated")
	mustWriteWorkspaceFile(t, filepath.Join(root, "azem.test"), "generated")
	if output, err := exec.Command("git", "-C", root, "init", "--quiet").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v (%s)", err, output)
	}

	directory, err := (&Bridge{workspace: root}).WorkspaceEntries("")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range directory.Entries {
		if entry.Name == "dist" || entry.Name == "app" || entry.Name == "azem.test" {
			t.Fatalf("ignored build output was exposed: %#v", directory.Entries)
		}
	}
	if len(directory.Entries) != 2 || directory.Entries[0].Name != ".gitignore" || directory.Entries[1].Name != "main.go" {
		t.Fatalf("visible entries = %#v", directory.Entries)
	}
}

func TestWorkspaceFileClassifiesBinaryAndTruncatesLargeText(t *testing.T) {
	root := t.TempDir()
	mustWriteWorkspaceFile(t, filepath.Join(root, "binary.dat"), string([]byte{1, 0, 2}))
	mustWriteWorkspaceFile(t, filepath.Join(root, "large.txt"), strings.Repeat("x", maxPreviewBytes+32))
	bridge := &Bridge{workspace: root}

	binary, err := bridge.WorkspaceFile("binary.dat")
	if err != nil {
		t.Fatal(err)
	}
	if binary.Kind != "binary" || binary.Content != "" {
		t.Fatalf("binary = %#v", binary)
	}
	large, err := bridge.WorkspaceFile("large.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !large.Truncated || len(large.Content) != maxPreviewBytes {
		t.Fatalf("large preview = %#v, content=%d", large, len(large.Content))
	}
}

func TestWorkspaceFileClassifiesExtensionlessProjectFiles(t *testing.T) {
	root := t.TempDir()
	mustWriteWorkspaceFile(t, filepath.Join(root, "go.sum"), "example.com/module v1.0.0 h1:value\n")
	file, err := (&Bridge{workspace: root}).WorkspaceFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}
	if file.Language != "go-module" {
		t.Fatalf("language = %q", file.Language)
	}
}

func TestWorkspaceFilesRejectEscapesIncludingSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWriteWorkspaceFile(t, filepath.Join(outside, "secret.txt"), "secret")
	bridge := &Bridge{workspace: root}

	if _, err := bridge.WorkspaceFile("../secret.txt"); err == nil {
		t.Fatal("parent traversal was accepted")
	}
	link := filepath.Join(root, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := bridge.WorkspaceEntries("outside"); err == nil {
		t.Fatal("escaping symlink was accepted")
	}
	directory, err := bridge.WorkspaceEntries("")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range directory.Entries {
		if entry.Name == "outside" {
			t.Fatal("escaping symlink was exposed in the tree")
		}
	}
}

func mustWriteWorkspaceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
