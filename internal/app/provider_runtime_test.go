package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/stream"
	"github.com/Viking602/go-hydaelyn/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

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
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-test","title":"GPT Test","context_window":128000,"supported_reasoning_levels":["minimal","high"],"default_reasoning_level":"high","supports_tools":true}]}`))
		case "/responses":
			var payload struct {
				Reasoning map[string]any `json:"reasoning"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode provider request: %v", err)
			} else if payload.Reasoning["effort"] != "minimal" {
				t.Errorf("reasoning effort = %v, want minimal", payload.Reasoning)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			if responseCalls.Add(1) == 1 {
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"write-1\",\"name\":\"coding.write_file\",\"arguments\":\"{\\\"path\\\":\\\"created.txt\\\",\\\"content\\\":\\\"created by agent\\\\n\\\"}\"}}\n\n")
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14,\"input_tokens_details\":{\"cached_tokens\":6}}}}\n\n")
				return
			}
			_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Created and verified.\"}\n\n")
			_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-2\",\"status\":\"completed\",\"usage\":{\"input_tokens\":20,\"output_tokens\":6,\"total_tokens\":26,\"input_tokens_details\":{\"cached_tokens\":15}}}}\n\n")
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
	var output strings.Builder
	approved := false
	var contextUsage [][3]string
	estimatedUsage := 0
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
			output.WriteString(event.Text)
		case EventContextUsage:
			if event.State == "reported" {
				contextUsage = append(contextUsage, [3]string{event.Data["inputTokens"], event.Data["cachedInputTokens"], event.Data["outputTokens"]})
			} else if event.State == "estimated" {
				estimatedUsage++
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
	if want := [][3]string{{"10", "6", "4"}, {"20", "15", "6"}}; !reflect.DeepEqual(contextUsage, want) {
		t.Fatalf("context usage events = %v, want %v", contextUsage, want)
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
	if len(sessionProjection.Blocks) != 2 || sessionProjection.Blocks[1].Content != "Created and verified." {
		t.Fatalf("session projection = %+v", sessionProjection.Blocks)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestTurnContextBuildsPriorConversationBeforeCurrentRequest(t *testing.T) {
	contextManager := turnContext{
		instructions: "system rules",
		history: []session.Block{
			{Kind: "user", Content: "first request"},
			{Kind: "assistant", Content: "first answer"},
		},
	}
	messages, err := contextManager.Build(context.Background(), api.Task{Goal: "follow-up request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("message count=%d", len(messages))
	}
	if messages[0].Role != message.RoleSystem || messages[0].Text != "system rules" ||
		messages[1].Role != message.RoleUser || messages[1].Text != "first request" ||
		messages[2].Role != message.RoleAssistant || messages[2].Text != "first answer" ||
		messages[3].Role != message.RoleUser || messages[3].Text != "follow-up request" {
		t.Fatalf("messages=%+v", messages)
	}
}

func TestTurnContextRefreshesTodoReminderAfterMutation(t *testing.T) {
	latest := session.TodoList{
		Goal: "ship todo", Revision: 2,
		Phases: []session.TodoPhase{{ID: "phase-1", Title: "Build", Items: []session.TodoItem{
			{ID: "item-1", Content: "finished", Status: session.TodoCompleted},
			{ID: "item-2", Content: "verify", Status: session.TodoInProgress},
		}}},
	}
	manager := turnContext{loadTodo: func(context.Context) (session.TodoList, error) { return latest, nil }}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleSystem, todoReminder(session.TodoList{Goal: "ship todo", Revision: 1})),
		message.NewText(message.RoleUser, "continue"),
	}
	refreshed, err := manager.CompactTo(context.Background(), history, 0)
	if err != nil {
		t.Fatal(err)
	}
	reminders := 0
	for _, current := range refreshed {
		if strings.HasPrefix(current.Text, todoReminderPrefix) {
			reminders++
			if !strings.Contains(current.Text, "revision=2") || !strings.Contains(current.Text, "item-2:in_progress:verify") || strings.Contains(current.Text, "revision=1") {
				t.Fatalf("stale todo reminder: %q", current.Text)
			}
		}
	}
	if reminders != 1 {
		t.Fatalf("todo reminders=%d, want 1: %+v", reminders, refreshed)
	}
}

func TestTurnContextCompactPreservesFullSystemPrefixAndFreshTodo(t *testing.T) {
	latest := session.TodoList{Goal: "ship", Revision: 4, Phases: []session.TodoPhase{{
		ID: "phase", Title: "Build", Items: []session.TodoItem{{ID: "current", Content: "verify", Status: session.TodoInProgress}},
	}}}
	manager := turnContext{loadTodo: func(context.Context) (session.TodoList, error) { return latest, nil }}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleSystem, todoReminder(session.TodoList{Goal: "ship", Revision: 1})),
	}
	for index := 0; index < 20; index++ {
		role := message.RoleUser
		if index%2 == 1 {
			role = message.RoleAssistant
		}
		history = append(history, message.NewText(role, fmt.Sprintf("message %d", index)))
	}
	compacted, err := manager.Compact(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	if len(compacted) >= len(history) || compacted[0].Text != "system rules" {
		t.Fatalf("unexpected compacted history: %+v", compacted)
	}
	reminders := 0
	for _, current := range compacted {
		if strings.HasPrefix(current.Text, todoReminderPrefix) {
			reminders++
			if !strings.Contains(current.Text, "revision=4") {
				t.Fatalf("stale compact reminder: %q", current.Text)
			}
		}
	}
	if reminders != 1 {
		t.Fatalf("todo reminder count=%d", reminders)
	}
}

func TestTurnContextCompactToFitsTargetAndPreservesLatestRequest(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "old request"),
		message.NewText(message.RoleAssistant, strings.Repeat("old result ", 600)),
		message.NewText(message.RoleUser, "current request"),
	}
	const target = 500
	compacted, err := (turnContext{}).CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	if estimated := estimateContextTokens(compacted); estimated > target {
		t.Fatalf("estimated compacted tokens = %d, target = %d", estimated, target)
	}
	if compacted[0].Text != "system rules" || compacted[len(compacted)-1].Text != "current request" {
		t.Fatalf("compacted history lost mandatory context: %#v", compacted)
	}
	if len(compacted) >= len(history) {
		t.Fatalf("history was not compacted: %#v", compacted)
	}
	again, err := (turnContext{}).CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compacted, again) {
		t.Fatalf("compaction is not deterministic:\nfirst: %#v\nsecond: %#v", compacted, again)
	}
}

func TestTurnContextCompactToTruncatesOversizedToolResultWithoutSplittingTurn(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "read the file"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read-1", Name: "read_file"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "read-1", Name: "read_file", Content: "prefix" + string([]byte{0xff}) + strings.Repeat("文件内容", 2_000)}),
	}
	const target = 1_000
	compacted, err := (turnContext{}).CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := message.ValidateCompleteTurns(compacted); err != nil {
		t.Fatalf("compaction split a tool turn: %v", err)
	}
	last := compacted[len(compacted)-1]
	if last.ToolResult == nil || !strings.Contains(last.ToolResult.Content, "[tool result truncated") {
		t.Fatalf("oversized tool result was not truncated: %#v", last)
	}
	if !utf8.ValidString(last.ToolResult.Content) {
		t.Fatal("truncated tool result is not valid UTF-8")
	}
	if estimated := estimateContextTokens(compacted); estimated > target {
		t.Fatalf("estimated compacted tokens = %d, target = %d", estimated, target)
	}
}

func TestTurnContextCompactToIgnoresAndClearsDuplicatedStructuredToolOutput(t *testing.T) {
	content := strings.Repeat("package recovery\n", 800)
	structured, err := json.Marshal(map[string]any{"content": content, "lineCount": 800})
	if err != nil {
		t.Fatal(err)
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "inspect recovery"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read-1", Name: "coding.read_file"}}},
		message.NewToolResult(message.ToolResult{
			ToolCallID: "read-1", Name: "coding.read_file", Content: content, Structured: structured,
		}),
	}

	const target = 2_000
	compacted, err := (turnContext{}).CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	if estimated := estimateContextTokens(compacted); estimated > target {
		t.Fatalf("estimated compacted tokens = %d, target = %d", estimated, target)
	}
	result := compacted[len(compacted)-1].ToolResult
	if result == nil || !strings.Contains(result.Content, "[tool result truncated") {
		t.Fatalf("large tool result was not truncated: %#v", result)
	}
	if len(result.Structured) != 0 {
		t.Fatalf("truncated result retained duplicated structured output (%d bytes)", len(result.Structured))
	}
}

func TestTurnContextCompactToUsesContentInsteadOfDuplicatedStructuredOutput(t *testing.T) {
	content := strings.Repeat("x", 3_000)
	structured, err := json.Marshal(map[string]string{"content": strings.Repeat("y", 8_000)})
	if err != nil {
		t.Fatal(err)
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"),
		message.NewText(message.RoleUser, "inspect"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read-1", Name: "coding.read_file"}}},
		message.NewToolResult(message.ToolResult{
			ToolCallID: "read-1", Name: "coding.read_file", Content: content, Structured: structured,
		}),
	}

	const target = 1_000
	compacted, err := (turnContext{}).CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	result := compacted[len(compacted)-1].ToolResult
	if result == nil || result.Content != content || !bytes.Equal(result.Structured, structured) {
		t.Fatalf("provider-visible content was needlessly compacted: %#v", result)
	}
}

func TestTurnContextCompactToTruncatesMandatoryResultWhenOldResultsExhaustInitialShare(t *testing.T) {
	history := []message.Message{message.NewText(message.RoleSystem, "rules")}
	for index := 0; index < 260; index++ {
		id := fmt.Sprintf("old-%d", index)
		history = append(history,
			message.NewText(message.RoleUser, "old"),
			message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: id, Name: "read"}}},
			message.NewToolResult(message.ToolResult{ToolCallID: id, Name: "read", Content: "ok"}),
		)
	}
	history = append(history,
		message.NewText(message.RoleUser, "latest"),
		message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "latest", Name: "read"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "latest", Name: "read", Content: strings.Repeat("z", 2_000)}),
	)

	const target = 64
	compacted, err := (turnContext{}).CompactTo(context.Background(), history, target)
	if err != nil {
		t.Fatal(err)
	}
	if estimated := estimateContextTokens(compacted); estimated > target {
		t.Fatalf("estimated compacted tokens = %d, target = %d", estimated, target)
	}
	if result := compacted[len(compacted)-1].ToolResult; result == nil || !strings.Contains(result.Content, "[tool result truncated") {
		t.Fatalf("mandatory result was not truncated: %#v", result)
	}
}

func TestProviderStreamSinkDoesNotReportMissingUsageAsZero(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	if err := service.providerStreamSink("session", "run").Emit(context.Background(), stream.Frame{Kind: stream.FrameDone}); err != nil {
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

func TestModelContextTokenTargetRequiresCatalogMetadataAndAvoidsOverflow(t *testing.T) {
	if _, err := modelContextTokenTarget("missing", 0); err == nil {
		t.Fatal("missing context window was accepted")
	}
	maxInt := int(^uint(0) >> 1)
	target, err := modelContextTokenTarget("large", maxInt)
	if err != nil {
		t.Fatal(err)
	}
	if target <= 0 || target > maxInt {
		t.Fatalf("large context target = %d", target)
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
	requested, err := service.NextEvent(ctx)
	if err != nil || requested.Kind != EventApprovalRequested || requested.ToolCallID != "write-1" {
		t.Fatalf("initial prompt = event:%+v err:%v", requested, err)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionSetApprovalMode, Target: string(ApprovalModeYolo)}); err != nil {
		t.Fatal(err)
	}
	if result := <-first; result.err != nil || result.mode != agentservice.ApprovalOnce {
		t.Fatalf("drained approval = mode:%q err:%v", result.mode, result.err)
	}
	resolved, err := service.NextEvent(ctx)
	if err != nil || resolved.Kind != EventApprovalResolved || resolved.ApprovalID != requested.ApprovalID {
		t.Fatalf("drained event = event:%+v err:%v", resolved, err)
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
	requested, err = service.NextEvent(ctx)
	if err != nil || requested.Kind != EventApprovalRequested || requested.ToolCallID != "write-3" {
		t.Fatalf("restored prompt = event:%+v err:%v", requested, err)
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

type skillRuntimeHarness struct {
	service        *Service
	calls          *atomic.Int32
	workspace      string
	catalog        *skills.Catalog
	definitionPath string
}

func newSkillRuntimeHarness(t *testing.T, definition string, resources map[string]string, respond func(int, string, http.ResponseWriter)) skillRuntimeHarness {
	t.Helper()
	ctx := context.Background()
	workspace := t.TempDir()
	skillRoot := filepath.Join(t.TempDir(), "skills")
	skillDir := filepath.Join(skillRoot, "demo")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range resources {
		path := filepath.Join(skillDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var responseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-skill","title":"GPT Skill","context_window":128000,"supported_reasoning_levels":["minimal"],"default_reasoning_level":"minimal","supports_tools":true}]}`))
		case "/responses":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read provider request: %v", err)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			respond(int(responseCalls.Add(1)), string(body), writer)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))

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
	skillCatalog, err := skills.Load(skills.LoadOptions{Config: config.SkillsConfig{
		Enabled:        true,
		AdditionalDirs: []string{skillRoot},
	}})
	if err != nil {
		t.Fatal(err)
	}
	coding, err := agentservice.NewService(store, workspace, agentservice.WithSkills(skillCatalog))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	providerRuntime, err := NewProviderRuntime(cfg, authentication, modelCatalog, coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	providerRuntime.ChatGPTEndpoint = server.URL + "/responses"
	service := NewService(ctx, cfg)
	service.AttachDurable(session.NewService(store.DB()), coding)
	service.AttachAuth(authentication, modelCatalog)
	service.AttachProviderRuntime(providerRuntime)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		server.Close()
		if err := store.Close(shutdownCtx); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return skillRuntimeHarness{
		service: service, calls: &responseCalls, workspace: workspace,
		catalog: skillCatalog, definitionPath: filepath.Join(skillDir, "SKILL.md"),
	}
}

