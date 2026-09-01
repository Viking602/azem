package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

const ToolGitHub = "github"

var (
	githubOps = map[string]bool{
		"repo_view": true, "file_read": true, "pr_create": true, "pr_checkout": true, "pr_push": true,
		"search_issues": true, "search_prs": true, "search_code": true, "search_commits": true,
		"search_repos": true, "run_watch": true,
	}
	githubReadOnlyOps = map[string]bool{
		"repo_view": true, "file_read": true, "search_issues": true, "search_prs": true,
		"search_code": true, "search_commits": true, "search_repos": true, "run_watch": true,
	}
	githubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

type githubDriver struct {
	root          string
	readOnly      bool
	bridge        *lspBridgeRuntime
	networkPolicy string
	execute       func(context.Context, string, string, string, map[string]any) (lspBridgeResponse, error)
}

type githubToolResult struct {
	Operation string          `json:"operation"`
	Content   string          `json:"content"`
	Details   json.RawMessage `json:"details,omitempty"`
}

func newGitHubDriver(root string, bridge *lspBridgeRuntime, networkPolicy string) tool.Driver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	driver := &githubDriver{root: root, bridge: bridge, networkPolicy: networkPolicy}
	driver.execute = func(ctx context.Context, cwd, sessionID, artifactsDir string, params map[string]any) (lspBridgeResponse, error) {
		return bridge.requestGitHub(ctx, cwd, sessionID, artifactsDir, params)
	}
	return driver
}

func ReadOnlyGitHubDriver(driver tool.Driver) tool.Driver {
	if current, ok := driver.(*githubDriver); ok {
		copy := *current
		copy.readOnly = true
		return &copy
	}
	return driver
}

func (driver *githubDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolGitHub,
		Description: "Governed gh wrapper for repository/file reads, GitHub search, PR creation, isolated PR checkout worktrees, PR push, and Actions run watching. repo defaults to the current checkout. Use file_read for GitHub-hosted files and issue:// or pr:// resources for issue/PR reads.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"op"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"op": {Type: "string"}, "repo": {Type: "string"}, "branch": {Type: "string"}, "path": {Type: "string"},
				"pr": {}, "force": {Type: "boolean"}, "forceWithLease": {Type: "boolean"}, "title": {Type: "string"},
				"body": {Type: "string"}, "base": {Type: "string"}, "head": {Type: "string"}, "draft": {Type: "boolean"},
				"fill": {Type: "boolean"}, "reviewer": {Type: "array", Items: &tool.Schema{Type: "string"}},
				"assignee": {Type: "array", Items: &tool.Schema{Type: "string"}}, "label": {Type: "array", Items: &tool.Schema{Type: "string"}},
				"query": {Type: "string"}, "since": {Type: "string"}, "until": {Type: "string"}, "dateField": {Type: "string"},
				"limit": {Type: "integer"}, "run": {Type: "string"}, "tail": {Type: "integer"},
			},
		},
		Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver *githubDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	policy := agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectReadOnly, RiskLevel: "low", PolicyTags: []string{"github", "network"},
		Concurrency: tool.ConcurrencyParallel,
	}
	var input struct {
		Operation string `json:"op"`
	}
	if json.Unmarshal(call.Arguments, &input) != nil || !githubReadOnlyOps[strings.ToLower(strings.TrimSpace(input.Operation))] {
		policy.Effect = agentruntime.ToolEffectExternalSideEffect
		policy.RequiresApproval = true
		policy.RequiresActionTask = true
		policy.RiskLevel = "high"
		policy.Concurrency = tool.ConcurrencyExclusive
		policy.ConcurrencyGroup = "github-mutation"
	}
	return policy
}

