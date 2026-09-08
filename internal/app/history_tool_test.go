package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/tool"
)

func TestHistoryToolIsExplicitAndSessionScoped(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	for _, id := range []string{"one", "two"} {
		if _, err := sessions.Ensure(ctx, session.Session{ID: id}); err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.AppendBlock(ctx, id, session.Block{Kind: "user", Content: "needle " + id}); err != nil {
			t.Fatal(err)
		}
	}
	driver := historyDriver{sessions: sessions, sessionID: "one"}
	call := tool.Call{ID: "lookup", Name: "context.search_history", Arguments: json.RawMessage(`{"query":"needle"}`)}
	result, err := driver.Execute(ctx, call, nil)
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	var records []session.HistoryRecord
	if err := json.Unmarshal([]byte(result.Content), &records); err != nil || len(records) != 1 || records[0].Content != "needle one" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	result, err = driver.Execute(cancelled, call, nil)
	if err != nil || !result.IsError {
		t.Fatalf("cancel swallowed: %+v %v", result, err)
	}
	call.Arguments = json.RawMessage(`{"query":""}`)
	result, err = driver.Execute(ctx, call, nil)
	if err != nil || !result.IsError {
		t.Fatalf("empty query accepted: %+v %v", result, err)
	}
}

func TestTurnAdmissionDoesNotSearchLongHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "admission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 5000; i++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO context_artifacts(id,session_id,kind,sha256,preview,created_at) VALUES(?,'s','tool_result',?,'needle',0)`, fmt.Sprint(i), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	start := time.Now()
	id, err := service.StartConfiguredTurn(TurnRequest{SessionID: "s", Prompt: "needle"})
	if err != nil || id == "" {
		t.Fatalf("admission: %s %v", id, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("admission waited for optional history: %s", elapsed)
	}
}

func TestProviderHistoryRecallIsLazy(t *testing.T) {
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: demo\n---\ndemo\n", nil, func(call int, body string, w http.ResponseWriter) {
		switch call {
		case 1:
			if strings.Contains(body, "ARCHIVED_NEEDLE_PAYLOAD") {
				t.Error("history injected before explicit lookup")
			}
			writeProviderToolCall(w, "lookup-1", "history-1", "context_search_history", `{"query":"needle"}`)
		case 2:
			if !strings.Contains(body, "ARCHIVED_NEEDLE_PAYLOAD") {
				t.Error("explicit lookup did not return history")
			}
			writeProviderText(w, "lookup-2", "History retrieved.")
		default:
			t.Errorf("unexpected call %d", call)
			writeProviderText(w, "end", "Done.")
		}
	})
	ctx := context.Background()
	if _, err := harness.service.sessions.Ensure(ctx, session.Session{ID: "history-lazy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.sessions.PutArtifact(ctx, "history-lazy", "old", "tool_result", []byte("body"), "needle ARCHIVED_NEEDLE_PAYLOAD"); err != nil {
		t.Fatal(err)
	}
	id, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "history-lazy", Prompt: "needle", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, id)
	if harness.calls.Load() != 2 {
		t.Fatalf("calls=%d", harness.calls.Load())
	}
}