func writeProviderToolCall(writer http.ResponseWriter, responseID, callID, name, arguments string) {
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":%q,\"call_id\":%q,\"name\":%q,\"arguments\":%q}}\n\n", responseID+"-item", callID, name, arguments)
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n\n", responseID)
}

func writeProviderText(writer http.ResponseWriter, responseID, text string) {
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", text)
	_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n\n", responseID)
}

func waitForProviderRun(t *testing.T, service *Service, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventRunFinished:
			return
		case EventRunFailed, EventRunCancelled:
			t.Fatalf("run ended as %s: %s", event.Kind, event.Text)
		}
	}
}

func TestProviderRuntimeLazySkillActivation(t *testing.T) {
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			if !strings.Contains(body, "demo catalog") || !strings.Contains(body, "demo") {
				t.Errorf("first request omitted the skill catalog: %s", body)
			}
			if strings.Contains(body, "DEMO_BODY_SECRET") {
				t.Errorf("first request eagerly disclosed the skill body: %s", body)
			}
			writeProviderToolCall(writer, "response-1", "activate-1", "hydaelyn_activate_skill", `{"name":"demo"}`)
		case 2:
			if !strings.Contains(body, "DEMO_BODY_SECRET") {
				t.Errorf("second request omitted the activated skill body: %s", body)
			}
			writeProviderText(writer, "response-2", "activated")
		default:
			t.Errorf("unexpected provider call %d", call)
			writeProviderText(writer, "response-extra", "unexpected")
		}
	})
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "lazy", Prompt: "inspect parser", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if harness.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", harness.calls.Load())
	}
}

