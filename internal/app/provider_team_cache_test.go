package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	cursordriver "github.com/Viking602/azem/internal/provider/cursor"
	"github.com/Viking602/azem/internal/provider/responses"
)

type cursorHostCaptureDriver struct {
	host    cursordriver.ExecHost
	root    string
	calls   int
	request provider.Request
}

func (*cursorHostCaptureDriver) Metadata() provider.Metadata { return provider.Metadata{} }

func (driver *cursorHostCaptureDriver) Stream(ctx context.Context, request provider.Request) (provider.Stream, error) {
	driver.calls++
	driver.host = cursordriver.ExecHostFromContext(ctx)
	driver.root = responses.AttachmentRootFromContext(ctx)
	driver.request = request
	return provider.NewSliceStream([]provider.Event{{Kind: provider.EventDone, StopReason: provider.StopReasonComplete}}), nil
}

func TestProviderRequestScopePassesExactRequestToNextOnce(t *testing.T) {
	host := &cursorExecHost{bus: tool.NewBus()}
	engine := bindProviderRequestScope(agent.Engine{}, " /tmp/attachments ", host)
	if engine.ModelInterceptor == nil {
		t.Fatal("request scope did not install a stream interceptor")
	}
	request := provider.Request{
		Model:     "composer-2.5",
		Messages:  []message.Message{message.NewText(message.RoleUser, "inspect")},
		Tools:     []message.ToolDefinition{{Name: "coding.read_file", Description: "Read a file"}},
		ExtraBody: map[string]any{"stable": "value"},
	}
	capture := &cursorHostCaptureDriver{}
	stream, err := engine.ModelInterceptor.Stream(context.Background(), capture, request)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if capture.calls != 1 {
		t.Fatalf("next stream calls = %d, want 1", capture.calls)
	}
	if capture.root != "/tmp/attachments" || capture.host != host {
		t.Fatalf("request scope root=%q host=%p, want root=%q host=%p", capture.root, capture.host, "/tmp/attachments", host)
	}
	if !reflect.DeepEqual(capture.request, request) {
		t.Fatalf("provider request changed:\ngot:  %#v\nwant: %#v", capture.request, request)
	}
}

