package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverCommandsHonorsProviderPrecedenceAndPromptSuffix(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	native := filepath.Join(home, ".omp", "agent")
	writeCommand(t, filepath.Join(native, "commands", "review.md"), "---\ndescription: Native review\n---\nNative $1")
	writeCommand(t, filepath.Join(home, ".claude", "commands", "review.md"), "Claude $1")
	writeCommand(t, filepath.Join(workspace, ".github", "prompts", "explain.prompt.md"), "Explain $ARGUMENTS")
	catalog, diagnostics, err := Discover(Options{Workspace: workspace, HomeDir: home, NativeAgentDir: native})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("discover diagnostics=%v error=%v", diagnostics, err)
	}
	entries := catalog.Entries()
	if len(entries) != 2 || entries[0].Name != "explain" || entries[1].Provider != "native" || entries[1].Description != "Native review" {
		t.Fatalf("entries = %#v", entries)
	}
	if expanded, ok := catalog.Expand(`/review "one two"`); !ok || expanded != "Native one two" {
		t.Fatalf("native expansion = %q, %v", expanded, ok)
	}
}

func TestCommandExpansionSupportsPositionalSlicesTemplatesAndFallback(t *testing.T) {
	root := t.TempDir()
	writeCommand(t, filepath.Join(root, ".omp", "commands", "slice.md"), `$1|$2|$@[2:2]|{{arg 3}}|{{arguments}}`)
	writeCommand(t, filepath.Join(root, ".omp", "commands", "fallback.md"), "Review this change")
	catalog, _, err := Discover(Options{Workspace: root, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if expanded, ok := catalog.Expand(`/slice one "two words" three four`); !ok || expanded != "one|two words|two words three|three|one two words three four" {
		t.Fatalf("slice expansion = %q, %v", expanded, ok)
	}
	if expanded, ok := catalog.Expand(`/fallback src/main.go`); !ok || expanded != "Review this change\n\nsrc/main.go" {
		t.Fatalf("fallback expansion = %q, %v", expanded, ok)
	}
	if expanded, ok := catalog.Expand(`/unknown x`); ok || expanded != "/unknown x" {
		t.Fatalf("unknown expansion = %q, %v", expanded, ok)
	}
}

func TestDisabledCommandProviderIsExcluded(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	writeCommand(t, filepath.Join(home, ".claude", "commands", "review.md"), "Claude")
	catalog, _, err := Discover(Options{Workspace: workspace, HomeDir: home, DisabledProviders: map[string]bool{"claude": true}})
	if err != nil {
		t.Fatal(err)
	}
	if entries := catalog.Entries(); len(entries) != 0 {
		t.Fatalf("disabled commands = %#v", entries)
	}
}

func writeCommand(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
