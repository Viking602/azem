package securityscan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type SQLStore struct {
	db *sql.DB
}

func NewSQLStore(db *sql.DB) (*SQLStore, error) {
	if db == nil {
		return nil, fmt.Errorf("security scan: database is nil")
	}
	return &SQLStore{db: db}, nil
}

type persistedScanRoutes struct {
	Audit    Route `json:"audit"`
	Reducer  Route `json:"reducer"`
	Fixer    Route `json:"fixer"`
	Verifier Route `json:"verifier"`
}

func scanRoutes(scan Scan) persistedScanRoutes {
	reducer, fixer, verifier := scan.ReducerRoute, scan.FixerRoute, scan.VerifierRoute
	if reducer.Provider == "" {
		reducer = scan.Route
	}
	if fixer.Provider == "" {
		fixer = scan.Route
	}
	if verifier.Provider == "" {
		verifier = fixer
	}
	return persistedScanRoutes{Audit: scan.Route, Reducer: reducer, Fixer: fixer, Verifier: verifier}
}

func (s *SQLStore) Close() error { return nil }

func encodeJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("security scan: encode persistence payload: %w", err)
	}
	return encoded, nil
}

func unixMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func timeFromMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func (s *SQLStore) CreateScan(ctx context.Context, scan Scan) error {
	route, err := encodeJSON(scanRoutes(scan))
	if err != nil {
		return err
	}
	budget, err := encodeJSON(scan.Budget)
	if err != nil {
		return err
	}
	deep, err := encodeJSON(scan.Deep)
	if err != nil {
		return err
	}
	target, err := encodeJSON(scan.Target)
	if err != nil {
		return err
	}
	knowledge, err := encodeJSON(scan.Knowledge)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO security_scans (
			id,project_id,requested_session_id,root_run_id,parent_scan_id,
			target_id,target_kind,target_path,target_snapshot_digest,mode,status,phase,completeness,
			route_json,budget_json,deep_json,target_json,knowledge_json,user_context,
			workflow_version,contract_version,output_dir,failure_message,blocking_reason,warning,
			input_tokens,cached_input_tokens,output_tokens,estimated_cost_usd,
			created_at,started_at,completed_at,updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		scan.ID, scan.ProjectID, scan.RequestedBySessionID, scan.RootRunID, scan.ParentScanID,
		scan.Target.TargetID, scan.Target.Kind, scan.Target.Repository, scan.Target.SnapshotDigest,
		scan.Mode, scan.Status, scan.Phase, scan.Completeness,
		route, budget, deep, target, knowledge, scan.UserContext,
		scan.WorkflowVersion, scan.ContractVersion, scan.OutputDirectory, scan.FailureMessage, scan.BlockingReason, scan.Warning,
		scan.InputTokens, scan.CachedInputTokens, scan.OutputTokens, scan.EstimatedCostUSD,
		unixMillis(scan.CreatedAt), unixMillis(scan.StartedAt), unixMillis(scan.CompletedAt), unixMillis(scan.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("security scan: create scan: %w", err)
	}
	return nil
}

func (s *SQLStore) UpdateScan(ctx context.Context, scan Scan) error {
	route, err := encodeJSON(scanRoutes(scan))
	if err != nil {
		return err
	}
	budget, err := encodeJSON(scan.Budget)
	if err != nil {
		return err
	}
	deep, err := encodeJSON(scan.Deep)
	if err != nil {
		return err
	}
	target, err := encodeJSON(scan.Target)
	if err != nil {
		return err
	}
	knowledge, err := encodeJSON(scan.Knowledge)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE security_scans SET
			project_id=?,requested_session_id=?,root_run_id=?,parent_scan_id=?,
			target_id=?,target_kind=?,target_path=?,target_snapshot_digest=?,mode=?,status=?,phase=?,completeness=?,
			route_json=?,budget_json=?,deep_json=?,target_json=?,knowledge_json=?,user_context=?,
			workflow_version=?,contract_version=?,output_dir=?,failure_message=?,blocking_reason=?,warning=?,
			input_tokens=?,cached_input_tokens=?,output_tokens=?,estimated_cost_usd=?,
			started_at=?,completed_at=?,updated_at=?
		WHERE id=? AND (status NOT IN ('complete','failed','canceled') OR status=?)`,
		scan.ProjectID, scan.RequestedBySessionID, scan.RootRunID, scan.ParentScanID,
		scan.Target.TargetID, scan.Target.Kind, scan.Target.Repository, scan.Target.SnapshotDigest,
		scan.Mode, scan.Status, scan.Phase, scan.Completeness,
		route, budget, deep, target, knowledge, scan.UserContext,
		scan.WorkflowVersion, scan.ContractVersion, scan.OutputDirectory, scan.FailureMessage, scan.BlockingReason, scan.Warning,
		scan.InputTokens, scan.CachedInputTokens, scan.OutputTokens, scan.EstimatedCostUSD,
		unixMillis(scan.StartedAt), unixMillis(scan.CompletedAt), unixMillis(scan.UpdatedAt), scan.ID, scan.Status,
	)
	if err != nil {
		return fmt.Errorf("security scan: update scan: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		var exists int
		if lookupErr := s.db.QueryRowContext(ctx, `SELECT 1 FROM security_scans WHERE id=?`, scan.ID).Scan(&exists); errors.Is(lookupErr, sql.ErrNoRows) {
			return ErrNotFound
		} else if lookupErr != nil {
			return lookupErr
		}
		return ErrTerminalState
	}
	return nil
}

func (s *SQLStore) AddUsage(ctx context.Context, scanID string, usage ExecutionResult) error {
	result, err := s.db.ExecContext(ctx, `UPDATE security_scans SET
		input_tokens=input_tokens+?,cached_input_tokens=cached_input_tokens+?,
		output_tokens=output_tokens+?,estimated_cost_usd=estimated_cost_usd+?
		WHERE id=? AND status NOT IN ('complete','failed','canceled')`,
		usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.EstimatedCostUSD, scanID)
	if err != nil {
		return fmt.Errorf("security scan: persist usage: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrTerminalState
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanScanRow(row rowScanner) (Scan, error) {
	var scan Scan
	var routeJSON, budgetJSON, deepJSON, targetJSON, knowledgeJSON []byte
	var targetKind string
	var mode, status, phase, completeness string
	var targetID, targetPath, targetDigest string
	var createdAt, startedAt, completedAt, updatedAt int64
	err := row.Scan(
		&scan.ID, &scan.ProjectID, &scan.RequestedBySessionID, &scan.RootRunID, &scan.ParentScanID,
		&targetID, &targetKind, &targetPath, &targetDigest, &mode, &status, &phase, &completeness,
		&routeJSON, &budgetJSON, &deepJSON, &targetJSON, &knowledgeJSON, &scan.UserContext,
		&scan.WorkflowVersion, &scan.ContractVersion, &scan.OutputDirectory,
		&scan.FailureMessage, &scan.BlockingReason, &scan.Warning,
		&scan.InputTokens, &scan.CachedInputTokens, &scan.OutputTokens, &scan.EstimatedCostUSD,
		&createdAt, &startedAt, &completedAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Scan{}, ErrNotFound
	}
	if err != nil {
		return Scan{}, err
	}
	var routes persistedScanRoutes
	if err := json.Unmarshal(routeJSON, &routes); err != nil {
		return Scan{}, fmt.Errorf("security scan: decode routes: %w", err)
	}
	if routes.Audit.Provider == "" {
		if err := json.Unmarshal(routeJSON, &routes.Audit); err != nil {
			return Scan{}, fmt.Errorf("security scan: decode legacy route: %w", err)
		}
	}
	scan.Route, scan.ReducerRoute, scan.FixerRoute, scan.VerifierRoute = routes.Audit, routes.Reducer, routes.Fixer, routes.Verifier
	if err := json.Unmarshal(budgetJSON, &scan.Budget); err != nil {
		return Scan{}, fmt.Errorf("security scan: decode budget: %w", err)
	}
	if err := json.Unmarshal(deepJSON, &scan.Deep); err != nil {
		return Scan{}, fmt.Errorf("security scan: decode deep options: %w", err)
	}
	if err := json.Unmarshal(targetJSON, &scan.Target); err != nil {
		return Scan{}, fmt.Errorf("security scan: decode target: %w", err)
	}
	if err := json.Unmarshal(knowledgeJSON, &scan.Knowledge); err != nil {
		return Scan{}, fmt.Errorf("security scan: decode knowledge paths: %w", err)
	}
	if scan.Target.TargetID == "" {
		scan.Target.TargetID, scan.Target.Kind, scan.Target.Repository, scan.Target.SnapshotDigest = targetID, TargetKind(targetKind), targetPath, targetDigest
	}
	scan.Mode, scan.Status, scan.Phase, scan.Completeness = Mode(mode), Status(status), Phase(phase), Completeness(completeness)
	scan.CreatedAt, scan.StartedAt, scan.CompletedAt, scan.UpdatedAt = timeFromMillis(createdAt), timeFromMillis(startedAt), timeFromMillis(completedAt), timeFromMillis(updatedAt)
	return scan, nil
}

const scanColumns = `
	id,project_id,requested_session_id,root_run_id,parent_scan_id,
	target_id,target_kind,target_path,target_snapshot_digest,mode,status,phase,completeness,
	route_json,budget_json,deep_json,target_json,knowledge_json,user_context,
	workflow_version,contract_version,output_dir,failure_message,blocking_reason,warning,
	input_tokens,cached_input_tokens,output_tokens,estimated_cost_usd,
	created_at,started_at,completed_at,updated_at`

func (s *SQLStore) Scan(ctx context.Context, id string) (Scan, error) {
	scan, err := scanScanRow(s.db.QueryRowContext(ctx, `SELECT `+scanColumns+` FROM security_scans WHERE id=?`, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Scan{}, fmt.Errorf("security scan: load scan: %w", err)
	}
	return scan, err
}

func (s *SQLStore) ListScans(ctx context.Context, projectID string, limit int) ([]Scan, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	query := `SELECT ` + scanColumns + ` FROM security_scans`
	arguments := []any{}
	if projectID != "" {
		query += ` WHERE project_id=?`
		arguments = append(arguments, projectID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("security scan: list scans: %w", err)
	}
	defer rows.Close()
	var scans []Scan
	for rows.Next() {
		scan, scanErr := scanScanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("security scan: scan list row: %w", scanErr)
		}
		scans = append(scans, scan)
	}
	return scans, rows.Err()
}

func (s *SQLStore) ActiveDeepScan(ctx context.Context, projectID, targetID, snapshotDigest string) (Scan, error) {
	scan, err := scanScanRow(s.db.QueryRowContext(ctx, `SELECT `+scanColumns+` FROM security_scans
		WHERE project_id=? AND target_id=? AND target_snapshot_digest=? AND mode='deep' AND status IN ('queued','running','blocked')
		ORDER BY created_at DESC LIMIT 1`, projectID, targetID, snapshotDigest))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Scan{}, fmt.Errorf("security scan: find active deep scan: %w", err)
	}
	return scan, err
}

func (s *SQLStore) SaveProgress(ctx context.Context, progress Progress) error {
	reviewed, err := encodeJSON(progress.ReviewedPaths)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO security_scan_progress
		(scan_id,phase,files_completed,files_total,reviewed_paths_json,workers_planned,workers_running,workers_done,message,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(scan_id) DO UPDATE SET
		phase=excluded.phase,files_completed=excluded.files_completed,files_total=excluded.files_total,
		reviewed_paths_json=excluded.reviewed_paths_json,workers_planned=excluded.workers_planned,
		workers_running=excluded.workers_running,workers_done=excluded.workers_done,message=excluded.message,updated_at=excluded.updated_at`,
		progress.ScanID, progress.Phase, progress.FilesCompleted, progress.FilesTotal, reviewed,
		progress.WorkersPlanned, progress.WorkersRunning, progress.WorkersDone, progress.Message, unixMillis(progress.UpdatedAt))
	if err != nil {
		return fmt.Errorf("security scan: save progress: %w", err)
	}
	return nil
}

