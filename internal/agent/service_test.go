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

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/coding"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/provider/scripted"
	"github.com/Viking602/venat/tool"
	hyworker "github.com/Viking602/venat/worker"

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
	patch := read.Header + "\nreplace 2:\n+BETA\n"
	arguments, _ := json.Marshal(map[string]string{"input": patch})
	call := tool.Call{ID: "edit-1", Name: coding.ToolEditHashline, Arguments: arguments}
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

	staleArgs, _ := json.Marshal(map[string]string{"input": read.Header + "\nreplace 2:\n+STALE\n"})
	staleCall := tool.Call{ID: "edit-stale", Name: coding.ToolEditHashline, Arguments: staleArgs}
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
	arguments, _ := json.Marshal(map[string]string{"input": read.Header + "\nreplace 1:\n+approved\n"})
	call := tool.Call{
		ID: "provider-before-crash", OperationID: "turn:1:call:0",
		Name: coding.ToolEditHashline, Arguments: arguments,
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
	arguments, _ = json.Marshal(map[string]string{"input": read.Header + "\nreplace 1:\n+denied-write\n"})
	deniedCall := tool.Call{
		ID: "provider-denied-before-crash", OperationID: "turn:3:call:0",
		Name: coding.ToolEditHashline, Arguments: arguments,
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
	arguments, _ = json.Marshal(map[string]string{"input": read.Header + "\nreplace 1:\n+expired-write\n"})
	expiredCall := tool.Call{
		ID: "provider-expired-before-crash", OperationID: "turn:5:call:0",
		Name: coding.ToolEditHashline, Arguments: arguments,
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
	); !errors.Is(err, api.ErrInvalidCommand) {
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

func newRecoveredApprovalRun(run *Run) *Run {
	return &Run{
		RunID:        run.RunID,
		Goal:         run.Goal,
		TaskID:       run.TaskID,
		EnvelopeID:   run.EnvelopeID,
		LeaseID:      run.LeaseID,
		TaskVersion:  run.TaskVersion,
		HolderID:     run.HolderID,
		pending:      make(map[string]PendingApproval),
		approvedOnce: make(map[string]string),
	}
}

func TestFailedEditRequiresReadBeforeNextEdit(t *testing.T) {
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
	malformed := tool.Call{ID: "edit-malformed", Name: coding.ToolEditHashline, Arguments: malformedArgs}
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
	for _, required := range []string{"Required hashline retry format:", "replace N..M:", "Never use @@", "-old rows"} {
		if !strings.Contains(failed.Result.Content, required) {
			t.Fatalf("malformed edit result omitted %q: %q", required, failed.Result.Content)
		}
	}

	validArgs, _ := json.Marshal(map[string]string{"input": read.Header + "\nreplace 2:\n+BETA\n"})
	blocked, err := service.ExecuteTool(ctx, run, tool.Call{ID: "edit-without-reread", Name: coding.ToolEditHashline, Arguments: validArgs}, nil)
	if err != nil || !blocked.Executed || !blocked.Result.IsError || blocked.Approval != nil {
		t.Fatalf("edit without re-read = %+v, error=%v", blocked, err)
	}
	if !strings.Contains(blocked.Result.Content, coding.ToolReadFile) {
		t.Fatalf("blocked edit did not require %s: %q", coding.ToolReadFile, blocked.Result.Content)
	}

	refreshed := executeRead(t, ctx, service, run, "read-after-failure", "note.txt")
	retryArgs, _ := json.Marshal(map[string]string{"input": refreshed.Header + "\nreplace 2:\n+BETA\n"})
	retryCall := tool.Call{ID: "edit-after-reread", Name: coding.ToolEditHashline, Arguments: retryArgs}
	retry, err := service.ExecuteTool(ctx, run, retryCall, nil)
	if err != nil || retry.Approval == nil {
		t.Fatalf("edit after re-read = %+v, error=%v", retry, err)
	}
	if err := service.ResolveApproval(ctx, run, retryCall.ID, ApprovalOnce, "user"); err != nil {
		t.Fatal(err)
	}
	applied, err := service.ExecuteTool(ctx, run, retryCall, nil)
	if err != nil || !applied.Executed || applied.Result.IsError {
		t.Fatalf("edit after re-read apply = %+v, error=%v", applied, err)
	}
	assertFile(t, path, "alpha\nBETA\ngamma\n")
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
		call := tool.Call{ID: callID, Name: coding.ToolGofmt, Arguments: arguments}
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
		arguments, _ := json.Marshal(map[string]string{"input": read.Header + "\nreplace 2:\n+UPDATED\n"})
		calls[index] = tool.Call{ID: "shared-call-id", Name: coding.ToolEditHashline, Arguments: arguments}
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
	escape, err := service.ExecuteTool(ctx, run, tool.Call{ID: "read-escape", Name: coding.ToolReadFile, Arguments: escapeArgs}, nil)
	if err != nil {
		t.Fatalf("escape read returned Go error: %v", err)
	}
	if !escape.Executed || !escape.Result.IsError {
		t.Fatalf("symlink escape result = %+v", escape)
	}

	read := executeRead(t, ctx, service, run, "read-safe", "safe.txt")
	editArgs, _ := json.Marshal(map[string]string{"input": read.Header + "\nreplace 1:\n+changed\n"})
	call := tool.Call{ID: "edit-denied", Name: coding.ToolEditHashline, Arguments: editArgs}
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
	editArguments, _ := json.Marshal(map[string]string{"input": read.Header + "\nreplace 1:\n+after\n"})
	editCall := tool.Call{ID: "edit-before-resume", Name: coding.ToolEditHashline, Arguments: editArguments}
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
	if projection.Run.Status != api.RunStatusCompleted {
		t.Fatalf("run status = %q", projection.Run.Status)
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
	claim := api.ResourceClaimSpec{Key: "workspace:test", Mode: api.ResourceClaimExclusive}
	run, err := service.StartRunWithMetadata(ctx, "execute through coordinator", nil, RunExecutionPolicy{
		ResourceClaims: []api.ResourceClaimSpec{claim},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.LeaseID != "" {
		t.Fatalf("StartRun eagerly acquired lease %q", run.LeaseID)
	}
	task, err := service.Runner().Task(ctx, run.RunID, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(task.ResourceClaims, []api.ResourceClaimSpec{claim}) {
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
	if outcome.State != hyworker.ExecutionCompleted || run.LeaseID == "" {
		t.Fatalf("outcome=%+v run=%+v", outcome, run)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != api.RunStatusCompleted {
		t.Fatalf("run status=%s", projection.Run.Status)
	}
	if active := service.runner.ActiveLeaseCountContext(ctx, run.RunID, run.TaskID); active != 0 {
		t.Fatalf("active lease count=%d, want 0", active)
	}
	claims, err := service.Runner().ListResourceClaims(ctx, api.ResourceClaimSelector{RunIDs: []string{run.RunID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].State != api.ResourceClaimReleased {
		t.Fatalf("resource claims=%#v", claims)
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
	policy := RunExecutionPolicy{ResourceClaims: []api.ResourceClaimSpec{{
		Key: "workspace:shared", Mode: api.ResourceClaimExclusive,
	}}}
	owner, err := service.StartRunWithMetadata(ctx, "hold workspace", nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	waiter, err := service.StartRunWithMetadata(ctx, "wait for workspace", nil, policy)
	if err != nil {
		t.Fatal(err)
	}
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
	var unavailable *hyworker.TaskExecutionUnavailableError
	if !errors.As(err, &unavailable) ||
		unavailable.ResourceClaims.Reason != api.ResourceClaimDeniedConflict {
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
	if err != nil || outcome.State != hyworker.ExecutionCompleted || outcome.Result.Text != "acquired" {
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
	service.runLeaseTTL = 120 * time.Millisecond
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
	if projection.Run.Status != api.RunStatusCompleted {
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
	service.runLeaseTTL = time.Second
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
	if projection.Run.Status != api.RunStatusFailed {
		t.Fatalf("run status = %q, want failed", projection.Run.Status)
	}
}

func executeRead(t *testing.T, ctx context.Context, service *Service, run *Run, id string, path string) coding.ReadFileToolResult {
	t.Helper()
	arguments, _ := json.Marshal(map[string]string{"path": path})
	execution, err := service.ExecuteTool(ctx, run, tool.Call{ID: id, Name: coding.ToolReadFile, Arguments: arguments}, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !execution.Executed || execution.Result.IsError {
		t.Fatalf("read result = %+v", execution)
	}
	var result coding.ReadFileToolResult
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
	events, err := service.Runner().ListEvents(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type != api.EventApprovalDecided {
			continue
		}
		if got := events[index].Payload["decidedBy"]; got != want {
			t.Fatalf("approval decider=%v, want %q", got, want)
		}
		return
	}
	t.Fatalf("run %s has no approval decision", runID)
}
