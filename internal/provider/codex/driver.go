package codex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"resty.dev/v3"

	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/responses"
)

const DefaultEndpoint = "https://chatgpt.com/backend-api/codex/responses"

type Driver struct {
	auth            *auth.Service
	accountID       string
	endpoint        string
	models          []string
	toolIDsMu       sync.RWMutex
	toolItemIDs     map[string]string
	reasoningEffort string
	retryDelay      func(int) time.Duration
	maxRetryDelay   time.Duration
	retryObserver   hyprovider.RetryObserver
}

func New(authentication *auth.Service, accountID string, endpoint string, models []string, reasoningEffort string) (*Driver, error) {
	if authentication == nil {
		return nil, fmt.Errorf("codex driver auth service is nil")
	}
	if accountID == "" {
		return nil, fmt.Errorf("codex driver account ID is empty")
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Driver{
		auth: authentication, accountID: accountID, endpoint: endpoint,
		models: append([]string(nil), models...), toolItemIDs: make(map[string]string),
		reasoningEffort: reasoningEffort, retryDelay: providerStreamRetryDelay,
	}, nil
}

func (d *Driver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "chatgpt-codex-responses", Models: append([]string(nil), d.models...), Version: "1"}
}

// SetRetryObserver reports SDK-managed provider stream retries.
func (d *Driver) SetRetryObserver(observer hyprovider.RetryObserver) {
	d.retryObserver = observer
}

func (d *Driver) SetMaxRetryDelay(delay time.Duration) {
	d.maxRetryDelay = delay
}

func (d *Driver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	cacheKey := promptCacheKey(request)
	request, reverseNames := mapToolNames(request)
	payload, err := responses.Build(request, responses.BuildOptions{
		IncludeEncryptedReasoning: true, DefaultParallelTools: true, ToolCallItemID: d.toolItemID,
		DefaultReasoningEffort: d.reasoningEffort,
	})
	if err != nil {
		return nil, err
	}
	// ChatGPT/Codex reports explicit cache write counters; tag metering so UI and
	// facts stay on the write-token model without coupling to xAI automatic cache.
	reporter := responses.WrapUsageReporter(responses.RequestUsageReporter(request), responses.CacheModelWriteTokens)
	open := func() (hyprovider.Stream, error) {
		return d.openStream(ctx, payload, reverseNames, cacheKey, reporter)
	}
	return hyprovider.OpenRetryingStream(ctx, open, hyprovider.StreamRetryOptions{
		Max:      maxProviderStreamRetries,
		Delay:    d.retryDelay,
		MaxDelay: d.maxRetryDelay,
		Observer: d.retryObserver,
	})
}

func (d *Driver) openStream(ctx context.Context, payload []byte, reverseNames map[string]string, cacheKey string, reporter responses.UsageReporter) (hyprovider.Stream, error) {
	streamContext, cancel := context.WithCancel(ctx)
	response, err := d.auth.DoStreamWithRefresh(
		streamContext,
		"chatgpt",
		d.accountID,
		resty.MethodPost,
		d.endpoint,
		func(request *resty.Request) {
			request.SetBody(payload)
			request.SetHeader("Content-Type", "application/json")
			request.SetHeader("Accept", "text/event-stream")
			request.SetHeader("OpenAI-Beta", "responses=experimental")
			request.SetHeader("originator", "codex_cli_rs")
			request.SetHeader("User-Agent", "azem/1")
			if cacheKey != "" {
				request.SetHeader("conversation_id", cacheKey)
				request.SetHeader("session_id", cacheKey)
			}
		},
	)
	if err != nil {
		cancel()
		return nil, err
	}
	stream, err := responses.Open(response, streamContext, cancel, reporter)
	if err != nil {
		return nil, err
	}
	return &toolNameStream{inner: stream, reverse: reverseNames, recordItemID: d.recordToolItemID}, nil
}

func promptCacheKey(request hyprovider.Request) string {
	value, _ := request.ExtraBody["prompt_cache_key"].(string)
	return strings.TrimSpace(value)
}

const maxProviderStreamRetries = hyprovider.DefaultMaxStreamRetries

func providerStreamRetryDelay(attempt int) time.Duration {
	delay := 500 * time.Millisecond * time.Duration(1<<max(0, attempt-1))
	return min(delay, 8*time.Second)
}

func (d *Driver) toolItemID(callID string) string {
	d.toolIDsMu.RLock()
	defer d.toolIDsMu.RUnlock()
	return d.toolItemIDs[callID]
}

func (d *Driver) recordToolItemID(callID string, itemID string) {
	d.toolIDsMu.Lock()
	defer d.toolIDsMu.Unlock()
	d.toolItemIDs[callID] = itemID
}

var _ hyprovider.Driver = (*Driver)(nil)
