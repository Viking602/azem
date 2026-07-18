//go:build live

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/catalog"
)

func TestLiveSubscriptionReadTurns(t *testing.T) {
	if os.Getenv("AZEM_LIVE_ACCEPTANCE") != "1" {
		t.Skip("set AZEM_LIVE_ACCEPTANCE=1 to use local subscription credentials")
	}
	ctx, boot, _ := liveBootstrap(t)

	for _, providerID := range []string{"chatgpt", "grok"} {
		t.Run(providerID, func(t *testing.T) {
			account, model, reasoning := liveProviderSelection(t, ctx, boot.Service, providerID)
			marker := "AZEM_LIVE_" + strings.ToUpper(providerID) + "_OK"
			runID, err := boot.Service.StartConfiguredTurn(TurnRequest{
				SessionID: "default", Prompt: "Reply with exactly " + marker + ". Do not call any tools.",
				Provider: providerID, Model: model.ID, Reasoning: reasoning, AgentMode: "single",
			})
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			for {
				event, err := boot.Service.NextEvent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.RunID != runID {
					continue
				}
				switch event.Kind {
				case EventTextDelta:
					output.WriteString(event.Text)
				case EventApprovalRequested:
					t.Fatalf("read-only acceptance requested tool approval: %+v", event)
				case EventRunFailed:
					t.Fatalf("%s live turn failed: %s", providerID, event.Text)
				case EventRunFinished:
					if !strings.Contains(output.String(), marker) {
						t.Fatalf("%s live response=%q", providerID, output.String())
					}
					t.Logf("%s account %s model %s completed a subscription read turn", providerID, account.ID, model.ID)
					return
				}
			}
		})
	}
}

func TestLiveSubscriptionGovernedEdits(t *testing.T) {
	if os.Getenv("AZEM_LIVE_ACCEPTANCE") != "1" {
		t.Skip("set AZEM_LIVE_ACCEPTANCE=1 to use local subscription credentials")
	}
	ctx, boot, workspace := liveBootstrap(t)
	for _, providerID := range []string{"chatgpt", "grok"} {
		t.Run(providerID, func(t *testing.T) {
			account, model, reasoning := liveProviderSelection(t, ctx, boot.Service, providerID)
			filename := "live-" + providerID + ".txt"
			marker := "written by " + providerID + "\n"
			runID, err := boot.Service.StartConfiguredTurn(TurnRequest{
				SessionID: "default",
				Prompt:    fmt.Sprintf("Use the available write-file tool to create %s with exactly this content: %q. You must perform the tool call, then briefly confirm completion.", filename, marker),
				Provider:  providerID, Model: model.ID, Reasoning: reasoning, AgentMode: "single",
			})
			if err != nil {
				t.Fatal(err)
			}
			approvals := 0
			for {
				event, err := boot.Service.NextEvent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.RunID != runID {
					continue
				}
				switch event.Kind {
				case EventApprovalRequested:
					approvals++
					if err := boot.Service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
						t.Fatal(err)
					}
				case EventRunFailed:
					t.Fatalf("%s governed edit failed: %s", providerID, event.Text)
				case EventRunFinished:
					if approvals == 0 {
						t.Fatalf("%s edit completed without a governed approval", providerID)
					}
					content, err := os.ReadFile(filepath.Join(workspace, filename))
					if err != nil {
						t.Fatal(err)
					}
					if string(content) != marker {
						t.Fatalf("%s file content=%q", providerID, content)
					}
					t.Logf("%s account %s model %s completed an approved edit", providerID, account.ID, model.ID)
					return
				}
			}
		})
	}
}

