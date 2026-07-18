package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/stream"
	"github.com/Viking602/go-hydaelyn/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/session"
)

const (
	subagentSpawnTool     = "subagent.spawn"
	subagentGetOutputTool = "subagent.get_output"
	subagentKillTool      = "subagent.kill"
)

type subagentParentRuntime struct {
	SessionID     string
	ParentRunID   string
	ParentAgentID string
	ProviderID    string
	ModelID       string
	Reasoning     string
	WorkspaceRoot string
	Driver        hyprovider.Driver
	Coding        *agentservice.Service
	Host          *Service
}

type effectiveSubagentProfile struct {
	Type               string
	Persona            string
	Instructions       string
	Inputs             []config.SubagentContractItem
	Outputs            []config.SubagentContractItem
	Seed               []message.Message
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
	ctx, cancel := context.WithCancel(parent)
	return &subagentRuntime{
		cfg: cfg, store: store, worktreeRoot: worktreeRoot, ctx: ctx, cancel: cancel,
		active: make(map[string]*activeSubagent), terminalFallback: make(map[string]agentservice.SubagentSnapshot),
		hosts: make(map[string]*Service), wakeInFlight: make(map[string]bool), changed: make(chan struct{}),
	}, nil
}

func (r *subagentRuntime) Drivers(parent subagentParentRuntime) ([]tool.Driver, error) {
	if !r.cfg.Enabled {
		return nil, nil
	}
	if parent.Coding == nil || parent.Driver == nil || parent.SessionID == "" || parent.ParentRunID == "" {
		return nil, fmt.Errorf("subagent parent runtime is incomplete")
	}
	return []tool.Driver{
		&subagentSpawnDriver{runtime: r, parent: parent},
		&subagentGetOutputDriver{runtime: r, sessionID: parent.SessionID},
		&subagentKillDriver{runtime: r, sessionID: parent.SessionID},
	}, nil
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
	id, err := newSubagentID()
	if err != nil {
		return agentservice.SubagentRun{}, err
	}
	now := time.Now().UTC()
	run := agentservice.SubagentRun{
		ID: id, SessionID: parent.SessionID, ParentRunID: parent.ParentRunID, ParentAgentID: parent.ParentAgentID,
		ParentToolCallID: input.parentToolCallID, Description: input.Description, Type: profile.Type,
		State: agentservice.SubagentInitializing, Summary: "initializing", Model: profile.Model, Reasoning: profile.Reasoning,
		CapabilityMode: profile.CapabilityMode, RequestedIsolation: profile.RequestedIsolation, Isolation: profile.Isolation,
		CWD: profile.CWD, Background: input.Background, StartedAt: now,
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
	if !r.cfg.Enabled {
		return effectiveSubagentProfile{}, fmt.Errorf("subagents are disabled")
	}
	role, ok := r.cfg.Roles[input.SubagentType]
	if !ok {
		return effectiveSubagentProfile{}, fmt.Errorf("unknown subagent type %q (available: %s)", input.SubagentType, strings.Join(sortedRoleNames(r.cfg.Roles, r.cfg.Toggle), ", "))
	}
	if enabled, configured := r.cfg.Toggle[input.SubagentType]; configured && !enabled {
		return effectiveSubagentProfile{}, fmt.Errorf("subagent type %q is disabled", input.SubagentType)
	}
	persona := config.SubagentPersonaConfig{}
	if role.Persona != "" {
		var found bool
		persona, found = r.cfg.Personas[role.Persona]
		if !found {
			return effectiveSubagentProfile{}, fmt.Errorf("subagent type %q references unknown persona %q", input.SubagentType, role.Persona)
		}
	}
	model := firstNonempty(input.Model, role.Model, persona.Model, parent.ModelID)
	if !providerHasModel(parent.Driver, model) {
		return effectiveSubagentProfile{}, fmt.Errorf("model %q is not available from provider %s", model, parent.ProviderID)
	}
	capability := firstNonempty(input.CapabilityMode, role.CapabilityMode, "read-only")
	isolation := firstNonempty(input.Isolation, role.Isolation, persona.Isolation, "none")
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
		Model: model, Reasoning: firstNonempty(role.Reasoning, persona.Reasoning, parent.Reasoning), CapabilityMode: capability,
		RequestedIsolation: isolation, Isolation: "none", CWD: cwd, Tools: append([]string(nil), role.Tools...),
	}, nil
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
	profile.Reasoning = source.Reasoning
	profile.CapabilityMode = source.CapabilityMode
	profile.RequestedIsolation = source.RequestedIsolation
	profile.Isolation = "none"
	profile.CWD = cwd
	profile.Seed = seed
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
		prepared, prepareErr := r.prepareWorktree(ctx, parent, profile, id)
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
			if prepared.Path != "" {
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
	childRun, err = parent.Coding.StartRun(ctx, prompt)
	if err != nil {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("start child run: %w", err)})
		return
	}
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
		metadata := hooks.Metadata{SessionID: parent.SessionID, RunID: childRun.RunID, AgentID: id, AgentType: profile.Type,
			ParentRunID: parent.ParentRunID, ParentToolCallID: parentToolCallID, CWD: profile.CWD}
		dispatcher := hooks.Dispatcher{}
		if parent.Host != nil {
			dispatcher = parent.Host.hooks
		}
		governed = append(governed, hooks.WrapDriver(dispatcher, metadata, governedDriver))
		toolNames = append(toolNames, definition.Name)
	}
	skillSnapshot := parent.Coding.SkillSnapshot()
	instructions := renderSubagentInstructions(profile)
	spec := hyagent.Spec{
		Skills: skillSnapshot.Eager, AvailableSkills: skillSnapshot.Available,
		Instructions: instructions, Model: profile.Model, Tools: toolNames,
		LoopPolicy: hyagent.LoopPolicy{MaxIterations: r.cfg.Budget.MaxTurns, MaxWallClock: r.cfg.Budget.MaxWallClockDuration},
	}
	contextManager := subagentTurnContext{instructions: instructions, privateContext: active.privateContext, seed: profile.Seed}
	if parent.Host != nil {
		contextManager.compactHooks = parent.Host.autoCompactHooks(hooks.Metadata{SessionID: parent.SessionID, RunID: childRun.RunID, AgentID: id, AgentType: profile.Type, ParentRunID: parent.ParentRunID, ParentToolCallID: parentToolCallID, CWD: profile.CWD})
	}
	engine, err := hyagent.Build(spec, hyagent.BuildDeps{
		Skills:    skillSnapshot.Registry,
		Providers: hyprovider.Single(parent.Driver), Tools: tool.NewBus(governed...), ContextManager: contextManager,
	})
	if err != nil {
		_ = parent.Coding.CompleteRun(context.WithoutCancel(ctx), childRun, "", err)
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("build child engine: %w", err)})
		return
	}
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

	task := api.Task{
		ID: childRun.TaskID, RunID: childRun.RunID, Type: api.TaskTypeWorker, Goal: prompt,
		Budget: &api.TaskBudget{
			MaxTokens: int64(r.cfg.Budget.MaxTokens), MaxWallClock: r.cfg.Budget.MaxWallClockDuration,
			MaxToolCalls: r.cfg.Budget.MaxToolCalls, MaxSteps: r.cfg.Budget.MaxTurns,
		},
	}
	result := engine.RunStream(ctx, task, hyagent.OutputPolicy{}, stream.SinkFunc(func(_ context.Context, frame stream.Frame) error {
		frame.Source = "child:" + id
		r.handleFrame(id, frame)
		return nil
	}))
	var runErr error
	if result.Failure != nil {
		runErr = result.Failure
	}
	stopHookRan := runErr == nil && ctx.Err() == nil && parent.Host != nil
	completionCtx, completionCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if completeErr := parent.Coding.CompleteRun(completionCtx, childRun, result.Text, runErr); completeErr != nil {
		if runErr == nil {
			runErr = completeErr
		} else {
			runErr = fmt.Errorf("%v; durable completion: %w", runErr, completeErr)
		}
	}
	completionCancel()
	state := agentservice.SubagentCompleted
	if ctx.Err() != nil {
		state = agentservice.SubagentCancelled
		runErr = nil
	} else if runErr != nil {
		state = agentservice.SubagentFailed
	}
	r.terminalize(id, terminalRequest{state: state, err: runErr, result: &result, stopHookRan: stopHookRan})
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
		run.Output = strings.TrimSpace(request.result.Text)
		if run.Output == "" && len(request.result.Structured) > 0 {
			var compact bytes.Buffer
			if json.Compact(&compact, request.result.Structured) == nil {
				run.Output = compact.String()
			}
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
	return prepareSubagentWorktree(ctx, profile.CWD, r.worktreeRoot, id), nil
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

func (r *subagentRuntime) Query(ctx context.Context, sessionID string, ids []string, timeout time.Duration) []agentservice.SubagentSnapshot {
	deadline := time.Now().Add(timeout)
	for {
		snapshots, allTerminal := r.queryOnce(sessionID, ids)
		if timeout <= 0 || allTerminal || time.Now().After(deadline) {
			return snapshots
		}
		r.mu.Lock()
		changed := r.changed
		r.mu.Unlock()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return snapshots
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return r.queryCurrent(sessionID, ids)
		case <-changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			return r.queryCurrent(sessionID, ids)
		}
	}
}

