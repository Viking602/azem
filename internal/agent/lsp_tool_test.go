package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

func TestLSPDefinitionReferencesAndRenamePreviewApply(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is unavailable")
	}
	ctx := context.Background()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/lspfixture\n\ngo 1.25\n")
	const source = "package lspfixture\n\nfunc Target() int { return 1 }\n\nfunc Use() int { return Target() }\n"
	writeTestFile(t, filepath.Join(root, "main.go"), source)
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newLSPDriver(root, bridge, false)

	status := executeLSP(t, ctx, driver, map[string]any{"action": "status", "timeout": 20})
	if !strings.Contains(status.Content, "gopls") {
		t.Fatalf("LSP status = %s", status.Content)
	}
	definition := executeLSP(t, ctx, driver, map[string]any{"action": "definition", "file": "main.go", "line": 5, "symbol": "Target", "timeout": 60})
	if !strings.Contains(definition.Content, "main.go") || !strings.Contains(definition.Content, "Target") {
		t.Fatalf("definition = %s", definition.Content)
	}
	references := executeLSP(t, ctx, driver, map[string]any{"action": "references", "file": "main.go", "line": 3, "symbol": "Target", "timeout": 60})
	if !strings.Contains(references.Content, "reference") || !strings.Contains(references.Content, "main.go") {
		t.Fatalf("references = %s", references.Content)
	}
	preview := executeLSP(t, ctx, driver, map[string]any{"action": "rename", "file": "main.go", "line": 3, "symbol": "Target", "new_name": "Renamed", "apply": false, "timeout": 60})
	if !strings.Contains(preview.Content, "Rename preview") {
		t.Fatalf("rename preview = %s", preview.Content)
	}
	assertFileContent(t, filepath.Join(root, "main.go"), source)
	applied := executeLSP(t, ctx, driver, map[string]any{"action": "rename", "file": "main.go", "line": 3, "symbol": "Target", "new_name": "Renamed", "timeout": 60})
	if !strings.Contains(applied.Content, "Applied rename") {
		t.Fatalf("rename apply = %s", applied.Content)
	}
	assertFileContent(t, filepath.Join(root, "main.go"), strings.ReplaceAll(source, "Target", "Renamed"))
}

func TestLSPDiagnosticsSymbolsCapabilitiesRequestReloadAndRenameFile(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is unavailable")
	}
	ctx := context.Background()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/lspmatrix\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(root, "main.go"), "package lspmatrix\n\nfunc Value() int { return 1 }\n")
	writeTestFile(t, filepath.Join(root, "helper.go"), "package lspmatrix\n\nfunc Helper() int { return Value() }\n")
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newLSPDriver(root, bridge, false)

	for _, call := range []map[string]any{
		{"action": "diagnostics", "file": "main.go", "timeout": 60},
		{"action": "hover", "file": "main.go", "line": 3, "symbol": "Value", "timeout": 60},
		{"action": "symbols", "file": "main.go", "timeout": 60},
		{"action": "capabilities", "file": "main.go", "timeout": 60},
		{"action": "request", "file": "main.go", "query": "textDocument/documentSymbol", "timeout": 60},
		{"action": "code_actions", "file": "main.go", "line": 3, "symbol": "Value", "apply": false, "timeout": 60},
	} {
		executeLSP(t, ctx, driver, call)
	}
	reload := executeLSP(t, ctx, driver, map[string]any{"action": "reload", "file": "main.go", "timeout": 60})
	if !strings.Contains(strings.ToLower(reload.Content), "reload") {
		t.Fatalf("reload = %s", reload.Content)
	}
	preview := executeLSP(t, ctx, driver, map[string]any{"action": "rename_file", "file": "helper.go", "new_name": "renamed.go", "apply": false, "timeout": 60})
	if !strings.Contains(strings.ToLower(preview.Content), "preview") {
		t.Fatalf("rename_file preview = %s", preview.Content)
	}
	executeLSP(t, ctx, driver, map[string]any{"action": "rename_file", "file": "helper.go", "new_name": "renamed.go", "apply": true, "timeout": 60})
	if _, err := os.Stat(filepath.Join(root, "renamed.go")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "helper.go")); !os.IsNotExist(err) {
		t.Fatalf("rename source remains: %v", err)
	}
}

