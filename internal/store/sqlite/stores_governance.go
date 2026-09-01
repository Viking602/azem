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

func (u *unitOfWork) LoadTraceSpan(ctx context.Context, id string) (agentruntime.TraceSpan, error) {
	return loadRecord[agentruntime.TraceSpan](ctx, u, kindTrace, id, "")
}

func (u *unitOfWork) UpdateTraceSpan(ctx context.Context, value agentruntime.TraceSpan) error {
	return u.SaveTraceSpan(ctx, value)
}

func (u *unitOfWork) SaveLease(ctx context.Context, value agentruntime.TaskExecutionLease) error {
	syncLeaseExpiry(&value)
	queries := dbgen.New(u.tx)
	current, err := queries.MaxLeaseVersion(ctx, dbgen.MaxLeaseVersionParams{RunID: value.RunID, TaskID: value.TaskID})
	if err != nil {
		return fmt.Errorf("read lease version: %w", err)
	}
	currentVersion, err := uint64FromInt64(current)
	if err != nil {
		return fmt.Errorf("read lease version: %w", err)
	}
	if value.Version <= currentVersion {
		value.Version = currentVersion + 1
	}
	version, err := int64FromUint64(value.Version)
	if err != nil {
		return fmt.Errorf("save lease: %w", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal lease: %w", err)
	}
	err = queries.UpsertLease(ctx, dbgen.UpsertLeaseParams{ID: value.ID, RunID: value.RunID, TaskID: value.TaskID, HolderID: value.HolderID, Status: string(value.Status), ExpiresAt: nanos(value.ExpiresAt), Version: version, Data: data})
	if err != nil {
		return fmt.Errorf("save lease: %w", err)
	}
	return nil
}

func (u *unitOfWork) LoadLease(ctx context.Context, id string) (agentruntime.TaskExecutionLease, error) {
	var value agentruntime.TaskExecutionLease
	data, err := dbgen.New(u.tx).GetLeaseData(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return value, agentruntime.ErrNotFound
		}
		return value, fmt.Errorf("load lease: %w", err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("decode lease: %w", err)
	}
	return value, nil
}

func (u *unitOfWork) ActiveLeaseForTask(ctx context.Context, runID string, taskID string) (agentruntime.TaskExecutionLease, bool, error) {
	var value agentruntime.TaskExecutionLease
	data, err := dbgen.New(u.tx).GetLatestLeaseData(ctx, dbgen.GetLatestLeaseDataParams{RunID: runID, TaskID: taskID})
	if errors.Is(err, sql.ErrNoRows) {
		return value, false, nil
	}
	if err != nil {
		return value, false, fmt.Errorf("load active lease: %w", err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, false, fmt.Errorf("decode active lease: %w", err)
	}
	return value, true, nil
}

func (u *unitOfWork) AcquireWithExpectedVersion(ctx context.Context, value agentruntime.TaskExecutionLease, expected uint64) (bool, error) {
	queries := dbgen.New(u.tx)
	row, err := queries.GetLatestLease(ctx, dbgen.GetLatestLeaseParams{RunID: value.RunID, TaskID: value.TaskID})
	var currentVersion uint64
	data := row.Data
	if errors.Is(err, sql.ErrNoRows) {
		currentVersion = 0
		data = nil
	} else if err != nil {
		if isBusy(err) {
			return false, nil
		}
		return false, fmt.Errorf("read lease slot: %w", err)
	} else if currentVersion, err = uint64FromInt64(row.Version); err != nil {
		return false, fmt.Errorf("read lease slot: %w", err)
	}
	if currentVersion != expected {
		return false, nil
	}
	if len(data) > 0 {
		var previous agentruntime.TaskExecutionLease
		if err := json.Unmarshal(data, &previous); err != nil {
			return false, fmt.Errorf("decode previous lease: %w", err)
		}
		if previous.Status == agentruntime.LeaseStatusActive && previous.ExpiresAt.After(time.Now()) {
			return false, nil
		}
		if previous.Status == agentruntime.LeaseStatusActive {
			previous.Status = agentruntime.LeaseStatusExpired
			previousVersion, err := int64FromUint64(previous.Version)
			if err != nil {
				return false, fmt.Errorf("expire previous lease: %w", err)
			}
			previousData, err := json.Marshal(previous)
			if err != nil {
				return false, err
			}
			if _, err := queries.ExpireLeaseCAS(ctx, dbgen.ExpireLeaseCASParams{Status: string(previous.Status), Data: previousData, ID: previous.ID, Version: previousVersion}); err != nil {
				if isBusy(err) {
					return false, nil
				}
				return false, fmt.Errorf("expire previous lease: %w", err)
			}
		}
	}
	value.Version = expected + 1
	value.Status = agentruntime.LeaseStatusActive
	syncLeaseExpiry(&value)
	version, err := int64FromUint64(value.Version)
	if err != nil {
		return false, fmt.Errorf("acquire lease: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("marshal acquired lease: %w", err)
	}
	err = queries.InsertLease(ctx, dbgen.InsertLeaseParams{ID: value.ID, RunID: value.RunID, TaskID: value.TaskID, HolderID: value.HolderID, Status: string(value.Status), ExpiresAt: nanos(value.ExpiresAt), Version: version, Data: encoded})
	if err != nil {
		if isBusy(err) || isConstraint(err) {
			return false, nil
		}
		return false, fmt.Errorf("acquire lease: %w", err)
	}
	return true, nil
}

func (u *unitOfWork) ExtendLease(ctx context.Context, leaseID string, workerID string, newExpiry time.Time) (bool, error) {
	value, err := u.LoadLease(ctx, leaseID)
	if errors.Is(err, agentruntime.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	latest, exists, err := u.ActiveLeaseForTask(ctx, value.RunID, value.TaskID)
	if err != nil || !exists {
		return false, err
	}
	if latest.ID != leaseID || value.HolderID != workerID || value.Status != agentruntime.LeaseStatusActive || !value.ExpiresAt.After(time.Now()) {
		return false, nil
	}
	oldVersion := value.Version
	oldVersionSQL, err := int64FromUint64(oldVersion)
	if err != nil {
		return false, fmt.Errorf("extend lease: %w", err)
	}
	value.Version++
	nextVersionSQL, err := int64FromUint64(value.Version)
	if err != nil {
		return false, fmt.Errorf("extend lease: %w", err)
	}
	value.ExpiresAt = newExpiry.UTC()
	value.Expiry = value.ExpiresAt
	value.HeartbeatAt = time.Now().UTC()
	data, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	result, err := dbgen.New(u.tx).ExtendLeaseCAS(ctx, dbgen.ExtendLeaseCASParams{ExpiresAt: nanos(value.ExpiresAt), Version: nextVersionSQL, Data: data, ID: leaseID, HolderID: workerID, Status: string(agentruntime.LeaseStatusActive), Version_2: oldVersionSQL})
	if err != nil {
		if isBusy(err) {
			return false, nil
		}
		return false, fmt.Errorf("extend lease: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (u *unitOfWork) ReleaseExpiredLease(ctx context.Context, leaseID string, expectedVersion uint64, releasedAt time.Time) (bool, error) {
	value, err := u.LoadLease(ctx, leaseID)
	if errors.Is(err, agentruntime.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	syncLeaseExpiry(&value)
	if value.Status != agentruntime.LeaseStatusActive ||
		value.Version != expectedVersion ||
		value.ExpiresAt.IsZero() ||
		value.ExpiresAt.After(releasedAt) {
		return false, nil
	}
	oldVersion, err := int64FromUint64(value.Version)
	if err != nil {
		return false, fmt.Errorf("release expired lease: %w", err)
	}
	value.Status = agentruntime.LeaseStatusReleased
	value.Version++
	nextVersion, err := int64FromUint64(value.Version)
	if err != nil {
		return false, fmt.Errorf("release expired lease: %w", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("release expired lease: %w", err)
	}
	result, err := dbgen.New(u.tx).ReleaseExpiredLeaseCAS(ctx, dbgen.ReleaseExpiredLeaseCASParams{
		Status:    string(agentruntime.LeaseStatusReleased),
		Version:   nextVersion,
		Data:      data,
		ID:        leaseID,
		Status_2:  string(agentruntime.LeaseStatusActive),
		Version_2: oldVersion,
		ExpiresAt: nanos(releasedAt),
	})
	if err != nil {
		if isBusy(err) {
			return false, nil
		}
		return false, fmt.Errorf("release expired lease: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (u *unitOfWork) SaveActionAttempt(ctx context.Context, value agentruntime.ActionAttempt) error {
	return u.save(ctx, kindAction, value.AttemptID, "", value.RunID, value.TaskID, string(value.Status), time.Time{}, value.ToolName, value.IdempotencyKey, value, true)
}

func (u *unitOfWork) LoadActionAttempt(ctx context.Context, id string) (agentruntime.ActionAttempt, error) {
	return loadRecord[agentruntime.ActionAttempt](ctx, u, kindAction, id, "")
}

func (u *unitOfWork) LoadActionAttemptByIdempotencyKey(ctx context.Context, runID string, taskID string, toolName string, key string) (agentruntime.ActionAttempt, error) {
	var value agentruntime.ActionAttempt
	row, err := dbgen.New(u.tx).GetActionAttemptByIdempotency(ctx, dbgen.GetActionAttemptByIdempotencyParams{Kind: kindAction, RunID: runID, TaskID: taskID, ToolName: toolName, IdempotencyKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return value, agentruntime.ErrNotFound
	}
	if err != nil {
		return value, err
	}
	data, err := loadPayload(ctx, u.blobs, row.Data, row.DataSha256)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}

func (u *unitOfWork) ListActionAttempts(ctx context.Context, selector agentruntime.ActionAttemptSelector) ([]agentruntime.ActionAttempt, error) {
	values, err := listRecords[agentruntime.ActionAttempt](ctx, u, kindAction, selector.RunID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if selector.TaskID != "" && value.TaskID != selector.TaskID ||
			selector.ToolName != "" && value.ToolName != selector.ToolName ||
			len(selector.Statuses) > 0 && !contains(selector.Statuses, value.Status) ||
			selector.RequiresReconcile != nil && value.RequiresReconcile != *selector.RequiresReconcile {
			continue
		}
		filtered = append(filtered, value)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].AttemptID < filtered[j].AttemptID
	})
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) ResolveActionAttempt(ctx context.Context, value agentruntime.ActionAttempt) (bool, error) {
	current, err := u.LoadActionAttempt(ctx, value.AttemptID)
	if errors.Is(err, agentruntime.ErrNotFound) {
		return false, agentruntime.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if current.Status != agentruntime.ActionAttemptUnknown || !current.RequiresReconcile {
		return false, nil
	}
	switch value.Status {
	case agentruntime.ActionAttemptSucceeded, agentruntime.ActionAttemptFailed, agentruntime.ActionAttemptTimeout, agentruntime.ActionAttemptCancelled:
	default:
		return false, nil
	}
	if value.RequiresReconcile ||
		value.ActionID != current.ActionID ||
		value.RunID != current.RunID ||
		value.TaskID != current.TaskID ||
		value.LeaseID != current.LeaseID ||
		value.ToolName != current.ToolName ||
		value.IdempotencyKey != current.IdempotencyKey ||
		value.InputHash != current.InputHash ||
		value.ExternalRequestID != current.ExternalRequestID {
		return false, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	result, err := dbgen.New(u.tx).ResolveReconcileAttemptCAS(ctx, dbgen.ResolveReconcileAttemptCASParams{
		Status:   string(value.Status),
		Data:     data,
		Kind:     kindAction,
		Key1:     value.AttemptID,
		Status_2: string(agentruntime.ActionAttemptUnknown),
	})
	if err != nil {
		if isBusy(err) {
			return false, nil
		}
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (u *unitOfWork) SaveAgentProfile(ctx context.Context, value agentruntime.AgentProfile) error {
	return u.save(ctx, kindAgentProfile, value.ID, "", "", "", "", time.Time{}, "", "", value, true)
}

func (u *unitOfWork) LoadAgentProfile(ctx context.Context, id string) (agentruntime.AgentProfile, error) {
	return loadRecord[agentruntime.AgentProfile](ctx, u, kindAgentProfile, id, "")
}

func (u *unitOfWork) ListAgentProfiles(ctx context.Context, selector agentruntime.AgentSelector) ([]agentruntime.AgentProfile, error) {
	values, err := listRecords[agentruntime.AgentProfile](ctx, u, kindAgentProfile, "")
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if len(selector.IDs) > 0 && !contains(selector.IDs, value.ID) || len(selector.Roles) > 0 && !contains(selector.Roles, value.Role) || len(selector.Groups) > 0 && !intersects(selector.Groups, value.Groups) {
			continue
		}
		filtered = append(filtered, value)
	}
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) SaveCapability(ctx context.Context, value agentruntime.Capability) error {
	if err := agentruntime.ValidateCapabilityName(value.Name); err != nil {
		return err
	}
	return u.save(ctx, kindCapability, value.Name, value.AgentID, "", "", "", time.Time{}, "", "", value, true)
}

func (u *unitOfWork) LoadCapability(ctx context.Context, name string, agentID string) (agentruntime.Capability, error) {
	return loadRecord[agentruntime.Capability](ctx, u, kindCapability, name, agentID)
}

func (u *unitOfWork) ListCapabilities(ctx context.Context, selector agentruntime.CapabilitySelector) ([]agentruntime.Capability, error) {
	values, err := listRecords[agentruntime.Capability](ctx, u, kindCapability, "")
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if len(selector.Names) > 0 && !contains(selector.Names, value.Name) || len(selector.AgentIDs) > 0 && !contains(selector.AgentIDs, value.AgentID) || len(selector.Tags) > 0 && !intersects(selector.Tags, value.Tags) {
			continue
		}
		filtered = append(filtered, value)
	}
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) AppendDeadLetter(ctx context.Context, value agentruntime.DeadLetterEntry) error {
	return u.save(ctx, kindDeadLetter, value.ID, "", value.RunID, value.TaskID, "", value.CreatedAt, "", "", value, false)
}

func (u *unitOfWork) ListDeadLetters(ctx context.Context, selector agentruntime.DeadLetterSelector) ([]agentruntime.DeadLetterEntry, error) {
	values, err := listRecords[agentruntime.DeadLetterEntry](ctx, u, kindDeadLetter, selector.RunID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if selector.TaskID != "" && value.TaskID != selector.TaskID || !within(value.CreatedAt, selector.Since, selector.Until) {
			continue
		}
		filtered = append(filtered, value)
	}
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) Requeue(context.Context, string) error {
	return fmt.Errorf("dead-letter requeue is not supported")
}

func syncLeaseExpiry(value *agentruntime.TaskExecutionLease) {
	if value.ExpiresAt.IsZero() {
		value.ExpiresAt = value.Expiry
	}
	value.ExpiresAt = value.ExpiresAt.UTC()
	value.Expiry = value.ExpiresAt
}

func intersects[T comparable](left []T, right []T) bool {
	for _, value := range left {
		if contains(right, value) {
			return true
		}
	}
	return false
}

func isConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "constraint") || strings.Contains(message, "unique")
}

var _ agentruntime.TraceSpanUpdater = (*unitOfWork)(nil)
