package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestDurableToolContinuityVerifiesOnlyObservedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stable.go"), []byte("package stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := turnContext{
		workspaceRoot: root,
		toolRecords: []session.ToolRecord{{
			RunID: "run", ToolCallID: "read-1", Name: "coding.read_file", State: session.ToolCompleted,
			Observations: []session.FileObservation{{
				Path: "stable.go", Operation: "read", SHA256: sha256Hex([]byte("package stable\n")),
			}},
		}},
	}
	messages := manager.toolContinuityMessages(context.Background())
	if len(messages) != 2 || !strings.Contains(messages[1].Text, `"state":"verified_unchanged"`) {
		t.Fatalf("unchanged file evidence=%#v", messages)
	}
	if err := os.WriteFile(filepath.Join(root, "stable.go"), []byte("package changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	messages = manager.toolContinuityMessages(context.Background())
	if len(messages) != 2 || !strings.Contains(messages[1].Text, `"state":"stale"`) {
		t.Fatalf("changed file evidence=%#v", messages)
	}
}

func TestDurableToolTimelineCapturesCompletedReadObservation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("durable note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Timeline"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "session", session.Block{Kind: "user", RunID: "run", Content: "read note"}); err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"path":"note.txt"}`)
	timeline := newDurableToolTimeline(sessions, root, "session", "run")
	if err := timeline.start(ctx, tool.Call{ID: "read-1", Name: coding.ToolReadFile, Arguments: arguments}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := timeline.finish(ctx, tool.Result{ToolCallID: "read-1", Name: coding.ToolReadFile, Content: "durable note"}); err != nil {
		t.Fatal(err)
	}
	projection, err := sessions.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.ToolRecords) != 1 || projection.ToolRecords[0].State != session.ToolCompleted ||
		len(projection.ToolRecords[0].Observations) != 1 ||
		projection.ToolRecords[0].Observations[0].SHA256 != sha256Hex([]byte("durable note\n")) {
		t.Fatalf("durable tool record=%#v", projection.ToolRecords)
	}
}

func TestShellArtifactSinkPersistsAfterExecutionCancellation(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "shell-artifact.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Shell artifact"}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("shell-output\n"), 100)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	result, err := newShellArtifactSink(sessions)(cancelled, agentservice.ShellExecutionSnapshot{
		SessionID: "session", RunID: "run", Output: string(payload[:64]),
	}, payload)
	if err != nil || !strings.HasPrefix(result.Reference, "artifact:") {
		t.Fatalf("artifact result=%#v err=%v", result, err)
	}
	artifact, err := sessions.LoadArtifact(ctx, "session", strings.TrimPrefix(result.Reference, "artifact:"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact.Payload, payload) || artifact.RunID != "run" || artifact.Kind != "shell_output" {
		t.Fatalf("persisted artifact=%#v", artifact)
	}
}

type yoloApprovalDriver struct{}

func (yoloApprovalDriver) Definition() tool.Definition {
	return tool.Definition{
		Name: "test.write", Description: "write", EffectType: tool.EffectWrite,
		RequiresApproval: true, RiskLevel: "high", InputSchema: tool.Schema{Type: "object"},
	}
}

func (yoloApprovalDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "written"}, nil
}

func TestYoloApprovalModeResolvesDurableCodingApproval(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	host := NewService(ctx, config.Default())
	host.coding = coding
	defer host.cancel()
	if err := host.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: string(ApprovalModeYolo)}); err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRun(ctx, "write")
	if err != nil {
		t.Fatal(err)
	}
	call := tool.Call{ID: "write-1", Name: "test.write"}
	execution, err := coding.ExecuteDriver(ctx, run, yoloApprovalDriver{}, call, nil)
	if err != nil || execution.Approval == nil || execution.Executed {
		t.Fatalf("pending execution = %#v err:%v", execution, err)
	}
	resolution, err := host.awaitApproval(ctx, "session", "", "main", run, call, *execution.Approval)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("yolo resolution = mode:%q err:%v", resolution.Mode, err)
	}
	if decider := durableApprovalDecider(t, coding, run.RunID); decider != "approval-mode:yolo" {
		t.Fatalf("YOLO durable decider=%q", decider)
	}
	execution, err = coding.ExecuteDriver(ctx, run, yoloApprovalDriver{}, call, nil)
	if err != nil || !execution.Executed || execution.Result.IsError || execution.Result.Content != "written" {
		t.Fatalf("approved execution = %#v err:%v", execution, err)
	}
	modeEvent, err := host.NextEvent(ctx)
	if err != nil || modeEvent.Kind != EventApprovalMode || modeEvent.State != "yolo" {
		t.Fatalf("YOLO mode projection=%+v error=%v", modeEvent, err)
	}
	if err := coding.CompleteRun(ctx, run, "done", nil); err != nil {
		t.Fatal(err)
	}
}

func TestYoloApprovalModeDrainsPendingAndSkipsFuturePrompts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	service := NewService(ctx, config.Default())
	definition := tool.Definition{
		Name: "coding.write_file", EffectType: tool.EffectWrite, RequiresApproval: true, RiskLevel: "high",
	}
	type approvalResult struct {
		mode agentservice.ApprovalMode
		err  error
	}
	await := func(call tool.Call) <-chan approvalResult {
		result := make(chan approvalResult, 1)
		go func() {
			resolution, err := service.awaitTeamApproval(ctx, "session", "run", "goal", call, definition)
			result <- approvalResult{mode: resolution.Mode, err: err}
		}()
		return result
	}

	first := await(tool.Call{ID: "write-1", Name: definition.Name})
	requested := nextApprovalEvent(t, service, EventApprovalRequested)
	if requested.ToolCallID != "write-1" {
		t.Fatalf("initial prompt = event:%+v", requested)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: string(ApprovalModeYolo)}); err != nil {
		t.Fatal(err)
	}
	if result := <-first; result.err != nil || result.mode != agentservice.ApprovalOnce {
		t.Fatalf("drained approval = mode:%q err:%v", result.mode, result.err)
	}
	resolved := nextApprovalEvent(t, service, EventApprovalResolved)
	if resolved.ApprovalID != requested.ApprovalID {
		t.Fatalf("drained event = event:%+v", resolved)
	}
	modeEvent, err := service.NextEvent(ctx)
	if err != nil || modeEvent.Kind != EventApprovalMode || modeEvent.State != "yolo" {
		t.Fatalf("YOLO mode event=%+v error=%v", modeEvent, err)
	}

	resolution, err := service.awaitTeamApproval(ctx, "session", "run", "goal", tool.Call{ID: "write-2", Name: definition.Name}, definition)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("yolo approval = mode:%q err:%v", resolution.Mode, err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: string(ApprovalModePrompt)}); err != nil {
		t.Fatal(err)
	}
	modeEvent, err = service.NextEvent(ctx)
	if err != nil || modeEvent.Kind != EventApprovalMode || modeEvent.State != "prompt" {
		t.Fatalf("prompt mode event=%+v error=%v", modeEvent, err)
	}
	third := await(tool.Call{ID: "write-3", Name: definition.Name})
	requested = nextApprovalEvent(t, service, EventApprovalRequested)
	if requested.ToolCallID != "write-3" {
		t.Fatalf("restored prompt = event:%+v", requested)
	}
	if _, err := service.resolveLiveApproval(ctx, requested.ApprovalID, "once", "user"); err != nil {
		t.Fatal(err)
	}
	if result := <-third; result.err != nil || result.mode != agentservice.ApprovalOnce {
		t.Fatalf("prompt approval = mode:%q err:%v", result.mode, result.err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: "unsafe"}); err == nil {
		t.Fatal("invalid approval mode was accepted")
	}
}

type namedApprovalDriver struct {
	name       string
	executions *atomic.Int32
}

func (d namedApprovalDriver) Definition() tool.Definition {
	return tool.Definition{
		Name: d.name, Description: "write under automatic review", EffectType: tool.EffectWrite,
		RequiresApproval: true, RiskLevel: "high", InputSchema: tool.Schema{Type: "object"},
	}
}

func (d namedApprovalDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	d.executions.Add(1)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "executed"}, nil
}

type countedApprovalDriver struct {
	executions *atomic.Int32
}

func (d countedApprovalDriver) Definition() tool.Definition {
	return tool.Definition{
		Name: "test.auto_write", Description: "write under automatic review", EffectType: tool.EffectWrite,
		RequiresApproval: true, RiskLevel: "high", InputSchema: tool.Schema{Type: "object"},
	}
}

func (d countedApprovalDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	d.executions.Add(1)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "executed"}, nil
}

type autoReviewHarness struct {
	host           *Service
	runtime        *ProviderRuntime
	coding         *agentservice.Service
	authentication *auth.Service
	run            *agentservice.Run
	driver         countedApprovalDriver
}

func TestToolStartStateSkipsQueueWhenNothingIsExecuting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode ApprovalMode
		name string
		want string
	}{
		{ApprovalModeYolo, "coding.shell", "running"},
		{ApprovalModeAutoReview, coding.ToolEditHashline, "reviewing_approval"},
		{ApprovalModeAutoReview, coding.ToolWriteFile, "reviewing_approval"},
		{ApprovalModeAutoReview, coding.ToolReadFile, "running"},
		{ApprovalModeAutoReview, "coding.shell", "reviewing_approval"},
		{ApprovalModePrompt, coding.ToolReadFile, "running"},
		{ApprovalModePrompt, coding.ToolWriteFile, "queued"},
		{ApprovalModePrompt, "coding.shell", "queued"},
	}
	for _, test := range cases {
		service := &Service{approvalMode: test.mode}
		if got := service.toolStartState(test.name); got != test.want {
			t.Fatalf("%s %s start state = %q, want %q", test.mode, test.name, got, test.want)
		}
	}
}

func TestAutoReviewReviewsWorkspaceFileEdits(t *testing.T) {
	reviews := atomic.Int32{}
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			reviews.Add(1)
		}
		writeAutomaticReviewWithUsage(writer, "```json\n"+`{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized workspace edit"}`+"\n```")
	})
	_ = nextApprovalEvent(t, harness.host, EventApprovalMode)
	driver := namedApprovalDriver{name: "coding.edit_hashline", executions: &atomic.Int32{}}
	call := tool.Call{ID: "edit-1", Name: "coding.edit_hashline", Arguments: json.RawMessage(`{"input":"¶src/main.go#ABCD replace 1:\n-old\n+new"}`)}
	execution, err := harness.coding.ExecuteDriver(context.Background(), harness.run, driver, call, nil)
	if err != nil || execution.Approval == nil {
		t.Fatalf("prepare workspace edit=%+v error=%v", execution, err)
	}
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, *execution.Approval)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("workspace edit resolution=%+v error=%v", resolution, err)
	}
	if reviews.Load() == 0 {
		t.Fatal("workspace file edit skipped the reviewer")
	}
	executed, err := harness.coding.ExecuteDriver(context.Background(), harness.run, driver, call, nil)
	if err != nil || !executed.Executed || driver.executions.Load() != 1 {
		t.Fatalf("workspace edit execution=%+v count=%d error=%v", executed, driver.executions.Load(), err)
	}
}

