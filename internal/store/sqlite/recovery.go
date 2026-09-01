package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
	"github.com/Viking602/venat/durable"
)

// PrepareRecovery is called once at the exclusive application startup
// boundary. Every active lease belongs to the prior process and must be
// expired immediately; waiting for its TTL would make crash recovery stall for
// up to ten minutes. Quarantined attempts are never eligible for replay.
func (p *Provider) PrepareRecovery(ctx context.Context, at time.Time) (expiredLeases int64, quarantinedAttempts int64, err error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin recovery preparation: %w", err)
	}
	defer tx.Rollback()
	queries := dbgen.New(tx)
	leaseRows, err := queries.ListActiveLeases(ctx, string(agentruntime.LeaseStatusActive))
	if err != nil {
		return 0, 0, fmt.Errorf("list expired leases: %w", err)
	}
	for _, row := range leaseRows {
		if _, err := uint64FromInt64(row.Version); err != nil {
			return 0, 0, fmt.Errorf("scan expired lease: %w", err)
		}
		var lease agentruntime.TaskExecutionLease
		if err := json.Unmarshal(row.Data, &lease); err != nil {
			return 0, 0, fmt.Errorf("decode expired lease %s: %w", row.ID, err)
		}
		lease.Status = agentruntime.LeaseStatusExpired
		encoded, err := json.Marshal(lease)
		if err != nil {
			return 0, 0, err
		}
		result, err := queries.ExpireActiveLeaseCAS(ctx, dbgen.ExpireActiveLeaseCASParams{Status: string(agentruntime.LeaseStatusExpired), Data: encoded, ID: row.ID, Version: row.Version, Status_2: string(agentruntime.LeaseStatusActive)})
		if err != nil {
			return 0, 0, fmt.Errorf("expire lease %s: %w", row.ID, err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, 0, err
		}
		expiredLeases += changed
	}

	attemptRows, err := queries.ListIncompleteActionAttempts(ctx, dbgen.ListIncompleteActionAttemptsParams{Kind: kindAction, Status: string(agentruntime.ActionAttemptCreated), Status_2: string(agentruntime.ActionAttemptRunning)})
	if err != nil {
		return 0, 0, fmt.Errorf("list incomplete action attempts: %w", err)
	}
	for _, row := range attemptRows {
		var attempt agentruntime.ActionAttempt
		if err := json.Unmarshal(row.Data, &attempt); err != nil {
			return 0, 0, fmt.Errorf("decode incomplete action attempt %s: %w", row.Key1, err)
		}
		attempt.Status = agentruntime.ActionAttemptUnknown
		attempt.RequiresReconcile = true
		encoded, err := json.Marshal(attempt)
		if err != nil {
			return 0, 0, err
		}
		result, err := queries.QuarantineActionAttemptCAS(ctx, dbgen.QuarantineActionAttemptCASParams{Status: string(agentruntime.ActionAttemptUnknown), Data: encoded, Kind: kindAction, Key1: row.Key1, Status_2: string(agentruntime.ActionAttemptCreated), Status_3: string(agentruntime.ActionAttemptRunning)})
		if err != nil {
			return 0, 0, fmt.Errorf("quarantine action attempt %s: %w", row.Key1, err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, 0, err
		}
		quarantinedAttempts += changed
	}
	if err := queries.QuarantineStartedProviderRequests(ctx); err != nil {
		return 0, 0, fmt.Errorf("quarantine incomplete provider requests: %w", err)
	}
	if err := expireActiveResourceClaims(ctx, queries, at); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit recovery preparation: %w", err)
	}
	return expiredLeases, quarantinedAttempts, nil
}

