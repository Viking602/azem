package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/codex"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

type governedAgentTool struct {
	definition       tool.Definition
	driver           tool.Driver
	coding           *agentservice.Service
	run              *agentservice.Run
	host             providerHost
	sessionID        string
	agentID          string
	agentType        string
	parentToolCallID string
	streamRunID      string
	update           func(tool.Update)
	approvalGate     *approvalGate
}

type preparedGovernedCall struct {
	result agentservice.ExecutionResult
	ready  bool
}

type approvalGate struct {
	mu       sync.Mutex
	tools    map[string]*governedAgentTool
	prepared map[string]preparedGovernedCall
}

type suspendableApprovalHost interface {
	awaitSuspendableApproval(context.Context, string, string, string, *agentservice.Run, tool.Call, agentservice.PendingApproval) (approvalResolution, error)
}

func governedOperationKey(call tool.Call) string {
	if strings.TrimSpace(call.OperationID) != "" {
		return call.OperationID
	}
	return call.ID
}

func (d *governedAgentTool) invocationContext(ctx context.Context) context.Context {
	return agentservice.WithInvocation(ctx, agentservice.Invocation{
		SessionID:        d.sessionID,
		RunID:            firstNonempty(d.streamRunID, d.run.RunID),
		TeamRunID:        firstNonempty(d.streamRunID, d.run.RunID),
		AgentID:          firstNonempty(d.agentID, "main"),
		ParentToolCallID: d.parentToolCallID,
		TaskID:           d.run.TaskID,
	})
}

func (gate *approvalGate) store(call tool.Call, prepared preparedGovernedCall) {
	gate.mu.Lock()
	gate.prepared[governedOperationKey(call)] = prepared
	gate.mu.Unlock()
}

func (gate *approvalGate) take(call tool.Call) (preparedGovernedCall, bool) {
	key := governedOperationKey(call)
	gate.mu.Lock()
	prepared, ok := gate.prepared[key]
	delete(gate.prepared, key)
	gate.mu.Unlock()
	return prepared, ok
}

func (*approvalGate) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	return messages, nil
}

func (*approvalGate) BeforeModelCall(context.Context, *hyprovider.Request) error { return nil }

func (gate *approvalGate) BeforeToolCall(ctx context.Context, call *tool.Call) error {
	if call == nil {
		return fmt.Errorf("tool call is nil")
	}
	driver := gate.tools[call.Name]
	if driver == nil {
		return nil
	}
	ctx = driver.invocationContext(ctx)
	policyDriver := driver.driver
	if hooks.PreToolPermissionFromContext(ctx) == "ask" && policyDriver != nil {
		policyDriver = approvalRequiredDriver{inner: policyDriver}
	}
	execution, ready, err := driver.coding.PrepareDriver(ctx, driver.run, policyDriver, *call)
	if err != nil {
		return err
	}
	if !ready && execution.Approval != nil {
		if driver.host == nil {
			return errors.New("approval UI is unavailable")
		}
		var resolution approvalResolution
		if suspendable, ok := driver.host.(suspendableApprovalHost); ok {
			resolution, err = suspendable.awaitSuspendableApproval(
				ctx, driver.sessionID, driver.agentID, driver.agentType, driver.run, *call, *execution.Approval,
			)
		} else {
			resolution, err = driver.host.AwaitApproval(
				ctx, driver.sessionID, driver.agentID, driver.agentType, driver.run, *call, *execution.Approval,
			)
		}
		if err != nil {
			return err
		}
		if resolution.Mode == agentservice.ApprovalDenied {
			if resolution.Prevent {
				return hooks.ErrPreventContinuation
			}
			text := firstNonempty(resolution.DenialMessage, "Denied by user")
			if resolution.Retry {
				text += " PermissionDenied hook permits retrying this tool request."
			}
			execution = agentservice.ExecutionResult{Result: tool.Result{
				ToolCallID: call.ID, Name: call.Name, Content: text, IsError: true,
			}}
			ready = false
		} else {
			execution, ready, err = driver.coding.PrepareDriver(ctx, driver.run, driver.driver, *call)
			if err != nil {
				return err
			}
			if !ready && execution.Approval != nil {
				return errors.New("approval remained pending after resolution")
			}
		}
	}
	gate.store(*call, preparedGovernedCall{result: execution, ready: ready})
	return nil
}

func (*approvalGate) AfterToolCall(context.Context, *tool.Result) error { return nil }
func (*approvalGate) OnEvent(context.Context, hyprovider.Event) error   { return nil }

type callerToolDriver struct {
	inner  tool.Driver
	caller agentservice.Invocation
}

func (d callerToolDriver) Definition() tool.Definition { return d.inner.Definition() }

func (d callerToolDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	descriptor, err := agentservice.DescribeTool(d.inner)
	if err != nil {
		return agentruntime.ToolPolicy{
			Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
			RequiresActionTask: true, RiskLevel: "high",
		}
	}
	return descriptor.PolicyForCall(call)
}

func (d callerToolDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	return d.inner.Execute(agentservice.WithInvocation(ctx, d.caller), call, sink)
}

func (d *governedAgentTool) Definition() tool.Definition { return d.definition }

func (d *governedAgentTool) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	descriptor, err := agentservice.DescribeTool(d.driver)
	if err != nil {
		return agentruntime.ToolPolicy{
			Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
			RequiresActionTask: true, RiskLevel: "high",
		}
	}
	return descriptor.PolicyForCall(call)
}

