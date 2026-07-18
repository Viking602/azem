package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxCapture = 64 << 10

type boundedBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remain := maxCapture - b.buffer.Len()
	if remain > 0 {
		if remain > len(p) {
			remain = len(p)
		}
		_, _ = b.buffer.Write(p[:remain])
	}
	if len(p) > remain {
		b.truncated = true
	}
	return n, nil
}

func (b *boundedBuffer) String() string { return b.buffer.String() }

type Output struct {
	Async              bool   `json:"async,omitempty"`
	AsyncTimeout       int    `json:"asyncTimeout,omitempty"`
	Continue           *bool  `json:"continue,omitempty"`
	SuppressOutput     bool   `json:"suppressOutput,omitempty"`
	StopReason         string `json:"stopReason,omitempty"`
	Decision           string `json:"decision,omitempty"`
	Reason             string `json:"reason,omitempty"`
	SystemMessage      string `json:"systemMessage,omitempty"`
	HookSpecificOutput struct {
		HookEventName            Event                     `json:"hookEventName,omitempty"`
		PermissionDecision       string                    `json:"permissionDecision,omitempty"`
		PermissionDecisionReason string                    `json:"permissionDecisionReason,omitempty"`
		UpdatedInput             json.RawMessage           `json:"updatedInput,omitempty"`
		AdditionalContext        string                    `json:"additionalContext,omitempty"`
		UpdatedMCPToolOutput     any                       `json:"updatedMCPToolOutput,omitempty"`
		WatchPaths               []string                  `json:"watchPaths,omitempty"`
		InitialUserMessage       string                    `json:"initialUserMessage,omitempty"`
		WorktreePath             string                    `json:"worktreePath,omitempty"`
		Decision                 PermissionRequestDecision `json:"decision,omitempty"`
		Action                   string                    `json:"action,omitempty"`
		Content                  any                       `json:"content,omitempty"`
		Retry                    bool                      `json:"retry,omitempty"`
	} `json:"hookSpecificOutput,omitempty"`
}

type PermissionRequestDecision struct {
	Behavior           string          `json:"behavior,omitempty"`
	UpdatedInput       json.RawMessage `json:"updatedInput,omitempty"`
	UpdatedPermissions any             `json:"updatedPermissions,omitempty"`
	Message            string          `json:"message,omitempty"`
	Interrupt          bool            `json:"interrupt,omitempty"`
}
type RunResult struct {
	Event               Event
	Name                string
	Source              string
	SessionID           string
	RunID               string
	AgentID             string
	ToolCallID          string
	ToolName            string
	Duration            time.Duration
	ExitCode            int
	Stdout              string
	Stderr              string
	StdoutTruncated     bool
	StderrTruncated     bool
	Output              Output
	Denied              bool
	Async               bool
	PreventContinuation bool
	StopReason          string
	Failure             error
}
type Runner struct {
	Workspace   string
	Environment []string
}

