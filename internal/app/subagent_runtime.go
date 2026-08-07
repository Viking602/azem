package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/stream"
	"github.com/Viking602/venat/tool"
	hyworker "github.com/Viking602/venat/worker"
)

const (
	subagentSpawnTool     = "subagent.spawn"
	subagentGetOutputTool = "subagent.get_output"
	subagentKillTool      = "subagent.kill"
)

const processRestartInterruption = "interrupted by process restart"

func durableSubagentAgentID(agentType string) string {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		agentType = "worker"
	}
	return "azem-subagent-" + agentType
}

func subagentResourceClaims(profile effectiveSubagentProfile, workspaceRoot string) ([]api.ResourceClaimSpec, error) {
	if profile.Isolation == "worktree" || !subagentMayMutateWorkspace(profile) {
		return nil, nil
	}
	claim, err := workspaceWriteClaim(workspaceRoot)
	if err != nil {
		return nil, err
	}
	return []api.ResourceClaimSpec{claim}, nil
}

func (r *subagentRuntime) childWorkspaceClaims(ctx context.Context, parent subagentParentRuntime, profile effectiveSubagentProfile) ([]api.ResourceClaimSpec, error) {
	claims, err := subagentResourceClaims(profile, parent.WorkspaceRoot)
	if err != nil || len(claims) == 0 {
		return claims, err
	}
	claim := claims[0]
	parentHasClaim, err := parentTaskHasWorkspaceClaim(ctx, parent, claim.Key)
	if err != nil {
		return nil, err
	}
	if !parentHasClaim {
		return claims, nil
	}
	for {
		active, err := parentWorkspaceClaimActive(ctx, parent, claim.Key)
		if err != nil {
			return nil, err
		}
		if active {
			return nil, nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func parentTaskHasWorkspaceClaim(ctx context.Context, parent subagentParentRuntime, key string) (bool, error) {
	tasks, err := parent.Coding.Runner().ListTasks(ctx, parent.ParentRunID)
	if err != nil {
		return false, fmt.Errorf("list parent tasks for workspace claim: %w", err)
	}
	for _, task := range tasks {
		for _, claim := range task.ResourceClaims {
			if claim.Key == key && claim.Mode == api.ResourceClaimExclusive {
				return true, nil
			}
		}
	}
	return false, nil
}

func parentWorkspaceClaimActive(ctx context.Context, parent subagentParentRuntime, key string) (bool, error) {
	claims, err := parent.Coding.Runner().ListResourceClaims(ctx, api.ResourceClaimSelector{
		RunIDs: []string{parent.ParentRunID}, Keys: []string{key}, States: []api.ResourceClaimState{api.ResourceClaimActive},
	})
	if err != nil {
		return false, fmt.Errorf("list parent workspace claims: %w", err)
	}
	for _, claim := range claims {
		if claim.Key == key && claim.Mode == api.ResourceClaimExclusive && claim.RunID == parent.ParentRunID {
			return true, nil
		}
	}
	return false, nil
}

func clearRecoveredSharedWorkspaceClaim(ctx context.Context, parent subagentParentRuntime, runID, key string) error {
	tasks, err := parent.Coding.Runner().ListTasks(ctx, runID)
	if err != nil {
		return fmt.Errorf("list recovered child tasks for workspace claim: %w", err)
	}
	for _, task := range tasks {
		hasClaim := false
		for _, claim := range task.ResourceClaims {
			if claim.Key == key && claim.Mode == api.ResourceClaimExclusive {
				hasClaim = true
				break
			}
		}
		if !hasClaim {
			continue
		}
		filtered := make([]api.ResourceClaimSpec, 0, len(task.ResourceClaims))
		for _, claim := range task.ResourceClaims {
			if claim.Key != key {
				filtered = append(filtered, claim)
			}
		}
		task.ResourceClaims = filtered
		if err := parent.Coding.Runner().SaveTask(ctx, task); err != nil {
			return fmt.Errorf("save recovered child task without inherited workspace claim: %w", err)
		}
		return nil
	}
	return nil
}

type subagentParentRuntime struct {
	SessionID               string
	ParentRunID             string
	ParentAgentID           string
	ProviderID              string
	AccountID               string
	ModelID                 string
	Reasoning               string
	PlanMode                bool
	ContextTokenTarget      int
	ContextConfig           config.ContextConfig
	WorkspaceRoot           string
	Driver                  hyprovider.Driver
	ResolveDriver           func(context.Context, string, string, string) (string, int, hyprovider.Driver, error)
	ResolveAccountDriver    func(context.Context, string, string, string, string) (string, string, int, hyprovider.Driver, error)
	CompactionRoute         config.ModelRouteConfig
	CompactionRouteSnapshot func() config.ModelRouteConfig
	Coding                  *agentservice.Service
	Host                    *Service
}

type effectiveSubagentProfile struct {
	Type               string
	Persona            string
	Instructions       string
	Inputs             []config.SubagentContractItem
	Outputs            []config.SubagentContractItem
	Seed               []message.Message
	Provider           string
	AccountID          string
	Model              string
	Reasoning          string
	CapabilityMode     string
	RequestedIsolation string
	Isolation          string
	CWD                string
	WorktreeRepoRoot   string
	Tools              []string
}

type activeSubagent struct {
	run                 agentservice.SubagentRun
	profile             effectiveSubagentProfile
	prompt              string
	privateContext      string
	parent              subagentParentRuntime
	ctx                 context.Context
	cancel              context.CancelFunc
	done                chan struct{}
	slot                bool
	terminalizing       bool
	terminalized        bool
	ToolStarted         bool
	usage               hyprovider.Usage
	activity            string
	blocks              []AgentTranscriptBlock
	toolNames           map[string]struct{}
	lastActivityPersist time.Time
	persistedActivity   string
	lastStateEmit       time.Time
	lastEmittedTools    int
}

type subagentRuntime struct {
	mu               sync.Mutex
	cfg              config.SubagentConfig
	store            agentservice.SubagentRunStore
	worktreeRoot     string
	ctx              context.Context
	cancel           context.CancelFunc
	active           map[string]*activeSubagent
	pending          []string
	running          int
	terminalFallback map[string]agentservice.SubagentSnapshot
	hosts            map[string]*Service
	wakeInFlight     map[string]bool
	changed          chan struct{}
	wg               sync.WaitGroup
}

func newSubagentRuntime(parent context.Context, cfg config.SubagentConfig, store agentservice.SubagentRunStore, worktreeRoot string) (*subagentRuntime, error) {
	if store == nil {
		return nil, fmt.Errorf("subagent runtime: store is nil")
	}
	if cfg.MaxConcurrency < 1 {
		return nil, fmt.Errorf("subagent runtime: max concurrency must be positive")
	}
	cfg = cloneSubagentConfig(cfg)
	ctx, cancel := context.WithCancel(parent)
	return &subagentRuntime{
		cfg: cfg, store: store, worktreeRoot: worktreeRoot, ctx: ctx, cancel: cancel,
		active: make(map[string]*activeSubagent), terminalFallback: make(map[string]agentservice.SubagentSnapshot),
		hosts: make(map[string]*Service), wakeInFlight: make(map[string]bool), changed: make(chan struct{}),
	}, nil
}

func (r *subagentRuntime) Drivers(parent subagentParentRuntime) ([]tool.Driver, error) {
	r.mu.Lock()
	enabled := r.cfg.Enabled
	r.mu.Unlock()
	if !enabled {
		return nil, nil
	}
	if parent.Coding == nil || parent.Driver == nil || parent.SessionID == "" || parent.ParentRunID == "" {
		return nil, fmt.Errorf("subagent parent runtime is incomplete")
	}
	if err := r.recoverInterrupted(parent); err != nil {
		return nil, err
	}
	return []tool.Driver{
		&subagentSpawnDriver{runtime: r, parent: parent},
		&subagentGetOutputDriver{runtime: r, sessionID: parent.SessionID},
		&subagentKillDriver{runtime: r, sessionID: parent.SessionID},
	}, nil
}

func (r *subagentRuntime) recoverInterrupted(parent subagentParentRuntime) error {
	runs, err := r.store.List(r.ctx, parent.SessionID)
	if err != nil {
		return fmt.Errorf("list interrupted subagents: %w", err)
	}
	for _, run := range runs {
		if run.ParentRunID != parent.ParentRunID || run.State != agentservice.SubagentInterrupted ||
			run.Error != processRestartInterruption || strings.TrimSpace(run.ChildRunID) == "" {
			continue
		}
		profile, profileErr := r.resolveProfile(subagentSpawnInput{
			SubagentType: run.Type, Model: run.Model, CapabilityMode: run.CapabilityMode,
			Isolation: run.RequestedIsolation, CWD: ".",
		}, parent)
		if profileErr != nil {
			continue
		}
		profile.Provider = firstNonempty(run.Provider, profile.Provider)
		profile.AccountID = run.AccountID
		profile.Model = firstNonempty(run.Model, profile.Model)
		profile.Reasoning = firstNonempty(run.Reasoning, profile.Reasoning)
		profile.CapabilityMode = run.CapabilityMode
		profile.RequestedIsolation = run.RequestedIsolation
		profile.Isolation = run.Isolation
		profile.CWD = run.CWD
		run.Background = run.Background && subagentMayRunInBackground(profile)

		run.State = agentservice.SubagentQueued
		run.Summary = "queued after process restart"
		run.Error = ""
		run.FinishedAt = time.Time{}
		childCtx, cancel := context.WithCancel(r.ctx)
		active := &activeSubagent{
			run: run, profile: profile, prompt: run.Description, parent: parent, ctx: childCtx, cancel: cancel,
			done: make(chan struct{}), toolNames: make(map[string]struct{}),
		}
		r.mu.Lock()
		if _, exists := r.active[run.ID]; exists {
			r.mu.Unlock()
			cancel()
			continue
		}
		r.active[run.ID] = active
		if parent.Host != nil {
			r.hosts[parent.SessionID] = parent.Host
		}
		r.mu.Unlock()
		if err := r.store.Save(r.ctx, run); err != nil {
			r.mu.Lock()
			delete(r.active, run.ID)
			r.mu.Unlock()
			cancel()
			return fmt.Errorf("queue interrupted subagent %q: %w", run.ID, err)
		}
		r.mu.Lock()
		r.pending = append(r.pending, run.ID)
		r.signalChangedLocked()
		r.pumpLocked()
		r.mu.Unlock()
		r.emitState(run, string(run.State))
	}
	return nil
}

func (r *subagentRuntime) updateModelRoute(roleName string, route config.ModelRouteConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	role := r.cfg.Roles[roleName]
	role.Provider, role.Model, role.Reasoning = route.Provider, route.Model, route.Reasoning
	r.cfg.Roles[roleName] = role
	delete(r.cfg.Models, roleName)
	if route == (config.ModelRouteConfig{}) {
		delete(r.cfg.Routes, roleName)
	} else {
		r.cfg.Routes[roleName] = route
	}
}

func (r *subagentRuntime) updateMaxConcurrency(maxConcurrency int) {
	r.mu.Lock()
	r.cfg.MaxConcurrency = maxConcurrency
	r.pumpLocked()
	r.mu.Unlock()
}

func (r *subagentRuntime) Spawn(_ context.Context, input subagentSpawnInput, parent subagentParentRuntime) (agentservice.SubagentRun, error) {
	return r.spawn(input, parent, nil)
}

func (r *subagentRuntime) spawn(input subagentSpawnInput, parent subagentParentRuntime, beforeEnqueue func(agentservice.SubagentRun) error) (agentservice.SubagentRun, error) {
	var profile effectiveSubagentProfile
	var err error
	if input.ResumeFrom != "" {
		profile, err = r.resolveResumeProfile(input.ResumeFrom, parent)
	} else {
		profile, err = r.resolveProfile(input, parent)
	}
	if err != nil {
		return agentservice.SubagentRun{}, err
	}
	if parent.CompactionRouteSnapshot != nil {
		parent.CompactionRoute = parent.CompactionRouteSnapshot()
	}
	id, err := newSubagentID()
	if err != nil {
		return agentservice.SubagentRun{}, err
	}
	now := time.Now().UTC()
	run := agentservice.SubagentRun{
		ID: id, SessionID: parent.SessionID, ParentRunID: parent.ParentRunID, ParentAgentID: parent.ParentAgentID,
		ParentToolCallID: input.parentToolCallID, Description: input.Description, Type: profile.Type,
		State: agentservice.SubagentInitializing, Summary: "initializing", Provider: profile.Provider, Model: profile.Model, Reasoning: profile.Reasoning,
		AccountID:      profile.AccountID,
		CapabilityMode: profile.CapabilityMode, RequestedIsolation: profile.RequestedIsolation, Isolation: profile.Isolation,
		CWD: profile.CWD, Background: input.Background && subagentMayRunInBackground(profile), StartedAt: now,
	}
	if parent.Host != nil {
		metadata := hooks.Metadata{SessionID: run.SessionID, RunID: run.ParentRunID, AgentID: run.ID, AgentType: run.Type, ParentRunID: run.ParentRunID, ParentToolCallID: run.ParentToolCallID, CWD: run.CWD}
		if err := parent.Host.dispatchLifecycle(parent.Host.ctx, hooks.TaskCreated, metadata, func(e *hooks.Envelope) {
			e.TaskID, e.TaskSubject, e.TaskDescription = run.ID, run.Description, input.Prompt
		}); err != nil {
			return run, err
		}
	}
	if err := r.store.Create(r.ctx, run); err != nil {
		return agentservice.SubagentRun{}, fmt.Errorf("create subagent task: %w", err)
	}
	if beforeEnqueue != nil {
		if err := beforeEnqueue(run); err != nil {
			run.State = agentservice.SubagentFailed
			run.Summary = "failed before enqueue"
			run.Error = err.Error()
			run.FinishedAt = time.Now().UTC()
			if saveErr := r.store.Save(r.ctx, run); saveErr != nil {
				return run, errors.Join(err, fmt.Errorf("persist failed subagent: %w", saveErr))
			}
			return run, err
		}
	}
	childCtx, cancel := context.WithCancel(r.ctx)
	active := &activeSubagent{
		run: run, profile: profile, prompt: input.Prompt, parent: parent, ctx: childCtx, cancel: cancel,
		done: make(chan struct{}), toolNames: make(map[string]struct{}),
	}
	r.mu.Lock()
	r.active[id] = active
	if parent.Host != nil {
		r.hosts[parent.SessionID] = parent.Host
	}
	r.mu.Unlock()
	r.emitState(run, "initializing")
	r.mu.Lock()
	active.run.State = agentservice.SubagentQueued
	active.run.Summary = "queued"
	queued := cloneSubagentRun(active.run)
	r.mu.Unlock()
	if err := r.store.Save(r.ctx, queued); err != nil {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("persist queued subagent: %w", err)})
		return r.snapshot(id, parent.SessionID).Run, nil
	}
	if parent.Host != nil {
		metadata := hooks.Metadata{SessionID: queued.SessionID, RunID: queued.ParentRunID, AgentID: queued.ID, AgentType: queued.Type, ParentRunID: queued.ParentRunID, ParentToolCallID: queued.ParentToolCallID, CWD: queued.CWD}
		decision := parent.Host.hooks.Dispatch(parent.Host.ctx, hooks.Envelope{
			SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID, AgentType: metadata.AgentType,
			ParentRunID: metadata.ParentRunID, ParentToolCallID: metadata.ParentToolCallID, CWD: metadata.CWD,
			HookEventName: hooks.SubagentStart, Trigger: string(queued.State), TaskID: queued.ID,
			TaskSubject: queued.Description, TaskDescription: input.Prompt,
		})
		if decision.PreventContinuation {
			err := fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, decision.StopReason)
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: err})
			return r.snapshot(id, parent.SessionID).Run, err
		}
		var additional []string
		for _, hookRun := range decision.Runs {
			if text := strings.TrimSpace(hookRun.Output.HookSpecificOutput.AdditionalContext); text != "" {
				additional = append(additional, text)
			}
		}
		if len(additional) > 0 {
			r.mu.Lock()
			if current := r.active[id]; current != nil {
				current.privateContext = strings.Join(additional, "\n")
			}
			r.mu.Unlock()
		}
	}
	r.mu.Lock()
	if current := r.active[id]; current != nil && !current.terminalizing {
		current.run = queued
		r.pending = append(r.pending, id)
		r.signalChangedLocked()
	}
	r.mu.Unlock()
	r.emitState(queued, "queued")
	r.mu.Lock()
	r.pumpLocked()
	r.mu.Unlock()
	return queued, nil
}

