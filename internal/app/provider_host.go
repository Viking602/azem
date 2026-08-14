package app

import (
	"context"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/session"
)

// providerHost is the complete surface the provider/subagent runtime may use
// from the application service. The runtime must depend on this interface
// instead of *Service so the coupling stays explicit, testable, and ready for
// package extraction; every method is implemented by *Service below.
type providerHost interface {
	// Core plumbing.
	BaseContext() context.Context
	Sessions() *session.Service
	EmitEvent(ctx context.Context, event Event) bool
	AttachmentRoot() string
	RetryConfig() config.RetryConfig

	// Provider runtime lookups used by child (subagent) execution.
	HasProviderRuntime() bool
	ModelMaxOutputTokens(providerID, modelID string) int
	ProviderTransport(providerID string) string

	// Hook integration.
	HookMetadata(sessionID, runID string) hooks.Metadata
	HookDispatcher() hooks.Dispatcher
	DispatchLifecycleHook(ctx context.Context, event hooks.Event, metadata hooks.Metadata, add func(*hooks.Envelope)) error
	AutoCompactHooks(metadata hooks.Metadata) func(context.Context, []message.Message, []message.Message, error) error
	StopHookGuardrail(metadata hooks.Metadata, event hooks.Event, transcriptPath func(hyagent.OutputGuardrailInput) string) hyagent.OutputGuardrail

	// Todo projection.
	EmitTodoUpdated(sessionID string, todo session.TodoList) bool

	// Active-run ownership and turn execution.
	ClaimActiveRun(runID, sessionID string, cancel context.CancelFunc, openGuidance bool) error
	ClearRun(runID string)
	BindProviderEngine(engine hyagent.Engine) hyagent.Engine
	SpawnProviderTurn(ctx context.Context, request TurnRequest, run *agentservice.Run, engine hyagent.Engine)
	SpawnResumedProviderTeam(ctx context.Context, request TurnRequest, runID, recapGoal string, resolution teamProviderResolution)

	// Live user guidance.
	PeekActiveGuidance(sessionID, runID string) activeGuidanceSnapshot
	AcknowledgeActiveGuidance(sessionID, runID string, snapshot activeGuidanceSnapshot)
	FinishActiveGuidance(sessionID, runID string) []activeGuidanceMessage

	// Plan and historical context.
	ApprovedPlanContext(ctx context.Context, sessionID, planID string) (string, error)
	LoadTurnHistoricalContext(ctx context.Context, sessionID, query string, checkpointBoundary *int64) string

	// Approvals and interactive input.
	AwaitApproval(ctx context.Context, sessionID, agentID, agentType string, run *agentservice.Run, call tool.Call, pending agentservice.PendingApproval) (approvalResolution, error)
	AwaitTeamApproval(ctx context.Context, sessionID, runID, goal string, call tool.Call, definition tool.Definition) (approvalResolution, error)
	ClearAutoReviewTracker(runID string)
	RegisterUserInput(requestID string, live *liveUserInput) bool
	FinishUserInput(live *liveUserInput)

	// Background subagent wake-up.
	CanStartAutoWake(sessionID string) bool
	StartSubagentAutoWake(runs []agentservice.SubagentRun) error
	RunningBackgroundChildren(sessionID, parentRunID string) []agentservice.SubagentRun
}

func (s *Service) BaseContext() context.Context { return s.ctx }

func (s *Service) Sessions() *session.Service { return s.sessions }

func (s *Service) EmitEvent(ctx context.Context, event Event) bool { return s.emit(ctx, event) }

func (s *Service) AttachmentRoot() string { return s.attachments.Root }

func (s *Service) RetryConfig() config.RetryConfig { return s.cfg.Retry }

func (s *Service) HasProviderRuntime() bool { return s.providers != nil }

func (s *Service) ModelMaxOutputTokens(providerID, modelID string) int {
	if s.providers == nil {
		return 0
	}
	return s.providers.modelMaxOutputTokens(providerID, modelID)
}

func (s *Service) ProviderTransport(providerID string) string {
	return s.providerTransport(providerID)
}

func (s *Service) HookMetadata(sessionID, runID string) hooks.Metadata {
	return s.hookMetadata(sessionID, runID)
}

func (s *Service) HookDispatcher() hooks.Dispatcher { return s.hooks }

func (s *Service) DispatchLifecycleHook(ctx context.Context, event hooks.Event, metadata hooks.Metadata, add func(*hooks.Envelope)) error {
	return s.dispatchLifecycle(ctx, event, metadata, add)
}