func (d *governedAgentTool) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	ctx = d.invocationContext(ctx)
	updates := func(update tool.Update) error {
		if d.approvalGate == nil && d.update != nil {
			d.update(update)
		}
		if d.approvalGate == nil && d.host != nil {
			runID := firstNonempty(d.streamRunID, d.run.RunID)
			state := string(update.Kind)
			if update.Kind == tool.UpdateProgress && update.Message == "running" {
				state = "running"
			}
			if !d.host.EmitEvent(ctx, Event{
				Kind: EventToolUpdate, SessionID: d.sessionID, RunID: runID, AgentID: d.agentID,
				ToolCallID: call.ID, State: state, Text: update.Message,
				Data: childFrameData("child:"+d.agentID, d.parentToolCallID, update.Data),
			}) {
				return eventDeliveryError(ctx)
			}
		}
		if sink != nil {
			return sink(update)
		}
		return nil
	}
	var execution agentservice.ExecutionResult
	var ready bool
	var err error
	if d.approvalGate != nil {
		var found bool
		prepared, ok := d.approvalGate.take(call)
		if ok {
			execution, ready, found = prepared.result, prepared.ready, true
		}
		if !found {
			return tool.Result{
				ToolCallID: call.ID, Name: call.Name, Content: "tool governance was not prepared", IsError: true,
			}, errors.Join(tool.ErrNotExecuted, errors.New("approval hook did not prepare the operation"))
		}
	} else {
		policyDriver := d.driver
		if hooks.PreToolPermissionFromContext(ctx) == "ask" && d.driver != nil {
			policyDriver = approvalRequiredDriver{inner: d.driver}
		}
		execution, ready, err = d.coding.PrepareDriver(ctx, d.run, policyDriver, call)
		if err != nil {
			return execution.Result, errors.Join(tool.ErrNotExecuted, err)
		}
		if !ready && execution.Approval != nil {
			if d.host == nil {
				return tool.Result{
					ToolCallID: call.ID, Name: call.Name, Content: "approval UI is unavailable", IsError: true,
				}, nil
			}
			resolution, approvalErr := d.host.AwaitApproval(ctx, d.sessionID, d.agentID, d.agentType, d.run, call, *execution.Approval)
			if approvalErr != nil {
				return tool.Result{
					ToolCallID: call.ID, Name: call.Name, Content: approvalErr.Error(), IsError: true,
				}, errors.Join(tool.ErrNotExecuted, approvalErr)
			}
			if resolution.Mode == agentservice.ApprovalDenied {
				if resolution.Prevent {
					return tool.Result{
						ToolCallID: call.ID, Name: call.Name, IsError: true, Content: "Hook prevented continuation",
					}, errors.Join(tool.ErrNotExecuted, hooks.ErrPreventContinuation)
				}
				text := firstNonempty(resolution.DenialMessage, "Denied by user")
				if resolution.Retry {
					text += " PermissionDenied hook permits retrying this tool request."
				}
				return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: text, IsError: true}, nil
			}
			execution, ready, err = d.coding.PrepareDriver(ctx, d.run, d.driver, call)
			if err != nil {
				return execution.Result, errors.Join(tool.ErrNotExecuted, err)
			}
			if !ready && execution.Approval != nil {
				return tool.Result{}, errors.Join(tool.ErrNotExecuted, errors.New("approval remained pending after resolution"))
			}
		}
	}
	if !ready {
		return execution.Result, nil
	}
	if updateErr := updates(tool.Update{Kind: tool.UpdateProgress, Message: "running"}); updateErr != nil {
		return tool.Result{
			ToolCallID: call.ID, Name: call.Name, Content: updateErr.Error(), IsError: true,
		}, updateErr
	}
	executed, executeErr := d.coding.ExecutePreparedDriver(ctx, d.run, d.driver, call, updates)
	return spillAgentToolResult(ctx, d.spillStore(), d.sessionID, firstNonempty(d.streamRunID, d.run.RunID), executed.Result), executeErr
}

// spillStore returns the durable artifact store used to spill oversized tool
// results, or nil when the host is unavailable (tests, degraded runtime), in
// which case spilling falls back to plain truncation.
func (d *governedAgentTool) spillStore() *session.Service {
	if d.host == nil {
		return nil
	}
	return d.host.Sessions()
}

func (d *governedAgentTool) execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (agentservice.ExecutionResult, error) {
	if d.driver != nil {
		return d.coding.ExecuteDriver(ctx, d.run, d.driver, call, sink)
	}
	return d.coding.ExecuteTool(ctx, d.run, call, sink)
}

type approvalReviewRequest struct {
	Goal            string
	AgentID         string
	AgentType       string
	ToolName        string
	Arguments       json.RawMessage
	Target          string
	Effect          string
	Risk            string
	RequestedAction string
	RequestedReason string
}

func (r approvalReviewRequest) codexRequest() (codex.ApprovalReviewRequest, error) {
	if len(r.Arguments) == 0 || !json.Valid(r.Arguments) {
		return codex.ApprovalReviewRequest{}, fmt.Errorf("tool arguments are not valid JSON")
	}
	return codex.ApprovalReviewRequest{
		Goal: r.Goal, AgentID: r.AgentID, AgentType: r.AgentType, ToolName: r.ToolName,
		Arguments: r.Arguments, Target: r.Target, Effect: r.Effect, Risk: r.Risk,
		RequestedAction: r.RequestedAction, RequestedReason: r.RequestedReason,
	}, nil
}

func runApprovalReviewRequest(run *agentservice.Run, agentID, agentType string, call tool.Call, pending agentservice.PendingApproval) approvalReviewRequest {
	return approvalReviewRequest{
		Goal: run.Goal, AgentID: agentID, AgentType: agentType, ToolName: call.Name,
		Arguments: call.Arguments, Target: pending.Scope.Target, Effect: pending.Effect, Risk: pending.Scope.Risk,
		RequestedAction: pending.Request.RequestedAction, RequestedReason: pending.Request.Reason,
	}
}

func durableApprovalKey(call tool.Call) string {
	return firstNonempty(call.OperationID, call.ID)
}

func teamApprovalReviewRequest(goal, runID string, call tool.Call, policy agentruntime.ToolPolicy) approvalReviewRequest {
	target := teamToolTarget(call)
	risk := firstNonempty(policy.RiskLevel, "medium")
	if call.Name == agentservice.ToolShell {
		var input struct {
			Command string `json:"command"`
			Network bool   `json:"network"`
		}
		_ = json.Unmarshal(call.Arguments, &input)
		risk = agentservice.ClassifyShellRisk(input.Command, input.Network)
	}
	return approvalReviewRequest{
		Goal: goal, AgentID: runID, AgentType: "team", ToolName: call.Name, Arguments: call.Arguments,
		Target: target, Effect: string(policy.Effect), Risk: risk,
		RequestedAction: call.Name + " · " + target, RequestedReason: "team agent requested a governed tool action",
	}
}

type approvalResolution struct {
	Mode              agentservice.ApprovalMode
	DenialMessage     string
	NeedsUserApproval bool
	Retry             bool
	Prevent           bool
}

type permissionHookDecision struct {
	behavior  string
	message   string
	interrupt bool
	name      string
}

func (s *Service) permissionHook(ctx context.Context, metadata hooks.Metadata, call tool.Call) permissionHookDecision {
	s.mu.Lock()
	mode := claudePermissionMode(s.approvalMode)
	s.mu.Unlock()
	result := s.hooks.Dispatch(ctx, hooks.Envelope{
		SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID, AgentType: metadata.AgentType,
		ParentRunID: metadata.ParentRunID, ParentToolCallID: metadata.ParentToolCallID, CWD: metadata.CWD,
		HookEventName: hooks.PermissionRequest, ToolCallID: call.ID, ToolUseID: call.ID, ToolName: call.Name,
		ToolInput: call.Arguments, PermissionMode: mode,
	})
	if result.PreventContinuation {
		return permissionHookDecision{behavior: "deny", message: result.StopReason, interrupt: true, name: "continue:false"}
	}
	if result.Denied {
		name := "unknown"
		if len(result.Runs) > 0 {
			name = result.Runs[len(result.Runs)-1].Name
		}
		return permissionHookDecision{behavior: "deny", message: result.Reason, interrupt: true, name: name}
	}
	var allowed permissionHookDecision
	for _, run := range result.Runs {
		decision := run.Output.HookSpecificOutput.Decision
		behavior := strings.ToLower(strings.TrimSpace(decision.Behavior))
		if behavior == "" || behavior == "ask" {
			continue
		}
		if behavior == "allow" && len(decision.UpdatedInput) > 0 && string(decision.UpdatedInput) != string(call.Arguments) {
			return permissionHookDecision{behavior: "deny", message: "PermissionRequest updatedInput is not supported because the modified input has not passed approval policy validation", interrupt: true, name: run.Name}
		}
		if behavior == "allow" && decision.UpdatedPermissions != nil {
			return permissionHookDecision{behavior: "deny", message: "PermissionRequest updatedPermissions is not supported by this permission store", interrupt: true, name: run.Name}
		}
		if behavior == "deny" {
			return permissionHookDecision{behavior: behavior, message: decision.Message, interrupt: decision.Interrupt, name: run.Name}
		}
		if behavior == "allow" && allowed.behavior == "" {
			allowed = permissionHookDecision{behavior: behavior, message: decision.Message, interrupt: decision.Interrupt, name: run.Name}
		}
	}
	return allowed
}

