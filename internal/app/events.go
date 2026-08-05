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
	EventBootstrapDone     EventKind = "bootstrap_done"
	EventSessionLoaded     EventKind = "session_loaded"
	EventTodoUpdated       EventKind = "todo_updated"
	EventRunStarted        EventKind = "run_started"
	EventContextUsage      EventKind = "context_usage"
	EventContextProfile    EventKind = "context_profile"
	EventAgentState        EventKind = "agent_state"
	EventAgentDetail       EventKind = "agent_detail"
	EventProviderRetry     EventKind = "provider_retry"
	EventThinkingDelta     EventKind = "thinking_delta"
	EventTextDelta         EventKind = "text_delta"
	EventToolStarted       EventKind = "tool_started"
	EventToolUpdate        EventKind = "tool_update"
	EventToolFinished      EventKind = "tool_finished"
	EventDiffReady         EventKind = "diff_ready"
	EventApprovalRequested EventKind = "approval_requested"
	EventApprovalResolved  EventKind = "approval_resolved"
	EventApprovalMode      EventKind = "approval_mode"
	EventModelCatalog      EventKind = "model_catalog"
	EventSkillCatalog      EventKind = "skill_catalog"
	EventAuthState         EventKind = "auth_state"
	EventMCPState          EventKind = "mcp_state"
	EventRecoveryState     EventKind = "recovery_state"
	EventRunFinished       EventKind = "run_finished"
	EventRunFailed         EventKind = "run_failed"
	EventRunCancelled      EventKind = "run_cancelled"
	EventHookStarted       EventKind = "hook_started"
	EventHookFinished      EventKind = "hook_finished"
	EventHookDiagnostic    EventKind = "hook_diagnostic"
	EventMemoryState       EventKind = "memory_state"
	EventRecapState        EventKind = "recap_state"
	EventModelRoutes       EventKind = "model_routes"
	EventBackgroundState   EventKind = "background_state"
	EventBackgroundLogs    EventKind = "background_logs"
	EventGitBranches       EventKind = "git_branches"
)

type ModelRouteEntry struct {
	Scope string
	Role  string
	Label string
	Route config.ModelRouteConfig
}

type GitBranchEntry struct {
	Name    string
	Current bool
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
	ID         string
	Kind       string
	RunID      string
	ToolCallID string
	Title      string
	Content    string
	State      string
}

type AgentCatalogEntry struct {
	Name           string
	Description    string
	Persona        string
	Model          string
	Reasoning      string
	CapabilityMode string
	Isolation      string
	Source         string
	Enabled        bool
}

type SkillCatalogEntry struct {
	Name          string
	Description   string
	SourcePath    string
	Bundled       bool
	Eager         bool
	Disabled      bool
	ModelVisible  bool
	ResourceCount int
}

type SkillDiagnostic struct {
	Path    string
	Message string
}

type AgentSnapshotPayload struct {
	ID      string
	State   string
	Summary string
	Agent   AgentStatePayload
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
	Source               string                `json:"source"`
	Estimated            bool                  `json:"estimated"`
	Contributions        []ContextContribution `json:"contributions"`
	ReportedInputTokens  int                   `json:"reportedInputTokens,omitempty"`
	ReportedOutputTokens int                   `json:"reportedOutputTokens,omitempty"`
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
	Kind             EventKind
	SessionID        string
	RunID            string
	AgentID          string
	ToolCallID       string
	ApprovalID       string
	Text             string
	TextPhase        string
	State            string
	Data             map[string]string
	Agent            *AgentStatePayload
	AgentBlocks      []AgentTranscriptBlock
	AgentCatalog     []AgentCatalogEntry
	AgentSnapshots   []AgentSnapshotPayload
	SkillCatalog     []SkillCatalogEntry
	SkillDiagnostics []SkillDiagnostic
	ContextProfile   *ContextProfile
	Todo             *session.TodoList
	Memories         []memory.Memory
	Recap            *recap.Recap
	ModelRoutes      []ModelRouteEntry
	Background       []backgroundservice.Process
	BackgroundLogs   *backgroundservice.LogSnapshot
	GitBranches      []GitBranchEntry
	WorkspaceDirty   bool
	At               time.Time
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
	return cloned
}
