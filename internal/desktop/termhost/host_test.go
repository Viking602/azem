package termhost

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreateWriteResizeClose(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "azem-term-marker")
	events := newEventLog()
	host := NewPlain(workspace, "/bin/sh", events.push)
	t.Cleanup(host.CloseAll)

	session, err := host.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if session.CWD != workspace || session.State != "running" || session.Shell != "sh" {
		t.Fatalf("session = %+v", session)
	}
	if !host.Alive(session.ID) {
		t.Fatal("created session is not alive")
	}

	command := "echo azem-term; pwd; printf 'ready\\n' > '" + marker + "'\n"
	if err := host.Write(session.ID, []byte(command)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return events.containsOutput("azem-term") && fileExists(marker)
	})
	if !events.containsOutput(workspace) && !events.containsOutput(resolved(t, workspace)) {
		t.Fatalf("cwd output missing workspace %q: %q", workspace, events.outputText())
	}

	if err := host.Resize(session.ID, 100, 30); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		for _, event := range events.snapshot() {
			if event.Kind == EventSession && event.Session.ID == session.ID && event.Session.Cols == 100 && event.Session.Rows == 30 {
				return true
			}
		}
		return false
	})

	if err := host.Close(session.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		return !host.Alive(session.ID) && events.hasKind(EventExit)
	})
}

func TestSecondSessionIsIndependent(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	events := newEventLog()
	host := NewPlain(workspace, "/bin/sh", events.push)
	t.Cleanup(host.CloseAll)

	first, err := host.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	second, err := host.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("sessions share an id")
	}
	if err := host.Write(first.ID, []byte("echo first-session-only\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return events.sessionOutput(first.ID, "first-session-only")
	})
	if events.sessionOutput(second.ID, "first-session-only") {
		t.Fatal("second session received the first session write")
	}
}

func TestCloseAllReapsSessions(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	events := newEventLog()
	host := NewPlain(workspace, "/bin/sh", events.push)
	session, err := host.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	host.CloseAll()
	waitFor(t, 3*time.Second, func() bool {
		return !host.Alive(session.ID) && events.hasKind(EventExit)
	})
	if _, err := host.Create(80, 24); err != ErrClosed {
		t.Fatalf("create after CloseAll = %v", err)
	}
}

func TestCreateUsesWorkspaceCWD(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	events := newEventLog()
	host := NewPlain(workspace, "/bin/sh", events.push)
	t.Cleanup(host.CloseAll)
	session, err := host.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if session.CWD != workspace {
		t.Fatalf("cwd = %q, want %q", session.CWD, workspace)
	}
}

func TestWriteRejectsOversizedPayload(t *testing.T) {
	requireUnixPTY(t)
	host := NewPlain(t.TempDir(), "/bin/sh", nil)
	t.Cleanup(host.CloseAll)
	session, err := host.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Write(session.ID, bytes.Repeat([]byte("a"), maxWriteBytes+1)); err != ErrWriteTooLarge {
		t.Fatalf("oversize write = %v", err)
	}
}