func TestProviderRuntimeManualSkillActivation(t *testing.T) {
	const prompt = "inspect parser"
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n", nil, func(call int, body string, writer http.ResponseWriter) {
		if call != 1 {
			t.Errorf("unexpected provider call %d", call)
		}
		if !strings.Contains(body, "DEMO_BODY_SECRET") || !strings.Contains(body, prompt) {
			t.Errorf("manual activation request omitted body or prompt: %s", body)
		}
		writeProviderText(writer, "response-manual", "done")
	})
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "manual", Prompt: prompt, Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single", ActiveSkills: []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if harness.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", harness.calls.Load())
	}
}

func TestProviderRuntimeUsesFixedSkillSnapshot(t *testing.T) {
	var harness skillRuntimeHarness
	harness = newSkillRuntimeHarness(t, "---\nname: demo\ndescription: old catalog\n---\nOLD_BODY_SECRET\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			updated := "---\nname: demo\ndescription: new catalog\n---\nNEW_BODY_SECRET\n"
			if err := os.WriteFile(harness.definitionPath, []byte(updated), 0o600); err != nil {
				t.Errorf("update skill: %v", err)
			}
			if err := harness.catalog.Reload(); err != nil {
				t.Errorf("reload skills: %v", err)
			}
			writeProviderToolCall(writer, "response-old-1", "activate-old", "hydaelyn_activate_skill", `{"name":"demo"}`)
		case 2:
			if !strings.Contains(body, "OLD_BODY_SECRET") || strings.Contains(body, "NEW_BODY_SECRET") {
				t.Errorf("running engine did not retain its original snapshot: %s", body)
			}
			writeProviderText(writer, "response-old-2", "old snapshot")
		case 3:
			if !strings.Contains(body, "new catalog") || strings.Contains(body, "NEW_BODY_SECRET") {
				t.Errorf("new engine did not receive the reloaded catalog lazily: %s", body)
			}
			writeProviderText(writer, "response-new", "new snapshot")
		default:
			t.Errorf("unexpected provider call %d", call)
			writeProviderText(writer, "response-extra", "unexpected")
		}
	})
	firstRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "fixed-old", Prompt: "use demo", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, firstRun)
	secondRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "fixed-new", Prompt: "inspect demo", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, secondRun)
	if harness.calls.Load() != 3 {
		t.Fatalf("provider calls = %d, want 3", harness.calls.Load())
	}
}

