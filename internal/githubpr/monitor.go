package githubpr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultMonitorInterval = 60 * time.Second
	maxMonitorBackoff      = 5 * time.Minute
	monitorStateVersion    = 3
)

// RepairStarter starts an isolated Azem session for one PR failure fingerprint.
type RepairStarter func(context.Context, PullRequest, RepairIssue) (string, error)

// MonitorEmitter publishes monitor state changes to desktop clients.
type MonitorEmitter func(MonitorState)

// PendingError reports a temporary local condition such as another active run.
type PendingError struct{ Reason string }

func (e *PendingError) Error() string { return e.Reason }

func NewPendingError(reason string) error {
	return &PendingError{Reason: strings.TrimSpace(reason)}
}

// Monitor polls enabled pull requests and starts at most one repair per failure fingerprint.
type Monitor struct {
	client    *Client
	statePath string
	repair    RepairStarter
	emit      MonitorEmitter
	interval  time.Duration

	mu               sync.Mutex
	states           map[int]MonitorState
	handled          map[int]string
	repositories     map[int]string
	launchingRepairs int
	pendingTerminals map[string]string
	ctx              context.Context
	cancel           context.CancelFunc
	wake             chan struct{}
	wg               sync.WaitGroup
	start            sync.Once
	close            sync.Once
}

type monitorFile struct {
	Version int            `json:"version"`
	Entries []monitorEntry `json:"entries"`
}

type monitorEntry struct {
	Number             int       `json:"number"`
	Enabled            bool      `json:"enabled"`
	Status             string    `json:"status,omitempty"`
	Message            string    `json:"message,omitempty"`
	SessionID          string    `json:"sessionId,omitempty"`
	Fingerprint        string    `json:"fingerprint,omitempty"`
	Repository         string    `json:"repository"`
	HandledFingerprint string    `json:"handledFingerprint,omitempty"`
	FailingChecks      []string  `json:"failingChecks,omitempty"`
	Conflict           bool      `json:"conflict,omitempty"`
	LastCheckedAt      time.Time `json:"lastCheckedAt,omitempty"`
	LastTriggeredAt    time.Time `json:"lastTriggeredAt,omitempty"`
}

func NewMonitor(parent context.Context, client *Client, statePath string, repair RepairStarter, emit MonitorEmitter) *Monitor {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	monitor := &Monitor{
		client: client, statePath: strings.TrimSpace(statePath), repair: repair, emit: emit,
		interval: defaultMonitorInterval, states: make(map[int]MonitorState), handled: make(map[int]string),
		repositories: make(map[int]string), pendingTerminals: make(map[string]string),
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
	}
	monitor.load()
	return monitor
}

func (m *Monitor) Start() {
	m.start.Do(func() {
		m.wg.Add(1)
		go m.loop()
	})
}

func (m *Monitor) Close() {
	m.close.Do(func() {
		m.cancel()
		m.wg.Wait()
	})
}

func (m *Monitor) Set(number int, enabled bool) (MonitorState, error) {
	if number <= 0 {
		return MonitorState{}, fmt.Errorf("pull request number must be positive")
	}
	repository := ""
	if enabled {
		if m.client == nil {
			return MonitorState{}, fmt.Errorf("pull request monitor has no GitHub client")
		}
		var err error
		repository, err = m.client.currentRepositoryName(m.ctx)
		if err != nil {
			return MonitorState{}, err
		}
	}
	m.mu.Lock()
	state := m.states[number]
	state.Number = number
	state.Enabled = enabled
	state.Message = ""
	if enabled {
		state.Status = MonitorWatching
		state.SessionID = ""
		state.Fingerprint = ""
		state.FailingChecks = nil
		state.Conflict = false
		m.repositories[number] = repository
		delete(m.handled, number)
	} else {
		state.Status = MonitorDisabled
		state.SessionID = ""
		state.Fingerprint = ""
		state.FailingChecks = nil
		state.Conflict = false
		delete(m.repositories, number)
		delete(m.handled, number)
	}
	m.states[number] = state
	err := m.saveLocked()
	m.mu.Unlock()
	m.publish(state)
	if enabled {
		m.signal()
	}
	return state, err
}

func (m *Monitor) State(number int) MonitorState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[number]
	if !ok {
		return MonitorState{Number: number, Status: MonitorDisabled}
	}
	return cloneMonitorState(state)
}

func (m *Monitor) States() []MonitorState {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := make([]MonitorState, 0, len(m.states))
	for _, state := range m.states {
		states = append(states, cloneMonitorState(state))
	}
	sort.Slice(states, func(left, right int) bool { return states[left].Number < states[right].Number })
	return states
}

