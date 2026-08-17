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
	"strconv"
	"strings"
	"time"

	"github.com/Viking602/venat/api"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
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
	ActionLogin                   ActionKind = "login"
	ActionLogout                  ActionKind = "logout"
	ActionNewSession              ActionKind = "new_session"
	ActionListSessions            ActionKind = "list_sessions"
	ActionListUsage               ActionKind = "list_usage"
	ActionResumeSession           ActionKind = "resume_session"
	ActionRefreshSession          ActionKind = "refresh_session"
	ActionRenameSession           ActionKind = "rename_session"
	ActionPinSession              ActionKind = "pin_session"
	ActionArchiveSession          ActionKind = "archive_session"
	ActionArchiveInactiveSessions ActionKind = "archive_inactive_sessions"
	ActionMarkSessionUnread       ActionKind = "mark_session_unread"
	ActionCompact                 ActionKind = "compact"
	ActionResolveApproval         ActionKind = "resolve_approval"
	ActionResolveUserInput        ActionKind = "resolve_user_input"
	ActionResolvePlan             ActionKind = "resolve_plan"
	ActionSetApprovalMode         ActionKind = "set_approval_mode"
	ActionSetLanguage             ActionKind = "set_language"
	ActionSetQueueMode            ActionKind = "set_queue_mode"
	ActionReconcileAttempt        ActionKind = "reconcile_attempt"
	ActionInspectAgent            ActionKind = "inspect_agent"
	ActionListAgentTypes          ActionKind = "list_agent_types"
	ActionListPersonas            ActionKind = "list_personas"
	ActionCancelAgent             ActionKind = "cancel_agent"
	ActionRefreshMCP              ActionKind = "refresh_mcp"
	ActionReconnectMCP            ActionKind = "reconnect_mcp"
	ActionSetMCPEnabled           ActionKind = "set_mcp_enabled"
	ActionUpsertMCPServer         ActionKind = "upsert_mcp_server"
	ActionDeleteMCPServer         ActionKind = "delete_mcp_server"
	ActionListSkills              ActionKind = "list_skills"
	ActionListPlugins             ActionKind = "list_plugins"
	ActionSetPluginImported       ActionKind = "set_plugin_imported"
	ActionListHooks               ActionKind = "list_hooks"
	ActionSetPluginHooksTrusted   ActionKind = "set_plugin_hooks_trusted"
	ActionSetHookEnabled          ActionKind = "set_hook_enabled"
	ActionReloadSkills            ActionKind = "reload_skills"
	ActionSetSkillEnabled         ActionKind = "set_skill_enabled"
	ActionListMemories            ActionKind = "list_memories"
	ActionRemember                ActionKind = "remember"
	ActionForgetMemory            ActionKind = "forget_memory"
	ActionShowRecap               ActionKind = "show_recap"
	ActionListModels              ActionKind = "list_models"
	ActionListModelProviders      ActionKind = "list_model_providers"
	ActionDiscoverProviderModels  ActionKind = "discover_provider_models"
	ActionSetModelProvider        ActionKind = "set_model_provider"
	ActionSetModelEnabled         ActionKind = "set_model_enabled"
	ActionListModelRoutes         ActionKind = "list_model_routes"
	ActionSetModelRoute           ActionKind = "set_model_route"
	ActionResetModelRoute         ActionKind = "reset_model_route"
	ActionSetSubagentConcurrency  ActionKind = "set_subagent_concurrency"
	ActionSetSubagentDepth        ActionKind = "set_subagent_depth"
	ActionSetShellConcurrency     ActionKind = "set_shell_concurrency"
	ActionSetShellMaxWallClock    ActionKind = "set_shell_max_wall_clock"
	ActionSetSubagentAwait        ActionKind = "set_subagent_await_timeout"
	ActionSetSubagentIdle         ActionKind = "set_subagent_idle_timeout"
	ActionSetChatGPTFastMode      ActionKind = "set_chatgpt_fast_mode"
	ActionSetSessionPreferences   ActionKind = "set_session_preferences"
	ActionListBackground          ActionKind = "list_background"
	ActionStartBackground         ActionKind = "start_background"
	ActionStopBackground          ActionKind = "stop_background"
	ActionLogsBackground          ActionKind = "logs_background"
	ActionListGitBranches         ActionKind = "list_git_branches"
	ActionSwitchGitBranch         ActionKind = "switch_git_branch"
	ActionCreateGitBranch         ActionKind = "create_git_branch"
)

