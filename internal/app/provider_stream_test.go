package app

import (
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
	"sync/atomic"
	"testing"
	"time"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/stream"
	"github.com/Viking602/venat/tool"
	hyworker "github.com/Viking602/venat/worker"

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
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB())
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
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "suspended-session", Title: "Suspended"}); err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRun(ctx, "perform one durable write")
	if err != nil {
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
	if recovery.State != "suspended" || recovery.Data["kind"] != string(hyworker.SuspensionReconciliation) {
		t.Fatalf("recovery event = %#v", recovery)
	}
	if driver.calls != 1 {
		t.Fatalf("durable side effect calls = %d, want one", driver.calls)
	}
	task, err := coding.Runner().Task(ctx, run.RunID, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != api.TaskStatusReconcileRequired {
		t.Fatalf("durable task status = %q, want reconcile_required", task.Status)
	}
}

type uncertainActionDriver struct {
	calls int
}

func (*uncertainActionDriver) Definition() tool.Definition {
	return tool.Definition{
		Name:               "durable.write",
		EffectType:         tool.EffectExternalSideEffect,
		RequiresActionTask: true,
	}
}

func (d *uncertainActionDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	d.calls++
	return tool.Result{ToolCallID: call.ID, Name: call.Name}, errors.New("connection lost after write")
}

func TestProviderTurnAutoRetryDiscardsPartialAssistant(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "retry-session", Title: "Retry"}); err != nil {
		t.Fatal(err)
	}
	run, err := coding.StartRunWithMetadata(ctx, "recover this turn", nil, agentservice.RunExecutionPolicy{
		RetryPolicy: api.RetryPolicy{MaxAttempts: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendBlock(ctx, "retry-session", session.Block{
		Kind: "user", RunID: run.RunID, Title: "You", Content: "recover this turn",
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
	})
	driver := &compactionTestDriver{streams: [][]hyprovider.Event{
		{
			{Kind: hyprovider.EventThinkingDelta, Thinking: "discarded partial reasoning"},
			{Kind: hyprovider.EventTextDelta, Text: "uncommitted partial"},
			{Kind: hyprovider.EventError, Err: &responses.APIError{Kind: responses.ErrorRateLimit, Code: "server_is_overloaded"}},
		},
		{
			{Kind: hyprovider.EventThinkingDelta, Thinking: "**Recovered reasoning**"},
			{Kind: hyprovider.EventTextDelta, Text: "recovered answer"},
			{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
		},
	}}
	service.wg.Add(1)
	service.runProviderTurn(ctx, TurnRequest{
		SessionID: "retry-session", Prompt: "recover this turn", Provider: "test", Model: "test",
	}, run, hyagent.Engine{Provider: driver, Model: "test", ContextBuilder: turnContext{instructions: "test"}})
	eventCtx, eventCancel := context.WithTimeout(ctx, time.Second)
	defer eventCancel()
	var emitted []Event
	for {
		event, nextErr := service.NextEvent(eventCtx)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		emitted = append(emitted, event)
		if event.Kind == EventRunFinished {
			break
		}
	}
	retryIndex, firstTextIndex, recoveredTextIndex := -1, -1, -1
	for index, event := range emitted {
		if event.Kind == EventProviderRetry && event.State == "restarted" {
			retryIndex = index
		}
		if event.Kind == EventTextDelta && strings.Contains(event.Text, "uncommitted partial") {
			firstTextIndex = index
		}
		if event.Kind == EventTextDelta && strings.Contains(event.Text, "recovered answer") {
			recoveredTextIndex = index
		}
	}
	if firstTextIndex < 0 || retryIndex <= firstTextIndex || recoveredTextIndex <= retryIndex {
		t.Fatalf("retry event ordering first=%d retry=%d recovered=%d events=%+v", firstTextIndex, retryIndex, recoveredTextIndex, emitted)
	}
	childBlocks := discardAgentAttemptBlocks([]AgentTranscriptBlock{
		{Kind: "tool", RunID: "child", Content: "keep"},
		{Kind: "thinking", RunID: "child", Content: "discard"},
		{Kind: "assistant", RunID: "child", Content: "discard"},
	}, "child")
	if len(childBlocks) != 1 || childBlocks[0].Kind != "tool" {
		t.Fatalf("subagent retry retained uncommitted blocks: %+v", childBlocks)
	}

	if len(driver.requests) != 2 {
		t.Fatalf("provider requests = %d, want initial request plus one session retry", len(driver.requests))
	}
	for _, current := range driver.requests[1].Messages {
		if strings.Contains(current.Text, "uncommitted partial") {
			t.Fatalf("failed partial assistant leaked into retry context: %#v", driver.requests[1].Messages)
		}
	}
	projection, err := sessions.LoadProjection(ctx, "retry-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) != 3 ||
		projection.Blocks[1].Kind != "thinking" ||
		projection.Blocks[1].State != "completed" ||
		projection.Blocks[1].Content != "**Recovered reasoning**" ||
		projection.Blocks[2].Kind != "assistant" ||
		projection.Blocks[2].State != "completed" ||
		projection.Blocks[2].Content != "recovered answer" {
		t.Fatalf("recovered transcript blocks=%+v", projection.Blocks)
	}
	if err := message.ValidateCompleteTurns(projection.ModelHistory.Messages); err != nil {
		t.Fatalf("recovered model history is incomplete: %v", err)
	}
}

func TestAuthenticatedTurnStreamsGovernedWriteAndCompletesDurably(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
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
			if responseCalls.Add(1) == 1 {
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"write-1\",\"name\":\"coding.write_file\",\"arguments\":\"{\\\"path\\\":\\\"created.txt\\\",\\\"content\\\":\\\"created by agent\\\\n\\\"}\"}}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14,\"input_tokens_details\":{\"cached_tokens\":6,\"cache_write_tokens\":2}}}}\n\n")
				return
			}
			_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Created and verified.\"}\n\n")
			_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-2\",\"status\":\"completed\",\"usage\":{\"input_tokens\":20,\"output_tokens\":6,\"total_tokens\":26,\"input_tokens_details\":{\"cached_tokens\":15,\"cache_write_tokens\":3}}}}\n\n")
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
	sessions := session.NewService(store.DB())
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
	durable, err := coding.Runner().Run(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := coding.Runner().ListTasks(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	wantClaim, err := workspaceWriteClaim(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var workerClaimFound bool
	for _, task := range tasks {
		if task.ID == durable.RootTaskID {
			if len(task.ResourceClaims) != 0 {
				t.Fatalf("top-level root task claims=%#v", task.ResourceClaims)
			}
			continue
		}
		for _, taskClaim := range task.ResourceClaims {
			if taskClaim == wantClaim {
				workerClaimFound = true
			}
		}
	}
	if !workerClaimFound {
		t.Fatalf("top-level worker claims=%#v, want %#v", tasks, wantClaim)
	}
	manifest, err := decodeSingleRunManifest(durable.Metadata["single_run_manifest"])
	if err != nil || manifest.AccountID != "acct" {
		t.Fatalf("durable account binding=%q error=%v", manifest.AccountID, err)
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
			if event.ToolCallID != "write-1" || event.Data["tool"] != "coding.write_file" {
				t.Fatalf("approval event = %+v", event)
			}
			if err := service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
				t.Fatal(err)
			}
			approved = true
		case EventRunFailed:
			t.Fatalf("run failed: %s", event.Text)
		case EventRunFinished:
			goto finished
		}
	}

finished:
	if !approved || output.String() != "Created and verified." || responseCalls.Load() != 2 {
		t.Fatalf("turn = approved:%v output:%q response calls:%d", approved, output.String(), responseCalls.Load())
	}
	if want := []string{"queued", "awaiting_approval", "running"}; !reflect.DeepEqual(toolLifecycle, want) {
		t.Fatalf("tool lifecycle = %v, want %v", toolLifecycle, want)
	}
	if want := [][3]string{{"10", "6", "4"}, {"20", "15", "6"}}; !reflect.DeepEqual(contextUsage, want) {
		t.Fatalf("context usage events = %v, want %v", contextUsage, want)
	}
	if !reflect.DeepEqual(cacheWrites, []string{"2", "3"}) {
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
	if len(sessionProjection.Blocks) != 3 ||
		sessionProjection.Blocks[1].Kind != "commentary" ||
		sessionProjection.Blocks[1].Content != fallbackToolAnnouncement ||
		sessionProjection.Blocks[1].Data["synthetic"] != fallbackToolAnnouncementSynthetic ||
		sessionProjection.Blocks[2].Kind != "assistant" ||
		sessionProjection.Blocks[2].TextPhase != string(hyprovider.TextPhaseFinalAnswer) ||
		sessionProjection.Blocks[2].Content != "Created and verified." {
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
	if err := service.providerStreamSink("session", "run", "grok", "model", "high", "xai-responses").Emit(context.Background(), stream.Frame{Kind: stream.FrameDone}); err != nil {
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
