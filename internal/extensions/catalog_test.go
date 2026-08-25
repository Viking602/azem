package extensions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAgentsAndThemesUsesNativeThenExtensionPrecedence(t *testing.T) {
	workspace, home, plugin := t.TempDir(), t.TempDir(), t.TempDir()
	native := filepath.Join(home, ".omp", "agent")
	writeExtensionFile(t, filepath.Join(workspace, ".omp", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Project reviewer\nmodel: extension/model\nthinking-level: high\ntools: [coding.read_file]\n---\nReview project code.\n")
	writeExtensionFile(t, filepath.Join(native, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: User reviewer\n---\nReview user code.\n")
	writeExtensionFile(t, filepath.Join(plugin, "agents", "designer.md"), "---\nname: designer\ndescription: Plugin designer\nmodel: ['@design', fallback/model]\nblocking: true\n---\nDesign the requested surface.\n")
	writeExtensionFile(t, filepath.Join(native, "themes", "dark.json"), `{"name":"custom-dark","vars":{"accent":"#fff"},"colors":{"accent":"accent","text":"#fff"}}`)
	writeExtensionFile(t, filepath.Join(plugin, "themes", "light.json"), `{"name":"custom-light","colors":{"accent":"#000","text":"#111"}}`)

	agents, themes, diagnostics, err := Discover(DiscoveryOptions{
		Workspace: workspace, HomeDir: home, NativeAgentDir: native,
		AgentDirs: []string{filepath.Join(plugin, "agents")}, ThemeDirs: []string{filepath.Join(plugin, "themes")},
	})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	if len(agents) != 2 || agents[1].Name != "reviewer" || agents[1].Description != "Project reviewer" || agents[1].Thinking != "high" || agents[1].Models[0] != "extension/model" {
		t.Fatalf("agents = %#v", agents)
	}
	if len(themes) != 2 || themes[0].Name != "custom-dark" || themes[1].Name != "custom-light" {
		t.Fatalf("themes = %#v", themes)
	}
}

func TestDiscoverThemeAcceptsDirectExtensionThemePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "direct.json")
	writeExtensionFile(t, path, `{"name":"direct","colors":{"accent":"#fff"}}`)
	_, themes, diagnostics, err := Discover(DiscoveryOptions{Workspace: root, HomeDir: t.TempDir(), ThemeDirs: []string{path}})
	if err != nil || len(diagnostics) != 0 || len(themes) != 1 || themes[0].Path != path {
		t.Fatalf("themes=%#v diagnostics=%v error=%v", themes, diagnostics, err)
	}
}

func writeExtensionFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
