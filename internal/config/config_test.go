package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUILanguageAcceptsTranslationPackIdentifiers(t *testing.T) {
	for _, language := range []string{"en", "zh-CN", "ja", "de-DE", "pt-BR", "zh-Hant", "es-419"} {
		cfg := Default()
		cfg.Defaults.Language = language
		if err := cfg.Validate(); err != nil {
			t.Fatalf("valid language %q rejected: %v", language, err)
		}
	}
	for _, language := range []string{"", "a", "../en", "en_US", "en--US", "en/US", "en\n", strings.Repeat("en-", 30) + "US"} {
		cfg := Default()
		cfg.Defaults.Language = language
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid language %q accepted", language)
		}
	}
}

func TestWorkspaceShellMaxWallClockDefaultsAndValidation(t *testing.T) {
	cfg := Default()
	if cfg.Workspace.Shell.MaxWallClockDuration != DefaultShellMaxWallClock {
		t.Fatalf("default shell wall clock = %s", cfg.Workspace.Shell.MaxWallClockDuration)
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workspace.Shell.MaxWallClockDuration != DefaultShellMaxWallClock {
		t.Fatalf("omitted shell wall clock = %s", loaded.Workspace.Shell.MaxWallClockDuration)
	}
	if err := os.WriteFile(path, []byte("version: 1\nworkspace:\n  shell:\n    max_context_output_bytes: 65536\n    max_artifact_output_bytes: 4194304\n    stop_on_output_limit: true\n    max_concurrency: 2\n    max_wall_clock: 30m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workspace.Shell.MaxWallClockDuration != 30*time.Minute {
		t.Fatalf("configured shell wall clock = %s", loaded.Workspace.Shell.MaxWallClockDuration)
	}
	if err := os.WriteFile(path, []byte("version: 1\nworkspace:\n  shell:\n    max_context_output_bytes: 65536\n    max_artifact_output_bytes: 4194304\n    stop_on_output_limit: true\n    max_concurrency: 2\n    max_wall_clock: 0s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, root); err == nil {
		t.Fatal("zero shell wall clock was accepted")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, root); err == nil {
		t.Fatal("Load accepted an unknown field")
	}
}

func TestPluginsConfigDefaultsAndLoad(t *testing.T) {
	cfg := Default()
	if !cfg.Plugins.Enabled || !cfg.Plugins.ImportCodex || cfg.Plugins.TrustHooks {
		t.Fatalf("plugin defaults = %#v", cfg.Plugins)
	}

	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nplugins:\n  enabled: true\n  import_codex: false\n  codex_imports: [demo@market]\n  trust_hooks: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Plugins.Enabled || loaded.Plugins.ImportCodex || !loaded.Plugins.TrustHooks || !reflect.DeepEqual(loaded.Plugins.CodexImports, []string{"demo@market"}) {
		t.Fatalf("loaded plugins = %#v", loaded.Plugins)
	}
}

func TestAuthBrokerConfigurationDefaultsAndValidation(t *testing.T) {
	cfg := Default()
	if cfg.Auth.Broker.SnapshotTTLParsed != time.Hour || cfg.Auth.Broker.SnapshotTTL != "1h" {
		t.Fatalf("broker defaults=%#v", cfg.Auth.Broker)
	}
	cfg.Auth.Broker.URL = "http://broker.example.com"
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted insecure remote broker URL")
	}
	cfg.Auth.Broker.URL = "http://127.0.0.1:8765"
	cfg.Auth.Broker.Token = "secret-that-must-not-be-projected"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(cfg.Auth.Broker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), cfg.Auth.Broker.Token) {
		t.Fatalf("broker token projected in JSON: %s", encoded)
	}
}

func TestUpdatePluginTrustHooksPreservesOtherPluginFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nplugins:\n  enabled: true\n  import_codex: true\n  codex_imports: [demo@market]\n  trust_hooks: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdatePluginTrustHooks(path, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Plugins.TrustHooks || !loaded.Plugins.ImportCodex || !reflect.DeepEqual(loaded.Plugins.CodexImports, []string{"demo@market"}) {
		t.Fatalf("updated plugins = %#v", loaded.Plugins)
	}
}

func TestHooksConfigDefaultsAndLoad(t *testing.T) {
	cfg := Default()
	if !cfg.Hooks.Enabled || cfg.Hooks.TrustProject || cfg.Hooks.ClaudeCompatibility || cfg.Hooks.DefaultTimeoutParsed != 5*time.Second || cfg.Hooks.FailurePolicy != "open" {
		t.Fatalf("hook defaults = %#v", cfg.Hooks)
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nhooks:\n  claude_compatibility: true\n  default_timeout: 2s\n  failure_policy: closed\n  additional_paths: [hooks]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Hooks.ClaudeCompatibility || cfg.Hooks.DefaultTimeoutParsed != 2*time.Second || cfg.Hooks.AdditionalPaths[0] != filepath.Join(root, "hooks") {
		t.Fatalf("loaded hooks = %#v", cfg.Hooks)
	}
	cfg.Hooks.FailurePolicy = "unsafe"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid hook failure policy accepted")
	}
}

func TestHooksDisabledLoadUpdateAndValidation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	id := "SessionStart\x1fnotify\x1f" + filepath.Join(root, "hooks.json") + "\x1f"
	if err := os.WriteFile(path, []byte("version: 1\nhooks:\n  disabled:\n    - "+strconv.Quote(id)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Hooks.Disabled, []string{id}) {
		t.Fatalf("loaded disabled hooks = %#v", loaded.Hooks.Disabled)
	}
	if err := UpdateHooksDisabled(path, nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.Hooks.Disabled) != 0 {
		t.Fatalf("cleared disabled hooks = %#v", cleared.Hooks.Disabled)
	}
	if err := UpdateHooksDisabled(path, []string{id, id}); err == nil {
		t.Fatal("duplicate disabled hook was accepted")
	}
	invalid := Default()
	invalid.Hooks.Disabled = []string{" "}
	if err := invalid.Validate(); err == nil {
		t.Fatal("empty disabled hook identity was accepted")
	}
}

func TestRetryConfigDefaultsLoadAndValidation(t *testing.T) {
	cfg := Default()
	if !cfg.Retry.Enabled || cfg.Retry.MaxRetries != 5 ||
		cfg.Retry.BaseDelayDuration != 500*time.Millisecond || cfg.Retry.MaxDelayDuration != 5*time.Minute {
		t.Fatalf("retry defaults = %#v", cfg.Retry)
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nretry:\n  enabled: true\n  max_retries: 3\n  base_delay: 250ms\n  max_delay: 1m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Retry.MaxRetries != 3 || loaded.Retry.BaseDelayDuration != 250*time.Millisecond ||
		loaded.Retry.MaxDelayDuration != time.Minute {
		t.Fatalf("loaded retry config = %#v", loaded.Retry)
	}
	loaded.Retry.MaxDelay = "100ms"
	if err := loaded.Validate(); err == nil {
		t.Fatal("retry max delay below base delay was accepted")
	}
	loaded = Default()
	loaded.Retry.MaxDelay = "0s"
	if err := loaded.Validate(); err != nil {
		t.Fatalf("zero retry max delay should disable the fail-fast cap: %v", err)
	}
}

func TestLanguageDefaultAndValidation(t *testing.T) {
	cfg := Default()
	if cfg.Defaults.Language != "en" {
		t.Fatalf("language = %q", cfg.Defaults.Language)
	}
	if cfg.Defaults.ApprovalMode != "prompt" {
		t.Fatalf("approval mode = %q", cfg.Defaults.ApprovalMode)
	}
	if cfg.Defaults.QueueMode != "queue" {
		t.Fatalf("queue mode = %q", cfg.Defaults.QueueMode)
	}
	cfg.Defaults.Language = "zh-CN"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Defaults.Language = "zh/../../en"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid language identifier accepted")
	}
	cfg = Default()
	cfg.Defaults.ApprovalMode = "unsafe"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unsupported approval mode accepted")
	}
	cfg = Default()
	cfg.Defaults.QueueMode = "later"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unsupported queue mode accepted")
	}
}

