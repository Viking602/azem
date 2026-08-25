package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/venat/tool"
)

const ToolDebug = "debug"

var (
	debugActions = map[string]bool{
		"launch": true, "attach": true, "set_breakpoint": true, "remove_breakpoint": true,
		"set_instruction_breakpoint": true, "remove_instruction_breakpoint": true,
		"data_breakpoint_info": true, "set_data_breakpoint": true, "remove_data_breakpoint": true,
		"continue": true, "step_over": true, "step_in": true, "step_out": true, "pause": true,
		"evaluate": true, "stack_trace": true, "threads": true, "scopes": true, "variables": true,
		"disassemble": true, "read_memory": true, "write_memory": true, "modules": true,
		"loaded_sources": true, "custom_request": true, "output": true, "terminate": true, "sessions": true,
	}
	debugReadOnlyActions = map[string]bool{
		"output": true, "threads": true, "stack_trace": true, "scopes": true, "variables": true,
		"disassemble": true, "read_memory": true, "loaded_sources": true, "modules": true, "sessions": true,
	}
)

type debugDriver struct {
	root     string
	bridge   *lspBridgeRuntime
	readOnly bool
}

type debugToolResult struct {
	Action  string          `json:"action"`
	Success bool            `json:"success"`
	Content string          `json:"content"`
	Details json.RawMessage `json:"details,omitempty"`
}

func newDebugDriver(root string, bridge *lspBridgeRuntime, readOnly bool) tool.Driver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	return &debugDriver{root: root, bridge: bridge, readOnly: readOnly}
}

func ReadOnlyDebugDriver(driver tool.Driver) tool.Driver {
	if current, ok := driver.(*debugDriver); ok {
		return newDebugDriver(current.root, current.bridge, true)
	}
	return driver
}

func (driver *debugDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolDebug,
		Description: "Debugger Adapter Protocol access. One active session: launch/attach, breakpoints, stepping, pause/continue, evaluate, threads, stack_trace, scopes, variables, memory, disassembly, modules, loaded_sources, custom requests, output, sessions, and terminate. program is a target path, never a shell command.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"action"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"action": {Type: "string"}, "program": {Type: "string"}, "args": {Type: "array", Items: &tool.Schema{Type: "string"}},
				"adapter": {Type: "string"}, "cwd": {Type: "string"}, "file": {Type: "string"}, "line": {Type: "integer"},
				"function": {Type: "string"}, "name": {Type: "string"}, "condition": {Type: "string"}, "hit_condition": {Type: "string"},
				"expression": {Type: "string"}, "context": {Type: "string"}, "frame_id": {Type: "integer"}, "scope_id": {Type: "integer"},
				"variable_ref": {Type: "integer"}, "pid": {Type: "integer"}, "port": {Type: "integer"}, "host": {Type: "string"}, "levels": {Type: "integer"},
				"memory_reference": {Type: "string"}, "instruction_reference": {Type: "string"}, "instruction_count": {Type: "integer"},
				"instruction_offset": {Type: "integer"}, "count": {Type: "integer"}, "data": {Type: "string"}, "data_id": {Type: "string"},
				"access_type": {Type: "string"}, "command": {Type: "string"}, "arguments": {Type: "object"}, "offset": {Type: "integer"},
				"resolve_symbols": {Type: "boolean"}, "allow_partial": {Type: "boolean"}, "start_module": {Type: "integer"},
				"module_count": {Type: "integer"}, "timeout": {Type: "integer", Description: "Seconds, default 20, range 5-300."},
			},
		},
		EffectType: tool.EffectReadOnly, RiskLevel: "low", PolicyTags: []string{"coding", "debug", "dap"}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "debug-session",
	}
}

func (driver *debugDriver) DefinitionForCall(call tool.Call) tool.Definition {
	definition := driver.Definition()
	var input struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(call.Arguments, &input) != nil || !debugReadOnlyActions[strings.ToLower(strings.TrimSpace(input.Action))] {
		definition.EffectType = tool.EffectExternalSideEffect
		definition.RequiresActionTask = true
		definition.RequiresApproval = true
		definition.RiskLevel = "high"
	}
	return definition
}

func (driver *debugDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var params map[string]any
	if err := json.Unmarshal(call.Arguments, &params); err != nil {
		return debugError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	action, _ := params["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if !debugActions[action] {
		return debugError(call, fmt.Errorf("unsupported action %q", action)), nil
	}
	params["action"] = action
	if driver.readOnly && !debugReadOnlyActions[action] {
		return debugError(call, fmt.Errorf("action %s is disabled in this read-only session", action)), nil
	}
	timeout, err := normalizeDebugTimeout(params["timeout"])
	if err != nil {
		return debugError(call, err), nil
	}
	params["timeout"] = timeout
	if err := validateDebugPaths(driver.root, action, params); err != nil {
		return debugError(call, err), nil
	}
	driver.bridge.debugMu.Lock()
	defer driver.bridge.debugMu.Unlock()
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	response, err := driver.bridge.requestTool(requestCtx, ToolDebug, driver.root, driver.readOnly, params)
	if err != nil {
		return debugError(call, err), nil
	}
	result := debugToolResult{Action: action, Success: !response.IsError, Content: response.Content, Details: response.Details}
	if len(response.Details) > 0 {
		var details struct {
			Success *bool `json:"success"`
		}
		if json.Unmarshal(response.Details, &details) == nil && details.Success != nil {
			result.Success = *details.Success
		}
	}
	structured, _ := json.Marshal(result)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: response.Content, Structured: structured, IsError: !result.Success}, nil
}

func normalizeDebugTimeout(value any) (int, error) {
	timeout := 20
	if value != nil {
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) {
			return 0, errors.New("timeout must be an integer number of seconds")
		}
		timeout = int(number)
	}
	if timeout < 5 || timeout > 300 {
		return 0, errors.New("timeout must be between 5 and 300 seconds")
	}
	return timeout, nil
}

func validateDebugPaths(root, action string, params map[string]any) error {
	cwd := root
	if value, ok := params["cwd"].(string); ok && strings.TrimSpace(value) != "" {
		absolute, relative, info, err := secureReadPath(root, value)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("debug cwd is not a directory")
		}
		cwd = absolute
		params["cwd"] = relative
	}
	if action == "launch" {
		program, _ := params["program"].(string)
		if strings.TrimSpace(program) == "" {
			return errors.New("program is required for launch")
		}
		candidate := program
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cwd, filepath.FromSlash(candidate))
		}
		absolute, _, info, err := secureReadPath(root, candidate)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return errors.New("debug program is not a regular file or directory")
		}
		params["program"] = absolute
	}
	if file, ok := params["file"].(string); ok && strings.TrimSpace(file) != "" {
		absolute, _, info, err := secureReadPath(root, file)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("debug source file is not regular")
		}
		params["file"] = absolute
	}
	if action == "attach" {
		if _, pid := params["pid"]; !pid {
			if _, port := params["port"]; !port {
				if adapter, _ := params["adapter"].(string); strings.TrimSpace(adapter) == "" {
					return errors.New("attach requires pid, port, or adapter")
				}
			}
		}
	}
	for _, key := range []string{"line", "pid", "port", "levels", "count", "instruction_count", "module_count"} {
		if value, ok := params[key].(float64); ok && (value < 0 || value != float64(int(value))) {
			return fmt.Errorf("%s must be a non-negative integer", key)
		}
	}
	return nil
}

func debugError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "debug failed: " + err.Error(), IsError: true}
}