func TestProviderRuntimeSkillResourceRequiresActivation(t *testing.T) {
	const fixture = "REFERENCE_FIXTURE"
	harness := newSkillRuntimeHarness(
		t,
		"---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n",
		map[string]string{"reference.txt": fixture},
		func(call int, body string, writer http.ResponseWriter) {
			switch call {
			case 1:
				if strings.Contains(body, "DEMO_BODY_SECRET") {
					t.Errorf("first request eagerly disclosed the skill body: %s", body)
				}
				writeProviderToolCall(writer, "resource-1", "read-before", "hydaelyn_read_skill_resource", `{"skill":"demo","path":"reference.txt"}`)
			case 2:
				if !strings.Contains(body, "DEMO_BODY_SECRET") {
					t.Errorf("manually activated body missing from second request: %s", body)
				}
				writeProviderToolCall(writer, "resource-2", "read-after", "hydaelyn_read_skill_resource", `{"skill":"demo","path":"reference.txt"}`)
			case 3:
				if !strings.Contains(body, fixture) {
					t.Errorf("resource fixture missing from third request: %s", body)
				}
				writeProviderText(writer, "resource-3", "resource read")
			default:
				t.Errorf("unexpected provider call %d", call)
				writeProviderText(writer, "resource-extra", "unexpected")
			}
		},
	)
	blockedRunID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "resource-blocked", Prompt: "read the reference", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		event, nextErr := harness.service.NextEvent(ctx)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if event.RunID != blockedRunID {
			continue
		}
		if event.Kind == EventRunFailed {
			if !strings.Contains(event.Text, `skill "demo" is not active`) {
				t.Fatalf("resource guard error = %q", event.Text)
			}
			break
		}
		if event.Kind == EventRunFinished || event.Kind == EventRunCancelled {
			t.Fatalf("unactivated resource run ended as %s", event.Kind)
		}
	}
	activeRunID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "resource-active", Prompt: "read the reference", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single", ActiveSkills: []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, activeRunID)
	if harness.calls.Load() != 3 {
		t.Fatalf("provider calls = %d, want 3", harness.calls.Load())
	}
}

