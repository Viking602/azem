package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
)

var _ agentruntime.ExecutionBindingRepository = (*Provider)(nil)

const executionBindingSelectColumns = `execution_id,session_id,run_id,stable_id,agent_id,kind,segment,manifest_inline,manifest_digest,profile_hash,state,version,updated_at`

func (p *Provider) SaveExecutionBinding(ctx context.Context, binding agentruntime.ExecutionBinding, expectedVersion uint64) (agentruntime.ExecutionBinding, error) {
	if err := validateExecutionBinding(binding); err != nil {
		return agentruntime.ExecutionBinding{}, err
	}
	payload, err := json.Marshal(binding.Manifest)
	if err != nil {
		return agentruntime.ExecutionBinding{}, fmt.Errorf("encode execution manifest: %w", err)
	}
	inline, digest := preparePayload(payload)
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentruntime.ExecutionBinding{}, fmt.Errorf("begin execution binding transaction: %w", err)
	}
	work := &unitOfWork{db: p.db, tx: tx, blobs: p.Blobs()}
	defer func() {
		if !work.closed {
			_ = work.Rollback(context.Background())
		}
	}()
	var (
		currentVersion uint64
		currentSession string
		currentRun     string
		currentStable  string
		currentAgent   string
		currentKind    string
		currentSegment int
		currentInline  []byte
		currentDigest  string
		currentProfile string
		currentState   string
	)
	err = tx.QueryRowContext(ctx, `SELECT version,session_id,run_id,stable_id,agent_id,kind,segment,manifest_inline,manifest_digest,profile_hash,state FROM agent_execution_bindings WHERE execution_id=?`, binding.ExecutionID).
		Scan(&currentVersion, &currentSession, &currentRun, &currentStable, &currentAgent, &currentKind, &currentSegment, &currentInline, &currentDigest, &currentProfile, &currentState)
	switch {
	case errors.Is(err, sql.ErrNoRows) && expectedVersion != 0:
		return agentruntime.ExecutionBinding{}, agentruntime.ErrConflict
	case errors.Is(err, sql.ErrNoRows):
		currentVersion = 0
	case err != nil:
		return agentruntime.ExecutionBinding{}, fmt.Errorf("load execution binding version: %w", err)
	case currentVersion != expectedVersion:
		return agentruntime.ExecutionBinding{}, agentruntime.ErrConflict
	default:
		if currentSession != binding.SessionID || currentRun != binding.RunID || currentStable != binding.StableID ||
			currentAgent != binding.AgentID || currentKind != binding.Kind || currentSegment != binding.Segment {
			return agentruntime.ExecutionBinding{}, fmt.Errorf("execution binding identity is immutable: %w", agentruntime.ErrConflict)
		}
		if agentruntime.ExecutionBindingState(currentState) != agentruntime.ExecutionBindingPending {
			currentPayload, loadErr := loadPayload(ctx, p.Blobs(), currentInline, currentDigest)
			if loadErr != nil {
				return agentruntime.ExecutionBinding{}, fmt.Errorf("load immutable execution manifest: %w", loadErr)
			}
			if currentProfile != binding.ProfileHash || !bytes.Equal(currentPayload, payload) {
				return agentruntime.ExecutionBinding{}, fmt.Errorf("execution manifest is immutable after start: %w", agentruntime.ErrConflict)
			}
		}
	}
	var nowNanos int64
	if err := tx.QueryRowContext(ctx, `SELECT CAST((julianday('now') - 2440587.5) * 86400000000000 AS INTEGER)`).Scan(&nowNanos); err != nil {
		return agentruntime.ExecutionBinding{}, fmt.Errorf("read execution binding time: %w", err)
	}
	binding.Version = expectedVersion + 1
	binding.UpdatedAt = time.Unix(0, nowNanos).UTC()
	work.stagePayload(digest, payload)
	if currentDigest != "" && currentDigest != digest {
		work.stageObsoletePayload(currentDigest)
	}
	if expectedVersion == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_execution_bindings(execution_id,session_id,run_id,stable_id,agent_id,kind,segment,manifest_inline,manifest_digest,profile_hash,state,version,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			binding.ExecutionID, binding.SessionID, binding.RunID, binding.StableID, binding.AgentID, binding.Kind, binding.Segment, inline, digest, binding.ProfileHash, binding.State, binding.Version, binding.UpdatedAt.UnixNano())
	} else {
		result, updateErr := tx.ExecContext(ctx, `UPDATE agent_execution_bindings SET manifest_inline=?,manifest_digest=?,profile_hash=?,state=?,version=?,updated_at=? WHERE execution_id=? AND version=?`,
			inline, digest, binding.ProfileHash, binding.State, binding.Version, binding.UpdatedAt.UnixNano(), binding.ExecutionID, expectedVersion)
		if updateErr == nil {
			var changed int64
			changed, updateErr = result.RowsAffected()
			if updateErr == nil && changed != 1 {
				updateErr = agentruntime.ErrConflict
			}
		}
		err = updateErr
	}
	if err != nil {
		if isUniqueConstraint(err) {
			return agentruntime.ExecutionBinding{}, agentruntime.ErrConflict
		}
		return agentruntime.ExecutionBinding{}, fmt.Errorf("save execution binding: %w", err)
	}
	if err := work.Commit(ctx); err != nil {
		return agentruntime.ExecutionBinding{}, err
	}
	return cloneExecutionBinding(binding), nil
}

