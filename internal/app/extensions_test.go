package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/customtools"
	"github.com/Viking602/azem/internal/extensions"
	llmuxdriver "github.com/Viking602/azem/internal/provider/llmux"
)

func TestExtensionProvidersAndAgentsMergeIntoRuntimeConfiguration(t *testing.T) {
	cfg := config.Default()
	providerConfig := json.RawMessage(`{
		"baseUrl":"https://example.test/v1","apiKey":"EXTENSION_API_KEY","api":"openai-completions",
		"headers":{"X-Extension":"enabled"},
		"models":[{"id":"extension-model","name":"Extension Model","reasoning":true,"input":["text","image"],"contextWindow":128000,"maxTokens":4096}]
	}`)
	diagnostics := applyExtensionProviders(&cfg, []customtools.ExtensionProvider{{Name: "extension-provider", Config: providerConfig, ModulePath: "/extension.ts"}})
	if len(diagnostics) != 0 {
		t.Fatalf("provider diagnostics = %v", diagnostics)
	}
	provider := cfg.Providers.LLMux["extension-provider"]
	if !provider.Enabled || provider.Backend != "openai-compatible" || provider.EnvKey != "EXTENSION_API_KEY" || len(provider.Models) != 1 || provider.RuntimeHeaders["X-Extension"] != "enabled" {
		t.Fatalf("provider config = %#v", provider)
	}
	profile, ok := llmuxdriver.LookupProfileWithConfig("extension-provider", cfg.Providers.LLMux)
	if !ok || profile.Backend != "openai-compatible" || profile.BaseURL != "https://example.test/v1" {
		t.Fatalf("provider profile = %#v, %v", profile, ok)
	}
	if _, err := llmuxdriver.New(llmuxdriver.Config{ProviderID: profile.ID, Backend: profile.Backend, BaseURL: profile.BaseURL, APIKey: "test", Models: []string{"extension-model"}}); err != nil {
		t.Fatal(err)
	}

	agentConfig := json.RawMessage(`{"description":"Extension reviewer","systemPrompt":"Review extension code.","model":"extension-provider/extension-model","thinkingLevel":"high","tools":["coding.read_file"]}`)
	diagnostics = mergeExtensionAgents(&cfg, []extensions.Agent{{Name: "reviewer", Description: "Native reviewer", SystemPrompt: "Review native code.", Source: "/.omp/agents/reviewer.md"}}, []customtools.ExtensionAgent{{Name: "extension-reviewer", Config: agentConfig, ModulePath: "/extension.ts"}})
	if len(diagnostics) != 0 {
		t.Fatalf("agent diagnostics = %v", diagnostics)
	}
	if cfg.Agents.Subagents.Roles["reviewer"].Instructions != "Review native code." || cfg.Agents.Subagents.Roles["extension-reviewer"].Provider != "extension-provider" ||
		cfg.Agents.Subagents.Roles["extension-reviewer"].Reasoning != "high" || cfg.Agents.Subagents.Roles["extension-reviewer"].Tools[0] != "coding.read_file" {
		t.Fatalf("roles = %#v", cfg.Agents.Subagents.Roles)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestThemeCatalogActionProjectsDiscoveredThemes(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	service.AttachThemes([]extensions.Theme{{Name: "custom-dark", Path: "/theme.json", Colors: map[string]any{"accent": "#fff"}, Source: "user"}}, []string{"one warning"})
	if err := service.ExecuteAction(context.Background(), Action{Kind: ActionListThemes}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(context.Background())
	if err != nil || event.Kind != EventThemeCatalog || !strings.Contains(event.Data["themes"], "custom-dark") || !strings.Contains(event.Data["diagnostics"], "one warning") {
		t.Fatalf("theme event = %#v, %v", event, err)
	}
}

func TestConfiguredTurnExecutesExtensionCommand(t *testing.T) {
	module := filepath.Join(t.TempDir(), "extension.ts")
	if err := os.WriteFile(module, []byte(`
export default (pi) => {
  pi.registerCommand("extcmd", {description:"Extension command", handler(args){return {prompt:"Extension says " + args}}});
};
`), 0o600); err != nil {
		t.Fatal(err)
	}
	harness := newSkillRuntimeHarness(t, "---\\nname: demo\\ndescription: stable catalog\\n---\\nstable body\\n", nil, func(_ int, body string, writer http.ResponseWriter) {
		if !strings.Contains(body, "Extension says hello") || strings.Contains(body, "/extcmd") {
			t.Errorf("extension command was not expanded: %s", body)
		}
		writeProviderText(writer, "extension-command", "handled")
	})
	host, err := customtools.NewWithExtensions(context.Background(), harness.workspace, nil, []string{module})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.coding.AttachExternalTools(nil, host.Close); err != nil {
		t.Fatal(err)
	}
	harness.service.AttachExtensionHost(host)
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "extension-command", Prompt: "/extcmd hello", Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
}
