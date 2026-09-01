package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

func TestTTSRMatchesCrossDeltaTextPersistsAndInjectsOnce(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	coding, err := agentservice.NewService(provider, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "ttsr", Title: "TTSR"}); err != nil {
		t.Fatal(err)
	}
	control := newTurnControlQueue()
	hook, err := newTTSRHook(config.TTSRConfig{
		Enabled: true, ContextMode: "discard", InterruptMode: "always", RepeatMode: "once", RepeatGap: 10,
		Rules: []config.StreamRuleConfig{{Name: "forbidden-call", Content: "Use the safe call instead.", Conditions: []string{`forbidden\s+call`}, Scope: []string{"text"}}},
	}, coding, sessions, "ttsr", "run", control)
	if err != nil {
		t.Fatal(err)
	}
	if err := hook.OnEvent(ctx, hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: "forbidden "}); err != nil {
		t.Fatal(err)
	}
	if matchErr := hook.OnEvent(ctx, hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: "call"}); matchErr != nil {
		t.Fatalf("TTSR queue = %#v", matchErr)
	}
	controls, err := control.Drain(ctx, turnControlBeforeModel)
	if err != nil || len(controls) != 1 || controls[0].Kind != turnControlSteer || !isPrivateMessage(controls[0].Message) ||
		!strings.Contains(controls[0].Message.Text, "Use the safe call instead") {
		t.Fatalf("TTSR control = %#v, %v", controls, err)
	}
	artifact, err := sessions.LoadLatestArtifactByKind(ctx, "ttsr", ttsrArtifactKind)
	if err != nil || !strings.Contains(string(artifact.Payload), "forbidden-call") {
		t.Fatalf("TTSR artifact = %#v, %v", artifact, err)
	}
	if err := hook.OnEvent(ctx, hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: " forbidden call again"}); err != nil {
		t.Fatalf("once-only rule repeated: %v", err)
	}
}

func TestTTSRScopesDeferredToolRulesAndASTSnapshots(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	coding, err := agentservice.NewService(provider, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Close(ctx)
	control := newTurnControlQueue()
	hook, err := newTTSRHook(config.TTSRConfig{
		Enabled: true, ContextMode: "keep", InterruptMode: "always", RepeatMode: "once", RepeatGap: 10,
		Rules: []config.StreamRuleConfig{
			{Name: "deferred-ts", Content: "Remove the dangerous marker.", Conditions: []string{"dangerous"}, Scope: []string{"tool:coding.write_file(*.ts)"}, InterruptMode: "never"},
			{Name: "no-console", Content: "Use the project logger.", ASTConditions: []string{"console.log($MSG)"}, Scope: []string{"tool:coding.write_file(*.ts)"}},
		},
	}, coding, nil, "ttsr", "run", control)
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(map[string]any{"path": "main.ts", "content": "const dangerous = true;"})
	if err := hook.OnEvent(ctx, hyprovider.Event{Kind: hyprovider.EventToolCall, ToolCall: &message.ToolCall{ID: "write-1", Name: "coding.write_file", Arguments: arguments}}); err != nil {
		t.Fatal(err)
	}
	if controls, err := control.Drain(ctx, turnControlBeforeModel); err != nil || len(controls) != 0 {
		t.Fatalf("deferred rule interrupted tools: %#v, %v", controls, err)
	}
	deferred, err := control.Drain(ctx, turnControlAfterAnswer)
	if err != nil || len(deferred) != 1 || deferred[0].Kind != turnControlFollowUp {
		t.Fatalf("deferred TTSR control = %#v, %v", deferred, err)
	}

	astControl := newTurnControlQueue()
	astHook, err := newTTSRHook(config.TTSRConfig{
		Enabled: true, ContextMode: "keep", InterruptMode: "tool-only", RepeatMode: "once", RepeatGap: 10,
		Rules: []config.StreamRuleConfig{{Name: "no-console", Content: "Use the project logger.", ASTConditions: []string{"console.log($MSG)"}, Scope: []string{"tool:coding.write_file(*.ts)"}}},
	}, coding, nil, "ttsr", "run", astControl)
	if err != nil {
		t.Fatal(err)
	}
	matched, astErr := coding.MatchASTSnapshot(ctx, `console.log("unsafe")`, "ts", []string{"console.log($MSG)"})
	if astErr != nil {
		t.Skip(astErr)
	}
	if !matched {
		t.Fatal("AST fixture did not match")
	}
	arguments, _ = json.Marshal(map[string]any{"path": "main.ts", "content": `console.log("unsafe")`})
	if matchErr := astHook.OnEvent(ctx, hyprovider.Event{Kind: hyprovider.EventToolCall, ToolCall: &message.ToolCall{ID: "write-2", Name: "coding.write_file", Arguments: arguments}}); matchErr != nil {
		t.Fatalf("AST TTSR queue = %#v", matchErr)
	}
	steers, err := astControl.Drain(ctx, turnControlBeforeModel)
	if err != nil || len(steers) != 1 || steers[0].Kind != turnControlSteer {
		t.Fatalf("AST TTSR control = %#v, %v", steers, err)
	}
}

func TestProviderRuntimeTTSRInterruptsAndRegenerates(t *testing.T) {
	ctx := context.Background()
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			writeProviderText(writer, "ttsr-primary-1", "FORBIDDEN_GENERATION")
		case 2:
			if !strings.Contains(body, "Use SAFE_GENERATION instead") || !strings.Contains(body, "stream-rule") {
				t.Errorf("TTSR injection missing from retry: %s", body)
			}
			if strings.Contains(body, "FORBIDDEN_GENERATION") {
				t.Errorf("discard-mode partial output leaked into retry: %s", body)
			}
			writeProviderText(writer, "ttsr-primary-2", "SAFE_GENERATION")
		default:
			t.Errorf("unexpected TTSR provider call %d", call)
			writeProviderText(writer, "ttsr-extra", "unexpected")
		}
	})
	harness.service.providers.mu.Lock()
	harness.service.providers.cfg.TTSR = config.TTSRConfig{
		Enabled: true, ContextMode: "discard", InterruptMode: "always", RepeatMode: "once", RepeatGap: 10,
		Rules: []config.StreamRuleConfig{{Name: "safe-generation", Content: "Use SAFE_GENERATION instead.", Conditions: []string{"FORBIDDEN_GENERATION"}, Scope: []string{"text"}}},
	}
	harness.service.providers.mu.Unlock()
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "ttsr-e2e", Prompt: "Generate safely", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if harness.calls.Load() != 2 {
		t.Fatalf("TTSR provider calls = %d, want 2", harness.calls.Load())
	}
	projection, err := harness.service.sessions.LoadProjection(ctx, "ttsr-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Blocks) == 0 || projection.Blocks[len(projection.Blocks)-1].Content != "SAFE_GENERATION" {
		t.Fatalf("TTSR final projection = %#v", projection.Blocks)
	}
	if _, err := harness.service.sessions.LoadLatestArtifactByKind(ctx, "ttsr-e2e", ttsrArtifactKind); err != nil {
		t.Fatalf("missing durable TTSR evidence: %v", err)
	}
}
