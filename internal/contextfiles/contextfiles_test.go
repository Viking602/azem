package contextfiles

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverCrossHarnessContextPrecedenceImportsAndPointers(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	workspace := filepath.Join(root, "packages", "api")
	mustMkdir(t, workspace)
	if err := exec.Command("git", "init", "-q", root).Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	mustWrite(t, filepath.Join(root, "shared.md"), "SHARED_ARCHITECTURE")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "ROOT_CONTEXT\nRead @shared.md.\n`@literal.md`\n```text\n@fenced.md\n```\n")
	mustWrite(t, filepath.Join(workspace, "AGENTS.md"), "LOW_PRIORITY_PACKAGE")
	mustWrite(t, filepath.Join(workspace, ".github", "copilot-instructions.md"), "GITHUB_PACKAGE_CONTEXT")
	mustWrite(t, filepath.Join(root, ".omp", "AGENTS.md"), "SHOULD_BE_SHADOWED_BY_NEAREST_NONEMPTY")
	mustWrite(t, filepath.Join(workspace, ".omp", "config.yml"), "enabled: true")
	mustWrite(t, filepath.Join(home, ".omp", "agent", "AGENTS.md"), "USER_NATIVE_CONTEXT")
	mustWrite(t, filepath.Join(home, ".claude", "CLAUDE.md"), "LOW_PRIORITY_USER")
	mustWrite(t, filepath.Join(workspace, "deep", "AGENTS.md"), "DEEP_POINTER")

	result, err := Discover(context.Background(), Options{Workspace: workspace, HomeDir: home, NativeAgentDir: filepath.Join(home, ".omp", "agent")})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 3 {
		t.Fatalf("context files = %#v warnings=%v", result.Files, result.Warnings)
	}
	if !strings.HasSuffix(result.Files[0].Path, filepath.Join("AGENTS.md")) || !strings.Contains(result.Files[0].Content, "ROOT_CONTEXT") ||
		!strings.Contains(result.Files[0].Content, "SHARED_ARCHITECTURE") || !strings.Contains(result.Files[0].Content, "@literal.md") || !strings.Contains(result.Files[0].Content, "@fenced.md") {
		t.Fatalf("root context/imports = %#v", result.Files[0])
	}
	if result.Files[1].Provider != "github" || result.Files[1].Content != "GITHUB_PACKAGE_CONTEXT" {
		t.Fatalf("same-depth precedence = %#v", result.Files[1])
	}
	if result.Files[2].Provider != "native" || result.Files[2].Level != "user" || result.Files[2].Content != "USER_NATIVE_CONTEXT" {
		t.Fatalf("user precedence = %#v", result.Files[2])
	}
	for _, file := range result.Files {
		if strings.Contains(file.Content, "SHOULD_BE_SHADOWED") || strings.Contains(file.Content, "LOW_PRIORITY_PACKAGE") || strings.Contains(file.Content, "LOW_PRIORITY_USER") {
			t.Fatalf("shadowed context survived: %#v", file)
		}
	}
	if len(result.DirPointers) != 1 || !strings.HasSuffix(result.DirPointers[0], filepath.Join("deep", "AGENTS.md")) {
		t.Fatalf("nested pointers = %v", result.DirPointers)
	}
	rendered := Render(result)
	if !strings.Contains(rendered, "<repo-rules>") || !strings.Contains(rendered, "<dir-context>") || !strings.Contains(rendered, "provider=\"github\"") {
		t.Fatalf("rendered context = %s", rendered)
	}
}

func TestDiscoverCanDisableProvidersAndDedupeIdenticalContent(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	workspace := filepath.Join(root, "child")
	mustMkdir(t, workspace)
	if err := exec.Command("git", "init", "-q", root).Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "SAME_CONTEXT")
	mustWrite(t, filepath.Join(workspace, "AGENTS.md"), "PACKAGE_CONTEXT")
	mustWrite(t, filepath.Join(workspace, ".github", "copilot-instructions.md"), "GITHUB_CONTEXT")
	mustWrite(t, filepath.Join(home, ".omp", "agent", "AGENTS.md"), "SAME_CONTEXT")
	result, err := Discover(context.Background(), Options{
		Workspace: workspace, HomeDir: home, NativeAgentDir: filepath.Join(home, ".omp", "agent"), DisabledProviders: map[string]bool{"github": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 || result.Files[0].Content != "PACKAGE_CONTEXT" || result.Files[1].Level != "user" || result.Files[1].Content != "SAME_CONTEXT" {
		t.Fatalf("disabled/deduped context = %#v", result.Files)
	}
}

func TestImportCyclesAndMissingFilesRemainBounded(t *testing.T) {
	root := t.TempDir()
	one, two := filepath.Join(root, "one.md"), filepath.Join(root, "two.md")
	mustWrite(t, one, "ONE @two.md @missing.md")
	mustWrite(t, two, "TWO @one.md")
	result := expandImports(one, "ONE @two.md @missing.md", root, 0, map[string]bool{one: true})
	if !strings.Contains(result, "ONE") || !strings.Contains(result, "TWO") || !strings.Contains(result, "@one.md") || !strings.Contains(result, "@missing.md") || len(result) > 1024 {
		t.Fatalf("cyclic expansion = %q", result)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
