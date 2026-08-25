package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/app"
)

func TestPrintVersion(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := version, gitCommit, buildTime
	version, gitCommit, buildTime = "v1.2.3", "abc1234", "2026-03-20T12:34:56Z"
	t.Cleanup(func() {
		version, gitCommit, buildTime = oldVersion, oldCommit, oldBuildTime
	})

	var output bytes.Buffer
	printVersion(&output)
	const want = "azem v1.2.3\ngit commit: abc1234\nbuild time: 2026-03-20T12:34:56Z\n"
	if output.String() != want {
		t.Fatalf("version output = %q, want %q", output.String(), want)
	}
}

func TestParseCLIHeadlessModesAndDurations(t *testing.T) {
	options, err := parseCLI([]string{"-p", "--mode", "json", "--max-time", "90", "--print-thoughts", "--yolo", "prompt"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !options.print || options.mode != "json" || options.maxTime != 90*time.Second || !options.printThinking || !options.autoApprove || len(options.prompts) != 1 {
		t.Fatalf("options=%#v", options)
	}
	if _, err := parseCLI([]string{"--mode", "unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted unknown mode")
	}
	if _, err := parseCLI([]string{"--max-time", "0"}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted zero max-time")
	}
}

func TestReadPipedInputAndPromptCombination(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("piped context\n"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	piped, err := readPipedInput(reader, 1024)
	reader.Close()
	if err != nil || piped != "piped context" {
		t.Fatalf("piped=%q error=%v", piped, err)
	}
	prompts := combinePrompts(piped, []string{"positional", "next"})
	if len(prompts) != 2 || prompts[0] != "piped context\npositional" || prompts[1] != "next" {
		t.Fatalf("prompts=%#v", prompts)
	}
}

func TestRunLaunchShutsDownBootstrapOnProtocolValidationError(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("AZEM_HOME", home)
	t.Chdir(workspace)
	err := runLaunch([]string{"--mode", "rpc", "unexpected"})
	if err == nil || !strings.Contains(err.Error(), "does not accept positional prompts") {
		t.Fatalf("runLaunch error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	boot, err := app.Bootstrap(ctx, workspace, "")
	if err != nil {
		t.Fatalf("bootstrap after rejected protocol mode: %v", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := boot.Service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}
