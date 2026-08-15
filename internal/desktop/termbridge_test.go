package desktop

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/desktop/termhost"
)

func TestEmbeddedTerminalIsDesktopOnly(t *testing.T) {
	for _, kind := range azemapp.AllActionKinds() {
		if strings.Contains(string(kind), "terminal") {
			t.Fatalf("runtime action catalog must not include terminal writes: %s", kind)
		}
	}
	for _, kind := range []azemapp.ActionKind{"write_terminal", "create_terminal", "list_terminals", "close_terminal"} {
		if allowedAction(kind) {
			t.Fatalf("desktop Execute allowlist must not expose %q", kind)
		}
	}
}

func TestBridgeCreateWriteResizeCloseTerminal(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	events := make(chan TerminalEvent, 64)
	bridge := newTerminalBridge(t, workspace, events)

	session, err := bridge.CreateTerminal(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if session.CWD != workspace || session.State != "running" {
		t.Fatalf("session = %+v", session)
	}
	listed := bridge.ListTerminals()
	if len(listed) != 1 || listed[0].ID != session.ID {
		t.Fatalf("list = %+v", listed)
	}

	if err := bridge.WriteTerminal(session.ID, "echo azem-term\n"); err != nil {
		t.Fatal(err)
	}
	waitTerminalOutput(t, events, "azem-term")

	if err := bridge.ResizeTerminal(session.ID, 120, 32); err != nil {
		t.Fatal(err)
	}
	if err := bridge.CloseTerminal(session.ID); err != nil {
		t.Fatal(err)
	}
	waitTerminalKind(t, events, termhost.EventExit)
	if got := bridge.ListTerminals(); len(got) != 0 {
		t.Fatalf("list after close = %+v", got)
	}
}

func TestBridgeCloseStopsTerminals(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	events := make(chan TerminalEvent, 32)
	bridge := newTerminalBridge(t, workspace, events)
	session, err := bridge.CreateTerminal(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	bridge.Close()
	waitTerminalKind(t, events, termhost.EventExit)
	if bridge.terminals.Alive(session.ID) {
		t.Fatal("session survived Bridge.Close")
	}
}

func TestBridgeWriteTerminalDoesNotUseShellC(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	events := make(chan TerminalEvent, 32)
	bridge := newTerminalBridge(t, workspace, events)
	session, err := bridge.CreateTerminal(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	// Bytes go to the PTY stdin. A quoted echo must print the literal text,
	// not be reinterpreted as a host-side sh -c string.
	if err := bridge.WriteTerminal(session.ID, "printf '%s\\n' 'not-a-host-command'\n"); err != nil {
		t.Fatal(err)
	}
	waitTerminalOutput(t, events, "not-a-host-command")
}

func newTerminalBridge(t *testing.T, workspace string, events chan TerminalEvent) *Bridge {
	t.Helper()
	cfg := config.Default()
	runtime := azemapp.NewService(context.Background(), cfg)
	bridge := NewBridge(context.Background(), azemapp.BootstrapResult{
		Config: cfg, SessionID: "session-term", Service: runtime,
		Paths: config.Paths{Workspace: workspace, StateDir: t.TempDir()},
	}, func(name string, data ...any) bool {
		if name != TerminalEventName || len(data) == 0 {
			return false
		}
		event, ok := data[0].(TerminalEvent)
		if !ok {
			return false
		}
		select {
		case events <- event:
		default:
		}
		return true
	}, nil)
	bridge.terminals = termhost.NewPlain(workspace, "/bin/sh", bridge.emitTerminal)
	t.Cleanup(bridge.Close)
	return bridge
}

func requireUnixPTY(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("embedded terminal is unix-only")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is required")
	}
}

func waitTerminalOutput(t *testing.T, events <-chan TerminalEvent, text string) {
	t.Helper()
	var builder strings.Builder
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind != termhost.EventOutput || event.Data == "" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(event.Data)
			if err != nil {
				t.Fatalf("decode output: %v", err)
			}
			if event.Encoding != "base64" {
				t.Fatalf("encoding = %q", event.Encoding)
			}
			builder.Write(decoded)
			if strings.Contains(builder.String(), text) {
				return
			}
		case <-deadline:
			t.Fatalf("missing output %q in %q", text, builder.String())
		}
	}
}

func waitTerminalKind(t *testing.T, events <-chan TerminalEvent, kind string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == kind {
				return
			}
		case <-deadline:
			t.Fatalf("missing event kind %q", kind)
		}
	}
}

func TestWorkspacePathIsProjectRoot(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "keep.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	events := make(chan TerminalEvent, 16)
	bridge := newTerminalBridge(t, workspace, events)
	session, err := bridge.CreateTerminal(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if session.CWD != workspace {
		t.Fatalf("cwd = %q, want %q", session.CWD, workspace)
	}
}