type Action struct {
	Kind      ActionKind
	Target    string
	Decision  string
	SessionID string
	Route     *ModelRouteEntry
	Provider  *ModelProviderEntry
	Secret    string
	Name      string
	CWD       string
	Offset    int
	Limit     int
	Payload   json.RawMessage
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

func (s *Service) emitGitBranches(ctx context.Context, state string) error {
	branches, current, dirty, changedFiles, err := s.gitBranchSnapshot(ctx)
	if err != nil {
		return err
	}
	additions, deletions := 0, 0
	if dirty {
		s.mu.Lock()
		root := s.cfg.Workspace.Root
		s.mu.Unlock()
		additions, deletions, err = gitWorkspaceLineChanges(ctx, root)
		if err != nil {
			return err
		}
	}
	s.emitGitBranchSnapshot(ctx, state, branches, current, dirty, additions, deletions, changedFiles)
	return nil
}

func (s *Service) switchGitBranch(ctx context.Context, target string, confirmDirty bool) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("git branch is required")
	}
	const branchSwitchReservation = "maintenance:switch-git-branch"
	s.mu.Lock()
	root := s.cfg.Workspace.Root
	allowWrite := s.cfg.Workspace.AllowWrite
	if s.activeRun != "" {
		s.mu.Unlock()
		return ErrRunActive
	}
	if !allowWrite {
		s.mu.Unlock()
		return fmt.Errorf("git branch switching is disabled by workspace.allow_write")
	}
	s.activeRun = branchSwitchReservation
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.activeRun == branchSwitchReservation {
			s.activeRun = ""
		}
		s.mu.Unlock()
	}()
	branches, current, dirty, changedFiles, err := gitBranchSnapshot(ctx, root)
	if err != nil {
		return err
	}
	additions, deletions := 0, 0
	if dirty {
		additions, deletions, err = gitWorkspaceLineChanges(ctx, root)
		if err != nil {
			return err
		}
	}
	found := false
	for _, branch := range branches {
		if branch.Name == target {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown local git branch %q", target)
	}
	if target == current {
		s.emitGitBranchSnapshot(ctx, "switched", branches, current, dirty, additions, deletions, changedFiles)
		return nil
	}
	if dirty && !confirmDirty {
		s.emitGitBranchSnapshot(ctx, "dirty_confirmation_required", branches, current, true, additions, deletions, changedFiles)
		return ErrDirtyWorkspace
	}
	if _, err := gitOutputLimited(ctx, root, 64*1024, "switch", "--no-guess", "--", target); err != nil {
		return fmt.Errorf("switch git branch to %q: %w", target, err)
	}
	return s.emitGitBranches(ctx, "switched")
}

func (s *Service) createGitBranch(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("git branch is required")
	}
	const branchCreateReservation = "maintenance:create-git-branch"
	s.mu.Lock()
	root := s.cfg.Workspace.Root
	allowWrite := s.cfg.Workspace.AllowWrite
	if s.activeRun != "" {
		s.mu.Unlock()
		return ErrRunActive
	}
	if !allowWrite {
		s.mu.Unlock()
		return fmt.Errorf("git branch creation is disabled by workspace.allow_write")
	}
	s.activeRun = branchCreateReservation
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.activeRun == branchCreateReservation {
			s.activeRun = ""
		}
		s.mu.Unlock()
	}()
	if _, err := gitOutputLimited(ctx, root, 1024, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("invalid git branch name %q", name)
	}
	branches, current, _, _, err := gitBranchSnapshot(ctx, root)
	if err != nil {
		return err
	}
	for _, branch := range branches {
		if branch.Name == name {
			if name == current {
				return s.emitGitBranches(ctx, "switched")
			}
			if _, err := gitOutputLimited(ctx, root, 64*1024, "switch", "--no-guess", "--", name); err != nil {
				return fmt.Errorf("switch git branch to %q: %w", name, err)
			}
			return s.emitGitBranches(ctx, "switched")
		}
	}
	if _, err := gitOutputLimited(ctx, root, 64*1024, "switch", "-c", name); err != nil {
		return fmt.Errorf("create git branch %q: %w", name, err)
	}
	return s.emitGitBranches(ctx, "created")
}

