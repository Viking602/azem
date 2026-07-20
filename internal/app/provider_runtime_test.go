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

	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/multiagent"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/stream"
	"github.com/Viking602/go-hydaelyn/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type compactionTestDriver struct {
	requests []hyprovider.Request
	streams  [][]hyprovider.Event
}

func (d *compactionTestDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "test"}
}

func (d *compactionTestDriver) Stream(_ context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	d.requests = append(d.requests, request)
	events := d.streams[0]
	d.streams = d.streams[1:]
	return hyprovider.NewSliceStream(events), nil
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

func TestActiveGuidanceIsFIFOAndInjectedAtModelBoundaries(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.mu.Lock()
	service.activeRun = "run-guided"
	service.activeSession = "session-guided"
	service.guidanceOpen = true
	service.mu.Unlock()

	for _, text := range []string{"first correction", "second correction"} {
		if err := service.GuideActiveTurn("session-guided", "run-guided", text); err != nil {
			t.Fatal(err)
		}
	}
	inner := turnContext{instructions: "rules"}
	manager := activeGuidanceContext{
		inner: inner,
		peek:  func() activeGuidanceSnapshot { return service.peekActiveGuidance("session-guided", "run-guided") },
		acknowledge: func(snapshot activeGuidanceSnapshot) {
			service.acknowledgeActiveGuidance("session-guided", "run-guided", snapshot)
		},
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("old context ", 500)),
		message.NewText(message.RoleAssistant, "old answer"),
	}
	prepared, err := manager.CompactTo(context.Background(), history, 100)
	if err != nil {
		t.Fatal(err)
	}
	latest := prepared[len(prepared)-1]
	firstIndex, secondIndex := strings.Index(latest.Text, "first correction"), strings.Index(latest.Text, "second correction")
	if latest.Role != message.RoleUser || firstIndex < 0 || secondIndex <= firstIndex {
		t.Fatalf("prepared guidance context = %#v", prepared)
	}
	if remaining := service.drainActiveGuidance("session-guided", "run-guided"); len(remaining) != 0 {
		t.Fatalf("guidance was not drained exactly once: %#v", remaining)
	}
	if err := service.GuideActiveTurn("session-guided", "run-guided", "terminal correction"); err != nil {
		t.Fatal(err)
	}
	if pending := service.finishActiveGuidance("session-guided", "run-guided"); len(pending) != 1 || pending[0] != "terminal correction" {
		t.Fatalf("terminal guidance = %#v", pending)
	}
	if err := service.GuideActiveTurn("session-guided", "run-guided", "accepted after retry"); err != nil {
		t.Fatalf("guidance closed while terminal retry was required: %v", err)
	}
	if pending := service.finishActiveGuidance("session-guided", "run-guided"); len(pending) != 1 || pending[0] != "accepted after retry" {
		t.Fatalf("guidance after terminal retry = %#v", pending)
	}
	if pending := service.finishActiveGuidance("session-guided", "run-guided"); len(pending) != 0 {
		t.Fatalf("terminal close unexpectedly drained guidance: %#v", pending)
	}
	if err := service.GuideActiveTurn("session-guided", "run-guided", "too late"); err == nil {
		t.Fatal("finishing run accepted late guidance")
	}
	if err := service.GuideActiveTurn("session-guided", "stale-run", "wrong run"); err == nil {
		t.Fatal("stale run accepted guidance")
	}
}

