package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

const CurrentVersion = 1

var mcpServerNamePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)
var uiLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)

// ValidUILanguage validates a translation-pack identifier, not a fixed language list.
func ValidUILanguage(value string) bool {
	return len(value) <= 64 && uiLanguagePattern.MatchString(value)
}

const (
	maxConfiguredSubagentRoles         = 64
	maxConfiguredSubagentRoleNameBytes = 64
	// DefaultSubagentIdleTimeout cancels a running child that produces no
	// thinking, output, or tool activity. Zero remains a valid explicit
	// disable. Open tools, including approval waits, are not cancelled.
	DefaultSubagentIdleTimeout = 5 * time.Minute
	// DefaultShellMaxWallClock is the per-command coding.shell ceiling when
	// workspace.shell.max_wall_clock is omitted. The model may request less.
	DefaultShellMaxWallClock = 10 * time.Minute
)

func SubscriptionProviderIDs() []string {
	return []string{"chatgpt", "grok", "cursor"}
}

func IsSubscriptionProvider(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "chatgpt", "grok", "cursor":
		return true
	default:
		return false
	}
}

type Config struct {
	Version    int              `yaml:"version"`
	Defaults   DefaultsConfig   `yaml:"defaults"`
	Workspace  WorkspaceConfig  `yaml:"workspace"`
	Auth       AuthConfig       `yaml:"auth"`
	Providers  ProvidersConfig  `yaml:"providers"`
	Agents     AgentsConfig     `yaml:"agents"`
	Security   SecurityConfig   `yaml:"security"`
	MCP        MCPConfig        `yaml:"mcp"`
	Skills     SkillsConfig     `yaml:"skills"`
	Plugins    PluginsConfig    `yaml:"plugins"`
	Hooks      HooksConfig      `yaml:"hooks"`
	Retry      RetryConfig      `yaml:"retry"`
	Discovery  DiscoveryConfig  `yaml:"discovery" json:"discovery"`
	Extensions ExtensionsConfig `yaml:"extensions" json:"extensions"`
	AutoLearn  AutoLearnConfig  `yaml:"autolearn" json:"autolearn"`
	TTSR       TTSRConfig       `yaml:"ttsr" json:"ttsr"`
}

type RetryConfig struct {
	Enabled           bool          `yaml:"enabled"`
	MaxRetries        int           `yaml:"max_retries"`
	BaseDelay         string        `yaml:"base_delay"`
	BaseDelayDuration time.Duration `yaml:"-"`
	MaxDelay          string        `yaml:"max_delay"`
	MaxDelayDuration  time.Duration `yaml:"-"`
}

type HooksConfig struct {
	Enabled              bool          `yaml:"enabled"`
	TrustProject         bool          `yaml:"trust_project"`
	ClaudeCompatibility  bool          `yaml:"claude_compatibility"`
	DefaultTimeout       string        `yaml:"default_timeout"`
	DefaultTimeoutParsed time.Duration `yaml:"-"`
	FailurePolicy        string        `yaml:"failure_policy"`
	AdditionalPaths      []string      `yaml:"additional_paths,omitempty"`
	Disabled             []string      `yaml:"disabled,omitempty"`
}
type TTSRConfig struct {
	Enabled       bool               `yaml:"enabled" json:"enabled"`
	ContextMode   string             `yaml:"context_mode" json:"contextMode"`
	InterruptMode string             `yaml:"interrupt_mode" json:"interruptMode"`
	RepeatMode    string             `yaml:"repeat_mode" json:"repeatMode"`
	RepeatGap     int                `yaml:"repeat_gap" json:"repeatGap"`
	Rules         []StreamRuleConfig `yaml:"rules,omitempty" json:"rules,omitempty"`
}

