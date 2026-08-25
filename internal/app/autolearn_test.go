package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/skills"
)

func TestSuccessfulSubstantiveTurnRunsHiddenAutoLearnCapture(t *testing.T) {
	var harness skillRuntimeHarness
	harness = newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			writeProviderToolCall(writer, "autolearn-parent-1", "read", "coding.read_file", `{"path":"fixture.txt"}`)
		case 2:
			writeProviderText(writer, "autolearn-parent-2", "parent completed")
		case 3:
			var request struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if err := json.Unmarshal([]byte(body), &request); err != nil {
				t.Error(err)
			}
			toolNames := make(map[string]bool, len(request.Tools))
			for _, definition := range request.Tools {
				toolNames[definition.Name] = true
			}
			if !strings.Contains(body, autoLearnCapturePrompt) || !strings.Contains(body, `"prompt_cache_key":"autolearn:autolearn"`) ||
				!toolNames["manage_skill"] || toolNames["coding_read_file"] {
				t.Errorf("autolearn tool boundary is wrong: %v", toolNames)
			}
			writeProviderToolCall(writer, "autolearn-capture-1", "manage", "manage_skill", `{"action":"create","name":"fixture-recipe","description":"Use for repeated fixture investigations","body":"# Procedure\n\nRead fixture.txt before diagnosing."}`)
		case 4:
			if !strings.Contains(body, "Created managed skill") {
				t.Errorf("managed skill result missing: %s", body)
			}
			writeProviderText(writer, "autolearn-capture-2", "captured")
		default:
			t.Errorf("unexpected provider call %d", call)
			writeProviderText(writer, "autolearn-extra", "unexpected")
		}
	})
	if err := os.WriteFile(filepath.Join(harness.workspace, "fixture.txt"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	managedRoot := filepath.Join(t.TempDir(), "managed-skills")
	cfg := config.Default()
	catalog, err := skills.Load(skills.LoadOptions{HomeDir: t.TempDir(), ConfigDir: t.TempDir(), ManagedDir: managedRoot, Config: cfg.Skills, Discovery: cfg.Discovery})
	if err != nil {
		t.Fatal(err)
	}
	manager := skills.NewManagedSkillManager(managedRoot, catalog)
	if err := harness.coding.AttachManagedSkillTool(manager.Driver()); err != nil {
		t.Fatal(err)
	}
	harness.service.cfg.AutoLearn = config.AutoLearnConfig{Enabled: true, AutoContinue: true, MinToolCalls: 1}
	harness.service.providers.mu.Lock()
	harness.service.providers.cfg.AutoLearn = harness.service.cfg.AutoLearn
	harness.service.providers.mu.Unlock()
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "autolearn", Prompt: "inspect the fixture", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	deadline := time.Now().Add(5 * time.Second)
	managedFile := filepath.Join(managedRoot, "fixture-recipe", "SKILL.md")
	for time.Now().Before(deadline) {
		if payload, readErr := os.ReadFile(managedFile); readErr == nil && harness.calls.Load() >= 4 {
			if !strings.Contains(string(payload), "Read fixture.txt") {
				t.Fatalf("managed skill = %s", payload)
			}
			projection, loadErr := harness.service.sessions.LoadProjection(context.Background(), "autolearn")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			for _, block := range projection.Blocks {
				if strings.Contains(block.Content, autoLearnCapturePrompt) || strings.Contains(block.Content, "captured") {
					t.Fatalf("hidden autolearn capture leaked into transcript: %#v", block)
				}
			}
			historyJSON, _ := json.Marshal(projection.ModelHistory)
			if strings.Contains(string(historyJSON), autoLearnCapturePrompt) || strings.Contains(string(historyJSON), "captured") {
				t.Fatalf("autolearn capture replaced primary model history: %s", historyJSON)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("managed skill was not captured; provider calls=%d", harness.calls.Load())
}
