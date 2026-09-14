package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/session"
)

const (
	SessionProjectionVersion = 1
	maxLiveProjectionBytes   = 1 << 20
	maxProjectionDetailBytes = 4 << 10
)

type TranscriptBlock struct {
	ID          string               `json:"id"`
	Sequence    int64                `json:"sequence"`
	Kind        string               `json:"kind"`
	RunID       string               `json:"runId,omitempty"`
	AgentID     string               `json:"agentId,omitempty"`
	ToolCallID  string               `json:"toolCallId,omitempty"`
	Title       string               `json:"title,omitempty"`
	Content     string               `json:"content,omitempty"`
	TextPhase   string               `json:"textPhase,omitempty"`
	State       string               `json:"state,omitempty"`
	Collapsed   bool                 `json:"collapsed,omitempty"`
	Attachments []session.Attachment `json:"attachments"`
	Data        map[string]string    `json:"data"`
}

type SessionProjection struct {
	Version        int                    `json:"version"`
	Session        session.Session        `json:"session"`
	Blocks         []TranscriptBlock      `json:"blocks"`
	ToolRecords    []session.ToolRecord   `json:"toolRecords"`
	Todo           session.TodoList       `json:"todo"`
	AgentSnapshots []AgentSnapshotPayload `json:"agentSnapshots"`
	Recap          *recap.Recap           `json:"recap,omitempty"`
	Usage          session.Usage          `json:"usage"`
	LastRunID      string                 `json:"lastRunId,omitempty"`
}

type RunActivity string

const (
	RunActivityPreparing        RunActivity = "preparing"
	RunActivityWaitingModel     RunActivity = "waiting_model"
	RunActivityThinking         RunActivity = "thinking"
	RunActivityStreaming        RunActivity = "streaming"
	RunActivityRunningTools     RunActivity = "running_tools"
	RunActivityAwaitingApproval RunActivity = "awaiting_approval"
	RunActivityAwaitingInput    RunActivity = "awaiting_input"
	RunActivityReviewing        RunActivity = "reviewing_approval"
	RunActivityCompacting       RunActivity = "compacting"
	RunActivityCheckpointing    RunActivity = "checkpointing"
	RunActivityRecovering       RunActivity = "recovering"
	RunActivityStopping         RunActivity = "stopping"
	RunActivityIdle             RunActivity = "idle"
	RunActivityUnknown          RunActivity = "unknown"
)

type ActiveOperation struct {
	ID             string    `json:"id"`
	ToolCallID     string    `json:"toolCallId,omitempty"`
	AgentID        string    `json:"agentId,omitempty"`
	Name           string    `json:"name"`
	Target         string    `json:"target,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	State          string    `json:"state"`
	StartedAt      time.Time `json:"startedAt,omitempty"`
	LastActivityAt time.Time `json:"lastActivityAt,omitempty"`
}

type RunProjection struct {
	SessionID         string                             `json:"sessionId"`
	RunID             string                             `json:"runId,omitempty"`
	TaskID            string                             `json:"taskId,omitempty"`
	ExecutionID       string                             `json:"executionId,omitempty"`
	State             string                             `json:"state"`
	BindingState      agentruntime.ExecutionBindingState `json:"bindingState,omitempty"`
	Checkpoint        uint64                             `json:"checkpointSequence,omitempty"`
	Continuation      string                             `json:"continuationPhase,omitempty"`
	StartedAt         time.Time                          `json:"startedAt,omitempty"`
	UpdatedAt         time.Time                          `json:"updatedAt,omitempty"`
	LastActivityAt    time.Time                          `json:"lastActivityAt,omitempty"`
	GuidanceOpen      bool                               `json:"guidanceOpen"`
	HasActiveChildren bool                               `json:"hasActiveChildren"`
	Provider          string                             `json:"provider,omitempty"`
	Model             string                             `json:"model,omitempty"`
	Reasoning         string                             `json:"reasoning,omitempty"`
	Activity          RunActivity                        `json:"activity"`
	ActiveOperations  []ActiveOperation                  `json:"activeOperations"`
	Failure           string                             `json:"failure,omitempty"`
	Progress          string                             `json:"progress,omitempty"`
	AllowedActions    []string                           `json:"allowedActions"`
}

type LiveBlockProjection struct {
	ID         string            `json:"id"`
	SessionID  string            `json:"sessionId"`
	RunID      string            `json:"runId"`
	AgentID    string            `json:"agentId,omitempty"`
	Kind       string            `json:"kind"`
	TextPhase  string            `json:"textPhase,omitempty"`
	ToolCallID string            `json:"toolCallId,omitempty"`
	Content    string            `json:"content,omitempty"`
	State      string            `json:"state"`
	Data       map[string]string `json:"data"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	Truncated  bool              `json:"truncated,omitempty"`
}

