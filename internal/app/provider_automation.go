package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/api"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

type automationObservedDriver struct {
	tool.Driver
	observePath func(string)
	observeTool func(string)
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
		Governance: api.GovernancePolicy{Budget: api.Budget{
			MaxTokens: automation.Budget.MaxTokens, MaxToolCalls: automation.Budget.MaxToolCalls,
			MaxRuntime: automation.Budget.MaxWallClock,
		}},
		Budget: &automation.Budget,
	}
	if r.cfg.Retry.Enabled {
		executionPolicy.RetryPolicy = api.RetryPolicy{MaxAttempts: r.cfg.Retry.MaxRetries + 1, Backoff: r.cfg.Retry.BaseDelayDuration, MaxBackoff: r.cfg.Retry.MaxDelayDuration}
	}
	run, err := r.coding.StartRunWithMetadata(ctx, request.Prompt, metadata, executionPolicy)
	if err != nil {
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
	for key, value := range metadata {
		durable.Metadata[key] = value
	}
	if err := r.coding.Runner().SaveRun(ctx, durable); err != nil {
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

func (r *ProviderRuntime) buildAutomationRun(ctx context.Context, request TurnRequest, run *agentservice.Run, accountID, modelID string, contextWindow int, driver hyprovider.Driver) (*agentservice.Run, hyagent.Engine, error) {
	automation := request.automation
	usageBudget := &providerUsageBudget{maxTokens: automation.Budget.MaxTokens}
	driver = &budgetedProviderDriver{inner: driver, budget: usageBudget}
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
	instructionDigest := sha256.Sum256([]byte(automation.Instructions))
	instructionFingerprint := hex.EncodeToString(instructionDigest[:])
	maxOutputTokens := r.modelMaxOutputTokens(request.Provider, modelID)
	hardContextTarget := budgetConfig.Trigger
	if !r.cfg.Agents.Context.Enabled {
		hardContextTarget = 0
	}
	spec := hyagent.Spec{
		Instructions: automation.Instructions, Model: modelID, Tools: toolNames, MaxTokens: maxOutputTokens,
		LoopPolicy: hyagent.LoopPolicy{UnlimitedIterations: true, MaxWallClock: automation.Budget.MaxWallClock, ContextTokenTarget: hardContextTarget},
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
	}{automation.Kind, automation.Version, request.Provider, accountID, modelID, request.Reasoning, instructionFingerprint, tool.NewBus(drivers...).Definitions()})
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	staticDigest := sha256.Sum256(staticPayload)
	contextManager.staticIdentity = hex.EncodeToString(staticDigest[:])
	durable, err := r.coding.Runner().Run(ctx, run.RunID)
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	if durable.Metadata == nil {
		durable.Metadata = map[string]string{}
	}
	durable.Metadata["automation_identity"] = contextManager.staticIdentity
	if err := r.coding.Runner().SaveRun(ctx, durable); err != nil {
		return nil, hyagent.Engine{}, err
	}
	definition := agentDefinitionForSpec(run.HolderID, "Azem Security", "Background security analysis agent", spec,
		api.GovernancePolicy{Budget: api.Budget{MaxTokens: automation.Budget.MaxTokens, MaxToolCalls: automation.Budget.MaxToolCalls, MaxRuntime: automation.Budget.MaxWallClock}},
		map[string]string{"role": "security", "provider": request.Provider, "runtime_identity": contextManager.staticIdentity, "automation_kind": automation.Kind})
	toolBus := tool.NewBus(drivers...)
	engine, err := materializeAgentDefinition(ctx, r.coding, definition, spec, hyagent.BuildDeps{
		Providers: hyprovider.Single(driver), Skills: r.coding.SkillSnapshot().Registry, Tools: toolBus, ContextManager: contextManager,
	})
	if err != nil {
		return nil, hyagent.Engine{}, err
	}
	parallelToolCalls := true
	engine.PromptCacheKey = request.SessionID
	engine.ParallelToolCalls = &parallelToolCalls
	engine.NativeToolHost = newAttachmentRequestHost(host)
	if request.Provider == "cursor" {
		engine.NativeToolHost = newCursorExecHost(host, automation.WorkspaceRoot, request.SessionID, run.RunID, run.RunID, "", toolBus)
	}
	if host != nil && !request.DisableSubagents {
		sessionID, parentRunID := request.SessionID, run.RunID
		engine.OutputGuardrails = append(engine.OutputGuardrails, pendingBackgroundChildrenGuardrail(func() []backgroundChildStatus {
			return backgroundChildStatuses(host.UnfinishedChildren(sessionID, parentRunID))
		}))
	}
	return run, engine, nil
}

func cloneAutomationMetadata(values map[string]string) map[string]string {
	return maps.Clone(values)
}

var _ = time.Second
