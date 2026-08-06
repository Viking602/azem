package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
	"github.com/Viking602/venat/api"
)

func (u *unitOfWork) AgentDefinitions() api.AgentDefinitionStore { return u }

func (u *unitOfWork) AdmissionReservations() api.AdmissionReservationStore { return u }

func (u *unitOfWork) ResourceClaims() api.ResourceClaimStore { return u }

func (u *unitOfWork) SaveAgentDefinitionSnapshot(ctx context.Context, snapshot api.AgentDefinitionSnapshot) error {
	definitionID := snapshot.Definition.ID
	version := snapshot.Definition.Version
	if strings.TrimSpace(definitionID) == "" || strings.TrimSpace(version) == "" {
		return fmt.Errorf("agent definition ID and version required: %w", api.ErrInvalidCommand)
	}
	definition, err := json.Marshal(snapshot.Definition)
	if err != nil {
		return fmt.Errorf("marshal agent definition: %w", err)
	}
	sum := sha256.Sum256(definition)
	if snapshot.Digest != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("agent definition digest mismatch: %w", api.ErrInvalidCommand)
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal agent definition snapshot: %w", err)
	}
	result, err := dbgen.New(u.tx).InsertAgentDefinitionSnapshot(ctx, dbgen.InsertAgentDefinitionSnapshotParams{
		DefinitionID: definitionID,
		Version:      version,
		CreatedAt:    nanos(snapshot.CreatedAt),
		Data:         data,
	})
	if err != nil {
		return fmt.Errorf("save agent definition snapshot: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("save agent definition snapshot: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	existing, err := u.LoadAgentDefinitionSnapshot(ctx, definitionID, version)
	if err != nil {
		return err
	}
	existingDefinition, err := json.Marshal(existing.Definition)
	if err != nil {
		return fmt.Errorf("marshal stored agent definition: %w", err)
	}
	if existing.Digest == snapshot.Digest && bytes.Equal(existingDefinition, definition) {
		return nil
	}
	return api.ErrDefinitionVersionConflict
}

func (u *unitOfWork) LoadAgentDefinitionSnapshot(ctx context.Context, definitionID, version string) (api.AgentDefinitionSnapshot, error) {
	data, err := dbgen.New(u.tx).GetAgentDefinitionSnapshotData(ctx, dbgen.GetAgentDefinitionSnapshotDataParams{
		DefinitionID: definitionID,
		Version:      version,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return api.AgentDefinitionSnapshot{}, api.ErrNotFound
	}
	if err != nil {
		return api.AgentDefinitionSnapshot{}, fmt.Errorf("load agent definition snapshot: %w", err)
	}
	return decodeControlRecord[api.AgentDefinitionSnapshot](data, "agent definition snapshot")
}

func (u *unitOfWork) ListAgentDefinitionSnapshots(ctx context.Context, selector api.AgentDefinitionSnapshotSelector) ([]api.AgentDefinitionSnapshot, error) {
	rows, err := dbgen.New(u.tx).ListAgentDefinitionSnapshotData(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agent definition snapshots: %w", err)
	}
	out := make([]api.AgentDefinitionSnapshot, 0, len(rows))
	for _, data := range rows {
		snapshot, err := decodeControlRecord[api.AgentDefinitionSnapshot](data, "agent definition snapshot")
		if err != nil {
			return nil, err
		}
		if len(selector.DefinitionIDs) > 0 && !contains(selector.DefinitionIDs, snapshot.Definition.ID) ||
			len(selector.Versions) > 0 && !contains(selector.Versions, snapshot.Definition.Version) ||
			!selector.Since.IsZero() && snapshot.CreatedAt.Before(selector.Since) {
			continue
		}
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			if out[i].Definition.ID == out[j].Definition.ID {
				return out[i].Definition.Version < out[j].Definition.Version
			}
			return out[i].Definition.ID < out[j].Definition.ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return limit(out, selector.Limit), nil
}

func (u *unitOfWork) PreviewAdmission(ctx context.Context, request api.AdmissionRequest) (api.AdmissionDecision, error) {
	if err := validateAdmissionRequest(request); err != nil {
		return api.AdmissionDecision{}, err
	}
	reservations, err := u.admissionReservationsForAgent(ctx, request.AgentID)
	if err != nil {
		return api.AdmissionDecision{}, err
	}
	return evaluateAdmission(reservations, request, ""), nil
}

func (u *unitOfWork) ReserveAdmission(ctx context.Context, request api.AdmissionRequest) (api.AdmissionDecision, error) {
	if err := validateAdmissionRequest(request); err != nil {
		return api.AdmissionDecision{}, err
	}
	existing, err := u.LoadAdmissionReservation(ctx, request.ReservationID)
	switch {
	case err == nil:
		if admissionRequestMatches(existing, request) {
			return api.AdmissionDecision{Allowed: true, Reservation: existing}, nil
		}
		return api.AdmissionDecision{}, fmt.Errorf("admission reservation %q: %w", request.ReservationID, api.ErrIdempotencyConflict)
	case !errors.Is(err, api.ErrNotFound):
		return api.AdmissionDecision{}, err
	}
	if data, runErr := dbgen.New(u.tx).GetAdmissionReservationDataForRun(ctx, dbgen.GetAdmissionReservationDataForRunParams{
		AgentID: request.AgentID,
		RunID:   request.RunID,
	}); runErr == nil {
		reservation, decodeErr := decodeControlRecord[api.AdmissionReservation](data, "admission reservation")
		if decodeErr != nil {
			return api.AdmissionDecision{}, decodeErr
		}
		return api.AdmissionDecision{}, fmt.Errorf("admission run %q already has reservation %q: %w", request.RunID, reservation.ID, api.ErrIdempotencyConflict)
	} else if !errors.Is(runErr, sql.ErrNoRows) {
		return api.AdmissionDecision{}, fmt.Errorf("load admission reservation for run: %w", runErr)
	}
	reservations, err := u.admissionReservationsForAgent(ctx, request.AgentID)
	if err != nil {
		return api.AdmissionDecision{}, err
	}
	decision := evaluateAdmission(reservations, request, "")
	if !decision.Allowed {
		return decision, nil
	}
	reservation := api.AdmissionReservation{
		ID: request.ReservationID, AgentID: request.AgentID, AgentVersion: request.AgentVersion, RunID: request.RunID,
		State: api.AdmissionReserved, Limits: request.Limits, ReservedCredits: request.ReservedCredits,
		Version: 1, CreatedAt: request.RequestedAt, UpdatedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt,
	}
	data, err := json.Marshal(reservation)
	if err != nil {
		return api.AdmissionDecision{}, fmt.Errorf("marshal admission reservation: %w", err)
	}
	result, err := dbgen.New(u.tx).InsertAdmissionReservation(ctx, dbgen.InsertAdmissionReservationParams{
		ID: reservation.ID, AgentID: reservation.AgentID, RunID: reservation.RunID, State: string(reservation.State),
		Version: 1, CreatedAt: nanos(reservation.CreatedAt), UpdatedAt: nanos(reservation.UpdatedAt),
		ExpiresAt: nanos(reservation.ExpiresAt), Data: data,
	})
	if err != nil {
		return api.AdmissionDecision{}, fmt.Errorf("reserve admission: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return api.AdmissionDecision{}, fmt.Errorf("reserve admission: %w", err)
	}
	if inserted != 1 {
		return api.AdmissionDecision{}, fmt.Errorf("admission reservation %q collided: %w", request.ReservationID, api.ErrIdempotencyConflict)
	}
	decision.Reservation = reservation
	return decision, nil
}

func (u *unitOfWork) TransitionAdmission(ctx context.Context, transition api.AdmissionTransition) (api.AdmissionDecision, error) {
	reservation, err := u.LoadAdmissionReservation(ctx, transition.ReservationID)
	if err != nil {
		return api.AdmissionDecision{}, err
	}
	reservations, err := u.admissionReservationsForAgent(ctx, reservation.AgentID)
	if err != nil {
		return api.AdmissionDecision{}, err
	}
	if reservation.Version != transition.ExpectedVersion {
		return api.AdmissionDecision{
			Reason: api.AdmissionDeniedVersionConflict,
			Usage:  admissionUsage(reservations, reservation.AgentID, reservation.Limits, transition.At, ""),
		}, nil
	}
	if err := validateAdmissionTransition(reservation, transition); err != nil {
		return api.AdmissionDecision{}, err
	}
	usage := admissionUsage(reservations, reservation.AgentID, reservation.Limits, transition.At, reservation.ID)
	if reservation.State == api.AdmissionSuspended && transition.To == api.AdmissionActive &&
		reservation.Limits.MaxConcurrentRuns > 0 && usage.ConcurrentRuns+1 > reservation.Limits.MaxConcurrentRuns {
		return api.AdmissionDecision{Reason: api.AdmissionDeniedConcurrency, Usage: usage}, nil
	}
	previousVersion := reservation.Version
	reservation.State = transition.To
	reservation.Version++
	reservation.UpdatedAt = transition.At
	if !transition.ExpiresAt.IsZero() {
		reservation.ExpiresAt = transition.ExpiresAt
	}
	switch transition.To {
	case api.AdmissionActive:
		if reservation.ActivatedAt.IsZero() {
			reservation.ActivatedAt = transition.At
		}
	case api.AdmissionSettled:
		reservation.ConsumedCredits = transition.ConsumedCredits
		reservation.Failed = transition.Failed
		reservation.SettledAt = transition.At
	}
	data, err := json.Marshal(reservation)
	if err != nil {
		return api.AdmissionDecision{}, fmt.Errorf("marshal admission reservation: %w", err)
	}
	version, err := int64FromUint64(reservation.Version)
	if err != nil {
		return api.AdmissionDecision{}, err
	}
	expected, err := int64FromUint64(previousVersion)
	if err != nil {
		return api.AdmissionDecision{}, err
	}
	result, err := dbgen.New(u.tx).UpdateAdmissionReservationCAS(ctx, dbgen.UpdateAdmissionReservationCASParams{
		NextState: string(reservation.State), NextVersion: version, UpdatedAt: nanos(reservation.UpdatedAt),
		ExpiresAt: nanos(reservation.ExpiresAt), Data: data, ID: reservation.ID, ExpectedVersion: expected,
	})
	if err != nil {
		return api.AdmissionDecision{}, fmt.Errorf("transition admission: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return api.AdmissionDecision{}, fmt.Errorf("transition admission: %w", err)
	}
	if updated != 1 {
		return api.AdmissionDecision{Reason: api.AdmissionDeniedVersionConflict, Usage: usage}, nil
	}
	return api.AdmissionDecision{Allowed: true, Usage: usage, Reservation: reservation}, nil
}

func (u *unitOfWork) LoadAdmissionReservation(ctx context.Context, id string) (api.AdmissionReservation, error) {
	data, err := dbgen.New(u.tx).GetAdmissionReservationData(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return api.AdmissionReservation{}, api.ErrNotFound
	}
	if err != nil {
		return api.AdmissionReservation{}, fmt.Errorf("load admission reservation: %w", err)
	}
	return decodeControlRecord[api.AdmissionReservation](data, "admission reservation")
}

func (u *unitOfWork) ListAdmissionReservations(ctx context.Context, selector api.AdmissionReservationSelector) ([]api.AdmissionReservation, error) {
	rows, err := dbgen.New(u.tx).ListAdmissionReservationData(ctx)
	if err != nil {
		return nil, fmt.Errorf("list admission reservations: %w", err)
	}
	out := make([]api.AdmissionReservation, 0, len(rows))
	for _, data := range rows {
		reservation, err := decodeControlRecord[api.AdmissionReservation](data, "admission reservation")
		if err != nil {
			return nil, err
		}
		if !matchesAdmissionSelector(reservation, selector) {
			continue
		}
		out = append(out, reservation)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return limit(out, selector.Limit), nil
}

func (u *unitOfWork) admissionReservationsForAgent(ctx context.Context, agentID string) ([]api.AdmissionReservation, error) {
	rows, err := dbgen.New(u.tx).ListAdmissionReservationDataByAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list admission reservations: %w", err)
	}
	out := make([]api.AdmissionReservation, 0, len(rows))
	for _, data := range rows {
		reservation, err := decodeControlRecord[api.AdmissionReservation](data, "admission reservation")
		if err != nil {
			return nil, err
		}
		out = append(out, reservation)
	}
	return out, nil
}

func validateAdmissionRequest(request api.AdmissionRequest) error {
	if request.ReservationID == "" || request.AgentID == "" || request.RunID == "" {
		return fmt.Errorf("admission reservation, agent, and run ids are required: %w", api.ErrInvalidCommand)
	}
	if request.RequestedAt.IsZero() || !request.ExpiresAt.After(request.RequestedAt) {
		return fmt.Errorf("admission timestamps are invalid: %w", api.ErrInvalidCommand)
	}
	if request.ReservedCredits < 0 || request.Limits.MaxConcurrentRuns < 0 || request.Limits.MaxRunsPerWindow < 0 ||
		request.Limits.MaxCredits < 0 || request.Limits.PauseOnExcessFailures < 0 {
		return fmt.Errorf("admission limits cannot be negative: %w", api.ErrInvalidCommand)
	}
	usesWindow := request.Limits.MaxRunsPerWindow > 0 || request.Limits.MaxCredits > 0 || request.Limits.PauseOnExcessFailures > 0
	if usesWindow && request.Limits.Window <= 0 {
		return fmt.Errorf("admission window is required by aggregate limits: %w", api.ErrInvalidCommand)
	}
	return nil
}

func validateAdmissionTransition(reservation api.AdmissionReservation, transition api.AdmissionTransition) error {
	if transition.At.IsZero() || transition.At.Before(reservation.UpdatedAt) {
		return fmt.Errorf("admission transition timestamp is invalid: %w", api.ErrInvalidTransition)
	}
	if transition.ConsumedCredits < 0 {
		return fmt.Errorf("admission consumed credits cannot be negative: %w", api.ErrInvalidTransition)
	}
	if !transition.ExpiresAt.IsZero() && !transition.ExpiresAt.After(transition.At) {
		return fmt.Errorf("admission transition expiry is invalid: %w", api.ErrInvalidTransition)
	}
	if !validAdmissionTransition(reservation.State, transition.To) {
		return fmt.Errorf("admission transition %q to %q: %w", reservation.State, transition.To, api.ErrInvalidTransition)
	}
	if !reservation.ExpiresAt.After(transition.At) && transition.To != api.AdmissionExpired && transition.To != api.AdmissionSettled {
		return fmt.Errorf("admission reservation has expired: %w", api.ErrInvalidTransition)
	}
	if transition.To != api.AdmissionSettled && (transition.ConsumedCredits != 0 || transition.Failed) {
		return fmt.Errorf("admission outcome is only valid when settling: %w", api.ErrInvalidTransition)
	}
	return nil
}

func validAdmissionTransition(from, to api.AdmissionState) bool {
	switch from {
	case api.AdmissionReserved:
		return to == api.AdmissionActive || to == api.AdmissionReleased || to == api.AdmissionExpired
	case api.AdmissionActive:
		return to == api.AdmissionSuspended || to == api.AdmissionSettled || to == api.AdmissionReleased || to == api.AdmissionExpired
	case api.AdmissionSuspended:
		return to == api.AdmissionActive || to == api.AdmissionSettled || to == api.AdmissionReleased || to == api.AdmissionExpired
	default:
		return false
	}
}

func evaluateAdmission(reservations []api.AdmissionReservation, request api.AdmissionRequest, excludeID string) api.AdmissionDecision {
	usage := admissionUsage(reservations, request.AgentID, request.Limits, request.RequestedAt, excludeID)
	switch {
	case request.Limits.MaxConcurrentRuns > 0 && usage.ConcurrentRuns+1 > request.Limits.MaxConcurrentRuns:
		return api.AdmissionDecision{Reason: api.AdmissionDeniedConcurrency, Usage: usage}
	case request.Limits.MaxRunsPerWindow > 0 && usage.RunsInWindow+1 > request.Limits.MaxRunsPerWindow:
		return api.AdmissionDecision{Reason: api.AdmissionDeniedRunWindow, Usage: usage}
	case request.Limits.MaxCredits > 0 && usage.CommittedCredits+usage.ReservedCredits+request.ReservedCredits > request.Limits.MaxCredits:
		return api.AdmissionDecision{Reason: api.AdmissionDeniedCredits, Usage: usage}
	case request.Limits.PauseOnExcessFailures > 0 && usage.TrailingFailures >= request.Limits.PauseOnExcessFailures:
		return api.AdmissionDecision{Reason: api.AdmissionDeniedFailureBreaker, Usage: usage}
	default:
		return api.AdmissionDecision{Allowed: true, Usage: usage}
	}
}

func admissionUsage(reservations []api.AdmissionReservation, agentID string, limits api.AdmissionLimits, now time.Time, excludeID string) api.AdmissionUsage {
	usage := api.AdmissionUsage{}
	windowStart := now.Add(-limits.Window)
	settled := make([]api.AdmissionReservation, 0)
	for _, reservation := range reservations {
		if reservation.ID == excludeID || reservation.AgentID != agentID || reservation.State == api.AdmissionReleased || reservation.State == api.AdmissionExpired {
			continue
		}
		expired := !reservation.ExpiresAt.After(now) && reservation.State != api.AdmissionSettled
		if !expired && (reservation.State == api.AdmissionReserved || reservation.State == api.AdmissionActive) {
			usage.ConcurrentRuns++
		}
		if limits.Window <= 0 || reservation.CreatedAt.Before(windowStart) {
			continue
		}
		usage.RunsInWindow++
		switch reservation.State {
		case api.AdmissionReserved, api.AdmissionActive, api.AdmissionSuspended:
			if !expired {
				usage.ReservedCredits += reservation.ReservedCredits
			}
		case api.AdmissionSettled:
			usage.CommittedCredits += reservation.ConsumedCredits
			settled = append(settled, reservation)
		}
	}
	sort.Slice(settled, func(i, j int) bool {
		if settled[i].SettledAt.Equal(settled[j].SettledAt) {
			return settled[i].ID > settled[j].ID
		}
		return settled[i].SettledAt.After(settled[j].SettledAt)
	})
	for _, reservation := range settled {
		if !reservation.Failed {
			break
		}
		usage.TrailingFailures++
	}
	return usage
}

func admissionRequestMatches(reservation api.AdmissionReservation, request api.AdmissionRequest) bool {
	return reservation.ID == request.ReservationID && reservation.AgentID == request.AgentID &&
		reservation.AgentVersion == request.AgentVersion && reservation.RunID == request.RunID &&
		reservation.Limits == request.Limits && reservation.ReservedCredits == request.ReservedCredits
}

func matchesAdmissionSelector(reservation api.AdmissionReservation, selector api.AdmissionReservationSelector) bool {
	return (len(selector.AgentIDs) == 0 || contains(selector.AgentIDs, reservation.AgentID)) &&
		(len(selector.RunIDs) == 0 || contains(selector.RunIDs, reservation.RunID)) &&
		(len(selector.States) == 0 || contains(selector.States, reservation.State)) &&
		(selector.Since.IsZero() || !reservation.CreatedAt.Before(selector.Since)) &&
		(selector.ExpiresBefore.IsZero() || !reservation.ExpiresAt.After(selector.ExpiresBefore))
}

func (u *unitOfWork) AcquireResourceClaims(ctx context.Context, request api.ResourceClaimRequest) (api.ResourceClaimDecision, error) {
	if err := validateResourceClaimRequest(request); err != nil {
		return api.ResourceClaimDecision{}, err
	}
	requestedIDs := make(map[string]struct{}, len(request.Claims))
	claims := make([]api.ResourceClaim, 0, len(request.Claims))
	newClaims := make([]api.ResourceClaim, 0, len(request.Claims))
	for _, spec := range request.Claims {
		requestedIDs[spec.ID] = struct{}{}
		existing, err := u.LoadResourceClaim(ctx, spec.ID)
		if err == nil {
			if !resourceClaimRequestMatches(existing, request, spec) {
				return api.ResourceClaimDecision{}, fmt.Errorf("resource claim %q: %w", spec.ID, api.ErrIdempotencyConflict)
			}
			claims = append(claims, existing)
			continue
		}
		if !errors.Is(err, api.ErrNotFound) {
			return api.ResourceClaimDecision{}, err
		}
		claim := api.ResourceClaim{
			ID: spec.ID, Key: spec.Key, Mode: spec.Mode,
			RunID: request.RunID, TaskID: request.TaskID, LeaseID: request.LeaseID, HolderID: request.HolderID,
			State: api.ResourceClaimActive, Version: 1,
			CreatedAt: request.RequestedAt, UpdatedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt,
		}
		claims = append(claims, claim)
		newClaims = append(newClaims, claim)
	}

	conflicts := make([]api.ResourceClaim, 0)
	queries := dbgen.New(u.tx)
	for _, spec := range request.Claims {
		rows, err := queries.ListActiveResourceClaimDataByKey(ctx, dbgen.ListActiveResourceClaimDataByKeyParams{
			ResourceKey: spec.Key,
			ExpiresAt:   nanos(request.RequestedAt),
		})
		if err != nil {
			return api.ResourceClaimDecision{}, fmt.Errorf("list active resource claims: %w", err)
		}
		for _, data := range rows {
			existing, err := decodeControlRecord[api.ResourceClaim](data, "resource claim")
			if err != nil {
				return api.ResourceClaimDecision{}, err
			}
			if _, ownRequest := requestedIDs[existing.ID]; ownRequest {
				continue
			}
			if existing.Mode == api.ResourceClaimExclusive || spec.Mode == api.ResourceClaimExclusive {
				conflicts = append(conflicts, existing)
			}
		}
	}
	if len(conflicts) > 0 {
		sortResourceClaims(conflicts)
		return api.ResourceClaimDecision{Reason: api.ResourceClaimDeniedConflict, Conflicts: conflicts}, nil
	}
	for _, claim := range newClaims {
		data, err := json.Marshal(claim)
		if err != nil {
			return api.ResourceClaimDecision{}, fmt.Errorf("marshal resource claim: %w", err)
		}
		result, err := queries.InsertResourceClaim(ctx, dbgen.InsertResourceClaimParams{
			ID: claim.ID, ResourceKey: claim.Key, RunID: claim.RunID, TaskID: claim.TaskID,
			LeaseID: claim.LeaseID, HolderID: claim.HolderID, Mode: string(claim.Mode), State: string(claim.State),
			Version: 1, CreatedAt: nanos(claim.CreatedAt), UpdatedAt: nanos(claim.UpdatedAt), ExpiresAt: nanos(claim.ExpiresAt), Data: data,
		})
		if err != nil {
			return api.ResourceClaimDecision{}, fmt.Errorf("acquire resource claim: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return api.ResourceClaimDecision{}, fmt.Errorf("acquire resource claim: %w", err)
		}
		if inserted != 1 {
			return api.ResourceClaimDecision{}, fmt.Errorf("resource claim %q collided: %w", claim.ID, api.ErrIdempotencyConflict)
		}
	}
	return api.ResourceClaimDecision{Acquired: true, Claims: claims}, nil
}

func (u *unitOfWork) TransitionResourceClaims(ctx context.Context, request api.ResourceClaimTransitionRequest) (api.ResourceClaimDecision, error) {
	if len(request.Transitions) == 0 {
		return api.ResourceClaimDecision{}, fmt.Errorf("resource claim transitions are required: %w", api.ErrInvalidCommand)
	}
	seen := make(map[string]struct{}, len(request.Transitions))
	claims := make([]api.ResourceClaim, len(request.Transitions))
	versionConflicts := make([]api.ResourceClaim, 0)
	for index, transition := range request.Transitions {
		if strings.TrimSpace(transition.ClaimID) == "" {
			return api.ResourceClaimDecision{}, fmt.Errorf("resource claim ID is required: %w", api.ErrInvalidCommand)
		}
		if _, duplicate := seen[transition.ClaimID]; duplicate {
			return api.ResourceClaimDecision{}, fmt.Errorf("duplicate resource claim transition %q: %w", transition.ClaimID, api.ErrInvalidCommand)
		}
		seen[transition.ClaimID] = struct{}{}
		claim, err := u.LoadResourceClaim(ctx, transition.ClaimID)
		if err != nil {
			return api.ResourceClaimDecision{}, err
		}
		claims[index] = claim
		if claim.Version != transition.ExpectedVersion {
			versionConflicts = append(versionConflicts, claim)
			continue
		}
		if err := validateResourceClaimTransition(claim, transition); err != nil {
			return api.ResourceClaimDecision{}, err
		}
	}
	if len(versionConflicts) > 0 {
		sortResourceClaims(versionConflicts)
		return api.ResourceClaimDecision{Reason: api.ResourceClaimDeniedVersionConflict, Conflicts: versionConflicts}, nil
	}
	queries := dbgen.New(u.tx)
	transitionByID := make(map[string]api.ResourceClaimTransition, len(request.Transitions))
	for _, transition := range request.Transitions {
		transitionByID[transition.ClaimID] = transition
	}
	conflicts := make([]api.ResourceClaim, 0)
	conflictIDs := make(map[string]struct{})
	for _, transition := range request.Transitions {
		if transition.To != api.ResourceClaimActive {
			continue
		}
		requested := claims[0]
		for index, candidate := range claims {
			if request.Transitions[index].ClaimID == transition.ClaimID {
				requested = candidate
				break
			}
		}
		rows, err := queries.ListActiveResourceClaimDataByKey(ctx, dbgen.ListActiveResourceClaimDataByKeyParams{
			ResourceKey: requested.Key,
			ExpiresAt:   nanos(transition.At),
		})
		if err != nil {
			return api.ResourceClaimDecision{}, fmt.Errorf("check resource claim renewal conflicts: %w", err)
		}
		for _, data := range rows {
			existing, err := decodeControlRecord[api.ResourceClaim](data, "resource claim")
			if err != nil {
				return api.ResourceClaimDecision{}, err
			}
			if candidateTransition, transitioning := transitionByID[existing.ID]; transitioning && candidateTransition.To != api.ResourceClaimActive {
				continue
			}
			if existing.ID == transition.ClaimID ||
				(existing.Mode != api.ResourceClaimExclusive && requested.Mode != api.ResourceClaimExclusive) {
				continue
			}
			if _, duplicate := conflictIDs[existing.ID]; duplicate {
				continue
			}
			conflictIDs[existing.ID] = struct{}{}
			conflicts = append(conflicts, existing)
		}
	}
	if len(conflicts) > 0 {
		sortResourceClaims(conflicts)
		return api.ResourceClaimDecision{Reason: api.ResourceClaimDeniedConflict, Conflicts: conflicts}, nil
	}
	for index, transition := range request.Transitions {
		claim := claims[index]
		previousVersion := claim.Version
		claim.State = transition.To
		claim.Version++
		claim.UpdatedAt = transition.At
		if transition.To == api.ResourceClaimActive {
			claim.ExpiresAt = transition.ExpiresAt
		}
		data, err := json.Marshal(claim)
		if err != nil {
			return api.ResourceClaimDecision{}, fmt.Errorf("marshal resource claim: %w", err)
		}
		version, err := int64FromUint64(claim.Version)
		if err != nil {
			return api.ResourceClaimDecision{}, err
		}
		expected, err := int64FromUint64(previousVersion)
		if err != nil {
			return api.ResourceClaimDecision{}, err
		}
		result, err := queries.UpdateResourceClaimCAS(ctx, dbgen.UpdateResourceClaimCASParams{
			NextState: string(claim.State), NextVersion: version, UpdatedAt: nanos(claim.UpdatedAt),
			ExpiresAt: nanos(claim.ExpiresAt), Data: data, ID: claim.ID, ExpectedVersion: expected,
		})
		if err != nil {
			return api.ResourceClaimDecision{}, fmt.Errorf("transition resource claim: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return api.ResourceClaimDecision{}, fmt.Errorf("transition resource claim: %w", err)
		}
		if updated != 1 {
			return api.ResourceClaimDecision{}, fmt.Errorf("resource claim %q changed during batch transition: %w", claim.ID, api.ErrIdempotencyConflict)
		}
		claims[index] = claim
	}
	return api.ResourceClaimDecision{Acquired: true, Claims: claims}, nil
}

func (u *unitOfWork) LoadResourceClaim(ctx context.Context, id string) (api.ResourceClaim, error) {
	data, err := dbgen.New(u.tx).GetResourceClaimData(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return api.ResourceClaim{}, api.ErrNotFound
	}
	if err != nil {
		return api.ResourceClaim{}, fmt.Errorf("load resource claim: %w", err)
	}
	return decodeControlRecord[api.ResourceClaim](data, "resource claim")
}

func (u *unitOfWork) ListResourceClaims(ctx context.Context, selector api.ResourceClaimSelector) ([]api.ResourceClaim, error) {
	data, err := dbgen.New(u.tx).ListResourceClaimData(ctx)
	if err != nil {
		return nil, fmt.Errorf("list resource claims: %w", err)
	}
	claims := make([]api.ResourceClaim, 0, len(data))
	for _, raw := range data {
		claim, err := decodeControlRecord[api.ResourceClaim](raw, "resource claim")
		if err != nil {
			return nil, err
		}
		if matchesResourceClaimSelector(claim, selector) {
			claims = append(claims, claim)
			if selector.Limit > 0 && len(claims) == selector.Limit {
				break
			}
		}
	}
	return claims, nil
}

func validateResourceClaimTransition(claim api.ResourceClaim, transition api.ResourceClaimTransition) error {
	if transition.At.IsZero() || transition.At.Before(claim.UpdatedAt) {
		return fmt.Errorf("resource claim transition timestamp is invalid: %w", api.ErrInvalidTransition)
	}
	if claim.State != api.ResourceClaimActive {
		return fmt.Errorf("resource claim %q is terminal: %w", claim.ID, api.ErrInvalidTransition)
	}
	switch transition.To {
	case api.ResourceClaimActive:
		if !claim.ExpiresAt.After(transition.At) {
			return fmt.Errorf("resource claim %q has expired: %w", claim.ID, api.ErrInvalidTransition)
		}
		if !transition.ExpiresAt.After(transition.At) {
			return fmt.Errorf("resource claim renewal expiry is invalid: %w", api.ErrInvalidTransition)
		}
	case api.ResourceClaimReleased, api.ResourceClaimExpired:
		if !transition.ExpiresAt.IsZero() {
			return fmt.Errorf("terminal resource claim transition cannot set expiry: %w", api.ErrInvalidTransition)
		}
	default:
		return fmt.Errorf("resource claim transition to %q: %w", transition.To, api.ErrInvalidTransition)
	}
	return nil
}

func validateResourceClaimRequest(request api.ResourceClaimRequest) error {
	if request.RunID == "" || request.TaskID == "" || request.LeaseID == "" || request.HolderID == "" {
		return fmt.Errorf("resource claim run, task, lease, and holder IDs are required: %w", api.ErrInvalidCommand)
	}
	if request.RequestedAt.IsZero() || !request.ExpiresAt.After(request.RequestedAt) {
		return fmt.Errorf("resource claim timestamps are invalid: %w", api.ErrInvalidCommand)
	}
	if len(request.Claims) == 0 {
		return fmt.Errorf("resource claims are required: %w", api.ErrInvalidCommand)
	}
	ids := make(map[string]struct{}, len(request.Claims))
	keys := make(map[string]struct{}, len(request.Claims))
	for _, claim := range request.Claims {
		if strings.TrimSpace(claim.ID) == "" || strings.TrimSpace(claim.Key) == "" {
			return fmt.Errorf("resource claim ID and key are required: %w", api.ErrInvalidCommand)
		}
		if claim.Mode != api.ResourceClaimShared && claim.Mode != api.ResourceClaimExclusive {
			return fmt.Errorf("resource claim %q has invalid mode %q: %w", claim.ID, claim.Mode, api.ErrInvalidCommand)
		}
		if _, duplicate := ids[claim.ID]; duplicate {
			return fmt.Errorf("duplicate resource claim ID %q: %w", claim.ID, api.ErrInvalidCommand)
		}
		ids[claim.ID] = struct{}{}
		if _, duplicate := keys[claim.Key]; duplicate {
			return fmt.Errorf("duplicate resource claim key %q: %w", claim.Key, api.ErrInvalidCommand)
		}
		keys[claim.Key] = struct{}{}
	}
	return nil
}

func resourceClaimRequestMatches(claim api.ResourceClaim, request api.ResourceClaimRequest, spec api.ResourceClaimSpec) bool {
	return claim.ID == spec.ID && claim.Key == spec.Key && claim.Mode == spec.Mode &&
		claim.RunID == request.RunID && claim.TaskID == request.TaskID && claim.LeaseID == request.LeaseID &&
		claim.HolderID == request.HolderID && claim.State == api.ResourceClaimActive &&
		claim.ExpiresAt.After(request.RequestedAt) &&
		claim.ExpiresAt.Sub(claim.CreatedAt) == request.ExpiresAt.Sub(request.RequestedAt)
}

func matchesResourceClaimSelector(claim api.ResourceClaim, selector api.ResourceClaimSelector) bool {
	return (len(selector.IDs) == 0 || contains(selector.IDs, claim.ID)) &&
		(len(selector.Keys) == 0 || contains(selector.Keys, claim.Key)) &&
		(len(selector.RunIDs) == 0 || contains(selector.RunIDs, claim.RunID)) &&
		(len(selector.TaskIDs) == 0 || contains(selector.TaskIDs, claim.TaskID)) &&
		(len(selector.LeaseIDs) == 0 || contains(selector.LeaseIDs, claim.LeaseID)) &&
		(len(selector.HolderIDs) == 0 || contains(selector.HolderIDs, claim.HolderID)) &&
		(len(selector.Modes) == 0 || contains(selector.Modes, claim.Mode)) &&
		(len(selector.States) == 0 || contains(selector.States, claim.State)) &&
		(selector.ExpiresBefore.IsZero() || !claim.ExpiresAt.After(selector.ExpiresBefore))
}

func sortResourceClaims(claims []api.ResourceClaim) {
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].CreatedAt.Equal(claims[j].CreatedAt) {
			return claims[i].ID < claims[j].ID
		}
		return claims[i].CreatedAt.Before(claims[j].CreatedAt)
	})
}

func decodeControlRecord[T any](data []byte, kind string) (T, error) {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", kind, err)
	}
	return value, nil
}

var (
	_ api.AgentDefinitionStore           = (*unitOfWork)(nil)
	_ api.AdmissionReservationStore      = (*unitOfWork)(nil)
	_ api.AdmissionReservationUnitOfWork = (*unitOfWork)(nil)
	_ api.ResourceClaimStore             = (*unitOfWork)(nil)
	_ api.ResourceClaimUnitOfWork        = (*unitOfWork)(nil)
)