func TestSkillAllowedToolsDoNotBypassApproval(t *testing.T) {
	harness := newSkillRuntimeHarness(
		t,
		"---\nname: demo\ndescription: demo catalog\nallowed-tools: coding.shell\n---\nUse the shell when requested.\n",
		nil,
		func(call int, body string, writer http.ResponseWriter) {
			switch call {
			case 1:
				writeProviderToolCall(writer, "approval-1", "shell-approval", "coding.shell", `{"command":"printf skill-approval | tee approval-marker.txt"}`)
			case 2:
				if !strings.Contains(body, "skill-approval") {
					t.Errorf("approved shell output missing from second request: %s", body)
				}
				writeProviderText(writer, "approval-2", "approved")
			default:
				t.Errorf("unexpected provider call %d", call)
				writeProviderText(writer, "approval-extra", "unexpected")
			}
		},
	)
	markerPath := filepath.Join(harness.workspace, "approval-marker.txt")
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "allowed-tools", Prompt: "run the approved command", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single", ActiveSkills: []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approved := false
	for {
		event, nextErr := harness.service.NextEvent(ctx)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventApprovalRequested:
			if event.ToolCallID != "shell-approval" || event.Data["tool"] != "coding.shell" {
				t.Fatalf("approval event = %+v", event)
			}
			if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
				t.Fatalf("shell command ran before approval, stat error = %v", err)
			}
			if err := harness.service.ExecuteAction(context.Background(), Action{
				Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once",
			}); err != nil {
				t.Fatal(err)
			}
			approved = true
		case EventRunFailed:
			t.Fatalf("run failed: %s", event.Text)
		case EventRunCancelled:
			t.Fatal("run cancelled")
		case EventRunFinished:
			if !approved {
				t.Fatal("skill allowed-tools bypassed the approval event")
			}
			content, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != "skill-approval" {
				t.Fatalf("approved marker = %q", content)
			}
			if harness.calls.Load() != 2 {
				t.Fatalf("provider calls = %d, want 2", harness.calls.Load())
			}
			return
		}
	}
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
	coding         *agentservice.Service
	authentication *auth.Service
	run            *agentservice.Run
	driver         countedApprovalDriver
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
		entry, _ := input[0].(map[string]any)
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
		writeAutomaticReview(writer, `{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"authorized bounded write"}`, true)
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

