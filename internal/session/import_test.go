package session

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/message"
)

func TestImportSessionPreservesSourceTreeAndModelMessages(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), store.Blobs())
	workspace := t.TempDir()
	createdAt := time.Unix(100, 0).UTC()
	toolMessage := message.Message{
		Role:       message.RoleAssistant,
		ToolCalls:  []message.ToolCall{{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{"path":"a.txt"}`)}},
		Visibility: message.VisibilityShared,
		CreatedAt:  createdAt.Add(time.Second),
	}
	encodedTool, err := json.Marshal(toolMessage)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := SessionImport{
		Session:    Session{ID: "imported", Title: "Imported tree", CreatedAt: createdAt, UpdatedAt: createdAt.Add(3 * time.Second)},
		SourceKind: ImportSourceClaude,
		SourceRef:  "/foreign/claude/session.jsonl",
		Workspace:  workspace,
		Entries: []ImportEntry{
			{SourceID: "source-user", Block: Block{Kind: "user", Content: "question"}, CreatedAt: createdAt},
			{SourceID: "source-tool", ParentSourceID: "source-user", Block: Block{Kind: "assistant", Content: "Tool call: read", ImportedMessage: encodedTool}, CreatedAt: createdAt.Add(time.Second)},
			{SourceID: "source-old", ParentSourceID: "source-tool", Block: Block{Kind: "assistant", Content: "old answer"}, CreatedAt: createdAt.Add(2 * time.Second)},
			{SourceID: "source-new", ParentSourceID: "source-user", Block: Block{Kind: "assistant", Content: "new answer"}, CreatedAt: createdAt.Add(3 * time.Second)},
		},
		ActiveSourceID: "source-new",
	}
	if err := service.ImportSession(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	tree, err := service.LoadSessionTree(ctx, "imported")
	if err != nil {
		t.Fatal(err)
	}
	if tree.RootSessionID != "imported" || tree.ActiveLeafEntryID != sessionEntryID("imported", 3) || len(tree.Roots) != 1 {
		t.Fatalf("imported tree=%#v", tree)
	}
	root := tree.Roots[0]
	if root.Entry.SourceID != "source-user" || len(root.Children) != 2 || root.Children[0].Entry.SourceID != "source-tool" || root.Children[1].Entry.SourceID != "source-new" {
		t.Fatalf("imported source topology=%#v", root)
	}
	projection, err := service.LoadProjection(ctx, "imported")
	if err != nil {
		t.Fatal(err)
	}
	if got := blockSequences(projection.Blocks); len(got) != 2 || got[0] != 0 || got[1] != 3 {
		t.Fatalf("active imported blocks=%v", got)
	}
	var sourceKind, sourceRef string
	if err := store.DB().QueryRowContext(ctx, `SELECT source_kind,source_ref FROM session_graphs WHERE session_id='imported'`).Scan(&sourceKind, &sourceRef); err != nil {
		t.Fatal(err)
	}
	if sourceKind != ImportSourceClaude || sourceRef != snapshot.SourceRef {
		t.Fatalf("source provenance=%q/%q", sourceKind, sourceRef)
	}
	var assigned string
	if err := store.DB().QueryRowContext(ctx, `SELECT workspace FROM session_workspaces WHERE session_id='imported'`).Scan(&assigned); err != nil || assigned != workspace {
		t.Fatalf("workspace=%q error=%v", assigned, err)
	}
}

func TestImportSessionRejectsCyclesAndDuplicateIDsAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), store.Blobs())
	base := SessionImport{Session: Session{ID: "invalid", Title: "Invalid"}, SourceKind: ImportSourceCodex, SourceRef: "/foreign/codex.jsonl"}
	base.Entries = []ImportEntry{
		{SourceID: "a", ParentSourceID: "b", Block: Block{Kind: "user", Content: "a"}},
		{SourceID: "b", ParentSourceID: "a", Block: Block{Kind: "assistant", Content: "b"}},
	}
	if err := service.ImportSession(ctx, base); err == nil {
		t.Fatal("accepted cyclic import")
	}
	base.Entries = []ImportEntry{
		{SourceID: "same", Block: Block{Kind: "user", Content: "a"}},
		{SourceID: "same", Block: Block{Kind: "assistant", Content: "b"}},
	}
	if err := service.ImportSession(ctx, base); err == nil {
		t.Fatal("accepted duplicate import ids")
	}
	if _, err := service.LoadSession(ctx, "invalid"); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("invalid import left session: %v", err)
	}
}
