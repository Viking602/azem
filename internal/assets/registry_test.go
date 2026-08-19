package assets

import (
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/plugins"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
)

func TestBuildReusesExecutableCatalogsWithEvaluationMetadata(t *testing.T) {
	t.Parallel()
	evaluation := EvaluationMetadataV1{
		Status: "pass", EvaluationID: "eval-1", EvaluatorVersion: "validator-v1", DatasetVersion: "tasks-v1", Score: 0.9, SafetyStatus: "pass",
		Evidence: []session.SourceRefV1{{Kind: "verification_result", ID: "result-1"}}, EvaluatedAt: time.Unix(1, 0).UTC(),
	}
	registry, err := Build(Inputs{
		Plugins: plugins.Integration{
			Entries:    []plugins.Entry{{ID: "demo", Name: "demo", Version: "1.2.3", Origin: "local", Enabled: true, Status: "installed"}},
			MCPServers: map[string]config.MCPServerConfig{"files": {Enabled: true, Transport: "stdio", Command: "files"}},
		},
		Skills:    skills.Snapshot{Entries: []skills.Entry{{Name: "verify", SourcePath: "/skills/verify/SKILL.md", Bundled: true, ModelVisible: true}}},
		Subagents: config.SubagentConfig{Enabled: true, Roles: map[string]config.SubagentRoleConfig{"review": {Description: "review", Source: "/agents/review.md"}}, Personas: map[string]config.SubagentPersonaConfig{"careful": {Description: "careful"}}},
	}, map[string]EvaluationMetadataV1{"skill:verify": evaluation})
	if err != nil {
		t.Fatal(err)
	}
	if registry.Version != RegistryVersionV1 || len(registry.Entries) != 5 {
		t.Fatalf("registry = %+v", registry)
	}
	byID := make(map[string]EntryV1, len(registry.Entries))
	for _, entry := range registry.Entries {
		byID[entry.ID] = entry
	}
	if byID["plugin:demo"].Version != "1.2.3" || !byID["plugin:demo"].Enabled || byID["skill:verify"].Evaluation.EvaluationID != "eval-1" || byID["mcp:files"].Evaluation.Status != "unevaluated" || byID["subagent:role:review"].Provenance.Origin != "discovered" {
		t.Fatalf("asset metadata = %+v", byID)
	}
}

func TestGeneratedAssetCandidatesCannotBeInstalledOrEnabled(t *testing.T) {
	t.Parallel()
	evaluation := EvaluationMetadataV1{Status: "uncertain", EvaluationID: "eval", EvaluatorVersion: "v1", DatasetVersion: "tasks-v1", Score: 0.4, SafetyStatus: "uncertain", Evidence: []session.SourceRefV1{{Kind: "sandbox", ID: "run"}}, EvaluatedAt: time.Unix(1, 0).UTC()}
	entry, err := GeneratedCandidate("tool:generated", "tool", "generated", "sha256:abc", session.SourceRefV1{Kind: "generated_source", ID: "artifact"}, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Installable || entry.Enabled || !entry.Generated {
		t.Fatalf("generated entry became executable: %+v", entry)
	}
	entry.Installable = true
	if err := entry.Validate(); err == nil {
		t.Fatal("accepted installable generated asset")
	}
}

func TestBuildRejectsEvaluationForUnknownAsset(t *testing.T) {
	t.Parallel()
	_, err := Build(Inputs{}, map[string]EvaluationMetadataV1{"missing": {Status: "unevaluated"}})
	if err == nil {
		t.Fatal("accepted orphan evaluation metadata")
	}
}
