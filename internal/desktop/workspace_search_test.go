package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceFileSearchUsesRealRelativePathsAndGitIgnores(t *testing.T) {
	root := t.TempDir()
	runWorkspaceGit(t, root, "init", "--quiet")
	mustWriteWorkspaceFile(t, filepath.Join(root, ".gitignore"), "dist/\n")
	mustWriteWorkspaceFile(t, filepath.Join(root, "src", "main.go"), "package main")
	mustWriteWorkspaceFile(t, filepath.Join(root, "docs", "中文 说明.md"), "test")
	mustWriteWorkspaceFile(t, filepath.Join(root, "dist", "main.go"), "ignored")
	runWorkspaceGit(t, root, "add", "src/main.go")
	bridge := &Bridge{workspace: root}
	result, err := bridge.SearchWorkspaceFiles("MAIN", 20)
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Path != "src/main.go" {
		t.Fatalf("search = %#v, %v", result, err)
	}
	result, err = bridge.SearchWorkspaceFiles("中文 说明", 20)
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Path != "docs/中文 说明.md" {
		t.Fatalf("unicode search = %#v, %v", result, err)
	}
	result, err = bridge.SearchWorkspaceFiles("", 1)
	if err != nil || len(result.Entries) != 1 || !result.Truncated {
		t.Fatalf("bounded search = %#v, %v", result, err)
	}
}

func TestWorkspaceFileSearchWithoutGitRejectsEscapingSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	mustWriteWorkspaceFile(t, filepath.Join(root, "nested", "valid.md"), "local")
	mustWriteWorkspaceFile(t, filepath.Join(outside, "secret.md"), "private")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "secret.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	bridge := &Bridge{workspace: root}
	result, err := bridge.SearchWorkspaceFiles("md", 50)
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Path != "nested/valid.md" {
		t.Fatalf("bounded workspace = %#v, %v", result, err)
	}
	for _, query := range []string{"\x00", strings.Repeat("x", 1025)} {
		if _, err := bridge.SearchWorkspaceFiles(query, 50); err == nil {
			t.Fatalf("accepted invalid query %q", query)
		}
	}
}
