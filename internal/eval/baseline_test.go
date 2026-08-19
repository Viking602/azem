package eval

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Viking602/venat/message"
)

func TestCaptureBaselineIsDeterministicAndTracksRepositoryState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBaselineFile(t, root, "go.mod", "module example.test/replay\n\ngo 1.25\n")
	writeBaselineFile(t, root, "go.sum", "dependency checksum\n")
	runBaselineCommand(t, root, "git", "init", "-q")
	runBaselineCommand(t, root, "git", "config", "user.name", "Azem Test")
	runBaselineCommand(t, root, "git", "config", "user.email", "azem@example.invalid")
	runBaselineCommand(t, root, "git", "add", "go.mod", "go.sum")
	runBaselineCommand(t, root, "git", "commit", "-q", "-m", "baseline")

	tools := []message.ToolDefinition{
		{Name: "write", Description: "write", InputSchema: message.JSONSchema{Type: "object"}},
		{Name: "read", Description: "read", InputSchema: message.JSONSchema{Type: "object"}},
	}
	options := BaselineOptions{
		Workspace: root, AzemVersion: "1.2.3", AzemCommit: "build-commit", BuildTime: "time",
		Provider: "provider", Model: "model", Reasoning: "high", InstructionFingerprint: "prompt-static",
		TaskPrompt: "fix it", Tools: tools, DependencyFiles: []string{"go.sum", "go.mod"},
		Validators: []ValidatorCommand{{Name: "git", Command: []string{"git", "--version"}}},
	}
	first, err := CaptureBaseline(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	options.Tools[0], options.Tools[1] = options.Tools[1], options.Tools[0]
	second, err := CaptureBaseline(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.ID == "" {
		t.Fatalf("baseline changed with input ordering\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if len(first.Tools) != 2 || first.Tools[0].Name != "read" || len(first.Dependencies) != 2 || first.Validators[0].Status != "available" {
		t.Fatalf("baseline metadata = %+v", first)
	}

	var encoded bytes.Buffer
	if err := WriteBaseline(&encoded, first); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := ReadBaseline(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, first) {
		t.Fatalf("round trip changed baseline\n got: %+v\nwant: %+v", roundTrip, first)
	}

	writeBaselineFile(t, root, "dirty.txt", "changed\n")
	third, err := CaptureBaseline(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if third.RepositoryCommit != first.RepositoryCommit || third.RepositoryDirtySHA256 == first.RepositoryDirtySHA256 || third.ID == first.ID {
		t.Fatalf("dirty state was not reflected\nclean: %+v\ndirty: %+v", first, third)
	}
}
func TestDirtyWorktreeHashIncludesFileBytes(t *testing.T) {
	t.Parallel()
	first := t.TempDir()
	second := t.TempDir()
	writeBaselineFile(t, first, "dirty.txt", "one")
	writeBaselineFile(t, second, "dirty.txt", "two")
	status := []byte(" M dirty.txt\x00")
	hashFirst, err := dirtyWorktreeHash(first, status)
	if err != nil {
		t.Fatal(err)
	}
	hashSecond, err := dirtyWorktreeHash(second, status)
	if err != nil {
		t.Fatal(err)
	}
	if hashFirst == hashSecond {
		t.Fatal("dirty worktree hash ignored file bytes")
	}
}

func TestReadBaselineRejectsTampering(t *testing.T) {
	t.Parallel()
	raw := `{"version":1,"id":"bad","azem_version":"dev","azem_commit":"unknown","provider":"p","model":"m","instruction_fingerprint":"i","task_prompt_sha256":"t","tool_catalog_sha256":"c","tools":[],"repository_commit":"r","repository_dirty_sha256":"d","dependency_lock_sha256":"l","dependencies":[],"validators":[]}`
	if _, err := ReadBaseline(bytes.NewBufferString(raw)); err == nil {
		t.Fatal("accepted tampered baseline identity")
	}
}

func writeBaselineFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runBaselineCommand(t *testing.T, directory, name string, args ...string) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
}
