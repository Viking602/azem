package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/venat/tool"
)

const ToolShell = "coding.shell"

type ShellOptions struct {
	MaxContextOutputBytes  int
	MaxArtifactOutputBytes int
	StopOnOutputLimit      bool
	MaxWallClockDuration   time.Duration
	MaxConcurrency         int
	ArtifactSink           func(context.Context, ShellExecutionSnapshot, []byte) (ShellArtifactResult, error)
}

type ShellArtifactResult struct {
	Reference string `json:"reference,omitempty"`
}

func defaultShellOptions() ShellOptions {
	return ShellOptions{MaxContextOutputBytes: 65536, MaxArtifactOutputBytes: 4194304, StopOnOutputLimit: true, MaxConcurrency: 2, MaxWallClockDuration: 10 * time.Minute}
}

func (d *shellDriver) maxWallClock() time.Duration {
	if d == nil || d.runtime == nil || d.runtime.opts.MaxWallClockDuration <= 0 {
		return defaultShellOptions().MaxWallClockDuration
	}
	return d.runtime.opts.MaxWallClockDuration
}

func maxWallClockSeconds(maxWall time.Duration) int {
	if maxWall <= 0 {
		maxWall = defaultShellOptions().MaxWallClockDuration
	}
	sec := int((maxWall + time.Second - 1) / time.Second)
	if sec < 1 {
		return 1
	}
	return sec
}

func resolveShellTimeouts(input shellInput, maxWall time.Duration) (inactivity, wall time.Duration, err error) {
	if maxWall <= 0 {
		maxWall = defaultShellOptions().MaxWallClockDuration
	}
	maxSec := maxWallClockSeconds(maxWall)
	wall = maxWall
	if input.WallClockSeconds != 0 {
		if input.WallClockSeconds < 1 || input.WallClockSeconds > maxSec {
			return 0, 0, fmt.Errorf("wall_clock_seconds must be between 1 and %d", maxSec)
		}
		wall = time.Duration(input.WallClockSeconds) * time.Second
		if wall > maxWall {
			wall = maxWall
		}
	}
	inactivity = 2 * time.Minute
	if inactivity > wall {
		inactivity = wall
	}
	if input.TimeoutSeconds != 0 {
		if input.TimeoutSeconds < 1 || input.TimeoutSeconds > maxSec {
			return 0, 0, fmt.Errorf("timeout_seconds must be between 1 and %d", maxSec)
		}
		inactivity = time.Duration(input.TimeoutSeconds) * time.Second
		if inactivity > wall {
			inactivity = wall
		}
	} else if input.WallClockSeconds != 0 {
		inactivity = wall
	}
	return inactivity, wall, nil
}

type ShellExecutionSnapshot struct {
	SessionID, RunID, AgentID, ToolCallID string
	PID, PGID                             int
	JobID                                 string
	CommandHash                           string
	StartedAt, Deadline                   time.Time
	State, Reason                         string
	ExitCode, OutputBytes                 int
	Output                                string
}

type shellRuntime struct {
	ctx     context.Context
	mu      sync.Mutex
	wg      sync.WaitGroup
	active  map[string]ShellExecutionSnapshot
	opts    ShellOptions
	closing bool
	running int
	limit   int
	changed chan struct{}
}

func newShellRuntime(ctx context.Context, opts ShellOptions) *shellRuntime {
	defaults := defaultShellOptions()
	opts.StopOnOutputLimit = true
	if opts.MaxContextOutputBytes <= 0 {
		opts.MaxContextOutputBytes = defaults.MaxContextOutputBytes
	}
	if opts.MaxArtifactOutputBytes <= 0 {
		opts.MaxArtifactOutputBytes = defaults.MaxArtifactOutputBytes
	}
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = defaults.MaxConcurrency
	}
	if opts.MaxWallClockDuration <= 0 {
		opts.MaxWallClockDuration = defaults.MaxWallClockDuration
	}
	return &shellRuntime{ctx: ctx, active: map[string]ShellExecutionSnapshot{}, opts: opts, limit: opts.MaxConcurrency, changed: make(chan struct{})}
}