func TestUpdateDefaultPersistsSelectionsAndPreservesConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "# keep this comment\nversion: 1\ndefaults:\n  provider: grok\nworkspace:\n  allow_write: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateDefault(path, "language", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateDefault(path, "approval_mode", "yolo"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateDefault(path, "queue_mode", "guide"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# keep this comment") {
		t.Fatalf("config comment was lost:\n%s", data)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.Language != "zh-CN" || cfg.Defaults.ApprovalMode != "yolo" || cfg.Defaults.QueueMode != "guide" || cfg.Defaults.Provider != "grok" || !cfg.Workspace.AllowWrite {
		t.Fatalf("persisted config = %#v", cfg)
	}
}

func TestUpdateSessionModelDefaultsPersistsProviderModelReasoning(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "# keep defaults comment\nversion: 1\ndefaults:\n  provider: grok\n  model: grok-4.20\n  reasoning: high\nworkspace:\n  allow_write: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSessionModelDefaults(path, "chatgpt", "gpt-5.6-luna", "xhigh"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# keep defaults comment") {
		t.Fatalf("config comment was lost:\n%s", data)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.Provider != "chatgpt" || cfg.Defaults.Model != "gpt-5.6-luna" || cfg.Defaults.Reasoning != "xhigh" {
		t.Fatalf("session model defaults = %#v", cfg.Defaults)
	}
	if err := UpdateSessionModelDefaults(path, "", "model", "high"); err == nil {
		t.Fatal("empty provider was accepted")
	}
}

func TestUpdateSubscriptionDisabledModelsPreservesProviderSettings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\nproviders:\n  chatgpt:\n    enabled: true\n    catalog_ttl: 5m\n    fast_mode: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSubscriptionDisabledModels(path, "chatgpt", []string{"codex-auto-review"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Providers.ChatGPT.FastMode || !reflect.DeepEqual(loaded.Providers.ChatGPT.DisabledModels, []string{"codex-auto-review"}) {
		t.Fatalf("chatgpt config = %#v", loaded.Providers.ChatGPT)
	}
}

func TestUpdateSkillsSelectionPreservesSettingsAndRemovesEmptyLists(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "# keep skills comment\nversion: 1\nskills:\n  enabled: true\n  trust_project: true\n  additional_dirs: [custom-skills]\n  eager: [old]\nworkspace:\n  allow_write: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSkillsSelection(path, []string{"verify"}, []string{"old", "simplify"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# keep skills comment") {
		t.Fatalf("skills update lost the existing comment:\n%s", data)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Skills.Enabled || !loaded.Skills.TrustProject || !loaded.Workspace.AllowWrite ||
		!reflect.DeepEqual(loaded.Skills.Eager, []string{"verify"}) ||
		!reflect.DeepEqual(loaded.Skills.Disabled, []string{"old", "simplify"}) {
		t.Fatalf("persisted skills config = %#v", loaded.Skills)
	}
	if err := UpdateSkillsSelection(path, nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "eager:") || strings.Contains(string(data), "disabled:") {
		t.Fatalf("empty skill selections remained in config:\n%s", data)
	}
	if err := UpdateSkillsSelection(path, []string{"same"}, []string{"same"}); err == nil {
		t.Fatal("skill was accepted as both eager and disabled")
	}

	selectionOnly := filepath.Join(root, "selection-only.yaml")
	if err := os.WriteFile(selectionOnly, []byte("version: 1\nskills:\n  disabled: [temporary]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSkillsSelection(selectionOnly, nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(selectionOnly)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "skills:") {
		t.Fatalf("empty skills mapping remained after restoring the last disabled skill:\n%s", data)
	}
}

func TestUpdateMCPServerPersistsValidatedEntryAndPreservesOtherSettings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "# keep MCP comment\nversion: 1\ndefaults:\n  language: zh-CN\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := UpdateMCPServer(path, "local_docs", MCPServerConfig{
		Enabled: true, Transport: "stdio", Command: "docs-mcp", Args: []string{"serve"}, Approval: "never",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.ConnectTimeout != "30s" || server.CallTimeout != "60s" || server.MaxConcurrency != 2 {
		t.Fatalf("normalized MCP defaults = %#v", server)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# keep MCP comment") || !strings.Contains(string(data), "language: zh-CN") {
		t.Fatalf("MCP update lost unrelated configuration:\n%s", data)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.MCP.Servers["local_docs"]
	if !got.Enabled || got.Transport != "stdio" || got.Command != "docs-mcp" || !reflect.DeepEqual(got.Args, []string{"serve"}) || got.Approval != "never" {
		t.Fatalf("persisted MCP server = %#v", got)
	}
}

func TestDeleteMCPServerPreservesOtherSettingsAndWritesTombstone(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "# keep this comment\nversion: 1\ndefaults:\n  language: zh-CN\nmcp:\n  servers:\n    local_docs:\n      enabled: false\n      transport: stdio\n      command: docs-mcp\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteMCPServer(path, "local_docs"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "removed_servers:") || !strings.Contains(string(data), "- local_docs") {
		t.Fatalf("MCP deletion tombstone was not persisted:\n%s", data)
	}
	if !strings.Contains(string(data), "# keep this comment") || !strings.Contains(string(data), "language: zh-CN") {
		t.Fatalf("MCP delete lost unrelated configuration:\n%s", data)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.MCP.Servers["local_docs"]; ok {
		t.Fatalf("deleted MCP server returned after reload: %#v", loaded.MCP.Servers)
	}
	if !reflect.DeepEqual(loaded.MCP.RemovedServers, []string{"local_docs"}) {
		t.Fatalf("removed MCP servers = %#v", loaded.MCP.RemovedServers)
	}
}

func TestDeleteBuiltInMCPServerStaysRemovedUntilReadded(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteMCPServer(path, "grep"); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := loaded.MCP.Servers["grep"]; exists {
		t.Fatal("deleted built-in MCP server reappeared")
	}
	server := builtInMCPServers()["grep"]
	if _, err := UpdateMCPServer(path, "grep", server); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := loaded.MCP.Servers["grep"]; !exists || len(loaded.MCP.RemovedServers) != 0 {
		t.Fatalf("re-added built-in MCP = %#v removed=%#v", loaded.MCP.Servers, loaded.MCP.RemovedServers)
	}
}

func TestUpdateModelRoutePreservesYAMLAndDeletesOnlyRouteScalars(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "# top comment\nversion: 1\nagents:\n  # subagents comment\n  subagents:\n    models:\n      explore: legacy-model\n      review: keep-model\n    roles:\n      explore:\n        description: keep me\n        instructions: inspect only\n        unknown_future_field: keep too\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	route := ModelRouteConfig{Provider: "grok", Model: "grok-4", Reasoning: "high"}
	if err := UpdateModelRoute(path, "subagent", "explore", route); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# top comment", "# subagents comment", "description: keep me", "unknown_future_field: keep too", "review: keep-model", "provider: grok", "model: grok-4", "reasoning: high"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("updated config missing %q:\n%s", expected, data)
		}
	}
	if strings.Contains(string(data), "explore: legacy-model") {
		t.Fatalf("updated route retained its legacy override:\n%s", data)
	}
	reloadPath := filepath.Join(root, "reload.yaml")
	reloadable := strings.ReplaceAll(string(data), "        unknown_future_field: keep too\n", "")
	if err := os.WriteFile(reloadPath, []byte(reloadable), 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(reloadPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if role := reloaded.Agents.Subagents.Roles["explore"]; role.Provider != "grok" || role.Model != "grok-4" || role.Reasoning != "high" {
		t.Fatalf("reloaded route = %+v", role)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
	if err := UpdateModelRoute(path, "subagent", "explore", ModelRouteConfig{}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"provider: grok", "model: grok-4", "reasoning: high"} {
		if strings.Contains(string(data), removed) {
			t.Fatalf("cleared config retained %q:\n%s", removed, data)
		}
	}
	if !strings.Contains(string(data), "description: keep me") || !strings.Contains(string(data), "unknown_future_field: keep too") {
		t.Fatalf("clearing route removed role fields:\n%s", data)
	}
}

func TestUpdateLLMuxProviderPreservesYAMLAndReloadsModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n# preserve this comment\ndefaults:\n  theme: dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := LLMuxProviderConfig{Enabled: true, BaseURL: "https://openrouter.ai/api/v1", Models: []LLMuxModelConfig{{
		ID: "openai/gpt-5.4", Name: "GPT 5.4", ContextWindow: 400000,
		ReasoningLevels: []string{"low", "high"}, DefaultReasoning: "high",
	}}}
	if err := UpdateLLMuxProvider(path, "openrouter", want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "preserve this comment") || !strings.Contains(string(data), "theme: dark") || strings.Contains(string(data), "api_key") {
		t.Fatalf("provider update corrupted or leaked a secret:\n%s", data)
	}
	loaded, err := Load(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Providers.LLMux["openrouter"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("llmux provider = %#v, want %#v", got, want)
	}
}

