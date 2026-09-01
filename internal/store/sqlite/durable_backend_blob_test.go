package sqlite

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/durable"
	"github.com/Viking602/venat/message"
)

func TestDurableBackendOffloadsLargeStateAndReopens(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "durable.db")
	blobRoot := filepath.Join(root, "blobs")
	provider, err := Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	backend := provider.DurableBackend()
	spec := durable.ExecutionSpec{Request: hyagent.Request{Prompt: strings.Repeat("large-spec ", 1_000)}}
	specHash, err := durable.HashExecutionSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	claim := durable.ClaimID{15: 1}
	started, err := backend.StartExecution(ctx, durable.StartExecutionRequest{
		ExecutionID: "blob-execution", OwnerID: "owner", ClaimID: claim, LeaseTTL: time.Minute, Spec: spec, SpecHash: specHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	var executionDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT execution_digest FROM agent_executions WHERE execution_id='blob-execution'`).Scan(&executionDigest); err != nil {
		t.Fatal(err)
	}
	if executionDigest == "" {
		t.Fatal("large execution stayed inline")
	}
	lease := durable.LeaseRef{OwnerID: started.Execution.Lease.OwnerID, Token: started.Execution.Lease.Token}
	inputHash := sha256.Sum256([]byte("large-attempt"))
	attempt, err := backend.StartAttempt(ctx, durable.StartAttemptRequest{
		ExecutionID: "blob-execution", Lease: lease, OperationID: "turn:0:model", Kind: durable.AttemptKindModel, InputHash: inputHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(strings.Repeat("large-result ", 1_000))
	finished, err := backend.FinishAttempt(ctx, durable.FinishAttemptRequest{
		ExecutionID: "blob-execution", Lease: lease, OperationID: attempt.Attempt.OperationID,
		AttemptNumber: attempt.Attempt.Number, ExpectedAttemptVersion: attempt.Attempt.Version, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	var attemptDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT attempt_digest FROM agent_effect_attempts WHERE execution_id='blob-execution' AND operation_id='turn:0:model'`).Scan(&attemptDigest); err != nil {
		t.Fatal(err)
	}
	if attemptDigest == "" {
		t.Fatal("large attempt stayed inline")
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	backend = provider.DurableBackend()
	loaded, err := backend.LoadExecution(ctx, "blob-execution")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Spec.Request.Prompt != spec.Request.Prompt {
		t.Fatal("reopened execution lost large spec")
	}
	replayed, err := backend.StartAttempt(ctx, durable.StartAttemptRequest{
		ExecutionID: "blob-execution", Lease: lease, OperationID: "turn:0:model", Kind: durable.AttemptKindModel, InputHash: inputHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Decision != durable.AttemptDecisionReplay || string(replayed.Attempt.Payload) != string(payload) || replayed.Attempt.Version != finished.Version {
		t.Fatalf("replayed attempt = %#v", replayed)
	}
}

func TestDurableBackendRemovesSupersededBlobPayloads(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	provider, err := Open(ctx, filepath.Join(root, "durable.db"), WithBlobRoot(filepath.Join(root, "blobs")))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	backend := provider.DurableBackend()
	spec := durable.ExecutionSpec{Request: hyagent.Request{Prompt: strings.Repeat("superseded spec ", 1_000)}}
	specHash, err := durable.HashExecutionSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	started, err := backend.StartExecution(ctx, durable.StartExecutionRequest{
		ExecutionID: "superseded-execution", OwnerID: "owner", ClaimID: durable.ClaimID{15: 2},
		LeaseTTL: time.Minute, Spec: spec, SpecHash: specHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	var firstExecutionDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT execution_digest FROM agent_executions WHERE execution_id='superseded-execution'`).Scan(&firstExecutionDigest); err != nil {
		t.Fatal(err)
	}
	lease := durable.LeaseRef{OwnerID: started.Execution.Lease.OwnerID, Token: started.Execution.Lease.Token}
	if _, err := backend.RenewExecution(ctx, durable.RenewExecutionRequest{
		ExecutionID: "superseded-execution", Lease: lease, LeaseTTL: 2 * time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	var renewedExecutionDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT execution_digest FROM agent_executions WHERE execution_id='superseded-execution'`).Scan(&renewedExecutionDigest); err != nil {
		t.Fatal(err)
	}
	if firstExecutionDigest == "" || renewedExecutionDigest == "" || firstExecutionDigest == renewedExecutionDigest {
		t.Fatalf("execution digests before=%q after=%q", firstExecutionDigest, renewedExecutionDigest)
	}
	if exists, err := provider.Blobs().Exists(ctx, firstExecutionDigest); err != nil || exists {
		t.Fatalf("superseded execution blob exists=%v error=%v", exists, err)
	}

	inputHash := sha256.Sum256([]byte("superseded-attempt"))
	startedAttempt, err := backend.StartAttempt(ctx, durable.StartAttemptRequest{
		ExecutionID: "superseded-execution", Lease: lease, OperationID: "turn:0:tool",
		Kind: durable.AttemptKindTool, InputHash: inputHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := backend.MarkAttemptUnknown(ctx, durable.MarkAttemptUnknownRequest{
		ExecutionID: "superseded-execution", Lease: lease, OperationID: startedAttempt.Attempt.OperationID,
		AttemptNumber: startedAttempt.Attempt.Number, ExpectedAttemptVersion: startedAttempt.Attempt.Version,
		Payload: []byte(strings.Repeat("uncertain result ", 1_000)),
	})
	if err != nil {
		t.Fatal(err)
	}
	var uncertainDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT attempt_digest FROM agent_effect_attempts WHERE execution_id='superseded-execution' AND operation_id='turn:0:tool'`).Scan(&uncertainDigest); err != nil {
		t.Fatal(err)
	}
	if uncertainDigest == "" {
		t.Fatal("uncertain attempt payload stayed inline")
	}
	if _, err := backend.ReleaseExecution(ctx, durable.ReleaseExecutionRequest{ExecutionID: "superseded-execution", Lease: lease}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ReconcileAttempt(ctx, durable.ReconcileAttemptRequest{
		ExecutionID: "superseded-execution", OperationID: unknown.OperationID,
		AttemptNumber: unknown.Number, ExpectedAttemptVersion: unknown.Version,
		Resolution: durable.ReconcileResolutionRetry,
	}); err != nil {
		t.Fatal(err)
	}
	var reconciledDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT attempt_digest FROM agent_effect_attempts WHERE execution_id='superseded-execution' AND operation_id='turn:0:tool'`).Scan(&reconciledDigest); err != nil {
		t.Fatal(err)
	}
	if reconciledDigest != "" {
		t.Fatalf("reconciled attempt digest=%q, want inline", reconciledDigest)
	}
	if exists, err := provider.Blobs().Exists(ctx, uncertainDigest); err != nil || exists {
		t.Fatalf("superseded attempt blob exists=%v error=%v", exists, err)
	}
}

func TestDurableBackendOffloadsCheckpointAndResultAcrossReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "checkpoint-result.db")
	blobRoot := filepath.Join(root, "blobs")
	provider, err := Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	backend := provider.DurableBackend()
	spec := durable.ExecutionSpec{Request: hyagent.Request{Prompt: "persist continuation and result"}}
	specHash, err := durable.HashExecutionSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	started, err := backend.StartExecution(ctx, durable.StartExecutionRequest{
		ExecutionID: "checkpoint-result", OwnerID: "owner", ClaimID: durable.ClaimID{15: 3},
		LeaseTTL: time.Minute, Spec: spec, SpecHash: specHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease := durable.LeaseRef{OwnerID: started.Execution.Lease.OwnerID, Token: started.Execution.Lease.Token}
	continuation := hyagent.Continuation{
		SchemaVersion: hyagent.ContinuationSchemaVersion,
		Request:       hyagent.Request{Prompt: strings.Repeat("continuation ", 1_000)},
		Messages:      []message.Message{message.NewText(message.RoleUser, strings.Repeat("continuation ", 1_000))},
		Phase:         hyagent.ContinuationReady,
	}
	continuationHash, err := durable.HashContinuation(continuation)
	if err != nil {
		t.Fatal(err)
	}
	checkpointed, err := backend.SaveCheckpoint(ctx, durable.SaveCheckpointRequest{
		ExecutionID: "checkpoint-result", Lease: lease, ExpectedVersion: started.Execution.Version,
		Checkpoint: durable.Checkpoint{Sequence: 1, Continuation: continuation, ContinuationHash: continuationHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	var checkpointDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT execution_digest FROM agent_executions WHERE execution_id='checkpoint-result'`).Scan(&checkpointDigest); err != nil {
		t.Fatal(err)
	}
	if checkpointDigest == "" {
		t.Fatal("large continuation stayed inline")
	}
	result := hyagent.Result{Text: strings.Repeat("terminal result ", 1_000), Valid: true}
	resultHash, err := durable.HashResult(result)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := backend.FinishExecution(ctx, durable.FinishExecutionRequest{
		ExecutionID: "checkpoint-result", Lease: lease, ExpectedVersion: checkpointed.Version,
		Result: result, ResultHash: resultHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	var resultDigest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT execution_digest FROM agent_executions WHERE execution_id='checkpoint-result'`).Scan(&resultDigest); err != nil {
		t.Fatal(err)
	}
	if resultDigest == "" || resultDigest == checkpointDigest {
		t.Fatalf("checkpoint digest=%q result digest=%q", checkpointDigest, resultDigest)
	}
	if exists, err := provider.Blobs().Exists(ctx, checkpointDigest); err != nil || exists {
		t.Fatalf("superseded checkpoint blob exists=%v error=%v", exists, err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err = Open(ctx, path, WithBlobRoot(blobRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	loaded, err := provider.DurableBackend().LoadExecution(ctx, "checkpoint-result")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != durable.ExecutionStatusCompleted || loaded.Result == nil ||
		loaded.Result.Text != result.Text || loaded.Checkpoint == nil ||
		loaded.Checkpoint.Continuation.Request.Prompt != continuation.Request.Prompt ||
		loaded.ResultHash != finished.ResultHash {
		t.Fatalf("reopened execution=%+v", loaded)
	}
}
