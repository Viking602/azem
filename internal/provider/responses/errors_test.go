package responses

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"resty.dev/v3"

	hyprovider "github.com/Viking602/venat/provider"
)

func testResponse(status int, header http.Header, body string) *resty.Response {
	raw := &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	return &resty.Response{RawResponse: raw, Body: raw.Body}
}

func TestHTTPErrorClassifiesAndBoundsBody(t *testing.T) {
	response := testResponse(http.StatusTooManyRequests, make(http.Header), `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`)
	response.Header().Set("Retry-After", "3")
	err := HTTPError(response)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Kind != ErrorRateLimit || apiError.RetryAfter != 3*time.Second || apiError.Code != "rate_limit_exceeded" {
		t.Fatalf("error=%+v", err)
	}
	if !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("provider diagnostic was lost from Error(): %v", err)
	}
	if hyprovider.ErrorKindOf(err) != hyprovider.ErrorRateLimit || !hyprovider.IsRetryableError(err) {
		t.Fatalf("SDK classification = %q retryable=%v", hyprovider.ErrorKindOf(err), hyprovider.IsRetryableError(err))
	}
}

func TestHTTPErrorClassifiesConnectionInterruptionStatusesAsServerErrors(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusConflict} {
		response := testResponse(status, make(http.Header), `{"error":{"message":"connection interrupted"}}`)
		err := HTTPError(response)
		var apiError *APIError
		if !errors.As(err, &apiError) || apiError.Kind != ErrorServer {
			t.Fatalf("HTTP %d error=%+v, want retryable server error", status, err)
		}
	}
}

func TestHTTPErrorParsesNonstandardCodexBadRequestDetail(t *testing.T) {
	response := testResponse(
		http.StatusBadRequest,
		make(http.Header),
		`{"detail":"The model 'codex-auto-review' does not exist"}`,
	)
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

func TestRetryAfterParsesCodexAndRateLimitHints(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	for _, test := range []struct {
		name   string
		header string
		value  string
		want   time.Duration
	}{
		{name: "milliseconds", header: "retry-after-ms", value: "250", want: 250 * time.Millisecond},
		{name: "seconds", header: "Retry-After", value: "1.5", want: 1500 * time.Millisecond},
		{name: "reset epoch milliseconds", header: "x-ratelimit-reset-ms", value: "2000000000250", want: 250 * time.Millisecond},
		{name: "reset epoch seconds", header: "x-ratelimit-reset", value: "2000000002", want: 2 * time.Second},
		{name: "reset after", header: "x-ratelimit-reset-after", value: "3.5", want: 3500 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			header := make(http.Header)
			header.Set(test.header, test.value)
			if got := retryAfter(header, now); got != test.want {
				t.Fatalf("retry delay = %s, want %s", got, test.want)
			}
		})
	}
}