func (r Runner) Run(ctx context.Context, c Command, e Envelope) (result RunResult) {
	started := time.Now()
	result = RunResult{
		Event: c.Event, Name: c.Name, Source: c.Source, SessionID: e.SessionID, RunID: e.RunID,
		AgentID: e.AgentID, ToolCallID: e.ToolCallID, ToolName: e.ToolName, ExitCode: -1, Async: c.Async,
	}
	defer func() { result.Duration = time.Since(started) }()
	if strings.TrimSpace(e.CWD) == "" {
		e.CWD = r.Workspace
	}
	input, err := marshalEnvelope(e)
	if err != nil {
		result.Failure = err
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	var cmd *exec.Cmd
	if len(c.Args) > 0 {
		cmd = exec.CommandContext(ctx, c.RawCommand, c.Args...)
	} else if c.Shell == "powershell" {
		cmd = exec.CommandContext(ctx, "pwsh", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", c.RawCommand)
	} else if runtime.GOOS == "windows" {
		bash := windowsBash()
		if bash == "" {
			result.Failure = errors.New("bash hook requires Git Bash on Windows")
			return result
		}
		cmd = exec.CommandContext(ctx, bash, "-lc", c.RawCommand)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-lc", c.RawCommand)
	}
	configureCommand(cmd)
	cmd.WaitDelay = time.Second
	cmd.Dir = r.Workspace
	if strings.TrimSpace(e.CWD) != "" {
		cmd.Dir = e.CWD
	}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr boundedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	env := append(os.Environ(), r.Environment...)
	env = append(env, "AZEM_HOOK_EVENT="+string(e.HookEventName), "AZEM_HOOK_NAME="+c.Name, "AZEM_SESSION_ID="+e.SessionID, "AZEM_RUN_ID="+e.RunID, "AZEM_AGENT_ID="+e.AgentID, "AZEM_WORKSPACE_ROOT="+r.Workspace, "CLAUDE_PROJECT_DIR="+r.Workspace)
	cmd.Env = env
	err = cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.StdoutTruncated = stdout.truncated
	result.StderrTruncated = stderr.truncated
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if result.ExitCode == 2 {
		result.Denied = true
		result.Output.Reason = strings.TrimSpace(result.Stderr)
		return result
	}
	trim := strings.TrimSpace(result.Stdout)
	if strings.HasPrefix(trim, "{") {
		var rawOutput struct {
			HookSpecificOutput json.RawMessage `json:"hookSpecificOutput"`
		}
		if jerr := json.Unmarshal([]byte(trim), &rawOutput); jerr != nil {
			result.Failure = fmt.Errorf("parse hook JSON output: %w", jerr)
			return result
		}
		if jerr := json.Unmarshal([]byte(trim), &result.Output); jerr != nil {
			result.Failure = fmt.Errorf("parse hook JSON output: %w", jerr)
			return result
		}
		if result.Output.Async {
			result.Failure = errors.New("dynamic async hook output is not supported; configure async: true in settings")
			return result
		}
		if len(rawOutput.HookSpecificOutput) > 0 && result.Output.HookSpecificOutput.HookEventName == "" {
			result.Failure = errors.New("hookSpecificOutput.hookEventName is required")
			return result
		}
		if outputEvent := result.Output.HookSpecificOutput.HookEventName; outputEvent != "" && outputEvent != e.HookEventName {
			result.Failure = fmt.Errorf("hook returned event %q for %q", outputEvent, e.HookEventName)
			return result
		}
		if result.Output.Continue != nil && !*result.Output.Continue {
			result.PreventContinuation = true
			result.StopReason = firstNonempty(strings.TrimSpace(result.Output.StopReason), strings.TrimSpace(result.Output.Reason))
			return result
		}
		pd := result.Output.HookSpecificOutput.PermissionDecision
		if pd == "deny" || result.Output.Decision == "block" {
			result.Denied = true
			return result
		}
		if pd != "" && pd != "allow" && pd != "ask" {
			result.Failure = fmt.Errorf("invalid permissionDecision %q", pd)
			return result
		}
		if result.Output.Decision != "" && result.Output.Decision != "approve" && result.Output.Decision != "block" {
			result.Failure = fmt.Errorf("invalid decision %q", result.Output.Decision)
			return result
		}
		if validationErr := validateSpecificOutput(e.HookEventName, result.Output); validationErr != nil {
			result.Failure = validationErr
			return result
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			result.Failure = ctx.Err()
		} else {
			result.Failure = err
		}
		return result
	}
	return result
}

func windowsBash() string {
	if path, err := exec.LookPath("bash.exe"); err == nil {
		return path
	}
	for _, path := range []string{`C:\Program Files\Git\bin\bash.exe`, `C:\Program Files\Git\usr\bin\bash.exe`} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func validateSpecificOutput(event Event, output Output) error {
	specific := output.HookSpecificOutput
	if len(specific.WatchPaths) > 0 {
		if event != SessionStart && event != CwdChanged && event != FileChanged {
			return fmt.Errorf("watchPaths is not supported for %s", event)
		}
		for _, path := range specific.WatchPaths {
			if !filepath.IsAbs(path) {
				return fmt.Errorf("watchPaths entry %q is not absolute", path)
			}
		}
	}
	if specific.Action != "" {
		if event != Elicitation && event != ElicitationResult {
			return fmt.Errorf("elicitation action is not supported for %s", event)
		}
		if specific.Action != "accept" && specific.Action != "decline" && specific.Action != "cancel" {
			return fmt.Errorf("invalid elicitation action %q", specific.Action)
		}
	}
	decision := specific.Decision
	if decision.Behavior != "" {
		if event != PermissionRequest {
			return fmt.Errorf("permission request decision is not supported for %s", event)
		}
		if decision.Behavior != "allow" && decision.Behavior != "deny" {
			return fmt.Errorf("invalid permission request behavior %q", decision.Behavior)
		}
	}
	return nil
}
