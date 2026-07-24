package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Viking602/go-hydaelyn/api"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/session"
)

type ApprovalMode string

const (
	ApprovalModePrompt     ApprovalMode = "prompt"
	ApprovalModeAutoReview ApprovalMode = "auto_review"
	ApprovalModeYolo       ApprovalMode = "yolo"
)

type ActionKind string

const (
	ActionLogin            ActionKind = "login"
	ActionLogout           ActionKind = "logout"
	ActionNewSession       ActionKind = "new_session"
	ActionListSessions     ActionKind = "list_sessions"
	ActionResumeSession    ActionKind = "resume_session"
	ActionCompact          ActionKind = "compact"
	ActionResolveApproval  ActionKind = "resolve_approval"
	ActionSetApprovalMode  ActionKind = "set_approval_mode"
	ActionSetLanguage      ActionKind = "set_language"
	ActionReconcileAttempt ActionKind = "reconcile_attempt"
	ActionInspectAgent     ActionKind = "inspect_agent"
	ActionListAgentTypes   ActionKind = "list_agent_types"
	ActionListPersonas     ActionKind = "list_personas"
	ActionCancelAgent      ActionKind = "cancel_agent"
	ActionRefreshMCP       ActionKind = "refresh_mcp"
	ActionReconnectMCP     ActionKind = "reconnect_mcp"
	ActionListSkills       ActionKind = "list_skills"
	ActionReloadSkills     ActionKind = "reload_skills"
	ActionListMemories     ActionKind = "list_memories"
	ActionRemember         ActionKind = "remember"
	ActionForgetMemory     ActionKind = "forget_memory"
	ActionShowRecap        ActionKind = "show_recap"
	ActionListModelRoutes  ActionKind = "list_model_routes"
	ActionSetModelRoute    ActionKind = "set_model_route"
	ActionResetModelRoute  ActionKind = "reset_model_route"
)

type Action struct {
	Kind      ActionKind
	Target    string
	Decision  string
	SessionID string
	Route     *ModelRouteEntry
}

type ActionExecutor interface {
	ExecuteAction(context.Context, Action) error
}

type ReconcileResolver interface {
	ResolveReconcileAttempt(context.Context, string, api.ActionAttemptStatus, string) error
}

func (s *Service) AttachReconcileResolver(resolver ReconcileResolver) {
	s.reconciler = resolver
}

