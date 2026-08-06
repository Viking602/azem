package session

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestStartToolRecordAtUsesCommentarySequence(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, statement := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY)`,
		`CREATE TABLE session_blocks (session_id TEXT NOT NULL, sequence INTEGER NOT NULL, kind TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '', agent_id TEXT NOT NULL DEFAULT '', data BLOB NOT NULL, PRIMARY KEY(session_id,sequence))`,
		`CREATE TABLE session_tool_records (session_id TEXT NOT NULL, run_id TEXT NOT NULL, tool_call_id TEXT NOT NULL, anchor_sequence INTEGER NOT NULL DEFAULT -1, name TEXT NOT NULL, arguments BLOB NOT NULL DEFAULT '{}', state TEXT NOT NULL, content TEXT NOT NULL DEFAULT '', structured BLOB NOT NULL DEFAULT 'null', artifact_id TEXT NOT NULL DEFAULT '', observations BLOB NOT NULL DEFAULT '[]', started_at INTEGER NOT NULL, completed_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(session_id,run_id,tool_call_id))`,
		`INSERT INTO sessions(id) VALUES('session')`,
		`INSERT INTO session_blocks(session_id,sequence,kind,data) VALUES('session',0,'user','{}'),('session',1,'commentary','{}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	commentarySequence := int64(1)
	record, err := NewService(db).StartToolRecordAt(context.Background(), "session", ToolRecord{
		RunID: "run", ToolCallID: "read-1", Name: "coding.read_file",
	}, commentarySequence)
	if err != nil {
		t.Fatal(err)
	}
	if record.AnchorSequence != commentarySequence {
		t.Fatalf("anchor sequence = %d, want %d", record.AnchorSequence, commentarySequence)
	}
}
