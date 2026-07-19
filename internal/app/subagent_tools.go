package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/go-hydaelyn/tool"
)

type subagentSpawnInput struct {
	Prompt            string
	Description       string
	TodoItemID        string
	SubagentType      string
	SubagentTypeSet   bool
	Background        bool
	BackgroundSet     bool
	CapabilityMode    string
	CapabilityModeSet bool
	Isolation         string
	IsolationSet      bool
	ResumeFrom        string
	CWD               string
	CWDSet            bool
	Model             string
	ModelSet          bool
	parentToolCallID  string
}

type subagentSpawnDriver struct {
	runtime *subagentRuntime
	parent  subagentParentRuntime
}

func (d *subagentSpawnDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: subagentSpawnTool, Description: "Spawn one supervised subagent task. Returns a durable task ID for background work.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"prompt", "description"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"prompt": {Type: "string"}, "description": {Type: "string"}, "subagent_type": {Type: "string"},
				"todo_item_id": {Type: "string"},
				"background":   {Type: "boolean"}, "capability_mode": {Type: "string", Enum: []string{"read-only", "read-write", "execute", "all"}},
				"isolation": {Type: "string", Enum: []string{"none", "worktree"}}, "resume_from": {Type: "string"},
				"cwd": {Type: "string"}, "model": {Type: "string"},
			},
		},
		EffectType: tool.EffectReadOnly, PolicyTags: []string{"subagent", "spawn"},
	}
}

func (d *subagentSpawnDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	input, err := decodeSubagentSpawnInput(call.Arguments)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	input.parentToolCallID = call.ID
	todoRevision, err := prepareSubagentTodoBinding(ctx, d.parent, input.TodoItemID)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	var beforeEnqueue func(agentservice.SubagentRun) error
	if input.TodoItemID != "" {
		beforeEnqueue = func(run agentservice.SubagentRun) error {
			return commitSubagentTodoBinding(ctx, d.parent, input.TodoItemID, run.ID, todoRevision)
		}
	}
	run, err := d.runtime.spawn(input, d.parent, beforeEnqueue)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	if input.Background {
		return subagentJSONResult(call, map[string]any{"task_id": run.ID, "status": string(run.State), "description": run.Description, "type": run.Type, "warning": run.Warning}), nil
	}
	snapshot := d.runtime.waitForForegroundStart(ctx, run.SessionID, run.ID)
	done := d.runtime.parentDone(run.ID)
	if snapshot.Found && subagentTerminal(snapshot.Run.State) {
		_ = d.runtime.store.SetCompletionDelivered(d.runtime.ctx, run.ID, true)
		return subagentJSONResult(call, foregroundSubagentResult(snapshot)), nil
	}
	timer := time.NewTimer(d.runtime.cfg.AwaitDuration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		warning := "foreground wait interrupted; task continues in background"
		d.runtime.demote(run.SessionID, run.ID, warning)
		snapshot := d.runtime.snapshot(run.ID, run.SessionID)
		return subagentJSONResult(call, map[string]any{
			"task_id": run.ID, "status": string(snapshot.Run.State), "description": run.Description, "type": run.Type,
			"warning": snapshot.Run.Warning,
		}), nil
	case <-timer.C:
		warning := fmt.Sprintf("foreground wait timed out after %s; task continues in background", d.runtime.cfg.AwaitDuration)
		d.runtime.demote(run.SessionID, run.ID, warning)
		snapshot := d.runtime.snapshot(run.ID, run.SessionID)
		return subagentJSONResult(call, map[string]any{
			"task_id": run.ID, "status": string(snapshot.Run.State), "description": run.Description, "type": run.Type,
			"warning": snapshot.Run.Warning,
		}), nil
	case <-done:
	}
	snapshot = d.runtime.snapshot(run.ID, run.SessionID)
	if snapshot.Found && subagentTerminal(snapshot.Run.State) {
		_ = d.runtime.store.SetCompletionDelivered(d.runtime.ctx, run.ID, true)
	}
	return subagentJSONResult(call, foregroundSubagentResult(snapshot)), nil
}

func prepareSubagentTodoBinding(ctx context.Context, parent subagentParentRuntime, itemID string) (int64, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return 0, nil
	}
	if parent.Host == nil || parent.Host.sessions == nil {
		return 0, fmt.Errorf("todo store is unavailable")
	}
	todo, err := parent.Host.sessions.LoadTodo(ctx, parent.SessionID)
	if err != nil {
		return 0, err
	}
	for _, phase := range todo.Phases {
		for _, item := range phase.Items {
			if item.ID != itemID {
				continue
			}
			if item.Status != session.TodoPending && item.Status != session.TodoInProgress {
				return 0, fmt.Errorf("todo item %q is closed", itemID)
			}
			if item.SubagentRunID != "" {
				return 0, fmt.Errorf("todo item %q is already assigned", itemID)
			}
			return todo.Revision, nil
		}
	}
	return 0, fmt.Errorf("todo item %q not found", itemID)
}

