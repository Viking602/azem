package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/adapterdeployment"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

func TestProviderRuntimeResolvesAdapterInsideExistingRouteBoundary(t *testing.T) {
	t.Parallel()
	compatibility := adapterdeployment.CompatibilityV1{
		Provider: "openrouter", Model: "base", Reasoning: "high", BaseModelSHA256: strings.Repeat("a", 64), TokenizerSHA256: strings.Repeat("b", 64),
	}
	artifact, err := adapterdeployment.NewArtifact(adapterdeployment.ArtifactInputV1{
		Method: "lora", PayloadSHA256: strings.Repeat("c", 64), Compatibility: compatibility,
		TrainDataLineage: []session.SourceRefV1{{Kind: "dataset", ID: "train"}},
		Validation: adapterdeployment.ValidationReportV1{
			Status: "pass", HeldOutTasks: 100, CorrectnessDelta: 0.05, SafetyRetention: 1, FalseDecisionDelta: -0.01,
			TrainServeCostMicros: 1000, Digest: strings.Repeat("d", 64), Evidence: []session.SourceRefV1{{Kind: "report", ID: "held-out"}},
		},
		ServingRoute: config.ModelRouteConfig{Provider: "openrouter", Model: "adapted", Reasoning: "high"}, RollbackTarget: "base", CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := adapterdeployment.New()
	resolver := adapterdeployment.ValidationReportResolverFunc(func(digest string) (adapterdeployment.ValidationReportV1, error) {
		if digest != artifact.Validation.Digest {
			return adapterdeployment.ValidationReportV1{}, errors.New("report is not in trusted storage")
		}
		return artifact.Validation, nil
	})
	if err := registry.Deploy(artifact, compatibility, resolver, func(config.ModelRouteConfig) error { return nil }); err != nil {
		t.Fatal(err)
	}
	runtime := &ProviderRuntime{}
	runtime.AttachAdapterRegistry(registry)
	provider, model, reasoning := runtime.resolveAdapterRoute("openrouter", "base", "high")
	if provider != "openrouter" || model != "adapted" || reasoning != "high" {
		t.Fatalf("resolved route = %s/%s/%s", provider, model, reasoning)
	}
	provider, model, reasoning = runtime.resolveAdapterRoute("openrouter", "unrelated", "low")
	if provider != "openrouter" || model != "unrelated" || reasoning != "low" {
		t.Fatalf("unrelated route changed = %s/%s/%s", provider, model, reasoning)
	}
}

func TestProviderRuntimeRejectsAdapterWhenBaseProviderIsDisabled(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Providers.LLMux["openrouter"] = config.LLMuxProviderConfig{
		Enabled: false,
		Models:  []config.LLMuxModelConfig{{ID: "base"}},
	}
	cfg.Providers.LLMux["openai"] = config.LLMuxProviderConfig{
		Enabled: true,
		Models:  []config.LLMuxModelConfig{{ID: "adapted"}},
	}
	runtime := &ProviderRuntime{cfg: cfg}
	runtime.AttachAdapterRegistry(testAdapterRegistry(t, config.ModelRouteConfig{Provider: "openai", Model: "adapted"}))

	_, _, _, _, err := runtime.resolveDriverForAccount(context.Background(), "openrouter", "base", "", "")
	if err == nil || !strings.Contains(err.Error(), "enable openrouter") {
		t.Fatalf("disabled base provider was bypassed: %v", err)
	}
}

func TestProviderRuntimeRejectsAdapterWhenBaseModelIsDisabled(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Providers.LLMux["openrouter"] = config.LLMuxProviderConfig{
		Enabled: true,
		Models: []config.LLMuxModelConfig{
			{ID: "base", Disabled: true},
			{ID: "adapted"},
		},
	}
	runtime := &ProviderRuntime{cfg: cfg}
	runtime.AttachAdapterRegistry(testAdapterRegistry(t, config.ModelRouteConfig{Provider: "openrouter", Model: "adapted"}))

	_, _, _, _, err := runtime.resolveDriverForAccount(context.Background(), "openrouter", "base", "", "")
	if err == nil || !strings.Contains(err.Error(), `model "base" is disabled`) {
		t.Fatalf("disabled base model was bypassed: %v", err)
	}
}

func testAdapterRegistry(t *testing.T, servingRoute config.ModelRouteConfig) *adapterdeployment.Registry {
	t.Helper()
	compatibility := adapterdeployment.CompatibilityV1{
		Provider: "openrouter", Model: "base", Reasoning: servingRoute.Reasoning,
		BaseModelSHA256: strings.Repeat("a", 64), TokenizerSHA256: strings.Repeat("b", 64),
	}
	artifact, err := adapterdeployment.NewArtifact(adapterdeployment.ArtifactInputV1{
		Method: "lora", PayloadSHA256: strings.Repeat("c", 64), Compatibility: compatibility,
		TrainDataLineage: []session.SourceRefV1{{Kind: "dataset", ID: "train"}},
		Validation: adapterdeployment.ValidationReportV1{
			Status: "pass", HeldOutTasks: 100, CorrectnessDelta: 0.05, SafetyRetention: 1, FalseDecisionDelta: -0.01,
			TrainServeCostMicros: 1000, Digest: strings.Repeat("d", 64), Evidence: []session.SourceRefV1{{Kind: "report", ID: "held-out"}},
		},
		ServingRoute: config.ModelRouteConfig{Provider: compatibility.Provider, Model: servingRoute.Model, Reasoning: servingRoute.Reasoning}, RollbackTarget: "base", CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := adapterdeployment.New()
	resolver := adapterdeployment.ValidationReportResolverFunc(func(digest string) (adapterdeployment.ValidationReportV1, error) {
		if digest != artifact.Validation.Digest {
			return adapterdeployment.ValidationReportV1{}, errors.New("report is not in trusted storage")
		}
		return artifact.Validation, nil
	})
	if err := registry.Deploy(artifact, compatibility, resolver, func(config.ModelRouteConfig) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return registry
}
