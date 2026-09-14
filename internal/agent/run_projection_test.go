package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/durable"
	"github.com/Viking602/venat/message"

	"github.com/Viking602/azem/internal/agentruntime"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestQueueItemRunsReturnsDurableSessionScopedBindings(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "queue-runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	run, err := service.StartRunWithMetadata(ctx, "queued", map[string]string{
		"session_id": "session-a", "queue_item_id": "item-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := service.QueueItemRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bindings["session-a\x00item-1"] != run.RunID {
		t.Fatalf("queue item run bindings = %#v", bindings)
	}
}

func TestLoadRunProjectionDoesNotLoadDurableExecutionGraph(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "run-projection.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	run, err := service.StartRunWithMetadata(ctx, "project", map[string]string{"session_id": "session"})
	if err != nil {
		t.Fatal(err)
	}
	backend := store.DurableBackend()
	spec := durable.ExecutionSpec{Request: hyagent.Request{Prompt: "project"}}
	specHash, err := durable.HashExecutionSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	startedExecution, err := backend.StartExecution(ctx, durable.StartExecutionRequest{
		ExecutionID: durable.ExecutionID(run.ExecutionID), OwnerID: "owner", ClaimID: durable.ClaimID{15: 9},
		LeaseTTL: time.Minute, Spec: spec, SpecHash: specHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	continuation := hyagent.Continuation{
		SchemaVersion: hyagent.ContinuationSchemaVersion,
		Request:       hyagent.Request{Prompt: strings.Repeat("projection ", 20_000)},
		Messages:      []message.Message{message.NewText(message.RoleUser, strings.Repeat("projection ", 20_000))},
		Phase:         hyagent.ContinuationReady,
	}
	continuationHash, err := durable.HashContinuation(continuation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.SaveCheckpoint(ctx, durable.SaveCheckpointRequest{
		ExecutionID:     durable.ExecutionID(run.ExecutionID),
		Lease:           durable.LeaseRef{OwnerID: startedExecution.Execution.Lease.OwnerID, Token: startedExecution.Execution.Lease.Token},
		ExpectedVersion: startedExecution.Execution.Version,
		Checkpoint:      durable.Checkpoint{Sequence: 1, Continuation: continuation, ContinuationHash: continuationHash},
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	source, err := service.LoadRunProjection(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("run projection loaded execution graph in %s", elapsed)
	}
	if source.Binding == nil || source.Binding.ExecutionID != run.ExecutionID {
		t.Fatalf("binding = %#v", source.Binding)
	}
	if _, err := backend.LoadExecution(ctx, durable.ExecutionID(run.ExecutionID)); err != nil {
		t.Fatalf("execution payload remains independently loadable: %v", err)
	}
}

func TestClassifyRunRecoveryDoesNotLoadDurableExecutionGraph(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "classify-projection.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	run, err := service.StartRunWithMetadata(ctx, "project", map[string]string{"session_id": "session"})
	if err != nil {
		t.Fatal(err)
	}
	backend := store.DurableBackend()
	spec := durable.ExecutionSpec{Request: hyagent.Request{Prompt: "project"}}
	specHash, err := durable.HashExecutionSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	startedExecution, err := backend.StartExecution(ctx, durable.StartExecutionRequest{
		ExecutionID: durable.ExecutionID(run.ExecutionID), OwnerID: "owner", ClaimID: durable.ClaimID{15: 11},
		LeaseTTL: time.Minute, Spec: spec, SpecHash: specHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	continuation := hyagent.Continuation{
		SchemaVersion: hyagent.ContinuationSchemaVersion,
		Request:       hyagent.Request{Prompt: strings.Repeat("classify ", 20_000)},
		Messages:      []message.Message{message.NewText(message.RoleUser, strings.Repeat("classify ", 20_000))},
		Phase:         hyagent.ContinuationReady,
	}
	continuationHash, err := durable.HashContinuation(continuation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.SaveCheckpoint(ctx, durable.SaveCheckpointRequest{
		ExecutionID:     durable.ExecutionID(run.ExecutionID),
		Lease:           durable.LeaseRef{OwnerID: startedExecution.Execution.Lease.OwnerID, Token: startedExecution.Execution.Lease.Token},
		ExpectedVersion: startedExecution.Execution.Version,
		Checkpoint:      durable.Checkpoint{Sequence: 1, Continuation: continuation, ContinuationHash: continuationHash},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SealExecutionProfile(ctx, run, agentruntime.ExecutableProfile{
		Provider: "test", AccountID: "account", RawModel: "test-model", Model: "test-model", Reasoning: "none",
		ActiveSkills: []string{}, ToolSetHash: "tools", ToolProfileHash: "tool-profile",
		StaticIdentity: "static", WorkspaceAnchor: t.TempDir(),
		PromptFingerprint: "prompt", ToolSchemaFingerprint: "tool-schema",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE agent_execution_bindings SET state=? WHERE execution_id=?`, agentruntime.ExecutionBindingRunning, run.ExecutionID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	kind, replay, err := service.ClassifyRunRecovery(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("recovery classification loaded execution graph in %s", elapsed)
	}
	if kind == "" || !replay {
		t.Fatalf("kind=%q replay=%t", kind, replay)
	}
}
