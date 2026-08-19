package workrevision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Viking602/azem/internal/session"
)

const revisionArtifactPrefix = session.InternalArtifactKindPrefix + "work_revision_v1:"

type Store struct {
	sessions  *session.Service
	sessionID string
	runID     string
}

func NewStore(sessions *session.Service, sessionID, runID string) (*Store, error) {
	if sessions == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("work revision: store requires session service, session id, and run id")
	}
	return &Store{sessions: sessions, sessionID: sessionID, runID: runID}, nil
}

func (s *Store) SaveRevision(ctx context.Context, revision session.WorkRevisionV1) error {
	if revision.SessionID != s.sessionID || revision.Validate() != nil {
		return fmt.Errorf("work revision: invalid revision for session")
	}
	return s.save(ctx, revisionArtifactKind(revision.ID), revision.ID, revision)
}

func (s *Store) LatestRevision(ctx context.Context) (session.WorkRevisionV1, error) {
	artifact, err := s.sessions.LoadLatestArtifactByKindPrefix(ctx, s.sessionID, revisionArtifactPrefix)
	if err != nil {
		return session.WorkRevisionV1{}, err
	}
	return s.decodeRevision(artifact, "")
}

func (s *Store) Revision(ctx context.Context, revisionID string) (session.WorkRevisionV1, error) {
	if strings.TrimSpace(revisionID) == "" {
		return session.WorkRevisionV1{}, fmt.Errorf("work revision: revision id is required")
	}
	artifact, err := s.sessions.LoadLatestArtifactByKind(ctx, s.sessionID, revisionArtifactKind(revisionID))
	if err != nil {
		return session.WorkRevisionV1{}, err
	}
	return s.decodeRevision(artifact, revisionID)
}

func (s *Store) decodeRevision(artifact session.ContextArtifact, expectedID string) (session.WorkRevisionV1, error) {
	var revision session.WorkRevisionV1
	if err := decodeStrict(artifact.Payload, &revision); err != nil {
		return session.WorkRevisionV1{}, fmt.Errorf("work revision: decode artifact %s: %w", artifact.ID, err)
	}
	if err := revision.Validate(); err != nil {
		return session.WorkRevisionV1{}, err
	}
	if revision.SessionID != s.sessionID || (expectedID != "" && revision.ID != expectedID) {
		return session.WorkRevisionV1{}, fmt.Errorf("work revision: artifact identity mismatch")
	}
	return revision, nil
}

func (s *Store) SaveIntent(ctx context.Context, intent session.ActionIntentV1) error {
	if intent.SessionID != s.sessionID || intent.RunID != s.runID {
		return fmt.Errorf("work revision: intent is not bound to this run")
	}
	if err := s.requireCurrentRevision(ctx, intent.RevisionID, intent.SnapshotHash); err != nil {
		return err
	}
	return s.save(ctx, session.InternalArtifactKindPrefix+"action_intent_v1:"+shortID(intent.ID), intent.ID, intent)
}

func (s *Store) SaveObservation(ctx context.Context, observation session.ObservationEnvelopeV1) error {
	if observation.SessionID != s.sessionID || observation.RunID != s.runID {
		return fmt.Errorf("work revision: observation is not bound to this run")
	}
	if err := s.requireCurrentRevision(ctx, observation.RevisionID, observation.SnapshotHash); err != nil {
		return err
	}
	return s.save(ctx, session.InternalArtifactKindPrefix+"observation_envelope_v1:"+shortID(observation.ID), observation.ID, observation)
}

func (s *Store) requireCurrentRevision(ctx context.Context, revisionID, snapshotHash string) error {
	current, err := s.LatestRevision(ctx)
	if err != nil {
		return fmt.Errorf("work revision: resolve latest revision: %w", err)
	}
	if revisionID == "" || revisionID != current.ID || (snapshotHash != "" && snapshotHash != current.SnapshotHash) {
		return fmt.Errorf("work revision: artifact is stale relative to latest durable revision")
	}
	return nil
}

func (s *Store) SaveDisposition(ctx context.Context, disposition DispositionV1) error {
	if err := disposition.Validate(); err != nil {
		return err
	}
	if err := s.requireCurrentRevision(ctx, disposition.CurrentRevisionID, ""); err != nil {
		return err
	}
	if _, err := s.Revision(ctx, disposition.SourceRevisionID); err != nil {
		return err
	}
	if err := s.requireIntentRun(ctx, disposition.IntentID); err != nil {
		return err
	}
	return s.save(ctx, session.InternalArtifactKindPrefix+"work_disposition_v1:"+shortID(disposition.IntentID), disposition.ID, disposition)
}

