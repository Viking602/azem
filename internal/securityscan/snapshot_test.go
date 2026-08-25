package securityscan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTestCommand(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func testRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "context.go"), []byte("package main\nconst contextValue = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, root, "init", "--initial-branch=main")
	gitTestCommand(t, root, "config", "user.name", "Synthetic User")
	gitTestCommand(t, root, "config", "user.email", "synthetic@example.test")
	gitTestCommand(t, root, "add", "--", ".")
	gitTestCommand(t, root, "commit", "-m", "initial")
	return root
}

func TestSnapshotterCreatesImmutableScopeAndDetectsDrift(t *testing.T) {
	repository := testRepository(t)
	snapshotter := Snapshotter{DataRoot: t.TempDir()}
	request := StartRequest{
		Repository: repository, TargetKind: TargetPaths, Paths: []string{"src"}, Mode: ModeStandard,
		Route: Route{Provider: "chatgpt", Model: "gpt-test"}, Deep: DefaultDeepOptions(),
	}
	target, output, err := snapshotter.Prepare(context.Background(), "scan_snapshot", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(target.Inventory) != 2 || len(target.SnapshotPaths) != 2 || output == "" {
		t.Fatalf("target = %+v output=%q", target, output)
	}
	defer func() {
		if err := snapshotter.Cleanup(target); err != nil {
			t.Error(err)
		}
	}()
	metadata, err := os.Stat(filepath.Join(target.SnapshotRoot, "src", "main.go"))
	if err != nil || metadata.Mode().Perm() != 0o400 {
		t.Fatalf("snapshot file mode = %v, %v", metadata, err)
	}
	changed, err := snapshotter.Changed(context.Background(), target)
	if err != nil || changed {
		t.Fatalf("fresh snapshot changed=%t error=%v", changed, err)
	}
	if err := os.WriteFile(filepath.Join(repository, "src", "main.go"), []byte("package main\nfunc main(){ panic(1) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err = snapshotter.Changed(context.Background(), target)
	if err != nil || !changed {
		t.Fatalf("modified snapshot changed=%t error=%v", changed, err)
	}
}

func TestCommittedDiffSnapshotsContextButScopesChangedFiles(t *testing.T) {
	repository := testRepository(t)
	base := gitTestCommand(t, repository, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repository, "src", "main.go"), []byte("package main\nfunc main(){ println(contextValue) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repository, "add", "--", ".")
	gitTestCommand(t, repository, "commit", "-m", "change main")
	snapshotter := Snapshotter{DataRoot: t.TempDir()}
	target, _, err := snapshotter.Prepare(context.Background(), "scan_diff", StartRequest{
		Repository: repository, TargetKind: TargetGitRefs, Base: base, Head: "HEAD", Mode: ModeStandard,
		Route: Route{Provider: "chatgpt", Model: "gpt-test"}, Deep: DefaultDeepOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(target.Inventory) != 2 || target.Inventory[0] != target.DiffArtifact || target.Inventory[1] != "src/main.go" {
		t.Fatalf("diff inventory = %#v artifact=%q", target.Inventory, target.DiffArtifact)
	}
	if len(target.ScopePaths) != 1 || target.ScopePaths[0] != "src/main.go" || len(target.SnapshotPaths) != 3 {
		t.Fatalf("snapshot context = %#v scope=%#v", target.SnapshotPaths, target.ScopePaths)
	}
	defer func() {
		if err := snapshotter.Cleanup(target); err != nil {
			t.Error(err)
		}
	}()
	if _, err := os.Stat(filepath.Join(target.SnapshotRoot, "src", "context.go")); err != nil {
		t.Fatalf("unchanged context missing: %v", err)
	}
}

func TestCommittedDiffPreservesDeletedFileEvidence(t *testing.T) {
	repository := testRepository(t)
	base := gitTestCommand(t, repository, "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(repository, "src", "context.go")); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repository, "add", "--", ".")
	gitTestCommand(t, repository, "commit", "-m", "delete context")
	snapshotter := Snapshotter{DataRoot: t.TempDir()}
	target, _, err := snapshotter.Prepare(context.Background(), "scan_deleted_diff", StartRequest{
		Repository: repository, TargetKind: TargetGitRefs, Base: base, Head: "HEAD", Mode: ModeStandard,
		Route: Route{Provider: "chatgpt", Model: "gpt-test"}, Deep: DefaultDeepOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := snapshotter.Cleanup(target); err != nil {
			t.Error(err)
		}
	}()
	if len(target.ScopePaths) != 1 || target.ScopePaths[0] != "src/context.go" {
		t.Fatalf("deleted scope = %#v", target.ScopePaths)
	}
	if len(target.Inventory) != 1 || target.Inventory[0] != target.DiffArtifact {
		t.Fatalf("deleted review inventory = %#v", target.Inventory)
	}
	evidence, err := os.ReadFile(filepath.Join(target.SnapshotRoot, filepath.FromSlash(target.DiffArtifact)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(evidence), "const contextValue = 1") || !strings.Contains(string(evidence), "deleted file mode") {
		t.Fatalf("deleted evidence = %q", evidence)
	}
}

func TestSnapshotterRejectsSymlinkedParentDirectory(t *testing.T) {
	repository := testRepository(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", "context.go"} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte("outside secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(repository, "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, "src")); err != nil {
		t.Fatal(err)
	}
	_, _, err := (Snapshotter{DataRoot: t.TempDir()}).Prepare(context.Background(), "scan_symlink_parent", StartRequest{
		Repository: repository, TargetKind: TargetRepository, Mode: ModeStandard,
		Route: Route{Provider: "chatgpt", Model: "gpt-test"}, Deep: DefaultDeepOptions(),
	})
	if err == nil || !strings.Contains(err.Error(), "symlinked snapshot path") {
		t.Fatalf("symlinked parent error = %v", err)
	}
}