func (s *Service) ExecuteAction(ctx context.Context, action Action) error {
	switch action.Kind {
	case ActionListModelRoutes:
		s.emit(ctx, Event{Kind: EventModelRoutes, State: "listed", ModelRoutes: s.modelRouteEntries()})
		return nil
	case ActionSetModelRoute:
		return s.updateModelRoute(ctx, action.Route, false)
	case ActionResetModelRoute:
		return s.updateModelRoute(ctx, action.Route, true)
	case ActionListMemories:
		if s.memory == nil {
			return fmt.Errorf("memory is unavailable")
		}
		items, err := s.memory.List(ctx, action.Target, 20)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventMemoryState, State: "listed", Memories: items})
		return nil
	case ActionRemember:
		if s.memory == nil {
			return fmt.Errorf("memory is unavailable")
		}
		item, err := s.memory.Remember(ctx, action.Target, action.SessionID, "manual", 50)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventMemoryState, SessionID: action.SessionID, State: "remembered", Memories: []memory.Memory{item}})
		return nil
	case ActionForgetMemory:
		if s.memory == nil {
			return fmt.Errorf("memory is unavailable")
		}
		if err := s.memory.Forget(ctx, action.Target); err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventMemoryState, State: "forgotten", Text: action.Target})
		return nil
	case ActionShowRecap:
		item, err := s.loadRecap(ctx, action.SessionID)
		if err != nil {
			return err
		}
		state := "loaded"
		if item == nil {
			state = "empty"
		}
		s.emit(ctx, Event{Kind: EventRecapState, SessionID: action.SessionID, State: state, Recap: item})
		return nil
	case ActionListSkills:
		return s.emitSkillCatalog(ctx, "listed")
	case ActionReloadSkills:
		if s.skillCatalog == nil {
			return fmt.Errorf("skills are unavailable")
		}
		if err := s.skillCatalog.Reload(); err != nil {
			return err
		}
		return s.emitSkillCatalog(ctx, "reloaded")
	case ActionSetApprovalMode:
		return s.setApprovalMode(ctx, ApprovalMode(action.Target))
	case ActionSetLanguage:
		if action.Target != "en" && action.Target != "zh-CN" {
			return fmt.Errorf("invalid language %q", action.Target)
		}
		if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(s.currentSession, ""), func(e *hooks.Envelope) {
			e.Source, e.FilePath = "user_settings", s.configPath
		}); err != nil {
			return err
		}
		if s.configPath != "" {
			if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
				return config.UpdateDefault(s.configPath, "language", action.Target)
			}); err != nil {
				return err
			}
		}
		s.mu.Lock()
		s.cfg.Defaults.Language = action.Target
		s.mu.Unlock()
		return nil
	case ActionResolveApproval:
		if s.coding == nil {
			return fmt.Errorf("coding runtime is unavailable")
		}
		if resolved, err := s.resolveLiveApproval(ctx, action.Target, action.Decision, "user"); resolved {
			return err
		}
		for _, pending := range s.recovery.Approvals {
			if pending.Approval.ApprovalID != action.Target {
				continue
			}
			if err := s.coding.ResolveRecoveredApproval(ctx, pending.Approval.RunID, pending.Approval.ApprovalID, pending.Token.TokenID, action.Decision); err != nil {
				return err
			}
			s.emit(ctx, Event{Kind: EventApprovalResolved, RunID: pending.Approval.RunID, State: action.Decision, Text: pending.Approval.ApprovalID})
			return nil
		}
		return fmt.Errorf("approval %q is not pending", action.Target)
	case ActionReconcileAttempt:
		if s.reconciler == nil {
			return fmt.Errorf("action reconciliation is unavailable")
		}
		status, err := reconciledStatus(action.Decision)
		if err != nil {
			return err
		}
		if err := s.reconciler.ResolveReconcileAttempt(ctx, action.Target, status, "user-confirmed"); err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventRecoveryState, State: "reconciled", Text: action.Target, Data: map[string]string{"decision": string(status)}})
		return nil
	case ActionLogout:
		if s.authentication == nil {
			return fmt.Errorf("authentication is unavailable")
		}
		if action.Target == "" {
			return fmt.Errorf("logout requires a provider or provider/account id")
		}
		provider, accountID, _ := strings.Cut(action.Target, "/")
		if accountID == "" {
			accounts, err := s.authentication.Accounts(ctx, provider)
			if err != nil {
				return err
			}
			for _, account := range accounts {
				if account.Status == "active" {
					accountID = account.ID
					break
				}
			}
		}
		if accountID == "" {
			return fmt.Errorf("no active %s account is available", provider)
		}
		if err := s.authentication.Logout(ctx, provider, accountID); err != nil {
			return err
		}
		return nil
	case ActionNewSession:
		return s.createSession(ctx, action.Target)
	case ActionResumeSession:
		return s.emitSession(ctx, action.Target)
	case ActionListSessions:
		return s.emitSessionList(ctx)
	case ActionCompact:
		if s.sessions == nil {
			return fmt.Errorf("session store is unavailable")
		}
		const compactReservation = "maintenance:compact"
		s.mu.Lock()
		if s.shuttingDown {
			s.mu.Unlock()
			return fmt.Errorf("application is shutting down")
		}
		if s.activeRun != "" {
			s.mu.Unlock()
			return ErrRunActive
		}
		s.activeRun = compactReservation
		s.activeSession = action.Target
		s.mu.Unlock()
		defer s.clearRun(compactReservation)
		projection, err := s.sessions.LoadProjection(ctx, action.Target)
		if err != nil {
			return err
		}
		if !manualCompactionEligible(projection.Blocks) {
			return ErrNothingToCompact
		}
		if s.providers == nil {
			return fmt.Errorf("compaction model runtime is unavailable")
		}
		if err := s.dispatchLifecycle(ctx, hooks.PreCompact, s.hookMetadata(action.Target, ""), func(e *hooks.Envelope) { e.Trigger = "manual" }); err != nil {
			return err
		}
		plan, changed, prepareErr := s.providers.PrepareManualCompaction(ctx, projection)
		if prepareErr != nil {
			return prepareErr
		}
		if !changed {
			return ErrNothingToCompact
		}
		projection, err = s.sessions.CompactWithSummary(ctx, action.Target, plan)
		if err != nil {
			return err
		}
		// Compaction shrinks the live context. Clear stale main occupancy while
		// preserving the independently attributed compaction and cache totals.
		cleared, err := s.clearMainUsageOccupancy(ctx, action.Target, projection.Usage)
		if err != nil {
			return err
		}
		projection.Usage = cleared
		blocks, err := json.Marshal(projection.Blocks)
		if err != nil {
			return err
		}
		todo, err := s.sessions.LoadTodo(ctx, action.Target)
		if err != nil {
			return err
		}
		currentRecap, err := s.loadRecap(ctx, action.Target)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{
			Kind: EventSessionLoaded, SessionID: action.Target, State: "compacted",
			Data: sessionProjectionData(projection, string(blocks)), AgentSnapshots: s.subagentSnapshots(ctx, action.Target), Todo: &todo, Recap: currentRecap,
		})
		_ = s.dispatchLifecycle(ctx, hooks.PostCompact, s.hookMetadata(action.Target, ""), func(e *hooks.Envelope) {
			e.Trigger = "manual"
			e.CompactSummary = fmt.Sprintf("Session compacted to %d persisted blocks.", len(projection.Blocks))
		})
		return nil
	case ActionLogin:
		return s.login(ctx, action.Target)
	case ActionInspectAgent:
		if s.providers == nil {
			return fmt.Errorf("subagent runtime is unavailable")
		}
		blocks, err := s.providers.DetailSubagent(ctx, action.SessionID, action.Target)
		if err != nil {
			return fmt.Errorf("inspect subagent %q: %w", action.Target, err)
		}
		s.emit(ctx, Event{
			Kind: EventAgentDetail, SessionID: action.SessionID, AgentID: action.Target,
			State: "detail", AgentBlocks: blocks,
		})
		return nil
	case ActionListAgentTypes:
		s.emit(ctx, Event{Kind: EventAgentDetail, SessionID: action.SessionID, State: "agent_types", AgentCatalog: s.agentTypeCatalog()})
		return nil
	case ActionListPersonas:
		s.emit(ctx, Event{Kind: EventAgentDetail, SessionID: action.SessionID, State: "personas", AgentCatalog: s.personaCatalog()})
		return nil
	case ActionCancelAgent:
		if s.providers == nil {
			return fmt.Errorf("subagent runtime is unavailable")
		}
		outcome := s.providers.CancelSubagent(action.SessionID, action.Target)
		if outcome.Outcome == "not_found" {
			return fmt.Errorf("subagent %q was not found", action.Target)
		}
		s.emit(ctx, Event{
			Kind: EventAgentDetail, SessionID: action.SessionID, AgentID: action.Target, State: outcome.Outcome,
			Text: map[string]string{
				"cancel_requested": "Child cancellation requested.",
				"already_finished": "Child task already finished.",
			}[outcome.Outcome],
		})
		return nil
	case ActionRefreshMCP:
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		if err := s.mcp.Refresh(ctx, action.Target); err != nil {
			return err
		}
		return s.emitMCPSnapshot(ctx)
	case ActionReconnectMCP:
		if s.mcp == nil {
			return fmt.Errorf("no MCP manager is attached")
		}
		if err := s.mcp.Reconnect(ctx, action.Target); err != nil {
			return err
		}
		return s.emitMCPSnapshot(ctx)
	default:
		return fmt.Errorf("unsupported action %q", action.Kind)
	}
}