type PendingControlProjection struct {
	Kind           string            `json:"kind"`
	ID             string            `json:"id"`
	SessionID      string            `json:"sessionId,omitempty"`
	RunID          string            `json:"runId,omitempty"`
	AgentID        string            `json:"agentId,omitempty"`
	ToolCallID     string            `json:"toolCallId,omitempty"`
	State          string            `json:"state"`
	Title          string            `json:"title,omitempty"`
	Text           string            `json:"text,omitempty"`
	Data           map[string]string `json:"data"`
	AllowedActions []string          `json:"allowedActions"`
}

type RecoveryProjection struct {
	State                string                     `json:"state"`
	ExpiredLeases        int64                      `json:"expiredLeases"`
	QuarantinedAttempts  int64                      `json:"quarantinedAttempts"`
	InterruptedSubagents int64                      `json:"interruptedSubagents"`
	Items                []PendingControlProjection `json:"items"`
}

type RuntimeProjectionSnapshot struct {
	Session         *SessionProjection         `json:"session,omitempty"`
	Runs            []RunProjection            `json:"runs"`
	LiveBlocks      []LiveBlockProjection      `json:"liveBlocks"`
	PendingControls []PendingControlProjection `json:"pendingControls"`
	PromptQueues    []session.PromptQueueV1    `json:"promptQueues"`
	Recovery        RecoveryProjection         `json:"recovery"`
}

func (s *Service) RuntimeProjection(ctx context.Context, sessionID string) (RuntimeProjectionSnapshot, error) {
	result := RuntimeProjectionSnapshot{
		Runs: []RunProjection{}, LiveBlocks: []LiveBlockProjection{},
		PendingControls: []PendingControlProjection{}, PromptQueues: []session.PromptQueueV1{},
		Recovery: s.recoveryProjection(),
	}
	if s.sessions == nil {
		return result, nil
	}
	queues, err := s.sessions.ListPromptQueues(ctx)
	if err != nil {
		return RuntimeProjectionSnapshot{}, err
	}
	result.PromptQueues = queues
	projection, err := s.sessions.LoadDisplayProjection(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		if !errors.Is(err, session.ErrSessionNotFound) {
			return RuntimeProjectionSnapshot{}, err
		}
	} else {
		sessionProjection, err := s.sessionProjection(ctx, projection)
		if err != nil {
			return RuntimeProjectionSnapshot{}, err
		}
		result.Session = &sessionProjection
		result.PendingControls = s.pendingControlProjections(sessionProjection)
	}
	s.mu.Lock()
	active := cloneRunProjection(s.activeRunProjection)
	for _, live := range s.liveBlocks {
		if live.SessionID == sessionID {
			result.LiveBlocks = append(result.LiveBlocks, cloneLiveBlock(live))
		}
	}
	operations := make([]ActiveOperation, 0, len(s.activeOperations))
	for _, operation := range s.activeOperations {
		if active.RunID == "" || operation.RunID == active.RunID {
			operations = append(operations, operation.ActiveOperation)
		}
	}
	active.ActiveOperations = operations
	s.mu.Unlock()
	if active.SessionID != "" {
		active, err = s.hydrateRunProjection(ctx, active)
		if err != nil {
			return RuntimeProjectionSnapshot{}, err
		}
		durableOperations, err := s.durableOperationsForRun(ctx, active.SessionID, active.RunID, result.Session)
		if err != nil {
			return RuntimeProjectionSnapshot{}, err
		}
		active.ActiveOperations = mergeActiveOperations(active.ActiveOperations, durableOperations)
		result.Runs = append(result.Runs, active)
	}
	if result.Session != nil && result.Session.LastRunID != "" && !containsRun(result.Runs, result.Session.LastRunID) {
		candidate, err := s.hydrateRunProjection(ctx, RunProjection{SessionID: sessionID, RunID: result.Session.LastRunID, Activity: RunActivityUnknown, State: "unknown", ActiveOperations: []ActiveOperation{}, AllowedActions: []string{}})
		if err != nil {
			return RuntimeProjectionSnapshot{}, err
		}
		candidate.ActiveOperations = activeOperationsFromToolRecords(result.Session.ToolRecords, candidate.RunID)
		if !terminalRunProjection(candidate) {
			result.Runs = append(result.Runs, candidate)
		}
	}
	for _, recovered := range s.recovery.Runs {
		session := recovered.Run.Metadata["session_id"]
		if session == "" || containsRun(result.Runs, recovered.Run.ID) {
			continue
		}
		candidate, err := s.hydrateRunProjection(ctx, RunProjection{SessionID: session, RunID: recovered.Run.ID, Activity: RunActivityRecovering, State: string(recovered.Run.Status), ActiveOperations: []ActiveOperation{}, AllowedActions: []string{"stop"}})
		if err != nil {
			return RuntimeProjectionSnapshot{}, err
		}
		durableOperations, err := s.durableOperationsForRun(ctx, session, recovered.Run.ID, result.Session)
		if err != nil {
			return RuntimeProjectionSnapshot{}, err
		}
		candidate.ActiveOperations = mergeActiveOperations(candidate.ActiveOperations, durableOperations)
		result.Runs = append(result.Runs, candidate)
	}
	sort.Slice(result.Runs, func(left, right int) bool { return result.Runs[left].StartedAt.Before(result.Runs[right].StartedAt) })
	sort.Slice(result.LiveBlocks, func(left, right int) bool {
		if result.LiveBlocks[left].UpdatedAt.Equal(result.LiveBlocks[right].UpdatedAt) {
			return result.LiveBlocks[left].ID < result.LiveBlocks[right].ID
		}
		return result.LiveBlocks[left].UpdatedAt.Before(result.LiveBlocks[right].UpdatedAt)
	})
	return result, nil
}

