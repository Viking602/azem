package verification

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

const verificationResultArtifactPrefix = session.InternalArtifactKindPrefix + "verification_result_v1:"

type ArtifactResultStore struct {
	sessions  *session.Service
	sessionID string
	runID     string
}

func NewArtifactResultStore(sessions *session.Service, sessionID, runID string) (*ArtifactResultStore, error) {
	if sessions == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("verification: artifact result store requires session service, session id, and run id")
	}
	return &ArtifactResultStore{sessions: sessions, sessionID: sessionID, runID: runID}, nil
}

func (s *ArtifactResultStore) Latest(ctx context.Context, workSpecID string) (session.VerificationResultV1, error) {
	kind, err := verificationResultArtifactKind(workSpecID)
	if err != nil {
		return session.VerificationResultV1{}, err
	}
	artifact, err := s.sessions.LoadLatestArtifactByKind(ctx, s.sessionID, kind)
	if errors.Is(err, session.ErrContextArtifactNotFound) {
		return session.VerificationResultV1{}, ErrNoVerificationResult
	}
	if err != nil {
		return session.VerificationResultV1{}, err
	}
	var result session.VerificationResultV1
	if err := decodeStrictJSON(artifact.Payload, &result); err != nil {
		return session.VerificationResultV1{}, fmt.Errorf("verification: decode result artifact %s: %w", artifact.ID, err)
	}
	if result.WorkSpecID != workSpecID {
		return session.VerificationResultV1{}, fmt.Errorf("verification: artifact %s belongs to work spec %s", artifact.ID, result.WorkSpecID)
	}
	if err := result.Validate(); err != nil {
		return session.VerificationResultV1{}, fmt.Errorf("verification: invalid result artifact %s: %w", artifact.ID, err)
	}
	return result, nil
}

func (s *ArtifactResultStore) Save(ctx context.Context, result session.VerificationResultV1) error {
	if err := result.Validate(); err != nil {
		return err
	}
	kind, err := verificationResultArtifactKind(result.WorkSpecID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("verification: encode result: %w", err)
	}
	preview := fmt.Sprintf("Verification %s · %d criteria · snapshot %s", result.Status, len(result.Criteria), result.SnapshotHash)
	if _, err := s.sessions.PutArtifact(ctx, s.sessionID, s.runID, kind, payload, preview); err != nil {
		return fmt.Errorf("verification: persist result artifact: %w", err)
	}
	return nil
}

func verificationResultArtifactKind(workSpecID string) (string, error) {
	if strings.TrimSpace(workSpecID) == "" {
		return "", fmt.Errorf("verification: work spec id is required")
	}
	return verificationResultArtifactPrefix + shortHash(workSpecID), nil
}

func decodeStrictJSON(payload []byte, destination any) error {
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
