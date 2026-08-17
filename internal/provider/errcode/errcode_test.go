package errcode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/Viking602/azem/internal/provider/responses"
	hyprovider "github.com/Viking602/venat/provider"
)

func TestClassifyAPIErrors(t *testing.T) {
	cases := map[responses.ErrorKind]Code{
		responses.ErrorAuthentication: CodeAuth,
		responses.ErrorEntitlement:    CodeQuota,
		responses.ErrorRateLimit:      CodeRateLimit,
		responses.ErrorContextLimit:   CodeContextOverflow,
		responses.ErrorInvalidRequest: CodeInvalidRequest,
		responses.ErrorServer:         CodeServer,
		responses.ErrorStream:         CodeTransport,
	}
	for kind, expected := range cases {
		err := fmt.Errorf("wrapped: %w", &responses.APIError{Kind: kind, StatusCode: 500})
		if got := Classify(err); got != expected {
			t.Errorf("APIError %s classified as %s, want %s", kind, got, expected)
		}
	}
}

func TestClassifyVenatProviderErrors(t *testing.T) {
	cases := map[hyprovider.ErrorKind]Code{
		hyprovider.ErrorAuthentication: CodeAuth,
		hyprovider.ErrorPermission:     CodeQuota,
		hyprovider.ErrorRateLimit:      CodeRateLimit,
		hyprovider.ErrorInvalidRequest: CodeInvalidRequest,
		hyprovider.ErrorServer:         CodeServer,
		hyprovider.ErrorStream:         CodeTransport,
		hyprovider.ErrorUnknown:        CodeUnknown,
	}
	for kind, expected := range cases {
		err := fmt.Errorf("wrapped: %w", &hyprovider.Error{Kind: kind, Message: "x"})
		if got := Classify(err); got != expected {
			t.Errorf("provider error %s classified as %s, want %s", kind, got, expected)
		}
	}
}

func TestClassifyCancellationAndTransport(t *testing.T) {
	if got := Classify(context.Canceled); got != CodeCancelled {
		t.Errorf("context.Canceled classified as %s", got)
	}
	if got := Classify(fmt.Errorf("read: %w", io.ErrUnexpectedEOF)); got != CodeTransport {
		t.Errorf("unexpected EOF classified as %s", got)
	}
	if got := Classify(errors.New("dial tcp: connection refused")); got != CodeTransport {
		t.Errorf("connection refused classified as %s", got)
	}
	if got := Classify(errors.New("something novel went wrong")); got != CodeUnknown {
		t.Errorf("novel error classified as %s, want unknown", got)
	}
}

func TestRetryableGuidance(t *testing.T) {
	retryable := []Code{CodeRateLimit, CodeServer, CodeTransport}
	terminal := []Code{CodeAuth, CodeQuota, CodeContextOverflow, CodeEmptyResponse, CodeInvalidRequest, CodeCancelled, CodeUnknown}
	for _, code := range retryable {
		if !Retryable(code) {
			t.Errorf("%s must be retry guidance", code)
		}
	}
	for _, code := range terminal {
		if Retryable(code) {
			t.Errorf("%s must not be retry guidance", code)
		}
	}
}
