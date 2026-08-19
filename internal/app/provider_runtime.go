package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/adapterdeployment"
	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/provider/errcode"
	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	hyskill "github.com/Viking602/venat/skill"
	"github.com/Viking602/venat/tool"
	hyworker "github.com/Viking602/venat/worker"
)

type ProviderRuntime struct {
	cfg                   config.Config
	auth                  *auth.Service
	catalog               *catalog.Service
	coding                *agentservice.Service
	subagentWorktreeRoot  string
	approvalReviewTimeout time.Duration
	ChatGPTEndpoint       string
	GrokEndpoint          string
	adapters              *adapterdeployment.Registry

	mu              sync.RWMutex
	host            providerHost
	mcp             *mcpruntime.Manager
	subagents       *subagentRuntime
	subagentInitErr error
}

type editRecoveryRequirement interface {
	RequiredEditReadTarget() (string, bool)
}

type editRecoveryHook struct {
	run editRecoveryRequirement
}

func (h editRecoveryHook) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	return messages, nil
}

func (h editRecoveryHook) BeforeModelCall(_ context.Context, request *hyprovider.Request) error {
	target, required := h.run.RequiredEditReadTarget()
	if !required {
		return nil
	}
	readTools := make([]message.ToolDefinition, 0, 1)
	for _, definition := range request.Tools {
		if definition.Name == coding.ToolReadFile {
			readTools = append(readTools, definition)
		}
	}
	if len(readTools) == 0 {
		return fmt.Errorf("edit recovery for %q requires unavailable tool %s", target, coding.ToolReadFile)
	}
	request.Tools = readTools
	return nil
}

func (editRecoveryHook) BeforeToolCall(context.Context, *tool.Call) error  { return nil }
func (editRecoveryHook) AfterToolCall(context.Context, *tool.Result) error { return nil }
func (editRecoveryHook) OnEvent(context.Context, hyprovider.Event) error   { return nil }

var (
	errResumeProfileChanged  = errors.New("resume run execution profile changed")
	errResumeBudgetExhausted = errors.New("resume run budget exhausted")
)

const teamWorkspaceAnchorMetadata = "workspace_anchor"

type singleRunManifest struct {
	Version          int       `json:"version"`
	Provider         string    `json:"provider"`
	AccountID        string    `json:"account_id"`
	Model            string    `json:"model"`
	Reasoning        string    `json:"reasoning"`
	ActiveSkills     []string  `json:"active_skills"`
	PlanMode         bool      `json:"plan_mode,omitempty"`
	ApprovedPlanID   string    `json:"approved_plan_id,omitempty"`
	DisableSubagents bool      `json:"disable_subagents"`
	StaticIdentity   string    `json:"static_identity"`
	MaxTokens        int64     `json:"max_tokens"`
	MaxToolCalls     int       `json:"max_tool_calls"`
	MaxWallClockNS   int64     `json:"max_wall_clock_ns"`
	StartedAt        time.Time `json:"started_at"`
}

type liveApproval struct {
	approvalID  string
	agentID     string
	agentType   string
	run         *agentservice.Run
	callID      string
	sessionID   string
	runID       string
	fingerprint string
	request     approvalReviewRequest
	decision    chan agentservice.ApprovalMode
	resolving   bool
	resolved    bool
}

func NewProviderRuntime(cfg config.Config, authentication *auth.Service, modelCatalog *catalog.Service, codingService *agentservice.Service, subagentWorktreeRoot string) (*ProviderRuntime, error) {
	if authentication == nil || modelCatalog == nil || codingService == nil {
		return nil, fmt.Errorf("provider runtime dependencies are incomplete")
	}
	if strings.TrimSpace(subagentWorktreeRoot) == "" {
		return nil, fmt.Errorf("subagent worktree root is empty")
	}
	cfg.Agents.Subagents = cloneSubagentConfig(cfg.Agents.Subagents)
	cfg.Providers.LLMux = cloneLLMuxProviders(cfg.Providers.LLMux)
	return &ProviderRuntime{
		cfg: cfg, auth: authentication, catalog: modelCatalog, coding: codingService,
		subagentWorktreeRoot: subagentWorktreeRoot,
	}, nil
}

func (r *ProviderRuntime) Attach(host providerHost, manager *mcpruntime.Manager, subagentStore agentservice.SubagentRunStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.host = host
	r.mcp = manager
	if r.subagents == nil && r.subagentInitErr == nil && host != nil && subagentStore != nil {
		r.subagents, r.subagentInitErr = newSubagentRuntime(host.BaseContext(), r.cfg.Agents.Subagents, subagentStore, r.subagentWorktreeRoot)
	}
	if r.subagents != nil {
		r.subagents.setHost(host)
	}
}