func (r *shellRuntime) snapshot() []ShellExecutionSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]ShellExecutionSnapshot, 0, len(r.active))
	for _, item := range r.active {
		result = append(result, item)
	}
	return result
}

func (r *shellRuntime) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return false
	}
	r.wg.Add(1)
	return true
}

func (r *shellRuntime) shutdown() {
	r.mu.Lock()
	r.closing = true
	r.signalChangedLocked()
	r.mu.Unlock()
}

func (r *shellRuntime) acquire(ctx context.Context) error {
	for {
		r.mu.Lock()
		if r.closing {
			r.mu.Unlock()
			return fmt.Errorf("service shutting down")
		}
		if r.running < r.limit {
			r.running++
			r.mu.Unlock()
			return nil
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.ctx.Done():
			return fmt.Errorf("service shutting down")
		case <-changed:
		}
	}
}

func (r *shellRuntime) release() {
	r.mu.Lock()
	if r.running > 0 {
		r.running--
	}
	r.signalChangedLocked()
	r.mu.Unlock()
}

func (r *shellRuntime) updateMaxConcurrency(maxConcurrency int) {
	if maxConcurrency < 1 {
		return
	}
	r.mu.Lock()
	r.limit = maxConcurrency
	r.opts.MaxConcurrency = maxConcurrency
	r.signalChangedLocked()
	r.mu.Unlock()
}

func (r *shellRuntime) updateMaxWallClock(wall time.Duration) {
	if wall < time.Second {
		return
	}
	r.mu.Lock()
	r.opts.MaxWallClockDuration = wall
	r.signalChangedLocked()
	r.mu.Unlock()
}

func (r *shellRuntime) signalChangedLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}

type shellDriver struct {
	root, approval, allowNetwork string
	runtime                      *shellRuntime
}

type shellSupervisor struct {
	command *exec.Cmd
	owner   *shellProcessOwner
}

func newShellSupervisor(command *exec.Cmd) (*shellSupervisor, error) {
	owner, err := newShellProcessOwner(command)
	if err != nil {
		return nil, err
	}
	return &shellSupervisor{command: command, owner: owner}, nil
}

func (s *shellSupervisor) Start() error {
	if err := s.command.Start(); err != nil {
		return err
	}
	if err := s.owner.Assign(s.command); err != nil {
		_ = s.command.Process.Kill()
		_ = s.command.Wait()
		return err
	}
	return nil
}
func (s *shellSupervisor) Wait() error      { return s.command.Wait() }
func (s *shellSupervisor) Terminate() error { return s.owner.Terminate() }
func (s *shellSupervisor) Close() error     { return s.owner.Close() }

