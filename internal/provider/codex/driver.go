package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"resty.dev/v3"

	hyprovider "github.com/Viking602/go-hydaelyn/provider"

	"github.com/Viking602/azem/internal/auth"
	azprovider "github.com/Viking602/azem/internal/provider"
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
	retryObserver   RetryObserver
}

type (
	RetryProgress = azprovider.RetryProgress
	RetryObserver = azprovider.RetryObserver
)

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

// SetRetryObserver reports transport retries from the retry loop itself. The
// observer must not block and is expected to be configured before Stream.
func (d *Driver) SetRetryObserver(observer RetryObserver) {
	d.retryObserver = observer
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
	stream, retries, err := openProviderStream(ctx, open, d.retryDelay, d.retryObserver, 0)
	if err != nil {
		return nil, err
	}
	return &retryingStream{ctx: ctx, current: stream, open: open, delay: d.retryDelay, observe: d.retryObserver, retries: retries}, nil
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

const maxProviderStreamRetries = 5

type retryingStream struct {
	ctx     context.Context
	current hyprovider.Stream
	open    func() (hyprovider.Stream, error)
	delay   func(int) time.Duration
	observe RetryObserver
	retries int
	emitted bool
	closed  bool
}

func (s *retryingStream) Recv() (hyprovider.Event, error) {
	for {
		if s.closed {
			return hyprovider.Event{}, fmt.Errorf("provider stream is closed")
		}
		event, recvErr := s.current.Recv()
		cause := recvErr
		if event.Kind == hyprovider.EventError && event.Err != nil {
			cause = event.Err
		}
		if s.emitted && errors.Is(recvErr, io.EOF) {
			return event, recvErr
		}
		if !isRetryableProviderTransport(cause) {
			if recvErr == nil && event.Kind != hyprovider.EventError && event.Kind != hyprovider.EventDone {
				s.emitted = true
			}
			return event, recvErr
		}
		if s.emitted {
			interrupted := fmt.Errorf("provider connection reset after partial response; refusing unsafe replay: %w", cause)
			return streamFailure(event, interrupted)
		}
		if s.retries >= maxProviderStreamRetries {
			return streamFailure(event, fmt.Errorf("provider stream failed after %d retries: %w", maxProviderStreamRetries, cause))
		}

		_ = s.current.Close()
		s.retries++
		wait := providerRetryWait(s.delay, s.retries, cause)
		if err := reportProviderRetry(s.ctx, s.observe, s.retries, wait, cause); err != nil {
			return hyprovider.Event{}, err
		}
		if err := waitForProviderRetry(s.ctx, wait); err != nil {
			return hyprovider.Event{}, err
		}
		next, retries, openErr := openProviderStream(s.ctx, s.open, s.delay, s.observe, s.retries)
		s.retries = retries
		if openErr != nil {
			return streamFailure(event, openErr)
		}
		s.current = next
	}
}

func (s *retryingStream) Close() error {
	s.closed = true
	if s.current == nil {
		return nil
	}
	return s.current.Close()
}

func openProviderStream(ctx context.Context, open func() (hyprovider.Stream, error), delay func(int) time.Duration, observe RetryObserver, retries int) (hyprovider.Stream, int, error) {
	for {
		stream, err := open()
		if err == nil {
			return stream, retries, nil
		}
		if !isRetryableProviderTransport(err) || retries >= maxProviderStreamRetries {
			if isRetryableProviderTransport(err) {
				err = fmt.Errorf("provider stream failed after %d retries: %w", maxProviderStreamRetries, err)
			}
			return nil, retries, err
		}
		retries++
		wait := providerRetryWait(delay, retries, err)
		if err := reportProviderRetry(ctx, observe, retries, wait, err); err != nil {
			return nil, retries, err
		}
		if err := waitForProviderRetry(ctx, wait); err != nil {
			return nil, retries, err
		}
	}
}

func providerRetryWait(delay func(int) time.Duration, attempt int, cause error) time.Duration {
	configured := time.Duration(0)
	if delay != nil {
		configured = delay(attempt)
	}
	return max(0, configured, azprovider.SuggestedRetryDelay(cause))
}

func reportProviderRetry(ctx context.Context, observe RetryObserver, attempt int, delay time.Duration, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if observe != nil {
		return observe(RetryProgress{Attempt: attempt, Max: maxProviderStreamRetries, Delay: delay, Cause: cause})
	}
	return nil
}

func waitForProviderRetry(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func streamFailure(event hyprovider.Event, err error) (hyprovider.Event, error) {
	if event.Kind == hyprovider.EventError {
		event.Err = err
		return event, nil
	}
	return hyprovider.Event{}, err
}

func isRetryableProviderTransport(err error) bool {
	return azprovider.IsRetryableTransport(err)
}

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