func (s *Service) gitBranchSnapshot(ctx context.Context) ([]GitBranchEntry, string, bool, int, error) {
	s.mu.Lock()
	root := s.cfg.Workspace.Root
	s.mu.Unlock()
	return gitBranchSnapshot(ctx, root)
}

func gitBranchSnapshot(ctx context.Context, root string) ([]GitBranchEntry, string, bool, int, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, "", false, 0, fmt.Errorf("workspace root is empty")
	}
	inside, err := gitOutputLimited(ctx, root, 1024, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(inside)) != "true" {
		if err != nil {
			return nil, "", false, 0, fmt.Errorf("inspect git workspace: %w", err)
		}
		return nil, "", false, 0, fmt.Errorf("workspace %q is not a git work tree", root)
	}
	currentOutput, err := gitOutputLimited(ctx, root, 64*1024, "branch", "--show-current")
	if err != nil {
		return nil, "", false, 0, fmt.Errorf("read current git branch: %w", err)
	}
	current := strings.TrimSpace(string(currentOutput))
	branchOutput, err := gitOutputLimited(ctx, root, 1024*1024, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, "", false, 0, fmt.Errorf("list git branches: %w", err)
	}
	lines := strings.Split(string(branchOutput), "\n")
	branches := make([]GitBranchEntry, 0, len(lines))
	currentListed := false
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		isCurrent := name == current
		currentListed = currentListed || isCurrent
		branches = append(branches, GitBranchEntry{Name: name, Current: isCurrent})
	}
	if current != "" && !currentListed {
		branches = append(branches, GitBranchEntry{Name: current, Current: true})
	}
	sort.Slice(branches, func(left, right int) bool { return branches[left].Name < branches[right].Name })
	status, err := gitOutputLimited(ctx, root, 1024*1024, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, "", false, 0, fmt.Errorf("read git workspace status: %w", err)
	}
	changedFiles := countPorcelainStatusEntries(status)
	return branches, current, changedFiles > 0, changedFiles, nil
}

func countPorcelainStatusEntries(status []byte) int {
	if len(status) == 0 {
		return 0
	}
	count := 0
	for i := 0; i < len(status); {
		if status[i] == 0 {
			i++
			continue
		}
		start := i
		for i < len(status) && status[i] != 0 {
			i++
		}
		entry := status[start:i]
		count++
		if i < len(status) {
			i++ // skip NUL
		}
		// Rename/copy records include a second path after the first NUL.
		if len(entry) >= 1 && (entry[0] == 'R' || entry[0] == 'C') {
			for i < len(status) && status[i] != 0 {
				i++
			}
			if i < len(status) {
				i++
			}
		}
	}
	return count
}

func (s *Service) emitGitBranchSnapshot(ctx context.Context, state string, branches []GitBranchEntry, current string, dirty bool, additions, deletions, changedFiles int) {
	s.emit(ctx, Event{
		Kind: EventGitBranches, State: state, Text: current,
		GitBranches: branches, WorkspaceDirty: dirty,
		Data: map[string]string{
			"additions":     strconv.Itoa(additions),
			"deletions":     strconv.Itoa(deletions),
			"changed_files": strconv.Itoa(changedFiles),
		},
	})
}

func (s *Service) modelRouteEntries() []ModelRouteEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := []ModelRouteEntry{
		{Scope: "main", Label: "Main", Route: config.ModelRouteConfig{Provider: s.cfg.Defaults.Provider, Model: s.cfg.Defaults.Model, Reasoning: s.cfg.Defaults.Reasoning}},
		{Scope: "title", Label: "Title", Route: s.cfg.Agents.Title},
		{Scope: "plan", Label: "Plan", Route: s.cfg.Agents.Plan},
		{Scope: "approval", Label: "Approval", Route: s.cfg.Agents.Approval},
		{Scope: "vision", Label: "Vision", Route: s.cfg.Agents.Vision},
		{Scope: "compaction", Label: "Compaction", Route: s.cfg.Agents.Compaction},
		{Scope: "recap", Label: "Recap", Route: s.cfg.Agents.Recap},
	}
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

