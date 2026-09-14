package provider

import (
	"context"
	"errors"
	"fmt"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
	"github.com/Viking602/venat/tool/kit"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/responses"
	hyprovider "github.com/Viking602/venat/provider"
)

type retryableFixtureError struct{ message string }

func (failure retryableFixtureError) Error() string { return failure.message }
func (retryableFixtureError) Retryable() bool       { return true }

type retryFixtureDriver struct {
	streams [][]hyprovider.Event
	calls   int
	openErr error
	recvErr error
}

func (*retryFixtureDriver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "retry-fixture"}
}

func (driver *retryFixtureDriver) Stream(context.Context, hyprovider.Request) (hyprovider.Stream, error) {
	driver.calls++
	if driver.openErr != nil {
		return nil, driver.openErr
	}
	events := driver.streams[0]
	driver.streams = driver.streams[1:]
	if driver.recvErr != nil {
		return &failureAfterEvents{Stream: hyprovider.NewSliceStream(events), failure: driver.recvErr}, nil
	}
	return hyprovider.NewSliceStream(events), nil
}

type failureAfterEvents struct {
	hyprovider.Stream
	failure error
}

func (s *failureAfterEvents) Recv() (hyprovider.Event, error) {
	event, err := s.Stream.Recv()
	if err == io.EOF {
		return hyprovider.Event{}, s.failure
	}
	return event, err
}

func TestRejectedStreamingTrailerIsTerminalWithoutReplay(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 502} {
		for _, partial := range []bool{false, true} {
			for _, retries := range []int{0, 2} {
				t.Run(fmt.Sprintf("%d/partial=%t/retries=%d", status, partial, retries), func(t *testing.T) {
					failure := hyprovider.NewHTTPError("devin", status, "request rejected")
					var events []hyprovider.Event
					if partial {
						events = append(events, hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: "partial"})
					}
					inner := &retryFixtureDriver{streams: [][]hyprovider.Event{events, events, events}, recvErr: failure}
					stream, err := WithRetry(inner, RetryConfig{MaxRetries: retries}).Stream(context.Background(), hyprovider.Request{})
					if err != nil {
						t.Fatal(err)
					}
					assertRejectedStream(t, stream, failure, partial)
					expectedCalls := 1
					if !partial && (status == 429 || status == 502) {
						expectedCalls += retries
					}
					if inner.calls != expectedCalls {
						t.Fatalf("physical calls=%d want=%d", inner.calls, expectedCalls)
					}
					_ = stream.Close()
				})
			}
		}
	}
}

