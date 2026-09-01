package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/adapterdeployment"
	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	providerretry "github.com/Viking602/azem/internal/provider"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	cursordriver "github.com/Viking602/azem/internal/provider/cursor"
	"github.com/Viking602/azem/internal/provider/errcode"
	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	hyskill "github.com/Viking602/venat/skill"
	"github.com/Viking602/venat/tool"
)

const providerDefaultReasoningIdentity = "provider-default"

func durableReasoningIdentity(reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return providerDefaultReasoningIdentity
	}
	return reasoning
}

func requestedReasoning(identity string) string {
	if strings.TrimSpace(identity) == providerDefaultReasoningIdentity {
		return ""
	}
	return identity
}

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
	cursorConversations   *cursordriver.ConversationCache

	mu              sync.RWMutex
	host            providerHost
	mcp             *mcpruntime.Manager
	subagents       *subagentRuntime
	subagentInitErr error
	advisors        map[string]*advisorWatchdog
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
		if definition.Name == agentservice.ToolReadFile {
			readTools = append(readTools, definition)
		}
	}
	if len(readTools) == 0 {
		return fmt.Errorf("edit recovery for %q requires unavailable tool %s", target, agentservice.ToolReadFile)
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

type liveApproval struct {
	approvalID  string
	agentID     string
	agentType   string
	run         *agentservice.Run
	callID      string
	operationID string
	sessionID   string
	runID       string
	fingerprint string
	request     approvalReviewRequest
	pending     agentservice.PendingApproval
	decision    chan agentservice.ApprovalMode
	suspension  chan error
	suspendable bool
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
	runtime := &ProviderRuntime{
		cfg: cfg, auth: authentication, catalog: modelCatalog, coding: codingService,
		subagentWorktreeRoot: subagentWorktreeRoot, cursorConversations: cursordriver.NewConversationCache(), advisors: make(map[string]*advisorWatchdog),
	}
	return runtime, nil
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
	r.coding.SetHubPeerBroker(r.subagents)
}

func (r *ProviderRuntime) Start(ctx context.Context, request TurnRequest) (*agentservice.Run, hyagent.Engine, error) {
	if request.automation != nil {
		return r.startAutomation(ctx, request)
	}
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
		Budget: &agentruntime.TaskBudget{
			MaxTokens: r.cfg.Agents.Main.MaxTokens, MaxWallClock: r.cfg.Agents.Main.MaxWallClockDuration,
			MaxToolCalls: r.cfg.Agents.Main.MaxToolCalls,
		},
	}
	executionPolicy.ResourceClaims, err = topLevelWorkspaceWriteClaims(r.cfg.Workspace.AllowWrite, r.cfg.Workspace.ShellPolicy, r.cfg.Workspace.Root)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	run, err := r.coding.StartRunWithMetadata(ctx, request.Prompt, map[string]string{"session_id": request.SessionID}, executionPolicy)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	r.mu.RLock()
	host := r.host
	r.mu.RUnlock()
	if host != nil && host.Sessions() != nil && request.origin != turnOriginAutoLearn {
		persistedUser := userTurnBlock(run.RunID, request)
		sequence, appendErr := host.Sessions().AppendBlock(ctx, request.SessionID, persistedUser)
		if appendErr != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, appendErr.Error(), appendErr)
			return nil, hyagent.Engine{}, fmt.Errorf("persist user turn: %w", appendErr)
		}
		persistedUser.Sequence = sequence
		request.History = append(request.History, persistedUser)
	}
	request, err = r.prepareVisionAssistance(ctx, request, run.RunID, account.ID, modelID)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	durable, err := r.coding.LoadRun(ctx, run.RunID)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	if durable.Metadata == nil {
		durable.Metadata = map[string]string{}
	}
	durable.Metadata["session_id"] = request.SessionID
	if err := r.coding.SaveRun(ctx, durable); err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	return r.buildSingleRun(ctx, request, run, account.ID, modelID, contextWindow, driver)
}

func canonicalRunUserHighWater(blocks []session.Block, runID string) *int64 {
	var highWater *int64
	for _, block := range blocks {
		if block.RunID != runID || block.Kind != "user" || (highWater != nil && block.Sequence <= *highWater) {
			continue
		}
		sequence := block.Sequence
		highWater = &sequence
	}
	return highWater
}