func (r *ProviderRuntime) Start(ctx context.Context, request TurnRequest) (*agentservice.Run, hyagent.Engine, error) {
	account, modelID, contextWindow, driver, err := r.resolveDriver(ctx, request.Provider, request.Model, request.Reasoning)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	request.Reasoning, err = r.resolvedReasoningEffort(ctx, request.Provider, account.ID, modelID, request.Reasoning)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	executionPolicy := agentservice.RunExecutionPolicy{
		AgentVersion: "runtime",
		Governance:   singleRunGovernance(r.cfg, request),
		Budget: &api.TaskBudget{
			MaxTokens: r.cfg.Agents.Main.MaxTokens, MaxWallClock: r.cfg.Agents.Main.MaxWallClockDuration,
			MaxToolCalls: r.cfg.Agents.Main.MaxToolCalls,
		},
	}
	executionPolicy.ResourceClaims, err = topLevelWorkspaceWriteClaims(r.cfg.Workspace.AllowWrite, r.cfg.Workspace.ShellPolicy, r.cfg.Workspace.Root)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	if r.cfg.Retry.Enabled {
		executionPolicy.RetryPolicy = api.RetryPolicy{
			MaxAttempts: r.cfg.Retry.MaxRetries + 1,
			Backoff:     r.cfg.Retry.BaseDelayDuration,
			MaxBackoff:  r.cfg.Retry.MaxDelayDuration,
		}
	}
	run, err := r.coding.StartRunWithMetadata(ctx, request.Prompt, map[string]string{"session_id": request.SessionID}, executionPolicy)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	r.mu.RLock()
	host := r.host
	r.mu.RUnlock()
	if host != nil && host.Sessions() != nil {
		if _, appendErr := host.Sessions().AppendBlock(ctx, request.SessionID, userTurnBlock(run.RunID, request)); appendErr != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, appendErr.Error(), appendErr)
			return nil, hyagent.Engine{}, fmt.Errorf("persist user turn: %w", appendErr)
		}
	}
	request, err = r.prepareVisionAssistance(ctx, request, run.RunID, account.ID, modelID)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	durable, err := r.coding.Runner().Run(ctx, run.RunID)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	if durable.Metadata == nil {
		durable.Metadata = map[string]string{}
	}
	durable.Metadata["session_id"] = request.SessionID
	if err := r.coding.Runner().SaveRun(ctx, durable); err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	return r.buildSingleRun(ctx, request, run, account.ID, modelID, contextWindow, driver)
}