func (s *Service) modelRoutesEvent(state string) Event {
	s.mu.Lock()
	maxConcurrency := s.cfg.Agents.Subagents.MaxConcurrency
	maxDepth := s.cfg.Agents.Subagents.MaxDepth
	shellConcurrency := s.cfg.Workspace.Shell.MaxConcurrency
	shellWallSeconds := int(s.cfg.Workspace.Shell.MaxWallClockDuration.Seconds())
	awaitSeconds := int(s.cfg.Agents.Subagents.AwaitDuration.Seconds())
	idleSeconds := int(s.cfg.Agents.Subagents.IdleDuration.Seconds())
	fastMode := s.cfg.Providers.ChatGPT.FastMode
	s.mu.Unlock()
	return Event{
		Kind: EventModelRoutes, State: state, ModelRoutes: s.modelRouteEntries(),
		Data: map[string]string{"subagent_max_concurrency": strconv.Itoa(maxConcurrency), "subagent_max_depth": strconv.Itoa(maxDepth), "shell_max_concurrency": strconv.Itoa(shellConcurrency), "shell_max_wall_clock_seconds": strconv.Itoa(shellWallSeconds), "subagent_await_seconds": strconv.Itoa(awaitSeconds), "subagent_idle_seconds": strconv.Itoa(idleSeconds), "chatgpt_fast_mode": strconv.FormatBool(fastMode)},
	}
}

func (s *Service) emitBackgroundSnapshot(ctx context.Context, state string) error {
	if s.background == nil {
		return fmt.Errorf("background runtime is unavailable")
	}
	s.emit(ctx, Event{Kind: EventBackgroundState, State: state, Background: s.background.List()})
	return nil
}

