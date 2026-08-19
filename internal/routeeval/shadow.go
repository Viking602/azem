package routeeval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const ShadowDecisionVersionV1 = 1

type ControlPlaneV1 struct {
	Route             string `json:"route"`
	PermissionsHash   string `json:"permissions_hash"`
	ApprovalMode      string `json:"approval_mode"`
	RetryOwner        string `json:"retry_owner"`
	Budget            string `json:"budget"`
	CancellationOwner string `json:"cancellation_owner"`
}

type ShadowAlternativeV1 struct {
	Route       string  `json:"route"`
	Score       float64 `json:"score"`
	Uncertainty float64 `json:"uncertainty"`
}

type ShadowDecisionV1 struct {
	Version           int                   `json:"version"`
	ID                string                `json:"id"`
	SessionID         string                `json:"session_id"`
	RunID             string                `json:"run_id"`
	Mode              string                `json:"mode"`
	ExecutedRoute     string                `json:"executed_route"`
	ShadowChoice      string                `json:"shadow_choice"`
	Alternatives      []ShadowAlternativeV1 `json:"alternatives"`
	Uncertainty       float64               `json:"uncertainty"`
	ExplorationReason string                `json:"exploration_reason"`
	ControlDigest     string                `json:"control_digest"`
	ObservedAt        time.Time             `json:"observed_at"`
}

func LogShadowDecision(sessionID, runID string, before, after ControlPlaneV1, alternatives []ShadowAlternativeV1, explorationReason string, observedAt time.Time) (ShadowDecisionV1, error) {
	if err := before.Validate(); err != nil {
		return ShadowDecisionV1{}, err
	}
	if err := after.Validate(); err != nil {
		return ShadowDecisionV1{}, err
	}
	beforeDigest := controlDigest(before)
	if beforeDigest != controlDigest(after) {
		return ShadowDecisionV1{}, fmt.Errorf("routeeval: shadow scoring changed execution control")
	}
	if sessionID == "" || runID == "" || len(alternatives) == 0 || observedAt.IsZero() {
		return ShadowDecisionV1{}, fmt.Errorf("routeeval: incomplete shadow decision")
	}
	ordered := append([]ShadowAlternativeV1(nil), alternatives...)
	seen := make(map[string]struct{}, len(ordered))
	for _, alternative := range ordered {
		if alternative.Route == "" || alternative.Score < 0 || alternative.Score > 1 || alternative.Uncertainty < 0 || alternative.Uncertainty > 1 {
			return ShadowDecisionV1{}, fmt.Errorf("routeeval: invalid shadow alternative")
		}
		if _, exists := seen[alternative.Route]; exists {
			return ShadowDecisionV1{}, fmt.Errorf("routeeval: duplicate shadow route %q", alternative.Route)
		}
		seen[alternative.Route] = struct{}{}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		if ordered[i].Uncertainty != ordered[j].Uncertainty {
			return ordered[i].Uncertainty < ordered[j].Uncertainty
		}
		return ordered[i].Route < ordered[j].Route
	})
	identity := struct {
		SessionID     string                `json:"session_id"`
		RunID         string                `json:"run_id"`
		ControlDigest string                `json:"control_digest"`
		Alternatives  []ShadowAlternativeV1 `json:"alternatives"`
	}{sessionID, runID, beforeDigest, ordered}
	decision := ShadowDecisionV1{
		Version: ShadowDecisionVersionV1, ID: "shadow:" + digestValue(identity)[:24], SessionID: sessionID, RunID: runID, Mode: "shadow",
		ExecutedRoute: before.Route, ShadowChoice: ordered[0].Route, Alternatives: ordered, Uncertainty: ordered[0].Uncertainty,
		ExplorationReason: strings.TrimSpace(explorationReason), ControlDigest: beforeDigest, ObservedAt: observedAt.UTC(),
	}
	return decision, decision.Validate()
}

func (control ControlPlaneV1) Validate() error {
	if control.Route == "" || control.PermissionsHash == "" || control.ApprovalMode == "" || control.RetryOwner == "" || control.Budget == "" || control.CancellationOwner == "" {
		return fmt.Errorf("routeeval: incomplete control plane")
	}
	return nil
}

func (decision ShadowDecisionV1) Validate() error {
	if decision.Version != ShadowDecisionVersionV1 || decision.ID == "" || decision.SessionID == "" || decision.RunID == "" || decision.Mode != "shadow" || decision.ExecutedRoute == "" || decision.ShadowChoice == "" || len(decision.Alternatives) == 0 || decision.Uncertainty < 0 || decision.Uncertainty > 1 || decision.ControlDigest == "" || decision.ObservedAt.IsZero() {
		return fmt.Errorf("routeeval: invalid shadow decision")
	}
	return nil
}

type ShadowStore struct {
	sessions  *session.Service
	sessionID string
	runID     string
}

func NewShadowStore(sessions *session.Service, sessionID, runID string) (*ShadowStore, error) {
	if sessions == nil || sessionID == "" || runID == "" {
		return nil, fmt.Errorf("routeeval: shadow store requires session and run ids")
	}
	return &ShadowStore{sessions: sessions, sessionID: sessionID, runID: runID}, nil
}

func (s *ShadowStore) Save(ctx context.Context, decision ShadowDecisionV1) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	if decision.SessionID != s.sessionID || decision.RunID != s.runID {
		return fmt.Errorf("routeeval: shadow store identity mismatch")
	}
	payload, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	kind := session.InternalArtifactKindPrefix + "route_shadow_v1:" + digestValue(decision.ID)[:24]
	if _, err := s.sessions.PutArtifact(ctx, s.sessionID, s.runID, kind, payload, decision.ShadowChoice); err != nil {
		return err
	}
	return nil
}

func (s *ShadowStore) Latest(ctx context.Context, decisionID string) (ShadowDecisionV1, error) {
	kind := session.InternalArtifactKindPrefix + "route_shadow_v1:" + digestValue(decisionID)[:24]
	artifact, err := s.sessions.LoadLatestArtifactByKind(ctx, s.sessionID, kind)
	if err != nil {
		return ShadowDecisionV1{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(artifact.Payload))
	decoder.DisallowUnknownFields()
	var decision ShadowDecisionV1
	if err := decoder.Decode(&decision); err != nil {
		return ShadowDecisionV1{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ShadowDecisionV1{}, fmt.Errorf("routeeval: shadow decision has trailing JSON")
	}
	if err := decision.Validate(); err != nil {
		return ShadowDecisionV1{}, err
	}
	if decision.ID != decisionID || decision.SessionID != s.sessionID || decision.RunID != s.runID {
		return ShadowDecisionV1{}, fmt.Errorf("routeeval: shadow artifact identity mismatch")
	}
	return decision, nil
}

func controlDigest(control ControlPlaneV1) string {
	return digestValue(control)
}

func digestValue(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