func (r *subagentRuntime) resolveProfile(input subagentSpawnInput, parent subagentParentRuntime) (effectiveSubagentProfile, error) {
	r.mu.Lock()
	cfg := r.cfg
	cfg.Roles = cloneSubagentRoles(r.cfg.Roles)
	cfg.Personas = cloneSubagentPersonas(r.cfg.Personas)
	cfg.Toggle = cloneBoolMap(r.cfg.Toggle)
	r.mu.Unlock()
	if !cfg.Enabled {
		return effectiveSubagentProfile{}, fmt.Errorf("subagents are disabled")
	}
	role, ok := cfg.Roles[input.SubagentType]
	if !ok {
		return effectiveSubagentProfile{}, fmt.Errorf("unknown subagent type %q (available: %s)", input.SubagentType, strings.Join(sortedRoleNames(cfg.Roles, cfg.Toggle), ", "))
	}
	if enabled, configured := cfg.Toggle[input.SubagentType]; configured && !enabled {
		return effectiveSubagentProfile{}, fmt.Errorf("subagent type %q is disabled", input.SubagentType)
	}
	persona := config.SubagentPersonaConfig{}
	if role.Persona != "" {
		var found bool
		persona, found = cfg.Personas[role.Persona]
		if !found {
			return effectiveSubagentProfile{}, fmt.Errorf("subagent type %q references unknown persona %q", input.SubagentType, role.Persona)
		}
	}
	provider, model := parent.ProviderID, parent.ModelID
	if input.Model != "" {
		model = input.Model
	} else if role.Model != "" {
		provider, model = firstNonempty(role.Provider, parent.ProviderID), role.Model
	} else if persona.Model != "" {
		provider, model = firstNonempty(persona.Provider, parent.ProviderID), persona.Model
	}
	capability := firstNonempty(input.CapabilityMode, role.CapabilityMode, "read-only")
	isolation := firstNonempty(input.Isolation, role.Isolation, persona.Isolation, "none")
	if parent.PlanMode {
		capability = "read-only"
		isolation = "none"
	}
	cwd := parent.WorkspaceRoot
	if input.CWD != "" {
		resolved, resolveErr := resolveSubagentCWD(parent.WorkspaceRoot, input.CWD)
		if resolveErr != nil {
			return effectiveSubagentProfile{}, resolveErr
		}
		cwd = resolved
	}
	if input.CWD != "" && isolation == "worktree" {
		return effectiveSubagentProfile{}, fmt.Errorf("cwd and isolation=worktree are mutually exclusive")
	}
	instructions := strings.TrimSpace(persona.Instructions)
	if roleInstructions := strings.TrimSpace(role.Instructions); roleInstructions != "" {
		if instructions != "" {
			instructions += "\n\n"
		}
		instructions += roleInstructions
	}
	return effectiveSubagentProfile{
		Type: input.SubagentType, Persona: role.Persona,
		Instructions: instructions,
		Inputs:       append([]config.SubagentContractItem(nil), persona.Inputs...), Outputs: append([]config.SubagentContractItem(nil), persona.Outputs...),
		Provider: provider, Model: model, Reasoning: firstNonempty(role.Reasoning, persona.Reasoning, parent.Reasoning), CapabilityMode: capability,
		RequestedIsolation: isolation, Isolation: "none", CWD: cwd, Tools: append([]string(nil), role.Tools...),
	}, nil
}