func (s *Service) sessionProjection(ctx context.Context, projection session.Projection) (SessionProjection, error) {
	agents, err := s.projectFusionSession(ctx, &projection)
	if err != nil {
		return SessionProjection{}, err
	}
	blocks := make([]TranscriptBlock, len(projection.Blocks))
	for index, block := range projection.Blocks {
		blocks[index] = TranscriptBlock{
			ID: firstNonempty(block.Data["fusionBlockId"], fmt.Sprintf("block:%d", block.Sequence)), Sequence: block.Sequence,
			Kind: block.Kind, RunID: block.RunID, AgentID: block.AgentID,
			ToolCallID: block.ParentToolCallID, Title: block.Title, Content: block.Content,
			TextPhase: block.TextPhase, State: block.State, Collapsed: block.Collapsed,
			Attachments: append([]session.Attachment(nil), block.Attachments...), Data: cloneProjectionData(block.Data),
		}
	}
	toolRecords := append([]session.ToolRecord(nil), projection.ToolRecords...)
	if blocks == nil {
		blocks = []TranscriptBlock{}
	}
	if toolRecords == nil {
		toolRecords = []session.ToolRecord{}
	}
	todo, err := s.sessions.LoadTodo(ctx, projection.Session.ID)
	if err != nil {
		return SessionProjection{}, err
	}
	if todo.Phases == nil {
		todo.Phases = []session.TodoPhase{}
	}
	currentRecap, err := s.loadRecap(ctx, projection.Session.ID)
	if err != nil {
		return SessionProjection{}, err
	}
	if agents == nil {
		agents = []AgentSnapshotPayload{}
	}
	return SessionProjection{
		Version: SessionProjectionVersion, Session: projection.Session, Blocks: blocks,
		ToolRecords: toolRecords, Todo: todo, AgentSnapshots: agents, Recap: currentRecap,
		Usage: projection.Usage, LastRunID: projection.LastRunID,
	}, nil
}

