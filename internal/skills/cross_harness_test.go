package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
)

func TestCatalogDiscoversCrossHarnessSkillsWithNearestNativePrecedence(t *testing.T) {
	root, home, configDir := t.TempDir(), t.TempDir(), t.TempDir()
	workspace := filepath.Join(root, "packages", "api")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "init", "-q", root).Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	mustWriteSkill(t, filepath.Join(home, ".claude", "skills", "shared"), "shared", "Claude copy", "CLAUDE_SKILL")
	mustWriteSkill(t, filepath.Join(root, ".omp", "skills", "shared"), "shared", "Root native", "ROOT_NATIVE")
	mustWriteSkill(t, filepath.Join(workspace, ".omp", "skills", "shared"), "shared", "Nearest native", "NEAREST_NATIVE")
	mustWriteSkill(t, filepath.Join(workspace, ".github", "skills", "github-skill"), "github-skill", "GitHub skill", "GITHUB_SKILL")
	mustWriteSkill(t, filepath.Join(home, ".config", "opencode", "skills", "opencode-skill"), "opencode-skill", "OpenCode skill", "OPENCODE_SKILL")
	mustWriteSkill(t, filepath.Join(home, ".codex", "skills", "codex-skill"), "codex-skill", "Codex skill", "CODEX_SKILL")

	cfg := config.Default()
	cfg.Skills.TrustProject = true
	catalog, err := Load(LoadOptions{HomeDir: home, ConfigDir: configDir, WorkspaceDir: workspace, Config: cfg.Skills, Discovery: cfg.Discovery})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := catalog.Snapshot()
	shared, ok := snapshot.Registry.Get("shared")
	if !ok || shared.Description != "Nearest native" || strings.TrimSpace(shared.Body) != "NEAREST_NATIVE" {
		t.Fatalf("shared Skill = %#v, %v", shared, ok)
	}
	for _, name := range []string{"github-skill", "opencode-skill", "codex-skill"} {
		if _, ok := snapshot.Registry.Get(name); !ok {
			t.Fatalf("missing cross-harness Skill %q in %#v", name, snapshot.Entries)
		}
	}
}

func TestCatalogDisabledNativeProviderLeavesClaudeSkill(t *testing.T) {
	home := t.TempDir()
	mustWriteSkill(t, filepath.Join(home, ".claude", "skills", "shared"), "shared", "Claude copy", "CLAUDE_SKILL")
	mustWriteSkill(t, filepath.Join(home, ".omp", "agent", "skills", "shared"), "shared", "Native copy", "NATIVE_SKILL")
	cfg := config.Default()
	cfg.Discovery.DisabledProviders = []string{"native"}
	catalog, err := Load(LoadOptions{HomeDir: home, ConfigDir: filepath.Join(home, ".azem"), Config: cfg.Skills, Discovery: cfg.Discovery})
	if err != nil {
		t.Fatal(err)
	}
	shared, ok := catalog.Snapshot().Registry.Get("shared")
	if !ok || shared.Description != "Claude copy" {
		t.Fatalf("disabled provider Skill = %#v, %v", shared, ok)
	}
}

func mustWriteSkill(t *testing.T, directory, name, description, body string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
