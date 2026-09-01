package app

import (
	"context"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestSharedChildDoesNotWaitForParentWorkspaceClaim(t *testing.T) {
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
	parentRun, err := coding.StartRunWithMetadata(ctx, "parent", nil, agentservice.RunExecutionPolicy{ResourceClaims: []agentruntime.ResourceClaimSpec{claim}})
	if err != nil {
		t.Fatal(err)
	}
	durableParent, err := coding.LoadRun(ctx, parentRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	parentTasks, err := coding.ListTasks(ctx, parentRun.RunID)
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
	childClaims, claimErr := runtime.childWorkspaceClaims(ctx, parent, profile)
	if claimErr != nil {
		t.Fatal(claimErr)
	}
	if len(childClaims) != 0 {
		t.Fatalf("child claims=%#v, want none so sessions can run in parallel", childClaims)
	}
	childRun, err := coding.StartRunWithMetadata(ctx, "recovered child", nil, agentservice.RunExecutionPolicy{ResourceClaims: []agentruntime.ResourceClaimSpec{claim}})
	if err != nil {
		t.Fatal(err)
	}
	if err := clearRecoveredSharedWorkspaceClaim(ctx, parent, childRun.RunID, claim.Key); err != nil {
		t.Fatal(err)
	}
	childDurable, err := coding.LoadRun(ctx, childRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	childTasks, err := coding.ListTasks(ctx, childRun.RunID)
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
	if len(claims) != 0 {
		t.Fatalf("child claims=%#v, want none so sessions can run in parallel", claims)
	}
}

func TestTopLevelWorkspaceClaimsAllowParallelSessions(t *testing.T) {
	workspace := t.TempDir()
	first, err := topLevelWorkspaceWriteClaims(true, "deny", workspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := topLevelWorkspaceWriteClaims(true, "prompt", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 0 || len(second) != 0 {
		t.Fatalf("parallel sessions received workspace claims first=%#v second=%#v", first, second)
	}
}
