package session

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/go-hydaelyn/message"
)

func TestPhase3ArtifactRoundTripAfterReopenAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "artifacts.db")
	store, err := sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test"}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0, 1, 2, 255}, 512*1024)
	first, err := service.PutArtifact(ctx, "session", "run", "tool_result", payload, "preview")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PutArtifact(ctx, "session", "other-run", "tool_result", payload, "other")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("dedup ids %q != %q", first.ID, second.ID)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	store, err = sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	loaded, err := NewService(store.DB()).LoadArtifact(ctx, "session", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.Payload, payload) {
		t.Fatalf("round trip bytes=%d want=%d", len(loaded.Payload), len(payload))
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM context_artifacts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestPhase6SearchHistoryIsolationSafetyBudgetsAndProvenance(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "history.db")
	store, err := sqlitestore.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store.DB())
	for _, id := range []string{"one", "two"} {
		if _, err := service.Ensure(ctx, Session{ID: id, Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	sequence, err := service.AppendBlock(ctx, "one", Block{Kind: "user", Content: "needle canonical transcript"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "one", Block{Kind: "agent", Content: "needle mutable agent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "one", Block{Kind: "assistant", RunID: "cancelled", Content: "needle partial answer", State: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "two", Block{Kind: "user", Content: "needle other session"}); err != nil {
		t.Fatal(err)
	}
	artifact, err := service.PutArtifact(ctx, "one", "run", "tool_result", bytes.Repeat([]byte("SECRET"), 1000), "needle artifact preview")
	if err != nil {
		t.Fatal(err)
	}
	items, err := service.SearchHistory(ctx, "one", `needle " OR ( ) : * -`, 1000, 4096, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("search results=%+v", items)
	}
	wantSources := map[string]bool{fmt.Sprintf("sequence:%d", sequence): false, "artifact:" + artifact.ID: false}
	for _, item := range items {
		if item.SessionID != "one" {
			t.Fatalf("cross-session result: %+v", item)
		}
		if _, ok := wantSources[item.SourceID]; !ok {
			t.Fatalf("fabricated or mutable source: %+v", item)
		}
		wantSources[item.SourceID] = true
		if strings.Contains(item.Preview, "SECRET") {
			t.Fatal("artifact payload leaked into search result")
		}
	}
	for source, found := range wantSources {
		if !found {
			t.Fatalf("missing source %s", source)
		}
	}
	bounded, err := service.SearchHistory(ctx, "one", "needle", 1000, 1, 3)
	if err != nil || len(bounded) > 20 {
		t.Fatalf("bounded search len=%d err=%v", len(bounded), err)
	}
	bytesUsed := 0
	for _, item := range bounded {
		bytesUsed += len(item.Content) + len(item.Preview)
	}
	if bytesUsed > 3 {
		t.Fatalf("byte budget exceeded: %d", bytesUsed)
	}
	if empty, err := service.SearchHistory(ctx, "one", `!!! ""`, 8, 10, 40); err != nil || len(empty) != 0 {
		t.Fatalf("meaningless search=%+v err=%v", empty, err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	store, err = sqlitestore.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	if reopened, err := NewService(store.DB()).SearchHistory(ctx, "one", "needle", 8, 4096, 4096); err != nil || len(reopened) != 2 {
		t.Fatalf("reopened search=%+v err=%v", reopened, err)
	}
}

func TestPhase6HistoryTriggersUpdateDeleteAndSessionCascade(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "triggers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	sequence, err := service.AppendBlock(ctx, "s", Block{Kind: "assistant", RunID: "r", Content: "firstterm"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "s", Block{Kind: "assistant", RunID: "r", Content: " secondterm"}); err != nil {
		t.Fatal(err)
	}
	if old, _ := service.SearchHistory(ctx, "s", "firstterm", 8, 100, 400); len(old) != 1 {
		t.Fatalf("coalesced update lost old content: %+v", old)
	}
	if updated, _ := service.SearchHistory(ctx, "s", "secondterm", 8, 100, 400); len(updated) != 1 || updated[0].SourceID != fmt.Sprintf("sequence:%d", sequence) {
		t.Fatalf("coalesced update not indexed: %+v", updated)
	}
	if _, err := service.PutArtifact(ctx, "s", "r", "tool", []byte("payload"), "artifactterm"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM session_blocks WHERE session_id='s' AND sequence=?`, sequence); err != nil {
		t.Fatal(err)
	}
	if found, _ := service.SearchHistory(ctx, "s", "secondterm", 8, 100, 400); len(found) != 0 {
		t.Fatalf("deleted block remained indexed: %+v", found)
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM sessions WHERE id='s'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM history_fts WHERE session_id='s'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade left %d FTS rows: %v", count, err)
	}
}

func TestCompactWithSummaryPersistsMatchingModelHistory(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "summary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test", ProviderID: "chatgpt", ModelID: "model"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		kind := "user"
		if index%2 == 1 {
			kind = "assistant"
		}
		if _, err := service.AppendBlock(ctx, "session", Block{Kind: kind, Content: fmt.Sprintf("message %d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	summary := "[Untrusted historical record]\n## Objective\n- ship the fix"
	history := ModelHistory{
		ProviderID: "chatgpt", ModelID: "model", InstructionFingerprint: "fingerprint",
		Messages: []message.Message{message.NewText(message.RoleSystem, "rules"), message.NewText(message.RoleAssistant, summary)},
	}
	before, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	var rowsBefore string
	if err := store.DB().QueryRowContext(ctx, `SELECT json_group_array(json_object('sequence',sequence,'data',hex(data))) FROM session_blocks WHERE session_id='session' ORDER BY sequence`).Scan(&rowsBefore); err != nil {
		t.Fatal(err)
	}
	projection, err := service.CompactWithSummary(ctx, "session", CompactionPlan{
		Summary: summary, ModelHistory: history, ExpectedUpdatedAt: before.UpdatedAt, TailStart: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 8 || projection.Blocks[0].Content != "message 0" {
		t.Fatalf("compacted blocks = %#v", projection.Blocks)
	}
	if projection.ModelHistory.ProviderID != "chatgpt" || len(projection.ModelHistory.Messages) != 2 {
		t.Fatalf("compacted model history = %#v", projection.ModelHistory)
	}
	var rowsAfter string
	if err := store.DB().QueryRowContext(ctx, `SELECT json_group_array(json_object('sequence',sequence,'data',hex(data))) FROM session_blocks WHERE session_id='session' ORDER BY sequence`).Scan(&rowsAfter); err != nil {
		t.Fatal(err)
	}
	if rowsAfter != rowsBefore {
		t.Fatalf("manual compaction rewrote session blocks\nbefore=%s\nafter=%s", rowsBefore, rowsAfter)
	}
	reloaded, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Blocks[0].Content != "message 0" || reloaded.ModelHistory.InstructionFingerprint != "fingerprint" {
		t.Fatalf("reloaded compaction = %#v %#v", reloaded.Blocks, reloaded.ModelHistory)
	}
}

func TestSaveRunCheckpointPersistsHistoryWithoutCompletingTurn(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "run-checkpoint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test"}); err != nil {
		t.Fatal(err)
	}
	sequence, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-1", Content: "long task"})
	if err != nil {
		t.Fatal(err)
	}
	summary := message.NewText(message.RoleAssistant, "checkpoint summary")
	summary.Kind = message.KindCompactionSummary
	facts := message.NewText(message.RoleSystem, `{"version":1,"run_id":"run-1"}`)
	facts.Kind = message.KindCustom
	facts.Visibility = message.VisibilityPrivate
	facts.Metadata = map[string]string{"azem.context.execution_checkpoint": "1"}
	history := ModelHistory{
		ProviderID: "chatgpt", ModelID: "model", InstructionFingerprint: "instructions", StaticPrefixHash: "instructions",
		Messages: []message.Message{message.NewText(message.RoleSystem, "rules"), message.NewText(message.RoleUser, "long task"), summary, facts},
	}
	checkpoint := RunCheckpoint{RunID: "run-1", ModelHistory: history, CacheIdentity: "cache-checkpoint-1", ExpectedHighWater: &sequence}
	if err := service.SaveRunCheckpoint(ctx, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	projection, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 1 || projection.Blocks[0].Kind != "user" {
		t.Fatalf("checkpoint completed canonical turn: %#v", projection.Blocks)
	}
	if projection.ModelHistory.CoveredThroughSequence == nil || *projection.ModelHistory.CoveredThroughSequence != sequence ||
		projection.ModelHistory.SummaryHash != ModelCheckpointHash(history.Messages) || projection.CheckpointGeneration != 1 ||
		projection.CacheEpoch != 1 || projection.CacheIdentityHash != "cache-checkpoint-1" {
		t.Fatalf("persisted checkpoint=%+v projection=%+v", projection.ModelHistory, projection)
	}
	if err := service.SaveRunCheckpoint(ctx, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	repeated, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if repeated.CheckpointGeneration != projection.CheckpointGeneration || repeated.CacheEpoch != projection.CacheEpoch {
		t.Fatalf("idempotent checkpoint advanced generation: before=%+v after=%+v", projection, repeated)
	}
	finalHistory := history
	finalHistory.Messages = append(append([]message.Message(nil), history.Messages...), message.NewText(message.RoleAssistant, "final answer"))
	checkpoint.ModelHistory = finalHistory
	if err := service.SaveRunCheckpoint(ctx, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	withFinal, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if withFinal.CheckpointGeneration != repeated.CheckpointGeneration+1 || withFinal.CacheEpoch != repeated.CacheEpoch ||
		len(withFinal.ModelHistory.Messages) != len(finalHistory.Messages) || withFinal.ModelHistory.Messages[len(finalHistory.Messages)-1].Text != "final answer" {
		t.Fatalf("standard final assistant was not checkpointed: before=%+v after=%+v", repeated, withFinal)
	}
	repeated = withFinal
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-1", Content: "late guidance", State: "guidance"}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveRunCheckpoint(ctx, "session", checkpoint); err != nil {
		t.Fatalf("checkpoint with uncovered same-run guidance: %v", err)
	}
	afterStaleReplay, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if afterStaleReplay.CheckpointGeneration != repeated.CheckpointGeneration || afterStaleReplay.CacheEpoch != repeated.CacheEpoch ||
		afterStaleReplay.ModelHistory.CoveredThroughSequence == nil || *afterStaleReplay.ModelHistory.CoveredThroughSequence != sequence {
		t.Fatalf("uncovered guidance changed idempotent checkpoint: before=%+v after=%+v", repeated, afterStaleReplay)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-2", Content: "new owner"}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveRunCheckpoint(ctx, "session", checkpoint); err == nil || !strings.Contains(err.Error(), "active run changed") {
		t.Fatalf("stale run checkpoint error=%v", err)
	}
}

func TestCompleteTurnRejectsOlderRunAfterNewerRunCompleted(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "late-completion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-1", Content: "old task"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-2", Content: "new task"}); err != nil {
		t.Fatal(err)
	}
	newHistory := ModelHistory{ProviderID: "chatgpt", ModelID: "model", Messages: []message.Message{message.NewText(message.RoleAssistant, "new history")}}
	if err := service.CompleteTurn(ctx, "session", Block{Kind: "assistant", RunID: "run-2", Content: "new answer"}, newHistory); err != nil {
		t.Fatal(err)
	}
	before, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	oldHistory := ModelHistory{ProviderID: "chatgpt", ModelID: "model", Messages: []message.Message{message.NewText(message.RoleAssistant, "old history")}}
	if err := service.CompleteTurn(ctx, "session", Block{Kind: "assistant", RunID: "run-1", Content: "late old answer"}, oldHistory); err == nil || !strings.Contains(err.Error(), "active run changed") {
		t.Fatalf("late old completion error=%v", err)
	}
	after, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if after.LastRunID != "run-2" || after.CheckpointGeneration != before.CheckpointGeneration || !reflect.DeepEqual(after.ModelHistory, before.ModelHistory) || len(after.Blocks) != len(before.Blocks) {
		t.Fatalf("late completion changed projection: before=%+v after=%+v", before, after)
	}
}

func TestCompleteTurnPersistsModelHistoryWithoutEmptyAssistantBlock(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "tool-only.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run", Content: "use the tool"}); err != nil {
		t.Fatal(err)
	}
	history := ModelHistory{ProviderID: "chatgpt", ModelID: "model", Messages: []message.Message{
		message.NewText(message.RoleUser, "use the tool"),
		message.NewText(message.RoleAssistant, "tool completed without final text"),
	}}
	if err := service.CompleteTurn(ctx, "session", Block{Kind: "assistant", RunID: "run"}, history); err != nil {
		t.Fatal(err)
	}
	projection, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 1 || len(projection.ModelHistory.Messages) != 2 || projection.LastRunID != "run" {
		t.Fatalf("tool-only completion = %#v", projection)
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
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "parent", Content: "delegate"}); err != nil {
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
		if _, err := service.AppendBlock(ctx, "session", Block{
			Kind: "assistant", RunID: fmt.Sprintf("run-%d", index), Content: fmt.Sprintf("message %d", index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompactWithSummary(ctx, "session", CompactionPlan{
		Summary: "model-generated summary", ExpectedUpdatedAt: before.UpdatedAt, TailStart: 3,
		ModelHistory: ModelHistory{ProviderID: "chatgpt", ModelID: "model"},
	}); err != nil {
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
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-1", Content: "request"}); err != nil {
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
}

func TestCompactWithSummaryRejectsStaleProjectionWithoutMutation(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "stale-summary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := NewService(store.DB())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Test"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 6; index++ {
		if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", Content: fmt.Sprintf("message %d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	stale, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "assistant", Content: "concurrent update"}); err != nil {
		t.Fatal(err)
	}
	_, err = service.CompactWithSummary(ctx, "session", CompactionPlan{
		Summary: "stale summary", ExpectedUpdatedAt: stale.UpdatedAt, TailStart: 2,
	})
	if err == nil || !strings.Contains(err.Error(), "projection changed") {
		t.Fatalf("stale compaction error = %v", err)
	}
	current, err := service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Blocks) != 7 || current.Blocks[6].Content != "concurrent update" {
		t.Fatalf("stale compaction mutated blocks = %#v", current.Blocks)
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
		if _, err := service.AppendBlock(ctx, "session", block); err != nil {
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
	if len(projection.ModelHistory.Messages) != 1 {
		t.Fatalf("agent mutation invalidated provider history = %#v", projection.ModelHistory)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-2", Content: "next turn"}); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteTurn(ctx, "session", Block{Kind: "assistant", RunID: "run-2", Content: "next"}, history); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-3", Content: "failed turn"}); err != nil {
		t.Fatal(err)
	}
	projection, err = service.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.ModelHistory.Messages) != 1 {
		t.Fatalf("append invalidated provider history = %#v", projection.ModelHistory)
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
	if _, err := service.AppendBlock(ctx, "session", Block{Kind: "user", RunID: "run-1", Content: "request"}); err != nil {
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