func (r *subagentRuntime) queryOnce(sessionID string, ids []string) ([]agentservice.SubagentSnapshot, bool) {
	snapshots := r.queryCurrent(sessionID, ids)
	allTerminal := true
	for _, snapshot := range snapshots {
		if snapshot.Found && !subagentTerminal(snapshot.Run.State) {
			allTerminal = false
			break
		}
	}
	return snapshots, allTerminal
}

func (r *subagentRuntime) waitForForegroundStart(ctx context.Context, sessionID, id string) agentservice.SubagentSnapshot {
	for {
		r.mu.Lock()
		active := r.active[id]
		if active == nil || active.run.SessionID != sessionID {
			r.mu.Unlock()
			return r.snapshot(id, sessionID)
		}
		snapshot := r.snapshotFromActiveLocked(active)
		if active.run.State != agentservice.SubagentInitializing && active.run.State != agentservice.SubagentQueued {
			r.mu.Unlock()
			return snapshot
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return r.snapshot(id, sessionID)
		case <-changed:
		}
	}
}

func (r *subagentRuntime) queryCurrent(sessionID string, ids []string) []agentservice.SubagentSnapshot {
	result := make([]agentservice.SubagentSnapshot, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.snapshot(id, sessionID))
	}
	return result
}

func (r *subagentRuntime) snapshot(id, sessionID string) agentservice.SubagentSnapshot {
	r.mu.Lock()
	if active := r.active[id]; active != nil && active.run.SessionID == sessionID {
		snapshot := r.snapshotFromActiveLocked(active)
		r.mu.Unlock()
		return snapshot
	}
	if fallback, ok := r.terminalFallback[id]; ok && fallback.Run.SessionID == sessionID {
		fallback.Run = cloneSubagentRun(fallback.Run)
		r.mu.Unlock()
		return fallback
	}
	r.mu.Unlock()
	run, err := r.store.Get(r.ctx, id)
	if err != nil || run.SessionID != sessionID {
		return agentservice.SubagentSnapshot{Run: agentservice.SubagentRun{ID: id}}
	}
	return snapshotFromRun(run)
}

func (r *subagentRuntime) snapshotFromActiveLocked(active *activeSubagent) agentservice.SubagentSnapshot {
	run := cloneSubagentRun(active.run)
	run.ToolCalls = active.run.ToolCalls
	run.Turns = active.run.Turns
	run.TokensUsed = active.run.TokensUsed
	run.ToolsUsed = sortedToolSet(active.toolNames)
	return snapshotFromRun(run)
}

