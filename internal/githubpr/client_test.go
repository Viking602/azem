package githubpr

import (
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

type runnerFunc func(context.Context, string, string, string, ...string) ([]byte, error)

func (run runnerFunc) Run(ctx context.Context, cwd, stdin, name string, args ...string) ([]byte, error) {
	return run(ctx, cwd, stdin, name, args...)
}

func TestDashboardFindsCurrentPRWhenStatusOmitsCurrentBranch(t *testing.T) {
	runner := runnerFunc(func(_ context.Context, cwd, stdin, name string, args ...string) ([]byte, error) {
		if cwd != "/workspace" {
			t.Fatalf("unexpected cwd %q", cwd)
		}
		if stdin != "" {
			t.Fatalf("read command received stdin %q", stdin)
		}
		command := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(command, "gh repo view "):
			return []byte(`{"nameWithOwner":"octo/demo","url":"https://github.com/octo/demo","defaultBranchRef":{"name":"main"},"viewerPermission":"WRITE","mergeCommitAllowed":true,"rebaseMergeAllowed":true,"squashMergeAllowed":true}`), nil
		case command == "gh api user --jq .login":
			return []byte("octocat\n"), nil
		case command == "git branch --show-current":
			return []byte("feature/pr-ui\n"), nil
		case command == "git status --porcelain=v1 -z --untracked-files=all":
			return nil, nil
		case strings.HasPrefix(command, "gh pr status "):
			return []byte(`{"createdBy":[{"number":42,"title":"PR workspace","state":"OPEN","author":{"login":"octocat"},"headRefName":"feature/pr-ui","headRefOid":"abcdef1234567890","baseRefName":"main","url":"https://github.com/octo/demo/pull/42","updatedAt":"2026-08-03T00:00:00Z","statusCheckRollup":[{"name":"unit","workflowName":"CI","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://example.test/unit"},{"context":"lint","state":"SUCCESS","targetUrl":"https://example.test/lint"},{"name":"deploy","status":"IN_PROGRESS"}]}],"needsReview":[]}`), nil
		case strings.HasPrefix(command, "gh pr list "):
			return []byte(`[{"number":42,"title":"PR workspace","state":"OPEN","author":{"login":"octocat"},"headRefName":"feature/pr-ui","headRefOid":"abcdef1234567890","baseRefName":"main","url":"https://github.com/octo/demo/pull/42","updatedAt":"2026-08-03T00:00:00Z","statusCheckRollup":[]}]`), nil
		default:
			t.Fatalf("unexpected command %q", command)
			return nil, nil
		}
	})

	dashboard, err := NewClientWithRunner("/workspace", runner).Dashboard(context.Background())
	if err != nil {
		t.Fatalf("Dashboard() error = %v", err)
	}
	if !dashboard.Capability.Available || dashboard.Repository == nil {
		t.Fatalf("dashboard capability = %+v, repository = %#v", dashboard.Capability, dashboard.Repository)
	}
	if dashboard.Repository.NameWithOwner != "octo/demo" || dashboard.Repository.ViewerLogin != "octocat" {
		t.Fatalf("repository = %+v", dashboard.Repository)
	}
	if want := []string{"merge", "squash", "rebase"}; !slices.Equal(dashboard.Repository.AllowedMergeMethods, want) {
		t.Fatalf("allowed merge methods = %v, want %v", dashboard.Repository.AllowedMergeMethods, want)
	}
	if dashboard.CurrentBranch != "feature/pr-ui" || dashboard.Current == nil || dashboard.Current.Number != 42 {
		t.Fatalf("current pull request = %#v on %q", dashboard.Current, dashboard.CurrentBranch)
	}
	if dashboard.Current.Author.AvatarURL != "https://avatars.githubusercontent.com/octocat?size=64" {
		t.Fatalf("current pull request author avatar = %q", dashboard.Current.Author.AvatarURL)
	}
	if got := dashboard.Current.Checks; got.Total != 3 || got.Failing != 1 || got.Passing != 1 || got.Pending != 1 {
		t.Fatalf("check summary = %+v", got)
	}
	if len(dashboard.Open) != 1 || dashboard.Open[0].Number != 42 {
		t.Fatalf("open pull requests = %+v", dashboard.Open)
	}
	if dashboard.RefreshedAt.IsZero() {
		t.Fatal("dashboard refresh time is zero")
	}
}

