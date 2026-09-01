package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
)

// ResumeRun rebuilds a single-agent engine around the durable run and task
// recovered by Venat. It resumes only when this session still owns a
// checkpoint for the same run; runs requiring side-effect reconciliation stay
// paused for explicit resolution.
func (r *ProviderRuntime) ResumeRun(ctx context.Context, runID string) error {
	return r.resumeRun(ctx, runID, "")
}

func (r *ProviderRuntime) ResumeRunAtOperation(ctx context.Context, runID, operationID string) error {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return fmt.Errorf("resume operation id is empty")
	}
	return r.resumeRun(ctx, runID, operationID)
}

func (r *ProviderRuntime) resumeRun(ctx context.Context, runID, operationID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	durable, err := r.coding.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	if durable.Status == agentruntime.RunStatusReconcileRequired {
		return nil
	}
	switch durable.Status {
	case agentruntime.RunStatusCompleted, agentruntime.RunStatusFailed, agentruntime.RunStatusCancelled:
		return nil
	}
	sessionID := strings.TrimSpace(durable.Metadata["session_id"])
	r.mu.RLock()
	host := r.host
	subagents := r.subagents
	r.mu.RUnlock()
	if sessionID == "" {
		if subagents != nil && subagents.ownsDurableRun(runID) {
			// The recovered subagent execution owns resumption. Its wait loop
			// acquires the replacement lease after this approval transition.
			return nil
		}
		return r.coding.RequireRunReconciliation(context.Background(), runID, "durable run is missing session ownership")
	}
	if host == nil || host.Sessions() == nil {
		return fmt.Errorf("resume run %s: application session runtime is unavailable", runID)
	}
	projection, err := host.Sessions().LoadProjection(host.BaseContext(), sessionID)
	if err != nil {
		return fmt.Errorf("resume run %s session: %w", runID, err)
	}
	if projection.LastRunID != runID {
		return r.coding.RequireRunReconciliation(host.BaseContext(), runID, "session projection does not own the recovered run")
	}
	manifest, err := r.coding.LoadRunExecutionManifest(host.BaseContext(), runID)
	if err != nil {
		return r.coding.RequireRunReconciliation(host.BaseContext(), runID, "immutable execution manifest is missing or invalid")
	}
	if manifest.WorkspaceAnchor != canonicalWorkspaceAnchor(r.cfg.Workspace.Root) {
		return r.coding.RequireRunReconciliation(host.BaseContext(), runID, "immutable execution workspace no longer matches")
	}
	request := TurnRequest{
		SessionID: sessionID, Prompt: durable.Request,
		Provider: manifest.Provider, Model: manifest.RawModel,
		Reasoning: requestedReasoning(manifest.Reasoning), AgentMode: projection.Session.AgentMode,
		History: append([]session.Block(nil), projection.Blocks...), modelHistory: projection.ModelHistory,
		toolRecords:        append([]session.ToolRecord(nil), projection.ToolRecords...),
		checkpointBoundary: projection.ModelHistory.CoveredThroughSequence, resuming: true,
	}
	request.ActiveSkills = append([]string(nil), manifest.ActiveSkills...)
	request.PlanMode = manifest.PlanMode
	request.approvedPlanArtifactID = manifest.ApprovedPlanID
	if request.approvedPlanArtifactID != "" {
		request.approvedPlanContext, err = host.ApprovedPlanContext(host.BaseContext(), sessionID, request.approvedPlanArtifactID)
		if err != nil {
			return r.coding.RequireRunReconciliation(host.BaseContext(), runID, "approved execution plan is missing or invalid")
		}
	}
	request.DisableSubagents = manifest.DisableSubagents
	request.budgetRestored = true
	request.maxTokens, request.maxToolCalls = manifest.Budget.MaxTokens, manifest.Budget.MaxToolCalls
	request.maxWallClock = manifest.Budget.MaxWallClock
	request.startedAt = manifest.StartedAt
	request.usedTokens, err = host.Sessions().ProviderRunTotalTokens(host.BaseContext(), sessionID, runID)
	if err != nil {
		return fmt.Errorf("resume run %s usage: %w", runID, err)
	}
	for index := len(projection.Blocks) - 1; index >= 0; index-- {
		if block := projection.Blocks[index]; block.RunID == runID && block.Kind == "user" {
			request.Images = CloneAttachments(block.Attachments)
			break
		}
	}
	request.Todo, err = host.Sessions().LoadTodo(host.BaseContext(), sessionID)
	if err != nil {
		return fmt.Errorf("resume run %s todo: %w", runID, err)
	}
	account, modelID, contextWindow, driver, err := r.resolveDriverForAccount(host.BaseContext(), request.Provider, request.Model, request.Reasoning, manifest.AccountID)
	if err != nil {
		return err
	}
	if _, err := calculateContextBudget(modelID, contextWindow, 0, r.cfg.Agents.Context); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(host.BaseContext())
	for {
		claimErr := host.ClaimActiveRun(runID, sessionID, cancel, true)
		if claimErr == nil {
			break
		}
		if !errors.Is(claimErr, ErrRunActive) {
			cancel()
			return claimErr
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			cancel()
			return context.Cause(ctx)
		case <-host.BaseContext().Done():
			timer.Stop()
			cancel()
			return context.Cause(host.BaseContext())
		case <-timer.C:
		}
	}
	var run *agentservice.Run
	if operationID != "" {
		run, err = r.coding.ResumeRunAtOperation(runCtx, runID, operationID)
	} else {
		run, err = r.coding.ResumeRun(runCtx, runID)
	}
	if err != nil {
		cancel()
		host.ClearRun(runID)
		return err
	}
	_, engine, err := r.buildSingleRun(runCtx, request, run, account.ID, modelID, contextWindow, driver)
	if err != nil {
		cancel()
		if errors.Is(err, errResumeProfileChanged) {
			_ = r.coding.ReleaseRun(context.WithoutCancel(host.BaseContext()), run)
			host.ClearRun(runID)
			return r.coding.RequireRunReconciliation(context.WithoutCancel(host.BaseContext()), runID, err.Error())
		}
		if errors.Is(err, errResumeBudgetExhausted) {
			host.ClearRun(runID)
			return r.terminalizeRecoveredBudget(host, sessionID, runID, run)
		}
		host.ClearRun(runID)
		return err
	}
	engine = host.BindProviderEngine(engine)
	host.SpawnProviderTurn(runCtx, request, run, engine)
	return nil
}

