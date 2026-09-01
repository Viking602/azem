package agentruntime

import (
	"context"
	"time"
)

// ResourceClaimMode controls whether a resource key may be held concurrently.
type ResourceClaimMode string

const (
	ResourceClaimShared    ResourceClaimMode = "shared"
	ResourceClaimExclusive ResourceClaimMode = "exclusive"
)

// ResourceClaimState is the durable lifecycle of one resource claim.
type ResourceClaimState string

const (
	ResourceClaimActive   ResourceClaimState = "active"
	ResourceClaimReleased ResourceClaimState = "released"
	ResourceClaimExpired  ResourceClaimState = "expired"
)

// ResourceClaimDenialReason identifies why an atomic claim operation failed.
type ResourceClaimDenialReason string

const (
	ResourceClaimDeniedConflict        ResourceClaimDenialReason = "conflict"
	ResourceClaimDeniedVersionConflict ResourceClaimDenialReason = "version_conflict"
)

// ResourceClaimSpec declares one opaque coordination key. ID is assigned by the
// runtime when a task lease is acquired and is omitted on task definitions.
type ResourceClaimSpec struct {
	ID   string            `json:"id,omitempty"`
	Key  string            `json:"key"`
	Mode ResourceClaimMode `json:"mode"`
}

// ResourceClaim is one durable shared or exclusive claim tied to a task lease.
// Version advances on every renewal or terminal transition.
type ResourceClaim struct {
	ID        string             `json:"id"`
	Key       string             `json:"key"`
	Mode      ResourceClaimMode  `json:"mode"`
	RunID     string             `json:"runId"`
	TaskID    string             `json:"taskId"`
	LeaseID   string             `json:"leaseId"`
	HolderID  string             `json:"holderId"`
	State     ResourceClaimState `json:"state"`
	Version   uint64             `json:"version"`
	CreatedAt time.Time          `json:"createdAt"`
	UpdatedAt time.Time          `json:"updatedAt"`
	ExpiresAt time.Time          `json:"expiresAt"`
}

// ResourceClaimRequest atomically acquires every claim or none. Claims must
// contain unique, non-empty IDs and keys.
type ResourceClaimRequest struct {
	RunID       string              `json:"runId"`
	TaskID      string              `json:"taskId"`
	LeaseID     string              `json:"leaseId"`
	HolderID    string              `json:"holderId"`
	Claims      []ResourceClaimSpec `json:"claims"`
	RequestedAt time.Time           `json:"requestedAt"`
	ExpiresAt   time.Time           `json:"expiresAt"`
}

// ResourceClaimTransition requests one expected-version renewal or terminal
// transition. Active-to-active renews the TTL.
type ResourceClaimTransition struct {
	ClaimID         string             `json:"claimId"`
	ExpectedVersion uint64             `json:"expectedVersion"`
	To              ResourceClaimState `json:"to"`
	At              time.Time          `json:"at"`
	ExpiresAt       time.Time          `json:"expiresAt,omitempty"`
}

// ResourceClaimTransitionRequest applies every transition atomically.
type ResourceClaimTransitionRequest struct {
	Transitions []ResourceClaimTransition `json:"transitions"`
}

// ResourceClaimDecision is returned by atomic acquire and transition methods.
// Conflicts contains active claims that prevented acquisition or claims whose
// expected version was stale.
type ResourceClaimDecision struct {
	Acquired  bool                      `json:"acquired"`
	Reason    ResourceClaimDenialReason `json:"reason,omitempty"`
	Claims    []ResourceClaim           `json:"claims,omitempty"`
	Conflicts []ResourceClaim           `json:"conflicts,omitempty"`
}

// ResourceClaimSelector filters claims. Populated fields AND-combine;
// ExpiresBefore is inclusive.
type ResourceClaimSelector struct {
	IDs           []string             `json:"ids,omitempty"`
	Keys          []string             `json:"keys,omitempty"`
	RunIDs        []string             `json:"runIds,omitempty"`
	TaskIDs       []string             `json:"taskIds,omitempty"`
	LeaseIDs      []string             `json:"leaseIds,omitempty"`
	HolderIDs     []string             `json:"holderIds,omitempty"`
	Modes         []ResourceClaimMode  `json:"modes,omitempty"`
	States        []ResourceClaimState `json:"states,omitempty"`
	ExpiresBefore time.Time            `json:"expiresBefore,omitempty"`
	Limit         int                  `json:"limit,omitempty"`
}

// ResourceClaimStore owns opaque shared/exclusive coordination claims.
// AcquireResourceClaims and TransitionResourceClaims are all-or-nothing.
type ResourceClaimStore interface {
	AcquireResourceClaims(context.Context, ResourceClaimRequest) (ResourceClaimDecision, error)
	TransitionResourceClaims(context.Context, ResourceClaimTransitionRequest) (ResourceClaimDecision, error)
	LoadResourceClaim(context.Context, string) (ResourceClaim, error)
	ListResourceClaims(context.Context, ResourceClaimSelector) ([]ResourceClaim, error)
}

// ResourceClaimUnitOfWork is the optional UnitOfWork extension exposed when
// StoreCapabilities.SupportsResourceClaims and SupportsTransactions are true.
type ResourceClaimUnitOfWork interface {
	ResourceClaims() ResourceClaimStore
}
