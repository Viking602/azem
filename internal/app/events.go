package app

import (
	"time"

	backgroundservice "github.com/Viking602/azem/internal/background"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/session"
)

type EventKind string

const (
	EventBootstrapDone      EventKind = "bootstrap_done"
	EventSessionLoaded      EventKind = "session_loaded"
	EventTodoUpdated        EventKind = "todo_updated"
	EventRunStarted         EventKind = "run_started"
	EventContextUsage       EventKind = "context_usage"
	EventContextProfile     EventKind = "context_profile"
	EventAgentState         EventKind = "agent_state"
	EventAgentDetail        EventKind = "agent_detail"
	EventProviderRetry      EventKind = "provider_retry"
	EventThinkingDelta      EventKind = "thinking_delta"
	EventTextDelta          EventKind = "text_delta"
	EventProjectionResync   EventKind = "projection_resync"
	EventToolStarted        EventKind = "tool_started"
	EventToolUpdate         EventKind = "tool_update"
	EventToolFinished       EventKind = "tool_finished"
	EventDiffReady          EventKind = "diff_ready"
	EventApprovalRequested  EventKind = "approval_requested"
	EventApprovalResolved   EventKind = "approval_resolved"
	EventUserInputRequested EventKind = "user_input_requested"
	EventUserInputResolved  EventKind = "user_input_resolved"
	EventPlanProposed       EventKind = "plan_proposed"
	EventPlanResolved       EventKind = "plan_resolved"
	EventApprovalMode       EventKind = "approval_mode"
	EventModelCatalog       EventKind = "model_catalog"
	EventModelProviders     EventKind = "model_providers"
	EventSkillCatalog       EventKind = "skill_catalog"
	EventPluginCatalog      EventKind = "plugin_catalog"
	EventHookCatalog        EventKind = "hook_catalog"
	EventAuthState          EventKind = "auth_state"
	EventMCPState           EventKind = "mcp_state"
	EventRecoveryState      EventKind = "recovery_state"
	EventRunFinished        EventKind = "run_finished"
	EventRunFailed          EventKind = "run_failed"
	EventRunCancelled       EventKind = "run_cancelled"
	EventHookStarted        EventKind = "hook_started"
	EventHookFinished       EventKind = "hook_finished"
	EventHookDiagnostic     EventKind = "hook_diagnostic"
	EventMemoryState        EventKind = "memory_state"
	EventRecapState         EventKind = "recap_state"
	EventModelRoutes        EventKind = "model_routes"
	EventBackgroundState    EventKind = "background_state"
	EventBackgroundLogs     EventKind = "background_logs"
	EventGitBranches        EventKind = "git_branches"
	EventUsageReport        EventKind = "usage_report"
)

type ModelRouteEntry struct {
	Scope string                  `json:"scope"`
	Role  string                  `json:"role"`
	Label string                  `json:"label"`
	Route config.ModelRouteConfig `json:"route"`
}

type ModelProviderEntry struct {
	ID                   string                    `json:"id"`
	DisplayName          string                    `json:"displayName"`
	Backend              string                    `json:"backend"`
	DefaultBaseURL       string                    `json:"defaultBaseUrl"`
	BaseURL              string                    `json:"baseUrl"`
	EnvKey               string                    `json:"envKey"`
	Enabled              bool                      `json:"enabled"`
	CredentialConfigured bool                      `json:"credentialConfigured"`
	CredentialSource     string                    `json:"credentialSource"`
	Subscription         bool                      `json:"subscription,omitempty"`
	AccountID            string                    `json:"accountId,omitempty"`
	AccountLabel         string                    `json:"accountLabel,omitempty"`
	AccountPlan          string                    `json:"accountPlan,omitempty"`
	QuotaAvailable       bool                      `json:"quotaAvailable,omitempty"`
	QuotaPeriod          string                    `json:"quotaPeriod,omitempty"`
	QuotaUsedPercent     float64                   `json:"quotaUsedPercent,omitempty"`
	QuotaResetsAt        int64                     `json:"quotaResetsAt,omitempty"`
	QuotaBalance         string                    `json:"quotaBalance,omitempty"`
	QuotaUnlimited       bool                      `json:"quotaUnlimited,omitempty"`
	QuotaWarning         string                    `json:"quotaWarning,omitempty"`
	ModelsDevID          string                    `json:"modelsDevId,omitempty"`
	ModelsSource         string                    `json:"modelsSource,omitempty"`
	ModelsWarning        string                    `json:"modelsWarning,omitempty"`
	Models               []config.LLMuxModelConfig `json:"models"`
}

