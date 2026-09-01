package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

func (u *unitOfWork) AdmissionReservations() agentruntime.AdmissionReservationStore { return u }

func (u *unitOfWork) ResourceClaims() agentruntime.ResourceClaimStore { return u }

func (u *unitOfWork) PreviewAdmission(ctx context.Context, request agentruntime.AdmissionRequest) (agentruntime.AdmissionDecision, error) {
	if err := validateAdmissionRequest(request); err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	reservations, err := u.admissionReservationsForAgent(ctx, request.AgentID)
	if err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	return evaluateAdmission(reservations, request, ""), nil
}

func (u *unitOfWork) ReserveAdmission(ctx context.Context, request agentruntime.AdmissionRequest) (agentruntime.AdmissionDecision, error) {
	if err := validateAdmissionRequest(request); err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	existing, err := u.LoadAdmissionReservation(ctx, request.ReservationID)
	switch {
	case err == nil:
		if admissionRequestMatches(existing, request) {
			return agentruntime.AdmissionDecision{Allowed: true, Reservation: existing}, nil
		}
		return agentruntime.AdmissionDecision{}, fmt.Errorf("admission reservation %q: %w", request.ReservationID, agentruntime.ErrIdempotencyConflict)
	case !errors.Is(err, agentruntime.ErrNotFound):
		return agentruntime.AdmissionDecision{}, err
	}
	if data, runErr := dbgen.New(u.tx).GetAdmissionReservationDataForRun(ctx, dbgen.GetAdmissionReservationDataForRunParams{
		AgentID: request.AgentID,
		RunID:   request.RunID,
	}); runErr == nil {
		reservation, decodeErr := decodeControlRecord[agentruntime.AdmissionReservation](data, "admission reservation")
		if decodeErr != nil {
			return agentruntime.AdmissionDecision{}, decodeErr
		}
		return agentruntime.AdmissionDecision{}, fmt.Errorf("admission run %q already has reservation %q: %w", request.RunID, reservation.ID, agentruntime.ErrIdempotencyConflict)
	} else if !errors.Is(runErr, sql.ErrNoRows) {
		return agentruntime.AdmissionDecision{}, fmt.Errorf("load admission reservation for run: %w", runErr)
	}
	reservations, err := u.admissionReservationsForAgent(ctx, request.AgentID)
	if err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	decision := evaluateAdmission(reservations, request, "")
	if !decision.Allowed {
		return decision, nil
	}
	reservation := agentruntime.AdmissionReservation{
		ID: request.ReservationID, AgentID: request.AgentID, AgentVersion: request.AgentVersion, RunID: request.RunID,
		State: agentruntime.AdmissionReserved, Limits: request.Limits, ReservedCredits: request.ReservedCredits,
		Version: 1, CreatedAt: request.RequestedAt, UpdatedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt,
	}
	data, err := json.Marshal(reservation)
	if err != nil {
		return agentruntime.AdmissionDecision{}, fmt.Errorf("marshal admission reservation: %w", err)
	}
	result, err := dbgen.New(u.tx).InsertAdmissionReservation(ctx, dbgen.InsertAdmissionReservationParams{
		ID: reservation.ID, AgentID: reservation.AgentID, RunID: reservation.RunID, State: string(reservation.State),
		Version: 1, CreatedAt: nanos(reservation.CreatedAt), UpdatedAt: nanos(reservation.UpdatedAt),
		ExpiresAt: nanos(reservation.ExpiresAt), Data: data,
	})
	if err != nil {
		return agentruntime.AdmissionDecision{}, fmt.Errorf("reserve admission: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return agentruntime.AdmissionDecision{}, fmt.Errorf("reserve admission: %w", err)
	}
	if inserted != 1 {
		return agentruntime.AdmissionDecision{}, fmt.Errorf("admission reservation %q collided: %w", request.ReservationID, agentruntime.ErrIdempotencyConflict)
	}
	decision.Reservation = reservation
	return decision, nil
}

func (u *unitOfWork) TransitionAdmission(ctx context.Context, transition agentruntime.AdmissionTransition) (agentruntime.AdmissionDecision, error) {
	reservation, err := u.LoadAdmissionReservation(ctx, transition.ReservationID)
	if err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	reservations, err := u.admissionReservationsForAgent(ctx, reservation.AgentID)
	if err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	if reservation.Version != transition.ExpectedVersion {
		return agentruntime.AdmissionDecision{
			Reason: agentruntime.AdmissionDeniedVersionConflict,
			Usage:  admissionUsage(reservations, reservation.AgentID, reservation.Limits, transition.At, ""),
		}, nil
	}
	if err := validateAdmissionTransition(reservation, transition); err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	usage := admissionUsage(reservations, reservation.AgentID, reservation.Limits, transition.At, reservation.ID)
	if reservation.State == agentruntime.AdmissionSuspended && transition.To == agentruntime.AdmissionActive &&
		reservation.Limits.MaxConcurrentRuns > 0 && usage.ConcurrentRuns+1 > reservation.Limits.MaxConcurrentRuns {
		return agentruntime.AdmissionDecision{Reason: agentruntime.AdmissionDeniedConcurrency, Usage: usage}, nil
	}
	previousVersion := reservation.Version
	reservation.State = transition.To
	reservation.Version++
	reservation.UpdatedAt = transition.At
	if !transition.ExpiresAt.IsZero() {
		reservation.ExpiresAt = transition.ExpiresAt
	}
	switch transition.To {
	case agentruntime.AdmissionActive:
		if reservation.ActivatedAt.IsZero() {
			reservation.ActivatedAt = transition.At
		}
	case agentruntime.AdmissionSettled:
		reservation.ConsumedCredits = transition.ConsumedCredits
		reservation.Failed = transition.Failed
		reservation.SettledAt = transition.At
	}
	data, err := json.Marshal(reservation)
	if err != nil {
		return agentruntime.AdmissionDecision{}, fmt.Errorf("marshal admission reservation: %w", err)
	}
	version, err := int64FromUint64(reservation.Version)
	if err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	expected, err := int64FromUint64(previousVersion)
	if err != nil {
		return agentruntime.AdmissionDecision{}, err
	}
	result, err := dbgen.New(u.tx).UpdateAdmissionReservationCAS(ctx, dbgen.UpdateAdmissionReservationCASParams{
		NextState: string(reservation.State), NextVersion: version, UpdatedAt: nanos(reservation.UpdatedAt),
		ExpiresAt: nanos(reservation.ExpiresAt), Data: data, ID: reservation.ID, ExpectedVersion: expected,
	})
	if err != nil {
		return agentruntime.AdmissionDecision{}, fmt.Errorf("transition admission: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return agentruntime.AdmissionDecision{}, fmt.Errorf("transition admission: %w", err)
	}
	if updated != 1 {
		return agentruntime.AdmissionDecision{Reason: agentruntime.AdmissionDeniedVersionConflict, Usage: usage}, nil
	}
	return agentruntime.AdmissionDecision{Allowed: true, Usage: usage, Reservation: reservation}, nil
}

func (u *unitOfWork) LoadAdmissionReservation(ctx context.Context, id string) (agentruntime.AdmissionReservation, error) {
	data, err := dbgen.New(u.tx).GetAdmissionReservationData(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return agentruntime.AdmissionReservation{}, agentruntime.ErrNotFound
	}
	if err != nil {
		return agentruntime.AdmissionReservation{}, fmt.Errorf("load admission reservation: %w", err)
	}
	return decodeControlRecord[agentruntime.AdmissionReservation](data, "admission reservation")
}

func (u *unitOfWork) ListAdmissionReservations(ctx context.Context, selector agentruntime.AdmissionReservationSelector) ([]agentruntime.AdmissionReservation, error) {
	rows, err := dbgen.New(u.tx).ListAdmissionReservationData(ctx)
	if err != nil {
		return nil, fmt.Errorf("list admission reservations: %w", err)
	}
	out := make([]agentruntime.AdmissionReservation, 0, len(rows))
	for _, data := range rows {
		reservation, err := decodeControlRecord[agentruntime.AdmissionReservation](data, "admission reservation")
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

func (u *unitOfWork) admissionReservationsForAgent(ctx context.Context, agentID string) ([]agentruntime.AdmissionReservation, error) {
	rows, err := dbgen.New(u.tx).ListAdmissionReservationDataByAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list admission reservations: %w", err)
	}
	out := make([]agentruntime.AdmissionReservation, 0, len(rows))
	for _, data := range rows {
		reservation, err := decodeControlRecord[agentruntime.AdmissionReservation](data, "admission reservation")
		if err != nil {
			return nil, err
		}
		out = append(out, reservation)
	}
	return out, nil
}

func validateAdmissionRequest(request agentruntime.AdmissionRequest) error {
	if request.ReservationID == "" || request.AgentID == "" || request.RunID == "" {
		return fmt.Errorf("admission reservation, agent, and run ids are required: %w", agentruntime.ErrInvalidCommand)
	}
	if request.RequestedAt.IsZero() || !request.ExpiresAt.After(request.RequestedAt) {
		return fmt.Errorf("admission timestamps are invalid: %w", agentruntime.ErrInvalidCommand)
	}
	if request.ReservedCredits < 0 || request.Limits.MaxConcurrentRuns < 0 || request.Limits.MaxRunsPerWindow < 0 ||
		request.Limits.MaxCredits < 0 || request.Limits.PauseOnExcessFailures < 0 {
		return fmt.Errorf("admission limits cannot be negative: %w", agentruntime.ErrInvalidCommand)
	}
	usesWindow := request.Limits.MaxRunsPerWindow > 0 || request.Limits.MaxCredits > 0 || request.Limits.PauseOnExcessFailures > 0
	if usesWindow && request.Limits.Window <= 0 {
		return fmt.Errorf("admission window is required by aggregate limits: %w", agentruntime.ErrInvalidCommand)
	}
	return nil
}

func validateAdmissionTransition(reservation agentruntime.AdmissionReservation, transition agentruntime.AdmissionTransition) error {
	if transition.At.IsZero() || transition.At.Before(reservation.UpdatedAt) {
		return fmt.Errorf("admission transition timestamp is invalid: %w", agentruntime.ErrInvalidTransition)
	}
	if transition.ConsumedCredits < 0 {
		return fmt.Errorf("admission consumed credits cannot be negative: %w", agentruntime.ErrInvalidTransition)
	}
	if !transition.ExpiresAt.IsZero() && !transition.ExpiresAt.After(transition.At) {
		return fmt.Errorf("admission transition expiry is invalid: %w", agentruntime.ErrInvalidTransition)
	}
	if !validAdmissionTransition(reservation.State, transition.To) {
		return fmt.Errorf("admission transition %q to %q: %w", reservation.State, transition.To, agentruntime.ErrInvalidTransition)
	}
	if !reservation.ExpiresAt.After(transition.At) && transition.To != agentruntime.AdmissionExpired && transition.To != agentruntime.AdmissionSettled {
		return fmt.Errorf("admission reservation has expired: %w", agentruntime.ErrInvalidTransition)
	}
	if transition.To != agentruntime.AdmissionSettled && (transition.ConsumedCredits != 0 || transition.Failed) {
		return fmt.Errorf("admission outcome is only valid when settling: %w", agentruntime.ErrInvalidTransition)
	}
	return nil
}

func validAdmissionTransition(from, to agentruntime.AdmissionState) bool {
	switch from {
	case agentruntime.AdmissionReserved:
		return to == agentruntime.AdmissionActive || to == agentruntime.AdmissionReleased || to == agentruntime.AdmissionExpired
	case agentruntime.AdmissionActive:
		return to == agentruntime.AdmissionSuspended || to == agentruntime.AdmissionSettled || to == agentruntime.AdmissionReleased || to == agentruntime.AdmissionExpired
	case agentruntime.AdmissionSuspended:
		return to == agentruntime.AdmissionActive || to == agentruntime.AdmissionSettled || to == agentruntime.AdmissionReleased || to == agentruntime.AdmissionExpired
	default:
		return false
	}
}

func evaluateAdmission(reservations []agentruntime.AdmissionReservation, request agentruntime.AdmissionRequest, excludeID string) agentruntime.AdmissionDecision {
	usage := admissionUsage(reservations, request.AgentID, request.Limits, request.RequestedAt, excludeID)
	switch {
	case request.Limits.MaxConcurrentRuns > 0 && usage.ConcurrentRuns+1 > request.Limits.MaxConcurrentRuns:
		return agentruntime.AdmissionDecision{Reason: agentruntime.AdmissionDeniedConcurrency, Usage: usage}
	case request.Limits.MaxRunsPerWindow > 0 && usage.RunsInWindow+1 > request.Limits.MaxRunsPerWindow:
		return agentruntime.AdmissionDecision{Reason: agentruntime.AdmissionDeniedRunWindow, Usage: usage}
	case request.Limits.MaxCredits > 0 && usage.CommittedCredits+usage.ReservedCredits+request.ReservedCredits > request.Limits.MaxCredits:
		return agentruntime.AdmissionDecision{Reason: agentruntime.AdmissionDeniedCredits, Usage: usage}
	case request.Limits.PauseOnExcessFailures > 0 && usage.TrailingFailures >= request.Limits.PauseOnExcessFailures:
		return agentruntime.AdmissionDecision{Reason: agentruntime.AdmissionDeniedFailureBreaker, Usage: usage}
	default:
		return agentruntime.AdmissionDecision{Allowed: true, Usage: usage}
	}
}

func admissionUsage(reservations []agentruntime.AdmissionReservation, agentID string, limits agentruntime.AdmissionLimits, now time.Time, excludeID string) agentruntime.AdmissionUsage {
	usage := agentruntime.AdmissionUsage{}
	windowStart := now.Add(-limits.Window)
	settled := make([]agentruntime.AdmissionReservation, 0)
	for _, reservation := range reservations {
		if reservation.ID == excludeID || reservation.AgentID != agentID || reservation.State == agentruntime.AdmissionReleased || reservation.State == agentruntime.AdmissionExpired {
			continue
		}
		expired := !reservation.ExpiresAt.After(now) && reservation.State != agentruntime.AdmissionSettled
		if !expired && (reservation.State == agentruntime.AdmissionReserved || reservation.State == agentruntime.AdmissionActive) {
			usage.ConcurrentRuns++
		}
		if limits.Window <= 0 || reservation.CreatedAt.Before(windowStart) {
			continue
		}
		usage.RunsInWindow++
		switch reservation.State {
		case agentruntime.AdmissionReserved, agentruntime.AdmissionActive, agentruntime.AdmissionSuspended:
			if !expired {
				usage.ReservedCredits += reservation.ReservedCredits
			}
		case agentruntime.AdmissionSettled:
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

func admissionRequestMatches(reservation agentruntime.AdmissionReservation, request agentruntime.AdmissionRequest) bool {
	return reservation.ID == request.ReservationID && reservation.AgentID == request.AgentID &&
		reservation.AgentVersion == request.AgentVersion && reservation.RunID == request.RunID &&
		reservation.Limits == request.Limits && reservation.ReservedCredits == request.ReservedCredits
}

func matchesAdmissionSelector(reservation agentruntime.AdmissionReservation, selector agentruntime.AdmissionReservationSelector) bool {
	return (len(selector.AgentIDs) == 0 || contains(selector.AgentIDs, reservation.AgentID)) &&
		(len(selector.RunIDs) == 0 || contains(selector.RunIDs, reservation.RunID)) &&
		(len(selector.States) == 0 || contains(selector.States, reservation.State)) &&
		(selector.Since.IsZero() || !reservation.CreatedAt.Before(selector.Since)) &&
		(selector.ExpiresBefore.IsZero() || !reservation.ExpiresAt.After(selector.ExpiresBefore))
}

func (u *unitOfWork) AcquireResourceClaims(ctx context.Context, request agentruntime.ResourceClaimRequest) (agentruntime.ResourceClaimDecision, error) {
	if err := validateResourceClaimRequest(request); err != nil {
		return agentruntime.ResourceClaimDecision{}, err
	}
	requestedIDs := make(map[string]struct{}, len(request.Claims))
	claims := make([]agentruntime.ResourceClaim, 0, len(request.Claims))
	newClaims := make([]agentruntime.ResourceClaim, 0, len(request.Claims))
	for _, spec := range request.Claims {
		requestedIDs[spec.ID] = struct{}{}
		existing, err := u.LoadResourceClaim(ctx, spec.ID)
		if err == nil {
			if !resourceClaimRequestMatches(existing, request, spec) {
				return agentruntime.ResourceClaimDecision{}, fmt.Errorf("resource claim %q: %w", spec.ID, agentruntime.ErrIdempotencyConflict)
			}
			claims = append(claims, existing)
			continue
		}
		if !errors.Is(err, agentruntime.ErrNotFound) {
			return agentruntime.ResourceClaimDecision{}, err
		}
		claim := agentruntime.ResourceClaim{
			ID: spec.ID, Key: spec.Key, Mode: spec.Mode,
			RunID: request.RunID, TaskID: request.TaskID, LeaseID: request.LeaseID, HolderID: request.HolderID,
			State: agentruntime.ResourceClaimActive, Version: 1,
			CreatedAt: request.RequestedAt, UpdatedAt: request.RequestedAt, ExpiresAt: request.ExpiresAt,
		}
		claims = append(claims, claim)
		newClaims = append(newClaims, claim)
	}

	conflicts := make([]agentruntime.ResourceClaim, 0)
	queries := dbgen.New(u.tx)
	for _, spec := range request.Claims {
		rows, err := queries.ListActiveResourceClaimDataByKey(ctx, dbgen.ListActiveResourceClaimDataByKeyParams{
			ResourceKey: spec.Key,
			ExpiresAt:   nanos(request.RequestedAt),
		})
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("list active resource claims: %w", err)
		}
		for _, data := range rows {
			existing, err := decodeControlRecord[agentruntime.ResourceClaim](data, "resource claim")
			if err != nil {
				return agentruntime.ResourceClaimDecision{}, err
			}
			if _, ownRequest := requestedIDs[existing.ID]; ownRequest {
				continue
			}
			if existing.Mode == agentruntime.ResourceClaimExclusive || spec.Mode == agentruntime.ResourceClaimExclusive {
				conflicts = append(conflicts, existing)
			}
		}
	}
	if len(conflicts) > 0 {
		sortResourceClaims(conflicts)
		return agentruntime.ResourceClaimDecision{Reason: agentruntime.ResourceClaimDeniedConflict, Conflicts: conflicts}, nil
	}
	for _, claim := range newClaims {
		data, err := json.Marshal(claim)
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("marshal resource claim: %w", err)
		}
		result, err := queries.InsertResourceClaim(ctx, dbgen.InsertResourceClaimParams{
			ID: claim.ID, ResourceKey: claim.Key, RunID: claim.RunID, TaskID: claim.TaskID,
			LeaseID: claim.LeaseID, HolderID: claim.HolderID, Mode: string(claim.Mode), State: string(claim.State),
			Version: 1, CreatedAt: nanos(claim.CreatedAt), UpdatedAt: nanos(claim.UpdatedAt), ExpiresAt: nanos(claim.ExpiresAt), Data: data,
		})
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("acquire resource claim: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("acquire resource claim: %w", err)
		}
		if inserted != 1 {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("resource claim %q collided: %w", claim.ID, agentruntime.ErrIdempotencyConflict)
		}
	}
	return agentruntime.ResourceClaimDecision{Acquired: true, Claims: claims}, nil
}

func (u *unitOfWork) TransitionResourceClaims(ctx context.Context, request agentruntime.ResourceClaimTransitionRequest) (agentruntime.ResourceClaimDecision, error) {
	if len(request.Transitions) == 0 {
		return agentruntime.ResourceClaimDecision{}, fmt.Errorf("resource claim transitions are required: %w", agentruntime.ErrInvalidCommand)
	}
	seen := make(map[string]struct{}, len(request.Transitions))
	claims := make([]agentruntime.ResourceClaim, len(request.Transitions))
	versionConflicts := make([]agentruntime.ResourceClaim, 0)
	for index, transition := range request.Transitions {
		if strings.TrimSpace(transition.ClaimID) == "" {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("resource claim ID is required: %w", agentruntime.ErrInvalidCommand)
		}
		if _, duplicate := seen[transition.ClaimID]; duplicate {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("duplicate resource claim transition %q: %w", transition.ClaimID, agentruntime.ErrInvalidCommand)
		}
		seen[transition.ClaimID] = struct{}{}
		claim, err := u.LoadResourceClaim(ctx, transition.ClaimID)
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, err
		}
		claims[index] = claim
		if claim.Version != transition.ExpectedVersion {
			versionConflicts = append(versionConflicts, claim)
			continue
		}
		if err := validateResourceClaimTransition(claim, transition); err != nil {
			return agentruntime.ResourceClaimDecision{}, err
		}
	}
	if len(versionConflicts) > 0 {
		sortResourceClaims(versionConflicts)
		return agentruntime.ResourceClaimDecision{Reason: agentruntime.ResourceClaimDeniedVersionConflict, Conflicts: versionConflicts}, nil
	}
	queries := dbgen.New(u.tx)
	transitionByID := make(map[string]agentruntime.ResourceClaimTransition, len(request.Transitions))
	for _, transition := range request.Transitions {
		transitionByID[transition.ClaimID] = transition
	}
	conflicts := make([]agentruntime.ResourceClaim, 0)
	conflictIDs := make(map[string]struct{})
	for _, transition := range request.Transitions {
		if transition.To != agentruntime.ResourceClaimActive {
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
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("check resource claim renewal conflicts: %w", err)
		}
		for _, data := range rows {
			existing, err := decodeControlRecord[agentruntime.ResourceClaim](data, "resource claim")
			if err != nil {
				return agentruntime.ResourceClaimDecision{}, err
			}
			if candidateTransition, transitioning := transitionByID[existing.ID]; transitioning && candidateTransition.To != agentruntime.ResourceClaimActive {
				continue
			}
			if existing.ID == transition.ClaimID ||
				(existing.Mode != agentruntime.ResourceClaimExclusive && requested.Mode != agentruntime.ResourceClaimExclusive) {
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
		return agentruntime.ResourceClaimDecision{Reason: agentruntime.ResourceClaimDeniedConflict, Conflicts: conflicts}, nil
	}
	for index, transition := range request.Transitions {
		claim := claims[index]
		previousVersion := claim.Version
		claim.State = transition.To
		claim.Version++
		claim.UpdatedAt = transition.At
		if transition.To == agentruntime.ResourceClaimActive {
			claim.ExpiresAt = transition.ExpiresAt
		}
		data, err := json.Marshal(claim)
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("marshal resource claim: %w", err)
		}
		version, err := int64FromUint64(claim.Version)
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, err
		}
		expected, err := int64FromUint64(previousVersion)
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, err
		}
		result, err := queries.UpdateResourceClaimCAS(ctx, dbgen.UpdateResourceClaimCASParams{
			NextState: string(claim.State), NextVersion: version, UpdatedAt: nanos(claim.UpdatedAt),
			ExpiresAt: nanos(claim.ExpiresAt), Data: data, ID: claim.ID, ExpectedVersion: expected,
		})
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("transition resource claim: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("transition resource claim: %w", err)
		}
		if updated != 1 {
			return agentruntime.ResourceClaimDecision{}, fmt.Errorf("resource claim %q changed during batch transition: %w", claim.ID, agentruntime.ErrIdempotencyConflict)
		}
		claims[index] = claim
	}
	return agentruntime.ResourceClaimDecision{Acquired: true, Claims: claims}, nil
}

func (u *unitOfWork) LoadResourceClaim(ctx context.Context, id string) (agentruntime.ResourceClaim, error) {
	data, err := dbgen.New(u.tx).GetResourceClaimData(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return agentruntime.ResourceClaim{}, agentruntime.ErrNotFound
	}
	if err != nil {
		return agentruntime.ResourceClaim{}, fmt.Errorf("load resource claim: %w", err)
	}
	return decodeControlRecord[agentruntime.ResourceClaim](data, "resource claim")
}

func (u *unitOfWork) ListResourceClaims(ctx context.Context, selector agentruntime.ResourceClaimSelector) ([]agentruntime.ResourceClaim, error) {
	data, err := dbgen.New(u.tx).ListResourceClaimData(ctx)
	if err != nil {
		return nil, fmt.Errorf("list resource claims: %w", err)
	}
	claims := make([]agentruntime.ResourceClaim, 0, len(data))
	for _, raw := range data {
		claim, err := decodeControlRecord[agentruntime.ResourceClaim](raw, "resource claim")
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

func validateResourceClaimTransition(claim agentruntime.ResourceClaim, transition agentruntime.ResourceClaimTransition) error {
	if transition.At.IsZero() || transition.At.Before(claim.UpdatedAt) {
		return fmt.Errorf("resource claim transition timestamp is invalid: %w", agentruntime.ErrInvalidTransition)
	}
	if claim.State != agentruntime.ResourceClaimActive {
		return fmt.Errorf("resource claim %q is terminal: %w", claim.ID, agentruntime.ErrInvalidTransition)
	}
	switch transition.To {
	case agentruntime.ResourceClaimActive:
		if !claim.ExpiresAt.After(transition.At) {
			return fmt.Errorf("resource claim %q has expired: %w", claim.ID, agentruntime.ErrInvalidTransition)
		}
		if !transition.ExpiresAt.After(transition.At) {
			return fmt.Errorf("resource claim renewal expiry is invalid: %w", agentruntime.ErrInvalidTransition)
		}
	case agentruntime.ResourceClaimReleased, agentruntime.ResourceClaimExpired:
		if !transition.ExpiresAt.IsZero() {
			return fmt.Errorf("terminal resource claim transition cannot set expiry: %w", agentruntime.ErrInvalidTransition)
		}
	default:
		return fmt.Errorf("resource claim transition to %q: %w", transition.To, agentruntime.ErrInvalidTransition)
	}
	return nil
}

func validateResourceClaimRequest(request agentruntime.ResourceClaimRequest) error {
	if request.RunID == "" || request.TaskID == "" || request.LeaseID == "" || request.HolderID == "" {
		return fmt.Errorf("resource claim run, task, lease, and holder IDs are required: %w", agentruntime.ErrInvalidCommand)
	}
	if request.RequestedAt.IsZero() || !request.ExpiresAt.After(request.RequestedAt) {
		return fmt.Errorf("resource claim timestamps are invalid: %w", agentruntime.ErrInvalidCommand)
	}
	if len(request.Claims) == 0 {
		return fmt.Errorf("resource claims are required: %w", agentruntime.ErrInvalidCommand)
	}
	ids := make(map[string]struct{}, len(request.Claims))
	keys := make(map[string]struct{}, len(request.Claims))
	for _, claim := range request.Claims {
		if strings.TrimSpace(claim.ID) == "" || strings.TrimSpace(claim.Key) == "" {
			return fmt.Errorf("resource claim ID and key are required: %w", agentruntime.ErrInvalidCommand)
		}
		if claim.Mode != agentruntime.ResourceClaimShared && claim.Mode != agentruntime.ResourceClaimExclusive {
			return fmt.Errorf("resource claim %q has invalid mode %q: %w", claim.ID, claim.Mode, agentruntime.ErrInvalidCommand)
		}
		if _, duplicate := ids[claim.ID]; duplicate {
			return fmt.Errorf("duplicate resource claim ID %q: %w", claim.ID, agentruntime.ErrInvalidCommand)
		}
		ids[claim.ID] = struct{}{}
		if _, duplicate := keys[claim.Key]; duplicate {
			return fmt.Errorf("duplicate resource claim key %q: %w", claim.Key, agentruntime.ErrInvalidCommand)
		}
		keys[claim.Key] = struct{}{}
	}
	return nil
}

func resourceClaimRequestMatches(claim agentruntime.ResourceClaim, request agentruntime.ResourceClaimRequest, spec agentruntime.ResourceClaimSpec) bool {
	return claim.ID == spec.ID && claim.Key == spec.Key && claim.Mode == spec.Mode &&
		claim.RunID == request.RunID && claim.TaskID == request.TaskID && claim.LeaseID == request.LeaseID &&
		claim.HolderID == request.HolderID && claim.State == agentruntime.ResourceClaimActive &&
		claim.ExpiresAt.After(request.RequestedAt) &&
		claim.ExpiresAt.Sub(claim.CreatedAt) == request.ExpiresAt.Sub(request.RequestedAt)
}

func matchesResourceClaimSelector(claim agentruntime.ResourceClaim, selector agentruntime.ResourceClaimSelector) bool {
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

func sortResourceClaims(claims []agentruntime.ResourceClaim) {
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
	_ agentruntime.AdmissionReservationStore      = (*unitOfWork)(nil)
	_ agentruntime.AdmissionReservationUnitOfWork = (*unitOfWork)(nil)
	_ agentruntime.ResourceClaimStore             = (*unitOfWork)(nil)
	_ agentruntime.ResourceClaimUnitOfWork        = (*unitOfWork)(nil)
)
