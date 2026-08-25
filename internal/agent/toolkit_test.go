package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/tool"
)

func TestGlobFindsWorkspaceFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"pattern": "*.go", "path": "internal"})
	result, err := newGlobDriver(coding.NewLocalWorkspace(dir)).Execute(context.Background(), tool.Call{ID: "g1", Name: ToolGlob, Arguments: args}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "internal/a.go") || strings.Contains(result.Content, "README.md") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestSearchSkipsIgnoredTreesInsteadOfTruncatingBeforeSource(t *testing.T) {
	dir := t.TempDir()
	if output, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("a_generated/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(dir, "a_generated")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1_005; index++ {
		path := filepath.Join(generated, fmt.Sprintf("generated-%04d.txt", index))
		if err := os.WriteFile(path, []byte("noise\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(dir, "internal", "app", "target.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package app\n\nconst ReliableSearchNeedle = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &Service{allowWrite: true, shellPolicy: "deny"}
	drivers, err := service.WorkspaceDrivers(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var search, editDriver tool.Driver
	for _, candidate := range drivers {
		switch candidate.Definition().Name {
		case coding.ToolSearch:
			search = candidate
		case coding.ToolEditHashline:
			editDriver = candidate
		}
	}
	if search == nil || editDriver == nil {
		t.Fatal("coding.search/edit_hashline driver unavailable")
	}
	arguments := json.RawMessage(`{"query":"ReliableSearchNeedle"}`)
	result, err := search.Execute(context.Background(), tool.Call{ID: "search", Name: coding.ToolSearch, Arguments: arguments}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "internal/app/target.go") {
		t.Fatalf("search result=%+v err=%v", result, err)
	}
	var searchResult coding.SearchToolResult
	if json.Unmarshal(result.Structured, &searchResult) != nil || searchResult.Truncated ||
		len(searchResult.Files) != 1 || len(searchResult.Files[0].Matches) != 1 {
		t.Fatalf("structured search result=%+v", searchResult)
	}
	patch, _ := json.Marshal(map[string]string{
		"input": ompTestPatch(searchResult.Files[0].Header, "PUT 3.=3:\n+const ReliableSearchNeedle = false"),
	})
	edited, err := editDriver.Execute(context.Background(), tool.Call{ID: "edit", Name: coding.ToolEditHashline, Arguments: patch}, nil)
	if err != nil || edited.IsError {
		t.Fatalf("edit from search anchors=%+v err=%v", edited, err)
	}
	data, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(data), "ReliableSearchNeedle = false") {
		t.Fatalf("edited target=%q err=%v", data, err)
	}
}

func TestApplyUniqueReplaces(t *testing.T) {
	got, err := applyUniqueReplaces("alpha\nbeta\n", []replaceEdit{{OldText: "beta", NewText: "gamma"}})
	if err != nil || got != "alpha\ngamma\n" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := applyUniqueReplaces("aa", []replaceEdit{{OldText: "a", NewText: "b"}}); err == nil || !strings.Contains(err.Error(), "not unique") {
		t.Fatalf("duplicate old_text err=%v", err)
	}
}

func TestReplacePreservesOuterToolIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := testReplaceDriver(t, dir)
	args, _ := json.Marshal(replaceInput{Path: "note.txt", Edits: []replaceEdit{{OldText: "beta", NewText: "gamma"}}})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "replace-1", Name: ToolReplace, Arguments: args}, nil)
	if err != nil || result.IsError || result.ToolCallID != "replace-1" || result.Name != ToolReplace {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "alpha\ngamma\n\n\n" {
		t.Fatalf("file=%q err=%v", data, err)
	}
}

func TestReplaceRejectsTruncatedReadWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	before := "TARGET\n" + strings.Repeat("x", (1<<20)+32)
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := testReplaceDriver(t, dir)
	args, _ := json.Marshal(replaceInput{Path: "large.txt", Edits: []replaceEdit{{OldText: "TARGET", NewText: "CHANGED"}}})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "replace-large", Name: ToolReplace, Arguments: args}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "safe read window") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != before {
		t.Fatalf("large file changed=%v err=%v", string(data) != before, err)
	}
}

