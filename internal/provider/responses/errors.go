package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"resty.dev/v3"
)

type ErrorKind string

const (
	ErrorAuthentication ErrorKind = "authentication"
	ErrorEntitlement    ErrorKind = "entitlement"
	ErrorRateLimit      ErrorKind = "rate_limit"
	ErrorContextLimit   ErrorKind = "context_limit"
	ErrorInvalidRequest ErrorKind = "invalid_request"
	ErrorServer         ErrorKind = "server"
	ErrorStream         ErrorKind = "stream"
)

const (
	statusBadRequest          = 400
	statusUnauthorized        = 401
	statusForbidden           = 403
	statusRequestTimeout      = 408
	statusConflict            = 409
	statusUnprocessableEntity = 422
	statusTooManyRequests     = 429
)

type APIError struct {
	Kind       ErrorKind
	StatusCode int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	detail := boundedMessage(e.Message)
	if e.Code != "" {
		if detail != "" {
			return fmt.Sprintf("provider %s error (%s): %s", e.Kind, e.Code, detail)
		}
		return fmt.Sprintf("provider %s error (%s)", e.Kind, e.Code)
	}
	if e.StatusCode != 0 {
		if detail != "" {
			return fmt.Sprintf("provider %s error (HTTP %d): %s", e.Kind, e.StatusCode, detail)
		}
		return fmt.Sprintf("provider %s error (HTTP %d)", e.Kind, e.StatusCode)
	}
	if detail != "" {
		return "provider " + string(e.Kind) + " error: " + detail
	}
	return "provider " + string(e.Kind) + " error"
}

func HTTPError(response *resty.Response) error {
	var body []byte
	if response.Body != nil {
		defer response.Body.Close()
		body, _ = io.ReadAll(io.LimitReader(response.Body, 64<<10))
	}
	code, message := decodeError(body)
	if strings.TrimSpace(message) == "" {
		// ChatGPT's Codex endpoint does not consistently use the public API error
		// envelope. Preserve its bounded raw diagnostic, as Codex CLI does, rather
		// than reducing a useful HTTP 400 response to only the status code.
		message = strings.TrimSpace(string(body))
	}
	statusCode := response.StatusCode()
	kind := ErrorServer
	switch statusCode {
	case statusUnauthorized:
		kind = ErrorAuthentication
	case statusForbidden:
		kind = ErrorEntitlement
	case statusRequestTimeout, statusConflict:
		kind = ErrorServer
	case statusTooManyRequests:
		kind = ErrorRateLimit
	case statusBadRequest, statusUnprocessableEntity:
		kind = classifyCode(code)
		if kind == ErrorStream {
			kind = ErrorInvalidRequest
		}
	default:
		if statusCode < 500 {
			kind = ErrorInvalidRequest
		}
	}
	return &APIError{
		Kind: kind, StatusCode: statusCode, Code: code, Message: boundedMessage(message),
		RetryAfter: retryAfter(response.Header(), string(body), time.Now()),
	}
}

func streamError(payload json.RawMessage) error {
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   *struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Response *struct {
			Error *struct {
				Code    string `json:"code"`
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
			Incomplete *struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
		} `json:"response"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return &APIError{Kind: ErrorStream, Message: "malformed provider error event", RetryAfter: retryAfter(nil, string(payload), time.Now())}
	}
	failure := envelope.Error
	if failure == nil && envelope.Response != nil {
		failure = envelope.Response.Error
	}
	if failure != nil {
		code := firstString(failure.Code, failure.Type)
		return &APIError{
			Kind: classifyCode(code), Code: code, Message: boundedMessage(failure.Message),
			RetryAfter: retryAfter(nil, failure.Message, time.Now()),
		}
	}
	if envelope.Code != "" || envelope.Message != "" {
		return &APIError{
			Kind: classifyCode(envelope.Code), Code: envelope.Code, Message: boundedMessage(envelope.Message),
			RetryAfter: retryAfter(nil, envelope.Message, time.Now()),
		}
	}
	if envelope.Response != nil && envelope.Response.Incomplete != nil {
		code := envelope.Response.Incomplete.Reason
		kind := classifyCode(code)
		if code == "max_output_tokens" {
			kind = ErrorContextLimit
		}
		return &APIError{
			Kind: kind, Code: code, Message: "provider returned an incomplete response",
			RetryAfter: retryAfter(nil, string(payload), time.Now()),
		}
	}
	return &APIError{
		Kind: ErrorStream, Message: "provider stream failed",
		RetryAfter: retryAfter(nil, string(payload), time.Now()),
	}
}

func decodeError(body []byte) (string, string) {
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Code    string          `json:"code"`
		Detail  string          `json:"detail"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return "", ""
	}
	if len(envelope.Error) > 0 {
		var nested struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &nested) == nil {
			return firstString(nested.Code, nested.Type), nested.Message
		}
		var text string
		if json.Unmarshal(envelope.Error, &text) == nil {
			return envelope.Code, text
		}
	}
	return envelope.Code, firstString(envelope.Message, envelope.Detail)
}