// ObserveSession updates a repair lifecycle from the Azem runtime event stream.
func (m *Monitor) ObserveSession(sessionID, eventKind string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	var changed []MonitorState
	m.mu.Lock()
	matched := false
	for number, state := range m.states {
		if state.SessionID != sessionID || state.Status != MonitorRepairing {
			continue
		}
		terminal, ok := monitorTerminalState(state, eventKind)
		if !ok {
			continue
		}
		matched = true
		m.states[number] = terminal
		changed = append(changed, cloneMonitorState(terminal))
	}
	// StartAutomatedTurn launches asynchronously before its starter returns. Keep
	// a terminal event emitted in that narrow window until check records SessionID.
	if !matched && m.launchingRepairs > 0 && isMonitorTerminalEvent(eventKind) {
		m.pendingTerminals[sessionID] = eventKind
	}
	_ = m.saveLocked()
	m.mu.Unlock()
	for _, state := range changed {
		m.publish(state)
	}
}

func (m *Monitor) loop() {
	defer m.wg.Done()
	delay := time.Duration(0)
	failures := 0
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-m.ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-m.wake:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
			}
		} else {
			select {
			case <-m.ctx.Done():
				return
			default:
			}
		}

		hadError := m.checkAll()
		if hadError {
			failures++
		} else {
			failures = 0
		}
		delay = monitorDelay(m.interval, failures)
	}
}

func monitorDelay(base time.Duration, failures int) time.Duration {
	if base <= 0 {
		base = defaultMonitorInterval
	}
	if failures <= 0 {
		return base
	}
	delay := base
	for step := 0; step < failures && delay < maxMonitorBackoff; step++ {
		delay *= 2
	}
	if delay > maxMonitorBackoff {
		return maxMonitorBackoff
	}
	return delay
}

func (m *Monitor) checkAll() bool {
	m.mu.Lock()
	numbers := make([]int, 0, len(m.states))
	for number, state := range m.states {
		if state.Enabled {
			numbers = append(numbers, number)
		}
	}
	m.mu.Unlock()
	sort.Ints(numbers)
	hadError := false
	for _, number := range numbers {
		if m.ctx.Err() != nil {
			return hadError
		}
		if err := m.check(number); err != nil {
			hadError = true
		}
	}
	return hadError
}

func (m *Monitor) check(number int) error {
	if !m.enabled(number) {
		return nil
	}
	expectedRepository := m.repository(number)
	currentRepository, err := m.client.currentRepositoryName(m.ctx)
	if !m.enabled(number) {
		return nil
	}
	if expectedRepository != m.repository(number) {
		return nil
	}
	if err != nil {
		state := m.update(number, func(state MonitorState) MonitorState {
			state.Status = MonitorError
			state.Message = err.Error()
			state.LastCheckedAt = time.Now().UTC()
			return state
		})
		m.publish(state)
		return err
	}
	if expectedRepository == "" || !strings.EqualFold(expectedRepository, currentRepository) {
		state := m.disableForRepositoryMismatch(number, expectedRepository, currentRepository)
		m.publish(state)
		return nil
	}
	pr, err := m.client.Detail(m.ctx, number)
	if !m.enabled(number) {
		return nil
	}
	if err != nil {
		state := m.update(number, func(state MonitorState) MonitorState {
			state.Status = MonitorError
			state.Message = err.Error()
			state.LastCheckedAt = time.Now().UTC()
			return state
		})
		m.publish(state)
		return err
	}
	if pr.State != "OPEN" {
		state := m.update(number, func(state MonitorState) MonitorState {
			state.Enabled = false
			state.Status = MonitorDisabled
			state.Message = fmt.Sprintf("pull request is %s", strings.ToLower(pr.State))
			state.LastCheckedAt = time.Now().UTC()
			return state
		})
		m.mu.Lock()
		delete(m.handled, number)
		delete(m.repositories, number)
		_ = m.saveLocked()
		m.mu.Unlock()
		m.publish(state)
		return nil
	}

	issue := m.client.repairIssue(pr)
	now := time.Now().UTC()
	if !issue.Conflict && len(issue.FailingChecks) == 0 {
		state := m.update(number, func(state MonitorState) MonitorState {
			state.Status = MonitorWatching
			state.Message = ""
			state.SessionID = ""
			state.Conflict = false
			state.FailingChecks = nil
			state.Fingerprint = ""
			state.LastCheckedAt = now
			return state
		})
		m.mu.Lock()
		delete(m.handled, number)
		_ = m.saveLocked()
		m.mu.Unlock()
		m.publish(state)
		return nil
	}

	m.mu.Lock()
	handled := m.handled[number]
	prior := m.states[number]
	m.mu.Unlock()
	if handled == issue.Fingerprint {
		state := m.update(number, func(state MonitorState) MonitorState {
			state.Conflict = issue.Conflict
			state.FailingChecks = append([]string(nil), issue.FailingChecks...)
			state.Fingerprint = issue.Fingerprint
			state.LastCheckedAt = now
			if state.Status != MonitorRepairing && state.Status != MonitorCompleted {
				state.Status = prior.Status
			}
			return state
		})
		m.publish(state)
		return nil
	}

	workspace, err := m.client.workspaceState(m.ctx)
	if !m.enabled(number) {
		return nil
	}
	if err != nil {
		state := m.pending(number, issue, now, err.Error())
		m.publish(state)
		return err
	}
	if workspace.Branch != pr.HeadRefName {
		state := m.pending(number, issue, now, fmt.Sprintf("switch to %s to start repair", pr.HeadRefName))
		m.publish(state)
		return nil
	}
	if workspace.Dirty {
		state := m.pending(number, issue, now, "commit or discard workspace changes to start repair")
		m.publish(state)
		return nil
	}
	if pr.HeadRefOID == "" || workspace.HeadOID != pr.HeadRefOID {
		state := m.pending(number, issue, now, "local HEAD does not match the pull request head commit")
		m.publish(state)
		return nil
	}
	if pr.HeadRepository == "" || !strings.EqualFold(workspace.UpstreamRepository, pr.HeadRepository) {
		state := m.pending(number, issue, now, "local branch upstream does not match the pull request head repository")
		m.publish(state)
		return nil
	}
	if m.repair == nil {
		state := m.pending(number, issue, now, "automated repair is unavailable")
		m.publish(state)
		return nil
	}
	if !m.enabled(number) {
		return nil
	}

	m.mu.Lock()
	m.launchingRepairs++
	m.mu.Unlock()
	sessionID, err := m.repair(m.ctx, pr, issue)
	if err != nil {
		m.abandonRepairLaunch(sessionID)
		if !m.enabled(number) {
			return nil
		}
		var pending *PendingError
		if errors.As(err, &pending) {
			state := m.pending(number, issue, now, pending.Reason)
			m.publish(state)
			return nil
		}
		state := m.update(number, func(state MonitorState) MonitorState {
			state.Status = MonitorError
			state.Message = err.Error()
			state.Conflict = issue.Conflict
			state.FailingChecks = append([]string(nil), issue.FailingChecks...)
			state.Fingerprint = issue.Fingerprint
			state.LastCheckedAt = now
			return state
		})
		m.publish(state)
		return err
	}

	state, enabled := m.finishRepairLaunch(number, sessionID, issue, now)
	if !enabled {
		return nil
	}
	m.publish(state)
	return nil
}