func (r *subagentRuntime) List(_ context.Context, sessionID string) []agentservice.SubagentSnapshot {
	runs, _ := r.store.List(r.ctx, sessionID)
	byID := make(map[string]agentservice.SubagentSnapshot, len(runs))
	for _, run := range runs {
		byID[run.ID] = snapshotFromRun(run)
	}
	r.mu.Lock()
	for id, active := range r.active {
		if active.run.SessionID == sessionID {
			byID[id] = r.snapshotFromActiveLocked(active)
		}
	}
	for id, fallback := range r.terminalFallback {
		if fallback.Run.SessionID == sessionID {
			fallback.Run = cloneSubagentRun(fallback.Run)
			byID[id] = fallback
		}
	}
	r.mu.Unlock()
	result := make([]agentservice.SubagentSnapshot, 0, len(byID))
	for _, snapshot := range byID {
		result = append(result, snapshot)
	}
	slices.SortFunc(result, func(a, b agentservice.SubagentSnapshot) int {
		if comparison := a.Run.StartedAt.Compare(b.Run.StartedAt); comparison != 0 {
			return comparison
		}
		return strings.Compare(a.Run.ID, b.Run.ID)
	})
	return result
}

func (r *subagentRuntime) Detail(ctx context.Context, sessionID, id string) ([]AgentTranscriptBlock, error) {
	r.mu.Lock()
	if active := r.active[id]; active != nil && active.run.SessionID == sessionID {
		blocks := append([]AgentTranscriptBlock(nil), active.blocks...)
		r.mu.Unlock()
		return blocks, nil
	}
	r.mu.Unlock()
	snapshot := r.snapshot(id, sessionID)
	if !snapshot.Found {
		return nil, api.ErrNotFound
	}
	return transcriptToAgentBlocks(snapshot.Run.Transcript)
}

func (r *subagentRuntime) Cancel(sessionID, id string) agentservice.SubagentCancelOutcome {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.run.SessionID != sessionID {
		r.mu.Unlock()
		snapshot := r.snapshot(id, sessionID)
		if !snapshot.Found {
			return agentservice.SubagentCancelOutcome{Outcome: "not_found"}
		}
		return agentservice.SubagentCancelOutcome{Outcome: "already_finished", Snapshot: snapshot}
	}
	if active.run.State == agentservice.SubagentCancelling {
		snapshot := r.snapshotFromActiveLocked(active)
		r.mu.Unlock()
		return agentservice.SubagentCancelOutcome{Outcome: "cancel_requested", Snapshot: snapshot}
	}
	if subagentTerminal(active.run.State) {
		snapshot := r.snapshotFromActiveLocked(active)
		r.mu.Unlock()
		return agentservice.SubagentCancelOutcome{Outcome: "already_finished", Snapshot: snapshot}
	}
	active.run.State = agentservice.SubagentCancelling
	active.run.Summary = "cancelling"
	cancelling := cloneSubagentRun(active.run)
	if err := r.store.Save(r.ctx, cancelling); err != nil {
		r.mu.Unlock()
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("persist cancelling subagent: %w", err)})
		return agentservice.SubagentCancelOutcome{Outcome: "cancel_requested", Snapshot: r.snapshot(id, sessionID)}
	}
	active.run = cancelling
	snapshot := r.snapshotFromActiveLocked(active)
	cancel := active.cancel
	queued := !active.slot
	r.mu.Unlock()
	r.emitState(cancelling, "cancelling")
	cancel()
	if queued {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentCancelled})
	}
	return agentservice.SubagentCancelOutcome{Outcome: "cancel_requested", Snapshot: snapshot}
}

func (r *subagentRuntime) HasForegroundByParentRun(sessionID, parentRunID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, active := range r.active {
		if active.run.SessionID == sessionID && active.run.ParentRunID == parentRunID && !active.run.Background &&
			!active.terminalizing && !subagentTerminal(active.run.State) {
			return true
		}
	}
	return false
}

