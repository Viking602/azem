package session

import (
	"context"
	"path/filepath"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestProjectCatalogOwnsSessionsAndRestoresMostRecentWorkspace(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), store.Blobs())
	projectA, projectB := t.TempDir(), t.TempDir()
	projectA, _ = filepath.EvalSymlinks(projectA)
	projectB, _ = filepath.EvalSymlinks(projectB)

	for _, id := range []string{"a", "b"} {
		if _, err := service.Ensure(ctx, Session{ID: id, Title: id}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.AppendBlock(ctx, id, Block{Kind: "user", Content: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.SetWorkspaceSession(ctx, projectA, "a"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetWorkspaceSession(ctx, projectB, "b"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetWorkspaceSession(ctx, projectB, "a"); err == nil {
		t.Fatal("a session must not move between projects")
	}
	if err := service.Fork(ctx, "a", "a-fork"); err != nil {
		t.Fatal(err)
	}

	projects, err := service.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0].Workspace != projectB || projects[1].Workspace != projectA {
		t.Fatalf("projects = %#v", projects)
	}
	last, err := service.LastProject(ctx)
	if err != nil || last != projectB {
		t.Fatalf("last project = %q, err = %v", last, err)
	}
	sessions, err := service.List(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, item := range sessions {
		owners[item.ID] = item.Workspace
	}
	if owners["a"] != projectA || owners["a-fork"] != projectA || owners["b"] != projectB {
		t.Fatalf("session owners = %#v", owners)
	}
}

func TestHiddenProjectLeavesOwnershipSurvivesTouchAndReappearsOnRestore(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB(), store.Blobs())
	workspace := t.TempDir()
	workspace, _ = filepath.EvalSymlinks(workspace)
	if _, err := service.Ensure(ctx, Session{ID: "owned", Title: "Owned"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "owned", Block{Kind: "user", Content: "owned"}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetWorkspaceSession(ctx, workspace, "owned"); err != nil {
		t.Fatal(err)
	}
	if err := service.HideProject(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	projects, err := service.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("hidden projects = %#v", projects)
	}
	sessions, err := service.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Workspace != workspace {
		t.Fatalf("hidden project changed session ownership = %#v", sessions)
	}
	if err := service.TouchProject(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	projects, err = service.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("startup touch restored hidden projects = %#v", projects)
	}
	if err := service.RestoreProject(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	projects, err = service.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Workspace != workspace {
		t.Fatalf("explicitly reopened projects = %#v", projects)
	}
}

func TestHiddenProjectSurvivesDatabaseReopenAndStartupTouch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "projects.db")
	workspace := t.TempDir()
	workspace, _ = filepath.EvalSymlinks(workspace)
	store, err := sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store.DB(), store.Blobs())
	if err := service.TouchProject(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if err := service.HideProject(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	service = NewService(reopened.DB(), reopened.Blobs())
	if err := service.TouchProject(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	projects, err := service.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("restart restored hidden project = %#v", projects)
	}
	if err := service.RestoreProject(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	projects, err = service.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Workspace != workspace {
		t.Fatalf("explicit reopen did not restore project = %#v", projects)
	}
}
