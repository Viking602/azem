package workrevision

import (
	"testing"

	"github.com/Viking602/azem/internal/session"
)

func TestProjectionStatusUsesDurableDispositionAndVerification(t *testing.T) {
	t.Parallel()
	accepted := &DispositionV1{Status: DispositionAccepted, SourceRevisionID: "revision"}
	stale := &DispositionV1{Status: DispositionQuarantinedStale, SourceRevisionID: "revision"}
	pass := &session.VerificationResultV1{Status: "pass", RevisionID: "revision"}
	if got := ProjectionStatus(nil, nil); got != EvidenceProvisional {
		t.Fatalf("missing disposition = %q", got)
	}
	if got := ProjectionStatus(stale, pass); got != EvidenceStale {
		t.Fatalf("stale disposition = %q", got)
	}
	if got := ProjectionStatus(accepted, pass); got != EvidenceVerified {
		t.Fatalf("accepted pass = %q", got)
	}
	pass.RevisionID = "other"
	if got := ProjectionStatus(accepted, pass); got != EvidenceProvisional {
		t.Fatalf("mismatched verification = %q", got)
	}
}
