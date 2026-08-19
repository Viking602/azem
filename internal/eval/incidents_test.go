package eval

import (
	"reflect"
	"testing"

	"github.com/Viking602/azem/internal/session"
)

func TestLabelTrajectoryIncidentsCoversNoiseTaxonomy(t *testing.T) {
	t.Parallel()
	turn := session.SourceRefV1{Kind: "sequence", ID: "10"}
	prior := session.SourceRefV1{Kind: "sequence", ID: "9"}
	validator := session.SourceRefV1{Kind: "validator", ID: "test-1"}
	failed := session.ObservationEnvelopeV1{
		Version: 1, ID: "observation:failed", SessionID: "session", RunID: "run", ToolCallID: "failed-call",
		Status: "failed", ContentPresence: "missing", Truncation: "truncated", SchemaValidity: "unknown", Retryability: "retryable",
		Sources: []session.SourceRefV1{{Kind: "tool_record", ID: "failed-call"}},
	}
	completed := session.ObservationEnvelopeV1{
		Version: 1, ID: "observation:completed", SessionID: "session", RunID: "run", ToolCallID: "completed-call",
		Status: "completed", ContentPresence: "present", Truncation: "none", SchemaValidity: "valid", Retryability: "terminal",
		Sources: []session.SourceRefV1{{Kind: "tool_record", ID: "completed-call"}},
	}
	trajectory := TrajectoryV1{Version: 1, SessionID: "session", Tables: []TrajectoryTableV1{{Name: "session_blocks", Columns: []string{"data"}, Rows: []TrajectoryRowV1{{Values: map[string]StoredValueV1{"data": {Kind: "blob", Base64: "e30="}}}}}}}
	rawTables := append([]TrajectoryTableV1(nil), trajectory.Tables...)
	duplicateTool := session.SourceRefV1{Kind: "tool_record", ID: "earlier-call"}
	err := LabelTrajectoryIncidents(&trajectory, IncidentSignalsV1{
		Users: []UserIncidentSignalV1{{
			ID: "turn-10", Source: turn, InterpretationIDs: []string{"a", "b"}, Contradictions: []session.SourceRefV1{prior},
			DuplicateOf: &prior, ExpectedWorkSpecID: "work-1", ObservedWorkSpecID: "work-2", BoundaryProbe: true,
			BoundaryEvidence: []session.SourceRefV1{{Kind: "policy", ID: "workspace-only"}},
		}},
		Tools: []ToolIncidentSignalV1{
			{Observation: failed, ValidatorStatus: "unknown"},
			{Observation: completed, ValidatorStatus: "fail", ValidatorEvidence: []session.SourceRefV1{validator}, ClaimedSuccess: true, DuplicateOf: &duplicateTool},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	categories := make(map[string]bool)
	for _, incident := range trajectory.Incidents {
		categories[incident.Category] = true
		if incident.Version != 1 || incident.ID == "" || len(incident.Evidence) == 0 {
			t.Fatalf("invalid incident = %+v", incident)
		}
	}
	want := []string{
		IncidentUserAmbiguity, IncidentUserInconsistency, IncidentUserRedundancy, IncidentUserDrift, IncidentUserBoundaryProbe,
		IncidentToolFailure, IncidentToolIncomplete, IncidentToolErroneous, IncidentToolMisleading, IncidentToolRedundant,
	}
	if len(trajectory.Incidents) != len(want) {
		t.Fatalf("incident count = %d, want %d: %+v", len(trajectory.Incidents), len(want), trajectory.Incidents)
	}
	for _, category := range want {
		if !categories[category] {
			t.Fatalf("missing incident category %s", category)
		}
	}
	if !reflect.DeepEqual(trajectory.Tables, rawTables) {
		t.Fatal("incident labeling changed raw trajectory tables")
	}
}

func TestLabelTrajectoryIncidentsDoesNotGuessUnknownMetadata(t *testing.T) {
	t.Parallel()
	trajectory := TrajectoryV1{Version: 1, SessionID: "session"}
	observation := session.ObservationEnvelopeV1{
		Version: 1, ID: "observation", SessionID: "session", RunID: "run", ToolCallID: "call",
		Status: "completed", ContentPresence: "empty", Truncation: "unknown", SchemaValidity: "unknown", Retryability: "unknown",
		Sources: []session.SourceRefV1{{Kind: "tool_record", ID: "call"}},
	}
	if err := LabelTrajectoryIncidents(&trajectory, IncidentSignalsV1{
		Users: []UserIncidentSignalV1{{ID: "turn", Source: session.SourceRefV1{Kind: "sequence", ID: "1"}}},
		Tools: []ToolIncidentSignalV1{{Observation: observation, ValidatorStatus: "unknown"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(trajectory.Incidents) != 0 {
		t.Fatalf("unknown metadata produced incidents: %+v", trajectory.Incidents)
	}
}
