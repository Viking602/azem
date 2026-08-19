package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/toolview"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/stream"
	"github.com/Viking602/venat/tool"
)

func (r *subagentRuntime) Query(ctx context.Context, sessionID string, ids []string, timeout time.Duration) []agentservice.SubagentSnapshot {
	deadline := time.Now().Add(timeout)
	for {
		snapshots, allTerminal := r.queryOnce(sessionID, ids)
		if timeout <= 0 || allTerminal || time.Now().After(deadline) {
			return snapshots
		}
		r.mu.Lock()
		changed := r.changed
		r.mu.Unlock()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return snapshots
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return r.queryCurrent(sessionID, ids)
		case <-changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			return r.queryCurrent(sessionID, ids)
		}
	}
}

func (r *subagentRuntime) queryOnce(sessionID string, ids []string) ([]agentservice.SubagentSnapshot, bool) {
	snapshots := r.queryCurrent(sessionID, ids)
	allTerminal := true
	for _, snapshot := range snapshots {
		if snapshot.Found && !subagentTerminal(snapshot.Run.State) {
			allTerminal = false
			break
		}
	}
	return snapshots, allTerminal
}

func (r *subagentRuntime) waitForForegroundStart(ctx context.Context, sessionID, id string) agentservice.SubagentSnapshot {
	for {
		r.mu.Lock()
		active := r.active[id]
		if active == nil || active.run.SessionID != sessionID {
			r.mu.Unlock()
			return r.snapshot(id, sessionID)
		}
		snapshot := r.snapshotFromActiveLocked(active)
		if active.run.State != agentservice.SubagentInitializing && active.run.State != agentservice.SubagentQueued {
			r.mu.Unlock()
			return snapshot
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return r.snapshot(id, sessionID)
		case <-changed:
		}
	}
}

func (r *subagentRuntime) queryCurrent(sessionID string, ids []string) []agentservice.SubagentSnapshot {
	result := make([]agentservice.SubagentSnapshot, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.snapshot(id, sessionID))
	}
	return result
}

func (r *subagentRuntime) snapshot(id, sessionID string) agentservice.SubagentSnapshot {
	r.mu.Lock()
	if active := r.active[id]; active != nil && active.run.SessionID == sessionID {
		snapshot := r.snapshotFromActiveLocked(active)
		r.mu.Unlock()
		return snapshot
	}
	if fallback, ok := r.terminalFallback[id]; ok && fallback.Run.SessionID == sessionID {
		fallback.Run = cloneSubagentRun(fallback.Run)
		r.mu.Unlock()
		return fallback
	}
	r.mu.Unlock()
	run, err := r.store.Get(r.ctx, id)
	if err != nil || run.SessionID != sessionID {
		return agentservice.SubagentSnapshot{Run: agentservice.SubagentRun{ID: id}}
	}
	return snapshotFromRun(run)
}

func (r *subagentRuntime) snapshotFromActiveLocked(active *activeSubagent) agentservice.SubagentSnapshot {
	run := cloneSubagentRun(active.run)
	run.ToolCalls = active.run.ToolCalls
	run.Turns = active.run.Turns
	run.TokensUsed = active.run.TokensUsed
	run.ToolsUsed = sortedToolSet(active.toolNames)
	return snapshotFromRun(run)
}

func (r *subagentRuntime) setHost(host providerHost) {
	r.mu.Lock()
	r.host = host
	r.mu.Unlock()
}

func (r *subagentRuntime) List(ctx context.Context, sessionID string) []agentservice.SubagentSnapshot {
	runs, _ := r.store.List(r.ctx, sessionID)
	byID := make(map[string]agentservice.SubagentSnapshot, len(runs))
	for _, run := range runs {
		byID[run.ID] = snapshotFromRun(run)
	}
	r.mu.Lock()
	for id, active := range r.active {
		if active.run.SessionID == sessionID {
			byID[id] = r.snapshotFromActiveLocked(active)
		}
	}
	for id, fallback := range r.terminalFallback {
		if fallback.Run.SessionID == sessionID {
			fallback.Run = cloneSubagentRun(fallback.Run)
			byID[id] = fallback
		}
	}
	for id, status := range r.evidenceStatus {
		snapshot, exists := byID[id]
		if !exists || snapshot.Run.EvidenceStatus != "" {
			continue
		}
		snapshot.Run.EvidenceStatus = status
		byID[id] = snapshot
	}
	host := r.host
	r.mu.Unlock()
	result := make([]agentservice.SubagentSnapshot, 0, len(byID))
	for _, snapshot := range byID {
		result = append(result, snapshot)
	}
	slices.SortFunc(result, func(a, b agentservice.SubagentSnapshot) int {
		if comparison := a.Run.StartedAt.Compare(b.Run.StartedAt); comparison != 0 {
			return comparison
		}
		return strings.Compare(a.Run.ID, b.Run.ID)
	})
	if host != nil {
		for index := range result {
			run := &result[index].Run
			if run.EvidenceStatus != "" || run.ChildRunID == "" || !subagentTerminal(run.State) {
				continue
			}
			run.EvidenceStatus = loadRunEvidenceStatus(ctx, host.Sessions(), sessionID, run.ChildRunID)
			if run.EvidenceStatus != "" {
				r.mu.Lock()
				r.evidenceStatus[run.ID] = run.EvidenceStatus
				r.mu.Unlock()
			}
		}
	}
	return result
}

