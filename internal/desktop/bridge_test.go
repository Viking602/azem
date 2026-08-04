package desktop

import (
	"context"
	"testing"
	"time"

	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
)

func TestBridgeInitialiseAndEventProjection(t *testing.T) {
	cfg := config.Default()
	runtime := azemapp.NewService(context.Background(), cfg)
	runtime.Bootstrap()
	events := make(chan Event, 16)
	bridge := NewBridge(context.Background(), azemapp.BootstrapResult{
		Config: cfg, SessionID: "session-test", Service: runtime,
	}, func(_ string, data ...any) bool {
		events <- data[0].(Event)
		return false
	}, nil)
	t.Cleanup(bridge.Close)

	snapshot := bridge.Initialise()
	if snapshot.SessionID != "session-test" || snapshot.Model != cfg.Defaults.Model || snapshot.QueueMode != "queue" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	select {
	case event := <-events:
		if event.Kind != string(azemapp.EventBootstrapDone) || event.Sequence == 0 {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for projected event")
	}
}

func TestDesktopAttachmentRoundTrip(t *testing.T) {
	input := []Attachment{{ID: "image-1", Name: "screen.png", MIMEType: "image/png", Path: "/tmp/screen.png", Size: 42}}
	converted := attachmentsToSession(input)
	if len(converted) != 1 || converted[0].MIME != "image/png" {
		t.Fatalf("unexpected session attachment: %#v", converted)
	}
	if got := attachmentFromSession(converted[0]); got != input[0] {
		t.Fatalf("unexpected desktop attachment: %#v", got)
	}
}

func TestAllowedDesktopActions(t *testing.T) {
	if !allowedAction(azemapp.ActionResolveApproval) {
		t.Fatal("approval resolution must be available to the desktop")
	}
	if !allowedAction(azemapp.ActionListModels) {
		t.Fatal("model catalog must be available to the desktop")
	}
	if !allowedAction(azemapp.ActionSetQueueMode) {
		t.Fatal("queue mode must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionSetSessionPreferences) {
		t.Fatal("session preferences must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionSetChatGPTFastMode) {
		t.Fatal("ChatGPT fast mode must be configurable from the desktop")
	}
	if !allowedAction(azemapp.ActionCreateGitBranch) {
		t.Fatal("git branch creation must be available to the desktop")
	}
	if allowedAction(azemapp.ActionKind("arbitrary_shell")) {
		t.Fatal("unknown desktop actions must be rejected")
	}
}
