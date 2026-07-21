package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/go-hydaelyn/coding"
	"github.com/Viking602/go-hydaelyn/tool"
)

func TestShellUsesWorkspaceAndReturnsStructuredExit(t *testing.T) {
	root := t.TempDir()
	driver := newShellDriver(root, "prompt", "prompt")
	arguments, _ := json.Marshal(shellInput{Command: "pwd; printf shell-ok"})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "shell-1", Name: ToolShell, Arguments: arguments}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Content, root) || !strings.Contains(result.Content, "shell-ok") {
		t.Fatalf("shell result=%+v", result)
	}
	var output shellOutput
	if err := json.Unmarshal(result.Structured, &output); err != nil {
		t.Fatal(err)
	}
	if output.ExitCode != 0 || output.Truncated {
		t.Fatalf("structured shell output=%+v", output)
	}
}

func TestWorkspacePolicyFiltersWritesAndShell(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir(), WithWorkspacePolicy(false, "deny", "deny"))
	if err != nil {
		t.Fatal(err)
	}
	definitions := map[string]bool{}
	for _, definition := range service.ToolDefinitions() {
		definitions[definition.Name] = true
	}
	for _, forbidden := range []string{coding.ToolEditHashline, coding.ToolWriteFile, coding.ToolGofmt, ToolShell} {
		if definitions[forbidden] {
			t.Fatalf("workspace policy exposed %q", forbidden)
		}
	}
	if !definitions[coding.ToolReadFile] {
		t.Fatal("workspace policy removed read_file")
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceExposesShellAccordingToPolicy(t *testing.T) {
	for _, policy := range []string{"allow", "prompt", "deny"} {
		t.Run(policy, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlitestore.Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewService(store, t.TempDir(), WithWorkspacePolicy(true, policy, "prompt"))
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close(ctx)
			exposed := false
			for _, definition := range service.ToolDefinitions() {
				if definition.Name == ToolShell {
					exposed = true
				}
			}
			if exposed != (policy != "deny") {
				t.Fatalf("shell policy %q exposed=%v", policy, exposed)
			}
		})
	}
}

func TestShellRequiresApprovalAndPersistsActionAttempt(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	service, err := NewService(store, root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartRun(ctx, "create marker")
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(shellInput{Command: "printf approved > marker.txt"})
	call := tool.Call{ID: "shell-approved", Name: ToolShell, Arguments: arguments}
	first, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Approval == nil || first.Executed {
		t.Fatalf("first shell execution=%+v", first)
	}
	if err := service.ResolveApproval(ctx, run, call.ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	second, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Executed || second.Result.IsError {
		t.Fatalf("approved shell execution=%+v", second)
	}
	contents, err := os.ReadFile(filepath.Join(root, "marker.txt"))
	if err != nil || string(contents) != "approved" {
		t.Fatalf("marker=%q error=%v", contents, err)
	}
	if err := service.CompleteRun(ctx, run, "done", nil); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='action_attempt'`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("durable shell action attempts=%d", attempts)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
