package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/venat/tool"
)

func TestVibeWorkersRunConcurrentlyPersistAndAcceptFollowups(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runtime, provider, coding, _ := newGatedForegroundHarness(t, ctx, 0)
	defer runtime.Shutdown(ctx)
	defer coding.Close(ctx)
	parent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "director", ProviderID: "test", AccountID: "test-account", ModelID: "model", Reasoning: "high",
		Driver: provider, Coding: coding, WorkspaceRoot: t.TempDir(), DirectorReadOnly: true,
	}
	if _, err := runtime.Drivers(parent); err != nil {
		t.Fatal(err)
	}
	drivers, err := newVibeDrivers(runtime, parent, config.VibeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	spawn := vibeTool(t, drivers, vibeSpawnTool)
	fast := executeVibe(t, ctx, spawn, `{"cli":"fast","name":"FastWorker","prompt":"inspect fast work"}`)
	good := executeVibe(t, ctx, spawn, `{"cli":"good","name":"GoodWorker","prompt":"inspect hard work"}`)
	if fast.IsError || good.IsError || !strings.Contains(fast.Content, "FastWorker") || !strings.Contains(good.Content, "GoodWorker") {
		t.Fatalf("Vibe spawn fast=%#v good=%#v", fast, good)
	}
	started := make([]string, 0, 2)
	startDeadline := time.NewTimer(5 * time.Second)
	defer startDeadline.Stop()
	for len(started) < 2 {
		select {
		case goal := <-provider.started:
			started = append(started, goal)
		case <-startDeadline.C:
			t.Fatalf("Vibe workers did not start: %q", started)
		}
	}
	provider.mu.Lock()
	maxAlive := provider.maxAlive
	provider.mu.Unlock()
	if maxAlive != 2 {
		t.Fatalf("Vibe concurrency = %d, want 2", maxAlive)
	}
	listed := executeVibe(t, ctx, vibeTool(t, drivers, vibeListTool), `{}`)
	if !strings.Contains(listed.Content, "FastWorker") || !strings.Contains(listed.Content, "GoodWorker") || !strings.Contains(listed.Content, "running") {
		t.Fatalf("Vibe running roster = %s", listed.Content)
	}
	provider.release <- struct{}{}
	provider.release <- struct{}{}
	waited := executeVibe(t, ctx, vibeTool(t, drivers, vibeWaitTool), `{"sessions":["FastWorker","GoodWorker"],"timeout":2}`)
	if waited.IsError || !strings.Contains(waited.Content, "completed") {
		t.Fatalf("Vibe wait = %#v", waited)
	}
	sent := executeVibe(t, ctx, vibeTool(t, drivers, vibeSendTool), `{"session":"FastWorker","message":"inspect the follow-up"}`)
	if sent.IsError || !strings.Contains(sent.Content, "prior conversation") {
		t.Fatalf("Vibe follow-up = %#v", sent)
	}
	reviveDeadline := time.NewTimer(5 * time.Second)
	defer reviveDeadline.Stop()
	select {
	case goal := <-provider.started:
		if !strings.Contains(goal, "inspect the follow-up") {
			t.Fatalf("revived Vibe prompt = %q", goal)
		}
	case <-reviveDeadline.C:
		t.Fatal("idle Vibe worker did not revive within 5 seconds")
	}
	provider.release <- struct{}{}
	_ = executeVibe(t, ctx, vibeTool(t, drivers, vibeWaitTool), `{"sessions":["FastWorker"],"timeout":2}`)
	killed := executeVibe(t, ctx, vibeTool(t, drivers, vibeKillTool), `{"session":"FastWorker"}`)
	if killed.IsError || !strings.Contains(killed.Content, "Killed") {
		t.Fatalf("Vibe kill = %#v", killed)
	}
	listed = executeVibe(t, ctx, vibeTool(t, drivers, vibeListTool), `{}`)
	if !strings.Contains(listed.Content, "FastWorker") || !strings.Contains(listed.Content, "dead") {
		t.Fatalf("Vibe dead roster = %s", listed.Content)
	}
}