func (r *subagentRuntime) CancelByParentRun(sessionID, parentRunID string, cancelBackground bool) {
	r.mu.Lock()
	ids := make([]string, 0)
	for _, active := range r.active {
		if active.run.SessionID == sessionID && active.run.ParentRunID == parentRunID && (!active.run.Background || cancelBackground) {
			ids = append(ids, active.run.ID)
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.Cancel(sessionID, id)
	}
}

func (r *subagentRuntime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	ids := make([]struct{ sessionID, id string }, 0, len(r.active))
	for _, active := range r.active {
		ids = append(ids, struct{ sessionID, id string }{active.run.SessionID, active.run.ID})
	}
	r.mu.Unlock()
	for _, item := range ids {
		r.Cancel(item.sessionID, item.id)
	}
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (r *subagentRuntime) handleFrame(id string, frame stream.Frame) {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.terminalizing {
		r.mu.Unlock()
		return
	}
	sessionID := active.run.SessionID
	childRunID := active.run.ChildRunID
	parentRunID := active.run.ParentRunID
	parentToolCallID := active.run.ParentToolCallID
	switch frame.Kind {
	case stream.FrameThinking:
		active.activity = compactActivity(frame.Thinking)
		appendAgentDelta(&active.blocks, "thinking", childRunID, "Thinking", frame.Thinking)
	case stream.FrameText:
		active.activity = compactActivity(frame.Text)
		appendAgentDelta(&active.blocks, "assistant", childRunID, "Assistant", frame.Text)
	case stream.FrameToolCall:
		if frame.ToolCall != nil {
			active.ToolStarted = true
			active.run.ToolCalls++
			active.toolNames[frame.ToolCall.Name] = struct{}{}
			active.activity = frame.ToolCall.Name
			active.blocks = append(active.blocks, AgentTranscriptBlock{
				ID: "call-" + frame.ToolCall.ID, Kind: "tool", RunID: childRunID, ToolCallID: frame.ToolCall.ID,
				Title: frame.ToolCall.Name, Content: string(frame.ToolCall.Arguments), State: "running",
			})
		}
	case stream.FrameToolResult:
		if frame.ToolResult != nil {
			finishAgentToolBlock(active.blocks, frame.ToolResult.ToolCallID, frame.ToolResult.Content, frame.ToolResult.IsError)
		}
	case stream.FrameDone:
		active.run.Turns++
		active.usage.InputTokens += frame.Usage.InputTokens
		active.usage.OutputTokens += frame.Usage.OutputTokens
		active.usage.TotalTokens += frame.Usage.TotalTokens
		active.run.TokensUsed = active.usage.TotalTokens
	}
	r.mu.Unlock()
	r.persistActivity(id)

	event := Event{
		SessionID: sessionID, RunID: childRunID, AgentID: id, State: "running",
		Data: childFrameData(frame.Source, parentToolCallID, nil),
	}
	switch frame.Kind {
	case stream.FrameThinking:
		event.Kind = EventThinkingDelta
		event.Text = frame.Thinking
	case stream.FrameText:
		event.Kind = EventTextDelta
		event.Text = frame.Text
	case stream.FrameToolCall:
		if frame.ToolCall == nil {
			return
		}
		event.Kind = EventToolStarted
		event.ToolCallID = frame.ToolCall.ID
		event.Data = childFrameData(frame.Source, parentToolCallID, map[string]string{
			"name": frame.ToolCall.Name, "arguments": string(frame.ToolCall.Arguments),
		})
	case stream.FrameToolResult:
		if frame.ToolResult == nil {
			return
		}
		event.Kind = EventToolFinished
		event.ToolCallID = frame.ToolResult.ToolCallID
		event.Text = frame.ToolResult.Content
		event.Data = childFrameData(frame.Source, parentToolCallID, map[string]string{"name": frame.ToolResult.Name})
		if len(frame.ToolResult.Structured) > 0 {
			event.Data["structured"] = string(frame.ToolResult.Structured)
		}
		if frame.ToolResult.IsError {
			event.State = "failed"
		} else {
			event.State = "completed"
		}
	case stream.FrameDone:
		event.Kind = EventContextUsage
		event.RunID = parentRunID
		event.AgentID = ""
		event.State = "reported"
		event.Data = map[string]string{
			"inputTokens": fmt.Sprint(frame.Usage.InputTokens), "cachedInputTokens": fmt.Sprint(frame.Usage.CachedInputTokens),
			"outputTokens": fmt.Sprint(frame.Usage.OutputTokens), "totalTokens": fmt.Sprint(frame.Usage.TotalTokens),
			"cacheStatus": "reported", "aggregateOnly": "true", "source": frame.Source,
		}
	default:
		return
	}
	if parent := r.parentHost(id); parent != nil {
		parent.emit(parent.ctx, event)
	}
}

func childFrameData(source, parentToolCallID string, values map[string]string) map[string]string {
	data := make(map[string]string, len(values)+2)
	for key, value := range values {
		data[key] = value
	}
	data["source"] = source
	data["parent_tool_call_id"] = parentToolCallID
	return data
}

func (r *subagentRuntime) handleToolUpdate(id string, update tool.Update) {
	r.mu.Lock()
	active := r.active[id]
	if active != nil && !active.terminalizing {
		active.activity = compactActivity(firstNonempty(update.Message, update.Kind))
		for index := len(active.blocks) - 1; index >= 0; index-- {
			if active.blocks[index].Kind == "tool" && active.blocks[index].State == "running" {
				appendAgentBlockContent(&active.blocks[index], update.Message)
				break
			}
		}
	}
	r.mu.Unlock()
	r.persistActivity(id)
}

func (r *subagentRuntime) parentHost(id string) *Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	if active := r.active[id]; active != nil {
		return active.parent.Host
	}
	return nil
}

func (r *subagentRuntime) persistActivity(id string) {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.parent.Host == nil || active.parent.Host.sessions == nil || active.activity == "" ||
		active.activity == active.persistedActivity || time.Since(active.lastActivityPersist) < 500*time.Millisecond {
		r.mu.Unlock()
		return
	}
	active.persistedActivity = active.activity
	active.lastActivityPersist = time.Now()
	run := cloneSubagentRun(active.run)
	activity := active.activity
	parent := active.parent.Host
	r.mu.Unlock()
	_ = parent.sessions.UpsertAgentBlock(parent.ctx, run.SessionID, run.ID, session.Block{
		Kind: "agent", RunID: run.ParentRunID, AgentID: run.ID, ParentToolCallID: run.ParentToolCallID,
		Title: run.Type, Content: activity, State: string(run.State),
	})
}

func (r *subagentRuntime) emitState(run agentservice.SubagentRun, activity string) {
	r.mu.Lock()
	active := r.active[run.ID]
	if active != nil && active.activity != "" {
		activity = active.activity
	}
	parent := (*Service)(nil)
	if active != nil {
		parent = active.parent.Host
		active.persistedActivity = activity
		active.lastActivityPersist = time.Now()
	}
	r.mu.Unlock()
	r.emitStateTo(parent, run, activity)
}

func (r *subagentRuntime) emitStateTo(parent *Service, run agentservice.SubagentRun, activity string) {
	if parent == nil {
		return
	}
	if parent.sessions != nil {
		content := firstNonempty(activity, run.Summary, run.Description)
		_ = parent.sessions.UpsertAgentBlock(parent.ctx, run.SessionID, run.ID, session.Block{
			Kind: "agent", RunID: run.ParentRunID, AgentID: run.ID, ParentToolCallID: run.ParentToolCallID,
			Title: run.Type, Content: content, State: string(run.State),
		})
	}
	parent.emit(parent.ctx, subagentStateEvent(run, activity))
}

func subagentStateEvent(run agentservice.SubagentRun, activity string) Event {
	elapsed := snapshotFromRun(run).Elapsed
	return Event{
		Kind: EventAgentState, SessionID: run.SessionID, RunID: run.ParentRunID, AgentID: run.ID,
		State: string(run.State), Text: run.Summary,
		Agent: &AgentStatePayload{
			Type: run.Type, Description: run.Description, Model: run.Model, Background: run.Background,
			CapabilityMode: run.CapabilityMode, RequestedIsolation: run.RequestedIsolation, Isolation: run.Isolation,
			CWD: run.CWD, ParentRunID: run.ParentRunID, ParentToolCallID: run.ParentToolCallID,
			ChildRunID: run.ChildRunID, Activity: activity, Warning: run.Warning, WorktreePath: run.WorktreePath,
			ToolCalls: run.ToolCalls, Turns: run.Turns, TokensUsed: run.TokensUsed, ElapsedMS: elapsed.Milliseconds(),
		},
		Data: map[string]string{"id": run.ID, "role": run.Type, "state": string(run.State), "summary": run.Summary},
	}
}

func (r *subagentRuntime) signalChangedLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *subagentRuntime) parentDone(id string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if active := r.active[id]; active != nil {
		return active.done
	}
	closed := make(chan struct{})
	close(closed)
	return closed
}

