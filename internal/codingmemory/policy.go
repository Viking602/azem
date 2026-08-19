package codingmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const PolicyVersionV1 = 1

type PolicyV1 struct {
	Version                    int       `json:"version"`
	UserID                     string    `json:"user_id"`
	ExperiencePromotionEnabled bool      `json:"experience_promotion_enabled"`
	LocalLearningEnabled       bool      `json:"local_learning_enabled"`
	RetentionDays              int       `json:"retention_days"`
	Revision                   int64     `json:"revision"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

func DefaultPolicy(userID string) PolicyV1 {
	return PolicyV1{Version: PolicyVersionV1, UserID: userID, RetentionDays: 30}
}

func (policy PolicyV1) Validate() error {
	if policy.Version != PolicyVersionV1 || strings.TrimSpace(policy.UserID) == "" || policy.RetentionDays < 0 || policy.RetentionDays > 3650 || policy.Revision < 0 {
		return fmt.Errorf("coding memory: invalid policy")
	}
	if policy.Revision > 0 && policy.UpdatedAt.IsZero() {
		return fmt.Errorf("coding memory: updated policy lacks timestamp")
	}
	return nil
}

type PolicyStore struct {
	sessions  *session.Service
	sessionID string
	runID     string
	userID    string
}

func NewPolicyStore(sessions *session.Service, sessionID, runID, userID string) (*PolicyStore, error) {
	if sessions == nil || sessionID == "" || runID == "" || strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("coding memory: policy store requires session, run, and user ids")
	}
	return &PolicyStore{sessions: sessions, sessionID: sessionID, runID: runID, userID: userID}, nil
}

func (s *PolicyStore) Load(ctx context.Context) (PolicyV1, error) {
	artifact, err := s.sessions.LoadLatestArtifactByKind(ctx, s.sessionID, policyArtifactKind(s.userID))
	if err != nil {
		if errors.Is(err, session.ErrContextArtifactNotFound) {
			return DefaultPolicy(s.userID), nil
		}
		return PolicyV1{}, err
	}
	var policy PolicyV1
	if err := decodeStrict(artifact.Payload, &policy); err != nil {
		return PolicyV1{}, err
	}
	if err := policy.Validate(); err != nil {
		return PolicyV1{}, err
	}
	if policy.UserID != s.userID {
		return PolicyV1{}, fmt.Errorf("coding memory: policy user mismatch")
	}
	return policy, nil
}

// Update accepts policy changes only through an explicit user action. Internal
// jobs and model output cannot silently enable promotion or local learning.
func (s *PolicyStore) Update(ctx context.Context, next PolicyV1, explicitUserAction bool, now time.Time) (PolicyV1, error) {
	if !explicitUserAction {
		return PolicyV1{}, fmt.Errorf("coding memory: explicit user action required to update policy")
	}
	current, err := s.Load(ctx)
	if err != nil {
		return PolicyV1{}, err
	}
	if next.UserID != s.userID || next.Revision != current.Revision {
		return PolicyV1{}, fmt.Errorf("coding memory: stale or mismatched policy update")
	}
	next.Version = PolicyVersionV1
	next.Revision++
	next.UpdatedAt = now.UTC()
	if err := next.Validate(); err != nil {
		return PolicyV1{}, err
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return PolicyV1{}, err
	}
	if _, err := s.sessions.PutArtifact(ctx, s.sessionID, s.runID, policyArtifactKind(s.userID), payload, fmt.Sprintf("coding memory policy revision %d", next.Revision)); err != nil {
		return PolicyV1{}, err
	}
	return next, nil
}

func PromoteExperience(candidate session.ExperienceCandidateV1, scope ScopeV1, policy PolicyV1, now time.Time) (MemoryV1, error) {
	if err := policy.Validate(); err != nil {
		return MemoryV1{}, err
	}
	if !policy.ExperiencePromotionEnabled {
		return MemoryV1{}, fmt.Errorf("coding memory: experience promotion is disabled")
	}
	if err := candidate.Validate(); err != nil {
		return MemoryV1{}, err
	}
	if candidate.Scope != scope.Kind {
		return MemoryV1{}, fmt.Errorf("coding memory: experience scope %q does not authorize %q", candidate.Scope, scope.Kind)
	}
	if scope.ID == "" {
		return MemoryV1{}, fmt.Errorf("coding memory: experience scope id is required")
	}
	if candidate.ExpiresAt != nil && !candidate.ExpiresAt.After(now) {
		return MemoryV1{}, fmt.Errorf("coding memory: experience candidate expired")
	}
	kind := candidate.Kind
	if !oneOf(kind, KindStrategy, KindToolLesson, KindAssetRef) {
		return MemoryV1{}, fmt.Errorf("coding memory: unsupported experience kind %q", kind)
	}
	confidence, ok := map[string]float64{"low": 0.5, "medium": 0.7, "high": 0.9, "verified": 1}[candidate.Confidence]
	if !ok {
		return MemoryV1{}, fmt.Errorf("coding memory: unsupported confidence %q", candidate.Confidence)
	}
	sources := make([]AttributionV1, 0, len(candidate.Evidence))
	for _, ref := range candidate.Evidence {
		sources = append(sources, AttributionV1{Ref: ref, Authority: sourceAuthority(ref)})
	}
	memory := MemoryV1{
		Version: 1, ID: "experience:" + candidate.ID, Kind: kind, Scope: scope, Content: candidate.Content,
		Confidence: confidence, Status: StatusActive, Origin: OriginDerived, Sources: sources,
		Supersedes: append([]string(nil), candidate.Supersedes...), CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	return memory, memory.Validate()
}

func AllowsLocalLearning(policy PolicyV1) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if !policy.LocalLearningEnabled {
		return fmt.Errorf("coding memory: local learning is disabled")
	}
	return nil
}

func policyArtifactKind(userID string) string {
	digest := sha256.Sum256([]byte(userID))
	return session.InternalArtifactKindPrefix + "coding_memory_policy_v1:" + hex.EncodeToString(digest[:12])
}

func sourceAuthority(ref session.SourceRefV1) string {
	switch {
	case ref.Kind == "user_turn":
		return "user"
	case ref.Kind == "workspace_file":
		return "workspace"
	case strings.HasPrefix(ref.Kind, "external"):
		return "secondary"
	case strings.HasPrefix(ref.Kind, "validator"):
		return "validator"
	default:
		return "history"
	}
}
