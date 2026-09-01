package agent

import (
	"context"
	"sync"

	"github.com/Viking602/azem/internal/agentruntime"
)

type invocationScope struct {
	Fingerprint string
	Target      string
	Risk        string
	Authorized  bool
}

type invocationScopeKey struct{}

type ApprovalPolicy struct {
	mu            sync.RWMutex
	sessionGrants map[string]struct{}
}

func NewApprovalPolicy() *ApprovalPolicy {
	return &ApprovalPolicy{sessionGrants: make(map[string]struct{})}
}

func (p *ApprovalPolicy) Authorize(ctx context.Context, request agentruntime.PolicyRequest) (agentruntime.PolicyDecision, error) {
	if !sideEffect(request) {
		return agentruntime.PolicyDecision{Effect: agentruntime.PolicyEffectAllow}, nil
	}
	scope, _ := ctx.Value(invocationScopeKey{}).(invocationScope)
	if scope.Authorized || p.sessionGranted(scope.Fingerprint) {
		return agentruntime.PolicyDecision{Effect: agentruntime.PolicyEffectAllow}, nil
	}
	return agentruntime.PolicyDecision{Effect: agentruntime.PolicyEffectDeny, Reason: "side effect requires an Azem approval"}, nil
}

func (p *ApprovalPolicy) GrantSession(fingerprint string) {
	if fingerprint == "" {
		return
	}
	p.mu.Lock()
	p.sessionGrants[fingerprint] = struct{}{}
	p.mu.Unlock()
}

func (p *ApprovalPolicy) sessionGranted(fingerprint string) bool {
	p.mu.RLock()
	_, ok := p.sessionGrants[fingerprint]
	p.mu.RUnlock()
	return ok
}

func withAuthorizedInvocation(ctx context.Context, scope invocationScope) context.Context {
	scope.Authorized = true
	return context.WithValue(ctx, invocationScopeKey{}, scope)
}

// DelegatedApprovalContext tells Venat that the enclosing Azem tool adapter
// owns the interactive approval prompt. Venat still owns action-attempt
// persistence, idempotency, and reconciliation around the driver call.
func DelegatedApprovalContext(ctx context.Context) context.Context {
	return withAuthorizedInvocation(ctx, invocationScope{})
}

func sideEffect(request agentruntime.PolicyRequest) bool {
	if request.Tool != nil {
		return request.Tool.RequiresActionTask || request.Tool.EffectType == agentruntime.ToolEffectWrite || request.Tool.EffectType == agentruntime.ToolEffectExternalSideEffect
	}
	return request.Operation == agentruntime.PolicyOperationAction
}

var _ agentruntime.PolicyEngine = (*ApprovalPolicy)(nil)
