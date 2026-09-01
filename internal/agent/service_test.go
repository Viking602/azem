package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/durable"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/provider/scripted"
	"github.com/Viking602/venat/tool"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestGovernedReadApprovalEditAndStaleAnchor(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	run, err := service.StartRun(ctx, "edit note")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.Goal != "edit note" {
		t.Fatalf("run goal = %q", run.Goal)
	}

	read := executeRead(t, ctx, service, run, "read-1", "note.txt")
	patch := ompTestPatch(read.Header, "PUT 2.=2:\n+BETA")
	arguments, _ := json.Marshal(map[string]string{"input": patch})
	call := tool.Call{ID: "edit-1", Name: ToolEditHashline, Arguments: arguments}
	first, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil {
		t.Fatalf("request edit approval: %v", err)
	}
	if first.Executed || first.Approval == nil {
		t.Fatalf("first edit = %+v, want approval", first)
	}
	if first.Approval.Scope.Target != "note.txt" {
		t.Fatalf("approval target = %q", first.Approval.Scope.Target)
	}
	assertFile(t, path, "alpha\nbeta\ngamma\n")
	if err := service.ResolveApproval(ctx, run, call.ID, ApprovalOnce, "user"); err != nil {
		t.Fatalf("approve once: %v", err)
	}
	assertApprovalDecider(t, service, run.RunID, "user")
	second, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil {
		t.Fatalf("execute approved edit: %v", err)
	}
	if !second.Executed || second.Result.IsError {
		t.Fatalf("approved edit result = %+v", second)
	}
	assertFile(t, path, "alpha\nBETA\ngamma\n")

	staleArgs, _ := json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 2.=2:\n+STALE")})
	staleCall := tool.Call{ID: "edit-stale", Name: ToolEditHashline, Arguments: staleArgs}
	pending, err := service.ExecuteTool(ctx, run, staleCall, nil)
	if err != nil || pending.Approval == nil {
		t.Fatalf("stale approval: result=%+v err=%v", pending, err)
	}
	if err := service.ResolveApproval(ctx, run, staleCall.ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	stale, err := service.ExecuteTool(ctx, run, staleCall, nil)
	if err != nil {
		t.Fatalf("stale execution error: %v", err)
	}
	if !stale.Executed || !stale.Result.IsError || !strings.Contains(strings.ToLower(stale.Result.Content), "stale") {
		t.Fatalf("stale result = %+v", stale)
	}
	assertFile(t, path, "alpha\nBETA\ngamma\n")
	blockedArgs, _ := json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 3.=3:\n+GAMMA")})
	blocked, err := service.ExecuteTool(ctx, run, tool.Call{ID: "edit-after-stale-without-read", Name: ToolEditHashline, Arguments: blockedArgs}, nil)
	if err != nil || !blocked.Executed || !blocked.Result.IsError || blocked.Approval != nil {
		t.Fatalf("edit after stale without read = %+v, error=%v", blocked, err)
	}
	if !strings.Contains(blocked.Result.Content, ToolReadFile) {
		t.Fatalf("stale recovery did not require %s: %q", ToolReadFile, blocked.Result.Content)
	}
}

func TestRecoveredApprovalResumesExactOperationAndPreservesDenial(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	run, err := service.StartRun(ctx, "recover approval")
	if err != nil {
		t.Fatal(err)
	}

	read := executeRead(t, ctx, service, run, "read-recovery", "note.txt")
	arguments, _ := json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 1.=1:\n+approved")})
	call := tool.Call{
		ID: "provider-before-crash", OperationID: "turn:1:call:0",
		Name: ToolEditHashline, Arguments: arguments,
	}
	pending, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil || pending.Approval == nil {
		t.Fatalf("approval request=%+v error=%v", pending, err)
	}
	if pending.Approval.Request.ActionID != call.OperationID ||
		pending.Approval.Request.Metadata[approvalMetadataOperationID] != call.OperationID ||
		pending.Approval.Request.Metadata[approvalMetadataScope] == "" {
		t.Fatalf("durable approval identity = %+v", pending.Approval.Request)
	}
	responseLostRun := newRecoveredApprovalRun(run)
	replayedCall := call
	replayedCall.ID = "provider-after-request-response-loss"
	replayed, err := service.ExecuteTool(ctx, responseLostRun, replayedCall, nil)
	if err != nil || replayed.Approval == nil {
		t.Fatalf("replayed approval request=%+v error=%v", replayed, err)
	}
	if replayed.Approval.Request.ApprovalID != pending.Approval.Request.ApprovalID ||
		replayed.Approval.Token.TokenID != pending.Approval.Token.TokenID {
		t.Fatalf("response-loss replay created another pending approval: first=%+v replay=%+v", pending.Approval, replayed.Approval)
	}
	recoveredRun := newRecoveredApprovalRun(run)
	if err := service.ResolveRecoveredApproval(
		ctx, pending.Approval.Request, pending.Approval.Token.TokenID, "once",
	); err != nil {
		t.Fatalf("ResolveRecoveredApproval(once) error = %v", err)
	}
	regenerated := call
	regenerated.ID = "provider-after-crash"
	executed, err := service.ExecuteTool(ctx, recoveredRun, regenerated, nil)
	if err != nil || !executed.Executed || executed.Result.IsError {
		t.Fatalf("recovered approved execution=%+v error=%v", executed, err)
	}
	assertFile(t, path, "approved\n")

	read = executeRead(t, ctx, service, recoveredRun, "read-denial", "note.txt")
	arguments, _ = json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 1.=1:\n+denied-write")})
	deniedCall := tool.Call{
		ID: "provider-denied-before-crash", OperationID: "turn:3:call:0",
		Name: ToolEditHashline, Arguments: arguments,
	}
	deniedPending, err := service.ExecuteTool(ctx, recoveredRun, deniedCall, nil)
	if err != nil || deniedPending.Approval == nil {
		t.Fatalf("denied approval request=%+v error=%v", deniedPending, err)
	}
	secondRecovery := newRecoveredApprovalRun(recoveredRun)
	if err := service.ResolveRecoveredApproval(
		ctx, deniedPending.Approval.Request, deniedPending.Approval.Token.TokenID, "deny",
	); err != nil {
		t.Fatalf("ResolveRecoveredApproval(deny) error = %v", err)
	}
	deniedCall.ID = "provider-denied-after-crash"
	denied, err := service.ExecuteTool(ctx, secondRecovery, deniedCall, nil)
	if err != nil || denied.Executed || !denied.Result.IsError || denied.Approval != nil ||
		!strings.Contains(denied.Result.Content, "Denied") {
		t.Fatalf("recovered denied execution=%+v error=%v", denied, err)
	}
	assertFile(t, path, "approved\n")

	read = executeRead(t, ctx, service, secondRecovery, "read-expired", "note.txt")
	arguments, _ = json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 1.=1:\n+expired-write")})
	expiredCall := tool.Call{
		ID: "provider-expired-before-crash", OperationID: "turn:5:call:0",
		Name: ToolEditHashline, Arguments: arguments,
	}
	expiredPending, err := service.ExecuteTool(ctx, secondRecovery, expiredCall, nil)
	if err != nil || expiredPending.Approval == nil {
		t.Fatalf("expired approval request=%+v error=%v", expiredPending, err)
	}
	uow, err := store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token, err := uow.ResumeTokens().LoadResumeToken(ctx, expiredPending.Approval.Token.TokenID)
	if err != nil {
		_ = uow.Rollback(ctx)
		t.Fatal(err)
	}
	token.ExpiresAt = time.Now().Add(-time.Minute)
	if err := uow.ResumeTokens().SaveResumeToken(ctx, token); err != nil {
		_ = uow.Rollback(ctx)
		t.Fatal(err)
	}
	if err := uow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.ResolveRecoveredApproval(
		ctx, expiredPending.Approval.Request, expiredPending.Approval.Token.TokenID, "once",
	); !errors.Is(err, agentruntime.ErrInvalidCommand) {
		t.Fatalf("expired ResolveRecoveredApproval() error = %v, want ErrInvalidCommand", err)
	}
	uow, err = store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = uow.Rollback(ctx) }()
	storedApproval, err := uow.Approvals().LoadApproval(ctx, expiredPending.Approval.Request.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	if storedApproval.Status != "pending" {
		t.Fatalf("expired token mutated approval = %#v", storedApproval)
	}
	assertFile(t, path, "approved\n")
}

func TestResolvedApprovalOperationUsesLatestDurableDecisionAfterRestart(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	run, err := service.StartRun(ctx, "resume parallel approval")
	if err != nil {
		t.Fatal(err)
	}
	operations := []string{"turn:0:tool:0", "turn:0:tool:1"}
	for index, operationID := range operations {
		command := operationApprovalCommand(run, operationID, "scope-"+operationID)
		approval, token, err := service.requestApproval(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ResolveRecoveredApproval(ctx, approval, token.TokenID, "once"); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	execution := durable.Execution{
		ID: durable.ExecutionID(run.ExecutionID),
		Checkpoint: &durable.Checkpoint{Sequence: 2, Continuation: hyagent.Continuation{
			Phase: hyagent.ContinuationModelComplete,
			Messages: []message.Message{{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{
				{ID: "call-0", OperationID: operations[0]},
				{ID: "call-1", OperationID: operations[1]},
			}}},
		}},
	}
	service.approvalMu.Lock()
	delete(service.recoveredApprovals, run.RunID)
	service.approvalMu.Unlock()
	operationID, err := service.resolvedApprovalOperation(ctx, run.RunID, run.ExecutionID, execution)
	if err != nil {
		t.Fatal(err)
	}
	if operationID != operations[1] {
		t.Fatalf("resolved operation=%q, want latest durable decision %q", operationID, operations[1])
	}
}

func newRecoveredApprovalRun(run *Run) *Run {
	return &Run{
		RunID:        run.RunID,
		Goal:         run.Goal,
		TaskID:       run.TaskID,
		EnvelopeID:   run.EnvelopeID,
		LeaseID:      run.LeaseID,
		ExecutionID:  run.ExecutionID,
		TaskVersion:  run.TaskVersion,
		HolderID:     run.HolderID,
		pending:      make(map[string]PendingApproval),
		approvedOnce: make(map[string]string),
	}
}

func TestInvalidToolArgumentsDoNotCreateApproval(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	run, err := service.StartRun(ctx, "reject duplicate keys before approval")
	if err != nil {
		t.Fatal(err)
	}
	call := tool.Call{
		ID: "invalid-edit", OperationID: "turn:0:call:0", Name: ToolEditHashline,
		Arguments: json.RawMessage(`{"input":"first","input":"second"}`),
	}
	result, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil || result.Approval != nil || !result.Result.IsError ||
		!strings.Contains(result.Result.Content, "duplicate object key") {
		t.Fatalf("invalid call result=%+v error=%v", result, err)
	}
	command := operationApprovalCommand(run, call.OperationID, "")
	approvalID, _, deterministic, err := approvalRecordIDs(command)
	if err != nil || !deterministic {
		t.Fatalf("approval identity=%q deterministic=%v error=%v", approvalID, deterministic, err)
	}
	work, err := store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = work.Rollback(ctx) }()
	if _, err := work.Approvals().LoadApproval(ctx, approvalID); !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("invalid call approval load error=%v, want ErrNotFound", err)
	}
}

func TestSyntaxFailureReusesCurrentHashlineSnapshot(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	run, err := service.StartRun(ctx, "recover from rejected edit")
	if err != nil {
		t.Fatal(err)
	}

	read := executeRead(t, ctx, service, run, "read-before-failure", "note.txt")
	malformedArgs, _ := json.Marshal(map[string]string{"input": read.Header + "\n@@\n"})
	malformed := tool.Call{ID: "edit-malformed", Name: ToolEditHashline, Arguments: malformedArgs}
	pending, err := service.ExecuteTool(ctx, run, malformed, nil)
	if err != nil || pending.Approval == nil {
		t.Fatalf("malformed edit approval = %+v, error=%v", pending, err)
	}
	if err := service.ResolveApproval(ctx, run, malformed.ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	failed, err := service.ExecuteTool(ctx, run, malformed, nil)
	if err != nil || !failed.Executed || !failed.Result.IsError {
		t.Fatalf("malformed edit result = %+v, error=%v", failed, err)
	}
	for _, required := range []string{"Required OMP Hashline retry format:", "PUT N.=M:", "never use @@ hunks", "-old rows"} {
		if !strings.Contains(failed.Result.Content, required) {
			t.Fatalf("malformed edit result omitted %q: %q", required, failed.Result.Content)
		}
	}

	retryArgs, _ := json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 2.=2:\n+BETA")})
	retryCall := tool.Call{ID: "edit-after-syntax-error", Name: ToolEditHashline, Arguments: retryArgs}
	retry, err := service.ExecuteTool(ctx, run, retryCall, nil)
	if err != nil || retry.Approval == nil {
		t.Fatalf("syntax retry approval = %+v, error=%v", retry, err)
	}
	if err := service.ResolveApproval(ctx, run, retryCall.ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	applied, err := service.ExecuteTool(ctx, run, retryCall, nil)
	if err != nil || !applied.Executed || applied.Result.IsError {
		t.Fatalf("syntax retry apply = %+v, error=%v", applied, err)
	}
	assertFile(t, path, "alpha\nBETA\ngamma\n")
}

func TestDuplicateToolArgumentKeysFailBeforeApprovalOrExecution(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	run, err := service.StartRun(ctx, "reject ambiguous tool arguments")
	if err != nil {
		t.Fatal(err)
	}

	call := tool.Call{
		ID:        "duplicate-path",
		Name:      ToolWriteFile,
		Arguments: json.RawMessage(`{"path":"first.txt","content":"unsafe\n","path":"second.txt"}`),
	}
	execution, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil || !execution.Executed || execution.Approval != nil || !execution.Result.IsError {
		t.Fatalf("duplicate-key execution = %+v, error=%v", execution, err)
	}
	if execution.Result.ToolCallID != call.ID || execution.Result.Name != call.Name ||
		!strings.Contains(execution.Result.Content, `duplicate object key "path"`) {
		t.Fatalf("duplicate-key result = %+v", execution.Result)
	}
	for _, name := range []string{"first.txt", "second.txt"} {
		if _, err := os.Lstat(filepath.Join(workspace, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was created after ambiguous arguments: %v", name, err)
		}
	}
}

func TestToolArgumentValidationRejectsNestedDuplicateKeys(t *testing.T) {
	for _, arguments := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"edits":[{"old_text":"a","new_text":"b"}]}`)} {
		if err := validateToolArguments(arguments); err != nil {
			t.Fatalf("valid arguments %s rejected: %v", arguments, err)
		}
	}
	for _, arguments := range []json.RawMessage{
		json.RawMessage(`{"edits":[{"old_text":"a","old_text":"b"}]}`),
		json.RawMessage(`{"path":"a","\u0070ath":"b"}`),
		json.RawMessage(`[]`),
		json.RawMessage(`{"path":"a"} {"path":"b"}`),
	} {
		if err := validateToolArguments(arguments); err == nil {
			t.Fatalf("ambiguous arguments accepted: %s", arguments)
		}
	}
}

func TestGofmtCanFormatSamePathAgainAfterAnotherEdit(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc one(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	run, err := service.StartRun(ctx, "format after each edit")
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"path":"main.go"}`)

	format := func(callID string) {
		t.Helper()
		call := tool.Call{ID: callID, Name: ToolGofmt, Arguments: arguments}
		pending, err := service.ExecuteTool(ctx, run, call, nil)
		if err != nil || pending.Approval == nil {
			t.Fatalf("request %s approval: result=%+v err=%v", callID, pending, err)
		}
		if err := service.ResolveApproval(ctx, run, callID, ApprovalOnce, "user"); err != nil {
			t.Fatal(err)
		}
		executed, err := service.ExecuteTool(ctx, run, call, nil)
		if err != nil || !executed.Executed || executed.Result.IsError {
			t.Fatalf("execute %s: result=%+v err=%v", callID, executed, err)
		}
	}

	format("gofmt-1")
	if err := os.WriteFile(path, []byte("package main\nfunc two(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	format("gofmt-2")
	assertFile(t, path, "package main\n\nfunc two() {}\n")
}

func TestConcurrentRunsKeepTasksLeasesAndApprovalsIsolated(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("alpha\nbeta\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })

	type startOutcome struct {
		run *Run
		err error
	}
	started := make(chan startOutcome, 2)
	for _, request := range []string{"edit one", "edit two"} {
		request := request
		go func() {
			run, startErr := service.StartRun(ctx, request)
			started <- startOutcome{run: run, err: startErr}
		}()
	}
	runs := make([]*Run, 0, 2)
	for range 2 {
		outcome := <-started
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		runs = append(runs, outcome.run)
	}
	if runs[0].RunID == runs[1].RunID || runs[0].TaskID == runs[1].TaskID ||
		runs[0].LeaseID != "" || runs[1].LeaseID != "" {
		t.Fatalf("run identities or eager leases overlap: %#v %#v", runs[0], runs[1])
	}

	calls := make([]tool.Call, 2)
	for index, name := range []string{"one.txt", "two.txt"} {
		read := executeRead(t, ctx, service, runs[index], "read-"+name, name)
		arguments, _ := json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 2.=2:\n+UPDATED")})
		calls[index] = tool.Call{ID: "shared-call-id", Name: ToolEditHashline, Arguments: arguments}
	}
	type executeOutcome struct {
		index  int
		result ExecutionResult
		err    error
	}
	executed := make(chan executeOutcome, 2)
	for index := range runs {
		index := index
		go func() {
			result, executeErr := service.ExecuteTool(ctx, runs[index], calls[index], nil)
			executed <- executeOutcome{index: index, result: result, err: executeErr}
		}()
	}
	for range 2 {
		outcome := <-executed
		if outcome.err != nil || outcome.result.Approval == nil || outcome.result.Executed {
			t.Fatalf("run %d approval = %#v, %v", outcome.index, outcome.result, outcome.err)
		}
	}
	if len(runs[0].pending) != 1 || len(runs[1].pending) != 1 {
		t.Fatalf("pending approvals crossed runs: %v %v", runs[0].pending, runs[1].pending)
	}

	if err := service.ResolveApproval(ctx, runs[0], calls[0].ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	if _, ok := runs[1].approvedOnce[calls[1].ID]; ok {
		t.Fatal("approval from first run leaked into second run")
	}
	first, err := service.ExecuteTool(ctx, runs[0], calls[0], nil)
	if err != nil || !first.Executed || first.Result.IsError {
		t.Fatalf("first execution = %#v, %v", first, err)
	}
	stillPending, err := service.ExecuteTool(ctx, runs[1], calls[1], nil)
	if err != nil || stillPending.Executed || stillPending.Approval == nil {
		t.Fatalf("second run lost its approval boundary: %#v, %v", stillPending, err)
	}
	if err := service.ResolveApproval(ctx, runs[1], calls[1].ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	second, err := service.ExecuteTool(ctx, runs[1], calls[1], nil)
	if err != nil || !second.Executed || second.Result.IsError {
		t.Fatalf("second execution = %#v, %v", second, err)
	}

	completed := make(chan error, 2)
	for _, run := range runs {
		run := run
		go func() { completed <- service.CompleteRun(ctx, run, "done", nil) }()
	}
	for range 2 {
		if err := <-completed; err != nil {
			t.Fatal(err)
		}
	}
	assertFile(t, filepath.Join(workspace, "one.txt"), "alpha\nUPDATED\n")
	assertFile(t, filepath.Join(workspace, "two.txt"), "alpha\nUPDATED\n")
}

func TestConcurrentRunsWithFileSQLiteDoNotLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "azem.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })

	const runCount = 8
	start := make(chan struct{})
	outcomes := make(chan error, runCount)
	for index := range runCount {
		go func() {
			<-start
			_, startErr := service.StartRun(ctx, fmt.Sprintf("child %d", index))
			outcomes <- startErr
		}()
	}
	close(start)
	for range runCount {
		if err := <-outcomes; err != nil {
			t.Fatalf("concurrent StartRun: %v", err)
		}
	}
}

func TestDeniedEditAndSymlinkEscape(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	external := t.TempDir()
	outside := filepath.Join(external, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "safe.txt")
	if err := os.WriteFile(path, []byte("safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx) })
	run, err := service.StartRun(ctx, "boundary test")
	if err != nil {
		t.Fatal(err)
	}

	escapeArgs, _ := json.Marshal(map[string]string{"path": "escape.txt"})
	escape, err := service.ExecuteTool(ctx, run, tool.Call{ID: "read-escape", Name: ToolReadFile, Arguments: escapeArgs}, nil)
	if err != nil {
		t.Fatalf("escape read returned Go error: %v", err)
	}
	if !escape.Executed || !escape.Result.IsError {
		t.Fatalf("symlink escape result = %+v", escape)
	}

	read := executeRead(t, ctx, service, run, "read-safe", "safe.txt")
	editArgs, _ := json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 1.=1:\n+changed")})
	call := tool.Call{ID: "edit-denied", Name: ToolEditHashline, Arguments: editArgs}
	pending, err := service.ExecuteTool(ctx, run, call, nil)
	if err != nil || pending.Approval == nil {
		t.Fatalf("edit approval: result=%+v err=%v", pending, err)
	}
	if err := service.ResolveApproval(ctx, run, call.ID, ApprovalDenied, "user"); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "safe\n")
}

func TestRunnerRecoversFromSQLite(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database := filepath.Join(t.TempDir(), "azem.db")
	store, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartRun(ctx, "recover me")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	recoveredService, err := NewService(reopened, root)
	if err != nil {
		t.Fatal(err)
	}
	defer recoveredService.Close(ctx)
	projection, err := recoveredService.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if projection.Run.ID != run.RunID || len(projection.Tasks) == 0 {
		t.Fatalf("projection = %+v", projection)
	}
}

func TestResumeRunReacquiresRecoveredTaskWithoutChangingRunID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	notePath := filepath.Join(root, "note.txt")
	if err := os.WriteFile(notePath, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(t.TempDir(), "resume.db")
	store, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewService(store, root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := first.StartRun(ctx, "resume same logical run")
	if err != nil {
		t.Fatal(err)
	}
	read := executeRead(t, ctx, first, run, "read-before-resume", "note.txt")
	editArguments, _ := json.Marshal(map[string]string{"input": ompTestPatch(read.Header, "PUT 1.=1:\n+after")})
	editCall := tool.Call{ID: "edit-before-resume", Name: ToolEditHashline, Arguments: editArguments}
	pending, err := first.ExecuteTool(ctx, run, editCall, nil)
	if err != nil || pending.Approval == nil {
		t.Fatalf("resume setup approval=%+v err=%v", pending, err)
	}
	if err := first.ResolveApproval(ctx, run, editCall.ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	if executed, err := first.ExecuteTool(ctx, run, editCall, nil); err != nil || !executed.Executed || executed.Result.IsError {
		t.Fatalf("resume setup execution=%+v err=%v", executed, err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reopened.PrepareRecovery(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewService(reopened, root)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close(ctx)
	if _, err := recovered.Recover(ctx, run.RunID); err != nil {
		t.Fatal(err)
	}
	resumed, err := recovered.ResumeRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RunID != run.RunID || resumed.TaskID != run.TaskID || resumed.EnvelopeID == "" || resumed.LeaseID != "" {
		t.Fatalf("resumed run=%+v original=%+v", resumed, run)
	}
	if err := recovered.CompleteRun(ctx, resumed, "done after recovery", nil); err != nil {
		t.Fatal(err)
	}
}

type runtimeOwnerBlockingDriver struct {
	started chan struct{}
}

func (d *runtimeOwnerBlockingDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "runtime-owner-blocking", Models: []string{"blocking"}}
}

func (d *runtimeOwnerBlockingDriver) Stream(ctx context.Context, _ hyprovider.Request) (hyprovider.Stream, error) {
	d.started <- struct{}{}
	return &claimBlockingStream{ctx: ctx}, nil
}

func sealTestExecutionProfile(t *testing.T, ctx context.Context, service *Service, run *Run, model string) {
	t.Helper()
	err := service.SealExecutionProfile(ctx, run, agentruntime.ExecutableProfile{
		Provider: "test", AccountID: "test-account", RawModel: model, Model: model, Reasoning: "none",
		ActiveSkills: []string{}, ToolSetHash: "test-tool-set", ToolProfileHash: "test-tool-profile:" + model,
		StaticIdentity: "test-static:" + model, WorkspaceAnchor: service.workspaceRoot,
		PromptFingerprint: "test-prompt", ToolSchemaFingerprint: "test-tool-schema",
	})
	if err != nil {
		t.Fatalf("SealExecutionProfile: %v", err)
	}
}

func TestServiceKeepsOneDurableRuntimeOwnerForItsLifetime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "runtime-owner.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs := make([]*Run, 2)
	for index := range runs {
		runs[index], err = service.StartRun(ctx, fmt.Sprintf("runtime owner %d", index))
		if err != nil {
			t.Fatal(err)
		}
		sealTestExecutionProfile(t, ctx, service, runs[index], "blocking")
	}
	driver := &runtimeOwnerBlockingDriver{started: make(chan struct{}, len(runs))}
	done := make(chan error, len(runs))
	for _, run := range runs {
		go func(run *Run) {
			_, executeErr := service.ExecuteRun(ctx, run, hyagent.Engine{Provider: driver, Model: "blocking"}, nil)
			done <- executeErr
		}(run)
	}
	for range runs {
		select {
		case <-driver.started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	var active, owners int
	var owner string
	if err := store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT lease_owner), MIN(lease_owner)
		FROM agent_executions
		WHERE lease_owner<>''`,
	).Scan(&active, &owners, &owner); err != nil {
		t.Fatal(err)
	}
	if active != len(runs) || owners != 1 || owner != service.runtimeOwnerID {
		t.Fatalf("active=%d owners=%d owner=%q runtime owner=%q", active, owners, owner, service.runtimeOwnerID)
	}
	for _, run := range runs {
		if err := service.CancelRun(ctx, run, errors.New("test complete")); err != nil {
			t.Fatal(err)
		}
	}
	for range runs {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClosedServiceRejectsNewRuntimeWork(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartRun(ctx, "must not persist"); !errors.Is(err, durable.ErrClosed) {
		t.Fatalf("StartRun after Close error=%v, want durable.ErrClosed", err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestConcurrentExecuteIsBusyAndExplicitCancelStaysCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	run, err := service.StartRun(ctx, "cancel exactly once")
	if err != nil {
		t.Fatal(err)
	}
	sealTestExecutionProfile(t, ctx, service, run, "blocking")
	blocking := &runtimeOwnerBlockingDriver{started: make(chan struct{}, 1)}
	type executionResult struct {
		outcome ExecutionOutcome
		err     error
	}
	firstDone := make(chan executionResult, 1)
	go func() {
		outcome, executeErr := service.ExecuteRun(ctx, run, hyagent.Engine{Provider: blocking, Model: "blocking"}, nil)
		firstDone <- executionResult{outcome: outcome, err: executeErr}
	}()
	select {
	case <-blocking.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, busyErr := service.ExecuteRun(ctx, run, hyagent.Engine{
		Provider: scripted.New([]hyprovider.Event{{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}}),
		Model:    "must-not-run",
	}, nil)
	var unavailable *TaskExecutionUnavailableError
	if !errors.As(busyErr, &unavailable) {
		t.Fatalf("concurrent ExecuteRun error=%v, want TaskExecutionUnavailableError", busyErr)
	}
	if err := service.CancelRun(ctx, run, errors.New("user stopped")); err != nil {
		t.Fatal(err)
	}
	var first executionResult
	select {
	case first = <-firstDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if first.err != nil || first.outcome.State != ExecutionCancelled {
		t.Fatalf("cancelled execution outcome=%+v error=%v", first.outcome, first.err)
	}
	binding, err := store.LoadExecutionBinding(ctx, run.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.State != agentruntime.ExecutionBindingCancelled {
		t.Fatalf("binding state=%q", binding.State)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusCancelled || projection.Tasks[run.TaskID].Status != agentruntime.TaskStatusCancelled {
		t.Fatalf("cancelled projection=%+v", projection)
	}
	unexpected := &runtimeOwnerBlockingDriver{started: make(chan struct{}, 1)}
	replayed, err := service.ExecuteRun(ctx, run, hyagent.Engine{Provider: unexpected, Model: "must-not-run"}, nil)
	if err != nil || replayed.State != ExecutionCancelled {
		t.Fatalf("re-execute cancelled outcome=%+v error=%v", replayed, err)
	}
	select {
	case <-unexpected.started:
		t.Fatal("cancelled binding opened a provider stream")
	default:
	}
}

func TestCallerCancellationKeepsRunNonterminalAndRequiresReconcile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	run, err := service.StartRun(ctx, "resume after caller cancellation")
	if err != nil {
		t.Fatal(err)
	}
	sealTestExecutionProfile(t, ctx, service, run, "blocking")
	blocking := &runtimeOwnerBlockingDriver{started: make(chan struct{}, 1)}
	runCtx, cancelRun := context.WithCancel(ctx)
	type executionResult struct {
		outcome ExecutionOutcome
		err     error
	}
	done := make(chan executionResult, 1)
	go func() {
		outcome, executeErr := service.ExecuteRun(runCtx, run, hyagent.Engine{Provider: blocking, Model: "blocking"}, nil)
		done <- executionResult{outcome: outcome, err: executeErr}
	}()
	select {
	case <-blocking.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelRun()
	var cancelled executionResult
	select {
	case cancelled = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !errors.Is(cancelled.err, context.Canceled) || cancelled.outcome.State != ExecutionSuspended {
		t.Fatalf("caller-cancelled outcome=%+v error=%v", cancelled.outcome, cancelled.err)
	}
	binding, err := store.LoadExecutionBinding(ctx, run.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.State != agentruntime.ExecutionBindingSuspended {
		t.Fatalf("binding state=%q", binding.State)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusRunning {
		t.Fatalf("caller cancellation terminalized run as %q", projection.Run.Status)
	}
	resumed, err := service.ResumeRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	unexpected := &runtimeOwnerBlockingDriver{started: make(chan struct{}, 1)}
	reconciled, err := service.ExecuteRun(ctx, resumed, hyagent.Engine{Provider: unexpected, Model: "blocking"}, nil)
	if !errors.Is(err, durable.ErrReconcileRequired) ||
		reconciled.State != ExecutionSuspended ||
		reconciled.Suspension == nil ||
		reconciled.Suspension.Kind != SuspensionReconciliation {
		t.Fatalf("reconcile outcome=%+v error=%v", reconciled, err)
	}
	select {
	case <-unexpected.started:
		t.Fatal("unknown model attempt reopened the provider stream")
	default:
	}
	binding, err = store.LoadExecutionBinding(ctx, run.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.State != agentruntime.ExecutionBindingReconcileRequired {
		t.Fatalf("binding state after resume=%q", binding.State)
	}
	projection, err = service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusReconcileRequired ||
		projection.Tasks[run.TaskID].Status != agentruntime.TaskStatusReconcileRequired {
		t.Fatalf("reconcile projection=%+v", projection)
	}
	attempts, err := store.ListDurableReconcileAttempts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("durable reconcile attempts=%+v", attempts)
	}
	attempt := attempts[0]
	if attempt.ExecutionID != run.ExecutionID || attempt.OperationID == "" || attempt.AttemptNumber != 1 ||
		attempt.AttemptVersion == 0 || attempt.AttemptKind != string(durable.AttemptKindModel) ||
		attempt.CheckpointSequence == 0 || attempt.ContinuationPhase == "" {
		t.Fatalf("durable reconcile identity=%+v", attempt)
	}
	if err := service.ResolveReconcileAttempt(ctx, attempt.AttemptID, agentruntime.ActionAttemptSucceeded, nil); err == nil {
		t.Fatal("model reconciliation without a complete event sequence succeeded")
	}
	if err := service.ResolveReconcileAttempt(ctx, attempt.AttemptID, agentruntime.ActionAttemptRetry, nil); err != nil {
		t.Fatal(err)
	}
	// A lost action response may replay the exact resolution. The durable
	// attempt version and decision identify the same reconciliation receipt.
	if err := service.ResolveReconcileAttempt(ctx, attempt.AttemptID, agentruntime.ActionAttemptRetry, nil); err != nil {
		t.Fatalf("replay exact reconciliation: %v", err)
	}
	if remaining, err := store.ListDurableReconcileAttempts(ctx); err != nil || len(remaining) != 0 {
		t.Fatalf("remaining durable attempts=%+v error=%v", remaining, err)
	}
	binding, err = store.LoadExecutionBinding(ctx, run.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.State != agentruntime.ExecutionBindingSuspended {
		t.Fatalf("binding state after reconciliation=%q", binding.State)
	}
	projection, err = service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusRunning ||
		projection.Tasks[run.TaskID].Status != agentruntime.TaskStatusDispatched {
		t.Fatalf("reconciled projection=%+v", projection)
	}
	resumed, err = service.ResumeRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	stale := *resumed
	stale.resumeTarget.CheckpointSequence++
	mismatched, mismatchErr := service.ExecuteRun(ctx, &stale, hyagent.Engine{
		Provider: scripted.New([]hyprovider.Event{
			{Kind: hyprovider.EventTextDelta, Text: "must not run", TextPhase: hyprovider.TextPhaseFinalAnswer},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		}),
		Model: "blocking",
	}, nil)
	if !errors.Is(mismatchErr, durable.ErrResumeTargetMismatch) || mismatched.State != ExecutionSuspended {
		t.Fatalf("stale resume target outcome=%+v error=%v", mismatched, mismatchErr)
	}
	resumed, err = service.ResumeRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.ExecuteRun(ctx, resumed, hyagent.Engine{
		Provider: scripted.New([]hyprovider.Event{
			{Kind: hyprovider.EventTextDelta, Text: "retried", TextPhase: hyprovider.TextPhaseFinalAnswer},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		}),
		Model: "blocking",
	}, nil)
	if err != nil || completed.State != ExecutionCompleted {
		t.Fatalf("resumed reconciled execution=%+v error=%v", completed, err)
	}
}

func TestNewServiceCanStartRunAfterProcessRestart(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "azem.db")
	workspace := t.TempDir()

	firstStore, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewService(firstStore, workspace)
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := first.StartRun(ctx, "first process")
	if err != nil {
		t.Fatalf("first StartRun: %v", err)
	}
	if err := first.CompleteRun(ctx, firstRun, "done", nil); err != nil {
		t.Fatalf("complete first run: %v", err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}

	secondStore, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewService(secondStore, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)
	if _, err := second.StartRun(ctx, "second process"); err != nil {
		t.Fatalf("second StartRun: %v", err)
	}
}

func TestCompleteRunPersistsTerminalState(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	run, err := service.StartRun(ctx, "finish")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteRun(ctx, run, "done", nil); err != nil {
		t.Fatal(err)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status = %q", projection.Run.Status)
	}
}

func TestStartRunPersistsManifestBeforeDurableExecution(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	run, err := service.StartRunWithMetadata(ctx, "persist first", map[string]string{"session_id": "session-manifest"}, RunExecutionPolicy{
		AgentID:      "child-agent",
		AgentVersion: "profile-v1",
		Budget:       &agentruntime.TaskBudget{MaxSteps: 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.LoadExecutionBinding(ctx, run.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.State != agentruntime.ExecutionBindingPending ||
		binding.Manifest.RunID != run.RunID ||
		binding.Manifest.SessionID != "session-manifest" ||
		binding.Manifest.AgentID != "child-agent" ||
		binding.Manifest.AgentVersion != "profile-v1" ||
		binding.Manifest.Prompt != "persist first" ||
		binding.Manifest.Budget.MaxSteps != 7 ||
		binding.Manifest.ProfileHash == "" ||
		binding.Manifest.ProfileHash != binding.ProfileHash {
		t.Fatalf("persisted binding=%+v", binding)
	}
	var executions int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_executions WHERE execution_id=?`, run.ExecutionID).Scan(&executions); err != nil {
		t.Fatal(err)
	}
	if executions != 0 {
		t.Fatalf("durable execution rows=%d before ExecuteRun, want 0", executions)
	}
	profile := agentruntime.ExecutableProfile{
		Provider: "test", AccountID: "account-1", RawModel: "family", Model: "model-1", Reasoning: "high",
		ActiveSkills: []string{}, ToolSetHash: "tools", ToolProfileHash: "tool-profile",
		PlanMode: true, ApprovedPlanID: "plan-1", DisableSubagents: true,
		StaticIdentity: "static", WorkspaceAnchor: service.workspaceRoot,
		PromptFingerprint: "prompt", ToolSchemaFingerprint: "schemas",
	}
	if err := service.SealExecutionProfile(ctx, run, profile); err != nil {
		t.Fatal(err)
	}
	if err := service.SealExecutionProfile(ctx, run, profile); err != nil {
		t.Fatalf("idempotent profile seal: %v", err)
	}
	sealed, err := service.LoadRunExecutionManifest(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !sealed.Sealed || sealed.Version != agentruntime.ExecutionManifestVersion ||
		sealed.Provider != profile.Provider || sealed.AccountID != profile.AccountID ||
		sealed.RawModel != profile.RawModel || sealed.Model != profile.Model ||
		sealed.Reasoning != profile.Reasoning || sealed.ToolSetHash != profile.ToolSetHash ||
		sealed.ToolProfileHash != profile.ToolProfileHash || sealed.ApprovedPlanID != profile.ApprovedPlanID ||
		sealed.StaticIdentity != profile.StaticIdentity || sealed.WorkspaceAnchor != profile.WorkspaceAnchor ||
		sealed.PromptFingerprint != profile.PromptFingerprint || sealed.ToolSchemaFingerprint != profile.ToolSchemaFingerprint ||
		sealed.ActiveSkills == nil {
		t.Fatalf("sealed manifest=%+v", sealed)
	}
	changed := profile
	changed.Model = "model-2"
	if err := service.SealExecutionProfile(ctx, run, changed); !errors.Is(err, agentruntime.ErrConflict) {
		t.Fatalf("changed profile seal error=%v, want conflict", err)
	}
}

type requestBudgetCaptureContext struct {
	request hyagent.Request
}

func (capture *requestBudgetCaptureContext) Build(_ context.Context, request hyagent.Request) ([]message.Message, error) {
	capture.request = request
	return []message.Message{
		message.NewText(message.RoleSystem, "budget capture"),
		message.NewText(message.RoleUser, request.Prompt),
	}, nil
}

func (*requestBudgetCaptureContext) Compact(_ context.Context, history []message.Message) ([]message.Message, error) {
	return message.CloneMessages(history), nil
}

func TestExecuteRunMapsManifestBudgetToAgentRequest(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	budget := agentruntime.TaskBudget{
		MaxTokens: 55, MaxToolCalls: 3, MaxSteps: 4, MaxWallClock: time.Minute,
	}
	run, err := service.StartRunWithMetadata(ctx, "budget mapping", nil, RunExecutionPolicy{Budget: &budget})
	if err != nil {
		t.Fatal(err)
	}
	sealTestExecutionProfile(t, ctx, service, run, "test-model")
	capture := &requestBudgetCaptureContext{}
	outcome, err := service.ExecuteRun(ctx, run, hyagent.Engine{
		Provider: scripted.New([]hyprovider.Event{
			{Kind: hyprovider.EventTextDelta, Text: "done", TextPhase: hyprovider.TextPhaseFinalAnswer},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete, Usage: hyprovider.Usage{InputTokens: 6, OutputTokens: 4, TotalTokens: 10}},
		}),
		Model: "test-model", ContextBuilder: capture,
	}, nil)
	if err != nil || outcome.State != ExecutionCompleted {
		t.Fatalf("outcome=%+v error=%v", outcome, err)
	}
	if capture.request.Budget == nil ||
		capture.request.Budget.MaxTokens != budget.MaxTokens ||
		capture.request.Budget.MaxToolCalls != budget.MaxToolCalls ||
		capture.request.Budget.MaxSteps != budget.MaxSteps ||
		capture.request.Budget.MaxWallClock != budget.MaxWallClock {
		t.Fatalf("agent request budget=%+v, want %+v", capture.request.Budget, budget)
	}
}

func TestStartRunPersistsOutputSchema(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	schema := json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`)
	run, err := service.StartRunWithMetadata(ctx, "structured child", nil, RunExecutionPolicy{OutputSchema: schema})
	if err != nil {
		t.Fatal(err)
	}
	task, err := loadTaskForTest(ctx, service, run.RunID, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if string(task.OutputSchema) != string(schema) {
		t.Fatalf("task output schema = %s", task.OutputSchema)
	}
	if err := service.CompleteRun(ctx, run, "not executed", errors.New("not executed")); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteRunUsesCoordinatorLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	claim := agentruntime.ResourceClaimSpec{Key: "workspace:test", Mode: agentruntime.ResourceClaimExclusive}
	run, err := service.StartRunWithMetadata(ctx, "execute through coordinator", nil, RunExecutionPolicy{
		ResourceClaims: []agentruntime.ResourceClaimSpec{claim},
	})
	if err != nil {
		t.Fatal(err)
	}
	sealTestExecutionProfile(t, ctx, service, run, "test-model")
	if run.LeaseID != "" {
		t.Fatalf("StartRun eagerly acquired lease %q", run.LeaseID)
	}
	task, err := loadTaskForTest(ctx, service, run.RunID, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(task.ResourceClaims, []agentruntime.ResourceClaimSpec{claim}) {
		t.Fatalf("task resource claims=%#v", task.ResourceClaims)
	}
	engine := hyagent.Engine{
		Provider: scripted.New([]hyprovider.Event{
			{Kind: hyprovider.EventTextDelta, Text: "done"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		}),
		Model: "test-model",
	}
	outcome, err := service.ExecuteRun(ctx, run, engine, nil)
	if err != nil {
		t.Fatalf("ExecuteRun: %v", err)
	}
	if outcome.State != ExecutionCompleted || run.LeaseID == "" {
		t.Fatalf("outcome=%+v run=%+v", outcome, run)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status=%s", projection.Run.Status)
	}
	var active int
	err = store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_executions WHERE execution_id=? AND lease_owner<>''`, run.ExecutionID).Scan(&active)
	if err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("active lease count=%d, want 0", active)
	}
	claims, err := service.ListResourceClaims(ctx, agentruntime.ResourceClaimSelector{RunIDs: []string{run.RunID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].State != agentruntime.ResourceClaimReleased {
		t.Fatalf("resource claims=%#v", claims)
	}
}

func TestExecuteRunPersistsAgentFailureAsTerminalData(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	run, err := service.StartRunWithMetadata(ctx, "invalid structured output", nil, RunExecutionPolicy{
		OutputSchema: json.RawMessage(`{"type":"bogus"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	sealTestExecutionProfile(t, ctx, service, run, "test-model")
	outcome, err := service.ExecuteRun(ctx, run, hyagent.Engine{
		Provider: scripted.New([]hyprovider.Event{
			{Kind: hyprovider.EventTextDelta, Text: "partial answer"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		}),
		Model: "test-model",
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteRun infrastructure error=%v", err)
	}
	if outcome.State != ExecutionFailed || outcome.Failure == nil || outcome.Failure.Kind != hyagent.FailureKindSchemaInvalid {
		t.Fatalf("outcome=%+v", outcome)
	}
	binding, err := store.LoadExecutionBinding(ctx, run.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.State != agentruntime.ExecutionBindingFailed {
		t.Fatalf("binding state=%q", binding.State)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusFailed ||
		projection.Run.Metadata["azem.agent_failure_kind"] != string(hyagent.FailureKindSchemaInvalid) ||
		projection.Run.Metadata["azem.agent_stop_reason"] != string(hyprovider.StopReasonComplete) ||
		projection.Run.Metadata["azem.agent_usage"] == "" {
		t.Fatalf("recovered run=%+v", projection.Run)
	}
	task := projection.Tasks[run.TaskID]
	if task.Result == nil || task.Result.Status != agentruntime.ReportStatusFailed || task.Result.Kind != string(hyagent.FailureKindSchemaInvalid) {
		t.Fatalf("recovered task=%+v", task)
	}
}

func TestExecuteRunResourceClaimConflictCanRetryAfterOwnerCancels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "claims.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	policy := RunExecutionPolicy{ResourceClaims: []agentruntime.ResourceClaimSpec{{
		Key: "workspace:shared", Mode: agentruntime.ResourceClaimExclusive,
	}}}
	owner, err := service.StartRunWithMetadata(ctx, "hold workspace", nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	waiter, err := service.StartRunWithMetadata(ctx, "wait for workspace", nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	sealTestExecutionProfile(t, ctx, service, owner, "blocking")
	sealTestExecutionProfile(t, ctx, service, waiter, "waiter")
	blocking := &claimBlockingDriver{started: make(chan struct{})}
	ownerDone := make(chan error, 1)
	go func() {
		_, executeErr := service.ExecuteRun(ctx, owner, hyagent.Engine{
			Provider: blocking, Model: "blocking",
		}, nil)
		ownerDone <- executeErr
	}()
	select {
	case <-blocking.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err = service.ExecuteRun(ctx, waiter, hyagent.Engine{
		Provider: scripted.New([]hyprovider.Event{
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		}),
		Model: "waiter",
	}, nil)
	var unavailable *TaskExecutionUnavailableError
	if !errors.As(err, &unavailable) ||
		unavailable.ResourceClaims.Reason != agentruntime.ResourceClaimDeniedConflict {
		t.Fatalf("waiter execution error=%v", err)
	}
	if cancelErr := service.CancelRun(ctx, owner, errors.New("release workspace")); cancelErr != nil {
		t.Fatalf("cancel owner: %v", cancelErr)
	}
	select {
	case <-ownerDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	outcome, err := service.ExecuteRun(ctx, waiter, hyagent.Engine{
		Provider: scripted.New([]hyprovider.Event{
			{Kind: hyprovider.EventTextDelta, Text: "acquired"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		}),
		Model: "waiter",
	}, nil)
	if err != nil || outcome.State != ExecutionCompleted || outcome.Result.Text != "acquired" {
		t.Fatalf("retried waiter outcome=%+v error=%v", outcome, err)
	}
}

type claimBlockingDriver struct {
	once    sync.Once
	started chan struct{}
}

func (d *claimBlockingDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "blocking", Models: []string{"blocking"}}
}

func (d *claimBlockingDriver) Stream(ctx context.Context, _ hyprovider.Request) (hyprovider.Stream, error) {
	d.once.Do(func() { close(d.started) })
	return &claimBlockingStream{ctx: ctx}, nil
}

type claimBlockingStream struct {
	ctx context.Context
}

func (s *claimBlockingStream) Recv() (hyprovider.Event, error) {
	<-s.ctx.Done()
	return hyprovider.Event{}, s.ctx.Err()
}

func (*claimBlockingStream) Close() error { return nil }

func TestCompleteRunReportsSetupResultWithoutEagerLease(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Durable v0.16 execution leases are runtime-owned and not used by setup-only completion.
	defer service.Close(ctx)
	run, err := service.StartRun(ctx, "setup-only task")
	if err != nil {
		t.Fatal(err)
	}
	var leases int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM leases WHERE run_id=?`, run.RunID).Scan(&leases); err != nil {
		t.Fatal(err)
	}
	if leases != 0 || run.LeaseID != "" {
		t.Fatalf("eager leases=%d run=%+v", leases, run)
	}
	if err := service.CompleteRun(ctx, run, "done", nil); err != nil {
		t.Fatal(err)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status=%s", projection.Run.Status)
	}
}

func TestCompleteRunPersistsProviderFailureAfterLeaseRefresh(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Provider failures complete through the app-owned run state without an eager execution lease.
	defer service.Close(ctx)
	run, err := service.StartRun(ctx, "provider failure")
	if err != nil {
		t.Fatal(err)
	}
	failure := fmt.Errorf("provider connection failed")
	if err := service.CompleteRun(ctx, run, "", failure); err != nil {
		t.Fatal(err)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != agentruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want failed", projection.Run.Status)
	}
}

func loadTaskForTest(ctx context.Context, service *Service, runID, taskID string) (agentruntime.Task, error) {
	tasks, err := service.ListTasks(ctx, runID)
	if err != nil {
		return agentruntime.Task{}, err
	}
	for _, task := range tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return agentruntime.Task{}, fmt.Errorf("task %s not found in run %s", taskID, runID)
}

func executeRead(t *testing.T, ctx context.Context, service *Service, run *Run, id string, path string) ReadFileToolResult {
	t.Helper()
	arguments, _ := json.Marshal(map[string]string{"path": path})
	execution, err := service.ExecuteTool(ctx, run, tool.Call{ID: id, Name: ToolReadFile, Arguments: arguments}, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !execution.Executed || execution.Result.IsError {
		t.Fatalf("read result = %+v", execution)
	}
	var result ReadFileToolResult
	if err := json.Unmarshal(execution.Result.Structured, &result); err != nil {
		t.Fatalf("decode read: %v", err)
	}
	return result
}

func assertFile(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
}

func assertApprovalDecider(t *testing.T, service *Service, runID, want string) {
	t.Helper()
	events, err := service.ListEvents(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type != agentruntime.EventApprovalDecided {
			continue
		}
		if got := events[index].Payload["decidedBy"]; got != want {
			t.Fatalf("approval decider=%v, want %q", got, want)
		}
		return
	}
	t.Fatalf("run %s has no approval decision", runID)
}
