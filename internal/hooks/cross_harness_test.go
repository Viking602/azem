package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverHarnessScriptsLoadsNativeClaudeAndCodexHooks(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	native := filepath.Join(home, ".omp", "agent")
	mustWriteHookScript(t, filepath.Join(workspace, ".omp", "hooks", "pre", "write.sh"))
	mustWriteHookScript(t, filepath.Join(home, ".claude", "hooks", "post", "read.sh"))
	mustWriteHookScript(t, filepath.Join(home, ".codex", "hooks", "pre-bash.sh"))

	scripts, diagnostics := DiscoverHarnessScripts(HarnessDiscoveryOptions{
		Workspace: workspace, HomeDir: home, NativeAgentDir: native, TrustProject: true,
	})
	if len(diagnostics) != 0 || len(scripts) != 3 {
		t.Fatalf("scripts=%#v diagnostics=%#v", scripts, diagnostics)
	}
	registry := Discover(Options{Scripts: scripts, DefaultTimeout: time.Second})
	if len(registry.Diagnostics) != 0 {
		t.Fatalf("registry diagnostics = %#v", registry.Diagnostics)
	}
	pre := registry.Commands(PreToolUse)
	post := registry.Commands(PostToolUse)
	if len(pre) != 2 || len(post) != 1 {
		t.Fatalf("pre=%#v post=%#v", pre, post)
	}
	if !commandFor(pre, "coding.write_file") || !commandFor(pre, "coding.shell") || !commandFor(post, "coding.read_file") {
		t.Fatalf("hook aliases pre=%#v post=%#v", pre, post)
	}
	for _, command := range append(pre, post...) {
		if !command.direct {
			t.Fatalf("script hook is not direct: %#v", command)
		}
	}
	for _, command := range pre {
		if command.Matches("coding.write_file") {
			result := (Runner{Workspace: workspace}).Run(context.Background(), command, Envelope{HookEventName: PreToolUse, ToolName: "coding.write_file"})
			if result.Failure != nil || result.ExitCode != 0 {
				t.Fatalf("direct discovered Hook execution = %#v", result)
			}
		}
	}
}

func TestDiscoverHarnessScriptsHonorsProviderAndProjectTrust(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	mustWriteHookScript(t, filepath.Join(workspace, ".omp", "hooks", "pre", "write.sh"))
	mustWriteHookScript(t, filepath.Join(home, ".codex", "hooks", "pre-bash.sh"))
	scripts, _ := DiscoverHarnessScripts(HarnessDiscoveryOptions{
		Workspace: workspace, HomeDir: home, TrustProject: false, DisabledProviders: map[string]bool{"codex": true},
	})
	if len(scripts) != 0 {
		t.Fatalf("untrusted/disabled scripts = %#v", scripts)
	}
}

func commandFor(commands []Command, tool string) bool {
	for _, command := range commands {
		if command.Matches(tool) {
			return true
		}
	}
	return false
}

func mustWriteHookScript(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
