package recovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/venat/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type recordingRunResumer struct {
	runIDs []string
	events *[]string
}

type recordingRunSelectorStore struct {
	agentruntime.StoreProvider
	preparer     StorePreparer
	selector     agentruntime.RunSelector
	prepareCalls int
}

func (s *recordingRunSelectorStore) Begin(ctx context.Context) (agentruntime.UnitOfWork, error) {
	work, err := s.StoreProvider.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &recordingRunSelectorWork{UnitOfWork: work, store: s}, nil
}

func (s *recordingRunSelectorStore) PrepareRecovery(ctx context.Context, now time.Time) (int64, int64, error) {
	s.prepareCalls++
	return s.preparer.PrepareRecovery(ctx, now)
}

func (s *recordingRunSelectorStore) ListReconcileAttempts(ctx context.Context) ([]agentruntime.ActionAttempt, error) {
	return s.preparer.ListReconcileAttempts(ctx)
}

type recordingRunSelectorWork struct {
	agentruntime.UnitOfWork
	store *recordingRunSelectorStore
}

func (w *recordingRunSelectorWork) Runs() agentruntime.RunStore {
	return &recordingRunSelectorRuns{RunStore: w.UnitOfWork.Runs(), store: w.store}
}

type recordingRunSelectorRuns struct {
	agentruntime.RunStore
	store *recordingRunSelectorStore
}

func (r *recordingRunSelectorRuns) ListRuns(ctx context.Context, selector agentruntime.RunSelector) ([]agentruntime.Run, error) {
	r.store.selector = selector
	return r.RunStore.ListRuns(ctx, selector)
}

type noopRunRecoverer struct{}

func (noopRunRecoverer) Recover(context.Context, string) (agentruntime.Projection, error) {
	return agentruntime.Projection{}, nil
}

type recordingRunRecoverer struct {
	runIDs []string
}

func (r *recordingRunRecoverer) Recover(_ context.Context, runID string) (agentruntime.Projection, error) {
	r.runIDs = append(r.runIDs, runID)
	return agentruntime.Projection{Run: agentruntime.Run{ID: runID, Status: agentruntime.RunStatusReconcileRequired}}, nil
}

func (r *recordingRunResumer) ResumeRun(_ context.Context, runID string) error {
	if r.events != nil {
		*r.events = append(*r.events, "resume")
	}
	r.runIDs = append(r.runIDs, runID)
	return nil
}

