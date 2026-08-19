// Package assets projects existing executable catalogs into one metadata view.
// It does not install, enable, or execute assets.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/plugins"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
)

const RegistryVersionV1 = 1

type ProvenanceV1 struct {
	Origin string              `json:"origin"`
	Source session.SourceRefV1 `json:"source"`
}

type EvaluationMetadataV1 struct {
	Status           string                `json:"status"`
	EvaluationID     string                `json:"evaluation_id,omitempty"`
	EvaluatorVersion string                `json:"evaluator_version,omitempty"`
	DatasetVersion   string                `json:"dataset_version,omitempty"`
	Score            float64               `json:"score,omitempty"`
	SafetyStatus     string                `json:"safety_status,omitempty"`
	Evidence         []session.SourceRefV1 `json:"evidence,omitempty"`
	EvaluatedAt      time.Time             `json:"evaluated_at,omitempty"`
}

type EntryV1 struct {
	ID          string               `json:"id"`
	Kind        string               `json:"kind"`
	Name        string               `json:"name"`
	Version     string               `json:"version"`
	Enabled     bool                 `json:"enabled"`
	Installable bool                 `json:"installable"`
	Generated   bool                 `json:"generated"`
	Provenance  ProvenanceV1         `json:"provenance"`
	Evaluation  EvaluationMetadataV1 `json:"evaluation"`
}

type RegistryV1 struct {
	Version int       `json:"version"`
	Entries []EntryV1 `json:"entries"`
}

type Inputs struct {
	Plugins   plugins.Integration
	Skills    skills.Snapshot
	Subagents config.SubagentConfig
}