func (s *Service) updateModelRoute(ctx context.Context, entry *ModelRouteEntry, reset bool) error {
	if entry == nil {
		return fmt.Errorf("model route is required")
	}
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	if entry.Scope != "main" && entry.Scope != "title" && entry.Scope != "plan" && entry.Scope != "approval" && entry.Scope != "vision" && entry.Scope != "compaction" && entry.Scope != "recap" && entry.Scope != "subagent" {
		return fmt.Errorf("unsupported model route scope %q", entry.Scope)
	}
	if entry.Scope != "subagent" && entry.Role != "" {
		return fmt.Errorf("role is not valid for %s route", entry.Scope)
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
		if entry.Scope == "main" {
			return fmt.Errorf("main model route cannot be reset")
		}
		route = config.ModelRouteConfig{}
	} else {
		if strings.TrimSpace(route.Provider) == "" || strings.TrimSpace(route.Model) == "" {
			return fmt.Errorf("model route must set both provider and model")
		}
		if s.providers == nil {
			return fmt.Errorf("provider runtime is unavailable")
		}
		account, modelID, _, _, err := s.providers.resolveDriver(ctx, route.Provider, route.Model, route.Reasoning)
		if err != nil {
			return err
		}
		if entry.Scope == "vision" {
			known, supported, supportErr := s.providers.modelImageInputSupport(ctx, route.Provider, account.ID, modelID)
			if supportErr != nil {
				return supportErr
			}
			if known && !supported {
				return fmt.Errorf("model %q does not accept image input and cannot be used as the vision assistant", modelID)
			}
		}
	}
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			if entry.Scope == "main" {
				return config.UpdateSessionModelDefaults(s.configPath, route.Provider, route.Model, route.Reasoning)
			}
			if reset {
				return config.ResetModelRoute(s.configPath, entry.Scope, entry.Role)
			}
			return config.UpdateModelRoute(s.configPath, entry.Scope, entry.Role, route)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	if entry.Scope == "main" {
		s.cfg.Defaults.Provider, s.cfg.Defaults.Model, s.cfg.Defaults.Reasoning = route.Provider, route.Model, route.Reasoning
	} else if entry.Scope == "title" {
		s.cfg.Agents.Title = route
	} else if entry.Scope == "plan" {
		s.cfg.Agents.Plan = route
	} else if entry.Scope == "approval" {
		s.cfg.Agents.Approval = route
	} else if entry.Scope == "vision" {
		s.cfg.Agents.Vision = route
	} else if entry.Scope == "compaction" {
		s.cfg.Agents.Compaction = route
	} else if entry.Scope == "recap" {
		s.cfg.Agents.Recap = route
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
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func (s *Service) updateSubagentMaxConcurrency(ctx context.Context, maxConcurrency int) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.mu.Lock()
	currentSession := s.currentSession
	s.mu.Unlock()
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateSubagentMaxConcurrency(s.configPath, maxConcurrency)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Agents.Subagents.MaxConcurrency = maxConcurrency
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateSubagentMaxConcurrency(maxConcurrency)
	}
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func (s *Service) updateSubagentMaxDepth(ctx context.Context, maxDepth int) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.mu.Lock()
	currentSession := s.currentSession
	s.mu.Unlock()
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateSubagentMaxDepth(s.configPath, maxDepth)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Agents.Subagents.MaxDepth = maxDepth
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateSubagentMaxDepth(maxDepth)
	}
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func (s *Service) updateShellMaxConcurrency(ctx context.Context, maxConcurrency int) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.mu.Lock()
	currentSession := s.currentSession
	s.mu.Unlock()
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateShellMaxConcurrency(s.configPath, maxConcurrency)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Workspace.Shell.MaxConcurrency = maxConcurrency
	s.mu.Unlock()
	if s.coding != nil {
		s.coding.UpdateShellMaxConcurrency(maxConcurrency)
	}
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func (s *Service) updateShellMaxWallClock(ctx context.Context, wall time.Duration) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	seconds := int(wall.Seconds())
	s.mu.Lock()
	currentSession := s.currentSession
	s.mu.Unlock()
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateShellMaxWallClock(s.configPath, seconds)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Workspace.Shell.MaxWallClock = wall.String()
	s.cfg.Workspace.Shell.MaxWallClockDuration = wall
	s.mu.Unlock()
	if s.coding != nil {
		s.coding.UpdateShellMaxWallClock(wall)
	}
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func (s *Service) updateSubagentAwaitTimeout(ctx context.Context, timeout time.Duration) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	seconds := int(timeout.Seconds())
	s.mu.Lock()
	currentSession := s.currentSession
	s.mu.Unlock()
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateSubagentAwaitTimeout(s.configPath, seconds)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Agents.Subagents.AwaitTimeout = timeout.String()
	s.cfg.Agents.Subagents.AwaitDuration = timeout
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateSubagentAwaitTimeout(timeout)
	}
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func (s *Service) updateSubagentIdleTimeout(ctx context.Context, timeout time.Duration) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	seconds := int(timeout.Seconds())
	s.mu.Lock()
	currentSession := s.currentSession
	s.mu.Unlock()
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateSubagentIdleTimeout(s.configPath, seconds)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Agents.Subagents.IdleTimeout = timeout.String()
	s.cfg.Agents.Subagents.IdleDuration = timeout
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateSubagentIdleTimeout(timeout)
	}
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func (s *Service) updateChatGPTFastMode(ctx context.Context, enabled bool) error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.mu.Lock()
	currentSession := s.currentSession
	s.mu.Unlock()
	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateChatGPTFastMode(s.configPath, enabled)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Providers.ChatGPT.FastMode = enabled
	s.mu.Unlock()
	if s.providers != nil {
		s.providers.UpdateChatGPTFastMode(enabled)
	}
	s.emit(ctx, s.modelRoutesEvent("updated"))
	return nil
}

