package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
)

func taskBudget(source *agentruntime.TaskBudget) hyagent.Budget {
	if source == nil {
		return hyagent.Budget{}
	}
	return hyagent.Budget{
		MaxTokens: source.MaxTokens, MaxToolCalls: source.MaxToolCalls,
		MaxSteps: source.MaxSteps, MaxWallClock: source.MaxWallClock,
	}
}

func cloneTaskBudget(source *agentruntime.TaskBudget) *agentruntime.TaskBudget {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}

func executionKind(agentID string, metadata map[string]string) string {
	if strings.TrimSpace(metadata["automation_kind"]) != "" {
		return "automation"
	}
	if agentID != mainAgentID {
		return "subagent"
	}
	return "main"
}

func executionProfileHash(manifest agentruntime.ExecutionManifest) (string, error) {
	manifest.ProfileHash = ""
	return executionValueHash(manifest)
}

func executionValueHash(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode execution profile: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func applyExecutableProfile(manifest *agentruntime.ExecutionManifest, profile agentruntime.ExecutableProfile) {
	manifest.Provider = strings.TrimSpace(profile.Provider)
	manifest.AccountID = strings.TrimSpace(profile.AccountID)
	manifest.RawModel = strings.TrimSpace(profile.RawModel)
	manifest.Model = strings.TrimSpace(profile.Model)
	manifest.Reasoning = strings.TrimSpace(profile.Reasoning)
	manifest.ActiveSkills = append([]string{}, profile.ActiveSkills...)
	manifest.ToolSetHash = strings.TrimSpace(profile.ToolSetHash)
	manifest.ToolProfileHash = strings.TrimSpace(profile.ToolProfileHash)
	manifest.PlanMode = profile.PlanMode
	manifest.ApprovedPlanID = strings.TrimSpace(profile.ApprovedPlanID)
	manifest.DisableSubagents = profile.DisableSubagents
	manifest.StaticIdentity = strings.TrimSpace(profile.StaticIdentity)
	manifest.WorkspaceAnchor = strings.TrimSpace(profile.WorkspaceAnchor)
	manifest.PromptFingerprint = strings.TrimSpace(profile.PromptFingerprint)
	manifest.ToolSchemaFingerprint = strings.TrimSpace(profile.ToolSchemaFingerprint)
}

func validateExecutableProfile(manifest agentruntime.ExecutionManifest) error {
	if manifest.Version != agentruntime.ExecutionManifestVersion ||
		strings.TrimSpace(manifest.Provider) == "" ||
		strings.TrimSpace(manifest.AccountID) == "" ||
		strings.TrimSpace(manifest.RawModel) == "" ||
		strings.TrimSpace(manifest.Model) == "" ||
		strings.TrimSpace(manifest.Reasoning) == "" ||
		manifest.ActiveSkills == nil ||
		strings.TrimSpace(manifest.ToolSetHash) == "" ||
		strings.TrimSpace(manifest.ToolProfileHash) == "" ||
		strings.TrimSpace(manifest.StaticIdentity) == "" ||
		strings.TrimSpace(manifest.WorkspaceAnchor) == "" ||
		strings.TrimSpace(manifest.PromptFingerprint) == "" ||
		strings.TrimSpace(manifest.ToolSchemaFingerprint) == "" {
		return fmt.Errorf("execution manifest has an incomplete executable profile")
	}
	return nil
}

func (s *Service) loadRunAggregate(ctx context.Context, runID string) (agentruntime.Run, agentruntime.Task, agentruntime.TaskEnvelope, error) {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return agentruntime.Run{}, agentruntime.Task{}, agentruntime.TaskEnvelope{}, err
	}
	defer work.Rollback(context.Background())
	run, err := work.Runs().LoadRun(ctx, runID)
	if err != nil {
		return agentruntime.Run{}, agentruntime.Task{}, agentruntime.TaskEnvelope{}, err
	}
	tasks, err := work.Tasks().ListTasks(ctx, runID)
	if err != nil {
		return agentruntime.Run{}, agentruntime.Task{}, agentruntime.TaskEnvelope{}, err
	}
	if len(tasks) == 0 {
		return agentruntime.Run{}, agentruntime.Task{}, agentruntime.TaskEnvelope{}, agentruntime.ErrNotFound
	}
	task := tasks[0]
	for _, candidate := range tasks {
		if candidate.ParentTaskID == "" {
			task = candidate
			break
		}
	}
	envelopes, err := work.MailboxOutbox().ListEnvelopes(ctx, runID)
	if err != nil {
		return agentruntime.Run{}, agentruntime.Task{}, agentruntime.TaskEnvelope{}, err
	}
	var envelope agentruntime.TaskEnvelope
	for _, candidate := range envelopes {
		if candidate.TaskID == task.ID {
			envelope = candidate
			break
		}
	}
	if envelope.ID == "" {
		return agentruntime.Run{}, agentruntime.Task{}, agentruntime.TaskEnvelope{}, agentruntime.ErrNotFound
	}
	if err := work.Commit(ctx); err != nil {
		return agentruntime.Run{}, agentruntime.Task{}, agentruntime.TaskEnvelope{}, err
	}
	return run, task, envelope, nil
}

