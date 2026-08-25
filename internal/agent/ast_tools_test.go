package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/venat/tool"
)

func TestASTGrepUsesStructuralIdentityAndReturnsHashlineAnchors(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "same.ts"), "if (left == left) { keep(); }\nif (left == right) { skip(); }\n")
	service := newWriteTestService(t, ctx, root)
	driver := findWorkspaceTool(t, service, root, ToolASTGrep)
	result := executeASTGrep(t, ctx, driver, map[string]any{"pat": "$A == $A", "path": "same.ts"})
	if !strings.Contains(result.Content, "[same.ts#") || !strings.Contains(result.Content, "left == left") || strings.Contains(result.Content, "left == right") {
		t.Fatalf("AST grep result = %#v", result)
	}
	var structured astGrepResult
	if err := json.Unmarshal(result.Structured, &structured); err != nil || len(structured.Matches) != 1 || structured.Matches[0].StartLine != 1 {
		t.Fatalf("AST grep structured = %#v, %v", structured, err)
	}
}

func TestASTGrepSupportsGlobsSemicolonTargetsAndInternalResources(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "src", "one.py"), "print(one)\n")
	writeTestFile(t, filepath.Join(root, "tests", "two.py"), "print(two)\n")
	router := resource.NewRouter(1 << 20)
	handler := &staticASTResource{data: []byte("print(resource)\n")}
	if err := router.Register("fixture", handler); err != nil {
		t.Fatal(err)
	}
	bridge := newASTBridge()
	if err := bridge.resolve(); err != nil {
		t.Skip(err)
	}
	driver := newASTGrepDriver(root, bridge, nil, router)
	result := executeASTGrep(t, ctx, driver, map[string]any{"pat": "print($$$ARGS)", "path": "src/**/*.py;tests/**/*.py"})
	if !strings.Contains(result.Content, "src/one.py") || !strings.Contains(result.Content, "tests/two.py") {
		t.Fatalf("multi-target AST grep = %s", result.Content)
	}
	internal := executeASTGrep(t, ctx, driver, map[string]any{"pat": "print($$$ARGS)", "path": "fixture://sample.py"})
	if !strings.Contains(internal.Content, "[fixture://sample.py#") || !strings.Contains(internal.Content, "print(resource)") {
		t.Fatalf("internal AST grep = %s", internal.Content)
	}
}

func TestASTGrepSurfacesMalformedPatterns(t *testing.T) {
	bridge := newASTBridge()
	if err := bridge.resolve(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "bad.ts"), "const value = 1\n")
	driver := newASTGrepDriver(root, bridge, nil, nil)
	arguments, _ := json.Marshal(map[string]any{"pat": "foo($$ARGS)", "path": "bad.ts"})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "bad", Name: ToolASTGrep, Arguments: arguments}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "use $$$NAME") {
		t.Fatalf("malformed pattern = %#v, %v", result, err)
	}
}

func executeASTGrep(t *testing.T, ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	t.Helper()
	if concrete, ok := driver.(*astGrepDriver); ok {
		if err := concrete.bridge.resolve(); err != nil {
			t.Skip(err)
		}
	}
	arguments, _ := json.Marshal(input)
	result, err := driver.Execute(ctx, tool.Call{ID: "ast", Name: ToolASTGrep, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		t.Fatalf("AST grep failed: %#v, %v", result, err)
	}
	return result
}

type staticASTResource struct{ data []byte }

func (handler *staticASTResource) Read(context.Context, resource.Request) (resource.Result, error) {
	return resource.Result{MediaType: "text/plain", Data: append([]byte(nil), handler.data...)}, nil
}
