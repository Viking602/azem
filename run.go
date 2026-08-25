package azem

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/Viking602/azem/internal/app"
)

type Turn struct {
	SessionID        string
	Prompt           string
	Provider         string
	Model            string
	Reasoning        string
	AgentMode        string
	PlanMode         bool
	VibeMode         bool
	DisableSubagents bool
	ActiveSkills     []string
	Images           []Attachment
}

type RunResult struct {
	SessionID string
	RunID     string
	FinalText string
	Terminal  EventKind
	State     string
	Err       error
}

type Run struct {
	runtime   *Runtime
	sessionID string
	runID     string
	events    chan Event
	done      chan struct{}

	mu     sync.RWMutex
	result RunResult
}

func (runtime *Runtime) Start(ctx context.Context, turn Turn) (*Run, error) {
	if err := runtime.ensureOpen(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("azem: turn context is required")
	}
	turn.Prompt = strings.TrimSpace(turn.Prompt)
	if turn.Prompt == "" {
		return nil, errors.New("azem: prompt is required")
	}
	if turn.SessionID == "" {
		turn.SessionID = runtime.sessionID
	}
	subscription, err := runtime.Subscribe(4096)
	if err != nil {
		return nil, err
	}
	runID, err := runtime.service.StartConfiguredTurn(app.TurnRequest{
		SessionID: turn.SessionID, Prompt: turn.Prompt, Provider: turn.Provider, Model: turn.Model, Reasoning: turn.Reasoning,
		AgentMode: turn.AgentMode, PlanMode: turn.PlanMode, VibeMode: turn.VibeMode, DisableSubagents: turn.DisableSubagents,
		ActiveSkills: append([]string(nil), turn.ActiveSkills...), Images: append([]Attachment(nil), turn.Images...),
	})
	if err != nil {
		subscription.Close()
		return nil, err
	}
	run := &Run{
		runtime: runtime, sessionID: turn.SessionID, runID: runID, events: make(chan Event, 256), done: make(chan struct{}),
		result: RunResult{SessionID: turn.SessionID, RunID: runID},
	}
	go run.consume(ctx, subscription)
	return run, nil
}

func (runtime *Runtime) RunTurn(ctx context.Context, turn Turn) (RunResult, error) {
	run, err := runtime.Start(ctx, turn)
	if err != nil {
		return RunResult{}, err
	}
	return run.Wait(ctx)
}

func (run *Run) ID() string            { return run.runID }
func (run *Run) SessionID() string     { return run.sessionID }
func (run *Run) Events() <-chan Event  { return run.events }
func (run *Run) Done() <-chan struct{} { return run.done }

func (run *Run) Wait(ctx context.Context) (RunResult, error) {
	if run == nil {
		return RunResult{}, errors.New("azem: run is unavailable")
	}
	select {
	case <-run.done:
		run.mu.RLock()
		defer run.mu.RUnlock()
		return run.result, run.result.Err
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	}
}

func (run *Run) Steer(ctx context.Context, text string) error {
	if run == nil || run.runtime == nil {
		return errors.New("azem: run is unavailable")
	}
	return run.runtime.service.GuideActiveTurnWithAttachments(run.sessionID, run.runID, text, nil)
}

func (run *Run) FollowUp(ctx context.Context, text string) error {
	if run == nil || run.runtime == nil {
		return errors.New("azem: run is unavailable")
	}
	return run.runtime.service.FollowUpActiveTurn(run.sessionID, run.runID, text)
}

func (run *Run) Cancel() bool {
	if run == nil || run.runtime == nil {
		return false
	}
	select {
	case <-run.done:
		return false
	default:
		return run.runtime.service.CancelActiveWithChildren(true)
	}
}

func (run *Run) consume(ctx context.Context, subscription *Subscription) {
	defer subscription.Close()
	defer close(run.events)
	defer close(run.done)
	var final strings.Builder
	for {
		select {
		case <-ctx.Done():
			run.Cancel()
			run.setResult(RunResult{SessionID: run.sessionID, RunID: run.runID, Err: ctx.Err()})
			return
		case envelope, ok := <-subscription.C:
			if !ok {
				run.setResult(RunResult{SessionID: run.sessionID, RunID: run.runID, Err: errors.New("azem: event stream closed before run completion")})
				return
			}
			if envelope.Err != nil {
				run.setResult(RunResult{SessionID: run.sessionID, RunID: run.runID, Err: envelope.Err})
				return
			}
			event := envelope.Event
			if event.RunID != run.runID {
				continue
			}
			if event.Kind == app.EventTextDelta && (event.TextPhase == "final_answer" || event.TextPhase == "") {
				final.WriteString(event.Text)
			}
			select {
			case run.events <- event:
			default:
			}
			switch event.Kind {
			case app.EventRunFinished:
				run.setResult(RunResult{SessionID: run.sessionID, RunID: run.runID, FinalText: final.String(), Terminal: event.Kind, State: event.State})
				return
			case app.EventRunFailed:
				run.setResult(RunResult{SessionID: run.sessionID, RunID: run.runID, FinalText: final.String(), Terminal: event.Kind, State: event.State, Err: errors.New(firstText(event.Text, "provider run failed"))})
				return
			case app.EventRunCancelled:
				run.setResult(RunResult{SessionID: run.sessionID, RunID: run.runID, FinalText: final.String(), Terminal: event.Kind, State: event.State, Err: context.Canceled})
				return
			}
		}
	}
}

func (run *Run) setResult(result RunResult) {
	run.mu.Lock()
	run.result = result
	run.mu.Unlock()
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