func TestAutoReviewPrefetchesWorkspaceEditsInParallel(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			return
		}
		started <- struct{}{}
		<-release
		writeAutomaticReviewWithUsage(writer, "```json\n"+`{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized"}`+"\n```")
	})
	_ = nextApprovalEvent(t, harness.host, EventApprovalMode)
	first := json.RawMessage(`{"input":"¶src/a.go#AAAA replace 1:\n-old\n+new"}`)
	second := json.RawMessage(`{"input":"¶src/b.go#BBBB replace 1:\n-old\n+new"}`)
	harness.host.prefetchAutoReview(context.Background(), "session", harness.run.RunID, "edit-1", coding.ToolEditHashline, first)
	harness.host.prefetchAutoReview(context.Background(), "session", harness.run.RunID, "edit-2", coding.ToolEditHashline, second)
	deadline := time.After(2 * time.Second)
	for count := 0; count < 2; count++ {
		select {
		case <-started:
		case <-deadline:
			t.Fatalf("started %d parallel reviews, want 2", count)
		}
	}
	close(release)
	for _, id := range []string{"edit-1", "edit-2"} {
		assessment, err, ok := harness.host.consumePrefetchedReview(context.Background(), harness.run.RunID, id)
		if !ok || err != nil || assessment.Outcome != "allow" {
			t.Fatalf("prefetched review %s = %+v ok=%v error=%v", id, assessment, ok, err)
		}
	}
}