func (s *Service) markLegacyReconciliation(ctx context.Context, run agentruntime.Run, task agentruntime.Task, reason string) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	now := time.Now().UTC()
	run.Status = agentruntime.RunStatusReconcileRequired
	run.UpdatedAt = now
	if run.Metadata == nil {
		run.Metadata = make(map[string]string)
	}
	run.Metadata["reconciliation_reason"] = strings.TrimSpace(reason)
	task.Status = agentruntime.TaskStatusReconcileRequired
	task.Error = strings.TrimSpace(reason)
	task.UpdatedAt = now
	if err := work.Runs().SaveRun(ctx, run); err != nil {
		return err
	}
	if err := work.Tasks().SaveTask(ctx, task); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func (s *Service) markExecutionReconciliation(ctx context.Context, runID, reason string) error {
	run, task, _, err := s.loadRunAggregate(ctx, runID)
	if err != nil {
		return err
	}
	return s.markLegacyReconciliation(ctx, run, task, reason)
}

func (s *Service) clearRunReconciliation(ctx context.Context, runID, taskID string) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	if err := clearReconciliationInWork(ctx, work, runID, taskID); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func clearReconciliationInWork(ctx context.Context, work agentruntime.UnitOfWork, runID, taskID string) error {
	requiresReconcile := true
	attempts, err := work.ActionAttempts().ListActionAttempts(ctx, agentruntime.ActionAttemptSelector{
		RunID: runID, TaskID: taskID, RequiresReconcile: &requiresReconcile, Limit: 1,
	})
	if err != nil {
		return err
	}
	if len(attempts) != 0 {
		return nil
	}
	run, err := work.Runs().LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	task, err := work.Tasks().LoadTask(ctx, runID, taskID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if run.Status == agentruntime.RunStatusReconcileRequired {
		run.Status = agentruntime.RunStatusRunning
		run.UpdatedAt = now
		if err := work.Runs().SaveRun(ctx, run); err != nil {
			return err
		}
	}
	if task.Status == agentruntime.TaskStatusReconcileRequired {
		task.Status = agentruntime.TaskStatusDispatched
		task.UpdatedAt = now
		if err := work.Tasks().SaveTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) finishExecutionBinding(ctx context.Context, executionID string, state agentruntime.ExecutionBindingState) error {
	for range 3 {
		binding, err := s.store.LoadExecutionBinding(ctx, executionID)
		if err != nil {
			return err
		}
		if binding.State == agentruntime.ExecutionBindingCancelled && state != agentruntime.ExecutionBindingCancelled {
			return nil
		}
		if binding.State == state {
			return nil
		}
		binding.State = state
		if _, err := s.store.SaveExecutionBinding(ctx, binding, binding.Version); err == nil {
			return nil
		} else if !errors.Is(err, agentruntime.ErrConflict) {
			return err
		}
	}
	return agentruntime.ErrConflict
}

func (s *Service) saveRunCompletion(ctx context.Context, run *Run, summary string, failure error, result *hyagent.Result) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	runRecord, err := work.Runs().LoadRun(ctx, run.RunID)
	if err != nil {
		return err
	}
	taskRecord, err := work.Tasks().LoadTask(ctx, run.RunID, run.TaskID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	report := agentruntime.TypedReport{Status: agentruntime.ReportStatusSuccess, Summary: summary}
	runRecord.Status = agentruntime.RunStatusCompleted
	taskRecord.Status = agentruntime.TaskStatusCompleted
	if failure != nil {
		report.Status = agentruntime.ReportStatusFailed
		report.Kind = "agent_error"
		if result != nil && result.Failure != nil {
			report.Kind = string(result.Failure.Kind)
		}
		if report.Summary == "" {
			report.Summary = failure.Error()
		}
		runRecord.Status = agentruntime.RunStatusFailed
		taskRecord.Status = agentruntime.TaskStatusFailed
		taskRecord.Error = failure.Error()
	}
	if result != nil {
		runRecord.Metadata = maps.Clone(runRecord.Metadata)
		if runRecord.Metadata == nil {
			runRecord.Metadata = make(map[string]string, 4)
		}
		runRecord.Metadata["azem.agent_stop_reason"] = string(result.StopReason)
		runRecord.Metadata["azem.agent_tool_calls"] = fmt.Sprint(result.ToolCallsUsed)
		if encodedUsage, encodeErr := json.Marshal(result.Usage); encodeErr != nil {
			return fmt.Errorf("encode terminal agent usage: %w", encodeErr)
		} else {
			runRecord.Metadata["azem.agent_usage"] = string(encodedUsage)
		}
		if result.Failure != nil {
			runRecord.Metadata["azem.agent_failure_kind"] = string(result.Failure.Kind)
		}
	}
	taskRecord.Result = &report
	taskRecord.UpdatedAt = now
	runRecord.UpdatedAt = now
	if err := work.Tasks().SaveTask(ctx, taskRecord); err != nil {
		return err
	}
	if err := work.Runs().SaveRun(ctx, runRecord); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func (s *Service) markRunCancelled(ctx context.Context, runID, taskID string, cause error) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	run, err := work.Runs().LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	if taskID == "" {
		tasks, err := work.Tasks().ListTasks(ctx, runID)
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			return agentruntime.ErrNotFound
		}
		taskID = tasks[0].ID
	}
	task, err := work.Tasks().LoadTask(ctx, runID, taskID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	run.Status = agentruntime.RunStatusCancelled
	run.UpdatedAt = now
	task.Status = agentruntime.TaskStatusCancelled
	task.UpdatedAt = now
	if cause != nil {
		task.Error = cause.Error()
	}
	if err := work.Runs().SaveRun(ctx, run); err != nil {
		return err
	}
	if err := work.Tasks().SaveTask(ctx, task); err != nil {
		return err
	}
	return work.Commit(ctx)
}

// SealExecutionProfile persists provider/tool/profile facts before the first
// durable effect. An already-sealed profile is accepted only when every fact
// hashes identically, which makes response-loss retries idempotent.
func (s *Service) SealExecutionProfile(ctx context.Context, run *Run, profile agentruntime.ExecutableProfile) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	binding, err := s.store.LoadExecutionBinding(ctx, run.ExecutionID)
	if err != nil {
		return err
	}
	candidate := binding
	applyExecutableProfile(&candidate.Manifest, profile)
	candidate.Manifest.Sealed = true
	if err := validateExecutableProfile(candidate.Manifest); err != nil {
		return err
	}
	candidate.Manifest.ProfileHash, err = executionProfileHash(candidate.Manifest)
	if err != nil {
		return err
	}
	candidate.ProfileHash = candidate.Manifest.ProfileHash
	if binding.Manifest.Sealed {
		if candidate.ProfileHash != binding.ProfileHash {
			return fmt.Errorf("execution profile changed after sealing: %w", agentruntime.ErrConflict)
		}
		run.binding = binding
		return nil
	}
	if binding.State != agentruntime.ExecutionBindingPending {
		return fmt.Errorf("execution profile was not sealed before start: %w", agentruntime.ErrConflict)
	}
	saved, err := s.store.SaveExecutionBinding(ctx, candidate, binding.Version)
	if err != nil {
		return err
	}
	run.binding = saved
	return nil
}

func (s *Service) LoadRunExecutionManifest(ctx context.Context, runID string) (agentruntime.ExecutionManifest, error) {
	runRecord, _, _, err := s.loadRunAggregate(ctx, runID)
	if err != nil {
		return agentruntime.ExecutionManifest{}, err
	}
	agentID := strings.TrimSpace(runRecord.Metadata[singleRunMetadataAgentID])
	if agentID == "" {
		agentID = mainAgentID
	}
	binding, err := s.store.LoadLatestExecutionBinding(ctx, runID, agentID, executionKind(agentID, runRecord.Metadata))
	if err != nil {
		return agentruntime.ExecutionManifest{}, err
	}
	if !binding.Manifest.Sealed {
		return agentruntime.ExecutionManifest{}, fmt.Errorf("execution manifest is not sealed: %w", agentruntime.ErrConflict)
	}
	if err := validateExecutableProfile(binding.Manifest); err != nil {
		return agentruntime.ExecutionManifest{}, err
	}
	hash, err := executionProfileHash(binding.Manifest)
	if err != nil {
		return agentruntime.ExecutionManifest{}, err
	}
	if hash != binding.ProfileHash {
		return agentruntime.ExecutionManifest{}, fmt.Errorf("execution manifest profile hash changed: %w", agentruntime.ErrConflict)
	}
	payload, err := json.Marshal(binding.Manifest)
	if err != nil {
		return agentruntime.ExecutionManifest{}, err
	}
	var manifest agentruntime.ExecutionManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return agentruntime.ExecutionManifest{}, err
	}
	return manifest, nil
}

