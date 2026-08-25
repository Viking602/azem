package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/venat/tool"
)

const ToolLSP = "lsp"

var (
	lspActions = map[string]bool{
		"diagnostics": true, "definition": true, "references": true, "hover": true,
		"symbols": true, "rename": true, "rename_file": true, "code_actions": true,
		"type_definition": true, "implementation": true, "status": true, "reload": true,
		"capabilities": true, "request": true,
	}
	lspReadOnlyActions = map[string]bool{
		"diagnostics": true, "definition": true, "references": true, "hover": true,
		"symbols": true, "type_definition": true, "implementation": true,
		"status": true, "capabilities": true,
	}
)

type lspDriver struct {
	root     string
	bridge   *lspBridgeRuntime
	readOnly bool
}

type lspInput struct {
	Action     string `json:"action"`
	File       string `json:"file,omitempty"`
	Line       int    `json:"line,omitempty"`
	Symbol     string `json:"symbol,omitempty"`
	Query      string `json:"query,omitempty"`
	NewName    string `json:"new_name,omitempty"`
	Apply      *bool  `json:"apply,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
	RawPayload string `json:"payload,omitempty"`
}

type lspToolResult struct {
	Action  string          `json:"action"`
	Success bool            `json:"success"`
	Content string          `json:"content"`
	Details json.RawMessage `json:"details,omitempty"`
}

func newLSPDriver(root string, bridge *lspBridgeRuntime, readOnly bool) tool.Driver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	return &lspDriver{root: root, bridge: bridge, readOnly: readOnly}
}

func ReadOnlyLSPDriver(driver tool.Driver) tool.Driver {
	if current, ok := driver.(*lspDriver); ok {
		return newLSPDriver(current.root, current.bridge, true)
	}
	return driver
}

func (driver *lspDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolLSP,
		Description: "Symbol-aware language-server operations: diagnostics, definition, references, hover, symbols, rename, rename_file, code_actions, type_definition, implementation, status, reload, capabilities, and raw request. Position operations use file + 1-based line + symbol; #N selects the Nth substring match.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"action"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"action":   {Type: "string"},
				"file":     {Type: "string"},
				"line":     {Type: "integer"},
				"symbol":   {Type: "string"},
				"query":    {Type: "string"},
				"new_name": {Type: "string"},
				"apply":    {Type: "boolean"},
				"timeout":  {Type: "integer", Description: "Seconds, default 20, range 5-300."},
				"payload":  {Type: "string", Description: "Raw JSON request params."},
			},
		},
		EffectType: tool.EffectReadOnly, RiskLevel: "low", PolicyTags: []string{"coding", "lsp"}, Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver *lspDriver) DefinitionForCall(call tool.Call) tool.Definition {
	definition := driver.Definition()
	var input struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(call.Arguments, &input) != nil || !lspReadOnlyActions[strings.ToLower(strings.TrimSpace(input.Action))] {
		definition.EffectType = tool.EffectWrite
		definition.RequiresActionTask = true
		definition.RiskLevel = "medium"
		definition.Concurrency = tool.ConcurrencyExclusive
		definition.ConcurrencyGroup = "workspace-files"
	}
	return definition
}

func (driver *lspDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input lspInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return lspError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	if !lspActions[input.Action] {
		return lspError(call, fmt.Errorf("unsupported action %q", input.Action)), nil
	}
	if driver.readOnly && !lspReadOnlyActions[input.Action] {
		return lspError(call, fmt.Errorf("action %s is disabled in this read-only session", input.Action)), nil
	}
	if input.Timeout == 0 {
		input.Timeout = 20
	}
	if input.Timeout < 5 || input.Timeout > 300 {
		return lspError(call, errors.New("timeout must be between 5 and 300 seconds")), nil
	}
	if len(input.RawPayload) > 1<<20 {
		return lspError(call, errors.New("payload exceeds 1 MiB")), nil
	}
	if err := validateLSPInputPath(driver.root, input); err != nil {
		return lspError(call, err), nil
	}
	params := map[string]any{"action": input.Action, "timeout": input.Timeout}
	if input.File != "" {
		params["file"] = input.File
	}
	if input.Line != 0 {
		params["line"] = input.Line
	}
	if input.Symbol != "" {
		params["symbol"] = input.Symbol
	}
	if input.Query != "" {
		params["query"] = input.Query
	}
	if input.NewName != "" {
		params["new_name"] = input.NewName
	}
	if input.Apply != nil {
		params["apply"] = *input.Apply
	}
	if input.RawPayload != "" {
		params["payload"] = input.RawPayload
	}
	if !lspReadOnlyActions[input.Action] {
		driver.bridge.mutationMu.Lock()
		defer driver.bridge.mutationMu.Unlock()
	}
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()
	response, err := driver.bridge.request(requestCtx, driver.root, driver.readOnly, params)
	if err != nil {
		return lspError(call, err), nil
	}
	result := lspToolResult{Action: input.Action, Success: !response.IsError, Content: response.Content, Details: response.Details}
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

func validateLSPInputPath(root string, input lspInput) error {
	fileOptional := input.Action == "status" || input.Action == "capabilities" || input.Action == "reload" || input.Action == "request" || input.Action == "symbols" || input.Action == "diagnostics"
	if strings.TrimSpace(input.File) == "" {
		if fileOptional {
			return nil
		}
		return errors.New("file is required for this action")
	}
	if input.File == "*" {
		if input.Action == "diagnostics" || input.Action == "symbols" || input.Action == "reload" || input.Action == "capabilities" || input.Action == "request" {
			return nil
		}
		return errors.New("file '*' is unsupported for this action")
	}
	if input.Action == "diagnostics" && (strings.ContainsAny(input.File, "*?[") || strings.Contains(input.File, "{")) {
		base, _ := splitASTGlob(input.File)
		_, _, info, err := secureReadPath(root, base)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("diagnostics glob base is not a directory")
		}
		return nil
	}
	_, relative, _, err := secureReadPath(root, input.File)
	if err != nil {
		return err
	}
	input.File = relative
	if input.Line < 0 {
		return errors.New("line must be positive")
	}
	positionAction := input.Action == "definition" || input.Action == "references" || input.Action == "hover" || input.Action == "rename" || input.Action == "type_definition" || input.Action == "implementation" || input.Action == "code_actions"
	if positionAction && input.Line <= 0 {
		return errors.New("line is required and must be 1-based")
	}
	if input.Action == "rename" && strings.TrimSpace(input.NewName) == "" {
		return errors.New("new_name is required for rename")
	}
	if input.Action == "rename_file" {
		if strings.TrimSpace(input.NewName) == "" {
			return errors.New("new_name is required for rename_file")
		}
		destination, err := workspaceRelativePath(root, input.NewName)
		if err != nil {
			return err
		}
		workspace, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		defer workspace.Close()
		parent := filepath.Dir(filepath.FromSlash(destination))
		if info, err := workspace.Stat(parent); err != nil || !info.IsDir() {
			return fmt.Errorf("rename_file destination parent %s is unavailable", filepath.ToSlash(parent))
		}
	}
	return nil
}

func lspError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "lsp failed: " + err.Error(), IsError: true}
}
