// Package background supervises long-running workspace applications without
// coupling their output rate to the main application event loop.
package background

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxLogBytes  = int64(8 << 20)
	defaultMaxLogLines  = 2000
	defaultConcurrency  = 4
	defaultPollInterval = time.Second
)

// StartRequest describes one supervised application. Command is passed to the
// platform shell so users can use the same syntax as foreground shell actions.
type StartRequest struct {
	Name    string
	Command string
	CWD     string
}

// Process is the low-cost status projection used by the TUI.
type Process struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Command    string    `json:"command"`
	CWD        string    `json:"cwd"`
	PID        int       `json:"pid"`
	State      string    `json:"state"`
	ExitCode   int       `json:"exitCode"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	LogPath    string    `json:"logPath"`
	LogBytes   int64     `json:"logBytes"`
	Generation uint64    `json:"generation"`
	Error      string    `json:"error,omitempty"`
}

// LogSnapshot is a bounded tail. Offset counts retained lines, not file bytes.
type LogSnapshot struct {
	Process    Process
	Lines      []string
	Offset     int
	TotalLines int
	Truncated  bool
}

type Options struct {
	Root         string
	LogDir       string
	MaxLogBytes  int64
	MaxLogLines  int
	MaxProcesses int
	PollInterval time.Duration
}

type Manager struct {
	root         string
	logDir       string
	maxLogBytes  int64
	maxLogLines  int
	pollInterval time.Duration
	sem          chan struct{}

	mu        sync.RWMutex
	entries   map[string]*entry
	closed    bool
	stop      chan struct{}
	stopOnce  sync.Once
	monitorWG sync.WaitGroup
	wg        sync.WaitGroup
}

type entry struct {
	mu        sync.RWMutex
	process   Process
	command   *exec.Cmd
	cancel    context.CancelFunc
	log       *logWriter
	recovered bool
}

func NewManager(options Options) (*Manager, error) {
	root, err := filepath.Abs(strings.TrimSpace(options.Root))
	if err != nil || strings.TrimSpace(options.Root) == "" {
		return nil, fmt.Errorf("resolve background workspace: %w", firstError(err, errors.New("workspace is empty")))
	}
	logDir, err := filepath.Abs(strings.TrimSpace(options.LogDir))
	if err != nil || strings.TrimSpace(options.LogDir) == "" {
		return nil, fmt.Errorf("resolve background log directory: %w", firstError(err, errors.New("log directory is empty")))
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("create background log directory: %w", err)
	}
	if options.MaxLogBytes <= 0 {
		options.MaxLogBytes = defaultMaxLogBytes
	}
	if options.MaxLogLines <= 0 {
		options.MaxLogLines = defaultMaxLogLines
	}
	if options.MaxProcesses <= 0 {
		options.MaxProcesses = defaultConcurrency
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	manager := &Manager{
		root: root, logDir: logDir, maxLogBytes: options.MaxLogBytes, maxLogLines: options.MaxLogLines,
		pollInterval: options.PollInterval, sem: make(chan struct{}, options.MaxProcesses), entries: make(map[string]*entry), stop: make(chan struct{}),
	}
	manager.recover()
	manager.monitorWG.Add(1)
	go manager.monitor()
	return manager, nil
}

func (m *Manager) Start(ctx context.Context, request StartRequest) (Process, error) {
	request.Command = strings.TrimSpace(request.Command)
	if request.Command == "" {
		return Process{}, errors.New("background command is empty")
	}
	cwd, err := m.resolveCWD(request.CWD)
	if err != nil {
		return Process{}, err
	}
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return Process{}, errors.New("background manager is closed")
	}
	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		return Process{}, ctx.Err()
	}
	release := true
	defer func() {
		if release {
			<-m.sem
		}
	}()

	id, err := newID()
	if err != nil {
		return Process{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = commandName(request.Command)
	}
	logPath := filepath.Join(m.logDir, id+".log")
	writer, err := newLogWriter(logPath, m.maxLogBytes, m.maxLogLines)
	if err != nil {
		return Process{}, err
	}
	processCtx, cancel := context.WithCancel(context.Background())
	command := shellCommand(processCtx, request.Command)
	command.Dir = cwd
	configureProcess(command)
	command.Stdout, command.Stderr = writer.Output(), writer.Output()
	if err := command.Start(); err != nil {
		cancel()
		_ = writer.Close()
		return Process{}, fmt.Errorf("start background process: %w", err)
	}
	item := Process{
		ID: id, Name: name, Command: request.Command, CWD: cwd, PID: command.Process.Pid,
		State: "running", ExitCode: -1, StartedAt: time.Now().UTC(), LogPath: logPath, Generation: 1,
	}
	tracked := &entry{process: item, command: command, cancel: cancel, log: writer}
	if err := m.persist(item); err != nil {
		cancel()
		_ = terminatePID(command.Process.Pid)
		_ = command.Wait()
		_ = writer.Close()
		return Process{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		_ = terminatePID(command.Process.Pid)
		_ = command.Wait()
		_ = writer.Close()
		return Process{}, errors.New("background manager is closed")
	}
	m.entries[id] = tracked
	m.mu.Unlock()

	release = false
	m.wg.Add(1)
	go m.wait(tracked)
	return item, nil
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	tracked, ok := m.get(id)
	if !ok {
		return fmt.Errorf("background process %q was not found", id)
	}
	tracked.mu.RLock()
	state, pid, cancel := tracked.process.State, tracked.process.PID, tracked.cancel
	tracked.mu.RUnlock()
	if state != "running" && state != "stopping" {
		return nil
	}
	tracked.mu.Lock()
	if tracked.process.State == "running" {
		tracked.process.State = "stopping"
		tracked.process.Generation++
	}
	item := tracked.process
	tracked.mu.Unlock()
	_ = m.persist(item)
	if cancel != nil {
		cancel()
	}
	if err := terminatePID(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	for {
		m.refreshRecovered(tracked)
		tracked.mu.RLock()
		finished := tracked.process.State != "running" && tracked.process.State != "stopping"
		tracked.mu.RUnlock()
		if finished {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (m *Manager) List() []Process {
	m.mu.RLock()
	entries := make([]*entry, 0, len(m.entries))
	for _, tracked := range m.entries {
		entries = append(entries, tracked)
	}
	m.mu.RUnlock()
	items := make([]Process, 0, len(entries))
	for _, tracked := range entries {
		m.refreshRecovered(tracked)
		tracked.mu.RLock()
		item := tracked.process
		item.LogBytes = tracked.log.Size()
		tracked.mu.RUnlock()
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.After(items[j].StartedAt) })
	return items
}

func (m *Manager) Logs(id string, offset, limit int) (LogSnapshot, error) {
	tracked, ok := m.get(id)
	if !ok {
		return LogSnapshot{}, fmt.Errorf("background process %q was not found", id)
	}
	m.refreshRecovered(tracked)
	tracked.mu.RLock()
	item := tracked.process
	tracked.mu.RUnlock()
	lines, total, truncated := tracked.log.Snapshot()
	item.LogBytes = tracked.log.Total()
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = max(0, len(lines)-limit)
	}
	offset = min(max(0, offset), len(lines))
	end := min(len(lines), offset+limit)
	return LogSnapshot{Process: item, Lines: append([]string(nil), lines[offset:end]...), Offset: offset, TotalLines: total, Truncated: truncated}, nil
}

// Close releases the supervisor but intentionally does not stop child
// applications. The child process groups remain alive and keep writing to
// their inherited log descriptors, which is the persistence contract.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	entries := make([]*entry, 0, len(m.entries))
	for _, tracked := range m.entries {
		entries = append(entries, tracked)
	}
	m.mu.Unlock()
	m.stopOnce.Do(func() { close(m.stop) })
	m.monitorWG.Wait()
	for _, tracked := range entries {
		tracked.mu.RLock()
		recovered := tracked.recovered
		tracked.mu.RUnlock()
		if recovered {
			_ = tracked.log.Close()
		}
	}
	return nil
}

func (m *Manager) monitor() {
	defer m.monitorWG.Done()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.mu.RLock()
			entries := make([]*entry, 0, len(m.entries))
			for _, tracked := range m.entries {
				entries = append(entries, tracked)
			}
			m.mu.RUnlock()
			for _, tracked := range entries {
				_ = tracked.log.Maintain()
				m.refreshRecovered(tracked)
			}
		}
	}
}

func (m *Manager) wait(tracked *entry) {
	defer m.wg.Done()
	defer func() { <-m.sem }()
	err := tracked.command.Wait()
	_ = tracked.log.Close()
	exitCode, state, message := 0, "exited", ""
	if err != nil {
		exitCode, state, message = -1, "failed", err.Error()
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
		tracked.mu.RLock()
		if tracked.process.State == "stopping" {
			state, message = "stopped", ""
		}
		tracked.mu.RUnlock()
	}
	tracked.mu.Lock()
	tracked.process.State = state
	tracked.process.ExitCode = exitCode
	tracked.process.Error = message
	tracked.process.FinishedAt = time.Now().UTC()
	tracked.process.LogBytes = tracked.log.Size()
	tracked.process.Generation++
	item := tracked.process
	tracked.mu.Unlock()
	_ = m.persist(item)
}

func (m *Manager) recover() {
	files, err := filepath.Glob(filepath.Join(m.logDir, "bg_*.json"))
	if err != nil {
		return
	}
	for _, path := range files {
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var item Process
		if json.Unmarshal(payload, &item) != nil || item.ID == "" || item.LogPath == "" {
			continue
		}
		writer, openErr := newLogWriter(item.LogPath, m.maxLogBytes, m.maxLogLines)
		if openErr != nil {
			continue
		}
		tracked := &entry{process: item, log: writer, recovered: true}
		m.refreshRecovered(tracked)
		m.entries[item.ID] = tracked
	}
}

func (m *Manager) refreshRecovered(tracked *entry) {
	tracked.mu.Lock()
	defer tracked.mu.Unlock()
	if !tracked.recovered || (tracked.process.State != "running" && tracked.process.State != "stopping") {
		return
	}
	if processExists(tracked.process.PID) {
		tracked.process.LogBytes = tracked.log.Size()
		return
	}
	tracked.process.State = "exited"
	tracked.process.ExitCode = -1
	tracked.process.FinishedAt = time.Now().UTC()
	tracked.process.Generation++
	_ = m.persist(tracked.process)
}

func (m *Manager) persist(item Process) error {
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode background metadata: %w", err)
	}
	temporary, err := os.CreateTemp(m.logDir, ".background-*.json")
	if err != nil {
		return fmt.Errorf("create background metadata: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(payload)
	}
	closeErr := temporary.Close()
	if err != nil {
		return fmt.Errorf("write background metadata: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close background metadata: %w", closeErr)
	}
	if err := os.Rename(temporaryName, filepath.Join(m.logDir, item.ID+".json")); err != nil {
		return fmt.Errorf("replace background metadata: %w", err)
	}
	return nil
}

func (m *Manager) get(id string) (*entry, bool) {
	m.mu.RLock()
	tracked, ok := m.entries[id]
	m.mu.RUnlock()
	return tracked, ok
}

func (m *Manager) resolveCWD(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return m.root, nil
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(m.root, value)
	}
	value, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve background cwd: %w", err)
	}
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("background cwd is not a directory: %s", value)
	}
	return value, nil
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}

func newID() (string, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create background id: %w", err)
	}
	return "bg_" + hex.EncodeToString(value[:]), nil
}

func commandName(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "background"
	}
	return filepath.Base(fields[0])
}

func firstError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

type logWriter struct {
	mu         sync.RWMutex
	file       *os.File
	lines      []string
	partial    string
	maxBytes   int64
	maxLines   int
	total      atomic.Int64
	totalLines int
	readOffset int64
	truncated  bool
	closed     bool
}

func newLogWriter(path string, maxBytes int64, maxLines int) (*logWriter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open background log: %w", err)
	}
	w := &logWriter{file: file, maxBytes: maxBytes, maxLines: maxLines}
	if info, statErr := file.Stat(); statErr == nil {
		size := info.Size()
		w.total.Store(size)
		start := int64(0)
		if size > maxBytes {
			start = size - maxBytes
			w.truncated = true
		}
		if start < size {
			payload := make([]byte, size-start)
			if n, readErr := file.ReadAt(payload, start); readErr == nil || errors.Is(readErr, io.EOF) {
				w.consume(string(payload[:n]))
			}
		}
		w.readOffset = size
	}
	return w, nil
}

func (w *logWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if w.total.Load()+int64(len(payload)) > w.maxBytes {
		if err := w.resetFileLocked(); err != nil {
			return 0, err
		}
	}
	written := payload
	if int64(len(written)) > w.maxBytes {
		written = written[len(written)-int(w.maxBytes):]
		w.truncated = true
	}
	if len(written) > 0 {
		if _, err := w.file.Write(written); err != nil {
			return 0, err
		}
		w.total.Add(int64(len(written)))
		w.consume(string(written))
	}
	return len(payload), nil
}

func (w *logWriter) consume(value string) {
	value = strings.ToValidUTF8(value, "�")
	parts := strings.Split(w.partial+value, "\n")
	w.partial = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		w.lines = append(w.lines, strings.TrimSuffix(line, "\r"))
		w.totalLines++
	}
	if overflow := len(w.lines) - w.maxLines; overflow > 0 {
		copy(w.lines, w.lines[overflow:])
		w.lines = w.lines[:len(w.lines)-overflow]
		w.truncated = true
	}
}

func (w *logWriter) Snapshot() ([]string, int, bool) {
	_ = w.Refresh()
	w.mu.RLock()
	defer w.mu.RUnlock()
	lines := append([]string(nil), w.lines...)
	total := w.totalLines
	if w.partial != "" {
		lines = append(lines, w.partial)
		total++
	}
	if len(lines) > w.maxLines {
		lines = lines[len(lines)-w.maxLines:]
	}
	return lines, total, w.truncated
}

func (w *logWriter) Size() int64 {
	if info, err := w.file.Stat(); err == nil {
		w.total.Store(info.Size())
	}
	return w.total.Load()
}

func (w *logWriter) Total() int64 { return w.Size() }

func (w *logWriter) Output() *os.File { return w.file }

func (w *logWriter) Refresh() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	if err := w.maintainLocked(); err != nil {
		return err
	}
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size < w.readOffset {
		w.readOffset = 0
		w.partial = ""
		w.truncated = true
	}
	start := w.readOffset
	if unread := size - start; unread > w.maxBytes {
		start = size - w.maxBytes
		w.partial = ""
		w.truncated = true
	}
	buffer := make([]byte, 64<<10)
	for start < size {
		count := int64(len(buffer))
		if remaining := size - start; remaining < count {
			count = remaining
		}
		n, readErr := w.file.ReadAt(buffer[:count], start)
		if n > 0 {
			w.consume(string(buffer[:n]))
			start += int64(n)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if n == 0 {
			break
		}
	}
	w.readOffset = size
	w.total.Store(size)
	return nil
}

// Maintain bounds disk use without parsing output. It is intentionally cheap
// enough for the supervisor's low-frequency monitor loop.
func (w *logWriter) Maintain() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	return w.maintainLocked()
}

func (w *logWriter) maintainLocked() error {
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	w.total.Store(size)
	if size <= w.maxBytes {
		return nil
	}
	return w.resetFileLocked()
}

// resetFileLocked uses copy-truncate semantics without copying. The child
// keeps the same file descriptor and continues appending after the reset,
// while the in-memory line ring still provides the recent tail to this TUI.
func (w *logWriter) resetFileLocked() error {
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	w.readOffset = 0
	w.partial = ""
	w.total.Store(0)
	w.truncated = true
	return nil
}

func (w *logWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.partial != "" {
		w.lines = append(w.lines, w.partial)
		w.partial = ""
		w.totalLines++
	}
	if len(w.lines) > w.maxLines {
		w.lines = w.lines[len(w.lines)-w.maxLines:]
		w.truncated = true
	}
	return w.file.Close()
}

var _ io.Writer = (*logWriter)(nil)
