package workrevision

import "github.com/Viking602/azem/internal/session"

const (
	EvidenceProvisional = "provisional"
	EvidenceVerified    = "verified"
	EvidenceStale       = "stale"
)

// ProjectionStatus derives transient UI state from durable records. The
// projection is never stored as a second source of truth.
func ProjectionStatus(disposition *DispositionV1, verification *session.VerificationResultV1) string {
	if disposition == nil {
		return EvidenceProvisional
	}
	if disposition.Status == DispositionQuarantinedStale {
		return EvidenceStale
	}
	if disposition.Status == DispositionAccepted && verification != nil && verification.Status == "pass" &&
		verification.RevisionID == disposition.SourceRevisionID {
		return EvidenceVerified
	}
	return EvidenceProvisional
}
