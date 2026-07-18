package app

import "time"

type EventKind string

const (
	EventBootstrapDone     EventKind = "bootstrap_done"
	EventSessionLoaded     EventKind = "session_loaded"
	EventRunStarted        EventKind = "run_started"
	EventContextUsage      EventKind = "context_usage"
	EventAgentState        EventKind = "agent_state"
	EventAgentDetail       EventKind = "agent_detail"
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
)

type AgentStatePayload struct {
	Type               string
	Description        string
	Model              string
	Background         bool
	CapabilityMode     string
	RequestedIsolation string
	Isolation          string
	CWD                string
	ParentRunID        string
	ParentToolCallID   string
	ChildRunID         string
	Activity           string
	Warning            string
	WorktreePath       string
	ToolCalls          int
	Turns              int
	TokensUsed         int
	ElapsedMS          int64
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

type Event struct {
	Kind             EventKind
	SessionID        string
	RunID            string
	AgentID          string
	ToolCallID       string
	ApprovalID       string
	Text             string
	State            string
	Data             map[string]string
	Agent            *AgentStatePayload
	AgentBlocks      []AgentTranscriptBlock
	AgentCatalog     []AgentCatalogEntry
	AgentSnapshots   []AgentSnapshotPayload
	SkillCatalog     []SkillCatalogEntry
	SkillDiagnostics []SkillDiagnostic
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
	return cloned
}
