package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/codex"
	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/tool"
)

type governedAgentTool struct {
	definition       tool.Definition
	driver           tool.Driver
	coding           *agentservice.Service
	run              *agentservice.Run
	host             *Service
	sessionID        string
	agentID          string
	agentType        string
	parentToolCallID string
	streamRunID      string
	update           func(tool.Update)
}

func (d *governedAgentTool) Definition() tool.Definition { return d.definition }

func (d *governedAgentTool) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	updates := func(update tool.Update) error {
		if d.update != nil {
			d.update(update)
		}
		if d.host != nil {
			runID := firstNonempty(d.streamRunID, d.run.RunID)
			d.host.emit(ctx, Event{
				Kind: EventToolUpdate, SessionID: d.sessionID, RunID: runID, AgentID: d.agentID,
				ToolCallID: call.ID, State: update.Kind, Text: update.Message,
				Data: childFrameData("child:"+d.agentID, d.parentToolCallID, update.Data),
			})
		}
		if sink != nil {
			return sink(update)
		}
		return nil
	}
	var execution agentservice.ExecutionResult
	var err error
	if hooks.PreToolPermissionFromContext(ctx) == "ask" && d.driver != nil {
		execution, err = d.coding.ExecuteDriver(ctx, d.run, approvalRequiredDriver{inner: d.driver}, call, updates)
	} else {
		execution, err = d.execute(ctx, call, updates)
	}
	if err != nil {
		return execution.Result, err
	}
	if execution.Approval == nil {
		return execution.Result, nil
	}
	if d.host == nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "approval UI is unavailable", IsError: true}, nil
	}
	resolution, err := d.host.awaitApproval(ctx, d.sessionID, d.agentID, d.agentType, d.run, call, *execution.Approval)
	if err != nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}, err
	}
	if resolution.Mode == agentservice.ApprovalDenied {
		if resolution.Prevent {
			return tool.Result{ToolCallID: call.ID, Name: call.Name, IsError: true, Content: "Hook prevented continuation"}, hooks.ErrPreventContinuation
		}
		message := firstNonempty(resolution.DenialMessage, "Denied by user")
		if resolution.Retry {
			message += " PermissionDenied hook permits retrying this tool request."
		}
		return tool.Result{
			ToolCallID: call.ID, Name: call.Name,
			Content: message, IsError: true,
		}, nil
	}
	execution, err = d.execute(ctx, call, updates)
	return execution.Result, err
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

func teamApprovalReviewRequest(goal, runID string, call tool.Call, definition tool.Definition) approvalReviewRequest {
	target := teamToolTarget(call)
	return approvalReviewRequest{
		Goal: goal, AgentID: runID, AgentType: "team", ToolName: call.Name, Arguments: call.Arguments,
		Target: target, Effect: string(definition.EffectType),
		Risk:            firstNonempty(definition.RiskLevel, definition.Security.RiskLevel, "high"),
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
	host      *Service
	sessionID string
	runID     string
	goal      string
}

type approvalRequiredDriver struct{ inner tool.Driver }

func (d approvalRequiredDriver) Definition() tool.Definition {
	definition := d.inner.Definition()
	definition.RequiresApproval = true
	definition.Security.RequiresApproval = true
	return definition
}

func (d approvalRequiredDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	return d.inner.Execute(ctx, call, sink)
}

func (d *teamApprovalDriver) Definition() tool.Definition {
	definition := d.inner.Definition()
	if teamToolHasSideEffect(definition) {
		// The outer TeamRunner gate sees a read-only adapter; Execute records
		// the real side-effect attempt after Azem receives approval.
		definition.EffectType = tool.EffectReadOnly
		definition.RequiresApproval = false
		definition.RequiresActionTask = false
		definition.Security.RequiresApproval = false
	}
	return definition
}

func (d *teamApprovalDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	definition := d.inner.Definition()
	if hooks.PreToolPermissionFromContext(ctx) == "ask" || teamToolRequiresApproval(definition, call) {
		resolution, err := d.host.awaitTeamApproval(ctx, d.sessionID, d.runID, d.goal, call, definition)
		if err != nil {
			return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}, err
		}
		if resolution.Mode == agentservice.ApprovalDenied {
			if resolution.Prevent {
				return tool.Result{ToolCallID: call.ID, Name: call.Name, IsError: true, Content: "Hook prevented continuation"}, hooks.ErrPreventContinuation
			}
			message := firstNonempty(resolution.DenialMessage, "Denied by user")
			if resolution.Retry {
				message += " PermissionDenied hook permits retrying this tool request."
			}
			return tool.Result{
				ToolCallID: call.ID, Name: call.Name,
				Content: message, IsError: true,
			}, nil
		}
	}
	if teamToolHasSideEffect(definition) {
		return d.host.coding.ExecuteTeamDriver(ctx, d.runID, d.inner, call, sink)
	}
	return d.inner.Execute(ctx, call, sink)
}

