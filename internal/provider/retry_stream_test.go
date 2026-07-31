package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	hyprovider "github.com/Viking602/go-hydaelyn/provider"

	"github.com/Viking602/azem/internal/provider/responses"
)

func TestIsRetryableTransportCoversConnectionInterruptions(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "end of stream", err: io.EOF},
		{name: "unexpected end of stream", err: io.ErrUnexpectedEOF},
		{name: "closed pipe", err: io.ErrClosedPipe},
		{name: "connection reset", err: syscall.ECONNRESET},
		{name: "connection refused", err: syscall.ECONNREFUSED},
		{name: "connection aborted", err: syscall.ECONNABORTED},
		{name: "broken pipe", err: syscall.EPIPE},
		{name: "network unreachable", err: syscall.ENETUNREACH},
		{name: "host unreachable", err: syscall.EHOSTUNREACH},
		{name: "timed out", err: syscall.ETIMEDOUT},
		{name: "wrapped connection reset", err: &url.Error{Op: "POST", URL: "https://example.test", Err: syscall.ECONNRESET}},
		{name: "network operation refused", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{name: "DNS lookup failure", err: &net.DNSError{Err: "no such host", Name: "api.example.test", IsNotFound: true}},
		{name: "provider server error", err: &responses.APIError{Kind: responses.ErrorServer, Code: "server_error"}},
		{name: "provider overload", err: &responses.APIError{Kind: responses.ErrorRateLimit, Code: "server_is_overloaded"}},
		{name: "provider stream EOF", err: &responses.APIError{Kind: responses.ErrorStream, Message: "EOF"}},
		{name: "provider stream closed", err: &responses.APIError{Kind: responses.ErrorStream, Message: "provider stream failed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !IsRetryableTransport(test.err) {
				t.Fatalf("connection interruption was not retryable: %T %v", test.err, test.err)
			}
		})
	}
}

func TestIsRetryableTransportRejectsTerminalFailures(t *testing.T) {
	for _, err := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		&responses.APIError{Kind: responses.ErrorAuthentication, Code: "invalid_token"},
		&responses.APIError{Kind: responses.ErrorInvalidRequest, Code: "invalid_request"},
		errors.New("x509: certificate signed by unknown authority"),
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("x509: certificate signed by unknown authority")},
	} {
		if IsRetryableTransport(err) {
			t.Fatalf("terminal failure was classified retryable: %v", err)
		}
	}
}

func TestOpenRetryingStreamAttemptsConnectionInterruptionFiveTimes(t *testing.T) {
	attempts := 0
	var progress []RetryProgress
	stream, err := OpenRetryingStream(context.Background(), func() (hyprovider.Stream, error) {
		attempts++
		return nil, io.EOF
	}, RetryOptions{
		Delay: func(int) time.Duration { return 0 },
		Observer: func(retry RetryProgress) error {
			progress = append(progress, retry)
			return nil
		},
	})
	if err == nil || stream != nil {
		t.Fatalf("stream=%T error=%v, want exhausted connection failure", stream, err)
	}
	if attempts != 6 || len(progress) != DefaultMaxStreamRetries {
		t.Fatalf("attempts=%d progress=%d, want initial connection plus five retries", attempts, len(progress))
	}
	for index, retry := range progress {
		if retry.Attempt != index+1 || retry.Max != DefaultMaxStreamRetries || !errors.Is(retry.Cause, io.EOF) {
			t.Fatalf("retry %d = %+v", index+1, retry)
		}
	}
}

type eventThenEOFStream struct {
	emitted bool
}

func (s *eventThenEOFStream) Recv() (hyprovider.Event, error) {
	if s.emitted {
		return hyprovider.Event{}, io.EOF
	}
	s.emitted = true
	return hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: "done"}, nil
}

func (*eventThenEOFStream) Close() error { return nil }

func TestRetryingStreamPreservesNormalEOFAfterOutput(t *testing.T) {
	attempts := 0
	stream, err := OpenRetryingStream(context.Background(), func() (hyprovider.Stream, error) {
		attempts++
		return &eventThenEOFStream{}, nil
	}, RetryOptions{Delay: func(int) time.Duration { return 0 }})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventTextDelta {
		t.Fatalf("first event=%+v error=%v", event, err)
	}
	_, err = stream.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stream ending error=%v, want EOF", err)
	}
	if attempts != 1 {
		t.Fatalf("normal EOF reopened provider %d times", attempts)
	}
}

func TestOpenRetryingStreamHonorsServerRetryDelayBeforeWaiting(t *testing.T) {
	stop := errors.New("stop before sleep")
	var progress RetryProgress
	stream, err := OpenRetryingStream(context.Background(), func() (hyprovider.Stream, error) {
		return nil, &responses.APIError{
			Kind: responses.ErrorRateLimit, Code: "server_is_overloaded", RetryAfter: 3 * time.Second,
		}
	}, RetryOptions{
		Delay: func(int) time.Duration { return 0 },
		Observer: func(retry RetryProgress) error {
			progress = retry
			return stop
		},
	})
	if stream != nil || !errors.Is(err, stop) {
		t.Fatalf("stream=%T error=%v, want observer stop", stream, err)
	}
	if progress.Delay != 3*time.Second || progress.Attempt != 1 {
		t.Fatalf("retry progress=%+v, want server delay on first retry", progress)
	}
}