func TestTurnContextInjectsHistoricalEvidenceAsPrivateSystemContext(t *testing.T) {
	contextManager := turnContext{
		instructions:      "system rules",
		privateContext:    "trusted hook context",
		historicalContext: `{"memories":[{"Content":"use sqlite"}]}`,
		history:           []session.Block{{Kind: "assistant", Content: "prior answer"}},
	}
	messages, err := contextManager.Build(context.Background(), api.Task{Goal: "current request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 6 || messages[2].Role != message.RoleSystem || messages[2].Visibility != message.VisibilityPrivate ||
		!strings.Contains(messages[2].Text, "untrusted JSON data") || messages[4].Role != message.RoleUser ||
		messages[4].Visibility != message.VisibilityPrivate || !strings.Contains(messages[4].Text, `"Content":"use sqlite"`) ||
		messages[5].Text != "current request" {
		t.Fatalf("historical context ordering/visibility = %+v", messages)
	}
}

func TestTeamHistoricalEvidenceIsPlannerOnlyAndNotSystemData(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	hooks := service.teamHooks("session-1", "run-1", "", `{"memories":[{"Content":"planner evidence"}]}`)
	for _, className := range []string{agentservice.PlannerClass, agentservice.ImplementerClass} {
		engine := hyagent.Engine{ContextBuilder: turnContext{instructions: "system rules"}}
		prepared, err := hooks.PrepareEngine(context.Background(), engine, multiagent.Dispatch{Task: api.Task{RunID: "run-1"}}, multiagent.AgentClass{Name: className})
		if err != nil {
			t.Fatal(err)
		}
		messages, err := prepared.ContextBuilder.Build(context.Background(), api.Task{Goal: "current task"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, current := range messages {
			if strings.Contains(current.Text, "planner evidence") {
				found = true
				if current.Role == message.RoleSystem || current.Visibility != message.VisibilityPrivate {
					t.Fatalf("%s historical data authority/visibility = %+v", className, current)
				}
			}
		}
		if found != (className == agentservice.PlannerClass) {
			t.Fatalf("%s historical evidence found=%v", className, found)
		}
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

func TestTurnContextCompactToGeneratesRecursiveSummary(t *testing.T) {
	old := message.NewText(message.RoleSystem, "Objective: preserve the old decision")
	old.Kind = message.KindCompactionSummary
	old.Visibility = message.VisibilityPrivate
	history := []message.Message{
		message.NewText(message.RoleSystem, "system rules"), old,
		message.NewText(message.RoleUser, "older request"),
		message.NewText(message.RoleAssistant, strings.Repeat("older work ", 300)),
		message.NewText(message.RoleUser, "newest request"),
	}
	var input string
	calls := 0
	manager := turnContext{summarize: func(_ context.Context, transcript string) (string, error) {
		calls++
		input = transcript
		return "Objective: newest request\nImportant Details: old decision retained\nWork State (Completed / Active / Blocked): Active\nNext Move: continue\nRelevant Files: provider_context.go", nil
	}}
	compacted, err := manager.CompactTo(context.Background(), history, 300)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(input, "old decision") || !strings.Contains(input, "older request") {
		t.Fatalf("recursive summary input omitted history: %q", input)
	}
	summaries := 0
	for _, current := range compacted {
		if current.Kind == message.KindCompactionSummary {
			summaries++
			if current.Role != message.RoleAssistant || current.Visibility != message.VisibilityPrivate || !strings.Contains(current.Text, "Untrusted historical record") || !strings.Contains(current.Text, "newest request") {
				t.Fatalf("generated summary = %#v", current)
			}
		}
	}
	if calls != 1 || summaries != 1 || compacted[len(compacted)-1].Text != "newest request" {
		t.Fatalf("compacted history = %#v", compacted)
	}
}

func TestTurnContextToolPruningRetainsPreviousSummaryWithoutRegeneration(t *testing.T) {
	previous := message.NewText(message.RoleAssistant, compactionSummaryLabel+"## Objective\n- keep state")
	previous.Kind = message.KindCompactionSummary
	previous.Visibility = message.VisibilityPrivate
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"), previous,
		message.NewText(message.RoleUser, "latest request"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "read", Name: "read"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "read", Name: "read", Content: strings.Repeat("data", 2_000)}),
	}
	calls := 0
	manager := turnContext{summarize: func(context.Context, string) (string, error) {
		calls++
		return "unexpected", nil
	}}
	got, err := manager.CompactTo(context.Background(), history, 500)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || message.ValidateCompleteTurns(got) != nil {
		t.Fatalf("tool-only pruning calls=%d history=%#v", calls, got)
	}
	summaries := 0
	for _, current := range got {
		if current.Kind == message.KindCompactionSummary {
			summaries++
		}
	}
	if summaries != 1 || got[len(got)-1].ToolResult == nil || !strings.Contains(got[len(got)-1].ToolResult.Content, "truncated") {
		t.Fatalf("tool-only pruning result = %#v", got)
	}
}

func TestManualCompactionTailRetainsLatestUserBeforeAgentBlocks(t *testing.T) {
	blocks := []session.Block{
		{Kind: "user", Content: "old"}, {Kind: "assistant", Content: "old answer"},
		{Kind: "user", Content: "latest guidance"},
		{Kind: "agent", Content: "one"}, {Kind: "agent", Content: "two"}, {Kind: "agent", Content: "three"},
		{Kind: "agent", Content: "four"}, {Kind: "agent", Content: "five"},
	}
	if start := manualCompactionTailStart(blocks, 4); start != 2 {
		t.Fatalf("tail start = %d, want latest user at 2", start)
	}
}

func TestCompactionUsageIsChargedToNextMainProviderTurn(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}}},
		{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}},
	}}
	driver := &compactionUsageDriver{inner: inner}
	compactStream, err := driver.Stream(context.Background(), hyprovider.Request{Metadata: map[string]string{compactionRequestMetadataKey: "true"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compactStream.Recv(); err != nil {
		t.Fatal(err)
	}
	mainStream, err := driver.Stream(context.Background(), hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	done, err := mainStream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if done.Usage.InputTokens != 22 || done.Usage.OutputTokens != 8 || done.Usage.TotalTokens != 30 {
		t.Fatalf("combined usage = %#v", done.Usage)
	}
}

func TestLazyCompactionRouteUsesIndependentDriverAndSharedUsage(t *testing.T) {
	active := &compactionTestDriver{streams: [][]hyprovider.Event{{{Kind: hyprovider.EventDone, Usage: hyprovider.Usage{TotalTokens: 20}}}}}
	compact := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "summary"},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete, Usage: hyprovider.Usage{TotalTokens: 10}},
	}}}
	resolveCalls := 0
	meter := &compactionUsageMeter{}
	summarize := lazyCompactionSummarizer(func(_ context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
		resolveCalls++
		if provider != "grok" || model != "summary-model" || reasoning != "high" {
			t.Fatalf("resolved route = %s/%s/%s", provider, model, reasoning)
		}
		return model, 8_000, compact, nil
	}, config.ModelRouteConfig{Provider: "grok", Model: "summary-model", Reasoning: "high"}, "chatgpt", "main-model", "low", meter)
	if resolveCalls != 0 {
		t.Fatal("compaction driver resolved eagerly")
	}
	if _, err := summarize(context.Background(), "history"); err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 1 || len(compact.requests) != 1 || compact.requests[0].Model != "summary-model" {
		t.Fatalf("resolve calls=%d requests=%#v", resolveCalls, compact.requests)
	}
	stream, err := (&compactionUsageDriver{inner: active, meter: meter}).Stream(context.Background(), hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	done, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if done.Usage.TotalTokens != 30 {
		t.Fatalf("shared usage = %#v", done.Usage)
	}
}