func (s *Service) durableOperationsForRun(ctx context.Context, sessionID, runID string, selected *SessionProjection) ([]ActiveOperation, error) {
	if selected != nil && selected.Session.ID == sessionID {
		return activeOperationsFromToolRecords(selected.ToolRecords, runID), nil
	}
	projection, err := s.sessions.LoadDisplayProjection(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return activeOperationsFromToolRecords(projection.ToolRecords, runID), nil
}

func activeOperationsFromToolRecords(records []session.ToolRecord, runID string) []ActiveOperation {
	result := make([]ActiveOperation, 0)
	for _, record := range records {
		if record.RunID != runID || record.Name == "sidekick" || terminalToolRecordState(record.State) {
			continue
		}
		target := ""
		var arguments map[string]any
		if json.Unmarshal(record.Arguments, &arguments) == nil {
			for _, key := range []string{"path", "package", "command", "target"} {
				if value, ok := arguments[key].(string); ok && strings.TrimSpace(value) != "" {
					target = value
					break
				}
			}
		}
		result = append(result, ActiveOperation{
			ID: "tool:" + record.ToolCallID, ToolCallID: record.ToolCallID,
			Name: record.Name, Target: boundedProjectionText(target),
			Summary: boundedProjectionText(string(record.Arguments)), State: record.State,
			StartedAt: record.StartedAt, LastActivityAt: firstTime(record.CompletedAt, record.StartedAt),
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].StartedAt.Equal(result[right].StartedAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].StartedAt.Before(result[right].StartedAt)
	})
	return result
}

func mergeActiveOperations(live, durable []ActiveOperation) []ActiveOperation {
	merged := make(map[string]ActiveOperation, len(live)+len(durable))
	for _, operation := range live {
		merged[operation.ID] = operation
	}
	for _, operation := range durable {
		merged[operation.ID] = operation
	}
	result := make([]ActiveOperation, 0, len(merged))
	for _, operation := range merged {
		result = append(result, operation)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].StartedAt.Equal(result[right].StartedAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].StartedAt.Before(result[right].StartedAt)
	})
	return result
}