func (r *ProviderRuntime) buildSingleRun(ctx context.Context, request TurnRequest, run *agentservice.Run, accountID, modelID string, contextWindow int, driver hyprovider.Driver) (*agentservice.Run, hyagent.Engine, error) {
	providerDriver := driver
	maxTokens := r.cfg.Agents.Main.MaxTokens
	maxToolCalls := r.cfg.Agents.Main.MaxToolCalls
	maxWallClock := r.cfg.Agents.Main.MaxWallClockDuration
	if request.budgetRestored {
		maxTokens, maxToolCalls, maxWallClock = request.maxTokens, request.maxToolCalls, request.maxWallClock
		if maxToolCalls > 0 {
			maxToolCalls -= request.usedToolCalls
			if maxToolCalls <= 0 {
				return nil, hyagent.Engine{}, fmt.Errorf("%w: max tool calls reached", errResumeBudgetExhausted)
			}
		}
		if maxWallClock > 0 {
			maxWallClock -= time.Since(request.startedAt)
			if maxWallClock <= 0 {
				return nil, hyagent.Engine{}, fmt.Errorf("%w: max wall clock reached", errResumeBudgetExhausted)
			}
		}
	}
	usageBudget := &providerUsageBudget{maxTokens: maxTokens, used: request.usedTokens}
	if maxTokens > 0 && usageBudget.used >= maxTokens {
		return nil, hyagent.Engine{}, fmt.Errorf("%w: max tokens reached", errResumeBudgetExhausted)
	}
	driver = &budgetedProviderDriver{inner: driver, budget: usageBudget}
	parentBudget, err := calculateContextBudget(modelID, contextWindow, 0, r.cfg.Agents.Context)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	contextTarget := parentBudget.Trigger
	r.mu.RLock()
	host := r.host
	manager := r.mcp
	subagents := r.subagents
	subagentInitErr := r.subagentInitErr
	r.mu.RUnlock()
	observeProviderRetries(ctx, host, request.SessionID, run.RunID, request.Provider, providerDriver)
	if subagentInitErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, subagentInitErr.Error(), subagentInitErr)
		return nil, hyagent.Engine{}, subagentInitErr
	}

	workspaceDrivers, err := r.coding.WorkspaceDrivers(ctx, r.cfg.Workspace.Root)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	drivers := make([]tool.Driver, 0, len(workspaceDrivers)+9)
	toolNames := make([]string, 0, len(workspaceDrivers)+8)
	for _, workspaceDriver := range workspaceDrivers {
		definition := workspaceDriver.Definition()
		governed := &governedAgentTool{definition: definition, driver: workspaceDriver, coding: r.coding, run: run, host: host, sessionID: request.SessionID}
		metadata := hooks.Metadata{SessionID: request.SessionID, RunID: run.RunID, AgentID: "main", AgentType: "main", CWD: r.cfg.Workspace.Root}
		drivers = append(drivers, wrapHookDriver(host, metadata, governed))
		toolNames = append(toolNames, definition.Name)
	}
	if host != nil && host.Sessions() != nil {
		drivers = append(drivers, wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), &todoDriver{sessionID: request.SessionID, store: host.Sessions(), emit: func(event Event) bool {
			return host.EmitTodoUpdated(request.SessionID, *event.Todo)
		}}))
		toolNames = append(toolNames, "todo")
		drivers = append(drivers, wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), &contextArtifactDriver{sessionID: request.SessionID, store: host.Sessions()}))
		toolNames = append(toolNames, contextReadArtifactTool)
		if request.PlanMode {
			drivers = append(drivers,
				&askDriver{sessionID: request.SessionID, runID: run.RunID, host: host},
				&submitPlanDriver{sessionID: request.SessionID, runID: run.RunID, host: host},
			)
			toolNames = append(toolNames, askToolName, submitPlanToolName)
		}
	}
	if manager != nil {
		for _, external := range manager.Snapshot() {
			definition := external.Definition()
			governed := &governedAgentTool{definition: definition, driver: external, coding: r.coding, run: run, host: host, sessionID: request.SessionID}
			drivers = append(drivers, wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), governed))
			toolNames = append(toolNames, definition.Name)
		}
	}
	if subagents != nil && !request.DisableSubagents {
		subagentDrivers, buildErr := subagents.Drivers(subagentParentRuntime{
			SessionID: request.SessionID, ParentRunID: run.RunID, ParentAgentID: run.HolderID,
			ProviderID: request.Provider, AccountID: accountID, ModelID: modelID, Reasoning: request.Reasoning, ContextTokenTarget: contextTarget,
			PlanMode:      request.PlanMode,
			ContextConfig: r.cfg.Agents.Context,
			WorkspaceRoot: r.cfg.Workspace.Root, Driver: driver, Coding: r.coding, Host: host,
			ResolveDriver: func(ctx context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
				boundAccountID := ""
				if provider == request.Provider {
					boundAccountID = accountID
				}
				_, resolvedModel, window, resolved, resolveErr := r.resolveDriverForAccount(ctx, provider, model, reasoning, boundAccountID)
				return resolvedModel, window, resolved, resolveErr
			},
			ResolveAccountDriver: func(ctx context.Context, provider, model, reasoning, requestedAccountID string) (string, string, int, hyprovider.Driver, error) {
				if requestedAccountID == "" && provider == request.Provider {
					requestedAccountID = accountID
				}
				resolvedAccount, resolvedModel, window, resolved, resolveErr := r.resolveDriverForAccount(ctx, provider, model, reasoning, requestedAccountID)
				return resolvedAccount.ID, resolvedModel, window, resolved, resolveErr
			},
		})
		if buildErr != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, buildErr.Error(), buildErr)
			return nil, hyagent.Engine{}, buildErr
		}
		for _, external := range subagentDrivers {
			definition := external.Definition()
			governed := &governedAgentTool{definition: definition, driver: external, coding: r.coding, run: run, host: host, sessionID: request.SessionID}
			drivers = append(drivers, wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), governed))
			toolNames = append(toolNames, definition.Name)
		}
	}
	if request.PlanMode {
		drivers = planModeToolDrivers(drivers)
		toolNames = toolDriverNames(drivers)
	}
	skillSnapshot := r.coding.SkillSnapshot()
	activeSkills := mergeSkillNames(skillSnapshot.Eager, request.ActiveSkills)
	activeSkills = mergeSkillNames(activeSkills, loadSessionActivatedSkills(ctx, host, request.SessionID, skillSnapshot.Registry))
	instructions, instructionFingerprint := turnInstructions(request.PlanMode)
	budgetConfig, err := calculateContextBudget(modelID, contextWindow, estimateToolDefinitionTokens(drivers), r.cfg.Agents.Context)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	extraBody := map[string]any{"prompt_cache_key": request.SessionID}
	enableExplicitPromptCache(extraBody, request.Provider, modelID)
	if host != nil && strings.TrimSpace(host.AttachmentRoot()) != "" {
		extraBody[responses.AttachmentRootExtraKey] = host.AttachmentRoot()
	}
	if reporter := r.responseUsageReporter(host, request.SessionID, run.RunID, "main", request.Provider, modelID, driver.Metadata().Name); reporter != nil && (host == nil || host.Sessions() == nil) {
		extraBody[responses.UsageReporterExtraKey] = reporter
	}
	// Catalog max output is not display-only: pass it on the main request so
	// providers that require max_tokens (Anthropic-compatible, including
	// DeepSeek) do not fall back to the adapter default of 4096.
	maxOutputTokens := r.modelMaxOutputTokens(request.Provider, modelID)
	if maxOutputTokens > 0 {
		extraBody["max_output_tokens"] = maxOutputTokens
	}
	hardContextTarget := budgetConfig.Trigger
	if !r.cfg.Agents.Context.Enabled {
		hardContextTarget = 0
	}
	spec := hyagent.Spec{
		Instructions:    instructions,
		Skills:          activeSkills,
		AvailableSkills: skillSnapshot.Available,
		Model:           modelID,
		Tools:           toolNames,
		MaxTokens:       maxOutputTokens,
		ExtraBody:       extraBody,
		LoopPolicy: hyagent.LoopPolicy{
			UnlimitedIterations: true,
			MaxWallClock:        maxWallClock,
			ContextTokenTarget:  hardContextTarget,
		},
	}
	deadlineAt := time.Time{}
	if maxWallClock > 0 {
		deadlineAt = time.Now().Add(maxWallClock)
	}
	if ctxDeadline, ok := ctx.Deadline(); ok && (deadlineAt.IsZero() || ctxDeadline.Before(deadlineAt)) {
		deadlineAt = ctxDeadline
	}
	contextManager := turnContext{
		sessionID:    request.SessionID,
		instructions: instructions, instructionFingerprint: instructionFingerprint, providerID: request.Provider, modelID: modelID, runID: run.RunID,
		privateContext: request.privateContext, visionContext: request.visionContext, approvedPlanContext: request.approvedPlanContext, historicalContext: request.historicalContext,
		deadlineAt: deadlineAt,
		resuming:   request.resuming,
		history:    request.History, modelHistory: request.modelHistory, toolRecords: request.toolRecords,
		workspaceRoot: r.cfg.Workspace.Root, checkpointBoundary: request.checkpointBoundary,
		images: effectiveTurnImages(request), todo: request.Todo,
		largeToolTokens:  r.cfg.Agents.Context.LargeToolResultTokens,
		keepRecentTokens: budgetConfig.KeepRecent,
		coordinator:      &compactionCoordinator{},
	}
	configureArchiveContext(ctx, &contextManager, host, request.SessionID, run.RunID, request.Provider, accountID, modelID)
	if host != nil {
		contextManager.reportCachePrefixDegraded = func(reason string) {
			host.EmitEvent(host.BaseContext(), Event{
				Kind: EventContextUsage, SessionID: request.SessionID, RunID: run.RunID, State: "degraded",
				Data: map[string]string{
					"cachePrefix": "degraded",
					"reason":      reason,
					"provider":    request.Provider,
					"model":       modelID,
					"cacheModel":  cacheModelForProvider(request.Provider, ""),
				},
			})
		}
	}
	type profileSkill struct {
		Skill          any               `json:"skill"`
		ResourceHashes map[string]string `json:"resource_hashes,omitempty"`
	}
	profileSkillNames := mergeSkillNames(activeSkills, skillSnapshot.Available)
	resolvedSkills := make([]profileSkill, 0, len(profileSkillNames))
	for _, name := range profileSkillNames {
		if resolved, ok := skillSnapshot.Registry.Get(name); ok {
			profile := profileSkill{Skill: resolved, ResourceHashes: make(map[string]string, len(resolved.Resources))}
			for _, resource := range resolved.Resources {
				payload, readErr := hyskill.ReadResource(resolved, resource.Name)
				if readErr != nil {
					_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, readErr.Error(), readErr)
					return nil, hyagent.Engine{}, fmt.Errorf("hash skill %s resource %s: %w", name, resource.Name, readErr)
				}
				digest := sha256.Sum256(payload)
				profile.ResourceHashes[resource.Name] = hex.EncodeToString(digest[:])
			}
			resolvedSkills = append(resolvedSkills, profile)
		}
	}
	attachmentRoot := ""
	if host != nil {
		attachmentRoot = host.AttachmentRoot()
	}
	staticPayload, marshalErr := json.Marshal(struct {
		Provider, Account, Model, Reasoning, Transport, Instructions string
		Skills, Tools                                                any
		RuntimeConfig                                                any
		ChatGPTEndpoint, GrokEndpoint, AttachmentRoot                string
		PlanMode, DisableSubagents                                   bool
		Wire                                                         int
	}{
		request.Provider, accountID, modelID, request.Reasoning, driver.Metadata().Name, instructionFingerprint,
		resolvedSkills, tool.NewBus(drivers...).Definitions(), r.cfg,
		r.ChatGPTEndpoint, r.GrokEndpoint, attachmentRoot,
		request.PlanMode, request.DisableSubagents, session.CurrentWireVersion,
	})
	if marshalErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, marshalErr.Error(), marshalErr)
		return nil, hyagent.Engine{}, fmt.Errorf("encode immutable run profile: %w", marshalErr)
	}
	staticDigest := sha256.Sum256(staticPayload)
	contextManager.staticIdentity = hex.EncodeToString(staticDigest[:])
	if request.resuming {
		if request.immutableIdentity != contextManager.staticIdentity {
			return nil, hyagent.Engine{}, fmt.Errorf("%w: tools, skills, or provider transport differ", errResumeProfileChanged)
		}
	} else if persistErr := r.persistSingleRunManifest(ctx, run.RunID, request, accountID, modelID, activeSkills, contextManager.staticIdentity); persistErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, persistErr.Error(), persistErr)
		return nil, hyagent.Engine{}, persistErr
	}
	if host != nil && host.Sessions() != nil {
		staticIdentity := activeCacheIdentity(contextManager.staticIdentity, request.modelHistory.ContextManifestHash, request.modelHistory.SummaryHash)
		_, _, identityErr := host.Sessions().EnsureCacheIdentity(ctx, request.SessionID, staticIdentity)
		if identityErr != nil {
			err := identityErr
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
			return nil, hyagent.Engine{}, err
		}
		contextManager.activateCompaction = func(activateCtx context.Context, messages []message.Message, identity string) error {
			manifest := extractArchiveContextManifestRecord(messages)
			expectedHighWater := request.checkpointBoundary
			if manifest != nil && manifest.CanonicalHighWater != nil {
				highWater := *manifest.CanonicalHighWater
				expectedHighWater = &highWater
			}
			return host.Sessions().SaveRunCheckpoint(activateCtx, request.SessionID, session.RunCheckpoint{
				RunID:             run.RunID,
				CacheIdentity:     identity,
				ExpectedHighWater: expectedHighWater,
				Manifest:          manifest,
				ModelHistory: session.ModelHistory{
					ProviderID: request.Provider, ModelID: modelID,
					InstructionFingerprint: instructionFingerprint,
					StaticPrefixHash:       contextManager.staticIdentity,
					WireVersion:            session.CurrentWireVersion,
					Messages:               messages,
					ContextManifestHash: func() string {
						if manifest != nil {
							return manifest.ManifestHash
						}
						return ""
					}(),
					PolicyVersion: func() int {
						if manifest != nil {
							return manifest.PolicyVersion
						}
						return 0
					}(),
				},
			})
		}
		if usage, err := host.Sessions().ProviderUsageSnapshot(ctx, request.SessionID, run.RunID); err == nil {
			_ = host.Sessions().UpdateUsage(ctx, request.SessionID, usage)
			encoded, _ := json.Marshal(usage)
			host.EmitEvent(host.BaseContext(), Event{
				Kind: EventContextUsage, SessionID: request.SessionID, RunID: run.RunID, State: "pending",
				Data: map[string]string{"factSnapshot": "true", "usageSnapshot": string(encoded), "requestKind": "main"},
			})
		}
		driver = &meteredProviderDriver{
			inner: driver, store: host.Sessions(), host: host, sessionID: request.SessionID,
			runID: run.RunID, kind: "main", provider: request.Provider, model: modelID, transport: driver.Metadata().Name,
		}
	}
	if host != nil && host.Sessions() != nil {
		contextManager.putArtifact = func(ctx context.Context, kind string, payload []byte, preview string) (session.ContextArtifact, error) {
			return host.Sessions().PutArtifact(ctx, request.SessionID, run.RunID, kind, payload, preview)
		}
	}
	if host != nil {
		contextManager.compactHooks = host.AutoCompactHooks(host.HookMetadata(request.SessionID, run.RunID))
		if host.Sessions() != nil {
			contextManager.loadTodo = func(ctx context.Context) (session.TodoList, error) {
				return host.Sessions().LoadTodo(ctx, request.SessionID)
			}
		}
		contextManager.reportContextTokens = func(_ context.Context, tokens int) {
			host.EmitEvent(host.BaseContext(), Event{Kind: EventContextUsage, SessionID: request.SessionID, RunID: run.RunID, State: "estimated", Data: map[string]string{
				"inputTokens": fmt.Sprint(tokens), "outputTokens": "0", "totalTokens": fmt.Sprint(tokens), "cacheStatus": "pending",
			}})
		}
	}
	var engineContext hyagent.ContextManager = contextManager
	if host != nil {
		engineContext = activeGuidanceContext{
			inner: contextManager,
			peek:  func() activeGuidanceSnapshot { return host.PeekActiveGuidance(request.SessionID, run.RunID) },
			acknowledge: func(snapshot activeGuidanceSnapshot) {
				host.AcknowledgeActiveGuidance(request.SessionID, run.RunID, snapshot)
			},
		}
	}
	if host != nil {
		driver = &contextProfileProviderDriver{inner: driver, emit: func(profile ContextProfile) {
			host.EmitEvent(host.BaseContext(), Event{
				Kind: EventContextProfile, SessionID: request.SessionID, RunID: run.RunID,
				State: "estimated", ContextProfile: &profile,
			})
		}}
	}
	definition := agentDefinitionForSpec(
		run.HolderID, "Azem Main", "Primary interactive coding agent", spec,
		singleRunGovernance(r.cfg, request),
		map[string]string{
			"role": "coding", "provider": request.Provider,
			"runtime_identity": contextManager.staticIdentity,
		},
	)
	engine, err := materializeAgentDefinition(ctx, r.coding, definition, spec, hyagent.BuildDeps{
		Providers:      hyprovider.Single(driver),
		Skills:         skillSnapshot.Registry,
		Tools:          tool.NewBus(drivers...),
		ContextManager: engineContext,
	})
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	engine.Hooks = engine.Hooks.Prepend(editRecoveryHook{run: run})
	if host != nil {
		engine.Hooks = engine.Hooks.Prepend(activeGuidanceModelHook{
			peek: func() activeGuidanceSnapshot {
				return host.PeekActiveGuidance(request.SessionID, run.RunID)
			},
		})
	}
	if host != nil {
		metadata := host.HookMetadata(request.SessionID, run.RunID)
		engine.OutputGuardrails = append(engine.OutputGuardrails, host.StopHookGuardrail(metadata, hooks.Stop, func(input hyagent.OutputGuardrailInput) string {
			messages := append(append([]message.Message(nil), input.Messages...), input.Output)
			return writeSessionHookTranscript(request.SessionID, messages)
		}))
		engine.OutputGuardrails = append(engine.OutputGuardrails, hyagent.NewOutputGuardrail("active-user-guidance", func(_ context.Context, _ hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
			guidance := host.FinishActiveGuidance(request.SessionID, run.RunID)
			if len(guidance) == 0 {
				return hyagent.AllowOutput(), nil
			}
			return hyagent.RetryOutput(guidanceMessages(guidance)...), nil
		}))
		if !request.DisableSubagents {
			sessionID, parentRunID := request.SessionID, run.RunID
			engine.OutputGuardrails = append(engine.OutputGuardrails, pendingBackgroundChildrenGuardrail(func() []backgroundChildStatus {
				return backgroundChildStatuses(host.RunningBackgroundChildren(sessionID, parentRunID))
			}))
		}
		sessionID, parentRunID := request.SessionID, run.RunID
		engine.OutputGuardrails = append(engine.OutputGuardrails, newMutatingVerificationGuardrail(
			host.Sessions(), r.cfg.Workspace.Root, sessionID, parentRunID,
			func() []string {
				if r.subagents == nil {
					return []string{parentRunID}
				}
				return r.subagents.relatedRunIDs(sessionID, parentRunID)
			},
		))
	}
	return run, engine, nil
}

