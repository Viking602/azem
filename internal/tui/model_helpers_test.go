package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/session"
)

type inertRuntime struct{}

func (inertRuntime) NextEvent(context.Context) (app.Event, error) {
	return app.Event{}, errors.New("closed")
}

func (inertRuntime) StartConfiguredTurn(app.TurnRequest) (string, error) {
	return "run_test", nil
}

func (inertRuntime) GuideActiveTurnWithAttachments(string, string, string, []session.Attachment) error {
	return nil
}

func (inertRuntime) CancelRunWithChildren(string, string, bool) (bool, error) {
	return true, nil
}
func (inertRuntime) ExecuteAction(context.Context, Action) error { return nil }
func (inertRuntime) Request(context.Context, desktopipc.Method, any, any) error {
	return errActionUnsupported
}

func (inertRuntime) ImportImage(string, string) (session.Attachment, error) {
	return session.Attachment{}, errActionUnsupported
}

func (inertRuntime) ImportImageBytes(string, string, string, []byte) (session.Attachment, error) {
	return session.Attachment{}, errActionUnsupported
}
func (inertRuntime) HasActiveChildren() bool { return false }
func (inertRuntime) ApprovalModeState() (ApprovalMode, bool) {
	return ApprovalModePrompt, false
}

func (inertRuntime) ActiveShellExecutions() []agentservice.ShellExecutionSnapshot {
	return nil
}
func (inertRuntime) Detach() error { return nil }

func assertTranscriptStatusOnly(t *testing.T, footer, label string) {
	t.Helper()
	fields := strings.Fields(ansi.Strip(footer))
	if len(fields) != 2 || fields[1] != label {
		t.Fatalf("transcript status is too detailed: %q", ansi.Strip(footer))
	}
}

func assertTranscriptTimedStatus(t *testing.T, footer, label string) {
	t.Helper()
	fields := strings.Fields(ansi.Strip(footer))
	if len(fields) != 3 || fields[1] != label || !strings.HasSuffix(fields[2], "s") {
		t.Fatalf("transcript status timer is missing or too detailed: %q", ansi.Strip(footer))
	}
}

type configuredTurnRuntime struct {
	inertRuntime
	request     app.TurnRequest
	guidance    []string
	guidanceErr error
}

func (r *configuredTurnRuntime) StartConfiguredTurn(request app.TurnRequest) (string, error) {
	r.request = request
	return "run_configured", nil
}

func (r *configuredTurnRuntime) GuideActiveTurnWithAttachments(_, _ string, text string, _ []session.Attachment) error {
	if r.guidanceErr != nil {
		return r.guidanceErr
	}
	r.guidance = append(r.guidance, text)
	return nil
}

type skillCommandRuntime struct {
	inertRuntime
	request        app.TurnRequest
	actions        []Action
	expandedPrompt string
}

func (r *skillCommandRuntime) StartConfiguredTurn(request app.TurnRequest) (string, error) {
	r.request = request
	return "run_skill", nil
}

func (r *skillCommandRuntime) ExecuteAction(_ context.Context, action Action) error {
	r.actions = append(r.actions, action)
	return nil
}

func (r *skillCommandRuntime) Request(_ context.Context, method desktopipc.Method, _ any, target any) error {
	if method != desktopipc.MethodExpandSkillInvocation {
		return errActionUnsupported
	}
	prompt := r.expandedPrompt
	if prompt == "" {
		prompt = "expanded verify instructions"
	}
	encoded, _ := json.Marshal(desktop.SkillInvocation{Name: "verify", Prompt: prompt})
	return json.Unmarshal(encoded, target)
}

type recordedRuntime struct {
	cancelled          bool
	actions            []Action
	shutdown           bool
	foregroundChildren bool
	backgroundChildren bool
	cancelChildren     bool
	shells             []agentservice.ShellExecutionSnapshot
	selectedSession    string
}

func (*recordedRuntime) NextEvent(context.Context) (app.Event, error) {
	return app.Event{}, errors.New("closed")
}

func (*recordedRuntime) StartConfiguredTurn(app.TurnRequest) (string, error) {
	return "run_next", nil
}

func (*recordedRuntime) GuideActiveTurnWithAttachments(string, string, string, []session.Attachment) error {
	return nil
}

func (r *recordedRuntime) CancelRunWithChildren(_, _ string, children bool) (bool, error) {
	r.cancelled = true
	r.cancelChildren = children
	return true, nil
}

func (r *recordedRuntime) HasActiveChildren() bool {
	return r.foregroundChildren || r.backgroundChildren
}

func (r *recordedRuntime) ActiveShellExecutions() []agentservice.ShellExecutionSnapshot {
	return append([]agentservice.ShellExecutionSnapshot(nil), r.shells...)
}

func (r *recordedRuntime) ExecuteAction(_ context.Context, action Action) error {
	r.actions = append(r.actions, action)
	return nil
}

func (r *recordedRuntime) Request(_ context.Context, method desktopipc.Method, payload any, target any) error {
	if method != desktopipc.MethodResumeSession {
		return errActionUnsupported
	}
	encoded, _ := json.Marshal(payload)
	var params struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(encoded, &params)
	r.selectedSession = params.SessionID
	snapshot := desktop.ReconnectSnapshot{
		DaemonEpoch: "test", SelectedSessionID: params.SessionID,
		Base: desktop.Snapshot{SessionID: params.SessionID},
		Session: &app.SessionProjection{
			Version: app.SessionProjectionVersion, Session: session.Session{ID: params.SessionID},
			Blocks: []app.TranscriptBlock{}, ToolRecords: []session.ToolRecord{},
			Todo: session.TodoList{Phases: []session.TodoPhase{}}, AgentSnapshots: []app.AgentSnapshotPayload{},
		},
		Runs: []app.RunProjection{}, LiveBlocks: []app.LiveBlockProjection{}, Controls: []app.PendingControlProjection{},
		PromptQueues: []session.PromptQueueV1{}, RuntimeRecovery: app.RecoveryProjection{State: "clear", Items: []app.PendingControlProjection{}},
	}
	encoded, _ = json.Marshal(snapshot)
	return json.Unmarshal(encoded, target)
}

func (r *recordedRuntime) ImportImage(string, string) (session.Attachment, error) {
	return session.Attachment{}, errActionUnsupported
}

func (r *recordedRuntime) ImportImageBytes(string, string, string, []byte) (session.Attachment, error) {
	return session.Attachment{}, errActionUnsupported
}

func (r *recordedRuntime) ApprovalModeState() (ApprovalMode, bool) {
	return ApprovalModePrompt, false
}

type blockingActionRuntime struct {
	recordedRuntime
	started chan struct{}
	release chan struct{}
}

func (r *blockingActionRuntime) ExecuteAction(ctx context.Context, _ Action) error {
	close(r.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.release:
		return nil
	}
}

func (r *recordedRuntime) Detach() error {
	r.shutdown = true
	return nil
}

func renderedTextPoint(t *testing.T, rendered, needle string) (int, int) {
	t.Helper()
	for row, line := range strings.Split(ansi.Strip(rendered), "\n") {
		offset := strings.Index(line, needle)
		if offset >= 0 {
			return ansi.StringWidth(line[:offset]), row
		}
	}
	t.Fatalf("rendered output does not contain %q:\n%s", needle, ansi.Strip(rendered))
	return 0, 0
}

func assertTextContainsAll(t *testing.T, text string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("text missing %q:\n%s", value, text)
		}
	}
}