func (s *Service) LoadRun(ctx context.Context, runID string) (agentruntime.Run, error) {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return agentruntime.Run{}, err
	}
	defer work.Rollback(context.Background())
	run, err := work.Runs().LoadRun(ctx, runID)
	if err != nil {
		return agentruntime.Run{}, err
	}
	if err := work.Commit(ctx); err != nil {
		return agentruntime.Run{}, err
	}
	return run, nil
}

func (s *Service) SaveRun(ctx context.Context, run agentruntime.Run) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	if err := work.Runs().SaveRun(ctx, run); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func (s *Service) ListTasks(ctx context.Context, runID string) ([]agentruntime.Task, error) {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer work.Rollback(context.Background())
	tasks, err := work.Tasks().ListTasks(ctx, runID)
	if err != nil {
		return nil, err
	}
	if err := work.Commit(ctx); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Service) SaveTask(ctx context.Context, task agentruntime.Task) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	if err := work.Tasks().SaveTask(ctx, task); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func (s *Service) ListEvents(ctx context.Context, runID string) ([]agentruntime.Event, error) {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer work.Rollback(context.Background())
	events, err := work.Events().ListEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	if err := work.Commit(ctx); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Service) AppendEvent(ctx context.Context, event agentruntime.Event) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	if err := work.Events().AppendEvent(ctx, event); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func (s *Service) ListResourceClaims(ctx context.Context, selector agentruntime.ResourceClaimSelector) ([]agentruntime.ResourceClaim, error) {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer work.Rollback(context.Background())
	claimWork, ok := work.(agentruntime.ResourceClaimUnitOfWork)
	if !ok {
		return nil, fmt.Errorf("resource claim repository is unavailable")
	}
	claims, err := claimWork.ResourceClaims().ListResourceClaims(ctx, selector)
	if err != nil {
		return nil, err
	}
	if err := work.Commit(ctx); err != nil {
		return nil, err
	}
	return claims, nil
}

