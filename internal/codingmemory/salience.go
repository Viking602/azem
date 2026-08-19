package codingmemory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Viking602/azem/internal/session"
	"io"
	"math"
	"sort"
	"strings"
)

const (
	MaxSemanticSalienceEntries = 32
	MaxSemanticSalienceBytes   = 8 << 10
)

type SalienceRefV1 struct {
	Version  int                   `json:"version"`
	Kind     string                `json:"kind"`
	ID       string                `json:"id"`
	Scope    ScopeV1               `json:"scope"`
	Salience float64               `json:"salience"`
	Reason   string                `json:"reason"`
	Sources  []session.SourceRefV1 `json:"sources"`
}

func (ref SalienceRefV1) Validate() error {
	if ref.Version != 1 || !oneOf(ref.Kind, "coding_memory", "evidence_ledger", "asset") || ref.ID == "" || len(ref.ID) > 512 || !oneOf(ref.Scope.Kind, ScopeRepository, ScopeProject, ScopeUser) || ref.Scope.ID == "" || math.IsNaN(ref.Salience) || math.IsInf(ref.Salience, 0) || ref.Salience < 0 || ref.Salience > 1 || strings.TrimSpace(ref.Reason) == "" || len(ref.Reason) > 240 || len(ref.Sources) == 0 || len(ref.Sources) > 8 {
		return fmt.Errorf("coding memory: invalid salience reference %q", ref.ID)
	}
	seen := make(map[string]struct{}, len(ref.Sources))
	for _, source := range ref.Sources {
		if source.Kind == "" || source.ID == "" {
			return fmt.Errorf("coding memory: invalid salience provenance")
		}
		key := source.Kind + "\x00" + source.ID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("coding memory: duplicate salience provenance")
		}
		seen[key] = struct{}{}
	}
	return nil
}

// AttachSemanticSalience inserts only bounded references into semantic state.
// Memory payload remains in its typed store and must be resolved at use time.
func AttachSemanticSalience(state json.RawMessage, candidates []SalienceRefV1) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(state))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("coding memory: semantic state must be one JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("coding memory: semantic state contains trailing JSON")
	}
	selected, err := selectSalience(candidates)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(selected)
	if err != nil {
		return nil, err
	}
	object["salient_evidence"] = encoded
	return json.Marshal(object)
}

func ExtractSemanticSalience(state json.RawMessage) ([]SalienceRefV1, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(state, &object); err != nil || object == nil {
		return nil, fmt.Errorf("coding memory: semantic state must be one JSON object")
	}
	payload, exists := object["salient_evidence"]
	if !exists {
		return nil, nil
	}
	var refs []SalienceRefV1
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&refs); err != nil {
		return nil, err
	}
	if len(refs) > MaxSemanticSalienceEntries || len(payload) > MaxSemanticSalienceBytes {
		return nil, fmt.Errorf("coding memory: semantic salience exceeds bounds")
	}
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func selectSalience(candidates []SalienceRefV1) ([]SalienceRefV1, error) {
	byID := make(map[string]SalienceRefV1, len(candidates))
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		key := candidate.Kind + "\x00" + candidate.ID
		if _, exists := byID[key]; exists {
			return nil, fmt.Errorf("coding memory: duplicate salience reference %q", candidate.ID)
		}
		candidate.Sources = append([]session.SourceRefV1(nil), candidate.Sources...)
		byID[key] = candidate
	}
	ordered := make([]SalienceRefV1, 0, len(byID))
	for _, candidate := range byID {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Salience != ordered[j].Salience {
			return ordered[i].Salience > ordered[j].Salience
		}
		return ordered[i].Kind+"\x00"+ordered[i].ID < ordered[j].Kind+"\x00"+ordered[j].ID
	})
	if len(ordered) > MaxSemanticSalienceEntries {
		ordered = ordered[:MaxSemanticSalienceEntries]
	}
	for len(ordered) > 0 {
		payload, err := json.Marshal(ordered)
		if err != nil {
			return nil, err
		}
		if len(payload) <= MaxSemanticSalienceBytes {
			return ordered, nil
		}
		ordered = ordered[:len(ordered)-1]
	}
	return []SalienceRefV1{}, nil
}
