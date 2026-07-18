package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/config"
)

func writeTestSkill(t *testing.T, root, name, description, body string) string {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n%s\n", name, description, body)
	path := filepath.Join(directory, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReloadIsAtomic(t *testing.T) {
	root := t.TempDir()
	path := writeTestSkill(t, root, "demo", "Demo skill", "DEMO_BODY")
	catalog, err := Load(LoadOptions{Config: config.SkillsConfig{
		Enabled:        true,
		AdditionalDirs: []string{root},
		Eager:          []string{"demo"},
	}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	before := catalog.Snapshot()
	if _, err := before.Registry.Resolve("demo"); err != nil {
		t.Fatalf("resolve demo before reload: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reload(); err == nil {
		t.Fatal("Reload succeeded after the eager skill disappeared")
	}
	after := catalog.Snapshot()
	if _, err := after.Registry.Resolve("demo"); err != nil {
		t.Fatalf("old snapshot was not retained: %v", err)
	}
	if len(after.Eager) != 1 || after.Eager[0] != "demo" {
		t.Fatalf("eager = %#v, want [demo]", after.Eager)
	}
}

func TestLoadPrecedenceAndBundledProtection(t *testing.T) {
	home := t.TempDir()
	configDir := t.TempDir()
	workspace := t.TempDir()
	additionalFirst := t.TempDir()
	additionalLast := t.TempDir()
	roots := []struct {
		path        string
		description string
	}{
		{filepath.Join(home, ".agents", "skills"), "home agents"},
		{filepath.Join(home, ".hydaelyn", "skills"), "home hydaelyn"},
		{filepath.Join(home, ".claude", "skills"), "home claude"},
		{filepath.Join(configDir, "skills"), "config"},
		{filepath.Join(workspace, ".agents", "skills"), "project agents"},
		{filepath.Join(workspace, ".hydaelyn", "skills"), "project hydaelyn"},
		{filepath.Join(workspace, ".claude", "skills"), "project claude"},
		{filepath.Join(workspace, ".azem", "skills"), "project azem"},
		{additionalFirst, "additional first"},
		{additionalLast, "additional last"},
	}
	for _, root := range roots {
		writeTestSkill(t, root.path, "demo", root.description, strings.ToUpper(strings.ReplaceAll(root.description, " ", "_")))
	}
	writeTestSkill(t, additionalLast, "verify", "Disk verify", "DISK_VERIFY_BODY")

	catalog, err := Load(LoadOptions{
		HomeDir:      home,
		ConfigDir:    configDir,
		WorkspaceDir: workspace,
		Config: config.SkillsConfig{
			Enabled:        true,
			TrustProject:   true,
			AdditionalDirs: []string{additionalFirst, additionalLast},
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	snapshot := catalog.Snapshot()
	demo, ok := snapshot.Registry.Get("demo")
	if !ok {
		t.Fatal("demo skill is missing")
	}
	if demo.Description != "additional last" {
		t.Fatalf("demo description = %q, want additional last", demo.Description)
	}
	verify, ok := snapshot.Registry.Get("verify")
	if !ok {
		t.Fatal("verify skill is missing")
	}
	if strings.Contains(verify.Body, "DISK_VERIFY_BODY") || !strings.Contains(verify.Body, "# Verify") {
		t.Fatalf("verify body did not come from the bundled skill: %q", verify.Body)
	}
	var bundledVerify bool
	for _, entry := range snapshot.Entries {
		if entry.Name == "verify" {
			bundledVerify = entry.Bundled
		}
	}
	if !bundledVerify {
		t.Fatal("verify entry is not marked bundled")
	}
	var shadowDiagnostic bool
	for _, diagnostic := range snapshot.Diagnostics {
		if strings.Contains(diagnostic.Message, `skill "verify"`) && strings.Contains(diagnostic.Message, "bundled") {
			shadowDiagnostic = true
			break
		}
	}
	if !shadowDiagnostic {
		t.Fatalf("bundled shadow diagnostic missing: %#v", snapshot.Diagnostics)
	}
}

func TestProjectTrustDisabledAndEagerValidation(t *testing.T) {
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, ".azem", "skills")
	writeTestSkill(t, projectRoot, "project-only", "Project skill", "PROJECT_BODY")

	untrusted, err := Load(LoadOptions{
		WorkspaceDir: workspace,
		Config:       config.SkillsConfig{Enabled: true, TrustProject: false},
	})
	if err != nil {
		t.Fatalf("Load untrusted project: %v", err)
	}
	if _, ok := untrusted.Snapshot().Registry.Get("project-only"); ok {
		t.Fatal("project skill loaded while trust_project was false")
	}

	additional := t.TempDir()
	writeTestSkill(t, additional, "disabled-demo", "Disabled demo", "DISABLED_BODY")
	disabled, err := Load(LoadOptions{Config: config.SkillsConfig{
		Enabled:        true,
		AdditionalDirs: []string{additional},
		Disabled:       []string{"disabled-demo", "missing-disabled"},
	}})
	if err != nil {
		t.Fatalf("Load disabled skills: %v", err)
	}
	disabledSnapshot := disabled.Snapshot()
	if _, ok := disabledSnapshot.Registry.Get("disabled-demo"); ok {
		t.Fatal("disabled skill remains in the registry")
	}
	var disabledEntry bool
	for _, entry := range disabledSnapshot.Entries {
		if entry.Name == "disabled-demo" {
			disabledEntry = entry.Disabled
		}
	}
	if !disabledEntry {
		t.Fatal("disabled skill is not visible as a disabled catalog entry")
	}

	if _, err := Load(LoadOptions{Config: config.SkillsConfig{
		Enabled: true,
		Eager:   []string{"missing-eager"},
	}}); err == nil {
		t.Fatal("Load accepted a missing eager skill")
	}

	off, err := Load(LoadOptions{
		WorkspaceDir: workspace,
		Config: config.SkillsConfig{
			Enabled:      false,
			TrustProject: true,
			Eager:        []string{"missing-eager"},
		},
	})
	if err != nil {
		t.Fatalf("Load disabled catalog: %v", err)
	}
	offSnapshot := off.Snapshot()
	if len(offSnapshot.Entries) != 0 || len(offSnapshot.Registry.List()) != 0 {
		t.Fatalf("disabled catalog is not empty: %#v", offSnapshot)
	}
}

func TestCatalogBudgetAndResources(t *testing.T) {
	root := t.TempDir()
	longDescription := strings.Repeat("界", 300)
	for i := range 40 {
		writeTestSkill(t, root, fmt.Sprintf("demo-%02d", i), longDescription, "BODY")
	}
	referencePath := filepath.Join(root, "demo-00", "reference.txt")
	if err := os.WriteFile(referencePath, []byte("reference fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	symlinkRoot := t.TempDir()
	writeTestSkill(t, symlinkRoot, "bad-symlink", "Bad symlink resource", "BODY")
	if err := os.Symlink(referencePath, filepath.Join(symlinkRoot, "bad-symlink", "link.txt")); err != nil {
		t.Fatal(err)
	}
	largeRoot := t.TempDir()
	writeTestSkill(t, largeRoot, "bad-large", "Bad large resource", "BODY")
	largePath := filepath.Join(largeRoot, "bad-large", "large.bin")
	if err := os.WriteFile(largePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(largePath, (8<<20)+1); err != nil {
		t.Fatal(err)
	}

	catalog, err := Load(LoadOptions{Config: config.SkillsConfig{
		Enabled:        true,
		AdditionalDirs: []string{root, symlinkRoot, largeRoot},
	}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	snapshot := catalog.Snapshot()
	demo, ok := snapshot.Registry.Get("demo-00")
	if !ok {
		t.Fatal("demo-00 is missing")
	}
	if got := utf8.RuneCountInString(demo.Description); got != maxModelSkillDescriptionChars {
		t.Fatalf("model description has %d runes, want %d", got, maxModelSkillDescriptionChars)
	}
	if len(demo.Resources) != 1 || demo.Resources[0].Name != "reference.txt" {
		t.Fatalf("resources = %#v, want reference.txt", demo.Resources)
	}

	var manualOnly bool
	var fullDescription bool
	var bundledVisible int
	for _, entry := range snapshot.Entries {
		if entry.Name == "demo-00" {
			fullDescription = utf8.RuneCountInString(entry.Description) == 300 && entry.ResourceCount == 1
		}
		if strings.HasPrefix(entry.Name, "demo-") && !entry.ModelVisible {
			manualOnly = true
		}
		if entry.Bundled && entry.ModelVisible {
			bundledVisible++
		}
	}
	if !fullDescription {
		t.Fatal("entry did not preserve the full description and resource count")
	}
	if !manualOnly {
		t.Fatal("budget did not leave any skill manual-only")
	}
	if bundledVisible != len(bundledSkillPaths) {
		t.Fatalf("visible bundled skills = %d, want %d", bundledVisible, len(bundledSkillPaths))
	}

	var rendered strings.Builder
	if len(snapshot.Available) > 0 {
		rendered.WriteString(modelSkillCatalogPrefix)
		for _, name := range snapshot.Available {
			current, ok := snapshot.Registry.Get(name)
			if !ok {
				t.Fatalf("available skill %q is missing from registry", name)
			}
			fmt.Fprintf(&rendered, "- %s: %s\n", current.Name, current.Description)
		}
	}
	directory := strings.TrimSuffix(rendered.String(), "\n")
	if got := utf8.RuneCountInString(directory); got > modelSkillCatalogCharBudget {
		t.Fatalf("model catalog has %d characters, budget is %d", got, modelSkillCatalogCharBudget)
	}

	var symlinkDiagnostic, largeDiagnostic bool
	for _, diagnostic := range snapshot.Diagnostics {
		symlinkDiagnostic = symlinkDiagnostic || strings.Contains(diagnostic.Message, "must not be a symlink")
		largeDiagnostic = largeDiagnostic || strings.Contains(diagnostic.Message, "exceeds 8 MiB")
	}
	if !symlinkDiagnostic || !largeDiagnostic {
		t.Fatalf("resource diagnostics missing: %#v", snapshot.Diagnostics)
	}
}

func TestBundledSkills(t *testing.T) {
	catalog, err := Load(LoadOptions{Config: config.SkillsConfig{Enabled: true}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	expected := map[string]struct {
		description string
		bodyMarker  string
	}{
		"verify": {
			description: "Verify the requested behavior with the strongest available evidence before reporting completion.",
			bodyMarker:  "Before reporting completion:",
		},
		"simplify": {
			description: "Review changed code for reuse, clarity, correctness, and unnecessary work, then fix confirmed issues and re-verify.",
			bodyMarker:  "Review only the code relevant to the current change:",
		},
		"skill-author": {
			description: "Create or refine an Agent Skills-compatible SKILL.md for a repeatable workflow.",
			bodyMarker:  "Capture one repeatable workflow as an Agent Skill:",
		},
	}
	snapshot := catalog.Snapshot()
	for name, want := range expected {
		current, ok := snapshot.Registry.Get(name)
		if !ok {
			t.Fatalf("bundled skill %q is missing", name)
		}
		if current.Description != want.description || !strings.Contains(current.Body, want.bodyMarker) {
			t.Fatalf("bundled skill %q content mismatch", name)
		}
	}
	if len(snapshot.Registry.List()) != len(expected) {
		t.Fatalf("bundled registry contains %d skills, want %d", len(snapshot.Registry.List()), len(expected))
	}
}
