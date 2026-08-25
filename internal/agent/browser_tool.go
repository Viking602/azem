package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

const ToolBrowser = "browser"

type browserAppInput struct {
	Path   string   `json:"path,omitempty"`
	CDPURL string   `json:"cdp_url,omitempty"`
	Relay  *bool    `json:"relay,omitempty"`
	Args   []string `json:"args,omitempty"`
	Target string   `json:"target,omitempty"`
}

type browserViewportInput struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale,omitempty"`
}

type browserInput struct {
	Action    string                `json:"action"`
	Name      string                `json:"name,omitempty"`
	URL       string                `json:"url,omitempty"`
	App       *browserAppInput      `json:"app,omitempty"`
	Viewport  *browserViewportInput `json:"viewport,omitempty"`
	WaitUntil string                `json:"wait_until,omitempty"`
	Dialogs   string                `json:"dialogs,omitempty"`
	Code      string                `json:"code,omitempty"`
	Timeout   int                   `json:"timeout,omitempty"`
	All       bool                  `json:"all,omitempty"`
	Kill      bool                  `json:"kill,omitempty"`
}

type browserDriver struct {
	root          string
	bridge        *lspBridgeRuntime
	networkPolicy string
}

type browserToolResult struct {
	Action  string          `json:"action"`
	Name    string          `json:"name,omitempty"`
	Content string          `json:"content"`
	Details json.RawMessage `json:"details,omitempty"`
}

func newBrowserDriver(root string, bridge *lspBridgeRuntime, networkPolicy string) tool.Driver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	return &browserDriver{root: root, bridge: bridge, networkPolicy: networkPolicy}
}

func (driver *browserDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolBrowser,
		Description: "Drive one or more persistent real Chromium tabs. open before run; run executes JavaScript with page, browser, tab, display, assert, and wait. Use tab.observe() for accessible state and screenshots only for appearance. close releases named/all sessions. Supports headless, spawned app.path, CDP, and explicit relay attachment.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"action"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"action": {Type: "string"}, "name": {Type: "string"}, "url": {Type: "string"},
				"app": {Type: "object"}, "viewport": {Type: "object"}, "wait_until": {Type: "string"},
				"dialogs": {Type: "string"}, "code": {Type: "string"}, "timeout": {Type: "integer"},
				"all": {Type: "boolean"}, "kill": {Type: "boolean"},
			},
		},
		EffectType: tool.EffectExternalSideEffect, RequiresApproval: true, RequiresActionTask: true, RiskLevel: "high",
		PolicyTags: []string{"browser", "network", "execute"}, Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver *browserDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input browserInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return browserError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	if input.Action != "open" && input.Action != "run" && input.Action != "close" {
		return browserError(call, fmt.Errorf("unsupported action %q", input.Action)), nil
	}
	if input.Name == "" {
		input.Name = "main"
	}
	if len(input.Name) > 128 || strings.ContainsAny(input.Name, "\r\n\x00") {
		return browserError(call, errors.New("name is invalid or exceeds 128 characters")), nil
	}
	if input.Timeout == 0 {
		input.Timeout = 30
	}
	if input.Timeout < 1 || input.Timeout > 300 {
		return browserError(call, errors.New("timeout must be between 1 and 300 seconds")), nil
	}
	if len(input.Code) > 4<<20 {
		return browserError(call, errors.New("code exceeds 4 MiB")), nil
	}
	if input.Action == "run" && strings.TrimSpace(input.Code) == "" {
		return browserError(call, errors.New("code is required for run")), nil
	}
	if err := driver.validateInput(&input); err != nil {
		return browserError(call, err), nil
	}
	params := map[string]any{"action": input.Action, "name": input.Name, "timeout": input.Timeout}
	encoded, _ := json.Marshal(input)
	_ = json.Unmarshal(encoded, &params)
	caller, _ := tool.CallerFromContext(ctx)
	sessionID := caller.SessionID
	if sessionID == "" {
		sessionID = caller.TeamRunID
	}
	if sessionID == "" {
		sessionID = driver.root
	}
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()
	response, err := driver.bridge.requestBrowser(requestCtx, driver.root, sessionID, params)
	if err != nil {
		return browserError(call, err), nil
	}
	result := browserToolResult{Action: input.Action, Name: input.Name, Content: response.Content, Details: response.Details}
	structured, _ := json.Marshal(result)
	toolResult := tool.Result{ToolCallID: call.ID, Name: call.Name, Content: response.Content, Structured: structured, IsError: response.IsError}
	toolResult.Parts = decodeBrowserBridgeParts(response.Parts)
	return toolResult, nil
}

func (driver *browserDriver) validateInput(input *browserInput) error {
	if input.WaitUntil != "" && input.WaitUntil != "load" && input.WaitUntil != "domcontentloaded" && input.WaitUntil != "networkidle0" && input.WaitUntil != "networkidle2" {
		return errors.New("wait_until is invalid")
	}
	if input.Dialogs != "" && input.Dialogs != "accept" && input.Dialogs != "dismiss" {
		return errors.New("dialogs must be accept or dismiss")
	}
	if input.Viewport != nil {
		if input.Viewport.Width < 1 || input.Viewport.Width > 10000 || input.Viewport.Height < 1 || input.Viewport.Height > 10000 {
			return errors.New("viewport dimensions must be between 1 and 10000")
		}
		if input.Viewport.Scale < 0 || input.Viewport.Scale > 4 {
			return errors.New("viewport scale must be between 0 and 4")
		}
	}
	if input.URL != "" {
		parsed, err := url.Parse(input.URL)
		if err != nil || parsed.Scheme == "" {
			return errors.New("url must be absolute")
		}
		switch parsed.Scheme {
		case "http", "https", "ws", "wss":
			if driver.networkPolicy == "deny" {
				return errors.New("browser network access is denied by workspace policy")
			}
		case "file":
			if _, _, _, err := secureReadPath(driver.root, parsed.Path); err != nil {
				return err
			}
		case "about", "data":
		default:
			return fmt.Errorf("unsupported browser URL scheme %q", parsed.Scheme)
		}
	}
	if input.App != nil {
		if input.App.Path != "" {
			path := input.App.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(driver.root, filepath.FromSlash(path))
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("app.path is not a regular executable")
			}
			if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
				return errors.New("app.path is not executable")
			}
			input.App.Path = path
		}
		if input.App.CDPURL != "" {
			parsed, err := url.Parse(input.App.CDPURL)
			if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss" {
				return errors.New("app.cdp_url must be HTTP(S) or WS(S)")
			}
		}
		if len(input.App.Args) > 64 {
			return errors.New("app.args has more than 64 entries")
		}
		for _, argument := range input.App.Args {
			if len(argument) > 4096 || strings.ContainsRune(argument, '\x00') {
				return errors.New("app argument is invalid or exceeds 4096 characters")
			}
		}
	}
	return nil
}

func decodeBrowserBridgeParts(payload json.RawMessage) []message.ContentPart {
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

func browserError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "browser failed: " + err.Error(), IsError: true}
}