func TestShutdownReturnsWithinBudgetWhenSessionNeverConverges(t *testing.T) {
	restoreCloseBudgets(t, 30*time.Millisecond)
	// A session whose readLoop never exits: done stays open forever, which
	// models a child stuck in an uninterruptible state after SIGKILL.
	session := &ptySession{done: make(chan struct{}), writeGate: make(chan struct{}, 1)}

	second := make(chan bool, 1)
	go func() { second <- session.shutdown() }()

	start := time.Now()
	if session.shutdown() {
		t.Fatal("shutdown reported convergence without a closed done channel")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shutdown blocked for %s; budgets must bound it", elapsed)
	}
	select {
	case converged := <-second:
		if converged {
			t.Fatal("concurrent shutdown reported convergence")
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent shutdown never returned")
	}
}

func TestCloseForgetsSessionThatNeverConverges(t *testing.T) {
	restoreCloseBudgets(t, 50*time.Millisecond)
	host := NewPlain(t.TempDir(), "/bin/sh", nil)
	// Inject a session whose done channel never closes, mirroring a child
	// wedged in an uninterruptible state where even SIGKILL cannot reap it.
	stuck := &ptySession{done: make(chan struct{}), writeGate: make(chan struct{}, 1)}
	host.mu.Lock()
	host.sessions["stuck"] = stuck
	host.mu.Unlock()

	start := time.Now()
	if err := host.Close("stuck"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close blocked for %s", elapsed)
	}
	if host.Alive("stuck") {
		t.Fatal("non-converged session must leave the roster after Close")
	}
}

func TestWriteTimesOutInsteadOfHangingWhenShellStopsReading(t *testing.T) {
	requireUnixPTY(t)
	restoreWriteBudget(t, 150*time.Millisecond)
	events := newEventLog()
	host := NewPlain(t.TempDir(), "/bin/sh", events.push)
	t.Cleanup(host.CloseAll)
	session, err := host.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := host.lookup(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	pid := tracked.cmd.Process.Pid
	t.Cleanup(func() { continueProcessGroup(pid) })

	// Canonical mode discards overlong lines instead of blocking, so switch
	// the slave to raw mode first, then stop the shell. Once nothing reads
	// the raw input queue, master writes block when the kernel buffer fills.
	if err := host.Write(session.ID, []byte("stty raw -echo; printf ready-marker; kill -STOP $$\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return events.containsOutput("ready-marker") })

	// The stopped shell never drains the PTY, so bounded writes must fail
	// with ErrWriteTimeout once the kernel buffer fills instead of hanging
	// the Bridge goroutine forever.
	chunk := bytes.Repeat([]byte("a"), 8<<10)
	var failure error
	for attempt := 0; attempt < 64; attempt++ {
		if err := host.Write(session.ID, chunk); err != nil {
			failure = err
			break
		}
	}
	if !errors.Is(failure, ErrWriteTimeout) {
		t.Fatalf("expected ErrWriteTimeout, got %v", failure)
	}
	// While the stuck bytes drain, later writes fail fast instead of piling
	// up blocked goroutines. Platforms with pollable PTYs release the gate
	// on deadline, so a repeated timeout is also acceptable.
	if err := host.Write(session.ID, []byte("x")); !errors.Is(err, ErrWriteBusy) && !errors.Is(err, ErrWriteTimeout) {
		t.Fatalf("expected fast-fail busy/timeout, got %v", err)
	}
	continueProcessGroup(pid)
}

func TestCloseAllRacingCreateLeavesNoRunningSession(t *testing.T) {
	requireUnixPTY(t)
	workspace := t.TempDir()
	for iteration := 0; iteration < 10; iteration++ {
		host := NewPlain(workspace, "/bin/sh", nil)
		type outcome struct {
			session Session
			err     error
		}
		created := make(chan outcome, 1)
		go func() {
			session, err := host.Create(80, 24)
			created <- outcome{session: session, err: err}
		}()
		host.CloseAll()
		result := <-created
		if result.err != nil {
			if result.err != ErrClosed {
				t.Fatalf("create during close-all = %v", result.err)
			}
			continue
		}
		// Create won the insert race before CloseAll copied the roster, or
		// lost it entirely; either way nothing may keep running.
		waitFor(t, 3*time.Second, func() bool { return !host.Alive(result.session.ID) })
	}
}

func TestResizeToSameSizeDoesNotEmitSession(t *testing.T) {
	requireUnixPTY(t)
	events := newEventLog()
	host := NewPlain(t.TempDir(), "/bin/sh", events.push)
	t.Cleanup(host.CloseAll)
	session, err := host.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	before := countSessionEvents(events, session.ID)
	if err := host.Resize(session.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	if got := countSessionEvents(events, session.ID); got != before {
		t.Fatalf("unchanged resize emitted %d extra session events", got-before)
	}
}

func countSessionEvents(events *eventLog, id string) int {
	count := 0
	for _, event := range events.snapshot() {
		if event.Kind == EventSession && event.Session.ID == id {
			count++
		}
	}
	return count
}

func restoreCloseBudgets(t *testing.T, budget time.Duration) {
	t.Helper()
	prevClose, prevReap := closeWait, reapWait
	closeWait, reapWait = budget, budget
	t.Cleanup(func() { closeWait, reapWait = prevClose, prevReap })
}

func restoreWriteBudget(t *testing.T, budget time.Duration) {
	t.Helper()
	prev := writeWait
	writeWait = budget
	t.Cleanup(func() { writeWait = prev })
}

func TestWindowsIsUnsupported(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only contract")
	}
	if _, err := New(t.TempDir(), nil).Create(80, 24); err != ErrUnsupportedOS {
		t.Fatalf("windows create = %v", err)
	}
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

type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func newEventLog() *eventLog { return &eventLog{} }

func (l *eventLog) push(event Event) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *eventLog) snapshot() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event(nil), l.events...)
}

func (l *eventLog) outputText() string {
	var builder strings.Builder
	for _, event := range l.snapshot() {
		if event.Kind == EventOutput {
			builder.Write(event.Data)
		}
	}
	return builder.String()
}

func (l *eventLog) containsOutput(text string) bool {
	return strings.Contains(l.outputText(), text)
}

func (l *eventLog) sessionOutput(id, text string) bool {
	var builder strings.Builder
	for _, event := range l.snapshot() {
		if event.Kind == EventOutput && event.Session.ID == id {
			builder.Write(event.Data)
		}
	}
	return strings.Contains(builder.String(), text)
}

func (l *eventLog) hasKind(kind string) bool {
	for _, event := range l.snapshot() {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s", timeout)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return absolute
}