func TestLSPDefinitionForCallUsesDynamicApprovalTier(t *testing.T) {
	driver := newLSPDriver(t.TempDir(), newLSPBridgeRuntime(), false).(*lspDriver)
	readArgs, _ := json.Marshal(map[string]any{"action": "definition", "file": "main.go", "line": 1, "symbol": "main"})
	read := driver.PolicyForCall(tool.Call{Name: ToolLSP, Arguments: readArgs})
	if read.Effect != agentruntime.ToolEffectReadOnly || read.RequiresActionTask {
		t.Fatalf("definition governance = %#v", read)
	}
	writeArgs, _ := json.Marshal(map[string]any{"action": "rename", "file": "main.go", "line": 1, "symbol": "main", "new_name": "renamed"})
	write := driver.PolicyForCall(tool.Call{Name: ToolLSP, Arguments: writeArgs})
	if write.Effect != agentruntime.ToolEffectWrite || !write.RequiresActionTask {
		t.Fatalf("rename governance = %#v", write)
	}
	readOnly := ReadOnlyLSPDriver(driver)
	blocked, err := readOnly.Execute(context.Background(), tool.Call{Name: ToolLSP, Arguments: writeArgs}, nil)
	if err != nil || !blocked.IsError || !strings.Contains(blocked.Content, "read-only session") {
		t.Fatalf("read-only rename = %#v, %v", blocked, err)
	}
}

func TestLSPRejectsWorkspaceEscapesBeforeStartingServer(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	writeTestFile(t, outside, "package outside\n")
	driver := newLSPDriver(root, newLSPBridgeRuntime(), false)
	arguments, _ := json.Marshal(map[string]any{"action": "definition", "file": outside, "line": 1, "symbol": "outside"})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "escape", Name: ToolLSP, Arguments: arguments}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "escapes workspace") {
		t.Fatalf("escape result = %#v, %v", result, err)
	}
}

func TestLSPDynamicGovernanceIntegratesWithService(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n")
	service := newWriteTestService(t, ctx, root)
	driver := findWorkspaceTool(t, service, root, ToolLSP)
	run, err := service.StartRun(ctx, "govern lsp")
	if err != nil {
		t.Fatal(err)
	}
	readArguments, _ := json.Marshal(map[string]any{"action": "definition", "file": "main.go", "line": 1, "symbol": "main"})
	if result, ready, err := service.PrepareDriver(ctx, run, driver, tool.Call{ID: "read", Name: ToolLSP, Arguments: readArguments}); err != nil || !ready || result.Approval != nil {
		t.Fatalf("read governance = %#v ready=%v err=%v", result, ready, err)
	}
	writeArguments, _ := json.Marshal(map[string]any{"action": "rename", "file": "main.go", "line": 1, "symbol": "main", "new_name": "renamed"})
	if result, ready, err := service.PrepareDriver(ctx, run, driver, tool.Call{ID: "write", Name: ToolLSP, Arguments: writeArguments}); err != nil || ready || result.Approval == nil {
		t.Fatalf("write governance = %#v ready=%v err=%v", result, ready, err)
	}
}

func executeLSP(t *testing.T, ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	t.Helper()
	if concrete, ok := driver.(*lspDriver); ok {
		if err := concrete.bridge.resolveAssets(); err != nil {
			t.Skip(err)
		}
	}
	arguments, _ := json.Marshal(input)
	result, err := driver.Execute(ctx, tool.Call{ID: "lsp", Name: ToolLSP, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		t.Fatalf("LSP call failed: %#v, %v", result, err)
	}
	return result
}