const runResourceClaimLifetime = 24 * time.Hour

func (s *Service) acquireRunResourceClaims(ctx context.Context, run *Run, specs []agentruntime.ResourceClaimSpec) (agentruntime.ResourceClaimDecision, error) {
	if len(specs) == 0 {
		return agentruntime.ResourceClaimDecision{Acquired: true}, nil
	}
	claims := make([]agentruntime.ResourceClaimSpec, len(specs))
	for index, spec := range specs {
		claims[index] = spec
		if strings.TrimSpace(claims[index].ID) == "" {
			claims[index].ID = fmt.Sprintf("%s:resource:%d", run.ExecutionID, index)
		}
	}
	now := time.Now().UTC()
	work, err := s.store.Begin(ctx)
	if err != nil {
		return agentruntime.ResourceClaimDecision{}, err
	}
	defer work.Rollback(context.Background())
	claimWork, ok := work.(agentruntime.ResourceClaimUnitOfWork)
	if !ok {
		return agentruntime.ResourceClaimDecision{}, fmt.Errorf("resource claim repository is unavailable")
	}
	decision, err := claimWork.ResourceClaims().AcquireResourceClaims(ctx, agentruntime.ResourceClaimRequest{
		RunID: run.RunID, TaskID: run.TaskID, LeaseID: run.ExecutionID, HolderID: run.HolderID,
		Claims: claims, RequestedAt: now, ExpiresAt: now.Add(runResourceClaimLifetime),
	})
	if err != nil || !decision.Acquired {
		return decision, err
	}
	if err := work.Commit(ctx); err != nil {
		return agentruntime.ResourceClaimDecision{}, err
	}
	return decision, nil
}