func singleRunGovernance(cfg config.Config, request TurnRequest) api.GovernancePolicy {
	maxTokens := cfg.Agents.Main.MaxTokens
	maxToolCalls := cfg.Agents.Main.MaxToolCalls
	maxRuntime := cfg.Agents.Main.MaxWallClockDuration
	if request.budgetRestored {
		maxTokens = request.maxTokens
		maxToolCalls = request.maxToolCalls
		maxRuntime = request.maxWallClock
	}
	return api.GovernancePolicy{Budget: api.Budget{
		MaxTokens: maxTokens, MaxToolCalls: maxToolCalls, MaxRuntime: maxRuntime,
	}}
}

func agentDefinitionForSpec(
	id, name, description string,
	spec hyagent.Spec,
	governance api.GovernancePolicy,
	metadata map[string]string,
) api.AgentDefinition {
	metadata = maps.Clone(metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	loopPolicy, _ := json.Marshal(spec.LoopPolicy)
	stopSequences, _ := json.Marshal(spec.StopSequences)
	metadata["venat.loop_policy"] = string(loopPolicy)
	metadata["venat.thinking_budget"] = fmt.Sprint(spec.ThinkingBudget)
	metadata["venat.stop_sequences"] = string(stopSequences)
	return api.AgentDefinition{
		ID: id, Name: name, Description: description,
		Instructions:    spec.Instructions,
		Skills:          append([]string(nil), spec.Skills...),
		AvailableSkills: append([]string(nil), spec.AvailableSkills...),
		Model: api.ModelPolicy{
			Provider: spec.Provider, Model: spec.Model, FallbackModel: spec.FallbackModel,
			Temperature: spec.Temperature, TopP: spec.TopP, MaxTokens: spec.MaxTokens,
		},
		Tools:      append([]string(nil), spec.Tools...),
		ToolMode:   api.ToolModeParallel,
		Governance: governance,
		Status:     "active",
		Metadata:   metadata,
	}
}

func materializeAgentDefinition(
	ctx context.Context,
	coding *agentservice.Service,
	definition api.AgentDefinition,
	spec hyagent.Spec,
	deps hyagent.BuildDeps,
) (hyagent.Engine, error) {
	versioned := definition
	versioned.Version = ""
	encoded, err := json.Marshal(versioned)
	if err != nil {
		return hyagent.Engine{}, fmt.Errorf("encode agent definition: %w", err)
	}
	digest := sha256.Sum256(encoded)
	definition.Version = hex.EncodeToString(digest[:])
	deployed, err := (hyworker.DefinitionDeployment{
		Runner: coding.Runner(), BuildDeps: deps,
		Admission: hyworker.StandardAdmissionController{Runner: coding.Runner()},
		TTL:       10 * time.Minute,
	}).Deploy(ctx, definition)
	if err != nil {
		return hyagent.Engine{}, err
	}
	if err := deployed.Close(); err != nil {
		return hyagent.Engine{}, fmt.Errorf("close agent definition deployment: %w", err)
	}
	engine := deployed.Worker.Engine
	engine.LoopPolicy = spec.LoopPolicy
	engine.ThinkingBudget = spec.ThinkingBudget
	engine.StopSequences = append([]string(nil), spec.StopSequences...)
	engine.ExtraBody = maps.Clone(spec.ExtraBody)
	return engine, nil
}

func planModeToolDrivers(drivers []tool.Driver) []tool.Driver {
	allowed := make([]tool.Driver, 0, len(drivers))
	for _, driver := range drivers {
		definition := driver.Definition()
		if (definition.EffectType == tool.EffectReadOnly || definition.Name == submitPlanToolName) && definition.Name != subagentKillTool {
			allowed = append(allowed, driver)
		}
	}
	return allowed
}

func toolDriverNames(drivers []tool.Driver) []string {
	names := make([]string, 0, len(drivers))
	for _, driver := range drivers {
		names = append(names, driver.Definition().Name)
	}
	return names
}

func (r *ProviderRuntime) persistSingleRunManifest(ctx context.Context, runID string, request TurnRequest, accountID, resolvedModel string, activeSkills []string, staticIdentity string) error {
	durable, err := r.coding.Runner().Run(ctx, runID)
	if err != nil {
		return err
	}
	if durable.Metadata == nil {
		durable.Metadata = map[string]string{}
	}
	manifest := singleRunManifest{
		Version: 2, Provider: request.Provider, AccountID: accountID, Model: resolvedModel, Reasoning: request.Reasoning,
		ActiveSkills: append([]string(nil), activeSkills...), PlanMode: request.PlanMode, ApprovedPlanID: request.approvedPlanArtifactID, DisableSubagents: request.DisableSubagents,
		StaticIdentity: staticIdentity, MaxTokens: r.cfg.Agents.Main.MaxTokens, MaxToolCalls: r.cfg.Agents.Main.MaxToolCalls,
		MaxWallClockNS: int64(r.cfg.Agents.Main.MaxWallClockDuration), StartedAt: durable.CreatedAt.UTC(),
	}
	if manifest.ActiveSkills == nil {
		manifest.ActiveSkills = []string{}
	}
	encodedManifest, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	durable.Metadata["session_id"] = request.SessionID
	durable.Metadata["single_run_manifest"] = string(encodedManifest)
	return r.coding.Runner().SaveRun(ctx, durable)
}

// activeGuidanceModelHook projects pending guidance into every model request.
// The engine's hook context is request-local, so guidance remains pending until
// compaction adopts it into durable history or the terminal guardrail retries.
type activeGuidanceModelHook struct {
	peek func() activeGuidanceSnapshot
}

func (h activeGuidanceModelHook) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	if h.peek == nil {
		return messages, nil
	}
	guidance := guidanceMessages(h.peek().values)
	if len(guidance) == 0 {
		return messages, nil
	}
	return append(append([]message.Message(nil), messages...), guidance...), nil
}