func sealRecoveryExecutionProfile(t *testing.T, ctx context.Context, service *agentservice.Service, run *agentservice.Run, workspace string) {
	t.Helper()
	if err := service.SealExecutionProfile(ctx, run, agentruntime.ExecutableProfile{
		Provider: "test", AccountID: "account", RawModel: "test-model", Model: "test-model", Reasoning: "none",
		ActiveSkills: []string{}, ToolSetHash: "tools", ToolProfileHash: "tool-profile",
		StaticIdentity: "static", WorkspaceAnchor: workspace,
		PromptFingerprint: "prompt", ToolSchemaFingerprint: "tool-schema",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverQueriesOnlyNonterminalRuns(t *testing.T) {
	ctx := t.Context()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	recording := &recordingRunSelectorStore{StoreProvider: store, preparer: store}
	service, err := NewService(recording, noopRunRecoverer{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	want := []agentruntime.RunStatus{
		agentruntime.RunStatusCreated,
		agentruntime.RunStatusPlanning,
		agentruntime.RunStatusValidating,
		agentruntime.RunStatusRouting,
		agentruntime.RunStatusDispatching,

		agentruntime.RunStatusRunning,
		agentruntime.RunStatusWaitingUserInput,
		agentruntime.RunStatusWaitingApproval,
		agentruntime.RunStatusExecuting,
		agentruntime.RunStatusReconcileRequired,
		agentruntime.RunStatusComposingResponse,
	}
	if !reflect.DeepEqual(recording.selector.Statuses, want) {
		t.Fatalf("recovery statuses = %v, want %v", recording.selector.Statuses, want)
	}
}

func TestRecoverPreparedDoesNotRepeatExclusivePreparation(t *testing.T) {
	ctx := t.Context()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	recording := &recordingRunSelectorStore{StoreProvider: store, preparer: store}
	service, err := NewService(recording, noopRunRecoverer{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecoverPrepared(ctx, Preparation{At: time.Now().UTC(), ExpiredLeases: 2, QuarantinedAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	if recording.prepareCalls != 0 {
		t.Fatalf("exclusive preparation repeated %d times", recording.prepareCalls)
	}
}

func TestRecoverDoesNotReplayAlreadyReconcileRequiredRun(t *testing.T) {
	ctx := t.Context()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	work, err := store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := agentruntime.Run{ID: "needs-reconcile", Status: agentruntime.RunStatusReconcileRequired, CreatedAt: time.Now().UTC()}
	if err := work.Runs().SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := work.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	recoverer := &recordingRunRecoverer{}
	service, err := NewService(store, recoverer, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	discovered := -1
	service.SetBeforeResume(func(_ context.Context, runs []agentruntime.Run) error {
		discovered = len(runs)
		return nil
	})
	summary, err := service.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recoverer.runIDs) != 0 {
		t.Fatalf("replayed runs = %v", recoverer.runIDs)
	}
	if discovered != 0 {
		t.Fatalf("before-resume runs = %d, want 0", discovered)
	}
	if len(summary.Runs) != 1 || summary.Runs[0].Projection.Run.ID != run.ID {
		t.Fatalf("recovery summary = %+v", summary.Runs)
	}
}

func TestRecoverProjectsPendingApprovalAndInterruptsSubagents(t *testing.T) {
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
	codingService, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer codingService.Close(ctx)
	run, err := codingService.StartRun(ctx, "edit note")
	if err != nil {
		t.Fatal(err)
	}
	sealRecoveryExecutionProfile(t, ctx, codingService, run, workspace)
	readArgs, _ := json.Marshal(map[string]string{"path": "note.txt"})
	read, err := codingService.ExecuteTool(ctx, run, tool.Call{ID: "read-1", Name: agentservice.ToolReadFile, Arguments: readArgs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var readResult agentservice.ReadFileToolResult
	if err := json.Unmarshal(read.Result.Structured, &readResult); err != nil {
		t.Fatal(err)
	}
	editArgs, _ := json.Marshal(map[string]string{"input": "*** Begin Patch\n" + readResult.Header + "\nPUT 1.=1:\n+after\n*** End Patch\n"})
	edit, err := codingService.ExecuteTool(ctx, run, tool.Call{ID: "edit-1", Name: agentservice.ToolEditHashline, Arguments: editArgs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if edit.Approval == nil {
		t.Fatal("write tool did not pause for approval")
	}

	subagents, err := agentservice.NewSQLSubagentRunStore(store.DB(), store.Blobs())
	if err != nil {
		t.Fatal(err)
	}
	if err := subagents.Create(ctx, agentservice.SubagentRun{ID: "child-1", SessionID: "default", ParentRunID: edit.Approval.Request.RunID, Type: "explore", State: agentservice.SubagentRunning, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	recoveryService, err := NewService(store, codingService, subagents, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := recoveryService.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.InterruptedSubagents != 1 {
		t.Fatalf("interrupted subagents = %d", summary.InterruptedSubagents)
	}
	if len(summary.Runs) != 1 || summary.Runs[0].Run.ID != edit.Approval.Request.RunID {
		t.Fatalf("recovered runs = %+v", summary.Runs)
	}
	if len(summary.Approvals) != 1 || summary.Approvals[0].Approval.ApprovalID != edit.Approval.Request.ApprovalID {
		t.Fatalf("pending approvals = %+v", summary.Approvals)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "before\n" {
		t.Fatalf("pending edit replayed during recovery: %q", contents)
	}
	children, err := subagents.List(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].State != agentservice.SubagentInterrupted {
		t.Fatalf("recovered subagents = %+v", children)
	}
}

func TestRecoverResumesRedispatchedSingleAgentRun(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "resume.db")
	store, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	runWorkspace := t.TempDir()
	codingService, err := agentservice.NewService(store, runWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	run, err := codingService.StartRun(ctx, "resume checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	sealRecoveryExecutionProfile(t, ctx, codingService, run, runWorkspace)
	if err := codingService.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	recoveredCoding, err := agentservice.NewService(reopened, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer recoveredCoding.Close(ctx)
	events := []string{}
	resumer := &recordingRunResumer{events: &events}
	recoveryService, err := NewService(reopened, recoveredCoding, nil, nil, resumer)
	if err != nil {
		t.Fatal(err)
	}
	recoveryService.SetBeforeResume(func(_ context.Context, runs []agentruntime.Run) error {
		if len(runs) != 1 || runs[0].ID != run.RunID {
			t.Fatalf("discovered runs=%v want=%s", runs, run.RunID)
		}
		events = append(events, "session_loaded")
		return nil
	})
	if _, err := recoveryService.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if len(resumer.runIDs) != 1 || resumer.runIDs[0] != run.RunID {
		t.Fatalf("resumed runs=%v want=%s", resumer.runIDs, run.RunID)
	}
	if !reflect.DeepEqual(events, []string{"session_loaded", "resume"}) {
		t.Fatalf("recovery event order=%v", events)
	}
}

func TestRecoverMarksLegacyRunWithoutV1BindingReconcileRequired(t *testing.T) {
	ctx := t.Context()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	run, err := coding.StartRun(ctx, "legacy interrupted run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM agent_execution_bindings WHERE run_id = ?`, run.RunID); err != nil {
		t.Fatal(err)
	}
	resumer := &recordingRunResumer{}
	service, err := NewService(store, coding, nil, nil, resumer)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumer.runIDs) != 0 {
		t.Fatalf("legacy run was replayed: %v", resumer.runIDs)
	}
	if len(summary.Runs) != 1 || summary.Runs[0].Projection.Run.Status != agentruntime.RunStatusReconcileRequired {
		t.Fatalf("legacy recovery summary = %+v", summary.Runs)
	}
	recovered, err := coding.LoadRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != agentruntime.RunStatusReconcileRequired {
		t.Fatalf("legacy run status = %q", recovered.Status)
	}
}

func TestRecoverLeavesSecurityAutomationToSecurityCoordinator(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	codingService, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer codingService.Close(ctx)
	if _, err := codingService.StartRunWithMetadata(ctx, "security audit", map[string]string{
		"automation_kind": "security_audit",
	}); err != nil {
		t.Fatal(err)
	}
	resumer := &recordingRunResumer{}
	recoveryService, err := NewService(store, codingService, nil, nil, resumer)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := recoveryService.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Runs) != 0 || len(resumer.runIDs) != 0 {
		t.Fatalf("security automation entered generic recovery: summary=%+v resumed=%v", summary.Runs, resumer.runIDs)
	}
}
