package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
	TodoCancelled  TodoStatus = "cancelled"
)

type TodoItem struct {
	ID            string     `json:"id"`
	Content       string     `json:"content"`
	Status        TodoStatus `json:"status"`
	SubagentRunID string     `json:"subagentRunId,omitempty"`
}

type TodoPhase struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Items []TodoItem `json:"items"`
}

type TodoList struct {
	Goal      string      `json:"goal"`
	Revision  int64       `json:"revision"`
	Phases    []TodoPhase `json:"phases"`
	UpdatedAt time.Time   `json:"updatedAt,omitempty"`
}

var ErrTodoRevisionConflict = errors.New("todo revision conflict")

func (t TodoList) Clone() TodoList {
	clone := t
	clone.Phases = make([]TodoPhase, len(t.Phases))
	for i := range t.Phases {
		clone.Phases[i] = t.Phases[i]
		clone.Phases[i].Items = append([]TodoItem(nil), t.Phases[i].Items...)
	}
	return clone
}

func (s *Service) LoadTodo(ctx context.Context, sessionID string) (TodoList, error) {
	var todo TodoList
	var phases []byte
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT goal,revision,phases,updated_at FROM session_todos WHERE session_id=?`, sessionID).Scan(&todo.Goal, &todo.Revision, &phases, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return TodoList{Phases: []TodoPhase{}}, nil
	}
	if err != nil {
		return TodoList{}, fmt.Errorf("load todo: %w", err)
	}
	if err := json.Unmarshal(phases, &todo.Phases); err != nil {
		return TodoList{}, fmt.Errorf("decode todo: %w", err)
	}
	todo.UpdatedAt = time.Unix(0, updated).UTC()
	return todo, nil
}

// UpdateTodo atomically applies fn when expectedRevision matches. The callback
// works on a clone and is fully validated before any state is committed.
func (s *Service) UpdateTodo(ctx context.Context, sessionID string, expectedRevision int64, fn func(*TodoList) error) (TodoList, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TodoList{}, err
	}
	defer tx.Rollback()
	var current TodoList
	var data []byte
	var updated int64
	err = tx.QueryRowContext(ctx, `SELECT goal,revision,phases,updated_at FROM session_todos WHERE session_id=?`, sessionID).Scan(&current.Goal, &current.Revision, &data, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		current.Phases = []TodoPhase{}
	} else if err != nil {
		return TodoList{}, err
	} else if err := json.Unmarshal(data, &current.Phases); err != nil {
		return TodoList{}, err
	}
	if current.Revision != expectedRevision {
		return TodoList{}, fmt.Errorf("%w: expected %d, current %d", ErrTodoRevisionConflict, expectedRevision, current.Revision)
	}
	next := current.Clone()
	if err := fn(&next); err != nil {
		return TodoList{}, err
	}
	if err := normalizeTodo(&next); err != nil {
		return TodoList{}, err
	}
	next.Revision++
	next.UpdatedAt = time.Now().UTC()
	encoded, err := json.Marshal(next.Phases)
	if err != nil {
		return TodoList{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO session_todos(session_id,goal,revision,phases,updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET goal=excluded.goal,revision=excluded.revision,phases=excluded.phases,updated_at=excluded.updated_at WHERE session_todos.revision=?`, sessionID, next.Goal, next.Revision, encoded, next.UpdatedAt.UnixNano(), expectedRevision)
	if err != nil {
		return TodoList{}, fmt.Errorf("update todo: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return TodoList{}, ErrTodoRevisionConflict
	}
	if err := tx.Commit(); err != nil {
		return TodoList{}, err
	}
	return next, nil
}

func normalizeTodo(todo *TodoList) error {
	seen := map[string]bool{}
	seenPhases := map[string]bool{}
	seenItems := map[string]bool{}
	inProgress := 0
	for pi := range todo.Phases {
		phase := &todo.Phases[pi]
		phase.Title = strings.TrimSpace(phase.Title)
		if phase.Title == "" {
			return fmt.Errorf("todo phase title is required")
		}
		phaseKey := strings.ToLower(phase.Title)
		if seenPhases[phaseKey] {
			return fmt.Errorf("duplicate todo phase %q", phase.Title)
		}
		seenPhases[phaseKey] = true
		if phase.ID == "" {
			phase.ID = todoID("phase")
		}
		if seen[phase.ID] {
			return fmt.Errorf("duplicate todo ID %q", phase.ID)
		}
		seen[phase.ID] = true
		for ii := range phase.Items {
			item := &phase.Items[ii]
			item.Content = strings.TrimSpace(item.Content)
			if item.Content == "" {
				return fmt.Errorf("todo item content is required")
			}
			itemKey := strings.ToLower(item.Content)
			if seenItems[itemKey] {
				return fmt.Errorf("duplicate todo item %q", item.Content)
			}
			seenItems[itemKey] = true
			if item.ID == "" {
				item.ID = todoID("item")
			}
			if seen[item.ID] {
				return fmt.Errorf("duplicate todo ID %q", item.ID)
			}
			seen[item.ID] = true
			if item.Status == "" {
				item.Status = TodoPending
			}
			switch item.Status {
			case TodoPending, TodoCompleted, TodoCancelled:
			case TodoInProgress:
				inProgress++
			default:
				return fmt.Errorf("invalid todo status %q", item.Status)
			}
		}
	}
	if inProgress > 1 {
		return fmt.Errorf("only one todo item may be in progress")
	}
	return nil
}

func todoID(prefix string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}
