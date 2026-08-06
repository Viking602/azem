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
	service := NewService(store.DB())
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
