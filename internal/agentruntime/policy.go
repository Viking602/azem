package agentruntime

import (
	"context"
	"encoding/json"
)

type PolicyEngine interface {
	Authorize(context.Context, PolicyRequest) (PolicyDecision, error)
}

// PolicyObligationEnforcer applies an allowed decision's obligations at each
// data boundary. Implementations must reject unknown or malformed obligations.
type PolicyObligationEnforcer interface {
	EnforceBlackboardRead(context.Context, PolicyDecision, BlackboardSelector, []BlackboardItem) (BlackboardSelector, []BlackboardItem, error)
	EnforceBlackboardWrite(context.Context, PolicyDecision, BlackboardItem) (BlackboardItem, error)
	EnforceToolResult(context.Context, PolicyDecision, json.RawMessage) (json.RawMessage, error)
	EnforceHandoff(context.Context, PolicyDecision, HandoffRequest) (HandoffRequest, error)
	EnforceResponse(context.Context, PolicyDecision, UserMessage) (UserMessage, error)
	EnforceTrace(context.Context, PolicyDecision, TraceSpan) (TraceSpan, bool, error)
}

type ToolResultEnforcementRequest struct {
	RunID      string
	TaskID     string
	Decision   PolicyDecision
	ToolResult json.RawMessage
}

type ToolResultEnforcementResult struct {
	ToolResult json.RawMessage
}
