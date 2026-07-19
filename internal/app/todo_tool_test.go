package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/go-hydaelyn/tool"
)

func TestTodoDriverReturnsStableIDsAndAdvancesCurrentItem(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "todo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session-1", Title: "Todo"}); err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 2)
	driver := &todoDriver{sessionID: "session-1", store: sessions, emit: func(event Event) bool {
		events <- event
		return true
	}}

	initResult, err := driver.Execute(ctx, tool.Call{ID: "init", Name: "todo", Arguments: json.RawMessage(`{
		"op":"init","expected_revision":0,"goal":"ship","phases":[{"title":"Build","items":[{"content":"first"},{"content":"second"}]}]
	}`)}, nil)
	if err != nil || initResult.IsError {
		t.Fatalf("init result=%+v err=%v", initResult, err)
	}
	var initialized session.TodoList
	if err := json.Unmarshal([]byte(initResult.Content), &initialized); err != nil {
		t.Fatalf("tool content does not expose snapshot: %v: %q", err, initResult.Content)
	}
	if initialized.Revision != 1 || initialized.Phases[0].ID == "" || initialized.Phases[0].Items[0].ID == "" || initialized.Phases[0].Items[0].Status != session.TodoInProgress {
		t.Fatalf("initialized todo=%+v", initialized)
	}
	if event := <-events; event.Kind != EventTodoUpdated || event.Todo == nil || event.Todo.Revision != 1 {
		t.Fatalf("todo event=%+v", event)
	}

	doneArguments, _ := json.Marshal(map[string]any{
		"op": "done", "expected_revision": initialized.Revision, "item_id": initialized.Phases[0].Items[0].ID,
	})
	doneResult, err := driver.Execute(ctx, tool.Call{ID: "done", Name: "todo", Arguments: doneArguments}, nil)
	if err != nil || doneResult.IsError {
		t.Fatalf("done result=%+v err=%v", doneResult, err)
	}
	var done session.TodoList
	if err := json.Unmarshal([]byte(doneResult.Content), &done); err != nil {
		t.Fatal(err)
	}
	if done.Phases[0].Items[0].Status != session.TodoCompleted || done.Phases[0].Items[1].Status != session.TodoInProgress {
		t.Fatalf("todo did not advance: %+v", done)
	}
}

func TestTodoDriverRequiresRevisionForMutations(t *testing.T) {
	driver := &todoDriver{}
	result, err := driver.Execute(context.Background(), tool.Call{ID: "init", Name: "todo", Arguments: json.RawMessage(`{"op":"init","goal":"ship","phases":[]}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Content != "expected_revision is required for init" {
		t.Fatalf("missing revision result=%+v", result)
	}
}

func TestTodoInitRejectsForgedSubagentBinding(t *testing.T) {
	revision := int64(0)
	err := applyTodoOp(&session.TodoList{}, todoInput{
		Op: "init", ExpectedRevision: &revision, Goal: "forge",
		Phases: []session.TodoPhase{{Title: "Build", Items: []session.TodoItem{{Content: "work", SubagentRunID: "fake-run"}}}},
	})
	if err == nil || err.Error() != "subagentRunId is owned by subagent.spawn" {
		t.Fatalf("forged binding error=%v", err)
	}
}

func TestCompactEventRetainsTodoSnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session-1", Title: "Todo"}); err != nil {
		t.Fatal(err)
	}
	initialized, err := sessions.UpdateTodo(ctx, "session-1", 0, func(todo *session.TodoList) error {
		todo.Goal = "survive compact"
		todo.Phases = []session.TodoPhase{{Title: "Build", Items: []session.TodoItem{{Content: "verify", Status: session.TodoInProgress}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.sessions = sessions
	service.activeRun = "run-1"
	service.activeSession = "session-1"
	if err := service.ExecuteAction(ctx, Action{Kind: ActionCompact, Target: "session-1"}); !errors.Is(err, ErrRunActive) {
		t.Fatalf("compact during active run error = %v", err)
	}
	service.activeRun = ""
	service.activeSession = ""
	if err := service.ExecuteAction(ctx, Action{Kind: ActionCompact, Target: "session-1"}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventSessionLoaded || event.Todo == nil || event.Todo.Revision != initialized.Revision || event.Todo.Goal != initialized.Goal {
		t.Fatalf("compact event lost todo: %+v", event)
	}
}