func (activeGuidanceModelHook) BeforeModelCall(context.Context, *hyprovider.Request) error {
	return nil
}

func (activeGuidanceModelHook) BeforeToolCall(context.Context, *tool.Call) error {
	return nil
}

func (activeGuidanceModelHook) AfterToolCall(context.Context, *tool.Result) error {
	return nil
}

func (activeGuidanceModelHook) OnEvent(context.Context, hyprovider.Event) error {
	return nil
}

type activeGuidanceContext struct {
	inner       hyagent.TargetContextManager
	peek        func() activeGuidanceSnapshot
	acknowledge func(activeGuidanceSnapshot)
}

func (c activeGuidanceContext) Build(ctx context.Context, task api.Task) ([]message.Message, error) {
	return c.inner.Build(ctx, task)
}

func (c activeGuidanceContext) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	return c.compactWithGuidance(ctx, history, func(prepared []message.Message) ([]message.Message, error) {
		return c.inner.Compact(ctx, prepared)
	})
}

func (c activeGuidanceContext) CompactTo(ctx context.Context, history []message.Message, targetTokens int) ([]message.Message, error) {
	return c.compactWithGuidance(ctx, history, func(prepared []message.Message) ([]message.Message, error) {
		return c.inner.CompactTo(ctx, prepared, targetTokens)
	})
}