func (m *Monitor) abandonRepairLaunch(sessionID string) {
	m.mu.Lock()
	if m.launchingRepairs > 0 {
		m.launchingRepairs--
	}
	delete(m.pendingTerminals, sessionID)
	if m.launchingRepairs == 0 {
		clear(m.pendingTerminals)
	}
	m.mu.Unlock()
}

func (m *Monitor) finishRepairLaunch(number int, sessionID string, issue RepairIssue, now time.Time) (MonitorState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.launchingRepairs > 0 {
		m.launchingRepairs--
	}
	state, enabled := m.states[number]
	if !enabled || !state.Enabled {
		delete(m.pendingTerminals, sessionID)
		if m.launchingRepairs == 0 {
			clear(m.pendingTerminals)
		}
		return cloneMonitorState(state), false
	}
	state.Status = MonitorRepairing
	state.Message = "repair session started"
	state.SessionID = sessionID
	state.Conflict = issue.Conflict
	state.FailingChecks = append([]string(nil), issue.FailingChecks...)
	state.Fingerprint = issue.Fingerprint
	state.LastCheckedAt = now
	state.LastTriggeredAt = now
	m.handled[number] = issue.Fingerprint
	if eventKind, ok := m.pendingTerminals[sessionID]; ok {
		state, _ = monitorTerminalState(state, eventKind)
	}
	m.states[number] = state
	delete(m.pendingTerminals, sessionID)
	if m.launchingRepairs == 0 {
		clear(m.pendingTerminals)
	}
	_ = m.saveLocked()
	return cloneMonitorState(state), true
}

func isMonitorTerminalEvent(eventKind string) bool {
	return eventKind == "run_finished" || eventKind == "run_failed" || eventKind == "run_cancelled"
}

func monitorTerminalState(state MonitorState, eventKind string) (MonitorState, bool) {
	switch eventKind {
	case "run_finished":
		state.Status = MonitorCompleted
		state.Message = "repair session completed"
	case "run_failed":
		state.Status = MonitorError
		state.Message = "repair session failed"
	case "run_cancelled":
		state.Status = MonitorError
		state.Message = "repair session was cancelled"
	default:
		return state, false
	}
	return state, true
}