func (s *Service) teamToolBus(ctx context.Context, sessionID, runID, goal string) (*tool.Bus, error) {
	drivers, err := s.coding.WorkspaceDrivers(ctx, s.cfg.Workspace.Root)
	if err != nil {
		return nil, err
	}
	if s.sessions != nil {
		drivers = append(drivers, &todoDriver{sessionID: sessionID, store: s.sessions, emit: func(event Event) bool {
			return s.emitTodoUpdated(sessionID, *event.Todo)
		}})
	}
	governed := make([]tool.Driver, 0, len(drivers))
	for _, driver := range drivers {
		approval := &teamApprovalDriver{inner: driver, host: s, sessionID: sessionID, runID: runID, goal: goal}
		metadata := hooks.Metadata{SessionID: sessionID, RunID: runID, AgentID: "team", AgentType: "team", CWD: s.cfg.Workspace.Root}
		governed = append(governed, hooks.WrapDriver(s.hooks, metadata, approval))
	}
	return tool.NewBus(governed...), nil
}

func teamToolHasSideEffect(definition tool.Definition) bool {
	return definition.RequiresActionTask || definition.EffectType == tool.EffectWrite || definition.EffectType == tool.EffectExternalSideEffect
}

func teamToolRequiresApproval(definition tool.Definition, call tool.Call) bool {
	required := definition.RequiresApproval || definition.Security.RequiresApproval || definition.RequiresActionTask ||
		definition.EffectType == tool.EffectWrite || definition.EffectType == tool.EffectExternalSideEffect
	if definition.Metadata["approval"] == "allow" {
		required = definition.Metadata["network"] == "prompt" && toolArgumentsRequestNetwork(call.Arguments)
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

func teamToolFingerprint(definition tool.Definition, call tool.Call) string {
	return call.Name + "\x00" + string(definition.EffectType) + "\x00" + teamToolTarget(call)
}

func (s *Service) awaitTeamApproval(ctx context.Context, sessionID, runID, goal string, call tool.Call, definition tool.Definition) (approvalResolution, error) {
	if strings.TrimSpace(call.ID) == "" {
		return approvalResolution{}, fmt.Errorf("team tool %q requires a call ID for approval", call.Name)
	}
	approvalID, err := randomID("approval")
	if err != nil {
		return approvalResolution{}, err
	}
	request := teamApprovalReviewRequest(goal, runID, call, definition)
	fingerprint := teamToolFingerprint(definition, call)
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
	if mode == ApprovalModeYolo {
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}
	if granted {
		return approvalResolution{Mode: agentservice.ApprovalSession}, nil
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
	event.State = "pending"
	s.notifyHook(s.ctx, metadata, "permission_prompt", "Approval required", action)
	s.emit(s.ctx, event)
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
	bound := make([]tool.Driver, 0)
	for _, definition := range engine.Tools.Definitions() {
		driver, ok := engine.Tools.Driver(definition.Name)
		if !ok {
			continue
		}
		if governed, ok := driver.(*governedAgentTool); ok {
			clone := *governed
			clone.host = s
			bound = append(bound, &clone)
		} else {
			bound = append(bound, driver)
		}
	}
	engine.Tools = tool.NewBus(bound...)
	return engine
}

func (s *Service) awaitApproval(ctx context.Context, sessionID, agentID, agentType string, run *agentservice.Run, call tool.Call, pending agentservice.PendingApproval) (approvalResolution, error) {
	approvalID, err := randomID("approval")
	if err != nil {
		return approvalResolution{}, err
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
		if err := s.coding.ResolveApproval(ctx, run, call.ID, agentservice.ApprovalOnce, "approval-mode:yolo"); err != nil {
			return approvalResolution{}, err
		}
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}
	if mode == ApprovalModeAutoReview {
		resolution, approvalErr := s.automaticApproval(ctx, event, request, func(decisionCtx context.Context, mode agentservice.ApprovalMode, decidedBy string) error {
			if s.coding == nil {
				return fmt.Errorf("coding runtime is unavailable")
			}
			return s.coding.ResolveApproval(decisionCtx, run, call.ID, mode, decidedBy)
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
		if err := s.coding.ResolveApproval(ctx, run, call.ID, resolvedMode, "hook:"+hookDecision.name); err != nil {
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
		callID: call.ID, sessionID: sessionID, decision: make(chan agentservice.ApprovalMode, 1),
	}
	s.mu.Lock()
	if _, exists := s.liveApprovals[approvalID]; exists {
		s.mu.Unlock()
		return approvalResolution{}, fmt.Errorf("approval ID collision")
	}
	s.liveApprovals[approvalID] = live
	s.mu.Unlock()
	defer s.finishLiveApproval(live)
	event.State = "pending"
	s.notifyHook(s.ctx, metadata, "permission_prompt", "Approval required", pending.Request.RequestedAction)
	s.emit(s.ctx, event)
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
	return s.coding.Runner().AppendEvent(ctx, api.Event{
		RunID: event.RunID, Type: api.EventApprovalDecided, RecordedAt: time.Now().UTC(),
		Payload: map[string]any{
			"approvalId": event.ApprovalID, "actionId": event.ToolCallID,
			"decidedBy": decidedBy, "decision": decision, "reason": request.RequestedReason,
			"tool": request.ToolName, "target": request.Target, "risk": request.Risk,
		},
	})
}

func (s *Service) automaticApproval(
	ctx context.Context,
	event Event,
	request approvalReviewRequest,
	decide func(context.Context, agentservice.ApprovalMode, string) error,
) (approvalResolution, error) {
	event.State = "reviewing"
	event.Data["reviewer"] = codex.ApprovalReviewerModel
	s.emit(s.ctx, event)

	providerRequest, err := request.codexRequest()
	failureKind := codex.ReviewFailureInvalidRequest
	var assessment codex.ApprovalReview
	if err == nil {
		if s.authentication == nil {
			err = fmt.Errorf("authentication is unavailable")
			failureKind = codex.ReviewFailureAuthentication
		} else if active, authErr := s.authentication.HasActiveChatGPTAccount(ctx); authErr != nil {
			err = authErr
			failureKind = codex.ReviewFailureAuthentication
		} else if !active {
			err = fmt.Errorf("no active ChatGPT account is available")
			failureKind = codex.ReviewFailureAuthentication
		}
	}
	if err == nil {
		if s.providers == nil {
			err = fmt.Errorf("provider runtime is unavailable")
			failureKind = codex.ReviewFailureProvider
		} else {
			var reviewer *codex.Reviewer
			reviewer, err = s.providers.ApprovalReviewer(ctx)
			if err == nil {
				assessment, err = reviewer.Review(ctx, providerRequest)
			}
			if err != nil {
				failureKind = codex.ReviewFailure(err)
			}
		}
	}

	if err != nil {
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
			message = "Automatic review timed out; action did not run."
			rationale = "Automatic approval review timed out while evaluating the requested action."
		}
		decisionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		decisionErr := decide(decisionCtx, agentservice.ApprovalDenied, "system:auto-review-failure")
		cancel()
		s.recordAutoReview(event.RunID, false)
		// Reviewer infrastructure failures are fail-closed assessments, not a
		// successful classification of the request's original risk.
		s.emitAutomaticApprovalResolved(event, state, "high", "unknown", rationale, string(failureKind), message)
		if decisionErr != nil {
			return approvalResolution{}, errors.Join(err, fmt.Errorf("record fail-closed approval decision: %w", decisionErr))
		}
		return approvalResolution{Mode: agentservice.ApprovalDenied, DenialMessage: message}, nil
	}

	rationale := boundedReviewText(assessment.Rationale, 600)
	if assessment.Outcome == "allow" {
		decisionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		decisionErr := decide(decisionCtx, agentservice.ApprovalOnce, codex.ApprovalReviewerModel)
		cancel()
		s.recordAutoReview(event.RunID, false)
		if decisionErr != nil {
			message := "Automatic review could not record approval; action did not run."
			s.emitAutomaticApprovalResolved(event, "auto_failed", assessment.RiskLevel, assessment.UserAuthorization, rationale, "decision", message)
			return approvalResolution{}, fmt.Errorf("record automatic approval: %w", decisionErr)
		}
		message := "Approved by automatic review: " + rationale
		s.emitAutomaticApprovalResolved(event, "auto_approved", assessment.RiskLevel, assessment.UserAuthorization, rationale, "", message)
		return approvalResolution{Mode: agentservice.ApprovalOnce}, nil
	}

	limitErr := s.recordAutoReview(event.RunID, true)
	message := "Denied by automatic review: " + rationale +
		"\nUser confirmation is required before this action can run."
	if limitErr != nil {
		message += "\nRepeated automatic denials were detected; automatic execution remains paused for user review."
	}
	s.emitAutomaticApprovalResolved(event, "auto_denied", assessment.RiskLevel, assessment.UserAuthorization, rationale, "", message)
	return approvalResolution{NeedsUserApproval: true, DenialMessage: message}, nil
}

func (s *Service) emitAutomaticApprovalResolved(event Event, state, risk, authorization, rationale, errorKind, text string) {
	s.emit(s.ctx, Event{
		Kind: EventApprovalResolved, SessionID: event.SessionID, RunID: event.RunID, AgentID: event.AgentID,
		ToolCallID: event.ToolCallID, ApprovalID: event.ApprovalID, State: state, Text: text,
		Data: map[string]string{
			"reviewer": codex.ApprovalReviewerModel, "risk": risk, "user_authorization": authorization,
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
	if mode == ApprovalModeAutoReview {
		if s.authentication == nil {
			s.mu.Lock()
			s.approvalMode = ApprovalModePrompt
			s.mu.Unlock()
			s.emitApprovalMode(s.ctx)
			return fmt.Errorf("authentication is unavailable")
		}
		active, err := s.authentication.HasActiveChatGPTAccount(ctx)
		if err != nil || !active {
			s.mu.Lock()
			s.approvalMode = ApprovalModePrompt
			s.mu.Unlock()
			s.emitApprovalMode(s.ctx)
			if err != nil {
				return err
			}
			return fmt.Errorf("Approve for me requires an active ChatGPT account")
		}
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

	if live.run != nil {
		if err := s.coding.ResolveApproval(ctx, live.run, live.callID, mode, decidedBy); err != nil {
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
