package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/go-hydaelyn/tool"
)

func TestDiscoveryTrustOrderingDiagnosticsAndDedup(t *testing.T) {
	dir := t.TempDir()
	first := `{"hooks":{"PreToolUse":[{"matcher":"Bash|Read","hooks":[{"type":"command","command":"echo first"},{"type":"prompt","command":"must-not-run"}]}]}}`
	second := `{"hooks":{"PreToolUse":[{"matcher":"[","hooks":[{"type":"command","command":"echo invalid"}]}],"PostToolUse":[{"hooks":[{"type":"command","command":"echo second"},{"type":"command","command":"echo second"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(second), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	untrusted := filepath.Join(t.TempDir(), "untrusted.json")
	if err := os.WriteFile(untrusted, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Discover(Options{Sources: []Source{{Path: dir, Trusted: true}, {Path: untrusted}}, DefaultTimeout: time.Second})
	commands := r.Commands(PreToolUse)
	if len(commands) != 1 || commands[0].RawCommand != "echo first" || !commands[0].Matches("coding.shell") || !commands[0].Matches("Read") {
		t.Fatalf("commands = %#v", commands)
	}
	if got := len(r.Commands(PostToolUse)); got != 1 {
		t.Fatalf("deduplicated commands = %d", got)
	}
	if len(r.Diagnostics) != 3 { // unsupported type, invalid regexp, untrusted source
		t.Fatalf("diagnostics = %#v", r.Diagnostics)
	}
	for _, diagnostic := range r.Diagnostics {
		if strings.Contains(diagnostic.Message, "must-not-run") {
			t.Fatalf("diagnostic leaked raw command: %#v", diagnostic)
		}
	}
}

func TestDiscoveryRegistersEveryClaudeEventAndAzemExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	events := []Event{PreToolUse, PostToolUse, PostToolUseFailure, Notification, UserPromptSubmit,
		SessionStart, SessionEnd, Stop, StopFailure, SubagentStart, SubagentStop, PreCompact,
		PostCompact, PermissionRequest, PermissionDenied, Setup, TeammateIdle, TaskCreated,
		TaskCompleted, Elicitation, ElicitationResult, ConfigChange, WorktreeCreate, WorktreeRemove,
		InstructionsLoaded, CwdChanged, FileChanged, TodoUpdated}
	configured := fileConfig{Hooks: make(map[Event][]group)}
	for _, event := range events {
		configured.Hooks[event] = []group{{Hooks: []hookSpec{{Type: "command", Command: "true"}}}}
	}
	settings, err := json.Marshal(configured)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, settings, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := Discover(Options{Sources: []Source{{Path: path, Trusted: true}}})
	if len(registry.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", registry.Diagnostics)
	}
	for _, event := range events {
		t.Run(string(event), func(t *testing.T) {
			if got := len(registry.Commands(event)); got != 1 {
				t.Fatalf("commands = %d; event was silently skipped", got)
			}
		})
	}
}

func TestClaudeCommandOptionsAndPermissionCondition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo ok","if":"Bash(git *)","shell":"bash","timeout":600,"statusMessage":"Checking git","once":true,"async":true}]}]}}`
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := Discover(Options{Sources: []Source{{Path: path, Trusted: true}}})
	if len(registry.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", registry.Diagnostics)
	}
	commands := registry.Commands(PreToolUse)
	if len(commands) != 1 {
		t.Fatalf("commands = %#v", commands)
	}
	command := commands[0]
	if command.Shell != "bash" || command.Timeout != 10*time.Minute || command.StatusMessage != "Checking git" || !command.Once || !command.Async || command.AsyncRewake {
		t.Fatalf("command options = %#v", command)
	}
	if !command.MatchesCondition(Envelope{ToolName: "coding.shell", ToolInput: json.RawMessage(`{"command":"git status"}`)}) {
		t.Fatal("Bash(git *) did not match coding.shell input")
	}
	if command.MatchesCondition(Envelope{ToolName: "coding.shell", ToolInput: json.RawMessage(`{"command":"go test ./..."}`)}) {
		t.Fatal("Bash(git *) matched unrelated shell input")
	}
	if !registry.Claim(command) || registry.Claim(command) {
		t.Fatal("once hook was not claimed exactly once")
	}
}

