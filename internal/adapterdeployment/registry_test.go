package adapterdeployment

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

func TestRegistryDeploysExactRoutesAndKillSwitchRollsBack(t *testing.T) {
	t.Parallel()
	registry := New()
	compatibility := CompatibilityV1{Provider: "openrouter", Model: "base-model", Reasoning: "high", BaseModelSHA256: strings.Repeat("a", 64), TokenizerSHA256: strings.Repeat("b", 64)}
	first := adapterArtifact(t, compatibility, "adapted-v1", "base")
	validated := 0
	if err := registry.Deploy(first, compatibility, resolverFor(first), func(route config.ModelRouteConfig) error {
		validated++
		if route.Provider != "openrouter" || route.Model != "adapted-v1" {
			return errors.New("unexpected route")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resolution := registry.Resolve(config.ModelRouteConfig{Provider: "openrouter", Model: "base-model", Reasoning: "high"})
	if resolution.AdapterID != first.ID || resolution.Route.Model != "adapted-v1" || resolution.Route.Reasoning != "high" || validated != 1 {
		t.Fatalf("resolution = %+v validated=%d", resolution, validated)
	}
	second := adapterArtifact(t, compatibility, "adapted-v2", first.ID)
	if err := registry.Deploy(second, compatibility, resolverFor(second), func(config.ModelRouteConfig) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := registry.Resolve(config.ModelRouteConfig{Provider: "openrouter", Model: "base-model", Reasoning: "high"}); got.AdapterID != second.ID {
		t.Fatalf("active = %+v", got)
	}
	if err := registry.Kill(second.ID); err != nil {
		t.Fatal(err)
	}
	if got := registry.Resolve(config.ModelRouteConfig{Provider: "openrouter", Model: "base-model", Reasoning: "high"}); got.AdapterID != first.ID || got.Route.Model != "adapted-v1" {
		t.Fatalf("rollback = %+v", got)
	}
	if err := registry.Kill(first.ID); err != nil {
		t.Fatal(err)
	}
	base := config.ModelRouteConfig{Provider: "openrouter", Model: "base-model", Reasoning: "low"}
	if got := registry.Resolve(base); got.AdapterID != "" || got.Route != base {
		t.Fatalf("kill switch did not restore base route: %+v", got)
	}
}

func TestRegistryRejectsIncompatibleUnvalidatedOrTamperedArtifacts(t *testing.T) {
	t.Parallel()
	registry := New()
	compatibility := CompatibilityV1{Provider: "openrouter", Model: "base", Reasoning: "high", BaseModelSHA256: strings.Repeat("a", 64), TokenizerSHA256: strings.Repeat("b", 64)}
	artifact := adapterArtifact(t, compatibility, "adapted", "base")
	mismatch := compatibility
	mismatch.TokenizerSHA256 = strings.Repeat("c", 64)
	if err := registry.Deploy(artifact, mismatch, resolverFor(artifact), func(config.ModelRouteConfig) error { return nil }); err == nil {
		t.Fatal("accepted incompatible tokenizer")
	}
	if err := registry.Deploy(artifact, compatibility, resolverFor(artifact), nil); err == nil {
		t.Fatal("accepted deployment without existing route validator")
	}
	if err := registry.Deploy(artifact, compatibility, resolverFor(artifact), func(config.ModelRouteConfig) error { return errors.New("model missing") }); err == nil {
		t.Fatal("accepted unresolved serving route")
	}
	tampered := artifact
	tampered.ServingRoute.Model = "other"
	if err := registry.Deploy(tampered, compatibility, resolverFor(artifact), func(config.ModelRouteConfig) error { return nil }); err == nil {
		t.Fatal("accepted tampered versioned artifact")
	}
	input := validArtifactInput(compatibility, "adapted", "base")
	input.KillSwitch = true
	if _, err := NewArtifact(input); err == nil {
		t.Fatal("created artifact with kill switch already active")
	}
}

func TestRegistryRequiresTrustedReportAndExactReasoning(t *testing.T) {
	t.Parallel()
	compatibility := CompatibilityV1{Provider: "openrouter", Model: "base", Reasoning: "high", BaseModelSHA256: strings.Repeat("a", 64), TokenizerSHA256: strings.Repeat("b", 64)}
	input := validArtifactInput(compatibility, "adapted", "base")
	artifact, err := NewArtifact(input)
	if err != nil {
		t.Fatal(err)
	}
	registry := New()
	forgedResolver := ValidationReportResolverFunc(func(string) (ValidationReportV1, error) {
		forged := artifact.Validation
		forged.Status = "pass"
		forged.CorrectnessDelta = 0.9
		return forged, nil
	})
	if err := registry.Deploy(artifact, compatibility, forgedResolver, func(config.ModelRouteConfig) error { return nil }); err == nil {
		t.Fatal("accepted a report that differs from the artifact's trusted report")
	}
	tamperedInput := input
	tamperedInput.Validation.Digest = strings.Repeat("e", 64)
	tampered, err := NewArtifact(tamperedInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Deploy(tampered, compatibility, resolverFor(artifact), func(config.ModelRouteConfig) error { return nil }); err == nil {
		t.Fatal("accepted a report digest absent from trusted storage")
	}
	if got := registry.Resolve(config.ModelRouteConfig{Provider: compatibility.Provider, Model: compatibility.Model, Reasoning: "low"}); got.AdapterID != "" {
		t.Fatal("resolved an adapter across a reasoning override")
	}
	nanInput := input
	nanInput.Validation.CorrectnessDelta = math.NaN()
	if _, err := NewArtifact(nanInput); err == nil {
		t.Fatal("accepted a non-finite validation delta")
	}
}

func resolverFor(artifact ArtifactV1) ValidationReportResolver {
	return ValidationReportResolverFunc(func(digest string) (ValidationReportV1, error) {
		if digest != artifact.Validation.Digest {
			return ValidationReportV1{}, errors.New("report is not in trusted storage")
		}
		return artifact.Validation, nil
	})
}

func adapterArtifact(t *testing.T, compatibility CompatibilityV1, model, rollback string) ArtifactV1 {
	t.Helper()
	artifact, err := NewArtifact(validArtifactInput(compatibility, model, rollback))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func validArtifactInput(compatibility CompatibilityV1, model, rollback string) ArtifactInputV1 {
	return ArtifactInputV1{
		Method: "prefix_soft_prompt", PayloadSHA256: strings.Repeat("c", 64), Compatibility: compatibility,
		TrainDataLineage: []session.SourceRefV1{{Kind: "noise_dataset", ID: "dataset-1"}},
		Validation: ValidationReportV1{
			Status: "pass", HeldOutTasks: 100, CorrectnessDelta: 0.05, SafetyRetention: 1, FalseDecisionDelta: -0.01,
			TrainServeCostMicros: 10_000, Digest: strings.Repeat("d", 64), Evidence: []session.SourceRefV1{{Kind: "held_out_report", ID: "report-1"}},
		},
		ServingRoute: config.ModelRouteConfig{Provider: compatibility.Provider, Model: model, Reasoning: compatibility.Reasoning}, RollbackTarget: rollback,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
}
