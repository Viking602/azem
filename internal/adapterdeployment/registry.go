// Package adapterdeployment validates versioned adapter artifacts and maps an
// exact base route to an adapted model. Provider selection remains owned by the
// existing application route resolver.
package adapterdeployment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

var (
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	adapterIDPattern = regexp.MustCompile(`^adapter:[0-9a-f]{64}$`)
)

type CompatibilityV1 struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	Reasoning       string `json:"reasoning"`
	BaseModelSHA256 string `json:"base_model_sha256"`
	TokenizerSHA256 string `json:"tokenizer_sha256"`
}

type ValidationReportV1 struct {
	Status               string                `json:"status"`
	HeldOutTasks         int                   `json:"held_out_tasks"`
	CorrectnessDelta     float64               `json:"correctness_delta"`
	SafetyRetention      float64               `json:"safety_retention"`
	FalseDecisionDelta   float64               `json:"false_decision_delta"`
	TrainServeCostMicros int64                 `json:"train_serve_cost_micros"`
	Digest               string                `json:"digest"`
	Evidence             []session.SourceRefV1 `json:"evidence"`
}

type ArtifactInputV1 struct {
	Method           string                  `json:"method"`
	PayloadSHA256    string                  `json:"payload_sha256"`
	Compatibility    CompatibilityV1         `json:"compatibility"`
	TrainDataLineage []session.SourceRefV1   `json:"train_data_lineage"`
	Validation       ValidationReportV1      `json:"validation"`
	ServingRoute     config.ModelRouteConfig `json:"serving_route"`
	RollbackTarget   string                  `json:"rollback_target"`
	KillSwitch       bool                    `json:"kill_switch"`
	CreatedAt        time.Time               `json:"created_at"`
}

type ArtifactV1 struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	ArtifactInputV1
}

type ResolutionV1 struct {
	Route          config.ModelRouteConfig `json:"route"`
	AdapterID      string                  `json:"adapter_id,omitempty"`
	ArtifactSHA256 string                  `json:"artifact_sha256,omitempty"`
}

// RouteValidator checks that the exact configured serving route is available
// in the existing provider/catalog boundary. It is intentionally separate from
// report verification: a route being resolvable is not evidence that an
// artifact passed held-out validation.
type RouteValidator func(config.ModelRouteConfig) error

// ValidationReportResolver resolves a held-out report from trusted evidence.
// Deploy never accepts the report embedded in an artifact as proof on its own;
// the resolver must return the report addressed by the artifact's digest.
type ValidationReportResolver interface {
	ResolveValidationReport(digest string) (ValidationReportV1, error)
}

// ValidationReportResolverFunc adapts a function to ValidationReportResolver.
type ValidationReportResolverFunc func(string) (ValidationReportV1, error)

func (resolver ValidationReportResolverFunc) ResolveValidationReport(digest string) (ValidationReportV1, error) {
	if resolver == nil {
		return ValidationReportV1{}, fmt.Errorf("adapter deployment: validation report resolver is unavailable")
	}
	return resolver(digest)
}

type Registry struct {
	mu        sync.RWMutex
	artifacts map[string]ArtifactV1
	active    map[string]string
	killed    map[string]bool
}

func New() *Registry {
	return &Registry{artifacts: make(map[string]ArtifactV1), active: make(map[string]string), killed: make(map[string]bool)}
}

func NewArtifact(input ArtifactInputV1) (ArtifactV1, error) {
	artifact := ArtifactV1{Version: 1, ArtifactInputV1: cloneInput(input)}
	artifact.ID = artifactIdentity(artifact)
	if err := artifact.Validate(); err != nil {
		return ArtifactV1{}, err
	}
	return artifact, nil
}

