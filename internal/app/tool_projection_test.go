package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

// TestProjectToolRecordsAttachesFileChangeSummaries verifies that durable
// session reloads carry the same structured file-change projection as live
// tool_finished events, and that non-file or failed tools stay untouched.
func TestProjectToolRecordsAttachesFileChangeSummaries(t *testing.T) {
	records := []session.ToolRecord{
		{
			ToolCallID: "edit-1", Name: "coding.edit_hashline", State: session.ToolCompleted,
			Structured: json.RawMessage(`{"sections":[{"path":"a.go","firstChangedLine":3,"diff":"-x\n+y"}]}`),
		},
		{
			ToolCallID: "write-1", Name: "coding.write_file", State: session.ToolCompleted,
			Arguments: json.RawMessage(`{"path":"b.md","content":"hello\n"}`),
		},
		{ToolCallID: "edit-2", Name: "coding.edit_hashline", State: session.ToolFailed},
		{ToolCallID: "shell-1", Name: "shell.execute", State: session.ToolCompleted},
	}

	projected := projectToolRecords(records)
	if len(projected) != 4 {
		t.Fatalf("expected 4 projected records, got %d", len(projected))
	}

	var editSummary struct {
		Files []struct {
			Path      string `json:"path"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(projected[0].FileChange), &editSummary); err != nil {
		t.Fatalf("decode edit projection: %v", err)
	}
	if len(editSummary.Files) != 1 || editSummary.Files[0].Path != "a.go" ||
		editSummary.Files[0].Additions != 1 || editSummary.Files[0].Deletions != 1 {
		t.Fatalf("unexpected edit projection: %+v", editSummary)
	}
	if projected[1].FileChange == "" {
		t.Fatal("completed write must project a file change")
	}
	if projected[2].FileChange != "" {
		t.Fatal("failed edits must not project file changes")
	}
	if projected[3].FileChange != "" {
		t.Fatal("non file-change tools must not project file changes")
	}
}

func TestNativeEditsAndMediaHaveDurableReadback(t *testing.T) {
	root := t.TempDir()
	payload := bytes.Repeat([]byte{1, 2, 3, 4}, 400000)
	if err := os.WriteFile(filepath.Join(root, "image.png"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	timeline := &durableToolTimeline{workspace: root}
	observations := timeline.fileObservations("generate_image", json.RawMessage(`{"output_path":"image.png","input":["source.png"]}`), nil, true)
	if len(observations) != 1 || observations[0].SHA256 != sha256Hex(payload) || observations[0].ErrorCode != "" {
		t.Fatal(observations)
	}
	record := session.ToolRecord{Name: "generate_image", State: session.ToolCompleted, Observations: observations, CompletedAt: time.Now()}
	files, mutated, _, failures := runtimeRevisionFiles(root, []session.ToolRecord{record})
	if len(failures) != 0 || !mutated || len(files) != 1 || files[0].SHA256 != sha256Hex(payload) {
		t.Fatal(files, mutated, failures)
	}
	readback := timeline.fileObservations("inspect_image", json.RawMessage(`{"path":"image.png"}`), nil, true)
	if len(readback) != 1 || readback[0].Operation != "read" || readback[0].SHA256 != observations[0].SHA256 {
		t.Fatal(readback)
	}
	args := json.RawMessage(`{"path":"**/*.go","apply":true}`)
	result := json.RawMessage(`{"sections":[{"path":"main.go"}]}`)
	ast := requestedFileObservations("ast_edit", args, result)
	if len(ast) != 1 || ast[0].Path != "main.go" || ast[0].Operation != "edit" {
		t.Fatal(ast)
	}
	if !toolRecordMutated(session.ToolRecord{Name: "ast_edit", Structured: result}) {
		t.Fatal("AST mutation missed")
	}
	if len(requestedFileObservations("ast_edit", args, json.RawMessage(`{"dryRun":true,"patch":"preview"}`))) != 0 {
		t.Fatal("preview recorded as mutation")
	}
}

func TestReplaceAndDeleteProduceDurableFileObservations(t *testing.T) {
	replace := requestedFileObservations(
		"coding.replace",
		json.RawMessage(`{"path":"a.go"}`),
		json.RawMessage(`{"sections":[{"path":"a.go"}]}`),
	)
	if len(replace) != 1 || replace[0].Path != "a.go" || replace[0].Operation != "edit" {
		t.Fatalf("replace observations = %+v", replace)
	}
	deleted := requestedFileObservations(
		"coding.delete_file",
		json.RawMessage(`{"path":"gone.txt"}`),
		nil,
	)
	if len(deleted) != 1 || deleted[0].Path != "gone.txt" || deleted[0].Operation != "delete" {
		t.Fatalf("delete observations = %+v", deleted)
	}
}

func TestObservationPathInWorkspaceRelativizesAbsoluteAndDropsEscapes(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "gpui", "src", "main.rs")
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relative, ok := observationPathInWorkspace(root, absolute)
	if !ok || relative != "gpui/src/main.rs" {
		t.Fatalf("absolute under workspace = %q ok=%v", relative, ok)
	}
	if path, ok := observationPathInWorkspace(root, "gpui/src/main.rs"); !ok || path != "gpui/src/main.rs" {
		t.Fatalf("relative path = %q ok=%v", path, ok)
	}
	if _, ok := observationPathInWorkspace(root, "../outside.go"); ok {
		t.Fatal("parent escape must be rejected")
	}
	if _, ok := observationPathInWorkspace(root, filepath.Join(t.TempDir(), "foreign.go")); ok {
		t.Fatal("foreign absolute path must be rejected")
	}
}

func TestFileObservationsRelativizeAbsolutePathsBeforeEvidence(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "note.txt")
	if err := os.WriteFile(absolute, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	timeline := &durableToolTimeline{workspace: root}
	observations := timeline.fileObservations(
		"coding.read_file",
		json.RawMessage(`{"path":`+strconv.Quote(absolute)+`}`),
		nil,
		true,
	)
	if len(observations) != 1 || observations[0].Path != "note.txt" || observations[0].SHA256 == "" || observations[0].ErrorCode != "" {
		t.Fatalf("observations = %+v", observations)
	}
}

func TestDirectoryReadDoesNotBecomeFileEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("documentation\n")
	if err := os.WriteFile(filepath.Join(root, "docs/index.md"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	timeline := &durableToolTimeline{workspace: root}
	args := json.RawMessage(`{"path":"docs","limit":50}`)
	listing := json.RawMessage(`{"path":"docs","kind":"directory","content":"index.md","bytes":8}`)
	if observations := timeline.fileObservations("coding.read_file", args, listing, true); len(observations) != 0 {
		t.Fatalf("directory listing acquired file observations: %+v", observations)
	}
	// Replay the exact legacy failure shape without modifying its stored record.
	legacy := session.ToolRecord{Name: "coding.read_file", State: session.ToolCompleted, Structured: listing,
		Observations: []session.FileObservation{{Path: "docs", Operation: "read", ErrorCode: "limit_exceeded"}}}
	file := session.ToolRecord{Name: "coding.write_file", State: session.ToolCompleted,
		Observations: []session.FileObservation{{Path: "docs/index.md", Operation: "write", SHA256: sha256Hex(body)}}}
	files, mutated, _, failures := runtimeRevisionFiles(root, []session.ToolRecord{legacy, file})
	if len(failures) != 0 || !mutated || len(files) != 1 || files[0].SHA256 != sha256Hex(body) {
		t.Fatalf("legacy directory blocked real file verification: files=%+v mutated=%v errors=%v", files, mutated, failures)
	}
	for _, kind := range []json.RawMessage{nil, json.RawMessage(`{"kind":"file"}`), json.RawMessage(`invalid`)} {
		legacy.Structured = kind
		if _, _, _, failures := runtimeRevisionFiles(root, []session.ToolRecord{legacy}); len(failures) == 0 {
			t.Fatal("non-directory evidence failure was hidden")
		}
	}
	legacy.Structured, legacy.Name = listing, "coding.write_file"
	if _, _, _, failures := runtimeRevisionFiles(root, []session.ToolRecord{legacy}); len(failures) == 0 {
		t.Fatal("a write impersonated a read-only directory listing")
	}
}
