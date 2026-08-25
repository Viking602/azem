package securityscan_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/securityscan"
)

func remediationGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func remediationRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nconst unsafe = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remediationGit(t, root, "init", "--initial-branch=main")
	remediationGit(t, root, "config", "user.name", "Synthetic User")
	remediationGit(t, root, "config", "user.email", "synthetic@example.test")
	remediationGit(t, root, "add", "--", ".")
	remediationGit(t, root, "commit", "-m", "initial")
	return root
}

func TestPatchUsesIsolatedWorktreeAndIndependentVerifier(t *testing.T) {
	service, _, cleanup := nativeService(t)
	defer cleanup()
	repository := remediationRepository(t)
	scan, err := service.Start(context.Background(), securityscan.StartRequest{
		Repository: repository, TargetKind: securityscan.TargetRepository, Mode: securityscan.ModeStandard,
		Route: securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"}, Deep: securityscan.DefaultDeepOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := waitForTerminalScan(t, service, scan.ID)
	if len(projection.Findings) != 1 {
		t.Fatalf("findings = %#v", projection.Findings)
	}
	results, err := service.Patch(context.Background(), securityscan.PatchRequest{
		OccurrenceIDs: []string{projection.Findings[0].OccurrenceID},
		Route:         securityscan.Route{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "verified" || results[0].Commit == "" || results[0].Branch == "" {
		t.Fatalf("patch results = %#v", results)
	}
	branchContents := remediationGit(t, repository, "show", results[0].Branch+":main.go")
	if !strings.Contains(branchContents, "fixed = true") {
		t.Fatalf("verified branch contents = %q", branchContents)
	}
	liveContents, err := os.ReadFile(filepath.Join(repository, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(liveContents), "unsafe = true") {
		t.Fatalf("live workspace was modified: %q", liveContents)
	}
}