func TestLiveSubagentCompletionAndCancellation(t *testing.T) {
	if os.Getenv("AZEM_LIVE_ACCEPTANCE") != "1" {
		t.Skip("set AZEM_LIVE_ACCEPTANCE=1 to use local subscription credentials")
	}
	ctx, boot, _ := liveBootstrap(t)
	account, model, reasoning := liveProviderSelection(t, ctx, boot.Service, "chatgpt")
	runID, err := boot.Service.StartConfiguredTurn(TurnRequest{
		SessionID: "default",
		Prompt:    "You must use the available explore subagent tool exactly once to inspect the empty workspace. After it completes, report its finding.",
		Provider:  "chatgpt", Model: model.ID, Reasoning: reasoning, AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	var succeededChild string
	for {
		event, err := boot.Service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			continue
		}
		if event.Kind == EventAgentState && event.Agent != nil && event.Agent.Type == "explore" && event.State == "completed" {
			succeededChild = event.AgentID
		}
		if event.Kind == EventRunFailed {
			t.Fatalf("subagent completion turn failed: %s", event.Text)
		}
		if event.Kind == EventRunFinished {
			if succeededChild == "" {
				t.Fatal("explore subagent never reached completed")
			}
			break
		}
	}

	cancelRunID, err := boot.Service.StartConfiguredTurn(TurnRequest{
		SessionID: "default",
		Prompt:    "Call subagent.spawn exactly once with subagent_type \"explore\" and background true to perform a detailed workspace investigation. After spawning it, call subagent.get_output for that task.",
		Provider:  "chatgpt", Model: model.ID, Reasoning: reasoning, AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	var cancelledChild string
	cancelRequested := false
	for cancelledChild == "" {
		event, err := boot.Service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != cancelRunID {
			continue
		}
		switch event.Kind {
		case EventAgentState:
			if event.Agent == nil || event.Agent.Type != "explore" {
				continue
			}
			switch event.State {
			case "initializing", "queued", "running":
				if !cancelRequested {
					cancelRequested = true
					if err := boot.Service.ExecuteAction(ctx, Action{Kind: ActionCancelAgent, SessionID: "default", Target: event.AgentID}); err != nil {
						t.Fatal(err)
					}
				}
			case "cancelled":
				cancelledChild = event.AgentID
			case "completed", "failed":
				t.Fatalf("child reached %s before cancellation", event.State)
			}
		case EventRunFailed:
			t.Fatalf("subagent cancellation turn failed: %s", event.Text)
		case EventRunFinished:
			t.Fatal("subagent cancellation turn finished without a cancellable child")
		}
	}
	if !boot.Service.CancelActive() {
		t.Fatal("parent run was not active after child cancellation")
	}
	for {
		event, err := boot.Service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID == cancelRunID && event.Kind == EventRunCancelled {
			break
		}
	}
	t.Logf("ChatGPT account %s model %s completed child %s and cancelled child %s", account.ID, model.ID, succeededChild, cancelledChild)
}

func TestLiveSubscriptionCodingTeam(t *testing.T) {
	if os.Getenv("AZEM_LIVE_ACCEPTANCE") != "1" {
		t.Skip("set AZEM_LIVE_ACCEPTANCE=1 to use local subscription credentials")
	}
	ctx, boot, workspace := liveBootstrap(t)
	account, model, reasoning := liveProviderSelection(t, ctx, boot.Service, "chatgpt")
	const filename = "live-team.txt"
	const marker = "written by the coding team\n"
	runID, err := boot.Service.StartConfiguredTurn(TurnRequest{
		SessionID: "default",
		Prompt:    fmt.Sprintf("Create %s with exactly this content: %q. Plan, implement with the available write-file tool, review the resulting file, and report completion.", filename, marker),
		Provider:  "chatgpt", Model: model.ID, Reasoning: reasoning, AgentMode: "team",
	})
	if err != nil {
		t.Fatal(err)
	}
	roles := make(map[string]bool)
	approvals := 0
	for {
		event, err := boot.Service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID {
			continue
		}
		switch event.Kind {
		case EventAgentState:
			if role := event.Data["role"]; role != "" {
				roles[role] = true
			}
		case EventApprovalRequested:
			approvals++
			if err := boot.Service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
				t.Fatal(err)
			}
		case EventRunFailed:
			t.Fatalf("live coding team failed: %s", event.Text)
		case EventRunFinished:
			for _, role := range []string{"planner", "implementer", "reviewer", "reporter"} {
				if !roles[role] {
					t.Fatalf("coding team never reported %s state: %+v", role, roles)
				}
			}
			if approvals == 0 {
				t.Fatal("coding team write did not request approval")
			}
			content, err := os.ReadFile(filepath.Join(workspace, filename))
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != marker {
				t.Fatalf("team file content=%q", content)
			}
			t.Logf("ChatGPT account %s model %s completed planner -> implementer -> reviewer -> reporter", account.ID, model.ID)
			return
		}
	}
}

func liveBootstrap(t *testing.T) (context.Context, BootstrapResult, string) {
	t.Helper()
	workspace := t.TempDir()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("CODEX_HOME", filepath.Join(mustUserHome(t), ".codex"))
	configPath := filepath.Join(root, "config", "azem", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	configBody := fmt.Sprintf(`version: 1
defaults:
  provider: chatgpt
  model: gpt-5.4
  reasoning: minimal
  agent_mode: single
  theme: system
workspace:
  root: %s
  allow_write: true
  shell_policy: prompt
  allow_network: prompt
auth:
  store: file
  import_codex: true
  import_grok: true
providers:
  chatgpt:
    enabled: true
    catalog_ttl: 5m
  grok:
    enabled: true
    catalog_ttl: 5m
    experimental_oauth: true
    transport: api
agents:
  team:
    max_concurrency: 2
    max_ticks: 12
  subagents:
    max_depth: 1
    max_concurrency: 2
mcp:
  servers: {}
`, workspace)
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	boot, err := Bootstrap(ctx, workspace, configPath)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := boot.Service.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return ctx, boot, workspace
}

func liveProviderSelection(t *testing.T, ctx context.Context, service *Service, providerID string) (auth.Account, catalog.Model, string) {
	t.Helper()
	accounts, err := service.Authentication().Accounts(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) == 0 {
		t.Fatalf("no imported %s subscription account", providerID)
	}
	result, err := service.Catalog().List(ctx, providerID, accounts[0].ID, true)
	for attempt := 1; err != nil && attempt < 3; attempt++ {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
		}
		result, err = service.Catalog().List(ctx, providerID, accounts[0].ID, true)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) == 0 {
		t.Fatalf("%s catalog is empty", providerID)
	}
	selected := result.Models[0]
	for _, model := range result.Models {
		if model.SupportsTools {
			selected = model
		}
		if providerID == "chatgpt" && model.ID == "gpt-5.4" {
			selected = model
			break
		}
	}
	reasoning := selected.DefaultReasoning
	if reasoning == "" && len(selected.ReasoningLevels) > 0 {
		reasoning = selected.ReasoningLevels[0]
	}
	if reasoning == "" {
		reasoning = "minimal"
	}
	return accounts[0], selected, reasoning
}

func mustUserHome(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return home
}