func expireActiveResourceClaims(ctx context.Context, queries *dbgen.Queries, at time.Time) error {
	rows, err := queries.ListResourceClaimData(ctx)
	if err != nil {
		return fmt.Errorf("list resource claims: %w", err)
	}
	for _, data := range rows {
		claim, err := decodeControlRecord[agentruntime.ResourceClaim](data, "resource claim")
		if err != nil {
			return err
		}
		if claim.State != agentruntime.ResourceClaimActive {
			continue
		}
		previous := claim.Version
		claim.State = agentruntime.ResourceClaimExpired
		claim.Version++
		claim.UpdatedAt = at
		encoded, err := json.Marshal(claim)
		if err != nil {
			return fmt.Errorf("marshal expired resource claim %s: %w", claim.ID, err)
		}
		nextVersion, err := int64FromUint64(claim.Version)
		if err != nil {
			return err
		}
		expected, err := int64FromUint64(previous)
		if err != nil {
			return err
		}
		if _, err := queries.UpdateResourceClaimCAS(ctx, dbgen.UpdateResourceClaimCASParams{
			NextState: string(claim.State), NextVersion: nextVersion, UpdatedAt: nanos(claim.UpdatedAt),
			ExpiresAt: nanos(claim.ExpiresAt), Data: encoded, ID: claim.ID, ExpectedVersion: expected,
		}); err != nil {
			return fmt.Errorf("expire resource claim %s: %w", claim.ID, err)
		}
	}
	return nil
}

func (p *Provider) ListReconcileAttempts(ctx context.Context) ([]agentruntime.ActionAttempt, error) {
	rows, err := dbgen.New(p.db).ListReconcileAttemptData(ctx, dbgen.ListReconcileAttemptDataParams{Kind: kindAction, Status: string(agentruntime.ActionAttemptUnknown)})
	if err != nil {
		return nil, fmt.Errorf("list reconcile attempts: %w", err)
	}
	attempts := make([]agentruntime.ActionAttempt, 0, len(rows))
	for _, data := range rows {
		var attempt agentruntime.ActionAttempt
		if err := json.Unmarshal(data, &attempt); err != nil {
			return nil, fmt.Errorf("decode reconcile attempt: %w", err)
		}
		if attempt.RequiresReconcile || attempt.Status == agentruntime.ActionAttemptUnknown {
			attempts = append(attempts, attempt)
		}
	}
	durableAttempts, err := p.ListDurableReconcileAttempts(ctx)
	if err != nil {
		return nil, err
	}
	return append(attempts, durableAttempts...), nil
}

func (p *Provider) ListDurableReconcileAttempts(ctx context.Context) ([]agentruntime.ActionAttempt, error) {
	unknown, err := (&DurableBackend{provider: p}).listAttemptsByStatus(ctx, durable.AttemptStatusUnknown)
	if err != nil {
		return nil, err
	}
	return p.projectDurableAttempts(ctx, unknown)
}

func (p *Provider) LoadDurableReconcileAttempt(ctx context.Context, attemptID string) (agentruntime.ActionAttempt, error) {
	all, err := (&DurableBackend{provider: p}).listAttemptsByStatus(ctx, "")
	if err != nil {
		return agentruntime.ActionAttempt{}, err
	}
	attempts, err := p.projectDurableAttempts(ctx, all)
	if err != nil {
		return agentruntime.ActionAttempt{}, err
	}
	for _, attempt := range attempts {
		if attempt.AttemptID == attemptID {
			return attempt, nil
		}
	}
	return agentruntime.ActionAttempt{}, agentruntime.ErrNotFound
}

func (p *Provider) projectDurableAttempts(ctx context.Context, stored []durable.Attempt) ([]agentruntime.ActionAttempt, error) {
	attempts := make([]agentruntime.ActionAttempt, 0, len(stored))
	bindings := make(map[string]agentruntime.ExecutionBinding)
	executions := make(map[string]durable.Execution)
	for _, attempt := range stored {
		executionID := string(attempt.ExecutionID)
		binding, ok := bindings[executionID]
		if !ok {
			var err error
			binding, err = p.LoadExecutionBinding(ctx, executionID)
			if err != nil {
				return nil, fmt.Errorf("load binding for durable attempt %q: %w", executionID, err)
			}
			bindings[executionID] = binding
		}
		execution, ok := executions[executionID]
		if !ok {
			var err error
			execution, err = p.DurableBackend().LoadExecution(ctx, attempt.ExecutionID)
			if err != nil {
				return nil, fmt.Errorf("load execution for durable attempt %q: %w", executionID, err)
			}
			executions[executionID] = execution
		}
		status := durableAttemptStatus(attempt.Status)
		expectedVersion := attempt.Version
		if attempt.Status == durable.AttemptStatusSucceeded || attempt.Status == durable.AttemptStatusFailed || attempt.Status == durable.AttemptStatusAbandoned {
			if expectedVersion > 1 {
				expectedVersion--
			}
		}
		projected := agentruntime.ActionAttempt{
			AttemptID: durableReconcileAttemptID(attempt), RunID: binding.RunID,
			TaskID: binding.Manifest.Metadata["task_id"], ToolName: durableAttemptDisplayName(execution, attempt),
			Status: status, InputHash: hex.EncodeToString(attempt.InputHash[:]),
			RequiresReconcile: attempt.Status == durable.AttemptStatusUnknown, ExecutionID: executionID,
			OperationID: attempt.OperationID, AttemptNumber: attempt.Number, AttemptVersion: expectedVersion,
			AttemptKind: string(attempt.Kind),
		}
		if execution.Checkpoint != nil {
			projected.CheckpointSequence = execution.Checkpoint.Sequence
			projected.ContinuationPhase = string(execution.Checkpoint.Continuation.Phase)
		}
		attempts = append(attempts, projected)
	}
	return attempts, nil
}

