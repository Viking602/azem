package agent

import (
	"context"
	"sync"

	"github.com/Viking602/go-hydaelyn/api"
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

func (p *ApprovalPolicy) Authorize(ctx context.Context, request api.PolicyRequest) (api.PolicyDecision, error) {
	if !sideEffect(request) {
		return api.PolicyDecision{Effect: api.PolicyEffectAllow}, nil
	}
	scope, _ := ctx.Value(invocationScopeKey{}).(invocationScope)
	if scope.Authorized || p.sessionGranted(scope.Fingerprint) {
		return api.PolicyDecision{Effect: api.PolicyEffectAllow}, nil
	}
	return api.PolicyDecision{Effect: api.PolicyEffectDeny, Reason: "side effect requires an Azem approval"}, nil
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

func sideEffect(request api.PolicyRequest) bool {
	if request.Tool != nil {
		return request.Tool.RequiresActionTask || request.Tool.EffectType == api.ToolEffectWrite || request.Tool.EffectType == api.ToolEffectExternalSideEffect
	}
	return request.Operation == api.PolicyOperationAction
}

var _ api.PolicyEngine = (*ApprovalPolicy)(nil)
