package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Viking602/venat/tool"
)

func TestWebSearchForwardsConstraintsAndProviderOptions(t *testing.T) {
	zero := 0.0
	var captured map[string]any
	driver := &webSearchDriver{root: t.TempDir(), networkPolicy: "allow"}
	if !strings.Contains(driver.Definition().Description, "configured provider fallback chain") {
		t.Fatalf("web search definition is not product-neutral: %q", driver.Definition().Description)
	}
	driver.execute = func(_ context.Context, _, _ string, params map[string]any) (lspBridgeResponse, error) {
		captured = params
		details := json.RawMessage(`{"response":{"provider":"fixture","sources":[{"title":"Official","url":"https://example.com/docs"}]}}`)
		return lspBridgeResponse{OK: true, Content: "[1] Official\n    https://example.com/docs", Details: details}, nil
	}
	arguments, _ := json.Marshal(webSearchInput{
		Query: `site:example.com intitle:Official "exact phrase" after:2026-01-01`, Recency: "month",
		Limit: 7, MaxTokens: 4000, Temperature: &zero, NumSearchResults: 9,
	})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "search", Name: ToolWebSearch, Arguments: arguments}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "https://example.com/docs") {
		t.Fatalf("search result = %#v, %v", result, err)
	}
	if captured["query"] != `site:example.com intitle:Official "exact phrase" after:2026-01-01` || captured["recency"] != "month" || captured["limit"] != 7 || captured["temperature"] != float64(0) || captured["num_search_results"] != 9 {
		t.Fatalf("forwarded params = %#v", captured)
	}
}

func TestWebSearchSurfacesProviderFailureAndNetworkPolicy(t *testing.T) {
	driver := &webSearchDriver{root: t.TempDir(), networkPolicy: "allow"}
	driver.execute = func(context.Context, string, string, map[string]any) (lspBridgeResponse, error) {
		return lspBridgeResponse{OK: true, Content: "Error: all providers failed", Details: json.RawMessage(`{"error":"all providers failed"}`)}, nil
	}
	arguments := json.RawMessage(`{"query":"current release"}`)
	result, err := driver.Execute(context.Background(), tool.Call{ID: "failed", Name: ToolWebSearch, Arguments: arguments}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "all providers failed") {
		t.Fatalf("provider failure = %#v, %v", result, err)
	}
	denied := newWebSearchDriver(t.TempDir(), newLSPBridgeRuntime(), "deny")
	result, err = denied.Execute(context.Background(), tool.Call{ID: "denied", Name: ToolWebSearch, Arguments: arguments}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "network policy") {
		t.Fatalf("network denial = %#v, %v", result, err)
	}
}

func TestWebSearchLiveProviderChain(t *testing.T) {
	if os.Getenv("AZEM_LIVE_WEB_SEARCH") != "1" {
		t.Skip("set AZEM_LIVE_WEB_SEARCH=1 for external provider verification")
	}
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newWebSearchDriver(t.TempDir(), bridge, "allow")
	arguments := json.RawMessage(`{"query":"site:openai.com OpenAI official","limit":3}`)
	result, err := driver.Execute(context.Background(), tool.Call{ID: "live", Name: ToolWebSearch, Arguments: arguments}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "http") {
		t.Fatalf("live search = %#v, %v", result, err)
	}
}