func (s *Service) updateSessionPreferences(ctx context.Context, action Action) error {
	if action.Route == nil {
		return fmt.Errorf("session preferences require provider, model, and reasoning")
	}
	provider := strings.TrimSpace(action.Route.Route.Provider)
	model := strings.TrimSpace(action.Route.Route.Model)
	reasoning := strings.TrimSpace(action.Route.Route.Reasoning)
	if provider == "" || model == "" {
		return fmt.Errorf("session preferences require provider and model")
	}

	s.mu.Lock()
	currentSession := firstNonempty(action.SessionID, s.currentSession)
	agentMode := s.cfg.Defaults.AgentMode
	s.mu.Unlock()

	if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateSessionModelDefaults(s.configPath, provider, model, reasoning)
		}); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.cfg.Defaults.Provider = provider
	s.cfg.Defaults.Model = model
	s.cfg.Defaults.Reasoning = reasoning
	s.mu.Unlock()

	// Durable sessions keep their own selection; blank/new sessions only exist in
	// memory until the first turn, so defaults cover restart for those.
	if s.sessions != nil && currentSession != "" {
		if existing, err := s.sessions.LoadSession(ctx, currentSession); err == nil {
			agentMode = firstNonempty(existing.AgentMode, agentMode)
			if err := s.sessions.UpdatePreferences(ctx, currentSession, provider, model, reasoning, agentMode); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) emitSkillCatalog(ctx context.Context, state string) error {
	entries, diagnostics, err := s.SkillCatalogSnapshot()
	if err != nil {
		return err
	}
	s.emit(ctx, Event{
		Kind: EventSkillCatalog, State: state,
		SkillCatalog: entries, SkillDiagnostics: diagnostics,
	})
	return nil
}

// SkillCatalogSnapshot returns the current durable projection without relying
// on the asynchronous UI event pump. Desktop settings uses this for an
// immediate readback after opening or reloading the catalog.
func (s *Service) SkillCatalogSnapshot() ([]SkillCatalogEntry, []SkillDiagnostic, error) {
	if s.skillCatalog == nil {
		return nil, nil, fmt.Errorf("skills are unavailable")
	}
	snapshot := s.skillCatalog.Snapshot()
	entries := make([]SkillCatalogEntry, len(snapshot.Entries))
	for i, entry := range snapshot.Entries {
		entries[i] = SkillCatalogEntry{
			Name: entry.Name, Description: entry.Description, SourcePath: entry.SourcePath,
			LogoPath: entry.LogoPath, Bundled: entry.Bundled, Eager: entry.Eager, Disabled: entry.Disabled,
			ModelVisible: entry.ModelVisible, ResourceCount: entry.ResourceCount,
		}
	}
	diagnostics := make([]SkillDiagnostic, len(snapshot.Diagnostics))
	for i, diagnostic := range snapshot.Diagnostics {
		diagnostics[i] = SkillDiagnostic{Path: diagnostic.Path, Message: diagnostic.Message}
	}
	return entries, diagnostics, nil
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
	case "timeout", "timed_out", "timed out":
		return api.ActionAttemptTimeout, nil
	default:
		return "", fmt.Errorf("reconcile decision must be succeeded, failed, timed out, or cancelled")
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
	_ = s.emitContextProfile(ctx, id)
	return nil
}

func (s *Service) markSessionUnread(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	visible := s.currentSession == sessionID
	s.mu.Unlock()
	if visible {
		return nil
	}
	if err := s.sessions.SetUIState(ctx, sessionID, "unread", true); err != nil {
		return err
	}
	return s.emitSessionList(ctx)
}

func (s *Service) ForkSession(ctx context.Context, sourceID string, activate bool) (string, error) {
	if s.sessions == nil {
		return "", fmt.Errorf("session store is unavailable")
	}
	id, err := randomID("session")
	if err != nil {
		return "", err
	}
	if err := s.sessions.Fork(ctx, sourceID, id); err != nil {
		return "", err
	}
	if activate {
		if err := s.emitSession(ctx, id); err != nil {
			return "", err
		}
	}
	if err := s.emitSessionList(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) emitSession(ctx context.Context, id string) error {
	modelID, err := s.emitSessionProjection(ctx, id, "loaded", true)
	if err != nil {
		return err
	}
	if err := s.switchSessionHooks(ctx, id, "resume", modelID); err != nil {
		return err
	}
	s.mu.Lock()
	s.currentSession = id
	s.mu.Unlock()
	_ = s.emitContextProfile(ctx, id)
	return nil
}

func (s *Service) emitSessionProjection(ctx context.Context, id, state string, activate bool) (string, error) {
	event, modelID, err := s.buildSessionProjectionEvent(ctx, id, state, activate)
	if err != nil {
		return "", err
	}
	s.emit(ctx, event)
	return modelID, nil
}

// SessionProjection returns the same durable projection used by the event
// stream without depending on an asynchronous renderer broadcast. Desktop
// navigation uses this readback after a resume action so the initiating window
// can update immediately even when another window is consuming runtime events.
func (s *Service) SessionProjection(ctx context.Context, id string) (Event, error) {
	event, _, err := s.buildSessionProjectionEvent(ctx, id, "loaded", false)
	return event, err
}

func (s *Service) buildSessionProjectionEvent(ctx context.Context, id, state string, activate bool) (Event, string, error) {
	if s.sessions == nil {
		return Event{}, "", fmt.Errorf("session store is unavailable")
	}
	if id == "" {
		return Event{}, "", fmt.Errorf("session id is required")
	}
	projection, err := s.sessions.LoadProjection(ctx, id)
	if err != nil {
		return Event{}, "", err
	}
	if activate {
		if err := s.rememberWorkspaceSession(ctx, id); err != nil {
			return Event{}, "", err
		}
	}
	blocks, err := json.Marshal(projection.Blocks)
	if err != nil {
		return Event{}, "", err
	}
	todo, err := s.sessions.LoadTodo(ctx, id)
	if err != nil {
		return Event{}, "", err
	}
	currentRecap, err := s.loadRecap(ctx, id)
	if err != nil {
		return Event{}, "", err
	}
	s.rememberSessionUsage(id, projection.Usage)
	data := sessionProjectionData(projection, string(blocks))
	s.addActiveRunProjection(data, id)
	event := Event{
		Kind: EventSessionLoaded, SessionID: id, State: state,
		Data: data, AgentSnapshots: s.subagentSnapshots(ctx, id), Todo: &todo, Recap: currentRecap,
	}
	return event, projection.Session.ModelID, nil
}

func (s *Service) addActiveRunProjection(data map[string]string, sessionID string) {
	s.mu.Lock()
	active := s.activeRun != "" && s.activeSession == sessionID
	activeRunID := s.activeRun
	activeSessionID := s.activeSession
	s.mu.Unlock()
	data["active"] = strconv.FormatBool(active)
	if active {
		data["activeRunID"] = activeRunID
	}
	if activeRunID != "" && activeSessionID != "" {
		data["globalActiveRunID"] = activeRunID
		data["globalActiveSessionID"] = activeSessionID
	}
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

func archiveInactiveDays(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 30, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 || days > 3650 {
		return 0, fmt.Errorf("archive inactivity days must be between 1 and 3650")
	}
	return days, nil
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
	projects, err := s.sessions.Projects(ctx)
	if err != nil {
		return err
	}
	encodedProjects, err := json.Marshal(projects)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventSessionLoaded, State: "list", Data: map[string]string{"sessions": string(encoded), "projects": string(encodedProjects)}})
	return nil
}

func (s *Service) RememberProject(ctx context.Context, workspace string) error {
	if s.sessions == nil {
		return fmt.Errorf("session store is unavailable")
	}
	if err := s.sessions.TouchProject(ctx, workspace); err != nil {
		return err
	}
	return s.emitSessionList(ctx)
}

// SearchSessions exposes the durable global session index without routing a
// read-only query through the asynchronous event stream.
func (s *Service) SearchSessions(ctx context.Context, query string, limit int) ([]session.SessionSearchResult, error) {
	if s.sessions == nil {
		return nil, fmt.Errorf("session store is unavailable")
	}
	return s.sessions.SearchSessions(ctx, query, limit)
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
	models = s.catalog.EnrichWithModelsDev(ctx, models)

	models.Models = s.catalogModelsWithAvailability(account.Provider, models.Models)
	encoded, err := json.Marshal(models.Models)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventModelCatalog, State: "fresh", Data: map[string]string{"provider": account.Provider, "accountID": account.ID, "models": string(encoded)}})
	return s.emitModelProviders(ctx, "auth_updated")
}

