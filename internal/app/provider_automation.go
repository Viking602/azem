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

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	cursordriver "github.com/Viking602/azem/internal/provider/cursor"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

type automationObservedDriver struct {
	tool.Driver
	observePath func(string)
	observeTool func(string)
}

func (d *automationObservedDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	descriptor, err := agentservice.DescribeTool(d.Driver)
	if err != nil {
		return agentruntime.ToolPolicy{
			Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
			RequiresActionTask: true, RiskLevel: "high",
		}
	}
	return descriptor.PolicyForCall(call)
}

func (d *automationObservedDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	result, err := d.Driver.Execute(ctx, call, sink)
	if err != nil || result.IsError {
		return result, err
	}
	if d.observeTool != nil {
		d.observeTool(d.Definition().Name)
	}
	if d.observePath != nil && d.Definition().Name == "coding.read_file" {
		var arguments map[string]any
		if json.Unmarshal(call.Arguments, &arguments) == nil {
			if path, ok := arguments["path"].(string); ok && strings.TrimSpace(path) != "" {
				d.observePath(path)
			}
		}
	}
	return result, nil
}

func (r *ProviderRuntime) startAutomation(ctx context.Context, request TurnRequest) (*agentservice.Run, hyagent.Engine, error) {
	automation := request.automation
	if automation == nil || strings.TrimSpace(automation.Kind) == "" || strings.TrimSpace(automation.Version) == "" || strings.TrimSpace(automation.WorkspaceRoot) == "" || strings.TrimSpace(automation.Instructions) == "" {
		return nil, hyagent.Engine{}, fmt.Errorf("automation runtime profile is incomplete")
	}
	if err := r.ensureAutomationSession(ctx, request, automation.Kind); err != nil {
		return nil, hyagent.Engine{}, err
	}
	account, modelID, contextWindow, driver, err := r.resolveDriver(ctx, request.Provider, request.Model, request.Reasoning)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	request.Reasoning, err = r.resolvedReasoningEffort(ctx, request.Provider, account.ID, modelID, request.Reasoning)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	metadata := maps.Clone(automation.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["session_id"] = request.SessionID
	metadata["automation_kind"] = automation.Kind
	metadata["automation_version"] = automation.Version
	executionPolicy := agentservice.RunExecutionPolicy{
		AgentVersion: automation.Version,
		Governance: agentruntime.GovernancePolicy{Budget: agentruntime.Budget{
			MaxTokens: automation.Budget.MaxTokens, MaxToolCalls: automation.Budget.MaxToolCalls,
			MaxRuntime: automation.Budget.MaxWallClock,
		}},
		Budget: &automation.Budget,
	}
	run, err := r.coding.StartRunWithMetadata(ctx, request.Prompt, metadata, executionPolicy)
	if err != nil {
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
	for key, value := range metadata {
		durable.Metadata[key] = value
	}
	if err := r.coding.SaveRun(ctx, durable); err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	builtRun, engine, err := r.buildAutomationRun(ctx, request, run, account.ID, modelID, contextWindow, driver)
	if err != nil {
		_ = r.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return nil, hyagent.Engine{}, err
	}
	return builtRun, engine, nil
}

func (r *ProviderRuntime) ensureAutomationSession(ctx context.Context, request TurnRequest, title string) error {
	r.mu.RLock()
	host := r.host
	r.mu.RUnlock()
	if host == nil || host.Sessions() == nil {
		return nil
	}
	sessions := host.Sessions()
	if _, err := sessions.LoadSession(ctx, request.SessionID); err == nil {
		return nil
	} else if !errors.Is(err, session.ErrSessionNotFound) {
		return fmt.Errorf("load automation session: %w", err)
	}
	if _, err := sessions.Ensure(ctx, session.Session{
		ID: request.SessionID, Title: title, ProviderID: request.Provider, ModelID: request.Model,
		Reasoning: request.Reasoning, AgentMode: "single",
	}); err != nil {
		return fmt.Errorf("create automation session: %w", err)
	}
	if err := sessions.SetArchived(ctx, request.SessionID, true); err != nil {
		return fmt.Errorf("archive automation session: %w", err)
	}
	return nil
}

func (r *ProviderRuntime) buildAutomationRun(ctx context.Context, request TurnRequest, run *agentservice.Run, accountID, modelID string, contextWindow int, driver hyprovider.Driver) (*agentservice.Run, hyagent.Engine, error) {
	automation := request.automation
	budgetConfig, err := calculateContextBudget(modelID, contextWindow, 0, r.cfg.Agents.Context)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	r.mu.RLock()
	host, subagents, subagentInitErr := r.host, r.subagents, r.subagentInitErr
	r.mu.RUnlock()
	if subagentInitErr != nil {
		return nil, hyagent.Engine{}, subagentInitErr
	}
	if host == nil {
		return nil, hyagent.Engine{}, fmt.Errorf("automation host coordinator is unavailable")
	}
	workspaceDrivers, err := r.coding.WorkspaceDrivers(ctx, automation.WorkspaceRoot)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	drivers := make([]tool.Driver, 0, len(workspaceDrivers)+len(automation.Drivers)+4)
	for _, workspaceDriver := range workspaceDrivers {
		definition := workspaceDriver.Definition()
		if !automation.AllowedTools[definition.Name] {
			continue
		}
		observed := tool.Driver(workspaceDriver)
		if automation.ObservePath != nil || automation.ObserveTool != nil {
			observed = &automationObservedDriver{Driver: workspaceDriver, observePath: automation.ObservePath, observeTool: automation.ObserveTool}
		}
		drivers = append(drivers, &governedAgentTool{definition: definition, driver: observed, coding: r.coding, run: run, host: host, sessionID: request.SessionID})
	}
	drivers = append(drivers, automation.Drivers...)
	if subagents != nil && !request.DisableSubagents {
		subagentDrivers, buildErr := subagents.Drivers(subagentParentRuntime{
			SessionID: request.SessionID, ParentRunID: run.RunID, ParentAgentID: run.HolderID,
			ProviderID: request.Provider, AccountID: accountID, ModelID: modelID, Reasoning: request.Reasoning,
			ContextTokenTarget: budgetConfig.Trigger, ContextConfig: r.cfg.Agents.Context,
			WorkspaceRoot: automation.WorkspaceRoot, Driver: driver, Coding: r.coding, Host: host,
			AllowedRoles: automation.AllowedSubagentTypes, AllowedTools: automation.AllowedTools,
			ForceReadOnly: true, DisableHooks: true, DelegationDepthLimit: 1, MaxChildren: automation.MaxSubagents,
			Budget: automation.ChildBudget, ObservePath: automation.ObservePath, ObserveTool: automation.ObserveTool,
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
			return nil, hyagent.Engine{}, buildErr
		}
		for _, subagentDriver := range subagentDrivers {
			definition := subagentDriver.Definition()
			drivers = append(drivers, &governedAgentTool{definition: definition, driver: subagentDriver, coding: r.coding, run: run, host: host, sessionID: request.SessionID})
		}
	}
	toolNames := toolDriverNames(drivers)
	wireDefinitions := tool.NewBus(drivers...).Definitions()
	instructionDigest := sha256.Sum256([]byte(automation.Instructions))
	instructionFingerprint := hex.EncodeToString(instructionDigest[:])
	maxOutputTokens := r.modelMaxOutputTokens(request.Provider, modelID)
	hardContextTarget := budgetConfig.Trigger
	if !r.cfg.Agents.Context.Enabled {
		hardContextTarget = 0
	}
	spec := hyagent.Spec{
		Instructions: automation.Instructions, Model: modelID, Tools: toolNames, MaxTokens: maxOutputTokens,
		LoopPolicy: hyagent.LoopPolicy{
			UnlimitedIterations: true,
			ContextTokenTarget:  hardContextTarget,
		},
	}
	request.History = []session.Block{{Kind: "user", RunID: run.RunID, Title: "Security scan", Content: request.Prompt, State: "submitted"}}
	contextManager := turnContext{
		sessionID: request.SessionID, runID: run.RunID, instructions: automation.Instructions,
		instructionFingerprint: instructionFingerprint, providerID: request.Provider, modelID: modelID,
		history: request.History, workspaceRoot: automation.WorkspaceRoot,
		largeToolTokens: r.cfg.Agents.Context.LargeToolResultTokens, keepRecentTokens: budgetConfig.KeepRecent,
		coordinator: &compactionCoordinator{}, providerPressure: &providerContextPressure{toolTokens: estimateToolDefinitionTokens(drivers)},
	}
	staticPayload, err := json.Marshal(struct {
		Kind, Version, Provider, Account, Model, Reasoning, Instructions string
		Tools                                                            any
	}{automation.Kind, automation.Version, request.Provider, accountID, modelID, request.Reasoning, instructionFingerprint, wireDefinitions})
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	toolSchemaPayload, err := json.Marshal(wireDefinitions)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	staticDigest := sha256.Sum256(staticPayload)
	contextManager.staticIdentity = hex.EncodeToString(staticDigest[:])
	toolSchemaFingerprint := hashText(string(toolSchemaPayload))
	toolSetHash := hashText(strings.Join(toolNames, "\x00"))
	toolProfileHash := hashText(toolSetHash + "\x00" + toolSchemaFingerprint + "\x00" + contextManager.staticIdentity)
	durable, err := r.coding.LoadRun(ctx, run.RunID)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	if durable.Metadata == nil {
		durable.Metadata = map[string]string{}
	}
	durable.Metadata["automation_identity"] = contextManager.staticIdentity
	if err := r.coding.SaveRun(ctx, durable); err != nil {
		return nil, hyagent.Engine{}, err
	}
	if host != nil && host.Sessions() != nil {
		driver = &meteredProviderDriver{
			inner: driver, store: host.Sessions(), host: host, sessionID: request.SessionID,
			runID: run.RunID, kind: "automation", provider: request.Provider, model: modelID, transport: driver.Metadata().Name,
			reportInputTokens: contextManager.providerPressure.observeInputTokens,
		}
	}
	driver = retryProviderDriver(ctx, host, request.SessionID, run.RunID, request.Provider, r.cfg.Retry, driver)
	toolBus := tool.NewBus(drivers...)
	engine, err := hyagent.Build(spec, hyagent.BuildDeps{
		Providers: hyprovider.Single(driver), Skills: r.coding.SkillSnapshot().Registry, Tools: toolBus, ContextManager: contextManager,
	})
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	if err := r.coding.SealExecutionProfile(ctx, run, agentruntime.ExecutableProfile{
		Provider: request.Provider, AccountID: accountID, RawModel: firstNonempty(request.Model, modelID),
		Model: modelID, Reasoning: durableReasoningIdentity(request.Reasoning), ActiveSkills: []string{},
		ToolSetHash: toolSetHash, ToolProfileHash: toolProfileHash, DisableSubagents: request.DisableSubagents,
		StaticIdentity: contextManager.staticIdentity, WorkspaceAnchor: canonicalWorkspaceAnchor(automation.WorkspaceRoot),
		PromptFingerprint: instructionFingerprint, ToolSchemaFingerprint: toolSchemaFingerprint,
	}); err != nil {
		return nil, hyagent.Engine{}, err
	}
	engine.ToolMode = tool.ModeParallel
	parallelToolCalls := true
	engine.PromptCacheKey = request.SessionID
	engine.ParallelToolCalls = &parallelToolCalls
	var cursorHost cursordriver.ExecHost
	if request.Provider == "cursor" {
		cursorHost = newCursorExecHost(host, automation.WorkspaceRoot, request.SessionID, run.RunID, run.RunID, "", toolBus)
	}
	engine = bindProviderRequestScope(engine, providerAttachmentRoot(host), cursorHost)
	if host != nil && !request.DisableSubagents {
		sessionID, parentRunID := request.SessionID, run.RunID
		engine.OutputGuardrails = append(engine.OutputGuardrails, pendingBackgroundChildrenGuardrail(func() []backgroundChildStatus {
			return backgroundChildStatuses(host.UnfinishedChildren(sessionID, parentRunID))
		}))
	}
	engine = host.BindProviderEngine(engine)
	return run, engine, nil
}

func cloneAutomationMetadata(values map[string]string) map[string]string {
	return maps.Clone(values)
}