func (s *Service) releaseRunResourceClaims(ctx context.Context, claims []agentruntime.ResourceClaim) error {
	if len(claims) == 0 {
		return nil
	}
	now := time.Now().UTC()
	transitions := make([]agentruntime.ResourceClaimTransition, len(claims))
	for index, claim := range claims {
		transitions[index] = agentruntime.ResourceClaimTransition{
			ClaimID: claim.ID, ExpectedVersion: claim.Version, To: agentruntime.ResourceClaimReleased, At: now,
		}
	}
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	claimWork, ok := work.(agentruntime.ResourceClaimUnitOfWork)
	if !ok {
		return fmt.Errorf("resource claim repository is unavailable")
	}
	decision, err := claimWork.ResourceClaims().TransitionResourceClaims(ctx, agentruntime.ResourceClaimTransitionRequest{Transitions: transitions})
	if err != nil {
		return err
	}
	if !decision.Acquired {
		return &TaskExecutionUnavailableError{ResourceClaims: decision}
	}
	return work.Commit(ctx)
}

const approvalResumeLifetime = 24 * time.Hour

func approvalRecordIDs(command agentruntime.RequestApprovalCommand) (approvalID, tokenID string, deterministic bool, err error) {
	executionID := strings.TrimSpace(command.Metadata[approvalMetadataExecutionID])
	operationID := strings.TrimSpace(command.Metadata[approvalMetadataOperationID])
	if executionID != "" {
		if operationID == "" {
			return "", "", false, fmt.Errorf("approval execution identity is incomplete")
		}
		sum := sha256.Sum256([]byte(executionID + "\x00" + operationID))
		digest := hex.EncodeToString(sum[:])
		return "approval_" + digest, "resume_" + digest, true, nil
	}
	approvalID, err = newID("approval")
	if err != nil {
		return "", "", false, err
	}
	tokenID, err = newID("resume")
	if err != nil {
		return "", "", false, err
	}
	return approvalID, tokenID, false, nil
}