func cloneSubagentRoles(source map[string]config.SubagentRoleConfig) map[string]config.SubagentRoleConfig {
	result := make(map[string]config.SubagentRoleConfig, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneSubagentConfig(source config.SubagentConfig) config.SubagentConfig {
	cloned := source
	cloned.Toggle = cloneBoolMap(source.Toggle)
	cloned.Models = cloneStringMap(source.Models)
	cloned.Routes = cloneModelRouteMap(source.Routes)
	cloned.Roles = cloneSubagentRoles(source.Roles)
	cloned.Personas = cloneSubagentPersonas(source.Personas)
	return cloned
}

func cloneModelRouteMap(source map[string]config.ModelRouteConfig) map[string]config.ModelRouteConfig {
	result := make(map[string]config.ModelRouteConfig, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneSubagentPersonas(source map[string]config.SubagentPersonaConfig) map[string]config.SubagentPersonaConfig {
	result := make(map[string]config.SubagentPersonaConfig, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (r *subagentRuntime) roleCatalogSnapshot() (map[string]config.SubagentRoleConfig, map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSubagentRoles(r.cfg.Roles), cloneBoolMap(r.cfg.Toggle)
}

func (r *subagentRuntime) resolveResumeProfile(sourceID string, parent subagentParentRuntime) (effectiveSubagentProfile, error) {
	source, err := r.store.Get(r.ctx, sourceID)
	if err != nil || source.SessionID != parent.SessionID {
		return effectiveSubagentProfile{}, fmt.Errorf("resume source %q was not found in this session", sourceID)
	}
	if !subagentTerminal(source.State) {
		return effectiveSubagentProfile{}, fmt.Errorf("resume source %q is not terminal", sourceID)
	}
	seed, err := sanitizedResumeSeed(source.Transcript)
	if err != nil {
		return effectiveSubagentProfile{}, fmt.Errorf("resume source %q: %w", sourceID, err)
	}
	profile, err := r.resolveProfile(subagentSpawnInput{
		SubagentType: source.Type, Model: source.Model, CapabilityMode: source.CapabilityMode, Isolation: "none",
	}, parent)
	if err != nil {
		return effectiveSubagentProfile{}, fmt.Errorf("resume source %q: %w", sourceID, err)
	}
	cwd := source.CWD
	if source.RequestedIsolation != "worktree" {
		cwd, err = resolveSubagentCWD(parent.WorkspaceRoot, source.CWD)
		if err != nil {
			return effectiveSubagentProfile{}, fmt.Errorf("resume source %q cwd: %w", sourceID, err)
		}
	} else {
		cwd = parent.WorkspaceRoot
	}
	profile.Model = source.Model
	profile.Provider = firstNonempty(source.Provider, parent.ProviderID)
	profile.AccountID = source.AccountID
	profile.Reasoning = source.Reasoning
	profile.CapabilityMode = source.CapabilityMode
	profile.RequestedIsolation = source.RequestedIsolation
	profile.Isolation = "none"
	profile.CWD = cwd
	profile.Seed = seed
	if parent.PlanMode {
		profile.CapabilityMode = "read-only"
		profile.RequestedIsolation = "none"
	}
	return profile, nil
}

func sanitizedResumeSeed(encoded json.RawMessage) ([]message.Message, error) {
	if len(encoded) == 0 {
		return nil, fmt.Errorf("transcript is empty")
	}
	var transcript []message.Message
	if err := json.Unmarshal(encoded, &transcript); err != nil {
		return nil, fmt.Errorf("decode transcript: %w", err)
	}
	seed := make([]message.Message, 0, len(transcript))
	for _, item := range transcript {
		if item.Role != message.RoleUser && item.Role != message.RoleAssistant {
			continue
		}
		if text := strings.TrimSpace(item.Text); text != "" {
			seed = append(seed, message.NewText(item.Role, text))
		}
	}
	if len(seed) == 0 {
		return nil, fmt.Errorf("transcript has no resumable user or assistant text")
	}
	return seed, nil
}

func (r *subagentRuntime) pumpLocked() {
	for r.running < r.cfg.MaxConcurrency && len(r.pending) > 0 {
		id := r.pending[0]
		r.pending = r.pending[1:]
		active := r.active[id]
		if active == nil || active.terminalizing || active.run.State != agentservice.SubagentQueued {
			continue
		}
		active.slot = true
		r.running++
		r.wg.Add(1)
		go r.execute(id)
	}
}

func (r *subagentRuntime) execute(id string) {
	defer r.wg.Done()

	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.terminalizing {
		r.mu.Unlock()
		return
	}
	parent := active.parent
	prompt := active.prompt
	profile := active.profile
	ctx := active.ctx
	parentToolCallID := active.run.ParentToolCallID
	durableRunID := strings.TrimSpace(active.run.ChildRunID)
	recoveredWorktreePath := active.run.WorktreePath
	r.mu.Unlock()
	var childRun *agentservice.Run
	defer func() {
		if childRun != nil && parent.Host != nil {
			parent.Host.clearAutoReviewTracker(childRun.RunID)
		}
		recovered := recover()
		if recovered == nil {
			return
		}
		panicErr := fmt.Errorf("subagent panic: %v", recovered)
		if childRun != nil {
			completionCtx, cancelCompletion := context.WithTimeout(context.Background(), 5*time.Second)
			if err := parent.Coding.CompleteRun(completionCtx, childRun, "", panicErr); err != nil {
				panicErr = fmt.Errorf("%v; durable completion: %w", panicErr, err)
			}
			cancelCompletion()
		}
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: panicErr})
	}()
	if profile.RequestedIsolation == "worktree" {
		oldCWD := profile.CWD
		prepared := preparedSubagentWorktree{}
		var prepareErr error
		if durableRunID != "" && recoveredWorktreePath != "" {
			worktreeInfo, worktreeErr := os.Stat(recoveredWorktreePath)
			cwdInfo, cwdErr := os.Stat(profile.CWD)
			if worktreeErr != nil || !worktreeInfo.IsDir() || cwdErr != nil || !cwdInfo.IsDir() {
				r.terminalize(id, terminalRequest{
					state: agentservice.SubagentFailed,
					err:   fmt.Errorf("recover subagent worktree %q: workspace is unavailable", recoveredWorktreePath),
				})
				return
			}
			prepared = preparedSubagentWorktree{
				CWD: profile.CWD, Path: recoveredWorktreePath, Isolation: "worktree",
			}
			if commonDir, commonErr := runGit(ctx, recoveredWorktreePath, nil,
				"rev-parse", "--path-format=absolute", "--git-common-dir"); commonErr == nil {
				prepared.RepoRoot = filepath.Dir(strings.TrimSpace(commonDir))
			}
		} else {
			prepared, prepareErr = r.prepareWorktree(ctx, parent, profile, id)
		}
		if prepareErr != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: prepareErr})
			return
		}
		profile.CWD = prepared.CWD
		profile.Isolation = prepared.Isolation
		profile.WorktreeRepoRoot = prepared.RepoRoot
		r.mu.Lock()
		active = r.active[id]
		if active == nil || active.terminalizing {
			r.mu.Unlock()
			if prepared.Path != "" && durableRunID == "" {
				cleanup := agentservice.SubagentRun{WorktreePath: prepared.Path}
				finalizeSubagentWorktree(&cleanup, prepared.RepoRoot)
			}
			return
		}
		active.profile = profile
		active.run.CWD = prepared.CWD
		active.run.Isolation = prepared.Isolation
		active.run.WorktreePath = prepared.Path
		active.run.Warning = appendWarning(active.run.Warning, prepared.Warning)
		r.mu.Unlock()
		if parent.Host != nil && prepared.CWD != oldCWD {
			metadata := hooks.Metadata{SessionID: active.run.SessionID, RunID: active.run.ParentRunID, AgentID: id, AgentType: profile.Type, ParentRunID: active.run.ParentRunID, ParentToolCallID: parentToolCallID, CWD: prepared.CWD}
			_ = parent.Host.dispatchLifecycle(ctx, hooks.CwdChanged, metadata, func(e *hooks.Envelope) { e.OldCWD, e.NewCWD = oldCWD, prepared.CWD })
		}
	}

	var err error
	childModel := profile.Model
	contextTarget := parent.ContextTokenTarget
	contextBudget := ContextBudget{HardTrigger: contextTarget, Target: contextTarget}
	childContextWindow := 0
	childDriver := parent.Driver
	usageBudget := &providerUsageBudget{maxTokens: int64(r.cfg.Budget.MaxTokens)}
	if parent.ResolveAccountDriver != nil {
		resolvedAccount, resolvedModel, contextWindow, resolvedDriver, resolveErr := parent.ResolveAccountDriver(ctx, profile.Provider, profile.Model, profile.Reasoning, profile.AccountID)
		if resolveErr != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("resolve subagent provider: %w", resolveErr)})
			return
		}
		profile.AccountID = resolvedAccount
		childModel = resolvedModel
		childDriver = resolvedDriver
		childContextWindow = contextWindow
		contextBudget, err = calculateContextBudget(profile.Provider, childModel, contextWindow, 0, parent.ContextConfig)
		contextTarget = contextBudget.HardTrigger
		if err != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: err})
			return
		}
	} else if parent.ResolveDriver != nil {
		childModel, contextWindow, resolvedDriver, resolveErr := parent.ResolveDriver(ctx, profile.Provider, profile.Model, profile.Reasoning)
		if resolveErr != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("resolve subagent provider: %w", resolveErr)})
			return
		}
		childDriver = resolvedDriver
		childContextWindow = contextWindow
		contextBudget, err = calculateContextBudget(profile.Provider, childModel, contextWindow, 0, parent.ContextConfig)
		contextTarget = contextBudget.HardTrigger
		if err != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: err})
			return
		}
	} else if profile.Provider != parent.ProviderID || !providerHasModel(parent.Driver, profile.Model) {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("model %q is not available from provider %s", profile.Model, profile.Provider)})
		return
	} else {
		profile.AccountID = parent.AccountID
	}
	executionPolicy := agentservice.RunExecutionPolicy{
		AgentID:      durableSubagentAgentID(profile.Type),
		AgentVersion: "runtime",
		Governance: api.GovernancePolicy{Budget: api.Budget{
			MaxTokens: int64(r.cfg.Budget.MaxTokens), MaxRuntime: r.cfg.Budget.MaxWallClockDuration,
			MaxToolCalls: r.cfg.Budget.MaxToolCalls,
		}},
		Budget: &api.TaskBudget{
			MaxTokens: int64(r.cfg.Budget.MaxTokens), MaxWallClock: r.cfg.Budget.MaxWallClockDuration,
			MaxToolCalls: r.cfg.Budget.MaxToolCalls, MaxSteps: r.cfg.Budget.MaxTurns,
		},
	}
	executionPolicy.ResourceClaims, err = r.childWorkspaceClaims(ctx, parent, profile)
	if err != nil {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("resolve workspace claim: %w", err)})
		return
	}
	if durableRunID != "" && profile.Isolation != "worktree" && subagentMayMutateWorkspace(profile) {
		claim, claimErr := workspaceWriteClaim(parent.WorkspaceRoot)
		if claimErr != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: claimErr})
			return
		}
		parentHasClaim, claimErr := parentTaskHasWorkspaceClaim(ctx, parent, claim.Key)
		if claimErr != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: claimErr})
			return
		}
		if parentHasClaim {
			if claimErr := clearRecoveredSharedWorkspaceClaim(ctx, parent, durableRunID, claim.Key); claimErr != nil {
				r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: claimErr})
				return
			}
		}
	}
	if parent.Host != nil && parent.Host.cfg.Retry.Enabled {
		executionPolicy.RetryPolicy = api.RetryPolicy{
			MaxAttempts: parent.Host.cfg.Retry.MaxRetries + 1,
			Backoff:     parent.Host.cfg.Retry.BaseDelayDuration,
			MaxBackoff:  parent.Host.cfg.Retry.MaxDelayDuration,
		}
	}
	if durableRunID == "" {
		childRun, err = parent.Coding.StartRunWithMetadata(ctx, prompt, nil, executionPolicy)
		if err != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("start child run: %w", err)})
			return
		}
	} else {
		childRun, err = waitForSubagentResume(ctx, parent.Coding, durableRunID)
		if err != nil {
			if ctx.Err() != nil {
				r.terminalize(id, terminalRequest{state: agentservice.SubagentCancelled})
				return
			}
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("resume child run: %w", err)})
			return
		}
	}
	observeProviderRetries(ctx, parent.Host, parent.SessionID, childRun.RunID, profile.Provider, childDriver)
	childDriver = &budgetedProviderDriver{inner: childDriver, budget: usageBudget}
	workspaceDrivers, err := parent.Coding.WorkspaceDrivers(ctx, profile.CWD)
	if err != nil {
		_ = parent.Coding.CompleteRun(context.WithoutCancel(ctx), childRun, "", err)
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: err})
		return
	}
	allowed := effectiveSubagentTools(profile.Tools, profile.CapabilityMode)
	governed := make([]tool.Driver, 0, len(workspaceDrivers))
	toolNames := make([]string, 0, len(workspaceDrivers))
	for _, driver := range workspaceDrivers {
		definition := driver.Definition()
		if !allowed[definition.Name] {
			continue
		}
		governedDriver := &governedAgentTool{
			definition: definition, driver: driver, coding: parent.Coding, run: childRun, host: parent.Host,
			sessionID: parent.SessionID, agentID: id, agentType: profile.Type, parentToolCallID: parentToolCallID,
			streamRunID: childRun.RunID, update: func(update tool.Update) { r.handleToolUpdate(id, update) },
		}
		metadata := hooks.Metadata{
			SessionID: parent.SessionID, RunID: childRun.RunID, AgentID: id, AgentType: profile.Type,
			ParentRunID: parent.ParentRunID, ParentToolCallID: parentToolCallID, CWD: profile.CWD,
		}
		dispatcher := hooks.Dispatcher{}
		if parent.Host != nil {
			dispatcher = parent.Host.hooks
		}
		governed = append(governed, hooks.WrapDriver(dispatcher, metadata, governedDriver))
		toolNames = append(toolNames, definition.Name)
	}
	if parent.Host != nil && parent.Host.sessions != nil {
		governed = append(governed, &contextArtifactDriver{sessionID: parent.SessionID, store: parent.Host.sessions})
		toolNames = append(toolNames, contextReadArtifactTool)
	}
	skillSnapshot := parent.Coding.SkillSnapshot()
	instructions := renderSubagentInstructions(profile)
	if childContextWindow > 0 {
		contextBudget, err = calculateContextBudget(profile.Provider, childModel, childContextWindow, estimateToolDefinitionTokens(governed), parent.ContextConfig)
		contextTarget = contextBudget.HardTrigger
		if err != nil {
			r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: err})
			return
		}
	}
	extraBody := map[string]any{"prompt_cache_key": childRun.RunID}
	enableExplicitPromptCache(extraBody, profile.Provider, childModel)
	if parent.Host != nil && strings.TrimSpace(parent.Host.attachments.Root) != "" {
		extraBody[responses.AttachmentRootExtraKey] = parent.Host.attachments.Root
	}
	maxOutputTokens := 0
	if parent.Host != nil && parent.Host.providers != nil {
		maxOutputTokens = parent.Host.providers.modelMaxOutputTokens(profile.Provider, childModel)
		if maxOutputTokens > 0 {
			extraBody["max_output_tokens"] = maxOutputTokens
		}
	}
	spec := hyagent.Spec{
		Skills: skillSnapshot.Eager, AvailableSkills: skillSnapshot.Available,
		Instructions: instructions, Model: childModel, Tools: toolNames,
		MaxTokens: maxOutputTokens,
		LoopPolicy: hyagent.LoopPolicy{
			MaxIterations:       r.cfg.Budget.MaxTurns,
			UnlimitedIterations: r.cfg.Budget.MaxTurns == 0,
			MaxWallClock:        r.cfg.Budget.MaxWallClockDuration,
			ContextTokenTarget:  contextTarget,
		},
		ExtraBody: extraBody,
	}
	if parent.Host != nil && parent.Host.providers != nil {
		if parent.Host.sessions != nil {
			childDriver = &meteredProviderDriver{
				inner: childDriver, store: parent.Host.sessions, host: parent.Host,
				sessionID: parent.SessionID, runID: parent.ParentRunID, kind: "subagent", provider: profile.Provider,
				model: childModel, transport: childDriver.Metadata().Name,
			}
		}
	}
	compactionResolve := parent.ResolveDriver
	if parent.ResolveAccountDriver != nil {
		compactionResolve = func(ctx context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
			boundAccountID := ""
			if provider == profile.Provider {
				boundAccountID = profile.AccountID
			}
			_, resolvedModel, window, driver, resolveErr := parent.ResolveAccountDriver(ctx, provider, model, reasoning, boundAccountID)
			return resolvedModel, window, driver, resolveErr
		}
	}
	if compactionResolve != nil {
		baseResolve := compactionResolve
		compactionResolve = func(ctx context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
			resolvedModel, window, driver, resolveErr := baseResolve(ctx, provider, model, reasoning)
			if resolveErr == nil {
				observeProviderRetries(ctx, parent.Host, parent.SessionID, childRun.RunID, provider, driver)
			}
			return resolvedModel, window, driver, resolveErr
		}
	}
	compactionReport := r.compactionReporter(parent, parent.ParentRunID)
	if parent.Host != nil && parent.Host.sessions != nil {
		baseResolve := compactionResolve
		compactionResolve = func(ctx context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
			resolvedModel, window, driver, resolveErr := baseResolve(ctx, provider, model, reasoning)
			if resolveErr == nil {
				driver = &meteredProviderDriver{
					inner: driver, store: parent.Host.sessions, host: parent.Host, sessionID: parent.SessionID,
					runID: parent.ParentRunID, kind: "compaction", provider: provider, model: resolvedModel, transport: driver.Metadata().Name,
				}
			}
			return resolvedModel, window, driver, resolveErr
		}
		compactionReport = nil
	}
	contextManager := subagentTurnContext{
		instructions: instructions, privateContext: active.privateContext, seed: profile.Seed,
		inner: turnContext{
			structuredSummary: true, largeToolTokens: parent.ContextConfig.LargeToolResultTokens,
			compactTargetTokens: contextBudget.Target, minReclaimTokens: parent.ContextConfig.MinReclaimTokens,
			softTriggerTokens: contextBudget.SoftTrigger, backgroundPrepare: parent.ContextConfig.BackgroundPrepare, coordinator: &compactionCoordinator{},
			resolveSummarizer: lazyCompactionResolver(compactionResolve, parent.CompactionRoute, profile.Provider, childModel, profile.Reasoning, childRun.RunID+":compaction", usageBudget, compactionReport, parent.ContextConfig.MaxSummaryTokens),
		},
	}
	if parent.Host != nil && parent.Host.sessions != nil {
		contextManager.inner.putArtifact = func(ctx context.Context, kind string, payload []byte, preview string) (session.ContextArtifact, error) {
			return parent.Host.sessions.PutArtifact(ctx, parent.SessionID, childRun.RunID, kind, payload, preview)
		}
	}
	if parent.Host != nil {
		contextManager.inner.compactHooks = parent.Host.autoCompactHooks(hooks.Metadata{SessionID: parent.SessionID, RunID: childRun.RunID, AgentID: id, AgentType: profile.Type, ParentRunID: parent.ParentRunID, ParentToolCallID: parentToolCallID, CWD: profile.CWD})
	}
	definitionID := childRun.HolderID
	if definitionID == "azem-main" {
		definitionID = durableSubagentAgentID(profile.Type)
	}
	definition := agentDefinitionForSpec(
		definitionID, "Azem "+profile.Type, "Durable "+profile.Type+" subagent",
		spec, executionPolicy.Governance,
		map[string]string{"role": profile.Type, "provider": profile.Provider},
	)
	engine, err := materializeAgentDefinition(ctx, parent.Coding, definition, spec, hyagent.BuildDeps{
		Skills:    skillSnapshot.Registry,
		Providers: hyprovider.Single(childDriver), Tools: tool.NewBus(governed...), ContextManager: contextManager,
	})
	if err != nil {
		_ = parent.Coding.CompleteRun(context.WithoutCancel(ctx), childRun, "", err)
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("build child engine: %w", err)})
		return
	}
	engine.Hooks = engine.Hooks.Prepend(editRecoveryHook{run: childRun})
	if parent.Host != nil {
		metadata := hooks.Metadata{SessionID: parent.SessionID, RunID: childRun.RunID, AgentID: id, AgentType: profile.Type, ParentRunID: parent.ParentRunID, ParentToolCallID: parentToolCallID, CWD: profile.CWD}
		taskCompleted := hyagent.NewOutputGuardrail("claude-task-completed-hook", func(guardCtx context.Context, input hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
			decision := parent.Host.hooks.Dispatch(guardCtx, hooks.Envelope{
				SessionID: parent.SessionID, AgentID: id, AgentType: profile.Type, CWD: profile.CWD,
				HookEventName: hooks.TaskCompleted, TaskID: id, TaskSubject: active.run.Description,
				TaskDescription: prompt, LastAssistantMessage: strings.TrimSpace(input.Output.Text),
			})
			if decision.PreventContinuation {
				return hyagent.OutputGuardrailResult{}, fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, decision.StopReason)
			}
			if !decision.Denied {
				return hyagent.AllowOutput(), nil
			}
			reason := firstNonempty(strings.TrimSpace(decision.Reason), "A TaskCompleted hook requested more work before completion.")
			return hyagent.RetryOutputWithPolicy(hyagent.RetryPolicy{IncludeRejectedOutput: true}, message.NewText(message.RoleUser, reason)), nil
		})
		engine.OutputGuardrails = append(engine.OutputGuardrails, parent.Host.stopHookGuardrail(metadata, hooks.SubagentStop, func(input hyagent.OutputGuardrailInput) string {
			messages := append(append([]message.Message(nil), input.Messages...), input.Output)
			transcript, marshalErr := json.Marshal(messages)
			if marshalErr != nil {
				return ""
			}
			path, _ := writeSubagentHookTranscript(r.worktreeRoot, id, transcript)
			return path
		}), taskCompleted)
	}

	r.mu.Lock()
	active = r.active[id]
	if active == nil || active.terminalizing {
		r.mu.Unlock()
		_ = parent.Coding.CompleteRun(context.WithoutCancel(ctx), childRun, "", context.Canceled)
		return
	}
	active.run.ChildRunID = childRun.RunID
	active.run.State = agentservice.SubagentRunning
	active.run.Summary = "running"
	active.run.CWD = profile.CWD
	active.run.AccountID = profile.AccountID
	active.run.Isolation = profile.Isolation
	running := cloneSubagentRun(active.run)
	r.mu.Unlock()
	if err := r.store.Save(r.ctx, running); err != nil {
		active.cancel()
		_ = parent.Coding.CompleteRun(context.WithoutCancel(ctx), childRun, "", err)
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("persist running subagent: %w", err)})
		return
	}
	r.mu.Lock()
	if active = r.active[id]; active != nil && !active.terminalizing {
		active.run = running
		r.signalChangedLocked()
	}
	r.mu.Unlock()
	r.emitState(running, "running")

	restartingAttempt := false
	sink := stream.SinkFunc(func(frameCtx context.Context, frame stream.Frame) error {
		if restartingAttempt && frame.Kind != stream.FrameError {
			r.mu.Lock()
			if current := r.active[id]; current != nil && !current.terminalizing {
				current.blocks = discardAgentAttemptBlocks(current.blocks, childRun.RunID)
			}
			r.mu.Unlock()
			if parent.Host != nil && !parent.Host.emit(frameCtx, Event{
				Kind: EventProviderRetry, SessionID: parent.SessionID, RunID: childRun.RunID, AgentID: id,
				State: "restarted", Data: map[string]string{"scope": "attempt"},
			}) {
				return eventDeliveryError(frameCtx)
			}
			restartingAttempt = false
		}
		if frame.Kind == stream.FrameError {
			restartingAttempt = true
		}
		frame.Source = "child:" + id
		r.handleFrame(id, frame)
		return nil
	})
	var result hyagent.Result
	workerStarted := false
	var runErr error