func TestTeamPrepareEnginePartitionsPromptCacheKeysAndPreservesOptions(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	recovery := &agentservice.EditRecovery{}
	hooks := service.teamHooks(TurnRequest{SessionID: "session-1", Provider: "chatgpt", Model: "gpt-team"}, "team-parent", teamExecutionPolicy{}, recovery)
	if hooks.RetryPolicy != (agentruntime.RetryPolicy{}) {
		t.Fatalf("Team task has a competing whole-execution retry policy: %#v", hooks.RetryPolicy)
	}
	parallelToolCalls := false
	base := agent.Engine{ParallelToolCalls: &parallelToolCalls}
	prepare := func(runID, role string) agent.Engine {
		t.Helper()
		prepared, err := hooks.PrepareEngine(context.Background(), base, agentruntime.TeamDispatch{
			Task: agentruntime.Task{RunID: runID}, To: role,
		}, agentruntime.TeamAgentClass{Name: role})
		if err != nil {
			t.Fatal(err)
		}
		if prepared.ParallelToolCalls == nil || *prepared.ParallelToolCalls {
			t.Fatalf("existing provider option lost: %#v", prepared.ParallelToolCalls)
		}
		return prepared
	}
	first := prepare("child-run-1", agentservice.ImplementerClass)
	repeated := prepare("child-run-2", agentservice.ImplementerClass)
	secondRole := prepare("child-run-2", agentservice.ReviewerClass)
	if first.PromptCacheKey != "session-1:team:chatgpt:gpt-team:implementer" ||
		repeated.PromptCacheKey != first.PromptCacheKey ||
		secondRole.PromptCacheKey == first.PromptCacheKey {
		t.Fatalf("team cache keys first=%q repeated=%q secondRole=%q", first.PromptCacheKey, repeated.PromptCacheKey, secondRole.PromptCacheKey)
	}
	if base.PromptCacheKey != "" {
		t.Fatalf("base engine prompt cache key mutated: %q", base.PromptCacheKey)
	}
	failedPatch, _ := json.Marshal(map[string]string{"input": "[internal/app/app.go#ABCD]\ninvalid"})
	recovery.Observe(
		tool.Call{ID: "failed-edit", Name: agentservice.ToolEditHashline, Arguments: failedPatch},
		tool.Result{ToolCallID: "failed-edit", Name: agentservice.ToolEditHashline, Content: "hashline edit failed: file changed since you read it", IsError: true},
		nil,
	)
	recoveryRequest := provider.Request{Tools: []message.ToolDefinition{
		{Name: agentservice.ToolEditHashline},
		{Name: agentservice.ToolReadFile},
	}}
	if err := first.Hooks.BeforeModelCall(context.Background(), &recoveryRequest); err != nil {
		t.Fatal(err)
	}
	if len(recoveryRequest.Tools) != 1 || recoveryRequest.Tools[0].Name != agentservice.ToolReadFile {
		t.Fatalf("team edit recovery tools = %#v", recoveryRequest.Tools)
	}
	readArguments, _ := json.Marshal(map[string]string{"path": "internal/app/app.go"})
	recovery.Observe(
		tool.Call{ID: "recovery-read", Name: agentservice.ToolReadFile, Arguments: readArguments},
		tool.Result{ToolCallID: "recovery-read", Name: agentservice.ToolReadFile},
		nil,
	)
	restoredRequest := provider.Request{Tools: []message.ToolDefinition{
		{Name: agentservice.ToolEditHashline},
		{Name: agentservice.ToolReadFile},
	}}
	if err := first.Hooks.BeforeModelCall(context.Background(), &restoredRequest); err != nil {
		t.Fatal(err)
	}
	if len(restoredRequest.Tools) != 2 {
		t.Fatalf("team tools were not restored after read: %#v", restoredRequest.Tools)
	}
}

func TestTeamPrepareEngineBindsDistinctCursorExecHostsPerRole(t *testing.T) {
	service := NewService(context.Background(), config.Default())
	hooks := service.teamHooks(
		TurnRequest{SessionID: "session-1", Provider: "cursor", Model: "composer-2.5"},
		"team-parent", teamExecutionPolicy{}, &agentservice.EditRecovery{},
	)
	base := agent.Engine{Tools: tool.NewBus(cursorApprovalResultDriver{})}
	prepare := func(runID, role string) (*cursorExecHost, agent.Engine) {
		t.Helper()
		prepared, err := hooks.PrepareEngine(context.Background(), base, agentruntime.TeamDispatch{
			Task: agentruntime.Task{RunID: runID}, To: role,
		}, agentruntime.TeamAgentClass{Name: role})
		if err != nil {
			t.Fatal(err)
		}
		if prepared.ModelInterceptor == nil {
			t.Fatalf("role %s has no request-scope interceptor", role)
		}
		capture := &cursorHostCaptureDriver{}
		stream, err := prepared.ModelInterceptor.Stream(context.Background(), capture, provider.Request{})
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Close()
		host, ok := capture.host.(*cursorExecHost)
		if !ok || host == nil || host.bus != prepared.Tools {
			t.Fatalf("role %s Cursor host=%#v tools=%p", role, host, prepared.Tools)
		}
		return host, prepared
	}
	implementer, first := prepare("run-1", agentservice.ImplementerClass)
	reviewer, second := prepare("run-2", agentservice.ReviewerClass)
	if implementer == reviewer || implementer.bus == reviewer.bus || first.PromptCacheKey == second.PromptCacheKey {
		t.Fatalf("Cursor Team roles shared state: implementer=%p reviewer=%p", implementer, reviewer)
	}
}
