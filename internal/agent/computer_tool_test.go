package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Viking602/venat/tool"
)

func TestComputerReadOnlySessionPersistsAcrossCalls(t *testing.T) {
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "computer-session"})
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newComputerDriver(t.TempDir(), bridge)
	first := executeComputer(t, ctx, driver, map[string]any{
		"read_only": true,
		"timeout":   30,
		"code":      "const caps = await desktop.capabilities(); var displayCount = (await desktop.displays()).length; display(caps); print(displayCount);",
	})
	if strings.TrimSpace(first.Content) == "" {
		t.Fatal("computer capabilities output is empty")
	}
	second := executeComputer(t, ctx, driver, map[string]any{"read_only": true, "timeout": 30, "code": "print(displayCount)"})
	if strings.TrimSpace(second.Content) == "" {
		t.Fatal("persistent computer state output is empty")
	}
}

func TestComputerReadOnlyBlocksDesktopMutation(t *testing.T) {
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "computer-read-only"})
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newComputerDriver(t.TempDir(), bridge)
	result := callComputer(ctx, driver, map[string]any{"read_only": true, "timeout": 10, "code": "await desktop.click(0, 0)"})
	if !result.IsError || !strings.Contains(strings.ToLower(result.Content), "read-only") {
		t.Fatalf("read-only mutation = %#v", result)
	}
}

func TestComputerUsesDynamicApprovalTier(t *testing.T) {
	driver := newComputerDriver(t.TempDir(), newLSPBridgeRuntime()).(*computerDriver)
	readArguments, _ := json.Marshal(map[string]any{"read_only": true, "code": "return await desktop.displays()"})
	read := driver.DefinitionForCall(tool.Call{Name: ToolComputer, Arguments: readArguments})
	if read.EffectType != tool.EffectReadOnly || read.RequiresApproval || read.RequiresActionTask {
		t.Fatalf("read-only governance = %#v", read)
	}
	writeArguments, _ := json.Marshal(map[string]any{"code": "await desktop.click(1, 1)"})
	write := driver.DefinitionForCall(tool.Call{Name: ToolComputer, Arguments: writeArguments})
	if write.EffectType != tool.EffectExternalSideEffect || !write.RequiresApproval || !write.RequiresActionTask {
		t.Fatalf("mutating governance = %#v", write)
	}
}

func executeComputer(t *testing.T, ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	t.Helper()
	if concrete, ok := driver.(*computerDriver); ok {
		if err := concrete.bridge.resolveAssets(); err != nil {
			t.Skip(err)
		}
	}
	result := callComputer(ctx, driver, input)
	if result.IsError {
		lower := strings.ToLower(result.Content)
		if strings.Contains(lower, "unsupported") || strings.Contains(lower, "permission") || strings.Contains(lower, "no active displays") {
			t.Skip(result.Content)
		}
		t.Fatalf("computer call failed: %s", result.Content)
	}
	return result
}

func callComputer(ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	arguments, _ := json.Marshal(input)
	result, err := driver.Execute(ctx, tool.Call{ID: "computer", Name: ToolComputer, Arguments: arguments}, nil)
	if err != nil {
		return tool.Result{ToolCallID: "computer", Name: ToolComputer, Content: err.Error(), IsError: true}
	}
	return result
}
