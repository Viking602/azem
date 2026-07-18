package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/tool"
	"github.com/Viking602/go-hydaelyn/transport/mcpcontract"
)

func TestHookSourcesExcludeUntrustedProjectPaths(t *testing.T) {
	cfg := config.Default().Hooks
	cfg.AdditionalPaths = []string{"/trusted/extra"}
	sources := hookSources(cfg, "/config", "/home", "/work")
	want := map[string]bool{
		filepath.Join("/config", "hooks"): true, "/trusted/extra": true,
		filepath.Join("/home", ".claude", "settings.json"):       true,
		filepath.Join("/home", ".claude", "settings.local.json"): true,
	}
	if len(sources) != len(want) {
		t.Fatalf("sources = %#v", sources)
	}
	for _, source := range sources {
		if !source.Trusted || !want[source.Path] {
			t.Fatalf("unexpected source %#v", source)
		}
	}
	cfg.TrustProject = true
	if got := hookSources(cfg, "/config", "/home", "/work"); len(got) != len(want)+3 {
		t.Fatalf("trusted project sources = %#v", got)
	}
	cfg.Enabled = false
	if got := hookSources(cfg, "/config", "/home", "/work"); len(got) != 0 {
		t.Fatalf("disabled sources = %#v", got)
	}
}

func TestHookCallbacksCarryIDsAndNeverExposeCommand(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"PreCompact":[{"hooks":[{"name":"audit","type":"command","command":"printf ok"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: path, Trusted: true}}})
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(context.Background(), cfg)
	service.AttachHooks(hooks.Dispatcher{Registry: registry})
	if err := service.dispatchLifecycle(context.Background(), hooks.PreCompact,
		hooks.Metadata{SessionID: "session", RunID: "run", AgentID: "agent", CWD: workspace}, nil); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []EventKind{EventHookStarted, EventHookFinished} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		event, err := service.NextEvent(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind != kind || event.SessionID != "session" || event.RunID != "run" || event.AgentID != "agent" {
			t.Fatalf("event = %#v", event)
		}
		for key, value := range event.Data {
			if key == "command" || value == "printf ok" {
				t.Fatalf("raw command exposed in %#v", event.Data)
			}
		}
	}
}

func TestUserPromptHookDenialReleasesAdmissionBeforeMaterialization(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"name":"deny","type":"command","command":"exit 2"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(context.Background(), cfg)
	service.AttachHooks(hooks.Dispatcher{Registry: hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: path, Trusted: true}}})})
	_, err := service.StartConfiguredTurn(TurnRequest{SessionID: "not-created", Prompt: "blocked"})
	if !errors.Is(err, hooks.ErrDenied) {
		t.Fatalf("error = %v", err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.activeRun != "" || service.activeSession != "" {
		t.Fatalf("admission remained occupied: run=%q session=%q", service.activeRun, service.activeSession)
	}
}

func TestSessionHooksCoverFirstTurnAndShutdownExactlyOnce(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell command")
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "hooks.json")
	configJSON := `{"hooks":{` +
		`"SessionStart":[{"hooks":[{"name":"start","type":"command","command":"printf s >> \"$HOOK_LOG\""}]}],` +
		`"SessionEnd":[{"hooks":[{"name":"end","type":"command","command":"printf e >> \"$HOOK_LOG\""}]}]}}`
	if err := os.WriteFile(path, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(workspace, "hook.log")
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(context.Background(), cfg)
	service.AttachHooks(hooks.Dispatcher{
		Registry: hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: path, Trusted: true}}}),
		Runner:   hooks.Runner{Environment: []string{"HOOK_LOG=" + logPath}},
	})
	if _, err := service.StartConfiguredTurn(TurnRequest{SessionID: "session-one", Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.Lock()
		active := service.activeRun
		service.mu.Unlock()
		if active == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake turn did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "se" {
		t.Fatalf("session hook sequence = %q, want %q", contents, "se")
	}
}

func TestSessionStartHookRunsOnceWhenReturningToSession(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell command")
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"name":"start","type":"command","command":"printf '%s,' \"$AZEM_SESSION_ID\" >> \"$HOOK_LOG\""}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(workspace, "hook.log")
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(context.Background(), cfg)
	service.AttachHooks(hooks.Dispatcher{
		Registry: hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: path, Trusted: true}}}),
		Runner:   hooks.Runner{Environment: []string{"HOOK_LOG=" + logPath}},
	})
	service.startSessionHooks(context.Background(), "A", "", "startup")
	service.startSessionHooks(context.Background(), "B", "", "startup")
	service.startSessionHooks(context.Background(), "A", "", "resume")
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "A,B," {
		t.Fatalf("session starts = %q, want %q", contents, "A,B,")
	}
}