func (s *Store) SaveGuidanceDecision(ctx context.Context, decision GuidanceDecisionV1) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	if err := s.requireCurrentRevision(ctx, decision.CurrentRevisionID, ""); err != nil {
		return err
	}
	if _, err := s.Revision(ctx, decision.SourceRevisionID); err != nil {
		return err
	}
	if err := s.requireIntentRun(ctx, decision.IntentID); err != nil {
		return err
	}
	return s.save(ctx, session.InternalArtifactKindPrefix+"guidance_decision_v1:"+shortID(decision.IntentID), decision.ID, decision)
}

func (s *Store) requireIntentRun(ctx context.Context, intentID string) error {
	artifact, err := s.sessions.LoadLatestArtifactByKind(ctx, s.sessionID, session.InternalArtifactKindPrefix+"action_intent_v1:"+shortID(intentID))
	if err != nil {
		return fmt.Errorf("work revision: resolve intent payload: %w", err)
	}
	var intent session.ActionIntentV1
	if err := decodeStrict(artifact.Payload, &intent); err != nil {
		return fmt.Errorf("work revision: decode intent payload: %w", err)
	}
	if intent.ID != intentID || intent.SessionID != s.sessionID || intent.RunID != s.runID {
		return fmt.Errorf("work revision: intent payload is not bound to this session and run")
	}
	return nil
}

func (s *Store) SaveVerificationPlan(ctx context.Context, plan session.VerificationPlanV1) error {
	if plan.RevisionID == "" || plan.SnapshotHash == "" {
		return fmt.Errorf("work revision: verification plan is unbound")
	}
	if err := s.requireCurrentRevision(ctx, plan.RevisionID, plan.SnapshotHash); err != nil {
		return err
	}
	return s.save(ctx, session.InternalArtifactKindPrefix+"verification_plan_v1:"+shortID(plan.ID), plan.ID, plan)
}

func (s *Store) SaveVerificationResult(ctx context.Context, result session.VerificationResultV1) error {
	if result.RevisionID == "" || result.SnapshotHash == "" {
		return fmt.Errorf("work revision: verification result is unbound")
	}
	if err := s.requireCurrentRevision(ctx, result.RevisionID, result.SnapshotHash); err != nil {
		return err
	}
	return s.save(ctx, session.InternalArtifactKindPrefix+"verification_result_v1:"+shortID(result.ID), result.ID, result)
}

func BindIntent(revision session.WorkRevisionV1, intent session.ActionIntentV1) (session.ActionIntentV1, error) {
	if err := revision.Validate(); err != nil {
		return session.ActionIntentV1{}, err
	}
	if intent.SessionID != revision.SessionID {
		return session.ActionIntentV1{}, fmt.Errorf("work revision: intent session mismatch")
	}
	intent.RevisionID = revision.ID
	intent.SnapshotHash = revision.SnapshotHash
	return intent, intent.Validate()
}

func BindObservation(revision session.WorkRevisionV1, intent session.ActionIntentV1, observation session.ObservationEnvelopeV1) (session.ObservationEnvelopeV1, error) {
	if intent.RevisionID != revision.ID || intent.SnapshotHash != revision.SnapshotHash || observation.IntentID != intent.ID || observation.SessionID != intent.SessionID || observation.RunID != intent.RunID {
		return session.ObservationEnvelopeV1{}, fmt.Errorf("work revision: observation intent mismatch")
	}
	observation.RevisionID = revision.ID
	observation.SnapshotHash = revision.SnapshotHash
	return observation, observation.Validate()
}

func BindVerificationPlan(revision session.WorkRevisionV1, plan session.VerificationPlanV1) (session.VerificationPlanV1, error) {
	plan.RevisionID = revision.ID
	plan.SnapshotHash = revision.SnapshotHash
	return plan, plan.Validate()
}

func BindVerificationResult(revision session.WorkRevisionV1, plan session.VerificationPlanV1, result session.VerificationResultV1) (session.VerificationResultV1, error) {
	if plan.RevisionID != revision.ID || plan.SnapshotHash != revision.SnapshotHash || result.PlanID != plan.ID || result.WorkSpecID != plan.WorkSpecID {
		return session.VerificationResultV1{}, fmt.Errorf("work revision: verification result plan mismatch")
	}
	result.RevisionID = revision.ID
	result.SnapshotHash = revision.SnapshotHash
	return result, result.Validate()
}

func (s *Store) save(ctx context.Context, kind, id string, value interface{ Validate() error }) error {
	if err := value.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("work revision: encode %s: %w", kind, err)
	}
	if _, err := s.sessions.PutArtifact(ctx, s.sessionID, s.runID, kind, payload, id); err != nil {
		return fmt.Errorf("work revision: persist %s: %w", kind, err)
	}
	return nil
}

func revisionArtifactKind(revisionID string) string {
	return revisionArtifactPrefix + shortID(revisionID)
}

func shortID(value string) string {
	digest := digestJSON(value)
	return digest[:24]
}

func decodeStrict(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
