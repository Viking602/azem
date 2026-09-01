package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Viking602/azem/internal/agentruntime"
)

var _ agentruntime.RunExecutionRepository = (*Provider)(nil)

func (p *Provider) CreateRunExecution(ctx context.Context, run agentruntime.Run, task agentruntime.Task, envelope agentruntime.TaskEnvelope, binding agentruntime.ExecutionBinding) error {
	if run.ID == "" || task.ID == "" || task.RunID != run.ID || envelope.ID == "" || envelope.RunID != run.ID || envelope.TaskID != task.ID || binding.RunID != run.ID {
		return fmt.Errorf("invalid run execution aggregate")
	}
	if err := validateExecutionBinding(binding); err != nil {
		return err
	}
	if binding.Version != 0 {
		return agentruntime.ErrConflict
	}
	manifest, err := json.Marshal(binding.Manifest)
	if err != nil {
		return fmt.Errorf("encode execution manifest: %w", err)
	}
	inline, digest := preparePayload(manifest)
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin run execution transaction: %w", err)
	}
	work := &unitOfWork{db: p.db, tx: tx, blobs: p.Blobs()}
	defer func() {
		if !work.closed {
			_ = work.Rollback(context.Background())
		}
	}()
	if err := work.SaveRun(ctx, run); err != nil {
		return err
	}
	if err := work.SaveTask(ctx, task); err != nil {
		return err
	}
	if err := work.QueueEnvelope(ctx, envelope); err != nil {
		return err
	}
	var nowNanos int64
	if err := tx.QueryRowContext(ctx, `SELECT CAST((julianday('now') - 2440587.5) * 86400000000000 AS INTEGER)`).Scan(&nowNanos); err != nil {
		return fmt.Errorf("read run execution time: %w", err)
	}
	work.stagePayload(digest, manifest)
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_execution_bindings(execution_id,session_id,run_id,stable_id,agent_id,kind,segment,manifest_inline,manifest_digest,profile_hash,state,version,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,1,?)`,
		binding.ExecutionID, binding.SessionID, binding.RunID, binding.StableID, binding.AgentID, binding.Kind, binding.Segment, inline, digest, binding.ProfileHash, binding.State, nowNanos); err != nil {
		if isUniqueConstraint(err) {
			return agentruntime.ErrConflict
		}
		return fmt.Errorf("save initial execution binding: %w", err)
	}
	return work.Commit(ctx)
}
