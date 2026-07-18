package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/go-hydaelyn/tool"
)

type todoDriver struct {
	sessionID string
	store     *session.Service
	emit      func(Event) bool
}
type todoInput struct {
	Op               string              `json:"op"`
	ExpectedRevision *int64              `json:"expected_revision,omitempty"`
	Goal             string              `json:"goal,omitempty"`
	Phases           []session.TodoPhase `json:"phases,omitempty"`
	ItemID           string              `json:"item_id,omitempty"`
	PhaseID          string              `json:"phase_id,omitempty"`
	Content          string              `json:"content,omitempty"`
}

func (d *todoDriver) Definition() tool.Definition {
	additional := false
	itemSchema := tool.Schema{Type: "object", Properties: map[string]tool.Schema{
		"id": {Type: "string"}, "content": {Type: "string"},
		"status": {Type: "string", Enum: []string{"pending", "in_progress", "completed", "cancelled"}},
	}, Required: []string{"content"}, AdditionalProperties: &additional}
	phaseSchema := tool.Schema{Type: "object", Properties: map[string]tool.Schema{
		"id": {Type: "string"}, "title": {Type: "string"},
		"items": {Type: "array", Items: &itemSchema},
	}, Required: []string{"title", "items"}, AdditionalProperties: &additional}
	return tool.Definition{Name: "todo", Description: "Maintain the durable session plan. Read with view; mutations require expected_revision from the latest snapshot.", InputSchema: tool.Schema{
		Type: "object", Properties: map[string]tool.Schema{
			"op": {Type: "string", Enum: []string{"init", "view", "start", "done", "append", "cancel", "remove"}}, "expected_revision": {Type: "integer"},
			"goal": {Type: "string"}, "phases": {Type: "array", Items: &phaseSchema}, "item_id": {Type: "string"}, "phase_id": {Type: "string"}, "content": {Type: "string"},
		}, Required: []string{"op"}, AdditionalProperties: &additional}, EffectType: tool.EffectWrite, RequiresApproval: false, RequiresActionTask: false, RiskLevel: "low", Metadata: map[string]string{"approval": "allow"}, PolicyTags: []string{"session", "todo"}}
}

func (d *todoDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var in todoInput
	if err := json.Unmarshal(call.Arguments, &in); err != nil {
		return todoResult(call, session.TodoList{}, fmt.Errorf("decode arguments: %w", err)), nil
	}
	if in.Op == "view" {
		todo, err := d.store.LoadTodo(ctx, d.sessionID)
		return todoResult(call, todo, err), nil
	}
	if in.ExpectedRevision == nil {
		return todoResult(call, session.TodoList{}, fmt.Errorf("expected_revision is required for %s", in.Op)), nil
	}
	todo, err := d.store.UpdateTodo(ctx, d.sessionID, *in.ExpectedRevision, func(todo *session.TodoList) error { return applyTodoOp(todo, in) })
	if err == nil && d.emit != nil {
		snapshot := todo.Clone()
		d.emit(Event{Kind: EventTodoUpdated, SessionID: d.sessionID, Todo: &snapshot})
	}
	return todoResult(call, todo, err), nil
}

func todoResult(call tool.Call, todo session.TodoList, err error) tool.Result {
	if err != nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
	}
	data, marshalErr := json.Marshal(todo)
	if marshalErr != nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: marshalErr.Error(), IsError: true}
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: string(data), Structured: data}
}

