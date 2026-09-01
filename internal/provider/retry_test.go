package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	hyprovider "github.com/Viking602/venat/provider"
)

type retryableFixtureError struct{ message string }

func (failure retryableFixtureError) Error() string { return failure.message }
func (retryableFixtureError) Retryable() bool       { return true }

type retryFixtureDriver struct {
	streams [][]hyprovider.Event
	calls   int
}

func (*retryFixtureDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "retry-fixture"}
}

func (driver *retryFixtureDriver) Stream(context.Context, hyprovider.Request) (hyprovider.Stream, error) {
	driver.calls++
	events := driver.streams[0]
	driver.streams = driver.streams[1:]
	return hyprovider.NewSliceStream(events), nil
}

func TestWithRetryReopensOnlyBeforeFirstValidEvent(t *testing.T) {
	inner := &retryFixtureDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventError, Err: retryableFixtureError{message: "open failed"}}},
		{{Kind: hyprovider.EventTextDelta, Text: "recovered"}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
	}}
	var attempts []int
	driver := WithRetry(inner, RetryConfig{
		MaxRetries: 2,
		Observer: func(progress hyprovider.RetryProgress) error {
			attempts = append(attempts, progress.Attempt)
			return nil
		},
	})
	stream, err := driver.Stream(context.Background(), hyprovider.Request{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Recv()
	if err != nil || first.Kind != hyprovider.EventTextDelta || first.Text != "recovered" {
		t.Fatalf("first recovered event = %#v, %v", first, err)
	}
	if inner.calls != 2 || len(attempts) != 1 || attempts[0] != 1 {
		t.Fatalf("physical calls=%d retry attempts=%v, want 2 and [1]", inner.calls, attempts)
	}
}

func TestWithRetryNeverReopensPartialStream(t *testing.T) {
	inner := &retryFixtureDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventTextDelta, Text: "partial"}, {Kind: hyprovider.EventError, Err: retryableFixtureError{message: "lost"}}},
		{{Kind: hyprovider.EventTextDelta, Text: "must not run"}},
	}}
	driver := WithRetry(inner, RetryConfig{MaxRetries: 2, BaseDelay: time.Nanosecond})
	stream, err := driver.Stream(context.Background(), hyprovider.Request{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if event, recvErr := stream.Recv(); recvErr != nil || event.Text != "partial" {
		t.Fatalf("partial event = %#v, %v", event, recvErr)
	}
	_, recvErr := stream.Recv()
	var partial *hyprovider.PartialStreamError
	if !errors.As(recvErr, &partial) || partial.Retryable() {
		t.Fatalf("partial failure = %v, want non-retryable PartialStreamError", recvErr)
	}
	if inner.calls != 1 {
		t.Fatalf("physical calls = %d, want 1", inner.calls)
	}
}

func TestWithRetryDisabledReturnsOriginalDriver(t *testing.T) {
	inner := &retryFixtureDriver{}
	if got := WithRetry(inner, RetryConfig{}); got != inner {
		t.Fatalf("disabled retry wrapper = %T, want original driver", got)
	}
}

func TestRetryDelayUsesConfiguredExponentialBackoffAndCap(t *testing.T) {
	delay := retryDelay(5*time.Millisecond, 12*time.Millisecond)
	for attempt, want := range []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 12 * time.Millisecond, 12 * time.Millisecond} {
		if got := delay(attempt + 1); got != want {
			t.Fatalf("attempt %d delay=%s, want %s", attempt+1, got, want)
		}
	}
}