func TestUpdateLLMuxProviderMigratesLegacyUnderscoreID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "providers:\n  llmux:\n    alibaba_coding_plan:\n      enabled: false\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateLLMuxProvider(path, "alibaba_coding_plan", LLMuxProviderConfig{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "alibaba_coding_plan") || !strings.Contains(string(data), "alibaba-coding-plan") {
		t.Fatalf("legacy provider ID was not migrated:\n%s", data)
	}
}

func TestUpdateLLMuxProviderMigratesLegacyOpenCodeID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "providers:\n  llmux:\n    opencode:\n      enabled: false\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateLLMuxProvider(path, "opencode", LLMuxProviderConfig{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "    opencode:") || !strings.Contains(string(data), "opencode-zen:") {
		t.Fatalf("legacy OpenCode provider ID was not migrated:\n%s", data)
	}
}

func TestResetModelRouteRemovesLegacyOverrideAndPreservesOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "agents:\n  subagents:\n    models:\n      explore: legacy-model\n      review: keep-model\n    roles:\n      explore:\n        description: keep me\n        provider: grok\n        model: grok-4\n        reasoning: high\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ResetModelRoute(path, "subagent", "explore"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, kept := range []string{"description: keep me", "review: keep-model"} {
		if !strings.Contains(text, kept) {
			t.Fatalf("reset removed %q:\n%s", kept, text)
		}
	}
	for _, removed := range []string{"explore: legacy-model", "provider: grok", "model: grok-4", "reasoning: high"} {
		if strings.Contains(text, removed) {
			t.Fatalf("reset retained %q:\n%s", removed, text)
		}
	}
}