func durableAttemptStatus(status durable.AttemptStatus) agentruntime.ActionAttemptStatus {
	switch status {
	case durable.AttemptStatusRunning:
		return agentruntime.ActionAttemptRunning
	case durable.AttemptStatusSucceeded:
		return agentruntime.ActionAttemptSucceeded
	case durable.AttemptStatusFailed:
		return agentruntime.ActionAttemptFailed
	case durable.AttemptStatusAbandoned:
		return agentruntime.ActionAttemptRetry
	default:
		return agentruntime.ActionAttemptUnknown
	}
}

func durableReconcileAttemptID(attempt durable.Attempt) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", attempt.ExecutionID, attempt.OperationID, attempt.Number)))
	return "durable_attempt_" + hex.EncodeToString(digest[:16])
}

func durableAttemptDisplayName(execution durable.Execution, attempt durable.Attempt) string {
	if attempt.Kind == durable.AttemptKindModel {
		return "provider.model"
	}
	if execution.Checkpoint != nil {
		for _, current := range execution.Checkpoint.Continuation.Messages {
			for _, call := range current.ToolCalls {
				if call.OperationID == attempt.OperationID && call.Name != "" {
					return call.Name
				}
			}
		}
	}
	return attempt.OperationID
}

// ListSucceededActionAttempts exposes the durable anti-replay ledger needed
// when a recovered model generates a fresh call ID for an already completed
// non-idempotent input.
func (p *Provider) ListSucceededActionAttempts(ctx context.Context, runID, taskID string) ([]agentruntime.ActionAttempt, error) {
	rows, err := dbgen.New(p.db).ListSucceededActionAttemptData(ctx, dbgen.ListSucceededActionAttemptDataParams{Kind: kindAction, RunID: runID, TaskID: taskID, Status: string(agentruntime.ActionAttemptSucceeded)})
	if err != nil {
		return nil, err
	}
	var attempts []agentruntime.ActionAttempt
	for _, data := range rows {
		var attempt agentruntime.ActionAttempt
		if err := json.Unmarshal(data, &attempt); err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, nil
}

func (p *Provider) RecordToolCallCharge(ctx context.Context, runID, taskID, callID, toolName, inputHash string) (bool, error) {
	queries := dbgen.New(p.db)
	result, err := queries.InsertToolCallCharge(ctx, dbgen.InsertToolCallChargeParams{RunID: runID, TaskID: taskID, CallID: callID, ToolName: toolName, InputHash: inputHash, CreatedAt: time.Now().UTC().UnixNano()})
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 1 {
		return changed == 1, err
	}
	recorded, err := queries.GetToolCallCharge(ctx, dbgen.GetToolCallChargeParams{RunID: runID, TaskID: taskID, CallID: callID})
	if err != nil {
		return false, err
	}
	if recorded.ToolName != toolName || recorded.InputHash != inputHash {
		return false, fmt.Errorf("tool call %s was already charged with different input", callID)
	}
	return false, nil
}

func (p *Provider) CountToolCallCharges(ctx context.Context, runID, taskID string) (int, error) {
	count, err := dbgen.New(p.db).CountToolCallCharges(ctx, dbgen.CountToolCallChargesParams{RunID: runID, TaskID: taskID})
	if err != nil {
		return 0, err
	}
	return intFromInt64(count)
}