func TestClaudeMatcherWithPunctuationUsesRegex(t *testing.T) {
	command := Command{Matcher: `foo.bar`}
	if err := compileMatcher(&command); err != nil {
		t.Fatal(err)
	}
	if !command.Matches("fooXbar") {
		t.Fatal("Claude-compatible punctuation matcher was not treated as a regular expression")
	}
}

func TestClaudeEventMatcherQueriesUseCanonicalField(t *testing.T) {
	tests := []struct {
		name  string
		input Envelope
		want  string
	}{
		{"tool", Envelope{HookEventName: PreToolUse, ToolName: "Read"}, "Read"},
		{"session", Envelope{HookEventName: SessionStart, Source: "resume"}, "resume"},
		{"setup", Envelope{HookEventName: Setup, Trigger: "init"}, "init"},
		{"notification", Envelope{HookEventName: Notification, NotificationType: "permission_prompt"}, "permission_prompt"},
		{"session end", Envelope{HookEventName: SessionEnd, Reason: "clear"}, "clear"},
		{"subagent", Envelope{HookEventName: SubagentStop, AgentType: "reviewer"}, "reviewer"},
		{"elicitation", Envelope{HookEventName: Elicitation, MCPServerName: "github"}, "github"},
		{"instructions", Envelope{HookEventName: InstructionsLoaded, LoadReason: "include"}, "include"},
		{"file", Envelope{HookEventName: FileChanged, FilePath: "/tmp/.env"}, ".env"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := matcherQuery(test.input); got != test.want {
				t.Fatalf("matcher query = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClaudeWireInputContainsRequiredBaseAndOnlyEventFields(t *testing.T) {
	encoded, err := marshalEnvelope(Envelope{
		SessionID: "session-1", TranscriptPath: "/tmp/transcript.jsonl", CWD: "/workspace",
		RunID: "private-run", ParentRunID: "private-parent", HookEventName: Notification,
		Message: "Approval required", Title: "Azem", NotificationType: "permission_prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal(encoded, &input); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session_id", "transcript_path", "cwd", "hook_event_name", "message", "title", "notification_type"} {
		if _, ok := input[key]; !ok {
			t.Fatalf("required key %q missing from %#v", key, input)
		}
	}
	for _, key := range []string{"run_id", "parent_run_id", "stop_hook_active", "tool_name"} {
		if _, ok := input[key]; ok {
			t.Fatalf("private or unrelated key %q leaked into %#v", key, input)
		}
	}
}

func TestRunnerJSONDenyOverridesExitAndBoundsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	runner := Runner{Workspace: t.TempDir(), Environment: []string{"AZEM_SESSION_ID=wrong"}}
	command := Command{Event: PreToolUse, RawCommand: `cat >/dev/null; printf '%070000d' 0; printf '{"decision":"block","reason":"blocked"}'; exit 0`, Timeout: 2 * time.Second}
	result := runner.Run(context.Background(), command, Envelope{HookEventName: PreToolUse, SessionID: "right"})
	if result.Denied { // JSON is beyond bounded stdout and therefore intentionally unavailable.
		t.Fatal("truncated JSON should not be interpreted")
	}
	if len(result.Stdout) != maxCapture {
		t.Fatalf("stdout size = %d", len(result.Stdout))
	}
	command.RawCommand = `printf '{"decision":"block","reason":"blocked"}'; exit 1`
	result = runner.Run(context.Background(), command, Envelope{HookEventName: PreToolUse})
	if !result.Denied || result.Output.Reason != "blocked" {
		t.Fatalf("JSON deny did not override exit: %#v", result)
	}
	command.RawCommand = `printf '{'; printf malformed >&2; exit 2`
	result = runner.Run(context.Background(), command, Envelope{HookEventName: PreToolUse})
	if !result.Denied || result.Output.Reason != "malformed" {
		t.Fatalf("exit 2 with malformed JSON did not deny: %#v", result)
	}
}

func TestContinueFalsePreventsContinuationWithoutBlockingStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"printf '%s' '{\"continue\":false,\"stopReason\":\"done now\"}'"}]}]}}`
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := Discover(Options{Sources: []Source{{Path: path, Trusted: true}}})
	result := (Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}}).Dispatch(context.Background(), Envelope{HookEventName: Stop})
	if result.Denied || !result.PreventContinuation || result.StopReason != "done now" {
		t.Fatalf("dispatch result = %#v", result)
	}
}

func TestRunnerTimeoutTerminatesShellProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	started := time.Now()
	result := (Runner{Workspace: t.TempDir()}).Run(context.Background(), Command{
		Event: PreToolUse, Name: "timeout", RawCommand: `(sleep 30) & wait`, Timeout: 100 * time.Millisecond,
	}, Envelope{HookEventName: PreToolUse})
	if !errors.Is(result.Failure, context.DeadlineExceeded) {
		t.Fatalf("timeout failure = %v", result.Failure)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("hook process tree outlived timeout: %s", elapsed)
	}
}

func TestDispatcherChainsUpdatedInputAndFailurePolicies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	registry := &Registry{commands: map[Event][]Command{PreToolUse: {
		{Event: PreToolUse, RawCommand: `printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"step":1}}}'`, Timeout: time.Second, FailurePolicy: FailureClosed},
		{Event: PreToolUse, RawCommand: `grep -q '"step":1' && printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"step":2}}}'`, Timeout: time.Second, FailurePolicy: FailureClosed},
	}}}
	result := (Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}}).Dispatch(context.Background(), Envelope{HookEventName: PreToolUse, ToolName: "Read", ToolInput: json.RawMessage(`{"step":0}`)})
	if result.Denied || string(result.UpdatedInput) != `{"step":2}` {
		t.Fatalf("dispatch result = %#v", result)
	}
	registry.commands[PreToolUse] = []Command{{Event: PreToolUse, RawCommand: "exit 1", Timeout: time.Second, FailurePolicy: FailureClosed}}
	if result := (Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}}).Dispatch(context.Background(), Envelope{HookEventName: PreToolUse}); !result.Denied {
		t.Fatal("closed gate failure did not deny")
	}
	registry.commands[PostToolUse] = registry.commands[PreToolUse]
	if result := (Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}}).Dispatch(context.Background(), Envelope{HookEventName: PostToolUse}); result.Denied {
		t.Fatal("observe failure denied")
	}
}

