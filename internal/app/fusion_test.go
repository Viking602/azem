package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

func TestFusionHandoffAppendsDeadlineWithoutRewritingHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	previousDeadline := message.NewText(message.RoleSystem, "[Trusted runtime deadline]\nEarlier deadline notice")
	markPrivateMessage(&previousDeadline)
	history := []message.Message{message.NewText(message.RoleSystem, "stable instructions"), previousDeadline, message.NewText(message.RoleUser, "first task"), message.NewText(message.RoleAssistant, "first result")}
	encoded, err := agentruntime.MarshalMessages(history)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := fusionResumeSeed(ctx, encoded, subagentParentRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	built, err := (subagentTurnContext{instructions: "stable instructions", seed: seed}).Build(ctx, hyagent.Request{Prompt: "next task"})
	if err != nil {
		t.Fatal(err)
	}
	if len(built) != len(history)+2 {
		t.Fatalf("history/deadline/task length = %d", len(built))
	}
	for i, old := range history {
		if built[i].Role != old.Role || built[i].Text != old.Text {
			t.Fatalf("handoff rewrote cached prefix at message %d", i)
		}
	}
	if !strings.Contains(built[len(history)].Text, "[Trusted runtime deadline]") || agentruntime.MessageVisibilityOf(built[len(history)]) != agentruntime.MessageVisibilityPrivate {
		t.Fatal("current deadline missing from private handoff tail")
	}
}

