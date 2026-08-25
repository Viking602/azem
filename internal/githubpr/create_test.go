package githubpr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type createRunner struct {
	calls    []string
	head     string
	fetchURL string
	pushURL  string
}

func (r *createRunner) Run(_ context.Context, _ string, _ string, name string, arguments ...string) ([]byte, error) {
	command := name + " " + strings.Join(arguments, " ")
	r.calls = append(r.calls, command)
	switch {
	case name == "gh" && len(arguments) >= 2 && arguments[0] == "repo" && arguments[1] == "view":
		return json.Marshal(map[string]any{
			"nameWithOwner": "example/repo", "url": "https://github.com/example/repo", "viewerPermission": "WRITE",
			"defaultBranchRef": map[string]any{"name": "main"}, "mergeCommitAllowed": true, "rebaseMergeAllowed": true, "squashMergeAllowed": true,
		})
	case name == "git" && containsArgument(arguments, "remote") && containsArgument(arguments, "--push"):
		if r.pushURL != "" {
			return []byte(r.pushURL + "\n"), nil
		}
		return []byte("git@github.com:example/repo.git\n"), nil
	case name == "git" && containsArgument(arguments, "remote"):
		if r.fetchURL != "" {
			return []byte(r.fetchURL + "\n"), nil
		}
		return []byte("git@github.com:example/repo.git\n"), nil
	case name == "git" && containsArgument(arguments, "rev-parse"):
		return []byte(r.head + "\n"), nil
	case name == "gh" && len(arguments) >= 2 && arguments[0] == "pr" && arguments[1] == "list":
		return []byte("\n"), nil
	case name == "git" && containsArgument(arguments, "push"):
		return nil, nil
	case name == "gh" && len(arguments) >= 2 && arguments[0] == "pr" && arguments[1] == "create":
		return []byte("https://github.com/example/repo/pull/42\n"), nil
	default:
		return nil, fmt.Errorf("unexpected command %s", command)
	}
}

func TestCreatePinsVerifiedPatchCommit(t *testing.T) {
	runner := &createRunner{head: "abcdef1234567890"}
	client := NewClientWithRunner(t.TempDir(), runner)
	created, err := client.Create(context.Background(), CreateRequest{
		Branch: "azem-security/patch-scan", ExpectedCommit: runner.head,
		Title: "fix: patch verified security findings", Body: "Verified patch.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.URL != "https://github.com/example/repo/pull/42" || created.Commit != runner.head {
		t.Fatalf("created = %+v", created)
	}
	calls := strings.Join(runner.calls, "\n")
	if !strings.Contains(calls, "core.hooksPath=/dev/null") || !strings.Contains(calls, " push git@github.com:example/repo.git refs/heads/azem-security/patch-scan:refs/heads/azem-security/patch-scan") {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestCreateUsesOnlyHostCredentialHelperForHTTPS(t *testing.T) {
	runner := &createRunner{
		head: "abcdef1234567890", fetchURL: "https://github.com/example/repo.git", pushURL: "https://github.com/example/repo.git",
	}
	client := NewClientWithRunner(t.TempDir(), runner)
	if _, err := client.Create(context.Background(), CreateRequest{
		Branch: "azem-security/patch-scan", ExpectedCommit: runner.head, Title: "fix: patch", Body: "body",
	}); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(runner.calls, "\n")
	for _, expected := range []string{"credential.helper=", "credential.helper=!gh auth git-credential", "core.askPass="} {
		if !strings.Contains(calls, expected) {
			t.Fatalf("HTTPS push omitted %q: %#v", expected, runner.calls)
		}
	}
}

func TestCreateRejectsChangedPatchBranch(t *testing.T) {
	runner := &createRunner{head: "bbbbbb1234567890"}
	client := NewClientWithRunner(t.TempDir(), runner)
	_, err := client.Create(context.Background(), CreateRequest{
		Branch: "azem-security/patch-scan", ExpectedCommit: "aaaaaa1234567890", Title: "fix: patch", Body: "body",
	})
	if err == nil || !strings.Contains(err.Error(), "changed after verification") {
		t.Fatalf("error = %v", err)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, " push ") || strings.HasPrefix(call, "gh pr create") {
			t.Fatalf("mutation ran after mismatch: %s", call)
		}
	}
}

func TestCreateRejectsMismatchedPushDestination(t *testing.T) {
	runner := &createRunner{head: "abcdef1234567890", pushURL: "git@github.com:other/repo.git"}
	client := NewClientWithRunner(t.TempDir(), runner)
	_, err := client.Create(context.Background(), CreateRequest{
		Branch: "azem-security/patch-scan", ExpectedCommit: runner.head, Title: "fix: patch", Body: "body",
	})
	if err == nil || !strings.Contains(err.Error(), "do not match verified GitHub repository") {
		t.Fatalf("error = %v", err)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, " push ") || strings.HasPrefix(call, "gh pr create") {
			t.Fatalf("mutation ran with mismatched destination: %s", call)
		}
	}
}

func TestGitHubRepositoryFromRemote(t *testing.T) {
	for remote, expected := range map[string]string{
		"https://github.com/example/repo.git": "example/repo",
		"git@github.com:example/repo.git":     "example/repo",
		"ssh://git@github.com/example/repo":   "example/repo",
	} {
		if actual := githubRepositoryFromRemote(remote); actual != expected {
			t.Fatalf("remote %q = %q", remote, actual)
		}
	}
	for _, remote := range []string{"https://example.test/example/repo.git", "file:///tmp/repo", "git@example.test:example/repo.git"} {
		if actual := githubRepositoryFromRemote(remote); actual != "" {
			t.Fatalf("unsafe remote %q accepted as %q", remote, actual)
		}
	}
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}
