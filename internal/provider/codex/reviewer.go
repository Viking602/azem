package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/provider/responses"
)

const (
	// Codex Guardian prefers gpt-5.4 for approval reviews. This value is sent
	// directly to the Responses API and therefore must be a real model ID, not
	// an internal reviewer label.
	ApprovalReviewerModel = "gpt-5.4"
	approvalReviewTimeout = 90 * time.Second
	maxReviewOutputBytes  = 64 << 10
)

type ApprovalReviewRequest struct {
	Goal            string          `json:"goal"`
	AgentID         string          `json:"agent_id"`
	AgentType       string          `json:"agent_type"`
	ToolName        string          `json:"tool_name"`
	Arguments       json.RawMessage `json:"arguments"`
	Target          string          `json:"target"`
	Effect          string          `json:"effect"`
	Risk            string          `json:"risk"`
	RequestedAction string          `json:"requested_action"`
	RequestedReason string          `json:"requested_reason"`
}

type ApprovalReview struct {
	RiskLevel         string `json:"risk_level"`
	UserAuthorization string `json:"user_authorization"`
	Outcome           string `json:"outcome"`
	Rationale         string `json:"rationale"`
}

type ReviewFailureKind string

const (
	ReviewFailureInvalidRequest ReviewFailureKind = "invalid_request"
	ReviewFailureAuthentication ReviewFailureKind = "authentication"
	ReviewFailureProvider       ReviewFailureKind = "provider"
	ReviewFailureStream         ReviewFailureKind = "stream"
	ReviewFailureParse          ReviewFailureKind = "parse"
	ReviewFailureTimeout        ReviewFailureKind = "timeout"
	ReviewFailureCancelled      ReviewFailureKind = "cancelled"
)

type ReviewError struct {
	Kind  ReviewFailureKind
	Cause error
}

func (e *ReviewError) Error() string {
	if e.Cause == nil {
		return "automatic approval review failed: " + string(e.Kind)
	}
	return fmt.Sprintf("automatic approval review failed (%s): %v", e.Kind, e.Cause)
}

func (e *ReviewError) Unwrap() error { return e.Cause }

func ReviewFailure(err error) ReviewFailureKind {
	var reviewErr *ReviewError
	if errors.As(err, &reviewErr) {
		return reviewErr.Kind
	}
	return ReviewFailureProvider
}

type Reviewer struct {
	driver  *Driver
	timeout time.Duration
}

func NewReviewer(driver *Driver) (*Reviewer, error) {
	if driver == nil {
		return nil, fmt.Errorf("Codex approval reviewer driver is nil")
	}
	return &Reviewer{driver: driver, timeout: approvalReviewTimeout}, nil
}

func (r *Reviewer) Review(ctx context.Context, request ApprovalReviewRequest) (ApprovalReview, error) {
	if r == nil || r.driver == nil {
		return ApprovalReview{}, reviewError(ReviewFailureProvider, errors.New("reviewer is not configured"))
	}
	if len(request.Arguments) == 0 || !json.Valid(request.Arguments) {
		return ApprovalReview{}, reviewError(ReviewFailureInvalidRequest, errors.New("tool arguments are not valid JSON"))
	}
	active, err := r.driver.auth.HasActiveChatGPTAccount(ctx)
	if err != nil {
		return ApprovalReview{}, reviewError(ReviewFailureAuthentication, err)
	}
	if !active {
		return ApprovalReview{}, reviewError(ReviewFailureAuthentication, errors.New("no active ChatGPT account"))
	}

	reviewCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		assessment, retryable, err := r.reviewOnce(reviewCtx, request)
		if err == nil {
			return assessment, nil
		}
		lastErr = err
		if !retryable || attempt == 1 || reviewCtx.Err() != nil {
			break
		}
	}
	if contextErr := reviewContextError(ctx, reviewCtx); contextErr != nil {
		return ApprovalReview{}, contextErr
	}
	if active, checkErr := r.driver.auth.HasActiveChatGPTAccount(context.WithoutCancel(ctx)); checkErr == nil && !active {
		return ApprovalReview{}, reviewError(ReviewFailureAuthentication, lastErr)
	}
	return ApprovalReview{}, lastErr
}

