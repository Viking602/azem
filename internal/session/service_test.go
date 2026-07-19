package session

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/go-hydaelyn/message"
)

func TestCompactSummarizesOlderBlocksAndKeepsRecent(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test", ProviderID: "chatgpt", ModelID: "model", Reasoning: "high", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		kind := "user"
		if index%2 == 1 {
			kind = "assistant"
		}
		if err := service.AppendBlock(ctx, "session", Block{Kind: kind, RunID: fmt.Sprintf("run-%d", index), Title: kind, Content: fmt.Sprintf("message %d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	projection, err := service.Compact(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 5 || projection.Blocks[0].State != "compacted" || !projection.Blocks[0].Collapsed {
		t.Fatalf("compacted blocks=%+v", projection.Blocks)
	}
	for index, block := range projection.Blocks[1:] {
		want := fmt.Sprintf("message %d", index+4)
		if block.Content != want {
			t.Fatalf("recent block %d=%q want %q", index, block.Content, want)
		}
	}
	reloaded, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Blocks) != 5 || reloaded.Blocks[0].Content != projection.Blocks[0].Content || reloaded.LastRunID != "run-7" {
		t.Fatalf("reloaded projection=%+v", reloaded)
	}
}

func TestUpsertAgentBlockPreservesLifecyclePosition(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test", ProviderID: "chatgpt", ModelID: "model", Reasoning: "high", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "parent", Content: "delegate"}); err != nil {
		t.Fatal(err)
	}
	queued := Block{
		Kind: "agent", RunID: "parent", AgentID: "child", ParentToolCallID: "spawn-call",
		Title: "explore", Content: "queued", State: "queued",
	}
	if err := service.UpsertAgentBlock(ctx, "session", "child", queued); err != nil {
		t.Fatal(err)
	}
	running := queued
	running.Content = "reading go.mod"
	running.State = "running"
	if err := service.UpsertAgentBlock(ctx, "session", "child", running); err != nil {
		t.Fatal(err)
	}
	completed := running
	completed.Content = "done"
	completed.State = "completed"
	if err := service.UpsertAgentBlock(ctx, "session", "child", completed); err != nil {
		t.Fatal(err)
	}

	projection, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 2 || projection.Blocks[0].Kind != "user" {
		t.Fatalf("projection order = %#v", projection.Blocks)
	}
	got := projection.Blocks[1]
	if got.AgentID != "child" || got.ParentToolCallID != "spawn-call" || got.State != "completed" || got.Content != "done" {
		t.Fatalf("agent block = %#v", got)
	}
}