type subagentTurnContext struct {
	instructions   string
	privateContext string
	seed           []message.Message
	compactHooks   func(context.Context, []message.Message, []message.Message, error) error
}

func (c subagentTurnContext) Build(_ context.Context, task api.Task) ([]message.Message, error) {
	messages := make([]message.Message, 0, len(c.seed)+2)
	if instructions := strings.TrimSpace(c.instructions); instructions != "" {
		messages = append(messages, message.NewText(message.RoleSystem, instructions))
	}
	if privateContext := strings.TrimSpace(c.privateContext); privateContext != "" {
		value := message.NewText(message.RoleSystem, "[Trusted SubagentStart hook context]\n"+privateContext)
		value.Visibility = message.VisibilityPrivate
		messages = append(messages, value)
	}
	for _, seeded := range c.seed {
		if seeded.Role != message.RoleSystem {
			messages = append(messages, seeded)
		}
	}
	if goal := strings.TrimSpace(task.Goal); goal != "" {
		messages = append(messages, message.NewText(message.RoleUser, goal))
	}
	return messages, nil
}

func (c subagentTurnContext) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	if c.compactHooks != nil {
		if err := c.compactHooks(ctx, history, nil, nil); err != nil {
			return history, err
		}
	}
	const recentMessages = 20
	prefixEnd := 0
	for prefixEnd < len(history) && history[prefixEnd].Role == message.RoleSystem {
		prefixEnd++
	}
	if len(history) <= recentMessages+prefixEnd {
		if c.compactHooks != nil {
			_ = c.compactHooks(ctx, history, history, nil)
		}
		return history, nil
	}
	start := len(history) - recentMessages
	if start < prefixEnd {
		start = prefixEnd
	}
	for start > prefixEnd && history[start].Role != message.RoleUser {
		start--
	}
	compacted := make([]message.Message, 0, len(history)-start+prefixEnd)
	compacted = append(compacted, history[:prefixEnd]...)
	compacted = append(compacted, history[start:]...)
	if c.compactHooks != nil {
		_ = c.compactHooks(ctx, history, compacted, nil)
	}
	return compacted, nil
}

func effectiveSubagentTools(roleTools []string, capability string) map[string]bool {
	readOnly := map[string]bool{"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true}
	modes := map[string]map[string]bool{
		"read-only":  readOnly,
		"read-write": {"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true, "coding.edit_hashline": true, "coding.write_file": true, "coding.gofmt": true},
		"execute":    {"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true, "coding.go_test": true, "coding.shell": true},
		"all":        {"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true, "coding.edit_hashline": true, "coding.write_file": true, "coding.gofmt": true, "coding.go_test": true, "coding.shell": true},
	}
	allowed := make(map[string]bool)
	for _, name := range roleTools {
		if modes[capability][name] {
			allowed[name] = true
		}
	}
	return allowed
}

func renderSubagentInstructions(profile effectiveSubagentProfile) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "You are the %s subagent.", profile.Type)
	if profile.Persona != "" {
		fmt.Fprintf(&rendered, " Apply the %s persona.", profile.Persona)
	}
	fmt.Fprintf(&rendered, " Work only within %s.\n", profile.CWD)
	if profile.Instructions != "" {
		rendered.WriteString(profile.Instructions)
		rendered.WriteByte('\n')
	}
	writeContract := func(title string, items []config.SubagentContractItem) {
		if len(items) == 0 {
			return
		}
		rendered.WriteString(title)
		rendered.WriteString(" contract:\n")
		for _, item := range items {
			requirement := "optional"
			if item.Required {
				requirement = "required"
			}
			fmt.Fprintf(&rendered, "- %s (%s, %s)", item.Name, item.Type, requirement)
			if item.Description != "" {
				fmt.Fprintf(&rendered, ": %s", item.Description)
			}
			rendered.WriteByte('\n')
		}
	}
	writeContract("Input", profile.Inputs)
	writeContract("Output", profile.Outputs)
	rendered.WriteString("Return a direct final answer with concrete evidence.")
	return strings.TrimSpace(rendered.String())
}

func transcriptToAgentBlocks(encoded json.RawMessage) ([]AgentTranscriptBlock, error) {
	if len(encoded) == 0 {
		return nil, nil
	}
	var messages []message.Message
	if err := json.Unmarshal(encoded, &messages); err != nil {
		return nil, fmt.Errorf("decode subagent transcript: %w", err)
	}
	blocks := make([]AgentTranscriptBlock, 0, len(messages))
	callIndex := make(map[string]int)
	for index, item := range messages {
		if item.Role == message.RoleSystem {
			continue
		}
		if item.Role == message.RoleUser && strings.TrimSpace(item.Text) != "" {
			blocks = append(blocks, AgentTranscriptBlock{ID: fmt.Sprintf("msg-%d-user", index), Kind: "user", RunID: item.RunID, Content: item.Text, State: "completed"})
		}
		if item.Role == message.RoleAssistant {
			if item.Thinking != "" {
				blocks = append(blocks, AgentTranscriptBlock{ID: fmt.Sprintf("msg-%d-thinking", index), Kind: "thinking", RunID: item.RunID, Content: item.Thinking, State: "completed"})
			}
			if item.Text != "" {
				blocks = append(blocks, AgentTranscriptBlock{ID: fmt.Sprintf("msg-%d-text", index), Kind: "assistant", RunID: item.RunID, Content: item.Text, State: "completed"})
			}
			for _, call := range item.ToolCalls {
				callIndex[call.ID] = len(blocks)
				blocks = append(blocks, AgentTranscriptBlock{ID: "call-" + call.ID, Kind: "tool", RunID: item.RunID, ToolCallID: call.ID, Title: call.Name, Content: string(call.Arguments), State: "running"})
			}
		}
		if item.ToolResult != nil {
			if blockIndex, ok := callIndex[item.ToolResult.ToolCallID]; ok {
				appendAgentBlockContent(&blocks[blockIndex], item.ToolResult.Content)
				if item.ToolResult.IsError {
					blocks[blockIndex].State = "failed"
				} else {
					blocks[blockIndex].State = "completed"
				}
				delete(callIndex, item.ToolResult.ToolCallID)
			} else {
				blocks = append(blocks, AgentTranscriptBlock{
					ID: fmt.Sprintf("result-%d", index), Kind: "tool", RunID: item.RunID,
					ToolCallID: item.ToolResult.ToolCallID, Title: item.ToolResult.Name,
					Content: item.ToolResult.Content, State: "failed",
				})
			}
		}
	}
	for _, index := range callIndex {
		blocks[index].State = "failed"
		appendAgentBlockContent(&blocks[index], "missing tool result")
	}
	return blocks, nil
}

func appendAgentDelta(blocks *[]AgentTranscriptBlock, kind, runID, title, content string) {
	if content == "" {
		return
	}
	if len(*blocks) > 0 {
		last := &(*blocks)[len(*blocks)-1]
		if last.Kind == kind && last.RunID == runID {
			last.Content += content
			return
		}
	}
	*blocks = append(*blocks, AgentTranscriptBlock{ID: fmt.Sprintf("live-%s-%d", kind, len(*blocks)), Kind: kind, RunID: runID, Title: title, Content: content, State: "streaming"})
}

func finishAgentToolBlock(blocks []AgentTranscriptBlock, callID, content string, failed bool) {
	for index := len(blocks) - 1; index >= 0; index-- {
		if blocks[index].Kind == "tool" && blocks[index].ToolCallID == callID {
			appendAgentBlockContent(&blocks[index], content)
			if failed {
				blocks[index].State = "failed"
			} else {
				blocks[index].State = "completed"
			}
			return
		}
	}
}

func appendAgentBlockContent(block *AgentTranscriptBlock, content string) {
	if content == "" {
		return
	}
	if block.Content != "" && !strings.HasSuffix(block.Content, "\n") {
		block.Content += "\n"
	}
	block.Content += content
}

func snapshotFromRun(run agentservice.SubagentRun) agentservice.SubagentSnapshot {
	elapsed := time.Duration(0)
	if !run.StartedAt.IsZero() {
		end := run.FinishedAt
		if end.IsZero() {
			end = time.Now().UTC()
		}
		elapsed = max(0, end.Sub(run.StartedAt))
	}
	return agentservice.SubagentSnapshot{Run: cloneSubagentRun(run), Elapsed: elapsed, Found: true}
}

func cloneSubagentRun(run agentservice.SubagentRun) agentservice.SubagentRun {
	run.Transcript = append(json.RawMessage(nil), run.Transcript...)
	run.ToolsUsed = append([]string(nil), run.ToolsUsed...)
	return run
}

func sortedToolSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func subagentTerminal(state agentservice.SubagentState) bool {
	switch state {
	case agentservice.SubagentCompleted, agentservice.SubagentFailed, agentservice.SubagentCancelled, agentservice.SubagentInterrupted:
		return true
	default:
		return false
	}
}

func subagentSummary(run agentservice.SubagentRun) string {
	value := firstNonempty(run.Output, run.Error, string(run.State))
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 240 {
		value = string(runes[:240])
	}
	return value
}

func appendWarning(current, warning string) string {
	if current == "" {
		return warning
	}
	if warning == "" || strings.Contains(current, warning) {
		return current
	}
	return current + "; " + warning
}

func compactActivity(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 120 {
		return string(runes[:120])
	}
	return value
}

func newSubagentID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate subagent ID: %w", err)
	}
	return "subagent_" + hex.EncodeToString(value), nil
}

