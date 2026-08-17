// Package termhost owns interactive user PTY sessions for the desktop window.
// It is not the agent shell tool: the model never writes here.
package termhost

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
)

const (
	EventSession = "terminal_session"
	EventOutput  = "terminal_output"
	EventExit    = "terminal_exit"

	maxSessions     = 8
	maxWriteBytes   = 64 << 10
	maxOutputChunk  = 32 << 10
	outputFlushWait = 16 * time.Millisecond
	defaultCols     = 80
	defaultRows     = 24
	minCols         = 20
	maxCols         = 400
	minRows         = 5
	maxRows         = 200
)

// Close and write budgets are variables so tests can shorten them. Every
// wait on a session converging or on the PTY accepting bytes must be
// bounded: an uninterruptible child must never hang the Bridge or the
// window shutdown path.
var (
	closeWait = 2 * time.Second
	reapWait  = 2 * time.Second
	writeWait = 2 * time.Second
)

var (
	ErrClosed          = errors.New("terminal host is closed")
	ErrUnsupportedOS   = errors.New("embedded terminal is not supported on windows")
	ErrTooManySessions = errors.New("too many terminal sessions")
	ErrUnknownSession  = errors.New("unknown terminal session")
	ErrSessionClosed   = errors.New("terminal session is closed")
	ErrWriteTooLarge   = errors.New("terminal write exceeds 64KiB")
	ErrWriteTimeout    = errors.New("terminal is not accepting input")
	ErrWriteBusy       = errors.New("terminal write is still draining")
)

// Session is the secret-free snapshot projected to the desktop renderer.
type Session struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	CWD    string    `json:"cwd"`
	Shell  string    `json:"shell"`
	Cols   int       `json:"cols"`
	Rows   int       `json:"rows"`
	State  string    `json:"state"`
	BornAt time.Time `json:"-"`
}

// Event is a bounded host notification. Output bytes are raw PTY data;
// the Bridge base64-encodes them. Events never include environment maps,
// PTY master paths, or process credentials.
type Event struct {
	Kind     string
	Session  Session
	Data     []byte
	ExitCode *int
}

// Host manages PTY sessions for one desktop window's project workspace.
type Host struct {
	workspace string
	shell     string
	login     bool
	emit      func(Event)

	mu       sync.Mutex
	sessions map[string]*ptySession
	starting int
	serial   atomic.Uint64
	closed   atomic.Bool
}

type ptySession struct {
	info   Session
	cmd    *exec.Cmd
	file   *os.File
	infoMu sync.Mutex

	pending   bytes.Buffer
	pendingMu sync.Mutex
	flush     *time.Timer
	emitMu    sync.Mutex

	writeGate chan struct{}

	closed atomic.Bool
	killed atomic.Bool
	done   chan struct{}
}

type hostConfig struct {
	workspace string
	shell     string
	login     bool
}

// New starts a host whose sessions spawn in workspace.
func New(workspace string, emit func(Event)) *Host {
	return newHost(hostConfig{workspace: workspace, login: true}, emit)
}

// NewPlain starts a host that execs a non-login shell. Tests use this so
// user profiles cannot hang the PTY.
func NewPlain(workspace, shell string, emit func(Event)) *Host {
	return newHost(hostConfig{workspace: workspace, shell: shell, login: false}, emit)
}

func newHost(cfg hostConfig, emit func(Event)) *Host {
	if emit == nil {
		emit = func(Event) {}
	}
	return &Host{
		workspace: cfg.workspace,
		shell:     cfg.shell,
		login:     cfg.login,
		emit:      emit,
		sessions:  make(map[string]*ptySession),
	}
}

