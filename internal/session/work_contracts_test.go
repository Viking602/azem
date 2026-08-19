package session

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWorkContractsV1RoundTripAndValidate(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 123).UTC()
	source := SourceRefV1{Kind: "sequence", ID: "42", Range: "body", SHA256: strings.Repeat("a", 64)}
	exit := 7
	work := WorkSpecV1{Version: 1, SessionID: "session-1", RunID: "run-1", Goal: "ship", Criteria: []CriterionV1{{ID: "criterion-1", Text: "tests pass", Origin: "explicit", Required: true, Sources: []SourceRefV1{source}}}, CreatedAt: now}
	work.ID = CanonicalWorkSpecID(work)
	values := []interface {
		Validate() error
	}{
		work,
		ActionIntentV1{Version: 1, ID: "intent-1", WorkSpecID: work.ID, SessionID: "session-1", RunID: "run-1", Kind: "tool", Name: "go test", CreatedAt: now},
		ObservationEnvelopeV1{Version: 1, ID: "observation-1", IntentID: "intent-1", SessionID: "session-1", RunID: "run-1", Status: "failed", ExitStatus: &exit, ContentPresence: "present", Truncation: "none", SchemaValidity: "unknown", Retryability: "terminal", FreshAt: now, RawSHA256: strings.Repeat("b", 64), RawRef: &source},
		VerificationPlanV1{Version: 1, ID: "plan-1", WorkSpecID: work.ID, RevisionID: "revision-1", SnapshotHash: "snapshot", Checks: []VerificationCheckV1{{ID: "check-1", CriterionIDs: []string{"criterion-1"}, Kind: "command", Command: []string{"go", "test", "./internal/session"}}}, CreatedAt: now},
		VerificationResultV1{Version: 1, ID: "result-1", PlanID: "plan-1", WorkSpecID: work.ID, RevisionID: "revision-1", SnapshotHash: "snapshot", Status: "pass", Criteria: []CriterionResultV1{{CriterionID: "criterion-1", Status: "pass", Evidence: []SourceRefV1{source}}}, CompletedAt: now},
		ExperienceCandidateV1{Version: 1, ID: "experience-1", Workspace: "/repo", Kind: "constraint", Content: "keep raw bytes", Scope: "project", Confidence: "verified", Status: "candidate", Evidence: []SourceRefV1{source}, CreatedAt: now},
		RouteOutcomeV1{Version: 1, ID: "outcome-1", WorkSpecID: work.ID, TaskClass: "go", Repository: "azem", Toolchain: "go1", PolicyVersion: "policy-1", Provider: "provider", Model: "model", Status: "pass", Evidence: []SourceRefV1{source}, CompletedAt: now, InputTokens: 1, OutputTokens: 1, CostMicros: 1, LatencyMS: 1},
	}
	for _, value := range values {
		if err := value.Validate(); err != nil {
			t.Fatalf("%T validation failed: %v", value, err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %T: %v", value, err)
		}
		copyValue := reflect.New(reflect.TypeOf(value)).Interface()
		if err := json.Unmarshal(raw, copyValue); err != nil {
			t.Fatalf("unmarshal %T: %v", value, err)
		}
		roundTrip, ok := copyValue.(interface{ Validate() error })
		if !ok {
			t.Fatalf("%T lost validator after round trip", value)
		}
		if err := roundTrip.Validate(); err != nil {
			t.Fatalf("round-trip %T validation failed: %v", value, err)
		}
	}
}

func TestWorkSpecCanonicalIdentityRejectsTamperingAndDuplicateConstraints(t *testing.T) {
	t.Parallel()
	work := WorkSpecV1{
		Version: 1, SessionID: "session", RunID: "run", Goal: "ship", ApprovedPlanID: "plan",
		Criteria:  []CriterionV1{{ID: "a", Text: "a", Origin: "explicit", Sources: []SourceRefV1{{Kind: "sequence", ID: "1"}}}},
		CreatedAt: time.Unix(1, 0).UTC(),
	}
	work.ID = CanonicalWorkSpecID(work)
	if err := work.Validate(); err != nil {
		t.Fatal(err)
	}
	work.Goal = "tampered"
	if err := work.Validate(); err == nil {
		t.Fatal("accepted work spec after goal tampering")
	}
	work.Goal = "ship"
	work.Criteria = append(work.Criteria, work.Criteria[0])
	work.ID = CanonicalWorkSpecID(work)
	if err := work.Validate(); err == nil {
		t.Fatal("accepted duplicate criterion")
	}
}

func TestRouteOutcomeRejectsNonFiniteAndIncompletePassAccounting(t *testing.T) {
	t.Parallel()
	outcome := RouteOutcomeV1{
		Version: 1, ID: "outcome", WorkSpecID: "work", Provider: "provider", Model: "model",
		Status: "pass", CompletedAt: time.Unix(1, 0).UTC(), Evidence: []SourceRefV1{{Kind: "validator", ID: "test"}},
		CriterionCoverage: math.NaN(), InputTokens: 1, OutputTokens: 1, CostMicros: 1, LatencyMS: 1,
	}
	if err := outcome.Validate(); err == nil {
		t.Fatal("accepted non-finite route metric")
	}
	outcome.CriterionCoverage = 1
	outcome.OutputTokens = 0
	if err := outcome.Validate(); err == nil {
		t.Fatal("accepted incomplete pass accounting")
	}
}

func TestWorkContractsV1RejectUnknownAndIncompleteValues(t *testing.T) {
	t.Parallel()
	cases := []interface{ Validate() error }{
		WorkSpecV1{Version: 2, ID: "work", SessionID: "session", Goal: "goal"},
		WorkSpecV1{Version: 1, ID: "work", SessionID: "session", Goal: "goal", Criteria: []CriterionV1{{ID: "criterion", Text: "text", Origin: "guessed"}}},
		ActionIntentV1{Version: 1, ID: "intent"},
		ObservationEnvelopeV1{Version: 1, ID: "observation", SessionID: "session", RunID: "run", Status: "success", ContentPresence: "present", Truncation: "none", SchemaValidity: "valid", Retryability: "terminal"},
		VerificationPlanV1{Version: 1, ID: "plan", WorkSpecID: "work"},
		VerificationResultV1{Version: 1, ID: "result", Status: "pass"},
		ExperienceCandidateV1{Version: 1, ID: "experience", Status: "active"},
		RouteOutcomeV1{Version: 1, ID: "outcome", Status: "pass"},
	}
	for _, value := range cases {
		if err := value.Validate(); !errors.Is(err, ErrInvalidWorkContract) {
			t.Fatalf("%T error = %v, want ErrInvalidWorkContract", value, err)
		}
	}
}