func TestClaudeBlockingEventsHonorExitTwo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	for _, event := range []Event{PermissionRequest, Stop, SubagentStop, TeammateIdle, TaskCreated, TaskCompleted, Elicitation, ElicitationResult, ConfigChange, WorktreeCreate} {
		t.Run(string(event), func(t *testing.T) {
			registry := &Registry{commands: map[Event][]Command{event: {{Event: event, RawCommand: "exit 2", Timeout: time.Second}}}}
			result := (Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}}).Dispatch(context.Background(), Envelope{HookEventName: event})
			if !result.Denied {
				t.Fatalf("%s exit 2 was not blocking", event)
			}
		})
	}
	if blockingEvent(Envelope{HookEventName: SubagentStart}) {
		t.Fatal("SubagentStart must not block startup")
	}
	if blockingEvent(Envelope{HookEventName: ConfigChange, Source: "policy_settings"}) {
		t.Fatal("policy settings changes must not be blocked")
	}
}

func TestHandlerRewritesArgumentsWithoutChangingNameAndReportsFailureEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	registry := &Registry{commands: map[Event][]Command{
		PreToolUse:         {{Event: PreToolUse, RawCommand: `printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"safe":true}}}'`, Timeout: time.Second}},
		PostToolUseFailure: {{Event: PostToolUseFailure, RawCommand: "exit 0", Timeout: time.Second}},
	}}
	var events []Event
	h := NewHandler(Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}, OnRun: func(result RunResult) { events = append(events, result.Event) }}, Metadata{SessionID: "session", RunID: "run"})
	call := &tool.Call{Name: "coding.shell", Arguments: json.RawMessage(`{"unsafe":true}`)}
	if err := h.BeforeToolCall(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if call.Name != "coding.shell" || string(call.Arguments) != `{"safe":true}` {
		t.Fatalf("call = %#v", call)
	}
	if err := h.AfterToolCall(context.Background(), &tool.Result{Name: call.Name, IsError: true, Content: "failed"}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1] != PostToolUseFailure {
		t.Fatalf("events = %#v", events)
	}
	registry.commands[PreToolUse][0].RawCommand = `printf '{"decision":"block","reason":"no"}'`
	if err := h.BeforeToolCall(context.Background(), call); !errors.Is(err, ErrDenied) {
		t.Fatalf("deny error = %v", err)
	}
}