func TestPersistedBuiltInRoleRouteReloadsWithoutExplicitRoleDefinition(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	route := ModelRouteConfig{Provider: "grok", Model: "grok-4.5", Reasoning: "low"}
	if err := UpdateModelRoute(path, "subagent", "explore", route); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	role := cfg.Agents.Subagents.Roles["explore"]
	if role.Provider != route.Provider || role.Model != route.Model || role.Reasoning != route.Reasoning || role.Instructions == "" {
		t.Fatalf("reloaded built-in route = %+v", role)
	}
	if err := ResetModelRoute(path, "subagent", "explore"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	role = cfg.Agents.Subagents.Roles["explore"]
	if role.Provider != "" || role.Model != "" || role.Reasoning != "" || role.Instructions == "" {
		t.Fatalf("reset built-in route = %+v", role)
	}
}

func TestModelRouteValidationAndDeprecatedCompactionRouteCompatibility(t *testing.T) {
	cfg := Default()
	cfg.Agents.Title = ModelRouteConfig{Provider: "chatgpt", Model: "gpt-title", Reasoning: "low"}
	cfg.Agents.Plan = ModelRouteConfig{Provider: "grok", Model: "grok-plan", Reasoning: "high"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid model routes: %v", err)
	}
	for _, route := range []ModelRouteConfig{
		{Provider: "chatgpt"},
		{Model: "gpt-test"},
		{Reasoning: "high"},
		{Provider: "invalid/provider", Model: "model"},
	} {
		cfg := Default()
		cfg.Agents.Plan = route
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted invalid route %#v", route)
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nagents:\n  title:\n    provider: grok\n    model: grok-title\n    reasoning: low\n  plan:\n    provider: grok\n    model: grok-plan\n    reasoning: high\n  compaction:\n    provider: chatgpt\n    model: gpt-test\n  context:\n    enabled: false\n    reserve_tokens: 12000\n    soft_trigger_ratio: 0.68\n    hard_trigger_ratio: 0.82\n    target_ratio: 0.45\n    background_prepare: true\n    safety_margin_ratio: 0.08\n    reserve_output_tokens: 16384\n    reserve_reasoning_tokens: 8192\n    min_reclaim_tokens: 16000\n    max_summary_tokens: 32768\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agents.Title != (ModelRouteConfig{Provider: "grok", Model: "grok-title", Reasoning: "low"}) {
		t.Fatalf("title route = %#v", loaded.Agents.Title)
	}
	if loaded.Agents.Plan != (ModelRouteConfig{Provider: "grok", Model: "grok-plan", Reasoning: "high"}) {
		t.Fatalf("plan route = %#v", loaded.Agents.Plan)
	}
	if loaded.Agents.Context.Enabled || loaded.Agents.Context.ReserveTokens != 12000 {
		t.Fatalf("context config = %#v", loaded.Agents.Context)
	}
}

func TestUpdatePlanModelRoutePersistsAndResets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n# keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	route := ModelRouteConfig{Provider: "grok", Model: "grok-plan", Reasoning: "high"}
	if err := UpdateModelRoute(path, "plan", "", route); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agents.Plan != route {
		t.Fatalf("plan route = %#v", loaded.Agents.Plan)
	}
	if err := ResetModelRoute(path, "plan", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil || loaded.Agents.Plan != (ModelRouteConfig{}) {
		t.Fatalf("reset plan route = %#v, error=%v", loaded.Agents.Plan, err)
	}
}

func TestUpdateApprovalModelRoutePersistsAndResets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n# keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	route := ModelRouteConfig{Provider: "deepseek", Model: "deepseek-v4-flash", Reasoning: "high"}
	if err := UpdateModelRoute(path, "approval", "", route); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil || loaded.Agents.Approval != route {
		t.Fatalf("approval route = %#v, error=%v", loaded.Agents.Approval, err)
	}
	if err := ResetModelRoute(path, "approval", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil || loaded.Agents.Approval != (ModelRouteConfig{}) {
		t.Fatalf("reset approval route = %#v, error=%v", loaded.Agents.Approval, err)
	}
}

func TestUpdateVisionModelRoutePersistsAndResets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n# keep vision route comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	route := ModelRouteConfig{Provider: "openrouter", Model: "google/gemini-vision", Reasoning: "low"}
	if err := UpdateModelRoute(path, "vision", "", route); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil || loaded.Agents.Vision != route {
		t.Fatalf("vision route = %#v, error=%v", loaded.Agents.Vision, err)
	}
	if err := ResetModelRoute(path, "vision", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil || loaded.Agents.Vision != (ModelRouteConfig{}) {
		t.Fatalf("reset vision route = %#v, error=%v", loaded.Agents.Vision, err)
	}
}

func TestUpdateRecapModelRoutePersistsAndResets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n# keep recap route comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	route := ModelRouteConfig{Provider: "deepseek", Model: "deepseek-v4-flash", Reasoning: "low"}
	if err := UpdateModelRoute(path, "recap", "", route); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil || loaded.Agents.Recap != route {
		t.Fatalf("recap route = %#v, error=%v", loaded.Agents.Recap, err)
	}
	if err := ResetModelRoute(path, "recap", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil || loaded.Agents.Recap != (ModelRouteConfig{}) {
		t.Fatalf("reset recap route = %#v, error=%v", loaded.Agents.Recap, err)
	}
}

func TestTTSRConfigValidation(t *testing.T) {
	valid := Default()
	valid.TTSR.Rules = []StreamRuleConfig{{
		Name: "safe-write", Content: "Use the safe writer.", Conditions: []string{`danger\s+write`},
		ASTConditions: []string{"console.log($MSG)"}, Scope: []string{"text", "tool:coding.write_file(*.ts)"}, Globs: []string{"*.ts"},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid TTSR config: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"context mode":   func(cfg *Config) { cfg.TTSR.ContextMode = "unknown" },
		"interrupt mode": func(cfg *Config) { cfg.TTSR.InterruptMode = "sometimes" },
		"repeat gap":     func(cfg *Config) { cfg.TTSR.RepeatGap = 0 },
		"invalid regex": func(cfg *Config) {
			cfg.TTSR.Rules = []StreamRuleConfig{{Name: "bad", Content: "bad", Conditions: []string{"("}}}
		},
		"duplicate": func(cfg *Config) {
			cfg.TTSR.Rules = []StreamRuleConfig{
				{Name: "Rule", Content: "one", Conditions: []string{"one"}},
				{Name: "rule", Content: "two", Conditions: []string{"two"}},
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid TTSR config accepted")
			}
		})
	}
}

func TestLoopGuardConfigValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"threshold":       func(cfg *Config) { cfg.Agents.LoopGuards.ToolCallThreshold = 1 },
		"unexpected mode": func(cfg *Config) { cfg.Agents.LoopGuards.UnexpectedStop = "guess" },
		"retry count":     func(cfg *Config) { cfg.Agents.LoopGuards.UnexpectedStopRetries = 11 },
		"empty exemption": func(cfg *Config) { cfg.Agents.LoopGuards.ToolCallExemptTools = []string{""} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid loop guard config accepted")
			}
		})
	}
}

func TestUpdateAdvisorModelRoutePreservesAdvisorSettings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nagents:\n  advisor:\n    enabled: true\n    catchup_timeout: 12s\n    instructions: review rollback\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	route := ModelRouteConfig{Provider: "deepseek", Model: "deepseek-v4-flash", Reasoning: "low"}
	if err := UpdateModelRoute(path, "advisor", "", route); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil || loaded.Agents.Advisor.Route() != route || !loaded.Agents.Advisor.Enabled ||
		loaded.Agents.Advisor.CatchupTimeout != "12s" || loaded.Agents.Advisor.Instructions != "review rollback" {
		t.Fatalf("advisor config = %#v, error=%v", loaded.Agents.Advisor, err)
	}
	if err := ResetModelRoute(path, "advisor", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil || loaded.Agents.Advisor.Route() != (ModelRouteConfig{}) || !loaded.Agents.Advisor.Enabled {
		t.Fatalf("reset advisor config = %#v, error=%v", loaded.Agents.Advisor, err)
	}
}

func TestUpdateVibeModelRoutes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fast := ModelRouteConfig{Provider: "deepseek", Model: "deepseek-v4-flash", Reasoning: "low"}
	if err := UpdateModelRoute(path, "vibe", "fast", fast); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil || loaded.Agents.Vibe.Fast != fast {
		t.Fatalf("Vibe fast route = %#v, %v", loaded.Agents.Vibe.Fast, err)
	}
	if err := ResetModelRoute(path, "vibe", "fast"); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil || loaded.Agents.Vibe.Fast != (ModelRouteConfig{}) {
		t.Fatalf("reset Vibe fast route = %#v, %v", loaded.Agents.Vibe.Fast, err)
	}
	if err := UpdateModelRoute(path, "vibe", "other", fast); err == nil {
		t.Fatal("invalid Vibe role accepted")
	}
}

func TestUpdateTitleModelRoutePersistsAndResetsToInherited(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	route := ModelRouteConfig{Provider: "grok", Model: "grok-title", Reasoning: "low"}
	if err := UpdateModelRoute(path, "title", "", route); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil || loaded.Agents.Title != route {
		t.Fatalf("title route = %#v, error=%v", loaded.Agents.Title, err)
	}
	if err := ResetModelRoute(path, "title", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path, root)
	if err != nil || loaded.Agents.Title != (ModelRouteConfig{}) {
		t.Fatalf("reset title route = %#v, error=%v", loaded.Agents.Title, err)
	}
}

func TestArchiveContextDefaultsAndValidation(t *testing.T) {
	defaults := Default().Agents.Context
	if !defaults.Enabled || defaults.ReserveTokens != 16384 ||
		defaults.KeepRecentTokens != 20000 || defaults.LargeToolResultTokens != 12000 ||
		defaults.HistoryRetrievalTokens != 4096 {
		t.Fatalf("defaults=%+v", defaults)
	}
	for _, mutate := range []func(*ContextConfig){
		func(c *ContextConfig) { c.ReserveTokens = 0 },
		func(c *ContextConfig) { c.KeepRecentTokens = 0 },
		func(c *ContextConfig) { c.LargeToolResultTokens = 0 },
		func(c *ContextConfig) { c.HistoryRetrievalTokens = 0 },
	} {
		cfg := Default()
		mutate(&cfg.Agents.Context)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted invalid context config %+v", cfg.Agents.Context)
		}
	}
}

