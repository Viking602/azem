package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
	"github.com/coder/websocket"
)

const ToolBrowser = "browser"

type browserDriver struct {
	root, networkPolicy string
	host                *nativeHost
}

func (*browserDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolBrowser, Description: "Control an isolated native Chrome/Chromium browser over CDP. Persistent tabs are private to this conversation/agent, not the user's signed-in browser. open/navigate require an HTTP(S) URL. snapshot returns the accessibility tree; click/type use a CSS selector. screenshot returns an image. evaluate executes a browser JavaScript expression. close terminates this isolated browser and deletes its temporary profile.", InputSchema: tool.Schema{Type: "object", Required: []string{"action"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
		"action": {Type: "string", Enum: []string{"open", "navigate", "tabs", "select", "snapshot", "screenshot", "click", "type", "press", "scroll", "evaluate", "close"}},
		"url":    {Type: "string"}, "tab": {Type: "string"}, "selector": {Type: "string"}, "text": {Type: "string"}, "key": {Type: "string"}, "expression": {Type: "string"}, "x": {Type: "number"}, "y": {Type: "number"}, "headless": {Type: "boolean", Description: "Used on first open; default true."},
	}}, Concurrency: tool.ConcurrencyParallel}
}

func (*browserDriver) ToolPolicy() agentruntime.ToolPolicy {
	return approvedExternalPolicy("browser", "network", "computer")
}

type cdpClient struct {
	conn    *websocket.Conn
	seq     int
	session string
}

func (c *cdpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.seq++
	request := map[string]any{"id": c.seq, "method": method, "params": params}
	if c.session != "" {
		request["sessionId"] = c.session
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if err := c.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return nil, err
	}
	for count := 0; count < 4096; count++ {
		_, response, err := c.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var value struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(response, &value); err != nil {
			return nil, err
		}
		if value.ID != c.seq {
			continue
		}
		if len(value.Error) > 0 {
			return nil, fmt.Errorf("browser %s: %s", method, value.Error)
		}
		return value.Result, nil
	}
	return nil, errors.New("browser response exceeded event budget")
}

func chromeProgram() (string, error) {
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "chrome"} {
		if program, err := nativeExecutable(name); err == nil {
			return program, nil
		}
	}
	if runtime.GOOS == "darwin" {
		program := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
		if info, err := os.Stat(program); err == nil && info.Mode().IsRegular() {
			return program, nil
		}
	}
	return "", errors.New("native Chrome/Chromium is not installed")
}