func validateApprovalIdentity(command agentruntime.RequestApprovalCommand, approval agentruntime.ApprovalRequest, token agentruntime.ResumeToken) error {
	if approval.RunID != command.RunID ||
		approval.TaskID != command.TaskID ||
		approval.ActionID != command.ActionID ||
		approval.Metadata[approvalMetadataExecutionID] != command.Metadata[approvalMetadataExecutionID] ||
		approval.Metadata[approvalMetadataOperationID] != command.Metadata[approvalMetadataOperationID] ||
		approval.Metadata[approvalMetadataScope] != command.Metadata[approvalMetadataScope] ||
		token.RunID != command.RunID ||
		token.TaskID != command.TaskID ||
		token.ApprovalID != approval.ApprovalID {
		return fmt.Errorf("durable approval identity disagrees with operation %s: %w", command.ActionID, agentruntime.ErrConflict)
	}
	return nil
}

func validateExistingApproval(command agentruntime.RequestApprovalCommand, approval agentruntime.ApprovalRequest, token agentruntime.ResumeToken, now time.Time) error {
	if err := validateApprovalIdentity(command, approval, token); err != nil {
		return err
	}
	if approval.Status != "pending" || token.Metadata["status"] != "pending" || !approval.ExpiresAt.After(now) || !token.ExpiresAt.After(now) {
		return fmt.Errorf("durable approval %s is not pending: %w", approval.ApprovalID, agentruntime.ErrInvalidCommand)
	}
	return nil
}

func (s *Service) requestApproval(ctx context.Context, command agentruntime.RequestApprovalCommand) (agentruntime.ApprovalRequest, agentruntime.ResumeToken, error) {
	approvalID, tokenID, deterministic, err := approvalRecordIDs(command)
	if err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
	}
	if deterministic {
		s.approvalMu.Lock()
		defer s.approvalMu.Unlock()
	}
	now := time.Now().UTC()
	approval := agentruntime.ApprovalRequest{
		ApprovalID: approvalID, RunID: command.RunID, TaskID: command.TaskID,
		ActionID: command.ActionID, RequesterAgentID: command.RequesterAgentID,
		Reason: command.Reason, RiskSummary: command.RiskSummary,
		RequestedAction: command.RequestedAction, ExpiresAt: now.Add(approvalResumeLifetime),
		Status: "pending", Metadata: maps.Clone(command.Metadata),
	}
	tokenMetadata := maps.Clone(command.Metadata)
	if tokenMetadata == nil {
		tokenMetadata = make(map[string]string)
	}
	tokenMetadata["status"] = "pending"
	token := agentruntime.ResumeToken{
		TokenID: tokenID, RunID: command.RunID, TaskID: command.TaskID,
		ApprovalID: approvalID, ExpiresAt: approval.ExpiresAt,
		ResumeCommand: "approval.resolve", ResumeRunState: agentruntime.RunStatusRunning,
		ResumeTaskState: agentruntime.TaskStatusRunning, Metadata: tokenMetadata,
	}
	work, err := s.store.Begin(ctx)
	if err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
	}
	defer work.Rollback(context.Background())
	if deterministic {
		existing, loadErr := work.Approvals().LoadApproval(ctx, approvalID)
		switch {
		case loadErr == nil:
			existingToken, tokenErr := work.ResumeTokens().LoadResumeToken(ctx, tokenID)
			if tokenErr != nil {
				return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, tokenErr
			}
			if err := validateExistingApproval(command, existing, existingToken, now); err != nil {
				return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
			}
			return existing, existingToken, nil
		case !errors.Is(loadErr, agentruntime.ErrNotFound):
			return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, loadErr
		}
	}
	if err := work.Approvals().SaveApproval(ctx, approval); err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
	}
	if err := work.ResumeTokens().SaveResumeToken(ctx, token); err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
	}
	if run, loadErr := work.Runs().LoadRun(ctx, command.RunID); loadErr == nil {
		run.Status = agentruntime.RunStatusWaitingApproval
		run.UpdatedAt = now
		if err := work.Runs().SaveRun(ctx, run); err != nil {
			return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
		}
	}
	if task, loadErr := work.Tasks().LoadTask(ctx, command.RunID, command.TaskID); loadErr == nil {
		task.Status = agentruntime.TaskStatusPaused
		task.UpdatedAt = now
		if err := work.Tasks().SaveTask(ctx, task); err != nil {
			return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
		}
	}
	if err := work.Events().AppendEvent(ctx, agentruntime.Event{
		RunID: command.RunID, TaskID: command.TaskID, Type: agentruntime.EventApprovalRequested, RecordedAt: now,
		Payload: map[string]any{
			"approvalId": approvalID, "actionId": command.ActionID,
			"requesterAgentId": command.RequesterAgentID, "reason": command.Reason,
			"riskSummary": command.RiskSummary, "requestedAction": command.RequestedAction,
		},
	}); err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
	}
	if err := work.Commit(ctx); err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, err
	}
	return approval, token, nil
}

