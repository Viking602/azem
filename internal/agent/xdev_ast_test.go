package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/resource"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestASTEditStagesThenResolvesWithoutEarlyMutation(t *testing.T) {
	ctx := WithInvocation(context.Background(), Invocation{SessionID: "ast-session", TeamRunID: "ast-run"})
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.ts"), "console.log(one);\nconsole.log(two);\n")
	router := resource.NewRouter(1 << 20)
	service := newASTWriteService(t, ctx, root, router)
	write := findWorkspaceTool(t, service, root, ToolWriteFile)
	payload := astEditPayload([]astEditOp{{Pattern: "console.log($$$ARGS)", Output: "logger.info($$$ARGS)"}}, []string{"main.ts"})
	staged := executeWrite(t, ctx, write, "xd://ast_edit", payload)
	if !strings.Contains(staged.Content, "Staged AST proposal") || !strings.Contains(staged.Content, "2 replacement(s)") {
		t.Fatalf("staged result = %#v", staged)
	}
	assertFileContent(t, filepath.Join(root, "main.ts"), "console.log(one);\nconsole.log(two);\n")
	resolved := executeWrite(t, ctx, write, "xd://resolve", "Replace console logging with the project logger.")
	if !strings.Contains(resolved.Content, "Applied AST proposal") {
		t.Fatalf("resolve result = %#v", resolved)
	}
	assertFileContent(t, filepath.Join(root, "main.ts"), "logger.info(one);\nlogger.info(two);\n")
}

func TestASTEditRejectsStaleProposalAndCanBeDiscarded(t *testing.T) {
	ctx := WithInvocation(context.Background(), Invocation{SessionID: "ast-stale"})
	root := t.TempDir()
	path := filepath.Join(root, "main.ts")
	writeTestFile(t, path, "console.log(one);\n")
	router := resource.NewRouter(1 << 20)
	service := newASTWriteService(t, ctx, root, router)
	write := findWorkspaceTool(t, service, root, ToolWriteFile)
	executeWrite(t, ctx, write, "xd://ast_edit", astEditPayload([]astEditOp{{Pattern: "console.log($$$ARGS)", Output: "logger.info($$$ARGS)"}}, []string{"main.ts"}))
	writeTestFile(t, path, "console.log(changed);\n")
	stale := callWrite(ctx, write, "xd://resolve", "Apply the staged logger rewrite.")
	if !stale.IsError || !strings.Contains(stale.Content, "stale") {
		t.Fatalf("stale resolve = %#v", stale)
	}
	assertFileContent(t, path, "console.log(changed);\n")
	rejected := executeWrite(t, ctx, write, "xd://reject", "Discard the stale proposal.")
	if !strings.Contains(rejected.Content, "Rejected AST proposal") {
		t.Fatalf("reject result = %#v", rejected)
	}
}

func TestASTEditRewritesWritableInternalResource(t *testing.T) {
	ctx := WithInvocation(context.Background(), Invocation{SessionID: "ast-resource"})
	root := t.TempDir()
	router := resource.NewRouter(1 << 20)
	handler := &mutableASTResource{data: []byte("console.log(resource);\n")}
	if err := router.Register("mutable", handler); err != nil {
		t.Fatal(err)
	}
	service := newASTWriteService(t, ctx, root, router)
	write := findWorkspaceTool(t, service, root, ToolWriteFile)
	executeWrite(t, ctx, write, "xd://ast_edit", astEditPayload([]astEditOp{{Pattern: "console.log($$$ARGS)", Output: "logger.info($$$ARGS)"}}, []string{"mutable://sample.ts"}))
	if string(handler.data) != "console.log(resource);\n" {
		t.Fatalf("resource mutated before resolve: %q", handler.data)
	}
	executeWrite(t, ctx, write, "xd://resolve", "Apply the resource logger rewrite.")
	if string(handler.data) != "logger.info(resource);\n" {
		t.Fatalf("resource resolve = %q", handler.data)
	}
}

func TestASTEditRollsBackEarlierResourcesWhenACommitFails(t *testing.T) {
	ctx := WithInvocation(context.Background(), Invocation{SessionID: "ast-rollback"})
	root := t.TempDir()
	router := resource.NewRouter(1 << 20)
	handler := &transactionalASTResource{data: map[string][]byte{
		"one.ts": []byte("console.log(one);\n"),
		"two.ts": []byte("console.log(two);\n"),
	}}
	if err := router.Register("txn", handler); err != nil {
		t.Fatal(err)
	}
	service := newASTWriteService(t, ctx, root, router)
	write := findWorkspaceTool(t, service, root, ToolWriteFile)
	executeWrite(t, ctx, write, "xd://ast_edit", astEditPayload(
		[]astEditOp{{Pattern: "console.log($$$ARGS)", Output: "logger.info($$$ARGS)"}},
		[]string{"txn://one.ts", "txn://two.ts"},
	))
	failed := callWrite(ctx, write, "xd://resolve", "Apply both resource rewrites atomically.")
	if !failed.IsError || !strings.Contains(failed.Content, "injected resource failure") {
		t.Fatalf("failed resolve = %#v", failed)
	}
	if string(handler.data["one.ts"]) != "console.log(one);\n" || string(handler.data["two.ts"]) != "console.log(two);\n" {
		t.Fatalf("resource rollback = %#v", handler.data)
	}
}

func newASTWriteService(t *testing.T, ctx context.Context, root string, router *resource.Router) *Service {
	t.Helper()
	bridge := newASTBridge()
	if err := bridge.resolve(); err != nil {
		t.Skip(err)
	}
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	service, err := NewService(store, root, WithResourceRouter(router))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	return service
}

func astEditPayload(ops []astEditOp, paths []string) string {
	payload, _ := json.Marshal(astEditInput{Ops: ops, Paths: paths})
	return string(payload)
}

type mutableASTResource struct{ data []byte }

func (handler *mutableASTResource) Read(_ context.Context, request resource.Request) (resource.Result, error) {
	return resource.Result{URI: request.URI.Raw, MediaType: "text/plain", Data: append([]byte(nil), handler.data...)}, nil
}

func (handler *mutableASTResource) Write(_ context.Context, request resource.Request, value resource.Result) (resource.Result, error) {
	handler.data = append([]byte(nil), value.Data...)
	return resource.Result{URI: request.URI.Raw, MediaType: value.MediaType, Data: append([]byte(nil), value.Data...)}, nil
}

type transactionalASTResource struct{ data map[string][]byte }

func (handler *transactionalASTResource) Read(_ context.Context, request resource.Request) (resource.Result, error) {
	return resource.Result{URI: request.URI.Raw, MediaType: "text/plain", Data: append([]byte(nil), handler.data[request.URI.Opaque]...)}, nil
}

func (handler *transactionalASTResource) Write(_ context.Context, request resource.Request, value resource.Result) (resource.Result, error) {
	if request.URI.Opaque == "two.ts" && strings.Contains(string(value.Data), "logger.info") {
		return resource.Result{}, errors.New("injected resource failure")
	}
	handler.data[request.URI.Opaque] = append([]byte(nil), value.Data...)
	return resource.Result{URI: request.URI.Raw, MediaType: value.MediaType, Data: append([]byte(nil), value.Data...)}, nil
}