func (m *Monitor) pending(number int, issue RepairIssue, checkedAt time.Time, message string) MonitorState {
	return m.update(number, func(state MonitorState) MonitorState {
		state.Status = MonitorPending
		state.Message = message
		state.Conflict = issue.Conflict
		state.FailingChecks = append([]string(nil), issue.FailingChecks...)
		state.Fingerprint = issue.Fingerprint
		state.LastCheckedAt = checkedAt
		return state
	})
}

func (m *Monitor) update(number int, mutate func(MonitorState) MonitorState) MonitorState {
	m.mu.Lock()
	state, ok := m.states[number]
	if !ok || !state.Enabled {
		m.mu.Unlock()
		return cloneMonitorState(state)
	}
	state.Number = number
	state = mutate(state)
	m.states[number] = state
	_ = m.saveLocked()
	m.mu.Unlock()
	return cloneMonitorState(state)
}

func (m *Monitor) enabled(number int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[number].Enabled
}

func (m *Monitor) repository(number int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.repositories[number]
}

func (m *Monitor) disableForRepositoryMismatch(number int, expected, current string) MonitorState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[number]
	if !state.Enabled || m.repositories[number] != expected {
		return cloneMonitorState(state)
	}
	state.Enabled = false
	state.Status = MonitorDisabled
	state.SessionID = ""
	state.Message = fmt.Sprintf("monitor repository changed from %s to %s; enable it again to confirm", expected, current)
	state.LastCheckedAt = time.Now().UTC()
	m.states[number] = state
	delete(m.handled, number)
	delete(m.repositories, number)
	_ = m.saveLocked()
	return cloneMonitorState(state)
}

func (m *Monitor) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Monitor) publish(state MonitorState) {
	if m.emit != nil {
		m.emit(cloneMonitorState(state))
	}
}

func (m *Monitor) load() {
	if m.statePath == "" {
		return
	}
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var file monitorFile
	if json.Unmarshal(data, &file) != nil || file.Version != monitorStateVersion {
		return
	}
	for _, entry := range file.Entries {
		if entry.Number <= 0 || !entry.Enabled {
			continue
		}
		if strings.TrimSpace(entry.Repository) == "" {
			continue
		}
		status := entry.Status
		switch status {
		case MonitorWatching, MonitorPending, MonitorRepairing, MonitorCompleted, MonitorError:
		default:
			status = MonitorWatching
			if entry.SessionID != "" && entry.HandledFingerprint != "" {
				status = MonitorRepairing
			}
		}
		interrupted := status == MonitorRepairing
		if interrupted {
			status = MonitorPending
			entry.Message = "repair session was interrupted; checking again"
			entry.SessionID = ""
		}
		m.states[entry.Number] = MonitorState{
			Number: entry.Number, Enabled: true, Status: status, Message: entry.Message,
			SessionID: entry.SessionID, Fingerprint: entry.Fingerprint,
			FailingChecks: append([]string(nil), entry.FailingChecks...), Conflict: entry.Conflict,
			LastCheckedAt: entry.LastCheckedAt, LastTriggeredAt: entry.LastTriggeredAt,
		}
		m.repositories[entry.Number] = strings.TrimSpace(entry.Repository)
		if entry.HandledFingerprint != "" && !interrupted {
			m.handled[entry.Number] = entry.HandledFingerprint
		}
	}
}

func (m *Monitor) saveLocked() error {
	if m.statePath == "" {
		return nil
	}
	entries := make([]monitorEntry, 0, len(m.states))
	for number, state := range m.states {
		if !state.Enabled {
			continue
		}
		entries = append(entries, monitorEntry{
			Number: number, Enabled: true, Status: state.Status, Message: state.Message,
			SessionID: state.SessionID, Fingerprint: state.Fingerprint,
			Repository:         m.repositories[number],
			HandledFingerprint: m.handled[number],
			FailingChecks:      append([]string(nil), state.FailingChecks...), Conflict: state.Conflict,
			LastCheckedAt: state.LastCheckedAt, LastTriggeredAt: state.LastTriggeredAt,
		})
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Number < entries[right].Number })
	data, err := json.MarshalIndent(monitorFile{Version: monitorStateVersion, Entries: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode PR monitor state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o700); err != nil {
		return fmt.Errorf("create PR monitor state directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.statePath), ".pr-monitors-*.tmp")
	if err != nil {
		return fmt.Errorf("create PR monitor state file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure PR monitor state file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write PR monitor state: %w", err)
	}
	if _, err := temporary.Write([]byte("\n")); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("finish PR monitor state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync PR monitor state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close PR monitor state: %w", err)
	}
	if err := os.Rename(temporaryPath, m.statePath); err != nil {
		return fmt.Errorf("replace PR monitor state: %w", err)
	}
	return nil
}

func cloneMonitorState(state MonitorState) MonitorState {
	state.FailingChecks = append([]string(nil), state.FailingChecks...)
	return state
}