func (s *Service) recoverResumeToken(ctx context.Context, tokenID string) (agentruntime.ResumeToken, error) {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return agentruntime.ResumeToken{}, err
	}
	defer work.Rollback(context.Background())
	token, err := work.ResumeTokens().LoadResumeToken(ctx, tokenID)
	if err != nil {
		return agentruntime.ResumeToken{}, err
	}
	if token.ExpiresAt.IsZero() || !token.ExpiresAt.After(time.Now().UTC()) || token.Metadata["status"] != "pending" {
		return agentruntime.ResumeToken{}, agentruntime.ErrInvalidCommand
	}
	return token, nil
}

func (s *Service) decideApproval(ctx context.Context, command agentruntime.DecideApprovalCommand) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	now := time.Now().UTC()
	approval, err := work.Approvals().LoadApproval(ctx, command.ApprovalID)
	if err != nil {
		return err
	}
	if approval.RunID != command.RunID || approval.Status != "pending" {
		return agentruntime.ErrInvalidCommand
	}
	if command.Decision != "approved" && command.Decision != "rejected" {
		return agentruntime.ErrInvalidCommand
	}
	approval.Status = command.Decision
	approval.Metadata = maps.Clone(approval.Metadata)
	if approval.Metadata == nil {
		approval.Metadata = make(map[string]string)
	}
	approval.Metadata["decided_by"] = command.DecidedBy
	approval.Metadata["decision_reason"] = command.Reason
	approval.Metadata["decided_at"] = now.Format(time.RFC3339Nano)
	if err := work.Approvals().SaveApproval(ctx, approval); err != nil {
		return err
	}
	tokens, err := work.ResumeTokens().ListPending(ctx, agentruntime.ResumeTokenSelector{
		RunID: command.RunID, TaskID: approval.TaskID, Statuses: []string{"pending"},
	})
	if err != nil {
		return err
	}
	for _, token := range tokens {
		if token.ApprovalID != approval.ApprovalID {
			continue
		}
		token.Metadata = maps.Clone(token.Metadata)
		if token.Metadata == nil {
			token.Metadata = make(map[string]string)
		}
		token.Metadata["status"] = "consumed"
		if err := work.ResumeTokens().SaveResumeToken(ctx, token); err != nil {
			return err
		}
	}
	if err := work.Events().AppendEvent(ctx, agentruntime.Event{
		RunID: command.RunID, TaskID: approval.TaskID, Type: agentruntime.EventApprovalDecided, RecordedAt: now,
		Payload: map[string]any{
			"approvalId": approval.ApprovalID, "actionId": approval.ActionID,
			"decidedBy": command.DecidedBy, "decision": command.Decision, "reason": command.Reason,
		},
	}); err != nil {
		return err
	}
	if run, loadErr := work.Runs().LoadRun(ctx, command.RunID); loadErr == nil {
		run.Status = agentruntime.RunStatusRunning
		run.UpdatedAt = now
		if err := work.Runs().SaveRun(ctx, run); err != nil {
			return err
		}
	}
	if task, loadErr := work.Tasks().LoadTask(ctx, command.RunID, approval.TaskID); loadErr == nil {
		task.Status = agentruntime.TaskStatusRunning
		task.UpdatedAt = now
		if err := work.Tasks().SaveTask(ctx, task); err != nil {
			return err
		}
	}
	return work.Commit(ctx)
}