func (c activeGuidanceContext) compactWithGuidance(
	ctx context.Context,
	history []message.Message,
	compact func([]message.Message) ([]message.Message, error),
) ([]message.Message, error) {
	var compacted []message.Message
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		snapshot := c.peek()
		prepared := append([]message.Message(nil), history...)
		compacted, err = compact(append(prepared, guidanceMessages(snapshot.values)...))
		if err == nil {
			c.acknowledge(snapshot)
			return compacted, nil
		}
		if !errors.Is(err, session.ErrRunCheckpointStale) {
			return compacted, err
		}
		select {
		case <-ctx.Done():
			return compacted, ctx.Err()
		default:
		}
	}
	return compacted, err
}

func guidanceMessages(values []activeGuidanceMessage) []message.Message {
	cleaned := make([]activeGuidanceMessage, 0, len(values))
	var highWater int64
	for _, value := range values {
		value.Text = strings.TrimSpace(value.Text)
		value.Attachments = CloneAttachments(value.Attachments)
		if value.Text != "" || len(value.Attachments) > 0 {
			cleaned = append(cleaned, value)
			highWater = max(highWater, value.Sequence)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	if len(cleaned) == 1 {
		value := UserMessageWithAttachments(cleaned[0].Text, cleaned[0].Attachments)
		if highWater > 0 {
			value.Metadata = copyMessageMetadata(value.Metadata, highWater)
		}
		return []message.Message{value}
	}
	var combined strings.Builder
	combined.WriteString("[User guidance received while the task was running]\n")
	attachments := make([]session.Attachment, 0)
	for index, value := range cleaned {
		text := value.Text
		if text == "" {
			text = "[Attached image]"
		}
		fmt.Fprintf(&combined, "%d. %s\n", index+1, text)
		attachments = append(attachments, value.Attachments...)
	}
	result := UserMessageWithAttachments(strings.TrimSpace(combined.String()), attachments)
	if highWater > 0 {
		result.Metadata = copyMessageMetadata(result.Metadata, highWater)
	}
	return []message.Message{result}
}

type ContextBudget struct {
	ContextWindow int
	Trigger       int
	KeepRecent    int
}

func calculateContextBudget(modelID string, contextWindow, toolTokens int, cfg config.ContextConfig) (ContextBudget, error) {
	if contextWindow <= 0 {
		return ContextBudget{}, fmt.Errorf("model %q catalog omitted a positive context window", modelID)
	}
	reserve := cfg.ReserveTokens
	trigger := contextWindow - max(0, toolTokens) - reserve
	if trigger <= 0 {
		return ContextBudget{}, fmt.Errorf("model %q context window is too small after the OMP reserve", modelID)
	}
	if cfg.KeepRecentTokens >= trigger {
		return ContextBudget{}, fmt.Errorf("model %q context window leaves %d history tokens, below keep_recent_tokens=%d", modelID, trigger, cfg.KeepRecentTokens)
	}
	return ContextBudget{ContextWindow: contextWindow, Trigger: trigger, KeepRecent: cfg.KeepRecentTokens}, nil
}

func estimateToolDefinitionTokens(drivers []tool.Driver) int {
	bytes := 0
	for _, driver := range drivers {
		if driver == nil {
			continue
		}
		encoded, err := json.Marshal(driver.Definition())
		if err == nil {
			bytes += len(encoded)
		}
	}
	return (bytes + estimatedBytesPerToken - 1) / estimatedBytesPerToken
}

func (r *ProviderRuntime) responseUsageReporter(host providerHost, sessionID, runID, requestKind, providerID, modelID, transport string) responses.UsageReporter {
	if host == nil {
		return nil
	}
	return func(details responses.UsageDetails) {
		details = responses.NormalizeUsage(details, cacheModelForProvider(providerID, details.CacheModel))
		if details.ReasoningTokens == 0 && details.CacheWriteTokens == 0 && !details.CacheWriteReported {
			return
		}
		host.EmitEvent(host.BaseContext(), Event{Kind: EventContextUsage, SessionID: sessionID, RunID: runID, State: "reported", Data: map[string]string{
			"reasoningTokens": fmt.Sprint(details.ReasoningTokens), "cacheWriteTokens": fmt.Sprint(details.CacheWriteTokens), "uncachedInputTokens": fmt.Sprint(max(0, details.InputTokens-details.CachedTokens)),
			"aggregateOnly": "true", "requestKind": requestKind, "provider": providerID, "model": modelID, "transport": transport,
			"cacheModel": details.CacheModel, "cacheWriteStatus": map[bool]string{true: "reported", false: "unreported"}[details.CacheWriteReported],
		}})
	}
}

func enableExplicitPromptCache(extraBody map[string]any, provider, model string) {
	if provider == "chatgpt" && responses.SupportsExplicitPromptCaching(model) {
		extraBody[responses.PromptCacheBreakpointExtraKey] = responses.PromptCacheBreakpointLastUser
	}
}

func mergeSkillNames(eager, requested []string) []string {
	if len(requested) == 0 {
		return eager
	}
	seen := make(map[string]struct{}, len(eager)+len(requested))
	merged := make([]string, 0, len(eager)+len(requested))
	for _, names := range [][]string{eager, requested} {
		for _, name := range names {
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			merged = append(merged, name)
		}
	}
	return merged
}

func loadSessionActivatedSkills(ctx context.Context, host providerHost, sessionID string, registry *hyskill.Registry) []string {
	if host == nil || host.Sessions() == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	records, err := host.Sessions().ListToolRecords(ctx, sessionID)
	if err != nil {
		return nil
	}
	return filterResolvableSkills(registry, sessionActivatedSkillNames(records))
}

func sessionActivatedSkillNames(records []session.ToolRecord) []string {
	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, record := range records {
		if record.Name != hyagent.SkillActivationToolName || record.State != session.ToolCompleted {
			continue
		}
		name := activatedSkillName(record)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func activatedSkillName(record session.ToolRecord) string {
	var payload struct {
		Name  string `json:"name"`
		Skill string `json:"skill"`
	}
	for _, raw := range []json.RawMessage{record.Structured, record.Arguments} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		if json.Unmarshal(raw, &payload) != nil {
			continue
		}
		if name := strings.TrimSpace(payload.Name); name != "" {
			return name
		}
		if name := strings.TrimSpace(payload.Skill); name != "" {
			return name
		}
	}
	return ""
}

func filterResolvableSkills(registry *hyskill.Registry, names []string) []string {
	if registry == nil || len(names) == 0 {
		return nil
	}
	resolved := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := registry.Get(name); ok {
			resolved = append(resolved, name)
		}
	}
	return resolved
}

func (r *ProviderRuntime) CancelSubagent(sessionID, id string) agentservice.SubagentCancelOutcome {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return agentservice.SubagentCancelOutcome{Outcome: "not_found"}
	}
	return runtime.Cancel(sessionID, id)
}

func (r *ProviderRuntime) HasActiveForegroundSubagents(sessionID, parentRunID string) bool {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	return runtime != nil && runtime.HasForegroundByParentRun(sessionID, parentRunID)
}

func (r *ProviderRuntime) HasActiveSubagents(sessionID, parentRunID string) bool {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	return runtime != nil && runtime.HasActiveByParentRun(sessionID, parentRunID)
}

func (r *ProviderRuntime) CancelParentSubagents(sessionID, parentRunID string) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime != nil {
		runtime.CancelByParentRun(sessionID, parentRunID, true)
	}
}

