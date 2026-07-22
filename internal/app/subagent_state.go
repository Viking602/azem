package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/stream"
	"github.com/Viking602/go-hydaelyn/tool"
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

func (r *subagentRuntime) List(_ context.Context, sessionID string) []agentservice.SubagentSnapshot {
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
	return result
}

func (r *subagentRuntime) Detail(ctx context.Context, sessionID, id string) ([]AgentTranscriptBlock, error) {
	r.mu.Lock()
	if active := r.active[id]; active != nil && active.run.SessionID == sessionID {
		blocks := append([]AgentTranscriptBlock(nil), active.blocks...)
		r.mu.Unlock()
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
	if err := r.store.Save(r.ctx, cancelling); err != nil {
		r.mu.Unlock()
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: fmt.Errorf("persist cancelling subagent: %w", err)})
		return agentservice.SubagentCancelOutcome{Outcome: "cancel_requested", Snapshot: r.snapshot(id, sessionID)}
	}
	active.run = cancelling
	snapshot := r.snapshotFromActiveLocked(active)
	cancel := active.cancel
	queued := !active.slot
	r.mu.Unlock()
	r.emitState(cancelling, "cancelling")
	cancel()
	if queued {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentCancelled})
	}
	return agentservice.SubagentCancelOutcome{Outcome: "cancel_requested", Snapshot: snapshot}
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
		active.activity = compactActivity(frame.Thinking)
		appendAgentDelta(&active.blocks, "thinking", childRunID, "Thinking", frame.Thinking)
	case stream.FrameText:
		active.activity = compactActivity(frame.Text)
		appendAgentDelta(&active.blocks, "assistant", childRunID, "Assistant", frame.Text)
	case stream.FrameToolCall:
		if frame.ToolCall != nil {
			active.ToolStarted = true
			active.run.ToolCalls++
			active.toolNames[frame.ToolCall.Name] = struct{}{}
			active.activity = frame.ToolCall.Name
			active.blocks = append(active.blocks, AgentTranscriptBlock{
				ID: "call-" + frame.ToolCall.ID, Kind: "tool", RunID: childRunID, ToolCallID: frame.ToolCall.ID,
				Title: frame.ToolCall.Name, Content: string(frame.ToolCall.Arguments), State: "running",
			})
		}
	case stream.FrameToolResult:
		if frame.ToolResult != nil {
			finishAgentToolBlock(active.blocks, frame.ToolResult.ToolCallID, frame.ToolResult.Content, frame.ToolResult.IsError)
		}
	case stream.FrameDone:
		active.run.Turns++
		active.usage.InputTokens += frame.Usage.InputTokens
		active.usage.OutputTokens += frame.Usage.OutputTokens
		active.usage.TotalTokens += frame.Usage.TotalTokens
		active.run.TokensUsed = active.usage.TotalTokens
	}
	r.mu.Unlock()
	r.persistActivity(id)

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
			event.Data["transport"] = parent.providerTransport(providerID)
		}
		if !parent.emit(parent.ctx, event) {
			r.Cancel(sessionID, id)
		}
	}
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
		active.activity = compactActivity(firstNonempty(update.Message, update.Kind))
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

func (r *subagentRuntime) parentHost(id string) *Service {
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
	if active == nil || active.parent.Host == nil || active.parent.Host.sessions == nil || active.activity == "" ||
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
	_ = parent.sessions.UpsertAgentBlock(parent.ctx, run.SessionID, run.ID, session.Block{
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
	parent := (*Service)(nil)
	if active != nil {
		parent = active.parent.Host
		active.persistedActivity = activity
		active.lastActivityPersist = time.Now()
	}
	r.mu.Unlock()
	r.emitStateTo(parent, run, activity)
}

func (r *subagentRuntime) emitStateTo(parent *Service, run agentservice.SubagentRun, activity string) {
	if parent == nil {
		return
	}
	if parent.sessions != nil {
		content := firstNonempty(activity, run.Summary, run.Description)
		_ = parent.sessions.UpsertAgentBlock(parent.ctx, run.SessionID, run.ID, session.Block{
			Kind: "agent", RunID: run.ParentRunID, AgentID: run.ID, ParentToolCallID: run.ParentToolCallID,
			Title: run.Type, Content: content, State: string(run.State),
		})
	}
	parent.emit(parent.ctx, subagentStateEvent(run, activity))
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
			ChildRunID: run.ChildRunID, Activity: activity, Warning: run.Warning, WorktreePath: run.WorktreePath,
			ToolCalls: run.ToolCalls, Turns: run.Turns, TokensUsed: run.TokensUsed, ElapsedMS: elapsed.Milliseconds(),
		},
		Data: map[string]string{"id": run.ID, "role": run.Type, "state": string(run.State), "summary": run.Summary},
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