func (r *ProviderRuntime) buildSingleRun(ctx context.Context, request TurnRequest, run *agentservice.Run, accountID, modelID string, contextWindow int, driver hyprovider.Driver) (*agentservice.Run, hyagent.Engine, error) {
	maxWallClock := r.cfg.Agents.Main.MaxWallClockDuration
	if request.budgetRestored {
		maxWallClock = request.maxWallClock
		if maxWallClock > 0 {
			maxWallClock -= time.Since(request.startedAt)
			if maxWallClock <= 0 {
				return nil, hyagent.Engine{}, fmt.Errorf("%w: max wall clock reached", errResumeBudgetExhausted)
			}
		}
	}
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
	if subagentInitErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, subagentInitErr.Error(), subagentInitErr)
		return nil, hyagent.Engine{}, subagentInitErr
	}
	if request.VibeMode && subagents == nil {
		err := fmt.Errorf("vibe mode requires an initialized subagent runtime")
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}

	workspaceDrivers, err := r.coding.WorkspaceDrivers(ctx, r.cfg.Workspace.Root)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	if managedSkill := r.coding.ManagedSkillDriver(); managedSkill != nil && !request.VibeMode {
		workspaceDrivers = append(workspaceDrivers, managedSkill)
	}
	if request.origin == turnOriginAutoLearn {
		workspaceDrivers = workspaceDrivers[:0]
		if managedSkill := r.coding.ManagedSkillDriver(); managedSkill != nil {
			workspaceDrivers = append(workspaceDrivers, managedSkill)
		}
	}
	drivers := make([]tool.Driver, 0, len(workspaceDrivers)+9)
	toolNames := make([]string, 0, len(workspaceDrivers)+8)
	var checkpoint *checkpointController
	if host != nil && host.Sessions() != nil && !request.VibeMode {
		checkpoint, err = newCheckpointController(ctx, host.Sessions(), request.SessionID, run.RunID)
		if err != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
			return nil, hyagent.Engine{}, err
		}
	}

	for _, workspaceDriver := range workspaceDrivers {
		definition := workspaceDriver.Definition()
		if request.origin == turnOriginAutoLearn {
			drivers = append(drivers, workspaceDriver)
			toolNames = append(toolNames, definition.Name)
			continue
		}
		if request.VibeMode && !vibeDirectorWorkspaceTool(definition.Name) {
			continue
		}

		metadata := hooks.Metadata{SessionID: request.SessionID, RunID: run.RunID, AgentID: "main", AgentType: "main", CWD: r.cfg.Workspace.Root}
		governed := &governedAgentTool{
			definition: definition, driver: wrapHookDriver(host, metadata, workspaceDriver),
			coding: r.coding, run: run, host: host, sessionID: request.SessionID,
		}
		drivers = append(drivers, governed)
	}
	if host != nil && host.Sessions() != nil && request.origin != turnOriginAutoLearn {
		drivers = append(drivers, wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), &todoDriver{sessionID: request.SessionID, store: host.Sessions(), emit: func(event Event) bool {
			return host.EmitTodoUpdated(request.SessionID, *event.Todo)
		}}))
		if checkpoint != nil {
			for _, checkpointDriver := range checkpoint.drivers() {
				drivers = append(drivers, wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), checkpointDriver))
				toolNames = append(toolNames, checkpointDriver.Definition().Name)
			}
		}
		toolNames = append(toolNames, "todo")
		if !request.VibeMode {
			drivers = append(drivers,
				wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), &goalDriver{sessionID: request.SessionID, runID: run.RunID, store: host.Sessions()}),
				&askDriver{sessionID: request.SessionID, runID: run.RunID, host: host},
			)
			toolNames = append(toolNames, goalToolName, askToolName)
		}
		drivers = append(drivers, wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), &contextArtifactDriver{sessionID: request.SessionID, store: host.Sessions()}))
		toolNames = append(toolNames, contextReadArtifactTool)
		if request.PlanMode {
			drivers = append(drivers, &submitPlanDriver{sessionID: request.SessionID, runID: run.RunID, host: host, planYolo: request.PlanYolo})
			toolNames = append(toolNames, submitPlanToolName)
		}
	}
	if manager != nil && !request.VibeMode && request.origin != turnOriginAutoLearn {
		for _, external := range manager.Snapshot() {
			definition := external.Definition()
			governed := &governedAgentTool{
				definition: definition, driver: wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), external),
				coding: r.coding, run: run, host: host, sessionID: request.SessionID,
			}
			drivers = append(drivers, governed)
		}
	}
	if subagents != nil && !request.DisableSubagents && request.origin != turnOriginAutoLearn {
		parentRuntime := subagentParentRuntime{
			SessionID: request.SessionID, ParentRunID: run.RunID, ParentAgentID: run.HolderID,
			ProviderID: request.Provider, AccountID: accountID, ModelID: modelID, Reasoning: request.Reasoning, ContextTokenTarget: contextTarget,
			PlanMode: request.PlanMode, DirectorReadOnly: request.VibeMode,
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
		}
		subagentDrivers, buildErr := subagents.Drivers(parentRuntime)
		if buildErr != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, buildErr.Error(), buildErr)
			return nil, hyagent.Engine{}, buildErr
		}
		if request.VibeMode {
			subagentDrivers, buildErr = newVibeDrivers(subagents, parentRuntime, r.cfg.Agents.Vibe)
			if buildErr != nil {
				_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, buildErr.Error(), buildErr)
				return nil, hyagent.Engine{}, buildErr
			}
		}
		for _, external := range subagentDrivers {
			definition := external.Definition()
			governed := &governedAgentTool{
				definition: definition, driver: wrapHookDriver(host, host.HookMetadata(request.SessionID, run.RunID), external),
				coding: r.coding, run: run, host: host, sessionID: request.SessionID,
			}
			drivers = append(drivers, governed)
		}
	}
	if request.PlanMode {
		drivers = planModeToolDrivers(drivers)
		toolNames = toolDriverNames(drivers)
	}
	turnTools := newDynamicToolCatalog()
	for _, driver := range drivers {
		if err := turnTools.Register(dynamicToolSource(driver.Definition().Name), driver); err != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
			return nil, hyagent.Engine{}, err
		}
	}
	drivers = turnTools.Drivers()
	toolNames = turnTools.Names()
	skillSnapshot := r.coding.SkillSnapshot()
	activeSkills := mergeSkillNames(skillSnapshot.Eager, request.ActiveSkills)
	activeSkills = mergeSkillNames(activeSkills, loadSessionActivatedSkills(ctx, host, request.SessionID, skillSnapshot.Registry))
	availableSkills := skillSnapshot.Available
	if request.VibeMode {
		activeSkills = nil
		availableSkills = nil
	}
	instructions, instructionFingerprint := turnInstructionsWithProject(request.PlanMode, request.projectContext)
	toolDefinitionTokens := estimateToolDefinitionTokens(drivers)
	budgetConfig, err := calculateContextBudget(modelID, contextWindow, toolDefinitionTokens, r.cfg.Agents.Context)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	extraBody := map[string]any{}
	enableExplicitPromptCache(extraBody, request.Provider, modelID)
	maxOutputTokens := r.modelMaxOutputTokens(request.Provider, modelID)
	hardContextTarget := budgetConfig.Trigger
	if !r.cfg.Agents.Context.Enabled {
		hardContextTarget = 0
	}
	spec := hyagent.Spec{
		Instructions:    instructions,
		Skills:          activeSkills,
		AvailableSkills: availableSkills,
		Model:           modelID,
		Tools:           toolNames,
		MaxTokens:       maxOutputTokens,
		ExtraBody:       extraBody,
		LoopPolicy: hyagent.LoopPolicy{
			UnlimitedIterations: true,
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
	canonicalHighWater := canonicalRunUserHighWater(request.History, run.RunID)
	if canonicalHighWater == nil && request.checkpointBoundary != nil {
		value := *request.checkpointBoundary
		canonicalHighWater = &value
	}
	contextManager := turnContext{
		sessionID:    request.SessionID,
		instructions: instructions, instructionFingerprint: instructionFingerprint, providerID: request.Provider, modelID: modelID, runID: run.RunID,
		privateContext: request.privateContext, visionContext: request.visionContext, approvedPlanContext: request.approvedPlanContext, historicalContext: request.historicalContext,
		deadlineAt: deadlineAt,
		resuming:   request.resuming,
		history:    request.History, modelHistory: request.modelHistory, toolRecords: request.toolRecords,
		workspaceRoot: r.cfg.Workspace.Root, checkpointBoundary: request.checkpointBoundary, canonicalHighWater: canonicalHighWater,
		images: effectiveTurnImages(request), todo: request.Todo,
		largeToolTokens:  r.cfg.Agents.Context.LargeToolResultTokens,
		keepRecentTokens: budgetConfig.KeepRecent,
		coordinator:      &compactionCoordinator{},
		providerPressure: &providerContextPressure{toolTokens: toolDefinitionTokens},
	}
	if request.origin != turnOriginAutoLearn {
		configureArchiveContext(ctx, &contextManager, host, request.SessionID, run.RunID, request.Provider, accountID, modelID)
	}
	requestKind := "main"
	if request.origin == turnOriginAutoLearn {
		requestKind = "autolearn"
	}
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
					"requestKind": requestKind,
				},
			})
		}
	}
	profileSkillNames := mergeSkillNames(activeSkills, skillSnapshot.Available)
	resolvedSkills, skillProfileErr := immutableSkillProfiles(skillSnapshot.Registry, profileSkillNames)
	if skillProfileErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, skillProfileErr.Error(), skillProfileErr)
		return nil, hyagent.Engine{}, skillProfileErr
	}
	attachmentRoot := ""
	if host != nil {
		attachmentRoot = host.AttachmentRoot()
	}
	driverMetadata := driver.Metadata()
	transportVersion := ""
	if request.Provider == "cursor" {
		transportVersion = driverMetadata.Version
	}
	wireDefinitions := turnTools.Bus().Definitions()
	staticPayload, marshalErr := json.Marshal(struct {
		Provider, Account, Model, Reasoning, Transport, Instructions string
		TransportVersion                                             string `json:"transport_version,omitempty"`
		Skills, Tools                                                any
		RuntimeConfig                                                any
		ChatGPTEndpoint, GrokEndpoint, AttachmentRoot                string
		PlanMode, DisableSubagents                                   bool
		Wire                                                         int
	}{
		request.Provider, accountID, modelID, request.Reasoning, driverMetadata.Name, instructionFingerprint, transportVersion,
		resolvedSkills, wireDefinitions, r.cfg,
		r.ChatGPTEndpoint, r.GrokEndpoint, attachmentRoot,
		request.PlanMode, request.DisableSubagents, session.CurrentWireVersion,
	})
	if marshalErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, marshalErr.Error(), marshalErr)
		return nil, hyagent.Engine{}, fmt.Errorf("encode immutable run profile: %w", marshalErr)
	}
	staticDigest := sha256.Sum256(staticPayload)
	contextManager.staticIdentity = hex.EncodeToString(staticDigest[:])
	toolSchemaPayload, marshalErr := json.Marshal(wireDefinitions)
	if marshalErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, marshalErr.Error(), marshalErr)
		return nil, hyagent.Engine{}, fmt.Errorf("encode immutable tool schema: %w", marshalErr)
	}
	toolSchemaFingerprint := hashText(string(toolSchemaPayload))
	toolSetHash := hashText(strings.Join(toolNames, "\x00"))
	toolProfileHash := hashText(toolSetHash + "\x00" + toolSchemaFingerprint + "\x00" + contextManager.staticIdentity)
	if host != nil && host.Sessions() != nil && request.origin != turnOriginAutoLearn {
		staticIdentity := activeCacheIdentity(contextManager.staticIdentity, request.modelHistory.ContextManifestHash, request.modelHistory.SummaryHash)
		_, _, identityErr := host.Sessions().EnsureCacheIdentity(ctx, request.SessionID, staticIdentity)
		if identityErr != nil {
			err := identityErr
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
			return nil, hyagent.Engine{}, err
		}
		contextManager.activateCompaction = func(activateCtx context.Context, messages []message.Message, identity string) error {
			manifest := extractArchiveContextManifestRecord(messages)
			// The current run's durable user block is the CAS boundary. Do not
			// infer it from provider-facing message metadata: provider
			// conversion and loop normalization do not own that host protocol.
			expectedHighWater := contextManager.canonicalHighWater
			if expectedHighWater == nil && manifest != nil && manifest.CanonicalHighWater != nil {
				highWater := *manifest.CanonicalHighWater
				expectedHighWater = &highWater
			} else if expectedHighWater == nil {
				if highWater := canonicalMessageHighWater(messages); highWater >= 0 {
					expectedHighWater = &highWater
				}
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
				Data: map[string]string{"factSnapshot": "true", "usageSnapshot": string(encoded), "requestKind": requestKind},
			})
		}
		driver = &meteredProviderDriver{
			inner: driver, store: host.Sessions(), host: host, sessionID: request.SessionID,
			runID: run.RunID, kind: requestKind, provider: request.Provider, model: modelID, transport: driver.Metadata().Name,
			reportInputTokens: contextManager.providerPressure.observeInputTokens,
		}
	}
	driver = retryProviderDriver(ctx, host, request.SessionID, run.RunID, request.Provider, r.cfg.Retry, driver)
	driver, prewalkSwitch, err := r.preparePrewalkDriver(ctx, request, run, host, accountID, driver)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
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
				"requestKind": requestKind,
			}})
		}
	}
	var engineContext hyagent.ContextManager = contextManager
	if host != nil && request.origin != turnOriginAutoLearn {
		driver = &contextProfileProviderDriver{inner: driver, emit: func(profile ContextProfile) {
			host.EmitEvent(host.BaseContext(), Event{
				Kind: EventContextProfile, SessionID: request.SessionID, RunID: run.RunID,
				State: "estimated", ContextProfile: &profile,
			})
		}}
	}
	toolBus := turnTools.Bus()
	engine, err := hyagent.Build(spec, hyagent.BuildDeps{
		Providers:      hyprovider.Single(driver),
		Skills:         skillSnapshot.Registry,
		Tools:          toolBus,
		ContextManager: engineContext,
	})
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	profile := agentruntime.ExecutableProfile{
		Provider: request.Provider, AccountID: accountID, RawModel: firstNonempty(request.Model, modelID),
		Model: modelID, Reasoning: durableReasoningIdentity(request.Reasoning), ActiveSkills: append([]string{}, activeSkills...),
		ToolSetHash: toolSetHash, ToolProfileHash: toolProfileHash,
		PlanMode: request.PlanMode, ApprovedPlanID: request.approvedPlanArtifactID, DisableSubagents: request.DisableSubagents,
		StaticIdentity: contextManager.staticIdentity, WorkspaceAnchor: canonicalWorkspaceAnchor(r.cfg.Workspace.Root),
		PromptFingerprint: instructionFingerprint, ToolSchemaFingerprint: toolSchemaFingerprint,
	}
	if sealErr := r.coding.SealExecutionProfile(ctx, run, profile); sealErr != nil {
		err = sealErr
		if request.resuming && errors.Is(sealErr, agentruntime.ErrConflict) {
			err = fmt.Errorf("%w: %v", errResumeProfileChanged, sealErr)
		}
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	engine.ToolMode = tool.ModeParallel
	parallelToolCalls := true
	engine.PromptCacheKey = request.SessionID
	if request.origin != turnOriginAutoLearn && host != nil && host.Sessions() != nil {
		inheritedCacheKey, cacheKeyErr := host.Sessions().PromptCacheKey(ctx, request.SessionID)
		if cacheKeyErr != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, cacheKeyErr.Error(), cacheKeyErr)
			return nil, hyagent.Engine{}, cacheKeyErr
		}
		engine.PromptCacheKey = inheritedCacheKey
	}
	if request.origin == turnOriginAutoLearn {
		engine.PromptCacheKey += ":autolearn"
	}
	engine.ParallelToolCalls = &parallelToolCalls
	var turnControl *turnControlQueue
	if host != nil {
		turnControl = host.TurnControl(run.RunID)
	}
	attachmentRoot = providerAttachmentRoot(host)
	var cursorHost cursordriver.ExecHost
	if request.Provider == "cursor" || prewalkSwitch != nil && prewalkSwitch.targetProvider == "cursor" {
		cursorHost = newCursorExecHost(
			host, r.cfg.Workspace.Root, request.SessionID, run.RunID, run.RunID, "", toolBus,
		)
	}
	engine = bindProviderRequestScope(engine, attachmentRoot, cursorHost)

	advisor := r.advisorForRun(ctx, host, request.SessionID, run.RunID, request.Provider, accountID, modelID, request.Reasoning)
	r.mu.RLock()
	ttsrConfig := cloneTTSRConfig(r.cfg.TTSR)
	loopGuardConfig := r.cfg.Agents.LoopGuards
	loopGuardConfig.ToolCallExemptTools = append([]string(nil), loopGuardConfig.ToolCallExemptTools...)
	r.mu.RUnlock()
	ttsr, ttsrErr := newTTSRHook(ttsrConfig, r.coding, func() *session.Service {
		if host != nil {
			return host.Sessions()
		}
		return nil
	}(), request.SessionID, run.RunID, turnControl)
	if ttsrErr != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, ttsrErr.Error(), ttsrErr)
		return nil, hyagent.Engine{}, ttsrErr
	}
	if checkpoint != nil {
		engine.Hooks = engine.Hooks.Prepend(checkpoint)
		engine.OutputGuardrails = append(engine.OutputGuardrails, checkpoint.guardrail())
	}
	var loopStore *session.Service
	if host != nil {
		loopStore = host.Sessions()
	}
	loopGuard := newModelLoopGuard(loopGuardConfig, turnControl, loopStore, request.SessionID, run.RunID)
	engine.Hooks = engine.Hooks.Prepend(loopGuard)
	if guardrail := newUnexpectedStopGuard(loopGuardConfig); guardrail != nil {
		engine.OutputGuardrails = append(engine.OutputGuardrails, guardrail)
	}
	if ttsr != nil {
		engine.Hooks = engine.Hooks.Prepend(ttsr)
	}
	engine.Hooks = engine.Hooks.Prepend(editRecoveryHook{run: run})
	if prewalkSwitch != nil {
		prewalk := &prewalkHook{driver: prewalkSwitch, control: turnControl, sessionID: request.SessionID, runID: run.RunID}
		if host != nil {
			prewalk.store = host.Sessions()
		}
		engine.Hooks = engine.Hooks.Prepend(prewalk)
	}
	if host != nil {
		metadata := host.HookMetadata(request.SessionID, run.RunID)
		engine.OutputGuardrails = append(engine.OutputGuardrails, host.StopHookGuardrail(metadata, hooks.Stop, func(input hyagent.OutputGuardrailInput) string {
			messages := append(append([]message.Message(nil), input.Messages...), input.Output)
			return writeSessionHookTranscript(request.SessionID, messages)
		}))
		if advisor != nil {
			if guardrail := advisor.Guardrail(); guardrail != nil {
				engine.OutputGuardrails = append(engine.OutputGuardrails, guardrail)
			}
		}
		if guardrail := goalOutputGuardrail(host.Sessions(), request.SessionID, run.RunID); guardrail != nil {
			engine.OutputGuardrails = append(engine.OutputGuardrails, guardrail)
		}
		if !request.DisableSubagents {
			sessionID, parentRunID := request.SessionID, run.RunID
			engine.OutputGuardrails = append(engine.OutputGuardrails, pendingBackgroundChildrenGuardrail(func() []backgroundChildStatus {
				return backgroundChildStatuses(host.UnfinishedChildren(sessionID, parentRunID))
			}))
		}
		sessionID, parentRunID := request.SessionID, run.RunID
		engine.OutputGuardrails = append(engine.OutputGuardrails, newMutatingVerificationGuardrail(
			host.Sessions(), r.cfg.Workspace.Root, sessionID, parentRunID, true,
			func() []string {
				if r.subagents == nil {
					return []string{parentRunID}
				}
				return r.subagents.relatedRunIDs(sessionID, parentRunID)
			},
		))
	}
	engine = bindTurnControl(engine, turnControl)
	return run, engine, nil
}