func classifyCode(code string) ErrorKind {
	code = strings.ToLower(code)
	switch {
	case strings.Contains(code, "context"), strings.Contains(code, "max_output"):
		return ErrorContextLimit
	case strings.Contains(code, "rate"), strings.Contains(code, "quota"), strings.Contains(code, "overload"):
		return ErrorRateLimit
	case strings.Contains(code, "server"), strings.Contains(code, "internal"), strings.Contains(code, "unavailable"), strings.Contains(code, "timeout"):
		return ErrorServer
	case strings.Contains(code, "auth"), strings.Contains(code, "token"):
		return ErrorAuthentication
	case strings.Contains(code, "entitlement"), strings.Contains(code, "permission"), strings.Contains(code, "plan"):
		return ErrorEntitlement
	case code == "":
		return ErrorStream
	default:
		return ErrorInvalidRequest
	}
}

var (
	quotaResetPattern  = regexp.MustCompile(`(?i)reset after (?:(\d+)h)?(?:(\d+)m)?(\d+(?:\.\d+)?)s`)
	pleaseRetryPattern = regexp.MustCompile(`(?i)please retry in ([0-9.]+)(ms|s)`)
	retryDelayPattern  = regexp.MustCompile(`(?i)"retryDelay"\s*:\s*"([0-9.]+)(ms|s)"`)
	tryAgainPattern    = regexp.MustCompile(`(?i)try again in\s+~?\s*([0-9.]+)\s*(ms|sec|s|minutes?|mins?|m|hours?|hrs?|h)\b`)
)

func retryAfter(header interface{ Get(string) string }, body string, now time.Time) time.Duration {
	if header != nil {
		if delay, ok := numericDuration(header.Get("retry-after-ms"), time.Millisecond); ok {
			return delay
		}
		if value := header.Get("Retry-After"); value != "" {
			if delay, ok := numericDuration(value, time.Second); ok {
				return delay
			}
			if at, err := http.ParseTime(value); err == nil {
				return max(0, at.Sub(now))
			}
		}
		if value := header.Get("x-ratelimit-reset-ms"); value != "" {
			if raw, err := strconv.ParseFloat(value, 64); err == nil && raw > 0 {
				switch {
				case raw > 1e12:
					return max(0, time.UnixMilli(int64(raw)).Sub(now))
				case raw > 1e9:
					return max(0, time.Unix(int64(raw), 0).Sub(now))
				default:
					if delay, ok := durationFromNumber(raw, time.Millisecond); ok {
						return delay
					}
				}
			}
		}
		if value := header.Get("x-ratelimit-reset"); value != "" {
			if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
				return max(0, time.Unix(int64(seconds), 0).Sub(now))
			}
		}
		if delay, ok := numericDuration(header.Get("x-ratelimit-reset-after"), time.Second); ok {
			return delay
		}
	}
	if match := quotaResetPattern.FindStringSubmatch(body); len(match) == 4 {
		hours, _ := strconv.ParseFloat(match[1], 64)
		minutes, _ := strconv.ParseFloat(match[2], 64)
		seconds, err := strconv.ParseFloat(match[3], 64)
		if err == nil {
			if delay, ok := durationFromNumber((hours*60+minutes)*60+seconds, time.Second); ok {
				return delay
			}
		}
	}
	for _, pattern := range []*regexp.Regexp{pleaseRetryPattern, retryDelayPattern, tryAgainPattern} {
		match := pattern.FindStringSubmatch(body)
		if len(match) != 3 {
			continue
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			continue
		}
		if delay, ok := durationFromNumber(value, retryUnit(match[2])); ok {
			return delay
		}
	}
	return 0
}

func numericDuration(value string, unit time.Duration) (time.Duration, bool) {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, false
	}
	return durationFromNumber(number, unit)
}

func durationFromNumber(value float64, unit time.Duration) (time.Duration, bool) {
	const maxDuration = time.Duration(1<<63 - 1)
	if value < 0 || unit <= 0 || value > float64(maxDuration)/float64(unit) {
		return 0, false
	}
	return time.Duration(value * float64(unit)), true
}

func retryUnit(value string) time.Duration {
	switch strings.ToLower(value) {
	case "ms":
		return time.Millisecond
	case "s", "sec":
		return time.Second
	case "m", "min", "mins", "minute", "minutes":
		return time.Minute
	case "h", "hr", "hrs", "hour", "hours":
		return time.Hour
	default:
		return 0
	}
}

func boundedMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 512 {
		return message[:512]
	}
	return message
}