executionLoop:
	for {
		outcome, executeErr := parent.Coding.ExecuteRun(agentservice.DelegatedApprovalContext(ctx), childRun, engine, sink)
		if outcome.State != "" {
			workerStarted = true
		}
		result, runErr = outcome.Result, executeErr
		var unavailable *hyworker.TaskExecutionUnavailableError
		if errors.As(executeErr, &unavailable) {
			summary := "waiting for durable execution lease"
			if unavailable.ResourceClaims.Reason == api.ResourceClaimDeniedConflict {
				summary = "waiting for shared workspace"
			}
			if err := r.persistActiveState(id, summary); err != nil {
				runErr = errors.Join(runErr, err)
				break
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				runErr = ctx.Err()
				break executionLoop
			case <-timer.C:
				continue
			}
		}
		if outcome.State != hyworker.ExecutionSuspended {
			break
		}
		summary := "waiting for durable resume"
		if outcome.Suspension != nil {
			summary = "waiting for " + string(outcome.Suspension.Kind)
		}
		if err := r.persistActiveState(id, summary); err != nil {
			runErr = errors.Join(runErr, err)
			break
		}
		if ctx.Err() != nil {
			runErr = ctx.Err()
			break
		}
		resumed, resumeErr := waitForSubagentResume(ctx, parent.Coding, childRun.RunID)
		if resumeErr != nil {
			if ctx.Err() != nil {
				runErr = ctx.Err()
			} else {
				runErr = errors.Join(runErr, resumeErr)
			}
			break
		}
		copyRunExecution(childRun, resumed)
		if err := r.persistActiveState(id, "running"); err != nil {
			runErr = err
			break
		}
	}
	if errors.Is(runErr, hyagent.ErrBudgetExhausted) && strings.Contains(runErr.Error(), "max tokens") {
		runErr = fmt.Errorf("%w (increase agents.subagents.budget.max_tokens, or set it to 0 for unbounded, in config.yaml)", runErr)
	}
	stopHookRan := runErr == nil && ctx.Err() == nil && parent.Host != nil
	if !workerStarted {
		completionCtx, completionCancel := context.WithTimeout(context.Background(), 5*time.Second)
		completeErr := parent.Coding.CompleteRun(completionCtx, childRun, result.Text, runErr)
		completionCancel()
		if completeErr != nil {
			if runErr == nil {
				runErr = completeErr
			} else {
				runErr = fmt.Errorf("%v; durable completion: %w", runErr, completeErr)
			}
		}
	}
	state := agentservice.SubagentCompleted
	if ctx.Err() != nil {
		state = agentservice.SubagentCancelled
		runErr = nil
	} else if runErr != nil {
		state = agentservice.SubagentFailed
	}
	r.terminalize(id, terminalRequest{state: state, err: runErr, result: &result, stopHookRan: stopHookRan})
}