func TestAutoReviewMarksNonWorkspaceToolsReviewingInsteadOfQueued(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReviewWithUsage(writer, "```json\n"+`{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized"}`+"\n```")
	})
	_ = nextApprovalEvent(t, harness.host, EventApprovalMode)
	call := tool.Call{ID: "allow-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"precise.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("automatic allow=%+v error=%v", resolution, err)
	}
	reviewingTool := nextApprovalEvent(t, harness.host, EventToolUpdate)
	if reviewingTool.State != "reviewing_approval" || reviewingTool.ToolCallID != "allow-1" {
		t.Fatalf("tool reviewing event=%+v", reviewingTool)
	}
}

func TestAutoReviewAllowUsesGoalArgumentsAndApprovesOnlyOnce(t *testing.T) {
	var requestChecked atomic.Bool
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != codex.ApprovalReviewerModel {
			t.Errorf("review model=%v", body["model"])
		}
		if _, found := body["tools"]; found {
			t.Errorf("automatic reviewer received tools: %v", body["tools"])
		}
		input, _ := body["input"].([]any)
		entry, _ := input[len(input)-1].(map[string]any)
		content, _ := entry["content"].([]any)
		part, _ := content[0].(map[string]any)
		var evidence map[string]any
		if err := json.Unmarshal([]byte(part["text"].(string)), &evidence); err != nil {
			t.Error(err)
		}
		arguments, _ := evidence["arguments"].(map[string]any)
		if evidence["goal"] != "original user goal" || evidence["agent_id"] != "agent-1" ||
			evidence["tool_name"] != "test.auto_write" || arguments["path"] != "precise.txt" {
			t.Errorf("review evidence=%v", evidence)
		}
		requestChecked.Store(true)
		writeAutomaticReviewWithUsage(writer, "```json\n"+`{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized bounded write"}`+"\n```")
	})
	modeEvent := nextApprovalEvent(t, harness.host, EventApprovalMode)
	if modeEvent.State != "auto_review" || modeEvent.Data["auto_review_available"] != "true" {
		t.Fatalf("automatic capability event=%+v", modeEvent)
	}
	call := tool.Call{ID: "allow-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"precise.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("automatic allow=%+v error=%v", resolution, err)
	}
	if !requestChecked.Load() {
		t.Fatal("automatic review request was not inspected")
	}
	reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if reviewing.State != "reviewing" || reviewing.ApprovalID == "" {
		t.Fatalf("reviewing event=%+v", reviewing)
	}
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" || resolved.Data["risk"] != "medium" ||
		resolved.Data["user_authorization"] != "high" || resolved.Data["reviewer"] != codex.ApprovalReviewerModel {
		t.Fatalf("resolved event=%+v", resolved)
	}
	executed, err := harness.coding.ExecuteDriver(context.Background(), harness.run, harness.driver, call, nil)
	if err != nil || !executed.Executed || harness.driver.executions.Load() != 1 {
		t.Fatalf("approved execution=%+v count=%d error=%v", executed, harness.driver.executions.Load(), err)
	}
	repeated, err := harness.coding.ExecuteDriver(context.Background(), harness.run, harness.driver, call, nil)
	if err != nil || repeated.Executed || repeated.Approval == nil || harness.driver.executions.Load() != 1 {
		t.Fatalf("approval was not once-only: result=%+v count=%d error=%v", repeated, harness.driver.executions.Load(), err)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != codex.ApprovalReviewerModel {
		t.Fatalf("durable decider=%q", decider)
	}
	usage, err := harness.host.sessions.ProviderUsageSnapshot(context.Background(), "session", harness.run.RunID)
	if err != nil || usage.CacheInputTokens != 11 || usage.CachedInputTokens != 0 || usage.CacheWriteTokens != 0 || !usage.CacheWriteReported {
		t.Fatalf("review usage=%+v error=%v", usage, err)
	}
}