func TestMutationsKeepUserTextOnStdinAndGuardMergeHead(t *testing.T) {
	var mutationArgs [][]string
	var mutationStdin []string
	runner := runnerFunc(func(_ context.Context, _ string, stdin, name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(command, "gh repo view "):
			return []byte(`{"nameWithOwner":"octo/demo","mergeCommitAllowed":true,"rebaseMergeAllowed":true,"squashMergeAllowed":true}`), nil
		case command == "gh api user --jq .login":
			return []byte("octocat\n"), nil
		case len(args) >= 1 && name == "gh" && ((args[0] == "pr" && len(args) >= 2 && (args[1] == "edit" || args[1] == "merge")) || args[0] == "api"):
			mutationArgs = append(mutationArgs, append([]string(nil), args...))
			mutationStdin = append(mutationStdin, stdin)
			return nil, nil
		case strings.HasPrefix(command, "gh pr view "):
			return []byte(`{"number":42,"title":"updated","state":"OPEN","headRefName":"feature/pr-ui","headRefOid":"abcdef1234567890","baseRefName":"main","statusCheckRollup":[]}`), nil
		default:
			t.Fatalf("unexpected command %q", command)
			return nil, nil
		}
	})
	client := NewClientWithRunner("/workspace", runner)

	body := "body with `ticks`, $(commands), and ; separators"
	if _, err := client.Mutate(context.Background(), MutationRequest{
		Number: 42, Kind: MutationEdit, Title: "title; touch /tmp/nope", Body: body, ExpectedRepository: "octo/demo",
	}); err != nil {
		t.Fatalf("edit mutation error = %v", err)
	}
	wantEdit := []string{"pr", "edit", "42", "--repo", "octo/demo", "--title", "title; touch /tmp/nope", "--body-file", "-"}
	if len(mutationArgs) != 1 || !slices.Equal(mutationArgs[0], wantEdit) || mutationStdin[0] != body {
		t.Fatalf("edit call args=%v stdin=%q", mutationArgs, mutationStdin)
	}

	if _, err := client.Mutate(context.Background(), MutationRequest{
		Number: 42, Kind: MutationMerge, MergeMethod: "squash", ExpectedHeadOID: "abcdef1234567890", ExpectedRepository: "octo/demo",
	}); err != nil {
		t.Fatalf("merge mutation error = %v", err)
	}
	wantMerge := []string{"pr", "merge", "42", "--repo", "octo/demo", "--squash", "--match-head-commit", "abcdef1234567890"}
	if len(mutationArgs) != 2 || !slices.Equal(mutationArgs[1], wantMerge) {
		t.Fatalf("merge args = %v, want %v", mutationArgs, wantMerge)
	}

	reviewBody := "Pin this review to the displayed revision."
	if _, err := client.Mutate(context.Background(), MutationRequest{
		Number: 42, Kind: MutationReview, ReviewKind: "request_changes", Body: reviewBody,
		ExpectedHeadOID: "abcdef1234567890", ExpectedRepository: "octo/demo",
	}); err != nil {
		t.Fatalf("review mutation error = %v", err)
	}
	wantReview := []string{"api", "--method", "POST", "repos/octo/demo/pulls/42/reviews", "--input", "-"}
	if len(mutationArgs) != 3 || !slices.Equal(mutationArgs[2], wantReview) {
		t.Fatalf("review args = %v, want %v", mutationArgs, wantReview)
	}
	var reviewPayload map[string]string
	if err := json.Unmarshal([]byte(mutationStdin[2]), &reviewPayload); err != nil {
		t.Fatal(err)
	}
	if reviewPayload["commit_id"] != "abcdef1234567890" || reviewPayload["event"] != "REQUEST_CHANGES" || reviewPayload["body"] != reviewBody {
		t.Fatalf("review payload = %#v", reviewPayload)
	}

	before := len(mutationArgs)
	if _, err := client.Mutate(context.Background(), MutationRequest{
		Number: 42, Kind: MutationAddReviewer, Login: "octocat;rm", ExpectedRepository: "octo/demo",
	}); err == nil {
		t.Fatal("unsafe reviewer login was accepted")
	}
	if _, err := client.Mutate(context.Background(), MutationRequest{
		Number: 42, Kind: MutationMerge, MergeMethod: "squash", ExpectedHeadOID: "not-an-oid", ExpectedRepository: "octo/demo",
	}); err == nil {
		t.Fatal("invalid expected head OID was accepted")
	}
	if _, err := client.Mutate(context.Background(), MutationRequest{
		Number: 42, Kind: MutationReview, ReviewKind: "approve",
		ExpectedHeadOID: "bbbbbbbbbbbbbbbb", ExpectedRepository: "octo/demo",
	}); err == nil || !strings.Contains(err.Error(), "head changed") {
		t.Fatalf("stale review error = %v", err)
	}
	if _, err := client.Mutate(context.Background(), MutationRequest{
		Number: 42, Kind: MutationClose, ExpectedRepository: "octo/other",
	}); err == nil || !strings.Contains(err.Error(), "repository changed") {
		t.Fatalf("repository mismatch error = %v", err)
	}
	if len(mutationArgs) != before {
		t.Fatalf("invalid mutation reached runner: %v", mutationArgs[before:])
	}
}

func TestDetailReResolvesRepositoryAfterWorkspaceRemoteChanges(t *testing.T) {
	repository := "octo/one"
	var detailRepositories []string
	runner := runnerFunc(func(_ context.Context, _ string, _ string, name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(command, "gh repo view "):
			return []byte(`{"nameWithOwner":"` + repository + `","mergeCommitAllowed":true}`), nil
		case command == "gh api user --jq .login":
			return []byte("octocat\n"), nil
		case strings.HasPrefix(command, "gh pr view "):
			for index, argument := range args {
				if argument == "--repo" && index+1 < len(args) {
					detailRepositories = append(detailRepositories, args[index+1])
				}
			}
			return []byte(`{"number":42,"title":"repository scoped","state":"OPEN","headRefName":"feature","headRefOid":"abcdef1234567890","baseRefName":"main","statusCheckRollup":[]}`), nil
		default:
			t.Fatalf("unexpected command %q", command)
			return nil, nil
		}
	})
	client := NewClientWithRunner("/workspace", runner)

	if _, err := client.Detail(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	repository = "octo/two"
	if _, err := client.Detail(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if want := []string{"octo/one", "octo/two"}; !slices.Equal(detailRepositories, want) {
		t.Fatalf("detail repositories = %v, want %v", detailRepositories, want)
	}
}

func TestDashboardReportsMissingGitHubCLIAsCapability(t *testing.T) {
	client := NewClientWithRunner("/workspace", runnerFunc(func(context.Context, string, string, string, ...string) ([]byte, error) {
		return nil, exec.ErrNotFound
	}))
	dashboard, err := client.Dashboard(context.Background())
	if err != nil {
		t.Fatalf("Dashboard() error = %v", err)
	}
	if dashboard.Capability.Available || dashboard.Capability.Code != "not_installed" {
		t.Fatalf("capability = %+v", dashboard.Capability)
	}
}