func (p *Provider) LoadExecutionBinding(ctx context.Context, executionID string) (agentruntime.ExecutionBinding, error) {
	return p.loadExecutionBindingRow(ctx, p.db.QueryRowContext(ctx, `SELECT `+executionBindingSelectColumns+` FROM agent_execution_bindings WHERE execution_id=?`, executionID))
}

func (p *Provider) LoadLatestExecutionBinding(ctx context.Context, runID, agentID, kind string) (agentruntime.ExecutionBinding, error) {
	return p.loadExecutionBindingRow(ctx, p.db.QueryRowContext(ctx, `SELECT `+executionBindingSelectColumns+` FROM agent_execution_bindings WHERE run_id=? AND agent_id=? AND kind=? ORDER BY segment DESC LIMIT 1`, runID, agentID, kind))
}

func (p *Provider) ListExecutionBindings(ctx context.Context, states []agentruntime.ExecutionBindingState) ([]agentruntime.ExecutionBinding, error) {
	query := `SELECT ` + executionBindingSelectColumns + ` FROM agent_execution_bindings`
	arguments := make([]any, 0, len(states))
	if len(states) > 0 {
		placeholders := make([]string, len(states))
		for index, state := range states {
			placeholders[index] = "?"
			arguments = append(arguments, state)
		}
		query += ` WHERE state IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY updated_at,execution_id`
	rows, err := p.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list execution bindings: %w", err)
	}
	defer rows.Close()
	bindings := make([]agentruntime.ExecutionBinding, 0)
	for rows.Next() {
		binding, err := p.loadExecutionBindingScanner(ctx, rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate execution bindings: %w", err)
	}
	return bindings, nil
}

type executionBindingScanner interface {
	Scan(...any) error
}

func (p *Provider) loadExecutionBindingRow(ctx context.Context, row *sql.Row) (agentruntime.ExecutionBinding, error) {
	return p.loadExecutionBindingScanner(ctx, row)
}

func (p *Provider) loadExecutionBindingScanner(ctx context.Context, scanner executionBindingScanner) (agentruntime.ExecutionBinding, error) {
	var (
		binding      agentruntime.ExecutionBinding
		inline       []byte
		digest       string
		state        string
		updatedNanos int64
	)
	if err := scanner.Scan(&binding.ExecutionID, &binding.SessionID, &binding.RunID, &binding.StableID, &binding.AgentID, &binding.Kind, &binding.Segment, &inline, &digest, &binding.ProfileHash, &state, &binding.Version, &updatedNanos); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agentruntime.ExecutionBinding{}, agentruntime.ErrNotFound
		}
		return agentruntime.ExecutionBinding{}, fmt.Errorf("load execution binding: %w", err)
	}
	payload, err := loadPayload(ctx, p.Blobs(), inline, digest)
	if err != nil {
		return agentruntime.ExecutionBinding{}, fmt.Errorf("load execution manifest %q: %w", binding.ExecutionID, err)
	}
	if err := json.Unmarshal(payload, &binding.Manifest); err != nil {
		return agentruntime.ExecutionBinding{}, fmt.Errorf("decode execution manifest %q: %w", binding.ExecutionID, err)
	}
	binding.State = agentruntime.ExecutionBindingState(state)
	binding.UpdatedAt = time.Unix(0, updatedNanos).UTC()
	if err := validateExecutionBinding(binding); err != nil {
		return agentruntime.ExecutionBinding{}, fmt.Errorf("validate execution binding %q: %w", binding.ExecutionID, err)
	}
	return cloneExecutionBinding(binding), nil
}

func validateExecutionBinding(binding agentruntime.ExecutionBinding) error {
	if strings.TrimSpace(binding.ExecutionID) == "" || strings.TrimSpace(binding.RunID) == "" ||
		strings.TrimSpace(binding.StableID) == "" || strings.TrimSpace(binding.AgentID) == "" ||
		strings.TrimSpace(binding.Kind) == "" || binding.Segment < 0 ||
		strings.TrimSpace(binding.ProfileHash) == "" || !validExecutionBindingState(binding.State) {
		return fmt.Errorf("invalid execution binding")
	}
	expectedID, err := agentruntime.ExecutionID(binding.Kind, binding.StableID, binding.Segment)
	if err != nil || binding.ExecutionID != expectedID {
		return fmt.Errorf("execution binding has invalid stable identity")
	}
	manifest := binding.Manifest
	if manifest.Version != agentruntime.ExecutionManifestVersion || manifest.RunID != binding.RunID ||
		manifest.StableID != binding.StableID || manifest.AgentID != binding.AgentID ||
		manifest.Kind != binding.Kind || manifest.Segment != binding.Segment ||
		manifest.ProfileHash != binding.ProfileHash || manifest.SessionID != binding.SessionID {
		return fmt.Errorf("execution binding disagrees with manifest")
	}
	return nil
}

func validExecutionBindingState(state agentruntime.ExecutionBindingState) bool {
	switch state {
	case agentruntime.ExecutionBindingPending, agentruntime.ExecutionBindingRunning,
		agentruntime.ExecutionBindingSuspended, agentruntime.ExecutionBindingReconcileRequired,
		agentruntime.ExecutionBindingCompleted, agentruntime.ExecutionBindingFailed,
		agentruntime.ExecutionBindingCancelled:
		return true
	default:
		return false
	}
}

func cloneExecutionBinding(binding agentruntime.ExecutionBinding) agentruntime.ExecutionBinding {
	payload, err := json.Marshal(binding)
	if err != nil {
		panic(err)
	}
	var cloned agentruntime.ExecutionBinding
	if err := json.Unmarshal(payload, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "constraint failed")
}
