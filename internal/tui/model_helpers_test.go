package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/app"
)

type inertRuntime struct{}

func (inertRuntime) NextEvent(context.Context) (app.Event, error) {
	return app.Event{}, errors.New("closed")
}

func (inertRuntime) StartTurn(string) (string, error) { return "run_test", nil }

func (inertRuntime) CancelActive() bool { return true }

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

func (r *configuredTurnRuntime) GuideActiveTurn(_, _ string, text string) error {
	if r.guidanceErr != nil {
		return r.guidanceErr
	}
	r.guidance = append(r.guidance, text)
	return nil
}

type skillCommandRuntime struct {
	inertRuntime
	request app.TurnRequest
	actions []Action
}

func (r *skillCommandRuntime) StartConfiguredTurn(request app.TurnRequest) (string, error) {
	r.request = request
	return "run_skill", nil
}

func (r *skillCommandRuntime) ExecuteAction(_ context.Context, action Action) error {
	r.actions = append(r.actions, action)
	return nil
}

type recordedRuntime struct {
	cancelled          bool
	actions            []Action
	shutdown           bool
	foregroundChildren bool
	backgroundChildren bool
	cancelChildren     bool
	shells             []agentservice.ShellExecutionSnapshot
}

func (*recordedRuntime) NextEvent(context.Context) (app.Event, error) {
	return app.Event{}, errors.New("closed")
}

func (*recordedRuntime) StartTurn(string) (string, error) { return "run_next", nil }

func (r *recordedRuntime) CancelActive() bool {
	r.cancelled = true
	return true
}

func (r *recordedRuntime) HasActiveForegroundChildren() bool {
	return r.foregroundChildren
}

func (r *recordedRuntime) HasActiveChildren() bool {
	return r.foregroundChildren || r.backgroundChildren
}

func (r *recordedRuntime) CancelActiveWithChildren(children bool) bool {
	r.cancelled = true
	r.cancelChildren = children
	return true
}

func (r *recordedRuntime) ActiveShellExecutions() []agentservice.ShellExecutionSnapshot {
	return append([]agentservice.ShellExecutionSnapshot(nil), r.shells...)
}

func (r *recordedRuntime) ExecuteAction(_ context.Context, action Action) error {
	r.actions = append(r.actions, action)
	return nil
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

func (r *recordedRuntime) Shutdown(context.Context) error {
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
