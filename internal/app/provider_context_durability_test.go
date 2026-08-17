package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/message"
)

// TestBuildEnforcesModelVisibleDurabilityInvariant pins the model-visible ⟺
// durably-logged invariant: a turn context assembled purely from durable
// sources passes, while injecting a model-visible message that has no durable
// origin fails the build explicitly instead of silently reaching the provider.
func TestBuildEnforcesModelVisibleDurabilityInvariant(t *testing.T) {
	history := []session.Block{
		{Kind: "user", Content: "please inspect", Sequence: 1},
		{Kind: "assistant", Content: "done earlier", Sequence: 2},
	}
	manager := turnContext{instructions: "system rules", history: history}
	messages, err := manager.Build(context.Background(), api.Task{Goal: "next step"})
	if err != nil {
		t.Fatalf("durable-only build failed the invariant: %v", err)
	}
	if len(messages) == 0 {
		t.Fatal("build produced no messages")
	}

	rogue := message.NewText(message.RoleUser, "injected content that was never persisted")
	err = manager.validateModelVisibleDurability(append(messages, rogue), false, "next step", nil)
	if err == nil {
		t.Fatal("unlogged model-visible message passed the durability invariant")
	}
	if !strings.Contains(err.Error(), "not reconstructible from durable state") {
		t.Fatalf("unexpected invariant error: %v", err)
	}
}

// TestModelVisibleDurabilityExemptsPrivateDerivedContext keeps the derived
// private sources (hook context, todo reminders, evidence) out of the
// assertion: they are rebuilt from durable stores or re-execution.
func TestModelVisibleDurabilityExemptsPrivateDerivedContext(t *testing.T) {
	private := message.NewText(message.RoleSystem, "[Trusted private hook context]\nephemeral")
	private.Visibility = message.VisibilityPrivate
	manager := turnContext{}
	if err := manager.validateModelVisibleDurability([]message.Message{private}, false, "", nil); err != nil {
		t.Fatalf("private derived context tripped the invariant: %v", err)
	}
}

// TestModelVisibleDurabilityCoversCompatibleCheckpointPrefix admits messages
// replayed verbatim from the durable ModelHistory checkpoint.
func TestModelVisibleDurabilityCoversCompatibleCheckpointPrefix(t *testing.T) {
	saved := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "checkpointed question"),
		message.NewText(message.RoleAssistant, "checkpointed answer"),
	}
	manager := turnContext{modelHistory: session.ModelHistory{Messages: saved}}
	if err := manager.validateModelVisibleDurability(saved, true, "", nil); err != nil {
		t.Fatalf("checkpoint prefix tripped the invariant: %v", err)
	}
	if err := manager.validateModelVisibleDurability(saved, false, "", nil); err == nil {
		t.Fatal("incompatible checkpoint messages must not be admitted as durable")
	}
}