func (s *Service) emitAuthCatalog(ctx context.Context) {
	if s.authentication == nil || s.catalog == nil {
		return
	}
	for _, provider := range []string{"chatgpt", "grok"} {
		account, ok := s.activeSubscriptionAccount(ctx, provider)
		if !ok {
			continue
		}
		s.emit(ctx, Event{Kind: EventAuthState, State: account.Status, Data: map[string]string{
			"provider": account.Provider, "accountID": account.ID, "email": account.Email, "displayName": account.DisplayName, "plan": account.Plan,
		}})
		cached, found, err := s.catalog.Cached(ctx, account.Provider, account.ID)
		if err != nil || !found {
			continue
		}
		cached.Models = s.catalogModelsWithAvailability(account.Provider, cached.Models)
		encoded, err := json.Marshal(cached.Models)
		if err != nil {
			continue
		}
		state := "cached"
		if cached.Stale {
			state = "stale"
		}
		s.emit(ctx, Event{Kind: EventModelCatalog, State: state, Text: cached.Warning, Data: map[string]string{"provider": account.Provider, "accountID": account.ID, "models": string(encoded)}})
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
	snapshots := s.mcp.Servers()
	values := make([]mcpServerView, 0, len(snapshots))
	for _, snapshot := range snapshots {
		tools := make([]mcpToolView, 0, len(snapshot.Tools))
		for _, tool := range snapshot.Tools {
			tools = append(tools, mcpToolView{
				Name: tool.Name, Description: tool.Description,
				Effect: tool.Effect, RequiresApproval: tool.RequiresApproval,
			})
		}
		values = append(values, buildMCPServerView(
			snapshot.Name, string(snapshot.State), snapshot.ToolCount,
			tools, snapshot.LastError, s.cfg.MCP.Servers[snapshot.Name],
		))
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventMCPState, State: "snapshot", Data: map[string]string{"servers": string(encoded)}})
	s.mu.Lock()
	sessionID := s.currentSession
	s.mu.Unlock()
	_ = s.emitContextProfile(ctx, sessionID)
	return nil
}

