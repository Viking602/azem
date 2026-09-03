package agent

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/tool"
)

func TestReadDriverCoversStructureArchiveSQLiteNotebookImageAndDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	service, err := NewService(store, root, WithWorkspacePolicy(true, "deny", "deny"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })

	writeTestFile(t, filepath.Join(root, "sample.go"), "package sample\n\ntype Thing struct{}\n\nfunc Build() {}\n")
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "conflict.txt"), "<<<<<<< ours\none\n=======\ntwo\n>>>>>>> theirs\noutside\n")
	writeTestFile(t, filepath.Join(root, "dir", "entry.txt"), "entry")
	writeTestFile(t, filepath.Join(root, "book.ipynb"), `{"cells":[{"cell_type":"code","source":["print(1)\n"],"outputs":[{"text":["1\n"]}]}]}`)
	writeTestFile(t, filepath.Join(root, "image.png"), string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}))
	writeZipFixture(t, filepath.Join(root, "bundle.zip"), "notes/a.txt", "inside")
	writeSQLiteFixture(t, filepath.Join(root, "data.db"))

	read := findWorkspaceTool(t, service, root, ToolReadFile)
	assertReadContains(t, ctx, read, `{"path":"sample.go"}`, "type Thing", "func Build")
	assertReadContains(t, ctx, read, `{"path":"sample.go:3-5"}`, "[sample.go#", "3:type Thing", "5:func Build")
	raw, err := read.Execute(ctx, tool.Call{ID: "raw", Name: ToolReadFile, Arguments: json.RawMessage(`{"path":"sample.go:raw"}`)}, nil)
	if err != nil || raw.IsError || raw.Content != "package sample\n\ntype Thing struct{}\n\nfunc Build() {}\n" {
		t.Fatalf("raw read = %#v, %v", raw, err)
	}
	assertReadContains(t, ctx, read, `{"path":"dir"}`, "entry.txt")
	assertReadContains(t, ctx, read, `{"path":"bundle.zip:notes/a.txt"}`, "inside")
	assertReadContains(t, ctx, read, `{"path":"data.db:items"}`, `"name": "one"`)
	assertReadContains(t, ctx, read, `{"path":"book.ipynb"}`, "Cell 1 (code)", "print(1)")

	assertReadContains(t, ctx, read, `{"path":"conflict.txt:conflicts"}`, "<<<<<<< ours", ">>>>>>> theirs")
	result, err := read.Execute(ctx, tool.Call{ID: "image", Name: ToolReadFile, Arguments: json.RawMessage(`{"path":"image.png"}`)}, nil)
	if err != nil || result.IsError || len(result.Parts) != 1 || result.Parts[0].MediaType != "image/png" {
		t.Fatalf("image read = %#v, %v", result, err)
	}
}

func TestSplitReadSelectorHandlesSSHPathsWithoutConfusingPorts(t *testing.T) {
	path, selector := splitReadSelector("ssh://host:22/tmp/file.txt:10-20", "")
	if path != "ssh://host:22/tmp/file.txt" || selector != "10-20" {
		t.Fatalf("SSH selector = %q %q", path, selector)
	}
	path, selector = splitReadSelector("ssh://host:22", "")
	if path != "ssh://host:22" || selector != "" {
		t.Fatalf("SSH authority parsed as selector = %q %q", path, selector)
	}
}

func TestReadDriverReadsAllowedWebContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html")
		_, _ = response.Write([]byte("<html><head><style>hidden</style></head><body><h1>Hello</h1><script>hidden()</script><p>world</p></body></html>"))
	}))
	defer server.Close()

	driver := newReadDriver(t.TempDir(), nil, nil, "allow")
	result, err := driver.Execute(context.Background(), tool.Call{
		ID:        "web",
		Name:      ToolReadFile,
		Arguments: json.RawMessage(`{"path":"` + server.URL + `"}`),
	}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "Hello") || strings.Contains(result.Content, "hidden") {
		t.Fatalf("web read = %#v, %v", result, err)
	}
}

func findWorkspaceTool(t *testing.T, service *Service, root, name string) tool.Driver {
	t.Helper()
	drivers, err := service.WorkspaceDrivers(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, driver := range drivers {
		if driver.Definition().Name == name {
			return driver
		}
	}
	t.Fatalf("tool %s not found", name)
	return nil
}

func assertReadContains(t *testing.T, ctx context.Context, driver tool.Driver, arguments string, wants ...string) {
	t.Helper()
	result, err := driver.Execute(ctx, tool.Call{ID: "read", Name: ToolReadFile, Arguments: json.RawMessage(arguments)}, nil)
	if err != nil || result.IsError {
		t.Fatalf("read %s = %#v, %v", arguments, result, err)
	}
	for _, want := range wants {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("read %s missing %q: %s", arguments, want, result.Content)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeZipFixture(t *testing.T, path, name, content string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeSQLiteFixture(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE items(name TEXT); INSERT INTO items(name) VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
}
