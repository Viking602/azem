package app

import (
	"context"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/api"
)

func TestSharedChildClaimWaitsForActiveParentClaimAndThenInherits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(context.Background())
	claim, err := workspaceWriteClaim(workspace)
	if err != nil {
		t.Fatal(err)
	}
	parentRun, err := coding.StartRunWithMetadata(ctx, "parent", nil, agentservice.RunExecutionPolicy{ResourceClaims: []api.ResourceClaimSpec{claim}})
	if err != nil {
		t.Fatal(err)
	}
	durableParent, err := coding.Runner().Run(ctx, parentRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	parentTasks, err := coding.Runner().ListTasks(ctx, parentRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var parentWorkerClaim bool
	for _, task := range parentTasks {
		if task.ID == durableParent.RootTaskID {
			if len(task.ResourceClaims) != 0 {
				t.Fatalf("parent root task claims=%#v", task.ResourceClaims)
			}
			continue
		}
		for _, taskClaim := range task.ResourceClaims {
			if taskClaim == claim {
				parentWorkerClaim = true
			}
		}
	}
	if !parentWorkerClaim {
		t.Fatalf("parent worker task claims=%#v", parentTasks)
	}
	parent := subagentParentRuntime{
		ParentRunID: parentRun.RunID, Coding: coding, WorkspaceRoot: workspace,
	}
	runtime := &subagentRuntime{}
	profile := effectiveSubagentProfile{CapabilityMode: "all", Isolation: "none", Tools: []string{"coding.write_file"}}
	result := make(chan []api.ResourceClaimSpec, 1)
	errors := make(chan error, 1)
	go func() {
		claims, claimErr := runtime.childWorkspaceClaims(ctx, parent, profile)
		result <- claims
		errors <- claimErr
	}()
	select {
	case <-result:
		t.Fatal("shared child ran before parent claim became active")
	case <-time.After(50 * time.Millisecond):
	}
	activeClaim := claim
	activeClaim.ID = "parent-claim"
	now := time.Now().UTC()
	decision, err := coding.Runner().AcquireResourceClaims(ctx, api.ResourceClaimRequest{
		RunID: parentRun.RunID, TaskID: parentRun.TaskID, LeaseID: "parent-lease", HolderID: "parent-holder",
		Claims: []api.ResourceClaimSpec{activeClaim}, RequestedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil || !decision.Acquired {
		t.Fatalf("activate parent claim decision=%#v error=%v", decision, err)
	}
	select {
	case childClaims := <-result:
		if claimErr := <-errors; claimErr != nil {
			t.Fatal(claimErr)
		}
		if len(childClaims) != 0 {
			t.Fatalf("inherited child claims=%#v", childClaims)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	childRun, err := coding.StartRunWithMetadata(ctx, "recovered child", nil, agentservice.RunExecutionPolicy{ResourceClaims: []api.ResourceClaimSpec{claim}})
	if err != nil {
		t.Fatal(err)
	}
	if err := clearRecoveredSharedWorkspaceClaim(ctx, parent, childRun.RunID, claim.Key); err != nil {
		t.Fatal(err)
	}
	childDurable, err := coding.Runner().Run(ctx, childRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	childTasks, err := coding.Runner().ListTasks(ctx, childRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var childWorkerClaims int
	for _, task := range childTasks {
		if task.ID == childDurable.RootTaskID {
			if len(task.ResourceClaims) != 0 {
				t.Fatalf("recovered child root claims=%#v", task.ResourceClaims)
			}
			continue
		}
		childWorkerClaims += len(task.ResourceClaims)
	}
	if childWorkerClaims != 0 {
		t.Fatalf("recovered child worker claims=%d, tasks=%#v", childWorkerClaims, childTasks)
	}
}

func TestSharedChildLegacyParentWithoutClaimUsesOwnClaim(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(context.Background())
	parentRun, err := coding.StartRun(ctx, "legacy parent")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := (&subagentRuntime{}).childWorkspaceClaims(ctx, subagentParentRuntime{
		ParentRunID: parentRun.RunID, Coding: coding, WorkspaceRoot: workspace,
	}, effectiveSubagentProfile{CapabilityMode: "read-write", Isolation: "none", Tools: []string{"coding.write_file"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Mode != api.ResourceClaimExclusive {
		t.Fatalf("legacy child claims=%#v", claims)
	}
}

func TestTopLevelWorkspaceClaimsConflictAcrossRuns(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(context.Background())
	claims, err := topLevelWorkspaceWriteClaims(true, "deny", workspace)
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := coding.StartRunWithMetadata(ctx, "first writer", nil, agentservice.RunExecutionPolicy{ResourceClaims: claims})
	if err != nil {
		t.Fatal(err)
	}
	secondRun, err := coding.StartRunWithMetadata(ctx, "second writer", nil, agentservice.RunExecutionPolicy{ResourceClaims: claims})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	firstClaim := claims[0]
	firstClaim.ID = "claim-first"
	first, err := coding.Runner().AcquireResourceClaims(ctx, api.ResourceClaimRequest{
		RunID: firstRun.RunID, TaskID: firstRun.TaskID, LeaseID: "lease-first", HolderID: "writer-first",
		Claims: []api.ResourceClaimSpec{firstClaim}, RequestedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil || !first.Acquired {
		t.Fatalf("first acquisition=%#v error=%v", first, err)
	}
	secondClaim := claims[0]
	secondClaim.ID = "claim-second"
	second, err := coding.Runner().AcquireResourceClaims(ctx, api.ResourceClaimRequest{
		RunID: secondRun.RunID, TaskID: secondRun.TaskID, LeaseID: "lease-second", HolderID: "writer-second",
		Claims: []api.ResourceClaimSpec{secondClaim}, RequestedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Acquired || second.Reason != api.ResourceClaimDeniedConflict || len(second.Conflicts) != 1 {
		t.Fatalf("second acquisition=%#v, want one conflict", second)
	}
	if second.Conflicts[0].RunID != firstRun.RunID || second.Conflicts[0].Key != claims[0].Key {
		t.Fatalf("conflict=%#v, want first writer", second.Conflicts[0])
	}
}