func (r *subagentRuntime) relatedRunIDs(sessionID, parentRunID string) []string {
	result := []string{parentRunID}
	known := map[string]struct{}{parentRunID: {}}
	snapshots := r.List(context.Background(), sessionID)
	for changed := true; changed; {
		changed = false
		for _, snapshot := range snapshots {
			run := snapshot.Run
			if run.ChildRunID == "" {
				continue
			}
			if _, parentKnown := known[run.ParentRunID]; !parentKnown {
				continue
			}
			if _, exists := known[run.ChildRunID]; exists {
				continue
			}
			known[run.ChildRunID] = struct{}{}
			result = append(result, run.ChildRunID)
			changed = true
		}
	}
	slices.Sort(result[1:])
	return result
}

func (r *subagentRuntime) Detail(ctx context.Context, sessionID, id string) ([]AgentTranscriptBlock, error) {
	r.mu.Lock()
	if active := r.active[id]; active != nil && active.run.SessionID == sessionID {
		blocks := append([]AgentTranscriptBlock(nil), active.blocks...)
		r.mu.Unlock()
		for index := range blocks {
			blocks[index] = boundedAgentTranscriptBlock(blocks[index])
		}
		return blocks, nil
	}
	r.mu.Unlock()
	snapshot := r.snapshot(id, sessionID)
	if !snapshot.Found {
		return nil, api.ErrNotFound
	}
	return transcriptToAgentBlocks(snapshot.Run.Transcript)
}

func (r *subagentRuntime) Cancel(sessionID, id string) agentservice.SubagentCancelOutcome {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.run.SessionID != sessionID {
		r.mu.Unlock()
		snapshot := r.snapshot(id, sessionID)
		if !snapshot.Found {
			return agentservice.SubagentCancelOutcome{Outcome: "not_found"}
		}
		return agentservice.SubagentCancelOutcome{Outcome: "already_finished", Snapshot: snapshot}
	}
	if active.run.State == agentservice.SubagentCancelling {
		snapshot := r.snapshotFromActiveLocked(active)
		r.mu.Unlock()
		return agentservice.SubagentCancelOutcome{Outcome: "cancel_requested", Snapshot: snapshot}
	}
	if subagentTerminal(active.run.State) {
		snapshot := r.snapshotFromActiveLocked(active)
		r.mu.Unlock()
		return agentservice.SubagentCancelOutcome{Outcome: "already_finished", Snapshot: snapshot}
	}
	active.run.State = agentservice.SubagentCancelling
	active.run.Summary = "cancelling"
	cancelling := cloneSubagentRun(active.run)
	snapshot := r.snapshotFromActiveLocked(active)
	cancel := active.cancel
	queued := !active.slot
	r.mu.Unlock()
	// Persist outside the runtime lock: r.mu serializes frame handling and
	// scheduling for every subagent, so a slow store write here would stall
	// the whole roster, and the idle watchdog can trigger many cancels.
	if err := r.store.Save(r.ctx, cancelling); err != nil {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("persist cancelling subagent: %w", err)})
		return agentservice.SubagentCancelOutcome{Outcome: "cancel_requested", Snapshot: r.snapshot(id, sessionID)}
	}
	r.emitState(cancelling, "cancelling")
	cancel()
	if queued {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentCancelled})
	}
	return agentservice.SubagentCancelOutcome{Outcome: "cancel_requested", Snapshot: snapshot}
}