func (artifact ArtifactV1) Validate() error {
	if artifact.Version != 1 || !adapterIDPattern.MatchString(artifact.ID) || artifact.ID != artifactIdentity(artifact) || !adapterMethod(artifact.Method) || !sha256Pattern.MatchString(artifact.PayloadSHA256) || artifact.KillSwitch || artifact.CreatedAt.IsZero() {
		return fmt.Errorf("adapter deployment: invalid artifact")
	}
	compatibility := artifact.Compatibility
	if strings.TrimSpace(compatibility.Provider) == "" || strings.TrimSpace(compatibility.Provider) != compatibility.Provider ||
		strings.TrimSpace(compatibility.Model) == "" || strings.TrimSpace(compatibility.Model) != compatibility.Model ||
		!sha256Pattern.MatchString(compatibility.BaseModelSHA256) || !sha256Pattern.MatchString(compatibility.TokenizerSHA256) ||
		compatibility.BaseModelSHA256 == compatibility.TokenizerSHA256 || artifact.PayloadSHA256 == compatibility.BaseModelSHA256 || artifact.PayloadSHA256 == compatibility.TokenizerSHA256 {
		return fmt.Errorf("adapter deployment: invalid compatibility identity")
	}
	if artifact.ServingRoute.Provider != compatibility.Provider || strings.TrimSpace(artifact.ServingRoute.Model) == "" || strings.TrimSpace(artifact.ServingRoute.Model) != artifact.ServingRoute.Model || artifact.ServingRoute.Model == compatibility.Model || artifact.ServingRoute.Reasoning != compatibility.Reasoning {
		return fmt.Errorf("adapter deployment: serving route must use an adapted model on the exact existing route")
	}
	if len(artifact.TrainDataLineage) == 0 {
		return fmt.Errorf("adapter deployment: training lineage is required")
	}
	seenEvidence := make(map[string]struct{}, len(artifact.TrainDataLineage)+len(artifact.Validation.Evidence))
	for _, ref := range append(append([]session.SourceRefV1(nil), artifact.TrainDataLineage...), artifact.Validation.Evidence...) {
		if err := validateSourceRef(ref); err != nil {
			return err
		}
		key := sourceRefIdentity(ref)
		if _, exists := seenEvidence[key]; exists {
			return fmt.Errorf("adapter deployment: duplicate evidence identity")
		}
		seenEvidence[key] = struct{}{}
	}
	if err := validateReport(artifact.Validation); err != nil {
		return err
	}
	if artifact.RollbackTarget == "" || strings.TrimSpace(artifact.RollbackTarget) != artifact.RollbackTarget || (artifact.RollbackTarget != "base" && (!adapterIDPattern.MatchString(artifact.RollbackTarget) || artifact.RollbackTarget == artifact.ID)) {
		return fmt.Errorf("adapter deployment: rollback target is required")
	}
	return nil
}

