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

type (
	TodoSync           func(context.Context, TodoSnapshot, string, string) HostResult
	execHostContextKey struct{}
)

// WithExecHost binds Azem's request-scoped native Cursor host without placing
// process objects in provider.Request or provider wire data.
func WithExecHost(ctx context.Context, host ExecHost) context.Context {
	return context.WithValue(ctx, execHostContextKey{}, host)
}

// ExecHostFromContext returns the request-scoped native Cursor host, if any.
func ExecHostFromContext(ctx context.Context) ExecHost {
	if ctx == nil {
		return nil
	}
	host, _ := ctx.Value(execHostContextKey{}).(ExecHost)
	return host
}

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