func terminalToolRecordState(state string) bool {
	switch state {
	case "completed", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

func (s *Service) hydrateRunProjection(ctx context.Context, projection RunProjection) (RunProjection, error) {
	if projection.ActiveOperations == nil {
		projection.ActiveOperations = []ActiveOperation{}
	}
	if projection.AllowedActions == nil {
		projection.AllowedActions = []string{}
	}
	if s.coding == nil || projection.RunID == "" {
		return projection, nil
	}
	source, err := s.coding.LoadRunProjection(ctx, projection.RunID)
	if err != nil {
		if errors.Is(err, agentruntime.ErrNotFound) {
			return projection, nil
		}
		return RunProjection{}, err
	}
	projection.TaskID = source.Task.ID
	projection.State = string(source.Run.Status)
	projection.StartedAt = firstTime(source.Run.CreatedAt, projection.StartedAt)
	projection.UpdatedAt = source.Run.UpdatedAt
	projection.LastActivityAt = firstTime(projection.LastActivityAt, source.Run.UpdatedAt)
	projection.Failure = boundedProjectionText(firstNonempty(source.Task.Error, source.Run.Metadata["error"]))
	if projection.SessionID == "" {
		projection.SessionID = source.Run.Metadata["session_id"]
	}
	if source.Binding != nil {
		projection.ExecutionID = source.Binding.ExecutionID
		projection.BindingState = source.Binding.State
		projection.Provider = firstNonempty(source.Binding.Manifest.Provider, projection.Provider)
		projection.Model = firstNonempty(source.Binding.Manifest.Model, projection.Model)
		projection.Reasoning = firstNonempty(source.Binding.Manifest.Reasoning, projection.Reasoning)
		projection.StartedAt = firstTime(source.Binding.Manifest.StartedAt, projection.StartedAt)
	}
	if projection.Activity == "" || projection.Activity == RunActivityUnknown {
		projection.Activity = runActivityFromDurable(source.Run.Status, projection.BindingState, projection.Continuation)
	}
	if source.Binding != nil && (source.Binding.State == agentruntime.ExecutionBindingRunning || source.Binding.State == agentruntime.ExecutionBindingPending || source.Binding.State == agentruntime.ExecutionBindingSuspended) {
		projection.AllowedActions = appendMissing(projection.AllowedActions, "stop")
	}
	projection.HasActiveChildren = projection.HasActiveChildren || len(s.UnfinishedChildren(projection.SessionID, projection.RunID)) > 0
	return projection, nil
}

func (s *Service) pendingControlProjections(projection SessionProjection) []PendingControlProjection {
	controls := make([]PendingControlProjection, 0)
	seen := make(map[string]struct{})
	for _, event := range s.PendingControlEvents(projection.Session.ID) {
		control := pendingControlFromEvent(event)
		if control.ID != "" {
			seen[control.Kind+"\x00"+control.ID] = struct{}{}
			controls = append(controls, control)
		}
	}
	for _, block := range projection.Blocks {
		control := pendingControlFromBlock(projection.Session.ID, block)
		if control.ID == "" {
			continue
		}
		key := control.Kind + "\x00" + control.ID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		controls = append(controls, control)
	}
	sort.Slice(controls, func(left, right int) bool {
		if controls[left].Kind == controls[right].Kind {
			return controls[left].ID < controls[right].ID
		}
		return controls[left].Kind < controls[right].Kind
	})
	return controls
}

func pendingControlFromEvent(event Event) PendingControlProjection {
	kind, id, actions := "", "", []string{}
	switch event.Kind {
	case EventApprovalRequested:
		kind, id, actions = "approval", event.ApprovalID, []string{"approve", "deny"}
	case EventUserInputRequested:
		kind, id, actions = "input", event.UserInputID, []string{"answer"}
	case EventPlanProposed:
		kind, id, actions = "plan", event.PlanID, []string{"execute", "revise", "cancel"}
	}
	return PendingControlProjection{
		Kind: kind, ID: id, SessionID: event.SessionID, RunID: event.RunID,
		AgentID: event.AgentID, ToolCallID: event.ToolCallID, State: event.State,
		Text: event.Text, Data: cloneProjectionData(event.Data), AllowedActions: actions,
	}
}

func pendingControlFromBlock(sessionID string, block TranscriptBlock) PendingControlProjection {
	control := PendingControlProjection{SessionID: sessionID, RunID: block.RunID, State: block.State, Title: block.Title, Text: block.Content, Data: cloneProjectionData(block.Data)}
	switch block.Kind {
	case "question":
		if block.State != "pending" && block.State != "interrupted" {
			return PendingControlProjection{}
		}
		control.Kind, control.ID, control.AllowedActions = "input", block.Data["userInputId"], []string{"answer"}
	case "approval":
		if block.State != "pending" && block.State != "interrupted" {
			return PendingControlProjection{}
		}
		control.Kind, control.ID, control.AllowedActions = "approval", block.Data["approvalId"], []string{"approve", "deny"}
	case "plan":
		if block.State != "proposed" && block.State != "interrupted" {
			return PendingControlProjection{}
		}
		control.Kind, control.ID, control.AllowedActions = "plan", block.Data["planId"], []string{"execute", "revise", "cancel"}
	default:
		return PendingControlProjection{}
	}
	return control
}

func (s *Service) recoveryProjection() RecoveryProjection {
	result := RecoveryProjection{
		State: "clear", ExpiredLeases: s.recovery.ExpiredLeases,
		QuarantinedAttempts:  s.recovery.QuarantinedAttempts,
		InterruptedSubagents: s.recovery.InterruptedSubagents, Items: []PendingControlProjection{},
	}
	for _, pending := range s.recovery.Approvals {
		result.Items = append(result.Items, PendingControlProjection{
			Kind: "approval", ID: pending.Approval.ApprovalID, RunID: pending.Approval.RunID,
			State: "pending", Title: "Pending approval",
			Text: firstNonempty(pending.Approval.RiskSummary, pending.Approval.Reason, pending.Approval.RequestedAction),
			Data: map[string]string{"tokenId": pending.Token.TokenID}, AllowedActions: []string{"approve", "deny"},
		})
	}
	for _, attempt := range s.recovery.ReconcileAttempts {
		result.Items = append(result.Items, PendingControlProjection{
			Kind: "reconcile", ID: attempt.AttemptID, RunID: attempt.RunID, State: "unknown",
			Title: "Unknown side effect", Text: "Confirm the external outcome before continuing.",
			Data:           map[string]string{"toolName": attempt.ToolName, "executionId": attempt.ExecutionID, "attemptKind": attempt.AttemptKind},
			AllowedActions: []string{"succeeded", "failed", "retry"},
		})
	}
	if len(s.recovery.Runs) > 0 || len(result.Items) > 0 || result.ExpiredLeases > 0 || result.QuarantinedAttempts > 0 || result.InterruptedSubagents > 0 {
		result.State = "attention_required"
	}
	return result
}

type projectedOperation struct {
	RunID string
	ActiveOperation
}

func runProjectionForRequest(request TurnRequest, startedAt time.Time) RunProjection {
	return RunProjection{
		SessionID: request.SessionID, State: "pending", StartedAt: startedAt,
		UpdatedAt: startedAt, LastActivityAt: startedAt, Provider: request.Provider,
		Model: request.Model, Reasoning: request.Reasoning, Activity: RunActivityPreparing,
		ActiveOperations: []ActiveOperation{}, AllowedActions: []string{"stop"},
	}
}

func (s *Service) bindActiveRunProjectionLocked(runID string, request TurnRequest, guidanceOpen bool) {
	s.activeRunProjection.RunID = runID
	s.activeRunProjection.SessionID = request.SessionID
	s.activeRunProjection.Provider = request.Provider
	s.activeRunProjection.Model = request.Model
	s.activeRunProjection.Reasoning = request.Reasoning
	s.activeRunProjection.State = "running"
	s.activeRunProjection.Activity = RunActivityWaitingModel
	s.activeRunProjection.GuidanceOpen = guidanceOpen
	if guidanceOpen {
		s.activeRunProjection.AllowedActions = appendMissing(s.activeRunProjection.AllowedActions, "guide")
	}
}

func (s *Service) observeRuntimeProjectionLocked(event Event) {
	if s.activeRunProjection.SessionID != "" && event.SessionID == s.activeRunProjection.SessionID && (event.RunID == "" || s.activeRunProjection.RunID == "" || event.RunID == s.activeRunProjection.RunID) {
		s.activeRunProjection.LastActivityAt = event.At
		s.activeRunProjection.UpdatedAt = event.At
		s.activeRunProjection.Activity = runActivityFromEvent(event, s.activeRunProjection.Activity)
		s.activeRunProjection.Progress = boundedProjectionText(event.Text)
		if event.RunID != "" {
			s.activeRunProjection.RunID = event.RunID
		}
		if event.Kind == EventApprovalRequested || event.Kind == EventUserInputRequested || event.Kind == EventPlanProposed {
			s.activeRunProjection.GuidanceOpen = false
		}
	}
	// Fusion prose already has an execution transcript projection. Keeping a
	// second accumulator here would mix it with the lead's live text.
	if event.Data["fusionBlockId"] == "" {
		s.updateLiveProjectionLocked(event)
	}
	s.updateActiveOperationLocked(event)
}

func (s *Service) updateLiveProjectionLocked(event Event) {
	switch event.Kind {
	case EventTextDelta, EventThinkingDelta, EventToolStarted, EventToolUpdate:
	default:
		return
	}
	if event.SessionID == "" || event.RunID == "" {
		return
	}
	kind := "tool"
	if event.Kind == EventTextDelta {
		kind = "text"
	} else if event.Kind == EventThinkingDelta {
		kind = "thinking"
	}
	key := liveProjectionKey(event.SessionID, event.RunID, event.AgentID, kind, event.TextPhase, event.ToolCallID)
	live := s.liveBlocks[key]
	if live.ID == "" {
		live = LiveBlockProjection{ID: key, SessionID: event.SessionID, RunID: event.RunID, AgentID: event.AgentID, Kind: kind, TextPhase: event.TextPhase, ToolCallID: event.ToolCallID}
	}
	if kind == "tool" && event.Kind == EventToolStarted {
		live.Content = ""
	}
	live.Content, live.Truncated = appendBounded(live.Content, event.Text, maxLiveProjectionBytes)
	live.State = event.State
	live.Data = boundedStringMap(event.Data)

	live.UpdatedAt = event.At
	s.liveBlocks[key] = live
	if event.Kind == EventToolStarted {
		for existingKey, existing := range s.liveBlocks {
			if existing.SessionID == event.SessionID && existing.RunID == event.RunID && (existing.Kind == "text" || existing.Kind == "thinking") {
				delete(s.liveBlocks, existingKey)
			}
		}
	}
}

func (s *Service) publishRunProjection(source Event) {
	switch source.Kind {
	case EventRunStarted, EventProviderRetry, EventThinkingDelta, EventTextDelta,
		EventToolStarted, EventToolUpdate, EventToolFinished, EventApprovalRequested,
		EventApprovalResolved, EventUserInputRequested, EventUserInputResolved,
		EventPlanProposed, EventPlanResolved, EventRecoveryState:
	default:
		return
	}
	s.mu.Lock()
	run := cloneRunProjection(s.activeRunProjection)
	if run.SessionID == "" {
		s.mu.Unlock()
		return
	}
	run.ActiveOperations = activeOperationsForRun(s.activeOperations, run.RunID)
	s.mu.Unlock()
	_ = s.events.Publish(Event{
		Kind: EventRunState, SessionID: run.SessionID, RunID: run.RunID,
		State: run.State, RunProjection: &run, At: source.At,
	})
}

func (s *Service) emitSessionProjectionState(ctx context.Context, sessionID string) error {
	if s.sessions == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	projection, err := s.sessions.LoadDisplayProjection(ctx, sessionID)
	if err != nil {
		return err
	}
	typed, err := s.sessionProjection(ctx, projection)
	if err != nil {
		return err
	}
	returnError := s.events.Publish(Event{
		Kind: EventSessionProjection, SessionID: sessionID, State: "snapshot",
		SessionProjection: &typed, At: time.Now().UTC(),
	})
	if returnError != eventPublishAccepted {
		return eventDeliveryError(ctx)
	}
	return nil
}

func activeOperationsForRun(operations map[string]projectedOperation, runID string) []ActiveOperation {
	result := make([]ActiveOperation, 0, len(operations))
	for _, operation := range operations {
		if runID == "" || operation.RunID == runID {
			result = append(result, operation.ActiveOperation)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].StartedAt.Equal(result[right].StartedAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].StartedAt.Before(result[right].StartedAt)
	})
	return result
}

func (s *Service) updateActiveOperationLocked(event Event) {
	if event.RunID == "" {
		return
	}
	switch event.Kind {
	case EventToolStarted, EventToolUpdate:
		if event.ToolCallID == "" {
			return
		}
		key := "tool:" + event.ToolCallID
		operation := s.activeOperations[key]
		if operation.ID == "" {
			operation = projectedOperation{RunID: event.RunID, ActiveOperation: ActiveOperation{ID: key, ToolCallID: event.ToolCallID, StartedAt: event.At}}
		}
		operation.Name = firstNonempty(event.Data["name"], operation.Name, "tool")
		operation.Target = boundedProjectionText(firstNonempty(event.Data["target"], event.Data["path"], event.Data["package"]))
		operation.Summary = boundedProjectionText(firstNonempty(event.Text, event.Data["arguments"]))
		operation.State = event.State
		operation.LastActivityAt = event.At
		s.activeOperations[key] = operation
	case EventToolFinished:
		delete(s.activeOperations, "tool:"+event.ToolCallID)
		for key, live := range s.liveBlocks {
			if live.ToolCallID == event.ToolCallID {
				delete(s.liveBlocks, key)
			}
		}
	case EventAgentState:
		if event.AgentID == "" {
			return
		}
		key := "agent:" + event.AgentID
		if terminalAgentState(event.State) {
			delete(s.activeOperations, key)
			return
		}
		operation := s.activeOperations[key]
		if operation.ID == "" {
			operation = projectedOperation{RunID: event.RunID, ActiveOperation: ActiveOperation{ID: key, AgentID: event.AgentID, StartedAt: event.At}}
		}
		operation.Name = "subagent"
		if event.Agent != nil {
			operation.Target = boundedProjectionText(firstNonempty(event.Agent.Description, event.Agent.Type))
			operation.Summary = boundedProjectionText(event.Agent.Activity)
		}
		operation.State = event.State
		operation.LastActivityAt = event.At
		s.activeOperations[key] = operation
	}
}

func (s *Service) clearRuntimeProjectionLocked(runID string) {
	if runID == "starting" || runID == "" || s.activeRunProjection.RunID == runID {
		s.activeRunProjection = RunProjection{}
	}
	for key, live := range s.liveBlocks {
		if runID == "starting" || runID == "" || live.RunID == runID {
			delete(s.liveBlocks, key)
		}
	}
	for key, operation := range s.activeOperations {
		if runID == "starting" || runID == "" || operation.RunID == runID {
			delete(s.activeOperations, key)
		}
	}
}

func runActivityFromEvent(event Event, current RunActivity) RunActivity {
	switch event.Kind {
	case EventRunStarted, EventProviderRetry:
		return RunActivityWaitingModel
	case EventThinkingDelta:
		return RunActivityThinking
	case EventTextDelta:
		return RunActivityStreaming
	case EventToolStarted, EventToolUpdate:
		switch event.State {
		case "awaiting_approval":
			return RunActivityAwaitingApproval
		case "reviewing_approval":
			return RunActivityReviewing
		default:
			return RunActivityRunningTools
		}
	case EventToolFinished, EventApprovalResolved, EventUserInputResolved, EventPlanResolved:
		return RunActivityWaitingModel
	case EventApprovalRequested:
		return RunActivityAwaitingApproval
	case EventUserInputRequested, EventPlanProposed:
		return RunActivityAwaitingInput
	case EventRecoveryState:
		return RunActivityRecovering
	default:
		if current == "" {
			return RunActivityUnknown
		}
		return current
	}
}

func runActivityFromDurable(status agentruntime.RunStatus, binding agentruntime.ExecutionBindingState, continuation string) RunActivity {
	switch status {
	case agentruntime.RunStatusWaitingApproval:
		return RunActivityAwaitingApproval
	case agentruntime.RunStatusWaitingUserInput:
		return RunActivityAwaitingInput
	case agentruntime.RunStatusExecuting:
		return RunActivityRunningTools
	case agentruntime.RunStatusReconcileRequired:
		return RunActivityRecovering
	case agentruntime.RunStatusCompleted, agentruntime.RunStatusFailed, agentruntime.RunStatusCancelled:
		return RunActivityIdle
	}
	switch binding {
	case agentruntime.ExecutionBindingSuspended:
		if continuation == "model_complete" {
			return RunActivityAwaitingApproval
		}
		return RunActivityRecovering
	case agentruntime.ExecutionBindingReconcileRequired:
		return RunActivityRecovering
	case agentruntime.ExecutionBindingRunning, agentruntime.ExecutionBindingPending:
		return RunActivityWaitingModel
	default:
		return RunActivityUnknown
	}
}

func terminalRunProjection(run RunProjection) bool {
	switch run.State {
	case string(agentruntime.RunStatusCompleted), string(agentruntime.RunStatusFailed), string(agentruntime.RunStatusCancelled):
		return true
	}
	switch run.BindingState {
	case agentruntime.ExecutionBindingCompleted, agentruntime.ExecutionBindingFailed, agentruntime.ExecutionBindingCancelled:
		return true
	default:
		return false
	}
}

func terminalAgentState(state string) bool {
	switch state {
	case "completed", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

func liveProjectionKey(sessionID, runID, agentID, kind, phase, toolCallID string) string {
	return strings.Join([]string{sessionID, runID, agentID, kind, phase, toolCallID}, ":")
}

func appendBounded(current, delta string, limit int) (string, bool) {
	if len(current) >= limit {
		return current, true
	}
	remaining := limit - len(current)
	if len(delta) > remaining {
		return current + delta[:remaining], true
	}
	return current + delta, false
}

func boundedProjectionText(value string) string {
	if len(value) <= maxProjectionDetailBytes {
		return value
	}
	return value[:maxProjectionDetailBytes]
}

func boundedStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[boundedProjectionText(key)] = boundedProjectionText(value)
	}
	return result
}

func cloneProjectionData(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneLiveBlock(value LiveBlockProjection) LiveBlockProjection {
	value.Data = cloneProjectionData(value.Data)
	return value
}

func cloneRunProjection(value RunProjection) RunProjection {
	value.ActiveOperations = append([]ActiveOperation(nil), value.ActiveOperations...)
	value.AllowedActions = append([]string(nil), value.AllowedActions...)
	return value
}

func cloneSessionProjection(value SessionProjection) SessionProjection {
	value.Blocks = append([]TranscriptBlock(nil), value.Blocks...)
	for index := range value.Blocks {
		value.Blocks[index].Attachments = append([]session.Attachment(nil), value.Blocks[index].Attachments...)
		value.Blocks[index].Data = cloneProjectionData(value.Blocks[index].Data)
	}
	value.ToolRecords = append([]session.ToolRecord(nil), value.ToolRecords...)
	value.Todo = value.Todo.Clone()
	value.AgentSnapshots = append([]AgentSnapshotPayload(nil), value.AgentSnapshots...)
	if value.Recap != nil {
		recapCopy := *value.Recap
		value.Recap = &recapCopy
	}
	value.Usage = value.Usage.Clone()
	return value
}

func containsRun(runs []RunProjection, runID string) bool {
	for _, run := range runs {
		if run.RunID == runID {
			return true
		}
	}
	return false
}

func appendMissing(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func firstTime(primary, fallback time.Time) time.Time {
	if !primary.IsZero() {
		return primary
	}
	return fallback
}