func TestCompactionSummarizerBoundsOversizedInput(t *testing.T) {
	inner := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "## Objective\n- continue"},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
	}}}
	transcript := "header\n<transcript>\n" + strings.Repeat("ROLE assistant\nTEXT old data\n", 500) + "ROLE user\nTEXT newest evidence\n</transcript>"
	summary, err := compactionSummarizer(inner, "grok", "model", 1_000, 200)(context.Background(), transcript)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "Objective") || len(inner.requests) != 1 {
		t.Fatalf("summary=%q requests=%d", summary, len(inner.requests))
	}
	requestText := inner.requests[0].Messages[1].Text
	if inner.requests[0].ExtraBody["max_output_tokens"] != 200 || len(requestText) > contextTokenBytes(1_000-200-256) || !strings.Contains(requestText, "newest evidence") || !strings.Contains(requestText, "historical evidence truncated") {
		t.Fatalf("bounded summary request (%d bytes) = %q", len(requestText), requestText)
	}
	chatGPT := &compactionTestDriver{streams: [][]hyprovider.Event{{
		{Kind: hyprovider.EventTextDelta, Text: "summary"},
		{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete},
	}}}
	if _, err := compactionSummarizer(chatGPT, "chatgpt", "model", 1_000, 200)(context.Background(), "history"); err != nil {
		t.Fatal(err)
	}
	if len(chatGPT.requests[0].ExtraBody) != 0 {
		t.Fatalf("ChatGPT summary sent unsupported extra body: %#v", chatGPT.requests[0].ExtraBody)
	}
}