type shellInput struct {
	Command          string `json:"command"`
	Stdin            string `json:"stdin,omitempty"`
	TimeoutSeconds   int    `json:"timeout_seconds,omitempty"`
	WallClockSeconds int    `json:"wall_clock_seconds,omitempty"`
	Network          bool   `json:"network,omitempty"`
}
type shellOutput struct {
	ExitCode    int    `json:"exitCode"`
	Output      string `json:"output"`
	Truncated   bool   `json:"truncated"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	OutputBytes int    `json:"outputBytes"`
	ArtifactRef string `json:"artifactRef,omitempty"`
}

func newShellDriver(root, approval, allowNetwork string) tool.Driver {
	ctx := context.Background()
	return &shellDriver{root: root, approval: approval, allowNetwork: allowNetwork, runtime: newShellRuntime(ctx, defaultShellOptions())}
}

func newRuntimeShellDriver(root, approval, allowNetwork string, runtime *shellRuntime) tool.Driver {
	return &shellDriver{root, approval, allowNetwork, runtime}
}

func (d *shellDriver) Definition() tool.Definition {
	additional := false
	maxWall := d.maxWallClock()
	maxSec := maxWallClockSeconds(maxWall)
	description := fmt.Sprintf("Run a supervised foreground command. Set wall_clock_seconds to the hard deadline you need for this command, from 1 to %d seconds (workspace.shell.max_wall_clock). Omit it to use the configured maximum. timeout_seconds is the maximum interval without stdout/stderr; active output extends that interval up to the wall clock. If you set wall_clock_seconds and omit timeout_seconds, a silent command may run until the wall clock. stdin is optional UTF-8 fed to the process (scripted keystrokes or piped input). POSIX background operators and known detach primitives are rejected because descendants that create another session can escape process-group supervision.", maxSec)
	if runtime.GOOS == "windows" {
		description += " Commands use PowerShell on Windows."
	}
	return tool.Definition{
		Name:        ToolShell,
		Description: description,
		InputSchema: tool.Schema{
			Type: "object",
			Properties: map[string]tool.Schema{
				"command":            {Type: "string"},
				"stdin":              {Type: "string"},
				"timeout_seconds":    {Type: "integer"},
				"wall_clock_seconds": {Type: "integer"},
				"network":            {Type: "boolean"},
			},
			Required:             []string{"command"},
			AdditionalProperties: &additional,
		},
		EffectType:         tool.EffectExternalSideEffect,
		RequiresApproval:   d.approval != "allow",
		RequiresActionTask: true,
		RiskLevel:          "high",
		PolicyTags:         []string{"coding", "shell", "workspace"},
		Metadata:           map[string]string{"approval": d.approval, "network": d.allowNetwork, "platform": runtime.GOOS, "max_wall_clock_seconds": fmt.Sprint(maxSec)},
	}
}

// rejectDetached is defense in depth for commands that can escape the process
// group or Job Object. Process ownership is the primary containment boundary.
func rejectDetached(command string) bool {
	return rejectDetachedForOS(command, runtime.GOOS)
}

func rejectDetachedForOS(command, _ string) bool {
	// POSIX shells concatenate quoted and escaped word fragments before command
	// lookup (for example set''sid and set\\sid both execute setsid). Removing
	// those syntax characters is conservative defense in depth; process
	// supervision must never depend on an exact raw token spelling.
	normalized := strings.NewReplacer("'", "", `"`, "", `\`, "").Replace(command)
	for _, token := range strings.Fields(normalized) {
		plain := strings.Trim(token, "();")
		if plain == "nohup" || plain == "setsid" || plain == "disown" || plain == "daemonize" {
			return true
		}
	}
	return false
}

func hasBackgroundOperator(command string) bool {
	return hasBackgroundOperatorForOS(command, runtime.GOOS)
}

func hasBackgroundOperatorForOS(command, goos string) bool {
	var quote byte
	escaped := false
	for index := 0; index < len(command); index++ {
		current := command[index]
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		if current != '&' || goos == "windows" {
			continue
		}
		if index+1 < len(command) && command[index+1] == '&' {
			index++
			continue
		}
		if ampersandIsRedirection(command, index) {
			continue
		}
		return true
	}
	return false
}

func ampersandIsRedirection(command string, index int) bool {
	if index > 0 {
		switch command[index-1] {
		case '>', '<', '|':
			return true
		}
	}
	return index+1 < len(command) && command[index+1] == '>'
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS != "windows" {
		return exec.Command("/bin/sh", "-c", command)
	}
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return exec.Command(path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
		}
	}
	return exec.Command("cmd.exe", "/d", "/s", "/c", command)
}

