package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestSelectSessionDoesNotNavigateOtherRenderersOrSwitchRuntimeHooks(t *testing.T) {
	ctx := context.Background()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "selection.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	sessions := session.NewService(store.DB(), store.Blobs())
	for _, id := range []string{"session-a", "session-b"} {
		if _, err := sessions.Ensure(ctx, session.Session{ID: id, Title: id, AgentMode: "single"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sessions.SetWorkspaceSession(ctx, workspace, "session-a"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = workspace
	service := NewService(ctx, cfg)
	service.AttachDurable(sessions, nil)
	service.SetWorkspaceAnchor(workspace)
	service.mu.Lock()
	service.currentSession = "session-a"
	service.hookSessions["session-a"] = struct{}{}
	service.mu.Unlock()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
	})

	projection, err := service.SelectSession(ctx, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Kind != EventSessionLoaded || projection.SessionID != "session-b" {
		t.Fatalf("selection projection = %#v", projection)
	}
	service.mu.Lock()
	current := service.currentSession
	_, switchedHooks := service.hookSessions["session-b"]
	service.mu.Unlock()
	if current != "session-a" || switchedHooks {
		t.Fatalf("client-local selection changed runtime state: current=%q hooks=%t", current, switchedHooks)
	}
	recent, err := sessions.WorkspaceSession(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if recent != "session-b" {
		t.Fatalf("workspace preference = %q, want session-b", recent)
	}

	deadline, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	for {
		event, err := service.NextEvent(deadline)
		if errors.Is(err, context.DeadlineExceeded) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == EventSessionLoaded && event.SessionID != "" {
			t.Fatalf("client-local selection broadcast navigation event %#v", event)
		}
	}
}
