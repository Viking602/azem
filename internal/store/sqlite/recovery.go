package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

	type leaseRow struct {
		id      string
		version uint64
		data    []byte
	}
	leaseRows := make([]leaseRow, 0)
	rows, err := tx.QueryContext(ctx, `SELECT id,version,data FROM leases WHERE status=?`, string(api.LeaseStatusActive))
	if err != nil {
		return 0, 0, fmt.Errorf("list expired leases: %w", err)
	}
	for rows.Next() {
		var row leaseRow
		if err := rows.Scan(&row.id, &row.version, &row.data); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan expired lease: %w", err)
		}
		leaseRows = append(leaseRows, row)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	for _, row := range leaseRows {
		var lease api.TaskExecutionLease
		if err := json.Unmarshal(row.data, &lease); err != nil {
			return 0, 0, fmt.Errorf("decode expired lease %s: %w", row.id, err)
		}
		lease.Status = api.LeaseStatusExpired
		encoded, err := json.Marshal(lease)
		if err != nil {
			return 0, 0, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE leases SET status=?,data=? WHERE id=? AND version=? AND status=?`, string(api.LeaseStatusExpired), encoded, row.id, row.version, string(api.LeaseStatusActive))
		if err != nil {
			return 0, 0, fmt.Errorf("expire lease %s: %w", row.id, err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, 0, err
		}
		expiredLeases += changed
	}

	type attemptRow struct {
		id   string
		data []byte
	}
	attemptRows := make([]attemptRow, 0)
	rows, err = tx.QueryContext(ctx, `SELECT key1,data FROM records WHERE kind=? AND status IN (?,?)`, kindAction, string(api.ActionAttemptCreated), string(api.ActionAttemptRunning))
	if err != nil {
		return 0, 0, fmt.Errorf("list incomplete action attempts: %w", err)
	}
	for rows.Next() {
		var row attemptRow
		if err := rows.Scan(&row.id, &row.data); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan incomplete action attempt: %w", err)
		}
		attemptRows = append(attemptRows, row)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	for _, row := range attemptRows {
		var attempt api.ActionAttempt
		if err := json.Unmarshal(row.data, &attempt); err != nil {
			return 0, 0, fmt.Errorf("decode incomplete action attempt %s: %w", row.id, err)
		}
		attempt.Status = api.ActionAttemptUnknown
		attempt.RequiresReconcile = true
		encoded, err := json.Marshal(attempt)
		if err != nil {
			return 0, 0, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE records SET status=?,data=? WHERE kind=? AND key1=? AND status IN (?,?)`, string(api.ActionAttemptUnknown), encoded, kindAction, row.id, string(api.ActionAttemptCreated), string(api.ActionAttemptRunning))
		if err != nil {
			return 0, 0, fmt.Errorf("quarantine action attempt %s: %w", row.id, err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, 0, err
		}
		quarantinedAttempts += changed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE provider_requests SET status='unknown' WHERE status='started'`); err != nil {
		return 0, 0, fmt.Errorf("quarantine incomplete provider requests: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit recovery preparation: %w", err)
	}
	return expiredLeases, quarantinedAttempts, nil
}

func (p *Provider) ListReconcileAttempts(ctx context.Context) ([]api.ActionAttempt, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT data FROM records WHERE kind=? AND status=? ORDER BY key1`, kindAction, string(api.ActionAttemptUnknown))
	if err != nil {
		return nil, fmt.Errorf("list reconcile attempts: %w", err)
	}
	defer rows.Close()
	attempts := make([]api.ActionAttempt, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var attempt api.ActionAttempt
		if err := json.Unmarshal(data, &attempt); err != nil {
			return nil, fmt.Errorf("decode reconcile attempt: %w", err)
		}
		if attempt.RequiresReconcile || attempt.Status == api.ActionAttemptUnknown {
			attempts = append(attempts, attempt)
		}
	}
	return attempts, rows.Err()
}

// ListSucceededActionAttempts exposes the durable anti-replay ledger needed
// when a recovered model generates a fresh call ID for an already completed
// non-idempotent input.
func (p *Provider) ListSucceededActionAttempts(ctx context.Context, runID, taskID string) ([]api.ActionAttempt, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT data FROM records WHERE kind=? AND run_id=? AND task_id=? AND status=? ORDER BY key1`,
		kindAction, runID, taskID, string(api.ActionAttemptSucceeded))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []api.ActionAttempt
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var attempt api.ActionAttempt
		if err := json.Unmarshal(data, &attempt); err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, rows.Err()
}

func (p *Provider) RecordToolCallCharge(ctx context.Context, runID, taskID, callID, toolName, inputHash string) (bool, error) {
	result, err := p.db.ExecContext(ctx, `INSERT OR IGNORE INTO tool_call_charges(run_id,task_id,call_id,tool_name,input_hash,created_at)
		VALUES(?,?,?,?,?,?)`, runID, taskID, callID, toolName, inputHash, time.Now().UTC().UnixNano())
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 1 {
		return changed == 1, err
	}
	var recordedTool, recordedHash string
	if err := p.db.QueryRowContext(ctx, `SELECT tool_name,input_hash FROM tool_call_charges WHERE run_id=? AND task_id=? AND call_id=?`,
		runID, taskID, callID).Scan(&recordedTool, &recordedHash); err != nil {
		return false, err
	}
	if recordedTool != toolName || recordedHash != inputHash {
		return false, fmt.Errorf("tool call %s was already charged with different input", callID)
	}
	return false, nil
}

func (p *Provider) CountToolCallCharges(ctx context.Context, runID, taskID string) (int, error) {
	var count int
	err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tool_call_charges WHERE run_id=? AND task_id=?`, runID, taskID).Scan(&count)
	return count, err
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
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM records WHERE kind=? AND key1=? AND status=?`, kindAction, attemptID, string(api.ActionAttemptUnknown)).Scan(&data); err != nil {
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
	result, err := tx.ExecContext(ctx, `UPDATE records SET status=?,data=? WHERE kind=? AND key1=? AND status=?`, string(status), encoded, kindAction, attemptID, string(api.ActionAttemptUnknown))
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