func TestFusionRetainsToolsAcrossHandoffsAndRootTurns(t *testing.T) {
	var mainCalls, childCalls atomic.Int32
	var keys sync.Map
	var previousChild map[string]any
	var previousSingle map[string]any
	writeChildCall := func(writer http.ResponseWriter, id, name, args string) {
		item := map[string]any{"type": "function_call", "id": id + "-item", "call_id": id, "name": name, "arguments": args}
		for _, event := range []any{
			map[string]any{"type": "response.output_item.done", "item": item},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "output": []any{item}}},
		} {
			encoded, _ := json.Marshal(event)
			fmt.Fprintf(writer, "data: %s\n\n", encoded)
		}
	}
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(_ int, body string, writer http.ResponseWriter) {
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Error(err)
		}
		key, _ := payload["prompt_cache_key"].(string)
		if key == "fusion-e2e" {
			step := mainCalls.Add(1)
			switch step {
			case 1, 3:
				if !strings.Contains(body, "Host: Fusion mode") || !strings.Contains(body, `"name":"sidekick"`) || strings.Contains(body, `"name":"coding_write_file"`) || strings.Contains(body, `"name":"coding_shell"`) || strings.Contains(body, `"name":"subagent_spawn"`) {
					t.Error("Fusion lead tool boundary is wrong")
				}
				writeProviderToolCall(writer, fmt.Sprintf("lead-call-%d", step), fmt.Sprintf("handoff-%d", step), "sidekick", `{"prompt":"Inspect fusion-evidence.txt and report the exact evidence."}`)
			case 2, 4:
				if !strings.Contains(body, "SIDEKICK_DONE") {
					t.Error("Sidekick output missing from lead")
				}
				writeProviderText(writer, "lead-final", "Lead verified SIDEKICK_DONE.")
			case 5, 6:
				if strings.Contains(body, "Host: Fusion mode") || strings.Contains(body, `"name":"sidekick"`) {
					t.Error("disabled Fusion retained its instructions or Sidekick tool")
				}
				if !strings.Contains(body, `"name":"coding_shell"`) || !strings.Contains(body, "Lead verified SIDEKICK_DONE.") {
					t.Error("disabling Fusion lost ordinary tools or conversation history")
				}
				if step == 6 {
					before, after := previousSingle["input"].([]any), payload["input"].([]any)
					if !reflect.DeepEqual(previousSingle["instructions"], payload["instructions"]) || !reflect.DeepEqual(previousSingle["tools"], payload["tools"]) || len(after) < len(before) || !reflect.DeepEqual(before, after[:len(before)]) {
						t.Error("ordinary turns did not retain their cached prefix after disabling Fusion")
					}
				}
				previousSingle = payload
				writeProviderText(writer, "single-final", "SINGLE_DONE")
			default:
				t.Error("unexpected lead call")
				writeProviderText(writer, "extra", "unexpected")
			}
			return
		}
		keys.Store(key, true)
		if strings.Contains(body, `"name":"subagent_spawn"`) {
			t.Error("Sidekick can delegate")
		}
		step := childCalls.Add(1)
		if step == 4 {
			for _, field := range []string{"instructions", "tools"} {
				if !reflect.DeepEqual(previousChild[field], payload[field]) {
					t.Errorf("handoff rewrote cached %s", field)
					if field == "instructions" {
						before, after := previousChild[field].(string), payload[field].(string)
						for i := 0; i < min(len(before), len(after)); i++ {
							if before[i] != after[i] {
								t.Logf("instruction difference at %d: before=%q after=%q", i, before[i:min(i+200, len(before))], after[i:min(i+200, len(after))])
								break
							}
						}
					}
				}
			}
			before, after := previousChild["input"].([]any), payload["input"].([]any)
			if len(after) < len(before) || !reflect.DeepEqual(before, after[:len(before)]) {
				t.Error("handoff did not preserve the complete cached input prefix")
				for i := 0; i < min(len(before), len(after)); i++ {
					if !reflect.DeepEqual(before[i], after[i]) {
						t.Errorf("handoff changed cached input item %d: before=%v after=%v", i, before[i], after[i])
						break
					}
				}
			}
		}
		previousChild = payload
		switch step {
		case 1:
			writeChildCall(writer, "activate-demo", "hydaelyn_activate_skill", `{"name":"demo"}`)
		case 2:
			writeChildCall(writer, "evidence-call", "coding_read_file", `{"path":"fusion-evidence.txt"}`)
		case 3, 4:
			if !strings.Contains(body, "FUSION_FULL_TOOL_HISTORY") || !strings.Contains(body, `"call_id":"evidence-call"`) || !strings.Contains(body, `"type":"function_call_output"`) {
				t.Error("Sidekick lost complete tool call/result history")
			}
			writeProviderText(writer, "child-final", "SIDEKICK_DONE")
		default:
			t.Error("unexpected Sidekick call")
			writeProviderText(writer, "extra", "unexpected")
		}
	})
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(harness.workspace, alias); err != nil {
		t.Fatal(err)
	}
	harness.service.cfg.Workspace.Root = alias
	harness.service.providers.cfg.Workspace.Root = alias
	if err := os.WriteFile(filepath.Join(harness.service.cfg.Workspace.Root, "fusion-evidence.txt"), []byte("FUSION_FULL_TOOL_HISTORY"), 0600); err != nil {
		t.Fatal(err)
	}
	route := config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal"}
	harness.service.cfg.Agents.Fusion = route
	harness.service.providers.cfg.Agents.Fusion = route
	subagentStore, err := agentservice.NewSQLSubagentRunStore(harness.store.DB(), harness.store.Blobs())
	if err != nil {
		t.Fatal(err)
	}
	harness.service.providers.Attach(harness.service, nil, subagentStore)
	liveTools := make(map[string]Event)
	for range 2 {
		runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "fusion-e2e", Prompt: "Inspect the evidence using Fusion", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "fusion"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		for {
			event, err := harness.service.NextEvent(ctx)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			if event.Kind == EventAgentState || event.AgentID != "" {
				t.Fatalf("Fusion lifecycle leaked: kind=%s name=%s", event.Kind, event.Data["name"])
			}
			if event.Kind == EventToolFinished {
				if event.RunID != runID || (event.Data["name"] != "sidekick" && !strings.HasPrefix(event.ToolCallID, "fusion:")) {
					t.Fatal("Fusion tool did not join the main run")
				}
				liveTools[event.ToolCallID] = event
			}
			if event.Kind == EventRunFailed || event.Kind == EventRunCancelled {
				t.Fatalf("Fusion run failed: %s", event.Text)
			}
			if event.Kind == EventRunFinished && event.RunID == runID {
				break
			}
		}
		cancel()
	}
	if mainCalls.Load() != 4 || childCalls.Load() != 4 {
		t.Fatalf("lead/child calls = %d/%d", mainCalls.Load(), childCalls.Load())
	}
	keyCount := 0
	keys.Range(func(key, _ any) bool {
		keyCount++
		if !strings.HasPrefix(key.(string), "fusion-sidekick-") {
			t.Errorf("unstable cache key: %v", key)
		}
		return true
	})
	if keyCount != 1 {
		t.Fatalf("Sidekick cache identities = %d", keyCount)
	}
	runs, err := subagentStore.List(context.Background(), "fusion-e2e")
	if err != nil || len(runs) != 2 {
		t.Fatalf("Sidekick history = %d, %v", len(runs), err)
	}
	for _, run := range runs {
		if run.State != agentservice.SubagentCompleted || run.Background {
			t.Fatalf("handoff did not complete in foreground: %#v", run)
		}
	}
	projection, err := harness.service.RuntimeProjection(context.Background(), "fusion-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Session.AgentSnapshots) != 0 {
		t.Fatal("Fusion execution leaked into the subagent roster")
	}
	reads := 0
	for _, record := range projection.Session.ToolRecords {
		if live, ok := liveTools[record.ToolCallID]; !ok || live.Text != record.Content || live.RunID != record.RunID {
			t.Fatalf("restored tool differs from its live event: %s", record.Name)
		}
		if record.Name == agentservice.ToolReadFile {
			reads++
			if record.State != session.ToolCompleted || !strings.Contains(record.Content, "FUSION_FULL_TOOL_HISTORY") {
				t.Fatal("inline Fusion tool lost its actual result")
			}
		}
	}
	if reads != 1 {
		t.Fatalf("inline Fusion reads = %d, want one across both handoffs", reads)
	}
	if len(liveTools) != 4 || len(projection.Session.ToolRecords) != 4 {
		t.Fatal("Fusion tool count changed during restore")
	}
	reports := 0
	for _, block := range projection.Session.Blocks {
		if block.Data["fusionRole"] == "sidekick" && block.Content == "SIDEKICK_DONE" {
			reports++
			if block.TextPhase != "commentary" {
				t.Fatal("Sidekick report became the lead final")
			}
		}
	}
	if reports != 2 {
		t.Fatalf("restored reports = %d, want one per handoff", reports)
	}
	selected, err := harness.service.SessionProjection(context.Background(), "fusion-e2e")
	if err != nil || len(selected.AgentSnapshots) != 0 {
		t.Fatalf("selection differs from reconnect: %v", err)
	}
	var selectedTools []session.ToolRecord
	if err := json.Unmarshal([]byte(selected.Data["toolRecords"]), &selectedTools); err != nil {
		t.Fatalf("selection tools differ from reconnect: %v", err)
	}
	selectionJSON, _ := json.Marshal(selectedTools)
	reconnectJSON, _ := json.Marshal(projection.Session.ToolRecords)
	if string(selectionJSON) != string(reconnectJSON) {
		t.Fatal("selection tools differ from reconnect")
	}
	legacy, err := harness.service.sessions.LoadDisplayProjection(context.Background(), "fusion-e2e")
	if err != nil {
		t.Fatal(err)
	}
	legacy.ToolRecords = rootToolRecords(legacy)
	if _, err := harness.service.projectFusionSession(context.Background(), &legacy); err != nil || len(legacy.ToolRecords) != 4 {
		t.Fatalf("legacy transcript replay duplicated or lost Fusion tools: count=%d error=%v", len(legacy.ToolRecords), err)
	}
	saved, err := harness.service.sessions.LoadSession(context.Background(), "fusion-e2e")
	if err != nil || saved.AgentMode != "fusion" {
		t.Fatalf("Fusion mode was not persisted: %v", err)
	}
	if err := harness.service.setSessionMode(context.Background(), saved.ID, "single"); err != nil {
		t.Fatal(err)
	}
	saved, _ = harness.service.sessions.LoadSession(context.Background(), saved.ID)
	if saved.AgentMode != "single" || saved.ModelID != "gpt-skill" {
		t.Fatal("mode toggle lost session preferences")
	}
	t.Run("disabled", func(t *testing.T) {
		// An unavailable Sidekick must not be resolved after the mode is disabled.
		route = config.ModelRouteConfig{Provider: "unavailable", Model: "unavailable"}
		harness.service.cfg.Agents.Fusion = route
		harness.service.providers.UpdateModelRoute("fusion", "", route)
		for range 2 {
			runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: saved.ID, Prompt: "Continue directly", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
			if err != nil {
				t.Fatal(err)
			}
			waitForProviderRun(t, harness.service, runID)
		}
		if mainCalls.Load() != 6 || childCalls.Load() != 4 {
			t.Fatalf("disabled Fusion ran an unexpected lead/child call: %d/%d", mainCalls.Load(), childCalls.Load())
		}
	})
}