func (d *shellDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	var input shellInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return shellError(call, "decode arguments: "+err.Error()), nil
	}
	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" {
		return shellError(call, "command is empty"), nil
	}
	if rejectDetached(input.Command) {
		return shellError(call, "detached execution that can escape process supervision is not permitted"), nil
	}
	if hasBackgroundOperator(input.Command) {
		return shellError(call, "background operators that can escape process supervision are not permitted"), nil
	}
	if d.approval == "deny" {
		return shellError(call, "shell commands are disabled by workspace.shell_policy"), nil
	}
	if input.Network && d.allowNetwork == "deny" {
		return shellError(call, "network access is disabled by workspace.allow_network"), nil
	}
	if err := d.runtime.acquire(ctx); err != nil {
		return shellError(call, err.Error()), nil
	}
	defer d.runtime.release()
	if ctx.Err() != nil || d.runtime.ctx.Err() != nil {
		return shellError(call, "command cancelled before start"), nil
	}
	if !d.runtime.begin() {
		return shellError(call, "service shutting down"), nil
	}
	defer d.runtime.wg.Done()
	inactivityTimeout, wallClock, timeoutErr := resolveShellTimeouts(input, d.maxWallClock())
	if timeoutErr != nil {
		return shellError(call, timeoutErr.Error()), nil
	}
	command := shellCommand(input.Command)
	supervisor, ownerErr := newShellSupervisor(command)
	if ownerErr != nil {
		return shellError(call, "prepare process owner: "+ownerErr.Error()), nil
	}
	ownerClosed := false
	defer func() {
		if !ownerClosed {
			_ = supervisor.Close()
		}
	}()
	command.Dir = d.root
	command.WaitDelay = 100 * time.Millisecond
	if input.Stdin != "" {
		command.Stdin = strings.NewReader(input.Stdin)
	}
	limitHit := make(chan struct{}, 1)
	output := &boundedShellBuffer{contextLimit: d.runtime.opts.MaxContextOutputBytes, artifactLimit: d.runtime.opts.MaxArtifactOutputBytes, onLimit: func() {
		select {
		case limitHit <- struct{}{}:
		default:
		}
	}}
	command.Stdout = output
	command.Stderr = output
	if ctx.Err() != nil || d.runtime.ctx.Err() != nil {
		return shellError(call, "command cancelled before start"), nil
	}
	if err := supervisor.Start(); err != nil {
		closeErr := supervisor.Close()
		ownerClosed = true
		return shellError(call, "start: "+errors.Join(err, closeErr).Error()), nil
	}
	startedAt := time.Now()
	deadline := startedAt.Add(inactivityTimeout)
	absoluteDeadline := startedAt.Add(wallClock)
	sum := sha256.Sum256([]byte(input.Command))
	caller, _ := tool.CallerFromContext(ctx)
	snap := ShellExecutionSnapshot{SessionID: caller.SessionID, RunID: caller.TeamRunID, AgentID: caller.AgentID, ToolCallID: call.ID, CommandHash: hex.EncodeToString(sum[:]), State: "running", PID: command.Process.Pid, PGID: supervisor.owner.PGID(), JobID: supervisor.owner.JobID(), StartedAt: startedAt, Deadline: deadline, ExitCode: -1}
	registryKey := fmt.Sprintf("%s/%d", call.ID, command.Process.Pid)
	d.runtime.mu.Lock()
	d.runtime.active[registryKey] = snap
	d.runtime.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- supervisor.Wait() }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	idleTimer := time.NewTimer(inactivityTimeout)
	defer func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
	}()
	wallClockTimer := time.NewTimer(wallClock)
	defer wallClockTimer.Stop()
	var err error
	reason := ""
	var terminationErr, updateSinkErr error
	finished := false
	if sink != nil {
		startedData := map[string]string{
			"cwd": d.root, "pid": fmt.Sprint(command.Process.Pid), "health": "running",
			"timeout_mode": "output_inactivity_with_wall_clock_limit", "timeout_seconds": fmt.Sprint(int(inactivityTimeout / time.Second)),
			"wall_clock_seconds": fmt.Sprint(int((wallClock + time.Second - 1) / time.Second)),
			"output":             "", "output_bytes": "0", "deadline": deadline.UTC().Format(time.RFC3339Nano),
			"wall_clock_deadline": absoluteDeadline.UTC().Format(time.RFC3339Nano),
		}
		if sinkErr := sink(tool.Update{Kind: "started", Data: startedData}); sinkErr != nil {
			reason = "update_sink_failure"
			updateSinkErr = fmt.Errorf("started update sink failed: %w", sinkErr)
		}
	}
	for !finished && reason == "" {
		select {
		case err = <-done:
			finished = true
		case <-d.runtime.ctx.Done():
			reason = "application_shutdown"
		case <-ctx.Done():
			reason = "context_cancelled"
		case probedAt := <-idleTimer.C:
			lastOutputAt := output.LastWrite()
			activityAt := lastOutputAt
			if activityAt.IsZero() {
				activityAt = startedAt
			}
			remaining := inactivityTimeout - probedAt.Sub(activityAt)
			if remaining <= 0 {
				if !probedAt.Before(absoluteDeadline) {
					reason = "wall_clock_timeout"
				} else {
					reason = "timeout"
				}
				break
			}
			deadline = activityAt.Add(inactivityTimeout)
			idleTimer.Reset(remaining)
			d.runtime.mu.Lock()
			current := d.runtime.active[registryKey]
			current.Deadline = deadline
			d.runtime.active[registryKey] = current
			d.runtime.mu.Unlock()
		case <-wallClockTimer.C:
			reason = "wall_clock_timeout"
		case <-limitHit:
			if d.runtime.opts.StopOnOutputLimit {
				reason = "output_limit"
			}
		case <-ticker.C:
			liveOutput, outputBytes, lastOutputAt := output.Progress()
			activityAt := lastOutputAt
			if activityAt.IsZero() {
				activityAt = startedAt
			}
			deadline = activityAt.Add(inactivityTimeout)
			d.runtime.mu.Lock()
			current := d.runtime.active[registryKey]
			current.OutputBytes, current.Output, current.Deadline = outputBytes, liveOutput, deadline
			d.runtime.active[registryKey] = current
			d.runtime.mu.Unlock()
			if sink != nil {
				data := map[string]string{
					"pid": fmt.Sprint(command.Process.Pid), "health": "running",
					"timeout_mode": "output_inactivity_with_wall_clock_limit", "timeout_seconds": fmt.Sprint(int(inactivityTimeout / time.Second)),
					"wall_clock_seconds": fmt.Sprint(int((wallClock + time.Second - 1) / time.Second)),
					"output":             liveOutput, "output_bytes": fmt.Sprint(outputBytes), "deadline": deadline.UTC().Format(time.RFC3339Nano),
					"wall_clock_deadline": absoluteDeadline.UTC().Format(time.RFC3339Nano),
				}
				if !lastOutputAt.IsZero() {
					data["last_output_at"] = lastOutputAt.UTC().Format(time.RFC3339Nano)
				}
				if sinkErr := sink(tool.Update{Kind: "progress", Data: data}); sinkErr != nil {
					reason = "update_sink_failure"
					updateSinkErr = fmt.Errorf("progress update sink failed: %w", sinkErr)
				}
			}
		}
	}
	if reason != "" {
		liveOutput, outputBytes, _ := output.Progress()
		d.runtime.mu.Lock()
		current := d.runtime.active[registryKey]
		current.State, current.Reason, current.OutputBytes, current.Output = "stopping", reason, outputBytes, liveOutput
		d.runtime.active[registryKey] = current
		d.runtime.mu.Unlock()
		terminationErr = supervisor.Terminate()
		if terminationErr != nil && command.Process != nil {
			_ = command.Process.Kill()
		}
		err = <-done
	}
	// Always reap residual group members, including children left after a normal shell exit.
	waitDelay := errors.Is(err, exec.ErrWaitDelay)
	terminationErr = errors.Join(terminationErr, supervisor.Terminate())
	terminationErr = errors.Join(terminationErr, supervisor.Close())
	ownerClosed = true
	if waitDelay {
		err = nil
	}
	d.runtime.mu.Lock()
	delete(d.runtime.active, registryKey)
	d.runtime.mu.Unlock()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	if terminationErr != nil {
		reason = "process_cleanup_failure"
		err = terminationErr
	}
	contextOut, artifact, total, truncated := output.Values()
	status := "exited"
	if reason != "" {
		status = "stopped"
	}
	value := shellOutput{ExitCode: exitCode, Output: contextOut, Truncated: truncated, Status: status, Reason: reason, OutputBytes: total}
	snap.State, snap.Reason, snap.ExitCode, snap.OutputBytes, snap.Output, snap.Deadline = status, reason, exitCode, total, contextOut, deadline
	if d.runtime.opts.ArtifactSink != nil && truncated {
		artifactResult, sinkErr := d.runtime.opts.ArtifactSink(ctx, snap, []byte(artifact))
		if sinkErr != nil {
			reason = "artifact_sink_failure"
			value.Reason, value.Status = reason, "stopped"
		} else {
			value.ArtifactRef = artifactResult.Reference
		}
	}
	structured, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return tool.Result{}, marshalErr
	}
	content := contextOut
	if value.ArtifactRef != "" {
		preview := contextOut
		if len(preview) > 512 {
			preview = preview[:512]
		}
		content = strings.TrimSpace(preview + "\nfull output: " + value.ArtifactRef)
	}
	if reason != "" {
		content = strings.TrimSpace(content + "\ncommand stopped: " + reason)
	}
	result := tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: structured, IsError: err != nil || reason != ""}
	if updateSinkErr != nil {
		result.IsError = true
		result.Content = strings.TrimSpace(result.Content + "\n" + updateSinkErr.Error())
	}
	if sink != nil {
		finishedData := map[string]string{
			"pid": fmt.Sprint(command.Process.Pid), "health": status, "status": status,
			"exit_code": fmt.Sprint(exitCode), "reason": reason, "output": contextOut, "output_bytes": fmt.Sprint(total),
		}
		if sinkErr := sink(tool.Update{Kind: "finished", Data: finishedData}); sinkErr != nil {
			result.IsError = true
			result.Content = strings.TrimSpace(result.Content + "\nfinished update sink failed: " + sinkErr.Error())
		}
	}
	return result, nil
}

