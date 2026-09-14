package app

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestPromptQueueMutationsUseRevisionedServerState(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "mutations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Queue"}); err != nil {
		t.Fatal(err)
	}
	seed, err := sessions.SavePromptQueueCAS(ctx, "session", 0, session.PromptQueueV1{State: session.PromptQueuePaused, PauseReason: "test", Items: []session.QueuedPromptV1{}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	service.AttachAttachments(filepath.Join(t.TempDir(), "attachments"))
	defer service.Shutdown(ctx)
	now := time.Now().UTC()
	attachment, err := service.ImportImageBytes("session", "screen.png", "image/png", minimalPNG())
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.MutatePromptQueue(ctx, PromptQueueMutation{
		Operation: PromptQueueEnqueue, SessionID: "session", MutationID: "enqueue-1", ExpectedRevision: seed.Revision,
		Item: session.QueuedPromptV1{ID: "first", Text: "first task", Attachments: []session.Attachment{attachment}, State: session.QueuedPromptQueued, CreatedAt: now, UpdatedAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.MutatePromptQueue(ctx, PromptQueueMutation{
		Operation: PromptQueueEnqueue, SessionID: "session", MutationID: "enqueue-2", ExpectedRevision: first.Revision,
		Item: session.QueuedPromptV1{ID: "second", Text: "second task", State: session.QueuedPromptQueued, CreatedAt: now, UpdatedAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	self, err := service.MutatePromptQueue(ctx, PromptQueueMutation{
		Operation: PromptQueueReorder, SessionID: "session", MutationID: "self-drop", ExpectedRevision: second.Revision,
		ItemID: "second", BeforeID: "second",
	})
	if err != nil || self.Revision != second.Revision || len(self.Items) != 2 || self.Items[0].ID != "first" || self.Items[1].ID != "second" {
		t.Fatalf("self drop must preserve queue and revision: %#v, %v", self, err)
	}
	reordered, err := service.MutatePromptQueue(ctx, PromptQueueMutation{
		Operation: PromptQueueReorder, SessionID: "session", MutationID: "reorder", ExpectedRevision: second.Revision,
		ItemID: "second", BeforeID: "first",
	})
	if err != nil || reordered.Items[0].ID != "second" {
		t.Fatalf("reordered queue = %#v, %v", reordered, err)
	}
	updated, err := service.MutatePromptQueue(ctx, PromptQueueMutation{
		Operation: PromptQueueUpdate, SessionID: "session", MutationID: "update", ExpectedRevision: reordered.Revision,
		ItemID: "second", Item: session.QueuedPromptV1{ID: "second", Text: "updated second", Attachments: []session.Attachment{}},
	})
	if err != nil || updated.Items[0].Text != "updated second" {
		t.Fatalf("updated queue = %#v, %v", updated, err)
	}
	current, err := service.MutatePromptQueue(ctx, PromptQueueMutation{
		Operation: PromptQueueRemove, SessionID: "session", MutationID: "stale", ExpectedRevision: reordered.Revision, ItemID: "first",
	})
	if !errors.Is(err, session.ErrPromptQueueRevisionConflict) || current.Revision != updated.Revision {
		t.Fatalf("stale mutation current=%#v error=%v", current, err)
	}
	removed, err := service.MutatePromptQueue(ctx, PromptQueueMutation{
		Operation: PromptQueueRemove, SessionID: "session", MutationID: "remove", ExpectedRevision: updated.Revision, ItemID: "first",
	})
	if err != nil || len(removed.Items) != 1 || removed.Items[0].ID != "second" {
		t.Fatalf("removed queue = %#v, %v", removed, err)
	}

	dispatching := removed.Clone()
	dispatching.Items[0].State = session.QueuedPromptDispatching
	dispatching, err = sessions.SavePromptQueueCAS(ctx, "session", dispatching.Revision, dispatching)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MutatePromptQueue(ctx, PromptQueueMutation{
		Operation: PromptQueueUpdate, SessionID: "session", MutationID: "immutable", ExpectedRevision: dispatching.Revision,
		ItemID: "second", Item: session.QueuedPromptV1{Text: "must fail"},
	}); !errors.Is(err, ErrInvalidPromptQueueAction) {
		t.Fatalf("dispatching item update error = %v", err)
	}
}

func TestPromptQueueCoordinatorDispatchesCrossSessionFIFOWithoutDuplicates(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "fifo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	for _, id := range []string{"session-a", "session-b"} {
		if _, err := sessions.Ensure(ctx, session.Session{ID: id, Title: id, ProviderID: "test", ModelID: "test", AgentMode: "single"}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().UTC().Add(-time.Minute)
	for index, id := range []string{"session-a", "session-b"} {
		if _, err := sessions.SavePromptQueueCAS(ctx, id, 0, session.PromptQueueV1{
			State: session.PromptQueueActive,
			Items: []session.QueuedPromptV1{{ID: "item-" + id, Text: "queued " + id, State: session.QueuedPromptQueued, CreatedAt: base.Add(time.Duration(index) * time.Second), UpdatedAt: base}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	defer service.Shutdown(context.Background())
	service.StartPromptQueueCoordinator()
	waitForQueueCondition(t, 5*time.Second, func() bool {
		left, leftErr := sessions.LoadPromptQueue(ctx, "session-a")
		right, rightErr := sessions.LoadPromptQueue(ctx, "session-b")
		return leftErr == nil && rightErr == nil && len(left.Items) == 0 && len(right.Items) == 0
	})
	var startedAt []int64
	for _, id := range []string{"session-a", "session-b"} {
		projection, err := sessions.LoadDisplayProjection(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		users := 0
		for _, block := range projection.Blocks {
			if block.Kind != "user" {
				continue
			}
			users++
			if block.Data["queueItemId"] != "item-"+id {
				t.Fatalf("queue identity missing from %s block %#v", id, block)
			}
			value, _ := strconv.ParseInt(block.Data["createdAt"], 10, 64)
			startedAt = append(startedAt, value)
		}
		if users != 1 {
			t.Fatalf("session %s queued user blocks=%d", id, users)
		}
	}
	if len(startedAt) != 2 || startedAt[0] > startedAt[1] {
		t.Fatalf("cross-session dispatch order = %#v", startedAt)
	}
}

func TestPromptQueueRecoveryConsumesBoundItemAndCancellationPausesRemainder(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Queue", AgentMode: "single"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := sessions.AppendBlock(ctx, "session", session.Block{Kind: "user", RunID: "run-consumed", Content: "consumed", Data: map[string]string{"queueItemId": "consumed"}}); err != nil {
		t.Fatal(err)
	}
	queue, err := sessions.SavePromptQueueCAS(ctx, "session", 0, session.PromptQueueV1{
		State: session.PromptQueuePaused, PauseReason: "recovery-test",
		Items: []session.QueuedPromptV1{
			{ID: "consumed", Text: "must not repeat", State: session.QueuedPromptDispatching, CreatedAt: now, UpdatedAt: now},
			{ID: "remaining", Text: "wait", State: session.QueuedPromptQueued, CreatedAt: now.Add(time.Second), UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	defer service.Shutdown(context.Background())
	service.StartPromptQueueCoordinator()
	waitForQueueCondition(t, time.Second, func() bool {
		loaded, loadErr := sessions.LoadPromptQueue(ctx, "session")
		return loadErr == nil && loaded.Revision > queue.Revision && len(loaded.Items) == 1 && loaded.Items[0].ID == "remaining"
	})
	activeQueue, err := sessions.LoadPromptQueue(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	activeQueue.State, activeQueue.PauseReason = session.PromptQueueActive, ""
	if _, err := sessions.SavePromptQueueCAS(ctx, "session", activeQueue.Revision, activeQueue); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.activeRun = "run-active"
	service.activeSession = "session"
	service.activeRunProjection = RunProjection{SessionID: "session", RunID: "run-active", State: "running", Activity: RunActivityWaitingModel, ActiveOperations: []ActiveOperation{}, AllowedActions: []string{"stop"}}
	service.mu.Unlock()
	service.emitTerminal(ctx, Event{Kind: EventRunCancelled, SessionID: "session", RunID: "run-active", State: "cancelled"})
	paused, err := sessions.LoadPromptQueue(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != session.PromptQueuePaused || paused.PauseReason != "cancelled" || len(paused.Items) != 1 {
		t.Fatalf("cancelled queue = %#v", paused)
	}
	resumed := paused.Clone()
	resumed.State, resumed.PauseReason = session.PromptQueueActive, ""
	resumed, err = sessions.SavePromptQueueCAS(ctx, "session", resumed.Revision, resumed)
	if err != nil {
		t.Fatal(err)
	}
	if !service.emit(ctx, Event{Kind: EventRecoveryState, SessionID: "session", RunID: "run-suspended", State: "suspended"}) {
		t.Fatal("suspension event was rejected")
	}
	suspended, err := sessions.LoadPromptQueue(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if suspended.State != session.PromptQueuePaused || suspended.PauseReason != "suspended" || suspended.Revision <= resumed.Revision {
		t.Fatalf("suspended queue = %#v", suspended)
	}
}

func waitForQueueCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("prompt queue condition did not converge")
}

func TestGuideQueuedPromptPersistsAndConsumesOnce(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "guide.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Queue"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	service.AttachAttachments(filepath.Join(t.TempDir(), "attachments"))
	defer service.Shutdown(ctx)
	control := service.TurnControl("run")
	service.mu.Lock()
	service.activeSession = "session"
	service.activeRun = "run"
	service.guidanceOpen = true
	service.mu.Unlock()
	now := time.Now().UTC()
	queue, err := sessions.SavePromptQueueCAS(ctx, "session", 0, session.PromptQueueV1{State: session.PromptQueuePaused, Items: []session.QueuedPromptV1{{ID: "item", Text: "调整实现顺序", State: session.QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	mutation := PromptQueueMutation{Operation: PromptQueueGuide, SessionID: "session", RunID: "stale", ItemID: "item", MutationID: "guide", ExpectedRevision: queue.Revision}
	if _, err := service.MutatePromptQueue(ctx, mutation); !errors.Is(err, ErrStaleRun) {
		t.Fatalf("stale guide = %v", err)
	}
	mutation.RunID = "run"
	saved, err := service.MutatePromptQueue(ctx, mutation)
	if err != nil || len(saved.Items) != 0 {
		t.Fatalf("guide = %+v, %v", saved, err)
	}
	if _, err := service.MutatePromptQueue(ctx, mutation); !errors.Is(err, session.ErrPromptQueueRevisionConflict) {
		t.Fatalf("duplicate guide = %v", err)
	}
	controls, err := control.Drain(ctx, turnControlBeforeModel)
	if err != nil || len(controls) != 1 || controls[0].Message.Text != "调整实现顺序" {
		t.Fatalf("controls=%+v, %v", controls, err)
	}
	projection, err := sessions.LoadDisplayProjection(ctx, "session")
	if err != nil || len(projection.Blocks) != 1 || projection.Blocks[0].State != "guidance" {
		t.Fatalf("projection=%+v, %v", projection, err)
	}
	service.mu.Lock()
	service.activeRun = ""
	service.activeSession = ""
	service.mu.Unlock()
}

func TestFailedTurnPausesQueueBeforeNextDispatch(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "failed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	sessions := session.NewService(provider.DB(), provider.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "Queue"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = sessions.SavePromptQueueCAS(ctx, "session", 0, session.PromptQueueV1{State: session.PromptQueueActive, Items: []session.QueuedPromptV1{{ID: "next", Text: "next task", State: session.QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ctx, config.Default())
	service.AttachDurable(sessions, nil)
	defer service.Shutdown(ctx)
	service.activeRun, service.activeSession = "failed-run", "session"
	service.emitTerminal(ctx, Event{Kind: EventRunFailed, SessionID: "session", RunID: "failed-run", State: "failed"})
	queue, err := sessions.LoadPromptQueue(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if queue.State != session.PromptQueuePaused || queue.PauseReason != "failed" || len(queue.Items) != 1 {
		t.Fatalf("failed turn lost or dispatched queue: %#v", queue)
	}
	if service.dispatchNextQueuedPrompt(ctx) {
		t.Fatal("failed run dispatched its successor")
	}
}
