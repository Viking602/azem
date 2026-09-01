package agentruntime

import (
	"context"
	"time"
)

// AdmissionState is the durable lifecycle of an aggregate-capacity reservation.
type AdmissionState string

const (
	AdmissionReserved  AdmissionState = "reserved"
	AdmissionActive    AdmissionState = "active"
	AdmissionSuspended AdmissionState = "suspended"
	AdmissionSettled   AdmissionState = "settled"
	AdmissionReleased  AdmissionState = "released"
	AdmissionExpired   AdmissionState = "expired"
)

// AdmissionDenialReason identifies which aggregate guard denied capacity.
type AdmissionDenialReason string

const (
	AdmissionDeniedConcurrency     AdmissionDenialReason = "concurrency_limit"
	AdmissionDeniedRunWindow       AdmissionDenialReason = "run_window_limit"
	AdmissionDeniedCredits         AdmissionDenialReason = "credit_limit"
	AdmissionDeniedFailureBreaker  AdmissionDenialReason = "failure_breaker"
	AdmissionDeniedVersionConflict AdmissionDenialReason = "version_conflict"
)

// AdmissionLimits is the application-supplied aggregate governance envelope.
// Zero disables a dimension. Window is required by every non-concurrency
// dimension.
type AdmissionLimits struct {
	Window                time.Duration `json:"window,omitempty"`
	MaxConcurrentRuns     int           `json:"maxConcurrentRuns,omitempty"`
	MaxRunsPerWindow      int           `json:"maxRunsPerWindow,omitempty"`
	MaxCredits            int64         `json:"maxCredits,omitempty"`
	PauseOnExcessFailures int           `json:"pauseOnExcessFailures,omitempty"`
}

// AdmissionReservation is the durable capacity grant for one run. Version is
// advanced by every lifecycle transition and is the compare-and-swap token.
type AdmissionReservation struct {
	ID              string          `json:"id"`
	AgentID         string          `json:"agentId"`
	AgentVersion    string          `json:"agentVersion,omitempty"`
	RunID           string          `json:"runId"`
	State           AdmissionState  `json:"state"`
	Limits          AdmissionLimits `json:"limits"`
	ReservedCredits int64           `json:"reservedCredits,omitempty"`
	ConsumedCredits int64           `json:"consumedCredits,omitempty"`
	Failed          bool            `json:"failed,omitempty"`
	Version         uint64          `json:"version"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	ActivatedAt     time.Time       `json:"activatedAt,omitempty"`
	SettledAt       time.Time       `json:"settledAt,omitempty"`
	ExpiresAt       time.Time       `json:"expiresAt"`
}

// AdmissionRequest is the prospective reservation evaluated by PreviewAdmission
// and atomically inserted by ReserveAdmission. RequestedAt and ExpiresAt must
// define a positive lifetime; the runtime reanchors that lifetime to its clock.
type AdmissionRequest struct {
	ReservationID   string          `json:"reservationId"`
	AgentID         string          `json:"agentId"`
	AgentVersion    string          `json:"agentVersion,omitempty"`
	RunID           string          `json:"runId"`
	Limits          AdmissionLimits `json:"limits"`
	ReservedCredits int64           `json:"reservedCredits,omitempty"`
	RequestedAt     time.Time       `json:"requestedAt"`
	ExpiresAt       time.Time       `json:"expiresAt"`
}

// AdmissionUsage is the aggregate state observed during one admission
// evaluation. It is diagnostic only; Allowed is the capacity grant.
type AdmissionUsage struct {
	ConcurrentRuns   int   `json:"concurrentRuns"`
	RunsInWindow     int   `json:"runsInWindow"`
	ReservedCredits  int64 `json:"reservedCredits"`
	CommittedCredits int64 `json:"committedCredits"`
	TrailingFailures int   `json:"trailingFailures"`
}

// AdmissionDecision is returned by preview, reserve, and lifecycle transitions.
// Preview never returns a populated Reservation and never grants capacity.
type AdmissionDecision struct {
	Allowed     bool                  `json:"allowed"`
	Reason      AdmissionDenialReason `json:"reason,omitempty"`
	Usage       AdmissionUsage        `json:"usage"`
	Reservation AdmissionReservation  `json:"reservation,omitempty"`
}

// AdmissionTransition requests an expected-version lifecycle transition.
// At is validated as the caller's lifetime anchor, then replaced by the
// runtime's clock. On settlement, the runtime replaces ConsumedCredits with
// the priced usage sum and Failed with the durable agent-task or terminal-run
// outcome computed in the same transaction.
type AdmissionTransition struct {
	ReservationID   string         `json:"reservationId"`
	ExpectedVersion uint64         `json:"expectedVersion"`
	To              AdmissionState `json:"to"`
	At              time.Time      `json:"at"`
	ExpiresAt       time.Time      `json:"expiresAt,omitempty"`
	ConsumedCredits int64          `json:"consumedCredits,omitempty"`
	Failed          bool           `json:"failed,omitempty"`
}

// AdmissionReservationSelector filters reservations. All populated fields
// AND-combine. ExpiresBefore is inclusive.
type AdmissionReservationSelector struct {
	AgentIDs      []string         `json:"agentIds,omitempty"`
	RunIDs        []string         `json:"runIds,omitempty"`
	States        []AdmissionState `json:"states,omitempty"`
	Since         time.Time        `json:"since,omitempty"`
	ExpiresBefore time.Time        `json:"expiresBefore,omitempty"`
	Limit         int              `json:"limit,omitempty"`
}

// AdmissionReservationStore atomically evaluates and persists aggregate
// capacity. PreviewAdmission is read-only. ReserveAdmission MUST evaluate and
// insert in one atomic operation. TransitionAdmission MUST use ExpectedVersion;
// reactivating a suspended reservation re-evaluates concurrency atomically.
type AdmissionReservationStore interface {
	PreviewAdmission(context.Context, AdmissionRequest) (AdmissionDecision, error)
	ReserveAdmission(context.Context, AdmissionRequest) (AdmissionDecision, error)
	TransitionAdmission(context.Context, AdmissionTransition) (AdmissionDecision, error)
	LoadAdmissionReservation(context.Context, string) (AdmissionReservation, error)
	ListAdmissionReservations(context.Context, AdmissionReservationSelector) ([]AdmissionReservation, error)
}

// AdmissionReservationUnitOfWork is the optional UnitOfWork extension exposed
// when StoreCapabilities.SupportsAdmissionReservations is true.
type AdmissionReservationUnitOfWork interface {
	AdmissionReservations() AdmissionReservationStore
}