func TestMCPElicitationHooksCanAnswerAndOverrideResult(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell command")
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "hooks.json")
	configJSON := `{"hooks":{` +
		`"Elicitation":[{"matcher":"github","hooks":[{"type":"command","command":"printf '{\"hookSpecificOutput\":{\"hookEventName\":\"Elicitation\",\"action\":\"accept\",\"content\":{\"answer\":\"yes\"}}}'"}]}],` +
		`"ElicitationResult":[{"matcher":"github","hooks":[{"type":"command","command":"printf '{\"hookSpecificOutput\":{\"hookEventName\":\"ElicitationResult\",\"action\":\"decline\"}}'"}]}]}}`
	if err := os.WriteFile(path, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(context.Background(), cfg)
	service.currentSession = "session"
	registry := hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: path, Trusted: true}}})
	if len(registry.Diagnostics) != 0 || len(registry.Commands(hooks.Elicitation)) != 1 || len(registry.Commands(hooks.ElicitationResult)) != 1 {
		t.Fatalf("elicitation registry diagnostics=%#v start=%d result=%d", registry.Diagnostics, len(registry.Commands(hooks.Elicitation)), len(registry.Commands(hooks.ElicitationResult)))
	}
	service.AttachHooks(hooks.Dispatcher{Registry: registry})
	probe := service.hooks.Dispatch(context.Background(), hooks.Envelope{HookEventName: hooks.ElicitationResult, MCPServerName: "github", Action: "accept", CWD: workspace})
	if len(probe.Runs) != 1 || probe.Runs[0].Failure != nil || probe.Runs[0].Output.HookSpecificOutput.Action != "decline" {
		t.Fatalf("result hook probe = %#v", probe)
	}
	result, err := service.handleMCPElicitation(context.Background(), "github", mcpcontract.Elicitation{Mode: "form", Message: "Choose"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "decline" {
		t.Fatalf("elicitation result = %#v", result)
	}
}

func TestPermissionHookDenyOverridesEarlierAllow(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell command")
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "hooks.json")
	configJSON := `{"hooks":{"PermissionRequest":[{"hooks":[` +
		`{"name":"allow","type":"command","command":"printf '{\"hookSpecificOutput\":{\"hookEventName\":\"PermissionRequest\",\"decision\":{\"behavior\":\"allow\"}}}'"},` +
		`{"name":"deny","type":"command","command":"printf '{\"hookSpecificOutput\":{\"hookEventName\":\"PermissionRequest\",\"decision\":{\"behavior\":\"deny\",\"message\":\"blocked\"}}}'"}` +
		`]}]}}`
	if err := os.WriteFile(path, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(context.Background(), cfg)
	service.AttachHooks(hooks.Dispatcher{Registry: hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: path, Trusted: true}}})})
	decision := service.permissionHook(context.Background(), hooks.Metadata{SessionID: "session", CWD: workspace}, tool.Call{ID: "call", Name: "Write", Arguments: json.RawMessage(`{"path":"file"}`)})
	if decision.behavior != "deny" || decision.message != "blocked" || decision.name != "deny" {
		t.Fatalf("permission decision = %#v", decision)
	}
}