func discardAgentAttemptBlocks(blocks []AgentTranscriptBlock, runID string) []AgentTranscriptBlock {
	boundary := len(blocks)
	for boundary > 0 {
		block := blocks[boundary-1]
		if block.RunID != runID || (block.Kind != "thinking" && block.Kind != "commentary" && block.Kind != "assistant") {
			break
		}
		boundary--
	}
	return blocks[:boundary]
}

func (r *subagentRuntime) persistActiveState(id, summary string) error {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.terminalizing || active.terminalized {
		r.mu.Unlock()
		return context.Canceled
	}
	run := cloneSubagentRun(active.run)
	run.State = agentservice.SubagentRunning
	run.Summary = summary
	r.mu.Unlock()
	if err := r.store.Save(r.ctx, run); err != nil {
		return err
	}
	r.mu.Lock()
	if current := r.active[id]; current != nil && !current.terminalizing && !current.terminalized {
		current.run = run
		r.signalChangedLocked()
	}
	r.mu.Unlock()
	r.emitState(run, string(run.State))
	return nil
}

func (r *subagentRuntime) ownsDurableRun(runID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, active := range r.active {
		if active != nil && active.run.ChildRunID == runID &&
			!active.terminalizing && !active.terminalized {
			return true
		}
	}
	return false
}