func (s *SQLStore) Progress(ctx context.Context, scanID string) (Progress, error) {
	var progress Progress
	var phase string
	var reviewed []byte
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT scan_id,phase,files_completed,files_total,reviewed_paths_json,
		workers_planned,workers_running,workers_done,message,updated_at FROM security_scan_progress WHERE scan_id=?`, scanID).Scan(
		&progress.ScanID, &phase, &progress.FilesCompleted, &progress.FilesTotal, &reviewed,
		&progress.WorkersPlanned, &progress.WorkersRunning, &progress.WorkersDone, &progress.Message, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, fmt.Errorf("security scan: load progress: %w", err)
	}
	if err := json.Unmarshal(reviewed, &progress.ReviewedPaths); err != nil {
		return Progress{}, fmt.Errorf("security scan: decode reviewed paths: %w", err)
	}
	progress.Phase, progress.UpdatedAt = Phase(phase), timeFromMillis(updated)
	return progress, nil
}

func (s *SQLStore) CreateWorker(ctx context.Context, worker Worker) error {
	route, err := encodeJSON(worker.Route)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO security_scan_workers
		(id,scan_id,run_id,kind,status,sequence,attempt,completion_sequence,route_json,result_path,error,started_at,completed_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, worker.ID, worker.ScanID, worker.RunID, worker.Kind, worker.Status,
		worker.Sequence, worker.Attempt, worker.CompletionSequence, route, worker.ResultPath, worker.Error,
		unixMillis(worker.StartedAt), unixMillis(worker.CompletedAt), unixMillis(worker.UpdatedAt))
	if err != nil {
		return fmt.Errorf("security scan: create worker: %w", err)
	}
	return nil
}

func (s *SQLStore) UpdateWorker(ctx context.Context, worker Worker) error {
	route, err := encodeJSON(worker.Route)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE security_scan_workers SET
		run_id=?,kind=?,status=?,sequence=?,attempt=?,completion_sequence=?,route_json=?,result_path=?,error=?,started_at=?,completed_at=?,updated_at=? WHERE id=?`,
		worker.RunID, worker.Kind, worker.Status, worker.Sequence, worker.Attempt, worker.CompletionSequence,
		route, worker.ResultPath, worker.Error, unixMillis(worker.StartedAt), unixMillis(worker.CompletedAt), unixMillis(worker.UpdatedAt), worker.ID)
	if err != nil {
		return fmt.Errorf("security scan: update worker: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) CompleteWorker(ctx context.Context, worker Worker, usage ExecutionResult) error {
	route, err := encodeJSON(worker.Route)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("security scan: begin worker completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE security_scan_workers SET
		run_id=?,kind=?,status=?,sequence=?,attempt=?,completion_sequence=?,route_json=?,result_path=?,error=?,started_at=?,completed_at=?,updated_at=? WHERE id=?`,
		worker.RunID, worker.Kind, worker.Status, worker.Sequence, worker.Attempt, worker.CompletionSequence,
		route, worker.ResultPath, worker.Error, unixMillis(worker.StartedAt), unixMillis(worker.CompletedAt), unixMillis(worker.UpdatedAt), worker.ID)
	if err != nil {
		return fmt.Errorf("security scan: complete worker: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	result, err = tx.ExecContext(ctx, `UPDATE security_scans SET
		input_tokens=input_tokens+?,cached_input_tokens=cached_input_tokens+?,
		output_tokens=output_tokens+?,estimated_cost_usd=estimated_cost_usd+?
		WHERE id=? AND status NOT IN ('complete','failed','canceled')`,
		usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.EstimatedCostUSD, worker.ScanID)
	if err != nil {
		return fmt.Errorf("security scan: persist completed worker usage: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrTerminalState
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("security scan: commit worker completion: %w", err)
	}
	return nil
}

func scanWorkerRow(row rowScanner) (Worker, error) {
	var worker Worker
	var kind, status string
	var route []byte
	var started, completed, updated int64
	err := row.Scan(&worker.ID, &worker.ScanID, &worker.RunID, &kind, &status, &worker.Sequence, &worker.Attempt,
		&worker.CompletionSequence, &route, &worker.ResultPath, &worker.Error, &started, &completed, &updated)
	if err != nil {
		return Worker{}, err
	}
	if err := json.Unmarshal(route, &worker.Route); err != nil {
		return Worker{}, err
	}
	worker.Kind, worker.Status = WorkerKind(kind), WorkerStatus(status)
	worker.StartedAt, worker.CompletedAt, worker.UpdatedAt = timeFromMillis(started), timeFromMillis(completed), timeFromMillis(updated)
	return worker, nil
}

func (s *SQLStore) Workers(ctx context.Context, scanID string) ([]Worker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,scan_id,run_id,kind,status,sequence,attempt,completion_sequence,
		route_json,result_path,error,started_at,completed_at,updated_at FROM security_scan_workers WHERE scan_id=? ORDER BY sequence,attempt,id`, scanID)
	if err != nil {
		return nil, fmt.Errorf("security scan: list workers: %w", err)
	}
	defer rows.Close()
	var workers []Worker
	for rows.Next() {
		worker, scanErr := scanWorkerRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("security scan: worker row: %w", scanErr)
		}
		workers = append(workers, worker)
	}
	return workers, rows.Err()
}