func TestVibeWorkerNamesAndMailboxesAreSessionScoped(t *testing.T) {
	runtime := &subagentRuntime{
		active: map[string]*activeSubagent{
			"a": {name: "SameName", run: agentservice.SubagentRun{SessionID: "session-a"}},
			"b": {name: "SameName", run: agentservice.SubagentRun{SessionID: "session-b"}},
		},
		parked: map[string]*parkedSubagent{
			"a": {name: "SameName", run: agentservice.SubagentRun{SessionID: "session-a"}},
			"b": {name: "SameName", run: agentservice.SubagentRun{SessionID: "session-b"}},
		},
	}
	a := &vibeDriver{runtime: runtime, parent: subagentParentRuntime{SessionID: "session-a"}}
	b := &vibeDriver{runtime: runtime, parent: subagentParentRuntime{SessionID: "session-b"}}
	a.saveRecord(vibeRecord{Name: "SameName", RunID: "a", CLI: "fast", State: "idle"})
	if _, exists := b.record("SameName"); exists {
		t.Fatal("registry leaked into another session")
	}
	b.saveRecord(vibeRecord{Name: "SameName", RunID: "b", CLI: "good", State: "idle"})
	if a.screens(nil)[0].RunID != "a" || b.screens(nil)[0].RunID != "b" {
		t.Fatal("active names crossed sessions")
	}
	if hubPeerMailboxKey("session-a", "SameName") == hubPeerMailboxKey("session-b", "SameName") {
		t.Fatal("mailboxes cross sessions")
	}
	runtime.active = nil
	killed := b.kill(context.Background(), tool.Call{ID: "kill-b", Name: vibeKillTool, Arguments: json.RawMessage(`{"session":"SameName"}`)})
	if killed.IsError {
		t.Fatal(killed.Content)
	}
	if _, exists := runtime.parked["a"]; !exists {
		t.Fatal("kill removed another session's parked worker")
	}
	if _, exists := runtime.parked["b"]; exists {
		t.Fatal("kill retained its own parked worker")
	}
	if a.screens(nil)[0].State != "idle" || b.screens(nil)[0].State != "dead" {
		t.Fatal("kill state crossed sessions")
	}
}

func TestProviderRuntimeVibeDirectorUsesOnlyReadAndVibeTools(t *testing.T) {
	var mainCalls atomic.Int32
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(_ int, body string, writer http.ResponseWriter) {
		if !strings.Contains(body, `"prompt_cache_key":"vibe-e2e"`) {
			writeProviderText(writer, "vibe-worker", "worker inspected the workstream")
			return
		}
		switch mainCalls.Add(1) {
		case 1:
			if !strings.Contains(body, "Trusted Vibe mode") || !strings.Contains(body, `"name":"vibe_spawn"`) ||
				!strings.Contains(body, `"name":"coding_read_file"`) || strings.Contains(body, `"name":"coding_write_file"`) ||
				strings.Contains(body, `"name":"coding_shell"`) || strings.Contains(body, `"name":"subagent_spawn"`) {
				t.Errorf("Vibe director tool boundary is wrong: %s", body)
			}
			writeProviderToolCall(writer, "vibe-main-1", "vibe-spawn", vibeSpawnTool, `{"cli":"fast","name":"WorkerA","prompt":"inspect the independent workstream"}`)
		case 2:
			writeProviderToolCall(writer, "vibe-main-2", "vibe-wait", vibeWaitTool, `{"sessions":["WorkerA"],"timeout":2}`)
		case 3:
			if !strings.Contains(body, "worker inspected the workstream") {
				t.Errorf("Vibe worker result missing from director: %s", body)
			}
			writeProviderText(writer, "vibe-main-3", "Director verified the worker result by reading evidence.")
		default:
			t.Errorf("unexpected Vibe director call %d", mainCalls.Load())
			writeProviderText(writer, "vibe-main-extra", "unexpected")
		}
	})
	harness.service.providers.mu.Lock()
	harness.service.providers.cfg.Agents.Vibe = config.VibeConfig{
		Fast: config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal"},
		Good: config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal"},
	}
	harness.service.providers.mu.Unlock()
	subagentStore, err := agentservice.NewSQLSubagentRunStore(harness.store.DB(), harness.store.Blobs())
	if err != nil {
		t.Fatal(err)
	}
	harness.service.providers.Attach(harness.service, nil, subagentStore)
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "vibe-e2e", Prompt: "Direct workers to inspect the task", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "vibe",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if mainCalls.Load() != 3 {
		t.Fatalf("Vibe main calls = %d, want 3", mainCalls.Load())
	}
	projection, err := harness.service.sessions.LoadProjection(context.Background(), "vibe-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) == 0 || projection.Blocks[len(projection.Blocks)-1].Content != "Director verified the worker result by reading evidence." {
		t.Fatalf("Vibe final projection = %#v", projection.Blocks)
	}
	if _, err := harness.service.sessions.LoadLatestArtifactByKind(context.Background(), "vibe-e2e", vibeRegistryArtifactKind); err != nil {
		t.Fatalf("Vibe registry was not persisted: %v", err)
	}
}

func TestVibeModeRequestValidation(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	for name, request := range map[string]TurnRequest{
		"team": {SessionID: "s", Prompt: "x", AgentMode: "team", VibeMode: true},
		"plan": {SessionID: "s", Prompt: "x", AgentMode: "single", PlanMode: true, VibeMode: true},
		"disabled": func() TurnRequest {
			request := TurnRequest{SessionID: "s", Prompt: "x", AgentMode: "single", VibeMode: true, DisableSubagents: true}
			return request
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.StartConfiguredTurn(request); err == nil {
				t.Fatal("invalid Vibe request accepted")
			}
		})
	}
}