// Create starts a login or plain user shell in the window workspace.
func (h *Host) Create(cols, rows int) (Session, error) {
	if h.closed.Load() {
		return Session{}, ErrClosed
	}
	if runtime.GOOS == "windows" {
		return Session{}, ErrUnsupportedOS
	}
	workspace, err := resolveWorkspace(h.workspace)
	if err != nil {
		return Session{}, err
	}
	cols, rows = clampSize(cols, rows)
	shell, err := resolveShell(h.shell)
	if err != nil {
		return Session{}, err
	}

	// Reserve a slot under the lock, but fork/exec the PTY outside it so a
	// slow spawn never stalls Write/Resize/Close/List on other sessions.
	h.mu.Lock()
	if h.closed.Load() {
		h.mu.Unlock()
		return Session{}, ErrClosed
	}
	if len(h.sessions)+h.starting >= maxSessions {
		h.mu.Unlock()
		return Session{}, ErrTooManySessions
	}
	h.starting++
	h.mu.Unlock()

	index := h.serial.Add(1)
	id := uuid.NewString()
	title := shellTitle(shell, index)
	info := Session{
		ID: id, Title: title, CWD: workspace, Shell: filepath.Base(shell),
		Cols: cols, Rows: rows, State: "running", BornAt: time.Now().UTC(),
	}
	args := []string{shell}
	if h.login {
		args = append(args, "-l")
	}
	command := exec.Command(args[0], args[1:]...)
	command.Dir = workspace
	command.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	applyProcessGroup(command)
	file, err := pty.StartWithSize(command, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		h.mu.Lock()
		h.starting--
		h.mu.Unlock()
		return Session{}, fmt.Errorf("start terminal: %w", err)
	}
	session := &ptySession{
		info: info, cmd: command, file: file,
		done: make(chan struct{}), writeGate: make(chan struct{}, 1),
	}

	h.mu.Lock()
	h.starting--
	if h.closed.Load() {
		// CloseAll won the race while the shell was starting. The new
		// session is not in its kill list, so tear it down here.
		h.mu.Unlock()
		session.abandon()
		return Session{}, ErrClosed
	}
	h.sessions[id] = session
	h.mu.Unlock()

	h.emit(Event{Kind: EventSession, Session: info})
	go session.readLoop(func(chunk []byte) {
		h.emit(Event{Kind: EventOutput, Session: session.snapshot(), Data: chunk})
	}, func(code int) {
		next := session.snapshot()
		next.State = "exited"
		session.setInfo(next)
		h.forget(id)
		h.emit(Event{Kind: EventExit, Session: next, ExitCode: &code})
	})
	return info, nil
}

// List returns sessions in creation order.
func (h *Host) List() []Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Session, 0, len(h.sessions))
	for _, session := range h.sessions {
		out = append(out, session.snapshot())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BornAt.Equal(out[j].BornAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].BornAt.Before(out[j].BornAt)
	})
	return out
}

// Write sends raw keystroke or paste bytes to the PTY. It never runs sh -c.
// The write is bounded: when the child stops reading and the kernel buffer
// fills, the call fails with ErrWriteTimeout instead of hanging the Bridge,
// and later writes fail fast with ErrWriteBusy until the stuck bytes drain.
func (h *Host) Write(id string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > maxWriteBytes {
		return ErrWriteTooLarge
	}
	session, err := h.lookup(id)
	if err != nil {
		return err
	}
	return session.write(data)
}

// Resize updates the PTY winsize for the renderer grid.
func (h *Host) Resize(id string, cols, rows int) error {
	session, err := h.lookup(id)
	if err != nil {
		return err
	}
	if session.closed.Load() {
		return ErrSessionClosed
	}
	cols, rows = clampSize(cols, rows)
	if current := session.snapshot(); current.Cols == cols && current.Rows == rows {
		return nil
	}
	if err := pty.Setsize(session.file, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		return err
	}
	next := session.snapshot()
	next.Cols = cols
	next.Rows = rows
	session.setInfo(next)
	h.emit(Event{Kind: EventSession, Session: next})
	return nil
}

