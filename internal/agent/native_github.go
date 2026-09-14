package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/githubpr"
	"github.com/Viking602/venat/tool"
)

const ToolGitHub = "github"

type githubDriver struct {
	client        *githubpr.Client
	networkPolicy string
	allowWrite    bool
	root          string
}

func (d *githubDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolGitHub, Description: "GitHub repository/file/search/Actions reads and PR dashboard/detail/create/mutate using native gh authentication. Read operations accept repo (owner/repo), query, path, branch, run, limit as relevant. Mutations reuse repository and expected-head guards through request; create requires branch and expectedCommit. Checkout and arbitrary git commands use governed coding.shell.", InputSchema: tool.Schema{Type: "object", AdditionalProperties: &additional, Required: []string{"op"}, Properties: map[string]tool.Schema{
		"op": {Type: "string", Enum: []string{"dashboard", "detail", "create", "mutate", "repo_view", "file_read", "search_issues", "search_prs", "search_code", "search_commits", "search_repos", "run_view", "run_watch"}}, "number": {Type: "integer"},
		"repo": {Type: "string"}, "query": {Type: "string"}, "path": {Type: "string"}, "branch": {Type: "string"}, "run": {Type: "integer"}, "limit": {Type: "integer"},
		"request": {Type: "object", Properties: map[string]tool.Schema{
			"number": {Type: "integer"}, "kind": {Type: "string"}, "title": {Type: "string"}, "body": {Type: "string"}, "login": {Type: "string"}, "reviewKind": {Type: "string"}, "mergeMethod": {Type: "string"}, "expectedHeadOid": {Type: "string"}, "expectedRepository": {Type: "string"}, "branch": {Type: "string"}, "expectedCommit": {Type: "string"},
		}, AdditionalProperties: &additional},
	}}, Concurrency: tool.ConcurrencyParallel}
}

func (*githubDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	var input struct {
		Op string `json:"op"`
	}
	if json.Unmarshal(call.Arguments, &input) == nil && githubReadOperation(input.Op) {
		return readOnlyPolicy("github", "network")
	}
	return approvedExternalPolicy("github", "network", "publish")
}

func (d *githubDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Op      string          `json:"op"`
		Number  int             `json:"number"`
		Request json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if d.networkPolicy != "allow" {
		return toolError(call, "GitHub requires workspace network policy allow"), nil
	}
	if !d.allowWrite && !githubReadOperation(input.Op) {
		return toolError(call, "GitHub mutations are disabled in this workspace"), nil
	}
	if githubReadOperation(input.Op) && input.Op != "dashboard" && input.Op != "detail" {
		return d.read(ctx, call)
	}
	var result any
	var err error
	switch input.Op {
	case "dashboard":
		var dashboard githubpr.Dashboard
		dashboard, err = d.client.Dashboard(ctx)
		if err == nil && !dashboard.Capability.Available {
			err = fmt.Errorf("%s: %s", dashboard.Capability.Code, dashboard.Capability.Message)
		}
		result = dashboard
	case "detail":
		result, err = d.client.Detail(ctx, input.Number)
	case "mutate":
		var request githubpr.MutationRequest
		if err = json.Unmarshal(input.Request, &request); err == nil {
			result, err = d.client.Mutate(ctx, request)
		}
	case "create":
		var request githubpr.CreateRequest
		if err = json.Unmarshal(input.Request, &request); err == nil {
			result, err = d.client.Create(ctx, request)
		}
	default:
		err = fmt.Errorf("unsupported GitHub operation %q", input.Op)
	}
	if err != nil {
		return toolError(call, "github: "+err.Error()), nil
	}
	return nativeJSONResult(call, result)
}

func githubReadOperation(operation string) bool {
	switch operation {
	case "dashboard", "detail", "repo_view", "file_read", "search_issues", "search_prs", "search_code", "search_commits", "search_repos", "run_view", "run_watch":
		return true
	}
	return false
}

func (d *githubDriver) read(ctx context.Context, call tool.Call) (tool.Result, error) {
	var input struct {
		Op, Repo, Query, Path, Branch string
		Run, Limit                    int
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if len(call.Arguments) > 64<<10 || input.Limit < 0 || input.Limit > 100 || input.Run < 0 {
		return toolError(call, "GitHub arguments exceed bounds"), nil
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Repo == "" && input.Op != "search_repos" {
		repo, err := nativeRun(ctx, d.root, "", "gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		input.Repo = strings.TrimSpace(repo)
	}
	if input.Repo != "" && !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(input.Repo) {
		return toolError(call, "repo must be owner/repo"), nil
	}
	endpoint := "repos/" + input.Repo
	switch input.Op {
	case "repo_view":
	case "file_read":
		if input.Path == "" || strings.HasPrefix(input.Path, "/") {
			return toolError(call, "path must be repository-relative"), nil
		}
		parts := strings.Split(input.Path, "/")
		for index, part := range parts {
			if part == ".." || part == "." || part == "" {
				return toolError(call, "invalid repository path"), nil
			}
			parts[index] = url.PathEscape(part)
		}
		endpoint += "/contents/" + strings.Join(parts, "/")
		if input.Branch != "" {
			endpoint += "?ref=" + url.QueryEscape(input.Branch)
		}
	case "run_view", "run_watch":
		if input.Run < 1 {
			return toolError(call, "run must be a positive Actions run id"), nil
		}
		if input.Op == "run_watch" {
			output, err := nativeRun(ctx, d.root, "", "gh", "run", "watch", strconv.Itoa(input.Run), "--repo", input.Repo, "--exit-status", "--compact")
			if err != nil {
				return toolError(call, err.Error()), nil
			}
			return nativeJSONResult(call, map[string]string{"output": output})
		}
		endpoint += "/actions/runs/" + strconv.Itoa(input.Run)
	default:
		kind := map[string]string{"search_issues": "issues", "search_prs": "issues", "search_code": "code", "search_commits": "commits", "search_repos": "repositories"}[input.Op]
		if kind == "" || strings.TrimSpace(input.Query) == "" {
			return toolError(call, "search query is required"), nil
		}
		query := input.Query
		if input.Repo != "" {
			query += " repo:" + input.Repo
		}
		if input.Op == "search_prs" {
			query += " is:pr"
		} else if input.Op == "search_issues" {
			query += " is:issue"
		}
		endpoint = "search/" + kind + "?q=" + url.QueryEscape(query) + "&per_page=" + strconv.Itoa(input.Limit)
	}
	args := []string{"api", "--method", "GET", endpoint}
	if input.Op == "file_read" {
		args = append(args, "--header", "Accept: application/vnd.github.raw+json")
	}
	output, err := nativeRun(ctx, d.root, "", "gh", args...)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	if input.Op == "file_read" {
		return nativeJSONResult(call, map[string]string{"repo": input.Repo, "path": input.Path, "content": output})
	}
	if !json.Valid([]byte(output)) {
		return toolError(call, "GitHub returned invalid or oversized JSON"), nil
	}
	return nativeJSONResult(call, json.RawMessage(output))
}