func TestSubagentRoutesAllowInheritedProvider(t *testing.T) {
	for _, route := range []ModelRouteConfig{{Model: "child"}, {Reasoning: "high"}, {Model: "child", Reasoning: "high"}} {
		cfg := Default()
		role := cfg.Agents.Subagents.Roles["explore"]
		role.Provider, role.Model, role.Reasoning = route.Provider, route.Model, route.Reasoning
		cfg.Agents.Subagents.Roles["explore"] = role
		if err := cfg.Validate(); err != nil {
			t.Fatalf("inherited role route %#v: %v", route, err)
		}
	}
	cfg := Default()
	role := cfg.Agents.Subagents.Roles["explore"]
	role.Provider = "grok"
	cfg.Agents.Subagents.Roles["explore"] = role
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted subagent provider without model")
	}
}

func TestLoadResolvesRelativeWorkspaceFromConfigDirectory(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\nworkspace:\n  root: workspace\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if cfg.Workspace.Root != workspace {
		t.Fatalf("workspace = %q, want %q", cfg.Workspace.Root, workspace)
	}
}

func TestLoadAtWorkspaceIgnoresConfiguredWorkspaceRoot(t *testing.T) {
	root, selected := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nworkspace:\n  root: .\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAtWorkspace(path, selected)
	if err != nil {
		t.Fatal(err)
	}
	selected, _ = filepath.EvalSymlinks(selected)
	if cfg.Workspace.Root != selected {
		t.Fatalf("workspace = %q, want selected project %q", cfg.Workspace.Root, selected)
	}
}

func TestLoadOverridesMainAgentBudgets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\nagents:\n  main:\n    max_tokens: 750000\n    max_tool_calls: 256\n    max_wall_clock: 45m\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.Main.MaxTokens != 750_000 {
		t.Fatalf("main agent max tokens = %d, want 750000", cfg.Agents.Main.MaxTokens)
	}
	if cfg.Agents.Main.MaxToolCalls != 256 {
		t.Fatalf("main agent max tool calls = %d, want 256", cfg.Agents.Main.MaxToolCalls)
	}
	if cfg.Agents.Main.MaxWallClockDuration != 45*time.Minute {
		t.Fatalf("main agent max wall clock = %s, want 45m", cfg.Agents.Main.MaxWallClockDuration)
	}
}

func TestLoadSparseConfigKeepsCodingBudgetsUnbounded(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\ndefaults:\n  language: zh-CN\n  approval_mode: auto_review\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.Main.MaxTokens != 0 || cfg.Agents.Main.MaxToolCalls != 0 || cfg.Agents.Main.MaxWallClockDuration != 0 {
		t.Fatalf("sparse config main budget = %#v, want unbounded", cfg.Agents.Main)
	}
	if cfg.Defaults.QueueMode != "queue" {
		t.Fatalf("sparse config queue mode = %q, want queue", cfg.Defaults.QueueMode)
	}
	budget := cfg.Agents.Subagents.Budget
	if budget.MaxTokens != 0 || budget.MaxToolCalls != 0 || budget.MaxTurns != 0 || budget.MaxWallClockDuration != 0 {
		t.Fatalf("sparse config subagent budget = %#v, want unbounded", budget)
	}
}

func TestResolveReferenceRequiresExplicitScheme(t *testing.T) {
	if _, err := ResolveReference("literal-secret", os.LookupEnv, nil); err == nil {
		t.Fatal("ResolveReference accepted a literal secret")
	}
}

func TestDefaultIncludesBuiltInGrepMCPServer(t *testing.T) {
	cfg := Default()
	server, ok := cfg.MCP.Servers["grep"]
	if !ok {
		t.Fatal("default config omitted built-in grep MCP server")
	}
	if !server.Enabled || server.Transport != "streamable_http" || server.URL != "https://mcp.grep.app" || server.Approval != "never" || !server.Managed {
		t.Fatalf("built-in grep MCP server = %#v", server)
	}
	override, ok := server.ToolOverrides["searchGitHub"]
	if !ok || override.Effect != "read_only" || override.Approval != "never" {
		t.Fatalf("built-in grep tool override = %#v, present=%v", override, ok)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate default config: %v", err)
	}
}

func TestLoadCanOverrideBuiltInGrepMCPServer(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\nmcp:\n  servers:\n    grep:\n      enabled: false\n    local:\n      enabled: false\n      transport: stdio\n      command: local-mcp\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCP.Servers["grep"]
	if server.Enabled || server.Transport != "streamable_http" || server.URL != "https://mcp.grep.app" || !server.Managed {
		t.Fatalf("disabled built-in grep MCP server = %#v", server)
	}
	if local := cfg.MCP.Servers["local"]; local.Command != "local-mcp" {
		t.Fatalf("custom MCP server was not retained: %#v", local)
	}
}

func TestMCPConfigValidatesTransportSecretsAndDefaults(t *testing.T) {
	cfg := Default()
	cfg.MCP.Servers["local_files"] = MCPServerConfig{
		Enabled: true, Transport: "stdio", Command: "server",
		Env: map[string]string{"TOKEN": "env:MCP_TOKEN"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	server := cfg.MCP.Servers["local_files"]
	if server.ConnectDuration.String() != "30s" || server.CallDuration.String() != "1m0s" || server.MaxConcurrency != 2 || server.Approval != "always" {
		t.Fatalf("server defaults = %#v", server)
	}
}

func TestMCPConfigRejectsUnsafeInputs(t *testing.T) {
	cases := map[string]MCPServerConfig{
		"invalid name!":   {Transport: "stdio", Command: "server"},
		"missing_command": {Transport: "stdio"},
		"insecure_http":   {Transport: "streamable_http", URL: "http://example.com/mcp"},
		"literal_secret":  {Transport: "stdio", Command: "server", Env: map[string]string{"TOKEN": "secret"}},
		"bad_override": {
			Transport: "stdio", Command: "server",
			ToolOverrides: map[string]ToolOverride{"read": {Effect: "safe", Approval: "never"}},
		},
	}
	for name, server := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			cfg.MCP.Servers[name] = server
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted %#v", server)
			}
		})
	}
}

