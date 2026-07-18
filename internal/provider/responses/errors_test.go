package responses

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPErrorClassifiesAndBoundsBody(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`))}
	response.Header.Set("Retry-After", "3")
	err := HTTPError(response)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Kind != ErrorRateLimit || apiError.RetryAfter != 3*time.Second || apiError.Code != "rate_limit_exceeded" {
		t.Fatalf("error=%+v", err)
	}
	if !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("provider diagnostic was lost from Error(): %v", err)
	}
}

func TestHTTPErrorParsesNonstandardCodexBadRequestDetail(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"detail":"The model 'codex-auto-review' does not exist"}`)),
	}
	err := HTTPError(response)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Kind != ErrorInvalidRequest || apiError.StatusCode != http.StatusBadRequest {
		t.Fatalf("error=%+v", err)
	}
	if !strings.Contains(err.Error(), "codex-auto-review") {
		t.Fatalf("Codex HTTP 400 diagnostic was lost: %v", err)
	}
	if strings.ContainsAny(err.Error(), "{}") {
		t.Fatalf("Codex HTTP 400 diagnostic exposed raw JSON: %v", err)
	}
}

func TestStreamErrorClassifiesAuthentication(t *testing.T) {
	err := streamError([]byte(`{"type":"response.failed","response":{"error":{"code":"invalid_token","message":"expired"}}}`))
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Kind != ErrorAuthentication {
		t.Fatalf("error=%+v", err)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("stream diagnostic was lost: %v", err)
	}
}