func waitForSubagentResume(ctx context.Context, coding *agentservice.Service, runID string) (*agentservice.Run, error) {
	const pollInterval = 100 * time.Millisecond
	for {
		durable, err := coding.Runner().Run(ctx, runID)
		if err != nil {
			return nil, err
		}
		switch durable.Status {
		case api.RunStatusRunning, api.RunStatusCreated, api.RunStatusBlocked:
			resumed, resumeErr := coding.ResumeRun(ctx, runID)
			if resumeErr != nil {
				return nil, resumeErr
			}
			return resumed, nil
		case api.RunStatusCompleted, api.RunStatusFailed, api.RunStatusCancelled:
			return nil, fmt.Errorf("subagent durable run %q became terminal with status %q", runID, durable.Status)
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func copyRunExecution(target, source *agentservice.Run) {
	target.EnvelopeID = source.EnvelopeID
	target.LeaseID = source.LeaseID
	target.TaskVersion = source.TaskVersion
	target.HolderID = source.HolderID
}

type terminalRequest struct {
	state       agentservice.SubagentState
	err         error
	result      *hyagent.Result
	stopHookRan bool
}

func (r *subagentRuntime) terminalize(id string, request terminalRequest) {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.terminalizing || active.terminalized {
		r.mu.Unlock()
		return
	}
	active.terminalizing = true
	run := cloneSubagentRun(active.run)
	run.State = request.state
	run.FinishedAt = time.Now().UTC()
	if request.result != nil {
		run.Output = sanitizeFinalAnswerText(request.result.Text)
		if run.Output == "" && len(request.result.Structured) > 0 {
			var compact bytes.Buffer
			if json.Compact(&compact, request.result.Structured) == nil {
				run.Output = compact.String()
			}
		}
		// Output-token length and iteration ceilings both surface as max_turns.
		// A completed empty answer after that ceiling is a truncation failure,
		// not a successful no-op completion.
		if request.err == nil && request.result.StopReason == hyprovider.StopReasonMaxTurns &&
			run.State == agentservice.SubagentCompleted {
			request.err = errOutputTruncated
			run.State = agentservice.SubagentFailed
		}
		transcript, transcriptErr := json.Marshal(request.result.Messages)
		if transcriptErr != nil && request.err == nil {
			request.err = fmt.Errorf("encode subagent transcript: %w", transcriptErr)
			run.State = agentservice.SubagentFailed
		} else {
			run.Transcript = transcript
		}
		run.Turns = max(active.run.Turns, len(request.result.Steps))
		run.TokensUsed = request.result.Usage.TotalTokens
	}
	if request.err != nil && run.State != agentservice.SubagentCancelled {
		run.Error = request.err.Error()
		run.State = agentservice.SubagentFailed
	}
	if run.State == agentservice.SubagentCompleted && run.Output == "" {
		run.State = agentservice.SubagentFailed
		run.Error = "subagent completed without a final answer"
	}
	if run.State == agentservice.SubagentCancelled {
		run.Error = ""
	}
	run.ToolCalls = active.run.ToolCalls
	if run.Turns == 0 {
		run.Turns = active.run.Turns
	}
	if run.TokensUsed == 0 {
		run.TokensUsed = active.run.TokensUsed
	}
	run.ToolsUsed = sortedToolSet(active.toolNames)
	run.Summary = subagentSummary(run)
	active.run = run
	parentHost := active.parent.Host
	activity := active.activity
	worktreeRepoRoot := active.profile.WorktreeRepoRoot
	r.mu.Unlock()
	agentTranscriptPath, transcriptWriteErr := writeSubagentHookTranscript(r.worktreeRoot, run.ID, run.Transcript)
	if transcriptWriteErr != nil {
		run.Warning = appendWarning(run.Warning, "write hook transcript: "+transcriptWriteErr.Error())
	}
	if parentHost != nil && !request.stopHookRan {
		metadata := hooks.Metadata{SessionID: run.SessionID, RunID: run.ChildRunID, AgentID: run.ID, AgentType: run.Type, ParentRunID: run.ParentRunID, ParentToolCallID: run.ParentToolCallID, CWD: run.CWD}
		if err := parentHost.dispatchLifecycle(parentHost.ctx, hooks.SubagentStop, metadata, func(e *hooks.Envelope) {
			e.Trigger, e.StopHookActive, e.LastAssistantMessage = string(run.State), false, run.Output
			e.AgentTranscriptPath = agentTranscriptPath
		}); err != nil && run.State == agentservice.SubagentCompleted {
			run.State, run.Error = agentservice.SubagentFailed, fmt.Sprintf("subagent stop blocked: %v", err)
		}
	}
	removedWorktree := run.WorktreePath
	finalizeSubagentWorktree(&run, worktreeRepoRoot)
	if parentHost != nil && removedWorktree != "" && run.WorktreePath == "" {
		metadata := hooks.Metadata{SessionID: run.SessionID, RunID: run.ChildRunID, AgentID: run.ID, AgentType: run.Type, ParentRunID: run.ParentRunID, ParentToolCallID: run.ParentToolCallID, CWD: run.CWD}
		_ = parentHost.dispatchLifecycle(parentHost.ctx, hooks.WorktreeRemove, metadata, func(e *hooks.Envelope) { e.WorktreePath = removedWorktree })
	}
	run.Summary = subagentSummary(run)

	saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
	saveErr := r.store.Save(saveCtx, run)
	saveCancel()
	if saveErr != nil {
		run.State = agentservice.SubagentFailed
		run.Error = fmt.Sprintf("persist terminal subagent: %v", saveErr)
		run.Warning = appendWarning(run.Warning, run.Error)
		run.Summary = subagentSummary(run)
	}

	r.mu.Lock()
	active = r.active[id]
	if active == nil {
		r.mu.Unlock()
		return
	}
	active.run = run
	active.terminalized = true
	if saveErr != nil {
		r.terminalFallback[id] = r.snapshotFromActiveLocked(active)
	}
	if active.slot {
		r.running--
		active.slot = false
	}
	delete(r.active, id)
	close(active.done)
	r.signalChangedLocked()
	r.pumpLocked()
	r.mu.Unlock()
	r.emitStateTo(parentHost, run, activity)
	r.maybeAutoWake(parentHost, run)
}

func writeSubagentHookTranscript(worktreeRoot, id string, transcript json.RawMessage) (string, error) {
	if strings.TrimSpace(worktreeRoot) == "" || strings.TrimSpace(id) == "" {
		return "", nil
	}
	directory := filepath.Join(filepath.Dir(worktreeRoot), "hook-transcripts")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(id))
	path := filepath.Join(directory, fmt.Sprintf("%x.jsonl", digest[:12]))
	var messages []json.RawMessage
	if len(transcript) > 0 {
		if err := json.Unmarshal(transcript, &messages); err != nil {
			return "", err
		}
	}
	var output bytes.Buffer
	for _, item := range messages {
		output.Write(item)
		output.WriteByte('\n')
	}
	if err := os.WriteFile(path, output.Bytes(), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (r *subagentRuntime) prepareWorktree(ctx context.Context, parent subagentParentRuntime, profile effectiveSubagentProfile, id string) (preparedSubagentWorktree, error) {
	if parent.Host != nil {
		metadata := hooks.Metadata{SessionID: parent.SessionID, RunID: parent.ParentRunID, AgentID: id, AgentType: profile.Type, ParentRunID: parent.ParentRunID, CWD: profile.CWD}
		envelope := hooks.Envelope{SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID, AgentType: metadata.AgentType, ParentRunID: metadata.ParentRunID, CWD: metadata.CWD, HookEventName: hooks.WorktreeCreate, Name: id}
		decision := parent.Host.hooks.Dispatch(ctx, envelope)
		if decision.PreventContinuation {
			return preparedSubagentWorktree{}, fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, decision.StopReason)
		}
		if decision.Denied {
			return preparedSubagentWorktree{}, fmt.Errorf("worktree creation blocked by hook: %s", decision.Reason)
		}
		if len(decision.Runs) > 0 {
			for _, run := range decision.Runs {
				if run.Failure != nil || run.ExitCode != 0 {
					return preparedSubagentWorktree{}, fmt.Errorf("worktree hook %q failed", run.Name)
				}
				path := strings.TrimSpace(run.Output.HookSpecificOutput.WorktreePath)
				if path == "" {
					path = strings.TrimSpace(run.Stdout)
				}
				if filepath.IsAbs(path) {
					if info, err := os.Stat(path); err == nil && info.IsDir() {
						return preparedSubagentWorktree{CWD: path, Path: path, Isolation: "worktree"}, nil
					}
				}
			}
			return preparedSubagentWorktree{}, fmt.Errorf("worktree hook returned no valid absolute directory")
		}
	}
	return prepareSubagentWorktree(ctx, profile.CWD, r.worktreeRoot, id)
}

func (r *subagentRuntime) AutoWakePending(sessionID string) {
	if !r.cfg.AutoWake {
		return
	}
	r.mu.Lock()
	host := r.hosts[sessionID]
	r.mu.Unlock()
	if host == nil {
		return
	}
	runs, err := r.store.List(r.ctx, sessionID)
	if err != nil {
		return
	}
	for _, run := range runs {
		if run.Background && !run.CompletionDelivered && run.State != agentservice.SubagentCancelled && subagentTerminal(run.State) {
			r.maybeAutoWake(host, run)
			return
		}
	}
}

func (r *subagentRuntime) maybeAutoWake(host *Service, run agentservice.SubagentRun) {
	if !r.cfg.AutoWake || host == nil || !run.Background || run.CompletionDelivered ||
		run.State == agentservice.SubagentCancelled || !subagentTerminal(run.State) {
		return
	}
	r.mu.Lock()
	if r.ctx.Err() != nil || r.wakeInFlight[run.ID] {
		r.mu.Unlock()
		return
	}
	r.wakeInFlight[run.ID] = true
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			delete(r.wakeInFlight, run.ID)
			r.mu.Unlock()
		}()
		current, err := r.store.Get(r.ctx, run.ID)
		if err != nil || current.CompletionDelivered || !host.canStartAutoWake(current.SessionID) {
			return
		}
		if err := host.startSubagentAutoWake(current); err != nil {
			return
		}
		_ = r.store.SetCompletionDelivered(r.ctx, run.ID, true)
	}()
}

func (r *subagentRuntime) compactionReporter(parent subagentParentRuntime, runID string) compactionUsageReporter {
	if parent.Host == nil || strings.TrimSpace(parent.SessionID) == "" {
		return nil
	}
	return func(providerID, modelID, reasoning, transport string, usage hyprovider.Usage, reasoningTokens, cacheWriteTokens int) {
		parent.Host.emit(parent.Host.ctx, Event{Kind: EventContextUsage, SessionID: parent.SessionID, RunID: runID, State: "reported", Data: map[string]string{
			"inputTokens": fmt.Sprint(usage.InputTokens), "cachedInputTokens": fmt.Sprint(usage.CachedInputTokens),
			"outputTokens": fmt.Sprint(usage.OutputTokens), "totalTokens": fmt.Sprint(usage.TotalTokens),
			"reasoningTokens":     fmt.Sprint(reasoningTokens),
			"cacheWriteTokens":    fmt.Sprint(cacheWriteTokens),
			"uncachedInputTokens": fmt.Sprint(max(0, usage.InputTokens-usage.CachedInputTokens)),
			"cacheStatus":         "reported", "aggregateOnly": "true", "requestKind": "compaction",
			"provider": providerID, "model": modelID, "reasoning": reasoning, "transport": transport,
		}})
	}
}