func TestMCPConfigAllowsLoopbackHTTP(t *testing.T) {
	cfg := Default()
	cfg.MCP.Servers["local"] = MCPServerConfig{Transport: "streamable_http", URL: "http://127.0.0.1:8080/mcp"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentConfigDefaultsAndBudgets(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Skills.Enabled || cfg.Skills.TrustProject {
		t.Fatalf("skill defaults = %#v, want enabled with project trust disabled", cfg.Skills)
	}
	if cfg.Agents.Main.MaxTokens != 0 || cfg.Agents.Main.MaxToolCalls != 0 || cfg.Agents.Main.MaxWallClockDuration != 0 {
		t.Fatalf("main agent budget = %#v", cfg.Agents.Main)
	}
	subagents := cfg.Agents.Subagents
	if !subagents.Enabled || subagents.MaxDepth != 2 || subagents.MaxConcurrency != 32 ||
		subagents.AwaitTimeout != "0s" || subagents.AwaitDuration != 0 ||
		subagents.IdleTimeout != "5m" || subagents.IdleDuration != DefaultSubagentIdleTimeout || !subagents.AutoWake {
		t.Fatalf("subagent defaults = %#v", subagents)
	}
	if subagents.Budget.SoftRequests != 200 || !subagents.Budget.SoftRequestNotice ||
		subagents.Budget.MaxTokens != 0 || subagents.Budget.MaxToolCalls != 0 ||
		subagents.Budget.MaxTurns != 0 || subagents.Budget.MaxWallClockDuration != 0 {
		t.Fatalf("subagent budget = %#v", subagents.Budget)
	}
	wantRoles := []string{"worker", "explore", "plan", "review", "verify", "security-baseline", "security-investigator"}
	if len(subagents.Roles) != len(wantRoles) {
		t.Fatalf("built-in roles = %#v, want exactly %q", subagents.Roles, wantRoles)
	}
	for _, name := range wantRoles {
		if _, ok := subagents.Roles[name]; !ok {
			t.Fatalf("built-in role %q is missing", name)
		}
	}
	if _, ok := subagents.Roles["general-purpose"]; ok {
		t.Fatal("removed general-purpose role remains built in")
	}

	invalid := Default()
	invalid.Agents.Subagents.MaxDepth = -2
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid max_depth was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.MaxConcurrency = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative max_concurrency was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.AwaitTimeout = "30m"
	invalid.Agents.Subagents.Budget.MaxWallClock = "20m"
	if err := invalid.Validate(); err != nil {
		t.Fatalf("foreground wait window was incorrectly treated as a task runtime limit: %v", err)
	}
	zeroWait := Default()
	for _, value := range []string{"0s", "0"} {
		zeroWait.Agents.Subagents.AwaitTimeout = value
		if err := zeroWait.Validate(); err != nil {
			t.Fatalf("await_timeout %q was rejected: %v", value, err)
		}
		if zeroWait.Agents.Subagents.AwaitDuration != 0 {
			t.Fatalf("await_timeout %q duration = %s", value, zeroWait.Agents.Subagents.AwaitDuration)
		}
	}
	invalid = Default()
	invalid.Agents.Subagents.AwaitTimeout = "-1s"
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative await_timeout was accepted")
	}
	zeroIdle := Default()
	for _, value := range []string{"0s", "0", ""} {
		zeroIdle.Agents.Subagents.IdleTimeout = value
		if err := zeroIdle.Validate(); err != nil {
			t.Fatalf("idle_timeout %q was rejected: %v", value, err)
		}
		if zeroIdle.Agents.Subagents.IdleDuration != 0 {
			t.Fatalf("idle_timeout %q duration = %s", value, zeroIdle.Agents.Subagents.IdleDuration)
		}
	}
	validIdle := Default()
	validIdle.Agents.Subagents.IdleTimeout = "5m"
	if err := validIdle.Validate(); err != nil {
		t.Fatalf("idle_timeout 5m was rejected: %v", err)
	}
	if validIdle.Agents.Subagents.IdleDuration != 5*time.Minute {
		t.Fatalf("idle_timeout 5m duration = %s", validIdle.Agents.Subagents.IdleDuration)
	}
	invalid = Default()
	invalid.Agents.Subagents.IdleTimeout = "-1s"
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative idle_timeout was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.Budget.MaxTokens = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative subagent token budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.Budget.MaxToolCalls = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative tool-call budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.Budget.SoftRequests = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative soft request budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.Budget.MaxTurns = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative turn budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.Budget.MaxWallClock = "-1s"
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative subagent wall-clock budget was accepted")
	}
	invalid = Default()
	for index := 0; index <= maxConfiguredSubagentRoles; index++ {
		invalid.Agents.Subagents.Roles[fmt.Sprintf("custom-%d", index)] = SubagentRoleConfig{
			Instructions: "inspect", CapabilityMode: "read-only",
		}
	}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("oversized subagent role catalog validation error = %v", err)
	}
	invalid = Default()
	invalid.Agents.Subagents.Roles[strings.Repeat("x", maxConfiguredSubagentRoleNameBytes+1)] = SubagentRoleConfig{
		Instructions: "inspect", CapabilityMode: "read-only",
	}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("oversized subagent role name validation error = %v", err)
	}
	invalid = Default()
	invalid.Agents.Main.MaxTokens = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative main-agent token budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Main.MaxToolCalls = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative main-agent tool-call budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Main.MaxWallClock = "invalid"
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid main-agent wall-clock budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Main.MaxWallClock = "-1s"
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative main-agent wall-clock budget was accepted")
	}
}

func TestBuiltInSubagentRoleContracts(t *testing.T) {
	roles := builtInSubagentRoles()
	readOnly := []string{"coding.list_files", "coding.glob", "coding.read_file", "coding.search", "ast_grep", "lsp", "web_search", "github", "recall", "coding.git_diff"}
	all := append(append([]string(nil), readOnly...), "coding.edit_hashline", "coding.replace", "coding.write_file", "coding.delete_file", "coding.gofmt", "coding.go_test", "coding.shell", "debug", "eval", "browser", "computer", "hub", "generate_image", "tts", "retain", "memory_edit")
	execute := append(append([]string(nil), readOnly...), "coding.go_test", "coding.shell", "debug", "eval", "browser", "computer", "hub")
	want := map[string]struct {
		description string
		capability  string
		tools       []string
		mission     string
	}{
		"worker": {
			"Implement one scoped coding task end-to-end and return verified evidence.",
			"all", all, "Implement one scoped coding assignment",
		},
		"explore": {
			"Investigate the workspace without changes and return file-backed evidence.",
			"read-only", readOnly, "Investigate the assigned workspace question",
		},
		"plan": {
			"Produce a decision-complete implementation plan without changing the workspace.",
			"read-only", readOnly, "Produce a decision-complete implementation plan",
		},
		"review": {
			"Review a delegated change for requirement, correctness, and regression risks without editing.",
			"read-only", readOnly, "Review the delegated change",
		},
		"security-baseline": {
			"Run one independent read-only source-backed security audit.",
			"read-only", readOnly, "Perform one independent, source-backed security audit",
		},
		"security-investigator": {
			"Investigate one concrete security packet with exact source evidence.",
			"read-only", readOnly, "Investigate only the concrete security packet",
		},
		"verify": {
			"Run governed checks without editing and report exact outcomes.",
			"execute", execute, "Verify the assigned behavior",
		},
	}
	if len(roles) != len(want) {
		t.Fatalf("built-in role count = %d, want %d", len(roles), len(want))
	}
	seenInstructions := map[string]string{}
	for name, expected := range want {
		role, ok := roles[name]
		if !ok {
			t.Fatalf("built-in role %q is missing", name)
		}
		if role.Description != expected.description || role.CapabilityMode != expected.capability ||
			role.Isolation != "none" || strings.Join(role.Tools, "\x00") != strings.Join(expected.tools, "\x00") {
			t.Errorf("role %q contract = %#v", name, role)
		}
		if strings.TrimSpace(role.Instructions) == "" || !strings.Contains(role.Instructions, expected.mission) {
			t.Errorf("role %q instructions do not contain mission %q: %q", name, expected.mission, role.Instructions)
		}
		if previous, duplicate := seenInstructions[role.Instructions]; duplicate {
			t.Errorf("roles %q and %q share the same instructions", previous, name)
		}
		seenInstructions[role.Instructions] = name
		if role.Provider != "" || role.Model != "" || role.Reasoning != "" {
			t.Errorf("role %q hard-codes model route: %#v", name, role)
		}
	}
}

