package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

func TestDebugLaunchBreakpointContinueInspectAndTerminate(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv is unavailable")
	}
	ctx := context.Background()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/debugfixture\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n\nimport \"fmt\"\n\nfunc add(a, b int) int { return a + b }\n\nfunc main() {\n\tvalue := add(2, 3)\n\tfmt.Println(value)\n}\n")
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newDebugDriver(root, bridge, false)

	launch := executeDebug(t, ctx, driver, map[string]any{"action": "launch", "adapter": "dlv", "program": ".", "timeout": 60})
	if !strings.Contains(strings.ToLower(launch.Content), "session") && !strings.Contains(strings.ToLower(launch.Content), "debug") {
		t.Fatalf("launch = %s", launch.Content)
	}
	executeDebug(t, ctx, driver, map[string]any{"action": "set_breakpoint", "file": "main.go", "line": 9, "timeout": 30})
	continued := executeDebug(t, ctx, driver, map[string]any{"action": "continue", "timeout": 60})
	if !strings.Contains(strings.ToLower(continued.Content), "stop") {
		t.Fatalf("continue = %s", continued.Content)
	}
	stack := executeDebug(t, ctx, driver, map[string]any{"action": "stack_trace", "levels": 8, "timeout": 30})
	if !strings.Contains(stack.Content, "main.go") {
		t.Fatalf("stack trace = %s", stack.Content)
	}
	threads := executeDebug(t, ctx, driver, map[string]any{"action": "threads", "timeout": 30})
	if strings.TrimSpace(threads.Content) == "" {
		t.Fatal("threads output is empty")
	}
	evaluation := executeDebug(t, ctx, driver, map[string]any{"action": "evaluate", "expression": "value", "context": "repl", "timeout": 30})
	if !strings.Contains(evaluation.Content, "5") {
		t.Fatalf("evaluation = %s", evaluation.Content)
	}
	sessions := executeDebug(t, ctx, driver, map[string]any{"action": "sessions", "timeout": 30})
	if !strings.Contains(strings.ToLower(sessions.Content), "debug-") {
		t.Fatalf("sessions = %s", sessions.Content)
	}
	executeDebug(t, ctx, driver, map[string]any{"action": "terminate", "timeout": 30})
}

func TestDebugDynamicGovernanceAndReadOnlyRestriction(t *testing.T) {
	root := t.TempDir()
	driver := newDebugDriver(root, newLSPBridgeRuntime(), false).(*debugDriver)
	readArguments, _ := json.Marshal(map[string]any{"action": "threads"})
	read := driver.PolicyForCall(tool.Call{Name: ToolDebug, Arguments: readArguments})
	if read.Effect != agentruntime.ToolEffectReadOnly || read.RequiresApproval {
		t.Fatalf("threads governance = %#v", read)
	}
	launchArguments, _ := json.Marshal(map[string]any{"action": "launch", "program": "."})
	launch := driver.PolicyForCall(tool.Call{Name: ToolDebug, Arguments: launchArguments})
	if launch.Effect != agentruntime.ToolEffectExternalSideEffect || !launch.RequiresApproval || !launch.RequiresActionTask {
		t.Fatalf("launch governance = %#v", launch)
	}
	readOnly := ReadOnlyDebugDriver(driver)
	blocked, err := readOnly.Execute(context.Background(), tool.Call{Name: ToolDebug, Arguments: launchArguments}, nil)
	if err != nil || !blocked.IsError || !strings.Contains(blocked.Content, "read-only session") {
		t.Fatalf("read-only launch = %#v, %v", blocked, err)
	}
}

func TestDebugRejectsProgramOutsideWorkspaceBeforeLaunching(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	writeTestFile(t, outside, "binary")
	driver := newDebugDriver(root, newLSPBridgeRuntime(), false)
	arguments, _ := json.Marshal(map[string]any{"action": "launch", "program": outside})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "escape", Name: ToolDebug, Arguments: arguments}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "escapes workspace") {
		t.Fatalf("escape result = %#v, %v", result, err)
	}
}

func executeDebug(t *testing.T, ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	t.Helper()
	if concrete, ok := driver.(*debugDriver); ok {
		if err := concrete.bridge.resolveAssets(); err != nil {
			t.Skip(err)
		}
	}
	arguments, _ := json.Marshal(input)
	result, err := driver.Execute(ctx, tool.Call{ID: "debug", Name: ToolDebug, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		t.Fatalf("debug call failed: %#v, %v", result, err)
	}
	return result
}
