package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/provider/xai"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/tool"
)

type ProviderRuntime struct {
	cfg                  config.Config
	auth                 *auth.Service
	catalog              *catalog.Service
	coding               *agentservice.Service
	subagentWorktreeRoot string
	ChatGPTEndpoint      string
	GrokEndpoint         string

	mu              sync.RWMutex
	host            *Service
	mcp             *mcpruntime.Manager
	subagents       *subagentRuntime
	subagentInitErr error
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
	return &ProviderRuntime{
		cfg: cfg, auth: authentication, catalog: modelCatalog, coding: codingService,
		subagentWorktreeRoot: subagentWorktreeRoot,
	}, nil
}

func (r *ProviderRuntime) Attach(host *Service, manager *mcpruntime.Manager, subagentStore agentservice.SubagentRunStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.host = host
	r.mcp = manager
	if r.subagents == nil && r.subagentInitErr == nil && host != nil && subagentStore != nil {
		r.subagents, r.subagentInitErr = newSubagentRuntime(host.ctx, r.cfg.Agents.Subagents, subagentStore, r.subagentWorktreeRoot)
	}
}

func (r *ProviderRuntime) Start(ctx context.Context, request TurnRequest) (*agentservice.Run, hyagent.Engine, error) {
	account, modelID, contextWindow, driver, err := r.resolveDriver(ctx, request.Provider, request.Model, request.Reasoning)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	contextTarget, err := modelContextTokenTarget(modelID, contextWindow)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	run, err := r.coding.StartRun(ctx, request.Prompt)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
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
	if host != nil && host.sessions != nil {
		drivers = append(drivers, wrapHookDriver(host, host.hookMetadata(request.SessionID, run.RunID), &todoDriver{sessionID: request.SessionID, store: host.sessions, emit: func(event Event) bool {
			return host.emitTodoUpdated(request.SessionID, *event.Todo)
		}}))
		toolNames = append(toolNames, "todo")
	}
	if manager != nil {
		for _, external := range manager.Snapshot() {
			definition := external.Definition()
			governed := &governedAgentTool{definition: definition, driver: external, coding: r.coding, run: run, host: host, sessionID: request.SessionID}
			drivers = append(drivers, wrapHookDriver(host, host.hookMetadata(request.SessionID, run.RunID), governed))
			toolNames = append(toolNames, definition.Name)
		}
	}
	if subagents != nil && !request.DisableSubagents {
		subagentDrivers, buildErr := subagents.Drivers(subagentParentRuntime{
			SessionID: request.SessionID, ParentRunID: run.RunID, ParentAgentID: run.HolderID,
			ProviderID: request.Provider, ModelID: modelID, Reasoning: request.Reasoning, ContextTokenTarget: contextTarget,
			WorkspaceRoot: r.cfg.Workspace.Root, Driver: driver, Coding: r.coding, Host: host,
		})
		if buildErr != nil {
			_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, buildErr.Error(), buildErr)
			return nil, hyagent.Engine{}, buildErr
		}
		for _, external := range subagentDrivers {
			definition := external.Definition()
			governed := &governedAgentTool{definition: definition, driver: external, coding: r.coding, run: run, host: host, sessionID: request.SessionID}
			drivers = append(drivers, wrapHookDriver(host, host.hookMetadata(request.SessionID, run.RunID), governed))
			toolNames = append(toolNames, definition.Name)
		}
	}
	skillSnapshot := r.coding.SkillSnapshot()
	activeSkills := mergeSkillNames(skillSnapshot.Eager, request.ActiveSkills)
	instructions := mainInstructions
	spec := hyagent.Spec{
		Instructions:    instructions,
		Skills:          activeSkills,
		AvailableSkills: skillSnapshot.Available,
		Model:           modelID,
		Tools:           toolNames,
		ExtraBody:       map[string]any{"prompt_cache_key": request.SessionID},
		LoopPolicy: hyagent.LoopPolicy{
			UnlimitedIterations: true,
			MaxWallClock:        r.cfg.Agents.Main.MaxWallClockDuration,
			ContextTokenTarget:  contextTarget,
		},
	}
	contextManager := turnContext{
		instructions: instructions, providerID: request.Provider, modelID: modelID, runID: run.RunID,
		privateContext: request.privateContext, historicalContext: request.historicalContext,
		history: request.History, persistedHistory: request.persistedHistory,
		modelHistory: request.modelHistory, todo: request.Todo,
	}
	if host != nil {
		contextManager.compactHooks = host.autoCompactHooks(host.hookMetadata(request.SessionID, run.RunID))
		if host.sessions != nil {
			contextManager.loadTodo = func(ctx context.Context) (session.TodoList, error) {
				return host.sessions.LoadTodo(ctx, request.SessionID)
			}
		}
		contextManager.reportContextTokens = func(_ context.Context, tokens int) {
			host.emit(host.ctx, Event{Kind: EventContextUsage, SessionID: request.SessionID, RunID: run.RunID, State: "estimated", Data: map[string]string{
				"inputTokens": fmt.Sprint(tokens), "outputTokens": "0", "totalTokens": fmt.Sprint(tokens), "cacheStatus": "pending",
			}})
		}
	}
	engine, err := hyagent.Build(spec, hyagent.BuildDeps{
		Providers:      hyprovider.Single(driver),
		Skills:         skillSnapshot.Registry,
		Tools:          tool.NewBus(drivers...),
		ContextManager: contextManager,
	})
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	if host != nil {
		metadata := host.hookMetadata(request.SessionID, run.RunID)
		engine.OutputGuardrails = append(engine.OutputGuardrails, host.stopHookGuardrail(metadata, hooks.Stop, func(input hyagent.OutputGuardrailInput) string {
			messages := append(append([]message.Message(nil), input.Messages...), input.Output)
			return writeSessionHookTranscript(request.SessionID, messages)
		}))
	}
	_ = account
	return run, engine, nil
}

