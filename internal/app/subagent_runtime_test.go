package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestNewSubagentCapturesLatestCompactionRoute(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = providerStore.Close(context.Background()) })
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := newSubagentRuntime(ctx, config.Default().Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		runtime.cancel()
		runtime.wg.Wait()
	})
	want := config.ModelRouteConfig{Provider: "grok", Model: "summary-new", Reasoning: "low"}
	parent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "parent", ProviderID: "chatgpt", ModelID: "main",
		WorkspaceRoot: t.TempDir(), CompactionRoute: config.ModelRouteConfig{Provider: "chatgpt", Model: "summary-old"},
		CompactionRouteSnapshot: func() config.ModelRouteConfig { return want },
		ResolveDriver: func(context.Context, string, string, string) (string, int, hyprovider.Driver, error) {
			return "", 0, nil, errors.New("stop before execution")
		},
	}
	run, err := runtime.Spawn(ctx, subagentSpawnInput{SubagentType: "explore", Prompt: "inspect"}, parent)
	if err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	active := runtime.active[run.ID]
	got := config.ModelRouteConfig{}
	if active != nil {
		got = active.parent.CompactionRoute
	}
	runtime.mu.Unlock()
	if active == nil || got != want {
		t.Fatalf("spawned child compaction route = %+v, want %+v", got, want)
	}
}

