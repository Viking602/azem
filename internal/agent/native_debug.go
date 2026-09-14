package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

const ToolDebug = "debug"

type debugDriver struct {
	root          string
	host          *nativeHost
	networkPolicy string
}

func (*debugDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolDebug, Description: "Persistent native Debug Adapter Protocol session. start launches a workspace program with the installed lldb-dap (native binaries) or Python debugpy adapter. Set breakpoints at launch or with breakpoints; inspect threads/stack/scopes/variables, evaluate, continue/next/step_in/step_out/pause, wait for events, or stop. Execution requires host approval. State is isolated per conversation and agent.", InputSchema: tool.Schema{Type: "object", Required: []string{"action"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
		"action":  {Type: "string", Enum: []string{"start", "breakpoints", "threads", "stack", "scopes", "variables", "evaluate", "continue", "next", "step_in", "step_out", "pause", "wait", "stop"}},
		"adapter": {Type: "string", Enum: []string{"lldb", "python"}}, "program": {Type: "string"}, "args": {Type: "array", Items: &tool.Schema{Type: "string"}}, "file": {Type: "string"}, "lines": {Type: "array", Items: &tool.Schema{Type: "integer"}}, "thread_id": {Type: "integer"}, "frame_id": {Type: "integer"}, "variables_reference": {Type: "integer"}, "expression": {Type: "string"}, "timeout": {Type: "integer", Description: "1-300 seconds, default 30."},
	}}, Concurrency: tool.ConcurrencyParallel}
}
func (*debugDriver) ToolPolicy() agentruntime.ToolPolicy {
	return approvedExternalPolicy("coding", "debug", "execute")
}

type dapEnvelope struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	RequestSeq int             `json:"request_seq"`
	Success    bool            `json:"success"`
	Command    string          `json:"command"`
	Message    string          `json:"message"`
	Event      string          `json:"event"`
	Body       json.RawMessage `json:"body"`
}

func (p *nativeProcess) dapSend(command string, arguments any) (int, error) {
	p.seq++
	return p.seq, p.writeFrame(map[string]any{"seq": p.seq, "type": "request", "command": command, "arguments": arguments})
}

func (p *nativeProcess) dapRead() (dapEnvelope, error) {
	payload, err := p.readFrame()
	if err != nil {
		return dapEnvelope{}, err
	}
	var envelope dapEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return envelope, err
	}
	if envelope.Type == "request" {
		p.seq++
		err := p.writeFrame(map[string]any{"seq": p.seq, "type": "response", "request_seq": envelope.Seq, "command": envelope.Command, "success": false, "message": "reverse execution requests are disabled; use internalConsole"})
		if err != nil {
			return envelope, err
		}
	}
	if envelope.Type == "event" {
		switch envelope.Event {
		case "stopped", "terminated", "exited":
			p.data["debug-state"] = envelope.Event
		case "continued":
			p.data["debug-state"] = "running"
		}
		if len(p.events) >= 64 {
			p.events = p.events[1:]
		}
		if len(payload) <= 64<<10 {
			p.events = append(p.events, payload)
		}
	}
	if envelope.Type == "response" && !envelope.Success {
		return envelope, fmt.Errorf("debug %s: %s %s", envelope.Command, envelope.Message, envelope.Body)
	}
	return envelope, nil
}

func (p *nativeProcess) dapWait(id int) (json.RawMessage, error) {
	key := fmt.Sprintf("dap-response-%d", id)
	if saved, ok := p.data[key]; ok {
		delete(p.data, key)
		return json.RawMessage(saved), nil
	}
	for count := 0; count < 4096; count++ {
		envelope, err := p.dapRead()
		if err != nil {
			return nil, err
		}
		if envelope.Type != "response" {
			continue
		}
		if envelope.RequestSeq == id {
			return envelope.Body, nil
		}
		if len(p.data) >= 16 || len(envelope.Body) > 256<<10 {
			return nil, errors.New("debug response buffer exceeded")
		}
		p.data[fmt.Sprintf("dap-response-%d", envelope.RequestSeq)] = string(envelope.Body)
	}
	return nil, errors.New("debug event budget exceeded")
}