func TestLostTransportRetainsUnknownOutcome(t *testing.T) {
	unknown := errors.New("connection lost")
	inner := &retryFixtureDriver{streams: [][]hyprovider.Event{nil}, recvErr: unknown}
	stream, err := WithRetry(inner, RetryConfig{}).Stream(context.Background(), hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Recv(); !errors.Is(err, unknown) {
		t.Fatalf("unknown outcome was changed: %v", err)
	}
}

func assertRejectedStream(t *testing.T, stream hyprovider.Stream, failure error, partial bool) {
	t.Helper()
	if partial {
		event, err := stream.Recv()
		if err != nil || event.Text != "partial" {
			t.Fatal("lost partial text")
		}
	}
	event, err := stream.Recv()
	if err != nil || event.Kind != hyprovider.EventError || !errors.Is(event.Err, failure) {
		t.Fatalf("terminal event=%+v err=%v", event, err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("terminal error repeated: %v", err)
	}
}

func TestForbiddenResponseIsTerminalWithoutRetryOrUnknownOutcome(t *testing.T) {
	for _, failure := range []error{auth.EntitlementError{Provider: "grok", Status: 403}, &responses.APIError{Kind: responses.ErrorEntitlement, StatusCode: 403, Message: "not permitted"}, hyprovider.NewHTTPError("cursor", 403, "not permitted"), hyprovider.NewHTTPError("devin", 400, "invalid_argument")} {
		for _, retries := range []int{0, 2} {
			inner := &retryFixtureDriver{openErr: failure}
			stream, err := WithRetry(inner, RetryConfig{MaxRetries: retries}).Stream(context.Background(), hyprovider.Request{})
			if err != nil {
				t.Fatalf("confirmed HTTP rejection escaped as uncertain stream-open failure: %v", err)
			}
			event, err := stream.Recv()
			_ = stream.Close()
			if err != nil || event.Kind != hyprovider.EventError || !errors.Is(event.Err, failure) || inner.calls != 1 {
				t.Fatalf("event=%+v err=%v calls=%d", event, err, inner.calls)
			}
		}
	}
	unknown := errors.New("connection lost after request was sent")
	inner := &retryFixtureDriver{openErr: unknown}
	if stream, err := WithRetry(inner, RetryConfig{}).Stream(context.Background(), hyprovider.Request{}); stream != nil || err != unknown {
		t.Fatalf("uncertain transport outcome was changed: stream=%v err=%v", stream, err)
	}
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

func TestEmptyResponseRetriesBeforeContinuation(t *testing.T) {
	inner := &retryFixtureDriver{streams: [][]hyprovider.Event{
		{{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
		{{Kind: hyprovider.EventTextDelta, Text: "final answer"}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
	}}
	stream, err := WithRetry(inner, RetryConfig{MaxRetries: 1}).Stream(context.Background(), hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Text != "final answer" || inner.calls != 2 {
		t.Fatalf("event=%+v err=%v calls=%d", event, err, inner.calls)
	}
}

func TestEmptyResponseFailsExplicitlyWhenRetriesDisabled(t *testing.T) {
	inner := &retryFixtureDriver{streams: [][]hyprovider.Event{{{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}}}}
	stream, err := WithRetry(inner, RetryConfig{}).Stream(context.Background(), hyprovider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, err = stream.Recv()
	var failure *hyprovider.Error
	if !errors.As(err, &failure) || failure.Code != "empty_response" || inner.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, inner.calls)
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

func TestEmptyModelTurnAfterToolDoesNotReplayTool(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(fmt.Sprint(guarded), func(t *testing.T) {
			calls := 0
			driver, err := kit.Tool("lookup", func(context.Context, struct{}) (string, error) { calls++; return "result", nil })
			if err != nil {
				t.Fatal(err)
			}
			inner := &retryFixtureDriver{streams: [][]hyprovider.Event{
				{{Kind: hyprovider.EventToolCall, ToolCall: &message.ToolCall{ID: "call-1", Name: "lookup", Arguments: []byte(`{}`)}}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonToolUse}},
				{{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
				{{Kind: hyprovider.EventTextDelta, Text: "finished"}, {Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}},
			}}
			var provider hyprovider.Driver = inner
			if guarded {
				provider = WithRetry(inner, RetryConfig{MaxRetries: 1})
			}
			engine := hyagent.Engine{Model: "test", Provider: provider, Tools: tool.NewBus(driver), LoopPolicy: hyagent.LoopPolicy{MaxIterations: 3}, Boundaries: hyagent.BoundaryObserverFunc(func(_ context.Context, c hyagent.Continuation) error {
				_, err := hyagent.EncodeContinuation(c)
				return err
			})}
			result := engine.Run(context.Background(), hyagent.Request{Prompt: "lookup then answer"}, hyagent.OutputPolicy{})
			if guarded && result.Failure != nil {
				t.Fatalf("guarded failed: %v", result.Failure)
			}
			if !guarded && (result.Failure == nil || !strings.Contains(fmt.Sprint(result.Failure), "validating_output phase must end")) {
				t.Fatalf("legacy did not reproduce: %+v", result)
			}
			if calls != 1 {
				t.Fatalf("tool executed %d times", calls)
			}
		})
	}
}

func TestEmptyResponseRetryBudgetIsBounded(t *testing.T) {
	for _, events := range [][]hyprovider.Event{nil, {{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete}}} {
		inner := &retryFixtureDriver{streams: [][]hyprovider.Event{events, events}}
		stream, err := WithRetry(inner, RetryConfig{MaxRetries: 1}).Stream(context.Background(), hyprovider.Request{})
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		_, err = stream.Recv()
		if err == nil || !strings.Contains(err.Error(), "empty_response") || inner.calls != 2 {
			t.Fatalf("err=%v calls=%d", err, inner.calls)
		}
	}
}