func providerHasModel(driver hyprovider.Driver, model string) bool {
	for _, candidate := range driver.Metadata().Models {
		if candidate == model {
			return true
		}
	}
	return false
}

func sortedRoleNames(roles map[string]config.SubagentRoleConfig, toggles map[string]bool) []string {
	names := make([]string, 0, len(roles))
	for name := range roles {
		if enabled, configured := toggles[name]; !configured || enabled {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func resolveSubagentCWD(workspaceRoot, requested string) (string, error) {
	requested = strings.Trim(strings.TrimSpace(requested), "`\"")
	if requested == "" {
		return "", fmt.Errorf("cwd is empty")
	}
	if requested == "~" || strings.HasPrefix(requested, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve cwd home: %w", err)
		}
		requested = filepath.Join(home, strings.TrimPrefix(requested, "~/"))
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(workspaceRoot, requested)
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	resolved, err := filepath.Abs(filepath.Clean(requested))
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve cwd symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", resolved)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("cwd %q escapes parent workspace %q", resolved, root)
	}
	return resolved, nil
}

// Driver protocol.

type subagentSpawnInput struct {
	Prompt            string
	Description       string
	TodoItemID        string
	SubagentType      string
	SubagentTypeSet   bool
	Background        bool
	BackgroundSet     bool
	CapabilityMode    string
	CapabilityModeSet bool
	Isolation         string
	IsolationSet      bool
	ResumeFrom        string
	CWD               string
	CWDSet            bool
	Model             string
	ModelSet          bool
	parentToolCallID  string
}

type subagentSpawnDriver struct {
	runtime *subagentRuntime
	parent  subagentParentRuntime
}

func (d *subagentSpawnDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: subagentSpawnTool, Description: "Spawn one supervised subagent task. Returns a durable task ID for background work.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"prompt", "description"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"prompt": {Type: "string"}, "description": {Type: "string"}, "subagent_type": {Type: "string"},
				"todo_item_id": {Type: "string"},
				"background":   {Type: "boolean"}, "capability_mode": {Type: "string", Enum: []string{"read-only", "read-write", "execute", "all"}},
				"isolation": {Type: "string", Enum: []string{"none", "worktree"}}, "resume_from": {Type: "string"},
				"cwd": {Type: "string"}, "model": {Type: "string"},
			},
		},
		EffectType: tool.EffectReadOnly, PolicyTags: []string{"subagent", "spawn"},
	}
}

