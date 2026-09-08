package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

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