func Build(inputs Inputs, evaluations map[string]EvaluationMetadataV1) (RegistryV1, error) {
	registry := RegistryV1{Version: RegistryVersionV1}
	for _, plugin := range inputs.Plugins.Entries {
		registry.Entries = append(registry.Entries, EntryV1{
			ID: "plugin:" + plugin.ID, Kind: "plugin", Name: first(plugin.DisplayName, plugin.Name, plugin.ID), Version: first(plugin.Version, "unversioned"),
			Enabled: plugin.Enabled, Installable: plugin.Status == "available", Provenance: ProvenanceV1{Origin: plugin.Origin, Source: session.SourceRefV1{Kind: "plugin", ID: plugin.ID}},
		})
	}
	for _, skill := range inputs.Skills.Entries {
		origin := "local"
		if skill.Bundled {
			origin = "bundled"
		}
		registry.Entries = append(registry.Entries, EntryV1{
			ID: "skill:" + skill.Name, Kind: "skill", Name: skill.Name, Version: descriptorVersion(skill), Enabled: !skill.Disabled,
			Provenance: ProvenanceV1{Origin: origin, Source: session.SourceRefV1{Kind: "skill_source", ID: first(skill.SourcePath, "bundled:"+skill.Name)}},
		})
	}
	mcpNames := make([]string, 0, len(inputs.Plugins.MCPServers))
	for name := range inputs.Plugins.MCPServers {
		mcpNames = append(mcpNames, name)
	}
	sort.Strings(mcpNames)
	for _, name := range mcpNames {
		server := inputs.Plugins.MCPServers[name]
		registry.Entries = append(registry.Entries, EntryV1{
			ID: "mcp:" + name, Kind: "mcp", Name: name, Version: descriptorVersion(server), Enabled: server.Enabled,
			Provenance: ProvenanceV1{Origin: "plugin_catalog", Source: session.SourceRefV1{Kind: "mcp_config", ID: name}},
		})
	}
	appendSubagentEntries(&registry, inputs.Subagents)
	seen := make(map[string]struct{}, len(registry.Entries))
	for index := range registry.Entries {
		entry := &registry.Entries[index]
		if _, exists := seen[entry.ID]; exists {
			return RegistryV1{}, fmt.Errorf("assets: duplicate asset id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		if evaluation, exists := evaluations[entry.ID]; exists {
			if err := evaluation.Validate(); err != nil {
				return RegistryV1{}, err
			}
			entry.Evaluation = evaluation
		} else {
			entry.Evaluation = EvaluationMetadataV1{Status: "unevaluated"}
		}
		if err := entry.Validate(); err != nil {
			return RegistryV1{}, err
		}
	}
	for id := range evaluations {
		if _, exists := seen[id]; !exists {
			return RegistryV1{}, fmt.Errorf("assets: evaluation references unknown asset %q", id)
		}
	}
	sort.Slice(registry.Entries, func(i, j int) bool { return registry.Entries[i].ID < registry.Entries[j].ID })
	return registry, nil
}

// GeneratedCandidate creates evaluation-only metadata. Generated assets are
// never installable or enabled through this registry.
func GeneratedCandidate(id, kind, name, version string, source session.SourceRefV1, evaluation EvaluationMetadataV1) (EntryV1, error) {
	entry := EntryV1{
		ID: id, Kind: kind, Name: name, Version: version, Generated: true, Installable: false, Enabled: false,
		Provenance: ProvenanceV1{Origin: "generated", Source: source}, Evaluation: evaluation,
	}
	return entry, entry.Validate()
}

func (evaluation EvaluationMetadataV1) Validate() error {
	if evaluation.Status == "unevaluated" {
		if evaluation.EvaluationID != "" || len(evaluation.Evidence) != 0 || !evaluation.EvaluatedAt.IsZero() {
			return fmt.Errorf("assets: unevaluated metadata contains results")
		}
		return nil
	}
	if !oneOf(evaluation.Status, "pass", "fail", "uncertain") || evaluation.EvaluationID == "" || evaluation.EvaluatorVersion == "" || evaluation.DatasetVersion == "" || evaluation.Score < 0 || evaluation.Score > 1 || !oneOf(evaluation.SafetyStatus, "pass", "fail", "uncertain") || len(evaluation.Evidence) == 0 || evaluation.EvaluatedAt.IsZero() {
		return fmt.Errorf("assets: invalid evaluation metadata")
	}
	return nil
}

func (entry EntryV1) Validate() error {
	if entry.ID == "" || !oneOf(entry.Kind, "plugin", "skill", "mcp", "subagent", "tool") || entry.Name == "" || entry.Version == "" || entry.Provenance.Origin == "" || entry.Provenance.Source.Kind == "" || entry.Provenance.Source.ID == "" {
		return fmt.Errorf("assets: invalid asset %q", entry.ID)
	}
	if entry.Generated && (entry.Installable || entry.Enabled || entry.Provenance.Origin != "generated") {
		return fmt.Errorf("assets: generated asset %q cannot be executable", entry.ID)
	}
	return entry.Evaluation.Validate()
}

func appendSubagentEntries(registry *RegistryV1, subagents config.SubagentConfig) {
	roleNames := make([]string, 0, len(subagents.Roles))
	for name := range subagents.Roles {
		roleNames = append(roleNames, name)
	}
	sort.Strings(roleNames)
	for _, name := range roleNames {
		profile := subagents.Roles[name]
		registry.Entries = append(registry.Entries, EntryV1{
			ID: "subagent:role:" + name, Kind: "subagent", Name: name, Version: descriptorVersion(profile), Enabled: subagents.Enabled,
			Provenance: ProvenanceV1{Origin: profileOrigin(profile.Source), Source: session.SourceRefV1{Kind: "subagent_profile", ID: first(profile.Source, "config:role:"+name)}},
		})
	}
	personaNames := make([]string, 0, len(subagents.Personas))
	for name := range subagents.Personas {
		personaNames = append(personaNames, name)
	}
	sort.Strings(personaNames)
	for _, name := range personaNames {
		profile := subagents.Personas[name]
		registry.Entries = append(registry.Entries, EntryV1{
			ID: "subagent:persona:" + name, Kind: "subagent", Name: name, Version: descriptorVersion(profile), Enabled: subagents.Enabled,
			Provenance: ProvenanceV1{Origin: profileOrigin(profile.Source), Source: session.SourceRefV1{Kind: "subagent_profile", ID: first(profile.Source, "config:persona:"+name)}},
		})
	}
}

func descriptorVersion(value any) string {
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:12])
}

func profileOrigin(source string) string {
	if strings.TrimSpace(source) == "" {
		return "config"
	}
	return "discovered"
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
