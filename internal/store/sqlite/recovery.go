package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
	"github.com/Viking602/go-hydaelyn/api"
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
	leaseRows, err := queries.ListActiveLeases(ctx, string(api.LeaseStatusActive))
	if err != nil {
		return 0, 0, fmt.Errorf("list expired leases: %w", err)
	}
	for _, row := range leaseRows {
		if _, err := uint64FromInt64(row.Version); err != nil {
			return 0, 0, fmt.Errorf("scan expired lease: %w", err)
		}
		var lease api.TaskExecutionLease
		if err := json.Unmarshal(row.Data, &lease); err != nil {
			return 0, 0, fmt.Errorf("decode expired lease %s: %w", row.ID, err)
		}
		lease.Status = api.LeaseStatusExpired
		encoded, err := json.Marshal(lease)
		if err != nil {
			return 0, 0, err
		}
		result, err := queries.ExpireActiveLeaseCAS(ctx, dbgen.ExpireActiveLeaseCASParams{Status: string(api.LeaseStatusExpired), Data: encoded, ID: row.ID, Version: row.Version, Status_2: string(api.LeaseStatusActive)})
		if err != nil {
			return 0, 0, fmt.Errorf("expire lease %s: %w", row.ID, err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, 0, err
		}
		expiredLeases += changed
	}

	attemptRows, err := queries.ListIncompleteActionAttempts(ctx, dbgen.ListIncompleteActionAttemptsParams{Kind: kindAction, Status: string(api.ActionAttemptCreated), Status_2: string(api.ActionAttemptRunning)})
	if err != nil {
		return 0, 0, fmt.Errorf("list incomplete action attempts: %w", err)
	}
	for _, row := range attemptRows {
		var attempt api.ActionAttempt
		if err := json.Unmarshal(row.Data, &attempt); err != nil {
			return 0, 0, fmt.Errorf("decode incomplete action attempt %s: %w", row.Key1, err)
		}
		attempt.Status = api.ActionAttemptUnknown
		attempt.RequiresReconcile = true
		encoded, err := json.Marshal(attempt)
		if err != nil {
			return 0, 0, err
		}
		result, err := queries.QuarantineActionAttemptCAS(ctx, dbgen.QuarantineActionAttemptCASParams{Status: string(api.ActionAttemptUnknown), Data: encoded, Kind: kindAction, Key1: row.Key1, Status_2: string(api.ActionAttemptCreated), Status_3: string(api.ActionAttemptRunning)})
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
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit recovery preparation: %w", err)
	}
	return expiredLeases, quarantinedAttempts, nil
}

func (p *Provider) ListReconcileAttempts(ctx context.Context) ([]api.ActionAttempt, error) {
	rows, err := dbgen.New(p.db).ListReconcileAttemptData(ctx, dbgen.ListReconcileAttemptDataParams{Kind: kindAction, Status: string(api.ActionAttemptUnknown)})
	if err != nil {
		return nil, fmt.Errorf("list reconcile attempts: %w", err)
	}
	attempts := make([]api.ActionAttempt, 0)
	for _, data := range rows {
		var attempt api.ActionAttempt
		if err := json.Unmarshal(data, &attempt); err != nil {
			return nil, fmt.Errorf("decode reconcile attempt: %w", err)
		}
		if attempt.RequiresReconcile || attempt.Status == api.ActionAttemptUnknown {
			attempts = append(attempts, attempt)
		}
	}
	return attempts, nil
}

// ListSucceededActionAttempts exposes the durable anti-replay ledger needed
// when a recovered model generates a fresh call ID for an already completed
// non-idempotent input.
func (p *Provider) ListSucceededActionAttempts(ctx context.Context, runID, taskID string) ([]api.ActionAttempt, error) {
	rows, err := dbgen.New(p.db).ListSucceededActionAttemptData(ctx, dbgen.ListSucceededActionAttemptDataParams{Kind: kindAction, RunID: runID, TaskID: taskID, Status: string(api.ActionAttemptSucceeded)})
	if err != nil {
		return nil, err
	}
	var attempts []api.ActionAttempt
	for _, data := range rows {
		var attempt api.ActionAttempt
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

func (p *Provider) ResolveReconcileAttempt(ctx context.Context, attemptID string, status api.ActionAttemptStatus, externalResultRef string) error {
	switch status {
	case api.ActionAttemptSucceeded, api.ActionAttemptFailed, api.ActionAttemptCancelled:
	default:
		return fmt.Errorf("invalid reconciled action status %q", status)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := dbgen.New(tx)
	data, err := queries.GetReconcileAttemptData(ctx, dbgen.GetReconcileAttemptDataParams{Kind: kindAction, Key1: attemptID, Status: string(api.ActionAttemptUnknown)})
	if err != nil {
		return fmt.Errorf("load reconcile attempt %s: %w", attemptID, err)
	}
	var attempt api.ActionAttempt
	if err := json.Unmarshal(data, &attempt); err != nil {
		return err
	}
	attempt.Status = status
	attempt.RequiresReconcile = false
	attempt.ExternalResultRef = externalResultRef
	encoded, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	result, err := queries.ResolveReconcileAttemptCAS(ctx, dbgen.ResolveReconcileAttemptCASParams{Status: string(status), Data: encoded, Kind: kindAction, Key1: attemptID, Status_2: string(api.ActionAttemptUnknown)})
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("reconcile attempt %s changed concurrently", attemptID)
	}
	return tx.Commit()
}
