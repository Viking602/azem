package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/coding"
	"github.com/Viking602/go-hydaelyn/tool"

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
	_, err = service.ExecuteTool(ctx, run, tool.Call{ID: "edit-replay-new-id", Name: coding.ToolEditHashline, Arguments: arguments}, nil)
	if !errors.Is(err, hydaelyn.ErrActionReconcileRequired) {
		t.Fatalf("same effect with new call ID error=%v", err)
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

func TestRestoredCompletedEffectBlocksNewCallID(t *testing.T) {
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
	run, err := service.StartRun(ctx, "do not replay")
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"input":"already-completed"}`)
	digest := sha256.Sum256(arguments)
	run.RestoreCompletedEffect(coding.ToolEditHashline, fmt.Sprintf("%x", digest[:]))
	_, err = service.ExecuteTool(ctx, run, tool.Call{ID: "fresh-call-id", Name: coding.ToolEditHashline, Arguments: arguments}, nil)
	if !errors.Is(err, hydaelyn.ErrActionReconcileRequired) {
		t.Fatalf("semantic replay error=%v", err)
	}
}

func TestCanonicalToolArgumentsGiveEquivalentSideEffectsOneIdentity(t *testing.T) {
	first, err := canonicalToolArguments(json.RawMessage(`{"command":"deploy","network":true}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalToolArguments(json.RawMessage(" { \"network\" : true, \"command\" : \"deploy\" } "))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical arguments differ: %s != %s", first, second)
	}
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
	if runs[0].RunID == runs[1].RunID || runs[0].TaskID == runs[1].TaskID || runs[0].LeaseID == runs[1].LeaseID {
		t.Fatalf("run identities overlap: %#v %#v", runs[0], runs[1])
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
	if resumed.RunID != run.RunID || resumed.TaskID != run.TaskID || resumed.LeaseID == run.LeaseID {
		t.Fatalf("resumed run=%+v original=%+v", resumed, run)
	}
	_, err = recovered.ExecuteTool(ctx, resumed, tool.Call{ID: "new-id-same-effect", Name: coding.ToolEditHashline, Arguments: editArguments}, nil)
	if !errors.Is(err, hydaelyn.ErrActionReconcileRequired) {
		t.Fatalf("resumed semantic replay error=%v", err)
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

func TestFinalizeReportedRunRepairsPostReportCrashWindow(t *testing.T) {
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
	run, err := service.StartRun(ctx, "finalize reported")
	if err != nil {
		t.Fatal(err)
	}
	if err := run.stopRunLeaseHeartbeat(); err != nil {
		t.Fatal(err)
	}
	if err := service.runner.HeartbeatTaskExecution(ctx, api.HeartbeatTaskExecutionCommand{LeaseID: run.LeaseID, HolderID: run.HolderID, TTL: service.runLeaseTTL}); err != nil {
		t.Fatal(err)
	}
	if err := service.runner.SubmitTypedReport(ctx, api.SubmitTypedReportCommand{
		RunID: run.RunID, TaskID: run.TaskID, LeaseID: run.LeaseID, HolderType: api.HolderAgent,
		HolderID: run.HolderID, TaskVersion: run.TaskVersion,
		Report: api.TypedReport{Status: api.ReportStatusSuccess, Summary: "already committed"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.FinalizeReportedRun(ctx, run.RunID); err != nil {
		t.Fatal(err)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != api.RunStatusCompleted {
		t.Fatalf("repaired run status=%s", projection.Run.Status)
	}
	cancelled, err := service.StartRun(ctx, "finalize cancelled report")
	if err != nil {
		t.Fatal(err)
	}
	if err := cancelled.stopRunLeaseHeartbeat(); err != nil {
		t.Fatal(err)
	}
	if err := service.runner.HeartbeatTaskExecution(ctx, api.HeartbeatTaskExecutionCommand{LeaseID: cancelled.LeaseID, HolderID: cancelled.HolderID, TTL: service.runLeaseTTL}); err != nil {
		t.Fatal(err)
	}
	if err := service.runner.SubmitTypedReport(ctx, api.SubmitTypedReportCommand{
		RunID: cancelled.RunID, TaskID: cancelled.TaskID, LeaseID: cancelled.LeaseID, HolderType: api.HolderAgent,
		HolderID: cancelled.HolderID, TaskVersion: cancelled.TaskVersion,
		Report: api.TypedReport{Status: api.ReportStatusFailed, Summary: "cancelled", Kind: "cancelled"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.FinalizeReportedRun(ctx, cancelled.RunID); err != nil {
		t.Fatal(err)
	}
	cancelledProjection, err := service.Recover(ctx, cancelled.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelledProjection.Run.Status != api.RunStatusCancelled {
		t.Fatalf("repaired cancelled run status=%s", cancelledProjection.Run.Status)
	}
}

func TestRunLeaseHeartbeatKeepsLongRunReportable(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	const leaseTTL = 120 * time.Millisecond
	service, err := NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.runLeaseTTL = leaseTTL
	service.runHeartbeatInterval = 20 * time.Millisecond
	defer service.Close(ctx)
	run, err := service.StartRun(ctx, "long running task")
	if err != nil {
		t.Fatal(err)
	}
	var initialExpiry int64
	if err := store.DB().QueryRowContext(ctx, `SELECT expires_at FROM leases WHERE id=?`, run.LeaseID).Scan(&initialExpiry); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		var version int
		var extendedExpiry int64
		if err := store.DB().QueryRowContext(ctx, `SELECT version,expires_at FROM leases WHERE id=?`, run.LeaseID).Scan(&version, &extendedExpiry); err != nil {
			t.Fatal(err)
		}
		if version > 1 && extendedExpiry > initialExpiry {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run lease was not renewed before its initial expiry")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if wait := time.Until(time.Unix(0, initialExpiry).Add(20 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	if err := service.CompleteRun(ctx, run, "done", nil); err != nil {
		t.Fatalf("complete run after initial lease expiry: %v", err)
	}
	projection, err := service.Recover(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != api.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", projection.Run.Status)
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
	service.runHeartbeatInterval = 50 * time.Millisecond
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
