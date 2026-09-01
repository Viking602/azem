package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestFailedProviderTurnPersistsStreamedBreakpoint(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "failed-session", Title: "Failed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "failed-session", session.Block{Kind: "user", RunID: "completed-run", Content: "previous request"}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.CompleteTurn(ctx, "failed-session", session.Block{
		Kind: "assistant", RunID: "completed-run", Content: "previous answer", State: "completed",
	}, session.ModelHistory{
		ProviderID: "test", ModelID: "test", InstructionFingerprint: mainInstructionFingerprint,
		StaticPrefixHash: mainInstructionFingerprint, WireVersion: session.CurrentWireVersion,
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "test"), message.NewText(message.RoleUser, "previous request"),
			message.NewText(message.RoleAssistant, "previous answer"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	beforeFailure, err := sessions.LoadProjection(ctx, "failed-session")
	if err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRun(ctx, "keep the failed breakpoint")
	if err != nil {
		t.Fatal(err)
	}
	if err := coding.SealExecutionProfile(ctx, run, agentruntime.ExecutableProfile{
		Provider: "test", AccountID: "test-account", RawModel: "test", Model: "test", Reasoning: "none",
		ActiveSkills: []string{}, ToolSetHash: "test-tools", ToolProfileHash: "test-tool-profile",
		StaticIdentity: "failed-stream-test", WorkspaceAnchor: workspace,
		PromptFingerprint: "failed-stream-prompt", ToolSchemaFingerprint: "failed-stream-schema",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "failed-session", session.Block{
		Kind: "user", RunID: run.RunID, Title: "You", Content: "keep the failed breakpoint",
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, coding)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	service.wg.Add(1)
	service.runProviderTurn(ctx, TurnRequest{
		SessionID: "failed-session", Prompt: "keep the failed breakpoint", Provider: "test", Model: "test",
	}, run, hyagent.Engine{
		Provider: &compactionTestDriver{streams: [][]hyprovider.Event{{
			{Kind: hyprovider.EventTextDelta, Text: "work completed before "},
			{Kind: hyprovider.EventTextDelta, Text: "the provider failed"},
			{Kind: hyprovider.EventError, Err: errors.New("provider failed")},
		}}},
		Model: "test", ContextBuilder: turnContext{instructions: "test"},
	})
	projection, err := sessions.LoadProjection(ctx, "failed-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 4 || projection.Blocks[3].Kind != "assistant" || projection.Blocks[3].State != "failed" ||
		projection.Blocks[3].Content != "work completed before the provider failed" {
		t.Fatalf("failed breakpoint blocks=%+v", projection.Blocks)
	}
	failureData := projection.Blocks[3].Data
	if failureData["failureKind"] != string(hyagent.FailureKindEngineError) {
		t.Fatalf("failure data=%v, want engine_error", failureData)
	}
	if _, ok := failureData["stopReason"]; !ok {
		t.Fatalf("failure data=%v, want exact stop reason", failureData)
	}
	if failureData["usage"] == "" {
		t.Fatalf("failure data=%v, want partial usage", failureData)
	}
	if !reflect.DeepEqual(projection.ModelHistory, beforeFailure.ModelHistory) ||
		projection.CheckpointGeneration != beforeFailure.CheckpointGeneration {
		t.Fatalf("failed breakpoint changed checkpoint:\n got=%+v generation=%d\nwant=%+v generation=%d",
			projection.ModelHistory, projection.CheckpointGeneration, beforeFailure.ModelHistory, beforeFailure.CheckpointGeneration)
	}
}

func TestProviderTurnSuspensionEmitsRecoveryWithoutTerminalFailure(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "suspended-session", Title: "Suspended"}); err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRun(ctx, "perform one durable write")
	if err != nil {
		t.Fatal(err)
	}
	if err := coding.SealExecutionProfile(ctx, run, agentruntime.ExecutableProfile{
		Provider: "test", AccountID: "test-account", RawModel: "test", Model: "test", Reasoning: "none",
		ActiveSkills: []string{}, ToolSetHash: "durable-write", ToolProfileHash: "durable-write-profile",
		StaticIdentity: "suspension-test", WorkspaceAnchor: workspace,
		PromptFingerprint: "perform-one-durable-write", ToolSchemaFingerprint: "durable-write-schema",
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, coding)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	driver := &uncertainActionDriver{}
	engine := service.bindProviderEngine(hyagent.Engine{
		Provider: &compactionTestDriver{streams: [][]hyprovider.Event{{
			{Kind: hyprovider.EventToolCall, ToolCall: &message.ToolCall{
				ID: "durable-write", Name: "durable.write", Arguments: json.RawMessage(`{"value":1}`),
			}},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonToolUse},
		}}},
		Model: "test", ContextBuilder: turnContext{instructions: "test"}, Tools: tool.NewBus(driver),
	})

	service.wg.Add(1)
	service.runProviderTurn(ctx, TurnRequest{
		SessionID: "suspended-session", Prompt: "perform one durable write", Provider: "test", Model: "test",
	}, run, engine)

	var recovery Event
	for {
		eventCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		event, eventErr := service.NextEvent(eventCtx)
		cancel()
		if eventErr != nil {
			break
		}
		switch event.Kind {
		case EventRecoveryState:
			recovery = event
		case EventRunFailed, EventRunFinished:
			t.Fatalf("suspended turn emitted terminal event: %#v", event)
		}
	}
	if recovery.State != "suspended" || recovery.Data["kind"] != string(agentservice.SuspensionReconciliation) {
		t.Fatalf("recovery event = %#v", recovery)
	}
	if driver.calls != 1 {
		t.Fatalf("durable side effect calls = %d, want one", driver.calls)
	}
	tasks, err := coding.ListTasks(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != run.TaskID {
		t.Fatalf("durable tasks = %#v, want task %q", tasks, run.TaskID)
	}
	if tasks[0].Status != agentruntime.TaskStatusReconcileRequired {
		t.Fatalf("durable task status = %q, want reconcile_required", tasks[0].Status)
	}
}

type uncertainActionDriver struct {
	calls int
}

func (*uncertainActionDriver) Definition() tool.Definition {
	return tool.Definition{Name: "durable.write"}
}

func (*uncertainActionDriver) ToolPolicy() agentruntime.ToolPolicy {
	return agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectExternalSideEffect, RequiresActionTask: true,
	}
}

func (d *uncertainActionDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	d.calls++
	return tool.Result{ToolCallID: call.ID, Name: call.Name}, errors.New("connection lost after write")
}

func TestProviderTurnDoesNotRetryPartialAssistant(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "retry-session", Title: "Retry"}); err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRunWithMetadata(ctx, "do not replay this turn", nil, agentservice.RunExecutionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "retry-session", session.Block{
		Kind: "user", RunID: run.RunID, Title: "You", Content: "do not replay this turn",
	}); err != nil {
		t.Fatal(err)
	}
	if err := coding.SealExecutionProfile(ctx, run, agentruntime.ExecutableProfile{
		Provider: "test", AccountID: "test-account", RawModel: "test", Model: "test", Reasoning: "none",
		ActiveSkills: []string{}, ToolSetHash: "test-tools", ToolProfileHash: "test-tool-profile",
		StaticIdentity: "test-static", WorkspaceAnchor: workspace,
		PromptFingerprint: "test-prompt", ToolSchemaFingerprint: "test-tool-schema",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Retry.BaseDelayDuration = 0
	cfg.Retry.MaxRetries = 2
	service := NewService(ctx, cfg)
	service.AttachDurable(sessions, coding)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		if err := store.Close(shutdownCtx); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	physical := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventThinkingDelta, Thinking: "discarded partial reasoning"},
			{Kind: hyprovider.EventTextDelta, Text: "uncommitted partial"},
			{Kind: hyprovider.EventError, Err: &responses.APIError{Kind: responses.ErrorRateLimit, Code: "server_is_overloaded"}},
		},
		{
			{Kind: hyprovider.EventTextDelta, Text: "must not be requested"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
	}}
	metered := &meteredProviderDriver{
		inner: physical, store: sessions, host: service, sessionID: "retry-session",
		runID: run.RunID, kind: "main", provider: "test", model: "test", transport: "fixture",
	}
	driver := retryProviderDriver(ctx, service, "retry-session", run.RunID, "test", cfg.Retry, metered)
	service.wg.Add(1)
	service.runProviderTurn(ctx, TurnRequest{
		SessionID: "retry-session", Prompt: "do not replay this turn", Provider: "test", Model: "test",
	}, run, hyagent.Engine{Provider: driver, Model: "test", ContextBuilder: turnContext{instructions: "test"}})

	eventCtx, eventCancel := context.WithTimeout(ctx, time.Second)
	defer eventCancel()
	var emitted []Event
	var terminal Event
	for {
		event, nextErr := service.NextEvent(eventCtx)
		if nextErr != nil {
			t.Fatalf("waiting for failed or suspended turn: %v; events=%+v", nextErr, emitted)
		}
		emitted = append(emitted, event)
		if event.Kind == EventRunFailed || event.Kind == EventRecoveryState {
			terminal = event
			break
		}
		if event.Kind == EventRunFinished {
			t.Fatalf("partial provider stream completed successfully: %+v", emitted)
		}
	}
	if terminal.Kind == EventRecoveryState && terminal.State != "suspended" {
		t.Fatalf("partial provider stream recovery state=%+v", terminal)
	}
	sawPartial := false
	for _, event := range emitted {
		if event.Kind == EventProviderRetry {
			t.Fatalf("partial provider stream retried: %+v", emitted)
		}
		if event.Kind == EventTextDelta && strings.Contains(event.Text, "uncommitted partial") {
			sawPartial = true
		}
		if event.Kind == EventTextDelta && strings.Contains(event.Text, "must not be requested") {
			t.Fatalf("second physical request reached the UI: %+v", emitted)
		}
	}
	if !sawPartial {
		t.Fatalf("partial output was not surfaced before failure: %+v", emitted)
	}
	if len(physical.requests) != 1 {
		t.Fatalf("physical provider requests=%d, want exactly one", len(physical.requests))
	}
	var requestCount int
	var requestStatus string
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(status), '') FROM provider_requests WHERE run_id = ?`, run.RunID).Scan(&requestCount, &requestStatus); err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 || requestStatus != "unknown" {
		t.Fatalf("provider ledger count=%d status=%q, want one unknown physical request", requestCount, requestStatus)
	}
	projection, err := sessions.LoadProjection(ctx, "retry-session")
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range projection.Blocks {
		if strings.Contains(block.Content, "must not be requested") || block.State == "completed" && block.Kind == "assistant" {
			t.Fatalf("failed partial turn committed as a recovered answer: %+v", projection.Blocks)
		}
	}
}

func TestAuthenticatedTurnStreamsGovernedWriteAndCompletesDurably(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	if output, err := exec.Command("git", "init", workspace).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	var responseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" || request.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Errorf("provider auth headers = %q %q", request.Header.Get("Authorization"), request.Header.Get("ChatGPT-Account-ID"))
		}
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-test","title":"GPT Test","context_window":128000,"supported_reasoning_levels":["minimal","high"],"default_reasoning_level":"high","supports_tools":true,"service_tiers":[{"id":"priority","name":"Fast"}]}]}`))
		case "/responses":
			var payload struct {
				Model       string         `json:"model"`
				Reasoning   map[string]any `json:"reasoning"`
				ServiceTier string         `json:"service_tier"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode provider request: %v", err)
			} else if payload.Model != "gpt-test" || payload.Reasoning["effort"] != "minimal" || payload.ServiceTier != "priority" {
				t.Errorf("provider selection = model:%q reasoning:%v service_tier:%q", payload.Model, payload.Reasoning, payload.ServiceTier)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			switch responseCalls.Add(1) {
			case 1:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"write-1\",\"name\":\"coding.write_file\",\"arguments\":\"{\\\"path\\\":\\\"created.txt\\\",\\\"content\\\":\\\"created by agent\\\\n\\\"}\"}}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14,\"input_tokens_details\":{\"cached_tokens\":6,\"cache_write_tokens\":2}}}}\n\n")
			case 2:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Created and verified.\"}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-2\",\"status\":\"completed\",\"usage\":{\"input_tokens\":20,\"output_tokens\":6,\"total_tokens\":26,\"input_tokens_details\":{\"cached_tokens\":15,\"cache_write_tokens\":3}}}}\n\n")
			case 3:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-3a\",\"call_id\":\"readback-1\",\"name\":\"coding.read_file\",\"arguments\":\"{\\\"path\\\":\\\"created.txt\\\"}\"}}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-3b\",\"call_id\":\"diff-check-1\",\"name\":\"coding.shell\",\"arguments\":\"{\\\"command\\\":\\\"git diff --check -- created.txt\\\",\\\"wall_clock_seconds\\\":60}\"}}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-3\",\"status\":\"completed\",\"usage\":{\"input_tokens\":25,\"output_tokens\":8,\"total_tokens\":33}}}\n\n")
			case 4:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Created and verified.\"}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-4\",\"status\":\"completed\",\"usage\":{\"input_tokens\":30,\"output_tokens\":6,\"total_tokens\":36}}}\n\n")
			default:
				t.Errorf("unexpected response call %d", responseCalls.Load())
			}
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	importPath := filepath.Join(t.TempDir(), "codex.json")
	if err := os.WriteFile(importPath, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authentication.ImportChatGPT(ctx, importPath); err != nil {
		t.Fatal(err)
	}
	modelCatalog := catalog.NewService(store.DB(), authentication)
	modelCatalog.Endpoints["chatgpt"] = server.URL + "/models"
	modelCatalog.AdditionalEndpoints["chatgpt"] = nil
	coding, err := agentservice.NewService(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	cfg.Providers.ChatGPT.FastMode = true
	providerRuntime, err := NewProviderRuntime(cfg, authentication, modelCatalog, coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	providerRuntime.ChatGPTEndpoint = server.URL + "/responses"
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "default", Title: "Test", ProviderID: "chatgpt", ModelID: "gpt-test", Reasoning: "high", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, cfg)
	service.AttachDurable(sessions, coding)
	service.AttachAuth(authentication, modelCatalog)
	service.AttachProviderRuntime(providerRuntime)

	runID, err := service.StartConfiguredTurn(TurnRequest{SessionID: "default", Prompt: "create created.txt", Provider: "chatgpt", Model: "gpt-test", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := coding.LoadRunExecutionManifest(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := coding.ListTasks(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if len(task.ResourceClaims) != 0 {
			t.Fatalf("session tasks must not take exclusive workspace claims: %#v", tasks)
		}
	}
	if manifest.AccountID != "acct" || manifest.RawModel != "gpt-test" || manifest.Model != "gpt-test" ||
		!manifest.Sealed || manifest.StaticIdentity == "" || manifest.ToolSchemaFingerprint == "" {
		t.Fatalf("durable execution manifest=%+v", manifest)
	}
	var output strings.Builder
	approved := false
	var contextUsage [][3]string
	var cacheWrites []string
	estimatedUsage := 0
	var toolLifecycle []string
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(deadline)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventTextDelta:
			if event.TextPhase != string(hyprovider.TextPhaseCommentary) {
				output.WriteString(event.Text)
			}
		case EventProviderRetry:
			if event.State == "restarted" && event.Data["scope"] == "output_guard" {
				output.Reset()
			}
		case EventContextUsage:
			if event.State == "reported" {
				if event.Data["inputTokens"] != "" {
					contextUsage = append(contextUsage, [3]string{event.Data["inputTokens"], event.Data["cachedInputTokens"], event.Data["outputTokens"]})
				}
				if event.Data["cacheWriteTokens"] != "" {
					cacheWrites = append(cacheWrites, event.Data["cacheWriteTokens"])
				}
			} else if event.State == "estimated" {
				estimatedUsage++
			}
		case EventToolStarted, EventToolUpdate:
			if event.ToolCallID == "write-1" &&
				(event.State == "queued" || event.State == "awaiting_approval" || event.State == "running") {
				toolLifecycle = append(toolLifecycle, event.State)
			}
		case EventApprovalRequested:
			switch event.ToolCallID {
			case "write-1":
				if event.Data["tool"] != "coding.write_file" {
					t.Fatalf("approval event = %+v", event)
				}
				approved = true
			case "diff-check-1":
				if !approved || event.Data["tool"] != "coding.shell" {
					t.Fatalf("verification approval event = %+v", event)
				}
			default:
				t.Fatalf("unexpected approval event = %+v", event)
			}
			if err := service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
				t.Fatal(err)
			}
		case EventRunFailed:
			t.Fatalf("run failed: %s", event.Text)
		case EventRunFinished:
			goto finished
		}
	}

finished:
	if !approved || output.String() != "Created and verified." || responseCalls.Load() != 4 {
		t.Fatalf("turn = approved:%v output:%q response calls:%d", approved, output.String(), responseCalls.Load())
	}
	if want := []string{"queued", "awaiting_approval", "running"}; !reflect.DeepEqual(toolLifecycle, want) {
		t.Fatalf("tool lifecycle = %v, want %v", toolLifecycle, want)
	}
	if want := [][3]string{{"10", "6", "4"}, {"20", "15", "6"}, {"25", "0", "8"}, {"30", "0", "6"}}; !reflect.DeepEqual(contextUsage, want) {
		t.Fatalf("context usage events = %v, want %v", contextUsage, want)
	}
	if !reflect.DeepEqual(cacheWrites, []string{"2", "3", "0", "0"}) {
		t.Fatalf("cache write events = %v", cacheWrites)
	}
	if estimatedUsage < 2 {
		t.Fatalf("estimated context usage events = %d, want at least 2", estimatedUsage)
	}
	contents, err := os.ReadFile(filepath.Join(workspace, "created.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "created by agent\n" {
		t.Fatalf("created file = %q", contents)
	}
	projection, err := coding.Recover(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Run.Status != "completed" {
		t.Fatalf("durable run status = %q", projection.Run.Status)
	}
	sessionProjection, err := sessions.LoadProjection(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if sessionProjection.Session.Reasoning != "minimal" {
		t.Fatalf("persisted reasoning = %q, want minimal", sessionProjection.Session.Reasoning)
	}
	if sessionProjection.Usage.CacheWriteTokens != 5 || sessionProjection.Usage.MainCacheWrite != 5 {
		t.Fatalf("persisted cache writes = %+v", sessionProjection.Usage)
	}
	if len(sessionProjection.Blocks) != 4 ||
		sessionProjection.Blocks[1].Kind != "commentary" ||
		sessionProjection.Blocks[1].Content != fallbackToolAnnouncement ||
		sessionProjection.Blocks[1].Data["synthetic"] != fallbackToolAnnouncementSynthetic ||
		sessionProjection.Blocks[2].Kind != "commentary" ||
		sessionProjection.Blocks[2].Content != fallbackToolAnnouncement ||
		sessionProjection.Blocks[2].Data["synthetic"] != fallbackToolAnnouncementSynthetic ||
		sessionProjection.Blocks[3].Kind != "assistant" ||
		sessionProjection.Blocks[3].TextPhase != string(hyprovider.TextPhaseFinalAnswer) ||
		sessionProjection.Blocks[3].Content != "Created and verified." {
		t.Fatalf("session projection = %+v", sessionProjection.Blocks)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestProviderStreamSinkDoesNotReportMissingUsageAsZero(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	if err := service.providerStreamSink("session", "run", "grok", "model", "high", "xai-responses").Emit(context.Background(), hyagent.Frame{Kind: hyagent.FrameDone}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventContextUsage || event.State != "reported" {
		t.Fatalf("event = %+v", event)
	}
	if event.Data["inputTokens"] != "" || event.Data["cachedInputTokens"] != "" || event.Data["outputTokens"] != "" || event.Data["totalTokens"] != "" || event.Data["cacheStatus"] != "" {
		t.Fatalf("missing provider usage was reported as tokens: %+v", event.Data)
	}
}

func TestReasoningTraceCollectorSeparatesDiscreteBoldTitles(t *testing.T) {
	var collector reasoningTraceCollector
	collector.append("**Inspecting workspace**")
	collector.append("**Planning fix**")
	collector.commit(false)
	if got, want := collector.text(), "**Inspecting workspace**\n\n**Planning fix**"; got != want {
		t.Fatalf("thinking trace = %q, want %q", got, want)
	}
}