func TestBuiltInSubagentRolePromptDepth(t *testing.T) {
	type promptContract struct {
		minBytes int
		maxBytes int
		sections []string
		markers  []string
	}
	want := map[string]promptContract{
		"worker": {
			minBytes: 3000,
			maxBytes: 4500,
			sections: []string{"Mission", "Before editing", "Implementation discipline", "Tool discipline", "Verification", "Failure handling", "Final response"},
			markers:  []string{"source of the problem", "observed result", "Status: BLOCKED"},
		},
		"explore": {
			minBytes: 2800,
			maxBytes: 4300,
			sections: []string{"Mission", "Choose the required depth", "Search strategy", "Evidence discipline", "Stop and failure conditions", "Final response"},
			markers:  []string{"Quick", "Medium", "Thorough", "try at least one independent strategy", "producer", "consumer"},
		},
		"plan": {
			minBytes: 2800,
			maxBytes: 4200,
			sections: []string{"Mission", "Investigation", "Plan construction", "Risk and verification", "Scope discipline", "Final response"},
			markers:  []string{"real dependency", "observable result", "Non-goals", "Open decisions"},
		},
		"review": {
			minBytes: 4200,
			maxBytes: 6500,
			sections: []string{"Mission", "Establish the review boundary", "Review procedure", "Finding criteria", "Severity", "Security and trust boundaries", "Compatibility and recovery", "Final response"},
			markers:  []string{"Introduced", "Provable", "Actionable", "Material", "Proportionate", "producer or entry point", "consumer-side dispatcher"},
		},
		"verify": {
			minBytes: 3000,
			maxBytes: 4500,
			sections: []string{"Mission", "Build the verification map", "Command discipline", "Failure attribution", "Evidence discipline", "Final response"},
			markers:  []string{"verification ladder", "changed-path failure", "demonstrably pre-existing", "unknown attribution"},
		},
	}

	roles := builtInSubagentRoles()
	for name, expected := range want {
		t.Run(name, func(t *testing.T) {
			instructions := roles[name].Instructions
			size := len([]byte(instructions))
			if size < expected.minBytes || size > expected.maxBytes {
				t.Fatalf("prompt size = %d bytes, want %d-%d", size, expected.minBytes, expected.maxBytes)
			}

			previousSection := -1
			for _, section := range expected.sections {
				heading := "## " + section
				index := strings.Index(instructions, heading)
				if index < 0 {
					t.Fatalf("prompt missing section %q", heading)
				}
				if index <= previousSection {
					t.Fatalf("prompt section %q is out of order", heading)
				}
				previousSection = index
			}
			for _, marker := range expected.markers {
				if !strings.Contains(instructions, marker) {
					t.Errorf("prompt missing role contract %q", marker)
				}
			}
			for _, shared := range []string{"Effective CWD:", "Capability mode:", "You start without the parent conversation"} {
				if strings.Contains(instructions, shared) {
					t.Errorf("prompt duplicates shared execution envelope %q", shared)
				}
			}
		})
	}
}

func TestRemovedGeneralPurposeBuiltInCompatibility(t *testing.T) {
	for name, configure := range map[string]func(*SubagentConfig){
		"models": func(subagents *SubagentConfig) {
			subagents.Models["general-purpose"] = "legacy-model"
		},
		"routes": func(subagents *SubagentConfig) {
			subagents.Routes["general-purpose"] = ModelRouteConfig{Provider: "chatgpt", Model: "legacy-model"}
		},
		"toggle": func(subagents *SubagentConfig) {
			subagents.Toggle["general-purpose"] = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			configure(&cfg.Agents.Subagents)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `unknown role "general-purpose"`) {
				t.Fatalf("legacy %s reference validation error = %v", name, err)
			}
		})
	}

	root := t.TempDir()
	t.Setenv("HOME", root)
	path := filepath.Join(root, "config.yaml")
	contents := `version: 1
agents:
  subagents:
    roles:
      general-purpose:
        description: Custom compatibility role
        instructions: Perform only the configured custom task.
        capability_mode: read-only
        isolation: none
        tools: [coding.list_files, coding.read_file, coding.search, coding.git_diff]
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	role, ok := cfg.Agents.Subagents.Roles["general-purpose"]
	if !ok || role.Source != "config:"+path || role.Description != "Custom compatibility role" {
		t.Fatalf("explicit custom general-purpose role = %#v", role)
	}
}

func TestLoadSubagentRolePersonaAndInstructionFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "role.txt"), []byte("Return a structured assessment."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "persona.txt"), []byte("Think like a reliability engineer."), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	contents := `version: 1
workspace:
  root: workspace
agents:
  subagents:
    roles:
      specialist:
        description: Inspect reliability
        instructions_file: role.txt
        persona: analyst
        capability_mode: read-only
        tools: [coding.read_file]
    personas:
      analyst:
        description: Reliability analyst
        instructions_file: persona.txt
        provider: chatgpt
        model: persona-model
        reasoning: high
        isolation: worktree
        inputs:
          - name: scope
            type: string
            required: true
        outputs:
          - name: findings
            type: array
            required: true
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, workspace)
	if err != nil {
		t.Fatal(err)
	}
	role := cfg.Agents.Subagents.Roles["specialist"]
	persona := cfg.Agents.Subagents.Personas["analyst"]
	if role.Instructions != "Return a structured assessment." || role.InstructionsFile != filepath.Join(root, "role.txt") ||
		role.Persona != "analyst" || role.Source != "config:"+path {
		t.Fatalf("role = %#v", role)
	}
	if persona.Instructions != "Think like a reliability engineer." || persona.InstructionsFile != filepath.Join(root, "persona.txt") ||
		persona.Source != "config:"+path || len(persona.Inputs) != 1 || len(persona.Outputs) != 1 {
		t.Fatalf("persona = %#v", persona)
	}
}