// Close kills one session and reaps its process within a bounded budget.
func (h *Host) Close(id string) error {
	session, err := h.lookup(id)
	if err != nil {
		return err
	}
	if !session.shutdown() {
		// The child is stuck in an uninterruptible state. Drop it from the
		// roster so the renderer and the exit path never wait on it again;
		// the OS reclaims the process when it finally unwedges.
		h.forget(id)
		log.Printf("termhost: session %s did not converge within the close budget", id)
	}
	return nil
}

// CloseAll stops every session in parallel under one overall budget. The
// window process calls this on exit; it must return even when a child is
// stuck, because Bridge.Close runs before the runtime shutdown path.
func (h *Host) CloseAll() {
	h.closed.Store(true)
	h.mu.Lock()
	sessions := make(map[string]*ptySession, len(h.sessions))
	for id, session := range h.sessions {
		sessions[id] = session
	}
	h.mu.Unlock()
	if len(sessions) == 0 {
		return
	}
	var wg sync.WaitGroup
	for id, session := range sessions {
		wg.Add(1)
		go func(id string, session *ptySession) {
			defer wg.Done()
			if !session.shutdown() {
				h.forget(id)
				log.Printf("termhost: session %s did not converge during close-all", id)
			}
		}(id, session)
	}
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	budget := time.NewTimer(closeWait + time.Second)
	defer budget.Stop()
	select {
	case <-finished:
	case <-budget.C:
		log.Printf("termhost: close-all exceeded its budget; leaving stuck sessions to the OS")
	}
}

// Alive reports whether id is still tracked.
func (h *Host) Alive(id string) bool {
	_, err := h.lookup(id)
	return err == nil
}

func (h *Host) lookup(id string) (*ptySession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	session, ok := h.sessions[id]
	if !ok {
		return nil, ErrUnknownSession
	}
	return session, nil
}

func (h *Host) forget(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
}

func (s *ptySession) snapshot() Session {
	s.infoMu.Lock()
	defer s.infoMu.Unlock()
	return s.info
}

func (s *ptySession) setInfo(info Session) {
	s.infoMu.Lock()
	s.info = info
	s.infoMu.Unlock()
}

func (s *ptySession) readLoop(emit func([]byte), onExit func(int)) {
	defer close(s.done)
	buf := make([]byte, 8192)
	for {
		n, err := s.file.Read(buf)
		if n > 0 {
			s.pushOutput(buf[:n], emit)
		}
		if err != nil {
			s.flushOutput(emit)
			onExit(s.reap())
			return
		}
	}
}

// write delivers bytes to the PTY without unbounded blocking. One in-flight
// write holds the gate; while it is stuck, later writes return ErrWriteBusy
// immediately instead of piling up blocked Bridge goroutines.
func (s *ptySession) write(data []byte) error {
	if s.closed.Load() {
		return ErrSessionClosed
	}
	select {
	case s.writeGate <- struct{}{}:
	default:
		return ErrWriteBusy
	}
	if s.file.SetWriteDeadline(time.Now().Add(writeWait)) == nil {
		defer func() {
			_ = s.file.SetWriteDeadline(time.Time{})
			<-s.writeGate
		}()
		_, err := s.file.Write(data)
		if err != nil && errors.Is(err, os.ErrDeadlineExceeded) {
			return ErrWriteTimeout
		}
		return err
	}
	// The PTY master is not pollable on this platform (macOS kqueue), so
	// bound the write with a monitored goroutine. A timed-out write keeps
	// the gate until the kernel accepts the bytes or the session dies.
	result := make(chan error, 1)
	go func() {
		_, err := s.file.Write(data)
		result <- err
		<-s.writeGate
	}()
	timer := time.NewTimer(writeWait)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		return ErrWriteTimeout
	}
}

func (s *ptySession) pushOutput(chunk []byte, emit func([]byte)) {
	s.pendingMu.Lock()
	_, _ = s.pending.Write(chunk)
	full := s.pending.Len() >= maxOutputChunk
	if !full && s.flush == nil {
		s.flush = time.AfterFunc(outputFlushWait, func() { s.flushOutput(emit) })
	}
	s.pendingMu.Unlock()
	if full {
		s.flushOutput(emit)
	}
}

