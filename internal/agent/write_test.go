package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/resource"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/tool"
)

func TestOMPWriteDriverCreatesOverwritesStripsHashlinesAndMarksScriptsExecutable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := newWriteTestService(t, ctx, root)
	write := findWorkspaceTool(t, service, root, ToolWriteFile)

	created := executeWrite(t, ctx, write, "script.sh", "#!/bin/sh\necho one\n")
	if !strings.Contains(created.Content, "Successfully wrote") || !strings.Contains(created.Content, "[script.sh#") {
		t.Fatalf("create result = %#v", created)
	}
	info, err := os.Stat(filepath.Join(root, "script.sh"))
	if err != nil || info.Mode().Perm()&0o111 != 0o111 {
		t.Fatalf("script mode = %v, %v", info, err)
	}

	overwritten := executeWrite(t, ctx, write, "script.sh", "#!/bin/sh\necho two\n")
	if !strings.Contains(overwritten.Content, "Successfully wrote") {
		t.Fatalf("overwrite result = %#v", overwritten)
	}
	assertFileContent(t, filepath.Join(root, "script.sh"), "#!/bin/sh\necho two\n")

	executeWrite(t, ctx, write, "copied.txt", "¶source.txt#ABCD\n1:alpha\n2:beta\n3:    indented\n")
	assertFileContent(t, filepath.Join(root, "copied.txt"), "alpha\nbeta\n    indented\n")

	misfire := callWrite(ctx, write, "missing.txt:1-2", "")
	if !misfire.IsError || !strings.Contains(misfire.Content, "read selector") {
		t.Fatalf("selector misfire = %#v", misfire)
	}
	if _, err := os.Stat(filepath.Join(root, "missing.txt:1-2")); !os.IsNotExist(err) {
		t.Fatalf("selector-shaped file was created: %v", err)
	}
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "secret.txt"), "safe")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	escaped := callWrite(ctx, write, "escape/secret.txt", "unsafe")
	if !escaped.IsError {
		t.Fatalf("symlink escape write = %#v", escaped)
	}
	assertFileContent(t, filepath.Join(outside, "secret.txt"), "safe")
}

func TestOMPWriteDriverUpdatesArchivesAndSQLiteRows(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := newWriteTestService(t, ctx, root)
	write := findWorkspaceTool(t, service, root, ToolWriteFile)
	read := findWorkspaceTool(t, service, root, ToolReadFile)

	writeZipFixture(t, filepath.Join(root, "bundle.zip"), "keep.txt", "keep")
	executeWrite(t, ctx, write, "bundle.zip:new.txt", "new")
	assertReadContains(t, ctx, read, `{"path":"bundle.zip:keep.txt"}`, "keep")
	assertReadContains(t, ctx, read, `{"path":"bundle.zip:new.txt"}`, "new")

	executeWrite(t, ctx, write, "bundle.asar:first.txt", "first")
	executeWrite(t, ctx, write, "bundle.asar:dir/second.txt", "second")
	assertReadContains(t, ctx, read, `{"path":"bundle.asar:first.txt"}`, "first")
	assertReadContains(t, ctx, read, `{"path":"bundle.asar:dir/second.txt"}`, "second")
	executeWrite(t, ctx, write, "bundle.tar.zst:zstd.txt", "compressed")
	assertReadContains(t, ctx, read, `{"path":"bundle.tar.zst:zstd.txt"}`, "compressed")
	database, err := sql.Open("sqlite", filepath.Join(root, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	unsafeMember := callWrite(ctx, write, "bundle.zip:../escape.txt", "unsafe")
	if !unsafeMember.IsError {
		t.Fatalf("archive traversal write = %#v", unsafeMember)
	}
	if _, err := database.Exec(`CREATE TABLE items(id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	executeWrite(t, ctx, write, "data.db:items", `{"id":"a","name":"one"}`)
	executeWrite(t, ctx, write, "data.db:items:a", `{"name":"two"}`)
	assertReadContains(t, ctx, read, `{"path":"data.db:items:a"}`, `"name": "two"`)
	executeWrite(t, ctx, write, "data.db:items:a", "")
	assertReadContains(t, ctx, read, `{"path":"data.db:items"}`, "[]")
	duplicate := callWrite(ctx, write, "data.db:items", `{"id":"b","name":"one","name":"two"}`)
	if !duplicate.IsError || !strings.Contains(duplicate.Content, "duplicate JSON object key") {
		t.Fatalf("duplicate SQLite object = %#v", duplicate)
	}
}

func TestOMPWriteDriverRoutesWritableInternalResources(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	router := resource.NewRouter(1024)
	handler := &writeCaptureResource{}
	if err := router.Register("capture", handler); err != nil {
		t.Fatal(err)
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
	write := findWorkspaceTool(t, service, root, ToolWriteFile)
	callerCtx := WithInvocation(ctx, Invocation{SessionID: "session-a", TeamRunID: "run-a"})
	result := executeWrite(t, callerCtx, write, "capture://note", "hello")
	if result.IsError || string(handler.data) != "hello" || handler.sessionID != "session-a" || handler.runID != "run-a" {
		t.Fatalf("resource write=%#v capture=%#v", result, handler)
	}
}

func newWriteTestService(t *testing.T, ctx context.Context, root string) *Service {
	t.Helper()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	service, err := NewService(store, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	return service
}

func executeWrite(t *testing.T, ctx context.Context, driver tool.Driver, path, content string) tool.Result {
	t.Helper()
	result := callWrite(ctx, driver, path, content)
	if result.IsError {
		t.Fatalf("write %s failed: %s", path, result.Content)
	}
	return result
}

func callWrite(ctx context.Context, driver tool.Driver, path, content string) tool.Result {
	arguments, _ := json.Marshal(map[string]string{"path": path, "content": content})
	result, err := driver.Execute(ctx, tool.Call{ID: "write", Name: ToolWriteFile, Arguments: arguments}, nil)
	if err != nil {
		return tool.Result{IsError: true, Content: err.Error()}
	}
	return result
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != want {
		t.Fatalf("%s = %q, want %q", path, payload, want)
	}
}

type writeCaptureResource struct {
	data      []byte
	sessionID string
	runID     string
}

func (handler *writeCaptureResource) Read(context.Context, resource.Request) (resource.Result, error) {
	return resource.Result{Data: append([]byte(nil), handler.data...)}, nil
}

func (handler *writeCaptureResource) Write(_ context.Context, request resource.Request, value resource.Result) (resource.Result, error) {
	handler.data = append([]byte(nil), value.Data...)
	handler.sessionID = request.Scope.SessionID
	handler.runID = request.Scope.RunID
	return resource.Result{URI: request.URI.Raw, Data: append([]byte(nil), value.Data...)}, nil
}