func (d *browserDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Action     string  `json:"action"`
		URL        string  `json:"url"`
		Tab        string  `json:"tab"`
		Selector   string  `json:"selector"`
		Text       string  `json:"text"`
		Key        string  `json:"key"`
		Expression string  `json:"expression"`
		X          float64 `json:"x"`
		Y          float64 `json:"y"`
		Headless   *bool   `json:"headless"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if len(call.Arguments) > 1<<20 {
		return toolError(call, "browser arguments exceed 1 MiB"), nil
	}
	if d.networkPolicy != "allow" && input.Action != "close" {
		return toolError(call, "browser requires workspace network policy allow"), nil
	}
	if (input.Action == "open" || input.Action == "navigate") && !isHTTPURL(input.URL) {
		return toolError(call, "url must be HTTP(S)"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	key := nativeScope(ctx, d.root, "browser", "chrome")
	d.host.mu.Lock()
	p := d.host.processes[key]
	d.host.mu.Unlock()
	if p != nil {
		select {
		case <-p.done:
			p = nil
		default:
		}
	}
	if input.Action == "close" {
		if p != nil {
			p.close()
			select {
			case <-p.done:
			case <-ctx.Done():
				return toolError(call, ctx.Err().Error()), nil
			}
		}
		return nativeJSONResult(call, map[string]bool{"closed": true})
	}
	if p == nil {
		if input.Action != "open" {
			return toolError(call, "open the browser first"), nil
		}
		program, err := chromeProgram()
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		profile, err := os.MkdirTemp("", "azem-browser-")
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		args := []string{"--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1", "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "about:blank"}
		if input.Headless == nil || *input.Headless {
			args = append(args, "--headless=new")
		}
		p, err = d.host.start(key, d.root, program, args, true)
		if err != nil {
			_ = os.RemoveAll(profile)
			return toolError(call, err.Error()), nil
		}
		go func() { <-p.done; _ = os.RemoveAll(profile) }()
		unlock, err := p.lock(ctx)
		if err != nil {
			p.close()
			return toolError(call, err.Error()), nil
		}
		if p.data["profile"] == "" {
			for _, arg := range p.supervisor.command.Args {
				if path, ok := strings.CutPrefix(arg, "--user-data-dir="); ok {
					p.data["profile"] = path
				}
			}
		}
		if p.data["profile"] != profile {
			_ = os.RemoveAll(profile)
		}
		unlock()
	}
	unlock, err := p.lock(ctx)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	defer unlock()
	if p.data["endpoint"] == "" {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for p.data["endpoint"] == "" {
			payload, readErr := os.ReadFile(filepath.Join(p.data["profile"], "DevToolsActivePort"))
			if readErr == nil {
				lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
				if len(lines) == 2 {
					port, err := strconv.Atoi(lines[0])
					if err == nil && port > 0 && port < 65536 && strings.HasPrefix(lines[1], "/devtools/browser/") {
						p.data["endpoint"] = "ws://127.0.0.1:" + strconv.Itoa(port) + lines[1]
						break
					}
				}
			}
			select {
			case <-ctx.Done():
				p.close()
				return toolError(call, "browser did not expose its debugging endpoint"), nil
			case <-p.done:
				return toolError(call, "browser exited: "+p.stderr.text()), nil
			case <-ticker.C:
			}
		}
	}
	conn, _, err := websocket.Dial(ctx, p.data["endpoint"], &websocket.DialOptions{HTTPClient: &http.Client{Transport: &http.Transport{Proxy: nil}}})
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20)
	client := &cdpClient{conn: conn}
	if input.Action == "tabs" {
		result, err := client.call(ctx, "Target.getTargets", map[string]any{})
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		return nativeJSONResult(call, result)
	}
	if input.Action == "open" {
		result, err := client.call(ctx, "Target.createTarget", map[string]any{"url": input.URL})
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		var target struct {
			ID string `json:"targetId"`
		}
		if err := json.Unmarshal(result, &target); err != nil {
			return toolError(call, err.Error()), nil
		}
		p.data["tab"] = target.ID
		return nativeJSONResult(call, result)
	}
	targetTab := p.data["tab"]
	if input.Tab != "" {
		targetTab = input.Tab
	}
	if targetTab == "" {
		return toolError(call, "no tab selected"), nil
	}
	attached, err := client.call(ctx, "Target.attachToTarget", map[string]any{"targetId": targetTab, "flatten": true})
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	var attachment struct {
		Session string `json:"sessionId"`
	}
	if err := json.Unmarshal(attached, &attachment); err != nil {
		return toolError(call, err.Error()), nil
	}
	client.session = attachment.Session
	p.data["tab"] = targetTab
	var result json.RawMessage
	switch input.Action {
	case "select":
		result = attached
	case "navigate":
		result, err = client.call(ctx, "Page.navigate", map[string]any{"url": input.URL})
		if err == nil {
			var navigation struct {
				Error string `json:"errorText"`
			}
			_ = json.Unmarshal(result, &navigation)
			if navigation.Error != "" {
				err = errors.New(navigation.Error)
			}
		}
	case "snapshot":
		result, err = client.call(ctx, "Accessibility.getFullAXTree", map[string]any{})
	case "screenshot":
		result, err = client.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"})
	case "evaluate":
		if input.Expression == "" {
			return toolError(call, "expression is required"), nil
		}
		result, err = client.call(ctx, "Runtime.evaluate", map[string]any{"expression": input.Expression, "awaitPromise": true, "returnByValue": true})
		if err == nil {
			var evaluated struct {
				Exception json.RawMessage `json:"exceptionDetails"`
			}
			_ = json.Unmarshal(result, &evaluated)
			if len(evaluated.Exception) > 0 {
				err = fmt.Errorf("browser expression failed: %s", evaluated.Exception)
			}
		}
	case "press":
		if input.Key == "" {
			return toolError(call, "key is required"), nil
		}
		key := map[string]any{"type": "keyDown", "key": input.Key}
		if code := map[string]int{"Enter": 13, "Tab": 9, "Backspace": 8, "Delete": 46, "Escape": 27, "ArrowLeft": 37, "ArrowUp": 38, "ArrowRight": 39, "ArrowDown": 40, "Home": 36, "End": 35, "PageUp": 33, "PageDown": 34}[input.Key]; code != 0 {
			key["windowsVirtualKeyCode"] = code
			if input.Key == "Enter" {
				key["text"] = "\r"
			}
		} else if len([]rune(input.Key)) == 1 {
			key["text"] = input.Key
		} else {
			return toolError(call, "unsupported key; use standard names such as Enter, Tab or ArrowLeft"), nil
		}
		result, err = client.call(ctx, "Input.dispatchKeyEvent", key)
		if err == nil {
			key["type"] = "keyUp"
			delete(key, "text")
			result, err = client.call(ctx, "Input.dispatchKeyEvent", key)
		}
	case "scroll":
		result, err = client.call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": 1, "y": 1, "deltaX": input.X, "deltaY": input.Y})
	case "click", "type":
		if input.Selector == "" {
			return toolError(call, "selector is required"), nil
		}
		document, callErr := client.call(ctx, "DOM.getDocument", map[string]any{})
		if callErr != nil {
			return toolError(call, callErr.Error()), nil
		}
		var root struct {
			Root struct {
				Node int `json:"nodeId"`
			} `json:"root"`
		}
		if err := json.Unmarshal(document, &root); err != nil {
			return toolError(call, err.Error()), nil
		}
		found, callErr := client.call(ctx, "DOM.querySelectorAll", map[string]any{"nodeId": root.Root.Node, "selector": input.Selector})
		if callErr != nil {
			return toolError(call, callErr.Error()), nil
		}
		var node struct {
			IDs []int `json:"nodeIds"`
			ID  int
		}
		if err := json.Unmarshal(found, &node); err != nil {
			return toolError(call, err.Error()), nil
		}
		if len(node.IDs) != 1 {
			return toolError(call, "selector must match exactly one element"), nil
		}
		node.ID = node.IDs[0]
		if input.Action == "type" {
			_, err = client.call(ctx, "DOM.focus", map[string]any{"nodeId": node.ID})
			if err == nil {
				result, err = client.call(ctx, "Input.insertText", map[string]any{"text": input.Text})
			}
		} else {
			_, err = client.call(ctx, "DOM.scrollIntoViewIfNeeded", map[string]any{"nodeId": node.ID})
			if err != nil {
				break
			}
			box, callErr := client.call(ctx, "DOM.getBoxModel", map[string]any{"nodeId": node.ID})
			if callErr != nil {
				err = callErr
				break
			}
			var model struct {
				Model struct {
					Content []float64 `json:"content"`
				} `json:"model"`
			}
			if err = json.Unmarshal(box, &model); err != nil {
				break
			}
			if len(model.Model.Content) != 8 {
				err = errors.New("element has no clickable box")
				break
			}
			x, y := (model.Model.Content[0]+model.Model.Content[4])/2, (model.Model.Content[1]+model.Model.Content[5])/2
			_, err = client.call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1})
			if err == nil {
				result, err = client.call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1})
			}
		}
	default:
		err = errors.New("unsupported browser action")
	}
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	if input.Action == "snapshot" {
		return nativeBrowserSnapshot(call, result)
	}
	if input.Action == "screenshot" {
		var screenshot struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(result, &screenshot); err != nil {
			return toolError(call, err.Error()), nil
		}
		image, err := base64.StdEncoding.DecodeString(screenshot.Data)
		if err != nil || len(image) > 4<<20 {
			return toolError(call, "invalid or oversized screenshot"), nil
		}
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Browser screenshot", Parts: []message.ContentPart{{Kind: message.ContentImage, Data: image, MediaType: "image/png"}}}, nil
	}
	return nativeJSONResult(call, result)
}

func nativeBrowserSnapshot(call tool.Call, payload json.RawMessage) (tool.Result, error) {
	type value struct {
		Value any `json:"value"`
	}
	var tree struct {
		Nodes []struct {
			ID      string `json:"nodeId"`
			Parent  string `json:"parentId"`
			Backend int    `json:"backendDOMNodeId"`
			Role    value  `json:"role"`
			Name    value  `json:"name"`
			Value   value  `json:"value"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(payload, &tree); err != nil {
		return toolError(call, err.Error()), nil
	}
	if len(tree.Nodes) > 2000 {
		return toolError(call, "accessibility tree exceeds 2000 nodes; narrow the page with browser evaluate"), nil
	}
	nodes := make([]map[string]any, 0, len(tree.Nodes))
	for _, node := range tree.Nodes {
		nodes = append(nodes, map[string]any{"id": node.ID, "parent": node.Parent, "backendNodeId": node.Backend, "role": node.Role.Value, "name": node.Name.Value, "value": node.Value.Value})
	}
	return nativeJSONResult(call, map[string]any{"nodes": nodes})
}
