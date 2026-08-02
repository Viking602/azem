package desktop

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

const EventName = "azem:event"

type EventEmitter func(string, ...any) bool

type Snapshot struct {
	Workspace           string `json:"workspace"`
	SessionID           string `json:"sessionId"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	Reasoning           string `json:"reasoning"`
	AgentMode           string `json:"agentMode"`
	Language            string `json:"language"`
	ApprovalMode        string `json:"approvalMode"`
	SubagentConcurrency int    `json:"subagentConcurrency"`
	ChatGPTFastMode     bool   `json:"chatgptFastMode"`
	Sequence            uint64 `json:"sequence"`
}

type TurnRequest struct {
	SessionID        string       `json:"sessionId"`
	Prompt           string       `json:"prompt"`
	Provider         string       `json:"provider"`
	Model            string       `json:"model"`
	Reasoning        string       `json:"reasoning"`
	AgentMode        string       `json:"agentMode"`
	PlanMode         bool         `json:"planMode"`
	DisableSubagents bool         `json:"disableSubagents"`
	ActiveSkills     []string     `json:"activeSkills"`
	Images           []Attachment `json:"images"`
}

type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
}

type ActionRequest struct {
	Kind      string                   `json:"kind"`
	Target    string                   `json:"target"`
	Decision  string                   `json:"decision"`
	SessionID string                   `json:"sessionId"`
	Name      string                   `json:"name"`
	CWD       string                   `json:"cwd"`
	Offset    int                      `json:"offset"`
	Limit     int                      `json:"limit"`
	Route     *azemapp.ModelRouteEntry `json:"route,omitempty"`
}

type Event struct {
	Sequence         uint64                         `json:"sequence"`
	Kind             string                         `json:"kind"`
	SessionID        string                         `json:"sessionId,omitempty"`
	RunID            string                         `json:"runId,omitempty"`
	AgentID          string                         `json:"agentId,omitempty"`
	ToolCallID       string                         `json:"toolCallId,omitempty"`
	ApprovalID       string                         `json:"approvalId,omitempty"`
	Text             string                         `json:"text,omitempty"`
	State            string                         `json:"state,omitempty"`
	Data             map[string]string              `json:"data,omitempty"`
	Agent            *azemapp.AgentStatePayload     `json:"agent,omitempty"`
	AgentBlocks      []azemapp.AgentTranscriptBlock `json:"agentBlocks,omitempty"`
	AgentCatalog     []azemapp.AgentCatalogEntry    `json:"agentCatalog,omitempty"`
	AgentSnapshots   []azemapp.AgentSnapshotPayload `json:"agentSnapshots,omitempty"`
	SkillCatalog     []azemapp.SkillCatalogEntry    `json:"skillCatalog,omitempty"`
	SkillDiagnostics []azemapp.SkillDiagnostic      `json:"skillDiagnostics,omitempty"`
	ContextProfile   *azemapp.ContextProfile        `json:"contextProfile,omitempty"`
	Todo             any                            `json:"todo,omitempty"`
	Memories         any                            `json:"memories,omitempty"`
	Recap            any                            `json:"recap,omitempty"`
	ModelRoutes      []azemapp.ModelRouteEntry      `json:"modelRoutes,omitempty"`
	Background       any                            `json:"background,omitempty"`
	BackgroundLogs   any                            `json:"backgroundLogs,omitempty"`
	GitBranches      []azemapp.GitBranchEntry       `json:"gitBranches,omitempty"`
	WorkspaceDirty   bool                           `json:"workspaceDirty,omitempty"`
	At               time.Time                      `json:"at"`
}

type Bridge struct {
	runtime     *azemapp.Service
	cfg         config.Config
	workspace   string
	sessionID   string
	openProject func(string) error
	emit        EventEmitter
	ctx         context.Context
	cancel      context.CancelFunc
	start       sync.Once
	sequence    atomic.Uint64
}

func NewBridge(parent context.Context, boot azemapp.BootstrapResult, emit EventEmitter, openProject func(string) error) *Bridge {
	ctx, cancel := context.WithCancel(parent)
	return &Bridge{
		runtime: boot.Service, cfg: boot.Config, workspace: boot.Paths.Workspace,
		sessionID: boot.SessionID, openProject: openProject, emit: emit, ctx: ctx, cancel: cancel,
	}
}

func (b *Bridge) Initialise() Snapshot {
	b.start.Do(func() {
		go b.pump()
		go b.prime()
	})
	return Snapshot{
		Workspace: b.workspace, SessionID: b.sessionID,
		Provider: b.cfg.Defaults.Provider, Model: b.cfg.Defaults.Model,
		Reasoning: b.cfg.Defaults.Reasoning, AgentMode: b.cfg.Defaults.AgentMode,
		Language: b.cfg.Defaults.Language, ApprovalMode: b.cfg.Defaults.ApprovalMode,
		SubagentConcurrency: b.cfg.Agents.Subagents.MaxConcurrency,
		ChatGPTFastMode:     b.cfg.Providers.ChatGPT.FastMode,
		Sequence:            b.sequence.Load(),
	}
}

func (b *Bridge) StartTurn(request TurnRequest) (string, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return "", fmt.Errorf("prompt is empty")
	}
	return b.runtime.StartConfiguredTurn(azemapp.TurnRequest{
		SessionID: request.SessionID, Prompt: request.Prompt,
		Provider: request.Provider, Model: request.Model, Reasoning: request.Reasoning,
		AgentMode: request.AgentMode, PlanMode: request.PlanMode,
		DisableSubagents: request.DisableSubagents,
		ActiveSkills:     append([]string(nil), request.ActiveSkills...),
		Images:           attachmentsToSession(request.Images),
	})
}

func (b *Bridge) ImportAttachment(sessionID, name, mimeType, encoded string) (Attachment, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Attachment{}, fmt.Errorf("decode attachment: %w", err)
	}
	item, err := b.runtime.ImportImageBytes(sessionID, name, mimeType, data)
	if err != nil {
		return Attachment{}, err
	}
	return attachmentFromSession(item), nil
}

func (b *Bridge) Guide(sessionID, runID, text string) error {
	return b.runtime.GuideActiveTurn(sessionID, runID, text)
}

func (b *Bridge) CancelActive(includeChildren bool) bool {
	return b.runtime.CancelActiveWithChildren(includeChildren)
}

func (b *Bridge) Execute(request ActionRequest) error {
	kind := azemapp.ActionKind(request.Kind)
	if !allowedAction(kind) {
		return fmt.Errorf("desktop action %q is not allowed", request.Kind)
	}
	return b.runtime.ExecuteAction(b.ctx, azemapp.Action{
		Kind: kind, Target: request.Target, Decision: request.Decision,
		SessionID: request.SessionID, Route: request.Route,
		Name: request.Name, CWD: request.CWD, Offset: request.Offset, Limit: request.Limit,
	})
}

func (b *Bridge) ForkSession(sessionID string, activate bool) (string, error) {
	return b.runtime.ForkSession(b.ctx, sessionID, activate)
}

func (b *Bridge) Close() { b.cancel() }

func (b *Bridge) prime() {
	actions := []azemapp.Action{
		{Kind: azemapp.ActionListSessions},
		{Kind: azemapp.ActionListGitBranches},
		{Kind: azemapp.ActionListModels},
		{Kind: azemapp.ActionListModelRoutes},
		{Kind: azemapp.ActionListAgentTypes, SessionID: b.sessionID},
		{Kind: azemapp.ActionListSkills, SessionID: b.sessionID},
	}
	for _, action := range actions {
		if err := b.runtime.ExecuteAction(b.ctx, action); err != nil && !errors.Is(err, context.Canceled) {
			b.emitEvent(Event{Kind: "bridge_error", State: "failed", Text: err.Error(), At: time.Now().UTC()})
		}
	}
}

func (b *Bridge) pump() {
	for {
		event, err := b.runtime.NextEvent(b.ctx)
		if err != nil {
			if b.ctx.Err() == nil {
				b.emitEvent(Event{Kind: "bridge_error", State: "failed", Text: err.Error(), At: time.Now().UTC()})
			}
			return
		}
		b.emitEvent(eventDTO(event))
	}
}

func (b *Bridge) emitEvent(event Event) {
	event.Sequence = b.sequence.Add(1)
	if b.emit != nil {
		b.emit(EventName, event)
	}
}

func eventDTO(event azemapp.Event) Event {
	return Event{
		Kind: string(event.Kind), SessionID: event.SessionID, RunID: event.RunID,
		AgentID: event.AgentID, ToolCallID: event.ToolCallID, ApprovalID: event.ApprovalID,
		Text: event.Text, State: event.State, Data: event.Data,
		Agent: event.Agent, AgentBlocks: event.AgentBlocks, AgentCatalog: event.AgentCatalog,
		AgentSnapshots: event.AgentSnapshots, SkillCatalog: event.SkillCatalog,
		SkillDiagnostics: event.SkillDiagnostics, ContextProfile: event.ContextProfile,
		Todo: event.Todo, Memories: event.Memories, Recap: event.Recap,
		ModelRoutes: event.ModelRoutes, Background: event.Background,
		BackgroundLogs: event.BackgroundLogs, GitBranches: event.GitBranches,
		WorkspaceDirty: event.WorkspaceDirty, At: event.At,
	}
}

func allowedAction(kind azemapp.ActionKind) bool {
	switch kind {
	case azemapp.ActionLogin, azemapp.ActionLogout,
		azemapp.ActionNewSession, azemapp.ActionListSessions, azemapp.ActionResumeSession,
		azemapp.ActionRenameSession, azemapp.ActionPinSession, azemapp.ActionArchiveSession, azemapp.ActionMarkSessionUnread,
		azemapp.ActionCompact, azemapp.ActionResolveApproval, azemapp.ActionSetApprovalMode,
		azemapp.ActionSetLanguage, azemapp.ActionReconcileAttempt,
		azemapp.ActionInspectAgent, azemapp.ActionListAgentTypes, azemapp.ActionListPersonas,
		azemapp.ActionCancelAgent, azemapp.ActionRefreshMCP, azemapp.ActionReconnectMCP,
		azemapp.ActionListSkills, azemapp.ActionReloadSkills,
		azemapp.ActionListMemories, azemapp.ActionRemember, azemapp.ActionForgetMemory,
		azemapp.ActionShowRecap, azemapp.ActionListModels, azemapp.ActionListModelRoutes, azemapp.ActionSetModelRoute,
		azemapp.ActionResetModelRoute, azemapp.ActionSetSubagentConcurrency,
		azemapp.ActionSetChatGPTFastMode, azemapp.ActionListBackground,
		azemapp.ActionStartBackground, azemapp.ActionStopBackground, azemapp.ActionLogsBackground,
		azemapp.ActionListGitBranches, azemapp.ActionSwitchGitBranch:
		return true
	default:
		return false
	}
}

func attachmentsToSession(items []Attachment) []session.Attachment {
	result := make([]session.Attachment, len(items))
	for index, item := range items {
		result[index] = session.Attachment{ID: item.ID, Name: item.Name, MIME: item.MIMEType, Path: item.Path, Size: item.Size}
	}
	return result
}

func attachmentFromSession(item session.Attachment) Attachment {
	return Attachment{ID: item.ID, Name: item.Name, MIMEType: item.MIME, Path: item.Path, Size: item.Size}
}
