package app

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	agentservice "github.com/Viking602/azem/internal/agent"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
)

type backgroundChildStatus struct {
	ID          string
	Type        string
	Description string
	State       string
}

func backgroundChildStatuses(runs []agentservice.SubagentRun) []backgroundChildStatus {
	statuses := make([]backgroundChildStatus, 0, len(runs))
	for _, run := range runs {
		statuses = append(statuses, backgroundChildStatus{
			ID: run.ID, Type: run.Type, Description: run.Description, State: string(run.State),
		})
	}
	return statuses
}

func pendingBackgroundChildrenGuardrail(list func() []backgroundChildStatus) hyagent.OutputGuardrail {
	var prompted atomic.Bool
	return hyagent.NewOutputGuardrail("pending-background-children", func(_ context.Context, _ hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
		if prompted.Load() {
			return hyagent.AllowOutput(), nil
		}
		children := list()
		if len(children) == 0 {
			return hyagent.AllowOutput(), nil
		}
		if !prompted.CompareAndSwap(false, true) {
			return hyagent.AllowOutput(), nil
		}
		return hyagent.RetryOutput(message.NewText(message.RoleUser, pendingBackgroundChildrenPrompt(children))), nil
	})
}

func pendingBackgroundChildrenPrompt(children []backgroundChildStatus) string {
	var builder strings.Builder
	builder.WriteString("[Host] Background subagents spawned in this run are still running:\n")
	for _, child := range children {
		role := strings.TrimSpace(child.Type)
		if role == "" {
			role = "subagent"
		}
		fmt.Fprintf(&builder, "- %s `%s`", role, child.ID)
		if description := strings.TrimSpace(child.Description); description != "" {
			fmt.Fprintf(&builder, " (%s)", description)
		}
		if state := strings.TrimSpace(child.State); state != "" {
			fmt.Fprintf(&builder, " state=%s", state)
		}
		builder.WriteByte('\n')
	}
	builder.WriteString("Call `subagent.get_output` with these task_ids and a `timeout_ms` long enough to wait for a terminal state, then consume the result before any gated action. If this background work is independent of the current conclusion, say so explicitly and then you may finish. Do not cancel the children merely because they are still running.")
	return builder.String()
}
