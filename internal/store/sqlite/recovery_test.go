package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn/api"
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
	attempt := api.ActionAttempt{AttemptID: "attempt-1", RunID: "run-1", TaskID: "task-1", ToolName: "coding.shell", Status: api.ActionAttemptUnknown, RequiresReconcile: true}
	if err := uow.ActionAttempts().SaveActionAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := store.ResolveReconcileAttempt(ctx, attempt.AttemptID, api.ActionAttemptRunning, ""); err == nil {
		t.Fatal("nonterminal reconciliation status accepted")
	}
	if err := store.ResolveReconcileAttempt(ctx, attempt.AttemptID, api.ActionAttemptSucceeded, "receipt-1"); err != nil {
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
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if resolved.Status != api.ActionAttemptSucceeded || resolved.RequiresReconcile || resolved.ExternalResultRef != "receipt-1" {
		t.Fatalf("resolved attempt = %+v", resolved)
	}
}
