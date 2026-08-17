package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeSemanticStateAcceptsStringSourcesThatCannotUnmarshalIntoEvidenceRefV1(t *testing.T) {
	authorities := map[string]string{
		"sequence:42":                 "user",
		"tool:run:1":                  "tool",
		"checkpoint:carried:evidence": "agent",
	}

	t.Run("objective.sources string", func(t *testing.T) {
		// Desktop failure: json: cannot unmarshal string into Go struct field
		// StateFactV1.objective.sources of type app.EvidenceRefV1
		normalized, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":"sequence:42"}}`,
			authorities,
		)
		if err != nil {
			t.Fatalf("string objective.sources rejected: %v", err)
		}
		assertNormalizedSources(t, normalized, []EvidenceRefV1{{Kind: "sequence", ID: "42"}}, "user")
	})

	t.Run("objective.sources string array", func(t *testing.T) {
		normalized, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":["sequence:42"]}}`,
			authorities,
		)
		if err != nil {
			t.Fatalf("string-array objective.sources rejected: %v", err)
		}
		assertNormalizedSources(t, normalized, []EvidenceRefV1{{Kind: "sequence", ID: "42"}}, "user")
	})

	t.Run("opaque string sources still compact", func(t *testing.T) {
		normalized, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":"some-ref"},"findings":[{"text":"found","status":"active","authority":"tool","confidence":"verified","sources":["a","b"]}]}`,
			authorities,
		)
		if err != nil {
			t.Fatalf("opaque string sources must compact, not crash: %v", err)
		}
		var state SemanticStateV1
		if err := json.Unmarshal([]byte(normalized), &state); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(state.Objective.Sources, []EvidenceRefV1{{Kind: "sequence", ID: "42"}}) {
			t.Fatalf("fallback objective sources=%+v", state.Objective.Sources)
		}
		if len(state.Findings) != 1 || !reflect.DeepEqual(state.Findings[0].Sources, []EvidenceRefV1{{Kind: "tool", ID: "run:1"}}) {
			t.Fatalf("fallback finding sources=%+v", state.Findings)
		}
	})

	t.Run("all fact collections and current_action", func(t *testing.T) {
		normalized, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":"sequence:42"},"acceptance_criteria":[{"text":"accept","status":"active","authority":"user","confidence":"reported","sources":["sequence:42"]}],"constraints":[{"text":"limit","status":"active","authority":"user","confidence":"reported","sources":"sequence:42"}],"decisions":[{"text":"decide","status":"active","authority":"user","confidence":"reported","sources":["sequence:42"]}],"current_action":{"text":"doing","status":"active","authority":"agent","confidence":"inferred","sources":"checkpoint:carried:evidence"},"workset":[{"text":"file","status":"active","authority":"user","confidence":"reported","sources":"sequence:42"}],"findings":[{"text":"found","status":"active","authority":"tool","confidence":"verified","sources":["tool:run:1"]}],"failures":[{"text":"failed","status":"active","authority":"tool","confidence":"verified","sources":"tool:run:1"}],"blockers":[{"text":"blocked","status":"active","authority":"tool","confidence":"verified","sources":["tool:run:1"]}],"next_actions":[{"text":"next","status":"active","authority":"agent","confidence":"inferred","sources":"checkpoint:carried:evidence"}]}`,
			authorities,
		)
		if err != nil {
			t.Fatalf("collection string sources rejected: %v", err)
		}
		var state SemanticStateV1
		if err := json.Unmarshal([]byte(normalized), &state); err != nil {
			t.Fatal(err)
		}
		wantUser := []EvidenceRefV1{{Kind: "sequence", ID: "42"}}
		wantTool := []EvidenceRefV1{{Kind: "tool", ID: "run:1"}}
		wantAgent := []EvidenceRefV1{{Kind: "checkpoint", ID: "carried:evidence"}}
		if !reflect.DeepEqual(state.Objective.Sources, wantUser) ||
			len(state.AcceptanceCriteria) != 1 || !reflect.DeepEqual(state.AcceptanceCriteria[0].Sources, wantUser) ||
			len(state.Constraints) != 1 || !reflect.DeepEqual(state.Constraints[0].Sources, wantUser) ||
			len(state.Decisions) != 1 || !reflect.DeepEqual(state.Decisions[0].Sources, wantUser) ||
			state.CurrentAction == nil || !reflect.DeepEqual(state.CurrentAction.Sources, wantAgent) ||
			len(state.Workset) != 1 || !reflect.DeepEqual(state.Workset[0].Sources, wantUser) ||
			len(state.Findings) != 1 || !reflect.DeepEqual(state.Findings[0].Sources, wantTool) ||
			len(state.Failures) != 1 || !reflect.DeepEqual(state.Failures[0].Sources, wantTool) ||
			len(state.Blockers) != 1 || !reflect.DeepEqual(state.Blockers[0].Sources, wantTool) ||
			len(state.NextActions) != 1 || !reflect.DeepEqual(state.NextActions[0].Sources, wantAgent) {
			t.Fatalf("normalized collections=%+v current=%+v", state, state.CurrentAction)
		}
	})

	t.Run("mixed objects and strings", func(t *testing.T) {
		normalized, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":[{"kind":"sequence","id":"42"},"tool:run:1"]}}`,
			authorities,
		)
		if err != nil {
			t.Fatalf("mixed sources rejected: %v", err)
		}
		var state SemanticStateV1
		if err := json.Unmarshal([]byte(normalized), &state); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(state.Objective.Sources, []EvidenceRefV1{{Kind: "sequence", ID: "42"}, {Kind: "tool", ID: "run:1"}}) {
			t.Fatalf("mixed sources=%+v", state.Objective.Sources)
		}
	})

	t.Run("single object sources", func(t *testing.T) {
		normalized, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":{"kind":"sequence","id":"42"}}}`,
			authorities,
		)
		if err != nil {
			t.Fatalf("single-object sources rejected: %v", err)
		}
		assertNormalizedSources(t, normalized, []EvidenceRefV1{{Kind: "sequence", ID: "42"}}, "user")
	})

	t.Run("alternate uri field", func(t *testing.T) {
		normalized, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":[{"uri":"sequence:42"}]}}`,
			authorities,
		)
		if err != nil {
			t.Fatalf("uri sources rejected: %v", err)
		}
		assertNormalizedSources(t, normalized, []EvidenceRefV1{{Kind: "sequence", ID: "42"}}, "user")
	})

	t.Run("whole-response fence still accepted", func(t *testing.T) {
		raw := "```json\n" + `{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":"sequence:42"}}` + "\n```"
		normalized, err := normalizeSemanticStateV1(raw, authorities)
		if err != nil {
			t.Fatalf("fenced string sources rejected: %v", err)
		}
		assertNormalizedSources(t, normalized, []EvidenceRefV1{{Kind: "sequence", ID: "42"}}, "user")
	})

	t.Run("prose around fence still rejected", func(t *testing.T) {
		if _, err := normalizeSemanticStateV1("Here is the state:\n```json\n"+`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":"sequence:42"}}`+"\n```", authorities); err == nil {
			t.Fatal("accepted prose around a JSON fence")
		}
	})

	t.Run("incompatible number sources fail closed", func(t *testing.T) {
		_, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":1}}`,
			authorities,
		)
		if err == nil || !strings.Contains(err.Error(), "cannot unmarshal number into Go struct field StateFactV1.sources of type app.EvidenceRefV1") {
			t.Fatalf("number sources error=%v", err)
		}
	})

	t.Run("incompatible bool array sources fail closed", func(t *testing.T) {
		_, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":[true]}}`,
			authorities,
		)
		if err == nil || !strings.Contains(err.Error(), "cannot unmarshal bool into Go struct field StateFactV1.sources of type app.EvidenceRefV1") {
			t.Fatalf("bool sources error=%v", err)
		}
	})

	t.Run("unmappable object sources fail closed", func(t *testing.T) {
		_, err := normalizeSemanticStateV1(
			`{"version":1,"objective":{"text":"x","status":"active","authority":"user","confidence":"reported","sources":[{"foo":"bar"}]}}`,
			authorities,
		)
		if err == nil || !strings.Contains(err.Error(), "cannot unmarshal object into Go struct field StateFactV1.sources of type app.EvidenceRefV1") {
			t.Fatalf("unmappable object error=%v", err)
		}
	})
}

func assertNormalizedSources(t *testing.T, normalized string, want []EvidenceRefV1, authority string) {
	t.Helper()
	var state SemanticStateV1
	if err := json.Unmarshal([]byte(normalized), &state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Objective.Sources, want) || state.Objective.Authority != authority {
		t.Fatalf("normalized provenance=%+v", state.Objective)
	}
}
