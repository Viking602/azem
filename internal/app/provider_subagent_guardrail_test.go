package app

import (
	"context"
	"strings"
	"testing"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
)

func TestPendingBackgroundChildrenGuardrailPromptsOnce(t *testing.T) {
	children := []backgroundChildStatus{{
		ID: "child-review", Type: "review", Description: "review the diff", State: "running",
	}}
	guardrail := pendingBackgroundChildrenGuardrail(func() []backgroundChildStatus { return children })
	input := hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "done")}

	first, err := guardrail.Check(context.Background(), input)
	if err != nil || first.Action != hyagent.OutputGuardrailActionRetry || len(first.RetryMessages) != 1 {
		t.Fatalf("first decision = %#v, %v", first, err)
	}
	prompt := first.RetryMessages[0].Text
	for _, fragment := range []string{
		"child-review", "review", "subagent.get_output", "timeout_ms", "independent of the current conclusion",
	} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("retry prompt omitted %q: %s", fragment, prompt)
		}
	}

	second, err := guardrail.Check(context.Background(), input)
	if err != nil || second.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("second decision = %#v, %v", second, err)
	}
}

func TestPendingBackgroundChildrenGuardrailAllowsWhenNone(t *testing.T) {
	guardrail := pendingBackgroundChildrenGuardrail(func() []backgroundChildStatus { return nil })
	result, err := guardrail.Check(context.Background(), hyagent.OutputGuardrailInput{
		Output: message.NewText(message.RoleAssistant, "done"),
	})
	if err != nil || result.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("decision = %#v, %v", result, err)
	}
}

func TestPendingBackgroundChildrenGuardrailAllowsWhenListBecomesEmpty(t *testing.T) {
	var children []backgroundChildStatus
	guardrail := pendingBackgroundChildrenGuardrail(func() []backgroundChildStatus { return children })
	result, err := guardrail.Check(context.Background(), hyagent.OutputGuardrailInput{
		Output: message.NewText(message.RoleAssistant, "done"),
	})
	if err != nil || result.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("empty decision = %#v, %v", result, err)
	}
	children = []backgroundChildStatus{{ID: "late", Type: "review", State: "running"}}
	result, err = guardrail.Check(context.Background(), hyagent.OutputGuardrailInput{
		Output: message.NewText(message.RoleAssistant, "done"),
	})
	if err != nil || result.Action != hyagent.OutputGuardrailActionRetry {
		t.Fatalf("late child decision = %#v, %v", result, err)
	}
}