func TestAutoReviewTeamDecisionWritesDurableAudit(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized team write"}`, true)
	})
	call := tool.Call{ID: "team-allow", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"team.txt"}`)}
	resolution, err := harness.host.awaitTeamApproval(
		context.Background(), "session", harness.run.RunID, "team user goal", call, harness.driver.Definition(),
	)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("team automatic approval=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" {
		t.Fatalf("team automatic event=%+v", resolved)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != codex.ApprovalReviewerModel {
		t.Fatalf("team durable decider=%q", decider)
	}
}

func TestAutoReviewTeamDenyFallsBackToUserApproval(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"team action needs confirmation"}`, true)
	})
	call := tool.Call{ID: "team-deny", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"team.txt"}`)}
	type approvalResult struct {
		resolution approvalResolution
		err        error
	}
	result := make(chan approvalResult, 1)
	go func() {
		resolution, err := harness.host.awaitTeamApproval(
			context.Background(), "session", harness.run.RunID, "team user goal", call, harness.driver.Definition(),
		)
		result <- approvalResult{resolution: resolution, err: err}
	}()
	reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if reviewing.State != "reviewing" {
		t.Fatalf("team reviewing event=%+v", reviewing)
	}
	denied := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if denied.State != "auto_denied" {
		t.Fatalf("team automatic denial=%+v", denied)
	}
	prompt := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if prompt.State != "pending" || prompt.ApprovalID != reviewing.ApprovalID {
		t.Fatalf("team manual fallback=%+v", prompt)
	}
	if err := harness.host.ExecuteAction(context.Background(), Action{
		Kind: ActionResolveApproval, Target: prompt.ApprovalID, Decision: "once",
	}); err != nil {
		t.Fatal(err)
	}
	outcome := <-result
	if outcome.err != nil || outcome.resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("team user approval=%+v error=%v", outcome.resolution, outcome.err)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != "user" {
		t.Fatalf("team durable decider=%q", decider)
	}
}

func TestAutoReviewDoesNotInvokeInteractivePermissionHooks(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell command")
	}
	var reviews atomic.Int32
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		reviews.Add(1)
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized write"}`, true)
	})
	hookPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(hookPath, []byte(`{"hooks":{"PermissionRequest":[{"matcher":"*","hooks":[{"name":"interactive-bridge","type":"command","command":"printf 'interactive permission hook ran' >&2; exit 2"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	harness.host.AttachHooks(hooks.Dispatcher{
		Registry: hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: hookPath, Trusted: true}}}),
	})

	call := tool.Call{ID: "hook-skip", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"reviewed.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("automatic approval was intercepted by interactive hook: resolution=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved); resolved.State != "auto_approved" {
		t.Fatalf("automatic resolution=%+v", resolved)
	}

	teamCall := tool.Call{ID: "team-hook-skip", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"team-reviewed.txt"}`)}
	resolution, err = harness.host.awaitTeamApproval(context.Background(), "session", harness.run.RunID, "team goal", teamCall, harness.driver.Definition())
	if err != nil || resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("team automatic approval was intercepted by interactive hook: resolution=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved); resolved.State != "auto_approved" {
		t.Fatalf("team automatic resolution=%+v", resolved)
	}
	if reviews.Load() != 2 {
		t.Fatalf("automatic reviewer calls=%d, want 2", reviews.Load())
	}
}

