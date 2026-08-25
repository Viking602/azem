package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestSessionTreeNavigationAndNamedBranches(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Tree"}); err != nil {
		t.Fatal(err)
	}
	for index, block := range []Block{
		{Kind: "user", RunID: "run-1", Content: "root question"},
		{Kind: "assistant", RunID: "run-1", Content: "root answer"},
		{Kind: "user", RunID: "run-2", Content: "original question"},
		{Kind: "assistant", RunID: "run-2", Content: "original answer"},
	} {
		sequence, err := service.AppendBlock(ctx, "session", block)
		if err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
		if sequence != int64(index) {
			t.Fatalf("sequence=%d want=%d", sequence, index)
		}
	}
	tree, err := service.LoadSessionTree(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if tree.ActiveBranch != "main" || tree.ActiveLeafEntryID != sessionEntryID("session", 3) || len(tree.Roots) != 1 {
		t.Fatalf("initial tree=%#v", tree)
	}
	if got := tree.Roots[0].Children[0].Children[0].Children[0].Entry.Sequence; got != 3 {
		t.Fatalf("linear leaf sequence=%d", got)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE session_projections SET model_history='invalid',cache_epoch=7 WHERE session_id='session'`); err != nil {
		t.Fatal(err)
	}
	navigation, err := service.NavigateSessionTree(ctx, "session", sessionEntryID("session", 1))
	if err != nil {
		t.Fatal(err)
	}
	if navigation.OldLeaf != sessionEntryID("session", 3) || navigation.NewLeaf != sessionEntryID("session", 1) || !navigation.ContextChange {
		t.Fatalf("navigation=%#v", navigation)
	}
	projection, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if projection.CacheEpoch != 8 || len(projection.Blocks) != 2 || projection.Blocks[1].Content != "root answer" {
		t.Fatalf("navigated projection epoch=%d blocks=%#v", projection.CacheEpoch, projection.Blocks)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-3", Content: "alternate question"}); err != nil {
		t.Fatal(err)
	}
	tree, err = service.LoadSessionTree(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	forkPoint := tree.Roots[0].Children[0]
	if len(forkPoint.Children) != 2 || forkPoint.Children[0].Entry.Sequence != 2 || forkPoint.Children[1].Entry.Sequence != 4 {
		t.Fatalf("fork children=%#v", forkPoint.Children)
	}
	path, err := service.LoadSessionBranch(ctx, "session", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := graphSequences(path); fmt.Sprint(got) != "[0 1 4]" {
		t.Fatalf("active path=%v", got)
	}
	created, err := service.CreateSessionBranch(ctx, "session", "Experiment", sessionEntryID("session", 1))
	if err != nil {
		t.Fatal(err)
	}
	if created.Branch != "Experiment" || created.NewLeaf != sessionEntryID("session", 1) {
		t.Fatalf("created branch=%#v", created)
	}
	if _, err := service.CreateSessionBranch(ctx, "session", "experiment", sessionEntryID("session", 1)); !errors.Is(err, ErrSessionBranchExists) {
		t.Fatalf("duplicate branch error=%v", err)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "assistant", RunID: "run-4", Content: "experiment answer"}); err != nil {
		t.Fatal(err)
	}
	branches, err := service.ListSessionBranches(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 || branches[0].Name != "main" || branches[0].Active || branches[1].Name != "Experiment" || !branches[1].Active || branches[1].HeadEntryID != sessionEntryID("session", 5) {
		t.Fatalf("branches=%#v", branches)
	}
	switched, err := service.SwitchSessionBranch(ctx, "session", "MAIN")
	if err != nil {
		t.Fatal(err)
	}
	if switched.NewLeaf != sessionEntryID("session", 4) || switched.Branch != "main" {
		t.Fatalf("switched=%#v", switched)
	}
	projection, err = service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if got := blockSequences(projection.Blocks); fmt.Sprint(got) != "[0 1 4]" {
		t.Fatalf("main projection=%v", got)
	}
	if err := service.RenameSessionBranch(ctx, "session", "Experiment", "Try again"); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSessionBranch(ctx, "session", "try AGAIN"); err != nil {
		t.Fatal(err)
	}
	branches, err = service.ListSessionBranches(ctx, "session")
	if err != nil || len(branches) != 1 || branches[0].Name != "main" {
		t.Fatalf("remaining branches=%#v error=%v", branches, err)
	}
}

func TestSessionTreeResetCreatesAnotherRootAndRejectsMissingEntries(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, Session{ID: "session"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", Content: "first root"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.NavigateSessionTree(ctx, "session", "missing"); !errors.Is(err, ErrSessionGraphEntryNotFound) {
		t.Fatalf("missing navigation error=%v", err)
	}
	if _, err := service.NavigateSessionTree(ctx, "session", ""); err != nil {
		t.Fatal(err)
	}
	projection, err := service.LoadProjection(ctx, "session")
	if err != nil || len(projection.Blocks) != 0 {
		t.Fatalf("reset projection=%#v error=%v", projection.Blocks, err)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", Content: "second root"}); err != nil {
		t.Fatal(err)
	}
	tree, err := service.LoadSessionTree(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 2 || tree.Roots[0].Entry.Sequence != 0 || tree.Roots[1].Entry.Sequence != 1 {
		t.Fatalf("roots=%#v", tree.Roots)
	}
	if err := service.DeleteSessionBranch(ctx, "session", "main"); err == nil {
		t.Fatal("deleted main branch")
	}
	if err := service.RenameSessionBranch(ctx, "session", "main", "other"); err == nil {
		t.Fatal("renamed main branch")
	}
}

func TestSessionForkPreservesTreeLineageLabelsAndCacheIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, Session{ID: "source", Title: "Source"}); err != nil {
		t.Fatal(err)
	}
	for _, block := range []Block{
		{Kind: "user", Content: "root"},
		{Kind: "assistant", Content: "answer"},
		{Kind: "user", Content: "original"},
		{Kind: "assistant", Content: "old leaf"},
	} {
		if _, err := service.AppendBlock(ctx, "source", block); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.NavigateSessionTree(ctx, "source", sessionEntryID("source", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "source", Block{Kind: "assistant", Content: "active leaf"}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSessionEntryLabel(ctx, "source", sessionEntryID("source", 1), "Shared boundary"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSessionEntryLabel(ctx, "source", sessionEntryID("source", 4), "Active leaf"); err != nil {
		t.Fatal(err)
	}
	if err := service.Fork(ctx, "source", "full"); err != nil {
		t.Fatal(err)
	}
	full, err := service.LoadSessionTree(ctx, "full")
	if err != nil {
		t.Fatal(err)
	}
	if full.RootSessionID != "source" || full.ParentSessionID != "source" ||
		full.ForkedFromEntryID != sessionEntryID("source", 4) || full.ActiveLeafEntryID != sessionEntryID("full", 4) {
		t.Fatalf("full fork lineage=%#v", full)
	}
	if len(full.Roots) != 1 || len(full.Roots[0].Children) != 1 || len(full.Roots[0].Children[0].Children) != 2 {
		t.Fatalf("full fork tree=%#v", full.Roots)
	}
	labels, err := service.ListSessionLabels(ctx, "full")
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || labels[0].EntryID != sessionEntryID("full", 1) || labels[1].EntryID != sessionEntryID("full", 4) {
		t.Fatalf("full fork labels=%#v", labels)
	}
	cacheKey, err := service.PromptCacheKey(ctx, "full")
	if err != nil || cacheKey != "source" {
		t.Fatalf("fork cache key=%q error=%v", cacheKey, err)
	}
	if err := service.Fork(ctx, "full", "second-generation"); err != nil {
		t.Fatal(err)
	}
	second, err := service.LoadSessionTree(ctx, "second-generation")
	if err != nil {
		t.Fatal(err)
	}
	cacheKey, err = service.PromptCacheKey(ctx, "second-generation")
	if err != nil || second.RootSessionID != "source" || second.ParentSessionID != "full" || cacheKey != "source" {
		t.Fatalf("second fork=%#v cache=%q error=%v", second, cacheKey, err)
	}
	if err := service.ForkAt(ctx, "source", "path", sessionEntryID("source", 3)); err != nil {
		t.Fatal(err)
	}
	pathTree, err := service.LoadSessionTree(ctx, "path")
	if err != nil {
		t.Fatal(err)
	}
	if pathTree.ParentSessionID != "source" || pathTree.ForkedFromEntryID != sessionEntryID("source", 3) ||
		pathTree.ActiveLeafEntryID != sessionEntryID("path", 3) || len(pathTree.Branches) != 1 || pathTree.Branches[0].Name != "main" {
		t.Fatalf("path fork=%#v", pathTree)
	}
	pathProjection, err := service.LoadProjection(ctx, "path")
	if err != nil {
		t.Fatal(err)
	}
	if got := blockSequences(pathProjection.Blocks); fmt.Sprint(got) != "[0 1 2 3]" {
		t.Fatalf("path fork blocks=%v", got)
	}
	labels, err = service.ListSessionLabels(ctx, "path")
	if err != nil || len(labels) != 1 || labels[0].EntryID != sessionEntryID("path", 1) {
		t.Fatalf("path labels=%#v error=%v", labels, err)
	}
	if err := service.ForkAt(ctx, "source", "empty-path", ""); err != nil {
		t.Fatal(err)
	}
	emptyProjection, err := service.LoadProjection(ctx, "empty-path")
	if err != nil || len(emptyProjection.Blocks) != 0 {
		t.Fatalf("empty fork blocks=%#v error=%v", emptyProjection.Blocks, err)
	}
	if err := service.ForkAt(ctx, "source", "missing-path", "missing"); !errors.Is(err, ErrSessionGraphEntryNotFound) {
		t.Fatalf("missing fork entry error=%v", err)
	}
}

func TestSessionEntryLabelsUpdateClearAndAllowDuplicates(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, Session{ID: "session"}); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"one", "two"} {
		if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	for sequence := int64(0); sequence < 2; sequence++ {
		if err := service.SetSessionEntryLabel(ctx, "session", sessionEntryID("session", sequence), "Milestone"); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := service.LoadSessionTree(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Entry.Label != "Milestone" || tree.Roots[0].Children[0].Entry.Label != "Milestone" {
		t.Fatalf("tree labels=%#v", tree.Roots)
	}
	if err := service.SetSessionEntryLabel(ctx, "session", sessionEntryID("session", 0), "Updated"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSessionEntryLabel(ctx, "session", sessionEntryID("session", 1), ""); err != nil {
		t.Fatal(err)
	}
	labels, err := service.ListSessionLabels(ctx, "session")
	if err != nil || len(labels) != 1 || labels[0].Label != "Updated" {
		t.Fatalf("updated labels=%#v error=%v", labels, err)
	}
	if err := service.SetSessionEntryLabel(ctx, "session", "missing", "label"); !errors.Is(err, ErrSessionGraphEntryNotFound) {
		t.Fatalf("missing label error=%v", err)
	}
	if err := service.SetSessionEntryLabel(ctx, "session", sessionEntryID("session", 0), string(make([]byte, 129))); err == nil {
		t.Fatal("accepted oversized label")
	}
}

func sessionEntryID(sessionID string, sequence int64) string {
	return fmt.Sprintf("%s:%020d", sessionID, sequence)
}

func graphSequences(entries []GraphEntry) []int64 {
	values := make([]int64, 0, len(entries))
	for _, entry := range entries {
		values = append(values, entry.Sequence)
	}
	return values
}

func blockSequences(blocks []Block) []int64 {
	values := make([]int64, 0, len(blocks))
	for _, block := range blocks {
		values = append(values, block.Sequence)
	}
	return values
}
