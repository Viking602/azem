package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

func TestDecodeSubagentBatchRequestCarriesSharedContextAndSchemas(t *testing.T) {
	request, err := decodeSubagentSpawnRequest(json.RawMessage(`{
		"context":"# Goal\nInspect two independent areas.",
		"tasks":[
			{"name":"ConfigAudit","agent":"explore","task":"Inspect configuration.","outputSchema":{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false},"schemaMode":"strict"},
			{"name":"UiAudit","task":"Inspect UI.","isolated":true}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !request.Batch || len(request.Inputs) != 2 {
		t.Fatalf("batch request = %#v", request)
	}
	first, second := request.Inputs[0], request.Inputs[1]
	if first.Name != "ConfigAudit" || first.SubagentType != "explore" || first.SchemaMode != "strict" || len(first.OutputSchema) == 0 ||
		!strings.Contains(first.Prompt, "# Shared context") || !strings.Contains(first.Prompt, "# Assignment\nInspect configuration.") {
		t.Fatalf("first batch item = %#v", first)
	}
	if second.Name != "UiAudit" || second.SubagentType != "worker" || second.Isolation != "worktree" {
		t.Fatalf("second batch item = %#v", second)
	}
	for name, payload := range map[string]string{
		"duplicate names": `{"context":"shared","tasks":[{"name":"Audit","task":"one"},{"name":"audit","task":"two"}]}`,
		"missing context": `{"tasks":[{"task":"one"}]}`,
		"mixed shape":     `{"context":"shared","prompt":"single","tasks":[{"task":"one"}]}`,
		"invalid schema":  `{"context":"shared","tasks":[{"task":"one","outputSchema":{"type":"not-a-type"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeSubagentSpawnRequest(json.RawMessage(payload)); err == nil {
				t.Fatal("invalid batch request accepted")
			}
		})
	}
}

func TestStructuredSubagentGuardrailRetriesThenHonorsMode(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false}`)
	for _, test := range []struct {
		mode string
		want hyagent.OutputGuardrailAction
	}{
		{mode: "permissive", want: hyagent.OutputGuardrailActionAllow},
		{mode: "strict", want: hyagent.OutputGuardrailActionBlock},
	} {
		t.Run(test.mode, func(t *testing.T) {
			contract, err := compileStructuredSubagentContract(schema, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			guardrail := structuredSubagentGuardrail(contract)
			input := hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, `{"ok":"no"}`)}
			for attempt := 0; attempt < structuredSubagentRetries; attempt++ {
				result, err := guardrail.Check(context.Background(), input)
				if err != nil || result.Action != hyagent.OutputGuardrailActionRetry || len(result.RetryMessages) != 1 {
					t.Fatalf("retry %d = %#v, %v", attempt, result, err)
				}
			}
			result, err := guardrail.Check(context.Background(), input)
			if err != nil || result.Action != test.want {
				t.Fatalf("exhausted %s = %#v, %v", test.mode, result, err)
			}
		})
	}

	contract, err := compileStructuredSubagentContract(schema, "strict")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := structuredSubagentGuardrail(contract).Check(context.Background(), hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, `{"ok":true}`)})
	if err != nil || valid.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("valid structured output = %#v, %v", valid, err)
	}
	parsed := contract.validate("```json\n{\"ok\":true}\n```")
	if parsed.Status != "valid" || string(parsed.Data) != `{"ok":true}` {
		t.Fatalf("fenced structured output = %#v", parsed)
	}
}

func TestStructuredSubagentResultProjectsTypedData(t *testing.T) {
	result := foregroundSubagentResult(agentservice.SubagentSnapshot{Found: true, Run: agentservice.SubagentRun{
		ID: "task-1", State: agentservice.SubagentCompleted, Output: `{"ok":true}`,
		StructuredSource: "caller", StructuredMode: "strict", StructuredStatus: "valid", StructuredOutput: json.RawMessage(`{"ok":true}`),
	}})
	structured, ok := result["structured"].(map[string]any)
	if !ok || structured["status"] != "valid" || string(structured["data"].(json.RawMessage)) != `{"ok":true}` {
		t.Fatalf("structured foreground result = %#v", result)
	}
}

func TestStructuredSubagentRepairsAndReturnsTypedData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runtime, _, coding, _ := newGatedForegroundHarness(t, ctx, 0)
	defer runtime.Shutdown(ctx)
	defer coding.Close(ctx)
	driver := &compactionTestDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventTextDelta, Text: `{"ok":"wrong"}`}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
		{{Kind: hyprovider.EventTextDelta, Text: `not json`}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
		{{Kind: hyprovider.EventTextDelta, Text: `{"ok":true}`}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
	}}
	parent := subagentParentRuntime{
		SessionID: "session", ParentRunID: "parent", ProviderID: "test", ModelID: "model", Reasoning: "high",
		Driver: driver, Coding: coding, WorkspaceRoot: t.TempDir(),
		ResolveDriver: func(context.Context, string, string, string) (string, int, hyprovider.Driver, error) {
			return "model", 128_000, driver, nil
		},
	}
	spawn := &subagentSpawnDriver{runtime: runtime, parent: parent}
	call := tool.Call{ID: "structured", Name: subagentSpawnTool, Arguments: json.RawMessage(`{
		"prompt":"return the structured result",
		"description":"return structured result",
		"subagent_type":"explore",
		"outputSchema":{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false},
		"schemaMode":"strict"
	}`)}
	result, err := spawn.Execute(ctx, call, nil)
	if err != nil || result.IsError {
		t.Fatalf("structured spawn = %#v, %v", result, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	structured, ok := payload["structured"].(map[string]any)
	if !ok || structured["status"] != "valid" || structured["mode"] != "strict" {
		t.Fatalf("structured spawn payload = %#v", payload)
	}
	data, ok := structured["data"].(map[string]any)
	if !ok || data["ok"] != true || len(driver.requests) != 3 {
		t.Fatalf("structured data=%#v requests=%d", data, len(driver.requests))
	}
}
