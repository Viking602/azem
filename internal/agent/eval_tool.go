package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

const ToolEval = "eval"

type evalInput struct {
	Language string `json:"language"`
	Code     string `json:"code"`
	Title    string `json:"title,omitempty"`
	Timeout  *int   `json:"timeout,omitempty"`
	Reset    bool   `json:"reset,omitempty"`
}

type evalDriver struct {
	root   string
	bridge *lspBridgeRuntime
}

type evalToolResult struct {
	Language string          `json:"language"`
	Title    string          `json:"title,omitempty"`
	Content  string          `json:"content"`
	Details  json.RawMessage `json:"details,omitempty"`
}

type evalBridgePart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

func newEvalDriver(root string, bridge *lspBridgeRuntime) tool.Driver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	return &evalDriver{root: root, bridge: bridge}
}

func (driver *evalDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolEval,
		Description: "Run one Python, Bun JavaScript, Ruby, or Julia cell in a persistent per-session kernel. State survives later calls in the same language; reset clears only that language. Work incrementally. Python helpers are synchronous; JavaScript helpers are async and top-level await/Bun globals are available. Helpers: display, read, write, env, output, parallel, pipeline, log, phase, budget.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"language", "code"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"language": {Type: "string", Description: "py, js, rb, or jl"},
				"code":     {Type: "string", Description: "One cell of code, verbatim."},
				"title":    {Type: "string", Description: "Short transcript label."},
				"timeout":  {Type: "integer", Description: "Seconds; 0 disables the cell deadline."},
				"reset":    {Type: "boolean", Description: "Reset this language kernel before execution."},
			},
		},
		EffectType: tool.EffectExternalSideEffect, RequiresApproval: true, RequiresActionTask: true, RiskLevel: "high",
		PolicyTags: []string{"coding", "eval", "execute"}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "eval-session",
	}
}

func (driver *evalDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input evalInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return evalError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	input.Language = strings.ToLower(strings.TrimSpace(input.Language))
	if input.Language != "py" && input.Language != "js" && input.Language != "rb" && input.Language != "jl" {
		return evalError(call, errors.New("language must be py, js, rb, or jl")), nil
	}
	if strings.TrimSpace(input.Code) == "" {
		return evalError(call, errors.New("code must not be empty")), nil
	}
	if len(input.Code) > 4<<20 {
		return evalError(call, errors.New("code exceeds 4 MiB")), nil
	}
	if len(input.Title) > 200 {
		return evalError(call, errors.New("title exceeds 200 characters")), nil
	}
	timeout := 30
	if input.Timeout != nil {
		timeout = *input.Timeout
	}
	if timeout < 0 || timeout > 3600 {
		return evalError(call, errors.New("timeout must be between 0 and 3600 seconds")), nil
	}
	caller, _ := tool.CallerFromContext(ctx)
	sessionID := caller.SessionID
	if sessionID == "" {
		sessionID = caller.TeamRunID
	}
	if sessionID == "" {
		sessionID = driver.root
	}
	artifactsDir, err := evalArtifactsDir(driver.root, sessionID)
	if err != nil {
		return evalError(call, err), nil
	}
	params := map[string]any{"language": input.Language, "code": input.Code, "reset": input.Reset, "timeout": timeout}
	if input.Title != "" {
		params["title"] = input.Title
	}
	driver.bridge.evalMu.Lock()
	defer driver.bridge.evalMu.Unlock()
	requestCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	}
	defer cancel()
	response, err := driver.bridge.requestEval(requestCtx, driver.root, sessionID, artifactsDir, params)
	if err != nil {
		return evalError(call, err), nil
	}
	isError := response.IsError
	if len(response.Details) > 0 {
		var details struct {
			IsError bool `json:"isError"`
		}
		if json.Unmarshal(response.Details, &details) == nil {
			isError = isError || details.IsError
		}
	}
	result := evalToolResult{Language: input.Language, Title: input.Title, Content: response.Content, Details: response.Details}
	structured, _ := json.Marshal(result)
	toolResult := tool.Result{ToolCallID: call.ID, Name: call.Name, Content: response.Content, Structured: structured, IsError: isError}
	toolResult.Parts = decodeEvalBridgeParts(response.Parts)
	return toolResult, nil
}

func evalArtifactsDir(root, sessionID string) (string, error) {
	digest := sha256.Sum256([]byte(filepath.Clean(root) + "\x00" + sessionID))
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	path := filepath.Join(base, "azem", "eval", hex.EncodeToString(digest[:12]))
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func decodeEvalBridgeParts(payload json.RawMessage) []message.ContentPart {
	if len(payload) == 0 {
		return nil
	}
	var parts []evalBridgePart
	if json.Unmarshal(payload, &parts) != nil {
		return nil
	}
	result := make([]message.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type != "image" || part.Data == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			continue
		}
		result = append(result, message.ContentPart{Kind: message.ContentImage, Data: data, MediaType: part.MimeType})
	}
	return result
}

func evalError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "eval failed: " + err.Error(), IsError: true}
}