func TestSingleModeRebuildsLegacyFusionCheckpoint(t *testing.T) {
	for _, policy := range []string{fusionInstructions, vibeInstructions} {
		boundary := int64(2)
		legacy := message.NewText(message.RoleSystem, "[Trusted private hook context]\n"+policy)
		markPrivateMessage(&legacy)
		manager := turnContext{
			instructions: mainInstructions, instructionFingerprint: mainInstructionFingerprint,
			providerID: "chatgpt", modelID: "model", checkpointBoundary: &boundary,
			history: []session.Block{{Sequence: 1, Kind: "user", Content: "old request"}, {Sequence: 2, Kind: "assistant", Content: "old answer"}},
			modelHistory: session.ModelHistory{
				ProviderID: "chatgpt", ModelID: "model", InstructionFingerprint: mainInstructionFingerprint,
				StaticPrefixHash: mainInstructionFingerprint, WireVersion: session.CurrentWireVersion, CoveredThroughSequence: &boundary,
				Messages: []message.Message{message.NewText(message.RoleSystem, mainInstructions), legacy, message.NewText(message.RoleUser, "old request"), message.NewText(message.RoleAssistant, "old answer")},
			},
		}
		built, err := manager.Build(context.Background(), hyagent.Request{Prompt: "continue directly"})
		if err != nil {
			t.Fatal(err)
		}
		if len(built) != 4 || built[1].Text != "old request" || built[2].Text != "old answer" || built[3].Text != "continue directly" {
			t.Fatal("legacy Fusion policy was replayed or conversation history was lost")
		}
	}
}

