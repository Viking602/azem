package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"

	"github.com/Viking602/azem/internal/session"
)

func TestIncompleteTodoItemsKeepsOpenWork(t *testing.T) {
	todo := session.TodoList{Phases: []session.TodoPhase{{
		ID: "p2", Title: "实现",
		Items: []session.TodoItem{
			{ID: "i1", Content: "定位现有 Plan 组件", Status: session.TodoCompleted},
			{ID: "i2", Content: "按截图重写 Plan 卡片 UI", Status: session.TodoInProgress},
			{ID: "i3", Content: "构建/渲染验证改动", Status: session.TodoPending},
			{ID: "i4", Content: "已取消", Status: session.TodoCancelled},
		},
	}}}
	items := incompleteTodoItems(todo)
	if len(items) != 2 || items[0].ID != "i2" || items[1].ID != "i3" {
		t.Fatalf("incomplete = %#v", items)
	}
	message := unfinishedTodoRetryMessage(items)
	if !strings.Contains(message, "You may not finish") || !strings.Contains(message, "in_progress: 按截图重写 Plan 卡片 UI") || !strings.Contains(message, "pending: 构建/渲染验证改动") {
		t.Fatalf("retry message = %q", message)
	}
	if strings.Contains(message, "定位现有 Plan") || strings.Contains(message, "已取消") {
		t.Fatalf("retry listed terminal items: %q", message)
	}
}

func TestUnfinishedTodosBlockFinishWithoutFileMutation(t *testing.T) {
	// Finish is blocked by open todos, not by whether this run mutated files.
	todo := session.TodoList{Phases: []session.TodoPhase{{
		Items: []session.TodoItem{{ID: "i2", Content: "按截图重写 Plan 卡片 UI", Status: session.TodoInProgress}},
	}}}
	if items := incompleteTodoItems(todo); len(items) != 1 {
		t.Fatalf("incomplete = %#v", items)
	}
}

func TestSessionTodoGuardDoesNotBlockSubagentCompletion(t *testing.T) {
	todo := session.TodoList{Phases: []session.TodoPhase{{
		Items: []session.TodoItem{{ID: "parent-item", Content: "Wait for delegated inspection", Status: session.TodoInProgress}},
	}}}
	if items := guardrailTodoItems(todo, true); len(items) != 1 {
		t.Fatalf("parent guard items = %#v", items)
	}
	if items := guardrailTodoItems(todo, false); len(items) != 0 {
		t.Fatalf("Subagent inherited parent Todo guard = %#v", items)
	}
}

func TestIncompleteTodoItemsEmptyWhenDone(t *testing.T) {
	todo := session.TodoList{Phases: []session.TodoPhase{{
		Items: []session.TodoItem{{ID: "i1", Content: "done", Status: session.TodoCompleted}},
	}}}
	if items := incompleteTodoItems(todo); len(items) != 0 {
		t.Fatalf("incomplete = %#v", items)
	}
	if items := incompleteTodoItems(session.TodoList{}); len(items) != 0 {
		t.Fatalf("empty todo incomplete = %#v", items)
	}
}

func TestSurfaceVerificationKeepsModelAnswer(t *testing.T) {
	answer := message.NewText(message.RoleAssistant, "修复完成，验证通过。")
	result := surfaceVerificationOutput(answer, "uncertain")
	if result.Action != hyagent.OutputGuardrailActionReplace || result.Replacement == nil {
		t.Fatalf("action = %#v", result)
	}
	text := result.Replacement.Text
	if !strings.Contains(text, "修复完成，验证通过。") {
		t.Fatalf("model answer dropped: %q", text)
	}
	if !strings.Contains(text, "Verification evidence is missing or stale") {
		t.Fatalf("uncertain notice missing: %q", text)
	}
}

func TestSurfaceVerificationFallsBackToNoticeWithoutAnswer(t *testing.T) {
	result := surfaceVerificationOutput(message.Message{}, "fail")
	text := result.Replacement.Text
	if !strings.HasPrefix(text, "Verification failed") || strings.Contains(text, "missing or stale") {
		t.Fatalf("fail notice = %q", text)
	}
}

func TestDedicatedGofmtSatisfiesFormatterVerification(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	files := []session.WorkRevisionFileV1{
		{Path: "internal/app/work_evidence.go", SHA256: "sha-work", Touched: true},
		{Path: "internal/app/work_evidence_guardrail_test.go", SHA256: "sha-test", Touched: true},
	}
	record := func(id, path, sha string, completedAt time.Time) session.ToolRecord {
		structured, err := json.Marshal(map[string]any{"path": path, "changed": true})
		if err != nil {
			t.Fatal(err)
		}
		return session.ToolRecord{
			RunID: "run", ToolCallID: id, Name: "coding.gofmt", State: session.ToolCompleted,
			Structured: structured, StartedAt: completedAt.Add(-time.Second), CompletedAt: completedAt,
			Observations: []session.FileObservation{{Path: path, Operation: "format", SHA256: sha}},
		}
	}
	snapshot := runtimeEvidenceSnapshot{
		revision: session.WorkRevisionV1{Files: files},
		plan: session.VerificationPlanV1{Checks: []session.VerificationCheckV1{{
			ID: "gofmt", Kind: "command", Command: []string{
				"gofmt", "-d", "internal/app/work_evidence.go", "internal/app/work_evidence_guardrail_test.go",
			},
		}}},
		records: []session.ToolRecord{
			record("format-work", files[0].Path, files[0].SHA256, now),
			record("format-test", files[1].Path, files[1].SHA256, now.Add(time.Second)),
		},
		latestMutationAt: now.Add(time.Second),
	}
	state := evaluateRuntimeChecks(snapshot)
	if state.status != "pass" || len(state.missing) != 0 || len(state.evidence) != 2 {
		t.Fatalf("dedicated gofmt evidence = %+v", state)
	}
}

func TestUnchangedGofmtDoesNotAdvanceMutationBoundary(t *testing.T) {
	root := t.TempDir()
	path := "internal/app/work_evidence.go"
	absolute := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("package app\n")
	if err := os.WriteFile(absolute, content, 0o644); err != nil {
		t.Fatal(err)
	}
	mutatedAt := time.Unix(1_700_000_000, 0).UTC()
	unchangedAt := mutatedAt.Add(time.Minute)
	records := []session.ToolRecord{
		{
			RunID: "run", ToolCallID: "edit", Name: "coding.edit_hashline", State: session.ToolCompleted,
			CompletedAt: mutatedAt, Observations: []session.FileObservation{{Path: path, Operation: "edit"}},
		},
		{
			RunID: "run", ToolCallID: "format", Name: "coding.gofmt", State: session.ToolCompleted,
			Structured: json.RawMessage(`{"path":"internal/app/work_evidence.go","changed":false}`),
			Content:    "already formatted internal/app/work_evidence.go", CompletedAt: unchangedAt,
			Observations: []session.FileObservation{{Path: path, Operation: "format"}},
		},
	}
	files, mutating, latestMutationAt, captureErrors := runtimeRevisionFiles(root, records)
	if !mutating || !latestMutationAt.Equal(mutatedAt) || len(captureErrors) != 0 {
		t.Fatalf("mutation boundary=%v mutating=%v errors=%v", latestMutationAt, mutating, captureErrors)
	}
	if len(files) != 1 || !files[0].Touched || !files[0].Observed {
		t.Fatalf("revision files=%+v", files)
	}
}