func TestSubagentRuntimeReceivesSkillCatalog(t *testing.T) {
	var calls atomic.Int32
	var cacheKeys sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" || request.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Errorf("provider auth headers missing")
		}
		switch request.URL.Path {
		case "/models":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-subagent","title":"GPT Subagent","context_window":128000,"supports_tools":true}]}`))
		case "/responses":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read provider request: %v", err)
			}
			call := calls.Add(1)
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode provider request %d: %v", call, err)
			}
			cacheKeys.Store(call, fmt.Sprint(payload["prompt_cache_key"]))
			if call == 2 {
				requestBody := string(body)
				if !strings.Contains(requestBody, "demo catalog") || !strings.Contains(requestBody, "demo") {
					t.Errorf("child request omitted skill catalog: %s", requestBody)
				}
				if strings.Contains(requestBody, "DEMO_BODY_SECRET") {
					t.Errorf("child request eagerly disclosed skill body: %s", requestBody)
				}
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			switch call {
			case 1:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"delegate-1\",\"name\":\"subagent.spawn\",\"arguments\":\"{\\\"prompt\\\":\\\"inspect the workspace\\\",\\\"description\\\":\\\"inspect workspace\\\",\\\"subagent_type\\\":\\\"explore\\\",\\\"background\\\":false}\"}}\n\n")
			case 2:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"child-item-1\",\"call_id\":\"child-read-1\",\"name\":\"coding_read_file\",\"arguments\":\"{\\\"path\\\":\\\"missing.txt\\\"}\"}}\n\n")
			case 3:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Found concrete evidence.\"}\n\n")
			case 4:
				_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Delegation complete.\"}\n\n")
			default:
				t.Errorf("unexpected response call %d", call)
			}
			inputTokens, totalTokens, cachedTokens := 10, 15, 0
			if call == 2 {
				// A child run may legitimately spend more than the old 128K
				// cumulative default and still have room in its model context.
				inputTokens, totalTokens = 149_995, 150_000
			}
			if call == 3 {
				cachedTokens = 6
			}
			_, _ = fmt.Fprintf(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-%d\",\"status\":\"completed\",\"usage\":{\"input_tokens\":%d,\"output_tokens\":5,\"total_tokens\":%d,\"input_tokens_details\":{\"cached_tokens\":%d}}}}\n\n", calls.Load(), inputTokens, totalTokens, cachedTokens)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := context.Background()
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
	workspace := t.TempDir()
	skillRoot := filepath.Join(t.TempDir(), "skills")
	skillDir := filepath.Join(skillRoot, "demo")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: demo catalog\n---\nDEMO_BODY_SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	subagentStore, err := agentservice.NewSQLSubagentRunStore(store.DB())
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
	if _, err := sessions.Ensure(ctx, session.Session{ID: "default", Title: "Test", ProviderID: "chatgpt", ModelID: "gpt-subagent", Reasoning: "minimal", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, cfg)
	service.AttachDurable(sessions, coding)
	service.AttachAuth(authentication, modelCatalog)
	service.AttachAgentExtensions(nil, subagentStore)
	service.AttachProviderRuntime(providerRuntime)

	runID, err := service.StartConfiguredTurn(TurnRequest{SessionID: "default", Prompt: "delegate inspection", Provider: "chatgpt", Model: "gpt-subagent", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]bool{}
	var childID string
	var answer string
	var toolResults []string
	var childStream Event
	var childUsage Event
	typedStates := true
	deadline, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		event, err := service.NextEvent(deadline)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			if event.Kind == EventTextDelta && event.AgentID != "" {
				childStream = event
			}
			continue
		}
		switch event.Kind {
		case EventAgentState:
			childID = event.AgentID
			states[event.State] = true
			if event.Agent == nil || event.Agent.Type != "explore" || event.Agent.ParentToolCallID != "delegate-1" {
				typedStates = false
			}
		case EventToolFinished:
			toolResults = append(toolResults, event.Text)
		case EventContextUsage:
			if event.Data["aggregateOnly"] == "true" {
				childUsage = event
			}
		case EventTextDelta:
			answer += event.Text
		case EventApprovalRequested:
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
	if calls.Load() != 4 || answer != "Delegation complete." || childID == "" || !typedStates ||
		!states["initializing"] || !states["queued"] || !states["running"] || !states["completed"] {
		t.Fatalf("subagent calls=%d answer=%q id=%q typed=%v states=%v tool_results=%v", calls.Load(), answer, childID, typedStates, states, toolResults)
	}
	if childStream.Text != "Found concrete evidence." || childStream.AgentID != childID ||
		childStream.ApprovalID != "" || childStream.Data["source"] != "child:"+childID ||
		childStream.Data["parent_tool_call_id"] != "delegate-1" {
		t.Fatalf("child stream event = %#v, child ID = %q", childStream, childID)
	}
	if childUsage.RunID != runID || childUsage.Data["cachedInputTokens"] != "6" || childUsage.Data["inputTokens"] != "10" {
		t.Fatalf("child cache usage event = %+v", childUsage)
	}
	firstKey, _ := cacheKeys.Load(int32(1))
	firstChildKey, _ := cacheKeys.Load(int32(2))
	secondChildKey, _ := cacheKeys.Load(int32(3))
	finalKey, _ := cacheKeys.Load(int32(4))
	if firstKey != "default" || finalKey != "default" ||
		firstChildKey == nil || firstChildKey == "" || firstChildKey == "default" ||
		firstChildKey != secondChildKey {
		t.Fatalf("prompt cache keys: first=%v child-first=%v child-second=%v final=%v",
			firstKey, firstChildKey, secondChildKey, finalKey)
	}
	runs, err := subagentStore.List(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != childID || runs[0].State != agentservice.SubagentCompleted || runs[0].ParentRunID != runID {
		t.Fatalf("durable subagent runs=%+v", runs)
	}
	if runs[0].TokensUsed <= 128_000 {
		t.Fatalf("completed child token usage = %d, want proof it continued beyond the old 128K limit", runs[0].TokensUsed)
	}
	projection, err := sessions.LoadProjection(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	var lifecycle []session.Block
	for _, block := range projection.Blocks {
		if block.Kind == "agent" {
			lifecycle = append(lifecycle, block)
		}
	}
	if len(lifecycle) != 1 || lifecycle[0].AgentID != childID || lifecycle[0].State != "completed" ||
		lifecycle[0].ParentToolCallID != "delegate-1" {
		t.Fatalf("reloaded lifecycle blocks = %#v", lifecycle)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentQueryUsesActiveSnapshotAndPreservesOrder(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := providerStore.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()

	started := time.Now().UTC().Add(-time.Second)
	completed := agentservice.SubagentRun{
		ID: "stored", SessionID: "session", ParentRunID: "parent", Type: "explore",
		State: agentservice.SubagentCompleted, Summary: "stored", Output: "stored output", StartedAt: started, FinishedAt: started.Add(time.Second),
	}
	if err := store.Create(ctx, completed); err != nil {
		t.Fatal(err)
	}
	activeRun := agentservice.SubagentRun{
		ID: "active", SessionID: "session", ParentRunID: "parent", Type: "verify",
		State: agentservice.SubagentRunning, Summary: "running", StartedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, activeRun); err != nil {
		t.Fatal(err)
	}
	childCtx, childCancel := context.WithCancel(runtime.ctx)
	runtime.active[activeRun.ID] = &activeSubagent{
		run: activeRun, ctx: childCtx, cancel: childCancel, done: make(chan struct{}),
		toolNames: map[string]struct{}{"coding.go_test": {}},
	}

	snapshots := runtime.Query(ctx, "session", []string{"stored", "active", "missing"}, 0)
	if len(snapshots) != 3 || snapshots[0].Run.ID != "stored" || snapshots[1].Run.ID != "active" || snapshots[2].Found {
		t.Fatalf("ordered snapshots = %#v", snapshots)
	}
	if snapshots[1].Run.State != agentservice.SubagentRunning || len(snapshots[1].Run.ToolsUsed) != 1 {
		t.Fatalf("active snapshot = %#v", snapshots[1])
	}
	if leaked := runtime.Query(ctx, "other-session", []string{"active"}, 0); len(leaked) != 1 || leaked[0].Found {
		t.Fatalf("cross-session query leaked %#v", leaked)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		runtime.terminalize("active", terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("fixture failure")})
	}()
	waited := runtime.Query(ctx, "session", []string{"active"}, time.Second)
	if len(waited) != 1 || !waited[0].Found || waited[0].Run.State != agentservice.SubagentFailed || waited[0].Run.Error != "fixture failure" {
		t.Fatalf("waited snapshot = %#v", waited)
	}
}

func TestConcurrentSubagentCancelIsDurableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := providerStore.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()

	run := agentservice.SubagentRun{
		ID: "cancel-me", SessionID: "session", ParentRunID: "parent", Type: "verify",
		State: agentservice.SubagentRunning, Summary: "running", StartedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	var cancelCalls atomic.Int32
	runtime.active[run.ID] = &activeSubagent{
		run: run, cancel: func() { cancelCalls.Add(1) }, done: make(chan struct{}),
		slot: true, toolNames: make(map[string]struct{}),
	}
	runtime.running = 1

	const callers = 8
	outcomes := make(chan agentservice.SubagentCancelOutcome, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			outcomes <- runtime.Cancel("session", run.ID)
		}()
	}
	wait.Wait()
	close(outcomes)
	for outcome := range outcomes {
		if outcome.Outcome != "cancel_requested" || !outcome.Snapshot.Found || outcome.Snapshot.Run.State != agentservice.SubagentCancelling {
			t.Fatalf("cancel outcome = %#v", outcome)
		}
	}
	if cancelCalls.Load() != 1 {
		t.Fatalf("cancel called %d times", cancelCalls.Load())
	}
	persisted, err := store.Get(ctx, run.ID)
	if err != nil || persisted.State != agentservice.SubagentCancelling {
		t.Fatalf("persisted cancelling run = %#v, %v", persisted, err)
	}

	runtime.terminalize(run.ID, terminalRequest{state: agentservice.SubagentCancelled})
	persisted, err = store.Get(ctx, run.ID)
	if err != nil || persisted.State != agentservice.SubagentCancelled {
		t.Fatalf("persisted cancelled run = %#v, %v", persisted, err)
	}
	if outcome := runtime.Cancel("session", run.ID); outcome.Outcome != "already_finished" || outcome.Snapshot.Run.State != agentservice.SubagentCancelled {
		t.Fatalf("terminal cancel outcome = %#v", outcome)
	}
	if outcome := runtime.Cancel("other-session", run.ID); outcome.Outcome != "not_found" {
		t.Fatalf("cross-session cancel outcome = %#v", outcome)
	}
}

func TestDecodeSubagentSpawnInputTracksPresenceAndDefaults(t *testing.T) {
	minimal, err := decodeSubagentSpawnInput(json.RawMessage(`{"prompt":"inspect","description":"short task"}`))
	if err != nil {
		t.Fatal(err)
	}
	if minimal.SubagentType != "general-purpose" || minimal.SubagentTypeSet || !minimal.Background || minimal.BackgroundSet || minimal.Isolation != "none" || minimal.IsolationSet {
		t.Fatalf("minimal input = %#v", minimal)
	}

	explicit, err := decodeSubagentSpawnInput(json.RawMessage(`{
		"prompt":"verify","description":"run checks","subagent_type":"verify","background":false,
		"capability_mode":"execute","isolation":"none","cwd":"./nested","model":"model","todo_item_id":"item-1"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !explicit.SubagentTypeSet || explicit.SubagentType != "verify" || !explicit.BackgroundSet || explicit.Background ||
		!explicit.CapabilityModeSet || explicit.IsolationSet || explicit.Isolation != "none" || !explicit.CWDSet || !explicit.ModelSet || explicit.TodoItemID != "item-1" {
		t.Fatalf("explicit input = %#v", explicit)
	}

	omittedStrings, err := decodeSubagentSpawnInput(json.RawMessage(`{
		"prompt":"inspect","description":"omitted strings","subagent_type":"none","cwd":"undefined","model":null
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if omittedStrings.SubagentType != "general-purpose" || omittedStrings.SubagentTypeSet || omittedStrings.CWDSet || omittedStrings.ModelSet {
		t.Fatalf("omitted strings = %#v", omittedStrings)
	}

	resume, err := decodeSubagentSpawnInput(json.RawMessage(`{
		"prompt":"continue","description":"resume task","resume_from":"source","cwd":"elsewhere",
		"isolation":"worktree","model":"ignored","capability_mode":"all"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if resume.SubagentType != "" || resume.SubagentTypeSet || !resume.CWDSet || !resume.IsolationSet || !resume.ModelSet || !resume.CapabilityModeSet {
		t.Fatalf("resume input = %#v", resume)
	}

	if _, err := decodeSubagentSpawnInput(json.RawMessage(`{"prompt":"x","description":"x","cwd":"nested","isolation":"worktree"}`)); err == nil {
		t.Fatal("fresh cwd/worktree combination was accepted")
	}
}

func TestSubagentTodoBindingPreservesStatusAndEmitsSnapshot(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	sessions := session.NewService(providerStore.DB())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Todo"}); err != nil {
		t.Fatal(err)
	}
	initialized, err := sessions.UpdateTodo(ctx, "session", 0, func(todo *session.TodoList) error {
		todo.Goal = "delegate"
		todo.Phases = []session.TodoPhase{{Title: "Research", Items: []session.TodoItem{{Content: "inspect", Status: session.TodoInProgress}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	host := NewService(ctx, config.Default())
	host.sessions = sessions
	parent := subagentParentRuntime{SessionID: "session", Host: host}
	itemID := initialized.Phases[0].Items[0].ID
	revision, err := prepareSubagentTodoBinding(ctx, parent, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitSubagentTodoBinding(ctx, parent, itemID, "subagent-1", revision); err != nil {
		t.Fatal(err)
	}
	updated, err := sessions.LoadTodo(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	item := updated.Phases[0].Items[0]
	if item.SubagentRunID != "subagent-1" || item.Status != session.TodoInProgress || updated.Revision != initialized.Revision+1 {
		t.Fatalf("bound todo=%+v", updated)
	}
	event, err := host.NextEvent(ctx)
	if err != nil || event.Kind != EventTodoUpdated || event.Todo == nil || event.Todo.Revision != updated.Revision {
		t.Fatalf("todo event=%+v err=%v", event, err)
	}
}

func TestSubagentGetOutputReturnsOrderedSnapshotsAndMarksDelivery(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()
	finished := time.Now().UTC()
	run := agentservice.SubagentRun{
		ID: "finished", SessionID: "session", ParentRunID: "parent", Description: "inspect", Type: "explore",
		State: agentservice.SubagentCompleted, Summary: "done", Model: "model", CapabilityMode: "read-only",
		RequestedIsolation: "none", Isolation: "none", CWD: t.TempDir(), Output: "answer",
		ToolsUsed: []string{"coding.read_file"}, StartedAt: finished.Add(-time.Second), FinishedAt: finished,
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	driver := &subagentGetOutputDriver{runtime: runtime, sessionID: "session"}
	arguments := json.RawMessage(`{"task_ids":["finished","missing","finished"],"timeout_ms":0}`)
	result, err := driver.Execute(ctx, tool.Call{ID: "query", Name: subagentGetOutputTool, Arguments: arguments}, nil)
	if err != nil || result.IsError {
		t.Fatalf("query result=%#v err=%v", result, err)
	}
	var payload struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Tasks) != 2 || payload.Tasks[0]["task_id"] != "finished" || payload.Tasks[0]["status"] != "completed" ||
		payload.Tasks[1]["task_id"] != "missing" || payload.Tasks[1]["status"] != "not_found" {
		t.Fatalf("query payload = %#v", payload)
	}
	delivered, err := store.Get(ctx, run.ID)
	if err != nil || !delivered.CompletionDelivered {
		t.Fatalf("completion delivery = %#v, %v", delivered, err)
	}
	if _, _, err := decodeSubagentQueryInput(json.RawMessage(`{"task_ids":["one"],"timeout_ms":600001}`)); err == nil {
		t.Fatal("oversized timeout was accepted")
	}
}

func TestSubagentKillReturnsTypedOrdinaryResults(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()
	run := agentservice.SubagentRun{
		ID: "queued-task", SessionID: "session", ParentRunID: "parent", Description: "wait", Type: "verify",
		State: agentservice.SubagentQueued, Summary: "queued", StartedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	var cancelCalls atomic.Int32
	runtime.active[run.ID] = &activeSubagent{
		run: run, cancel: func() { cancelCalls.Add(1) }, done: make(chan struct{}), toolNames: make(map[string]struct{}),
	}
	driver := &subagentKillDriver{runtime: runtime, sessionID: "session"}
	call := tool.Call{ID: "kill", Name: subagentKillTool, Arguments: json.RawMessage(`{"task_id":"queued-task"}`)}
	result, err := driver.Execute(ctx, call, nil)
	if err != nil || result.IsError {
		t.Fatalf("kill result=%#v err=%v", result, err)
	}
	var accepted map[string]any
	if err := json.Unmarshal([]byte(result.Content), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted["outcome"] != "cancel_requested" || accepted["status"] != "cancelling" || cancelCalls.Load() != 1 {
		t.Fatalf("accepted kill = %#v calls=%d", accepted, cancelCalls.Load())
	}

	result, err = driver.Execute(ctx, call, nil)
	if err != nil || result.IsError {
		t.Fatalf("repeat kill result=%#v err=%v", result, err)
	}
	var finished map[string]any
	if err := json.Unmarshal([]byte(result.Content), &finished); err != nil {
		t.Fatal(err)
	}
	if finished["outcome"] != "already_finished" || finished["status"] != "cancelled" || cancelCalls.Load() != 1 {
		t.Fatalf("finished kill = %#v calls=%d", finished, cancelCalls.Load())
	}

	unknownCall := tool.Call{ID: "unknown", Name: subagentKillTool, Arguments: json.RawMessage(`{"task_id":"missing"}`)}
	result, err = driver.Execute(ctx, unknownCall, nil)
	if err != nil || result.IsError {
		t.Fatalf("unknown kill result=%#v err=%v", result, err)
	}
	var unknown map[string]any
	if err := json.Unmarshal([]byte(result.Content), &unknown); err != nil {
		t.Fatal(err)
	}
	if unknown["outcome"] != "not_found" || unknown["status"] != "not_found" {
		t.Fatalf("unknown kill = %#v", unknown)
	}
}

func TestSubagentResultJSONContracts(t *testing.T) {
	snapshot := agentservice.SubagentSnapshot{
		Found: true,
		Run: agentservice.SubagentRun{
			ID: "task", State: agentservice.SubagentCompleted, Output: "answer",
			ToolCalls: 1, Turns: 2, TokensUsed: 3,
		},
	}
	encoded, err := json.Marshal(foregroundSubagentResult(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":"","output":"answer","status":"completed","task_id":"task","usage":{"tokens_used":3,"tool_calls":1,"turns":2},"warning":""}`
	if string(encoded) != want {
		t.Fatalf("foreground result = %s", encoded)
	}
	notFound, err := json.Marshal(subagentSnapshotJSON(agentservice.SubagentSnapshot{Run: agentservice.SubagentRun{ID: "missing"}}))
	if err != nil {
		t.Fatal(err)
	}
	if string(notFound) != `{"status":"not_found","task_id":"missing"}` {
		t.Fatalf("not-found result = %s", notFound)
	}
}

func TestEffectiveSubagentToolsIntersectsCapabilityAndRoleAllowlist(t *testing.T) {
	allTools := []string{
		"coding.list_files", "coding.read_file", "coding.search", "coding.git_diff",
		"coding.edit_hashline", "coding.write_file", "coding.gofmt", "coding.go_test", "coding.shell",
		"subagent.spawn", "mcp.external",
	}
	tests := []struct {
		mode string
		want []string
	}{
		{mode: "read-only", want: []string{"coding.git_diff", "coding.list_files", "coding.read_file", "coding.search"}},
		{mode: "read-write", want: []string{"coding.edit_hashline", "coding.git_diff", "coding.gofmt", "coding.list_files", "coding.read_file", "coding.search", "coding.write_file"}},
		{mode: "execute", want: []string{"coding.git_diff", "coding.go_test", "coding.list_files", "coding.read_file", "coding.search", "coding.shell"}},
		{mode: "all", want: []string{"coding.edit_hashline", "coding.git_diff", "coding.go_test", "coding.gofmt", "coding.list_files", "coding.read_file", "coding.search", "coding.shell", "coding.write_file"}},
		{mode: "invalid"},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			allowed := effectiveSubagentTools(allTools, test.mode)
			got := make([]string, 0, len(allowed))
			for name := range allowed {
				got = append(got, name)
			}
			slices.Sort(got)
			if !slices.Equal(got, test.want) {
				t.Fatalf("effective tools = %v, want %v", got, test.want)
			}
		})
	}
	restricted := effectiveSubagentTools([]string{"coding.read_file"}, "all")
	if len(restricted) != 1 || !restricted["coding.read_file"] {
		t.Fatalf("role allowlist expanded: %v", restricted)
	}
}

func TestRenderSubagentInstructionsComposesPersonaRoleAndContracts(t *testing.T) {
	profile := effectiveSubagentProfile{
		Type: "specialist", Persona: "analyst", CWD: "/workspace",
		Instructions: "Think like a reliability engineer.\n\nReturn a structured assessment.",
		Inputs: []config.SubagentContractItem{
			{Name: "scope", Type: "string", Required: true, Description: "Area to inspect"},
		},
		Outputs: []config.SubagentContractItem{
			{Name: "findings", Type: "array", Required: true},
		},
	}
	rendered := renderSubagentInstructions(profile)
	for _, wanted := range []string{
		"You are the specialist subagent. Apply the analyst persona.",
		"Work only within /workspace.",
		"Think like a reliability engineer.",
		"Return a structured assessment.",
		"Input contract:\n- scope (string, required): Area to inspect",
		"Output contract:\n- findings (array, required)",
	} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("rendered instructions missing %q:\n%s", wanted, rendered)
		}
	}
}

func TestResolveSubagentProfileKeepsRouteLayerAtomic(t *testing.T) {
	cfg := config.Default().Agents.Subagents
	cfg.Personas["specialist"] = config.SubagentPersonaConfig{Instructions: "persona", Provider: "chatgpt", Model: "persona-model", Reasoning: "medium"}
	cfg.Roles["explore"] = config.SubagentRoleConfig{Persona: "specialist", Instructions: "role", Reasoning: "high"}
	runtime := subagentRuntime{cfg: cfg}
	parent := subagentParentRuntime{ProviderID: "chatgpt", ModelID: "parent-model", Reasoning: "low", WorkspaceRoot: "/workspace"}

	profile, err := runtime.resolveProfile(subagentSpawnInput{SubagentType: "explore"}, parent)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Provider != "chatgpt" || profile.Model != "persona-model" || profile.Reasoning != "high" {
		t.Fatalf("reasoning-only role spliced route: %#v", profile)
	}

	role := cfg.Roles["explore"]
	role.Model = "role-model"
	cfg.Roles["explore"] = role
	runtime.cfg = cfg
	profile, err = runtime.resolveProfile(subagentSpawnInput{SubagentType: "explore", Model: "explicit-model"}, parent)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Provider != parent.ProviderID || profile.Model != "explicit-model" {
		t.Fatalf("explicit model did not inherit parent provider: %#v", profile)
	}
}

func TestForegroundWaitStartsAfterQueuedTaskRunsAndPersistsDemotion(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Agents.Subagents.AwaitDuration = 20 * time.Millisecond
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()
	run := agentservice.SubagentRun{
		ID: "queued", SessionID: "session", State: agentservice.SubagentQueued,
		Description: "queued task", StartedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	childCtx, childCancel := context.WithCancel(ctx)
	defer childCancel()
	runtime.active[run.ID] = &activeSubagent{
		run: run, ctx: childCtx, cancel: childCancel, done: make(chan struct{}), toolNames: make(map[string]struct{}),
	}
	returned := make(chan agentservice.SubagentSnapshot, 1)
	go func() {
		returned <- runtime.waitForForegroundStart(ctx, run.SessionID, run.ID)
	}()
	time.Sleep(3 * cfg.Agents.Subagents.AwaitDuration)
	select {
	case snapshot := <-returned:
		t.Fatalf("queued wait returned before start: %#v", snapshot)
	default:
	}
	runtime.mu.Lock()
	runtime.active[run.ID].run.State = agentservice.SubagentRunning
	runtime.signalChangedLocked()
	runtime.mu.Unlock()
	select {
	case snapshot := <-returned:
		if !snapshot.Found || snapshot.Run.State != agentservice.SubagentRunning {
			t.Fatalf("start snapshot = %#v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground wait did not observe running transition")
	}

	runtime.demote(run.SessionID, run.ID, "foreground wait timed out")
	persisted, err := store.Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Background || persisted.Warning != "foreground wait timed out" {
		t.Fatalf("persisted demotion = %#v", persisted)
	}
}

type metadataOnlyDriver struct{}

func (metadataOnlyDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "test", Models: []string{"model"}}
}

func (metadataOnlyDriver) Stream(context.Context, hyprovider.Request) (hyprovider.Stream, error) {
	return hyprovider.NewSliceStream(nil), nil
}

func TestCancelledForegroundToolWaitLeavesChildRunningInBackground(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()
	runtime.running = cfg.Agents.Subagents.MaxConcurrency
	parent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "parent", ProviderID: "test", ModelID: "model",
		WorkspaceRoot: t.TempDir(), Driver: metadataOnlyDriver{},
	}
	driver := &subagentSpawnDriver{runtime: runtime, parent: parent}
	callCtx, cancel := context.WithCancel(ctx)
	cancel()
	call := tool.Call{
		ID: "spawn", Name: subagentSpawnTool,
		Arguments: json.RawMessage(`{"prompt":"inspect","description":"queued child","subagent_type":"explore","background":false}`),
	}
	result, err := driver.Execute(callCtx, call, nil)
	if err != nil || result.IsError {
		t.Fatalf("spawn result=%#v err=%v", result, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "queued" || !strings.Contains(fmt.Sprint(payload["warning"]), "continues in background") {
		t.Fatalf("cancelled wait payload = %#v", payload)
	}
	runs, err := store.List(ctx, "session")
	if err != nil || len(runs) != 1 {
		t.Fatalf("stored runs=%#v err=%v", runs, err)
	}
	if !runs[0].Background || runs[0].State != agentservice.SubagentQueued {
		t.Fatalf("detached child = %#v", runs[0])
	}
	runtime.mu.Lock()
	_, active := runtime.active[runs[0].ID]
	runtime.mu.Unlock()
	if !active {
		t.Fatal("tool-call cancellation removed the child task")
	}
}

func TestCancelByParentRunIsSessionScopedAndIncludesBackgroundOnRequest(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()
	runs := []agentservice.SubagentRun{
		{ID: "foreground", SessionID: "session", ParentRunID: "parent", State: agentservice.SubagentQueued, StartedAt: time.Now().UTC()},
		{ID: "background", SessionID: "session", ParentRunID: "parent", State: agentservice.SubagentQueued, Background: true, StartedAt: time.Now().UTC()},
		{ID: "other-parent", SessionID: "session", ParentRunID: "other", State: agentservice.SubagentQueued, StartedAt: time.Now().UTC()},
		{ID: "other-session", SessionID: "other", ParentRunID: "parent", State: agentservice.SubagentQueued, StartedAt: time.Now().UTC()},
	}
	for _, run := range runs {
		if err := store.Create(ctx, run); err != nil {
			t.Fatal(err)
		}
		childCtx, childCancel := context.WithCancel(ctx)
		runtime.active[run.ID] = &activeSubagent{
			run: run, ctx: childCtx, cancel: childCancel, done: make(chan struct{}), toolNames: make(map[string]struct{}),
		}
	}
	if !runtime.HasForegroundByParentRun("session", "parent") {
		t.Fatal("foreground child was not detected")
	}
	runtime.CancelByParentRun("session", "parent", true)
	for _, id := range []string{"foreground", "background"} {
		snapshot := runtime.snapshot(id, "session")
		if !snapshot.Found || snapshot.Run.State != agentservice.SubagentCancelled {
			t.Fatalf("cancelled child %q = %#v", id, snapshot)
		}
	}
	for _, item := range []struct{ sessionID, id string }{{"session", "other-parent"}, {"other", "other-session"}} {
		snapshot := runtime.snapshot(item.id, item.sessionID)
		if !snapshot.Found || snapshot.Run.State != agentservice.SubagentQueued {
			t.Fatalf("unrelated child %q = %#v", item.id, snapshot)
		}
	}
}

type terminalFailingStore struct {
	agentservice.SubagentRunStore
}

func (s terminalFailingStore) Save(ctx context.Context, run agentservice.SubagentRun) error {
	if subagentTerminal(run.State) {
		return fmt.Errorf("fixture terminal save failure")
	}
	return s.SubagentRunStore.Save(ctx, run)
}

func TestTerminalizerFallsBackWithoutLeakingSlotOrDoneWaiter(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	sqlStore, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	store := terminalFailingStore{SubagentRunStore: sqlStore}
	cfg := config.Default()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()
	run := agentservice.SubagentRun{
		ID: "terminal", SessionID: "session", ParentRunID: "parent",
		State: agentservice.SubagentRunning, StartedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	childCtx, childCancel := context.WithCancel(ctx)
	defer childCancel()
	done := make(chan struct{})
	runtime.running = 1
	runtime.active[run.ID] = &activeSubagent{
		run: run, ctx: childCtx, cancel: childCancel, done: done, slot: true, toolNames: make(map[string]struct{}),
	}
	runtime.terminalize(run.ID, terminalRequest{state: agentservice.SubagentCancelled})
	select {
	case <-done:
	default:
		t.Fatal("terminalizer did not close done")
	}
	if runtime.running != 0 {
		t.Fatalf("running slots = %d", runtime.running)
	}
	snapshot := runtime.snapshot(run.ID, run.SessionID)
	if !snapshot.Found || snapshot.Run.State != agentservice.SubagentFailed ||
		!strings.Contains(snapshot.Run.Error, "persist terminal subagent") {
		t.Fatalf("fallback snapshot = %#v", snapshot)
	}
	runtime.terminalize(run.ID, terminalRequest{state: agentservice.SubagentCompleted})
	repeated := runtime.snapshot(run.ID, run.SessionID)
	if repeated.Run.State != snapshot.Run.State || repeated.Run.Error != snapshot.Run.Error {
		t.Fatalf("repeat terminalization changed fallback: first=%#v repeat=%#v", snapshot, repeated)
	}
}

func TestResumeCreatesNewTaskWithInheritedProfileAndSanitizedTranscript(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := json.Marshal([]message.Message{
		{Role: message.RoleSystem, Text: "old system", Metadata: map[string]string{"task_id": "source"}},
		{ID: "old-user", Role: message.RoleUser, Text: "original request", RunID: "old-run", Metadata: map[string]string{"task_id": "source"}},
		{ID: "old-tool-call", Role: message.RoleAssistant, Text: "checking", ToolCalls: []message.ToolCall{{ID: "call", Name: "coding.read_file"}}},
		message.NewToolResult(message.ToolResult{ToolCallID: "call", Name: "coding.read_file", Content: "secret"}),
		{ID: "old-answer", Role: message.RoleAssistant, Text: "source answer", RunID: "old-run"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := agentservice.SubagentRun{
		ID: "source", SessionID: "session", ParentRunID: "old-parent", ChildRunID: "old-child",
		Description: "old", Type: "explore", State: agentservice.SubagentCompleted, Provider: "grok", Model: "model",
		Reasoning: "high", CapabilityMode: "read-only", RequestedIsolation: "none", Isolation: "none",
		CWD: workspace, Output: "source answer", Transcript: transcript, WorktreePath: "/old/worktree",
		StartedAt: time.Now().Add(-time.Minute).UTC(), FinishedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()
	runtime.running = cfg.Agents.Subagents.MaxConcurrency
	parent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "new-parent", ProviderID: "test", ModelID: "other-model",
		WorkspaceRoot: workspace, Driver: metadataOnlyDriver{},
	}
	spawned, err := runtime.Spawn(ctx, subagentSpawnInput{
		Prompt: "continue safely", Description: "new description", ResumeFrom: source.ID, Background: true,
		SubagentType: "general-purpose", Model: "ignored", CapabilityMode: "all", Isolation: "worktree", CWD: "ignored",
	}, parent)
	if err != nil {
		t.Fatal(err)
	}
	if spawned.ID == source.ID || spawned.ParentRunID != "new-parent" || spawned.Type != source.Type ||
		spawned.Provider != source.Provider || spawned.Model != source.Model || spawned.Reasoning != source.Reasoning || spawned.CapabilityMode != source.CapabilityMode ||
		spawned.RequestedIsolation != source.RequestedIsolation || spawned.CWD != source.CWD || spawned.WorktreePath != "" ||
		spawned.Description != "new description" || !spawned.Background {
		t.Fatalf("resumed run = %#v", spawned)
	}
	runtime.mu.Lock()
	seed := append([]message.Message(nil), runtime.active[spawned.ID].profile.Seed...)
	runtime.mu.Unlock()
	if len(seed) != 3 || seed[0].Role != message.RoleUser || seed[0].Text != "original request" ||
		seed[1].Role != message.RoleAssistant || seed[1].Text != "checking" ||
		seed[2].Role != message.RoleAssistant || seed[2].Text != "source answer" {
		t.Fatalf("resume seed = %#v", seed)
	}
	for _, item := range seed {
		if item.ID != "" || item.RunID != "" || len(item.Metadata) != 0 || len(item.ToolCalls) != 0 || item.ToolResult != nil {
			t.Fatalf("unsafe resume metadata survived: %#v", item)
		}
	}
	otherSession := parent
	otherSession.SessionID = "other"
	if _, err := runtime.Spawn(ctx, subagentSpawnInput{Prompt: "x", Description: "x", ResumeFrom: source.ID}, otherSession); err == nil {
		t.Fatal("cross-session resume was accepted")
	}
	nonterminal := source
	nonterminal.ID = "running-source"
	nonterminal.State = agentservice.SubagentRunning
	nonterminal.FinishedAt = time.Time{}
	if err := store.Create(ctx, nonterminal); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Spawn(ctx, subagentSpawnInput{Prompt: "x", Description: "x", ResumeFrom: nonterminal.ID}, parent); err == nil {
		t.Fatal("non-terminal resume was accepted")
	}
	malformed := source
	malformed.ID = "malformed-source"
	malformed.Transcript = json.RawMessage(`{`)
	if err := store.Create(ctx, malformed); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Spawn(ctx, subagentSpawnInput{Prompt: "x", Description: "x", ResumeFrom: malformed.ID}, parent); err == nil {
		t.Fatal("malformed transcript resume was accepted")
	}
}

func TestBackgroundCompletionAutoWakesIdleSessionOnce(t *testing.T) {
	ctx := context.Background()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	sessions := session.NewService(providerStore.DB())
	if _, err := sessions.Ensure(ctx, session.Session{
		ID: "session", Title: "Auto wake", ProviderID: "chatgpt", ModelID: "model", Reasoning: "high", AgentMode: "single",
	}); err != nil {
		t.Fatal(err)
	}
	host := NewService(ctx, cfg)
	host.AttachDurable(sessions, nil)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := host.Shutdown(shutdownCtx); err != nil {
			t.Fatal(err)
		}
	}()
	runtime, err := newSubagentRuntime(ctx, cfg.Agents.Subagents, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cancel()
	run := agentservice.SubagentRun{
		ID: "background-completion", SessionID: "session", ParentRunID: "parent", Type: "explore",
		Description: "inspect", State: agentservice.SubagentRunning, Background: true, StartedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	childCtx, childCancel := context.WithCancel(ctx)
	defer childCancel()
	runtime.active[run.ID] = &activeSubagent{
		run: run, parent: subagentParentRuntime{Host: host}, ctx: childCtx, cancel: childCancel,
		done: make(chan struct{}), slot: true, toolNames: make(map[string]struct{}),
	}
	runtime.running = 1
	runtime.terminalize(run.ID, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("fixture failure")})

	deadline := time.Now().Add(2 * time.Second)
	for {
		persisted, err := store.Get(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.CompletionDelivered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completion was not delivered: %#v", persisted)
		}
		time.Sleep(10 * time.Millisecond)
	}
	projection, err := sessions.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	wakeBlocks := 0
	for _, block := range projection.Blocks {
		if block.Kind == "user" && strings.Contains(block.Content, "Background subagent background-completion") {
			wakeBlocks++
		}
	}
	if wakeBlocks != 1 {
		t.Fatalf("auto-wake blocks = %d, projection = %#v", wakeBlocks, projection.Blocks)
	}
	runtime.AutoWakePending("session")
	time.Sleep(20 * time.Millisecond)
	projection, err = sessions.LoadProjection(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	wakeBlocks = 0
	for _, block := range projection.Blocks {
		if block.Kind == "user" && strings.Contains(block.Content, "Background subagent background-completion") {
			wakeBlocks++
		}
	}
	if wakeBlocks != 1 {
		t.Fatalf("completion was delivered more than once: %#v", projection.Blocks)
	}
}

func TestTranscriptToAgentBlocksUsesStableOrderingAndFailureStates(t *testing.T) {
	encoded, err := json.Marshal([]message.Message{
		{ID: "system", Role: message.RoleSystem, Text: "hidden"},
		{ID: "user", Role: message.RoleUser, RunID: "run-user", Text: "inspect"},
		{
			ID: "assistant", Role: message.RoleAssistant, RunID: "run-child", Thinking: "reasoning", Text: "working",
			ToolCalls: []message.ToolCall{
				{ID: "matched", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"a"}`)},
				{ID: "missing", Name: "coding.search", Arguments: json.RawMessage(`{"query":"b"}`)},
			},
		},
		{ID: "result", Role: message.RoleTool, RunID: "run-tool", ToolResult: &message.ToolResult{ToolCallID: "matched", Name: "coding.read_file", Content: "result"}},
		{ID: "orphan", Role: message.RoleTool, RunID: "run-orphan", ToolResult: &message.ToolResult{ToolCallID: "unknown", Name: "coding.read_file", Content: "orphan"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := transcriptToAgentBlocks(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 6 {
		t.Fatalf("blocks = %#v", blocks)
	}
	for index, want := range []struct {
		id, kind, state, runID string
	}{
		{"msg-1-user", "user", "completed", "run-user"},
		{"msg-2-thinking", "thinking", "completed", "run-child"},
		{"msg-2-text", "assistant", "completed", "run-child"},
		{"call-matched", "tool", "completed", "run-child"},
		{"call-missing", "tool", "failed", "run-child"},
		{"result-4", "tool", "failed", "run-orphan"},
	} {
		got := blocks[index]
		if got.ID != want.id || got.Kind != want.kind || got.State != want.state || got.RunID != want.runID {
			t.Fatalf("block %d = %#v, want %#v", index, got, want)
		}
	}
	if !strings.Contains(blocks[3].Content, "result") || !strings.Contains(blocks[4].Content, "missing tool result") {
		t.Fatalf("tool result mapping = %#v", blocks)
	}
}

func TestEventCloneDeepCopiesTypedAgentContracts(t *testing.T) {
	event := Event{
		Agent:          &AgentStatePayload{Type: "explore"},
		AgentBlocks:    []AgentTranscriptBlock{{ID: "block", Content: "original"}},
		AgentCatalog:   []AgentCatalogEntry{{Name: "explore", Description: "original"}},
		AgentSnapshots: []AgentSnapshotPayload{{ID: "child", Agent: AgentStatePayload{Type: "review"}}},
	}
	cloned := event.Clone()
	cloned.Agent.Type = "changed"
	cloned.AgentBlocks[0].Content = "changed"
	cloned.AgentCatalog[0].Description = "changed"
	cloned.AgentSnapshots[0].Agent.Type = "changed"
	if event.Agent.Type != "explore" || event.AgentBlocks[0].Content != "original" ||
		event.AgentCatalog[0].Description != "original" || event.AgentSnapshots[0].Agent.Type != "review" {
		t.Fatalf("clone mutated original: %#v", event)
	}
}

type concurrentApprovalDriver struct{}

func (concurrentApprovalDriver) Definition() tool.Definition {
	return tool.Definition{
		Name: "test.write", Description: "write", EffectType: tool.EffectWrite,
		RequiresApproval: true, RiskLevel: "high", InputSchema: tool.Schema{Type: "object"},
	}
}

func (concurrentApprovalDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "written"}, nil
}

func TestConcurrentChildApprovalsWithSameToolCallIDRemainIsolated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	coding, err := agentservice.NewService(providerStore, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	host := NewService(ctx, config.Default())
	host.coding = coding
	defer host.cancel()
	call := tool.Call{ID: "shared-call", Name: "test.write", Arguments: json.RawMessage(`{"path":"same"}`)}
	runs := make([]*agentservice.Run, 2)
	pending := make([]agentservice.PendingApproval, 2)
	for index := range runs {
		runs[index], err = coding.StartRun(ctx, fmt.Sprintf("child %d", index))
		if err != nil {
			t.Fatal(err)
		}
		execution, executeErr := coding.ExecuteDriver(ctx, runs[index], concurrentApprovalDriver{}, call, nil)
		if executeErr != nil || execution.Approval == nil {
			t.Fatalf("prepare approval %d: execution=%#v err=%v", index, execution, executeErr)
		}
		pending[index] = *execution.Approval
	}
	type approvalResult struct {
		agent string
		mode  agentservice.ApprovalMode
		err   error
	}
	results := make(chan approvalResult, 2)
	for index := range runs {
		agentID := fmt.Sprintf("agent-%d", index+1)
		go func(run *agentservice.Run, request agentservice.PendingApproval, id string) {
			resolution, waitErr := host.awaitApproval(ctx, "session", id, "explore", run, call, request)
			results <- approvalResult{agent: id, mode: resolution.Mode, err: waitErr}
		}(runs[index], pending[index], agentID)
	}
	approvalIDs := make(map[string]string)
	for len(approvalIDs) < 2 {
		event, eventErr := host.NextEvent(ctx)
		if eventErr != nil {
			t.Fatal(eventErr)
		}
		if event.Kind != EventApprovalRequested {
			continue
		}
		if event.ToolCallID != "shared-call" || event.ApprovalID == "" {
			t.Fatalf("approval event = %#v", event)
		}
		approvalIDs[event.AgentID] = event.ApprovalID
	}
	if approvalIDs["agent-1"] == approvalIDs["agent-2"] {
		t.Fatalf("public approval IDs collided: %#v", approvalIDs)
	}
	if err := host.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: approvalIDs["agent-1"], Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	if err := host.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: approvalIDs["agent-2"], Decision: "once"}); err != nil {
		t.Fatal(err)
	}
	gotModes := make(map[string]agentservice.ApprovalMode)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		gotModes[result.agent] = result.mode
	}
	if gotModes["agent-1"] != agentservice.ApprovalDenied || gotModes["agent-2"] != agentservice.ApprovalOnce {
		t.Fatalf("approval modes crossed: %#v", gotModes)
	}
	host.mu.Lock()
	pendingCount := len(host.liveApprovals)
	host.mu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("live approvals leaked: %d", pendingCount)
	}
}

func TestAgentCatalogActionsExposeEffectiveConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cfg := config.Default()
	role := cfg.Agents.Subagents.Roles["explore"]
	role.Model = "child-model"
	role.Source = "/project/.azem/agents.yaml"
	cfg.Agents.Subagents.Roles["explore"] = role
	cfg.Agents.Subagents.Toggle["explore"] = false
	persona := cfg.Agents.Subagents.Personas["analysis"]
	persona.Source = "/home/user/.azem/personas.yaml"
	cfg.Agents.Subagents.Personas["analysis"] = persona
	service := NewService(ctx, cfg)
	defer service.cancel()
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListAgentTypes, SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventAgentDetail || event.State != "agent_types" {
		t.Fatalf("agent type event = %#v", event)
	}
	foundRole := false
	for _, entry := range event.AgentCatalog {
		if entry.Name == "explore" {
			foundRole = true
			if entry.Model != "child-model" || entry.Source != "/project/.azem/agents.yaml" || entry.Enabled {
				t.Fatalf("effective role entry = %#v", entry)
			}
		}
	}
	if !foundRole {
		t.Fatalf("explore role missing: %#v", event.AgentCatalog)
	}
	if err := service.ExecuteAction(ctx, Action{Kind: ActionListPersonas, SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	event, err = service.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundPersona := false
	for _, entry := range event.AgentCatalog {
		if entry.Name == "analysis" {
			foundPersona = entry.Source == "/home/user/.azem/personas.yaml"
		}
	}
	if event.State != "personas" || !foundPersona {
		t.Fatalf("persona catalog event = %#v", event)
	}
}

type gatedSubagentDriver struct {
	mu       sync.Mutex
	started  chan string
	release  chan struct{}
	running  int
	maxAlive int
}

func newGatedSubagentDriver() *gatedSubagentDriver {
	return &gatedSubagentDriver{
		started: make(chan string, 8),
		release: make(chan struct{}, 8),
	}
}

func (*gatedSubagentDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "test", Models: []string{"model"}}
}

func (d *gatedSubagentDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	goal := ""
	if len(request.Messages) > 0 {
		goal = request.Messages[len(request.Messages)-1].Text
	}
	d.mu.Lock()
	d.running++
	d.maxAlive = max(d.maxAlive, d.running)
	d.mu.Unlock()
	d.started <- goal
	return &gatedSubagentStream{ctx: ctx, driver: d, goal: goal}, nil
}

type gatedSubagentStream struct {
	ctx      context.Context
	driver   *gatedSubagentDriver
	goal     string
	released bool
	done     bool
}

func (s *gatedSubagentStream) Recv() (hyprovider.Event, error) {
	if !s.released {
		select {
		case <-s.ctx.Done():
			return hyprovider.Event{}, s.ctx.Err()
		case <-s.driver.release:
		}
		s.driver.mu.Lock()
		s.driver.running--
		s.driver.mu.Unlock()
		s.released = true
		return hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: "completed " + s.goal}, nil
	}
	if !s.done {
		s.done = true
		return hyprovider.Event{Kind: hyprovider.EventDone}, nil
	}
	return hyprovider.Event{}, io.EOF
}

func (*gatedSubagentStream) Close() error { return nil }

func TestSubagentCoordinatorEnforcesConcurrencyAndFIFOQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	providerStore, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer providerStore.Close(ctx)
	coding, err := agentservice.NewService(providerStore, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Agents.Subagents
	cfg.MaxConcurrency = 2
	runtime, err := newSubagentRuntime(ctx, cfg, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(ctx)
	driver := newGatedSubagentDriver()
	parent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "parent", ProviderID: "test", ModelID: "model", Reasoning: "high",
		Driver: driver, Coding: coding, WorkspaceRoot: t.TempDir(),
	}
	var runs []agentservice.SubagentRun
	for _, goal := range []string{"one", "two", "three", "four"} {
		run, err := runtime.Spawn(ctx, subagentSpawnInput{
			Prompt: goal, Description: goal, SubagentType: "explore", Background: true,
		}, parent)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	first := <-driver.started
	second := <-driver.started
	if !slices.Contains([]string{"one", "two"}, first) || !slices.Contains([]string{"one", "two"}, second) || first == second {
		t.Fatalf("first running tasks = %q, %q", first, second)
	}
	select {
	case unexpected := <-driver.started:
		t.Fatalf("concurrency cap admitted %q before a slot opened", unexpected)
	case <-time.After(50 * time.Millisecond):
	}
	driver.release <- struct{}{}
	if started := <-driver.started; started != "three" {
		t.Fatalf("first queued task started as %q", started)
	}
	driver.release <- struct{}{}
	if started := <-driver.started; started != "four" {
		t.Fatalf("second queued task started as %q", started)
	}
	driver.release <- struct{}{}
	driver.release <- struct{}{}
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	snapshots := runtime.Query(ctx, "session", ids, 5*time.Second)
	if len(snapshots) != len(runs) {
		t.Fatalf("terminal snapshots = %#v", snapshots)
	}
	for _, snapshot := range snapshots {
		if !snapshot.Found || snapshot.Run.State != agentservice.SubagentCompleted {
			t.Fatalf("non-terminal coordinator result = %#v", snapshot)
		}
	}
	driver.mu.Lock()
	peak := driver.maxAlive
	driver.mu.Unlock()
	if peak != cfg.MaxConcurrency {
		t.Fatalf("peak concurrent streams = %d, want %d", peak, cfg.MaxConcurrency)
	}
}

type failingSubagentDriver struct {
	panicValue string
	err        error
}

func (failingSubagentDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "test", Models: []string{"model"}}
}

func (d failingSubagentDriver) Stream(context.Context, hyprovider.Request) (hyprovider.Stream, error) {
	if d.panicValue != "" {
		panic(d.panicValue)
	}
	return nil, d.err
}

func TestSubagentCoordinatorTerminalizesProviderFailureAndPanic(t *testing.T) {
	for _, test := range []struct {
		name   string
		driver failingSubagentDriver
		wanted string
	}{
		{name: "error", driver: failingSubagentDriver{err: fmt.Errorf("provider unavailable")}, wanted: "provider unavailable"},
		{name: "panic", driver: failingSubagentDriver{panicValue: "provider exploded"}, wanted: "provider exploded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			providerStore, err := sqlitestore.Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer providerStore.Close(ctx)
			coding, err := agentservice.NewService(providerStore, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer coding.Close(ctx)
			store, err := agentservice.NewSQLSubagentRunStore(providerStore.DB())
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := newSubagentRuntime(ctx, config.Default().Agents.Subagents, store, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Shutdown(ctx)
			parent := subagentParentRuntime{
				SessionID: "session", ParentRunID: "parent", ProviderID: "test", ModelID: "model", Reasoning: "high",
				Driver: test.driver, Coding: coding, WorkspaceRoot: t.TempDir(),
			}
			run, err := runtime.Spawn(ctx, subagentSpawnInput{
				Prompt: "fail", Description: "fail", SubagentType: "explore", Background: true,
			}, parent)
			if err != nil {
				t.Fatal(err)
			}
			snapshots := runtime.Query(ctx, "session", []string{run.ID}, 3*time.Second)
			if len(snapshots) != 1 || !snapshots[0].Found || snapshots[0].Run.State != agentservice.SubagentFailed ||
				!strings.Contains(snapshots[0].Run.Error, test.wanted) {
				t.Fatalf("terminal failure snapshot = %#v", snapshots)
			}
			projection, err := coding.Recover(ctx, snapshots[0].Run.ChildRunID)
			if err != nil {
				t.Fatal(err)
			}
			if string(projection.Run.Status) != "failed" {
				t.Fatalf("child coding run status = %q", projection.Run.Status)
			}
		})
	}
}

func TestSubagentTurnContextCompactsToModelTarget(t *testing.T) {
	contextManager := subagentTurnContext{}
	history := []message.Message{
		message.NewText(message.RoleSystem, "stable rules"),
		message.NewText(message.RoleUser, "old request"),
		message.NewText(message.RoleAssistant, strings.Repeat("old evidence ", 2_000)),
		message.NewText(message.RoleUser, "latest request"),
	}
	compacted, err := contextManager.CompactTo(context.Background(), history, 300)
	if err != nil {
		t.Fatal(err)
	}
	if tokens := estimateContextTokens(compacted); tokens > 300 {
		t.Fatalf("compacted context estimate = %d, want <= 300", tokens)
	}
	if compacted[len(compacted)-1].Role != message.RoleUser || compacted[len(compacted)-1].Text != "latest request" {
		t.Fatalf("compacted context lost latest request: %#v", compacted)
	}
	foundSummary := false
	for _, current := range compacted {
		foundSummary = foundSummary || current.Kind == message.KindCompactionSummary
	}
	if !foundSummary {
		t.Fatalf("compacted context omitted the compaction marker: %#v", compacted)
	}
}
