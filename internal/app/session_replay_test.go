package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/azem/internal/toolview"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

// TestSessionReplayProjectsConsistentAssemblyAcrossReopen is the
// assembly-level replay check: a real on-disk SQLite session is written
// through the durable APIs, the database is closed and reopened by a fresh
// service, and the desktop projection payload is verified against the shared
// toolview source that the TUI renders from. Block order, TextPhase, and the
// structured file-change summary must survive the round trip so both UIs
// replay the identical projection.
func TestSessionReplayProjectsConsistentAssemblyAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "replay.db")

	store, err := sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB())
	if _, err = sessions.Ensure(ctx, session.Session{ID: "replay", Title: "replay session"}); err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.AppendBlock(ctx, "replay", session.Block{
		Kind: "user", RunID: "run-1", Content: "fix the bug",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.AppendBlock(ctx, "replay", session.Block{
		Kind: "commentary", RunID: "run-1", Title: "progress", Content: "Inspecting the file first.",
		TextPhase: string(hyprovider.TextPhaseCommentary), State: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	structured := `{"sections":[{"path":"main.go","firstChangedLine":7,"diff":"-return nil\n+return err\n+log(err)"}]}`
	record := session.ToolRecord{
		RunID: "run-1", ToolCallID: "edit-1", Name: "coding.edit_hashline",
		Arguments: json.RawMessage(`{"path":"main.go"}`),
	}
	if record, err = sessions.StartToolRecord(ctx, "replay", record); err != nil {
		t.Fatal(err)
	}
	record.State = session.ToolCompleted
	record.Content = "edited"
	record.Structured = json.RawMessage(structured)
	if _, err = sessions.FinishToolRecord(ctx, "replay", record); err != nil {
		t.Fatal(err)
	}
	if err = sessions.CompleteTurn(ctx, "replay", session.Block{
		Kind: "assistant", RunID: "run-1", Title: "Azem", Content: "Fixed the error handling.",
		TextPhase: string(hyprovider.TextPhaseFinalAnswer), State: "completed",
	}, session.ModelHistory{
		ProviderID: "chatgpt", ModelID: "gpt-test", WireVersion: session.CurrentWireVersion,
		Messages: []message.Message{
			message.NewText(message.RoleUser, "fix the bug"),
			message.NewText(message.RoleAssistant, "Fixed the error handling."),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	replayed := session.NewService(reopened.DB())
	projection, err := replayed.LoadProjection(ctx, "replay")
	if err != nil {
		t.Fatal(err)
	}

	kinds := make([]string, 0, len(projection.Blocks))
	phases := make([]string, 0, len(projection.Blocks))
	for _, block := range projection.Blocks {
		kinds = append(kinds, block.Kind)
		phases = append(phases, block.TextPhase)
	}
	wantKinds := []string{"user", "commentary", "assistant"}
	wantPhases := []string{"", string(hyprovider.TextPhaseCommentary), string(hyprovider.TextPhaseFinalAnswer)}
	if !reflect.DeepEqual(kinds, wantKinds) || !reflect.DeepEqual(phases, wantPhases) {
		t.Fatalf("replayed transcript kinds=%v phases=%v", kinds, phases)
	}

	data := sessionProjectionData(projection, "[]")
	var projected []projectedToolRecord
	if err = json.Unmarshal([]byte(data["toolRecords"]), &projected); err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 || projected[0].FileChange == "" {
		t.Fatalf("replayed tool records missing file change projection: %+v", projected)
	}
	fromWire, ok := toolview.DecodeSummary(projected[0].FileChange)
	if !ok {
		t.Fatalf("projected fileChange did not decode: %q", projected[0].FileChange)
	}
	durable := projection.ToolRecords[0]
	fromDurable, ok := toolview.CompletedFileChanges(durable.Name, string(durable.Arguments), string(durable.Structured), durable.Content)
	if !ok {
		t.Fatal("durable record no longer projects a file change")
	}
	if !reflect.DeepEqual(fromWire, fromDurable) {
		t.Fatalf("desktop wire summary diverged from the shared toolview projection:\nwire   =%+v\ndurable=%+v", fromWire, fromDurable)
	}
	if fromWire.Files[0].Path != "main.go" || fromWire.Additions != 2 || fromWire.Deletions != 1 {
		t.Fatalf("unexpected replayed summary: %+v", fromWire)
	}

	if projection.ModelHistory.ProviderID != "chatgpt" || len(projection.ModelHistory.Messages) != 2 {
		t.Fatalf("model history did not survive reopen: %+v", projection.ModelHistory)
	}
}
