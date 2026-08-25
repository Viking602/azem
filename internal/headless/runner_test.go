package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/app"
)

type fakeRuntime struct {
	mu         sync.Mutex
	runs       int
	events     chan app.Event
	actions    []app.Action
	cancelled  bool
	makeEvents func(runID string, request app.TurnRequest) []app.Event
}

func newFakeRuntime(makeEvents func(string, app.TurnRequest) []app.Event) *fakeRuntime {
	return &fakeRuntime{events: make(chan app.Event, 64), makeEvents: makeEvents}
}

func (runtime *fakeRuntime) StartConfiguredTurn(request app.TurnRequest) (string, error) {
	runtime.mu.Lock()
	runtime.runs++
	runID := fmt.Sprintf("run-%d", runtime.runs)
	runtime.mu.Unlock()
	for _, event := range runtime.makeEvents(runID, request) {
		runtime.events <- event
	}
	return runID, nil
}

func (runtime *fakeRuntime) NextEvent(ctx context.Context) (app.Event, error) {
	select {
	case event := <-runtime.events:
		return event, nil
	case <-ctx.Done():
		return app.Event{}, ctx.Err()
	}
}

func (runtime *fakeRuntime) ExecuteAction(_ context.Context, action app.Action) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.actions = append(runtime.actions, action)
	return nil
}

func (runtime *fakeRuntime) CancelActiveWithChildren(bool) bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.cancelled = true
	return true
}

func TestTextModeStreamsVisiblePhasesAndOptionalThinking(t *testing.T) {
	runtime := newFakeRuntime(func(runID string, _ app.TurnRequest) []app.Event {
		return []app.Event{
			{Kind: app.EventThinkingDelta, RunID: runID, Text: "private thought"},
			{Kind: app.EventTextDelta, RunID: runID, Text: "Checking. ", TextPhase: "commentary"},
			{Kind: app.EventTextDelta, RunID: runID, Text: "Done.", TextPhase: "final_answer"},
			{Kind: app.EventRunFinished, RunID: runID, State: "completed"},
		}
	})
	var output bytes.Buffer
	result, err := Run(context.Background(), runtime, Options{Mode: ModeText, SessionID: "session", Prompts: []string{"one"}, Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "Checking. Done.\n" || result.Text != "Checking. Done." || len(result.RunIDs) != 1 {
		t.Fatalf("text output=%q result=%#v", output.String(), result)
	}
	thinkingRuntime := newFakeRuntime(runtime.makeEvents)
	output.Reset()
	if _, err := Run(context.Background(), thinkingRuntime, Options{Mode: ModeText, SessionID: "session", Prompts: []string{"one"}, PrintThinking: true, Output: &output}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "private thought") {
		t.Fatalf("thinking output=%q", output.String())
	}
}

func TestJSONModeEmitsReadyAndVersionedOrderedEvents(t *testing.T) {
	runtime := newFakeRuntime(func(runID string, _ app.TurnRequest) []app.Event {
		return []app.Event{
			{Kind: app.EventTextDelta, SessionID: "session", RunID: runID, Text: "answer", TextPhase: "final_answer", At: time.Unix(1, 0)},
			{Kind: app.EventRunFinished, SessionID: "session", RunID: runID, State: "completed", At: time.Unix(2, 0)},
		}
	})
	var output bytes.Buffer
	if _, err := Run(context.Background(), runtime, Options{Mode: ModeJSON, SessionID: "session", Prompts: []string{"one"}, Output: &output}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("JSON lines=%q", lines)
	}
	for index, line := range lines {
		var frame JSONFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("line %d: %v", index, err)
		}
		if frame.Version != 1 {
			t.Fatalf("frame=%#v", frame)
		}
		if index == 0 && frame.Type != "ready" {
			t.Fatalf("ready frame=%#v", frame)
		}
		if index > 0 && (frame.Type != "event" || frame.Sequence != int64(index)) {
			t.Fatalf("event frame=%#v", frame)
		}
	}
}

func TestHeadlessApprovalRequiresExplicitAutoApprove(t *testing.T) {
	makeEvents := func(runID string, _ app.TurnRequest) []app.Event {
		return []app.Event{
			{Kind: app.EventApprovalRequested, RunID: runID, ApprovalID: "approval"},
			{Kind: app.EventRunFinished, RunID: runID, State: "completed"},
		}
	}
	denied := newFakeRuntime(makeEvents)
	_, err := Run(context.Background(), denied, Options{Mode: ModeText, Prompts: []string{"one"}, Output: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "requested approval") || len(denied.actions) != 1 || denied.actions[0].Decision != "deny" {
		t.Fatalf("denied actions=%#v error=%v", denied.actions, err)
	}
	approved := newFakeRuntime(makeEvents)
	if _, err := Run(context.Background(), approved, Options{Mode: ModeText, Prompts: []string{"one"}, AutoApprove: true, Output: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if len(approved.actions) != 1 || approved.actions[0].Decision != "once" {
		t.Fatalf("approved actions=%#v", approved.actions)
	}
}

func TestHeadlessCancellationPropagatesAndCancelsChildren(t *testing.T) {
	runtime := newFakeRuntime(func(runID string, _ app.TurnRequest) []app.Event {
		return []app.Event{{Kind: app.EventUserInputRequested, RunID: runID}}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := Run(ctx, runtime, Options{Mode: ModeText, Prompts: []string{"one"}, Output: &bytes.Buffer{}})
	if !errors.Is(err, context.DeadlineExceeded) || !runtime.cancelled {
		t.Fatalf("cancellation error=%v cancelled=%v", err, runtime.cancelled)
	}
}
