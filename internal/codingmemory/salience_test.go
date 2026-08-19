package codingmemory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/Viking602/azem/internal/session"
	"math"
	"testing"
)

func TestSemanticSalienceKeepsOnlyBoundedReferences(t *testing.T) {
	t.Parallel()
	candidates := make([]SalienceRefV1, 0, 42)
	for index := range 40 {
		candidates = append(candidates, SalienceRefV1{
			Version: 1, Kind: "coding_memory", ID: fmt.Sprintf("memory-%02d", index), Scope: ScopeV1{Kind: ScopeRepository, ID: "repo"},
			Salience: float64(index) / 40, Reason: "relevant verified strategy", Sources: []session.SourceRefV1{{Kind: "workspace_file", ID: fmt.Sprintf("file-%02d.go", index)}},
		})
	}
	state, err := AttachSemanticSalience(json.RawMessage(`{"version":1,"objective":{"text":"ship"}}`), candidates)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := ExtractSemanticSalience(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != MaxSemanticSalienceEntries || refs[0].ID != "memory-39" || refs[0].Salience != float64(39)/40 || len(state) > MaxSemanticSalienceBytes+128 {
		t.Fatalf("bounded refs len=%d first=%+v stateBytes=%d", len(refs), refs[0], len(state))
	}
	if bytes.Contains(state, []byte("Use the narrow verified path")) {
		t.Fatal("semantic projection copied memory payload")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(state, &object); err != nil || object["objective"] == nil {
		t.Fatalf("base semantic state was not preserved: %s err=%v", state, err)
	}
}
func TestSemanticSalienceRejectsDuplicateReferences(t *testing.T) {
	t.Parallel()
	ref := SalienceRefV1{
		Version: 1, Kind: "coding_memory", ID: "memory", Scope: ScopeV1{Kind: ScopeRepository, ID: "repo"},
		Salience: 0.5, Reason: "verified", Sources: []session.SourceRefV1{{Kind: "workspace_file", ID: "a.go"}},
	}
	if _, err := AttachSemanticSalience(json.RawMessage(`{}`), []SalienceRefV1{ref, ref}); err == nil {
		t.Fatal("accepted duplicate salience reference")
	}
}
func TestSemanticSalienceRejectsNonFiniteSalience(t *testing.T) {
	t.Parallel()
	ref := SalienceRefV1{
		Version: 1, Kind: "coding_memory", ID: "memory", Scope: ScopeV1{Kind: ScopeRepository, ID: "repo"},
		Salience: math.NaN(), Reason: "verified", Sources: []session.SourceRefV1{{Kind: "workspace_file", ID: "a.go"}},
	}
	if err := ref.Validate(); err == nil {
		t.Fatal("accepted non-finite salience")
	}
}

func TestSemanticSalienceShrinksToByteBudget(t *testing.T) {
	t.Parallel()
	candidates := make([]SalienceRefV1, 32)
	for index := range candidates {
		candidates[index] = SalienceRefV1{Version: 1, Kind: "evidence_ledger", ID: fmt.Sprintf("ledger-%02d", index), Scope: ScopeV1{Kind: ScopeProject, ID: "project"}, Salience: 1 - float64(index)/100, Reason: string(bytes.Repeat([]byte{'x'}, 240)), Sources: []session.SourceRefV1{{Kind: "evidence_ledger", ID: fmt.Sprintf("source-%02d", index)}}}
	}
	selected, err := selectSalience(candidates)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(selected)
	if len(payload) > MaxSemanticSalienceBytes || len(selected) >= len(candidates) {
		t.Fatalf("byte bound not enforced: entries=%d bytes=%d", len(selected), len(payload))
	}
}

func TestSemanticSalienceRejectsNonObjectAndTrailingJSON(t *testing.T) {
	t.Parallel()
	for _, state := range []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`{} {}`)} {
		if _, err := AttachSemanticSalience(state, nil); err == nil {
			t.Fatalf("accepted invalid semantic state %q", state)
		}
	}
}