func TestAutoReviewDenyFallsBackToUserWhileMalformedFailureStaysClosed(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantState  string
		wantText   string
		errorKind  string
		promptUser bool
	}{
		{
			name:      "explicit deny",
			output:    `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"target is not authorized"}`,
			wantState: "auto_denied", wantText: "Denied by automatic review", errorKind: "", promptUser: true,
		},
		{
			name: "malformed output", output: `{`,
			wantState: "auto_failed", wantText: "Automatic review failed", errorKind: "parse",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
				writeAutomaticReview(writer, test.output, true)
			})
			call := tool.Call{ID: "denied-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
			pending := prepareAutomaticApproval(t, harness, call)
			type approvalResult struct {
				resolution approvalResolution
				err        error
			}
			result := make(chan approvalResult, 1)
			go func() {
				resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
				result <- approvalResult{resolution: resolution, err: err}
			}()
			reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
			if reviewing.State != "reviewing" {
				t.Fatalf("reviewing event=%+v", reviewing)
			}
			resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
			if resolved.State != test.wantState || resolved.Data["error_kind"] != test.errorKind {
				t.Fatalf("resolved event=%+v", resolved)
			}
			if !strings.Contains(resolved.Text, test.wantText) {
				t.Fatalf("automatic resolution text=%q", resolved.Text)
			}
			if test.wantState == "auto_failed" && (resolved.Data["risk"] != "high" ||
				!strings.Contains(resolved.Data["rationale"], "Automatic approval review failed (parse)")) {
				t.Fatalf("fail-closed review omitted diagnostic assessment: %+v", resolved)
			}
			if test.promptUser {
				prompt := nextApprovalEvent(t, harness.host, EventApprovalRequested)
				if prompt.State != "pending" || prompt.ApprovalID != reviewing.ApprovalID {
					t.Fatalf("manual fallback event=%+v", prompt)
				}
				if err := harness.host.ExecuteAction(context.Background(), Action{
					Kind: ActionResolveApproval, Target: prompt.ApprovalID, Decision: "once",
				}); err != nil {
					t.Fatal(err)
				}
			}
			outcome := <-result
			wantMode := agentservice.ApprovalDenied
			wantDecider := "system:auto-review-failure"
			if test.promptUser {
				wantMode = agentservice.ApprovalOnce
				wantDecider = "user"
			}
			if outcome.err != nil || outcome.resolution.Mode != wantMode {
				t.Fatalf("approval outcome=%+v error=%v", outcome.resolution, outcome.err)
			}
			if harness.driver.executions.Load() != 0 {
				t.Fatalf("approval flow executed tool prematurely %d times", harness.driver.executions.Load())
			}
			if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != wantDecider {
				t.Fatalf("durable decider=%q", decider)
			}
		})
	}
}

