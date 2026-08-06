package llmuxdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	sdk "github.com/Viking602/llmux"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/responses"
)

type streamAdapter struct {
	inner     sdk.Stream
	reporter  responses.UsageReporter
	requestID string
	toolUse   bool
}

func (s *streamAdapter) Recv() (hyprovider.Event, error) {
	for {
		part, err := s.inner.Recv()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return hyprovider.Event{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonAborted}, nil
			}
			return hyprovider.Event{}, mapError(err)
		}
		switch part.Kind {
		case sdk.PartResponseMetadata:
			s.requestID = part.Response.ID
		case sdk.PartTextDelta:
			if part.Delta != "" {
				return hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: part.Delta, TextPhase: hyprovider.TextPhaseFinalAnswer}, nil
			}
		case sdk.PartReasoningDelta:
			if part.Delta != "" {
				return hyprovider.Event{Kind: hyprovider.EventThinkingDelta, Thinking: part.Delta}, nil
			}
		case sdk.PartToolCall:
			if part.ToolCall == nil {
				return hyprovider.Event{Kind: hyprovider.EventError, Err: &hyprovider.Error{Provider: "llmux", Kind: hyprovider.ErrorStream, Message: "SDK emitted an empty tool call"}}, nil
			}
			s.toolUse = true
			return hyprovider.Event{Kind: hyprovider.EventToolCall, ToolCall: &message.ToolCall{
				ID: part.ToolCall.ID, Name: part.ToolCall.Name, Arguments: append(json.RawMessage(nil), part.ToolCall.Arguments...),
			}}, nil
		case sdk.PartFinish:
			return s.finish(part), nil
		case sdk.PartError:
			if part.Err == nil {
				part.Err = fmt.Errorf("SDK provider stream failed")
			}
			return hyprovider.Event{Kind: hyprovider.EventError, Err: mapError(part.Err)}, nil
		}
	}
}

func (s *streamAdapter) finish(part sdk.Part) hyprovider.Event {
	usage := hyprovider.Usage{
		InputTokens: part.Usage.InputTokens, CachedInputTokens: part.Usage.CachedInputTokens,
		CacheWriteInputTokens: part.Usage.CacheWriteInputTokens, OutputTokens: part.Usage.OutputTokens,
		TotalTokens: part.Usage.TotalTokens,
	}
	if s.reporter != nil {
		s.reporter(responses.UsageDetails{
			ProviderRequestID: s.requestID, InputTokens: part.Usage.InputTokens,
			CachedTokens: part.Usage.CachedInputTokens, CacheReported: part.Usage.CachedInputTokensReported,
			CacheWriteTokens: part.Usage.CacheWriteInputTokens, CacheWriteReported: part.Usage.CacheWriteInputTokensReported,
			OutputTokens: part.Usage.OutputTokens, ReasoningTokens: part.Usage.ReasoningTokens, TotalTokens: part.Usage.TotalTokens,
		})
	}
	reason := stopReason(part.FinishReason)
	if s.toolUse {
		reason = hyprovider.StopReasonToolUse
	}
	return hyprovider.Event{Kind: hyprovider.EventDone, StopReason: reason, Usage: usage, ProviderState: append(json.RawMessage(nil), part.ProviderState...)}
}

func (s *streamAdapter) Close() error { return s.inner.Close() }

func stopReason(reason sdk.FinishReason) hyprovider.StopReason {
	switch reason {
	case sdk.FinishStop:
		return hyprovider.StopReasonComplete
	case sdk.FinishToolCalls:
		return hyprovider.StopReasonToolUse
	case sdk.FinishLength:
		return hyprovider.StopReasonMaxTurns
	case sdk.FinishCancelled:
		return hyprovider.StopReasonAborted
	case sdk.FinishError, sdk.FinishContent:
		return hyprovider.StopReasonError
	default:
		return hyprovider.StopReasonUnknown
	}
}

func mapError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var entitlement auth.EntitlementError
	if errors.As(err, &entitlement) {
		return &hyprovider.Error{Provider: entitlement.Provider, Kind: hyprovider.ErrorPermission, StatusCode: entitlement.Status, Message: entitlement.Error()}
	}
	var existing *hyprovider.Error
	if errors.As(err, &existing) {
		return existing
	}
	var providerError *sdk.ProviderError
	if !errors.As(err, &providerError) {
		return err
	}
	return &hyprovider.Error{
		Provider: providerError.Provider, Kind: mapErrorKind(providerError.Kind), Code: providerError.Code,
		StatusCode: providerError.StatusCode, Message: providerError.Message, RetryAfter: providerError.RetryAfter,
	}
}

func mapErrorKind(kind sdk.ErrorKind) hyprovider.ErrorKind {
	switch kind {
	case sdk.ErrorAuthentication:
		return hyprovider.ErrorAuthentication
	case sdk.ErrorPermission:
		return hyprovider.ErrorPermission
	case sdk.ErrorInvalidRequest, sdk.ErrorConflict:
		return hyprovider.ErrorInvalidRequest
	case sdk.ErrorNotFound:
		return hyprovider.ErrorNotFound
	case sdk.ErrorRateLimit:
		return hyprovider.ErrorRateLimit
	case sdk.ErrorServer:
		return hyprovider.ErrorServer
	case sdk.ErrorStream:
		return hyprovider.ErrorStream
	default:
		return hyprovider.ErrorUnknown
	}
}

var _ hyprovider.Stream = (*streamAdapter)(nil)