func TestTurnContextCompactToSummaryFailureFallsBackToDeterministicCompaction(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, "old"),
		message.NewText(message.RoleAssistant, strings.Repeat("work ", 300)),
		message.NewText(message.RoleUser, "latest"),
	}
	manager := turnContext{summarize: func(context.Context, string) (string, error) { return "partial", errors.New("offline") }}
	got, err := manager.CompactTo(context.Background(), history, 100)
	if err != nil || reflect.DeepEqual(got, history) || got[len(got)-1].Text != "latest" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if got[1].Kind != message.KindCompactionSummary || !strings.Contains(got[1].Text, "earlier messages omitted") {
		t.Fatalf("fallback summary = %#v", got[1])
	}
}

func TestActiveGuidanceSurvivesGeneratedCompaction(t *testing.T) {
	snapshot := activeGuidanceSnapshot{values: []string{"first correction", "second correction"}}
	acknowledged := false
	manager := activeGuidanceContext{
		inner: turnContext{summarize: func(context.Context, string) (string, error) { return "Objective: retain guidance", nil }},
		peek:  func() activeGuidanceSnapshot { return snapshot },
		acknowledge: func(got activeGuidanceSnapshot) {
			acknowledged = reflect.DeepEqual(got, snapshot)
		},
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("old ", 400)),
		message.NewText(message.RoleAssistant, "done"),
	}
	got, err := manager.CompactTo(context.Background(), history, 100)
	if err != nil {
		t.Fatal(err)
	}
	latest := got[len(got)-1].Text
	if !strings.Contains(latest, "first correction") || !strings.Contains(latest, "second correction") {
		t.Fatalf("trailing guidance lost: %#v", got)
	}
	if !acknowledged {
		t.Fatal("successful compaction did not acknowledge guidance")
	}
}

