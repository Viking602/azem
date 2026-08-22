package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
	cursordriver "github.com/Viking602/azem/internal/provider/cursor"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

func TestHashlineHeaderAndCount(t *testing.T) {
	header, count := hashlineHeaderAndCount("¶note.txt#ABCD\n1:alpha\n2:beta\n")
	if header != "¶note.txt#ABCD" || count != 2 {
		t.Fatalf("header=%q count=%d", header, count)
	}
	if got, n := hashlineHeaderAndCount("plain"); got != "" || n != 0 {
		t.Fatalf("plain header=%q count=%d", got, n)
	}
}

func TestHashlineOverwritePatch(t *testing.T) {
	got := hashlineOverwritePatch("¶note.txt#ABCD", 2, "new\nline")
	if !strings.Contains(got, "replace 1..2:") || !strings.Contains(got, "+new\n+line\n") {
		t.Fatalf("patch = %q", got)
	}
}

func TestRewriteExistingWriteSkipsMissingFile(t *testing.T) {
	host := &cursorExecHost{workspace: t.TempDir(), bus: tool.NewBus()}
	args, _ := json.Marshal(map[string]string{"path": "missing.txt", "content": "new"})
	if _, ok := host.rewriteExistingWrite(context.Background(), tool.Call{ID: "w1", Name: coding.ToolWriteFile, Arguments: args}); ok {
		t.Fatal("missing file must stay write_file")
	}
}

func TestCursorExecHostUnavailableWithoutBus(t *testing.T) {
	result, err := (*cursorExecHost)(nil).Execute(context.Background(), message.ToolCall{Name: coding.ToolReadFile})
	if err != nil || !result.IsError || result.Content != "Tool not available" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

type cursorApprovalResultDriver struct{}

func (cursorApprovalResultDriver) Definition() tool.Definition {
	return tool.Definition{Name: "coding.approval_test", EffectType: tool.EffectWrite}
}

func (cursorApprovalResultDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Denied by user", IsError: true}, nil
}

func TestCursorExecHostReturnsGovernedApprovalResult(t *testing.T) {
	host := &cursorExecHost{bus: tool.NewBus(cursorApprovalResultDriver{})}
	result, err := host.Execute(context.Background(), message.ToolCall{ID: "write-1", Name: "coding.approval_test"})
	if err != nil || !result.IsError || result.Content != "Denied by user" {
		t.Fatalf("approval result=%+v err=%v", result, err)
	}
}

type cursorTimelineDriver struct{}

func (cursorTimelineDriver) Definition() tool.Definition {
	return tool.Definition{Name: "coding.timeline_test", EffectType: tool.EffectReadOnly}
}

func (cursorTimelineDriver) Execute(_ context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	if sink != nil {
		if err := sink(tool.Update{Kind: "running"}); err != nil {
			return tool.Result{}, err
		}
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "timeline result"}, nil
}

func TestCursorExecHostPersistsNativeToolTimeline(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "cursor-timeline.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Cursor timeline"}); err != nil {
		t.Fatal(err)
	}
	events := NewService(ctx, config.Default())
	events.sessions = sessions
	host := newCursorExecHost(events, t.TempDir(), "session", "child-run", "parent-run", "agent-1", tool.NewBus(cursorTimelineDriver{}))
	result, err := host.Execute(ctx, message.ToolCall{ID: "native-1", Name: "coding.timeline_test"})
	if err != nil || result.IsError || result.Content != "timeline result" {
		t.Fatalf("native result=%+v err=%v", result, err)
	}
	projection, err := sessions.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.ToolRecords) != 1 || projection.ToolRecords[0].RunID != "child-run" ||
		projection.ToolRecords[0].ToolCallID != "native-1" || projection.ToolRecords[0].State != session.ToolCompleted {
		t.Fatalf("native Cursor timeline=%+v", projection.ToolRecords)
	}
}

func TestCursorExecHostSyncsServerConfirmedTodoSnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "cursor-todo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Cursor Todo"}); err != nil {
		t.Fatal(err)
	}
	events := NewService(ctx, config.Default())
	events.sessions = sessions
	host := newCursorExecHost(events, t.TempDir(), "session", "run", "run", "", tool.NewBus())
	result := host.syncTodos(ctx, cursordriver.TodoSnapshot{Merged: true, Items: []cursordriver.TodoSnapshotItem{{
		ID: "todo-1", Content: "verify cache", Status: "in_progress",
	}}}, "todo-call", "")
	if result.IsError {
		t.Fatalf("Todo sync result=%+v", result)
	}
	todo, err := sessions.LoadTodo(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if todo.Goal != "Cursor task plan" || len(todo.Phases) != 1 || len(todo.Phases[0].Items) != 1 ||
		todo.Phases[0].Items[0].Status != session.TodoInProgress {
		t.Fatalf("synced Cursor Todo=%+v", todo)
	}
}

func TestWithCursorExecHostClonesRequestBody(t *testing.T) {
	base := map[string]any{"prompt_cache_key": "stable"}
	host := &cursorExecHost{bus: tool.NewBus()}
	bound := withCursorExecHost(base, host)
	if base[cursordriver.ExecHostExtraKey] != nil || bound[cursordriver.ExecHostExtraKey] != host ||
		bound[cursordriver.TodoSyncExtraKey] == nil || bound["prompt_cache_key"] != "stable" {
		t.Fatalf("base=%v bound=%v", base, bound)
	}
}
