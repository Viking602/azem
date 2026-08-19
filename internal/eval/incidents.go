package eval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Viking602/azem/internal/session"
)

const (
	IncidentUserAmbiguity     = "user_ambiguity"
	IncidentUserInconsistency = "user_inconsistency"
	IncidentUserRedundancy    = "user_redundancy"
	IncidentUserDrift         = "user_drift"
	IncidentUserBoundaryProbe = "user_boundary_probe"
	IncidentToolFailure       = "tool_execution_failure"
	IncidentToolIncomplete    = "tool_incomplete"
	IncidentToolErroneous     = "tool_erroneous"
	IncidentToolMisleading    = "tool_misleading"
	IncidentToolRedundant     = "tool_redundant"
)

type IncidentLabelV1 struct {
	Version   int                   `json:"version"`
	ID        string                `json:"id"`
	Category  string                `json:"category"`
	Actor     string                `json:"actor"`
	SubjectID string                `json:"subject_id"`
	Reason    string                `json:"reason"`
	Evidence  []session.SourceRefV1 `json:"evidence"`
}

type UserIncidentSignalV1 struct {
	ID                 string
	Source             session.SourceRefV1
	InterpretationIDs  []string
	Contradictions     []session.SourceRefV1
	DuplicateOf        *session.SourceRefV1
	ExpectedWorkSpecID string
	ObservedWorkSpecID string
	BoundaryProbe      bool
	BoundaryEvidence   []session.SourceRefV1
}

type ToolIncidentSignalV1 struct {
	Observation       session.ObservationEnvelopeV1
	ValidatorStatus   string // pass, fail, uncertain, or unknown
	ValidatorEvidence []session.SourceRefV1
	ClaimedSuccess    bool
	DuplicateOf       *session.SourceRefV1
}

type IncidentSignalsV1 struct {
	Users []UserIncidentSignalV1
	Tools []ToolIncidentSignalV1
}

// LabelTrajectoryIncidents applies deterministic labels only when the caller
// supplies the fact required by that label. It does not infer intent from prose
// and does not rewrite exported raw rows.
func LabelTrajectoryIncidents(trajectory *TrajectoryV1, signals IncidentSignalsV1) error {
	if trajectory == nil || trajectory.Version != TrajectoryVersionV1 || trajectory.SessionID == "" {
		return fmt.Errorf("eval: invalid trajectory for incident labels")
	}
	labels := make([]IncidentLabelV1, 0)
	seen := make(map[string]struct{})
	appendLabel := func(category, actor, subject, reason string, evidence []session.SourceRefV1) error {
		if subject == "" || len(evidence) == 0 {
			return fmt.Errorf("eval: %s incident requires subject and evidence", category)
		}
		id := category + ":" + subject
		if _, exists := seen[id]; exists {
			return nil
		}
		seen[id] = struct{}{}
		labels = append(labels, IncidentLabelV1{
			Version: session.WorkContractVersionV1, ID: id, Category: category,
			Actor: actor, SubjectID: subject, Reason: reason,
			Evidence: append([]session.SourceRefV1(nil), evidence...),
		})
		return nil
	}
	for _, signal := range signals.Users {
		if signal.ID == "" || signal.Source.Kind == "" || signal.Source.ID == "" {
			return fmt.Errorf("eval: user incident signal requires identity and source")
		}
		base := []session.SourceRefV1{signal.Source}
		if len(signal.InterpretationIDs) > 1 {
			if err := appendLabel(IncidentUserAmbiguity, "user", signal.ID, "multiple unresolved interpretations were recorded", base); err != nil {
				return err
			}
		}
		if len(signal.Contradictions) > 0 {
			evidence := append(append([]session.SourceRefV1(nil), base...), signal.Contradictions...)
			if err := appendLabel(IncidentUserInconsistency, "user", signal.ID, "guidance contradicts recorded evidence", evidence); err != nil {
				return err
			}
		}
		if signal.DuplicateOf != nil {
			if err := appendLabel(IncidentUserRedundancy, "user", signal.ID, "guidance duplicates an earlier turn", append(base, *signal.DuplicateOf)); err != nil {
				return err
			}
		}
		if signal.ExpectedWorkSpecID != "" && signal.ObservedWorkSpecID != "" && signal.ExpectedWorkSpecID != signal.ObservedWorkSpecID {
			if err := appendLabel(IncidentUserDrift, "user", signal.ID, "guidance targets a different work specification", base); err != nil {
				return err
			}
		}
		if signal.BoundaryProbe {
			evidence := append(append([]session.SourceRefV1(nil), base...), signal.BoundaryEvidence...)
			if err := appendLabel(IncidentUserBoundaryProbe, "user", signal.ID, "guidance probes a declared side-effect boundary", evidence); err != nil {
				return err
			}
		}
	}
	for _, signal := range signals.Tools {
		observation := signal.Observation
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("eval: invalid tool incident observation: %w", err)
		}
		subject := firstNonempty(observation.ToolCallID, observation.ID)
		evidence := append([]session.SourceRefV1(nil), observation.Sources...)
		if len(evidence) == 0 && observation.RawRef != nil {
			evidence = append(evidence, *observation.RawRef)
		}
		if observation.Status == "failed" || observation.Status == "timeout" || observation.Status == "cancelled" {
			if err := appendLabel(IncidentToolFailure, "tool", subject, "host recorded a non-success terminal state", evidence); err != nil {
				return err
			}
		}
		if observation.ContentPresence == "missing" || observation.Truncation == "truncated" {
			if err := appendLabel(IncidentToolIncomplete, "tool", subject, "raw result is missing or truncated", evidence); err != nil {
				return err
			}
		}
		validatorEvidence := append(append([]session.SourceRefV1(nil), evidence...), signal.ValidatorEvidence...)
		if observation.Status == "completed" && signal.ValidatorStatus == "fail" {
			if err := appendLabel(IncidentToolErroneous, "tool", subject, "deterministic validator rejected a completed result", validatorEvidence); err != nil {
				return err
			}
		}
		if signal.ClaimedSuccess && signal.ValidatorStatus == "fail" {
			if err := appendLabel(IncidentToolMisleading, "tool", subject, "reported success conflicts with validator evidence", validatorEvidence); err != nil {
				return err
			}
		}
		if signal.DuplicateOf != nil {
			if err := appendLabel(IncidentToolRedundant, "tool", subject, "equivalent completed work already exists", append(evidence, *signal.DuplicateOf)); err != nil {
				return err
			}
		}
	}
	sort.Slice(labels, func(i, j int) bool {
		return strings.Compare(labels[i].ID, labels[j].ID) < 0
	})
	trajectory.Incidents = labels
	return nil
}