func (p *nativeProcess) dapCall(command string, arguments any) (json.RawMessage, error) {
	id, err := p.dapSend(command, arguments)
	if err != nil {
		return nil, err
	}
	return p.dapWait(id)
}

func (d *debugDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Action             string   `json:"action"`
		Adapter            string   `json:"adapter"`
		Program            string   `json:"program"`
		Args               []string `json:"args"`
		File               string   `json:"file"`
		Lines              []int    `json:"lines"`
		ThreadID           int      `json:"thread_id"`
		FrameID            int      `json:"frame_id"`
		VariablesReference int      `json:"variables_reference"`
		Expression         string   `json:"expression"`
		Timeout            int      `json:"timeout"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if len(call.Arguments) > 1<<20 || input.Timeout < 0 || input.Timeout > 300 || len(input.Lines) > 1000 {
		return toolError(call, "debug arguments exceed bounds"), nil
	}
	if d.networkPolicy == "deny" && input.Action != "stop" {
		return toolError(call, "debug execution cannot enforce network denial; use a network-isolated host"), nil
	}
	if input.Timeout == 0 {
		input.Timeout = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()
	key := nativeScope(ctx, d.root, "debug", "adapter")
	d.host.mu.Lock()
	p := d.host.processes[key]
	d.host.mu.Unlock()
	if input.Action == "stop" {
		if p != nil {
			p.close()
			select {
			case <-p.done:
			case <-ctx.Done():
				return toolError(call, ctx.Err().Error()), nil
			}
		}
		return nativeJSONResult(call, map[string]bool{"stopped": true})
	}
	if input.Action == "start" {
		if p != nil {
			select {
			case <-p.done:
			default:
				return toolError(call, "stop the current debug session before starting another"), nil
			}
		}
		absolute, _, info, err := secureReadPath(d.root, input.Program)
		if err != nil || !info.Mode().IsRegular() {
			return toolError(call, "program must be a regular workspace file"), nil
		}
		input.Program = absolute
		var program string
		var args []string
		switch input.Adapter {
		case "python":
			program, args = "python3", []string{"-I", "-m", "debugpy.adapter"}
		case "", "lldb":
			program, err = nativeExecutable("lldb-dap")
			if err != nil {
				program, err = nativeExecutable("lldb-vscode")
			}
			if err != nil {
				if discovered, lookupErr := nativeRun(ctx, d.root, "", "/usr/bin/xcrun", "--find", "lldb-dap"); lookupErr == nil {
					program, err = strings.TrimSpace(discovered), nil
				}
			}
			if err != nil {
				return toolError(call, "install the native lldb-dap executable"), nil
			}
		default:
			return toolError(call, "adapter must be lldb or python"), nil
		}
		p, err = d.host.start(key, d.root, program, args, false)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
	} else if p == nil {
		return toolError(call, "start a debug session first"), nil
	}
	unlock, err := p.lock(ctx)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	defer unlock()
	failed := true
	defer func() {
		if failed {
			p.close()
		}
	}()
	var result json.RawMessage
	if input.Action == "start" {
		_, err = p.dapCall("initialize", map[string]any{"clientID": "azem", "adapterID": input.Adapter, "pathFormat": "path", "linesStartAt1": true, "columnsStartAt1": true, "supportsRunInTerminalRequest": false})
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		launch := map[string]any{"program": input.Program, "args": input.Args, "cwd": d.root, "console": "internalConsole", "stopOnEntry": true}
		if canonical, resolveErr := filepath.EvalSymlinks(d.root); resolveErr == nil && canonical != d.root && input.Adapter != "python" {
			launch["sourceMap"] = map[string]string{d.root: canonical}
		}
		launchID, err := p.dapSend("launch", launch)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		initialized := false
		for _, event := range p.events {
			var envelope dapEnvelope
			if json.Unmarshal(event, &envelope) == nil && envelope.Event == "initialized" {
				initialized = true
			}
		}
		for count := 0; !initialized && count < 1024; count++ {
			envelope, err := p.dapRead()
			if err != nil {
				return toolError(call, err.Error()), nil
			}
			if envelope.Type == "response" {
				if len(p.data) >= 16 || len(envelope.Body) > 256<<10 {
					return toolError(call, "debug response buffer exceeded"), nil
				}
				p.data[fmt.Sprintf("dap-response-%d", envelope.RequestSeq)] = string(envelope.Body)
			}
			if envelope.Event == "initialized" {
				initialized = true
				break
			}
		}
		if !initialized {
			return toolError(call, "adapter did not initialize"), nil
		}
		var breakpoints json.RawMessage
		if input.File != "" {
			breakpoints, err = d.setBreakpoints(p, input.File, input.Lines)
			if err != nil {
				return toolError(call, err.Error()), nil
			}
		}
		if _, err := p.dapCall("configurationDone", map[string]any{}); err != nil {
			return toolError(call, err.Error()), nil
		}
		result, err = p.dapWait(launchID)
		if err == nil && len(breakpoints) > 0 {
			result, err = json.Marshal(map[string]any{"launch": result, "breakpoints": breakpoints})
		}
	} else {
		methods := map[string]string{"threads": "threads", "stack": "stackTrace", "scopes": "scopes", "variables": "variables", "evaluate": "evaluate", "continue": "continue", "next": "next", "step_in": "stepIn", "step_out": "stepOut", "pause": "pause"}
		switch input.Action {
		case "breakpoints":
			result, err = d.setBreakpoints(p, input.File, input.Lines)
		case "wait":
			for count := 0; p.data["debug-state"] != "stopped" && p.data["debug-state"] != "terminated" && p.data["debug-state"] != "exited" && count < 4096 && err == nil; count++ {
				_, err = p.dapRead()
			}
		default:
			method, ok := methods[input.Action]
			if !ok {
				return toolError(call, "unsupported debug action"), nil
			}
			args := map[string]any{}
			switch input.Action {
			case "stack", "continue", "next", "step_in", "step_out", "pause":
				if input.ThreadID < 1 {
					return toolError(call, "thread_id is required"), nil
				}
				args["threadId"] = input.ThreadID
			case "scopes":
				args["frameId"] = input.FrameID
			case "variables":
				if input.VariablesReference < 1 {
					return toolError(call, "variables_reference is required"), nil
				}
				args["variablesReference"], args["count"] = input.VariablesReference, 100
			case "evaluate":
				if strings.TrimSpace(input.Expression) == "" {
					return toolError(call, "expression is required"), nil
				}
				args["expression"], args["context"] = input.Expression, "repl"
				if input.FrameID != 0 {
					args["frameId"] = input.FrameID
				}
			}
			if input.Action == "stack" {
				args["levels"] = 50
			}
			if input.Action == "continue" || input.Action == "next" || input.Action == "step_in" || input.Action == "step_out" {
				p.data["debug-state"] = "running"
			}
			result, err = p.dapCall(method, args)
		}
	}
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	failed = false
	events := append([]json.RawMessage(nil), p.events...)
	p.events = nil
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	return nativeJSONResult(call, map[string]any{"result": result, "events": events, "state": p.data["debug-state"]})
}

func (d *debugDriver) setBreakpoints(p *nativeProcess, path string, lines []int) (json.RawMessage, error) {
	absolute, _, info, err := secureReadPath(d.root, path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("breakpoints require a regular workspace source file")
	}
	breakpoints := make([]map[string]int, 0, len(lines))
	for _, line := range lines {
		if line < 1 {
			return nil, errors.New("breakpoint lines must be positive")
		}
		breakpoints = append(breakpoints, map[string]int{"line": line})
	}
	return p.dapCall("setBreakpoints", map[string]any{"source": map[string]string{"name": filepath.Base(absolute), "path": absolute}, "breakpoints": breakpoints})
}