func shellError(call tool.Call, message string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "coding.shell rejected: " + message, IsError: true}
}

type boundedShellBuffer struct {
	mu                                 sync.Mutex
	context, artifact                  bytes.Buffer
	contextLimit, artifactLimit, total int
	lastWrite                          time.Time
	truncated, notified                bool
	onLimit                            func()
}

func (b *boundedShellBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(value)
	b.total += n
	if n > 0 {
		b.lastWrite = time.Now()
	}
	if room := b.artifactLimit - b.artifact.Len(); room > 0 {
		part := value
		if len(part) > room {
			part = part[:room]
		}
		_, _ = b.artifact.Write(part)
	}
	if room := b.contextLimit - b.context.Len(); room > 0 {
		part := value
		if len(part) > room {
			part = part[:room]
		}
		_, _ = b.context.Write(part)
	}
	if b.total > b.contextLimit {
		b.truncated = true
		if !b.notified {
			b.notified = true
			b.onLimit()
		}
	}
	return n, nil
}

func (b *boundedShellBuffer) LastWrite() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastWrite
}

func (b *boundedShellBuffer) Progress() (string, int, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.context.String(), b.total, b.lastWrite
}

func (b *boundedShellBuffer) Values() (string, string, int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.context.String(), b.artifact.String(), b.total, b.truncated
}