func (r *ProviderRuntime) terminalizeRecoveredBudget(host providerHost, sessionID, runID string, run *agentservice.Run) error {
	var err error
	if run == nil {
		run, err = r.coding.ResumeRun(context.WithoutCancel(host.BaseContext()), runID)
		if err != nil {
			return fmt.Errorf("resume exhausted run %s for finalization: %w", runID, err)
		}
	}
	failure := fmt.Errorf("%w: recovered run exhausted its original budget", hyagent.ErrBudgetExhausted)
	if host.Sessions() != nil {
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, persistErr := host.Sessions().AppendBlock(persistCtx, sessionID, session.Block{
			Kind: "assistant", RunID: runID, Title: "Azem", Content: failure.Error(), State: "failed",
		})
		cancel()
		if persistErr != nil {
			_ = r.coding.ReleaseRun(context.WithoutCancel(host.BaseContext()), run)
			return persistErr
		}
	}
	return r.coding.CompleteRun(context.WithoutCancel(host.BaseContext()), run, failure.Error(), failure)
}

func (r *ProviderRuntime) ResumeRecoveredRun(ctx context.Context, runID string) error {
	run, err := r.coding.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Metadata["team"] == "true" {
		return r.ResumeTeam(ctx, runID)
	}
	return r.ResumeRun(ctx, runID)
}