const (
	subagentIdleCheckInterval = time.Second
	subagentIdleCancelWarning = "cancelled after %s without thinking, output, or tool activity"
)

func (r *subagentRuntime) watchIdle() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.idleCheckInterval())
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.cancelIdleChildren()
		}
	}
}

func (r *subagentRuntime) idleCheckInterval() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idleCheckEvery > 0 {
		return r.idleCheckEvery
	}
	if r.cfg.IdleDuration > 0 && r.cfg.IdleDuration < subagentIdleCheckInterval {
		return r.cfg.IdleDuration
	}
	return subagentIdleCheckInterval
}

func (r *subagentRuntime) cancelIdleChildren() {
	type target struct {
		sessionID string
		id        string
		idle      time.Duration
	}
	now := time.Now()
	r.mu.Lock()
	idle := r.cfg.IdleDuration
	if idle <= 0 {
		r.mu.Unlock()
		return
	}
	targets := make([]target, 0)
	for id, active := range r.active {
		if active == nil || active.terminalizing || active.terminalized ||
			active.run.State != agentservice.SubagentRunning || active.hasLiveWork() {
			continue
		}
		last := active.lastVisibleAt
		if last.IsZero() {
			last = active.run.StartedAt
		}
		if last.IsZero() || now.Sub(last) < idle {
			continue
		}
		targets = append(targets, target{sessionID: active.run.SessionID, id: id, idle: idle})
	}
	r.mu.Unlock()
	for _, item := range targets {
		r.cancelIdle(item.sessionID, item.id, item.idle)
	}
}

func (r *subagentRuntime) cancelIdle(sessionID, id string, idle time.Duration) {
	warning := fmt.Sprintf(subagentIdleCancelWarning, idle)
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.run.SessionID != sessionID || active.terminalizing ||
		active.run.State != agentservice.SubagentRunning || active.hasLiveWork() {
		r.mu.Unlock()
		return
	}
	active.run.Warning = appendWarning(active.run.Warning, warning)
	active.activity = warning
	r.mu.Unlock()
	r.Cancel(sessionID, id)
}

func (r *subagentRuntime) noteVisibleActivity(id, activity string) {
	r.mu.Lock()
	if active := r.active[id]; active != nil && !active.terminalizing {
		noteVisibleActivityLocked(active, activity)
	}
	r.mu.Unlock()
	r.persistActivity(id)
}

func noteVisibleActivityLocked(active *activeSubagent, activity string) {
	active.lastVisibleAt = time.Now()
	if text := compactActivity(activity); text != "" {
		active.activity = text
	}
}

func noteVisibleContentLocked(active *activeSubagent, activity string) bool {
	if strings.TrimSpace(activity) == "" {
		return false
	}
	noteVisibleActivityLocked(active, activity)
	return true
}

func (active *activeSubagent) hasOpenTool() bool {
	if active == nil {
		return false
	}
	for _, block := range active.blocks {
		if block.Kind != "tool" {
			continue
		}
		switch block.State {
		case "running", "queued", "awaiting_approval", "reviewing_approval":
			return true
		}
	}
	return false
}

func (active *activeSubagent) hasLiveWork() bool {
	if active.hasOpenTool() {
		return true
	}
	if active == nil || active.parent.Coding == nil {
		return false
	}
	return childMatchesLiveShell(active, active.parent.Coding.ActiveShellExecutions())
}

func childMatchesLiveShell(active *activeSubagent, shells []agentservice.ShellExecutionSnapshot) bool {
	if active == nil {
		return false
	}
	childRunID := strings.TrimSpace(active.run.ChildRunID)
	sessionID := strings.TrimSpace(active.run.SessionID)
	agentID := durableSubagentAgentID(active.run.Type)
	for _, snap := range shells {
		if snap.State != "running" {
			continue
		}
		if childRunID != "" && snap.RunID == childRunID {
			return true
		}
		if childRunID == "" && sessionID != "" && snap.SessionID == sessionID && snap.AgentID == agentID {
			return true
		}
	}
	return false
}

func (r *subagentRuntime) listRunningBackgroundChildren(sessionID, parentRunID string) []agentservice.SubagentRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	children := make([]agentservice.SubagentRun, 0)
	for _, active := range r.active {
		if active.run.SessionID != sessionID || active.run.ParentRunID != parentRunID || !active.run.Background {
			continue
		}
		if active.run.CompletionDelivered || subagentTerminal(active.run.State) {
			continue
		}
		children = append(children, active.run)
	}
	slices.SortFunc(children, func(left, right agentservice.SubagentRun) int {
		return strings.Compare(left.ID, right.ID)
	})
	return children
}