func commitSubagentTodoBinding(ctx context.Context, parent subagentParentRuntime, itemID, runID string, expectedRevision int64) error {
	updated, err := parent.Host.sessions.UpdateTodo(ctx, parent.SessionID, expectedRevision, func(todo *session.TodoList) error {
		for pi := range todo.Phases {
			for ii := range todo.Phases[pi].Items {
				item := &todo.Phases[pi].Items[ii]
				if item.ID == itemID {
					if item.SubagentRunID != "" {
						return fmt.Errorf("todo item %q is already assigned", itemID)
					}
					item.SubagentRunID = runID
					return nil
				}
			}
		}
		return fmt.Errorf("todo item %q not found", itemID)
	})
	if err != nil {
		return err
	}
	snapshot := updated.Clone()
	parent.Host.emitTodoUpdated(parent.SessionID, snapshot)
	return nil
}

func (r *subagentRuntime) demote(sessionID, id, warning string) {
	r.mu.Lock()
	active := r.active[id]
	if active == nil || active.run.SessionID != sessionID || active.run.Background || active.terminalizing {
		r.mu.Unlock()
		return
	}
	active.run.Background = true
	active.run.Warning = appendWarning(active.run.Warning, warning)
	run := cloneSubagentRun(active.run)
	r.mu.Unlock()
	if r.store.Save(r.ctx, run) != nil {
		r.terminalize(id, terminalRequest{state: agentservice.SubagentFailed, err: errors.New("persist foreground demotion")})
		return
	}
	r.mu.Lock()
	if active = r.active[id]; active != nil && !active.terminalizing {
		active.run = run
	}
	r.mu.Unlock()
	r.emitState(run, "running in background")
}

type subagentGetOutputDriver struct {
	runtime   *subagentRuntime
	sessionID string
}

func (d *subagentGetOutputDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: subagentGetOutputTool, Description: "Get ordered snapshots for one or more supervised subagent task IDs, optionally waiting for terminal states.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"task_ids"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
			"task_ids": {Type: "array", Items: &tool.Schema{Type: "string"}}, "timeout_ms": {Type: "integer"},
		}}, EffectType: tool.EffectReadOnly, PolicyTags: []string{"subagent", "query"},
	}
}

func (d *subagentGetOutputDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	ids, timeout, err := decodeSubagentQueryInput(call.Arguments)
	if err != nil {
		return subagentToolError(call, err), nil
	}
	snapshots := d.runtime.Query(ctx, d.sessionID, ids, timeout)
	tasks := make([]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		tasks = append(tasks, subagentSnapshotJSON(snapshot))
		if snapshot.Found && subagentTerminal(snapshot.Run.State) {
			_ = d.runtime.store.SetCompletionDelivered(d.runtime.ctx, snapshot.Run.ID, true)
		}
	}
	return subagentJSONResult(call, map[string]any{"tasks": tasks}), nil
}

type subagentKillDriver struct {
	runtime   *subagentRuntime
	sessionID string
}

func (d *subagentKillDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: subagentKillTool, Description: "Request cancellation of one supervised subagent task without cancelling the parent run.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"task_id"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"task_id": {Type: "string"}}},
		EffectType:  tool.EffectReadOnly, PolicyTags: []string{"subagent", "cancel"},
	}
}

func (d *subagentKillDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return subagentToolError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		return subagentToolError(call, fmt.Errorf("task_id is required")), nil
	}
	outcome := d.runtime.Cancel(d.sessionID, input.TaskID)
	status := "not_found"
	if outcome.Snapshot.Found {
		status = string(outcome.Snapshot.Run.State)
	}
	return subagentJSONResult(call, map[string]any{"task_id": input.TaskID, "outcome": outcome.Outcome, "status": status}), nil
}

