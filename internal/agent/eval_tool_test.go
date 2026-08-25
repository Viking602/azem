package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/venat/tool"
)

func TestEvalJavaScriptPersistsStateSupportsHelpersAndReset(t *testing.T) {
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "eval-js"})
	root := t.TempDir()
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newEvalDriver(root, bridge)
	executeEval(t, ctx, driver, "js", `var value = 40; await write("eval-js.txt", "hello"); print("set")`, false)
	second := executeEval(t, ctx, driver, "js", `print(value + 2); print(await read("eval-js.txt"))`, false)
	if !strings.Contains(second.Content, "42") || !strings.Contains(second.Content, "hello") {
		t.Fatalf("persistent JS output = %s", second.Content)
	}
	assertFileContent(t, filepath.Join(root, "eval-js.txt"), "hello")
	helpers := executeEval(t, ctx, driver, "js", `const values = await parallel([async () => 1, async () => 2]); display(values); await env("AZEM_EVAL_TEST", "ok"); print(await env("AZEM_EVAL_TEST"))`, false)
	if !strings.Contains(helpers.Content, "1") || !strings.Contains(helpers.Content, "2") || !strings.Contains(helpers.Content, "ok") {
		t.Fatalf("JS helpers = %s", helpers.Content)
	}
	reset := executeEval(t, ctx, driver, "js", `print(typeof value)`, true)
	if !strings.Contains(reset.Content, "undefined") {
		t.Fatalf("reset JS output = %s", reset.Content)
	}
}

func TestEvalPythonPersistsStateSupportsAwaitAndLanguageIsolation(t *testing.T) {
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "eval-py"})
	root := t.TempDir()
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newEvalDriver(root, bridge)

	first := executeEvalMaybeUnavailable(t, ctx, driver, "py", "counter = 40\nprint('set')", false)
	if first.IsError {
		t.Skip(first.Content)
	}
	second := executeEval(t, ctx, driver, "py", "import asyncio\nawait asyncio.sleep(0)\nprint(counter + 2)\nwrite('eval-py.txt', 'python')", false)
	if !strings.Contains(second.Content, "42") {
		t.Fatalf("persistent Python output = %s", second.Content)
	}
	assertFileContent(t, filepath.Join(root, "eval-py.txt"), "python")
	helpers := executeEval(t, ctx, driver, "py", "display(parallel([lambda: 1, lambda: 2]))\\nenv('AZEM_EVAL_PY', 'ok')\\nprint(env('AZEM_EVAL_PY'))", false)
	if !strings.Contains(helpers.Content, "1") || !strings.Contains(helpers.Content, "2") || !strings.Contains(helpers.Content, "ok") {
		t.Fatalf("Python helpers = %s", helpers.Content)
	}
	executeEval(t, ctx, driver, "js", "var jsOnly = 1; print(jsOnly)", false)
	resetJS := executeEval(t, ctx, driver, "js", "print(typeof jsOnly)", true)
	if !strings.Contains(resetJS.Content, "undefined") {
		t.Fatalf("JS reset = %s", resetJS.Content)
	}
	pythonStillLive := executeEval(t, ctx, driver, "py", "print(counter)", false)
	if !strings.Contains(pythonStillLive.Content, "40") {
		t.Fatalf("Python kernel was reset with JS: %s", pythonStillLive.Content)
	}
}

func TestEvalRubyPersistsStateWhenRuntimeIsAvailable(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby is unavailable")
	}
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "eval-rb"})
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newEvalDriver(t.TempDir(), bridge)
	first := executeEvalMaybeUnavailable(t, ctx, driver, "rb", "$azem_counter = 40\nputs 'set'", false)
	if first.IsError {
		t.Skip(first.Content)
	}
	second := executeEval(t, ctx, driver, "rb", "puts $azem_counter + 2", false)
	if !strings.Contains(second.Content, "42") {
		t.Fatalf("persistent Ruby output = %s", second.Content)
	}
}

func TestEvalSessionsAreIsolatedAndErrorsDoNotEraseState(t *testing.T) {
	root := t.TempDir()
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newEvalDriver(root, bridge)
	firstCtx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "eval-a"})
	secondCtx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "eval-b"})
	executeEval(t, firstCtx, driver, "js", "var retained = 9; print(retained)", false)
	failed := executeEvalMaybeUnavailable(t, firstCtx, driver, "js", "throw new Error('expected failure')", false)
	if !failed.IsError || !strings.Contains(failed.Content, "expected failure") {
		t.Fatalf("failed cell = %#v", failed)
	}
	retained := executeEval(t, firstCtx, driver, "js", "print(retained)", false)
	if !strings.Contains(retained.Content, "9") {
		t.Fatalf("state lost after failure = %s", retained.Content)
	}
	isolated := executeEval(t, secondCtx, driver, "js", "print(typeof retained)", false)
	if !strings.Contains(isolated.Content, "undefined") {
		t.Fatalf("eval sessions shared state = %s", isolated.Content)
	}
}

func executeEval(t *testing.T, ctx context.Context, driver tool.Driver, language, code string, reset bool) tool.Result {
	t.Helper()
	result := executeEvalMaybeUnavailable(t, ctx, driver, language, code, reset)
	if result.IsError {
		t.Fatalf("eval failed: %s", result.Content)
	}
	return result
}

func executeEvalMaybeUnavailable(t *testing.T, ctx context.Context, driver tool.Driver, language, code string, reset bool) tool.Result {
	t.Helper()
	if concrete, ok := driver.(*evalDriver); ok {
		if err := concrete.bridge.resolveAssets(); err != nil {
			t.Skip(err)
		}
	}
	arguments, _ := json.Marshal(evalInput{Language: language, Code: code, Reset: reset})
	result, err := driver.Execute(ctx, tool.Call{ID: "eval", Name: ToolEval, Arguments: arguments}, nil)
	if err != nil {
		return tool.Result{ToolCallID: "eval", Name: ToolEval, Content: err.Error(), IsError: true}
	}
	return result
}
