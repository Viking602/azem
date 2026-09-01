package agent

import "context"

// Invocation is Azem-owned request identity for one provider or tool call. It
// is process-local context and is never serialized into provider wire data or
// durable continuations.
type Invocation struct {
	SessionID        string
	RunID            string
	ExecutionID      string
	AgentID          string
	ParentRunID      string
	ParentToolCallID string
	TaskID           string
	TeamRunID        string
	Workspace        string
}

type invocationContextKey struct{}

func WithInvocation(ctx context.Context, invocation Invocation) context.Context {
	return context.WithValue(ctx, invocationContextKey{}, invocation)
}

func InvocationFromContext(ctx context.Context) (Invocation, bool) {
	if ctx == nil {
		return Invocation{}, false
	}
	invocation, ok := ctx.Value(invocationContextKey{}).(Invocation)
	return invocation, ok
}