func (driver *githubDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var params map[string]any
	if err := json.Unmarshal(call.Arguments, &params); err != nil {
		return githubError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	op, _ := params["op"].(string)
	op = strings.ToLower(strings.TrimSpace(op))
	if !githubOps[op] {
		return githubError(call, fmt.Errorf("unsupported op %q", op)), nil
	}
	params["op"] = op
	if driver.readOnly && !githubReadOnlyOps[op] {
		return githubError(call, fmt.Errorf("op %s is disabled in this read-only session", op)), nil
	}
	if driver.networkPolicy == "deny" {
		return githubError(call, errors.New("GitHub access is denied by workspace network policy")), nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return githubError(call, errors.New("GitHub CLI (gh) is not installed")), nil
	}
	if err := validateGitHubParams(op, params); err != nil {
		return githubError(call, err), nil
	}
	caller, _ := InvocationFromContext(ctx)
	sessionID := caller.SessionID
	if sessionID == "" {
		sessionID = caller.TeamRunID
	}
	if sessionID == "" {
		sessionID = driver.root
	}
	artifactsDir, err := evalArtifactsDir(driver.root, sessionID+"\x00github")
	if err != nil {
		return githubError(call, err), nil
	}
	if !githubReadOnlyOps[op] {
		driver.bridge.githubMu.Lock()
		defer driver.bridge.githubMu.Unlock()
	}
	response, err := driver.execute(ctx, driver.root, sessionID, artifactsDir, params)
	if err != nil {
		return githubError(call, err), nil
	}
	result := githubToolResult{Operation: op, Content: response.Content, Details: response.Details}
	structured, _ := json.Marshal(result)
	toolResult := tool.Result{ToolCallID: call.ID, Name: call.Name, Content: response.Content, Structured: structured, IsError: response.IsError}
	toolResult.Parts = decodeGitHubBridgeParts(response.Parts)
	return toolResult, nil
}

func validateGitHubParams(op string, params map[string]any) error {
	if repo, ok := params["repo"].(string); ok && repo != "" && !githubRepoPattern.MatchString(repo) {
		return errors.New("repo must be owner/repo")
	}
	for _, key := range []string{"branch", "base", "head", "run", "since", "until", "dateField"} {
		if value, ok := params[key].(string); ok && (len(value) > 512 || strings.ContainsAny(value, "\r\n\x00")) {
			return fmt.Errorf("%s is invalid or too long", key)
		}
	}
	if path, ok := params["path"].(string); ok && path != "" {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		if filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, "../") {
			return errors.New("path must be repository-relative")
		}
		params["path"] = clean
	}
	if title, ok := params["title"].(string); ok && len(title) > 1000 {
		return errors.New("title exceeds 1000 characters")
	}
	if body, ok := params["body"].(string); ok && len(body) > 1<<20 {
		return errors.New("body exceeds 1 MiB")
	}
	for _, key := range []string{"reviewer", "assignee", "label"} {
		if values, ok := params[key].([]any); ok {
			if len(values) > 100 {
				return fmt.Errorf("%s has more than 100 entries", key)
			}
			for _, value := range values {
				text, ok := value.(string)
				if !ok || len(text) > 256 || strings.ContainsAny(text, "\r\n\x00") {
					return fmt.Errorf("%s contains an invalid entry", key)
				}
			}
		}
	}
	if pr, ok := params["pr"].([]any); ok {
		if len(pr) == 0 || len(pr) > 32 {
			return errors.New("pr array must contain 1-32 entries")
		}
		for _, value := range pr {
			if text, ok := value.(string); !ok || len(text) > 512 || strings.ContainsAny(text, "\r\n\x00") {
				return errors.New("pr contains an invalid entry")
			}
		}
	}
	if op == "file_read" {
		if path, _ := params["path"].(string); strings.TrimSpace(path) == "" {
			return errors.New("path is required for file_read")
		}
	}
	if op == "search_code" {
		if query, _ := params["query"].(string); strings.TrimSpace(query) == "" {
			return errors.New("query is required for search_code")
		}
		if params["since"] != nil || params["until"] != nil {
			return errors.New("search_code does not accept since or until")
		}
	}
	for _, key := range []string{"limit", "tail"} {
		if value, ok := params[key].(float64); ok {
			if value < 0 || value != float64(int(value)) || key == "limit" && value > 100 || key == "tail" && value > 10000 {
				return fmt.Errorf("%s is outside the supported range", key)
			}
		}
	}
	return nil
}

func decodeGitHubBridgeParts(payload json.RawMessage) []message.ContentPart {
	if len(payload) == 0 {
		return nil
	}
	var parts []evalBridgePart
	if json.Unmarshal(payload, &parts) != nil {
		return nil
	}
	result := make([]message.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type != "image" || part.Data == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			continue
		}
		result = append(result, message.ContentPart{Kind: message.ContentImage, Data: data, MediaType: part.MimeType})
	}
	return result
}

func githubError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "github failed: " + err.Error(), IsError: true}
}