func (r *ProviderRuntime) ResumeRecoveredRunAtOperation(ctx context.Context, runID, operationID string) error {
	run, err := r.coding.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Metadata["team"] == "true" {
		return fmt.Errorf("team run %s cannot resume a single-agent operation %s", runID, operationID)
	}
	return r.ResumeRunAtOperation(ctx, runID, operationID)
}

// ResumeTeam rebuilds provider and tool bindings from durable run metadata,
// then resumes Azem's persisted Team orchestration without blocking startup.
func (r *ProviderRuntime) ResumeTeam(_ context.Context, runID string) error {
	run, err := r.coding.LoadRun(context.Background(), runID)
	if err != nil {
		return err
	}
	currentWorkspace := canonicalWorkspaceAnchor(r.cfg.Workspace.Root)
	storedWorkspace := strings.TrimSpace(run.Metadata[teamWorkspaceAnchorMetadata])
	if storedWorkspace == "" {
		return r.coding.RequireRunReconciliation(context.Background(), runID, "durable team run is missing its workspace binding")
	}
	if storedWorkspace != currentWorkspace {
		return r.coding.RequireRunReconciliation(context.Background(), runID, "durable team run belongs to a different workspace")
	}
	request := TurnRequest{
		SessionID:      firstNonempty(run.Metadata["session_id"], "default"),
		Prompt:         run.Request,
		Provider:       firstNonempty(run.Metadata["provider"], r.cfg.Defaults.Provider),
		Model:          firstNonempty(run.Metadata["model"], r.cfg.Defaults.Model),
		Reasoning:      firstNonempty(run.Metadata["reasoning"], r.cfg.Defaults.Reasoning),
		AgentMode:      "team",
		Images:         DecodeAttachmentsMeta(run.Metadata["attachments"]),
		privateContext: run.Metadata["hook_private_context"],
	}
	request.accountID = run.Metadata["account_id"]
	if request.accountID == "" {
		return r.coding.RequireRunReconciliation(context.Background(), runID, "durable team run is missing its provider account binding")
	}
	if err := ValidateTurnAttachments(request.Images); err != nil {
		return fmt.Errorf("resume team %s attachments: %w", runID, err)
	}
	resolution, err := r.TeamResolver(context.Background(), request)
	if err != nil {
		return err
	}
	r.mu.RLock()
	host := r.host
	r.mu.RUnlock()
	if host == nil {
		return fmt.Errorf("resume team %s: application runtime is unavailable", runID)
	}
	if host.Sessions() != nil {
		projection, loadErr := host.Sessions().LoadProjection(host.BaseContext(), request.SessionID)
		if loadErr != nil {
			return fmt.Errorf("resume team %s session: %w", runID, loadErr)
		}
		request.History = append([]session.Block(nil), projection.Blocks...)
		request.modelHistory = projection.ModelHistory
		request.checkpointBoundary = projection.ModelHistory.CoveredThroughSequence
		if count := len(request.History); count > 0 && request.History[count-1].RunID == runID && request.History[count-1].Kind == "user" {
			request.History = request.History[:count-1]
		}
		request.Todo, loadErr = host.Sessions().LoadTodo(host.BaseContext(), request.SessionID)
		if loadErr != nil {
			return fmt.Errorf("resume team %s todo: %w", runID, loadErr)
		}
	}
	runCtx, cancel := context.WithCancel(host.BaseContext())
	originalPrompt := firstNonempty(run.Metadata["original_prompt"], request.Prompt)
	request.historicalContext = host.LoadTurnHistoricalContext(host.BaseContext(), request.SessionID, originalPrompt, historicalRetrievalBoundary(request.modelHistory))
	if err := host.ClaimActiveRun(runID, "", cancel, false); err != nil {
		cancel()
		return err
	}
	host.SpawnResumedProviderTeam(runCtx, request, runID, originalPrompt, resolution)
	return nil
}