func (r *ProviderRuntime) RunningBackgroundChildren(sessionID, parentRunID string) []agentservice.SubagentRun {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.listRunningBackgroundChildren(sessionID, parentRunID)
}

func (r *ProviderRuntime) AutoWakePending(sessionID string) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime != nil {
		runtime.AutoWakePending(sessionID)
	}
}

func (r *ProviderRuntime) ListSubagents(ctx context.Context, sessionID string) []agentservice.SubagentSnapshot {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.List(ctx, sessionID)
}

func (r *ProviderRuntime) DetailSubagent(ctx context.Context, sessionID, id string) ([]AgentTranscriptBlock, error) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return nil, api.ErrNotFound
	}
	return runtime.Detail(ctx, sessionID, id)
}

func (r *ProviderRuntime) Shutdown(ctx context.Context) error {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.Shutdown(ctx)
}

type teamProviderResolution struct {
	providerID    string
	accountID     string
	modelID       string
	contextWindow int
	driver        hyprovider.Driver
	resolver      hyprovider.Resolver
}

func (r *ProviderRuntime) TeamResolver(ctx context.Context, request TurnRequest) (teamProviderResolution, error) {
	account, modelID, contextWindow, driver, err := r.resolveDriverForAccount(ctx, request.Provider, request.Model, request.Reasoning, request.accountID)
	if err != nil {
		return teamProviderResolution{}, err
	}
	return teamProviderResolution{providerID: request.Provider, accountID: account.ID, modelID: modelID, contextWindow: contextWindow, driver: driver, resolver: hyprovider.Single(driver)}, nil
}