func (r *Reviewer) reviewOnce(ctx context.Context, request ApprovalReviewRequest) (ApprovalReview, bool, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return ApprovalReview{}, false, reviewError(ReviewFailureInvalidRequest, fmt.Errorf("encode review evidence: %w", err))
	}
	providerRequest := hyprovider.Request{
		Model: ApprovalReviewerModel,
		Messages: []message.Message{
			message.NewText(message.RoleSystem, guardianSystemPolicy()),
			message.NewText(message.RoleUser, string(payload)),
		},
		Metadata: map[string]string{"reasoning_effort": "low"},
		ResponseFormat: &hyprovider.ResponseFormat{
			Type:   "json_schema",
			Name:   "approval_review",
			Strict: true,
			Schema: approvalReviewSchema(),
		},
		ExtraBody: map[string]any{
			"parallel_tool_calls": false,
		},
	}
	stream, err := r.driver.Stream(ctx, providerRequest)
	if err != nil {
		kind, retryable := classifyReviewFailure(err)
		return ApprovalReview{}, retryable, reviewError(kind, err)
	}
	defer stream.Close()

	var output strings.Builder
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return ApprovalReview{}, true, reviewError(ReviewFailureStream, errors.New("review stream ended before done event"))
			}
			kind, retryable := classifyReviewFailure(recvErr)
			return ApprovalReview{}, retryable, reviewError(kind, recvErr)
		}
		switch event.Kind {
		case hyprovider.EventTextDelta:
			if output.Len()+len(event.Text) > maxReviewOutputBytes {
				return ApprovalReview{}, false, reviewError(ReviewFailureParse, errors.New("review output exceeds 64 KiB"))
			}
			output.WriteString(event.Text)
		case hyprovider.EventThinkingDelta:
			// Reasoning is deliberately ignored and never treated as the verdict.
		case hyprovider.EventToolCall, hyprovider.EventToolCallDelta:
			return ApprovalReview{}, false, reviewError(ReviewFailureParse, errors.New("approval reviewer attempted a tool call"))
		case hyprovider.EventError:
			kind, retryable := classifyReviewFailure(event.Err)
			return ApprovalReview{}, retryable, reviewError(kind, event.Err)
		case hyprovider.EventDone:
			if event.StopReason != hyprovider.StopReasonComplete {
				if contextErr := reviewContextError(ctx, ctx); contextErr != nil {
					return ApprovalReview{}, false, contextErr
				}
				return ApprovalReview{}, true, reviewError(ReviewFailureStream, fmt.Errorf("review stopped with reason %q", event.StopReason))
			}
			assessment, parseErr := parseApprovalReview(output.String())
			if parseErr != nil {
				return ApprovalReview{}, false, reviewError(ReviewFailureParse, parseErr)
			}
			return assessment, false, nil
		default:
			return ApprovalReview{}, false, reviewError(ReviewFailureParse, fmt.Errorf("unexpected review event %q", event.Kind))
		}
	}
}

func approvalReviewSchema() *message.JSONSchema {
	additionalProperties := false
	return &message.JSONSchema{
		Type: "object",
		Properties: map[string]message.JSONSchema{
			"risk_level":         {Type: "string", Enum: []string{"low", "medium", "high", "critical"}},
			"user_authorization": {Type: "string", Enum: []string{"unknown", "low", "medium", "high"}},
			"outcome":            {Type: "string", Enum: []string{"allow", "deny"}},
			"rationale":          {Type: "string"},
		},
		Required:             []string{"risk_level", "user_authorization", "outcome", "rationale"},
		AdditionalProperties: &additionalProperties,
	}
}

func parseApprovalReview(value string) (ApprovalReview, error) {
	if strings.TrimSpace(value) == "" {
		return ApprovalReview{}, errors.New("approval reviewer returned empty output")
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var assessment ApprovalReview
	if err := decoder.Decode(&assessment); err != nil {
		return ApprovalReview{}, fmt.Errorf("decode approval review: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ApprovalReview{}, errors.New("approval review contains trailing JSON")
		}
		return ApprovalReview{}, fmt.Errorf("decode trailing approval review content: %w", err)
	}
	if !oneOf(assessment.RiskLevel, "low", "medium", "high", "critical") {
		return ApprovalReview{}, fmt.Errorf("invalid risk_level %q", assessment.RiskLevel)
	}
	if !oneOf(assessment.UserAuthorization, "unknown", "low", "medium", "high") {
		return ApprovalReview{}, fmt.Errorf("invalid user_authorization %q", assessment.UserAuthorization)
	}
	if !oneOf(assessment.Outcome, "allow", "deny") {
		return ApprovalReview{}, fmt.Errorf("invalid outcome %q", assessment.Outcome)
	}
	assessment.Rationale = strings.TrimSpace(assessment.Rationale)
	if assessment.Rationale == "" {
		return ApprovalReview{}, errors.New("approval review rationale is empty")
	}
	return assessment, nil
}

func classifyReviewFailure(err error) (ReviewFailureKind, bool) {
	if err == nil {
		return ReviewFailureStream, true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ReviewFailureTimeout, false
	}
	if errors.Is(err, context.Canceled) {
		return ReviewFailureCancelled, false
	}
	var apiErr *responses.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Kind {
		case responses.ErrorAuthentication, responses.ErrorEntitlement:
			return ReviewFailureAuthentication, false
		case responses.ErrorServer:
			return ReviewFailureProvider, true
		case responses.ErrorStream:
			return ReviewFailureStream, true
		default:
			return ReviewFailureProvider, false
		}
	}
	var entitlement auth.EntitlementError
	if errors.As(err, &entitlement) {
		return ReviewFailureAuthentication, false
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return ReviewFailureProvider, true
	}
	return ReviewFailureProvider, false
}

func reviewContextError(parent context.Context, review context.Context) error {
	if errors.Is(review.Err(), context.DeadlineExceeded) {
		return reviewError(ReviewFailureTimeout, context.DeadlineExceeded)
	}
	if errors.Is(parent.Err(), context.DeadlineExceeded) {
		return reviewError(ReviewFailureTimeout, context.DeadlineExceeded)
	}
	if parent.Err() != nil || errors.Is(review.Err(), context.Canceled) {
		return reviewError(ReviewFailureCancelled, context.Canceled)
	}
	return nil
}

func reviewError(kind ReviewFailureKind, cause error) error {
	return &ReviewError{Kind: kind, Cause: cause}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
