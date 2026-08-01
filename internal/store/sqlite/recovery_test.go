package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/Viking602/venat"
	"github.com/Viking602/venat/api"
)

func TestPrepareRecoveryExpiresLeasesAndQuarantinesIncompleteActions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)

	uow, err := store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(-time.Minute)
	lease := api.TaskExecutionLease{ID: "lease-1", RunID: "run-1", TaskID: "task-1", HolderType: api.HolderAgent, HolderID: "agent-1", Status: api.LeaseStatusActive, ExpiresAt: expires, Version: 1}
	if err := uow.Leases().SaveLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	attempt := api.ActionAttempt{AttemptID: "attempt-1", ActionID: "action-1", RunID: "run-1", TaskID: "task-1", ToolName: "coding.write_file", Status: api.ActionAttemptRunning, IdempotencyKey: "key-1"}
	if err := uow.ActionAttempts().SaveActionAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO sessions(id,created_at,updated_at) VALUES('session-1',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO provider_requests(request_id,session_id,run_id,request_kind,status,started_at)
		VALUES('request-1','session-1','run-1','main','started',1)`); err != nil {
		t.Fatal(err)
	}

	expired, quarantined, err := store.PrepareRecovery(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 || quarantined != 1 {
		t.Fatalf("PrepareRecovery counts = %d,%d, want 1,1", expired, quarantined)
	}

	uow, err = store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loadedLease, err := uow.Leases().LoadLease(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	loadedAttempt, err := uow.ActionAttempts().LoadActionAttempt(ctx, attempt.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if loadedLease.Status != api.LeaseStatusExpired {
		t.Fatalf("lease status = %q", loadedLease.Status)
	}
	if loadedAttempt.Status != api.ActionAttemptUnknown || !loadedAttempt.RequiresReconcile {
		t.Fatalf("attempt = %+v", loadedAttempt)
	}
	var providerStatus string
	if err := store.DB().QueryRowContext(ctx, `SELECT status FROM provider_requests WHERE request_id='request-1'`).Scan(&providerStatus); err != nil {
		t.Fatal(err)
	}
	if providerStatus != "unknown" {
		t.Fatalf("provider request status = %q, want unknown", providerStatus)
	}

	expired, quarantined, err = store.PrepareRecovery(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if expired != 0 || quarantined != 0 {
		t.Fatalf("second PrepareRecovery replayed mutations: %d,%d", expired, quarantined)
	}
}

func TestResolveReconcileAttemptRequiresExplicitTerminalOutcome(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	uow, err := store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	attempt := api.ActionAttempt{AttemptID: "attempt-1", ActionID: "action-1", RunID: "run-1", TaskID: "task-1", ToolName: "coding.shell", Status: api.ActionAttemptUnknown, RequiresReconcile: true}
	if err := uow.Runs().SaveRun(ctx, api.Run{ID: attempt.RunID, RootTaskID: attempt.TaskID, Status: api.RunStatusReconcileRequired}); err != nil {
		t.Fatal(err)
	}
	if err := uow.Tasks().SaveTask(ctx, api.Task{ID: attempt.TaskID, RunID: attempt.RunID, Status: api.TaskStatusReconcileRequired}); err != nil {
		t.Fatal(err)
	}
	if err := uow.ActionAttempts().SaveActionAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	runner := venat.NewDevelopment(api.Config{StoreProvider: store})
	if _, err := runner.ResolveActionAttempt(ctx, api.ResolveActionAttemptCommand{AttemptID: attempt.AttemptID, Status: api.ActionAttemptRunning}); err == nil {
		t.Fatal("nonterminal reconciliation status accepted")
	}
	if _, err := runner.ResolveActionAttempt(ctx, api.ResolveActionAttemptCommand{AttemptID: attempt.AttemptID, Status: api.ActionAttemptSucceeded, ExternalResultRef: "receipt-1"}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListReconcileAttempts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending reconcile attempts = %+v", pending)
	}

	uow, err = store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := uow.ActionAttempts().LoadActionAttempt(ctx, attempt.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != api.ActionAttemptSucceeded || resolved.RequiresReconcile || resolved.ExternalResultRef != "receipt-1" {
		t.Fatalf("resolved attempt = %+v", resolved)
	}
	run, err := uow.Runs().LoadRun(ctx, attempt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := uow.Tasks().LoadTask(ctx, attempt.RunID, attempt.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if run.Status != api.RunStatusRunning || task.Status != api.TaskStatusDispatched {
		t.Fatalf("reconciled run/task = %s/%s", run.Status, task.Status)
	}
}