func (d *subagentSpawnDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	input, err := decodeSubagentSpawnInput(call.Arguments)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	input.parentToolCallID = call.ID
	todoRevision, err := prepareSubagentTodoBinding(ctx, d.parent, input.TodoItemID)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	var beforeEnqueue func(agentservice.SubagentRun) error
	if input.TodoItemID != "" {
		beforeEnqueue = func(run agentservice.SubagentRun) error {
			return commitSubagentTodoBinding(ctx, d.parent, input.TodoItemID, run.ID, todoRevision)
		}
	}
	run, err := d.runtime.spawn(input, d.parent, beforeEnqueue)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	if input.Background {
		return subagentJSONResult(call, map[string]any{"task_id": run.ID, "status": string(run.State), "description": run.Description, "type": run.Type, "warning": run.Warning}), nil
	}
	snapshot := d.runtime.waitForForegroundStart(ctx, run.SessionID, run.ID)
	done := d.runtime.parentDone(run.ID)
	if snapshot.Found && subagentTerminal(snapshot.Run.State) {
		_ = d.runtime.store.SetCompletionDelivered(d.runtime.ctx, run.ID, true)
		return subagentJSONResult(call, foregroundSubagentResult(snapshot)), nil
	}
	timer := time.NewTimer(d.runtime.cfg.AwaitDuration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		warning := "foreground wait interrupted; task continues in background"
		d.runtime.demote(run.SessionID, run.ID, warning)
		snapshot := d.runtime.snapshot(run.ID, run.SessionID)
		return subagentJSONResult(call, map[string]any{
			"task_id": run.ID, "status": string(snapshot.Run.State), "description": run.Description, "type": run.Type,
			"warning": snapshot.Run.Warning,
		}), nil
	case <-timer.C:
		warning := fmt.Sprintf("foreground wait timed out after %s; task continues in background", d.runtime.cfg.AwaitDuration)
		d.runtime.demote(run.SessionID, run.ID, warning)
		snapshot := d.runtime.snapshot(run.ID, run.SessionID)
		return subagentJSONResult(call, map[string]any{
			"task_id": run.ID, "status": string(snapshot.Run.State), "description": run.Description, "type": run.Type,
			"warning": snapshot.Run.Warning,
		}), nil
	case <-done:
	}
	snapshot = d.runtime.snapshot(run.ID, run.SessionID)
	if snapshot.Found && subagentTerminal(snapshot.Run.State) {
		_ = d.runtime.store.SetCompletionDelivered(d.runtime.ctx, run.ID, true)
	}
	return subagentJSONResult(call, foregroundSubagentResult(snapshot)), nil
}

func prepareSubagentTodoBinding(ctx context.Context, parent subagentParentRuntime, itemID string) (int64, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return 0, nil
	}
	if parent.Host == nil || parent.Host.sessions == nil {
		return 0, fmt.Errorf("todo store is unavailable")
	}
	todo, err := parent.Host.sessions.LoadTodo(ctx, parent.SessionID)
	if err != nil {
		return 0, err
	}
	for _, phase := range todo.Phases {
		for _, item := range phase.Items {
			if item.ID != itemID {
				continue
			}
			if item.Status != session.TodoPending && item.Status != session.TodoInProgress {
				return 0, fmt.Errorf("todo item %q is closed", itemID)
			}
			if item.SubagentRunID != "" {
				return 0, fmt.Errorf("todo item %q is already assigned", itemID)
			}
			return todo.Revision, nil
		}
	}
	return 0, fmt.Errorf("todo item %q not found", itemID)
}

func commitSubagentTodoBinding(ctx context.Context, parent subagentParentRuntime, itemID, runID string, expectedRevision int64) error {
	updated, err := parent.Host.sessions.UpdateTodo(ctx, parent.SessionID, expectedRevision, func(todo *session.TodoList) error {
		for pi := range todo.Phases {
			for ii := range todo.Phases[pi].Items {
				item := &todo.Phases[pi].Items[ii]
				if item.ID == itemID {
					if item.SubagentRunID != "" {
						return fmt.Errorf("todo item %q is already assigned", itemID)
					}
					item.SubagentRunID = runID
					return nil
				}
			}
		}
		return fmt.Errorf("todo item %q not found", itemID)
	})
	if err != nil {
		return err
	}
	snapshot := updated.Clone()
	parent.Host.emitTodoUpdated(parent.SessionID, snapshot)
	return nil
}

func (r *subagentRuntime) demote(sessionID, id, warning string) {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.run.SessionID != sessionID || active.run.Background || active.terminalizing {
		r.mu.Unlock()
		return
	}
	active.run.Background = true
	active.run.Warning = appendWarning(active.run.Warning, warning)
	run := cloneSubagentRun(active.run)
	r.mu.Unlock()
	if r.store.Save(r.ctx, run) != nil {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: errors.New("persist foreground demotion")})
		return
	}
	r.mu.Lock()
	if active = r.active[id]; active != nil && !active.terminalizing {
		active.run = run
	}
	r.mu.Unlock()
	r.emitState(run, "running in background")
}

type subagentGetOutputDriver struct {
	runtime   *subagentRuntime
	sessionID string
}

func (d *subagentGetOutputDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: subagentGetOutputTool, Description: "Get ordered snapshots for one or more supervised subagent task IDs, optionally waiting for terminal states.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"task_ids"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
			"task_ids": {Type: "array", Items: &tool.Schema{Type: "string"}}, "timeout_ms": {Type: "integer"},
		}}, EffectType: tool.EffectReadOnly, PolicyTags: []string{"subagent", "query"},
	}
}

func (d *subagentGetOutputDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	ids, timeout, err := decodeSubagentQueryInput(call.Arguments)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	snapshots := d.runtime.Query(ctx, d.sessionID, ids, timeout)
	tasks := make([]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		tasks = append(tasks, subagentSnapshotJSON(snapshot))
		if snapshot.Found && subagentTerminal(snapshot.Run.State) {
			_ = d.runtime.store.SetCompletionDelivered(d.runtime.ctx, snapshot.Run.ID, true)
		}
	}
	return subagentJSONResult(call, map[string]any{"tasks": tasks}), nil
}

type subagentKillDriver struct {
	runtime   *subagentRuntime
	sessionID string
}

func (d *subagentKillDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: subagentKillTool, Description: "Request cancellation of one supervised subagent task without cancelling the parent run.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"task_id"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"task_id": {Type: "string"}}},
		EffectType:  tool.EffectReadOnly, PolicyTags: []string{"subagent", "cancel"},
	}
}

