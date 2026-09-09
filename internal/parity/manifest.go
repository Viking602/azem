package parity

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed manifest.json
var manifestJSON []byte

type Status string

const (
	StatusComplete Status = "complete"
	StatusStronger Status = "stronger"
	StatusPartial  Status = "partial"
	StatusMissing  Status = "missing"
)

type Capability struct {
	ID               string `json:"id"`
	Group            string `json:"group"`
	Owner            string `json:"owner"`
	Status           Status `json:"status"`
	BaselineEvidence string `json:"baselineEvidence"`
	AzemEvidence     string `json:"azemEvidence,omitempty"`
	Gap              string `json:"gap,omitempty"`
}

type Manifest struct {
	SchemaVersion   int          `json:"schemaVersion"`
	BaselineVersion string       `json:"baselineVersion"`
	BaselineCommit  string       `json:"baselineCommit"`
	CapturedAt      string       `json:"capturedAt"`
	Scope           string       `json:"scope"`
	Capabilities    []Capability `json:"capabilities"`
}

func Load() (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode parity manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("parity manifest schema version %d is unsupported", m.SchemaVersion)
	}
	if strings.TrimSpace(m.BaselineVersion) == "" || strings.TrimSpace(m.BaselineCommit) == "" || strings.TrimSpace(m.CapturedAt) == "" {
		return fmt.Errorf("parity manifest baseline is incomplete")
	}
	if strings.TrimSpace(m.Scope) == "" || len(m.Capabilities) == 0 {
		return fmt.Errorf("parity manifest scope or capabilities are empty")
	}
	seen := make(map[string]struct{}, len(m.Capabilities))
	for _, capability := range m.Capabilities {
		if strings.TrimSpace(capability.ID) == "" || strings.TrimSpace(capability.Group) == "" || strings.TrimSpace(capability.Owner) == "" || strings.TrimSpace(capability.BaselineEvidence) == "" {
			return fmt.Errorf("parity capability %q is incomplete", capability.ID)
		}
		if _, exists := seen[capability.ID]; exists {
			return fmt.Errorf("parity capability %q is duplicated", capability.ID)
		}
		seen[capability.ID] = struct{}{}
		switch capability.Status {
		case StatusComplete, StatusStronger:
			if strings.TrimSpace(capability.AzemEvidence) == "" {
				return fmt.Errorf("implemented parity capability %q lacks Azem evidence", capability.ID)
			}
		case StatusPartial, StatusMissing:
			if strings.TrimSpace(capability.Gap) == "" {
				return fmt.Errorf("open parity capability %q lacks a concrete gap", capability.ID)
			}
		default:
			return fmt.Errorf("parity capability %q has unsupported status %q", capability.ID, capability.Status)
		}
	}
	return nil
}

func (m Manifest) OpenCapabilities() []Capability {
	open := make([]Capability, 0)
	for _, capability := range m.Capabilities {
		if capability.Status == StatusPartial || capability.Status == StatusMissing {
			open = append(open, capability)
		}
	}
	return open
}