// flushOutput serializes take-and-emit on emitMu so chunks stay ordered,
// while pendingMu is never held across emit and readLoop appends never
// wait behind a slow event sink.
func (s *ptySession) flushOutput(emit func([]byte)) {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.pendingMu.Lock()
	if s.flush != nil {
		s.flush.Stop()
		s.flush = nil
	}
	var chunk []byte
	if s.pending.Len() > 0 {
		chunk = append([]byte(nil), s.pending.Bytes()...)
		s.pending.Reset()
	}
	s.pendingMu.Unlock()
	if len(chunk) > 0 {
		emit(chunk)
	}
}

// reap waits for the child within a bounded budget. cmd.Wait can stall
// forever on an uninterruptible child, and the done channel must still
// close so shutdown never hangs the Bridge.
func (s *ptySession) reap() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return -1
	}
	codes := make(chan int, 1)
	go func() {
		err := s.cmd.Wait()
		if err == nil {
			codes <- 0
			return
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			codes <- exitErr.ExitCode()
			return
		}
		codes <- -1
	}()
	timer := time.NewTimer(reapWait)
	defer timer.Stop()
	select {
	case code := <-codes:
		return code
	case <-timer.C:
		return -1
	}
}

// shutdown reports whether the session converged (readLoop exited and the
// child was reaped) within the close budget. Every wait is bounded: a stuck
// child must never hang CloseTerminal or the window exit path.
func (s *ptySession) shutdown() bool {
	if !s.closed.CompareAndSwap(false, true) {
		return s.awaitDone(closeWait + reapWait)
	}
	s.killed.Store(true)
	if s.cmd != nil && s.cmd.Process != nil {
		killProcessGroup(s.cmd.Process.Pid)
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	if s.awaitDone(closeWait) {
		return true
	}
	if s.cmd != nil && s.cmd.Process != nil {
		killProcessGroupHard(s.cmd.Process.Pid)
		_ = s.cmd.Process.Kill()
	}
	return s.awaitDone(closeWait + reapWait)
}

func (s *ptySession) awaitDone(budget time.Duration) bool {
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-s.done:
		return true
	case <-timer.C:
		return false
	}
}

// abandon tears down a session whose readLoop never started (Create lost
// the race with CloseAll). It kills the child, closes the master, reaps in
// the background, and closes done so no waiter can hang on it.
func (s *ptySession) abandon() {
	s.closed.Store(true)
	s.killed.Store(true)
	if s.cmd != nil && s.cmd.Process != nil {
		killProcessGroup(s.cmd.Process.Pid)
		killProcessGroupHard(s.cmd.Process.Pid)
		_ = s.cmd.Process.Kill()
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	go s.reap()
	close(s.done)
}

func resolveWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("workspace is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect workspace: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory")
	}
	return absolute, nil
}

func resolveShell(override string) (string, error) {
	candidates := []string{strings.TrimSpace(override), strings.TrimSpace(os.Getenv("SHELL"))}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/bin/zsh")
	}
	candidates = append(candidates, "/bin/sh")
	for _, shell := range candidates {
		if shell == "" || strings.ContainsRune(shell, 0) || !filepath.IsAbs(shell) {
			continue
		}
		info, err := os.Stat(shell)
		if err != nil || info.IsDir() {
			continue
		}
		return shell, nil
	}
	return "", fmt.Errorf("no usable shell")
}

func shellTitle(shell string, index uint64) string {
	base := filepath.Base(shell)
	if index <= 1 {
		return base
	}
	return fmt.Sprintf("%s · %d", base, index)
}

func clampSize(cols, rows int) (int, int) {
	if cols < minCols {
		cols = defaultCols
	}
	if cols > maxCols {
		cols = maxCols
	}
	if rows < minRows {
		rows = defaultRows
	}
	if rows > maxRows {
		rows = maxRows
	}
	return cols, rows
}