func TestActiveGuidanceRemainsQueuedWhenCompactionFails(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.mu.Lock()
	service.activeRun = "run-guided"
	service.activeSession = "session-guided"
	service.guidanceOpen = true
	service.mu.Unlock()
	if err := service.GuideActiveTurn("session-guided", "run-guided", "do not lose this"); err != nil {
		t.Fatal(err)
	}
	manager := activeGuidanceContext{
		inner: turnContext{},
		peek:  func() activeGuidanceSnapshot { return service.peekActiveGuidance("session-guided", "run-guided") },
		acknowledge: func(snapshot activeGuidanceSnapshot) {
			service.acknowledgeActiveGuidance("session-guided", "run-guided", snapshot)
		},
	}
	history := []message.Message{
		message.NewText(message.RoleSystem, "rules"),
		message.NewText(message.RoleUser, strings.Repeat("mandatory context ", 100)),
	}
	if _, err := manager.CompactTo(context.Background(), history, 1); err == nil {
		t.Fatal("expected compaction failure")
	}
	remaining := service.drainActiveGuidance("session-guided", "run-guided")
	if len(remaining) != 1 || remaining[0] != "do not lose this" {
		t.Fatalf("guidance after failed compaction = %#v", remaining)
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

func TestTurnContextBuildReplaysCompatibleHistoryAndAppendsDynamicTail(t *testing.T) {
	saved := []message.Message{
		message.NewText(message.RoleSystem, mainInstructions),
		{
			Role: message.RoleSystem, Text: "saved skill catalog",
			Metadata: map[string]string{"hydaelyn.skill.context": "catalog"},
		},
		message.NewText(message.RoleUser, "old request"),
		{
			Role: message.RoleAssistant, Text: "old answer",
			ProviderState: json.RawMessage(`[{"type":"reasoning","id":"reasoning-1"}]`),
		},
	}
	manager := turnContext{
		instructions: mainInstructions, providerID: "chatgpt", modelID: "gpt-test",
		modelHistory: session.ModelHistory{
			ProviderID: "chatgpt", ModelID: "gpt-test",
			InstructionFingerprint: mainInstructionFingerprint, Messages: saved,
		},
		history: []session.Block{
			{Kind: "user", Content: "old request"},
			{Kind: "assistant", Content: "old answer"},
			{Kind: "user", Content: "current hook user", State: "hook"},
		},
		persistedHistory:  2,
		privateContext:    "current trusted hook",
		historicalContext: `{"memories":["current evidence"]}`,
		todo:              session.TodoList{Goal: "current todo", Revision: 3},
	}
	got, err := manager.Build(context.Background(), api.Task{Goal: "new request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(saved)+6 || !reflect.DeepEqual(got[:len(saved)], saved) {
		t.Fatalf("saved prefix changed:\n got=%#v\nwant=%#v", got, saved)
	}
	tail := got[len(saved):]
	if tail[0].Role != message.RoleSystem || tail[0].Visibility != message.VisibilityPrivate ||
		!strings.Contains(tail[0].Text, "current trusted hook") {
		t.Fatalf("private hook tail = %#v", tail[0])
	}
	if tail[1].Role != message.RoleSystem || tail[1].Visibility != message.VisibilityPrivate ||
		!strings.HasPrefix(tail[1].Text, todoReminderPrefix) {
		t.Fatalf("todo tail = %#v", tail[1])
	}
	if tail[2].Text != historicalEvidencePolicy || tail[2].Visibility != message.VisibilityPrivate {
		t.Fatalf("historical policy tail = %#v", tail[2])
	}
	if tail[3].Role != message.RoleUser || tail[3].Text != "current hook user" {
		t.Fatalf("hook user tail = %#v", tail[3])
	}
	if tail[4].Role != message.RoleUser || tail[4].Visibility != message.VisibilityPrivate ||
		!strings.Contains(tail[4].Text, "current evidence") {
		t.Fatalf("historical data tail = %#v", tail[4])
	}
	if tail[5].Role != message.RoleUser || tail[5].Text != "new request" {
		t.Fatalf("goal tail = %#v", tail[5])
	}
}

func TestTurnContextBuildFallsBackWhenModelHistoryScopeDiffers(t *testing.T) {
	manager := turnContext{
		instructions: mainInstructions, providerID: "chatgpt", modelID: "gpt-new",
		modelHistory: session.ModelHistory{
			ProviderID: "chatgpt", ModelID: "gpt-old",
			InstructionFingerprint: mainInstructionFingerprint,
			Messages: []message.Message{{
				Role: message.RoleAssistant, Text: "stale answer",
				ProviderState: json.RawMessage(`[{"type":"reasoning","id":"stale"}]`),
			}},
		},
		history: []session.Block{
			{Kind: "user", Content: "visible request"},
			{Kind: "assistant", Content: "visible answer"},
		},
		persistedHistory: 2,
	}
	got, err := manager.Build(context.Background(), api.Task{Goal: "switched request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].Role != message.RoleSystem || got[0].Text != mainInstructions ||
		got[1].Role != message.RoleUser || got[1].Text != "visible request" ||
		got[2].Role != message.RoleAssistant || got[2].Text != "visible answer" ||
		got[3].Role != message.RoleUser || got[3].Text != "switched request" {
		t.Fatalf("fallback messages = %#v", got)
	}
	for _, current := range got {
		if len(current.ProviderState) != 0 || current.Text == "stale answer" {
			t.Fatalf("stale exact state leaked into fallback: %#v", current)
		}
	}
}

func TestTeamPrepareEnginePartitionsPromptCacheKeysAndPreservesOptions(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	hooks := service.teamHooks("session-1", "team-parent", "", "")
	base := hyagent.Engine{ExtraBody: map[string]any{"parallel_tool_calls": false}}
	prepare := func(runID string) hyagent.Engine {
		t.Helper()
		prepared, err := hooks.PrepareEngine(context.Background(), base, multiagent.Dispatch{
			Task: api.Task{RunID: runID}, To: "implementer",
		}, multiagent.AgentClass{Name: agentservice.ImplementerClass})
		if err != nil {
			t.Fatal(err)
		}
		if prepared.ExtraBody["parallel_tool_calls"] != false {
			t.Fatalf("existing provider option lost: %#v", prepared.ExtraBody)
		}
		return prepared
	}
	first := prepare("child-run-1")
	repeated := prepare("child-run-1")
	second := prepare("child-run-2")
	if first.ExtraBody["prompt_cache_key"] != "child-run-1" ||
		repeated.ExtraBody["prompt_cache_key"] != "child-run-1" ||
		second.ExtraBody["prompt_cache_key"] != "child-run-2" {
		t.Fatalf("team cache keys first=%#v repeated=%#v second=%#v", first.ExtraBody, repeated.ExtraBody, second.ExtraBody)
	}
	if _, mutated := base.ExtraBody["prompt_cache_key"]; mutated {
		t.Fatalf("base engine ExtraBody mutated: %#v", base.ExtraBody)
	}
}

func TestMainTurnsKeepSerializedPrefixStableAndAppendRawOutputAndNewTail(t *testing.T) {
	const firstOutput = `[{"type":"reasoning","id":"rs_first","encrypted_content":"opaque"},{"type":"message","id":"msg_first","role":"assistant","content":[{"type":"output_text","text":"first answer"}]}]`
	const secondOutput = `[{"type":"message","id":"msg_second","role":"assistant","content":[{"type":"output_text","text":"second answer"}]}]`
	type capturedRequest struct {
		PromptCacheKey string            `json:"prompt_cache_key"`
		Instructions   string            `json:"instructions"`
		Input          []json.RawMessage `json:"input"`
	}
	var captured []capturedRequest
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		var request capturedRequest
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Errorf("decode captured request %d: %v", call, err)
		}
		captured = append(captured, request)
		switch call {
		case 1:
			_, _ = fmt.Fprint(writer, `data: {"type":"response.output_text.delta","delta":"first answer"}`+"\n\n")
			_, _ = fmt.Fprint(writer, `data: {"type":"response.completed","response":{"status":"completed","output":`+firstOutput+`}}`+"\n\n")
		case 2:
			_, _ = fmt.Fprint(writer, `data: {"type":"response.output_text.delta","delta":"second answer"}`+"\n\n")
			_, _ = fmt.Fprint(writer, `data: {"type":"response.completed","response":{"status":"completed","output":`+secondOutput+`}}`+"\n\n")
		default:
			t.Errorf("unexpected provider request %d", call)
		}
	})
	ctx := context.Background()
	sessions := harness.service.sessions
	if _, err := sessions.Ensure(ctx, session.Session{
		ID: "cache-session", Title: "Cache", ProviderID: "chatgpt", ModelID: "gpt-skill", Reasoning: "minimal", AgentMode: "single",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.UpdateTodo(ctx, "cache-session", 0, func(todo *session.TodoList) error {
		todo.Goal = "first todo"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	firstRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "cache-session", Prompt: "first request", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, firstRun)
	if _, err := sessions.UpdateTodo(ctx, "cache-session", 1, func(todo *session.TodoList) error {
		todo.Goal = "second todo"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	secondRun, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "cache-session", Prompt: "second request", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, secondRun)

	if len(captured) != 2 || captured[0].PromptCacheKey != "cache-session" ||
		captured[1].PromptCacheKey != "cache-session" || captured[0].Instructions != captured[1].Instructions {
		t.Fatalf("captured cache requests = %#v", captured)
	}
	var rawOutput []json.RawMessage
	if err := json.Unmarshal([]byte(firstOutput), &rawOutput); err != nil {
		t.Fatal(err)
	}
	wantPrefix := append(append([]json.RawMessage(nil), captured[0].Input...), rawOutput...)
	if len(captured[1].Input) <= len(wantPrefix) || !reflect.DeepEqual(captured[1].Input[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("second input did not preserve first request plus raw output:\nfirst=%s\nsecond=%s", captured[0].Input, captured[1].Input)
	}
	tailJSON, err := json.Marshal(captured[1].Input[len(wantPrefix):])
	if err != nil {
		t.Fatal(err)
	}
	tail := string(tailJSON)
	if !strings.Contains(tail, "second todo") || !strings.Contains(tail, "second request") ||
		strings.Contains(tail, "first todo") || strings.Contains(tail, "first request") {
		t.Fatalf("second dynamic tail = %s", tail)
	}
	projection, err := sessions.LoadProjection(ctx, "cache-session")
	if err != nil {
		t.Fatal(err)
	}
	if projection.ModelHistory.ProviderID != "chatgpt" || projection.ModelHistory.ModelID != "gpt-skill" ||
		projection.ModelHistory.InstructionFingerprint != mainInstructionFingerprint ||
		len(projection.ModelHistory.Messages) == 0 ||
		string(projection.ModelHistory.Messages[len(projection.ModelHistory.Messages)-1].ProviderState) != secondOutput {
		t.Fatalf("persisted exact history = %#v", projection.ModelHistory)
	}
}

func TestMainTurnReplacesMismatchedSnapshotOnlyAfterSuccessfulFallback(t *testing.T) {
	const freshOutput = `[{"type":"message","id":"fresh_message","role":"assistant","content":[{"type":"output_text","text":"fresh answer"}]}]`
	var capturedBody string
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		if call != 1 {
			t.Errorf("unexpected provider request %d", call)
		}
		capturedBody = body
		_, _ = fmt.Fprint(writer, `data: {"type":"response.output_text.delta","delta":"fresh answer"}`+"\n\n")
		_, _ = fmt.Fprint(writer, `data: {"type":"response.completed","response":{"status":"completed","output":`+freshOutput+`}}`+"\n\n")
	})
	ctx := context.Background()
	sessions := harness.service.sessions
	if _, err := sessions.Ensure(ctx, session.Session{
		ID: "mismatch-session", Title: "Mismatch", ProviderID: "chatgpt", ModelID: "gpt-skill", Reasoning: "minimal", AgentMode: "single",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendBlock(ctx, "mismatch-session", session.Block{Kind: "user", RunID: "old-run", Content: "visible request"}); err != nil {
		t.Fatal(err)
	}
	stale := session.ModelHistory{
		ProviderID: "chatgpt", ModelID: "different-model", InstructionFingerprint: mainInstructionFingerprint,
		Messages: []message.Message{{
			Role: message.RoleAssistant, Text: "stale hidden answer",
			ProviderState: json.RawMessage(`[{"type":"reasoning","id":"stale_reasoning"}]`),
		}},
	}
	if err := sessions.CompleteTurn(ctx, "mismatch-session", session.Block{
		Kind: "assistant", RunID: "old-run", Content: "visible answer",
	}, stale); err != nil {
		t.Fatal(err)
	}
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "mismatch-session", Prompt: "follow up", Provider: "chatgpt", Model: "gpt-skill",
		Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if !strings.Contains(capturedBody, "visible request") || !strings.Contains(capturedBody, "visible answer") ||
		strings.Contains(capturedBody, "stale hidden answer") || strings.Contains(capturedBody, "stale_reasoning") {
		t.Fatalf("mismatch fallback request = %s", capturedBody)
	}
	projection, err := sessions.LoadProjection(ctx, "mismatch-session")
	if err != nil {
		t.Fatal(err)
	}
	got := projection.ModelHistory
	if got.ModelID != "gpt-skill" || got.InstructionFingerprint != mainInstructionFingerprint ||
		len(got.Messages) == 0 || string(got.Messages[len(got.Messages)-1].ProviderState) != freshOutput {
		t.Fatalf("replacement snapshot = %#v", got)
	}
}
