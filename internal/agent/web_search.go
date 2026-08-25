package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/venat/tool"
)

const ToolWebSearch = "web_search"

type webSearchInput struct {
	Query            string   `json:"query"`
	Recency          string   `json:"recency,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	MaxTokens        int      `json:"max_tokens,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	NumSearchResults int      `json:"num_search_results,omitempty"`
}

type webSearchDriver struct {
	root          string
	bridge        *lspBridgeRuntime
	networkPolicy string
	execute       func(context.Context, string, string, map[string]any) (lspBridgeResponse, error)
}

type webSearchToolResult struct {
	Query   string          `json:"query"`
	Content string          `json:"content"`
	Details json.RawMessage `json:"details,omitempty"`
}

func newWebSearchDriver(root string, bridge *lspBridgeRuntime, networkPolicy string) tool.Driver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	driver := &webSearchDriver{root: root, bridge: bridge, networkPolicy: networkPolicy}
	driver.execute = func(ctx context.Context, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
		return bridge.requestWebSearch(ctx, cwd, sessionID, params)
	}
	return driver
}

func (driver *webSearchDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolWebSearch,
		Description: "Search current web information through the configured OMP provider fallback chain. Prefer primary sources and corroborate important claims. Query supports site:/-site:, after:/before: dates, inurl:, intitle:, filetype:, quoted phrases, exclusions, and OR; unsupported constraints are filtered leniently and relaxed rather than returning zero results.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"query"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"query": {Type: "string"}, "recency": {Type: "string"}, "limit": {Type: "integer"},
				"max_tokens": {Type: "integer"}, "temperature": {Type: "number"}, "num_search_results": {Type: "integer"},
			},
		},
		EffectType: tool.EffectReadOnly, RiskLevel: "low", PolicyTags: []string{"web", "search", "network"}, Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver *webSearchDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input webSearchInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return webSearchError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		return webSearchError(call, errors.New("query must not be empty")), nil
	}
	if len(input.Query) > 8192 {
		return webSearchError(call, errors.New("query exceeds 8192 characters")), nil
	}
	if input.Recency != "" && input.Recency != "day" && input.Recency != "week" && input.Recency != "month" && input.Recency != "year" {
		return webSearchError(call, errors.New("recency must be day, week, month, or year")), nil
	}
	if input.Limit < 0 || input.Limit > 50 || input.MaxTokens < 0 || input.MaxTokens > 100000 || input.NumSearchResults < 0 || input.NumSearchResults > 100 {
		return webSearchError(call, errors.New("search result/token limit is outside the supported range")), nil
	}
	if input.Temperature != nil && (*input.Temperature < 0 || *input.Temperature > 2) {
		return webSearchError(call, errors.New("temperature must be between 0 and 2")), nil
	}
	if driver.networkPolicy == "deny" {
		return webSearchError(call, errors.New("web search is denied by workspace network policy")), nil
	}
	params := map[string]any{"query": input.Query}
	if input.Recency != "" {
		params["recency"] = input.Recency
	}
	if input.Limit > 0 {
		params["limit"] = input.Limit
	}
	if input.MaxTokens > 0 {
		params["max_tokens"] = input.MaxTokens
	}
	if input.Temperature != nil {
		params["temperature"] = *input.Temperature
	}
	if input.NumSearchResults > 0 {
		params["num_search_results"] = input.NumSearchResults
	}
	caller, _ := tool.CallerFromContext(ctx)
	sessionID := caller.SessionID
	if sessionID == "" {
		sessionID = caller.TeamRunID
	}
	requestCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	response, err := driver.execute(requestCtx, driver.root, sessionID, params)
	if err != nil {
		return webSearchError(call, err), nil
	}
	isError := response.IsError
	if len(response.Details) > 0 {
		var details struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(response.Details, &details) == nil && strings.TrimSpace(details.Error) != "" {
			isError = true
		}
	}
	result := webSearchToolResult{Query: input.Query, Content: response.Content, Details: response.Details}
	structured, _ := json.Marshal(result)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: response.Content, Structured: structured, IsError: isError}, nil
}

func webSearchError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "web_search failed: " + err.Error(), IsError: true}
}