func (r *subagentRuntime) HasActiveByParentRun(sessionID, parentRunID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, active := range r.active {
		if active.run.SessionID == sessionID && active.run.ParentRunID == parentRunID &&
			!active.terminalizing && !subagentTerminal(active.run.State) {
			return true
		}
	}
	return false
}

// HasForegroundByParentRun is retained for callers that need the old, narrower
// query. Cancellation UI should use HasActiveByParentRun instead.
func (r *subagentRuntime) HasForegroundByParentRun(sessionID, parentRunID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, active := range r.active {
		if active.run.SessionID == sessionID && active.run.ParentRunID == parentRunID && !active.run.Background &&
			!active.terminalizing && !subagentTerminal(active.run.State) {
			return true
		}
	}
	return false
}

func (r *subagentRuntime) CancelByParentRun(sessionID, parentRunID string, cancelBackground bool) {
	r.mu.Lock()
	ids := make([]string, 0)
	parents := map[string]bool{parentRunID: true}
	for added := true; added; {
		added = false
		for _, active := range r.active {
			if active.run.SessionID == sessionID && parents[active.run.ParentRunID] && (!active.run.Background || cancelBackground) && !parents[active.run.ID] {
				parents[active.run.ID] = true
				ids = append(ids, active.run.ID)
				added = true
			}
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.Cancel(sessionID, id)
	}
}

func (r *subagentRuntime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	ids := make([]struct{ sessionID, id string }, 0, len(r.active))
	for _, active := range r.active {
		ids = append(ids, struct{ sessionID, id string }{active.run.SessionID, active.run.ID})
	}
	r.mu.Unlock()
	for _, item := range ids {
		r.Cancel(item.sessionID, item.id)
	}
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (r *subagentRuntime) handleFrame(id string, frame stream.Frame) {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.terminalizing {
		r.mu.Unlock()
		return
	}
	sessionID := active.run.SessionID
	childRunID := active.run.ChildRunID
	parentRunID := active.run.ParentRunID
	parentToolCallID := active.run.ParentToolCallID
	providerID, modelID, reasoning := active.profile.Provider, active.profile.Model, active.profile.Reasoning
	switch frame.Kind {
	case stream.FrameThinking:
		if !noteVisibleContentLocked(active, frame.Thinking) {
			r.mu.Unlock()
			return
		}
		// Title is a stable kind key; the GUI localizes "thinking" → 思考 / Thinking.
		appendAgentDelta(&active.blocks, "thinking", childRunID, "thinking", frame.Thinking)
	case stream.FrameText:
		if !noteVisibleContentLocked(active, frame.Text) {
			r.mu.Unlock()
			return
		}
		kind := subagentTextKind(frame.TextPhase, false)
		appendAgentDelta(&active.blocks, kind, childRunID, kind, frame.Text)
	case stream.FrameToolCall:
		if frame.ToolCall != nil {
			settleSubagentProcessText(active.blocks, childRunID)
			active.ToolStarted = true
			active.run.ToolCalls++
			active.toolNames[frame.ToolCall.Name] = struct{}{}
			noteVisibleActivityLocked(active, frame.ToolCall.Name)
			active.blocks = append(active.blocks, AgentTranscriptBlock{
				ID: "call-" + frame.ToolCall.ID, Kind: "tool", RunID: childRunID, ToolCallID: frame.ToolCall.ID,
				Title: frame.ToolCall.Name, Content: string(frame.ToolCall.Arguments), State: "running",
			})
		}
	case stream.FrameToolResult:
		if frame.ToolResult != nil {
			noteVisibleActivityLocked(active, firstNonempty(frame.ToolResult.Name, "tool"))
			finishAgentToolBlock(active.blocks, frame.ToolResult.ToolCallID, frame.ToolResult.Content, frame.ToolResult.IsError)
		}
	case stream.FrameDone:
		noteVisibleActivityLocked(active, active.activity)
		active.run.Turns++
		active.usage.InputTokens += frame.Usage.InputTokens
		active.usage.OutputTokens += frame.Usage.OutputTokens
		active.usage.TotalTokens += frame.Usage.TotalTokens
		active.run.TokensUsed = active.usage.TotalTokens
	}
	r.mu.Unlock()
	r.persistActivity(id)
	// Push live roster stats on tool boundaries and turn ends. Thinking and
	// commentary also emit a throttled agent_state so the card preview survives
	// coalesced or dropped child deltas (UI-002). Elapsed ticks must not reset
	// the idle clock (SUBAGENT-005).
	if frame.Kind == stream.FrameToolCall || frame.Kind == stream.FrameToolResult || frame.Kind == stream.FrameDone ||
		frame.Kind == stream.FrameThinking || frame.Kind == stream.FrameText {
		r.emitLiveState(id, frame.Kind == stream.FrameToolCall || frame.Kind == stream.FrameDone)
	}

	event := Event{
		SessionID: sessionID, RunID: childRunID, AgentID: id, State: "running",
		Data: childFrameData(frame.Source, parentToolCallID, nil),
	}
	switch frame.Kind {
	case stream.FrameThinking:
		event.Kind = EventThinkingDelta
		event.Text = frame.Thinking
	case stream.FrameText:
		event.Kind = EventTextDelta
		event.Text = frame.Text
		event.TextPhase = string(frame.TextPhase)
	case stream.FrameToolCall:
		if frame.ToolCall == nil {
			return
		}
		event.Kind = EventToolStarted
		event.ToolCallID = frame.ToolCall.ID
		event.Data = childFrameData(frame.Source, parentToolCallID, map[string]string{
			"name": frame.ToolCall.Name, "arguments": string(frame.ToolCall.Arguments),
		})
	case stream.FrameToolResult:
		if frame.ToolResult == nil {
			return
		}
		event.Kind = EventToolFinished
		event.ToolCallID = frame.ToolResult.ToolCallID
		event.Text = frame.ToolResult.Content
		event.Data = childFrameData(frame.Source, parentToolCallID, map[string]string{"name": frame.ToolResult.Name})
		if len(frame.ToolResult.Structured) > 0 {
			event.Data["structured"] = string(frame.ToolResult.Structured)
		}
		if frame.ToolResult.IsError {
			event.State = "failed"
		} else {
			event.State = "completed"
			if summary, ok := toolview.CompletedFileChanges(frame.ToolResult.Name, "", string(frame.ToolResult.Structured), frame.ToolResult.Content); ok {
				event.Data["fileChange"] = toolview.EncodeSummary(summary)
			}
		}
	case stream.FrameDone:
		event.Kind = EventContextUsage
		event.RunID = parentRunID
		event.AgentID = ""
		event.State = "reported"
		event.Data = map[string]string{
			"inputTokens": fmt.Sprint(frame.Usage.InputTokens), "cachedInputTokens": fmt.Sprint(frame.Usage.CachedInputTokens),
			"uncachedInputTokens": fmt.Sprint(max(0, frame.Usage.InputTokens-frame.Usage.CachedInputTokens)),
			"outputTokens":        fmt.Sprint(frame.Usage.OutputTokens), "totalTokens": fmt.Sprint(frame.Usage.TotalTokens),
			"cacheStatus": "reported", "aggregateOnly": "true", "source": frame.Source, "requestKind": "subagent",
			"provider": providerID, "model": modelID, "reasoning": reasoning,
		}
	default:
		return
	}
	if parent := r.parentHost(id); parent != nil {
		if event.Kind == EventContextUsage {
			event.Data["transport"] = parent.ProviderTransport(providerID)
		}
		parent.EmitEvent(parent.BaseContext(), event)
	}
}

// emitLiveState pushes agent_state so the GUI can tick toolCalls / elapsed while streaming.
// force=true always emits (tool start / turn done); otherwise throttle to avoid event floods.
func (r *subagentRuntime) emitLiveState(id string, force bool) {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.terminalizing {
		r.mu.Unlock()
		return
	}
	if !force && time.Since(active.lastStateEmit) < 750*time.Millisecond && active.lastEmittedTools == active.run.ToolCalls {
		r.mu.Unlock()
		return
	}
	active.lastStateEmit = time.Now()
	active.lastEmittedTools = active.run.ToolCalls
	run := cloneSubagentRun(active.run)
	activity := active.activity
	r.mu.Unlock()
	r.emitState(run, activity)
}

func childFrameData(source, parentToolCallID string, values map[string]string) map[string]string {
	data := make(map[string]string, len(values)+2)
	for key, value := range values {
		data[key] = value
	}
	data["source"] = source
	data["parent_tool_call_id"] = parentToolCallID
	return data
}

func (r *subagentRuntime) handleToolUpdate(id string, update tool.Update) {
	r.mu.Lock()
	active := r.active[id]
	if active != nil && !active.terminalizing {
		noteVisibleActivityLocked(active, firstNonempty(update.Message, update.Kind))
		for index := len(active.blocks) - 1; index >= 0; index-- {
			if active.blocks[index].Kind == "tool" && active.blocks[index].State == "running" {
				appendAgentBlockContent(&active.blocks[index], update.Message)
				break
			}
		}
	}
	r.mu.Unlock()
	r.persistActivity(id)
}

func (r *subagentRuntime) parentHost(id string) providerHost {
	r.mu.Lock()
	defer r.mu.Unlock()
	if active := r.active[id]; active != nil {
		return active.parent.Host
	}
	return nil
}

func (r *subagentRuntime) persistActivity(id string) {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.parent.Host == nil || active.parent.Host.Sessions() == nil || active.activity == "" ||
		active.activity == active.persistedActivity || time.Since(active.lastActivityPersist) < 500*time.Millisecond {
		r.mu.Unlock()
		return
	}
	active.persistedActivity = active.activity
	active.lastActivityPersist = time.Now()
	run := cloneSubagentRun(active.run)
	activity := active.activity
	parent := active.parent.Host
	r.mu.Unlock()
	_ = parent.Sessions().UpsertAgentBlock(parent.BaseContext(), run.SessionID, run.ID, session.Block{
		Kind: "agent", RunID: run.ParentRunID, AgentID: run.ID, ParentToolCallID: run.ParentToolCallID,
		Title: run.Type, Content: activity, State: string(run.State),
	})
}

func (r *subagentRuntime) emitState(run agentservice.SubagentRun, activity string) {
	r.mu.Lock()
	active := r.active[run.ID]
	if active != nil && active.activity != "" {
		activity = active.activity
	}
	parent := providerHost(nil)
	if active != nil {
		parent = active.parent.Host
		active.persistedActivity = activity
		active.lastActivityPersist = time.Now()
	}
	r.mu.Unlock()
	r.emitStateTo(parent, run, activity)
}

func (r *subagentRuntime) emitStateTo(parent providerHost, run agentservice.SubagentRun, activity string) {
	if parent == nil {
		return
	}
	if parent.Sessions() != nil {
		content := firstNonempty(activity, run.Summary, run.Description)
		_ = parent.Sessions().UpsertAgentBlock(parent.BaseContext(), run.SessionID, run.ID, session.Block{
			Kind: "agent", RunID: run.ParentRunID, AgentID: run.ID, ParentToolCallID: run.ParentToolCallID,
			Title: run.Type, Content: content, State: string(run.State),
		})
	}
	parent.EmitEvent(parent.BaseContext(), subagentStateEvent(run, activity))
}

func subagentStateEvent(run agentservice.SubagentRun, activity string) Event {
	elapsed := snapshotFromRun(run).Elapsed
	return Event{
		Kind: EventAgentState, SessionID: run.SessionID, RunID: run.ParentRunID, AgentID: run.ID,
		State: string(run.State), Text: run.Summary,
		Agent: &AgentStatePayload{
			Type: run.Type, Description: run.Description, Model: run.Model, Background: run.Background,
			CapabilityMode: run.CapabilityMode, RequestedIsolation: run.RequestedIsolation, Isolation: run.Isolation,
			CWD: run.CWD, ParentRunID: run.ParentRunID, ParentToolCallID: run.ParentToolCallID,
			ChildRunID: run.ChildRunID, Activity: activity, Warning: run.Warning,
			EvidenceStatus: projectedEvidenceStatus(run.EvidenceStatus), WorktreePath: run.WorktreePath,
			ToolCalls: run.ToolCalls, Turns: run.Turns, TokensUsed: run.TokensUsed, ElapsedMS: elapsed.Milliseconds(),
		},
		Data: map[string]string{"id": run.ID, "role": run.Type, "state": string(run.State), "summary": run.Summary},
	}
}

func projectedEvidenceStatus(value string) string {
	switch value {
	case "provisional", "verified", "stale":
		return value
	default:
		// Evidence status is populated only by a durable result/disposition
		// producer. Missing or unknown data must remain visibly unverified.
		return ""
	}
}

func (r *subagentRuntime) signalChangedLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *subagentRuntime) parentDone(id string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if active := r.active[id]; active != nil {
		return active.done
	}
	closed := make(chan struct{})
	close(closed)
	return closed
}