func TestFusionForbiddenHandoffSettlesWithoutReconciliation(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var mainCalls, childCalls atomic.Int32
			harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: fixture\n---\nfixture\n", nil, func(_ int, body string, writer http.ResponseWriter) {
				var payload struct {
					Key string `json:"prompt_cache_key"`
				}
				if err := json.Unmarshal([]byte(body), &payload); err != nil {
					t.Error(err)
				}
				if payload.Key != "fusion-forbidden" {
					childCalls.Add(1)
					writer.WriteHeader(status)
					fmt.Fprint(writer, `{"error":{"code":"permission_denied","message":"fixture request rejected"}}`)
					return
				}
				if mainCalls.Add(1) == 1 {
					writeProviderToolCall(writer, "lead-call", "handoff", "sidekick", `{"prompt":"Inspect the workspace."}`)
				} else {
					if !strings.Contains(body, "fixture request rejected") {
						t.Error("lead lost the rejection detail")
					}
					writeProviderText(writer, "lead-final", "Sidekick was rejected; no work was performed.")
				}
			})
			route := config.ModelRouteConfig{Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal"}
			harness.service.cfg.Agents.Fusion, harness.service.providers.cfg.Agents.Fusion = route, route
			subagentStore, err := agentservice.NewSQLSubagentRunStore(harness.store.DB(), harness.store.Blobs())
			if err != nil {
				t.Fatal(err)
			}
			harness.service.providers.Attach(harness.service, nil, subagentStore)
			runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "fusion-forbidden", Prompt: "Inspect with Fusion", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "fusion"})
			if err != nil {
				t.Fatal(err)
			}
			waitForProviderRun(t, harness.service, runID)
			runs, err := subagentStore.List(context.Background(), "fusion-forbidden")
			if err != nil || len(runs) != 1 {
				t.Fatalf("children=%d err=%v", len(runs), err)
			}
			if runs[0].State != agentservice.SubagentFailed || childCalls.Load() != 1 || mainCalls.Load() != 2 {
				t.Fatalf("child state=%s calls=%d/%d", runs[0].State, mainCalls.Load(), childCalls.Load())
			}
			var unknown, failed int
			if err := harness.store.DB().QueryRow(`SELECT count(*) FROM agent_effect_attempts WHERE status='unknown'`).Scan(&unknown); err != nil {
				t.Fatal(err)
			}
			if err := harness.store.DB().QueryRow(`SELECT count(*) FROM agent_effect_attempts WHERE kind='model' AND status='failed'`).Scan(&failed); err != nil {
				t.Fatal(err)
			}
			if unknown != 0 || failed != 1 {
				t.Fatalf("unknown attempts=%d failed model attempts=%d", unknown, failed)
			}
		})
	}
}

