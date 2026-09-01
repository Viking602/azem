package app

import (
	"context"
	"testing"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestPersistedTurnControlSurvivesCanonicalHistoryBuild(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "controlled", Title: "Controlled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "controlled", session.Block{Kind: "user", RunID: "run-1", Content: "original request"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "controlled", session.Block{Kind: "assistant", RunID: "run-1", Content: "original answer", State: "completed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "controlled", session.Block{Kind: "user", RunID: "run-1", Title: "Follow-up", Content: "durable follow-up", State: "follow_up"}); err != nil {
		t.Fatal(err)
	}
	projection, err := sessions.LoadProjection(ctx, "controlled")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 3 || projection.Blocks[2].State != "follow_up" {
		t.Fatalf("turn control projection = %#v", projection.Blocks)
	}
	messages, err := (turnContext{history: projection.Blocks}).Build(ctx, hyagent.Request{Prompt: "resumed request"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, current := range messages {
		if current.Role == message.RoleUser && current.Text == "durable follow-up" {
			found = true
		}
	}
	if !found {
		t.Fatalf("persisted follow-up missing from canonical provider history: %#v", messages)
	}
}
