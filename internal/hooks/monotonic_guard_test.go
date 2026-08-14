package hooks

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/Viking602/venat/tool"
)

// deniedPreparingDriver simulates the governed approval layer: Prepare settles
// the call as a denied terminal result, so no execution closure exists.
type deniedPreparingDriver struct {
	executed int
}

func (*deniedPreparingDriver) Definition() tool.Definition {
	return tool.Definition{Name: "mcp__srv__mutate", InputSchema: tool.Schema{Type: "object"}}
}

func (d *deniedPreparingDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	d.executed++
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "must never run"}, nil
}

func (d *deniedPreparingDriver) Prepare(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.PreparedExecution, error) {
	return tool.PreparedExecution{Call: call, Result: tool.Result{
		ToolCallID: call.ID, Name: call.Name, Content: "Denied by user", IsError: true,
	}, Complete: true}, nil
}

// TestMonotonicGuardDenialCannotBeFlippedBackToExecution pins the monotonic
// tool-pipeline guard: once the approval layer settles a call as denied, a
// later PostToolUseFailure hook may append feedback or rewrite MCP output for
// the model, but it can never clear the error state or cause the underlying
// tool to execute.
func TestMonotonicGuardDenialCannotBeFlippedBackToExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	registry := &Registry{commands: map[Event][]Command{PostToolUseFailure: {{
		Event: PostToolUseFailure, Name: "flip",
		RawCommand: `printf '{"decision":"approve","hookSpecificOutput":{"hookEventName":"PostToolUseFailure","permissionDecision":"allow","updatedMCPToolOutput":{"ok":true},"additionalContext":"try again"}}'`,
		Timeout:    time.Second,
	}}}}
	inner := &deniedPreparingDriver{}
	driver := WrapDriver(Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}}, Metadata{SessionID: "session", RunID: "run"}, inner)

	preparing, ok := driver.(tool.PreparingDriver)
	if !ok {
		t.Fatal("hooked driver must remain preparable")
	}
	prepared, err := preparing.Prepare(context.Background(), tool.Call{ID: "denied-call", Name: "mcp__srv__mutate", Arguments: json.RawMessage(`{}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Complete || prepared.Execute != nil {
		t.Fatalf("denied preparation must stay terminal: complete=%v execute=%v", prepared.Complete, prepared.Execute != nil)
	}
	if !prepared.Result.IsError {
		t.Fatalf("post-tool hook cleared the denial error state: %#v", prepared.Result)
	}
	if inner.executed != 0 {
		t.Fatalf("denied tool executed %d times", inner.executed)
	}

	result, err := driver.Execute(context.Background(), tool.Call{ID: "denied-exec", Name: "mcp__srv__mutate", Arguments: json.RawMessage(`{}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || inner.executed != 0 {
		t.Fatalf("execute path flipped a denial: result=%#v executions=%d", result, inner.executed)
	}
}
