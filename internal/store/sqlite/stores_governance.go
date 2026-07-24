package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
	"github.com/Viking602/go-hydaelyn/api"
)

func (u *unitOfWork) LoadTraceSpan(ctx context.Context, id string) (api.TraceSpan, error) {
	return loadRecord[api.TraceSpan](ctx, u.tx, kindTrace, id, "")
}

func (u *unitOfWork) UpdateTraceSpan(ctx context.Context, value api.TraceSpan) error {
	return u.SaveTraceSpan(ctx, value)
}

func (u *unitOfWork) SaveLease(ctx context.Context, value api.TaskExecutionLease) error {
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

func (u *unitOfWork) LoadLease(ctx context.Context, id string) (api.TaskExecutionLease, error) {
	var value api.TaskExecutionLease
	data, err := dbgen.New(u.tx).GetLeaseData(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return value, api.ErrNotFound
		}
		return value, fmt.Errorf("load lease: %w", err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("decode lease: %w", err)
	}
	return value, nil
}

func (u *unitOfWork) ActiveLeaseForTask(ctx context.Context, runID string, taskID string) (api.TaskExecutionLease, bool, error) {
	var value api.TaskExecutionLease
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

func (u *unitOfWork) AcquireWithExpectedVersion(ctx context.Context, value api.TaskExecutionLease, expected uint64) (bool, error) {
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
		var previous api.TaskExecutionLease
		if err := json.Unmarshal(data, &previous); err != nil {
			return false, fmt.Errorf("decode previous lease: %w", err)
		}
		if previous.Status == api.LeaseStatusActive && previous.ExpiresAt.After(time.Now()) {
			return false, nil
		}
		if previous.Status == api.LeaseStatusActive {
			previous.Status = api.LeaseStatusExpired
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
	value.Status = api.LeaseStatusActive
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
	if errors.Is(err, api.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	latest, exists, err := u.ActiveLeaseForTask(ctx, value.RunID, value.TaskID)
	if err != nil || !exists {
		return false, err
	}
	if latest.ID != leaseID || value.HolderID != workerID || value.Status != api.LeaseStatusActive || !value.ExpiresAt.After(time.Now()) {
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
	result, err := dbgen.New(u.tx).ExtendLeaseCAS(ctx, dbgen.ExtendLeaseCASParams{ExpiresAt: nanos(value.ExpiresAt), Version: nextVersionSQL, Data: data, ID: leaseID, HolderID: workerID, Status: string(api.LeaseStatusActive), Version_2: oldVersionSQL})
	if err != nil {
		if isBusy(err) {
			return false, nil
		}
		return false, fmt.Errorf("extend lease: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (u *unitOfWork) SaveActionAttempt(ctx context.Context, value api.ActionAttempt) error {
	return u.save(ctx, kindAction, value.AttemptID, "", value.RunID, value.TaskID, string(value.Status), time.Time{}, value.ToolName, value.IdempotencyKey, value, true)
}

func (u *unitOfWork) LoadActionAttempt(ctx context.Context, id string) (api.ActionAttempt, error) {
	return loadRecord[api.ActionAttempt](ctx, u.tx, kindAction, id, "")
}

func (u *unitOfWork) LoadActionAttemptByIdempotencyKey(ctx context.Context, runID string, taskID string, toolName string, key string) (api.ActionAttempt, error) {
	var value api.ActionAttempt
	data, err := dbgen.New(u.tx).GetActionAttemptByIdempotency(ctx, dbgen.GetActionAttemptByIdempotencyParams{Kind: kindAction, RunID: runID, TaskID: taskID, ToolName: toolName, IdempotencyKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return value, api.ErrNotFound
	}
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}

func (u *unitOfWork) SaveAgentProfile(ctx context.Context, value api.AgentProfile) error {
	return u.save(ctx, kindAgentProfile, value.ID, "", "", "", "", time.Time{}, "", "", value, true)
}

func (u *unitOfWork) LoadAgentProfile(ctx context.Context, id string) (api.AgentProfile, error) {
	return loadRecord[api.AgentProfile](ctx, u.tx, kindAgentProfile, id, "")
}

func (u *unitOfWork) ListAgentProfiles(ctx context.Context, selector api.AgentSelector) ([]api.AgentProfile, error) {
	values, err := listRecords[api.AgentProfile](ctx, u.tx, kindAgentProfile, "")
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

func (u *unitOfWork) SaveCapability(ctx context.Context, value api.Capability) error {
	return u.save(ctx, kindCapability, value.Name, value.AgentID, "", "", "", time.Time{}, "", "", value, true)
}

func (u *unitOfWork) LoadCapability(ctx context.Context, name string, agentID string) (api.Capability, error) {
	return loadRecord[api.Capability](ctx, u.tx, kindCapability, name, agentID)
}

func (u *unitOfWork) ListCapabilities(ctx context.Context, selector api.CapabilitySelector) ([]api.Capability, error) {
	values, err := listRecords[api.Capability](ctx, u.tx, kindCapability, "")
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

func (u *unitOfWork) AppendUsage(ctx context.Context, value api.UsageRecord) error {
	return u.save(ctx, kindUsage, value.ID, "", value.RunID, value.TaskID, "", value.CreatedAt, "", "", value, false)
}

func (u *unitOfWork) QueryUsage(ctx context.Context, selector api.UsageSelector) ([]api.UsageRecord, error) {
	values, err := listRecords[api.UsageRecord](ctx, u.tx, kindUsage, selector.RunID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if selector.TaskID != "" && value.TaskID != selector.TaskID || selector.AgentID != "" && value.AgentID != selector.AgentID || selector.Provider != "" && value.Provider != selector.Provider || !within(value.CreatedAt, selector.Since, selector.Until) {
			continue
		}
		filtered = append(filtered, value)
	}
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) SumCredits(ctx context.Context, selector api.UsageSelector) (int64, error) {
	values, err := u.QueryUsage(ctx, selector)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, value := range values {
		total += value.Credits
	}
	return total, nil
}

func (u *unitOfWork) AppendDeadLetter(ctx context.Context, value api.DeadLetterEntry) error {
	return u.save(ctx, kindDeadLetter, value.ID, "", value.RunID, value.TaskID, "", value.CreatedAt, "", "", value, false)
}

func (u *unitOfWork) ListDeadLetters(ctx context.Context, selector api.DeadLetterSelector) ([]api.DeadLetterEntry, error) {
	values, err := listRecords[api.DeadLetterEntry](ctx, u.tx, kindDeadLetter, selector.RunID)
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

func syncLeaseExpiry(value *api.TaskExecutionLease) {
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

var _ api.TraceSpanUpdater = (*unitOfWork)(nil)