type GitBranchEntry struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

type AgentStatePayload struct {
	Type               string `json:"type"`
	Description        string `json:"description,omitempty"`
	Model              string `json:"model,omitempty"`
	Background         bool   `json:"background,omitempty"`
	CapabilityMode     string `json:"capabilityMode,omitempty"`
	RequestedIsolation string `json:"requestedIsolation,omitempty"`
	Isolation          string `json:"isolation,omitempty"`
	CWD                string `json:"cwd,omitempty"`
	ParentRunID        string `json:"parentRunId,omitempty"`
	ParentToolCallID   string `json:"parentToolCallId,omitempty"`
	ChildRunID         string `json:"childRunId,omitempty"`
	Activity           string `json:"activity,omitempty"`
	Warning            string `json:"warning,omitempty"`
	WorktreePath       string `json:"worktreePath,omitempty"`
	ToolCalls          int    `json:"toolCalls"`
	Turns              int    `json:"turns"`
	TokensUsed         int    `json:"tokensUsed"`
	ElapsedMS          int64  `json:"elapsedMs"`
}

type AgentTranscriptBlock struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	RunID            string `json:"runId,omitempty"`
	ToolCallID       string `json:"toolCallId,omitempty"`
	Title            string `json:"title,omitempty"`
	Content          string `json:"content,omitempty"`
	ContentBytes     int    `json:"contentBytes,omitempty"`
	ContentTruncated bool   `json:"contentTruncated,omitempty"`
	State            string `json:"state,omitempty"`
}

type AgentCatalogEntry struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	Persona        string `json:"persona,omitempty"`
	Model          string `json:"model,omitempty"`
	Reasoning      string `json:"reasoning,omitempty"`
	CapabilityMode string `json:"capabilityMode,omitempty"`
	Isolation      string `json:"isolation,omitempty"`
	Source         string `json:"source,omitempty"`
	Enabled        bool   `json:"enabled"`
}

type SkillCatalogEntry struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	SourcePath    string `json:"sourcePath,omitempty"`
	LogoPath      string `json:"logoPath,omitempty"`
	Bundled       bool   `json:"bundled,omitempty"`
	Eager         bool   `json:"eager,omitempty"`
	Disabled      bool   `json:"disabled,omitempty"`
	ModelVisible  bool   `json:"modelVisible,omitempty"`
	ResourceCount int    `json:"resourceCount,omitempty"`
}

type SkillDiagnostic struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type PluginCatalogEntry struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	DisplayName        string   `json:"displayName,omitempty"`
	Version            string   `json:"version,omitempty"`
	Marketplace        string   `json:"marketplace,omitempty"`
	Origin             string   `json:"origin,omitempty"`
	Description        string   `json:"description,omitempty"`
	DeveloperName      string   `json:"developerName,omitempty"`
	Category           string   `json:"category,omitempty"`
	BrandColor         string   `json:"brandColor,omitempty"`
	LogoPath           string   `json:"logoPath,omitempty"`
	Enabled            bool     `json:"enabled"`
	SkillCount         int      `json:"skillCount,omitempty"`
	MCPServerCount     int      `json:"mcpServerCount,omitempty"`
	IntegratedMCPCount int      `json:"integratedMCPCount,omitempty"`
	HookCount          int      `json:"hookCount,omitempty"`
	HooksTrusted       bool     `json:"hooksTrusted,omitempty"`
	HasApp             bool     `json:"hasApp,omitempty"`
	Capabilities       []string `json:"capabilities,omitempty"`
	Status             string   `json:"status,omitempty"`
	Warning            string   `json:"warning,omitempty"`
	Imported           bool     `json:"imported,omitempty"`
}

