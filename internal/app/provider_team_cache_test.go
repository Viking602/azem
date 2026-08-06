package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/multiagent"
	"github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
)

func TestTeamPrepareEnginePartitionsPromptCacheKeysAndPreservesOptions(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	recovery := &agentservice.EditRecovery{}
	hooks := service.teamHooks(TurnRequest{SessionID: "session-1", Provider: "chatgpt", Model: "gpt-team"}, "team-parent", teamExecutionPolicy{}, recovery)
	if hooks.RetryPolicy.MaxBackoff != service.cfg.Retry.MaxDelayDuration {
		t.Fatalf("team retry max backoff = %v, want %v", hooks.RetryPolicy.MaxBackoff, service.cfg.Retry.MaxDelayDuration)
	}
	base := agent.Engine{ExtraBody: map[string]any{"parallel_tool_calls": false}}
	prepare := func(runID, role string) agent.Engine {
		t.Helper()
		prepared, err := hooks.PrepareEngine(context.Background(), base, multiagent.Dispatch{
			Task: api.Task{RunID: runID}, To: role,
		}, multiagent.AgentClass{Name: role})
		if err != nil {
			t.Fatal(err)
		}
		if prepared.ExtraBody["parallel_tool_calls"] != false {
			t.Fatalf("existing provider option lost: %#v", prepared.ExtraBody)
		}
		return prepared
	}
	first := prepare("child-run-1", agentservice.ImplementerClass)
	repeated := prepare("child-run-2", agentservice.ImplementerClass)
	secondRole := prepare("child-run-2", agentservice.ReviewerClass)
	if first.ExtraBody["prompt_cache_key"] != "session-1:team:chatgpt:gpt-team:implementer" ||
		repeated.ExtraBody["prompt_cache_key"] != first.ExtraBody["prompt_cache_key"] ||
		secondRole.ExtraBody["prompt_cache_key"] == first.ExtraBody["prompt_cache_key"] {
		t.Fatalf("team cache keys first=%#v repeated=%#v secondRole=%#v", first.ExtraBody, repeated.ExtraBody, secondRole.ExtraBody)
	}
	if _, mutated := base.ExtraBody["prompt_cache_key"]; mutated {
		t.Fatalf("base engine ExtraBody mutated: %#v", base.ExtraBody)
	}
	failedPatch, _ := json.Marshal(map[string]string{"input": "[internal/app/app.go#ABCD]\ninvalid"})
	recovery.Observe(
		tool.Call{ID: "failed-edit", Name: coding.ToolEditHashline, Arguments: failedPatch},
		tool.Result{ToolCallID: "failed-edit", Name: coding.ToolEditHashline, IsError: true},
		nil,
	)
	recoveryRequest := provider.Request{Tools: []message.ToolDefinition{
		{Name: coding.ToolEditHashline},
		{Name: coding.ToolReadFile},
	}}
	if err := first.Hooks.BeforeModelCall(context.Background(), &recoveryRequest); err != nil {
		t.Fatal(err)
	}
	if len(recoveryRequest.Tools) != 1 || recoveryRequest.Tools[0].Name != coding.ToolReadFile {
		t.Fatalf("team edit recovery tools = %#v", recoveryRequest.Tools)
	}
	readArguments, _ := json.Marshal(map[string]string{"path": "internal/app/app.go"})
	recovery.Observe(
		tool.Call{ID: "recovery-read", Name: coding.ToolReadFile, Arguments: readArguments},
		tool.Result{ToolCallID: "recovery-read", Name: coding.ToolReadFile},
		nil,
	)
	restoredRequest := provider.Request{Tools: []message.ToolDefinition{
		{Name: coding.ToolEditHashline},
		{Name: coding.ToolReadFile},
	}}
	if err := first.Hooks.BeforeModelCall(context.Background(), &restoredRequest); err != nil {
		t.Fatal(err)
	}
	if len(restoredRequest.Tools) != 2 {
		t.Fatalf("team tools were not restored after read: %#v", restoredRequest.Tools)
	}
}
