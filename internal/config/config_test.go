package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestLanguageDefaultAndValidation(t *testing.T) {
	cfg := Default()
	if cfg.Defaults.Language != "en" {
		t.Fatalf("language = %q", cfg.Defaults.Language)
	}
	if cfg.Defaults.ApprovalMode != "prompt" {
		t.Fatalf("approval mode = %q", cfg.Defaults.ApprovalMode)
	}
	cfg.Defaults.Language = "zh-CN"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Defaults.Language = "zh"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unsupported language accepted")
	}
	cfg = Default()
	cfg.Defaults.ApprovalMode = "unsafe"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unsupported approval mode accepted")
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
	if cfg.Defaults.Language != "zh-CN" || cfg.Defaults.ApprovalMode != "yolo" || cfg.Defaults.Provider != "grok" || !cfg.Workspace.AllowWrite {
		t.Fatalf("persisted config = %#v", cfg)
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

func TestModelRouteValidationAndCompactionLoad(t *testing.T) {
	cfg := Default()
	cfg.Agents.Compaction = ModelRouteConfig{Provider: "chatgpt", Model: "gpt-test"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid compaction route: %v", err)
	}
	for _, route := range []ModelRouteConfig{
		{Provider: "chatgpt"},
		{Model: "gpt-test"},
		{Reasoning: "high"},
		{Provider: "other", Model: "model"},
	} {
		cfg := Default()
		cfg.Agents.Compaction = route
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted invalid route %#v", route)
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nagents:\n  compaction:\n    provider: grok\n    model: grok-4\n    reasoning: high\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agents.Compaction != (ModelRouteConfig{Provider: "grok", Model: "grok-4", Reasoning: "high"}) {
		t.Fatalf("compaction route = %#v", loaded.Agents.Compaction)
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

func TestResolveReferenceRequiresExplicitScheme(t *testing.T) {
	if _, err := ResolveReference("literal-secret", os.LookupEnv, nil); err == nil {
		t.Fatal("ResolveReference accepted a literal secret")
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
	if cfg.Agents.Main.MaxTokens != 0 || cfg.Agents.Main.MaxToolCalls != 0 || cfg.Agents.Main.MaxWallClockDuration != 0 {
		t.Fatalf("main agent budget = %#v", cfg.Agents.Main)
	}
	subagents := cfg.Agents.Subagents
	if !subagents.Enabled || subagents.MaxDepth != 1 || subagents.MaxConcurrency != 2 ||
		subagents.AwaitDuration != 10*time.Minute || !subagents.AutoWake {
		t.Fatalf("subagent defaults = %#v", subagents)
	}
	if subagents.Budget.MaxTokens != 0 || subagents.Budget.MaxToolCalls != 64 ||
		subagents.Budget.MaxTurns != 32 || subagents.Budget.MaxWallClockDuration != 20*time.Minute {
		t.Fatalf("subagent budget = %#v", subagents.Budget)
	}
	for _, name := range []string{"general-purpose", "explore", "plan", "review", "verify"} {
		if _, ok := subagents.Roles[name]; !ok {
			t.Fatalf("built-in role %q is missing", name)
		}
	}

	invalid := Default()
	invalid.Agents.Subagents.MaxDepth = 2
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid max_depth was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.AwaitTimeout = "30m"
	if err := invalid.Validate(); err == nil {
		t.Fatal("await timeout beyond wall-clock budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.Budget.MaxTokens = -1
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative subagent token budget was accepted")
	}
	invalid = Default()
	invalid.Agents.Subagents.Budget.MaxToolCalls = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("zero tool-call budget was accepted")
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
	user := filepath.Join(home, ".config", "azem", "agents")
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
