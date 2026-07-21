package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/go-hydaelyn/tool"
)

const ToolShell = "coding.shell"

const maxShellOutput = 1 << 20

type shellDriver struct {
	root         string
	approval     string
	allowNetwork string
}

type shellInput struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Network        bool   `json:"network,omitempty"`
}

type shellOutput struct {
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

func newShellDriver(root, approval, allowNetwork string) tool.Driver {
	return &shellDriver{root: root, approval: approval, allowNetwork: allowNetwork}
}

func (d *shellDriver) Definition() tool.Definition {
	additional := false
	definition := tool.Definition{
		Name:        ToolShell,
		Description: "Run a foreground command such as git, build, test, or workspace inspection. Do not use shell redirection, cat, tee, touch, or scripts to create or edit files: create files with coding.write_file and modify existing files with coding.edit_hashline. Set network=true when the command may access the network.",
		InputSchema: tool.Schema{
			Type: "object",
			Properties: map[string]tool.Schema{
				"command":         {Type: "string", Description: "The exact shell command to run."},
				"timeout_seconds": {Type: "integer", Description: "Optional timeout from 1 to 600 seconds."},
				"network":         {Type: "boolean", Description: "Whether this command may access the network."},
			},
			Required:             []string{"command"},
			AdditionalProperties: &additional,
		},
		EffectType:         tool.EffectExternalSideEffect,
		RequiresApproval:   d.approval != "allow",
		RequiresActionTask: true,
		RiskLevel:          "high",
		Timeout:            10 * time.Minute,
		PolicyTags:         []string{"coding", "shell", "workspace"},
		Metadata:           map[string]string{"approval": d.approval, "network": d.allowNetwork},
	}
	return definition
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
	if d.approval == "deny" {
		return shellError(call, "shell commands are disabled by workspace.shell_policy"), nil
	}
	if input.Network && d.allowNetwork == "deny" {
		return shellError(call, "network access is disabled by workspace.allow_network"), nil
	}
	timeout := 2 * time.Minute
	if input.TimeoutSeconds != 0 {
		if input.TimeoutSeconds < 1 || input.TimeoutSeconds > 600 {
			return shellError(call, "timeout_seconds must be between 1 and 600"), nil
		}
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(commandCtx, "cmd.exe", "/d", "/s", "/c", input.Command)
	} else {
		command = exec.CommandContext(commandCtx, "/bin/sh", "-lc", input.Command)
	}
	configureShellCommand(command)
	command.WaitDelay = time.Second
	command.Dir = d.root
	output := &boundedShellBuffer{limit: maxShellOutput}
	command.Stdout = output
	command.Stderr = output
	if sink != nil {
		_ = sink(tool.Update{Kind: "started", Message: input.Command, Data: map[string]string{"cwd": d.root}})
	}
	err := command.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	value := shellOutput{ExitCode: exitCode, Output: output.String(), Truncated: output.Truncated()}
	structured, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return tool.Result{}, marshalErr
	}
	result := tool.Result{ToolCallID: call.ID, Name: call.Name, Content: value.Output, Structured: structured, IsError: err != nil}
	if commandCtx.Err() != nil {
		result.Content = strings.TrimSpace(result.Content + "\ncommand stopped: " + commandCtx.Err().Error())
	}
	if sink != nil {
		_ = sink(tool.Update{Kind: "finished", Message: fmt.Sprintf("exit %d", exitCode)})
	}
	return result, nil
}

func shellError(call tool.Call, message string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "coding.shell rejected: " + message, IsError: true}
}

type boundedShellBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedShellBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(value)
	return original, nil
}

func (b *boundedShellBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *boundedShellBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
