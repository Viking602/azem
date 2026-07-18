package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
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

func HTTPError(response *http.Response) error {
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	code, message := decodeError(body)
	if strings.TrimSpace(message) == "" {
		// ChatGPT's Codex endpoint does not consistently use the public API error
		// envelope. Preserve its bounded raw diagnostic, as Codex CLI does, rather
		// than reducing a useful HTTP 400 response to only the status code.
		message = strings.TrimSpace(string(body))
	}
	kind := ErrorServer
	switch response.StatusCode {
	case http.StatusUnauthorized:
		kind = ErrorAuthentication
	case http.StatusForbidden:
		kind = ErrorEntitlement
	case http.StatusTooManyRequests:
		kind = ErrorRateLimit
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		kind = classifyCode(code)
		if kind == ErrorStream {
			kind = ErrorInvalidRequest
		}
	default:
		if response.StatusCode < 500 {
			kind = ErrorInvalidRequest
		}
	}
	return &APIError{Kind: kind, StatusCode: response.StatusCode, Code: code, Message: boundedMessage(message), RetryAfter: retryAfter(response.Header)}
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
		return &APIError{Kind: ErrorStream, Message: "malformed provider error event"}
	}
	failure := envelope.Error
	if failure == nil && envelope.Response != nil {
		failure = envelope.Response.Error
	}
	if failure != nil {
		code := firstString(failure.Code, failure.Type)
		return &APIError{Kind: classifyCode(code), Code: code, Message: boundedMessage(failure.Message)}
	}
	if envelope.Code != "" || envelope.Message != "" {
		return &APIError{
			Kind: classifyCode(envelope.Code), Code: envelope.Code, Message: boundedMessage(envelope.Message),
		}
	}
	if envelope.Response != nil && envelope.Response.Incomplete != nil {
		code := envelope.Response.Incomplete.Reason
		kind := classifyCode(code)
		if code == "max_output_tokens" {
			kind = ErrorContextLimit
		}
		return &APIError{Kind: kind, Code: code, Message: "provider returned an incomplete response"}
	}
	return &APIError{Kind: ErrorStream, Message: "provider stream failed"}
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

func retryAfter(header http.Header) time.Duration {
	value := header.Get("Retry-After")
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := time.Until(at); delay > 0 {
			return delay
		}
	}
	return 0
}

func boundedMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 512 {
		return message[:512]
	}
	return message
}
