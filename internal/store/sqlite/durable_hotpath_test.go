package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/durable"
)

func durableHistoryFixture(t testing.TB, count int) (*Provider, durable.StartAttemptRequest) {
	t.Helper()
	ctx := context.Background()
	p, err := Open(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(ctx) })
	backend := p.DurableBackend().(*DurableBackend)
	spec := durable.ExecutionSpec{Request: hyagent.Request{Prompt: "long task"}}
	hash, err := durable.HashExecutionSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	started, err := backend.StartExecution(ctx, durable.StartExecutionRequest{
		ExecutionID: "long-task", OwnerID: "owner", ClaimID: durable.ClaimID{1}, LeaseTTL: time.Hour, Spec: spec, SpecHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = backend.withRecord(ctx, "long-task", "", func(record *durableRecord, _ time.Time) error {
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("history-%04d", i)
			record.attempts[id] = []durable.Attempt{{
				ExecutionID: "long-task", OperationID: id, Number: 1, Kind: durable.AttemptKindTool,
				InputHash: sha256.Sum256([]byte(id)), Status: durable.AttemptStatusSucceeded, Version: 2,
				Payload: []byte(strings.Repeat("observed output\n", 1024)),
			}}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, durable.StartAttemptRequest{
		ExecutionID: "long-task", Lease: durable.LeaseRef{OwnerID: "owner", Token: started.Execution.Lease.Token},
		OperationID: "current", Kind: durable.AttemptKindTool, InputHash: sha256.Sum256([]byte("current")),
	}
}

func TestAttemptMutationDoesNotRewriteUnrelatedHistory(t *testing.T) {
	p, request := durableHistoryFixture(t, 3)
	ctx := context.Background()
	// An unrelated attempt must not be written while settling this operation.
	if _, err := p.DB().Exec(`CREATE TEMP TRIGGER guard_history BEFORE UPDATE ON agent_effect_attempts WHEN OLD.operation_id <> 'current' BEGIN SELECT RAISE(ABORT,'unrelated attempt rewritten'); END`); err != nil {
		t.Fatal(err)
	}
	backend := p.DurableBackend()
	started, err := backend.StartAttempt(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := backend.FinishAttempt(ctx, durable.FinishAttemptRequest{
		ExecutionID: request.ExecutionID, Lease: request.Lease, OperationID: request.OperationID,
		AttemptNumber: started.Attempt.Number, ExpectedAttemptVersion: started.Attempt.Version, Payload: []byte("done"),
	})
	if err != nil || finished.Status != durable.AttemptStatusSucceeded {
		t.Fatalf("finish=%+v err=%v", finished, err)
	}
	replay, err := backend.StartAttempt(ctx, request)
	if err != nil || replay.Decision != durable.AttemptDecisionReplay || string(replay.Attempt.Payload) != "done" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := backend.LoadExecution(ctx, request.ExecutionID); err != nil {
		t.Fatalf("full graph validation: %v", err)
	}
}

func BenchmarkDurableAttemptWithHistory(b *testing.B) {
	for _, count := range []int{0, 300} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			p, request := durableHistoryFixture(b, count)
			backend := p.DurableBackend()
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				request.OperationID = fmt.Sprintf("current-%d", i)
				started, err := backend.StartAttempt(ctx, request)
				if err != nil {
					b.Fatal(err)
				}
				_, err = backend.FinishAttempt(ctx, durable.FinishAttemptRequest{
					ExecutionID: request.ExecutionID, Lease: request.Lease, OperationID: request.OperationID,
					AttemptNumber: started.Attempt.Number, ExpectedAttemptVersion: started.Attempt.Version, Payload: []byte("done"),
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestAttemptScopePreservesUnknownAndIntegrityChecks(t *testing.T) {
	p, request := durableHistoryFixture(t, 1)
	ctx := context.Background()
	backend := p.DurableBackend()
	started, err := backend.StartAttempt(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := backend.MarkAttemptUnknown(ctx, durable.MarkAttemptUnknownRequest{
		ExecutionID: request.ExecutionID, Lease: request.Lease, OperationID: request.OperationID,
		AttemptNumber: started.Attempt.Number, ExpectedAttemptVersion: started.Attempt.Version,
	})
	if err != nil || unknown.Status != durable.AttemptStatusUnknown {
		t.Fatalf("unknown=%+v err=%v", unknown, err)
	}
	replay, err := backend.StartAttempt(ctx, request)
	if err != nil || replay.Decision != durable.AttemptDecisionReconcile {
		t.Fatalf("unknown replay=%+v err=%v", replay, err)
	}
	execution, err := backend.LoadExecution(ctx, request.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	result := hyagent.Result{}
	hash, err := durable.HashResult(result)
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.FinishExecution(ctx, durable.FinishExecutionRequest{
		ExecutionID: request.ExecutionID, Lease: request.Lease, ExpectedVersion: execution.Version, Result: result, ResultHash: hash,
	})
	if !errors.Is(err, durable.ErrReconcileRequired) {
		t.Fatalf("finish ignored unknown attempt: %v", err)
	}
	// Corruption outside the current operation must still fail full graph loads.
	if _, err := p.DB().Exec(`UPDATE agent_effect_attempts SET input_hash=? WHERE operation_id='history-0000'`, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.LoadExecution(ctx, request.ExecutionID); err == nil {
		t.Fatal("full load ignored corrupt history")
	}
	request.OperationID = "history-0000"
	request.InputHash = sha256.Sum256([]byte(request.OperationID))
	if _, err := backend.StartAttempt(ctx, request); err == nil {
		t.Fatal("scoped mutation ignored corrupt target")
	}
}