// Deploy stores an artifact only after both independent trust boundaries pass.
// compatibility belongs to the caller's existing route boundary;
// reportResolver must resolve the held-out report from trusted storage, and
// validateRoute must resolve the exact configured serving route.
func (registry *Registry) Deploy(artifact ArtifactV1, compatibility CompatibilityV1, reportResolver ValidationReportResolver, validateRoute RouteValidator) error {
	if registry == nil {
		return fmt.Errorf("adapter deployment: registry is unavailable")
	}
	if err := artifact.Validate(); err != nil {
		return err
	}
	if compatibility != artifact.Compatibility {
		return fmt.Errorf("adapter deployment: route or base-model compatibility mismatch")
	}
	if reportResolver == nil {
		return fmt.Errorf("adapter deployment: trusted validation report resolver is required")
	}
	if validateRoute == nil {
		return fmt.Errorf("adapter deployment: existing route validator is required")
	}
	trustedReport, err := reportResolver.ResolveValidationReport(artifact.Validation.Digest)
	if err != nil {
		return fmt.Errorf("adapter deployment: held-out report resolution: %w", err)
	}
	if err := validateReport(trustedReport); err != nil {
		return fmt.Errorf("adapter deployment: trusted held-out report: %w", err)
	}
	if trustedReport.Digest != artifact.Validation.Digest || !sameReport(trustedReport, artifact.Validation) {
		return fmt.Errorf("adapter deployment: embedded validation report does not match trusted evidence")
	}
	if err := validateRoute(artifact.ServingRoute); err != nil {
		return fmt.Errorf("adapter deployment: serving route validation: %w", err)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if artifact.RollbackTarget != "base" {
		rollback, exists := registry.artifacts[artifact.RollbackTarget]
		if !exists || registry.killed[artifact.RollbackTarget] || baseRouteKey(rollback.Compatibility.Provider, rollback.Compatibility.Model, rollback.Compatibility.Reasoning) != baseRouteKey(artifact.Compatibility.Provider, artifact.Compatibility.Model, artifact.Compatibility.Reasoning) {
			return fmt.Errorf("adapter deployment: rollback artifact is unavailable or incompatible")
		}
	}
	if existing, exists := registry.artifacts[artifact.ID]; exists {
		if registry.killed[artifact.ID] {
			return fmt.Errorf("adapter deployment: artifact is permanently killed")
		}
		if !sameArtifact(existing, artifact) {
			return fmt.Errorf("adapter deployment: artifact identity is already registered with different content")
		}
	}
	registry.artifacts[artifact.ID] = cloneArtifact(artifact)
	registry.killed[artifact.ID] = false
	registry.active[baseRouteKey(artifact.Compatibility.Provider, artifact.Compatibility.Model, artifact.Compatibility.Reasoning)] = artifact.ID
	return nil
}

func (registry *Registry) Resolve(route config.ModelRouteConfig) ResolutionV1 {
	resolution := ResolutionV1{Route: route}
	if registry == nil {
		return resolution
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	artifactID := registry.active[baseRouteKey(route.Provider, route.Model, route.Reasoning)]
	artifact, exists := registry.artifacts[artifactID]
	if !exists || registry.killed[artifactID] || artifact.Validate() != nil {
		return resolution
	}
	resolution.Route = artifact.ServingRoute
	resolution.AdapterID = artifact.ID
	resolution.ArtifactSHA256 = artifact.PayloadSHA256
	return resolution
}

func (registry *Registry) Kill(artifactID string) error {
	if registry == nil {
		return fmt.Errorf("adapter deployment: registry is unavailable")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	artifact, exists := registry.artifacts[artifactID]
	if !exists {
		return fmt.Errorf("adapter deployment: artifact %q is unknown", artifactID)
	}
	registry.killed[artifactID] = true
	key := baseRouteKey(artifact.Compatibility.Provider, artifact.Compatibility.Model, artifact.Compatibility.Reasoning)
	if registry.active[key] != artifactID {
		return nil
	}
	if artifact.RollbackTarget == "base" || registry.killed[artifact.RollbackTarget] {
		delete(registry.active, key)
	} else if rollback, exists := registry.artifacts[artifact.RollbackTarget]; exists && rollback.Validate() == nil {
		registry.active[key] = artifact.RollbackTarget
	} else {
		delete(registry.active, key)
	}
	return nil
}

func artifactIdentity(artifact ArtifactV1) string {
	artifact.ID = ""
	payload, _ := json.Marshal(artifact)
	digest := sha256.Sum256(payload)
	return "adapter:" + hex.EncodeToString(digest[:])
}

func baseRouteKey(provider, model, reasoning string) string {
	return provider + "\x00" + model + "\x00" + reasoning
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func adapterMethod(method string) bool {
	switch method {
	case "prefix_soft_prompt", "reft", "lora", "qlora", "activation_steering":
		return true
	default:
		return false
	}
}

func validateReport(report ValidationReportV1) error {
	if report.Status != "pass" || report.HeldOutTasks <= 0 ||
		!finite(report.CorrectnessDelta) || report.CorrectnessDelta <= 0 || report.CorrectnessDelta > 1 ||
		!finite(report.SafetyRetention) || report.SafetyRetention < 0 || report.SafetyRetention > 1 ||
		!finite(report.FalseDecisionDelta) || report.FalseDecisionDelta < -1 || report.FalseDecisionDelta > 0 ||
		report.TrainServeCostMicros <= 0 || !sha256Pattern.MatchString(report.Digest) || len(report.Evidence) == 0 {
		return fmt.Errorf("adapter deployment: validation report is not deployable")
	}
	seen := make(map[string]struct{}, len(report.Evidence))
	for _, ref := range report.Evidence {
		if err := validateSourceRef(ref); err != nil {
			return err
		}
		key := sourceRefIdentity(ref)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("adapter deployment: duplicate validation evidence identity")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateSourceRef(ref session.SourceRefV1) error {
	if strings.TrimSpace(ref.Kind) == "" || strings.TrimSpace(ref.Kind) != ref.Kind || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.ID) != ref.ID {
		return fmt.Errorf("adapter deployment: invalid evidence")
	}
	if ref.SHA256 != "" && !sha256Pattern.MatchString(ref.SHA256) {
		return fmt.Errorf("adapter deployment: invalid evidence digest")
	}
	return nil
}

func sourceRefIdentity(ref session.SourceRefV1) string {
	return ref.Kind + "\x00" + ref.ID + "\x00" + ref.SHA256
}

func sameReport(left, right ValidationReportV1) bool {
	left.Evidence = append([]session.SourceRefV1(nil), left.Evidence...)
	right.Evidence = append([]session.SourceRefV1(nil), right.Evidence...)
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func sameArtifact(left, right ArtifactV1) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func cloneInput(input ArtifactInputV1) ArtifactInputV1 {
	input.TrainDataLineage = append([]session.SourceRefV1(nil), input.TrainDataLineage...)
	input.Validation.Evidence = append([]session.SourceRefV1(nil), input.Validation.Evidence...)
	return input
}

func cloneArtifact(artifact ArtifactV1) ArtifactV1 {
	artifact.ArtifactInputV1 = cloneInput(artifact.ArtifactInputV1)
	return artifact
}
