package agent

import (
	"context"
	"encoding/json"
	"errors"
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

func TestWorkspaceNeverExposesShell(t *testing.T) {
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
			for _, definition := range service.ToolDefinitions() {
				if definition.Name == ToolShell {
					t.Fatalf("shell policy %q exposed %s", policy, ToolShell)
				}
			}
		})
	}
}

func TestShellDriverIsRejectedBeforeApprovalOrExecution(t *testing.T) {
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
	defer service.Close(ctx)
	run, err := service.StartRun(ctx, "create marker")
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(shellInput{Command: "printf approved > marker.txt"})
	call := tool.Call{ID: "shell-approved", Name: ToolShell, Arguments: arguments}
	driver := newShellDriver(root, "allow", "allow")
	result, err := service.ExecuteDriver(ctx, run, driver, call, nil)
	if !errors.Is(err, tool.ErrToolNotFound) || result.Approval != nil || result.Executed {
		t.Fatalf("shell execution result=%+v error=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "marker.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shell created marker before rejection: %v", err)
	}
	if _, err := service.ExecuteTeamDriver(ctx, "team-run", driver, call, nil); !errors.Is(err, tool.ErrToolNotFound) {
		t.Fatalf("team shell error=%v", err)
	}
	var attempts int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='action_attempt'`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("durable shell action attempts=%d", attempts)
	}
}