func applyTodoOp(todo *session.TodoList, in todoInput) error {
	find := func(id string) (*session.TodoItem, error) {
		for pi := range todo.Phases {
			for ii := range todo.Phases[pi].Items {
				if todo.Phases[pi].Items[ii].ID == id {
					return &todo.Phases[pi].Items[ii], nil
				}
			}
		}
		return nil, fmt.Errorf("todo item %q not found", id)
	}
	switch in.Op {
	case "init":
		if strings.TrimSpace(in.Goal) == "" {
			return fmt.Errorf("goal is required")
		}
		if len(in.Phases) == 0 {
			return fmt.Errorf("at least one todo phase is required")
		}
		for _, phase := range in.Phases {
			for _, item := range phase.Items {
				if item.SubagentRunID != "" {
					return fmt.Errorf("subagentRunId is owned by subagent.spawn")
				}
			}
		}
		todo.Goal = in.Goal
		todo.Phases = in.Phases
		hasCurrent := false
		for pi := range todo.Phases {
			for ii := range todo.Phases[pi].Items {
				item := &todo.Phases[pi].Items[ii]
				if item.Status == "" {
					item.Status = session.TodoPending
				}
				hasCurrent = hasCurrent || item.Status == session.TodoInProgress
			}
		}
		if !hasCurrent {
			advanceNext(todo)
		}
	case "start":
		item, err := find(in.ItemID)
		if err != nil {
			return err
		}
		for pi := range todo.Phases {
			for ii := range todo.Phases[pi].Items {
				other := &todo.Phases[pi].Items[ii]
				if other.Status == session.TodoInProgress && other.ID != item.ID {
					other.Status = session.TodoPending
				}
			}
		}
		if item.Status != session.TodoPending {
			return fmt.Errorf("only pending items can start")
		}
		item.Status = session.TodoInProgress
	case "done":
		item, err := find(in.ItemID)
		if err != nil {
			return err
		}
		if item.Status != session.TodoInProgress {
			return fmt.Errorf("only the current item can be completed")
		}
		item.Status = session.TodoCompleted
		advanceNext(todo)
	case "cancel":
		item, err := find(in.ItemID)
		if err != nil {
			return err
		}
		if item.Status == session.TodoCompleted || item.Status == session.TodoCancelled {
			return fmt.Errorf("todo item is already closed")
		}
		wasCurrent := item.Status == session.TodoInProgress
		item.Status = session.TodoCancelled
		if wasCurrent {
			advanceNext(todo)
		}
	case "append":
		content := strings.TrimSpace(in.Content)
		if content == "" {
			return fmt.Errorf("content is required")
		}
		for pi := range todo.Phases {
			if todo.Phases[pi].ID == in.PhaseID {
				todo.Phases[pi].Items = append(todo.Phases[pi].Items, session.TodoItem{Content: content, Status: session.TodoPending})
				if !hasInProgress(todo) {
					advanceNext(todo)
				}
				return nil
			}
		}
		return fmt.Errorf("todo phase %q not found", in.PhaseID)
	case "remove":
		if in.ItemID != "" {
			for pi := range todo.Phases {
				for ii, item := range todo.Phases[pi].Items {
					if item.ID == in.ItemID {
						if item.Status == session.TodoInProgress {
							return fmt.Errorf("cannot remove current item")
						}
						todo.Phases[pi].Items = append(todo.Phases[pi].Items[:ii], todo.Phases[pi].Items[ii+1:]...)
						return nil
					}
				}
			}
		} else if in.PhaseID != "" {
			for pi, p := range todo.Phases {
				if p.ID == in.PhaseID {
					for _, item := range p.Items {
						if item.Status == session.TodoInProgress {
							return fmt.Errorf("cannot remove phase with current item")
						}
					}
					todo.Phases = append(todo.Phases[:pi], todo.Phases[pi+1:]...)
					return nil
				}
			}
		}
		return fmt.Errorf("item_id or phase_id not found")
	default:
		return fmt.Errorf("unsupported todo op %q", in.Op)
	}
	return nil
}

func hasInProgress(todo *session.TodoList) bool {
	for _, phase := range todo.Phases {
		for _, item := range phase.Items {
			if item.Status == session.TodoInProgress {
				return true
			}
		}
	}
	return false
}

func advanceNext(todo *session.TodoList) {
	for pi := range todo.Phases {
		for ii := range todo.Phases[pi].Items {
			if todo.Phases[pi].Items[ii].Status == session.TodoPending {
				todo.Phases[pi].Items[ii].Status = session.TodoInProgress
				return
			}
		}
	}
}