func TestAgentBlockUpsertAfterCompactionDoesNotDuplicate(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "agent-compaction.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test", ProviderID: "chatgpt", ModelID: "model", Reasoning: "high", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	agent := Block{
		Kind: "agent", RunID: "parent", AgentID: "child", ParentToolCallID: "spawn-call",
		Title: "explore", Content: "running", State: "running",
	}
	if err := service.UpsertAgentBlock(ctx, "session", agent.AgentID, agent); err != nil {
		t.Fatal(err)
	}
	for index := range 6 {
		if err := service.AppendBlock(ctx, "session", Block{
			Kind: "assistant", RunID: fmt.Sprintf("run-%d", index), Content: fmt.Sprintf("message %d", index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Compact(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	agent.Content = "done"
	agent.State = "completed"
	if err := service.UpsertAgentBlock(ctx, "session", agent.AgentID, agent); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertAgentBlock(ctx, "session", agent.AgentID, agent); err != nil {
		t.Fatal(err)
	}
	projection, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, block := range projection.Blocks {
		if block.Kind == "agent" && block.AgentID == agent.AgentID {
			count++
			if block.State != "completed" || block.Content != "done" {
				t.Fatalf("reloaded agent block = %#v", block)
			}
		}
	}
	if count != 1 {
		t.Fatalf("agent block count after compaction = %d: %#v", count, projection.Blocks)
	}
}

func TestCompleteTurnStoresAssistantBlockAndModelHistoryTogether(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "completion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test", ProviderID: "chatgpt", ModelID: "model"}); err != nil {
		t.Fatal(err)
	}
	projection, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if projection.ModelHistory.ProviderID != "" || len(projection.ModelHistory.Messages) != 0 {
		t.Fatalf("new projection history = %#v", projection.ModelHistory)
	}
	if err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-1", Content: "request"}); err != nil {
		t.Fatal(err)
	}
	history := ModelHistory{
		ProviderID: "chatgpt", ModelID: "gpt-test", InstructionFingerprint: "fingerprint",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "rules"),
			message.NewText(message.RoleUser, "request"),
			message.NewText(message.RoleAssistant, "answer"),
		},
	}
	if err := service.CompleteTurn(ctx, "session", Block{Kind: "assistant", RunID: "run-1", Content: "answer"}, history); err != nil {
		t.Fatal(err)
	}
	projection, err = service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 2 || projection.Blocks[1].Content != "answer" || projection.LastRunID != "run-1" {
		t.Fatalf("completed blocks = %#v", projection.Blocks)
	}
	got := projection.ModelHistory
	if got.ProviderID != history.ProviderID || got.ModelID != history.ModelID ||
		got.InstructionFingerprint != history.InstructionFingerprint || len(got.Messages) != 3 ||
		got.Messages[2].Role != message.RoleAssistant || got.Messages[2].Text != "answer" {
		t.Fatalf("completed model history = %#v", got)
	}
	compacted, err := service.Compact(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if compacted.ModelHistory.ProviderID != "" || len(compacted.ModelHistory.Messages) != 0 {
		t.Fatalf("compact retained provider model history = %#v", compacted.ModelHistory)
	}
}

func TestSessionBlocksUseRowsWithoutRewritingProjectionJSON(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "blocks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Rows"}); err != nil {
		t.Fatal(err)
	}
	for _, block := range []Block{
		{Kind: "user", RunID: "run-1", Content: "request"},
		{Kind: "assistant", RunID: "run-1", Content: "first "},
		{Kind: "assistant", RunID: "run-1", Content: "second"},
	} {
		if err := service.AppendBlock(ctx, "session", block); err != nil {
			t.Fatal(err)
		}
	}
	var legacy string
	var rowCount int
	if err := store.DB().QueryRowContext(ctx, `SELECT CAST(blocks AS TEXT) FROM session_projections WHERE session_id='session'`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM session_blocks WHERE session_id='session'`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	projection, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if legacy != "[]" || rowCount != 2 || len(projection.Blocks) != 2 || projection.Blocks[1].Content != "first second" {
		t.Fatalf("row projection legacy=%q rows=%d blocks=%#v", legacy, rowCount, projection.Blocks)
	}
}

func TestBlockMutationsInvalidateExactProviderHistory(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "History"}); err != nil {
		t.Fatal(err)
	}
	history := ModelHistory{ProviderID: "chatgpt", ModelID: "model", Messages: []message.Message{message.NewText(message.RoleAssistant, "raw")}}
	if err := service.CompleteTurn(ctx, "session", Block{Kind: "assistant", RunID: "run-1", Content: "answer"}, history); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertAgentBlock(ctx, "session", "child", Block{RunID: "run-1", Content: "child state"}); err != nil {
		t.Fatal(err)
	}
	projection, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.ModelHistory.Messages) != 0 {
		t.Fatalf("agent mutation retained stale provider history = %#v", projection.ModelHistory)
	}
	if err := service.CompleteTurn(ctx, "session", Block{Kind: "assistant", RunID: "run-2", Content: "next"}, history); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-3", Content: "failed turn"}); err != nil {
		t.Fatal(err)
	}
	projection, err = service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.ModelHistory.Messages) != 0 {
		t.Fatalf("append retained stale provider history = %#v", projection.ModelHistory)
	}
}

func TestCompleteTurnRollsBackBlockAndModelHistoryOnSessionUpdateFailure(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "completion-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test", ProviderID: "chatgpt", ModelID: "model"}); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-1", Content: "request"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `CREATE TRIGGER fail_session_update
		BEFORE UPDATE ON sessions BEGIN SELECT RAISE(FAIL, 'injected session update failure'); END`); err != nil {
		t.Fatal(err)
	}
	err = service.CompleteTurn(ctx, "session", Block{Kind: "assistant", RunID: "run-1", Content: "answer"}, ModelHistory{
		ProviderID: "chatgpt", ModelID: "gpt-test", InstructionFingerprint: "fingerprint",
		Messages: []message.Message{message.NewText(message.RoleAssistant, "answer")},
	})
	if err == nil {
		t.Fatal("completion unexpectedly succeeded")
	}
	projection, loadErr := service.LoadProjection(ctx, "session")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(projection.Blocks) != 1 || projection.Blocks[0].Kind != "user" ||
		projection.LastRunID != "run-1" || len(projection.ModelHistory.Messages) != 0 {
		t.Fatalf("rolled back projection = %#v", projection)
	}
}
