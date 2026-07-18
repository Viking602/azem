package session

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestTodoRevisionStateMachineAndRecovery(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "s", Title: "todo"}); err != nil {
		t.Fatal(err)
	}
	initialized, err := service.UpdateTodo(ctx, "s", 0, func(todo *TodoList) error {
		todo.Goal = "ship"
		todo.Phases = []TodoPhase{{Title: "Build", Items: []TodoItem{{Content: "first"}, {Content: "second"}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if initialized.Revision != 1 || initialized.Phases[0].ID == "" || initialized.Phases[0].Items[0].ID == "" {
		t.Fatalf("unstable initialized todo: %+v", initialized)
	}
	firstID := initialized.Phases[0].Items[0].ID
	started, err := service.UpdateTodo(ctx, "s", 1, func(todo *TodoList) error { todo.Phases[0].Items[0].Status = TodoInProgress; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.UpdateTodo(ctx, "s", 1, func(*TodoList) error { return nil }); !errors.Is(err, ErrTodoRevisionConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	done, err := service.UpdateTodo(ctx, "s", started.Revision, func(todo *TodoList) error {
		todo.Phases[0].Items[0].Status = TodoCompleted
		todo.Phases[0].Items[1].Status = TodoInProgress
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewService(store.DB()).LoadTodo(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != done.Revision || reloaded.Phases[0].Items[0].ID != firstID || reloaded.Phases[0].Items[1].Status != TodoInProgress {
		t.Fatalf("recovered todo mismatch: %+v", reloaded)
	}
}

func TestTodoValidationRejectsAmbiguousPlansAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "s", Title: "todo"}); err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateTodo(ctx, "s", 0, func(todo *TodoList) error {
		todo.Goal = "invalid"
		todo.Phases = []TodoPhase{
			{Title: "Build", Items: []TodoItem{{Content: "same"}}},
			{Title: "Verify", Items: []TodoItem{{Content: "same"}}},
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate todo item") {
		t.Fatalf("duplicate validation error=%v", err)
	}
	loaded, err := service.LoadTodo(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 0 || len(loaded.Phases) != 0 {
		t.Fatalf("invalid plan was partially persisted: %+v", loaded)
	}
}