func observeProviderRetries(ctx context.Context, host providerHost, sessionID, runID, providerID string, driver hyprovider.Driver) {
	if host == nil {
		return
	}
	if configurable, ok := driver.(hyprovider.RetryDelayConfigurable); ok {
		configurable.SetMaxRetryDelay(host.RetryConfig().MaxDelayDuration)
	}
	retryDriver, ok := driver.(hyprovider.RetryObservable)
	if !ok {
		return
	}
	retryDriver.SetRetryObserver(func(progress hyprovider.RetryProgress) error {
		cause := ""
		if progress.Cause != nil {
			cause = progress.Cause.Error()
		}
		data := map[string]string{
			"provider": providerID, "attempt": fmt.Sprint(progress.Attempt), "max": fmt.Sprint(progress.Max),
			"delay_ms": fmt.Sprint(progress.Delay.Milliseconds()),
		}
		if progress.Cause != nil {
			data[errcode.DataKey] = string(errcode.Classify(progress.Cause))
		}
		if !host.EmitEvent(ctx, Event{
			Kind: EventProviderRetry, SessionID: sessionID, RunID: runID, State: "waiting", Text: cause,
			Data: data,
		}) {
			return eventDeliveryError(ctx)
		}
		return nil
	})
}

func (r *ProviderRuntime) approvalModelRoute(ctx context.Context, sessionID string) config.ModelRouteConfig {
	r.mu.RLock()
	providerID, modelID, reasoning := r.cfg.Defaults.Provider, r.cfg.Defaults.Model, r.cfg.Defaults.Reasoning
	route := r.cfg.Agents.Approval
	host := r.host
	r.mu.RUnlock()
	if host != nil && host.Sessions() != nil {
		if saved, err := host.Sessions().LoadSession(ctx, sessionID); err == nil {
			providerID = firstNonempty(saved.ProviderID, providerID)
			modelID = firstNonempty(saved.ModelID, modelID)
			reasoning = firstNonempty(saved.Reasoning, reasoning)
		}
	}
	if route != (config.ModelRouteConfig{}) {
		return route
	}
	return config.ModelRouteConfig{Provider: providerID, Model: modelID, Reasoning: reasoning}
}

func (r *ProviderRuntime) ApprovalReviewer(ctx context.Context, sessionID, runID string) (*codex.Reviewer, error) {
	route := r.approvalModelRoute(ctx, sessionID)
	providerID, modelID, reasoning := route.Provider, route.Model, route.Reasoning
	r.mu.RLock()
	host := r.host
	r.mu.RUnlock()
	_, resolvedModel, _, driver, err := r.resolveDriver(ctx, providerID, modelID, reasoning)
	if err != nil {
		return nil, err
	}
	observeProviderRetries(ctx, host, sessionID, runID, providerID, driver)
	reviewer, err := codex.NewProviderReviewer(driver, resolvedModel, reasoning, r.approvalReviewTimeout)
	if err != nil || host == nil || host.Sessions() == nil {
		return reviewer, err
	}
	return reviewer.WithDriver(&meteredProviderDriver{
		inner: driver, store: host.Sessions(), host: host, sessionID: sessionID, runID: runID,
		kind: "review", provider: providerID, model: resolvedModel, transport: driver.Metadata().Name,
	}), nil
}