func decodeSubagentSpawnInput(arguments json.RawMessage) (subagentSpawnInput, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &raw); err != nil {
		return subagentSpawnInput{}, fmt.Errorf("decode arguments: %w", err)
	}
	prompt, err := requiredRawString(raw, "prompt")
	if err != nil {
		return subagentSpawnInput{}, err
	}
	description, err := requiredRawString(raw, "description")
	if err != nil {
		return subagentSpawnInput{}, err
	}
	input := subagentSpawnInput{Prompt: prompt, Description: description}
	decodeString := func(key string, target *string, set *bool) error {
		value, present, decodeErr := optionalRawString(raw, key)
		if decodeErr != nil {
			return decodeErr
		}
		if present {
			*target = value
			if set != nil {
				*set = true
			}
		}
		return nil
	}
	for _, item := range []struct {
		key    string
		target *string
		set    *bool
	}{
		{key: "subagent_type", target: &input.SubagentType, set: &input.SubagentTypeSet},
		{key: "todo_item_id", target: &input.TodoItemID},
		{key: "capability_mode", target: &input.CapabilityMode, set: &input.CapabilityModeSet},
		{key: "isolation", target: &input.Isolation, set: &input.IsolationSet},
		{key: "resume_from", target: &input.ResumeFrom},
		{key: "cwd", target: &input.CWD, set: &input.CWDSet},
		{key: "model", target: &input.Model, set: &input.ModelSet},
	} {
		if err := decodeString(item.key, item.target, item.set); err != nil {
			return subagentSpawnInput{}, err
		}
	}
	if encoded, present := raw["background"]; present && string(encoded) != "null" {
		if err := json.Unmarshal(encoded, &input.Background); err != nil {
			return subagentSpawnInput{}, fmt.Errorf("background must be a boolean")
		}
		input.BackgroundSet = true
	}
	if !input.BackgroundSet {
		input.Background = true
	}
	if input.ResumeFrom == "" {
		if !input.SubagentTypeSet {
			input.SubagentType = "general-purpose"
		}
		if !input.IsolationSet {
			input.Isolation = "none"
		}
		switch input.CapabilityMode {
		case "", "read-only", "read-write", "execute", "all":
		default:
			return subagentSpawnInput{}, fmt.Errorf("capability_mode is invalid")
		}
		if input.Isolation != "none" && input.Isolation != "worktree" {
			return subagentSpawnInput{}, fmt.Errorf("isolation must be none or worktree")
		}
		if input.CWDSet && input.Isolation == "worktree" {
			return subagentSpawnInput{}, fmt.Errorf("cwd and isolation=worktree are mutually exclusive")
		}
	}
	return input, nil
}

func decodeSubagentQueryInput(arguments json.RawMessage) ([]string, time.Duration, error) {
	var input struct {
		TaskIDs   []string `json:"task_ids"`
		TimeoutMS int      `json:"timeout_ms"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return nil, 0, fmt.Errorf("decode arguments: %w", err)
	}
	if input.TimeoutMS < 0 || input.TimeoutMS > 600_000 {
		return nil, 0, fmt.Errorf("timeout_ms must be between 0 and 600000")
	}
	seen := make(map[string]bool)
	ids := make([]string, 0, len(input.TaskIDs))
	for _, id := range input.TaskIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) < 1 || len(ids) > 20 {
		return nil, 0, fmt.Errorf("task_ids must contain 1 to 20 unique non-empty IDs")
	}
	return ids, time.Duration(input.TimeoutMS) * time.Millisecond, nil
}

func requiredRawString(raw map[string]json.RawMessage, key string) (string, error) {
	value, present, err := optionalRawString(raw, key)
	if err != nil {
		return "", err
	}
	if !present || value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func optionalRawString(raw map[string]json.RawMessage, key string) (string, bool, error) {
	encoded, present := raw[key]
	if !present || string(encoded) == "null" {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		return "", false, fmt.Errorf("%s must be a string", key)
	}
	value = strings.Trim(strings.TrimSpace(value), "`\"")
	switch strings.ToLower(value) {
	case "", "null", "none", "undefined":
		return "", false, nil
	default:
		return value, true, nil
	}
}

func foregroundSubagentResult(snapshot agentservice.SubagentSnapshot) map[string]any {
	if !snapshot.Found {
		return map[string]any{"task_id": snapshot.Run.ID, "status": "not_found"}
	}
	run := snapshot.Run
	return map[string]any{
		"task_id": run.ID, "status": string(run.State), "output": run.Output, "error": run.Error, "warning": run.Warning,
		"usage": map[string]any{"tool_calls": run.ToolCalls, "turns": run.Turns, "tokens_used": run.TokensUsed},
	}
}

func subagentSnapshotJSON(snapshot agentservice.SubagentSnapshot) map[string]any {
	if !snapshot.Found {
		return map[string]any{"task_id": snapshot.Run.ID, "status": "not_found"}
	}
	run := snapshot.Run
	return map[string]any{
		"task_id": run.ID, "status": string(run.State), "description": run.Description, "type": run.Type,
		"model": run.Model, "background": run.Background, "capability_mode": run.CapabilityMode,
		"requested_isolation": run.RequestedIsolation, "isolation": run.Isolation, "cwd": run.CWD,
		"elapsed_ms": snapshot.Elapsed.Milliseconds(), "tool_calls": run.ToolCalls, "turns": run.Turns,
		"tokens_used": run.TokensUsed, "tools_used": run.ToolsUsed, "output": run.Output, "error": run.Error,
		"warning": run.Warning, "worktree_path": run.WorktreePath,
	}
}

func subagentJSONResult(call tool.Call, value any) tool.Result {
	encoded, err := json.Marshal(value)
	if err != nil {
		return subagentToolError(call, fmt.Errorf("encode subagent result: %w", err))
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: string(encoded)}
}

func subagentToolError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
}
