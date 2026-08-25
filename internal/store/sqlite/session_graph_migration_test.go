package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestMigrationV24BackfillsSessionGraphAndMaintainsTriggers(t *testing.T) {
	if len(migrations) != schemaVersion || schemaVersion < 24 {
		t.Fatalf("migration count=%d schema=%d", len(migrations), schemaVersion)
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session-graph.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 23; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("apply fixture migration %d: %v", version, err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions(id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at)
		VALUES('source','Source','chatgpt','model','high','single',100,200),('empty','Empty','','','','single',110,210);
		INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data,data_sha256)
		VALUES('source',10,'user','run-1','','{}',''),('source',20,'assistant','run-1','','{}','');
		PRAGMA user_version=23;
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	provider, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := provider.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d error=%v", version, err)
	}
	var root, branch, leaf, title string
	if err := provider.db.QueryRowContext(ctx, `
		SELECT g.root_session_id,g.active_branch,g.active_leaf_entry_id,s.title
		FROM session_graphs g JOIN sessions s ON s.id=g.session_id WHERE g.session_id='source'
	`).Scan(&root, &branch, &leaf, &title); err != nil {
		t.Fatal(err)
	}
	if root != "source" || branch != "main" || leaf != graphEntryID("source", 20) || title != "Source" {
		t.Fatalf("backfilled graph=%q/%q/%q title=%q", root, branch, leaf, title)
	}
	rows, err := provider.db.QueryContext(ctx, `SELECT entry_id,COALESCE(parent_entry_id,''),block_sequence,kind FROM session_graph_entries WHERE session_id='source' ORDER BY block_sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var entries [][4]any
	for rows.Next() {
		var entry, parent, kind string
		var sequence int64
		if err := rows.Scan(&entry, &parent, &sequence, &kind); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, [4]any{entry, parent, sequence, kind})
	}
	if len(entries) != 2 || entries[0][0] != graphEntryID("source", 10) || entries[0][1] != "" || entries[1][1] != graphEntryID("source", 10) {
		t.Fatalf("backfilled entries=%#v", entries)
	}
	var branchHead string
	if err := provider.db.QueryRowContext(ctx, `SELECT head_entry_id FROM session_branches WHERE session_id='source' AND name='main'`).Scan(&branchHead); err != nil || branchHead != leaf {
		t.Fatalf("branch head=%q error=%v", branchHead, err)
	}
	if _, err := provider.db.ExecContext(ctx, `
		INSERT INTO sessions(id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at)
		VALUES('new','New','','','','single',300,300);
		INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data,data_sha256)
		VALUES('new',1,'user','run-new','','{}',''),('new',2,'assistant','run-new','','{}','');
	`); err != nil {
		t.Fatal(err)
	}
	if err := provider.db.QueryRowContext(ctx, `SELECT root_session_id,active_leaf_entry_id FROM session_graphs WHERE session_id='new'`).Scan(&root, &leaf); err != nil {
		t.Fatal(err)
	}
	if root != "new" || leaf != graphEntryID("new", 2) {
		t.Fatalf("trigger graph=%q/%q", root, leaf)
	}
	if _, err := provider.db.ExecContext(ctx, `DELETE FROM sessions WHERE id='new'`); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := provider.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_graph_entries WHERE session_id='new'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("graph cascade count=%d error=%v", remaining, err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	if err := reopened.db.QueryRowContext(ctx, `SELECT title FROM sessions WHERE id='source'`).Scan(&title); err != nil || title != "Source" {
		t.Fatalf("retained source=%q error=%v", title, err)
	}
}

func graphEntryID(sessionID string, sequence int64) string {
	return fmt.Sprintf("%s:%020d", sessionID, sequence)
}
