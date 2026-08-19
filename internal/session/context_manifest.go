package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

type ContextManifestRecord struct {
	ID                 string
	RunID              string
	CanonicalHighWater *int64
	PolicyVersion      int
	ManifestHash       string
	Data               json.RawMessage
	CreatedAt          time.Time
}

func (s *Service) LoadActiveContextManifest(ctx context.Context, sessionID string) (ContextManifestRecord, error) {
	row, err := dbgen.New(s.db).GetActiveContextManifest(ctx, sessionID)
	if err != nil {
		return ContextManifestRecord{}, err
	}
	return contextManifestFromRow(row), nil
}

func persistContextManifest(ctx context.Context, queries *dbgen.Queries, sessionID string, manifest *ContextManifestRecord, now int64) error {
	if manifest == nil {
		return nil
	}
	if err := validateContextManifest(*manifest); err != nil {
		return err
	}
	highWater := int64(-1)
	if manifest.CanonicalHighWater != nil {
		highWater = *manifest.CanonicalHighWater
	}
	if err := queries.DeactivateContextManifests(ctx, sessionID); err != nil {
		return err
	}
	return queries.UpsertContextManifest(ctx, dbgen.UpsertContextManifestParams{
		ID: manifest.ID, SessionID: sessionID, RunID: manifest.RunID, CanonicalHighWater: highWater,
		SemanticRevision: 0, PolicyVersion: int64(manifest.PolicyVersion), ManifestHash: manifest.ManifestHash,
		Activated: 1, Data: manifest.Data, CreatedAt: now,
	})
}

func validateContextManifest(manifest ContextManifestRecord) error {
	if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.ManifestHash) == "" || manifest.PolicyVersion <= 0 || !json.Valid(manifest.Data) {
		return fmt.Errorf("context manifest is incomplete")
	}
	var data struct {
		ID            string `json:"id"`
		PolicyVersion int    `json:"policy_version"`
		ManifestHash  string `json:"manifest_hash"`
	}
	if json.Unmarshal(manifest.Data, &data) != nil || data.ID != manifest.ID || data.ManifestHash != manifest.ManifestHash || data.PolicyVersion != manifest.PolicyVersion {
		return fmt.Errorf("context manifest metadata does not match its payload")
	}
	return nil
}

func contextManifestFromRow(row dbgen.GetActiveContextManifestRow) ContextManifestRecord {
	var highWater *int64
	if row.CanonicalHighWater >= 0 {
		value := row.CanonicalHighWater
		highWater = &value
	}
	return ContextManifestRecord{
		ID: row.ID, RunID: row.RunID, CanonicalHighWater: highWater,
		PolicyVersion: int(row.PolicyVersion), ManifestHash: row.ManifestHash, Data: append(json.RawMessage(nil), row.Data...),
		CreatedAt: time.Unix(0, row.CreatedAt).UTC(),
	}
}