func claudePermissionMode(mode ApprovalMode) string {
	if mode == ApprovalModeYolo {
		return "bypassPermissions"
	}
	return "default"
}

func (s *Service) observePermissionDenied(ctx context.Context, metadata hooks.Metadata, call tool.Call, reason string, interrupt bool) (bool, bool) {
	result := s.hooks.Dispatch(ctx, hooks.Envelope{
		SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID,
		AgentType: metadata.AgentType, CWD: metadata.CWD, HookEventName: hooks.PermissionDenied,
		ToolCallID: call.ID, ToolUseID: call.ID, ToolName: call.Name, ToolInput: call.Arguments, Reason: reason, IsInterrupt: interrupt,
	})
	if result.PreventContinuation {
		return false, true
	}
	for _, run := range result.Runs {
		if run.Output.HookSpecificOutput.Retry {
			return true, false
		}
	}
	return false, false
}

type autoReviewDenialTracker struct {
	recent          [50]bool
	reviewCount     int
	next            int
	recentDenials   int
	consecutiveDeny int
}

type AutoReviewDenialLimitError struct {
	RunID              string
	ConsecutiveDenials int
	RecentDenials      int
}

func (e *AutoReviewDenialLimitError) Error() string {
	return fmt.Sprintf(
		"automatic approval review denied too many actions for run %s (%d consecutive, %d in the last 50 reviews)",
		e.RunID, e.ConsecutiveDenials, e.RecentDenials,
	)
}

type teamApprovalDriver struct {
	inner     tool.Driver
	coding    *agentservice.Service
	host      providerHost
	sessionID string
	runID     string
	goal      string
	recovery  *agentservice.EditRecovery
}

type approvalRequiredDriver struct{ inner tool.Driver }

func (d approvalRequiredDriver) Definition() tool.Definition { return d.inner.Definition() }

func (d approvalRequiredDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	descriptor, err := agentservice.DescribeTool(d.inner)
	if err != nil {
		return agentruntime.ToolPolicy{
			Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
			RequiresActionTask: true, RiskLevel: "high",
		}
	}
	policy := descriptor.PolicyForCall(call)
	policy.RequiresApproval = true
	return policy
}

func (d approvalRequiredDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	return d.inner.Execute(ctx, call, sink)
}

func (d *teamApprovalDriver) Definition() tool.Definition { return d.inner.Definition() }

func (d *teamApprovalDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	descriptor, err := agentservice.DescribeTool(d.inner)
	if err != nil {
		return agentruntime.ToolPolicy{
			Effect: agentruntime.ToolEffectExternalSideEffect, RequiresApproval: true,
			RequiresActionTask: true, RiskLevel: "high",
		}
	}
	return descriptor.PolicyForCall(call)
}

func (d *teamApprovalDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	descriptor, err := agentservice.DescribeTool(d.inner)
	if err != nil {
		return tool.Result{}, errors.Join(tool.ErrNotExecuted, err)
	}
	policy := descriptor.PolicyForCall(call)
	if blocked, required := d.recovery.BlockedEdit(call); required {
		return blocked, nil
	}
	if hooks.PreToolPermissionFromContext(ctx) == "ask" || teamToolRequiresApproval(policy, call) {
		resolution, approvalErr := d.host.AwaitTeamApproval(ctx, d.sessionID, d.runID, d.goal, call, policy)
		if approvalErr != nil {
			return tool.Result{
				ToolCallID: call.ID, Name: call.Name, Content: approvalErr.Error(), IsError: true,
			}, errors.Join(tool.ErrNotExecuted, approvalErr)
		}
		if resolution.Mode == agentservice.ApprovalDenied {
			if resolution.Prevent {
				return tool.Result{
					ToolCallID: call.ID, Name: call.Name, IsError: true, Content: "Hook prevented continuation",
				}, errors.Join(tool.ErrNotExecuted, hooks.ErrPreventContinuation)
			}
			message := firstNonempty(resolution.DenialMessage, "Denied by user")
			if resolution.Retry {
				message += " PermissionDenied hook permits retrying this tool request."
			}
			return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: message, IsError: true}, nil
		}
	}
	result, executeErr := d.coding.ExecutePolicyCall(ctx, d.inner, call, sink)
	d.recovery.Observe(call, result, executeErr)
	return result, executeErr
}

func (s *Service) teamToolBus(ctx context.Context, sessionID, runID, goal string, recovery *agentservice.EditRecovery) (*tool.Bus, error) {
	drivers, err := s.coding.WorkspaceDrivers(ctx, s.cfg.Workspace.Root)
	if err != nil {
		return nil, err
	}
	if s.sessions != nil {
		drivers = append(drivers, &todoDriver{sessionID: sessionID, store: s.sessions, emit: func(event Event) bool {
			return s.emitTodoUpdated(sessionID, *event.Todo)
		}})
		drivers = append(drivers, &contextArtifactDriver{sessionID: sessionID, store: s.sessions})
	}
	governed := make([]tool.Driver, 0, len(drivers))
	for _, driver := range drivers {
		approval := &teamApprovalDriver{inner: driver, coding: s.coding, host: s, sessionID: sessionID, runID: runID, goal: goal, recovery: recovery}
		metadata := hooks.Metadata{SessionID: sessionID, RunID: runID, AgentID: "team", AgentType: "team", CWD: s.cfg.Workspace.Root}
		governed = append(governed, hooks.WrapDriver(s.hooks, metadata, approval))
	}
	return tool.NewBus(governed...), nil
}

func teamToolHasSideEffect(policy agentruntime.ToolPolicy) bool {
	return policy.RequiresActionTask ||
		policy.Effect == agentruntime.ToolEffectWrite || policy.Effect == agentruntime.ToolEffectExternalSideEffect
}

func teamToolRequiresApproval(policy agentruntime.ToolPolicy, call tool.Call) bool {
	required := policy.RequiresApproval || policy.RequiresActionTask ||
		policy.Effect == agentruntime.ToolEffectWrite || policy.Effect == agentruntime.ToolEffectExternalSideEffect
	if policy.Metadata["approval"] == "allow" {
		required = policy.Metadata["network"] == "prompt" && toolArgumentsRequestNetwork(call.Arguments)
	}
	return required
}