func TestAutoReviewAppliesCodexMatrixToMediumDenials(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"unknown","outcome":"deny","rationale":"user did not name this exact file"}`, true)
	})
	call := tool.Call{ID: "medium-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"notes.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce || resolution.NeedsUserApproval {
		t.Fatalf("medium denial should auto-allow: %+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" || resolved.Data["risk"] != "medium" {
		t.Fatalf("resolved event=%+v", resolved)
	}
}

func TestAutoReviewAllowsLowRiskShellWithoutModel(t *testing.T) {
	var reviews atomic.Int32
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		reviews.Add(1)
		t.Error("low-risk shell must not call the approval model")
		writeAutomaticReview(writer, `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"should not run"}`, true)
	})
	writeCall := tool.Call{ID: "shell-low", Name: "test.auto_write", Arguments: json.RawMessage(`{"command":"go test ./internal/agent"}`)}
	pending := prepareAutomaticApproval(t, harness, writeCall)
	call := writeCall
	call.Name = agentservice.ToolShell
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce || resolution.NeedsUserApproval {
		t.Fatalf("low-risk shell resolution=%+v error=%v", resolution, err)
	}
	if reviews.Load() != 0 {
		t.Fatalf("approval model calls=%d, want 0", reviews.Load())
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" || resolved.Data["risk"] != "low" || resolved.Data["reviewer"] != "host:guardian-policy" {
		t.Fatalf("resolved event=%+v", resolved)
	}
}

func TestAutoReviewAllowsRequestedGitPush(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"network command needs confirmation"}`, true)
	})
	run, err := harness.coding.StartRun(context.Background(), "帮我提交代码并推送")
	if err != nil {
		t.Fatal(err)
	}
	harness.run = run
	writeCall := tool.Call{ID: "push-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"command":"git push origin HEAD"}`)}
	pending := prepareAutomaticApproval(t, harness, writeCall)
	call := writeCall
	call.Name = agentservice.ToolShell
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce || resolution.NeedsUserApproval {
		t.Fatalf("requested git push should auto-allow: %+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" || resolved.Data["user_authorization"] != "high" {
		t.Fatalf("resolved event=%+v", resolved)
	}
}

func TestAutoReviewTimeoutFallsBackToUserApproval(t *testing.T) {
	var modelsMu sync.Mutex
	var models []string
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		modelsMu.Lock()
		models = append(models, body.Model)
		modelsMu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	})
	harness.runtime.approvalReviewTimeout = 20 * time.Millisecond
	call := tool.Call{ID: "timeout-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	type approvalResult struct {
		resolution approvalResolution
		err        error
	}
	result := make(chan approvalResult, 1)
	go func() {
		resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
		result <- approvalResult{resolution: resolution, err: err}
	}()
	reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_timed_out" || resolved.Data["error_kind"] != "timeout" {
		t.Fatalf("timeout event=%+v", resolved)
	}
	modelsMu.Lock()
	gotModels := append([]string(nil), models...)
	modelsMu.Unlock()
	wantModels := []string{codex.ApprovalReviewerModel, codex.ApprovalReviewerModel, codex.ApprovalReviewerModel}
	if !reflect.DeepEqual(gotModels, wantModels) {
		t.Fatalf("review models=%v, want %v", gotModels, wantModels)
	}
	prompt := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if prompt.State != "pending" || prompt.ApprovalID != reviewing.ApprovalID {
		t.Fatalf("timeout manual fallback=%+v", prompt)
	}
	if harness.driver.executions.Load() != 0 {
		t.Fatal("timed-out review executed tool")
	}
	if err := harness.host.ExecuteAction(context.Background(), Action{
		Kind: ActionResolveApproval, Target: prompt.ApprovalID, Decision: "once",
	}); err != nil {
		t.Fatal(err)
	}
	outcome := <-result
	if outcome.err != nil || outcome.resolution.Mode != agentservice.ApprovalOnce {
		t.Fatalf("timeout resolution=%+v error=%v", outcome.resolution, outcome.err)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != "user" {
		t.Fatalf("timeout decider=%q", decider)
	}
	executed, err := harness.coding.ExecuteDriver(context.Background(), harness.run, harness.driver, call, nil)
	if err != nil || !executed.Executed || harness.driver.executions.Load() != 1 {
		t.Fatalf("user-approved execution=%+v count=%d error=%v", executed, harness.driver.executions.Load(), err)
	}
}