func testReplaceDriver(t *testing.T, dir string) tool.Driver {
	t.Helper()
	var snapshotRead tool.Driver
	for _, driver := range coding.NewToolSet(coding.NewLocalWorkspace(dir)) {
		if driver.Definition().Name == coding.ToolReadFile {
			snapshotRead = driver
			break
		}
	}
	if snapshotRead == nil {
		t.Fatal("read driver unavailable")
	}
	readDriver := newOMPReadDriver(dir, snapshotRead, nil, "deny")
	editDriver := newOMPHashlineDriver(dir, snapshotRead, newHashlineClipboard(), nil)
	return newReplaceDriver(readDriver, editDriver)
}

func TestGoTestFailureIsToolError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/tooltest\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("package tooltest\nimport \"testing\"\nfunc TestFailure(t *testing.T){ t.Fatal(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &Service{allowWrite: true, shellPolicy: "deny"}
	drivers, err := service.WorkspaceDrivers(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var driver tool.Driver
	for _, candidate := range drivers {
		if candidate.Definition().Name == coding.ToolGoTest {
			driver = candidate
			break
		}
	}
	if driver == nil {
		t.Fatal("go_test driver unavailable")
	}
	args, _ := json.Marshal(map[string]string{"package": "./...", "run": "TestFailure"})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "test-fail", Name: coding.ToolGoTest, Arguments: args}, nil)
	if err != nil || !result.IsError {
		t.Fatalf("go_test result=%+v err=%v", result, err)
	}
	var status coding.GoTestToolResult
	if json.Unmarshal(result.Structured, &status) != nil || status.Passed || status.ExitCode == 0 {
		t.Fatalf("go_test status=%+v", status)
	}
}

func TestDeleteFileRemovesRegularFile(t *testing.T) {
	if !secureDeleteSupported {
		t.Skip("secure workspace deletion is unavailable on this platform")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	if err := os.WriteFile(path, []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": "gone.txt"})
	result, err := newDeleteFileDriver(dir).Execute(context.Background(), tool.Call{ID: "d1", Name: ToolDeleteFile, Arguments: args}, nil)
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Content, `"size":4`) {
		t.Fatalf("content=%s", result.Content)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestDeleteFileRejectsDirectoryAndEscape(t *testing.T) {
	if !secureDeleteSupported {
		t.Skip("secure workspace deletion is unavailable on this platform")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": "sub"})
	result, err := newDeleteFileDriver(dir).Execute(context.Background(), tool.Call{ID: "d1", Name: ToolDeleteFile, Arguments: args}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "not a file") {
		t.Fatalf("dir result=%+v err=%v", result, err)
	}
	args, _ = json.Marshal(map[string]string{"path": "../outside.txt"})
	result, err = newDeleteFileDriver(dir).Execute(context.Background(), tool.Call{ID: "d2", Name: ToolDeleteFile, Arguments: args}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "escapes") {
		t.Fatalf("escape result=%+v err=%v", result, err)
	}
	arguments, _ := json.Marshal(map[string]string{"path": ".GiT/config"})
	denied, err := newDeleteFileDriver(dir).Execute(context.Background(), tool.Call{
		ID: "git-tree", Name: ToolDeleteFile, Arguments: arguments,
	}, nil)
	if err != nil || !denied.IsError || !strings.Contains(strings.ToLower(denied.Content), ".git") {
		t.Fatalf("git tree result=%+v err=%v", denied, err)
	}
}

func TestDeleteFileRejectsSymlinkedParentEscape(t *testing.T) {
	if !secureDeleteSupported {
		t.Skip("secure workspace deletion is unavailable on this platform")
	}
	workspace := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(map[string]string{"path": "escape/keep.txt"})
	result, err := newDeleteFileDriver(workspace).Execute(context.Background(), tool.Call{
		ID: "symlink-parent", Name: ToolDeleteFile, Arguments: arguments,
	}, nil)
	if err != nil || !result.IsError {
		t.Fatalf("symlink-parent result=%+v err=%v", result, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatalf("outside target changed: %q err=%v", data, err)
	}
}

func TestWorkspaceDriversExposeToolkit(t *testing.T) {
	dir := t.TempDir()
	service := &Service{allowWrite: true, shellPolicy: "deny"}
	drivers, err := service.WorkspaceDrivers(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, driver := range drivers {
		got[driver.Definition().Name] = true
	}
	for _, name := range []string{ToolGlob, ToolReplace, ToolDeleteFile, coding.ToolEditHashline, coding.ToolReadFile} {
		if !got[name] {
			t.Fatalf("missing %s in %v", name, got)
		}
	}
}
