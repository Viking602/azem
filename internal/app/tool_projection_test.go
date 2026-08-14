package app

import (
	"encoding/json"
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