func (s *Service) AutoCompactHooks(metadata hooks.Metadata) func(context.Context, []message.Message, []message.Message, error) error {
	return s.autoCompactHooks(metadata)
}

func (s *Service) StopHookGuardrail(metadata hooks.Metadata, event hooks.Event, transcriptPath func(hyagent.OutputGuardrailInput) string) hyagent.OutputGuardrail {
	return s.stopHookGuardrail(metadata, event, transcriptPath)
}

func (s *Service) EmitTodoUpdated(sessionID string, todo session.TodoList) bool {
	return s.emitTodoUpdated(sessionID, todo)
}

// ClaimActiveRun atomically claims run ownership for a resumed run. It
// preserves the historical claim semantics: the session binding is only
// updated when provided, and the guidance channel only opens when requested.
func (s *Service) ClaimActiveRun(runID, sessionID string, cancel context.CancelFunc, openGuidance bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeRun != "" {
		return ErrRunActive
	}
	s.activeRun = runID
	if sessionID != "" {
		s.activeSession = sessionID
	}
	s.activeEnd = cancel
	s.activeCancelIntent = ""
	if openGuidance {
		s.guidanceOpen = true
	}
	return nil
}

func (s *Service) ClearRun(runID string) { s.clearRun(runID) }

func (s *Service) BindProviderEngine(engine hyagent.Engine) hyagent.Engine {
	return s.bindProviderEngine(engine)
}

func (s *Service) SpawnProviderTurn(ctx context.Context, request TurnRequest, run *agentservice.Run, engine hyagent.Engine) {
	s.wg.Add(1)
	go s.runProviderTurn(ctx, request, run, engine)
}

func (s *Service) SpawnResumedProviderTeam(ctx context.Context, request TurnRequest, runID, recapGoal string, resolution teamProviderResolution) {
	s.wg.Add(1)
	go s.runResumedProviderTeam(ctx, request, runID, recapGoal, resolution)
}

func (s *Service) PeekActiveGuidance(sessionID, runID string) activeGuidanceSnapshot {
	return s.peekActiveGuidance(sessionID, runID)
}

func (s *Service) AcknowledgeActiveGuidance(sessionID, runID string, snapshot activeGuidanceSnapshot) {
	s.acknowledgeActiveGuidance(sessionID, runID, snapshot)
}

func (s *Service) FinishActiveGuidance(sessionID, runID string) []activeGuidanceMessage {
	return s.finishActiveGuidance(sessionID, runID)
}

func (s *Service) ApprovedPlanContext(ctx context.Context, sessionID, planID string) (string, error) {
	return s.approvedPlanContext(ctx, sessionID, planID)
}

func (s *Service) LoadTurnHistoricalContext(ctx context.Context, sessionID, query string, checkpointBoundary *int64) string {
	return s.loadTurnHistoricalContext(ctx, sessionID, query, checkpointBoundary)
}

func (s *Service) AwaitApproval(ctx context.Context, sessionID, agentID, agentType string, run *agentservice.Run, call tool.Call, pending agentservice.PendingApproval) (approvalResolution, error) {
	return s.awaitApproval(ctx, sessionID, agentID, agentType, run, call, pending)
}

func (s *Service) AwaitTeamApproval(ctx context.Context, sessionID, runID, goal string, call tool.Call, definition tool.Definition) (approvalResolution, error) {
	return s.awaitTeamApproval(ctx, sessionID, runID, goal, call, definition)
}

// RegisterUserInput records a live interactive question. It returns false on
// an ID collision so the caller can fail the tool without racing another
// question that already owns the identifier.
func (s *Service) RegisterUserInput(requestID string, live *liveUserInput) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.liveUserInputs[requestID]; exists {
		return false
	}
	s.liveUserInputs[requestID] = live
	return true
}

func (s *Service) ClearAutoReviewTracker(runID string) { s.clearAutoReviewTracker(runID) }

func (s *Service) FinishUserInput(live *liveUserInput) { s.finishUserInput(live) }

func (s *Service) CanStartAutoWake(sessionID string) bool { return s.canStartAutoWake(sessionID) }

func (s *Service) StartSubagentAutoWake(runs []agentservice.SubagentRun) error {
	return s.startSubagentAutoWake(runs)
}

func (s *Service) RunningBackgroundChildren(sessionID, parentRunID string) []agentservice.SubagentRun {
	if s.providers == nil {
		return nil
	}
	return s.providers.RunningBackgroundChildren(sessionID, parentRunID)
}