func TestAutoReviewTimeoutRetriesConfiguredModel(t *testing.T) {
	var requests atomic.Int32
	var modelsMu sync.Mutex
	var models []string
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		modelsMu.Lock()
		models = append(models, body.Model)
		modelsMu.Unlock()
		if requests.Add(1) == 1 {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
			return
		}
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"fallback model completed review"}`, true)
	})
	harness.runtime.approvalReviewTimeout = 20 * time.Millisecond
	call := tool.Call{ID: "timeout-retry", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"retried.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalOnce || resolution.NeedsUserApproval {
		t.Fatalf("retry resolution=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_approved" || resolved.Data["error_kind"] != "" {
		t.Fatalf("retry event=%+v", resolved)
	}
	modelsMu.Lock()
	gotModels := append([]string(nil), models...)
	modelsMu.Unlock()
	wantModels := []string{codex.ApprovalReviewerModel, codex.ApprovalReviewerModel}
	if !reflect.DeepEqual(gotModels, wantModels) {
		t.Fatalf("review models=%v, want %v", gotModels, wantModels)
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != codex.ApprovalReviewerModel {
		t.Fatalf("retry decider=%q", decider)
	}
}

func TestAutoReviewCallerDeadlineStopsWithoutRetryOrManualApproval(t *testing.T) {
	var requests atomic.Int32
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	})
	harness.runtime.approvalReviewTimeout = time.Second
	call := tool.Call{ID: "caller-deadline", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"cancelled.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resolution, err := harness.host.awaitApproval(ctx, "session", "agent-1", "main", harness.run, call, pending)
	if !errors.Is(err, context.DeadlineExceeded) || resolution.NeedsUserApproval || requests.Load() != 1 {
		t.Fatalf("caller deadline resolution=%+v requests=%d error=%v", resolution, requests.Load(), err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	noEventCtx, noEventCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer noEventCancel()
	for {
		event, nextErr := harness.host.NextEvent(noEventCtx)
		if nextErr != nil {
			break
		}
		if event.Kind == EventApprovalRequested || event.Kind == EventApprovalResolved {
			t.Fatalf("caller deadline emitted follow-up approval event=%+v", event)
		}
	}
	events, listErr := harness.coding.Runner().ListEvents(context.Background(), harness.run.RunID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	for _, event := range events {
		if event.Type == api.EventApprovalDecided {
			t.Fatalf("caller deadline recorded approval decision=%+v", event)
		}
	}
}

func TestAutoReviewModeIsProviderIndependentAndDoesNotTakePendingHumanApproval(t *testing.T) {
	unauthed := NewService(context.Background(), config.Default())
	if err := unauthed.setApprovalMode(context.Background(), ApprovalModeAutoReview); err != nil {
		t.Fatalf("automatic mode requires a specific provider: %v", err)
	}
	if unauthed.approvalMode != ApprovalModeAutoReview {
		t.Fatalf("automatic mode=%q", unauthed.approvalMode)
	}

	var reviews atomic.Int32
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		reviews.Add(1)
		writeAutomaticReview(writer, `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"safe"}`, true)
	})
	if err := harness.host.setApprovalMode(context.Background(), ApprovalModePrompt); err != nil {
		t.Fatal(err)
	}
	definition := harness.driver.Definition()
	result := make(chan approvalResolution, 1)
	errs := make(chan error, 1)
	go func() {
		resolution, err := harness.host.awaitTeamApproval(
			context.Background(), "session", "team-run", "team goal",
			tool.Call{ID: "human-pending", Name: definition.Name, Arguments: json.RawMessage(`{"path":"manual.txt"}`)}, definition,
		)
		if err != nil {
			errs <- err
			return
		}
		result <- resolution
	}()
	pending := nextApprovalEvent(t, harness.host, EventApprovalRequested)
	if pending.State != "pending" {
		t.Fatalf("pending event=%+v", pending)
	}
	if err := harness.host.setApprovalMode(context.Background(), ApprovalModeAutoReview); err != nil {
		t.Fatal(err)
	}
	select {
	case resolution := <-result:
		t.Fatalf("automatic mode took over human approval: %+v", resolution)
	case err := <-errs:
		t.Fatalf("pending approval failed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := harness.host.resolveLiveApproval(context.Background(), pending.ApprovalID, "once", "user"); err != nil {
		t.Fatal(err)
	}
	select {
	case resolution := <-result:
		if resolution.Mode != agentservice.ApprovalOnce {
			t.Fatalf("human resolution=%+v", resolution)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("human approval was not delivered")
	}
	if err := harness.authentication.Logout(context.Background(), "chatgpt", "acct"); err != nil {
		t.Fatal(err)
	}
	loggedOut := nextApprovalEvent(t, harness.host, EventAuthState)
	if loggedOut.State != "logged_out" || loggedOut.Data["provider"] != "chatgpt" {
		t.Fatalf("logout event=%+v", loggedOut)
	}
	modeEvent := nextApprovalEvent(t, harness.host, EventApprovalMode)
	if modeEvent.State != "auto_review" || modeEvent.Data["auto_review_available"] != "true" ||
		harness.host.approvalMode != ApprovalModeAutoReview {
		t.Fatalf("logout mode projection=%+v service_mode=%q", modeEvent, harness.host.approvalMode)
	}
	if err := harness.host.ExecuteAction(context.Background(), Action{
		Kind: ActionSetApprovalMode, Target: string(ApprovalModeAutoReview),
	}); err != nil {
		t.Fatalf("automatic mode rejected after provider logout: %v", err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalMode)
	call := tool.Call{ID: "auth-race", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
	reviewPending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(
		context.Background(), "session", "agent-1", "main", harness.run, call, reviewPending,
	)
	if err != nil || resolution.Mode != agentservice.ApprovalDenied ||
		!strings.Contains(resolution.DenialMessage, "(provider)") {
		t.Fatalf("post-logout review=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	failed := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if failed.State != "auto_failed" || failed.Data["error_kind"] != "provider" || reviews.Load() != 0 {
		t.Fatalf("post-logout review event=%+v reviewer_calls=%d", failed, reviews.Load())
	}
}

func TestAutoReviewDenialTrackerThresholdsIsolationAndCleanup(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	for attempt := 1; attempt <= 3; attempt++ {
		err := service.recordAutoReview("run-consecutive", true)
		if attempt < 3 && err != nil {
			t.Fatalf("early consecutive limit at %d: %v", attempt, err)
		}
		if attempt == 3 {
			var limit *AutoReviewDenialLimitError
			if !errors.As(err, &limit) || limit.ConsecutiveDenials != 3 {
				t.Fatalf("consecutive limit=%v", err)
			}
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := service.recordAutoReview("isolated-run", true); err != nil {
			t.Fatalf("denials leaked between runs: %v", err)
		}
	}
	for denial := 1; denial <= 10; denial++ {
		err := service.recordAutoReview("run-window", true)
		if denial < 10 && err != nil {
			t.Fatalf("early window limit at %d: %v", denial, err)
		}
		if denial == 10 {
			var limit *AutoReviewDenialLimitError
			if !errors.As(err, &limit) || limit.RecentDenials != 10 || limit.ConsecutiveDenials != 1 {
				t.Fatalf("window limit=%v", err)
			}
			break
		}
		if err := service.recordAutoReview("run-window", false); err != nil {
			t.Fatal(err)
		}
	}
	service.clearRun("run-consecutive")
	service.mu.Lock()
	_, retained := service.autoReviewDenials["run-consecutive"]
	_, isolated := service.autoReviewDenials["isolated-run"]
	service.mu.Unlock()
	if retained || !isolated {
		t.Fatalf("tracker cleanup retained=%v isolated=%v", retained, isolated)
	}
}

func TestAutoReviewRepeatedDenialsStillRequireUserDecision(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"not authorized"}`, true)
	})
	for attempt := 1; attempt <= 3; attempt++ {
		call := tool.Call{ID: fmt.Sprintf("deny-%d", attempt), Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
		pending := prepareAutomaticApproval(t, harness, call)
		type approvalResult struct {
			resolution approvalResolution
			err        error
		}
		result := make(chan approvalResult, 1)
		go func() {
			resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
			result <- approvalResult{resolution: resolution, err: err}
		}()
		reviewing := nextApprovalEvent(t, harness.host, EventApprovalRequested)
		denied := nextApprovalEvent(t, harness.host, EventApprovalResolved)
		prompt := nextApprovalEvent(t, harness.host, EventApprovalRequested)
		if prompt.State != "pending" || prompt.ApprovalID != reviewing.ApprovalID {
			t.Fatalf("manual fallback %d=%+v", attempt, prompt)
		}
		if attempt == 3 && !strings.Contains(denied.Text, "Repeated automatic denials") {
			t.Fatalf("repeated-denial warning missing: %+v", denied)
		}
		if err := harness.host.ExecuteAction(context.Background(), Action{
			Kind: ActionResolveApproval, Target: prompt.ApprovalID, Decision: "deny",
		}); err != nil {
			t.Fatal(err)
		}
		outcome := <-result
		if outcome.err != nil || outcome.resolution.Mode != agentservice.ApprovalDenied {
			t.Fatalf("user denial %d=%+v error=%v", attempt, outcome.resolution, outcome.err)
		}
	}
	if harness.driver.executions.Load() != 0 {
		t.Fatal("denial threshold executed tool")
	}
}