type StreamRuleConfig struct {
	Name          string   `yaml:"name" json:"name"`
	Content       string   `yaml:"content" json:"content"`
	Conditions    []string `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	ASTConditions []string `yaml:"ast_conditions,omitempty" json:"astConditions,omitempty"`
	Scope         []string `yaml:"scope,omitempty" json:"scope,omitempty"`
	Globs         []string `yaml:"globs,omitempty" json:"globs,omitempty"`
	InterruptMode string   `yaml:"interrupt_mode,omitempty" json:"interruptMode,omitempty"`
}
type DiscoveryConfig struct {
	ContextFiles           bool     `yaml:"context_files" json:"contextFiles"`
	Rules                  bool     `yaml:"rules" json:"rules"`
	Skills                 bool     `yaml:"skills" json:"skills"`
	MCP                    bool     `yaml:"mcp" json:"mcp"`
	Hooks                  bool     `yaml:"hooks" json:"hooks"`
	DisabledProviders      []string `yaml:"disabled_providers,omitempty" json:"disabledProviders,omitempty"`
	DisabledRules          []string `yaml:"disabled_rules,omitempty" json:"disabledRules,omitempty"`
	AdditionalContextFiles []string `yaml:"additional_context_files,omitempty" json:"additionalContextFiles,omitempty"`
}

type ExtensionsConfig struct {
	Enabled                  bool     `yaml:"enabled" json:"enabled"`
	TrustProjectCode         bool     `yaml:"trust_project_code" json:"trustProjectCode"`
	AdditionalToolPaths      []string `yaml:"additional_tool_paths,omitempty" json:"additionalToolPaths,omitempty"`
	AdditionalCommandDirs    []string `yaml:"additional_command_dirs,omitempty" json:"additionalCommandDirs,omitempty"`
	AdditionalExtensionPaths []string `yaml:"additional_extension_paths,omitempty" json:"additionalExtensionPaths,omitempty"`
	AdditionalAgentDirs      []string `yaml:"additional_agent_dirs,omitempty" json:"additionalAgentDirs,omitempty"`
	AdditionalThemeDirs      []string `yaml:"additional_theme_dirs,omitempty" json:"additionalThemeDirs,omitempty"`
}
type AutoLearnConfig struct {
	Enabled      bool `yaml:"enabled" json:"enabled"`
	AutoContinue bool `yaml:"auto_continue" json:"autoContinue"`
	MinToolCalls int  `yaml:"min_tool_calls" json:"minToolCalls"`
}

type DefaultsConfig struct {
	Provider     string `yaml:"provider"`
	Model        string `yaml:"model"`
	Reasoning    string `yaml:"reasoning"`
	AgentMode    string `yaml:"agent_mode"`
	Theme        string `yaml:"theme"`
	Language     string `yaml:"language"`
	ApprovalMode string `yaml:"approval_mode"`
	QueueMode    string `yaml:"queue_mode"`
}

type WorkspaceConfig struct {
	Root         string      `yaml:"root,omitempty"`
	AllowWrite   bool        `yaml:"allow_write"`
	ShellPolicy  string      `yaml:"shell_policy"`
	AllowNetwork string      `yaml:"allow_network"`
	Shell        ShellConfig `yaml:"shell"`
}

type ShellConfig struct {
	MaxContextOutputBytes  int           `yaml:"max_context_output_bytes"`
	MaxArtifactOutputBytes int           `yaml:"max_artifact_output_bytes"`
	StopOnOutputLimit      bool          `yaml:"stop_on_output_limit"`
	MaxConcurrency         int           `yaml:"max_concurrency"`
	MaxWallClock           string        `yaml:"max_wall_clock,omitempty"`
	MaxWallClockDuration   time.Duration `yaml:"-"`
}

type AuthConfig struct {
	Store       string           `yaml:"store"`
	ImportCodex bool             `yaml:"import_codex"`
	ImportGrok  bool             `yaml:"import_grok"`
	Broker      AuthBrokerConfig `yaml:"broker,omitempty" json:"broker"`
}

type AuthBrokerConfig struct {
	URL               string        `yaml:"url,omitempty" json:"url,omitempty"`
	Token             string        `yaml:"token,omitempty" json:"-"`
	SnapshotCache     string        `yaml:"snapshot_cache,omitempty" json:"snapshotCache,omitempty"`
	SnapshotTTL       string        `yaml:"snapshot_ttl,omitempty" json:"snapshotTTL,omitempty"`
	SnapshotTTLParsed time.Duration `yaml:"-" json:"-"`
	AccountPoolFile   string        `yaml:"account_pool_file,omitempty" json:"accountPoolFile,omitempty"`
}

type ProvidersConfig struct {
	ChatGPT ChatGPTConfig                  `yaml:"chatgpt"`
	Grok    GrokConfig                     `yaml:"grok"`
	Cursor  CursorConfig                   `yaml:"cursor"`
	LLMux   map[string]LLMuxProviderConfig `yaml:"llmux,omitempty"`
}

type LLMuxProviderConfig struct {
	Enabled        bool              `yaml:"enabled" json:"enabled"`
	BaseURL        string            `yaml:"base_url,omitempty" json:"baseURL,omitempty"`
	DisplayName    string            `yaml:"display_name,omitempty" json:"displayName,omitempty"`
	Backend        string            `yaml:"backend,omitempty" json:"backend,omitempty"`
	EnvKey         string            `yaml:"env_key,omitempty" json:"envKey,omitempty"`
	AllowEmptyKey  bool              `yaml:"allow_empty_key,omitempty" json:"allowEmptyKey,omitempty"`
	APIKeyHeader   string            `yaml:"api_key_header,omitempty" json:"apiKeyHeader,omitempty"`
	APIKeyPrefix   string            `yaml:"api_key_prefix,omitempty" json:"apiKeyPrefix,omitempty"`
	RuntimeAPIKey  string            `yaml:"-" json:"-"`
	RuntimeHeaders map[string]string `yaml:"-" json:"-"`
	// Models is accepted from legacy YAML on load, then stored in SQLite.
	Models []LLMuxModelConfig `yaml:"models,omitempty" json:"models,omitempty"`
}

type LLMuxModelConfig struct {
	ID               string   `yaml:"id" json:"id"`
	Disabled         bool     `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	Name             string   `yaml:"name,omitempty" json:"name,omitempty"`
	Aliases          []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	Description      string   `yaml:"description,omitempty" json:"description,omitempty"`
	ContextWindow    int      `yaml:"context_window,omitempty" json:"contextWindow"`
	MaxOutputTokens  int      `yaml:"max_output_tokens,omitempty" json:"maxOutputTokens,omitempty"`
	ReasoningLevels  []string `yaml:"reasoning_levels,omitempty" json:"reasoningLevels,omitempty"`
	DefaultReasoning string   `yaml:"default_reasoning,omitempty" json:"defaultReasoning,omitempty"`
	Capabilities     []string `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	InputModalities  []string `yaml:"input_modalities,omitempty" json:"inputModalities,omitempty"`
	OutputModalities []string `yaml:"output_modalities,omitempty" json:"outputModalities,omitempty"`
}

type ProviderConfig struct {
	Enabled        bool          `yaml:"enabled"`
	DisabledModels []string      `yaml:"disabled_models,omitempty"`
	CatalogTTL     time.Duration `yaml:"-"`
	TTL            string        `yaml:"catalog_ttl"`
}

type ChatGPTConfig struct {
	ProviderConfig `yaml:",inline"`
	FastMode       bool `yaml:"fast_mode"`
}

type GrokConfig struct {
	ProviderConfig    `yaml:",inline"`
	ExperimentalOAuth bool   `yaml:"experimental_oauth"`
	Transport         string `yaml:"transport"`
}

type CursorConfig struct {
	ProviderConfig `yaml:",inline"`
}

type AgentsConfig struct {
	Main       MainAgentConfig  `yaml:"main"`
	Team       TeamConfig       `yaml:"team"`
	Title      ModelRouteConfig `yaml:"title" json:"title"`
	Plan       ModelRouteConfig `yaml:"plan" json:"plan"`
	Approval   ModelRouteConfig `yaml:"approval" json:"approval"`
	Vision     ModelRouteConfig `yaml:"vision" json:"vision"`
	Recap      ModelRouteConfig `yaml:"recap" json:"recap"`
	Advisor    AdvisorConfig    `yaml:"advisor" json:"advisor"`
	Context    ContextConfig    `yaml:"context"`
	Vibe       VibeConfig       `yaml:"vibe" json:"vibe"`
	LoopGuards LoopGuardConfig  `yaml:"loop_guards" json:"loopGuards"`
	Subagents  SubagentConfig   `yaml:"subagents"`
}

type VibeConfig struct {
	Fast ModelRouteConfig `yaml:"fast" json:"fast"`
	Good ModelRouteConfig `yaml:"good" json:"good"`
}
type LoopGuardConfig struct {
	ThinkingEnabled       bool     `yaml:"thinking_enabled" json:"thinkingEnabled"`
	AssistantTextEnabled  bool     `yaml:"assistant_text_enabled" json:"assistantTextEnabled"`
	ToolCallEnabled       bool     `yaml:"tool_call_enabled" json:"toolCallEnabled"`
	ToolCallThreshold     int      `yaml:"tool_call_threshold" json:"toolCallThreshold"`
	ToolCallExemptTools   []string `yaml:"tool_call_exempt_tools" json:"toolCallExemptTools"`
	UnexpectedStop        string   `yaml:"unexpected_stop" json:"unexpectedStop"`
	UnexpectedStopRetries int      `yaml:"unexpected_stop_retries" json:"unexpectedStopRetries"`
}

type ContextConfig struct {
	Enabled                bool `yaml:"enabled"`
	ReserveTokens          int  `yaml:"reserve_tokens"`
	KeepRecentTokens       int  `yaml:"keep_recent_tokens"`
	LargeToolResultTokens  int  `yaml:"large_tool_result_tokens"`
	HistoryRetrievalTokens int  `yaml:"history_retrieval_tokens"`
}
type AdvisorConfig struct {
	Enabled                bool          `yaml:"enabled" json:"enabled"`
	Provider               string        `yaml:"provider,omitempty" json:"provider,omitempty"`
	Model                  string        `yaml:"model,omitempty" json:"model,omitempty"`
	Reasoning              string        `yaml:"reasoning,omitempty" json:"reasoning,omitempty"`
	CatchupTimeout         string        `yaml:"catchup_timeout" json:"catchupTimeout"`
	CatchupTimeoutDuration time.Duration `yaml:"-" json:"-"`
	Instructions           string        `yaml:"instructions,omitempty" json:"instructions,omitempty"`
}

func (c AdvisorConfig) Route() ModelRouteConfig {
	return ModelRouteConfig{Provider: c.Provider, Model: c.Model, Reasoning: c.Reasoning}
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
	// user turn. Coding runs default to zero so deterministic context archiving,
	// rather than cumulative token usage, governs long tasks. A positive value
	// is checked between requests, so the final provider request can exceed it.
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

// PluginsConfig controls Azem's own plugin package directory. ImportCodex
// enables discovery while CodexImports is the explicit copy allowlist; runtime
// loading never executes directly from the Codex cache. Hooks remain explicitly
// trusted.
type PluginsConfig struct {
	Enabled               bool     `yaml:"enabled"`
	ImportCodex           bool     `yaml:"import_codex"`
	CodexImports          []string `yaml:"codex_imports,omitempty"`
	TrustHooks            bool     `yaml:"trust_hooks"`
	MarketplaceAutoUpdate string   `yaml:"marketplace_auto_update" json:"marketplaceAutoUpdate"`
}
type SecurityRoutesConfig struct {
	Audit    ModelRouteConfig `yaml:"audit" json:"audit"`
	Reducer  ModelRouteConfig `yaml:"reducer" json:"reducer"`
	Fixer    ModelRouteConfig `yaml:"fixer" json:"fixer"`
	Verifier ModelRouteConfig `yaml:"verifier" json:"verifier"`
}

type SecurityConfig struct {
	Enabled                    bool    `yaml:"enabled" json:"enabled"`
	DefaultMode                string  `yaml:"default_mode" json:"defaultMode"`
	Workers                    int     `yaml:"workers" json:"workers"`
	Subagents                  int     `yaml:"subagents" json:"subagents"`
	StopAfterNoNew             int     `yaml:"stop_after_no_new" json:"stopAfterNoNew"`
	StopAfterConsecutiveErrors int     `yaml:"stop_after_consecutive_errors" json:"stopAfterConsecutiveErrors"`
	MaxDiscoveryRuns           int     `yaml:"max_discovery_runs" json:"maxDiscoveryRuns"`
	MaxTimeHours               float64 `yaml:"max_time_hours" json:"maxTimeHours"`
	MaxCostUSD                 float64 `yaml:"max_cost_usd" json:"-"`
	// MaxTokens and MaxToolCalls are retained only to accept older YAML files.
	// Native scans ignore both values so a partial provider usage count cannot
	// terminate a security review mid-scan.
	MaxTokens                   int64                `yaml:"max_tokens,omitempty" json:"-"`
	MaxToolCalls                int                  `yaml:"max_tool_calls,omitempty" json:"-"`
	PublicationTool             string               `yaml:"publication_tool,omitempty" json:"publicationTool,omitempty"`
	PublicationDestination      string               `yaml:"publication_destination,omitempty" json:"publicationDestination,omitempty"`
	PublicationArguments        map[string]any       `yaml:"publication_arguments,omitempty" json:"publicationArguments,omitempty"`
	PublicationTitleField       string               `yaml:"publication_title_field,omitempty" json:"publicationTitleField,omitempty"`
	PublicationDescriptionField string               `yaml:"publication_description_field,omitempty" json:"publicationDescriptionField,omitempty"`
	Routes                      SecurityRoutesConfig `yaml:"routes" json:"routes"`
}

func (c SecurityConfig) Validate() error {
	if c.DefaultMode != "standard" && c.DefaultMode != "deep" {
		return fmt.Errorf("security.default_mode must be standard or deep")
	}
	if c.Workers < 1 || c.Workers > 32 ||
		c.Subagents < 0 || c.Subagents > 32 ||
		c.StopAfterNoNew < 1 || c.StopAfterNoNew > 1000 ||
		c.StopAfterConsecutiveErrors < 1 || c.StopAfterConsecutiveErrors > 1000 ||
		c.MaxDiscoveryRuns < 1 || c.MaxDiscoveryRuns > 1000 {
		return fmt.Errorf("security worker and stopping limits are invalid")
	}
	if c.MaxTimeHours <= 0 || c.MaxTimeHours > 96 {
		return fmt.Errorf("security max_time_hours must be in (0,96]")
	}
	if c.MaxCostUSD != 0 {
		return fmt.Errorf("security max_cost_usd requires trusted provider pricing and must remain zero")
	}
	if c.MaxTokens < 0 || c.MaxToolCalls < 0 {
		return fmt.Errorf("security legacy max_tokens and max_tool_calls must be non-negative")
	}
	for name, value := range map[string]string{
		"publication_tool": c.PublicationTool, "publication_destination": c.PublicationDestination,
		"publication_title_field": c.PublicationTitleField, "publication_description_field": c.PublicationDescriptionField,
	} {
		if len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("security %s is invalid", name)
		}
	}
	if err := validateSecurityPublicationArguments(c.PublicationArguments); err != nil {
		return err
	}
	for name, route := range map[string]ModelRouteConfig{
		"audit": c.Routes.Audit, "reducer": c.Routes.Reducer,
		"fixer": c.Routes.Fixer, "verifier": c.Routes.Verifier,
	} {
		if err := validateModelRoute("security.routes."+name, route); err != nil {
			return err
		}
	}
	return nil
}

func validateSecurityPublicationArguments(arguments map[string]any) error {
	if len(arguments) == 0 {
		return nil
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("security publication_arguments must be JSON-compatible: %w", err)
	}
	if len(encoded) > 64<<10 {
		return fmt.Errorf("security publication_arguments exceeds 64 KiB")
	}
	totalKeys := 0
	var inspect func(any, int) error
	inspect = func(value any, depth int) error {
		if depth > 8 {
			return fmt.Errorf("security publication_arguments exceeds maximum nesting depth")
		}
		switch typed := value.(type) {
		case map[string]any:
			totalKeys += len(typed)
			if totalKeys > 256 {
				return fmt.Errorf("security publication_arguments exceeds 256 keys")
			}
			for key, nested := range typed {
				normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(key))
				for _, sensitive := range []string{"token", "password", "secret", "api_key", "authorization", "credential"} {
					if strings.Contains(normalized, sensitive) {
						return fmt.Errorf("security publication_arguments key %q may not contain credentials", key)
					}
				}
				if len(key) > 256 {
					return fmt.Errorf("security publication_arguments key is too long")
				}
				if err := inspect(nested, depth+1); err != nil {
					return err
				}
			}
		case []any:
			if len(typed) > 256 {
				return fmt.Errorf("security publication_arguments array exceeds 256 values")
			}
			for _, nested := range typed {
				if err := inspect(nested, depth+1); err != nil {
					return err
				}
			}
		case string:
			if len(typed) > 8<<10 {
				return fmt.Errorf("security publication_arguments string exceeds 8 KiB")
			}
		case nil, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
			return nil
		default:
			return fmt.Errorf("security publication_arguments contains unsupported value %T", value)
		}
		return nil
	}
	return inspect(arguments, 1)
}

type TeamConfig struct {
	MaxConcurrency int `yaml:"max_concurrency"`
	MaxTicks       int `yaml:"max_ticks"`
}

type SubagentConfig struct {
	Enabled        bool `yaml:"enabled"`
	MaxDepth       int  `yaml:"max_depth"`
	MaxConcurrency int  `yaml:"max_concurrency"`
	// AwaitTimeout is the foreground tool-call wait window, not a child
	// execution timeout. Zero waits until the foreground child completes.
	// A positive duration only releases the parent; safe work continues
	// in the background when it elapses.
	AwaitTimeout  string        `yaml:"await_timeout"`
	AwaitDuration time.Duration `yaml:"-"`
	// IdleTimeout cancels a running child that produces no thinking, output,
	// or tool activity. The default is DefaultSubagentIdleTimeout. Zero
	// disables the watchdog. Open tools, including approval waits, are not
	// cancelled.
	IdleTimeout  string                           `yaml:"idle_timeout"`
	IdleDuration time.Duration                    `yaml:"-"`
	AutoWake     bool                             `yaml:"auto_wake"`
	Toggle       map[string]bool                  `yaml:"toggle,omitempty"`
	Models       map[string]string                `yaml:"models,omitempty"`
	Routes       map[string]ModelRouteConfig      `yaml:"routes,omitempty"`
	Roles        map[string]SubagentRoleConfig    `yaml:"roles,omitempty"`
	Personas     map[string]SubagentPersonaConfig `yaml:"personas,omitempty"`
	Budget       SubagentBudgetConfig             `yaml:"budget"`
}

type SubagentBudgetConfig struct {
	// SoftRequests injects one private wrap-up reminder before the next provider
	// request after the threshold is reached. It is advisory only: unlike the
	// hard limits below, it never cancels or fails a subagent run.
	SoftRequests      int  `yaml:"soft_requests"`
	SoftRequestNotice bool `yaml:"soft_request_notice"`
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
	Servers        map[string]MCPServerConfig `yaml:"servers"`
	RemovedServers []string                   `yaml:"removed_servers,omitempty"`
}

type MCPServerConfig struct {
	Enabled    bool              `yaml:"enabled"`
	Transport  string            `yaml:"transport"`
	Command    string            `yaml:"command,omitempty"`
	Args       []string          `yaml:"args,omitempty"`
	CWD        string            `yaml:"cwd,omitempty"`
	InheritEnv bool              `yaml:"inherit_env"`
	Env        map[string]string `yaml:"env,omitempty"`
	// RuntimeEnv contains plugin-scoped literal environment values. It is never
	// serialized into configuration or emitted in runtime events.
	RuntimeEnv      map[string]string       `yaml:"-" json:"-"`
	URL             string                  `yaml:"url,omitempty"`
	Headers         map[string]string       `yaml:"headers,omitempty"`
	RuntimeHeaders  map[string]string       `yaml:"-" json:"-"`
	ConnectTimeout  string                  `yaml:"connect_timeout"`
	CallTimeout     string                  `yaml:"call_timeout"`
	MaxConcurrency  int                     `yaml:"max_concurrency"`
	Approval        string                  `yaml:"approval"`
	ToolOverrides   map[string]ToolOverride `yaml:"tool_overrides,omitempty"`
	ConnectDuration time.Duration           `yaml:"-"`
	CallDuration    time.Duration           `yaml:"-"`
	// Managed records catalog ownership for diagnostics and migration. It does
	Auth                     *MCPAuthConfig  `yaml:"auth,omitempty" json:"auth,omitempty"`
	OAuth                    *MCPOAuthConfig `yaml:"oauth,omitempty" json:"oauth,omitempty"`
	RuntimeAuthClientSecret  string          `yaml:"-" json:"-"`
	RuntimeOAuthClientSecret string          `yaml:"-" json:"-"`
	// not restrict deletion: removed catalog entries are suppressed explicitly
	// through MCPConfig.RemovedServers.
	Managed bool `yaml:"managed,omitempty" json:"-"`
	// Icon is a bounded data URL projected from a plugin asset. It is never
	// written to configuration.
	Icon string `yaml:"-" json:"-"`
}
type MCPAuthConfig struct {
	Type         string `yaml:"type" json:"type"`
	CredentialID string `yaml:"credential_id,omitempty" json:"credentialId,omitempty"`
	TokenURL     string `yaml:"token_url,omitempty" json:"tokenUrl,omitempty"`
	ClientID     string `yaml:"client_id,omitempty" json:"clientId,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty" json:"-"`
	Resource     string `yaml:"resource,omitempty" json:"resource,omitempty"`
}

type MCPOAuthConfig struct {
	AuthorizationURL string   `yaml:"authorization_url,omitempty" json:"authorizationUrl,omitempty"`
	TokenURL         string   `yaml:"token_url,omitempty" json:"tokenUrl,omitempty"`
	RegistrationURL  string   `yaml:"registration_url,omitempty" json:"registrationUrl,omitempty"`
	ClientID         string   `yaml:"client_id,omitempty" json:"clientId,omitempty"`
	ClientSecret     string   `yaml:"client_secret,omitempty" json:"-"`
	Scopes           []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	RedirectURI      string   `yaml:"redirect_uri,omitempty" json:"redirectUri,omitempty"`
	CallbackPort     int      `yaml:"callback_port,omitempty" json:"callbackPort,omitempty"`
	CallbackPath     string   `yaml:"callback_path,omitempty" json:"callbackPath,omitempty"`
	Prompt           string   `yaml:"prompt,omitempty" json:"prompt,omitempty"`
}

type ToolOverride struct {
	Effect   string `yaml:"effect"`
	Approval string `yaml:"approval"`
}

func Default() Config {
	return Config{
		Version: CurrentVersion,
		Defaults: DefaultsConfig{
			Provider: "chatgpt", Model: "gpt-5.6-sol", Reasoning: "high", AgentMode: "single", Theme: "system", Language: "en", ApprovalMode: "prompt", QueueMode: "queue",
		},
		Workspace: WorkspaceConfig{AllowWrite: true, ShellPolicy: "prompt", AllowNetwork: "prompt", Shell: ShellConfig{MaxContextOutputBytes: 65536, MaxArtifactOutputBytes: 4194304, StopOnOutputLimit: true, MaxConcurrency: 2, MaxWallClock: "10m", MaxWallClockDuration: DefaultShellMaxWallClock}},
		Auth:      AuthConfig{Store: "sqlite", ImportCodex: true, ImportGrok: true, Broker: AuthBrokerConfig{SnapshotTTL: "1h", SnapshotTTLParsed: time.Hour}},
		Providers: ProvidersConfig{
			ChatGPT: ChatGPTConfig{ProviderConfig: ProviderConfig{Enabled: true, TTL: "5m", CatalogTTL: 5 * time.Minute}},
			Grok:    GrokConfig{ProviderConfig: ProviderConfig{Enabled: true, TTL: "5m", CatalogTTL: 5 * time.Minute}, ExperimentalOAuth: true, Transport: "api"},
			Cursor:  CursorConfig{ProviderConfig: ProviderConfig{Enabled: true, TTL: "5m", CatalogTTL: 5 * time.Minute}},
			LLMux:   map[string]LLMuxProviderConfig{},
		},
		Retry: RetryConfig{
			Enabled: true, MaxRetries: 5, BaseDelay: "500ms", BaseDelayDuration: 500 * time.Millisecond,
			MaxDelay: "5m", MaxDelayDuration: 5 * time.Minute,
		},
		TTSR: TTSRConfig{Enabled: true, ContextMode: "discard", InterruptMode: "always", RepeatMode: "once", RepeatGap: 10, Rules: []StreamRuleConfig{}},
		Agents: AgentsConfig{
			Main:     MainAgentConfig{MaxTokens: 0, MaxToolCalls: 0, MaxWallClock: "0s"},
			Team:     TeamConfig{MaxConcurrency: 2, MaxTicks: 12},
			Title:    ModelRouteConfig{Provider: "chatgpt", Model: "gpt-5.6-luna", Reasoning: "low"},
			Approval: ModelRouteConfig{Provider: "chatgpt", Model: "gpt-5.6-luna", Reasoning: "low"},
			Recap:    ModelRouteConfig{Provider: "chatgpt", Model: "gpt-5.6-luna", Reasoning: "low"},
			Advisor:  AdvisorConfig{Enabled: false, Provider: "chatgpt", Model: "gpt-5.6-luna", Reasoning: "low", CatchupTimeout: "30s", CatchupTimeoutDuration: 30 * time.Second},
			Vibe:     VibeConfig{Fast: ModelRouteConfig{Provider: "chatgpt", Model: "gpt-5.6-luna", Reasoning: "low"}},
			LoopGuards: LoopGuardConfig{
				ThinkingEnabled: true, AssistantTextEnabled: true, ToolCallEnabled: true, ToolCallThreshold: 5,
				ToolCallExemptTools: []string{"hub", "vibe_wait", "subagent.get_output"}, UnexpectedStop: "mechanical", UnexpectedStopRetries: 2,
			},
			Context: ContextConfig{
				Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000,
				LargeToolResultTokens: 12000, HistoryRetrievalTokens: 4096,
			},
			Subagents: SubagentConfig{
				Enabled: true, MaxDepth: 2, MaxConcurrency: 32, AwaitTimeout: "0s", AwaitDuration: 0, IdleTimeout: "5m", IdleDuration: DefaultSubagentIdleTimeout, AutoWake: true,
				Toggle: map[string]bool{}, Models: map[string]string{}, Routes: map[string]ModelRouteConfig{}, Roles: builtInSubagentRoles(),
				Personas: map[string]SubagentPersonaConfig{},
				Budget: SubagentBudgetConfig{
					SoftRequests: 200, SoftRequestNotice: true,
					MaxTokens: 0, MaxToolCalls: 0, MaxTurns: 0,
					MaxWallClock: "0s",
				},
			},
		},
		Security: SecurityConfig{
			Enabled: true, DefaultMode: "standard", Workers: 4, Subagents: 3,
			StopAfterNoNew: 4, StopAfterConsecutiveErrors: 3, MaxDiscoveryRuns: 40, MaxTimeHours: 96,
		},
		Skills:  SkillsConfig{Enabled: true, TrustProject: false},
		Plugins: PluginsConfig{Enabled: true, ImportCodex: true, TrustHooks: false, MarketplaceAutoUpdate: "notify"},
		Hooks: HooksConfig{
			Enabled: true, ClaudeCompatibility: false, DefaultTimeout: "5s",
			DefaultTimeoutParsed: 5 * time.Second, FailurePolicy: "open",
		},
		MCP: MCPConfig{Servers: builtInMCPServers()},
		Discovery: DiscoveryConfig{
			ContextFiles: true, Rules: true, Skills: true, MCP: true, Hooks: true,
			DisabledProviders: []string{}, DisabledRules: []string{}, AdditionalContextFiles: []string{},
		},
		Extensions: ExtensionsConfig{
			Enabled: true, TrustProjectCode: false,
			AdditionalToolPaths: []string{}, AdditionalCommandDirs: []string{}, AdditionalExtensionPaths: []string{},
			AdditionalAgentDirs: []string{}, AdditionalThemeDirs: []string{},
		},
		AutoLearn: AutoLearnConfig{Enabled: false, AutoContinue: false, MinToolCalls: 5},
	}
}

func builtInMCPServers() map[string]MCPServerConfig {
	return map[string]MCPServerConfig{
		"grep": {
			Enabled: true, Transport: "streamable_http", URL: "https://mcp.grep.app",
			ConnectTimeout: "30s", CallTimeout: "60s", MaxConcurrency: 2, Approval: "never",
			Managed: true,
			ToolOverrides: map[string]ToolOverride{
				"searchGitHub": {Effect: "read_only", Approval: "never"},
			},
		},
	}
}

func builtInSubagentRoles() map[string]SubagentRoleConfig {
	readOnly := []string{"coding.list_files", "coding.glob", "coding.read_file", "coding.search", "ast_grep", "lsp", "web_search", "github", "recall", "coding.git_diff"}
	all := append(append([]string(nil), readOnly...), "coding.edit_hashline", "coding.replace", "coding.write_file", "coding.delete_file", "coding.gofmt", "coding.go_test", "coding.shell", "debug", "eval", "browser", "computer", "hub", "generate_image", "tts", "retain", "memory_edit")
	execute := append(append([]string(nil), readOnly...), "coding.go_test", "coding.shell", "debug", "eval", "browser", "computer", "hub")
	return map[string]SubagentRoleConfig{
		"worker": {
			Description:    "Implement one scoped coding task end-to-end and return verified evidence.",
			Instructions:   strings.TrimSpace(workerSubagentInstructions),
			CapabilityMode: "all", Isolation: "none", Tools: all, Source: "builtin",
		},
		"explore": {
			Description:    "Investigate the workspace without changes and return file-backed evidence.",
			Instructions:   strings.TrimSpace(exploreSubagentInstructions),
			CapabilityMode: "read-only", Isolation: "none", Tools: append([]string(nil), readOnly...), Source: "builtin",
		},
		"plan": {
			Description:    "Produce a decision-complete implementation plan without changing the workspace.",
			Instructions:   strings.TrimSpace(planSubagentInstructions),
			CapabilityMode: "read-only", Isolation: "none", Tools: append([]string(nil), readOnly...), Source: "builtin",
		},
		"review": {
			Description:    "Review a delegated change for requirement, correctness, and regression risks without editing.",
			Instructions:   strings.TrimSpace(reviewSubagentInstructions),
			CapabilityMode: "read-only", Isolation: "none", Tools: append([]string(nil), readOnly...), Source: "builtin",
		},
		"security-baseline": {
			Description:    "Run one independent read-only source-backed security audit.",
			Instructions:   strings.TrimSpace(securityBaselineSubagentInstructions),
			CapabilityMode: "read-only", Isolation: "none", Tools: append([]string(nil), readOnly...), Source: "builtin",
		},
		"security-investigator": {
			Description:    "Investigate one concrete security packet with exact source evidence.",
			Instructions:   strings.TrimSpace(securityInvestigatorSubagentInstructions),
			CapabilityMode: "read-only", Isolation: "none", Tools: append([]string(nil), readOnly...), Source: "builtin",
		},
		"verify": {
			Description:    "Run governed checks without editing and report exact outcomes.",
			Instructions:   strings.TrimSpace(verifySubagentInstructions),
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
	if err := c.validateHooksDisabled(); err != nil {
		return err
	}
	if c.Defaults.AgentMode != "single" && c.Defaults.AgentMode != "team" {
		return fmt.Errorf("defaults.agent_mode must be single or team")
	}
	if !ValidUILanguage(c.Defaults.Language) {
		return fmt.Errorf("defaults.language must be a valid translation-pack identifier")
	}
	if c.Defaults.ApprovalMode != "prompt" && c.Defaults.ApprovalMode != "auto_review" && c.Defaults.ApprovalMode != "yolo" {
		return fmt.Errorf("defaults.approval_mode must be prompt, auto_review, or yolo")
	}
	if c.Defaults.QueueMode != "queue" && c.Defaults.QueueMode != "guide" {
		return fmt.Errorf("defaults.queue_mode must be queue or guide")
	}
	if c.Auth.Store != "sqlite" && c.Auth.Store != "keyring" && c.Auth.Store != "file" {
		return fmt.Errorf("auth.store must be sqlite, keyring, or file")
	}
	if strings.TrimSpace(c.Auth.Broker.URL) != "" {
		endpoint, err := url.Parse(strings.TrimSpace(c.Auth.Broker.URL))
		if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
			(endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && isLoopbackHost(endpoint.Hostname()))) {
			return fmt.Errorf("auth.broker.url must use https (http is allowed only for loopback)")
		}
		if strings.ContainsAny(c.Auth.Broker.Token, "\r\n\x00") {
			return fmt.Errorf("auth.broker.token contains an unsafe character")
		}
	}
	if strings.TrimSpace(c.Auth.Broker.SnapshotTTL) == "" {
		c.Auth.Broker.SnapshotTTL = "1h"
	}
	brokerTTL, err := time.ParseDuration(c.Auth.Broker.SnapshotTTL)
	if err != nil || brokerTTL < 0 {
		return fmt.Errorf("auth.broker.snapshot_ttl must be a non-negative duration")
	}
	c.Auth.Broker.SnapshotTTLParsed = brokerTTL
	if c.Workspace.ShellPolicy != "prompt" && c.Workspace.ShellPolicy != "deny" && c.Workspace.ShellPolicy != "allow" {
		return fmt.Errorf("workspace.shell_policy must be prompt, deny, or allow")
	}
	if c.Workspace.AllowNetwork != "prompt" && c.Workspace.AllowNetwork != "deny" && c.Workspace.AllowNetwork != "allow" {
		return fmt.Errorf("workspace.allow_network must be prompt, deny, or allow")
	}
	if c.Workspace.Shell.MaxContextOutputBytes <= 0 || c.Workspace.Shell.MaxArtifactOutputBytes < c.Workspace.Shell.MaxContextOutputBytes || c.Workspace.Shell.MaxConcurrency <= 0 {
		return fmt.Errorf("workspace.shell output limits and max_concurrency must be positive, and artifact limit must not be smaller than context limit")
	}
	if !c.Workspace.Shell.StopOnOutputLimit {
		return fmt.Errorf("workspace.shell.stop_on_output_limit must be true")
	}
	if strings.TrimSpace(c.Workspace.Shell.MaxWallClock) == "" {
		c.Workspace.Shell.MaxWallClock = DefaultShellMaxWallClock.String()
	}
	shellWall, err := time.ParseDuration(c.Workspace.Shell.MaxWallClock)
	if err != nil || shellWall < time.Second {
		return fmt.Errorf("workspace.shell.max_wall_clock must be a duration of at least 1s")
	}
	c.Workspace.Shell.MaxWallClockDuration = shellWall
	for name, provider := range map[string]*ProviderConfig{"chatgpt": &c.Providers.ChatGPT.ProviderConfig, "grok": &c.Providers.Grok.ProviderConfig, "cursor": &c.Providers.Cursor.ProviderConfig} {
		ttl, err := time.ParseDuration(provider.TTL)
		if err != nil || ttl <= 0 {
			return fmt.Errorf("providers.%s.catalog_ttl must be a positive duration", name)
		}
		provider.CatalogTTL = ttl
		if err := validateDisabledModels(name, provider.DisabledModels); err != nil {
			return err
		}
	}
	if c.Providers.Grok.Transport != "api" && c.Providers.Grok.Transport != "cli_proxy" {
		return fmt.Errorf("providers.grok.transport must be api or cli_proxy")
	}
	if c.Retry.MaxRetries < 1 || c.Retry.MaxRetries > 100 {
		return fmt.Errorf("retry.max_retries must be between 1 and 100")
	}
	retryBaseDelay, err := time.ParseDuration(c.Retry.BaseDelay)
	if err != nil || retryBaseDelay < 0 {
		return fmt.Errorf("retry.base_delay must be a non-negative duration")
	}
	retryMaxDelay, err := time.ParseDuration(c.Retry.MaxDelay)
	if err != nil || retryMaxDelay < 0 || (retryMaxDelay > 0 && retryMaxDelay < retryBaseDelay) {
		return fmt.Errorf("retry.max_delay must be zero or a duration greater than or equal to retry.base_delay")
	}
	c.Retry.BaseDelayDuration = retryBaseDelay
	c.Retry.MaxDelayDuration = retryMaxDelay
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
	if err := c.validateLLMuxProviders(); err != nil {
		return err
	}
	if err := validateModelRoute("agents.title", c.Agents.Title); err != nil {
		return err
	}
	if err := validateModelRoute("agents.plan", c.Agents.Plan); err != nil {
		return err
	}
	if err := validateModelRoute("agents.approval", c.Agents.Approval); err != nil {
		return err
	}
	if err := validateModelRoute("agents.vision", c.Agents.Vision); err != nil {
		return err
	}
	if err := validateModelRoute("agents.recap", c.Agents.Recap); err != nil {
		return err
	}
	if err := validateModelRoute("agents.advisor", c.Agents.Advisor.Route()); err != nil {
		return err
	}
	advisorTimeout, err := time.ParseDuration(c.Agents.Advisor.CatchupTimeout)
	if err != nil || advisorTimeout <= 0 || advisorTimeout > 30*time.Second {
		return fmt.Errorf("agents.advisor.catchup_timeout must be between 1ns and 30s")
	}
	if len(c.Agents.Advisor.Instructions) > 32<<10 {
		return fmt.Errorf("agents.advisor.instructions exceeds 32 KiB")
	}
	c.Agents.Advisor.CatchupTimeoutDuration = advisorTimeout
	if err := validateInheritedModelRoute("agents.vibe.fast", c.Agents.Vibe.Fast); err != nil {
		return err
	}
	if err := validateInheritedModelRoute("agents.vibe.good", c.Agents.Vibe.Good); err != nil {
		return err
	}
	contextConfig := c.Agents.Context
	if c.Agents.LoopGuards.ToolCallThreshold < 2 || c.Agents.LoopGuards.ToolCallThreshold > 100 {
		return fmt.Errorf("agents.loop_guards.tool_call_threshold must be between 2 and 100")
	}
	if len(c.Agents.LoopGuards.ToolCallExemptTools) > 128 {
		return fmt.Errorf("agents.loop_guards.tool_call_exempt_tools exceeds 128 entries")
	}
	for _, name := range c.Agents.LoopGuards.ToolCallExemptTools {
		if strings.TrimSpace(name) == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
			return fmt.Errorf("agents.loop_guards contains an invalid exempt tool")
		}
	}
	if c.Agents.LoopGuards.UnexpectedStop != "none" && c.Agents.LoopGuards.UnexpectedStop != "mechanical" && c.Agents.LoopGuards.UnexpectedStop != "smart" {
		return fmt.Errorf("agents.loop_guards.unexpected_stop must be none, mechanical, or smart")
	}
	if c.Agents.LoopGuards.UnexpectedStopRetries < 0 || c.Agents.LoopGuards.UnexpectedStopRetries > 10 {
		return fmt.Errorf("agents.loop_guards.unexpected_stop_retries must be between 0 and 10")
	}
	if contextConfig.ReserveTokens <= 0 || contextConfig.KeepRecentTokens <= 0 || contextConfig.LargeToolResultTokens <= 0 || contextConfig.HistoryRetrievalTokens <= 0 {
		return fmt.Errorf("agents.context token limits must be positive")
	}
	if err := c.validateSubagents(); err != nil {
		return err
	}
	if err := c.Security.Validate(); err != nil {
		return err
	}
	if err := c.validateTTSR(); err != nil {
		return err
	}
	if c.AutoLearn.MinToolCalls <= 0 || c.AutoLearn.MinToolCalls > 1000 {
		return fmt.Errorf("autolearn.min_tool_calls must be between 1 and 1000")
	}
	switch c.Plugins.MarketplaceAutoUpdate {
	case "", "off", "notify", "auto":
		if c.Plugins.MarketplaceAutoUpdate == "" {
			c.Plugins.MarketplaceAutoUpdate = "notify"
		}
	default:
		return fmt.Errorf("plugins.marketplace_auto_update must be off, notify, or auto")
	}
	if len(c.Extensions.AdditionalToolPaths) > 128 || len(c.Extensions.AdditionalCommandDirs) > 128 ||
		len(c.Extensions.AdditionalExtensionPaths) > 128 || len(c.Extensions.AdditionalAgentDirs) > 128 || len(c.Extensions.AdditionalThemeDirs) > 128 {
		return fmt.Errorf("extensions contains too many additional paths")
	}
	extensionPaths := append(append(append(append([]string(nil), c.Extensions.AdditionalToolPaths...), c.Extensions.AdditionalCommandDirs...), c.Extensions.AdditionalExtensionPaths...), c.Extensions.AdditionalAgentDirs...)
	extensionPaths = append(extensionPaths, c.Extensions.AdditionalThemeDirs...)
	for _, path := range extensionPaths {
		if strings.TrimSpace(path) == "" || len(path) > 4096 || strings.ContainsAny(path, "\r\n\x00") {
			return fmt.Errorf("extensions contains an invalid additional path")
		}
	}
	if len(c.Discovery.DisabledProviders) > 32 || len(c.Discovery.DisabledRules) > 256 || len(c.Discovery.AdditionalContextFiles) > 128 {
		return fmt.Errorf("discovery configuration exceeds safe limits")
	}
	for _, name := range append(append([]string(nil), c.Discovery.DisabledProviders...), c.Discovery.DisabledRules...) {
		if strings.TrimSpace(name) == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
			return fmt.Errorf("discovery contains an invalid provider or rule")
		}
	}
	for _, path := range c.Discovery.AdditionalContextFiles {
		if strings.TrimSpace(path) == "" || len(path) > 4096 || strings.ContainsAny(path, "\r\n\x00") {
			return fmt.Errorf("discovery contains an invalid additional context file")
		}
	}
	removedServers := make([]string, 0, len(c.MCP.RemovedServers))
	removedSet := make(map[string]struct{}, len(c.MCP.RemovedServers))
	for _, name := range c.MCP.RemovedServers {
		name = strings.TrimSpace(name)
		if !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("removed mcp server name %q must match [a-z0-9_-]+", name)
		}
		if _, duplicate := removedSet[name]; duplicate {
			continue
		}
		removedSet[name] = struct{}{}
		removedServers = append(removedServers, name)
		delete(c.MCP.Servers, name)
	}
	slices.Sort(removedServers)
	c.MCP.RemovedServers = removedServers
	for name, server := range c.MCP.Servers {
		normalized, err := NormalizeMCPServer(name, server)
		if err != nil {
			return err
		}
		c.MCP.Servers[name] = normalized
	}
	if runtime.GOOS == "js" && c.Auth.Store == "keyring" {
		return fmt.Errorf("keyring credential storage is unavailable on js")
	}
	return nil
}

func (c *Config) validateTTSR() error {
	switch c.TTSR.ContextMode {
	case "discard", "keep":
	default:
		return fmt.Errorf("ttsr.context_mode must be discard or keep")
	}
	switch c.TTSR.InterruptMode {
	case "never", "prose-only", "tool-only", "always":
	default:
		return fmt.Errorf("ttsr.interrupt_mode must be never, prose-only, tool-only, or always")
	}
	switch c.TTSR.RepeatMode {
	case "once", "after-gap":
	default:
		return fmt.Errorf("ttsr.repeat_mode must be once or after-gap")
	}
	if c.TTSR.RepeatGap < 1 || c.TTSR.RepeatGap > 10_000 {
		return fmt.Errorf("ttsr.repeat_gap must be between 1 and 10000")
	}
	if len(c.TTSR.Rules) > 128 {
		return fmt.Errorf("ttsr.rules exceeds 128 entries")
	}
	seen := make(map[string]bool, len(c.TTSR.Rules))
	for index, rule := range c.TTSR.Rules {
		rule.Name = strings.TrimSpace(rule.Name)
		if rule.Name == "" || len(rule.Name) > 64 || strings.ContainsAny(rule.Name, "\r\n\x00") {
			return fmt.Errorf("ttsr rule %d has an invalid name", index+1)
		}
		key := strings.ToLower(rule.Name)
		if seen[key] {
			return fmt.Errorf("ttsr rule name %q is duplicated", rule.Name)
		}
		seen[key] = true
		if strings.TrimSpace(rule.Content) == "" || len(rule.Content) > 64<<10 {
			return fmt.Errorf("ttsr rule %q content is empty or exceeds 64 KiB", rule.Name)
		}
		if len(rule.Conditions) > 16 || len(rule.ASTConditions) > 16 || len(rule.Scope) > 16 || len(rule.Globs) > 16 {
			return fmt.Errorf("ttsr rule %q exceeds 16 match entries per field", rule.Name)
		}
		if len(rule.Conditions) == 0 && len(rule.ASTConditions) == 0 {
			return fmt.Errorf("ttsr rule %q requires conditions or ast_conditions", rule.Name)
		}
		for _, pattern := range rule.Conditions {
			if len(pattern) > 4096 {
				return fmt.Errorf("ttsr rule %q condition exceeds 4096 bytes", rule.Name)
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("ttsr rule %q has invalid condition: %w", rule.Name, err)
			}
		}
		for _, pattern := range rule.ASTConditions {
			if strings.TrimSpace(pattern) == "" || len(pattern) > 4096 {
				return fmt.Errorf("ttsr rule %q has an invalid ast_condition", rule.Name)
			}
		}
		for _, pattern := range rule.Globs {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("ttsr rule %q has an empty glob", rule.Name)
			}
			if _, err := filepath.Match(pattern, "fixture"); err != nil {
				return fmt.Errorf("ttsr rule %q has invalid glob %q: %w", rule.Name, pattern, err)
			}
		}
		for _, scope := range rule.Scope {
			scope = strings.TrimSpace(scope)
			if scope == "" || len(scope) > 256 || strings.ContainsAny(scope, "\r\n\x00") {
				return fmt.Errorf("ttsr rule %q has an invalid scope", rule.Name)
			}
		}
		if rule.InterruptMode != "" {
			switch rule.InterruptMode {
			case "never", "prose-only", "tool-only", "always":
			default:
				return fmt.Errorf("ttsr rule %q has an invalid interrupt_mode", rule.Name)
			}
		}
		c.TTSR.Rules[index] = rule
	}
	return nil
}

// NormalizeMCPServer validates one MCP server and fills the same runtime
// defaults used during full configuration loading. Desktop mutations use this
// boundary before persisting or changing a live connection.
func NormalizeMCPServer(name string, server MCPServerConfig) (MCPServerConfig, error) {
	name = strings.TrimSpace(name)
	if !mcpServerNamePattern.MatchString(name) {
		return MCPServerConfig{}, fmt.Errorf("mcp server name %q must match [a-z0-9_-]+", name)
	}
	server = applyMCPServerDefaults(server)
	if err := parseMCPServerTimeouts(name, &server); err != nil {
		return MCPServerConfig{}, err
	}
	if err := validateMCPServerPolicy(name, server); err != nil {
		return MCPServerConfig{}, err
	}
	if err := validateMCPTransport(name, server); err != nil {
		return MCPServerConfig{}, err
	}
	if err := validateMCPReferences(name, "env", server.Env); err != nil {
		return MCPServerConfig{}, err
	}
	if err := validateMCPReferences(name, "headers", server.Headers); err != nil {
		return MCPServerConfig{}, err
	}
	if err := validateMCPToolOverrides(name, server.ToolOverrides); err != nil {
		return MCPServerConfig{}, err
	}
	if err := validateMCPOAuth(name, server); err != nil {
		return MCPServerConfig{}, err
	}
	return server, nil
}

func validateMCPOAuth(name string, server MCPServerConfig) error {
	if server.Auth != nil {
		if server.Auth.Type != "oauth" && server.Auth.Type != "apikey" {
			return fmt.Errorf("mcp.servers.%s.auth.type must be oauth or apikey", name)
		}
		if server.Auth.ClientSecret != "" {
			if err := validateSecretReference(server.Auth.ClientSecret); err != nil {
				return fmt.Errorf("mcp.servers.%s.auth.client_secret: %w", name, err)
			}
		}
	}
	if server.OAuth == nil {
		return nil
	}
	if server.Transport != "streamable_http" {
		return fmt.Errorf("mcp.servers.%s.oauth requires streamable_http transport", name)
	}
	if server.OAuth.ClientSecret != "" {
		if err := validateSecretReference(server.OAuth.ClientSecret); err != nil {
			return fmt.Errorf("mcp.servers.%s.oauth.client_secret: %w", name, err)
		}
	}
	for field, value := range map[string]string{
		"authorization_url": server.OAuth.AuthorizationURL, "token_url": server.OAuth.TokenURL,
		"registration_url": server.OAuth.RegistrationURL, "redirect_uri": server.OAuth.RedirectURI,
	} {
		if value == "" {
			continue
		}
		endpoint, err := url.Parse(value)
		if err != nil || endpoint.Host == "" || endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && isLoopbackHost(endpoint.Hostname())) {
			return fmt.Errorf("mcp.servers.%s.oauth.%s must use https or loopback http", name, field)
		}
	}
	if server.OAuth.CallbackPort < 0 || server.OAuth.CallbackPort > 65535 {
		return fmt.Errorf("mcp.servers.%s.oauth.callback_port is invalid", name)
	}
	if server.OAuth.CallbackPath != "" && !strings.HasPrefix(server.OAuth.CallbackPath, "/") {
		return fmt.Errorf("mcp.servers.%s.oauth.callback_path must start with /", name)
	}
	return nil
}

func applyMCPServerDefaults(server MCPServerConfig) MCPServerConfig {
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
	return server
}

func parseMCPServerTimeouts(name string, server *MCPServerConfig) error {
	var err error
	server.ConnectDuration, err = time.ParseDuration(server.ConnectTimeout)
	if err != nil || server.ConnectDuration <= 0 {
		return fmt.Errorf("mcp.servers.%s.connect_timeout must be a positive duration", name)
	}
	server.CallDuration, err = time.ParseDuration(server.CallTimeout)
	if err != nil || server.CallDuration <= 0 {
		return fmt.Errorf("mcp.servers.%s.call_timeout must be a positive duration", name)
	}
	return nil
}

func validateMCPServerPolicy(name string, server MCPServerConfig) error {
	if server.MaxConcurrency < 1 {
		return fmt.Errorf("mcp.servers.%s.max_concurrency must be positive", name)
	}
	if server.Approval != "always" && server.Approval != "never" {
		return fmt.Errorf("mcp.servers.%s.approval must be always or never", name)
	}
	return nil
}

func validateMCPTransport(name string, server MCPServerConfig) error {
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
	return nil
}

func validateMCPReferences(name, group string, references map[string]string) error {
	for key, reference := range references {
		if err := validateSecretReference(reference); err != nil {
			return fmt.Errorf("mcp.servers.%s.%s.%s: %w", name, group, key, err)
		}
	}
	return nil
}

func validateMCPToolOverrides(name string, overrides map[string]ToolOverride) error {
	for toolName, override := range overrides {
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
	return nil
}

func (c *Config) validateSkills() error {
	if len(c.Skills.AdditionalDirs) > 56 {
		return fmt.Errorf("skills.additional_dirs must contain at most 56 entries")
	}
	if len(c.Plugins.CodexImports) > 128 {
		return fmt.Errorf("plugins.codex_imports must contain at most 128 entries")
	}
	pluginImports := make([]string, 0, len(c.Plugins.CodexImports))
	seenPluginImports := make(map[string]struct{}, len(c.Plugins.CodexImports))
	for _, pluginID := range c.Plugins.CodexImports {
		pluginID = strings.TrimSpace(pluginID)
		if pluginID == "" || len(pluginID) > 256 || strings.ContainsAny(pluginID, "\r\n\x00") {
			return fmt.Errorf("plugins.codex_imports contains an invalid plugin id")
		}
		if _, exists := seenPluginImports[pluginID]; exists {
			continue
		}
		seenPluginImports[pluginID] = struct{}{}
		pluginImports = append(pluginImports, pluginID)
	}
	slices.Sort(pluginImports)
	c.Plugins.CodexImports = pluginImports
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

func (c *Config) validateHooksDisabled() error {
	seen := make(map[string]struct{}, len(c.Hooks.Disabled))
	for _, id := range c.Hooks.Disabled {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("hooks.disabled contains an empty hook identity")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("hooks.disabled contains duplicate hook %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (c *Config) validateSubagents() error {
	subagents := &c.Agents.Subagents
	if subagents.MaxDepth < -1 {
		return fmt.Errorf("agents.subagents.max_depth must be -1 (unlimited) or non-negative")
	}
	if subagents.MaxConcurrency < 0 {
		return fmt.Errorf("agents.subagents.max_concurrency must be non-negative (zero is unbounded)")
	}
	await, err := parseSubagentAwaitTimeout(subagents.AwaitTimeout)
	if err != nil {
		return err
	}
	idle, err := parseSubagentIdleTimeout(subagents.IdleTimeout)
	if err != nil {
		return err
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
	if subagents.Budget.SoftRequests < 0 {
		return fmt.Errorf("agents.subagents.budget.soft_requests must be non-negative (zero disables the reminder)")
	}
	subagents.AwaitDuration = await
	subagents.IdleDuration = idle
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
		"coding.list_files": true, "coding.glob": true, "coding.read_file": true, "coding.search": true, "ast_grep": true, "lsp": true, "web_search": true, "github": true, "recall": true, "coding.git_diff": true,
		"coding.edit_hashline": true, "coding.replace": true, "coding.write_file": true, "coding.delete_file": true, "coding.gofmt": true,
		"coding.go_test": true, "coding.shell": true, "debug": true, "eval": true, "browser": true, "computer": true, "hub": true, "generate_image": true, "tts": true, "retain": true, "memory_edit": true,
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
	readOnlyTools := []string{"coding.list_files", "coding.glob", "coding.read_file", "coding.search", "ast_grep", "lsp", "web_search", "github", "coding.git_diff"}
	if len(subagents.Roles) > maxConfiguredSubagentRoles {
		return fmt.Errorf("agents.subagents.roles must contain at most %d roles", maxConfiguredSubagentRoles)
	}
	for name, role := range subagents.Roles {
		if len(name) > maxConfiguredSubagentRoleNameBytes || !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("agents.subagents role %q must be at most %d bytes and match [a-z0-9_-]+", name, maxConfiguredSubagentRoleNameBytes)
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
	if !mcpServerNamePattern.MatchString(provider) {
		return fmt.Errorf("%s provider must match [a-z0-9_-]+", name)
	}
	return nil
}

func validateInheritedModelRoute(name string, route ModelRouteConfig) error {
	provider := strings.TrimSpace(route.Provider)
	model := strings.TrimSpace(route.Model)
	if provider != "" && model == "" {
		return fmt.Errorf("%s route must set model when provider is set", name)
	}
	if provider != "" && !mcpServerNamePattern.MatchString(provider) {
		return fmt.Errorf("%s provider must match [a-z0-9_-]+", name)
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

const (
	minSubagentAwaitSeconds = 5
	maxSubagentAwaitSeconds = 3600
	minSubagentIdleSeconds  = 30
	maxSubagentIdleSeconds  = 3600
)

// ValidSubagentAwaitSeconds reports whether a settings or YAML update may store
// this foreground wait. Zero waits until the foreground child completes;
// otherwise the value must be between 5 and 3600 seconds.
func ValidSubagentAwaitSeconds(seconds int) bool {
	return seconds == 0 || (seconds >= minSubagentAwaitSeconds && seconds <= maxSubagentAwaitSeconds)
}

func parseSubagentAwaitTimeout(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "0" {
		return 0, nil
	}
	await, err := time.ParseDuration(value)
	if err != nil || await < 0 {
		return 0, fmt.Errorf("agents.subagents.await_timeout must be a non-negative duration (zero waits until the foreground child completes)")
	}
	return await, nil
}

// ValidSubagentIdleSeconds reports whether a settings or YAML update may store
// this idle cancel window. Zero disables the watchdog; otherwise the value
// must be between 30 and 3600 seconds.
func ValidSubagentIdleSeconds(seconds int) bool {
	return seconds == 0 || (seconds >= minSubagentIdleSeconds && seconds <= maxSubagentIdleSeconds)
}

// ValidShellMaxWallClockSeconds reports whether a settings or YAML update may
// store this per-command coding.shell ceiling.
func ValidShellMaxWallClockSeconds(seconds int) bool {
	return seconds >= 60 && seconds <= 7200
}

func parseSubagentIdleTimeout(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return 0, nil
	}
	idle, err := time.ParseDuration(value)
	if err != nil || idle < 0 {
		return 0, fmt.Errorf("agents.subagents.idle_timeout must be a non-negative duration (zero disables idle cancellation)")
	}
	return idle, nil
}
