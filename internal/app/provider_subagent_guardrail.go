package app

import (
	"context"
	"fmt"
	"strings"

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
	return hyagent.NewOutputGuardrail("pending-background-children", func(_ context.Context, _ hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
		children := list()
		if len(children) == 0 {
			return hyagent.AllowOutput(), nil
		}
		return hyagent.RetryOutputWithPolicy(hyagent.RetryPolicy{IncludeRejectedOutput: true}, message.NewText(message.RoleUser, pendingBackgroundChildrenPrompt(children))), nil
	})
}

func pendingBackgroundChildrenPrompt(children []backgroundChildStatus) string {
	var builder strings.Builder
	builder.WriteString("[Host] Subagents spawned in this run are not terminal. You may not finish until every listed child is completed, failed, or cancelled.\n")
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
	builder.WriteString("Call `subagent.get_output` with these task_ids and a `timeout_ms` long enough to wait for a terminal state, then consume the result. Repeat until the list is empty. Do not cancel a child merely because it is still running. The host may cancel a silent child after idle_timeout; only then is that child terminal.")
	return builder.String()
}
