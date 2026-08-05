package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestImportClipboardImage(t *testing.T) {
	previous := readClipboardImage
	readClipboardImage = func() ([]byte, string, error) {
		return []byte{
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
			0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		}, "image/png", nil
	}
	t.Cleanup(func() { readClipboardImage = previous })

	runtime := azemapp.NewService(context.Background(), config.Default())
	runtime.AttachAttachments(filepath.Join(t.TempDir(), "attachments"))
	bridge := &Bridge{runtime: runtime}

	attachment, err := bridge.ImportClipboardImage("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if attachment == nil || attachment.MIMEType != "image/png" || !strings.HasPrefix(attachment.Name, "pasted-image-") {
		t.Fatalf("unexpected clipboard attachment: %#v", attachment)
	}
	if _, err := os.Stat(attachment.Path); err != nil {
		t.Fatalf("clipboard attachment was not stored: %v", err)
	}
}

func TestImportClipboardImageReturnsNilWhenClipboardHasNoImage(t *testing.T) {
	previous := readClipboardImage
	readClipboardImage = func() ([]byte, string, error) { return nil, "", nil }
	t.Cleanup(func() { readClipboardImage = previous })

	attachment, err := (&Bridge{}).ImportClipboardImage("session-1")
	if err != nil || attachment != nil {
		t.Fatalf("attachment = %#v, err = %v", attachment, err)
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

func TestPullRequestMonitorStateIsScopedToWorkspace(t *testing.T) {
	stateDir := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	pathA := pullRequestMonitorStatePath(stateDir, workspaceA)
	pathB := pullRequestMonitorStatePath(stateDir, workspaceB)
	if pathA == pathB {
		t.Fatalf("workspace monitor paths collided: %q", pathA)
	}
	if filepath.Dir(pathA) != stateDir || filepath.Dir(pathB) != stateDir {
		t.Fatalf("monitor paths escaped state directory: %q %q", pathA, pathB)
	}
	if filepath.Base(pathA) == "pr-monitors.json" || filepath.Base(pathB) == "pr-monitors.json" {
		t.Fatalf("monitor path is not workspace-scoped: %q %q", pathA, pathB)
	}
}