func newAutoReviewHarness(t *testing.T, handler http.HandlerFunc) autoReviewHarness {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-5.6-luna","title":"GPT-5.6 Luna","supported_reasoning_levels":["low"],"supports_tools":true}]}`))
			return
		}
		handler(writer, request)
	}))
	t.Cleanup(server.Close)
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	chatClient := chatgpt.NewClient()
	chatClient.RevokeURL = ""
	authentication := auth.NewService(store.DB(), credentials, chatClient, grok.NewClient())
	importPath := filepath.Join(t.TempDir(), "codex.json")
	if err := os.WriteFile(importPath, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authentication.ImportChatGPT(ctx, importPath); err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coding.Close(context.Background()) })
	cfg := config.Default()
	cfg.Workspace.Root = t.TempDir()
	modelCatalog := catalog.NewService(store.DB(), authentication)
	modelCatalog.Endpoints["chatgpt"] = server.URL
	runtime, err := NewProviderRuntime(cfg, authentication, modelCatalog, coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime.ChatGPTEndpoint = server.URL
	host := NewService(ctx, cfg)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session"}); err != nil {
		t.Fatal(err)
	}
	host.AttachDurable(sessions, coding)
	host.AttachAuth(authentication, nil)
	host.AttachProviderRuntime(runtime)
	if err := host.setApprovalMode(ctx, ApprovalModeAutoReview); err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRun(ctx, "original user goal")
	if err != nil {
		t.Fatal(err)
	}
	counter := &atomic.Int32{}
	return autoReviewHarness{
		host: host, runtime: runtime, coding: coding, authentication: authentication, run: run,
		driver: countedApprovalDriver{executions: counter},
	}
}

func prepareAutomaticApproval(t *testing.T, harness autoReviewHarness, call tool.Call) agentservice.PendingApproval {
	t.Helper()
	execution, err := harness.coding.ExecuteDriver(context.Background(), harness.run, harness.driver, call, nil)
	if err != nil || execution.Executed || execution.Approval == nil {
		t.Fatalf("prepare approval=%+v error=%v", execution, err)
	}
	if harness.driver.executions.Load() != 0 {
		t.Fatal("tool executed before automatic review")
	}
	return *execution.Approval
}

func writeAutomaticReview(writer http.ResponseWriter, output string, completed bool) {
	writer.Header().Set("Content-Type", "text/event-stream")
	delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": output})
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", delta)
	if completed {
		_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}
}

func writeAutomaticReviewWithUsage(writer http.ResponseWriter, output string) {
	writer.Header().Set("Content-Type", "text/event-stream")
	delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": output})
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", delta)
	_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":11,\"output_tokens\":2,\"total_tokens\":13,\"input_tokens_details\":{\"cached_tokens\":0,\"cache_write_tokens\":0}}}}\n\n")
}

func nextApprovalEvent(t *testing.T, service *Service, kind EventKind) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == kind {
			return event
		}
	}
}

func durableApprovalDecider(t *testing.T, coding *agentservice.Service, runID string) string {
	t.Helper()
	events, err := coding.Runner().ListEvents(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == api.EventApprovalDecided {
			return fmt.Sprint(events[index].Payload["decidedBy"])
		}
	}
	t.Fatalf("run %s has no durable approval decision", runID)
	return ""
}
