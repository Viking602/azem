package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPExecutorOptions struct {
	GatewayURL string
	Token      string
	HTTPClient *http.Client
}

type HTTPExecutor struct {
	endpoint string
	token    string
	http     *http.Client
}

func NewHTTPExecutor(options HTTPExecutorOptions) (*HTTPExecutor, error) {
	parsed, err := url.Parse(strings.TrimSpace(options.GatewayURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("benchmark gateway URL must be absolute")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback(parsed.Hostname())) {
		return nil, errors.New("benchmark gateway URL must use HTTPS outside loopback")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/chat/completions"
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 0}
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPExecutor{endpoint: parsed.String(), token: strings.TrimSpace(options.Token), http: &copyClient}, nil
}

func (executor *HTTPExecutor) Execute(ctx context.Context, query Query) Observation {
	started := time.Now()
	body := map[string]any{
		"model": query.Model, "messages": []map[string]string{{"role": "user", "content": query.Prompt}},
		"max_tokens": query.MaxTokens, "stream": true, "stream_options": map[string]bool{"include_usage": true},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return Observation{Error: err.Error()}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, executor.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Observation{Error: err.Error()}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if executor.token != "" {
		request.Header.Set("Authorization", "Bearer "+executor.token)
	}
	if query.Provider != "" {
		request.Header.Set("X-Azem-Provider", query.Provider)
	}
	response, err := executor.http.Do(request)
	if err != nil {
		return Observation{Latency: time.Since(started), Error: err.Error()}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return Observation{Latency: time.Since(started), Error: fmt.Sprintf("gateway HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))}
	}
	observation := Observation{}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		observation.OutputBytes += int64(len(line) + 1)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if observation.TimeToFirst == 0 {
			observation.TimeToFirst = time.Since(started)
		}
		var event struct {
			Usage *struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
				PromptDetails    *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &event) == nil && event.Usage != nil {
			observation.InputTokens = event.Usage.PromptTokens
			observation.OutputTokens = event.Usage.CompletionTokens
			if event.Usage.PromptDetails != nil {
				observation.CachedTokens = event.Usage.PromptDetails.CachedTokens
				observation.CacheReported = true
			}
		}
	}
	observation.Latency = time.Since(started)
	if err := scanner.Err(); err != nil {
		observation.Error = err.Error()
	}
	if observation.TimeToFirst == 0 && observation.Error == "" {
		observation.Error = "gateway stream completed without events"
	}
	return observation
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
