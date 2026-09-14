package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/extensions"
)

func TestExtensionAgentsMergeIntoRuntimeConfiguration(t *testing.T) {
	cfg := config.Default()
	diagnostics := mergeExtensionAgents(&cfg, []extensions.Agent{
		{Name: "reviewer", Description: "Native reviewer", SystemPrompt: "Review native code.", Source: "/.omp/agents/reviewer.md"},
		{Name: "scoped", Description: "Scoped agent", SystemPrompt: "Review scoped work.", Models: []string{"chatgpt/gpt-5.6-luna"}, Thinking: "high", Tools: []string{"read", "coding.search"}, Isolation: "worktree"},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("agent diagnostics = %v", diagnostics)
	}
	if cfg.Agents.Subagents.Roles["reviewer"].Instructions != "Review native code." {
		t.Fatalf("roles = %#v", cfg.Agents.Subagents.Roles)
	}
	scoped := cfg.Agents.Subagents.Roles["scoped"]
	if scoped.Provider != "chatgpt" || scoped.Model != "gpt-5.6-luna" || scoped.Reasoning != "high" || scoped.Isolation != "worktree" ||
		len(scoped.Tools) != 2 || scoped.Tools[0] != "coding.read_file" || scoped.Tools[1] != "coding.search" {
		t.Fatalf("scoped role = %#v", scoped)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeExtensionToolsReachGovernedSubagents(t *testing.T) {
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	worker := cfg.Agents.Subagents.Roles["worker"]
	workerTools := effectiveSubagentTools(worker.Tools, worker.CapabilityMode)
	for _, name := range []string{"eval", "lsp", "browser", "computer", "debug", "github", "generate_image", "tts", "ast_edit", "learn"} {
		tools := normalizeExtensionAgentTools([]string{name})
		if !workerTools[tools[0]] {
			t.Fatalf("default Vibe worker cannot use %s", name)
		}
		cfg.Agents.Subagents.Roles["native"] = config.SubagentRoleConfig{Instructions: "Native fixture", Tools: tools, CapabilityMode: "all"}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		capability := agentCapabilityMode(tools)
		if capability != "all" || !effectiveSubagentTools(tools, capability)[tools[0]] {
			t.Fatalf("native tool %s filtered out: %s", name, capability)
		}
		if effectiveSubagentTools(tools, "read-only")[tools[0]] {
			t.Fatalf("native mutation %s leaked into read-only role", name)
		}
	}
	for _, name := range []string{"ast_grep", "web_search", "inspect_image", "reflect"} {
		if !effectiveSubagentTools([]string{name}, "read-only")[name] {
			t.Fatalf("read tool %s unavailable", name)
		}
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
