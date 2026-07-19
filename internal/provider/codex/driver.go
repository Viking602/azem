package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	hyprovider "github.com/Viking602/go-hydaelyn/provider"

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
	open := func() (hyprovider.Stream, error) {
		return d.openStream(ctx, payload, reverseNames, cacheKey)
	}
	stream, retries, err := openProviderStream(ctx, open, d.retryDelay, 0)
	if err != nil {
		return nil, err
	}
	return &retryingStream{ctx: ctx, current: stream, open: open, delay: d.retryDelay, retries: retries}, nil
}

func (d *Driver) openStream(ctx context.Context, payload []byte, reverseNames map[string]string, cacheKey string) (hyprovider.Stream, error) {
	streamContext, cancel := context.WithCancel(ctx)
	response, err := d.auth.DoStreamWithRefresh(streamContext, "chatgpt", d.accountID, func(auth.Credential) (*http.Request, error) {
		httpRequest, err := http.NewRequest(http.MethodPost, d.endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		httpRequest.Header.Set("Accept", "text/event-stream")
		httpRequest.Header.Set("OpenAI-Beta", "responses=experimental")
		httpRequest.Header.Set("originator", "codex_cli_rs")
		httpRequest.Header.Set("User-Agent", "azem/1")
		if cacheKey != "" {
			httpRequest.Header.Set("conversation_id", cacheKey)
			httpRequest.Header.Set("session_id", cacheKey)
		}
		return httpRequest, nil
	})
	if err != nil {
		cancel()
		return nil, err
	}
	stream, err := responses.Open(response, streamContext, cancel)
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
		if err := waitForProviderRetry(s.ctx, s.delay, s.retries); err != nil {
			return hyprovider.Event{}, err
		}
		next, retries, openErr := openProviderStream(s.ctx, s.open, s.delay, s.retries)
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

func openProviderStream(ctx context.Context, open func() (hyprovider.Stream, error), delay func(int) time.Duration, retries int) (hyprovider.Stream, int, error) {
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
		if err := waitForProviderRetry(ctx, delay, retries); err != nil {
			return nil, retries, err
		}
	}
}

func waitForProviderRetry(ctx context.Context, delay func(int) time.Duration, attempt int) error {
	if delay == nil {
		return nil
	}
	wait := delay(attempt)
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
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiError *responses.APIError
	if errors.As(err, &apiError) && apiError.Kind != responses.ErrorServer && apiError.Kind != responses.ErrorStream {
		return false
	}
	if apiError != nil && apiError.Kind == responses.ErrorStream && strings.EqualFold(strings.TrimSpace(apiError.Message), "EOF") {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Err != nil && isRetryableProviderTransport(urlError.Err) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "upstream connection reset") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "tls: bad record mac") ||
		strings.Contains(message, "unexpected eof") ||
		strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "server closed idle connection") ||
		strings.Contains(message, "client connection lost") ||
		strings.Contains(message, "connection aborted") ||
		strings.Contains(message, "use of closed network connection")
}

func providerStreamRetryDelay(attempt int) time.Duration {
	delay := 200 * time.Millisecond * time.Duration(1<<max(0, attempt-1))
	return min(delay, 2*time.Second)
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
