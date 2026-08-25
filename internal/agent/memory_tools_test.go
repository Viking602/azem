package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/resource"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/tool"
)

func TestModelMemoryToolsRetainRecallReadEditAndInvalidate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	memories := memory.NewService(store.DB(), root)
	router := resource.NewRouter(resource.DefaultMaxReadBytes)
	service, err := NewService(store, root, WithMemory(memories), WithResourceRouter(router))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)

	retain := findWorkspaceTool(t, service, root, ToolRetain)
	callerCtx := tool.WithCaller(ctx, tool.CallerInfo{SessionID: "session-memory"})
	retained, err := retain.Execute(callerCtx, tool.Call{ID: "retain-1", Name: ToolRetain, Arguments: json.RawMessage(`{"items":[{"content":"Prefer narrow behavioral checks","context":"user testing preference"}]}`)}, nil)
	if err != nil || retained.IsError || retained.Content != "1 memory stored." {
		t.Fatalf("retain = %#v, %v", retained, err)
	}
	var retainDetails memoryToolDetails
	if err := json.Unmarshal(retained.Structured, &retainDetails); err != nil || len(retainDetails.Memories) != 1 {
		t.Fatalf("retain details = %#v, %v", retainDetails, err)
	}
	id := retainDetails.Memories[0].ID
	if retainDetails.Memories[0].SessionID != "session-memory" || !strings.Contains(retainDetails.Memories[0].Content, "Source context: user testing preference") {
		t.Fatalf("retained memory = %#v", retainDetails.Memories[0])
	}

	recall := findWorkspaceTool(t, service, root, ToolRecall)
	recalled, err := recall.Execute(ctx, tool.Call{ID: "recall-1", Name: ToolRecall, Arguments: json.RawMessage(`{"query":"behavioral checks"}`)}, nil)
	if err != nil || recalled.IsError || !strings.Contains(recalled.Content, id) || !strings.Contains(recalled.Content, "untrusted historical evidence") {
		t.Fatalf("recall = %#v, %v", recalled, err)
	}

	read := findWorkspaceTool(t, service, root, coding.ToolReadFile)
	readArgs, _ := json.Marshal(map[string]any{"path": "memory://" + id})
	full, err := read.Execute(ctx, tool.Call{ID: "read-memory", Name: coding.ToolReadFile, Arguments: readArgs}, nil)
	if err != nil || full.IsError || !strings.Contains(full.Content, "Prefer narrow behavioral checks") || !strings.Contains(full.Content, "Memory: "+id) {
		t.Fatalf("memory resource read = %#v, %v", full, err)
	}

	edit := findWorkspaceTool(t, service, root, ToolMemoryEdit)
	editArgs, _ := json.Marshal(map[string]any{"op": "update", "id": id, "content": "Prefer exact behavioral smoke checks", "importance": 0.4})
	updated, err := edit.Execute(ctx, tool.Call{ID: "edit-memory", Name: ToolMemoryEdit, Arguments: editArgs}, nil)
	if err != nil || updated.IsError || !strings.Contains(updated.Content, "updated") {
		t.Fatalf("memory update = %#v, %v", updated, err)
	}
	loaded, err := memories.Get(ctx, id)
	if err != nil || loaded.Content != "Prefer exact behavioral smoke checks" || loaded.Importance != 40 {
		t.Fatalf("updated memory = %#v, %v", loaded, err)
	}

	replacement, err := memories.Remember(ctx, "Replacement guidance", "session-memory", "runtime", 90)
	if err != nil {
		t.Fatal(err)
	}
	invalidateArgs, _ := json.Marshal(map[string]any{"op": "invalidate", "id": id, "replacement_id": replacement.ID})
	invalidated, err := edit.Execute(ctx, tool.Call{ID: "invalidate-memory", Name: ToolMemoryEdit, Arguments: invalidateArgs}, nil)
	if err != nil || invalidated.IsError || !strings.Contains(invalidated.Content, replacement.ID) {
		t.Fatalf("memory invalidate = %#v, %v", invalidated, err)
	}
	if _, err := memories.Get(ctx, id); err == nil {
		t.Fatal("invalidated memory remained active")
	}
}

func TestModelMemoryToolsClipRecallAndRejectInvalidMutations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	memories := memory.NewService(store.DB(), root)
	service, err := NewService(store, root, WithMemory(memories))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)

	longContent := "needle " + strings.Repeat("x", memoryPreviewRunes+100)
	item, err := memories.Remember(ctx, longContent, "session", "runtime", 50)
	if err != nil {
		t.Fatal(err)
	}
	recall := findWorkspaceTool(t, service, root, ToolRecall)
	result, err := recall.Execute(ctx, tool.Call{ID: "recall", Name: ToolRecall, Arguments: json.RawMessage(`{"query":"needle"}`)}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "full_length:") || !strings.Contains(result.Content, "read:memory://"+item.ID) || !strings.Contains(result.Content, "…") {
		t.Fatalf("clipped recall = %#v, %v", result, err)
	}

	retain := findWorkspaceTool(t, service, root, ToolRetain)
	invalidRetain, err := retain.Execute(ctx, tool.Call{ID: "retain", Name: ToolRetain, Arguments: json.RawMessage(`{"items":[]}`)}, nil)
	if err != nil || !invalidRetain.IsError {
		t.Fatalf("empty retain = %#v, %v", invalidRetain, err)
	}
	edit := findWorkspaceTool(t, service, root, ToolMemoryEdit)
	invalidUpdate, err := edit.Execute(ctx, tool.Call{ID: "edit", Name: ToolMemoryEdit, Arguments: json.RawMessage(`{"op":"update","id":"missing","content":"replacement"}`)}, nil)
	if err != nil || invalidUpdate.IsError || !strings.Contains(invalidUpdate.Content, "not found") {
		t.Fatalf("missing update = %#v, %v", invalidUpdate, err)
	}
}
