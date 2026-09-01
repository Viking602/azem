package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/tool"
)

func TestTodoDriverReturnsStableIDsAndAdvancesCurrentItem(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "todo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session-1", Title: "Todo"}); err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 2)
	driver := &todoDriver{sessionID: "session-1", store: sessions, emit: func(event Event) bool {
		events <- event
		return true
	}}

	initResult, err := driver.Execute(ctx, tool.Call{ID: "init", Name: "todo", Arguments: json.RawMessage(`{
		"op":"init","goal":"ship","phases":[{"title":"Build","items":[{"content":"first"},{"content":"second"}]}]
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
	reinit, err := driver.Execute(ctx, tool.Call{ID: "reinit", Name: "todo", Arguments: json.RawMessage(`{
		"op":"init","goal":"replace","phases":[{"title":"Other","items":[{"content":"replacement"}]}]
	}`)}, nil)
	if err != nil || reinit.IsError {
		t.Fatalf("implicit reinit result=%+v err=%v", reinit, err)
	}
	var replaced session.TodoList
	if err := json.Unmarshal([]byte(reinit.Content), &replaced); err != nil {
		t.Fatal(err)
	}
	if replaced.Revision != done.Revision+1 || replaced.Goal != "replace" || len(replaced.Phases) != 1 || replaced.Phases[0].Items[0].Content != "replacement" {
		t.Fatalf("implicit reinit todo=%+v", replaced)
	}
}

func TestTodoDriverInfersInitFromUnambiguousPayload(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session-1", Title: "Todo"}); err != nil {
		t.Fatal(err)
	}
	driver := &todoDriver{sessionID: "session-1", store: sessions}
	result, err := driver.Execute(ctx, tool.Call{ID: "implicit-init", Name: "todo", Arguments: json.RawMessage(`{
		"goal":"ship","phases":[{"title":"Build","items":[{"content":"first"}]}]
	}`)}, nil)
	if err != nil || result.IsError {
		t.Fatalf("implicit init result=%+v err=%v", result, err)
	}
	var todo session.TodoList
	if err := json.Unmarshal(result.Structured, &todo); err != nil {
		t.Fatal(err)
	}
	if todo.Revision != 1 || todo.Goal != "ship" || len(todo.Phases) != 1 ||
		len(todo.Phases[0].Items) != 1 || todo.Phases[0].Items[0].Status != session.TodoInProgress {
		t.Fatalf("implicit init todo=%+v", todo)
	}
}

func TestTodoDriverRequiresRevisionAfterInit(t *testing.T) {
	driver := &todoDriver{}
	result, err := driver.Execute(context.Background(), tool.Call{ID: "done", Name: "todo", Arguments: json.RawMessage(`{"op":"done","item_id":"item-1"}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Content != "expected_revision is required for done" {
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

func TestTodoDefinitionKeepsInitIdentityHostOwned(t *testing.T) {
	definition := (&todoDriver{}).Definition()
	phases := definition.InputSchema.Properties["phases"]
	if phases.Items == nil {
		t.Fatal("Todo phases schema has no item definition")
	}
	phaseSchema := *phases.Items
	itemList := phaseSchema.Properties["items"]
	if itemList.Items == nil {
		t.Fatal("Todo items schema has no item definition")
	}
	itemSchema := *itemList.Items
	if _, exposed := phaseSchema.Properties["id"]; exposed {
		t.Fatal("Todo init exposes caller-owned phase IDs")
	}
	for _, property := range []string{"id", "status"} {
		if _, exposed := itemSchema.Properties[property]; exposed {
			t.Fatalf("Todo init exposes caller-owned item %s", property)
		}
	}
	if !strings.Contains(definition.Description, "IDs and status are host-assigned") {
		t.Fatalf("Todo description omits host ownership: %q", definition.Description)
	}
	for _, required := range definition.InputSchema.Required {
		if required == "op" {
			t.Fatal("Todo init schema still requires the inferred op discriminator")
		}
	}
}

func TestTodoInitRejectsCallerOwnedIdentityAndStatus(t *testing.T) {
	tests := []struct {
		name   string
		phases []session.TodoPhase
		want   string
	}{
		{
			name: "phase ID",
			phases: []session.TodoPhase{{
				ID: "phase", Title: "Build", Items: []session.TodoItem{{Content: "work"}},
			}},
			want: "todo phase IDs are host-assigned",
		},
		{
			name: "item ID",
			phases: []session.TodoPhase{{
				Title: "Build", Items: []session.TodoItem{{ID: "item", Content: "work"}},
			}},
			want: "todo item IDs are host-assigned",
		},
		{
			name: "item status",
			phases: []session.TodoPhase{{
				Title: "Build", Items: []session.TodoItem{{Content: "work", Status: session.TodoCompleted}},
			}},
			want: "todo item status is host-assigned",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := applyTodoOp(&session.TodoList{}, todoInput{Op: "init", Goal: "ship", Phases: test.phases})
			if err == nil || err.Error() != test.want {
				t.Fatalf("init error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestTodoStartCannotReplaceCurrentItem(t *testing.T) {
	todo := session.TodoList{Phases: []session.TodoPhase{{Title: "Build", Items: []session.TodoItem{
		{ID: "current", Content: "finish current", Status: session.TodoInProgress},
		{ID: "next", Content: "start later", Status: session.TodoPending},
	}}}}
	want := todo.Clone()

	err := applyTodoOp(&todo, todoInput{Op: "start", ItemID: "next"})
	if err == nil || err.Error() != `todo item "current" is already in progress` {
		t.Fatalf("start while another item is current error=%v", err)
	}
	if !reflect.DeepEqual(todo, want) {
		t.Fatalf("rejected start changed todo: got=%+v want=%+v", todo, want)
	}
}

func TestTodoConcurrentMutationsCannotSkipCurrentItem(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "todo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session-1", Title: "Todo"}); err != nil {
		t.Fatal(err)
	}
	driver := &todoDriver{sessionID: "session-1", store: sessions}
	initialized, err := sessions.UpdateTodo(ctx, "session-1", 0, func(todo *session.TodoList) error {
		todo.Goal = "ship"
		todo.Phases = []session.TodoPhase{{Title: "Build", Items: []session.TodoItem{
			{ID: "current", Content: "finish current", Status: session.TodoInProgress},
			{ID: "next", Content: "finish next", Status: session.TodoPending},
			{ID: "later", Content: "start later", Status: session.TodoPending},
		}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := func(op, itemID string) json.RawMessage {
		encoded, _ := json.Marshal(map[string]any{"op": op, "expected_revision": initialized.Revision, "item_id": itemID})
		return encoded
	}
	results, err := tool.NewBus(driver).ExecuteBatch(ctx, []tool.Call{
		{ID: "done-current", Name: "todo", Arguments: arguments("done", "current")},
		{ID: "done-next", Name: "todo", Arguments: arguments("done", "next")},
		{ID: "start-later", Name: "todo", Arguments: arguments("start", "later")},
	}, tool.ModeParallel, tool.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].IsError || !results[1].IsError || !results[2].IsError {
		t.Fatalf("parallel todo results=%+v", results)
	}
	got, err := sessions.LoadTodo(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	items := got.Phases[0].Items
	if got.Revision != initialized.Revision+1 || items[0].Status != session.TodoCompleted || items[1].Status != session.TodoInProgress || items[2].Status != session.TodoPending {
		t.Fatalf("parallel todo state=%+v", got)
	}
}

func TestCompactRejectsShortSessionWithoutReportingFalseSuccess(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
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
	if err := service.ExecuteAction(ctx, Action{Kind: ActionCompact, Target: "session-1"}); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("short compact error = %v", err)
	}
	if todo, err := sessions.LoadTodo(ctx, "session-1"); err != nil || todo.Revision != initialized.Revision || todo.Goal != initialized.Goal {
		t.Fatalf("short compact changed todo=%+v error=%v", todo, err)
	}
}

func TestCompactRejectsWhenContextArchivingIsDisabled(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	cfg := config.Default()
	cfg.Agents.Context.Enabled = false
	service := NewService(ctx, cfg)
	service.sessions = session.NewService(store.DB(), store.Blobs())

	if err := service.ExecuteAction(ctx, Action{Kind: ActionCompact, Target: "session-1"}); !errors.Is(err, ErrContextArchivingDisabled) {
		t.Fatalf("disabled compact error = %v", err)
	}
}
