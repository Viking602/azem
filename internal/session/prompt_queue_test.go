package session

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestPromptQueueCASSpillReopenAndConflictProjection(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "queue.db")
	provider, err := sqlitestore.Open(ctx, path, sqlitestore.WithBlobRoot(filepath.Join(root, "blobs")))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(provider.DB(), provider.Blobs())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Queue"}); err != nil {
		t.Fatal(err)
	}
	empty, err := service.LoadPromptQueue(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Version != PromptQueueVersion || empty.Revision != 0 || empty.State != PromptQueueActive || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("empty queue = %#v", empty)
	}
	now := time.Now().UTC()
	largeText := strings.Repeat("queued work ", 800)
	attachment := Attachment{ID: "img-1", Name: "screen.png", MIME: "image/png", Path: filepath.Join(root, "attachments", "screen.png"), Size: 12}
	first, err := service.SavePromptQueueCAS(ctx, "session", 0, PromptQueueV1{
		State: PromptQueueActive,
		Items: []QueuedPromptV1{{ID: "item-1", Text: largeText, Attachments: []Attachment{attachment}, State: QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || len(first.Items) != 1 {
		t.Fatalf("saved queue = %#v", first)
	}
	var inline []byte
	var digest string
	if err := provider.DB().QueryRowContext(ctx, `SELECT items_inline,items_digest FROM session_prompt_queues WHERE session_id='session'`).Scan(&inline, &digest); err != nil {
		t.Fatal(err)
	}
	if len(inline) != 0 || digest == "" {
		t.Fatalf("large queue was not spilled: inline=%q digest=%q", inline, digest)
	}
	current, err := service.SavePromptQueueCAS(ctx, "session", 0, PromptQueueV1{State: PromptQueueActive, Items: []QueuedPromptV1{}})
	var conflict *PromptQueueRevisionConflictError
	if !errors.As(err, &conflict) || current.Revision != 1 || conflict.Current.Items[0].ID != "item-1" {
		t.Fatalf("conflict queue=%#v error=%v", current, err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(ctx, path, sqlitestore.WithBlobRoot(filepath.Join(root, "blobs")))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	loaded, err := NewService(reopened.DB(), reopened.Blobs()).LoadPromptQueue(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 1 || loaded.Items[0].Text != largeText || len(loaded.Items[0].Attachments) != 1 || loaded.Items[0].Attachments[0].ID != "img-1" {
		t.Fatalf("reopened queue = %#v", loaded)
	}
}

func TestPromptQueueConcurrentCASHasOneWinner(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "queue-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	service := NewService(provider.DB(), provider.Blobs())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Queue"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for index := range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := service.SavePromptQueueCAS(ctx, "session", 0, PromptQueueV1{
				State: PromptQueueActive,
				Items: []QueuedPromptV1{{ID: string(rune('a' + index)), Text: "queued", State: QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}},
			})
			results <- err
		}()
	}
	workers.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrPromptQueueRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected CAS error: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("CAS winners=%d conflicts=%d", winners, conflicts)
	}
}

func TestPromptQueueRejectsInvalidAndCorruptDocuments(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "queue-invalid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	service := NewService(provider.DB(), provider.Blobs())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Queue"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items := make([]QueuedPromptV1, MaxPromptQueueItems+1)
	for index := range items {
		items[index] = QueuedPromptV1{ID: string(rune(index + 1)), Text: "work", State: QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}
	}
	if _, err := service.SavePromptQueueCAS(ctx, "session", 0, PromptQueueV1{State: PromptQueueActive, Items: items}); err == nil {
		t.Fatal("oversized item count was accepted")
	}
	tooLarge := strings.Repeat("x", MaxPromptQueueDocument+1)
	if _, err := service.SavePromptQueueCAS(ctx, "session", 0, PromptQueueV1{State: PromptQueueActive, Items: []QueuedPromptV1{{ID: "large", Text: tooLarge, State: QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}}}); err == nil {
		t.Fatal("oversized queue document was accepted")
	}
	saved, err := service.SavePromptQueueCAS(ctx, "session", 0, PromptQueueV1{State: PromptQueueActive, Items: []QueuedPromptV1{{ID: "valid", Text: strings.Repeat("x", 5000), State: QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}}})
	if err != nil || saved.Revision != 1 {
		t.Fatalf("valid spilled queue = %#v, %v", saved, err)
	}
	if _, err := provider.DB().ExecContext(ctx, `UPDATE session_prompt_queues SET items_inline='[{"id":"different"}]' WHERE session_id='session'`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadPromptQueue(ctx, "session"); err == nil || !strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("corrupt inline/digest mismatch error = %v", err)
	}
}

func TestPromptQueueGuidanceRollsBackWhenTranscriptWriteFails(t *testing.T) {
	ctx := context.Background()
	provider, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	service := NewService(provider.DB(), provider.Blobs())
	if _, err := service.Ensure(ctx, Session{ID: "session", Title: "Queue"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	queue, err := service.SavePromptQueueCAS(ctx, "session", 0, PromptQueueV1{Items: []QueuedPromptV1{{ID: "item", Text: "keep me", State: QueuedPromptQueued, CreatedAt: now, UpdatedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DB().ExecContext(ctx, `CREATE TRIGGER fail_guidance BEFORE INSERT ON session_blocks BEGIN SELECT RAISE(ABORT, 'injected transcript failure'); END`); err != nil {
		t.Fatal(err)
	}
	next := queue.Clone()
	next.Items = nil
	if _, _, err := service.SavePromptQueueWithGuidance(ctx, "session", queue.Revision, next, Block{Kind: "user", State: "guidance", RunID: "run", Content: "keep me"}); err == nil {
		t.Fatal("expected transcript failure")
	}
	actual, err := service.LoadPromptQueue(ctx, "session")
	if err != nil || actual.Revision != queue.Revision || len(actual.Items) != 1 {
		t.Fatalf("queue lost after rollback: %+v, %v", actual, err)
	}
}