func (s *SQLStore) SaveCompletion(ctx context.Context, scan Scan, artifacts []Artifact, findings []Finding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("security scan: begin completion: %w", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM security_scan_artifacts WHERE scan_id=?`, scan.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM security_finding_occurrences WHERE scan_id=?`, scan.ID); err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_scan_artifacts
			(scan_id,kind,path,media_type,sha256,byte_size,created_at) VALUES (?,?,?,?,?,?,?)`,
			artifact.ScanID, artifact.Kind, artifact.Path, artifact.MediaType, artifact.SHA256, artifact.Bytes, unixMillis(artifact.CreatedAt)); err != nil {
			return fmt.Errorf("security scan: save artifact: %w", err)
		}
	}
	for _, finding := range findings {
		details, err := encodeJSON(finding)
		if err != nil {
			return err
		}
		now := unixMillis(scan.CompletedAt)
		if now == 0 {
			now = unixMillis(time.Now().UTC())
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_findings
			(id,target_id,fingerprint,rule_id,identity_anchor,identity_instance,first_seen_at,last_seen_at)
			VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET last_seen_at=excluded.last_seen_at`,
			finding.FindingID, scan.Target.TargetID, finding.Fingerprints.Primary, finding.RuleID,
			finding.Identity.Anchor, finding.Identity.Instance, now, now); err != nil {
			return fmt.Errorf("security scan: save finding: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_finding_occurrences
			(id,finding_id,scan_id,severity,confidence,status,title,summary,details_json,created_at)
			VALUES (?,?,?,?,?,'open',?,?,?,?)`, finding.OccurrenceID, finding.FindingID, scan.ID,
			finding.Severity.Level, finding.Confidence.Level, finding.Title, finding.Summary, details, now); err != nil {
			return fmt.Errorf("security scan: save occurrence: %w", err)
		}
		for index, location := range finding.Locations {
			endLine := location.EndLine
			if endLine == 0 {
				endLine = location.StartLine
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO security_finding_locations
				(occurrence_id,sort_order,path,start_line,end_line,role) VALUES (?,?,?,?,?,?)`,
				finding.OccurrenceID, index, location.Path, location.StartLine, endLine, location.Role); err != nil {
				return fmt.Errorf("security scan: save finding location: %w", err)
			}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE security_scans SET status=?,phase=?,completeness=?,failure_message=?,blocking_reason=?,warning=?,
		input_tokens=?,cached_input_tokens=?,output_tokens=?,estimated_cost_usd=?,completed_at=?,updated_at=? WHERE id=? AND status='running'`,
		scan.Status, scan.Phase, scan.Completeness, scan.FailureMessage, scan.BlockingReason, scan.Warning,
		scan.InputTokens, scan.CachedInputTokens, scan.OutputTokens, scan.EstimatedCostUSD,
		unixMillis(scan.CompletedAt), unixMillis(scan.UpdatedAt), scan.ID)
	if err != nil {
		return fmt.Errorf("security scan: complete scan: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrTerminalState
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("security scan: commit completion: %w", err)
	}
	rollback = false
	return nil
}

func (s *SQLStore) Findings(ctx context.Context, scanID string) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT details_json FROM security_finding_occurrences WHERE scan_id=? ORDER BY
		CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,id`, scanID)
	if err != nil {
		return nil, fmt.Errorf("security scan: list findings: %w", err)
	}
	defer rows.Close()
	var findings []Finding
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var finding Finding
		if err := json.Unmarshal(payload, &finding); err != nil {
			return nil, fmt.Errorf("security scan: decode finding: %w", err)
		}
		findings = append(findings, finding)
	}
	return findings, rows.Err()
}

