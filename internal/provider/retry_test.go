package provider

import (
	"context"
	"errors"
	"fmt"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
	"github.com/Viking602/venat/tool/kit"
	"strings"
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
