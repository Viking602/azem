package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Viking602/venat/tool"
)

func TestGitHubReadOperationsForwardGovernedArguments(t *testing.T) {
	var captured map[string]any
	driver := &githubDriver{root: t.TempDir(), bridge: newLSPBridgeRuntime(), networkPolicy: "allow"}
	driver.execute = func(_ context.Context, _, _, _ string, params map[string]any) (lspBridgeResponse, error) {
		captured = params
		return lspBridgeResponse{OK: true, Content: "repository view", Details: json.RawMessage(`{"repo":"owner/repo"}`)}, nil
	}
	arguments := json.RawMessage(`{"op":"file_read","repo":"owner/repo","path":"docs/readme.md","branch":"main"}`)
	result, err := driver.Execute(context.Background(), tool.Call{ID: "github", Name: ToolGitHub, Arguments: arguments}, nil)
	if err != nil || result.IsError || result.Content != "repository view" {
		t.Fatalf("GitHub read = %#v, %v", result, err)
	}
	if captured["op"] != "file_read" || captured["repo"] != "owner/repo" || captured["path"] != "docs/readme.md" || captured["branch"] != "main" {
		t.Fatalf("forwarded GitHub params = %#v", captured)
	}
}

func TestGitHubDynamicApprovalAndValidation(t *testing.T) {
	driver := newGitHubDriver(t.TempDir(), newLSPBridgeRuntime(), "allow").(*githubDriver)
	readArgs := json.RawMessage(`{"op":"search_issues","repo":"owner/repo","query":"is:open"}`)
	read := driver.DefinitionForCall(tool.Call{Name: ToolGitHub, Arguments: readArgs})
	if read.EffectType != tool.EffectReadOnly || read.RequiresApproval {
		t.Fatalf("read governance = %#v", read)
	}
	writeArgs := json.RawMessage(`{"op":"pr_create","title":"Change","body":"Body"}`)
	write := driver.DefinitionForCall(tool.Call{Name: ToolGitHub, Arguments: writeArgs})
	readOnly := ReadOnlyGitHubDriver(driver)
	blocked := callGitHub(context.Background(), readOnly, map[string]any{"op": "pr_create", "title": "Blocked"})
	if !blocked.IsError || !strings.Contains(blocked.Content, "read-only session") {
		t.Fatalf("read-only GitHub mutation = %#v", blocked)
	}
	if write.EffectType != tool.EffectExternalSideEffect || !write.RequiresApproval || !write.RequiresActionTask {
		t.Fatalf("write governance = %#v", write)
	}
	invalid := callGitHub(context.Background(), driver, map[string]any{"op": "file_read", "repo": "invalid", "path": "../secret"})
	if !invalid.IsError || !strings.Contains(invalid.Content, "owner/repo") {
		t.Fatalf("invalid GitHub request = %#v", invalid)
	}
	denied := newGitHubDriver(t.TempDir(), newLSPBridgeRuntime(), "deny")
	result := callGitHub(context.Background(), denied, map[string]any{"op": "repo_view", "repo": "owner/repo"})
	if !result.IsError || !strings.Contains(result.Content, "network policy") {
		t.Fatalf("GitHub network denial = %#v", result)
	}
}

func TestGitHubLiveRepoView(t *testing.T) {
	if os.Getenv("AZEM_LIVE_GITHUB") != "1" {
		t.Skip("set AZEM_LIVE_GITHUB=1 for authenticated gh verification")
	}
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newGitHubDriver(".", bridge, "allow")
	result := callGitHub(context.Background(), driver, map[string]any{"op": "repo_view"})
	if result.IsError || !strings.Contains(strings.ToLower(result.Content), "github") {
		t.Fatalf("live repository view = %#v", result)
	}
}

func callGitHub(ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	arguments, _ := json.Marshal(input)
	result, err := driver.Execute(ctx, tool.Call{ID: "github", Name: ToolGitHub, Arguments: arguments}, nil)
	if err != nil {
		return tool.Result{ToolCallID: "github", Name: ToolGitHub, Content: err.Error(), IsError: true}
	}
	return result
}
