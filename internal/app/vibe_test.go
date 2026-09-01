package app

import (
	"context"
	"encoding/json"
	"net/http"
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
		SessionID: "vibe-e2e", Prompt: "Direct workers to inspect the task", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single", VibeMode: true,
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
