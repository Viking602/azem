package provider

import (
	"context"
	"io"
	"time"

	hyprovider "github.com/Viking602/venat/provider"
)

// RetryConfig is Azem's single provider-stream retry policy. MaxRetries counts
// reopen attempts after the first physical request; zero disables retries.
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Observer   hyprovider.RetryObserver
}

// WithRetry wraps one provider driver with pre-emission retry semantics. The
// inner Stream call is one physical request, so metering must wrap the physical
// driver before WithRetry is applied.
func WithRetry(driver hyprovider.Driver, config RetryConfig) hyprovider.Driver {
	if driver == nil {
		return driver
	}
	return &retryDriver{inner: driver, config: config}
}

type retryDriver struct {
	inner  hyprovider.Driver
	config RetryConfig
}

func (driver *retryDriver) Metadata() hyprovider.Metadata { return driver.inner.Metadata() }

func (driver *retryDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	open := func() (hyprovider.Stream, error) {
		stream, err := driver.inner.Stream(ctx, request)
		if err != nil {
			return nil, err
		}
		return &outputCheckedStream{Stream: stream}, nil
	}
	if driver.config.MaxRetries <= 0 {
		return open()
	}
	return hyprovider.OpenRetryingStream(ctx, open, hyprovider.StreamRetryOptions{
		Max:      driver.config.MaxRetries,
		Delay:    retryDelay(driver.config.BaseDelay, driver.config.MaxDelay),
		MaxDelay: driver.config.MaxDelay,
		Observer: driver.config.Observer,
	})
}

func retryDelay(base, maximum time.Duration) func(int) time.Duration {
	if base <= 0 {
		return func(int) time.Duration { return 0 }
	}
	const maxDuration = time.Duration(1<<63 - 1)
	return func(attempt int) time.Duration {
		delay := base
		if maximum > 0 && delay >= maximum {
			return maximum
		}
		for remaining := max(attempt-1, 0); remaining > 0; remaining-- {
			if delay > maxDuration/2 {
				if maximum > 0 {
					return maximum
				}
				return maxDuration
			}
			delay *= 2
			if maximum > 0 && delay >= maximum {
				return maximum
			}
		}
		return delay
	}
}

// Reject empty successful turns before Venat builds a validating-output
// continuation. This runs inside the existing retry owner, so only the current
// physical model request can be reopened, never previously executed tools.
type outputCheckedStream struct {
	hyprovider.Stream
	output   bool
	terminal bool
}

func (s *outputCheckedStream) Identity() hyprovider.StreamIdentity {
	if identified, ok := s.Stream.(hyprovider.IdentifiedStream); ok {
		return identified.Identity()
	}
	return hyprovider.StreamIdentity{}
}

func (s *outputCheckedStream) Recv() (hyprovider.Event, error) {
	event, err := s.Stream.Recv()
	if s.terminal {
		return event, err
	}
	switch event.Kind {
	case hyprovider.EventTextDelta:
		s.output = s.output || event.Text != ""
	case hyprovider.EventThinkingDelta:
		s.output = s.output || event.Thinking != ""
	case hyprovider.EventToolCall:
		s.output = s.output || event.ToolCall != nil
	}
	empty := !s.output && ((err == io.EOF) || (err == nil && event.Kind == hyprovider.EventDone && event.StopReason != hyprovider.StopReasonAborted && event.StopReason != hyprovider.StopReasonError && event.StopReason != hyprovider.StopReasonContentFilter))
	if empty {
		s.terminal = true
		return hyprovider.Event{}, &hyprovider.Error{Kind: hyprovider.ErrorStream, Code: "empty_response", Message: "The model returned no response. Please try again."}
	}
	s.terminal = err != nil || event.Kind == hyprovider.EventDone || event.Kind == hyprovider.EventError
	return event, err
}