func TestStopHookGuardrailRetriesAndMarksRecursiveInvocation(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell command")
	}
	workspace := t.TempDir()
	marker, logPath := filepath.Join(workspace, "marker"), filepath.Join(workspace, "events.jsonl")
	path := filepath.Join(workspace, "hooks.json")
	command := `cat >> "$HOOK_LOG"; if [ ! -f "$HOOK_MARKER" ]; then touch "$HOOK_MARKER"; printf 'continue working' >&2; exit 2; fi`
	encodedCommand, _ := json.Marshal(command)
	configJSON := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":` + string(encodedCommand) + `}]}]}}`
	if err := os.WriteFile(path, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(context.Background(), cfg)
	service.AttachHooks(hooks.Dispatcher{
		Registry: hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: path, Trusted: true}}}),
		Runner: hooks.Runner{Environment: []string{
			"HOOK_LOG=" + logPath, "HOOK_MARKER=" + marker,
		}},
	})
	guardrail := service.stopHookGuardrail(hooks.Metadata{SessionID: "session", CWD: workspace}, hooks.Stop, nil)
	input := hyagent.OutputGuardrailInput{Output: message.NewText(message.RoleAssistant, "done")}
	first, err := guardrail.Check(context.Background(), input)
	if err != nil || first.Action != hyagent.OutputGuardrailActionRetry || len(first.RetryMessages) != 1 || first.RetryMessages[0].Text != "continue working" {
		t.Fatalf("first stop decision = %#v, %v", first, err)
	}
	second, err := guardrail.Check(context.Background(), input)
	if err != nil || second.Action != hyagent.OutputGuardrailActionAllow {
		t.Fatalf("second stop decision = %#v, %v", second, err)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) != 2 {
		t.Fatalf("stop inputs = %q", contents)
	}
	for index, active := range []bool{false, true} {
		var input map[string]any
		if err := json.Unmarshal([]byte(lines[index]), &input); err != nil {
			t.Fatal(err)
		}
		if input["stop_hook_active"] != active {
			t.Fatalf("stop input %d = %#v", index, input)
		}
	}
}

func TestHookWatcherReportsAddChangeAndUnlink(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell command")
	}
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "events.jsonl")
	configPath := filepath.Join(workspace, "hooks.json")
	if err := os.WriteFile(configPath, []byte(`{"hooks":{"FileChanged":[{"matcher":"watched.txt","hooks":[{"type":"command","command":"cat >> \"$HOOK_LOG\""}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(context.Background(), cfg)
	service.AttachHooks(hooks.Dispatcher{
		Registry: hooks.Discover(hooks.Options{Sources: []hooks.Source{{Path: configPath, Trusted: true}}}),
		Runner:   hooks.Runner{Environment: []string{"HOOK_LOG=" + logPath}},
	})
	watchPath := filepath.Join(workspace, "watched.txt")
	watcher := service.ensureHookWatcher()
	watcher.watchFiles("session", []string{watchPath})
	if err := os.WriteFile(watchPath, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher.scan(context.Background())
	if err := os.WriteFile(watchPath, []byte("changed-size"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher.scan(context.Background())
	if err := os.Remove(watchPath); err != nil {
		t.Fatal(err)
	}
	watcher.scan(context.Background())
	service.cancel()
	service.wg.Wait()
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) != 3 {
		t.Fatalf("file events = %q", contents)
	}
	for index, want := range []string{"add", "change", "unlink"} {
		var input map[string]any
		if err := json.Unmarshal([]byte(lines[index]), &input); err != nil {
			t.Fatal(err)
		}
		if input["event"] != want || input["file_path"] != watchPath || input["session_id"] != "session" {
			t.Fatalf("event %d = %#v", index, input)
		}
	}
}
