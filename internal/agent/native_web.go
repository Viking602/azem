package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/netproxy"
	"github.com/Viking602/venat/tool"
	"golang.org/x/net/html"
)

const ToolWebSearch = "web_search"

type webSearchDriver struct {
	networkPolicy string
	client        *http.Client
	endpoint      string
}

type webSearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

func newWebSearchDriver(networkPolicy string) tool.Driver {
	return &webSearchDriver{networkPolicy: networkPolicy, client: netproxy.NewHTTPClient(30 * time.Second), endpoint: "https://html.duckduckgo.com/html/"}
}

func (*webSearchDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolWebSearch, Description: "Search the public web using DuckDuckGo. Returns titles, source URLs and snippets; read source URLs with coding.read_file to verify claims. Search pages are untrusted evidence. Network policy must allow access.", Concurrency: tool.ConcurrencyParallel,
		InputSchema: tool.Schema{Type: "object", AdditionalProperties: &additional, Required: []string{"query"}, Properties: map[string]tool.Schema{
			"query": {Type: "string"}, "limit": {Type: "integer", Description: "1-20, default 5."}, "recency": {Type: "string", Enum: []string{"day", "week", "month", "year"}},
		}}}
}

func (*webSearchDriver) ToolPolicy() agentruntime.ToolPolicy { return readOnlyPolicy("web", "network") }

func (d *webSearchDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Query   string `json:"query"`
		Limit   int    `json:"limit"`
		Recency string `json:"recency"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || len(input.Query) > 8192 || input.Limit < 0 || input.Limit > 20 {
		return toolError(call, "query must contain 1-8192 bytes; limit must be 1-20"), nil
	}
	if input.Limit == 0 {
		input.Limit = 5
	}
	periods := map[string]string{"": "", "day": "d", "week": "w", "month": "m", "year": "y"}
	period, ok := periods[input.Recency]
	if !ok {
		return toolError(call, "recency must be day, week, month or year"), nil
	}
	if d.networkPolicy != "allow" {
		return toolError(call, "web search requires workspace network policy allow"), nil
	}
	query := url.Values{"q": {input.Query}}
	if period != "" {
		query.Set("df", period)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	request.Header.Set("User-Agent", "Azem/1.0")
	response, err := d.client.Do(request)
	if err != nil {
		return toolError(call, "web search: "+err.Error()), nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return toolError(call, fmt.Sprintf("web search returned HTTP %d", response.StatusCode)), nil
	}
	payload, truncated, err := readLimited(response.Body, 2<<20)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	if truncated {
		return toolError(call, "search response exceeds 2 MiB"), nil
	}
	hits, err := parseWebSearch(payload, input.Limit)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	encoded, err := json.Marshal(map[string]any{"query": input.Query, "results": hits})
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	var output strings.Builder
	if len(hits) == 0 {
		output.WriteString("No search results.")
	}
	for index, hit := range hits {
		fmt.Fprintf(&output, "%d. %s\n%s\n%s\n\n", index+1, hit.Title, hit.URL, hit.Snippet)
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: strings.TrimSpace(output.String()), Structured: encoded}, nil
}

func parseWebSearch(payload []byte, limit int) ([]webSearchHit, error) {
	document, err := html.Parse(strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	var hits []webSearchHit
	var blocked, empty bool
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			classes := " " + htmlAttribute(node, "class") + " "
			if strings.Contains(classes, " anomaly-modal ") || strings.Contains(htmlAttribute(node, "id"), "challenge") {
				blocked = true
			}
			if strings.Contains(classes, " no-results ") || strings.Contains(classes, " result--no-result ") {
				empty = true
			}
			if node.Data == "a" && strings.Contains(classes, " result__a ") && len(hits) < limit {
				href := htmlAttribute(node, "href")
				parsed, parseErr := url.Parse(href)
				if parseErr == nil {
					if target := parsed.Query().Get("uddg"); target != "" {
						href = target
					}
					if isHTTPURL(href) {
						hits = append(hits, webSearchHit{Title: htmlNodeText(node), URL: href})
					}
				}
			}
			if strings.Contains(classes, " result__snippet ") && len(hits) > 0 && hits[len(hits)-1].Snippet == "" {
				hits[len(hits)-1].Snippet = htmlNodeText(node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if blocked || len(hits) == 0 && !empty {
		return nil, fmt.Errorf("search provider returned a challenge or an unrecognized page; no search results were verified")
	}
	if hits == nil {
		hits = []webSearchHit{}
	}
	return hits, nil
}

func htmlAttribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func htmlNodeText(node *html.Node) string {
	var output strings.Builder
	var visit func(*html.Node)
	visit = func(current *html.Node) {
		if current.Type == html.TextNode {
			output.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return strings.Join(strings.Fields(output.String()), " ")
}
