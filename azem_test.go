package azem

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestPublicRuntimeSubscriptionAndSessionOperations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime, service, closeStore := publicTestRuntime(t, ctx)
	defer closeStore()
	subscription, err := runtime.Subscribe(32)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if err := service.ExecuteAction(ctx, app.Action{Kind: app.ActionNewSession, Target: "Created title"}); err != nil {
		t.Fatal(err)
	}
	select {
	case envelope := <-subscription.C:
		if envelope.Err != nil || envelope.Event.Kind != app.EventSessionLoaded {
			t.Fatalf("subscription event=%#v", envelope)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("public subscription received no event")
	}
	if err := runtime.ForkSession(ctx, "session", "forked"); err != nil {
		t.Fatal(err)
	}
	tree, err := runtime.LoadSessionTree(ctx, "forked")
	if err != nil || tree.ParentSessionID != "session" {
		t.Fatalf("fork tree=%#v error=%v", tree, err)
	}
	output := filepath.Join(t.TempDir(), "session.json")
	if _, err := runtime.ExportSession(ctx, "session", output, ExportJSON, ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer closeCancel()
	if err := runtime.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Subscribe(32); err == nil {
		t.Fatal("subscribed after runtime close")
	}
}

func TestPublicRunWaitCollectsFinalTextAndTerminalState(t *testing.T) {
	runtime := &Runtime{}
	state := &subscriptionState{channel: make(chan EventEnvelope, 8)}
	subscription := &Subscription{C: state.channel}
	run := &Run{runtime: runtime, sessionID: "session", runID: "run", events: make(chan Event, 8), done: make(chan struct{}), result: RunResult{SessionID: "session", RunID: "run"}}
	ctx := context.Background()
	go run.consume(ctx, subscription)
	state.channel <- EventEnvelope{Event: app.Event{Kind: app.EventTextDelta, RunID: "run", TextPhase: "commentary", Text: "working"}}
	state.channel <- EventEnvelope{Event: app.Event{Kind: app.EventTextDelta, RunID: "run", TextPhase: "final_answer", Text: "done"}}
	state.channel <- EventEnvelope{Event: app.Event{Kind: app.EventRunFinished, RunID: "run", State: "completed"}}
	result, err := run.Wait(ctx)
	if err != nil || result.FinalText != "done" || result.Terminal != app.EventRunFinished || result.State != "completed" {
		t.Fatalf("run result=%#v error=%v", result, err)
	}
}

func TestPublicRunWaitCancelsActiveRunWhenWaitContextEnds(t *testing.T) {
	called := 0
	run := &Run{
		sessionID: "session", runID: "run", events: make(chan Event, 1), done: make(chan struct{}),
		cancel: func() bool { called++; return true },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := run.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v", err)
	}
	if called != 1 {
		t.Fatalf("cancel calls = %d, want 1", called)
	}
}

func publicTestRuntime(t *testing.T, ctx context.Context) (*Runtime, *app.Service, func()) {
	t.Helper()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Public"}); err != nil {
		t.Fatal(err)
	}
	service := app.NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	runtimeCtx, cancel := context.WithCancel(ctx)
	runtime := &Runtime{service: service, sessions: sessions, config: config.Default(), workspace: t.TempDir(), sessionID: "session", ctx: runtimeCtx, cancel: cancel, subs: make(map[uint64]*subscriptionState)}
	go runtime.pumpEvents()
	return runtime, service, func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = service.Shutdown(shutdownCtx)
		_ = store.Close(context.Background())
	}
}
