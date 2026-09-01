package agent

import (
	"context"
	"reflect"
	"testing"
)

func TestInvocationContextRoundTripAndOverride(t *testing.T) {
	if got, ok := InvocationFromContext(nil); ok || got != (Invocation{}) {
		t.Fatalf("nil context invocation = %#v, %v", got, ok)
	}
	if got, ok := InvocationFromContext(context.Background()); ok || got != (Invocation{}) {
		t.Fatalf("empty context invocation = %#v, %v", got, ok)
	}

	want := Invocation{
		SessionID: "session", RunID: "run", ExecutionID: "execution", AgentID: "agent",
		ParentRunID: "parent-run", ParentToolCallID: "parent-call", TaskID: "task",
		TeamRunID: "team-run", Workspace: "/workspace",
	}
	ctx := WithInvocation(context.Background(), want)
	if got, ok := InvocationFromContext(ctx); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("invocation = %#v, %v; want %#v", got, ok, want)
	}

	replacement := Invocation{SessionID: "other", RunID: "other-run"}
	if got, ok := InvocationFromContext(WithInvocation(ctx, replacement)); !ok || !reflect.DeepEqual(got, replacement) {
		t.Fatalf("overridden invocation = %#v, %v; want %#v", got, ok, replacement)
	}
	if got, _ := InvocationFromContext(ctx); !reflect.DeepEqual(got, want) {
		t.Fatalf("parent invocation mutated = %#v; want %#v", got, want)
	}
}
