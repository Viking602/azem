package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/securityscan"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/tool"
)

type securityExecutor struct {
	runtime *ProviderRuntime
	coding  *agentservice.Service
	service *securityscan.Service
}

func (e *securityExecutor) Execute(ctx context.Context, request securityscan.ExecutionRequest) (securityscan.ExecutionResult, error) {
	if e == nil || e.runtime == nil || e.coding == nil || e.service == nil {
		return securityscan.ExecutionResult{}, fmt.Errorf("security scan runtime is unavailable")
	}
	allowed := map[string]bool{}
	switch request.Worker.Kind {
	case securityscan.WorkerAudit:
		for _, name := range []string{"coding.list_files", "coding.glob", "coding.read_file", "coding.search", agentservice.ToolASTGrep, agentservice.ToolWebSearch} {
			allowed[name] = true
		}
	case securityscan.WorkerFixer:
		for _, name := range []string{"coding.list_files", "coding.glob", "coding.read_file", "coding.search", agentservice.ToolASTGrep, agentservice.ToolWebSearch, "coding.edit_hashline", "coding.replace", "coding.write_file", "coding.delete_file", "coding.gofmt", "coding.go_test"} {
			allowed[name] = true
		}
	case securityscan.WorkerVerifier:
		for _, name := range []string{"coding.list_files", "coding.glob", "coding.read_file", "coding.search", agentservice.ToolASTGrep, agentservice.ToolWebSearch, "coding.git_diff", "coding.go_test"} {
			allowed[name] = true
		}
	}
	budget := agentruntime.TaskBudget{
		MaxTokens: request.Budget.MaxTokens, MaxToolCalls: request.Budget.MaxToolCalls,
		MaxWallClock: time.Duration(request.Budget.MaxWallClockNS),
	}
	childBudget := budget
	if request.Worker.Kind == securityscan.WorkerAudit && request.MaxSubagents > 0 {
		participants := int64(request.MaxSubagents + 1)
		if budget.MaxTokens > 0 {
			budget.MaxTokens /= participants
		}
		if budget.MaxToolCalls > 0 {
			budget.MaxToolCalls /= int(participants)
		}
		childBudget = budget
	}
	turn := TurnRequest{
		SessionID: "security:" + request.Scan.ID + ":" + request.Worker.ID,
		accountID: request.Route.AccountID,
		Prompt:    request.Prompt, Provider: request.Route.Provider, Model: request.Route.Model, Reasoning: request.Route.Reasoning,
		AgentMode: "single", DisableSubagents: request.Worker.Kind != securityscan.WorkerAudit || request.MaxSubagents == 0,
		automation: &automationTurn{
			Kind: "security_" + string(request.Worker.Kind), Version: securityscan.WorkflowVersion,
			WorkspaceRoot: request.WorkspaceRoot, Instructions: request.Instructions, AllowedTools: allowed,
			AllowedSubagentTypes: map[string]bool{"security-baseline": true, "security-investigator": true},
			MaxSubagents:         request.MaxSubagents,
			Drivers:              securityDrivers(e.service, request), Budget: budget, ChildBudget: childBudget,
			Metadata: map[string]string{"security_scan_id": request.Scan.ID, "security_worker_id": request.Worker.ID, "security_worker_kind": string(request.Worker.Kind)},
			ObservePath: func(path string) {
				if filepath.IsAbs(path) {
					if relative, err := filepath.Rel(request.WorkspaceRoot, path); err == nil {
						path = filepath.ToSlash(relative)
					}
				}
				e.service.ObservePath(request.Scan.ID, request.Worker.ID, filepath.ToSlash(path))
			},
			ObserveTool: func(name string) {
				e.service.ObserveTool(request.Scan.ID, request.Worker.ID, name)
			},
		},
	}
	run, engine, err := e.runtime.Start(ctx, turn)
	if err != nil {
		return securityscan.ExecutionResult{}, err
	}
	if err := e.service.BindWorkerRun(ctx, request.Scan.ID, request.Worker.ID, run.RunID); err != nil {
		_ = e.coding.CompleteRun(context.WithoutCancel(ctx), run, err.Error(), err)
		return securityscan.ExecutionResult{}, err
	}
	result := securityscan.ExecutionResult{RunID: run.RunID}
	sink := hyagent.SinkFunc(func(context.Context, hyagent.Frame) error { return nil })
	outcome, runErr := executeMainRunUntilAvailable(ctx, func() (agentservice.ExecutionOutcome, error) {
		return e.coding.ExecuteRun(agentservice.DelegatedApprovalContext(ctx), run, engine, sink)
	})
	result.FinalText = outcome.Result.Text
	result.InputTokens = int64(outcome.Result.Usage.InputTokens)
	result.CachedInputTokens = int64(outcome.Result.Usage.CachedInputTokens)
	result.OutputTokens = int64(outcome.Result.Usage.OutputTokens)
	if outcome.State == agentservice.ExecutionSuspended && runErr == nil {
		runErr = fmt.Errorf("security scan run suspended before completion")
	}
	if outcome.Result.Failure != nil && runErr == nil {
		runErr = fmt.Errorf("security scan model failure: %w", outcome.Result.Failure)
	}
	return result, securityExecutionError(runErr)
}

func securityExecutionError(err error) error {
	var batch *tool.BatchExecutionError
	if err == nil || !errors.As(err, &batch) {
		return err
	}
	details := make([]error, 0, len(batch.Failures)+1)
	details = append(details, err)
	for _, failure := range batch.Failures {
		details = append(details, failure)
	}
	return errors.Join(details...)
}

func (e *securityExecutor) FinalizeInterruptedRun(ctx context.Context, runID string, accepted bool) error {
	if e == nil || e.coding == nil || runID == "" {
		return nil
	}
	run, err := e.coding.ResumeRun(ctx, runID)
	if err != nil {
		return err
	}
	summary := "security scan worker interrupted before completion"
	var failure error = context.Canceled
	if accepted {
		summary = "security scan draft accepted before process interruption"
		failure = nil
	}
	return e.coding.CompleteRun(context.WithoutCancel(ctx), run, summary, failure)
}

func (e *securityExecutor) Cancel(ctx context.Context, runID string) error {
	if e == nil || e.coding == nil || runID == "" {
		return nil
	}
	if e.runtime != nil {
		e.runtime.CancelParentSubagentsAcrossSessions(runID)
	}
	tracked, err := e.coding.CancelTrackedRun(ctx, runID)
	if err != nil {
		return err
	}
	if !tracked {
		return errors.New("security scan run is not active in this process")
	}
	return nil
}