func singleRunGovernance(cfg config.Config, request TurnRequest) agentruntime.GovernancePolicy {
	maxTokens := cfg.Agents.Main.MaxTokens
	maxToolCalls := cfg.Agents.Main.MaxToolCalls
	maxRuntime := cfg.Agents.Main.MaxWallClockDuration
	if request.budgetRestored {
		maxTokens = request.maxTokens
		maxToolCalls = request.maxToolCalls
		maxRuntime = request.maxWallClock
	}
	return agentruntime.GovernancePolicy{Budget: agentruntime.Budget{
		MaxTokens: maxTokens, MaxToolCalls: maxToolCalls, MaxRuntime: maxRuntime,
	}}
}

func planModeToolDrivers(drivers []tool.Driver) []tool.Driver {
	allowed := make([]tool.Driver, 0, len(drivers))
	for _, driver := range drivers {
		definition := driver.Definition()
		descriptor, err := agentservice.DescribeTool(driver)
		if err != nil {
			continue
		}
		policy := descriptor.PolicyForCall(tool.Call{Name: definition.Name})
		if (policy.IsReadOnly() || definition.Name == submitPlanToolName) && definition.Name != subagentKillTool {
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

type immutableSkillProfile struct {
	Skill          any               `json:"skill"`
	ResourceHashes map[string]string `json:"resource_hashes,omitempty"`
}

func immutableSkillProfiles(registry *hyskill.Registry, names []string) ([]immutableSkillProfile, error) {
	profiles := make([]immutableSkillProfile, 0, len(names))
	for _, name := range names {
		resolved, ok := registry.Get(name)
		if !ok {
			return nil, fmt.Errorf("hash skill %s: skill is not registered", name)
		}
		profile := immutableSkillProfile{Skill: resolved, ResourceHashes: make(map[string]string, len(resolved.Resources))}
		for _, resource := range resolved.Resources {
			payload, err := hyskill.ReadResource(resolved, resource.Name)
			if err != nil {
				return nil, fmt.Errorf("hash skill %s resource %s: %w", name, resource.Name, err)
			}
			digest := sha256.Sum256(payload)
			profile.ResourceHashes[resource.Name] = hex.EncodeToString(digest[:])
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

type ContextBudget struct {
	ContextWindow int
	Trigger       int
	KeepRecent    int
}

const (
	snapcompactReserveNumerator   = 15
	snapcompactReserveDenominator = 100
)

func calculateContextBudget(modelID string, contextWindow, toolTokens int, cfg config.ContextConfig) (ContextBudget, error) {
	if contextWindow <= 0 {
		return ContextBudget{}, fmt.Errorf("model %q catalog omitted a positive context window", modelID)
	}
	reserve := max(
		cfg.ReserveTokens,
		contextWindow/snapcompactReserveDenominator*snapcompactReserveNumerator+
			(contextWindow%snapcompactReserveDenominator)*snapcompactReserveNumerator/snapcompactReserveDenominator,
	)
	trigger := contextWindow - max(0, toolTokens) - reserve
	if trigger <= 0 {
		return ContextBudget{}, fmt.Errorf("model %q context window is too small after the Snapcompact reserve", modelID)
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

func (r *ProviderRuntime) CancelParentSubagentsAcrossSessions(parentRunID string) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime != nil {
		runtime.CancelByParentRunAcrossSessions(parentRunID, true)
	}
}

func (r *ProviderRuntime) UnfinishedChildren(sessionID, parentRunID string) []agentservice.SubagentRun {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.listUnfinishedChildren(sessionID, parentRunID)
}

func (r *ProviderRuntime) MarkParentChildrenDelivered(sessionID, parentRunID string) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime != nil {
		runtime.markParentChildrenDelivered(runtime.ctx, sessionID, parentRunID)
	}
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
		return nil, agentruntime.ErrNotFound
	}
	return runtime.Detail(ctx, sessionID, id)
}

func (r *ProviderRuntime) Shutdown(ctx context.Context) error {
	r.mu.RLock()
	runtime := r.subagents
	conversations := r.cursorConversations
	r.mu.RUnlock()
	var err error
	if runtime != nil {
		err = runtime.Shutdown(ctx)
	}
	if conversations != nil {
		conversations.Clear()
	}
	return err
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

func retryProviderDriver(ctx context.Context, host providerHost, sessionID, runID, providerID string, policy config.RetryConfig, driver hyprovider.Driver) hyprovider.Driver {
	if !policy.Enabled || policy.MaxRetries <= 0 {
		return driver
	}
	var observer hyprovider.RetryObserver
	if host != nil {
		observer = func(progress hyprovider.RetryProgress) error {
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
		}
	}
	return providerretry.WithRetry(driver, providerretry.RetryConfig{
		MaxRetries: policy.MaxRetries, BaseDelay: policy.BaseDelayDuration, MaxDelay: policy.MaxDelayDuration, Observer: observer,
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
	if host != nil && host.Sessions() != nil {
		driver = &meteredProviderDriver{
			inner: driver, store: host.Sessions(), host: host, sessionID: sessionID, runID: runID,
			kind: "review", provider: providerID, model: resolvedModel, transport: driver.Metadata().Name,
		}
	}
	driver = retryProviderDriver(ctx, host, sessionID, runID, providerID, r.cfg.Retry, driver)
	return codex.NewProviderReviewer(driver, resolvedModel, reasoning, r.approvalReviewTimeout)
}
