package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	hyprovider "github.com/Viking602/go-hydaelyn/provider"

	"github.com/Viking602/azem/internal/provider/responses"
)

const DefaultMaxStreamRetries = 5

type RetryOptions struct {
	Max      int
	Delay    func(int) time.Duration
	Observer RetryObserver
}

func OpenRetryingStream(ctx context.Context, open func() (hyprovider.Stream, error), options RetryOptions) (hyprovider.Stream, error) {
	options = normalizedRetryOptions(options)
	stream, retries, err := openWithRetry(ctx, open, options, 0)
	if err != nil {
		return nil, err
	}
	return &retryingStream{ctx: ctx, current: stream, open: open, options: options, retries: retries}, nil
}

type retryingStream struct {
	ctx     context.Context
	current hyprovider.Stream
	open    func() (hyprovider.Stream, error)
	options RetryOptions
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
		if !IsRetryableTransport(cause) {
			if recvErr == nil && event.Kind != hyprovider.EventError && event.Kind != hyprovider.EventDone {
				s.emitted = true
			}
			return event, recvErr
		}
		if s.emitted {
			return retryStreamFailure(event, fmt.Errorf("provider connection reset after partial response; refusing unsafe replay: %w", cause))
		}
		if s.retries >= s.options.Max {
			return retryStreamFailure(event, fmt.Errorf("provider stream failed after %d retries: %w", s.options.Max, cause))
		}
		_ = s.current.Close()
		s.retries++
		if err := reportAndWait(s.ctx, s.options, s.retries, cause); err != nil {
			return hyprovider.Event{}, err
		}
		next, retries, err := openWithRetry(s.ctx, s.open, s.options, s.retries)
		s.retries = retries
		if err != nil {
			return retryStreamFailure(event, err)
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

func openWithRetry(ctx context.Context, open func() (hyprovider.Stream, error), options RetryOptions, retries int) (hyprovider.Stream, int, error) {
	for {
		stream, err := open()
		if err == nil {
			return stream, retries, nil
		}
		if !IsRetryableTransport(err) || retries >= options.Max {
			if IsRetryableTransport(err) {
				err = fmt.Errorf("provider stream failed after %d retries: %w", options.Max, err)
			}
			return nil, retries, err
		}
		retries++
		if err := reportAndWait(ctx, options, retries, err); err != nil {
			return nil, retries, err
		}
	}
}

func normalizedRetryOptions(options RetryOptions) RetryOptions {
	if options.Max <= 0 {
		options.Max = DefaultMaxStreamRetries
	}
	if options.Delay == nil {
		options.Delay = func(attempt int) time.Duration {
			return min(200*time.Millisecond*time.Duration(1<<max(0, attempt-1)), 2*time.Second)
		}
	}
	return options
}

func reportAndWait(ctx context.Context, options RetryOptions, attempt int, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := max(time.Duration(0), options.Delay(attempt))
	if options.Observer != nil {
		if err := options.Observer(RetryProgress{Attempt: attempt, Max: options.Max, Delay: delay, Cause: cause}); err != nil {
			return err
		}
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryStreamFailure(event hyprovider.Event, err error) (hyprovider.Event, error) {
	if event.Kind == hyprovider.EventError {
		event.Err = err
		return event, nil
	}
	return hyprovider.Event{}, err
}

func IsRetryableTransport(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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
	if errors.As(err, &urlError) && urlError.Err != nil {
		return errors.Is(urlError.Err, io.EOF) || IsRetryableTransport(urlError.Err)
	}
	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "upstream connection reset") || strings.Contains(message, "connection reset") ||
		strings.Contains(message, "tls: bad record mac") || strings.Contains(message, "unexpected eof") ||
		strings.Contains(message, "broken pipe") || strings.Contains(message, "server closed idle connection") ||
		strings.Contains(message, "client connection lost") || strings.Contains(message, "connection aborted") ||
		strings.Contains(message, "use of closed network connection")
}
