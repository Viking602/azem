package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/venat/tool"
)

func nativeHubOperation(op string, params map[string]any) bool {
	name, _ := params["name"].(string)
	return name != "" || op == "start" || op == "ps" || op == "logs" || op == "stop" || op == "restart" || op == "describe"
}

func (d *hubDriver) executeProcess(ctx context.Context, call tool.Call, op string) (tool.Result, error) {
	var input struct {
		Name        string   `json:"name"`
		Application string   `json:"application"`
		Args        []string `json:"args"`
		CWD         string   `json:"cwd"`
		Text        string   `json:"text"`
		Enter       bool     `json:"enter"`
		Timeout     int      `json:"timeoutMs"`
		To          string   `json:"to"`
		From        string   `json:"from"`
		IDs         []string `json:"ids"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return hubError(call, err), nil
	}
	if input.To != "" || input.From != "" || len(input.IDs) > 0 {
		return hubError(call, errors.New("process name cannot be combined with peer or job targets")), nil
	}
	if d.native == nil {
		return hubError(call, errors.New("native process host is unavailable")), nil
	}
	if len(call.Arguments) > 1<<20 || input.Timeout < 0 || input.Timeout > 300000 {
		return hubError(call, errors.New("process arguments exceed bounds")), nil
	}
	prefix := nativeScope(ctx, d.root, "hub", "")
	if op == "ps" {
		d.native.mu.Lock()
		var results []map[string]any
		for key, p := range d.native.processes {
			if strings.HasPrefix(key, prefix) {
				results = append(results, nativeProcessStatus(strings.TrimPrefix(key, prefix), p))
			}
		}
		d.native.mu.Unlock()
		sort.Slice(results, func(i, j int) bool { return results[i]["name"].(string) < results[j]["name"].(string) })
		return nativeJSONResult(call, map[string]any{"processes": results})
	}
	if input.Name == "" || len(input.Name) > 64 || strings.ContainsAny(input.Name, "\x00/\\\r\n") {
		return hubError(call, errors.New("process name must contain 1-64 characters without path separators")), nil
	}
	key := prefix + input.Name
	d.native.mu.Lock()
	p := d.native.processes[key]
	d.native.mu.Unlock()
	if op == "start" || op == "restart" {
		if d.shellPolicy == "deny" || d.networkPolicy == "deny" {
			return hubError(call, errors.New("native processes are denied by workspace execution/network policy")), nil
		}
		if p != nil {
			if op == "start" {
				select {
				case <-p.done:
				default:
					return hubError(call, errors.New("process name already running; use send or stop")), nil
				}
			}
			if op == "restart" {
				if input.Application == "" {
					input.Application = p.supervisor.command.Path
					input.Args = append([]string(nil), p.supervisor.command.Args[1:]...)
					input.CWD = p.supervisor.command.Dir
				}
				p.close()
				select {
				case <-p.done:
				case <-ctx.Done():
					return hubError(call, ctx.Err()), nil
				}
			}
		}
		if input.Application == "" || len(input.Application) > 4096 || len(input.Args) > 256 {
			return hubError(call, errors.New("application is required; at most 256 args")), nil
		}
		cwd := d.root
		if input.CWD != "" {
			absolute, _, info, err := secureReadPath(d.root, input.CWD)
			if err != nil || !info.IsDir() {
				return hubError(call, errors.New("cwd must be a workspace directory")), nil
			}
			cwd = absolute
		}
		var err error
		p, err = d.native.start(key, cwd, input.Application, input.Args, true)
		if err != nil {
			return hubError(call, err), nil
		}
		return nativeJSONResult(call, nativeProcessStatus(input.Name, p))
	}
	if p == nil {
		return hubError(call, fmt.Errorf("process %q is unavailable", input.Name)), nil
	}
	switch op {
	case "logs":
		return nativeJSONResult(call, map[string]any{"process": nativeProcessStatus(input.Name, p), "stdout": p.logs.text(), "stderr": p.stderr.text()})
	case "describe":
		return nativeJSONResult(call, nativeProcessStatus(input.Name, p))
	case "stop":
		p.close()
		select {
		case <-p.done:
		case <-ctx.Done():
			return hubError(call, ctx.Err()), nil
		}
	case "send":
		if input.Enter {
			input.Text += "\n"
		}
		if input.Text == "" {
			return hubError(call, errors.New("text is required")), nil
		}
		unlock, err := p.lock(ctx)
		if err != nil {
			return hubError(call, err), nil
		}
		defer unlock()
		if _, err := io.WriteString(p.stdin, input.Text); err != nil {
			return hubError(call, err), nil
		}
	case "wait":
		if input.Timeout == 0 {
			input.Timeout = 30000
		}
		timer := time.NewTimer(time.Duration(input.Timeout) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-p.done:
		case <-ctx.Done():
			return hubError(call, ctx.Err()), nil
		case <-timer.C:
		}
	default:
		return hubError(call, fmt.Errorf("unsupported process operation %q", op)), nil
	}
	return nativeJSONResult(call, nativeProcessStatus(input.Name, p))
}

func nativeProcessStatus(name string, p *nativeProcess) map[string]any {
	state := "running"
	exitCode := -1
	select {
	case <-p.done:
		state = "completed"
		exitCode = 0
		if p.err != nil {
			state = "failed"
			exitCode = p.supervisor.command.ProcessState.ExitCode()
		}
	default:
	}
	return map[string]any{"name": name, "pid": p.supervisor.command.Process.Pid, "state": state, "exitCode": exitCode, "startedAt": p.started.UTC(), "cwd": p.supervisor.command.Dir}
}

func (h *nativeHost) activeProcesses() []ShellExecutionSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	var result []ShellExecutionSnapshot
	for key, p := range h.processes {
		parts := strings.Split(key, "\x00")
		if len(parts) != 5 || parts[3] != "hub" {
			continue
		}
		select {
		case <-p.done:
			continue
		default:
		}
		result = append(result, ShellExecutionSnapshot{SessionID: parts[1], AgentID: parts[2], ToolCallID: "hub:" + parts[4], PID: p.supervisor.command.Process.Pid, StartedAt: p.started, State: "running", ExitCode: -1})
	}
	return result
}