type mcpToolView struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	Effect           string `json:"effect,omitempty"`
	RequiresApproval bool   `json:"requiresApproval,omitempty"`
}

type mcpServerView struct {
	Name           string        `json:"name"`
	Enabled        bool          `json:"enabled"`
	State          string        `json:"state"`
	Transport      string        `json:"transport"`
	Target         string        `json:"target"`
	Command        string        `json:"command,omitempty"`
	Args           []string      `json:"args,omitempty"`
	CWD            string        `json:"cwd,omitempty"`
	InheritEnv     bool          `json:"inheritEnv,omitempty"`
	URL            string        `json:"url,omitempty"`
	Approval       string        `json:"approval"`
	MaxConcurrency int           `json:"maxConcurrency"`
	ToolCount      int           `json:"toolCount"`
	Tools          []mcpToolView `json:"tools,omitempty"`
	Error          string        `json:"error"`
	Removable      bool          `json:"removable"`
	Icon           string        `json:"icon,omitempty"`
}

func buildMCPServerView(
	name string,
	state string,
	toolCount int,
	tools []mcpToolView,
	lastError string,
	serverConfig config.MCPServerConfig,
) mcpServerView {
	target := serverConfig.URL
	if serverConfig.Transport == "stdio" {
		target = strings.TrimSpace(strings.Join(append([]string{serverConfig.Command}, serverConfig.Args...), " "))
	}
	return mcpServerView{
		Name: name, Enabled: serverConfig.Enabled, State: state,
		Transport: serverConfig.Transport, Target: target, Command: serverConfig.Command,
		Args: append([]string(nil), serverConfig.Args...), CWD: serverConfig.CWD, InheritEnv: serverConfig.InheritEnv,
		URL: serverConfig.URL, Approval: serverConfig.Approval, MaxConcurrency: serverConfig.MaxConcurrency,
		ToolCount: toolCount, Tools: tools, Error: lastError, Removable: true, Icon: serverConfig.Icon,
	}
}