func TestSubagentRolePersonaValidationRejectsAmbiguousOrInvalidContracts(t *testing.T) {
	cfg := Default()
	cfg.Agents.Subagents.Personas["broken"] = SubagentPersonaConfig{
		Instructions: "inspect",
		Inputs: []SubagentContractItem{
			{Name: "scope", Type: "string"},
			{Name: "scope", Type: "string"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate persona contract item was accepted")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "instructions.txt"), []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	contents := `version: 1
workspace:
  root: workspace
agents:
  subagents:
    roles:
      ambiguous:
        instructions: inline
        instructions_file: instructions.txt
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, workspace); err == nil {
		t.Fatal("role with instructions and instructions_file was accepted")
	}
}

func TestDiscoverSubagentProfilesUsesStrictRootPrecedenceAndFormats(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	workspace := filepath.Join(t.TempDir(), "workspace")
	compat := filepath.Join(home, ".agents", "agents")
	user := filepath.Join(home, ".azem", "agents")
	project := filepath.Join(workspace, ".azem", "agents")
	for _, directory := range []string{compat, user, project} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile(filepath.Join(compat, "layered.json"), `{"name":"layered","instructions":"compat","capability_mode":"read-only"}`)
	writeProfile(filepath.Join(user, "layered.toml"), "name = \"layered\"\ninstructions = \"user\"\ncapability_mode = \"read-only\"\n")
	writeProfile(filepath.Join(project, "layered.md"), "---\nname: layered\nkind: role\ncapability_mode: read-only\n---\nproject\n")
	writeProfile(filepath.Join(compat, "toml-role.toml"), "name = \"toml-role\"\ninstructions = \"from toml\"\ncapability_mode = \"read-only\"\n")
	writeProfile(filepath.Join(user, "analyst.json"), `{
		"kind":"persona","name":"analyst","instructions":"from json",
		"inputs":[{"name":"scope","type":"string","required":true}]
	}`)

	cfg := Default()
	cfg.Agents.Subagents.Roles["explicit"] = SubagentRoleConfig{Instructions: "config", CapabilityMode: "read-only", Source: "config"}
	writeProfile(filepath.Join(project, "explicit.json"), `{"name":"explicit","instructions":"discovered","capability_mode":"read-only"}`)
	if err := discoverSubagentProfiles(&cfg, workspace, home, map[string]bool{"explicit": true}, nil); err != nil {
		t.Fatal(err)
	}
	layered := cfg.Agents.Subagents.Roles["layered"]
	if layered.Instructions != "project" || layered.Source != filepath.Join(project, "layered.md") {
		t.Fatalf("layered profile = %#v", layered)
	}
	if cfg.Agents.Subagents.Roles["toml-role"].Instructions != "from toml" {
		t.Fatalf("TOML role = %#v", cfg.Agents.Subagents.Roles["toml-role"])
	}
	if persona := cfg.Agents.Subagents.Personas["analyst"]; persona.Instructions != "from json" || len(persona.Inputs) != 1 {
		t.Fatalf("JSON persona = %#v", persona)
	}
	if explicit := cfg.Agents.Subagents.Roles["explicit"]; explicit.Instructions != "config" || explicit.Source != "config" {
		t.Fatalf("explicit role was overridden: %#v", explicit)
	}
}

func TestDiscoverSubagentProfilesRejectsMalformedSupportedFiles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	project := filepath.Join(workspace, ".azem", "agents")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "broken.json"), []byte(`{"instructions":"x","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if err := discoverSubagentProfiles(&cfg, workspace, home, nil, nil); err == nil {
		t.Fatal("malformed discovered profile was accepted")
	}
}

func TestLoadSkillsConfigAndRelativeAdditionalDirs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\nskills:\n  enabled: false\n  trust_project: false\n  additional_dirs:\n    - relative-skills\n  eager:\n    - alpha\n  disabled:\n    - beta\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Skills.Enabled || cfg.Skills.TrustProject {
		t.Fatalf("skills flags = enabled:%v trust_project:%v, want both false", cfg.Skills.Enabled, cfg.Skills.TrustProject)
	}
	wantDir := filepath.Join(root, "relative-skills")
	if len(cfg.Skills.AdditionalDirs) != 1 || cfg.Skills.AdditionalDirs[0] != wantDir {
		t.Fatalf("additional_dirs = %#v, want [%q]", cfg.Skills.AdditionalDirs, wantDir)
	}
	if len(cfg.Skills.Eager) != 1 || cfg.Skills.Eager[0] != "alpha" {
		t.Fatalf("eager = %#v, want [alpha]", cfg.Skills.Eager)
	}
	if len(cfg.Skills.Disabled) != 1 || cfg.Skills.Disabled[0] != "beta" {
		t.Fatalf("disabled = %#v, want [beta]", cfg.Skills.Disabled)
	}
}

func TestLoadSkillsConfigRejectsUnknownField(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\nskills:\n  enabled: true\n  trust_project: true\n  additional_dirs: []\n  eager: []\n  disabled: []\n  unknown: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, root); err == nil {
		t.Fatal("Load accepted an unknown skills field")
	}
}

func TestSkillsConfigValidation(t *testing.T) {
	cfg := Default()
	cfg.Skills.Eager = []string{"demo"}
	cfg.Skills.Disabled = []string{"demo"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a skill in eager and disabled")
	}

	cfg = Default()
	cfg.Skills.AdditionalDirs = make([]string, 57)
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted 57 additional skill directories")
	}

	cfg = Default()
	cfg.Skills.Eager = []string{"demo", "demo"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted duplicate eager skills")
	}

	cfg = Default()
	cfg.Skills.Disabled = []string{""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted an empty disabled skill name")
	}
}

func TestUpdateSubagentMaxConcurrencyPreservesConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\n# keep this comment\ndefaults:\n  language: zh-CN\nagents:\n  subagents:\n    max_concurrency: 2\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSubagentMaxConcurrency(path, 6); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "# keep this comment") || !strings.Contains(string(updated), "max_concurrency: 6") || !strings.Contains(string(updated), "language: zh-CN") {
		t.Fatalf("updated config:\n%s", updated)
	}
	if err := UpdateSubagentMaxConcurrency(path, 0); err != nil {
		t.Fatalf("unbounded concurrency was rejected: %v", err)
	}
	if err := UpdateSubagentMaxConcurrency(path, -1); err == nil {
		t.Fatal("negative concurrency was accepted")
	}
}

func TestUpdateRuntimeCapacitySettingsPreserveConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	contents := "version: 1\n# capacity comment\nworkspace:\n  shell:\n    max_concurrency: 2\nagents:\n  subagents:\n    await_timeout: 10m\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateShellMaxConcurrency(path, 4); err != nil {
		t.Fatal(err)
	}
	if err := UpdateShellMaxWallClock(path, 1800); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSubagentAwaitTimeout(path, 30); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSubagentMaxDepth(path, -1); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	if !strings.Contains(text, "# capacity comment") || !strings.Contains(text, "max_concurrency: 4") || !strings.Contains(text, "max_wall_clock: 1800s") || !strings.Contains(text, "max_depth: -1") || !strings.Contains(text, "await_timeout: 30s") {
		t.Fatalf("updated config:\n%s", updated)
	}
	if err := UpdateShellMaxWallClock(path, 30); err == nil {
		t.Fatal("too-short shell wall clock was accepted")
	}
	if err := UpdateShellMaxConcurrency(path, 0); err == nil {
		t.Fatal("zero shell concurrency was accepted")
	}
	if err := UpdateSubagentAwaitTimeout(path, 0); err != nil {
		t.Fatalf("wait-until-complete await timeout was rejected: %v", err)
	}
	updated, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "await_timeout: 0s") {
		t.Fatalf("zero await timeout was not persisted:\n%s", updated)
	}
	if err := UpdateSubagentAwaitTimeout(path, 4); err == nil {
		t.Fatal("too-short await timeout was accepted")
	}
	if err := UpdateSubagentAwaitTimeout(path, -1); err == nil {
		t.Fatal("negative await timeout was accepted")
	}
	if err := UpdateSubagentIdleTimeout(path, 300); err != nil {
		t.Fatal(err)
	}
	updated, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "idle_timeout: 300s") {
		t.Fatalf("idle timeout was not persisted:\n%s", updated)
	}
	if err := UpdateSubagentIdleTimeout(path, 0); err != nil {
		t.Fatalf("disabled idle timeout was rejected: %v", err)
	}
	if err := UpdateSubagentIdleTimeout(path, 10); err == nil {
		t.Fatal("too-short idle timeout was accepted")
	}
	if err := UpdateSubagentIdleTimeout(path, -1); err == nil {
		t.Fatal("negative idle timeout was accepted")
	}
	if err := UpdateSubagentMaxDepth(path, -2); err == nil {
		t.Fatal("invalid recursive depth was accepted")
	}
}

func TestUpdateChatGPTFastModePersistsBoolean(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("# keep\nversion: 1\nproviders:\n  chatgpt:\n    enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateChatGPTFastMode(path, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Providers.ChatGPT.FastMode {
		t.Fatal("ChatGPT fast mode was not persisted")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# keep") || !strings.Contains(string(data), "fast_mode: true") {
		t.Fatalf("updated config =\n%s", data)
	}
}
