// Package errcode defines the stable product-level provider error taxonomy.
// Every provider failure surfaced to users (run_failed, provider_retry) is
// classified into one of these codes so the desktop and TUI can present and
// reason about failures without parsing error prose. Classification is
// advisory presentation metadata only: Venat remains the single retry owner
// and its RetryableError contract is never overridden here.
package errcode

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/Viking602/azem/internal/provider/responses"
	hyprovider "github.com/Viking602/venat/provider"
)

// Code is a stable, machine-readable provider failure class.
type Code string

const (
	// CodeAuth covers missing, expired, or rejected credentials.
	CodeAuth Code = "auth"
	// CodeQuota covers entitlement and subscription/usage exhaustion.
	CodeQuota Code = "quota"
	// CodeRateLimit covers throttling that clears on its own.
	CodeRateLimit Code = "rate_limit"
	// CodeContextOverflow covers requests rejected for exceeding the model
	// context or output window.
	CodeContextOverflow Code = "context_overflow"
	// CodeEmptyResponse covers structurally successful calls that produced no
	// usable output.
	CodeEmptyResponse Code = "empty_response"
	// CodeInvalidRequest covers permanently malformed or unsupported requests.
	CodeInvalidRequest Code = "invalid_request"
	// CodeServer covers provider-side 5xx/overload failures.
	CodeServer Code = "server"
	// CodeTransport covers connection, TLS, and stream interruption failures.
	CodeTransport Code = "transport"
	// CodeCancelled covers local cancellation and deadline expiry.
	CodeCancelled Code = "cancelled"
	// CodeUnknown is the explicit fallback; consumers must treat it as
	// non-retryable and show the original message.
	CodeUnknown Code = "unknown"
)

// DataKey is the runtime event Data key carrying the classified code.
const DataKey = "errorCode"

// Classify maps any error from the provider call chain to a stable code.
// Typed errors win over sentinel and transport heuristics; unrecognized
// errors classify as CodeUnknown, never as a guess.
func Classify(err error) Code {
	if err == nil {
		return CodeUnknown
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CodeCancelled
	}
	var apiErr *responses.APIError
	if errors.As(err, &apiErr) {
		return fromAPIError(apiErr)
	}
	var providerErr *hyprovider.Error
	if errors.As(err, &providerErr) {
		return fromProviderKind(providerErr.Kind)
	}
	if isTransportError(err) {
		return CodeTransport
	}
	return CodeUnknown
}

// Retryable reports whether failures with this code are worth retrying.
// This is presentation guidance for the UI; the runtime retry decision stays
// with Venat's RetryableError contract.
func Retryable(code Code) bool {
	switch code {
	case CodeRateLimit, CodeServer, CodeTransport:
		return true
	default:
		return false
	}
}

func fromAPIError(err *responses.APIError) Code {
	switch err.Kind {
	case responses.ErrorAuthentication:
		return CodeAuth
	case responses.ErrorEntitlement:
		return CodeQuota
	case responses.ErrorRateLimit:
		return CodeRateLimit
	case responses.ErrorContextLimit:
		return CodeContextOverflow
	case responses.ErrorInvalidRequest:
		return CodeInvalidRequest
	case responses.ErrorServer:
		return CodeServer
	case responses.ErrorStream:
		return CodeTransport
	default:
		return CodeUnknown
	}
}

func fromProviderKind(kind hyprovider.ErrorKind) Code {
	switch kind {
	case hyprovider.ErrorAuthentication:
		return CodeAuth
	case hyprovider.ErrorPermission:
		return CodeQuota
	case hyprovider.ErrorRateLimit:
		return CodeRateLimit
	case hyprovider.ErrorInvalidRequest:
		return CodeInvalidRequest
	case hyprovider.ErrorServer:
		return CodeServer
	case hyprovider.ErrorStream:
		return CodeTransport
	default:
		return CodeUnknown
	}
}

func isTransportError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"connection reset", "connection refused", "broken pipe", "tls handshake", "no such host"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
