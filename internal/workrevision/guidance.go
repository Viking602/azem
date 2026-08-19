package workrevision

import (
	"context"
	"fmt"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const (
	GuidanceContinue           = "continue"
	GuidanceCancel             = "cancel"
	GuidanceContinueQuarantine = "continue_then_quarantine"
)

type ActiveIntentV1 struct {
	Intent         session.ActionIntentV1
	SourceRevision session.WorkRevisionV1
	CancelSafe     bool
}

type GuidanceDecisionV1 struct {
	Version                  int                   `json:"version"`
	ID                       string                `json:"id"`
	IntentID                 string                `json:"intent_id"`
	SourceRevisionID         string                `json:"source_revision_id"`
	CurrentRevisionID        string                `json:"current_revision_id"`
	Action                   string                `json:"action"`
	Reason                   string                `json:"reason"`
	CancelError              string                `json:"cancel_error,omitempty"`
	PreserveTerminalEvidence bool                  `json:"preserve_terminal_evidence"`
	Sources                  []session.SourceRefV1 `json:"sources"`
	CreatedAt                time.Time             `json:"created_at"`
}

func (d GuidanceDecisionV1) Validate() error {
	if d.Version != 1 || d.ID == "" || d.IntentID == "" || d.SourceRevisionID == "" || d.CurrentRevisionID == "" ||
		(d.Action != GuidanceContinue && d.Action != GuidanceCancel && d.Action != GuidanceContinueQuarantine) ||
		d.Reason == "" || !d.PreserveTerminalEvidence || len(d.Sources) == 0 || d.CreatedAt.IsZero() {
		return fmt.Errorf("work revision: invalid guidance decision")
	}
	for _, source := range d.Sources {
		if err := source.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type CancelIntent func(context.Context, string) error

func HandleGuidance(ctx context.Context, current session.WorkRevisionV1, active []ActiveIntentV1, cancel CancelIntent, now time.Time) ([]GuidanceDecisionV1, error) {
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, fmt.Errorf("work revision: guidance decision time is required")
	}
	decisions := make([]GuidanceDecisionV1, 0, len(active))
	for _, work := range active {
		if err := work.SourceRevision.Validate(); err != nil {
			return nil, err
		}
		if work.Intent.ID == "" || work.Intent.RevisionID != work.SourceRevision.ID || work.Intent.SnapshotHash != work.SourceRevision.SnapshotHash {
			return nil, fmt.Errorf("work revision: active intent is not bound to its source revision")
		}
		action := GuidanceContinue
		reason := "explicit constraints remain compatible"
		cancelError := ""
		if work.SourceRevision.ConstraintDigest != current.ConstraintDigest {
			action = GuidanceContinueQuarantine
			reason = "explicit constraints changed; terminal evidence must be quarantined"
			if work.CancelSafe {
				if cancel == nil {
					cancelError = "cancel callback unavailable"
				} else if err := cancel(ctx, work.Intent.ID); err != nil {
					cancelError = err.Error()
				} else {
					action = GuidanceCancel
					reason = "cancel-safe work became obsolete after explicit constraints changed"
				}
			}
		}
		decision := GuidanceDecisionV1{
			Version: 1, IntentID: work.Intent.ID, SourceRevisionID: work.SourceRevision.ID, CurrentRevisionID: current.ID,
			Action: action, Reason: reason, CancelError: cancelError, PreserveTerminalEvidence: true,
			Sources:   []session.SourceRefV1{{Kind: "action_intent_v1", ID: work.Intent.ID}, {Kind: "work_revision_v1", ID: current.ID}},
			CreatedAt: now.UTC(),
		}
		decision.ID = "guidance-decision:" + digestJSON(struct {
			IntentID          string `json:"intent_id"`
			CurrentRevisionID string `json:"current_revision_id"`
			Action            string `json:"action"`
		}{work.Intent.ID, current.ID, action})[:24]
		if err := decision.Validate(); err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, nil
}