func (d *subagentKillDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return subagentToolError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		return subagentToolError(call, fmt.Errorf("task_id is required")), nil
	}
	outcome := d.runtime.Cancel(d.sessionID, input.TaskID)
	status := "not_found"
	if outcome.Snapshot.Found {
		status = string(outcome.Snapshot.Run.State)
	}
	return subagentJSONResult(call, map[string]any{"task_id": input.TaskID, "outcome": outcome.Outcome, "status": status}), nil
}

func decodeSubagentSpawnInput(arguments json.RawMessage) (subagentSpawnInput, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &raw); err != nil {
		return subagentSpawnInput{}, fmt.Errorf("decode arguments: %w", err)
	}
	prompt, err := requiredRawString(raw, "prompt")
	if err != nil {
		return subagentSpawnInput{}, err
	}
	description, err := requiredRawString(raw, "description")
	if err != nil {
		return subagentSpawnInput{}, err
	}
	input := subagentSpawnInput{Prompt: prompt, Description: description}
	decodeString := func(key string, target *string, set *bool) error {
		value, present, decodeErr := optionalRawString(raw, key)
		if decodeErr != nil {
			return decodeErr
		}
		if present {
			*target = value
			if set != nil {
				*set = true
			}
		}
		return nil
	}
	for _, item := range []struct {
		key    string
		target *string
		set    *bool
	}{
		{key: "subagent_type", target: &input.SubagentType, set: &input.SubagentTypeSet},
		{key: "todo_item_id", target: &input.TodoItemID},
		{key: "capability_mode", target: &input.CapabilityMode, set: &input.CapabilityModeSet},
		{key: "isolation", target: &input.Isolation, set: &input.IsolationSet},
		{key: "resume_from", target: &input.ResumeFrom},
		{key: "cwd", target: &input.CWD, set: &input.CWDSet},
		{key: "model", target: &input.Model, set: &input.ModelSet},
	} {
		if err := decodeString(item.key, item.target, item.set); err != nil {
			return subagentSpawnInput{}, err
		}
	}
	if encoded, present := raw["background"]; present && string(encoded) != "null" {
		if err := json.Unmarshal(encoded, &input.Background); err != nil {
			return subagentSpawnInput{}, fmt.Errorf("background must be a boolean")
		}
		input.BackgroundSet = true
	}
	if !input.BackgroundSet {
		input.Background = true
	}
	if input.ResumeFrom == "" {
		if !input.SubagentTypeSet {
			input.SubagentType = "general-purpose"
		}
		if !input.IsolationSet {
			input.Isolation = "none"
		}
		switch input.CapabilityMode {
		case "", "read-only", "read-write", "execute", "all":
		default:
			return subagentSpawnInput{}, fmt.Errorf("capability_mode is invalid")
		}
		if input.Isolation != "none" && input.Isolation != "worktree" {
			return subagentSpawnInput{}, fmt.Errorf("isolation must be none or worktree")
		}
		if input.CWDSet && input.Isolation == "worktree" {
			return subagentSpawnInput{}, fmt.Errorf("cwd and isolation=worktree are mutually exclusive")
		}
	}
	return input, nil
}

func decodeSubagentQueryInput(arguments json.RawMessage) ([]string, time.Duration, error) {
	var input struct {
		TaskIDs   []string `json:"task_ids"`
		TimeoutMS int      `json:"timeout_ms"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return nil, 0, fmt.Errorf("decode arguments: %w", err)
	}
	if input.TimeoutMS < 0 || input.TimeoutMS > 600_000 {
		return nil, 0, fmt.Errorf("timeout_ms must be between 0 and 600000")
	}
	seen := make(map[string]bool)
	ids := make([]string, 0, len(input.TaskIDs))
	for _, id := range input.TaskIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) < 1 || len(ids) > 20 {
		return nil, 0, fmt.Errorf("task_ids must contain 1 to 20 unique non-empty IDs")
	}
	return ids, time.Duration(input.TimeoutMS) * time.Millisecond, nil
}

func requiredRawString(raw map[string]json.RawMessage, key string) (string, error) {
	value, present, err := optionalRawString(raw, key)
	if err != nil {
		return "", err
	}
	if !present || value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func optionalRawString(raw map[string]json.RawMessage, key string) (string, bool, error) {
	encoded, present := raw[key]
	if !present || string(encoded) == "null" {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		return "", false, fmt.Errorf("%s must be a string", key)
	}
	value = strings.Trim(strings.TrimSpace(value), "`\"")
	switch strings.ToLower(value) {
	case "", "null", "none", "undefined":
		return "", false, nil
	default:
		return value, true, nil
	}
}

func foregroundSubagentResult(snapshot agentservice.SubagentSnapshot) map[string]any {
	if !snapshot.Found {
		return map[string]any{"task_id": snapshot.Run.ID, "status": "not_found"}
	}
	run := snapshot.Run
	return map[string]any{
		"task_id": run.ID, "status": string(run.State), "output": run.Output, "error": run.Error, "warning": run.Warning,
		"usage": map[string]any{"tool_calls": run.ToolCalls, "turns": run.Turns, "tokens_used": run.TokensUsed},
	}
}

func subagentSnapshotJSON(snapshot agentservice.SubagentSnapshot) map[string]any {
	if !snapshot.Found {
		return map[string]any{"task_id": snapshot.Run.ID, "status": "not_found"}
	}
	run := snapshot.Run
	return map[string]any{
		"task_id": run.ID, "status": string(run.State), "description": run.Description, "type": run.Type,
		"model": run.Model, "background": run.Background, "capability_mode": run.CapabilityMode,
		"requested_isolation": run.RequestedIsolation, "isolation": run.Isolation, "cwd": run.CWD,
		"elapsed_ms": snapshot.Elapsed.Milliseconds(), "tool_calls": run.ToolCalls, "turns": run.Turns,
		"tokens_used": run.TokensUsed, "tools_used": run.ToolsUsed, "output": run.Output, "error": run.Error,
		"warning": run.Warning, "worktree_path": run.WorktreePath,
	}
}

func subagentJSONResult(call tool.Call, value any) tool.Result {
	encoded, err := json.Marshal(value)
	if err != nil {
		return subagentToolError(call, fmt.Errorf("encode subagent result: %w", err))
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: string(encoded)}
}

func subagentToolError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
}
