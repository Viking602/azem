package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Viking602/venat/tool"
)

func TestBrowserOpenRunInteractAndClosePersistentTab(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html")
		_, _ = response.Write([]byte(`<!doctype html><html><body><button id="change" onclick="document.querySelector('#status').textContent='changed'">Change</button><p id="status">ready</p></body></html>`))
	}))
	defer server.Close()
	ctx := tool.WithCaller(context.Background(), tool.CallerInfo{SessionID: "browser-session"})
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newBrowserDriver(t.TempDir(), bridge, "allow")
	opened := executeBrowser(t, ctx, driver, map[string]any{"action": "open", "name": "fixture", "url": server.URL, "viewport": map[string]any{"width": 900, "height": 700}, "timeout": 60})
	if !strings.Contains(strings.ToLower(opened.Content), "fixture") && !strings.Contains(opened.Content, server.URL) {
		t.Fatalf("open = %s", opened.Content)
	}
	observed := executeBrowser(t, ctx, driver, map[string]any{"action": "run", "name": "fixture", "code": "const obs = await tab.observe(); display(obs); return obs.elements.length;", "timeout": 30})
	if !strings.Contains(observed.Content, "Change") {
		t.Fatalf("observe = %s", observed.Content)
	}
	interacted := executeBrowser(t, ctx, driver, map[string]any{"action": "run", "name": "fixture", "code": "await tab.click('#change'); return await tab.evaluate(() => document.querySelector('#status').textContent);", "timeout": 30})
	if !strings.Contains(interacted.Content, "changed") {
		t.Fatalf("interaction = %s", interacted.Content)
	}
	executeBrowser(t, ctx, driver, map[string]any{"action": "run", "name": "fixture", "code": "return await tab.screenshot({ silent: true });", "timeout": 30})
	executeBrowser(t, ctx, driver, map[string]any{"action": "close", "name": "fixture", "kill": true, "timeout": 30})
	closed := callBrowser(ctx, driver, map[string]any{"action": "run", "name": "fixture", "code": "return 1", "timeout": 10})
	if !closed.IsError || !strings.Contains(strings.ToLower(closed.Content), "open") {
		t.Fatalf("run after close = %#v", closed)
	}
}

func TestBrowserValidatesNetworkPolicyAndInputBeforeLaunching(t *testing.T) {
	driver := newBrowserDriver(t.TempDir(), newLSPBridgeRuntime(), "deny")
	denied := callBrowser(context.Background(), driver, map[string]any{"action": "open", "url": "https://example.com"})
	if !denied.IsError || !strings.Contains(denied.Content, "network access is denied") {
		t.Fatalf("network policy = %#v", denied)
	}
	invalid := callBrowser(context.Background(), driver, map[string]any{"action": "run", "code": ""})
	if !invalid.IsError || !strings.Contains(invalid.Content, "code is required") {
		t.Fatalf("empty run = %#v", invalid)
	}
}

func executeBrowser(t *testing.T, ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	t.Helper()
	if concrete, ok := driver.(*browserDriver); ok {
		if err := concrete.bridge.resolveAssets(); err != nil {
			t.Skip(err)
		}
	}
	result := callBrowser(ctx, driver, input)
	if result.IsError {
		if strings.Contains(strings.ToLower(result.Content), "browser executable") || strings.Contains(strings.ToLower(result.Content), "chromium") && strings.Contains(strings.ToLower(result.Content), "not found") {
			t.Skip(result.Content)
		}
		t.Fatalf("browser call failed: %s", result.Content)
	}
	return result
}

func callBrowser(ctx context.Context, driver tool.Driver, input map[string]any) tool.Result {
	arguments, _ := json.Marshal(input)
	result, err := driver.Execute(ctx, tool.Call{ID: "browser", Name: ToolBrowser, Arguments: arguments}, nil)
	if err != nil {
		return tool.Result{ToolCallID: "browser", Name: ToolBrowser, Content: err.Error(), IsError: true}
	}
	return result
}