func toolArgumentsRequestNetwork(arguments json.RawMessage) bool {
	var input struct {
		Network bool `json:"network"`
	}
	return json.Unmarshal(arguments, &input) == nil && input.Network
}

func teamToolTarget(call tool.Call) string {
	var input map[string]any
	if json.Unmarshal(call.Arguments, &input) == nil {
		for _, key := range []string{"path", "command", "query", "url"} {
			if value := strings.TrimSpace(fmt.Sprint(input[key])); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return "workspace"
}

func teamToolFingerprint(policy agentruntime.ToolPolicy, call tool.Call) string {
	return call.Name + "\x00" + string(policy.Effect) + "\x00" + teamToolTarget(call)
}

func (s *Service) awaitTeamApproval(ctx context.Context, sessionID, runID, goal string, call tool.Call, policy agentruntime.ToolPolicy) (approvalResolution, error) {
	if strings.TrimSpace(call.ID) == "" {
		return approvalResolution{}, fmt.Errorf("team tool %q requires a call ID for approval", call.Name)
	}
	approvalID, err := randomID("approval")
	if err != nil {
		return approvalResolution{}, err
	}
	request := teamApprovalReviewRequest(goal, runID, call, policy)
	fingerprint := teamToolFingerprint(policy, call)
	target := request.Target
	action := request.RequestedAction
	event := Event{
		Kind: EventApprovalRequested, SessionID: sessionID, RunID: runID, ToolCallID: call.ID,
		ApprovalID: approvalID, Text: action,
		Data: map[string]string{
			"tool": call.Name, "target": target, "risk": request.Risk, "effect": request.Effect,
			"action": action, "agent_type": "team",
		},
	}
	metadata := hooks.Metadata{SessionID: sessionID, RunID: runID, AgentID: "team", AgentType: "team", CWD: s.cfg.Workspace.Root}
	s.mu.Lock()
	mode := s.approvalMode
	_, granted := s.teamApprovals[fingerprint]
	s.mu.Unlock()
	if mode == ApprovalModeYolo || granted {
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}
	if mode == ApprovalModeAutoReview {
		resolution, approvalErr := s.automaticApproval(ctx, event, request, func(decisionCtx context.Context, mode agentservice.ApprovalMode, decidedBy string) error {
			return s.recordTeamApprovalDecision(decisionCtx, event, request, mode, decidedBy)
		})
		if approvalErr != nil || !resolution.NeedsUserApproval {
			return resolution, approvalErr
		}
	}
	hookDecision := permissionHookDecision{}
	if mode != ApprovalModeAutoReview {
		hookDecision = s.permissionHook(ctx, metadata, call)
	}
	if hookDecision.behavior == "allow" || hookDecision.behavior == "deny" {
		resolvedMode := agentservice.ApprovalOnce
		if hookDecision.behavior == "deny" {
			resolvedMode = agentservice.ApprovalDenied
		}
		if err := s.recordTeamApprovalDecision(ctx, event, request, resolvedMode, "hook:"+hookDecision.name); err != nil {
			return approvalResolution{}, err
		}
		if resolvedMode == agentservice.ApprovalDenied {
			message := firstNonempty(hookDecision.message, "Denied by permission hook")
			retry, prevent := s.observePermissionDenied(ctx, metadata, call, message, hookDecision.interrupt)
			return approvalResolution{Mode: resolvedMode, DenialMessage: message, Retry: retry, Prevent: prevent}, nil
		}
		return approvalResolution{Mode: resolvedMode}, nil
	}
	live := &liveApproval{
		approvalID: approvalID, agentType: "team", runID: runID, callID: call.ID,
		sessionID: sessionID, fingerprint: fingerprint, request: request, decision: make(chan agentservice.ApprovalMode, 1),
	}
	s.mu.Lock()
	if _, exists := s.liveApprovals[approvalID]; exists {
		s.mu.Unlock()
		return approvalResolution{}, fmt.Errorf("approval ID collision")
	}
	s.liveApprovals[approvalID] = live
	s.mu.Unlock()
	defer s.finishLiveApproval(live)
	if !s.emit(ctx, Event{
		Kind: EventToolUpdate, SessionID: sessionID, RunID: runID, ToolCallID: call.ID,
		State: "awaiting_approval",
	}) {
		return approvalResolution{}, eventDeliveryError(ctx)
	}
	event.State = "pending"
	s.notifyHook(s.ctx, metadata, "permission_prompt", "Approval required", action)
	if !s.emit(ctx, event) {
		return approvalResolution{}, eventDeliveryError(ctx)
	}
	select {
	case <-ctx.Done():
		return approvalResolution{}, ctx.Err()
	case resolvedMode := <-live.decision:
		if resolvedMode == agentservice.ApprovalDenied {
			retry, prevent := s.observePermissionDenied(ctx, metadata, call, "Denied by user", false)
			return approvalResolution{Mode: resolvedMode, Retry: retry, Prevent: prevent}, nil
		}
		return approvalResolution{Mode: resolvedMode}, nil
	}
}

func (s *Service) bindProviderEngine(engine hyagent.Engine) hyagent.Engine {
	if engine.Tools == nil {
		return engine
	}
	gate := &approvalGate{
		tools:    make(map[string]*governedAgentTool),
		prepared: make(map[string]preparedGovernedCall),
	}
	bound := make([]tool.Driver, 0)
	for _, definition := range engine.Tools.Definitions() {
		driver, ok := engine.Tools.Driver(definition.Name)
		if !ok {
			continue
		}
		if governed, ok := driver.(*governedAgentTool); ok {
			clone := *governed
			clone.host = s
			clone.approvalGate = gate
			gate.tools[definition.Name] = &clone
			bound = append(bound, &clone)
		} else {
			bound = append(bound, driver)
		}
	}
	engine.Tools = tool.NewBus(bound...)
	if len(gate.tools) > 0 {
		engine.Hooks = engine.Hooks.Prepend(gate)
	}
	return engine
}

func (s *Service) awaitApproval(ctx context.Context, sessionID, agentID, agentType string, run *agentservice.Run, call tool.Call, pending agentservice.PendingApproval) (approvalResolution, error) {
	return s.awaitApprovalMode(ctx, sessionID, agentID, agentType, run, call, pending, false)
}

func (s *Service) awaitSuspendableApproval(ctx context.Context, sessionID, agentID, agentType string, run *agentservice.Run, call tool.Call, pending agentservice.PendingApproval) (approvalResolution, error) {
	return s.awaitApprovalMode(ctx, sessionID, agentID, agentType, run, call, pending, true)
}

func (s *Service) awaitApprovalMode(ctx context.Context, sessionID, agentID, agentType string, run *agentservice.Run, call tool.Call, pending agentservice.PendingApproval, suspendable bool) (approvalResolution, error) {
	approvalID := strings.TrimSpace(pending.Request.ApprovalID)
	if approvalID == "" {
		var err error
		approvalID, err = randomID("approval")
		if err != nil {
			return approvalResolution{}, err
		}
	}
	request := runApprovalReviewRequest(run, agentID, agentType, call, pending)
	event := Event{
		Kind: EventApprovalRequested, SessionID: sessionID, RunID: run.RunID, AgentID: agentID,
		ToolCallID: call.ID, ApprovalID: approvalID, Text: pending.Request.RequestedAction,
		Data: map[string]string{
			"tool": call.Name, "target": pending.Scope.Target, "risk": pending.Scope.Risk,
			"effect": pending.Effect, "action": pending.Request.RequestedAction, "agent_type": agentType,
		},
	}
	metadata := hooks.Metadata{SessionID: sessionID, RunID: run.RunID, AgentID: agentID, AgentType: agentType, CWD: s.cfg.Workspace.Root}
	s.mu.Lock()
	mode := s.approvalMode
	s.mu.Unlock()
	if mode == ApprovalModeYolo {
		if s.coding == nil {
			return approvalResolution{}, fmt.Errorf("coding runtime is unavailable")
		}
		if err := s.coding.ResolveApproval(ctx, run, durableApprovalKey(call), agentservice.ApprovalOnce, "approval-mode:yolo"); err != nil {
			return approvalResolution{}, err
		}
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}
	if mode == ApprovalModeAutoReview {
		resolution, approvalErr := s.automaticApproval(ctx, event, request, func(decisionCtx context.Context, mode agentservice.ApprovalMode, decidedBy string) error {
			if s.coding == nil {
				return fmt.Errorf("coding runtime is unavailable")
			}
			return s.coding.ResolveApproval(decisionCtx, run, durableApprovalKey(call), mode, decidedBy)
		})
		if approvalErr != nil || !resolution.NeedsUserApproval {
			return resolution, approvalErr
		}
	}
	hookDecision := permissionHookDecision{}
	if mode != ApprovalModeAutoReview {
		hookDecision = s.permissionHook(ctx, metadata, call)
	}
	if hookDecision.behavior == "allow" || hookDecision.behavior == "deny" {
		resolvedMode := agentservice.ApprovalOnce
		if hookDecision.behavior == "deny" {
			resolvedMode = agentservice.ApprovalDenied
		}
		if s.coding == nil {
			return approvalResolution{}, fmt.Errorf("coding runtime is unavailable")
		}
		if err := s.coding.ResolveApproval(ctx, run, durableApprovalKey(call), resolvedMode, "hook:"+hookDecision.name); err != nil {
			return approvalResolution{}, err
		}
		if resolvedMode == agentservice.ApprovalDenied {
			message := firstNonempty(hookDecision.message, "Denied by permission hook")
			retry, prevent := s.observePermissionDenied(ctx, metadata, call, message, hookDecision.interrupt)
			return approvalResolution{Mode: resolvedMode, DenialMessage: message, Retry: retry, Prevent: prevent}, nil
		}
		return approvalResolution{Mode: resolvedMode}, nil
	}
	live := &liveApproval{
		approvalID: approvalID, agentID: agentID, agentType: agentType, run: run, runID: run.RunID,
		callID: call.ID, operationID: durableApprovalKey(call), sessionID: sessionID, pending: pending,
		decision: make(chan agentservice.ApprovalMode, 1), suspension: make(chan error, 1), suspendable: suspendable,
	}
	s.mu.Lock()
	if _, exists := s.liveApprovals[approvalID]; exists {
		s.mu.Unlock()
		return approvalResolution{}, fmt.Errorf("approval ID collision")
	}
	if s.shuttingDown {
		s.mu.Unlock()
		return approvalResolution{}, context.Canceled
	}
	s.liveApprovals[approvalID] = live
	s.mu.Unlock()
	handedOff := false
	defer func() {
		if !handedOff {
			s.finishLiveApproval(live)
		}
	}()
	if !s.emit(ctx, Event{
		Kind: EventToolUpdate, SessionID: sessionID, RunID: run.RunID, AgentID: agentID,
		ToolCallID: call.ID, State: "awaiting_approval",
	}) {
		return approvalResolution{}, eventDeliveryError(ctx)
	}
	event.State = "pending"
	s.notifyHook(s.ctx, metadata, "permission_prompt", "Approval required", pending.Request.RequestedAction)
	if !s.emit(ctx, event) {
		return approvalResolution{}, eventDeliveryError(ctx)
	}
	if suspendable {
		s.mu.Lock()
		if s.shuttingDown {
			s.mu.Unlock()
			return approvalResolution{}, context.Canceled
		}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			live.suspension <- s.coding.SuspendRun(s.ctx, run)
		}()
		handedOff = true
		<-ctx.Done()
		return approvalResolution{}, context.Cause(ctx)
	}
	select {
	case <-ctx.Done():
		return approvalResolution{}, context.Cause(ctx)
	case resolvedMode := <-live.decision:
		if resolvedMode == agentservice.ApprovalDenied {
			retry, prevent := s.observePermissionDenied(ctx, metadata, call, "Denied by user", false)
			return approvalResolution{Mode: resolvedMode, Retry: retry, Prevent: prevent}, nil
		}
		return approvalResolution{Mode: resolvedMode}, nil
	}
}

func (s *Service) recordTeamApprovalDecision(
	ctx context.Context,
	event Event,
	request approvalReviewRequest,
	mode agentservice.ApprovalMode,
	decidedBy string,
) error {
	if s.coding == nil {
		return fmt.Errorf("coding runtime is unavailable")
	}
	decision := "rejected"
	if mode == agentservice.ApprovalOnce || mode == agentservice.ApprovalSession {
		decision = "approved"
	}
	return s.coding.AppendEvent(ctx, agentruntime.Event{
		RunID: event.RunID, Type: agentruntime.EventApprovalDecided, RecordedAt: time.Now().UTC(),
		Payload: map[string]any{
			"approvalId": event.ApprovalID, "actionId": event.ToolCallID,
			"decidedBy": decidedBy, "decision": decision, "reason": request.RequestedReason,
			"tool": request.ToolName, "target": request.Target, "risk": request.Risk,
		},
	})
}

func toolStartsWithoutApproval(toolName string) bool {
	switch toolName {
	case agentservice.ToolReadFile, agentservice.ToolListFiles, agentservice.ToolSearch, agentservice.ToolGitDiff:
		return true
	default:
		return false
	}
}

func (s *Service) toolStartState(toolName string) string {
	if s == nil {
		return "running"
	}
	s.mu.Lock()
	mode := s.approvalMode
	s.mu.Unlock()
	if mode == ApprovalModeYolo || toolStartsWithoutApproval(toolName) {
		return "running"
	}
	if mode == ApprovalModeAutoReview {
		return "reviewing_approval"
	}
	return "queued"
}

type prefetchedAutoReview struct {
	done       chan struct{}
	assessment codex.ApprovalReview
	err        error
}

func autoReviewCacheKey(runID, toolCallID string) string {
	return runID + "\x00" + toolCallID
}

func (s *Service) prefetchAutoReview(ctx context.Context, sessionID, runID, toolCallID, toolName string, arguments json.RawMessage) {
	if s == nil || strings.TrimSpace(toolCallID) == "" || toolStartsWithoutApproval(toolName) {
		return
	}
	if classifyApprovalRisk(approvalReviewRequest{ToolName: toolName, Arguments: arguments, Risk: "medium"}) == "low" {
		return
	}
	if len(arguments) == 0 || !json.Valid(arguments) {
		return
	}
	s.mu.Lock()
	if s.approvalMode != ApprovalModeAutoReview {
		s.mu.Unlock()
		return
	}
	if s.autoReviews == nil {
		s.autoReviews = map[string]*prefetchedAutoReview{}
	}
	key := autoReviewCacheKey(runID, toolCallID)
	if _, exists := s.autoReviews[key]; exists {
		s.mu.Unlock()
		return
	}
	pending := &prefetchedAutoReview{done: make(chan struct{})}
	s.autoReviews[key] = pending
	s.mu.Unlock()

	reviewCtx := s.ctx
	if ctx != nil && ctx.Err() == nil {
		reviewCtx = ctx
	}
	go s.runPrefetchedAutoReview(reviewCtx, pending, sessionID, runID, toolName, arguments)
}

func (s *Service) runPrefetchedAutoReview(
	ctx context.Context,
	pending *prefetchedAutoReview,
	sessionID, runID, toolName string,
	arguments json.RawMessage,
) {
	defer close(pending.done)
	request := approvalReviewRequest{ToolName: toolName, Arguments: arguments}
	providerRequest, err := request.codexRequest()
	if err != nil {
		pending.err = err
		return
	}
	if s.providers == nil {
		pending.err = fmt.Errorf("provider runtime is unavailable")
		return
	}
	reviewer, err := s.providers.ApprovalReviewer(ctx, sessionID, runID)
	if err != nil {
		pending.err = err
		return
	}
	pending.assessment, pending.err = reviewer.Review(ctx, providerRequest)
}

func (s *Service) consumePrefetchedReview(ctx context.Context, runID, toolCallID string) (codex.ApprovalReview, error, bool) {
	if s == nil || strings.TrimSpace(toolCallID) == "" {
		return codex.ApprovalReview{}, nil, false
	}
	s.mu.Lock()
	pending := s.autoReviews[autoReviewCacheKey(runID, toolCallID)]
	s.mu.Unlock()
	if pending == nil {
		return codex.ApprovalReview{}, nil, false
	}
	select {
	case <-ctx.Done():
		return codex.ApprovalReview{}, ctx.Err(), true
	case <-pending.done:
	}
	s.mu.Lock()
	delete(s.autoReviews, autoReviewCacheKey(runID, toolCallID))
	s.mu.Unlock()
	return pending.assessment, pending.err, true
}

func (s *Service) emitToolReviewing(ctx context.Context, sessionID, runID, agentID, toolCallID string) error {
	if !s.emit(ctx, Event{
		Kind: EventToolUpdate, SessionID: sessionID, RunID: runID, AgentID: agentID,
		ToolCallID: toolCallID, State: "reviewing_approval",
	}) {
		return eventDeliveryError(ctx)
	}
	return nil
}

func (s *Service) automaticApproval(
	ctx context.Context,
	event Event,
	request approvalReviewRequest,
	decide func(context.Context, agentservice.ApprovalMode, string) error,
) (approvalResolution, error) {
	if err := s.emitToolReviewing(ctx, event.SessionID, event.RunID, event.AgentID, event.ToolCallID); err != nil {
		return approvalResolution{}, err
	}
	event.State = "reviewing"
	event.Data["reviewer"] = codex.ApprovalReviewerModel
	if s.providers != nil {
		event.Data["reviewer"] = firstNonempty(s.providers.approvalModelRoute(ctx, event.SessionID).Model, event.Data["reviewer"])
	}
	if !s.emit(ctx, event) {
		return approvalResolution{}, eventDeliveryError(ctx)
	}
	request.Risk = classifyApprovalRisk(request)
	if request.Risk == "low" {
		return s.finishHostAutomaticApproval(ctx, event, request, decide)
	}

	providerRequest, err := request.codexRequest()
	failureKind := codex.ReviewFailureInvalidRequest
	var assessment codex.ApprovalReview
	if err == nil {
		if cached, cachedErr, ok := s.consumePrefetchedReview(ctx, event.RunID, event.ToolCallID); ok {
			assessment, err = cached, cachedErr
		} else if s.providers == nil {
			err = fmt.Errorf("provider runtime is unavailable")
			failureKind = codex.ReviewFailureProvider
		} else {
			var reviewer *codex.Reviewer
			reviewer, err = s.providers.ApprovalReviewer(ctx, event.SessionID, event.RunID)
			if err == nil {
				assessment, err = reviewer.Review(ctx, providerRequest)
			}
		}
		if err != nil {
			failureKind = codex.ReviewFailure(err)
		}
	}

	if err != nil {
		if ctx.Err() != nil {
			return approvalResolution{}, ctx.Err()
		}
		if failureKind == codex.ReviewFailureCancelled {
			return approvalResolution{}, err
		}
		state := "auto_failed"
		message := fmt.Sprintf("Automatic review failed (%s); action did not run.", failureKind)
		detail := strings.TrimSpace(err.Error())
		rationale := "Automatic approval review failed (" + string(failureKind) + "): " + detail
		if strings.HasPrefix(detail, "automatic approval review failed") {
			rationale = "Automatic" + strings.TrimPrefix(detail, "automatic")
		}
		rationale = boundedReviewText(rationale, 600)
		if failureKind == codex.ReviewFailureTimeout {
			state = "auto_timed_out"
			message = "Automatic review timed out after 3 model attempts; user confirmation is required before this action can run."
			rationale = "Automatic approval review timed out across 3 model attempts while evaluating the requested action."
			s.recordAutoReview(event.RunID, false)
			if !s.emitAutomaticApprovalResolved(event, state, "high", "unknown", rationale, string(failureKind), message) {
				return approvalResolution{}, errors.Join(err, eventDeliveryError(ctx))
			}
			return approvalResolution{NeedsUserApproval: true, DenialMessage: message}, nil
		}
		decisionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		decisionErr := decide(decisionCtx, agentservice.ApprovalDenied, "system:auto-review-failure")
		cancel()
		s.recordAutoReview(event.RunID, false)
		// Reviewer infrastructure failures are fail-closed assessments, not a
		// successful classification of the request's original risk.
		delivered := s.emitAutomaticApprovalResolved(event, state, "high", "unknown", rationale, string(failureKind), message)
		if decisionErr != nil {
			return approvalResolution{}, errors.Join(err, fmt.Errorf("record fail-closed approval decision: %w", decisionErr))
		}
		if !delivered {
			return approvalResolution{}, errors.Join(err, eventDeliveryError(ctx))
		}
		return approvalResolution{Mode: agentservice.ApprovalDenied, DenialMessage: message}, nil
	}

	assessment = codex.ApplyGuardianOutcome(assessment, reviewHostAuthorization(request))
	rationale := boundedReviewText(assessment.Rationale, 600)
	event.Data["reviewer"] = firstNonempty(assessment.Model, event.Data["reviewer"])
	if assessment.Outcome == "allow" {
		decisionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		decisionErr := decide(decisionCtx, agentservice.ApprovalOnce, event.Data["reviewer"])
		cancel()
		s.recordAutoReview(event.RunID, false)
		if decisionErr != nil {
			message := "Automatic review could not record approval; action did not run."
			_ = s.emitAutomaticApprovalResolved(event, "auto_failed", assessment.RiskLevel, assessment.UserAuthorization, rationale, "decision", message)
			return approvalResolution{}, fmt.Errorf("record automatic approval: %w", decisionErr)
		}
		message := "Approved by automatic review: " + rationale
		if !s.emitAutomaticApprovalResolved(event, "auto_approved", assessment.RiskLevel, assessment.UserAuthorization, rationale, "", message) {
			return approvalResolution{}, eventDeliveryError(ctx)
		}
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}

	limitErr := s.recordAutoReview(event.RunID, true)
	message := "Denied by automatic review: " + rationale +
		"\nUser confirmation is required before this action can run."
	if limitErr != nil {
		message += "\nRepeated automatic denials were detected; automatic execution remains paused for user review."
	}
	if !s.emitAutomaticApprovalResolved(event, "auto_denied", assessment.RiskLevel, assessment.UserAuthorization, rationale, "", message) {
		return approvalResolution{}, eventDeliveryError(ctx)
	}
	return approvalResolution{NeedsUserApproval: true, DenialMessage: message}, nil
}

func classifyApprovalRisk(request approvalReviewRequest) string {
	if request.ToolName == agentservice.ToolShell {
		var input struct {
			Command string `json:"command"`
			Network bool   `json:"network"`
		}
		_ = json.Unmarshal(request.Arguments, &input)
		return agentservice.ClassifyShellRisk(input.Command, input.Network)
	}
	return firstNonempty(request.Risk, "medium")
}

func reviewHostAuthorization(request approvalReviewRequest) string {
	var input struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(request.Arguments, &input)
	return codex.ScoreUserAuthorization(request.Goal, request.ToolName, request.Target, input.Command)
}

func (s *Service) finishHostAutomaticApproval(
	ctx context.Context,
	event Event,
	request approvalReviewRequest,
	decide func(context.Context, agentservice.ApprovalMode, string) error,
) (approvalResolution, error) {
	rationale := "Host classified this action as low-risk under the Codex guardian policy."
	event.Data["reviewer"] = "host:guardian-policy"
	decisionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	decisionErr := decide(decisionCtx, agentservice.ApprovalOnce, event.Data["reviewer"])
	cancel()
	s.recordAutoReview(event.RunID, false)
	if decisionErr != nil {
		message := "Automatic review could not record approval; action did not run."
		_ = s.emitAutomaticApprovalResolved(event, "auto_failed", request.Risk, "high", rationale, "decision", message)
		return approvalResolution{}, fmt.Errorf("record automatic approval: %w", decisionErr)
	}
	message := "Approved by automatic review: " + rationale
	if !s.emitAutomaticApprovalResolved(event, "auto_approved", request.Risk, "high", rationale, "", message) {
		return approvalResolution{}, eventDeliveryError(ctx)
	}
	return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
}

func (s *Service) emitAutomaticApprovalResolved(event Event, state, risk, authorization, rationale, errorKind, text string) bool {
	return s.emit(s.ctx, Event{
		Kind: EventApprovalResolved, SessionID: event.SessionID, RunID: event.RunID, AgentID: event.AgentID,
		ToolCallID: event.ToolCallID, ApprovalID: event.ApprovalID, State: state, Text: text,
		Data: map[string]string{
			"reviewer": firstNonempty(event.Data["reviewer"], codex.ApprovalReviewerModel), "risk": risk, "user_authorization": authorization,
			"rationale": rationale, "error_kind": errorKind, "tool": event.Data["tool"],
			"target": event.Data["target"], "effect": event.Data["effect"],
		},
	})
}

func (s *Service) recordAutoReview(runID string, denied bool) error {
	if strings.TrimSpace(runID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tracker := s.autoReviewDenials[runID]
	if tracker == nil {
		tracker = &autoReviewDenialTracker{}
		s.autoReviewDenials[runID] = tracker
	}
	if tracker.reviewCount == len(tracker.recent) {
		if tracker.recent[tracker.next] {
			tracker.recentDenials--
		}
	} else {
		tracker.reviewCount++
	}
	tracker.recent[tracker.next] = denied
	tracker.next = (tracker.next + 1) % len(tracker.recent)
	if denied {
		tracker.recentDenials++
		tracker.consecutiveDeny++
	} else {
		tracker.consecutiveDeny = 0
	}
	if tracker.consecutiveDeny >= 3 || tracker.recentDenials >= 10 {
		return &AutoReviewDenialLimitError{
			RunID: runID, ConsecutiveDenials: tracker.consecutiveDeny, RecentDenials: tracker.recentDenials,
		}
	}
	return nil
}

func (s *Service) clearAutoReviewTracker(runID string) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.mu.Lock()
	delete(s.autoReviewDenials, runID)
	s.mu.Unlock()
}

func boundedReviewText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func (s *Service) setApprovalMode(ctx context.Context, mode ApprovalMode) error {
	if mode != ApprovalModePrompt && mode != ApprovalModeAutoReview && mode != ApprovalModeYolo {
		return fmt.Errorf("invalid approval mode %q", mode)
	}
	s.mu.Lock()
	previousMode := s.approvalMode
	s.approvalMode = mode
	if mode == ApprovalModePrompt || mode == ApprovalModeAutoReview {
		s.mu.Unlock()
		if err := s.persistApprovalMode(mode); err != nil {
			s.mu.Lock()
			s.approvalMode = previousMode
			s.mu.Unlock()
			s.emitApprovalMode(s.ctx)
			return err
		}
		s.emitApprovalMode(s.ctx)
		return nil
	}
	approvalIDs := make([]string, 0, len(s.liveApprovals))
	for approvalID, live := range s.liveApprovals {
		if !live.resolving && !live.resolved {
			approvalIDs = append(approvalIDs, approvalID)
		}
	}
	s.mu.Unlock()

	var failures []error
	for _, approvalID := range approvalIDs {
		resolved, err := s.resolveLiveApproval(ctx, approvalID, "once", "approval-mode:yolo")
		if err == nil || !resolved {
			continue
		}
		s.mu.Lock()
		live := s.liveApprovals[approvalID]
		superseded := live == nil || live.resolving || live.resolved
		s.mu.Unlock()
		if !superseded {
			failures = append(failures, err)
		}
	}
	err := errors.Join(failures...)
	if err != nil {
		s.mu.Lock()
		if s.approvalMode == ApprovalModeYolo {
			s.approvalMode = ApprovalModePrompt
		}
		s.mu.Unlock()
	} else if persistErr := s.persistApprovalMode(mode); persistErr != nil {
		s.mu.Lock()
		s.approvalMode = previousMode
		s.mu.Unlock()
		err = persistErr
	}
	s.emitApprovalMode(s.ctx)
	return err
}

func (s *Service) ApprovalModeState() (ApprovalMode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvalMode, true
}

func (s *Service) persistApprovalMode(mode ApprovalMode) error {
	if err := s.dispatchLifecycle(s.ctx, hooks.ConfigChange, s.hookMetadata(s.currentSession, ""), func(e *hooks.Envelope) {
		e.Source, e.FilePath = "user_settings", s.configPath
	}); err != nil {
		return err
	}
	if s.configPath != "" {
		if err := s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateDefault(s.configPath, "approval_mode", string(mode))
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.Defaults.ApprovalMode = string(mode)
	s.mu.Unlock()
	return nil
}

func (s *Service) resolveLiveApproval(ctx context.Context, approvalID, decision, decidedBy string) (bool, error) {
	if strings.TrimSpace(decidedBy) == "" {
		return true, fmt.Errorf("approval decider is empty")
	}
	s.mu.Lock()
	live := s.liveApprovals[approvalID]
	if live == nil {
		s.mu.Unlock()
		return false, nil
	}
	if live.resolving || live.resolved {
		s.mu.Unlock()
		return true, fmt.Errorf("approval %q is already being resolved", approvalID)
	}
	mode, err := approvalDecisionMode(decision)
	if err != nil {
		s.mu.Unlock()
		return true, err
	}
	live.resolving = true
	s.mu.Unlock()
	if live.suspendable {
		return s.resolveSuspendedLiveApproval(ctx, live, decision, decidedBy, mode)
	}

	if live.run != nil {
		if err := s.coding.ResolveApproval(ctx, live.run, live.operationID, mode, decidedBy); err != nil {
			s.mu.Lock()
			if current := s.liveApprovals[approvalID]; current == live {
				live.resolving = false
			}
			s.mu.Unlock()
			return true, err
		}
	} else if live.request.ToolName != "" && s.coding != nil {
		event := Event{RunID: live.runID, ToolCallID: live.callID, ApprovalID: live.approvalID}
		if err := s.recordTeamApprovalDecision(ctx, event, live.request, mode, decidedBy); err != nil {
			s.mu.Lock()
			if current := s.liveApprovals[approvalID]; current == live {
				live.resolving = false
			}
			s.mu.Unlock()
			return true, err
		}
	}
	s.mu.Lock()
	if current := s.liveApprovals[approvalID]; current != live {
		s.mu.Unlock()
		return true, fmt.Errorf("approval %q is no longer pending", approvalID)
	}
	if live.run == nil && mode == agentservice.ApprovalSession {
		s.teamApprovals[live.fingerprint] = struct{}{}
	}
	live.resolving = false
	live.resolved = true
	s.mu.Unlock()
	select {
	case live.decision <- mode:
	default:
		return true, fmt.Errorf("approval %q was already resolved", approvalID)
	}
	s.emit(ctx, Event{
		Kind: EventApprovalResolved, SessionID: live.sessionID, RunID: live.runID, AgentID: live.agentID,
		ToolCallID: live.callID, ApprovalID: approvalID, State: decision, Data: map[string]string{"decided_by": decidedBy},
	})
	if s.providers != nil {
		s.providers.AutoWakePending(live.sessionID)
	}
	return true, nil
}

func (s *Service) resolveSuspendedLiveApproval(
	ctx context.Context,
	live *liveApproval,
	decision string,
	decidedBy string,
	mode agentservice.ApprovalMode,
) (bool, error) {
	select {
	case err := <-live.suspension:
		if err != nil {
			s.resetLiveApprovalResolution(live)
			return true, err
		}
	case <-ctx.Done():
		s.resetLiveApprovalResolution(live)
		return true, context.Cause(ctx)
	}
	if s.coding == nil {
		s.resetLiveApprovalResolution(live)
		return true, fmt.Errorf("coding runtime is unavailable")
	}
	if err := s.coding.ResolveRecoveredApproval(
		ctx, live.pending.Request, live.pending.Token.TokenID, decision,
	); err != nil {
		s.resetLiveApprovalResolution(live)
		return true, err
	}
	s.mu.Lock()
	if current := s.liveApprovals[live.approvalID]; current != live {
		s.mu.Unlock()
		return true, fmt.Errorf("approval %q is no longer pending", live.approvalID)
	}
	live.resolving = false
	live.resolved = true
	delete(s.liveApprovals, live.approvalID)
	s.mu.Unlock()
	s.emit(ctx, Event{
		Kind: EventApprovalResolved, SessionID: live.sessionID, RunID: live.runID, AgentID: live.agentID,
		ToolCallID: live.callID, ApprovalID: live.approvalID, State: decision, Data: map[string]string{"decided_by": decidedBy},
	})
	if s.providers == nil {
		return true, fmt.Errorf("provider runtime is unavailable")
	}
	if err := s.providers.ResumeRecoveredRunAtOperation(ctx, live.runID, live.operationID); err != nil {
		return true, err
	}
	if mode == agentservice.ApprovalDenied {
		s.notifyHook(ctx, hooks.Metadata{
			SessionID: live.sessionID, RunID: live.runID, AgentID: live.agentID, AgentType: live.agentType, CWD: s.cfg.Workspace.Root,
		}, "permission_denied", "Approval denied", live.pending.Request.RequestedAction)
	}
	return true, nil
}

func (s *Service) resetLiveApprovalResolution(live *liveApproval) {
	s.mu.Lock()
	if current := s.liveApprovals[live.approvalID]; current == live {
		live.resolving = false
	}
	s.mu.Unlock()
}

func approvalDecisionMode(decision string) (agentservice.ApprovalMode, error) {
	switch decision {
	case "once", "approved", "approve":
		return agentservice.ApprovalOnce, nil
	case "session":
		return agentservice.ApprovalSession, nil
	case "deny", "denied", "reject", "rejected":
		return agentservice.ApprovalDenied, nil
	default:
		return "", fmt.Errorf("invalid approval decision %q", decision)
	}
}

func (s *Service) finishLiveApproval(live *liveApproval) {
	s.mu.Lock()
	cancelled := false
	if s.liveApprovals[live.approvalID] == live {
		delete(s.liveApprovals, live.approvalID)
		cancelled = !live.resolved
	}
	s.mu.Unlock()
	if cancelled {
		s.emit(s.ctx, Event{
			Kind: EventApprovalResolved, SessionID: live.sessionID, RunID: live.runID, AgentID: live.agentID,
			ToolCallID: live.callID, ApprovalID: live.approvalID, State: "cancelled",
		})
		if s.providers != nil {
			s.providers.AutoWakePending(live.sessionID)
		}
	}
}