func (s *SQLStore) Finding(ctx context.Context, occurrenceID string) (Finding, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT details_json FROM security_finding_occurrences WHERE id=?`, occurrenceID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Finding{}, ErrNotFound
	}
	if err != nil {
		return Finding{}, fmt.Errorf("security scan: load finding: %w", err)
	}
	var finding Finding
	if err := json.Unmarshal(payload, &finding); err != nil {
		return Finding{}, fmt.Errorf("security scan: decode finding: %w", err)
	}
	return finding, nil
}

func (s *SQLStore) SaveTriage(ctx context.Context, triage Triage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO security_finding_triage
		(occurrence_id,status,close_reason,note,updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(occurrence_id) DO UPDATE SET status=excluded.status,close_reason=excluded.close_reason,note=excluded.note,updated_at=excluded.updated_at`,
		triage.OccurrenceID, triage.Status, triage.CloseReason, triage.Note, unixMillis(triage.UpdatedAt)); err != nil {
		return fmt.Errorf("security scan: save triage: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE security_finding_occurrences SET status=? WHERE id=?`, triage.Status, triage.OccurrenceID)
	if err != nil {
		return fmt.Errorf("security scan: update occurrence triage: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *SQLStore) Triage(ctx context.Context, occurrenceID string) (Triage, error) {
	var triage Triage
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT occurrence_id,status,close_reason,note,updated_at FROM security_finding_triage WHERE occurrence_id=?`, occurrenceID).
		Scan(&triage.OccurrenceID, &triage.Status, &triage.CloseReason, &triage.Note, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Triage{}, ErrNotFound
	}
	if err != nil {
		return Triage{}, fmt.Errorf("security scan: load triage: %w", err)
	}
	triage.UpdatedAt = timeFromMillis(updated)
	return triage, nil
}

func (s *SQLStore) SaveMatch(ctx context.Context, beforeOccurrenceID, afterOccurrenceID, kind string, confidence float64) error {
	if kind != "exact" && kind != "semantic" {
		return fmt.Errorf("security scan: invalid match kind %q", kind)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO security_scan_matches
		(before_occurrence_id,after_occurrence_id,match_kind,confidence,created_at) VALUES (?,?,?,?,?)
		ON CONFLICT(before_occurrence_id,after_occurrence_id) DO UPDATE SET match_kind=excluded.match_kind,confidence=excluded.confidence`,
		beforeOccurrenceID, afterOccurrenceID, kind, confidence, time.Now().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("security scan: save finding match: %w", err)
	}
	return nil
}
func (s *SQLStore) ScanForOccurrence(ctx context.Context, occurrenceID string) (Scan, error) {
	var scanID string
	err := s.db.QueryRowContext(ctx, `SELECT scan_id FROM security_finding_occurrences WHERE id=?`, occurrenceID).Scan(&scanID)
	if errors.Is(err, sql.ErrNoRows) {
		return Scan{}, ErrNotFound
	}
	if err != nil {
		return Scan{}, fmt.Errorf("security scan: resolve occurrence scan: %w", err)
	}
	return s.Scan(ctx, scanID)
}

func (s *SQLStore) SaveRemediation(ctx context.Context, attempt RemediationAttempt) error {
	files, err := encodeJSON(attempt.Files)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO security_remediation_attempts
		(id,occurrence_id,state,version,base_revision,base_snapshot_digest,applied_snapshot_digest,files_json,
		verification,reason,branch,commit_sha,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET state=excluded.state,applied_snapshot_digest=excluded.applied_snapshot_digest,
		files_json=excluded.files_json,verification=excluded.verification,reason=excluded.reason,
		branch=excluded.branch,commit_sha=excluded.commit_sha,updated_at=excluded.updated_at`,
		attempt.ID, attempt.OccurrenceID, attempt.State, attempt.Version, attempt.BaseRevision, attempt.BaseSnapshotDigest,
		attempt.AppliedSnapshotDigest, files, attempt.Verification, attempt.Reason, attempt.Branch, attempt.Commit,
		unixMillis(attempt.CreatedAt), unixMillis(attempt.UpdatedAt))
	if err != nil {
		return fmt.Errorf("security scan: save remediation attempt: %w", err)
	}
	return nil
}
func (s *SQLStore) SavePublication(ctx context.Context, publication Publication) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO security_publications
		(scan_id,occurrence_id,destination,status,external_id,external_url,error,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(scan_id,occurrence_id,destination) DO UPDATE SET
		status=excluded.status,external_id=excluded.external_id,external_url=excluded.external_url,
		error=excluded.error,updated_at=excluded.updated_at`,
		publication.ScanID, publication.OccurrenceID, publication.Destination, publication.Status,
		publication.ExternalID, publication.ExternalURL, publication.Error,
		unixMillis(publication.CreatedAt), unixMillis(publication.UpdatedAt))
	if err != nil {
		return fmt.Errorf("security scan: save publication: %w", err)
	}
	return nil
}

func (s *SQLStore) ClaimPublication(ctx context.Context, publication Publication) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO security_publications
		(scan_id,occurrence_id,destination,status,external_id,external_url,error,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(scan_id,occurrence_id,destination) DO NOTHING`,
		publication.ScanID, publication.OccurrenceID, publication.Destination, publication.Status,
		publication.ExternalID, publication.ExternalURL, publication.Error,
		unixMillis(publication.CreatedAt), unixMillis(publication.UpdatedAt))
	if err != nil {
		return fmt.Errorf("security scan: claim publication: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrPublicationExists
	}
	return nil
}

func (s *SQLStore) ReconcilePublication(ctx context.Context, publication Publication) error {
	result, err := s.db.ExecContext(ctx, `UPDATE security_publications SET
		status=?,external_id=?,external_url=?,error='',updated_at=?
		WHERE scan_id=? AND occurrence_id=? AND destination=? AND status IN ('publishing','failed')`,
		publication.Status, publication.ExternalID, publication.ExternalURL, unixMillis(publication.UpdatedAt),
		publication.ScanID, publication.OccurrenceID, publication.Destination)
	if err != nil {
		return fmt.Errorf("security scan: reconcile publication: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) DeletePublication(ctx context.Context, scanID, occurrenceID, destination string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM security_publications
		WHERE scan_id=? AND occurrence_id=? AND destination=? AND status IN ('publishing','failed')`,
		scanID, occurrenceID, destination)
	if err != nil {
		return fmt.Errorf("security scan: release publication claim: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) PublishedOccurrences(ctx context.Context, scanID, destination string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT occurrence_id FROM security_publications
		WHERE scan_id=? AND destination=? AND status='published'`, scanID, destination)
	if err != nil {
		return nil, fmt.Errorf("security scan: list publications: %w", err)
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var occurrenceID string
		if err := rows.Scan(&occurrenceID); err != nil {
			return nil, err
		}
		result[occurrenceID] = true
	}
	return result, rows.Err()
}
