package workrevision

import (
	"fmt"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const (
	DispositionAccepted         = "accepted"
	DispositionQuarantinedStale = "quarantined_stale"
)

type CompatibilityInput struct {
	IntentID    string
	Source      session.WorkRevisionV1
	Current     session.WorkRevisionV1
	ResultFiles []session.FileObservationV1
	CompletedAt time.Time
}

type DispositionV1 struct {
	Version           int                         `json:"version"`
	ID                string                      `json:"id"`
	IntentID          string                      `json:"intent_id"`
	SourceRevisionID  string                      `json:"source_revision_id"`
	CurrentRevisionID string                      `json:"current_revision_id"`
	Status            string                      `json:"status"`
	Reason            string                      `json:"reason"`
	ResultFiles       []session.FileObservationV1 `json:"result_files,omitempty"`
	CompletedAt       time.Time                   `json:"completed_at"`
}

func (d DispositionV1) Validate() error {
	if d.Version != 1 || d.ID == "" || d.IntentID == "" || d.SourceRevisionID == "" || d.CurrentRevisionID == "" ||
		(d.Status != DispositionAccepted && d.Status != DispositionQuarantinedStale) || d.Reason == "" || d.CompletedAt.IsZero() {
		return fmt.Errorf("work revision: invalid disposition")
	}
	for _, file := range d.ResultFiles {
		if file.Path == "" || file.State == "" {
			return fmt.Errorf("work revision: invalid disposition result file")
		}
	}
	return nil
}

func (d DispositionV1) Accepted() bool { return d.Status == DispositionAccepted }

func (d DispositionV1) CanSatisfyDependency() bool { return d.Accepted() }

func (d DispositionV1) CanSatisfyVerification() bool { return d.Accepted() }

func Disposition(input CompatibilityInput) (DispositionV1, error) {
	if input.IntentID == "" || input.CompletedAt.IsZero() {
		return DispositionV1{}, fmt.Errorf("work revision: disposition requires intent and completion time")
	}
	if err := input.Source.Validate(); err != nil {
		return DispositionV1{}, err
	}
	if err := input.Current.Validate(); err != nil {
		return DispositionV1{}, err
	}
	if input.Source.SessionID != input.Current.SessionID {
		return DispositionV1{}, fmt.Errorf("work revision: cannot compare different sessions")
	}
	status, reason := compatibilityStatus(input.Source, input.Current, input.ResultFiles)
	disposition := DispositionV1{
		Version: 1, IntentID: input.IntentID, SourceRevisionID: input.Source.ID, CurrentRevisionID: input.Current.ID,
		Status: status, Reason: reason, ResultFiles: append([]session.FileObservationV1(nil), input.ResultFiles...), CompletedAt: input.CompletedAt.UTC(),
	}
	disposition.ID = "work-disposition:" + digestJSON(struct {
		IntentID          string `json:"intent_id"`
		SourceRevisionID  string `json:"source_revision_id"`
		CurrentRevisionID string `json:"current_revision_id"`
		Status            string `json:"status"`
	}{input.IntentID, input.Source.ID, input.Current.ID, status})[:24]
	return disposition, nil
}

func compatibilityStatus(source, current session.WorkRevisionV1, resultFiles []session.FileObservationV1) (string, string) {
	if source.ID == current.ID {
		return DispositionAccepted, "exact work revision"
	}
	if source.ConstraintDigest != current.ConstraintDigest {
		return DispositionQuarantinedStale, "explicit constraints changed"
	}
	currentFiles := make(map[string]session.WorkRevisionFileV1, len(current.Files))
	for _, file := range current.Files {
		currentFiles[file.Path] = file
	}
	results := make(map[string]session.FileObservationV1, len(resultFiles))
	for _, file := range resultFiles {
		results[file.Path] = file
	}
	for _, before := range source.Files {
		after, exists := currentFiles[before.Path]
		if !exists {
			return DispositionQuarantinedStale, fmt.Sprintf("observed file %s is absent from current revision", before.Path)
		}
		if before.SHA256 == after.SHA256 {
			continue
		}
		result, explained := results[before.Path]
		if !explained || result.BeforeSHA256 != before.SHA256 || result.AfterSHA256 != after.SHA256 {
			return DispositionQuarantinedStale, fmt.Sprintf("observed file %s changed outside terminal evidence", before.Path)
		}
	}
	return DispositionAccepted, "explicit constraints and source-observed files remain compatible"
}