func modelContextTokenTarget(modelID string, contextWindow int) (int, error) {
	if contextWindow <= 0 {
		return 0, fmt.Errorf("model %q catalog omitted a positive context window", modelID)
	}
	target := contextWindow/4*3 + (contextWindow%4)*3/4
	if target <= 0 {
		return 0, fmt.Errorf("model %q context window is too small", modelID)
	}
	return target, nil
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

func (r *ProviderRuntime) CancelParentSubagents(sessionID, parentRunID string) {
	r.mu.RLock()
	runtime := r.subagents
	r.mu.RUnlock()
	if runtime != nil {
		runtime.CancelByParentRun(sessionID, parentRunID, true)
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

func (r *ProviderRuntime) TeamResolver(ctx context.Context, request TurnRequest) (string, hyprovider.Resolver, error) {
	_, modelID, _, driver, err := r.resolveDriver(ctx, request.Provider, request.Model, request.Reasoning)
	if err != nil {
		return "", nil, err
	}
	return modelID, hyprovider.Single(driver), nil
}

func (r *ProviderRuntime) ApprovalReviewer(ctx context.Context) (*codex.Reviewer, error) {
	accounts, err := r.auth.Accounts(ctx, "chatgpt")
	if err != nil {
		return nil, err
	}
	var accountID string
	for _, account := range accounts {
		if account.Status == "active" {
			accountID = account.ID
			break
		}
	}
	if accountID == "" {
		return nil, fmt.Errorf("no active ChatGPT account is available")
	}
	driver, err := codex.New(r.auth, accountID, r.ChatGPTEndpoint, []string{codex.ApprovalReviewerModel}, "low")
	if err != nil {
		return nil, err
	}
	return codex.NewReviewer(driver)
}

// ResumeTeam rebuilds provider and tool bindings from durable run metadata,
// then resumes the TeamRunner checkpoint without blocking startup.
func (r *ProviderRuntime) ResumeTeam(_ context.Context, runID string) error {
	run, err := r.coding.Runner().Run(context.Background(), runID)
	if err != nil {
		return err
	}
	request := TurnRequest{
		SessionID:      firstNonempty(run.Metadata["session_id"], "default"),
		Prompt:         run.Request,
		Provider:       firstNonempty(run.Metadata["provider"], r.cfg.Defaults.Provider),
		Model:          firstNonempty(run.Metadata["model"], r.cfg.Defaults.Model),
		Reasoning:      firstNonempty(run.Metadata["reasoning"], r.cfg.Defaults.Reasoning),
		AgentMode:      "team",
		privateContext: run.Metadata["hook_private_context"],
	}
	modelID, resolver, err := r.TeamResolver(context.Background(), request)
	if err != nil {
		return err
	}
	r.mu.RLock()
	host := r.host
	r.mu.RUnlock()
	if host == nil {
		return fmt.Errorf("resume team %s: application runtime is unavailable", runID)
	}
	runCtx, cancel := context.WithCancel(host.ctx)
	host.mu.Lock()
	if host.activeRun != "" {
		host.mu.Unlock()
		cancel()
		return ErrRunActive
	}
	host.activeRun = runID
	host.activeEnd = cancel
	host.mu.Unlock()
	host.wg.Add(1)
	originalPrompt := firstNonempty(run.Metadata["original_prompt"], request.Prompt)
	historicalContext := host.loadHistoricalContext(host.ctx, request.SessionID, originalPrompt)
	go host.runResumedProviderTeam(runCtx, request.SessionID, runID, request.Prompt, originalPrompt, modelID, request.privateContext, historicalContext, resolver)
	return nil
}

func (r *ProviderRuntime) resolveDriver(ctx context.Context, providerID, modelID, requestedReasoning string) (auth.Account, string, int, hyprovider.Driver, error) {
	accounts, err := r.auth.Accounts(ctx, providerID)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	var account auth.Account
	for _, candidate := range accounts {
		if candidate.Status == "active" {
			account = candidate
			break
		}
	}
	if account.ID == "" {
		return auth.Account{}, "", 0, nil, fmt.Errorf("sign in to %s before starting a turn", providerID)
	}
	models, err := r.catalog.List(ctx, providerID, account.ID, false)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	if modelID == "" && len(models.Models) > 0 {
		modelID = models.Models[0].ID
	}
	var selectedModel catalog.Model
	for _, model := range models.Models {
		if model.ID == modelID {
			selectedModel = model
			break
		}
	}
	if selectedModel.ID == "" {
		return auth.Account{}, "", 0, nil, fmt.Errorf("model %q is not available for %s account %s", modelID, providerID, account.ID)
	}
	reasoningEffort, err := catalog.ResolveReasoningEffort(providerID, selectedModel, requestedReasoning)
	if err != nil {
		return auth.Account{}, "", 0, nil, err
	}
	modelIDs := []string{modelID}
	switch providerID {
	case "chatgpt":
		driver, err := codex.New(r.auth, account.ID, r.ChatGPTEndpoint, modelIDs, reasoningEffort)
		return account, modelID, selectedModel.ContextWindow, driver, err
	case "grok":
		var transport xai.Transport
		switch r.cfg.Providers.Grok.Transport {
		case "", "api":
			transport = &xai.StandardTransport{Auth: r.auth, AccountID: account.ID, Endpoint: r.GrokEndpoint}
		case "cli_proxy":
			transport = &xai.CLIProxyTransport{Token: func(ctx context.Context) (string, error) {
				credential, err := r.auth.Credential(ctx, "grok", account.ID)
				return credential.AccessToken, err
			}}
		default:
			return auth.Account{}, "", 0, nil, fmt.Errorf("unsupported Grok transport %q", r.cfg.Providers.Grok.Transport)
		}
		driver, err := xai.New(transport, modelIDs, reasoningEffort)
		return account, modelID, selectedModel.ContextWindow, driver, err
	default:
		return auth.Account{}, "", 0, nil, fmt.Errorf("unsupported provider %q", providerID)
	}
}