func TestAutoReviewDenyAndMalformedFailureStayDistinctAndDoNotExecute(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantState   string
		wantDecider string
		wantText    string
		errorKind   string
	}{
		{
			name:      "explicit deny",
			output:    `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"target is not authorized"}`,
			wantState: "auto_denied", wantDecider: codex.ApprovalReviewerModel,
			wantText: "Denied by automatic review", errorKind: "",
		},
		{
			name: "malformed output", output: `{`,
			wantState: "auto_failed", wantDecider: "system:auto-review-failure",
			wantText: "Automatic review failed", errorKind: "parse",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
				writeAutomaticReview(writer, test.output, true)
			})
			call := tool.Call{ID: "denied-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
			pending := prepareAutomaticApproval(t, harness, call)
			resolution, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
			if err != nil || resolution.Mode != agentservice.ApprovalDenied {
				t.Fatalf("automatic rejection=%+v error=%v", resolution, err)
			}
			if !strings.Contains(resolution.DenialMessage, test.wantText) || strings.Contains(resolution.DenialMessage, "Denied by user") {
				t.Fatalf("denial message=%q", resolution.DenialMessage)
			}
			if test.wantState == "auto_denied" && !strings.Contains(resolution.DenialMessage, "materially safer alternative") {
				t.Fatalf("explicit denial omitted anti-circumvention guidance: %q", resolution.DenialMessage)
			}
			_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
			resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
			if resolved.State != test.wantState || resolved.Data["error_kind"] != test.errorKind {
				t.Fatalf("resolved event=%+v", resolved)
			}
			if test.wantState == "auto_failed" && (resolved.Data["risk"] != "high" ||
				!strings.Contains(resolved.Data["rationale"], "Automatic approval review failed (parse)")) {
				t.Fatalf("fail-closed review omitted diagnostic assessment: %+v", resolved)
			}
			if harness.driver.executions.Load() != 0 {
				t.Fatalf("denied tool executed %d times", harness.driver.executions.Load())
			}
			if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != test.wantDecider {
				t.Fatalf("durable decider=%q", decider)
			}
		})
	}
}

