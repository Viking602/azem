package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const CurrentVersion = 1

var mcpServerNamePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

type Config struct {
	Version   int             `yaml:"version"`
	Defaults  DefaultsConfig  `yaml:"defaults"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Auth      AuthConfig      `yaml:"auth"`
	Providers ProvidersConfig `yaml:"providers"`
	Agents    AgentsConfig    `yaml:"agents"`
	MCP       MCPConfig       `yaml:"mcp"`
	Skills    SkillsConfig    `yaml:"skills"`
	Hooks     HooksConfig     `yaml:"hooks"`
}

type HooksConfig struct {
	Enabled              bool          `yaml:"enabled"`
	TrustProject         bool          `yaml:"trust_project"`
	ClaudeCompatibility  bool          `yaml:"claude_compatibility"`
	DefaultTimeout       string        `yaml:"default_timeout"`
	DefaultTimeoutParsed time.Duration `yaml:"-"`
	FailurePolicy        string        `yaml:"failure_policy"`
	AdditionalPaths      []string      `yaml:"additional_paths,omitempty"`
}

type DefaultsConfig struct {
	Provider     string `yaml:"provider"`
	Model        string `yaml:"model"`
	Reasoning    string `yaml:"reasoning"`
	AgentMode    string `yaml:"agent_mode"`
	Theme        string `yaml:"theme"`
	Language     string `yaml:"language"`
	ApprovalMode string `yaml:"approval_mode"`
}

type WorkspaceConfig struct {
	Root         string `yaml:"root,omitempty"`
	AllowWrite   bool   `yaml:"allow_write"`
	ShellPolicy  string `yaml:"shell_policy"`
	AllowNetwork string `yaml:"allow_network"`
}

type AuthConfig struct {
	Store       string `yaml:"store"`
	ImportCodex bool   `yaml:"import_codex"`
	ImportGrok  bool   `yaml:"import_grok"`
}

type ProvidersConfig struct {
	ChatGPT ProviderConfig `yaml:"chatgpt"`
	Grok    GrokConfig     `yaml:"grok"`
}

type ProviderConfig struct {
	Enabled    bool          `yaml:"enabled"`
	CatalogTTL time.Duration `yaml:"-"`
	TTL        string        `yaml:"catalog_ttl"`
}

type GrokConfig struct {
	ProviderConfig    `yaml:",inline"`
	ExperimentalOAuth bool   `yaml:"experimental_oauth"`
	Transport         string `yaml:"transport"`
}

type AgentsConfig struct {
	Main       MainAgentConfig  `yaml:"main"`
	Team       TeamConfig       `yaml:"team"`
	Compaction ModelRouteConfig `yaml:"compaction" json:"compaction"`
	Context    ContextConfig    `yaml:"context"`
	Subagents  SubagentConfig   `yaml:"subagents"`
}

type ContextConfig struct {
	Enabled                bool    `yaml:"enabled"`
	SoftTriggerRatio       float64 `yaml:"soft_trigger_ratio"`
	HardTriggerRatio       float64 `yaml:"hard_trigger_ratio"`
	TargetRatio            float64 `yaml:"target_ratio"`
	BackgroundPrepare      bool    `yaml:"background_prepare"`
	SafetyMarginRatio      float64 `yaml:"safety_margin_ratio"`
	ReserveOutputTokens    int     `yaml:"reserve_output_tokens"`
	ReserveReasoningTokens int     `yaml:"reserve_reasoning_tokens"`
	MinReclaimTokens       int     `yaml:"min_reclaim_tokens"`
	MaxSummaryTokens       int     `yaml:"max_summary_tokens"`
	LargeToolResultTokens  int     `yaml:"large_tool_result_tokens"`
	HistoryRetrievalTokens int     `yaml:"history_retrieval_tokens"`
}

// ModelRouteConfig selects a provider model for a specific agent operation.
// Its zero value inherits the route from the surrounding/default context.
type ModelRouteConfig struct {
	Provider  string `yaml:"provider,omitempty" json:"provider,omitempty"`
	Model     string `yaml:"model,omitempty" json:"model,omitempty"`
	Reasoning string `yaml:"reasoning,omitempty" json:"reasoning,omitempty"`
}

type MainAgentConfig struct {
	// MaxTokens optionally limits cumulative provider-reported usage for one
	// user turn. Coding runs default to zero so context compaction, rather than
	// cumulative token usage, governs long tasks. A positive value is checked
	// between requests, so the final provider request can exceed it.
	MaxTokens int64 `yaml:"max_tokens"`
	// MaxToolCalls optionally limits tool calls in one user turn. Coding runs
	// default to zero so they can continue until the task is complete.
	MaxToolCalls int `yaml:"max_tool_calls"`
	// MaxWallClock optionally limits the total duration of one user turn. Coding
	// runs default to zero, which is unbounded.
	MaxWallClock         string        `yaml:"max_wall_clock"`
	MaxWallClockDuration time.Duration `yaml:"-"`
}

type SkillsConfig struct {
	Enabled        bool     `yaml:"enabled"`
	TrustProject   bool     `yaml:"trust_project"`
	AdditionalDirs []string `yaml:"additional_dirs,omitempty"`
	Eager          []string `yaml:"eager,omitempty"`
	Disabled       []string `yaml:"disabled,omitempty"`
}

type TeamConfig struct {
	MaxConcurrency int `yaml:"max_concurrency"`
	MaxTicks       int `yaml:"max_ticks"`
}

type SubagentConfig struct {
	Enabled        bool                             `yaml:"enabled"`
	MaxDepth       int                              `yaml:"max_depth"`
	MaxConcurrency int                              `yaml:"max_concurrency"`
	AwaitTimeout   string                           `yaml:"await_timeout"`
	AwaitDuration  time.Duration                    `yaml:"-"`
	AutoWake       bool                             `yaml:"auto_wake"`
	Toggle         map[string]bool                  `yaml:"toggle,omitempty"`
	Models         map[string]string                `yaml:"models,omitempty"`
	Routes         map[string]ModelRouteConfig      `yaml:"routes,omitempty"`
	Roles          map[string]SubagentRoleConfig    `yaml:"roles,omitempty"`
	Personas       map[string]SubagentPersonaConfig `yaml:"personas,omitempty"`
	Budget         SubagentBudgetConfig             `yaml:"budget"`
}

type SubagentBudgetConfig struct {
	// MaxTokens optionally limits cumulative provider-reported usage for one
	// subagent run. It defaults to zero so subagents can finish their assigned
	// coding task. A positive value is checked between requests and can be
	// exceeded by the final provider request.
	MaxTokens int `yaml:"max_tokens"`
	// MaxToolCalls, MaxTurns, and MaxWallClock are optional limits. They all
	// default to zero so coding subagents can run until completion.
	MaxToolCalls         int           `yaml:"max_tool_calls"`
	MaxTurns             int           `yaml:"max_turns"`
	MaxWallClock         string        `yaml:"max_wall_clock"`
	MaxWallClockDuration time.Duration `yaml:"-"`
}

type SubagentRoleConfig struct {
	Description      string   `json:"description,omitempty" toml:"description" yaml:"description"`
	Instructions     string   `json:"instructions,omitempty" toml:"instructions" yaml:"instructions"`
	InstructionsFile string   `json:"instructions_file,omitempty" toml:"instructions_file" yaml:"instructions_file"`
	Persona          string   `json:"persona,omitempty" toml:"persona" yaml:"persona"`
	Provider         string   `json:"provider,omitempty" toml:"provider" yaml:"provider"`
	Model            string   `json:"model,omitempty" toml:"model" yaml:"model"`
	Reasoning        string   `json:"reasoning,omitempty" toml:"reasoning" yaml:"reasoning"`
	CapabilityMode   string   `json:"capability_mode,omitempty" toml:"capability_mode" yaml:"capability_mode"`
	Isolation        string   `json:"isolation,omitempty" toml:"isolation" yaml:"isolation"`
	Tools            []string `json:"tools,omitempty" toml:"tools" yaml:"tools,omitempty"`
	Source           string   `json:"source,omitempty" toml:"-" yaml:"-"`
}

type SubagentPersonaConfig struct {
	Description      string                 `json:"description,omitempty" toml:"description" yaml:"description"`
	Instructions     string                 `json:"instructions,omitempty" toml:"instructions" yaml:"instructions"`
	InstructionsFile string                 `json:"instructions_file,omitempty" toml:"instructions_file" yaml:"instructions_file"`
	Provider         string                 `json:"provider,omitempty" toml:"provider" yaml:"provider"`
	Model            string                 `json:"model,omitempty" toml:"model" yaml:"model"`
	Reasoning        string                 `json:"reasoning,omitempty" toml:"reasoning" yaml:"reasoning"`
	Isolation        string                 `json:"isolation,omitempty" toml:"isolation" yaml:"isolation"`
	Inputs           []SubagentContractItem `json:"inputs,omitempty" toml:"inputs" yaml:"inputs,omitempty"`
	Outputs          []SubagentContractItem `json:"outputs,omitempty" toml:"outputs" yaml:"outputs,omitempty"`
	Source           string                 `json:"source,omitempty" toml:"-" yaml:"-"`
}

type SubagentContractItem struct {
	Name        string `json:"name" toml:"name" yaml:"name"`
	Type        string `json:"type" toml:"type" yaml:"type"`
	Required    bool   `json:"required,omitempty" toml:"required" yaml:"required"`
	Description string `json:"description,omitempty" toml:"description" yaml:"description"`
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `yaml:"servers"`
}

type MCPServerConfig struct {
	Enabled         bool                    `yaml:"enabled"`
	Transport       string                  `yaml:"transport"`
	Command         string                  `yaml:"command,omitempty"`
	Args            []string                `yaml:"args,omitempty"`
	CWD             string                  `yaml:"cwd,omitempty"`
	InheritEnv      bool                    `yaml:"inherit_env"`
	Env             map[string]string       `yaml:"env,omitempty"`
	URL             string                  `yaml:"url,omitempty"`
	Headers         map[string]string       `yaml:"headers,omitempty"`
	ConnectTimeout  string                  `yaml:"connect_timeout"`
	CallTimeout     string                  `yaml:"call_timeout"`
	MaxConcurrency  int                     `yaml:"max_concurrency"`
	Approval        string                  `yaml:"approval"`
	ToolOverrides   map[string]ToolOverride `yaml:"tool_overrides,omitempty"`
	ConnectDuration time.Duration           `yaml:"-"`
	CallDuration    time.Duration           `yaml:"-"`
}

type ToolOverride struct {
	Effect   string `yaml:"effect"`
	Approval string `yaml:"approval"`
}

func Default() Config {
	return Config{
		Version: CurrentVersion,
		Defaults: DefaultsConfig{
			Provider: "chatgpt", Model: "gpt-5.6-sol", Reasoning: "high", AgentMode: "single", Theme: "system", Language: "en", ApprovalMode: "prompt",
		},
		Workspace: WorkspaceConfig{AllowWrite: true, ShellPolicy: "prompt", AllowNetwork: "prompt"},
		Auth:      AuthConfig{Store: "sqlite", ImportCodex: true, ImportGrok: true},
		Providers: ProvidersConfig{
			ChatGPT: ProviderConfig{Enabled: true, TTL: "5m", CatalogTTL: 5 * time.Minute},
			Grok:    GrokConfig{ProviderConfig: ProviderConfig{Enabled: true, TTL: "5m", CatalogTTL: 5 * time.Minute}, ExperimentalOAuth: true, Transport: "api"},
		},
		Agents: AgentsConfig{
			Main: MainAgentConfig{MaxTokens: 0, MaxToolCalls: 0, MaxWallClock: "0s"},
			Team: TeamConfig{MaxConcurrency: 2, MaxTicks: 12},
			Context: ContextConfig{Enabled: true, SoftTriggerRatio: .68, HardTriggerRatio: .82, TargetRatio: .45, BackgroundPrepare: true, SafetyMarginRatio: .08,
				ReserveOutputTokens: 16384, ReserveReasoningTokens: 8192, MinReclaimTokens: 16000,
				MaxSummaryTokens: 4096, LargeToolResultTokens: 12000, HistoryRetrievalTokens: 4096},
			Subagents: SubagentConfig{
				Enabled: true, MaxDepth: 1, MaxConcurrency: 2, AwaitTimeout: "10m", AwaitDuration: 10 * time.Minute, AutoWake: true,
				Toggle: map[string]bool{}, Models: map[string]string{}, Routes: map[string]ModelRouteConfig{}, Roles: builtInSubagentRoles(),
				Personas: map[string]SubagentPersonaConfig{},
				Budget: SubagentBudgetConfig{
					MaxTokens: 0, MaxToolCalls: 0, MaxTurns: 0,
					MaxWallClock: "0s",
				},
			},
		},
		Skills: SkillsConfig{Enabled: true, TrustProject: false},
		Hooks: HooksConfig{
			Enabled: true, ClaudeCompatibility: false, DefaultTimeout: "5s",
			DefaultTimeoutParsed: 5 * time.Second, FailurePolicy: "open",
		},
		MCP: MCPConfig{Servers: map[string]MCPServerConfig{}},
	}
}

func builtInSubagentRoles() map[string]SubagentRoleConfig {
	readOnly := []string{"coding.list_files", "coding.read_file", "coding.search", "coding.git_diff"}
	all := append(append([]string(nil), readOnly...), "coding.edit_hashline", "coding.write_file", "coding.gofmt", "coding.go_test", "coding.shell")
	execute := append(append([]string(nil), readOnly...), "coding.go_test", "coding.shell")
	return map[string]SubagentRoleConfig{
		"general-purpose": {
			Description:    "Handle delegated coding work with the full governed tool set.",
			Instructions:   "Complete the delegated task, preserve user work, and return a concise verified result.",
			CapabilityMode: "all", Isolation: "none", Tools: all, Source: "builtin",
		},
		"explore": {
			Description:    "Investigate the workspace and return file-backed evidence.",
			Instructions:   "Investigate without modifying files. Return concise findings with file and line evidence.",
			CapabilityMode: "read-only", Isolation: "none", Tools: append([]string(nil), readOnly...), Source: "builtin",
		},
		"plan": {
			Description:    "Develop a concrete implementation plan without modifying files.",
			Instructions:   "Inspect the workspace as needed and return a decision-complete plan. Do not modify files.",
			CapabilityMode: "read-only", Isolation: "none", Tools: append([]string(nil), readOnly...), Source: "builtin",
		},
		"review": {
			Description:    "Review implementation quality and correctness without modifying files.",
			Instructions:   "Review the delegated change against its requirements. Return findings with concrete evidence.",
			CapabilityMode: "read-only", Isolation: "none", Tools: append([]string(nil), readOnly...), Source: "builtin",
		},
		"verify": {
			Description:    "Run governed verification and report exact outcomes.",
			Instructions:   "Verify the delegated behavior with inspection and governed commands. Do not edit files.",
			CapabilityMode: "execute", Isolation: "none", Tools: execute, Source: "builtin",
		},
	}
}

func (c *Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("config version %d is unsupported (want %d)", c.Version, CurrentVersion)
	}
	if err := c.validateSkills(); err != nil {
		return err
	}
	timeout, err := time.ParseDuration(c.Hooks.DefaultTimeout)
	if err != nil || timeout <= 0 {
		return fmt.Errorf("hooks.default_timeout must be a positive duration")
	}
	c.Hooks.DefaultTimeoutParsed = timeout
	if c.Hooks.FailurePolicy != "open" && c.Hooks.FailurePolicy != "closed" {
		return fmt.Errorf("hooks.failure_policy must be open or closed")
	}
	if c.Defaults.AgentMode != "single" && c.Defaults.AgentMode != "team" {
		return fmt.Errorf("defaults.agent_mode must be single or team")
	}
	if c.Defaults.Language != "en" && c.Defaults.Language != "zh-CN" {
		return fmt.Errorf("defaults.language must be en or zh-CN")
	}
	if c.Defaults.ApprovalMode != "prompt" && c.Defaults.ApprovalMode != "auto_review" && c.Defaults.ApprovalMode != "yolo" {
		return fmt.Errorf("defaults.approval_mode must be prompt, auto_review, or yolo")
	}
	if c.Auth.Store != "sqlite" && c.Auth.Store != "keyring" && c.Auth.Store != "file" {
		return fmt.Errorf("auth.store must be sqlite, keyring, or file")
	}
	if c.Workspace.ShellPolicy != "prompt" && c.Workspace.ShellPolicy != "deny" && c.Workspace.ShellPolicy != "allow" {
		return fmt.Errorf("workspace.shell_policy must be prompt, deny, or allow")
	}
	if c.Workspace.AllowNetwork != "prompt" && c.Workspace.AllowNetwork != "deny" && c.Workspace.AllowNetwork != "allow" {
		return fmt.Errorf("workspace.allow_network must be prompt, deny, or allow")
	}
	for name, provider := range map[string]*ProviderConfig{"chatgpt": &c.Providers.ChatGPT, "grok": &c.Providers.Grok.ProviderConfig} {
		ttl, err := time.ParseDuration(provider.TTL)
		if err != nil || ttl <= 0 {
			return fmt.Errorf("providers.%s.catalog_ttl must be a positive duration", name)
		}
		provider.CatalogTTL = ttl
	}
	if c.Providers.Grok.Transport != "api" && c.Providers.Grok.Transport != "cli_proxy" {
		return fmt.Errorf("providers.grok.transport must be api or cli_proxy")
	}
	if c.Agents.Main.MaxTokens < 0 {
		return fmt.Errorf("agents.main.max_tokens must be non-negative (zero is unbounded)")
	}
	if c.Agents.Main.MaxToolCalls < 0 {
		return fmt.Errorf("agents.main.max_tool_calls must be non-negative (zero is unbounded)")
	}
	mainWallClock, err := time.ParseDuration(c.Agents.Main.MaxWallClock)
	if err != nil || mainWallClock < 0 {
		return fmt.Errorf("agents.main.max_wall_clock must be a non-negative duration (zero is unbounded)")
	}
	c.Agents.Main.MaxWallClockDuration = mainWallClock
	if c.Agents.Team.MaxConcurrency < 1 || c.Agents.Team.MaxTicks < 1 {
		return fmt.Errorf("agents.team limits must be positive")
	}
	if err := validateModelRoute("agents.compaction", c.Agents.Compaction); err != nil {
		return err
	}
	contextConfig := c.Agents.Context
	if contextConfig.TargetRatio <= 0 || contextConfig.SoftTriggerRatio <= contextConfig.TargetRatio || contextConfig.HardTriggerRatio <= contextConfig.SoftTriggerRatio || contextConfig.HardTriggerRatio >= 1 || contextConfig.SafetyMarginRatio < 0 || contextConfig.SafetyMarginRatio >= 1 || contextConfig.HardTriggerRatio+contextConfig.SafetyMarginRatio > 1 {
		return fmt.Errorf("agents.context ratios must satisfy 0 < target_ratio < soft_trigger_ratio < hard_trigger_ratio < 1 and hard_trigger_ratio+safety_margin_ratio <= 1")
	}
	if contextConfig.ReserveOutputTokens < 0 || contextConfig.ReserveReasoningTokens < 0 || contextConfig.MinReclaimTokens < 0 || contextConfig.MaxSummaryTokens <= 0 || contextConfig.LargeToolResultTokens <= 0 || contextConfig.HistoryRetrievalTokens <= 0 {
		return fmt.Errorf("agents.context token limits must be non-negative and summary/tool limits positive")
	}
	if err := c.validateSubagents(); err != nil {
		return err
	}
	for name, server := range c.MCP.Servers {
		if !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("mcp server name %q must match [a-z0-9_-]+", name)
		}
		if server.ConnectTimeout == "" {
			server.ConnectTimeout = "30s"
		}
		if server.CallTimeout == "" {
			server.CallTimeout = "60s"
		}
		if server.MaxConcurrency == 0 {
			server.MaxConcurrency = 2
		}
		if server.Approval == "" {
			server.Approval = "always"
		}
		var err error
		server.ConnectDuration, err = time.ParseDuration(server.ConnectTimeout)
		if err != nil || server.ConnectDuration <= 0 {
			return fmt.Errorf("mcp.servers.%s.connect_timeout must be a positive duration", name)
		}
		server.CallDuration, err = time.ParseDuration(server.CallTimeout)
		if err != nil || server.CallDuration <= 0 {
			return fmt.Errorf("mcp.servers.%s.call_timeout must be a positive duration", name)
		}
		if server.MaxConcurrency < 1 {
			return fmt.Errorf("mcp.servers.%s.max_concurrency must be positive", name)
		}
		if server.Approval != "always" && server.Approval != "never" {
			return fmt.Errorf("mcp.servers.%s.approval must be always or never", name)
		}
		switch server.Transport {
		case "stdio":
			if strings.TrimSpace(server.Command) == "" {
				return fmt.Errorf("mcp.servers.%s.command is required for stdio", name)
			}
		case "streamable_http":
			endpoint, parseErr := url.Parse(server.URL)
			if parseErr != nil || endpoint.Host == "" {
				return fmt.Errorf("mcp.servers.%s.url is invalid", name)
			}
			if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && isLoopbackHost(endpoint.Hostname())) {
				return fmt.Errorf("mcp.servers.%s.url must use https (http is allowed only for localhost)", name)
			}
		default:
			return fmt.Errorf("mcp.servers.%s.transport must be stdio or streamable_http", name)
		}
		for key, reference := range server.Env {
			if err := validateSecretReference(reference); err != nil {
				return fmt.Errorf("mcp.servers.%s.env.%s: %w", name, key, err)
			}
		}
		for key, reference := range server.Headers {
			if err := validateSecretReference(reference); err != nil {
				return fmt.Errorf("mcp.servers.%s.headers.%s: %w", name, key, err)
			}
		}
		for toolName, override := range server.ToolOverrides {
			if strings.TrimSpace(toolName) == "" {
				return fmt.Errorf("mcp.servers.%s.tool_overrides contains an empty tool name", name)
			}
			if override.Effect != "read_only" && override.Effect != "write" && override.Effect != "external_side_effect" {
				return fmt.Errorf("mcp.servers.%s.tool_overrides.%s.effect is invalid", name, toolName)
			}
			if override.Approval != "always" && override.Approval != "never" {
				return fmt.Errorf("mcp.servers.%s.tool_overrides.%s.approval must be always or never", name, toolName)
			}
		}
		c.MCP.Servers[name] = server
	}
	if runtime.GOOS == "js" && c.Auth.Store == "keyring" {
		return fmt.Errorf("keyring credential storage is unavailable on js")
	}
	return nil
}

func (c *Config) validateSkills() error {
	if len(c.Skills.AdditionalDirs) > 56 {
		return fmt.Errorf("skills.additional_dirs must contain at most 56 entries")
	}
	eager := make(map[string]struct{}, len(c.Skills.Eager))
	for _, name := range c.Skills.Eager {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("skills.eager contains an empty skill name")
		}
		if _, exists := eager[name]; exists {
			return fmt.Errorf("skills.eager contains duplicate skill %q", name)
		}
		eager[name] = struct{}{}
	}
	disabled := make(map[string]struct{}, len(c.Skills.Disabled))
	for _, name := range c.Skills.Disabled {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("skills.disabled contains an empty skill name")
		}
		if _, exists := disabled[name]; exists {
			return fmt.Errorf("skills.disabled contains duplicate skill %q", name)
		}
		if _, exists := eager[name]; exists {
			return fmt.Errorf("skill %q cannot be both eager and disabled", name)
		}
		disabled[name] = struct{}{}
	}
	return nil
}

func (c *Config) validateSubagents() error {
	subagents := &c.Agents.Subagents
	if subagents.MaxDepth != 1 || subagents.MaxConcurrency < 1 {
		return fmt.Errorf("agents.subagents.max_depth must be 1 and max_concurrency positive")
	}
	await, err := time.ParseDuration(subagents.AwaitTimeout)
	if err != nil || await <= 0 {
		return fmt.Errorf("agents.subagents.await_timeout must be a positive duration")
	}
	wallClock, err := time.ParseDuration(subagents.Budget.MaxWallClock)
	if err != nil || wallClock < 0 {
		return fmt.Errorf("agents.subagents.budget.max_wall_clock must be a non-negative duration (zero is unbounded)")
	}
	if subagents.Budget.MaxTokens < 0 {
		return fmt.Errorf("agents.subagents.budget.max_tokens must be non-negative (zero is unbounded)")
	}
	if subagents.Budget.MaxToolCalls < 0 || subagents.Budget.MaxTurns < 0 {
		return fmt.Errorf("agents.subagents tool-call and turn budgets must be non-negative (zero is unbounded)")
	}
	if wallClock > 0 && await > wallClock {
		return fmt.Errorf("agents.subagents.await_timeout must not exceed budget.max_wall_clock")
	}
	subagents.AwaitDuration = await
	subagents.Budget.MaxWallClockDuration = wallClock
	if subagents.Toggle == nil {
		subagents.Toggle = map[string]bool{}
	}
	if subagents.Models == nil {
		subagents.Models = map[string]string{}
	}
	if subagents.Routes == nil {
		subagents.Routes = map[string]ModelRouteConfig{}
	}
	if subagents.Roles == nil {
		subagents.Roles = map[string]SubagentRoleConfig{}
	}
	if subagents.Personas == nil {
		subagents.Personas = map[string]SubagentPersonaConfig{}
	}
	allowedTools := map[string]bool{
		"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true,
		"coding.edit_hashline": true, "coding.write_file": true, "coding.gofmt": true,
		"coding.go_test": true, "coding.shell": true,
	}
	for name, persona := range subagents.Personas {
		if !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("agents.subagents persona %q must match [a-z0-9_-]+", name)
		}
		if strings.TrimSpace(persona.Instructions) == "" {
			return fmt.Errorf("agents.subagents persona %q has no instructions", name)
		}
		if err := validateInheritedModelRoute("agents.subagents persona "+fmt.Sprintf("%q", name), ModelRouteConfig{Provider: persona.Provider, Model: persona.Model, Reasoning: persona.Reasoning}); err != nil {
			return err
		}
		if err := validateSubagentContractItems("persona", name, "input", persona.Inputs); err != nil {
			return err
		}
		if err := validateSubagentContractItems("persona", name, "output", persona.Outputs); err != nil {
			return err
		}
		if persona.Isolation != "" && persona.Isolation != "none" && persona.Isolation != "worktree" {
			return fmt.Errorf("agents.subagents persona %q isolation must be none or worktree", name)
		}
	}
	readOnlyTools := []string{"coding.list_files", "coding.read_file", "coding.search", "coding.git_diff"}
	for name, role := range subagents.Roles {
		if !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("agents.subagents role %q must match [a-z0-9_-]+", name)
		}
		if role.CapabilityMode == "" {
			role.CapabilityMode = "read-only"
		}
		if role.Isolation == "" {
			role.Isolation = "none"
		}
		if len(role.Tools) == 0 {
			role.Tools = append([]string(nil), readOnlyTools...)
		}
		if role.CapabilityMode != "read-only" && role.CapabilityMode != "read-write" && role.CapabilityMode != "execute" && role.CapabilityMode != "all" {
			return fmt.Errorf("agents.subagents role %q capability_mode is invalid", name)
		}
		if role.Isolation != "none" && role.Isolation != "worktree" {
			return fmt.Errorf("agents.subagents role %q isolation must be none or worktree", name)
		}
		if role.Persona != "" {
			if _, ok := subagents.Personas[role.Persona]; !ok {
				return fmt.Errorf("agents.subagents role %q references unknown persona %q", name, role.Persona)
			}
		}
		if strings.TrimSpace(role.Instructions) == "" && role.Persona == "" {
			return fmt.Errorf("agents.subagents role %q has no instructions or persona", name)
		}
		if err := validateInheritedModelRoute("agents.subagents role "+fmt.Sprintf("%q", name), ModelRouteConfig{Provider: role.Provider, Model: role.Model, Reasoning: role.Reasoning}); err != nil {
			return err
		}
		for _, toolName := range role.Tools {
			if !allowedTools[toolName] {
				return fmt.Errorf("agents.subagents role %q references unknown or forbidden tool %q", name, toolName)
			}
		}
		if model := strings.TrimSpace(subagents.Models[name]); model != "" {
			role.Model = model
		}
		if route, configured := subagents.Routes[name]; configured {
			if err := validateModelRoute("agents.subagents route "+fmt.Sprintf("%q", name), route); err != nil {
				return err
			}
			role.Provider, role.Model, role.Reasoning = route.Provider, route.Model, route.Reasoning
		}
		subagents.Roles[name] = role
	}
	for name := range subagents.Models {
		if _, ok := subagents.Roles[name]; !ok {
			return fmt.Errorf("agents.subagents.models references unknown role %q", name)
		}
	}
	for name := range subagents.Routes {
		if _, ok := subagents.Roles[name]; !ok {
			return fmt.Errorf("agents.subagents.routes references unknown role %q", name)
		}
	}
	for name := range subagents.Toggle {
		if _, ok := subagents.Roles[name]; !ok {
			return fmt.Errorf("agents.subagents.toggle references unknown role %q", name)
		}
	}
	return nil
}

func validateModelRoute(name string, route ModelRouteConfig) error {
	provider := strings.TrimSpace(route.Provider)
	model := strings.TrimSpace(route.Model)
	reasoning := strings.TrimSpace(route.Reasoning)
	if provider == "" && model == "" && reasoning == "" {
		return nil
	}
	if provider == "" || model == "" {
		return fmt.Errorf("%s route must set both provider and model", name)
	}
	if provider != "chatgpt" && provider != "grok" {
		return fmt.Errorf("%s provider must be chatgpt or grok", name)
	}
	return nil
}

func validateInheritedModelRoute(name string, route ModelRouteConfig) error {
	provider := strings.TrimSpace(route.Provider)
	model := strings.TrimSpace(route.Model)
	if provider != "" && model == "" {
		return fmt.Errorf("%s route must set model when provider is set", name)
	}
	if provider != "" && provider != "chatgpt" && provider != "grok" {
		return fmt.Errorf("%s provider must be chatgpt or grok", name)
	}
	return nil
}

func validateSubagentContractItems(ownerKind, ownerName, contractKind string, items []SubagentContractItem) error {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("agents.subagents %s %q has invalid %s name %q", ownerKind, ownerName, contractKind, item.Name)
		}
		if strings.TrimSpace(item.Type) == "" {
			return fmt.Errorf("agents.subagents %s %q %s %q has no type", ownerKind, ownerName, contractKind, name)
		}
		if seen[name] {
			return fmt.Errorf("agents.subagents %s %q repeats %s %q", ownerKind, ownerName, contractKind, name)
		}
		seen[name] = true
	}
	return nil
}

func validateSecretReference(value string) error {
	kind, name, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(name) == "" || (kind != "env" && kind != "keyring") {
		return fmt.Errorf("secret must use env:NAME or keyring:NAME")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
