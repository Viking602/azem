package cursor

import (
	"context"

	"github.com/Viking602/venat/message"
)

type TodoSnapshotItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type TodoSnapshot struct {
	Items  []TodoSnapshotItem `json:"items"`
	Merged bool               `json:"merged"`
}

type TodoSync func(context.Context, TodoSnapshot, string, string) HostResult

// ExecHost runs a mapped Azem tool while the Cursor Run stream stays open.
type ExecHost interface {
	Execute(ctx context.Context, call message.ToolCall) (HostResult, error)
}

type TodoSynchronizer interface {
	SyncTodos(context.Context, TodoSnapshot, string, string) HostResult
}

// HostResult is the text Cursor should see in a typed exec result.
type HostResult struct {
	Content string
	IsError bool
	Code    string
}