func TestAutoReviewTimeoutFailsClosedWithoutBecomingModelDeny(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	})
	call := tool.Call{ID: "timeout-1", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
	pending := prepareAutomaticApproval(t, harness, call)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resolution, err := harness.host.awaitApproval(ctx, "session", "agent-1", "main", harness.run, call, pending)
	if err != nil || resolution.Mode != agentservice.ApprovalDenied ||
		!strings.Contains(resolution.DenialMessage, "timed out") || strings.Contains(resolution.DenialMessage, "Denied by automatic review") {
		t.Fatalf("timeout resolution=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	resolved := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if resolved.State != "auto_timed_out" || resolved.Data["error_kind"] != "timeout" {
		t.Fatalf("timeout event=%+v", resolved)
	}
	if harness.driver.executions.Load() != 0 {
		t.Fatal("timed-out review executed tool")
	}
	if decider := durableApprovalDecider(t, harness.coding, harness.run.RunID); decider != "system:auto-review-failure" {
		t.Fatalf("timeout decider=%q", decider)
	}
}

func TestAutoReviewModeRequiresChatGPTAndDoesNotTakePendingHumanApproval(t *testing.T) {
	unauthed := NewService(context.Background(), config.Default())
	if err := unauthed.setApprovalMode(context.Background(), ApprovalModeAutoReview); err == nil {
		t.Fatal("automatic mode accepted without authentication")
	}
	if unauthed.approvalMode != ApprovalModePrompt {
		t.Fatalf("unauthorized mode=%q", unauthed.approvalMode)
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
	if modeEvent.State != "prompt" || modeEvent.Data["auto_review_available"] != "false" ||
		harness.host.approvalMode != ApprovalModePrompt {
		t.Fatalf("logout mode projection=%+v service_mode=%q", modeEvent, harness.host.approvalMode)
	}
	if err := harness.host.ExecuteAction(context.Background(), Action{
		Kind: ActionSetApprovalMode, Target: string(ApprovalModeAutoReview),
	}); err == nil {
		t.Fatal("direct automatic mode action succeeded after logout")
	}
	harness.host.mu.Lock()
	harness.host.approvalMode = ApprovalModeAutoReview
	harness.host.mu.Unlock()
	call := tool.Call{ID: "auth-race", Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
	reviewPending := prepareAutomaticApproval(t, harness, call)
	resolution, err := harness.host.awaitApproval(
		context.Background(), "session", "agent-1", "main", harness.run, call, reviewPending,
	)
	if err != nil || resolution.Mode != agentservice.ApprovalDenied ||
		!strings.Contains(resolution.DenialMessage, "(authentication)") {
		t.Fatalf("post-logout review=%+v error=%v", resolution, err)
	}
	_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
	failed := nextApprovalEvent(t, harness.host, EventApprovalResolved)
	if failed.State != "auto_failed" || failed.Data["error_kind"] != "authentication" || reviews.Load() != 0 {
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

func TestAutoReviewThirdExplicitDenyReturnsTypedRunError(t *testing.T) {
	harness := newAutoReviewHarness(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeAutomaticReview(writer, `{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"not authorized"}`, true)
	})
	for attempt := 1; attempt <= 3; attempt++ {
		call := tool.Call{ID: fmt.Sprintf("deny-%d", attempt), Name: "test.auto_write", Arguments: json.RawMessage(`{"path":"blocked.txt"}`)}
		pending := prepareAutomaticApproval(t, harness, call)
		_, err := harness.host.awaitApproval(context.Background(), "session", "agent-1", "main", harness.run, call, pending)
		_ = nextApprovalEvent(t, harness.host, EventApprovalRequested)
		_ = nextApprovalEvent(t, harness.host, EventApprovalResolved)
		if attempt < 3 && err != nil {
			t.Fatalf("early denial termination at %d: %v", attempt, err)
		}
		if attempt == 3 {
			var limit *AutoReviewDenialLimitError
			if !errors.As(err, &limit) || limit.RunID != harness.run.RunID {
				t.Fatalf("third denial error=%v", err)
			}
		}
	}
	if harness.driver.executions.Load() != 0 {
		t.Fatal("denial threshold executed tool")
	}
}

func newAutoReviewHarness(t *testing.T, handler http.HandlerFunc) autoReviewHarness {
	t.Helper()
	server := httptest.NewServer(handler)
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
	runtime, err := NewProviderRuntime(cfg, authentication, catalog.NewService(store.DB(), authentication), coding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime.ChatGPTEndpoint = server.URL
	host := NewService(ctx, cfg)
	host.AttachDurable(nil, coding)
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
		host: host, coding: coding, authentication: authentication, run: run,
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
