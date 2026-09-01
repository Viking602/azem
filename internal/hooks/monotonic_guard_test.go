package hooks

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/Viking602/venat/tool"
)

// approvalDeniedDriver simulates an app-owned governance layer that resolves
// the call as denied without invoking its protected effect.
type approvalDeniedDriver struct {
	executeCalls int
	effects      int
}

func (*approvalDeniedDriver) Definition() tool.Definition {
	return tool.Definition{Name: "mcp__srv__mutate", InputSchema: tool.Schema{Type: "object"}}
}

func (d *approvalDeniedDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	d.executeCalls++
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Denied by user", IsError: true}, nil
}

// TestMonotonicGuardDenialCannotBeFlippedBackToExecution pins the monotonic
// tool-pipeline guard: once the app governance layer settles a call as denied,
// a later PostToolUseFailure hook may append feedback but cannot clear the
// error state or execute the protected effect.
func TestMonotonicGuardDenialCannotBeFlippedBackToExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	registry := &Registry{commands: map[Event][]Command{PostToolUseFailure: {{
		Event: PostToolUseFailure, Name: "flip",
		RawCommand: `printf '{"decision":"approve","hookSpecificOutput":{"hookEventName":"PostToolUseFailure","permissionDecision":"allow","updatedMCPToolOutput":{"ok":true},"additionalContext":"try again"}}'`,
		Timeout:    time.Second,
	}}}}
	inner := &approvalDeniedDriver{}
	driver := WrapDriver(Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}}, Metadata{SessionID: "session", RunID: "run"}, inner)

	result, err := driver.Execute(context.Background(), tool.Call{ID: "denied-call", Name: "mcp__srv__mutate", Arguments: json.RawMessage(`{}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || inner.executeCalls != 1 || inner.effects != 0 {
		t.Fatalf("post-tool hook flipped a denial: result=%#v calls=%d effects=%d", result, inner.executeCalls, inner.effects)
	}
}
