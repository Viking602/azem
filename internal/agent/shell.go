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

	"github.com/Viking602/go-hydaelyn/tool"
)

const ToolShell = "coding.shell"

type ShellOptions struct {
	MaxContextOutputBytes  int
	MaxArtifactOutputBytes int
	StopOnOutputLimit      bool
	MaxConcurrency         int
	ArtifactSink           func(context.Context, ShellExecutionSnapshot, []byte) (ShellArtifactResult, error)
}

type ShellArtifactResult struct {
	Reference string `json:"reference,omitempty"`
}

func defaultShellOptions() ShellOptions {
	return ShellOptions{MaxContextOutputBytes: 65536, MaxArtifactOutputBytes: 4194304, StopOnOutputLimit: true, MaxConcurrency: 2}
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
	sem     chan struct{}
	mu      sync.Mutex
	wg      sync.WaitGroup
	active  map[string]ShellExecutionSnapshot
	opts    ShellOptions
	closing bool
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
	return &shellRuntime{ctx: ctx, sem: make(chan struct{}, opts.MaxConcurrency), active: map[string]ShellExecutionSnapshot{}, opts: opts}
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
	r.mu.Unlock()
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
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Network        bool   `json:"network,omitempty"`
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
	return tool.Definition{Name: ToolShell, Description: "Run a foreground command. Detached/background processes are not permitted.", InputSchema: tool.Schema{Type: "object", Properties: map[string]tool.Schema{"command": {Type: "string"}, "timeout_seconds": {Type: "integer"}, "network": {Type: "boolean"}}, Required: []string{"command"}, AdditionalProperties: &additional}, EffectType: tool.EffectExternalSideEffect, RequiresApproval: d.approval != "allow", RequiresActionTask: true, RiskLevel: "high", Timeout: 10 * time.Minute, PolicyTags: []string{"coding", "shell", "workspace"}, Metadata: map[string]string{"approval": d.approval, "network": d.allowNetwork}}
}

// rejectDetached is defense in depth. Process-group/job ownership is the actual boundary.
func rejectDetached(command string) bool {
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
		if current == '&' {
			if index+1 < len(command) && command[index+1] == '&' {
				index++
				continue
			}
			return true
		}
	}
	tokens := strings.Fields(command)
	for _, token := range tokens {
		plain := strings.Trim(token, "'\"();")
		if plain == "nohup" || plain == "setsid" || plain == "disown" || plain == "daemonize" {
			return true
		}
	}
	return false
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
		return shellError(call, "detached/background execution is not permitted"), nil
	}
	if d.approval == "deny" {
		return shellError(call, "shell commands are disabled by workspace.shell_policy"), nil
	}
	if input.Network && d.allowNetwork == "deny" {
		return shellError(call, "network access is disabled by workspace.allow_network"), nil
	}
	if sink != nil {
		if err := sink(tool.Update{Kind: "started", Message: input.Command, Data: map[string]string{"cwd": d.root}}); err != nil {
			return shellError(call, "update sink failed before start: "+err.Error()), nil
		}
	}
	select {
	case d.runtime.sem <- struct{}{}:
		defer func() { <-d.runtime.sem }()
	case <-ctx.Done():
		return shellError(call, ctx.Err().Error()), nil
	case <-d.runtime.ctx.Done():
		return shellError(call, "service shutting down"), nil
	}
	if ctx.Err() != nil || d.runtime.ctx.Err() != nil {
		return shellError(call, "command cancelled before start"), nil
	}
	if !d.runtime.begin() {
		return shellError(call, "service shutting down"), nil
	}
	defer d.runtime.wg.Done()
	timeout := 2 * time.Minute
	if input.TimeoutSeconds != 0 {
		if input.TimeoutSeconds < 1 || input.TimeoutSeconds > 600 {
			return shellError(call, "timeout_seconds must be between 1 and 600"), nil
		}
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
	}
	commandCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command("cmd.exe", "/d", "/s", "/c", input.Command)
	} else {
		command = exec.Command("/bin/sh", "-c", input.Command)
	}
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
	deadline, _ := commandCtx.Deadline()
	sum := sha256.Sum256([]byte(input.Command))
	caller, _ := tool.CallerFromContext(ctx)
	snap := ShellExecutionSnapshot{SessionID: caller.SessionID, RunID: caller.TeamRunID, AgentID: caller.AgentID, ToolCallID: call.ID, CommandHash: hex.EncodeToString(sum[:]), State: "running", PID: command.Process.Pid, PGID: supervisor.owner.PGID(), JobID: supervisor.owner.JobID(), StartedAt: time.Now(), Deadline: deadline, ExitCode: -1}
	registryKey := fmt.Sprintf("%s/%d", call.ID, command.Process.Pid)
	d.runtime.mu.Lock()
	d.runtime.active[registryKey] = snap
	d.runtime.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- supervisor.Wait() }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var err error
	reason := ""
	var terminationErr error
	finished := false
	for !finished && reason == "" {
		select {
		case err = <-done:
			finished = true
		case <-d.runtime.ctx.Done():
			reason = "application_shutdown"
		case <-ctx.Done():
			reason = "context_cancelled"
		case <-commandCtx.Done():
			reason = "timeout"
		case <-limitHit:
			if d.runtime.opts.StopOnOutputLimit {
				reason = "output_limit"
			}
		case <-ticker.C:
			d.runtime.mu.Lock()
			current := d.runtime.active[registryKey]
			current.OutputBytes = output.Total()
			d.runtime.active[registryKey] = current
			d.runtime.mu.Unlock()
			if sink != nil {
				if sinkErr := sink(tool.Update{Kind: "progress", Message: fmt.Sprintf("%d output bytes", output.Total())}); sinkErr != nil {
					reason = "update_sink_failure"
				}
			}
		}
	}
	if reason != "" {
		d.runtime.mu.Lock()
		current := d.runtime.active[registryKey]
		current.State, current.Reason, current.OutputBytes = "stopping", reason, output.Total()
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
	snap.State, snap.Reason, snap.ExitCode, snap.OutputBytes, snap.Output = status, reason, exitCode, total, contextOut
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
	if sink != nil {
		if sinkErr := sink(tool.Update{Kind: "finished", Message: fmt.Sprintf("exit %d (%s)", exitCode, status)}); sinkErr != nil {
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
	truncated, notified                bool
	onLimit                            func()
}

func (b *boundedShellBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(value)
	b.total += n
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
func (b *boundedShellBuffer) Total() int { b.mu.Lock(); defer b.mu.Unlock(); return b.total }
func (b *boundedShellBuffer) Values() (string, string, int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.context.String(), b.artifact.String(), b.total, b.truncated
}