type recordingDriver struct {
	call   tool.Call
	result tool.Result
}

func (*recordingDriver) Definition() tool.Definition {
	return tool.Definition{Name: "coding.write_file", InputSchema: tool.Schema{Type: "object"}}
}

func (d *recordingDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	d.call = call
	if d.result.IsError || d.result.Content != "" || len(d.result.Structured) > 0 {
		return d.result, nil
	}
	return tool.Result{Content: "written"}, nil
}

func TestWrapDriverDenialIsToolErrorAndRewriteReachesInnerDriver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	registry := &Registry{commands: map[Event][]Command{PreToolUse: {{
		Event: PreToolUse, Name: "policy", RawCommand: `printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"path":"safe.txt"}}}'`, Timeout: time.Second,
	}}}}
	inner := &recordingDriver{}
	driver := WrapDriver(Dispatcher{Registry: registry, Runner: Runner{Workspace: t.TempDir()}}, Metadata{SessionID: "session", RunID: "run"}, inner)
	result, err := driver.Execute(context.Background(), tool.Call{ID: "call", Name: "coding.write_file", Arguments: json.RawMessage(`{"path":"unsafe.txt"}`)}, nil)
	if err != nil || result.IsError || string(inner.call.Arguments) != `{"path":"safe.txt"}` || result.ToolCallID != "call" || result.Name != "coding.write_file" {
		t.Fatalf("result=%#v call=%#v err=%v", result, inner.call, err)
	}
	registry.commands[PreToolUse][0].RawCommand = `printf '{"decision":"block","reason":"protected"}'`
	inner.call = tool.Call{}
	result, err = driver.Execute(context.Background(), tool.Call{ID: "blocked", Name: "coding.write_file", Arguments: json.RawMessage(`{}`)}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "protected") || inner.call.ID != "" {
		t.Fatalf("denied result=%#v call=%#v err=%v", result, inner.call, err)
	}
}

func TestBoundedToolResultKeepsHookEnvelopeValidJSON(t *testing.T) {
	result := boundedToolResult(tool.Result{
		Content:    strings.Repeat("\x01", 70<<10),
		Structured: json.RawMessage(`{"value":"` + strings.Repeat("y", 70<<10) + `"}`),
	})
	if result.Structured != nil {
		t.Fatalf("oversized structured result was retained: %d bytes", len(result.Structured))
	}
	if !strings.HasSuffix(result.Content, "…") {
		t.Fatalf("bounded content lacks truncation marker: %d bytes", len(result.Content))
	}
	if _, err := marshalEnvelope(Envelope{HookEventName: PostToolUse, ToolResponse: result}); err != nil {
		t.Fatalf("bounded result cannot be sent to hook: %v", err)
	}
}

func TestLargeFailureStillRunsPostToolUseFailureHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command")
	}
	marker := filepath.Join(t.TempDir(), "post-failure")
	registry := &Registry{commands: map[Event][]Command{PostToolUseFailure: {{
		Event: PostToolUseFailure, Name: "failure", RawCommand: `cat >/dev/null; : > "$HOOK_MARKER"`, Timeout: time.Second,
	}}}}
	inner := &recordingDriver{result: tool.Result{
		IsError: true, Content: strings.Repeat("\x01", 140<<10),
		Structured: json.RawMessage(`{"value":"` + strings.Repeat("z", 140<<10) + `"}`),
	}}
	driver := WrapDriver(Dispatcher{
		Registry: registry, Runner: Runner{Workspace: t.TempDir(), Environment: []string{"HOOK_MARKER=" + marker}},
	}, Metadata{}, inner)
	result, err := driver.Execute(context.Background(), tool.Call{ID: "failed", Name: "coding.write_file"}, nil)
	if err != nil || !result.IsError {
		t.Fatalf("tool result = %#v, err = %v", result, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("post-failure hook did not run: %v", err)
	}
}