func TestFusionPinsCrossProviderIdentityAndCancelsForeground(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, provider, coding, store := newGatedForegroundHarness(t, ctx, time.Millisecond)
	defer runtime.Shutdown(context.Background())
	defer coding.Close(context.Background())
	account := "sidekick-account"
	parent := subagentParentRuntime{SessionID: "session", ParentRunID: "lead", ProviderID: "lead-provider", AccountID: "lead-account", ModelID: "lead-model", Driver: provider, Coding: coding, WorkspaceRoot: t.TempDir(),
		ResolveAccountDriver: func(_ context.Context, p, m, reasoning, requestedAccount string) (string, string, int, hyprovider.Driver, error) {
			if p != "sidekick-provider" || m != "sidekick-model" || reasoning != "" || (requestedAccount != "" && requestedAccount != account) {
				t.Error("Sidekick inherited the lead route")
			}
			return account, m, 200000, provider, nil
		}}
	route := config.ModelRouteConfig{Provider: "sidekick-provider", Model: "sidekick-model"}
	d, err := newFusionDriver(ctx, runtime, parent, route)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Drivers(d.parent); err != nil {
		t.Fatal(err)
	}
	account = "another-account"
	other, err := newFusionDriver(ctx, runtime, parent, route)
	if err != nil || other.parent.PersistentKey == d.parent.PersistentKey {
		t.Fatal("account contexts are not isolated")
	}
	account = "sidekick-account"
	parent.SessionID = "another-session"
	other, err = newFusionDriver(ctx, runtime, parent, route)
	if err != nil || other.parent.PersistentKey == d.parent.PersistentKey {
		t.Fatal("session contexts are not isolated")
	}
	callCtx, stop := context.WithCancel(ctx)
	returned := make(chan tool.Result, 1)
	go func() {
		result, _ := d.Execute(callCtx, tool.Call{ID: "handoff", Name: "sidekick", Arguments: json.RawMessage(`{"prompt":"inspect"}`)}, nil)
		returned <- result
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("Sidekick never started")
	}
	stop()
	select {
	case result := <-returned:
		if !result.IsError {
			t.Fatal("cancelled handoff reported success")
		}
	case <-ctx.Done():
		t.Fatal("handoff did not cancel")
	}
	for {
		runs, err := store.List(ctx, "session")
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) == 1 && subagentTerminal(runs[0].State) {
			if runs[0].State != agentservice.SubagentCancelled {
				t.Fatalf("state = %s", runs[0].State)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Sidekick kept running after cancellation")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestFusionRejectsMissingRouteAndConflictingModes(t *testing.T) {
	s := NewService(context.Background(), config.Default())
	if err := s.validateFusion(TurnRequest{}); err == nil {
		t.Fatal("missing route accepted")
	}
	s.cfg.Agents.Fusion = config.ModelRouteConfig{Provider: "provider", Model: "model"}
	for _, request := range []TurnRequest{{PlanMode: true}, {VibeMode: true}, {DisableSubagents: true}, {Prewalk: &config.ModelRouteConfig{}}, {PlanYolo: &config.ModelRouteConfig{}}} {
		if err := s.validateFusion(request); err == nil {
			t.Fatalf("conflicting mode accepted: %#v", request)
		}
	}
	s.cfg.Agents.Subagents.MaxDepth = 0
	if err := s.validateFusion(TurnRequest{}); err == nil {
		t.Fatal("Fusion bypassed disabled delegation depth")
	}
}
