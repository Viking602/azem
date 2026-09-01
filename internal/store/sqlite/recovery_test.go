package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
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
	lease := agentruntime.TaskExecutionLease{ID: "lease-1", RunID: "run-1", TaskID: "task-1", HolderType: agentruntime.HolderAgent, HolderID: "agent-1", Status: agentruntime.LeaseStatusActive, ExpiresAt: expires, Version: 1}
	if err := uow.Leases().SaveLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	attempt := agentruntime.ActionAttempt{AttemptID: "attempt-1", ActionID: "action-1", RunID: "run-1", TaskID: "task-1", ToolName: "coding.write_file", Status: agentruntime.ActionAttemptRunning, IdempotencyKey: "key-1"}
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
	if loadedLease.Status != agentruntime.LeaseStatusExpired {
		t.Fatalf("lease status = %q", loadedLease.Status)
	}
	if loadedAttempt.Status != agentruntime.ActionAttemptUnknown || !loadedAttempt.RequiresReconcile {
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

func TestPrepareRecoveryExpiresOrphanWorkspaceClaims(t *testing.T) {
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
	now := time.Now().UTC()
	claimStore, ok := uow.(agentruntime.ResourceClaimUnitOfWork)
	if !ok {
		t.Fatal("unit of work does not expose resource claims")
	}
	decision, err := claimStore.ResourceClaims().AcquireResourceClaims(ctx, agentruntime.ResourceClaimRequest{
		RunID: "run-1", TaskID: "task-1", LeaseID: "lease-1", HolderID: "azem-main",
		RequestedAt: now, ExpiresAt: now.Add(time.Minute),
		Claims: []agentruntime.ResourceClaimSpec{{ID: "claim-1", Key: "azem:workspace-write:/tmp/azem", Mode: agentruntime.ResourceClaimExclusive}},
	})
	if err != nil || !decision.Acquired {
		t.Fatalf("acquire claim=%#v error=%v", decision, err)
	}
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.PrepareRecovery(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	uow, err = store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claimStore, ok = uow.(agentruntime.ResourceClaimUnitOfWork)
	if !ok {
		t.Fatal("unit of work does not expose resource claims")
	}
	claim, err := claimStore.ResourceClaims().LoadResourceClaim(ctx, "claim-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if claim.State != agentruntime.ResourceClaimExpired {
		t.Fatalf("recovered claim state = %q, want expired", claim.State)
	}
}
