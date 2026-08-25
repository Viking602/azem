package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityDefaultsAndValidation(t *testing.T) {
	cfg := Default()
	if !cfg.Security.Enabled || cfg.Security.DefaultMode != "standard" || cfg.Security.Workers != 4 || cfg.Security.Subagents != 3 ||
		cfg.Security.StopAfterNoNew != 4 || cfg.Security.StopAfterConsecutiveErrors != 3 || cfg.Security.MaxDiscoveryRuns != 40 ||
		cfg.Security.MaxTimeHours != 96 || cfg.Security.MaxTokens != 0 || cfg.Security.MaxToolCalls != 0 {
		t.Fatalf("security defaults = %+v", cfg.Security)
	}
	cfg.Security.Enabled = true
	cfg.Security.Routes.Audit = ModelRouteConfig{Provider: "grok", Model: "grok-security", Reasoning: "high"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid security config: %v", err)
	}
}

func TestSecurityLegacyUsageLimitsRemainReadableButOptional(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	legacy := "version: 1\nsecurity:\n  max_tokens: 50000000\n  max_tool_calls: 20000\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatalf("load legacy security limits: %v", err)
	}
	if cfg.Security.MaxTokens != 50_000_000 || cfg.Security.MaxToolCalls != 20_000 {
		t.Fatalf("legacy security limits = %d tokens, %d tool calls", cfg.Security.MaxTokens, cfg.Security.MaxToolCalls)
	}
	cfg.Security.Workers = 6
	if err := UpdateDesktopSecurityConfig(path, cfg.Security); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "max_tokens: 50000000") || !strings.Contains(string(raw), "max_tool_calls: 20000") {
		t.Fatalf("Desktop update rewrote legacy host fields:\n%s", raw)
	}
}

func TestSecurityValidationRejectsInvalidLimitsAndPartialRoutes(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"mode":            func(cfg *Config) { cfg.Security.DefaultMode = "exhaustive" },
		"workers":         func(cfg *Config) { cfg.Security.Workers = 0 },
		"workers_high":    func(cfg *Config) { cfg.Security.Workers = 33 },
		"subagents":       func(cfg *Config) { cfg.Security.Subagents = -1 },
		"subagents_high":  func(cfg *Config) { cfg.Security.Subagents = 33 },
		"discovery_high":  func(cfg *Config) { cfg.Security.MaxDiscoveryRuns = 1001 },
		"no_new_high":     func(cfg *Config) { cfg.Security.StopAfterNoNew = 1001 },
		"errors_high":     func(cfg *Config) { cfg.Security.StopAfterConsecutiveErrors = 1001 },
		"secret_argument": func(cfg *Config) { cfg.Security.PublicationArguments = map[string]any{"api_token": "secret"} },
		"oversized_argument": func(cfg *Config) {
			cfg.Security.PublicationArguments = map[string]any{"body": strings.Repeat("x", 65<<10)}
		},
		"deadline":          func(cfg *Config) { cfg.Security.MaxTimeHours = 97 },
		"cost":              func(cfg *Config) { cfg.Security.MaxCostUSD = -1 },
		"unsupported_cost":  func(cfg *Config) { cfg.Security.MaxCostUSD = 1 },
		"legacy_tokens":     func(cfg *Config) { cfg.Security.MaxTokens = -1 },
		"legacy_tool_calls": func(cfg *Config) { cfg.Security.MaxToolCalls = -1 },
		"route":             func(cfg *Config) { cfg.Security.Routes.Reducer = ModelRouteConfig{Provider: "chatgpt"} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("invalid security config was accepted: %+v", cfg.Security)
			}
		})
	}
}

func TestUpdateSecurityConfigPreservesFileAndPersistsEveryDesktopField(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n# keep this comment\nworkspace:\n  allow_write: true\nsecurity:\n  # keep security comment\n  enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	security := Default().Security
	security.Enabled = false
	security.DefaultMode = "deep"
	security.Workers = 6
	security.Subagents = 2
	security.StopAfterNoNew = 5
	security.StopAfterConsecutiveErrors = 4
	security.MaxDiscoveryRuns = 24
	security.MaxTimeHours = 12.5
	security.PublicationTool = "mcp__linear__create_issue"
	security.PublicationDestination = "linear-security"
	security.PublicationArguments = map[string]any{"team": "security", "priority": 2}
	security.PublicationTitleField = "title"
	security.PublicationDescriptionField = "description"
	security.Routes.Audit = ModelRouteConfig{Provider: "chatgpt", Model: "gpt-test", Reasoning: "high"}
	if err := UpdateSecurityConfig(path, security); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Security.Enabled || loaded.Security.DefaultMode != "deep" || loaded.Security.Workers != 6 ||
		loaded.Security.MaxTimeHours != 12.5 || loaded.Security.PublicationTool != "mcp__linear__create_issue" ||
		loaded.Security.Routes.Audit.Model != "gpt-test" || loaded.Security.PublicationArguments["team"] != "security" {
		t.Fatalf("persisted security config = %+v", loaded.Security)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# keep this comment", "# keep security comment", "allow_write: true", "publication_arguments:", "linear-security"} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("updated config omitted %q:\n%s", expected, raw)
		}
	}
	rotated := loaded.Security
	rotated.PublicationTool = "mcp__linear__create_issue_v2"
	rotated.PublicationArguments = map[string]any{"team": "security-v2"}
	rotated.Routes.Audit = ModelRouteConfig{Provider: "grok", Model: "grok-security", Reasoning: "high"}
	if err := UpdateSecurityConfig(path, rotated); err != nil {
		t.Fatal(err)
	}
	staleDesktop := loaded.Security
	staleDesktop.Workers = 7
	if err := UpdateDesktopSecurityConfig(path, staleDesktop); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Security.Workers != 7 || reloaded.Security.PublicationTool != "mcp__linear__create_issue_v2" ||
		reloaded.Security.PublicationArguments["team"] != "security-v2" || reloaded.Security.Routes.Audit.Model != "grok-security" {
		t.Fatalf("Desktop save overwrote host-admin security settings: %+v", reloaded.Security)
	}
}

func TestSecurityModelRouteUpdaterSupportsReset(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	route := ModelRouteConfig{Provider: "grok", Model: "grok-security", Reasoning: "high"}
	if err := UpdateModelRoute(path, "security", "verifier", route); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Security.Routes.Verifier != route {
		t.Fatalf("verifier route = %+v", loaded.Security.Routes.Verifier)
	}
	if err := ResetModelRoute(path, "security", "verifier"); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Security.Routes.Verifier != (ModelRouteConfig{}) {
		t.Fatalf("verifier route was not reset: %+v", loaded.Security.Routes.Verifier)
	}
	if err := UpdateModelRoute(path, "security", "unknown", route); err == nil {
		t.Fatal("unknown security route was accepted")
	}
}