type PluginDiagnostic struct {
	PluginID string `json:"pluginId,omitempty"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type HookSourceEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Origin    string `json:"origin,omitempty"`
	PluginID  string `json:"pluginId,omitempty"`
	Source    string `json:"source,omitempty"`
	HookCount int    `json:"hookCount,omitempty"`
	Trusted   bool   `json:"trusted,omitempty"`
	Warning   string `json:"warning,omitempty"`
	LogoPath  string `json:"logoPath,omitempty"`
}

type HookCommandEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Event   string `json:"event,omitempty"`
	Matcher string `json:"matcher,omitempty"`
	Command string `json:"command,omitempty"`
	Source  string `json:"source,omitempty"`
	Origin  string `json:"origin,omitempty"`
	Enabled bool   `json:"enabled"`
}

type HookCatalogSnapshot struct {
	Enabled     bool               `json:"enabled"`
	TrustHooks  bool               `json:"trustHooks"`
	Sources     []HookSourceEntry  `json:"sources,omitempty"`
	Commands    []HookCommandEntry `json:"commands,omitempty"`
	Diagnostics []HookDiagnostic   `json:"diagnostics,omitempty"`
}

type HookDiagnostic struct {
	Source  string `json:"source,omitempty"`
	Event   string `json:"event,omitempty"`
	Message string `json:"message"`
}

type AgentSnapshotPayload struct {
	ID      string            `json:"id"`
	State   string            `json:"state,omitempty"`
	Summary string            `json:"summary,omitempty"`
	Agent   AgentStatePayload `json:"agent"`
}

type ContextCategory string

const (
	ContextCategoryCore         ContextCategory = "core"
	ContextCategorySkills       ContextCategory = "skills"
	ContextCategoryBuiltinTools ContextCategory = "builtin_tools"
	ContextCategoryMCP          ContextCategory = "mcp"
	ContextCategoryConversation ContextCategory = "conversation"
	ContextCategoryOther        ContextCategory = "other"
)

const ContextContributionRemainingItems = "azem.context.remaining_items"

type ContextContribution struct {
	Category ContextCategory `json:"category"`
	Name     string          `json:"name"`
	Tokens   int             `json:"tokens"`
}

type ContextProfile struct {
	Source               string                 `json:"source"`
	Estimated            bool                   `json:"estimated"`
	Contributions        []ContextContribution  `json:"contributions"`
	ReportedInputTokens  int                    `json:"reportedInputTokens,omitempty"`
	ReportedOutputTokens int                    `json:"reportedOutputTokens,omitempty"`
	ManifestHash         string                 `json:"manifestHash,omitempty"`
	SemanticRevision     int64                  `json:"semanticRevision,omitempty"`
	SemanticCursor       session.WriterCursorV1 `json:"semanticCursor,omitempty"`
	CanonicalHighWater   int64                  `json:"canonicalHighWater,omitempty"`
	PolicyVersion        int                    `json:"policyVersion,omitempty"`
	RebuildReason        string                 `json:"rebuildReason,omitempty"`
	WriterLag            int64                  `json:"writerLag,omitempty"`
	Segments             []ContextSegmentV1     `json:"segments,omitempty"`
	Exclusions           []ContextExclusionV1   `json:"exclusions,omitempty"`
}

func (p ContextProfile) TotalTokens() int {
	total := 0
	for _, contribution := range p.Contributions {
		total = saturatingContextTokenSum(total, contribution.Tokens)
	}
	return total
}

func saturatingContextTokenSum(left, right int) int {
	left, right = max(0, left), max(0, right)
	if left > int(^uint(0)>>1)-right {
		return int(^uint(0) >> 1)
	}
	return left + right
}

type Event struct {
	Kind              EventKind
	SessionID         string
	RunID             string
	AgentID           string
	ToolCallID        string
	ApprovalID        string
	UserInputID       string
	PlanID            string
	Text              string
	TextPhase         string
	State             string
	Data              map[string]string
	Agent             *AgentStatePayload
	AgentBlocks       []AgentTranscriptBlock
	AgentCatalog      []AgentCatalogEntry
	AgentSnapshots    []AgentSnapshotPayload
	SkillCatalog      []SkillCatalogEntry
	SkillDiagnostics  []SkillDiagnostic
	PluginCatalog     []PluginCatalogEntry
	PluginDiagnostics []PluginDiagnostic
	HookCatalog       *HookCatalogSnapshot
	ContextProfile    *ContextProfile
	Todo              *session.TodoList
	Memories          []memory.Memory
	Recap             *recap.Recap
	ModelRoutes       []ModelRouteEntry
	ModelProviders    []ModelProviderEntry
	Background        []backgroundservice.Process
	BackgroundLogs    *backgroundservice.LogSnapshot
	GitBranches       []GitBranchEntry
	UsageReport       *session.UsageReport
	WorkspaceDirty    bool
	At                time.Time
}

func (e Event) Clone() Event {
	cloned := e
	if e.Data != nil {
		cloned.Data = make(map[string]string, len(e.Data))
		for key, value := range e.Data {
			cloned.Data[key] = value
		}
	}
	if e.Agent != nil {
		agent := *e.Agent
		cloned.Agent = &agent
	}
	if e.AgentBlocks != nil {
		cloned.AgentBlocks = append([]AgentTranscriptBlock(nil), e.AgentBlocks...)
	}
	if e.AgentCatalog != nil {
		cloned.AgentCatalog = append([]AgentCatalogEntry(nil), e.AgentCatalog...)
	}
	if e.AgentSnapshots != nil {
		cloned.AgentSnapshots = append([]AgentSnapshotPayload(nil), e.AgentSnapshots...)
	}
	if e.SkillCatalog != nil {
		cloned.SkillCatalog = append([]SkillCatalogEntry(nil), e.SkillCatalog...)
	}
	if e.SkillDiagnostics != nil {
		cloned.SkillDiagnostics = append([]SkillDiagnostic(nil), e.SkillDiagnostics...)
	}
	if e.PluginCatalog != nil {
		cloned.PluginCatalog = append([]PluginCatalogEntry(nil), e.PluginCatalog...)
		for i := range cloned.PluginCatalog {
			cloned.PluginCatalog[i].Capabilities = append([]string(nil), e.PluginCatalog[i].Capabilities...)
		}
	}
	if e.PluginDiagnostics != nil {
		cloned.PluginDiagnostics = append([]PluginDiagnostic(nil), e.PluginDiagnostics...)
	}
	if e.HookCatalog != nil {
		catalog := *e.HookCatalog
		catalog.Sources = append([]HookSourceEntry(nil), e.HookCatalog.Sources...)
		catalog.Commands = append([]HookCommandEntry(nil), e.HookCatalog.Commands...)
		catalog.Diagnostics = append([]HookDiagnostic(nil), e.HookCatalog.Diagnostics...)
		cloned.HookCatalog = &catalog
	}
	if e.ContextProfile != nil {
		profile := *e.ContextProfile
		profile.Contributions = append([]ContextContribution(nil), e.ContextProfile.Contributions...)
		cloned.ContextProfile = &profile
	}
	if e.Todo != nil {
		todo := e.Todo.Clone()
		cloned.Todo = &todo
	}
	if e.Memories != nil {
		cloned.Memories = append([]memory.Memory(nil), e.Memories...)
	}
	if e.Recap != nil {
		value := *e.Recap
		cloned.Recap = &value
	}
	if e.ModelRoutes != nil {
		cloned.ModelRoutes = append([]ModelRouteEntry(nil), e.ModelRoutes...)
	}
	if e.ModelProviders != nil {
		cloned.ModelProviders = append([]ModelProviderEntry(nil), e.ModelProviders...)
		for i := range cloned.ModelProviders {
			if e.ModelProviders[i].Models != nil {
				cloned.ModelProviders[i].Models = make([]config.LLMuxModelConfig, len(e.ModelProviders[i].Models))
				copy(cloned.ModelProviders[i].Models, e.ModelProviders[i].Models)
			}
			for j := range cloned.ModelProviders[i].Models {
				cloned.ModelProviders[i].Models[j].ReasoningLevels = append([]string(nil), e.ModelProviders[i].Models[j].ReasoningLevels...)
				cloned.ModelProviders[i].Models[j].Capabilities = append([]string(nil), e.ModelProviders[i].Models[j].Capabilities...)
				cloned.ModelProviders[i].Models[j].InputModalities = append([]string(nil), e.ModelProviders[i].Models[j].InputModalities...)
				cloned.ModelProviders[i].Models[j].OutputModalities = append([]string(nil), e.ModelProviders[i].Models[j].OutputModalities...)
			}
		}
	}
	if e.Background != nil {
		cloned.Background = append([]backgroundservice.Process(nil), e.Background...)
	}
	if e.BackgroundLogs != nil {
		value := *e.BackgroundLogs
		value.Lines = append([]string(nil), e.BackgroundLogs.Lines...)
		cloned.BackgroundLogs = &value
	}
	if e.GitBranches != nil {
		cloned.GitBranches = append([]GitBranchEntry(nil), e.GitBranches...)
	}
	if e.UsageReport != nil {
		report := e.UsageReport.Clone()
		cloned.UsageReport = &report
	}
	return cloned
}
