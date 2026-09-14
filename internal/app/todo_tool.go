package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/tool"
)

type todoDriver struct {
	sessionID      string
	store          *session.Service
	emit           func(Event) bool
	beforeComplete func(context.Context) error
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

const todoItemLabelDescription = "User-visible task title, not execution instructions. Use the current user message language (or their explicitly requested language), even when tool results or host checks are English. Use one concise action and object, ideally 8-24 Chinese characters or 3-8 English words. Preserve essential identifiers, but do not paste shell commands, file lists, verification policies, or reporting disclaimers. For example: 核对修改并运行相关测试; 整理验证结果."

func (d *todoDriver) Definition() tool.Definition {
	additional := false
	itemSchema := tool.Schema{Type: "object", Properties: map[string]tool.Schema{
		"content": {Type: "string", Description: todoItemLabelDescription},
	}, Required: []string{"content"}, AdditionalProperties: &additional}
	phaseSchema := tool.Schema{Type: "object", Properties: map[string]tool.Schema{
		"title": {Type: "string", Description: "Short phase label in the current user message language; follow an explicit language request. Describe the outcome, not a command sequence."},
		"items": {Type: "array", Items: &itemSchema},
	}, Required: []string{"title", "items"}, AdditionalProperties: &additional}
	return tool.Definition{Name: "todo", Description: "Maintain the durable session plan. Goal, phase titles and item content are user-visible: use the current user message language, concise outcome labels, and no raw command lists or host-policy text. init accepts a goal, phase titles, and item content; IDs and status are host-assigned and must be omitted. A complete init payload may omit op; every other operation requires it. Reconcile later user guidance before acting: append each distinct added deliverable to an existing phase, cancel withdrawn open work, and remove only an explicitly erased non-current item. After completing one item, immediately call done by itself and wait for the returned snapshot before continuing; never batch mutating todo calls. The final done is accepted only after current verification evidence passes; run required checks while that item is still in progress. Use read-only verify before choosing final checks to get current missing commands and readbacks without completing an item. A done that leaves one open item also includes this verification preview; ready=false requires resolving its details, while the returned Todo revision is already current. done automatically advances the next pending item, so do not follow it with start. init may omit expected_revision and safely replaces the latest stored plan; later mutations require expected_revision from the latest snapshot. Read with view when the latest revision is unknown.", InputSchema: tool.Schema{
		Type: "object", Properties: map[string]tool.Schema{
			"op": {Type: "string", Enum: []string{"init", "view", "verify", "start", "done", "append", "cancel", "remove"}}, "expected_revision": {Type: "integer"},
			"goal": {Type: "string"}, "phases": {Type: "array", Items: &phaseSchema}, "item_id": {Type: "string"}, "phase_id": {Type: "string"}, "content": {Type: "string", Description: todoItemLabelDescription},
		}, AdditionalProperties: &additional,
	}}
}

func (d *todoDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var in todoInput
	if err := json.Unmarshal(call.Arguments, &in); err != nil {
		return todoResult(call, session.TodoList{}, fmt.Errorf("decode arguments: %w", err)), nil
	}
	if strings.TrimSpace(in.Op) == "" && strings.TrimSpace(in.Goal) != "" && len(in.Phases) > 0 {
		in.Op = "init"
	}
	if strings.TrimSpace(in.Op) == "" {
		return todoResult(call, session.TodoList{}, fmt.Errorf("op is required unless goal and phases define init")), nil
	}
	if in.Op == "view" || in.Op == "verify" {
		todo, err := d.store.LoadTodo(ctx, d.sessionID)
		if err == nil && in.Op == "verify" {
			return d.verificationPreview(ctx, call, todo), nil
		}
		return todoResult(call, todo, err), nil
	}
	expectedRevision := int64(0)
	if in.ExpectedRevision != nil {
		expectedRevision = *in.ExpectedRevision
	} else if in.Op == "init" {
		current, err := d.store.LoadTodo(ctx, d.sessionID)
		if err != nil {
			return todoResult(call, session.TodoList{}, err), nil
		}
		expectedRevision = current.Revision
	} else {
		return todoResult(call, session.TodoList{}, fmt.Errorf("expected_revision is required for %s", in.Op)), nil
	}
	if in.Op == "done" && d.beforeComplete != nil {
		current, err := d.store.LoadTodo(ctx, d.sessionID)
		if err != nil {
			return todoResult(call, current, err), nil
		}
		if current.Revision != expectedRevision {
			return todoResult(call, current, session.ErrTodoRevisionConflict), nil
		}
		next := current.Clone()
		if err := applyTodoOp(&next, in); err != nil {
			return todoResult(call, current, err), nil
		}
		if len(incompleteTodoItems(next)) == 0 {
			if err := d.beforeComplete(ctx); err != nil {
				return todoResult(call, current, err), nil
			}
		}
	}
	todo, err := d.store.UpdateTodo(ctx, d.sessionID, expectedRevision, func(todo *session.TodoList) error { return applyTodoOp(todo, in) })
	if err == nil && d.emit != nil {
		snapshot := todo.Clone()
		d.emit(Event{Kind: EventTodoUpdated, SessionID: d.sessionID, Todo: &snapshot})
	}
	if err == nil && in.Op == "done" && len(incompleteTodoItems(todo)) == 1 && d.beforeComplete != nil {
		return d.verificationPreview(ctx, call, todo), nil
	}
	return todoResult(call, todo, err), nil
}

// Preview uses the same gate as final completion without changing Todo state.
// A missing check does not turn an already successful Todo mutation into an error.
func (d *todoDriver) verificationPreview(ctx context.Context, call tool.Call, todo session.TodoList) tool.Result {
	if d.beforeComplete == nil {
		return todoResult(call, todo, fmt.Errorf("verification is unavailable for this run"))
	}
	result := struct {
		session.TodoList
		Verification struct {
			Ready   bool   `json:"ready"`
			Details string `json:"details,omitempty"`
		} `json:"verification"`
	}{TodoList: todo}
	if err := d.beforeComplete(ctx); err != nil {
		result.Verification.Details = err.Error()
	} else {
		result.Verification.Ready = true
	}
	data, err := json.Marshal(result)
	if err != nil {
		return todoResult(call, todo, err)
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: string(data), Structured: data}
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
			if phase.ID != "" {
				return fmt.Errorf("todo phase IDs are host-assigned")
			}
			for _, item := range phase.Items {
				if item.ID != "" {
					return fmt.Errorf("todo item IDs are host-assigned")
				}
				if item.Status != "" {
					return fmt.Errorf("todo item status is host-assigned")
				}
				if item.SubagentRunID != "" {
					return fmt.Errorf("subagentRunId is owned by subagent.spawn")
				}
			}
		}
		todo.Goal = in.Goal
		todo.Phases = in.Phases
		for pi := range todo.Phases {
			for ii := range todo.Phases[pi].Items {
				todo.Phases[pi].Items[ii].Status = session.TodoPending
			}
		}
		advanceNext(todo)
	case "start":
		item, err := find(in.ItemID)
		if err != nil {
			return err
		}
		for pi := range todo.Phases {
			for ii := range todo.Phases[pi].Items {
				other := &todo.Phases[pi].Items[ii]
				if other.Status == session.TodoInProgress && other.ID != item.ID {
					return fmt.Errorf("todo item %q is already in progress", other.ID)
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
