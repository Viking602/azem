package rules

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverCrossHarnessRulesWithPrecedenceAndBuckets(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	workspace := filepath.Join(root, "packages", "api")
	mustMkdir(t, workspace)
	if err := exec.Command("git", "init", "-q", root).Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	mustWrite(t, filepath.Join(workspace, ".omp", "rules", "native.md"), "---\ndescription: Native project\nalwaysApply: true\n---\nNATIVE_ALWAYS")
	mustWrite(t, filepath.Join(home, ".omp", "agent", "rules", "native.md"), "---\ndescription: shadowed\n---\nSHADOWED_NATIVE")
	mustWrite(t, filepath.Join(home, ".omp", "agent", "RULES.md"), "USER_STICKY")
	mustWrite(t, filepath.Join(workspace, ".omp", "RULES.md"), "PROJECT_STICKY")
	mustWrite(t, filepath.Join(root, ".agent", "rules", "agent-rule.mdc"), "---\ndescription: Agent rule\nglobs: [\"**/*.go\"]\n---\nAGENT_RULE")
	mustWrite(t, filepath.Join(home, ".cursor", "rules", "guard.mdc"), "---\ncondition: '*.ts'\nastCondition: console.log($MSG)\nscope: \"text, thinking\"\ninterruptMode: tool-only\n---\nCURSOR_TTSR")
	mustWrite(t, filepath.Join(home, ".codeium", "windsurf", "memories", "global_rules.md"), "---\ndescription: Windsurf global\n---\nWINDSURF_GLOBAL")
	mustWrite(t, filepath.Join(workspace, ".windsurf", "rules", "guard.md"), "---\ndescription: shadowed by cursor\n---\nWINDSURF_SHADOWED")
	mustWrite(t, filepath.Join(root, ".clinerules"), "---\ndescription: Cline nearest\n---\nCLINE_RULE")
	mustWrite(t, filepath.Join(workspace, ".github", "instructions", "all.instructions.md"), "---\napplyTo: '**/*'\n---\nGITHUB_ALWAYS")
	mustWrite(t, filepath.Join(workspace, ".github", "instructions", "go.instructions.md"), "---\napplyTo: '**/*.go, **/*.mod'\n---\nGITHUB_GO")
	mustWrite(t, filepath.Join(workspace, ".github", "instructions", "missing.instructions.md"), "MISSING_APPLY_TO")

	result, err := Discover(context.Background(), Options{Workspace: workspace, HomeDir: home, NativeAgentDir: filepath.Join(home, ".omp", "agent")})
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Rule)
	for _, rule := range result.Rules {
		byName[rule.Name] = rule
	}
	for _, name := range []string{"native", "RULES", "RULES@project", "agent-rule", "guard", "global_rules", "clinerules", "all", "go", "missing"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing %q in %#v", name, result.Rules)
		}
	}
	if byName["native"].Content != "NATIVE_ALWAYS" || byName["guard"].Content != "CURSOR_TTSR" {
		t.Fatalf("provider precedence failed: native=%#v guard=%#v", byName["native"], byName["guard"])
	}
	if !byName["RULES"].AlwaysApply || !byName["RULES@project"].AlwaysApply || !byName["all"].AlwaysApply {
		t.Fatalf("sticky/always rules = %#v %#v %#v", byName["RULES"], byName["RULES@project"], byName["all"])
	}
	if got := strings.Join(byName["go"].Globs, ","); got != "**/*.go,**/*.mod" || byName["go"].Description == "" {
		t.Fatalf("github glob rule = %#v", byName["go"])
	}
	guard := byName["guard"]
	if strings.Join(guard.Conditions, ",") != ".*" || !contains(guard.Scope, "tool:edit(*.ts)") || !contains(guard.Scope, "thinking") || guard.InterruptMode != "tool-only" {
		t.Fatalf("TTSR normalization = %#v", guard)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, "\n"), "missing applyTo") {
		t.Fatalf("GitHub warning = %v", result.Warnings)
	}
	prompt := Render(result, "NATIVE_ALWAYS")
	if strings.Contains(prompt, "NATIVE_ALWAYS") || !strings.Contains(prompt, "USER_STICKY") || !strings.Contains(prompt, "rule://<name>") || !strings.Contains(prompt, "agent-rule") {
		t.Fatalf("rendered rules = %s", prompt)
	}
	ttsr := TTSRRules(result)
	if len(ttsr) != 1 || ttsr[0].Name != "guard" || ttsr[0].ASTConditions[0] != "console.log($MSG)" {
		t.Fatalf("TTSR rules = %#v", ttsr)
	}
	catalog := NewCatalog(result)
	if value, ok := catalog.Get("go"); !ok || value.Content != "GITHUB_GO" {
		t.Fatalf("catalog get = %#v %v", value, ok)
	}
}

func TestDiscoverRulesHonorsDisabledProviderAndRule(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".cursor", "rules", "cursor.md"), "---\ndescription: cursor\n---\nCURSOR")
	mustWrite(t, filepath.Join(root, ".windsurf", "rules", "windsurf.md"), "---\ndescription: windsurf\n---\nWINDSURF")
	result, err := Discover(context.Background(), Options{Workspace: root, HomeDir: t.TempDir(), DisabledProviders: map[string]bool{"cursor": true}, DisabledRules: map[string]bool{"windsurf": true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 0 {
		t.Fatalf("disabled rules survived: %#v", result.Rules)
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
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
