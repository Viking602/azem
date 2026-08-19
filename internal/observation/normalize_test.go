package observation

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestNormalizeToolObservationUsesOnlyHostKnownReliability(t *testing.T) {
	t.Parallel()
	completedAt := time.Unix(100, 5).UTC()
	exit := 7
	trueValue, falseValue := true, false
	cases := []struct {
		name       string
		record     session.ToolRecord
		metadata   ObservationMetadata
		status     string
		presence   string
		truncation string
		schema     string
		retry      string
	}{
		{
			name:     "empty completed result with known no truncation",
			record:   session.ToolRecord{SessionID: "session", RunID: "run", ToolCallID: "empty", State: session.ToolCompleted, Structured: json.RawMessage(`null`), CompletedAt: completedAt},
			metadata: ObservationMetadata{Spilled: &falseValue, Truncated: &falseValue, Retryable: &falseValue},
			status:   "completed", presence: "empty", truncation: "none", schema: "unknown", retry: "terminal",
		},
		{
			name:     "failed result with exit and invalid schema",
			record:   session.ToolRecord{SessionID: "session", RunID: "run", ToolCallID: "failed", State: session.ToolFailed, Content: "compiler failed", Structured: json.RawMessage(`{"broken"`), CompletedAt: completedAt},
			metadata: ObservationMetadata{ExitStatus: &exit, Retryable: &trueValue},
			status:   "failed", presence: "present", truncation: "unknown", schema: "invalid", retry: "retryable",
		},
		{
			name:     "timeout with spill and file evidence",
			record:   session.ToolRecord{SessionID: "session", RunID: "run", ToolCallID: "timeout", State: session.ToolInterrupted, ArtifactID: "artifact-1", Structured: json.RawMessage(`{"partial":true}`), Observations: []session.FileObservation{{Path: "main.go", Operation: "read", SHA256: "6ee165c9f77203d15ba520e5968923254945f4fd55d7f44e65f9cc3bc75b27d5"}}, CompletedAt: completedAt},
			metadata: ObservationMetadata{TimedOut: true, Spilled: &trueValue},
			status:   "timeout", presence: "present", truncation: "spilled", schema: "valid", retry: "unknown",
		},
		{
			name:     "explicitly missing raw result",
			record:   session.ToolRecord{SessionID: "session", RunID: "run", ToolCallID: "missing", State: session.ToolReconcileRequired, CompletedAt: completedAt},
			metadata: ObservationMetadata{RawAvailable: &falseValue},
			status:   "unknown", presence: "missing", truncation: "unknown", schema: "unknown", retry: "unknown",
		},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			before := test.record
			before.Structured = append(json.RawMessage(nil), test.record.Structured...)
			before.Observations = append([]session.FileObservation(nil), test.record.Observations...)
			envelope := NormalizeToolObservation(test.record, test.metadata)
			if envelope.Status != test.status || envelope.ContentPresence != test.presence || envelope.Truncation != test.truncation || envelope.SchemaValidity != test.schema || envelope.Retryability != test.retry {
				t.Fatalf("envelope reliability = %+v", envelope)
			}
			if envelope.FreshAt != completedAt || envelope.RawSHA256 == "" || envelope.RawRef == nil || envelope.RawRef.SHA256 != envelope.RawSHA256 {
				t.Fatalf("envelope evidence = %+v", envelope)
			}
			if err := envelope.Validate(); err != nil {
				t.Fatalf("envelope validation: %v", err)
			}
			if !reflect.DeepEqual(test.record, before) {
				t.Fatalf("normalization mutated raw tool record\n got: %+v\nwant: %+v", test.record, before)
			}
		})
	}
}

func TestNormalizeToolObservationMapsFileOperationsWithoutGuessing(t *testing.T) {
	t.Parallel()
	record := session.ToolRecord{SessionID: "session", RunID: "run", ToolCallID: "files", State: session.ToolCompleted, Observations: []session.FileObservation{
		{Path: "read.go", Operation: "read", SHA256: "c14cf750087639b12d216f49b9c9e1506e5389b60aa717d0880233877e78c69a"},
		{Path: "new.go", Operation: "created", SHA256: "39536e636aaa9086fa371a887ca7c81cdd1a452d907880f14012237e66002bb3"},
		{Path: "old.go", Operation: "deleted", SHA256: "9b307fa6b2060fb1ae4d7acc91996ba702242505f8b67fefb1c3b93c8df22488"},
		{Path: "mystery.go", Operation: "renamed", SHA256: "ffb918915b3220e898974b308a94d02a4d50888b4a1f1b783b66d3c8cf0525bc"},
	}}
	envelope := NormalizeToolObservation(record, ObservationMetadata{})
	if len(envelope.Files) != 4 || envelope.Files[0].State != "observed" || envelope.Files[0].AfterSHA256 != "c14cf750087639b12d216f49b9c9e1506e5389b60aa717d0880233877e78c69a" ||
		envelope.Files[1].State != "created" || envelope.Files[1].AfterSHA256 != "39536e636aaa9086fa371a887ca7c81cdd1a452d907880f14012237e66002bb3" ||
		envelope.Files[2].State != "deleted" || envelope.Files[2].BeforeSHA256 != "9b307fa6b2060fb1ae4d7acc91996ba702242505f8b67fefb1c3b93c8df22488" ||
		envelope.Files[3].State != "unknown" || envelope.Files[3].BeforeSHA256 != "" || envelope.Files[3].AfterSHA256 != "" {
		t.Fatalf("file observations = %+v", envelope.Files)
	}
}
