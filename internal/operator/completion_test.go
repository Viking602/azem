package operator

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCompletionScriptsContainEveryVisibleCommandAndGlobalFlag(t *testing.T) {
	registry := New()
	handler := func(context.Context, []string, IO) error { return nil }
	for _, command := range []Command{
		{Path: []string{"session", "list"}, Summary: "List", Handler: handler},
		{Path: []string{"session", "export"}, Summary: "Export", Handler: handler},
		{Path: []string{"auth-broker", "serve"}, Summary: "Serve", Handler: handler},
		{Path: []string{"hidden"}, Summary: "Hidden", Hidden: true, Handler: handler},
	} {
		if err := registry.Register(command); err != nil {
			t.Fatal(err)
		}
	}
	for _, shell := range []Shell{ShellBash, ShellZsh, ShellFish} {
		var output bytes.Buffer
		if err := WriteCompletion(&output, shell, registry, CompletionOptions{Program: "azem", GlobalFlags: []string{"--mode", "-p"}}); err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		script := output.String()
		for _, required := range []string{"azem", "session", "list", "export", "auth-broker", "serve", "mode"} {
			if !strings.Contains(script, required) {
				t.Fatalf("%s completion missing %q:\n%s", shell, required, script)
			}
		}
		if strings.Contains(script, "hidden") {
			t.Fatalf("%s completion exposed hidden command:\n%s", shell, script)
		}
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	if err := WriteCompletion(&bytes.Buffer{}, Shell("powershell"), New(), CompletionOptions{}); err == nil {
		t.Fatal("accepted unsupported completion shell")
	}
}