func (s *Service) modelRouteEntries() []ModelRouteEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := []ModelRouteEntry{{Scope: "compaction", Label: "Compaction", Route: s.cfg.Agents.Compaction}}
	names := make([]string, 0, len(s.cfg.Agents.Subagents.Roles))
	for name := range s.cfg.Agents.Subagents.Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		role := s.cfg.Agents.Subagents.Roles[name]
		entries = append(entries, ModelRouteEntry{Scope: "subagent", Role: name, Label: firstNonempty(role.Description, name), Route: config.ModelRouteConfig{Provider: role.Provider, Model: role.Model, Reasoning: role.Reasoning}})
	}
	return entries
}

func (s *Service) updateModelRoute(ctx context.Context, entry *ModelRouteEntry, reset bool) error {
	if entry == nil {
		return fmt.Errorf("model route is required")
	}
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	if entry.Scope != "compaction" && entry.Scope != "subagent" {
		return fmt.Errorf("unsupported model route scope %q", entry.Scope)
	}
	if entry.Scope == "compaction" && entry.Role != "" {
		return fmt.Errorf("role is not valid for compaction route")
	}
	s.mu.Lock()
	_, roleExists := s.cfg.Agents.Subagents.Roles[entry.Role]
	currentSession := s.currentSession
	s.mu.Unlock()
	if entry.Scope == "subagent" && (!roleExists || strings.TrimSpace(entry.Role) == "") {
		return fmt.Errorf("unknown subagent role %q", entry.Role)
	}
	route := entry.Route
	if reset {
		route = config.ModelRouteConfig{}
	} else {
		if strings.TrimSpace(route.Provider) == "" || strings.TrimSpace(route.Model) == "" {
			return fmt.Errorf("model route must set both provider and model")
		}
		if s.providers == nil {
			return fmt.Errorf("provider runtime is unavailable")
		}
		if _, _, _, _, err := s.providers.resolveDriver(ctx, route.Provider, route.Model, route.Reasoning); err != nil {
			return err
		}
	}
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			if reset {
				return config.ResetModelRoute(s.configPath, entry.Scope, entry.Role)
			}
			return config.UpdateModelRoute(s.configPath, entry.Scope, entry.Role, route)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	if entry.Scope == "compaction" {
		s.cfg.Agents.Compaction = route
	} else {
		role := s.cfg.Agents.Subagents.Roles[entry.Role]
		role.Provider, role.Model, role.Reasoning = route.Provider, route.Model, route.Reasoning
		s.cfg.Agents.Subagents.Roles[entry.Role] = role
		delete(s.cfg.Agents.Subagents.Models, entry.Role)
		if reset {
			delete(s.cfg.Agents.Subagents.Routes, entry.Role)
		} else {
			s.cfg.Agents.Subagents.Routes[entry.Role] = route
		}
	}
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateModelRoute(entry.Scope, entry.Role, route)
	}
	s.emit(ctx, Event{Kind: EventModelRoutes, State: "updated", ModelRoutes: s.modelRouteEntries()})
	return nil
}