func vibeTool(t *testing.T, drivers []tool.Driver, name string) tool.Driver {
	t.Helper()
	for _, driver := range drivers {
		if driver.Definition().Name == name {
			return driver
		}
	}
	t.Fatalf("Vibe tool %s not found", name)
	return nil
}

func executeVibe(t *testing.T, ctx context.Context, driver tool.Driver, input string) tool.Result {
	t.Helper()
	result, err := driver.Execute(ctx, tool.Call{ID: driver.Definition().Name, Name: driver.Definition().Name, Arguments: json.RawMessage(input)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestVibeWaitReturnsFirstWorkerWithoutWaitingForOthers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, provider, coding, _ := newGatedForegroundHarness(t, ctx, 0)
	defer runtime.Shutdown(ctx)
	defer coding.Close(ctx)
	parent := subagentParentRuntime{SessionID: "session", ParentRunID: "director", ProviderID: "test", AccountID: "test-account", ModelID: "model", Reasoning: "high", Driver: provider, Coding: coding, WorkspaceRoot: t.TempDir(), DirectorReadOnly: true}
	if _, err := runtime.Drivers(parent); err != nil {
		t.Fatal(err)
	}
	drivers, err := newVibeDrivers(runtime, parent, config.VibeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	spawn := vibeTool(t, drivers, vibeSpawnTool)
	for _, input := range []string{`{"cli":"fast","name":"A","prompt":"work A"}`, `{"cli":"good","name":"B","prompt":"work B"}`} {
		if result := executeVibe(t, ctx, spawn, input); result.IsError {
			t.Fatal(result.Content)
		}
	}
	for range 2 {
		select {
		case <-provider.started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	provider.release <- struct{}{}
	start := time.Now()
	result := executeVibe(t, ctx, vibeTool(t, drivers, vibeWaitTool), `{"sessions":["A","B"],"timeout":2}`)
	if result.IsError || !strings.Contains(result.Content, "completed") || !strings.Contains(result.Content, "Still running:") || time.Since(start) >= time.Second {
		t.Fatalf("wait did not return first completion: elapsed=%v result=%#v", time.Since(start), result)
	}
	provider.release <- struct{}{}
}

func TestWorkflowSettingPersistsAndRejectsInvalidChanges(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Agents.Fusion = config.ModelRouteConfig{Provider: "grok", Model: "grok-test", Reasoning: "high"}
	s := NewService(ctx, cfg)
	s.configPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(s.configPath, []byte("version: 1\n# preserve existing route settings\nagents:\n  fusion:\n    provider: grok\n    model: grok-test\n    reasoning: high\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.activeRun, s.activeSession = "existing-run", "existing-session"
	for _, mode := range []string{"fusion", "vibe"} {
		if err := s.ExecuteAction(ctx, Action{Kind: ActionSetWorkflowMode, Target: mode}); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.Load(s.configPath, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Agents.Workflow != mode || loaded.Agents.Fusion != cfg.Agents.Fusion || s.WorkflowMode() != mode || s.modelRoutesEvent("listed").Data["workflow_mode"] != mode {
			t.Fatalf("wrong workflow configuration: %#v", loaded.Agents)
		}
	}
	if s.activeRun != "existing-run" || s.activeSession != "existing-session" {
		t.Fatal("workflow setting changed the active run")
	}
	before, _ := os.ReadFile(s.configPath)
	for _, mode := range []string{"", "single", "team", "unknown"} {
		if err := s.setWorkflowMode(ctx, mode); err == nil {
			t.Fatalf("accepted %q", mode)
		}
	}
	s.cfg.Agents.Fusion = config.ModelRouteConfig{}
	if err := s.setWorkflowMode(ctx, "fusion"); err == nil {
		t.Fatal("accepted unconfigured Fusion")
	}
	after, _ := os.ReadFile(s.configPath)
	if string(before) != string(after) || s.WorkflowMode() != "vibe" {
		t.Fatal("failed setting changed saved selection")
	}
}

func TestWorkflowInstructionsAreExclusiveAndStable(t *testing.T) {
	for _, mode := range []string{"single", "vibe", "fusion"} {
		instructions, fingerprint := turnInstructionsWithProject(false, "project", mode)
		_, again := turnInstructionsWithProject(false, "project", mode)
		if fingerprint != again || strings.Contains(instructions, vibeInstructions) != (mode == "vibe") || strings.Contains(instructions, fusionInstructions) != (mode == "fusion") {
			t.Fatalf("wrong %s instructions", mode)
		}
	}
	request := normalizeTurnRequest(TurnRequest{VibeMode: true, AgentMode: "single"}, config.Default().Defaults)
	if request.AgentMode != "vibe" || !request.VibeMode {
		t.Fatal("legacy Vibe request was not normalized")
	}
}