func (s *Service) emitSkillCatalog(ctx context.Context, state string) error {
	if s.skillCatalog == nil {
		return fmt.Errorf("skills are unavailable")
	}
	snapshot := s.skillCatalog.Snapshot()
	entries := make([]SkillCatalogEntry, len(snapshot.Entries))
	for i, entry := range snapshot.Entries {
		entries[i] = SkillCatalogEntry{
			Name: entry.Name, Description: entry.Description, SourcePath: entry.SourcePath,
			Bundled: entry.Bundled, Eager: entry.Eager, Disabled: entry.Disabled,
			ModelVisible: entry.ModelVisible, ResourceCount: entry.ResourceCount,
		}
	}
	diagnostics := make([]SkillDiagnostic, len(snapshot.Diagnostics))
	for i, diagnostic := range snapshot.Diagnostics {
		diagnostics[i] = SkillDiagnostic{Path: diagnostic.Path, Message: diagnostic.Message}
	}
	s.emit(ctx, Event{
		Kind: EventSkillCatalog, State: state,
		SkillCatalog: entries, SkillDiagnostics: diagnostics,
	})
	return nil
}

func (s *Service) agentTypeCatalog() []AgentCatalogEntry {
	s.mu.Lock()
	roles := cloneSubagentRoles(s.cfg.Agents.Subagents.Roles)
	toggle := cloneBoolMap(s.cfg.Agents.Subagents.Toggle)
	s.mu.Unlock()
	entries := make([]AgentCatalogEntry, 0, len(roles))
	for name, role := range roles {
		enabled := true
		if configured, ok := toggle[name]; ok {
			enabled = configured
		}
		entries = append(entries, AgentCatalogEntry{
			Name: name, Description: role.Description, Persona: role.Persona, Model: role.Model,
			Reasoning: role.Reasoning, CapabilityMode: role.CapabilityMode, Isolation: role.Isolation,
			Source: firstNonempty(role.Source, "builtin"), Enabled: enabled,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func (s *Service) personaCatalog() []AgentCatalogEntry {
	s.mu.Lock()
	personas := cloneSubagentPersonas(s.cfg.Agents.Subagents.Personas)
	s.mu.Unlock()
	entries := make([]AgentCatalogEntry, 0, len(personas))
	for name, persona := range personas {
		entries = append(entries, AgentCatalogEntry{
			Name: name, Description: persona.Description, Model: persona.Model, Reasoning: persona.Reasoning,
			Isolation: persona.Isolation,
			Source:    firstNonempty(persona.Source, "builtin"), Enabled: true,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func reconciledStatus(value string) (api.ActionAttemptStatus, error) {
	switch strings.ToLower(value) {
	case "succeeded", "success", "completed":
		return api.ActionAttemptSucceeded, nil
	case "failed", "failure":
		return api.ActionAttemptFailed, nil
	case "cancelled", "canceled":
		return api.ActionAttemptCancelled, nil
	default:
		return "", fmt.Errorf("reconcile decision must be succeeded, failed, or cancelled")
	}
}

func (s *Service) createSession(ctx context.Context, title string) error {
	if s.sessions == nil {
		return fmt.Errorf("session store is unavailable")
	}
	id, err := randomID("session")
	if err != nil {
		return err
	}
	if title == "" {
		title = "New session"
	}
	projection := session.Projection{Session: session.Session{
		ID: id, Title: title, ProviderID: s.cfg.Defaults.Provider, ModelID: s.cfg.Defaults.Model,
		Reasoning: s.cfg.Defaults.Reasoning, AgentMode: s.cfg.Defaults.AgentMode,
	}}
	s.emit(ctx, Event{Kind: EventSessionLoaded, SessionID: id, State: "new", Data: sessionProjectionData(projection, "[]")})
	if err := s.switchSessionHooks(ctx, id, "clear", projection.Session.ModelID); err != nil {
		return err
	}
	s.mu.Lock()
	s.currentSession = id
	s.mu.Unlock()
	return nil
}

func (s *Service) emitSession(ctx context.Context, id string) error {
	if s.sessions == nil {
		return fmt.Errorf("session store is unavailable")
	}
	if id == "" {
		return fmt.Errorf("session id is required")
	}
	projection, err := s.sessions.LoadProjection(ctx, id)
	if err != nil {
		return err
	}
	blocks, err := json.Marshal(projection.Blocks)
	if err != nil {
		return err
	}
	todo, err := s.sessions.LoadTodo(ctx, id)
	if err != nil {
		return err
	}
	currentRecap, err := s.loadRecap(ctx, id)
	if err != nil {
		return err
	}
	s.rememberSessionUsage(id, projection.Usage)
	s.emit(ctx, Event{
		Kind: EventSessionLoaded, SessionID: id, State: "loaded",
		Data: sessionProjectionData(projection, string(blocks)), AgentSnapshots: s.subagentSnapshots(ctx, id), Todo: &todo, Recap: currentRecap,
	})
	if err := s.switchSessionHooks(ctx, id, "resume", projection.Session.ModelID); err != nil {
		return err
	}
	s.mu.Lock()
	s.currentSession = id
	s.mu.Unlock()
	return nil
}

func (s *Service) subagentSnapshots(ctx context.Context, sessionID string) []AgentSnapshotPayload {
	if s.providers == nil {
		return nil
	}
	snapshots := s.providers.ListSubagents(ctx, sessionID)
	result := make([]AgentSnapshotPayload, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !snapshot.Found {
			continue
		}
		event := subagentStateEvent(snapshot.Run, snapshot.Run.Summary)
		result = append(result, AgentSnapshotPayload{
			ID: snapshot.Run.ID, State: string(snapshot.Run.State), Summary: snapshot.Run.Summary, Agent: *event.Agent,
		})
	}
	return result
}

func (s *Service) emitSessionList(ctx context.Context) error {
	if s.sessions == nil {
		return fmt.Errorf("session store is unavailable")
	}
	sessions, err := s.sessions.List(ctx, 100)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(sessions)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventSessionLoaded, State: "list", Data: map[string]string{"sessions": string(encoded)}})
	return nil
}

func (s *Service) login(ctx context.Context, provider string) error {
	if s.authentication == nil || s.catalog == nil {
		return fmt.Errorf("authentication is unavailable")
	}
	provider, mode, _ := strings.Cut(provider, ":")
	var account auth.Account
	var err error
	switch provider {
	case "chatgpt":
		if mode == "import" {
			codexHome := os.Getenv("CODEX_HOME")
			if codexHome == "" {
				home, homeErr := os.UserHomeDir()
				if homeErr != nil {
					return homeErr
				}
				codexHome = filepath.Join(home, ".codex")
			}
			account, err = s.authentication.ImportChatGPT(ctx, filepath.Join(codexHome, "auth.json"))
			if err != nil && errors.Is(err, os.ErrNotExist) {
				account, err = s.authentication.ImportChatGPTKeyring(ctx, codexHome)
			}
		} else {
			account, err = s.authentication.LoginChatGPT(ctx, openBrowserURL)
		}
	case "grok":
		if mode == "import" {
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				return homeErr
			}
			account, err = s.authentication.ImportGrok(ctx, filepath.Join(home, ".grok", "auth.json"))
		} else {
			account, err = s.authentication.LoginGrok(ctx, func(authorization grok.DeviceAuthorization) error {
				verificationURL := firstNonempty(authorization.VerificationURIComplete, authorization.VerificationURI)
				s.emit(ctx, Event{Kind: EventAuthState, State: "device_authorization", Text: authorization.UserCode, Data: map[string]string{"provider": "grok", "userCode": authorization.UserCode, "verificationURL": verificationURL}})
				return openBrowserURL(verificationURL)
			})
		}
	default:
		return fmt.Errorf("provider must be chatgpt or grok")
	}
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventAuthState, State: "active", Data: map[string]string{
		"provider": account.Provider, "accountID": account.ID, "email": account.Email, "displayName": account.DisplayName, "plan": account.Plan,
	}})
	s.emitApprovalMode(ctx)
	models, err := s.catalog.List(ctx, account.Provider, account.ID, true)
	if err != nil {
		return fmt.Errorf("load %s model catalog: %w", account.Provider, err)
	}

	encoded, err := json.Marshal(models.Models)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventModelCatalog, State: "fresh", Data: map[string]string{"provider": account.Provider, "accountID": account.ID, "models": string(encoded)}})
	return nil
}

func (s *Service) emitAuthCatalog(ctx context.Context) {
	if s.authentication == nil || s.catalog == nil {
		return
	}
	for _, provider := range []string{"chatgpt", "grok"} {
		accounts, err := s.authentication.Accounts(ctx, provider)
		if err != nil {
			continue
		}
		for _, account := range accounts {
			s.emit(ctx, Event{Kind: EventAuthState, State: account.Status, Data: map[string]string{
				"provider": account.Provider, "accountID": account.ID, "email": account.Email, "displayName": account.DisplayName, "plan": account.Plan,
			}})
			if account.Status != "active" {
				continue
			}
			models, err := s.catalog.List(ctx, account.Provider, account.ID, false)
			if err != nil {
				continue
			}
			encoded, err := json.Marshal(models.Models)
			if err != nil {
				continue
			}
			state := "fresh"
			if models.Stale {
				state = "stale"
			}
			s.emit(ctx, Event{Kind: EventModelCatalog, State: state, Text: models.Warning, Data: map[string]string{"provider": account.Provider, "accountID": account.ID, "models": string(encoded)}})
		}
	}
}

func openBrowserURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("browser URL is empty")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func (s *Service) emitMCPSnapshot(ctx context.Context) error {
	if s.mcp == nil {
		return fmt.Errorf("no MCP manager is attached")
	}
	type toolView struct {
		Name             string `json:"name"`
		Description      string `json:"description,omitempty"`
		Effect           string `json:"effect,omitempty"`
		RequiresApproval bool   `json:"requiresApproval,omitempty"`
	}
	type view struct {
		Name      string     `json:"name"`
		State     string     `json:"state"`
		ToolCount int        `json:"toolCount"`
		Tools     []toolView `json:"tools,omitempty"`
		Error     string     `json:"error"`
	}
	snapshots := s.mcp.Servers()
	values := make([]view, 0, len(snapshots))
	for _, snapshot := range snapshots {
		tools := make([]toolView, 0, len(snapshot.Tools))
		for _, tool := range snapshot.Tools {
			tools = append(tools, toolView{Name: tool.Name, Description: tool.Description, Effect: tool.Effect, RequiresApproval: tool.RequiresApproval})
		}
		values = append(values, view{Name: snapshot.Name, State: string(snapshot.State), ToolCount: snapshot.ToolCount, Tools: tools, Error: snapshot.LastError})
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventMCPState, State: "snapshot", Data: map[string]string{"servers": string(encoded)}})
	return nil
}
