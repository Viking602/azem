package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/skills"
	"github.com/Viking602/venat"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/tool"
)

const mainAgentID = "azem-main"

const (
	defaultRunLeaseTTL               = 10 * time.Minute
	defaultRunLeaseHeartbeatInterval = 30 * time.Second
)

const (
	approvalMetadataOperationID = "azem.operation_id"
	approvalMetadataScope       = "azem.scope_fingerprint"
)

type recoveredApprovalDecision struct {
	fingerprint string
	approved    bool
}

type Service struct {
	runner               *venat.Runner
	store                api.StoreProvider
	workspace            coding.Workspace
	tools                *tool.Bus
	policy               *ApprovalPolicy
	allowWrite           bool
	shellPolicy          string
	allowNetwork         string
	shellRuntime         *shellRuntime
	teamMaxConcurrency   int
	teamMaxTicks         int
	skills               *skills.Catalog
	runLeaseTTL          time.Duration
	runHeartbeatInterval time.Duration
	ctx                  context.Context
	cancel               context.CancelFunc
	wg                   sync.WaitGroup
	approvalMu           sync.Mutex
	recoveredApprovals   map[string]map[string]recoveredApprovalDecision
}

var ErrTerminalReportMissing = errors.New("terminal worker report is missing")

const hashlineEditToolDescription = `Apply a hashline patch to existing files. This is not unified diff. Copy the exact ¶PATH#TAG header and N:TEXT line numbers from the latest coding.read_file result. Grammar:
¶PATH#TAG
replace N:
+final replacement line
replace N..M:
+first final line
+second final line
delete N..M
insert before N:
+new final line
insert after N:
+new final line
insert head:
+new final line
insert tail:
+new final line
replace block N:
+complete final block
delete block N
Use positive 1-based line numbers. Body rows are final content only and each starts with '+'. Never send @@ hunks, ~ or : ranges, -old rows, or bare context rows. Delete has no body.`

const hashlineRetryGuidance = `Required hashline retry format:
¶PATH#TAG
replace N..M:
+final content only
Copy ¶PATH#TAG and line numbers from the latest coding.read_file result. Allowed operations: replace N or N..M, delete N or N..M, insert before/after N, insert head/tail, replace block N, delete block N. Never use @@, ~N:M, -old rows, or bare context.`

type definitionOverrideDriver struct {
	tool.Driver
	definition tool.Definition
}

func (d definitionOverrideDriver) Definition() tool.Definition { return d.definition }

type EditRecovery struct {
	mu           sync.Mutex
	readRequired map[string]struct{}
}

func (recovery *EditRecovery) RequiredEditReadTarget() (string, bool) {
	if recovery == nil {
		return "", false
	}
	recovery.mu.Lock()
	defer recovery.mu.Unlock()
	target := ""
	for candidate := range recovery.readRequired {
		if target == "" || candidate < target {
			target = candidate
		}
	}
	return target, target != ""
}

func (recovery *EditRecovery) BlockedEdit(call tool.Call) (tool.Result, bool) {
	if recovery == nil || call.Name != coding.ToolEditHashline {
		return tool.Result{}, false
	}
	target, required := recovery.RequiredEditReadTarget()
	if !required {
		return tool.Result{}, false
	}
	return tool.Result{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content: fmt.Sprintf(
			"edit blocked: the previous edit for %q failed. Call %s for that file, then rebuild the patch from the new header and visible lines before editing again.",
			target, coding.ToolReadFile,
		),
		IsError: true,
	}, true
}

func addHashlineRetryGuidance(call tool.Call, result tool.Result) tool.Result {
	if call.Name != coding.ToolEditHashline || !result.IsError || strings.Contains(result.Content, "Required hashline retry format:") {
		return result
	}
	result.Content = strings.TrimSpace(result.Content) + "\n\n" + hashlineRetryGuidance
	return result
}

func (recovery *EditRecovery) Observe(call tool.Call, result tool.Result, executionErr error) {
	if recovery == nil {
		return
	}
	target := normalizedTarget(call.Arguments)
	if target == "" {
		target = "workspace"
	}
	recovery.mu.Lock()
	defer recovery.mu.Unlock()
	switch call.Name {
	case coding.ToolEditHashline:
		if executionErr == nil && !result.IsError {
			return
		}
		if recovery.readRequired == nil {
			recovery.readRequired = make(map[string]struct{})
		}
		recovery.readRequired[target] = struct{}{}
	case coding.ToolReadFile:
		if executionErr != nil || result.IsError {
			return
		}
		delete(recovery.readRequired, target)
		delete(recovery.readRequired, "workspace")
	}
}

type RunExecutionPolicy struct {
	Budget      *api.TaskBudget
	RetryPolicy api.RetryPolicy
}

type Run struct {
	RunID           string
	Goal            string
	TaskID          string
	EnvelopeID      string
	LeaseID         string
	TaskVersion     int
	HolderID        string
	pending         map[string]PendingApproval
	approvedOnce    map[string]string
	editRecovery    EditRecovery
	leaseCancel     context.CancelFunc
	leaseParentStop func() bool
	leaseDone       <-chan error
	leaseStopOnce   sync.Once
	leaseErr        error
}

type PendingApproval struct {
	Request    api.ApprovalRequest
	Token      api.ResumeToken
	Call       tool.Call
	Scope      invocationScope
	Effect     string
	Replayable bool
}

type ExecutionResult struct {
	Result   tool.Result
	Approval *PendingApproval
	Executed bool
}

func (run *Run) RequiredEditReadTarget() (string, bool) {
	if run == nil {
		return "", false
	}
	return run.editRecovery.RequiredEditReadTarget()
}

type ApprovalMode string

const (
	ApprovalOnce    ApprovalMode = "approved_once"
	ApprovalSession ApprovalMode = "approved_session"
	ApprovalDenied  ApprovalMode = "denied"
)

type serviceOptions struct {
	allowWrite         bool
	shellPolicy        string
	network            string
	teamMaxConcurrency int
	teamMaxTicks       int
	skills             *skills.Catalog
	shellOptions       ShellOptions
}

type ServiceOption func(*serviceOptions)

func WithWorkspacePolicy(allowWrite bool, shellPolicy, allowNetwork string) ServiceOption {
	return func(options *serviceOptions) {
		options.allowWrite = allowWrite
		options.shellPolicy = shellPolicy
		options.network = allowNetwork
	}
}

func WithTeamLimits(maxConcurrency, maxTicks int) ServiceOption {
	return func(options *serviceOptions) {
		if maxConcurrency > 0 {
			options.teamMaxConcurrency = maxConcurrency
		}
		if maxTicks > 0 {
			options.teamMaxTicks = maxTicks
		}
	}
}

func WithSkills(catalog *skills.Catalog) ServiceOption {
	return func(options *serviceOptions) {
		options.skills = catalog
	}
}

func WithShellOptions(options ShellOptions) ServiceOption {
	return func(settings *serviceOptions) { settings.shellOptions = options }
}

func NewService(store api.StoreProvider, workspaceRoot string, options ...ServiceOption) (*Service, error) {
	settings := serviceOptions{allowWrite: true, shellPolicy: "prompt", network: "prompt", teamMaxConcurrency: 2, teamMaxTicks: 12}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	policy := NewApprovalPolicy()
	runner, err := venat.NewProduction(api.Config{StoreProvider: store, PolicyEngine: policy})
	if err != nil {
		return nil, err
	}
	runner.RegisterAgent(api.AgentProfile{ID: mainAgentID, Role: "coding"})
	workspace := coding.NewLocalWorkspace(workspaceRoot)
	serviceCtx, serviceCancel := context.WithCancel(context.Background())
	service := &Service{
		runner: runner, store: store, workspace: workspace, policy: policy,
		allowWrite: settings.allowWrite, shellPolicy: settings.shellPolicy, allowNetwork: settings.network,
		teamMaxConcurrency: settings.teamMaxConcurrency, teamMaxTicks: settings.teamMaxTicks,
		skills: settings.skills, runLeaseTTL: defaultRunLeaseTTL, runHeartbeatInterval: defaultRunLeaseHeartbeatInterval,
		ctx: serviceCtx, cancel: serviceCancel,
		recoveredApprovals: make(map[string]map[string]recoveredApprovalDecision),
	}
	service.shellRuntime = newShellRuntime(serviceCtx, settings.shellOptions)
	drivers, err := service.WorkspaceDrivers(context.Background(), workspaceRoot)
	if err != nil {
		serviceCancel()
		return nil, err
	}
	service.tools = tool.NewBus(drivers...)
	return service, nil
}

func (s *Service) Runner() *venat.Runner { return s.runner }

func (s *Service) ResolveReconcileAttempt(ctx context.Context, attemptID string, status api.ActionAttemptStatus, externalResultRef string) error {
	_, err := s.runner.ResolveActionAttempt(ctx, api.ResolveActionAttemptCommand{
		AttemptID:         attemptID,
		Status:            status,
		ExternalResultRef: externalResultRef,
	})
	return err
}

func (s *Service) SkillSnapshot() skills.Snapshot {
	if s == nil || s.skills == nil {
		return skills.Snapshot{}
	}
	return s.skills.Snapshot()
}

func (s *Service) StartRun(ctx context.Context, request string) (*Run, error) {
	return s.StartRunWithMetadata(ctx, request, nil)
}

func (s *Service) StartRunWithMetadata(ctx context.Context, request string, metadata map[string]string, policies ...RunExecutionPolicy) (*Run, error) {
	runID, err := newID("run")
	if err != nil {
		return nil, err
	}
	rootID, err := newID("root")
	if err != nil {
		return nil, err
	}
	run, root, err := s.runner.StartRun(ctx, api.StartRunCommand{RunID: runID, RootTaskID: rootID, Request: request, Metadata: metadata})
	if err != nil {
		return nil, err
	}
	for _, status := range []api.RunStatus{
		api.RunStatusPlanning,
		api.RunStatusValidating,
		api.RunStatusRouting,
		api.RunStatusDispatching,
		api.RunStatusRunning,
	} {
		if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: run.ID, To: status}); err != nil {
			return nil, fmt.Errorf("start coding run %s: %w", status, err)
		}
	}
	taskID, err := newID("task")
	if err != nil {
		return nil, err
	}
	var executionPolicy RunExecutionPolicy
	if len(policies) > 0 {
		executionPolicy = policies[0]
	}
	task, err := s.runner.CreateTask(ctx, api.CreateTaskCommand{
		RunID: run.ID, TaskID: taskID, ParentTaskID: root.ID, Type: api.TaskTypeWorker,
		Goal: request, OwnerAgentID: mainAgentID, AssignedAgentID: mainAgentID, AllowsAction: true,
		Budget: executionPolicy.Budget, RetryPolicy: executionPolicy.RetryPolicy,
	})
	if err != nil {
		return nil, err
	}
	envelope, err := s.dispatchTask(ctx, api.DispatchTaskCommand{RunID: run.ID, TaskID: task.ID, TargetAgentID: mainAgentID})
	if err != nil {
		return nil, err
	}
	lease, acquired, err := s.runner.AcquireTaskExecution(ctx, api.AcquireTaskExecutionCommand{
		RunID: run.ID, TaskID: task.ID, EnvelopeID: envelope.ID, HolderType: api.HolderAgent, HolderID: mainAgentID, TTL: s.runLeaseTTL,
	})
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("acquire coding task lease: no lease granted")
	}
	tracked := &Run{
		RunID: run.ID, Goal: request, TaskID: task.ID, EnvelopeID: envelope.ID, LeaseID: lease.ID, TaskVersion: lease.TaskVersion, HolderID: mainAgentID,
		pending: make(map[string]PendingApproval), approvedOnce: make(map[string]string),
	}
	s.startRunLeaseHeartbeat(ctx, tracked)
	return tracked, nil
}

// ResumeRun reacquires the worker task that durable recovery redispatched for
// an interrupted single-agent run. It preserves the original run and task IDs
// so action-attempt idempotency remains scoped to the same execution.
func (s *Service) ResumeRun(ctx context.Context, runID string) (*Run, error) {
	durable, err := s.runner.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	tasks, err := s.runner.ListTasks(ctx, runID)
	if err != nil {
		return nil, err
	}
	var task api.Task
	for _, candidate := range tasks {
		if candidate.Type == api.TaskTypeWorker && candidate.AssignedAgentID == mainAgentID && candidate.Status == api.TaskStatusDispatched {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		return nil, fmt.Errorf("resume coding run %s: no redispatched main task", runID)
	}
	envelopes, err := s.runner.ListEnvelopes(ctx, runID)
	if err != nil {
		return nil, err
	}
	var envelope api.TaskEnvelope
	for _, candidate := range envelopes {
		if candidate.TaskID == task.ID && candidate.Status == "pending" {
			envelope = candidate
			break
		}
	}
	if envelope.ID == "" {
		return nil, fmt.Errorf("resume coding run %s: redispatched envelope is missing", runID)
	}
	lease, acquired, err := s.runner.AcquireTaskExecution(ctx, api.AcquireTaskExecutionCommand{
		RunID: runID, TaskID: task.ID, EnvelopeID: envelope.ID,
		HolderType: api.HolderAgent, HolderID: mainAgentID, TTL: s.runLeaseTTL,
	})
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("resume coding run %s: no lease granted", runID)
	}
	tracked := &Run{
		RunID: runID, Goal: task.Goal, TaskID: task.ID, EnvelopeID: envelope.ID, LeaseID: lease.ID, TaskVersion: lease.TaskVersion, HolderID: mainAgentID,
		pending: make(map[string]PendingApproval), approvedOnce: make(map[string]string),
	}
	if tracked.Goal == "" {
		tracked.Goal = durable.Request
	}
	s.startRunLeaseHeartbeat(ctx, tracked)
	return tracked, nil
}

// ReleaseRun relinquishes a recovered lease when the host cannot safely
// rebuild the immutable execution profile. The durable run remains
// non-terminal and can be resumed after the incompatibility is resolved.
func (s *Service) ReleaseRun(ctx context.Context, run *Run) error {
	if run == nil {
		return nil
	}
	heartbeatErr := run.stopRunLeaseHeartbeat()
	releaseErr := s.runner.ReleaseTaskExecution(ctx, api.ReleaseTaskExecutionCommand{LeaseID: run.LeaseID, HolderID: run.HolderID})
	return errors.Join(heartbeatErr, releaseErr)
}

func (s *Service) RequireRunReconciliation(ctx context.Context, runID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "reconciliation required"
	}
	if _, err := s.runner.Recover(ctx, runID); err != nil {
		return err
	}
	if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: runID, To: api.RunStatusReconcileRequired}); err != nil {
		return fmt.Errorf("mark run %s reconciliation required: %w", runID, err)
	}
	return nil
}

func (s *Service) startRunLeaseHeartbeat(parent context.Context, run *Run) {
	heartbeatCtx, cancel := context.WithCancel(s.ctx)
	done := make(chan error, 1)
	run.leaseCancel = cancel
	run.leaseParentStop = context.AfterFunc(parent, cancel)
	run.leaseDone = done
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.runHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				done <- nil
				return
			case <-ticker.C:
			}
			timeout := min(s.runHeartbeatInterval, 5*time.Second)
			beatCtx, beatCancel := context.WithTimeout(heartbeatCtx, timeout)
			err := s.runner.HeartbeatTaskExecution(beatCtx, api.HeartbeatTaskExecutionCommand{
				LeaseID: run.LeaseID, HolderID: run.HolderID, TTL: s.runLeaseTTL,
			})
			beatCancel()
			if err == nil {
				continue
			}
			if heartbeatCtx.Err() != nil {
				done <- nil
				return
			}
			if errors.Is(err, api.ErrLeaseNotActive) || errors.Is(err, api.ErrLeaseHolderMismatch) {
				done <- fmt.Errorf("maintain run lease: %w", err)
				return
			}
		}
	}()
}

func (run *Run) stopRunLeaseHeartbeat() error {
	run.leaseStopOnce.Do(func() {
		if run.leaseParentStop != nil {
			run.leaseParentStop()
		}
		if run.leaseCancel != nil {
			run.leaseCancel()
		}
		if run.leaseDone != nil {
			run.leaseErr = <-run.leaseDone
		}
	})
	return run.leaseErr
}

func (s *Service) TransferRunExecution(run *Run) (api.TaskEnvelope, api.TaskExecutionLease, error) {
	if run == nil {
		return api.TaskEnvelope{}, api.TaskExecutionLease{}, fmt.Errorf("run is nil")
	}
	if err := run.stopRunLeaseHeartbeat(); err != nil {
		return api.TaskEnvelope{}, api.TaskExecutionLease{}, err
	}
	return api.TaskEnvelope{
			ID: run.EnvelopeID, RunID: run.RunID, TaskID: run.TaskID, Status: "pending", TaskVersion: run.TaskVersion,
		}, api.TaskExecutionLease{
			ID: run.LeaseID, RunID: run.RunID, TaskID: run.TaskID, EnvelopeID: run.EnvelopeID,
			HolderType: api.HolderAgent, HolderID: run.HolderID, TaskVersion: run.TaskVersion, Status: api.LeaseStatusActive,
		}, nil
}

func (s *Service) dispatchTask(ctx context.Context, command api.DispatchTaskCommand) (api.TaskEnvelope, error) {
	for {
		envelope, err := s.runner.DispatchTask(ctx, command)
		if err == nil {
			return envelope, nil
		}
		if !errors.Is(err, api.ErrIdempotencyConflict) {
			return api.TaskEnvelope{}, err
		}
		if err := ctx.Err(); err != nil {
			return api.TaskEnvelope{}, err
		}
	}
}

func (s *Service) ExecuteTool(ctx context.Context, run *Run, call tool.Call, sink tool.UpdateSink) (ExecutionResult, error) {
	if run == nil {
		return ExecutionResult{}, fmt.Errorf("run is nil")
	}
	driver, ok := s.tools.Driver(call.Name)
	if !ok {
		return ExecutionResult{}, fmt.Errorf("%w: %s", tool.ErrToolNotFound, call.Name)
	}
	return s.ExecuteDriver(ctx, run, driver, call, sink)
}

// PrepareDriver applies Azem's approval policy without running the underlying
// operation. ready is true only when ExecutePreparedDriver may run immediately.
func (s *Service) PrepareDriver(ctx context.Context, run *Run, driver tool.Driver, call tool.Call) (result ExecutionResult, ready bool, err error) {
	if run == nil {
		return ExecutionResult{}, false, fmt.Errorf("run is nil")
	}
	if driver == nil {
		return ExecutionResult{}, false, fmt.Errorf("tool driver is nil")
	}
	definition := driver.Definition()
	if call.Name != definition.Name {
		return ExecutionResult{}, false, fmt.Errorf("tool call %q does not match driver %q", call.Name, definition.Name)
	}
	if blocked, required := run.editRecovery.BlockedEdit(call); required {
		return ExecutionResult{Result: blocked, Executed: true}, false, nil
	}
	scope := scopeForCall(definition, call)
	approvalKey := approvalCallKey(call)
	needsApproval := definition.RequiresApproval || definition.Security.RequiresApproval || definition.RequiresActionTask || definition.EffectType == tool.EffectWrite || definition.EffectType == tool.EffectExternalSideEffect
	if definition.Metadata["approval"] == "allow" {
		needsApproval = definition.Metadata["network"] == "prompt" && toolCallRequestsNetwork(call.Arguments)
	}
	approved := s.policy.sessionGranted(scope.Fingerprint) || run.approvedOnce[approvalKey] == scope.Fingerprint
	if needsApproval && !approved {
		if recovered, found := s.consumeRecoveredApproval(run.RunID, approvalKey, scope.Fingerprint); found {
			if !recovered.approved {
				return ExecutionResult{Result: tool.Result{
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    "Denied by user",
					IsError:    true,
				}}, false, nil
			}
			approved = true
		}
	}
	if needsApproval && !approved {
		if pending, found := run.pending[approvalKey]; found && pending.Scope.Fingerprint == scope.Fingerprint {
			return ExecutionResult{Approval: &pending}, false, nil
		}
		approval, token, requestErr := s.runner.RequestApproval(ctx, api.RequestApprovalCommand{
			RunID: run.RunID, TaskID: run.TaskID, ActionID: approvalKey, RequesterAgentID: run.HolderID,
			Reason: fmt.Sprintf("%s requests %s", run.HolderID, call.Name), RiskSummary: scope.Risk + " · " + scope.Target,
			RequestedAction: summarizeArguments(call.Arguments),
			Metadata: map[string]string{
				approvalMetadataOperationID: approvalKey,
				approvalMetadataScope:       scope.Fingerprint,
			},
		})
		if requestErr != nil {
			return ExecutionResult{}, false, requestErr
		}
		pending := PendingApproval{Request: approval, Token: token, Call: call, Scope: scope, Effect: string(definition.EffectType), Replayable: definition.Idempotent || definition.Security.Idempotent}
		run.pending[approvalKey] = pending
		return ExecutionResult{Approval: &pending}, false, nil
	}
	return ExecutionResult{}, true, nil
}

// ExecutePreparedDriver runs an operation that already passed PrepareDriver.
func (s *Service) ExecutePreparedDriver(ctx context.Context, run *Run, driver tool.Driver, call tool.Call, sink tool.UpdateSink) (ExecutionResult, error) {
	delete(run.approvedOnce, approvalCallKey(call))
	result, err := driver.Execute(ctx, call, sink)
	result = addHashlineRetryGuidance(call, result)
	run.editRecovery.Observe(call, result, err)
	if err != nil {
		return ExecutionResult{Result: result}, err
	}
	return ExecutionResult{Result: result, Executed: true}, nil
}

// ExecuteDriver preserves the direct-call API by preparing and then executing.
// Venat workers call PrepareDriver before journaling side effects.
func (s *Service) ExecuteDriver(ctx context.Context, run *Run, driver tool.Driver, call tool.Call, sink tool.UpdateSink) (ExecutionResult, error) {
	prepared, ready, err := s.PrepareDriver(ctx, run, driver, call)
	if err != nil || !ready {
		return prepared, err
	}
	return s.ExecutePreparedDriver(ctx, run, driver, call, sink)
}

func (s *Service) ResolveApproval(ctx context.Context, run *Run, callID string, mode ApprovalMode, decidedBy string) error {
	pending, ok := run.pending[callID]
	if !ok {
		return api.ErrNotFound
	}
	decision := "approved"
	if mode == ApprovalDenied {
		decision = "rejected"
	}
	if mode != ApprovalOnce && mode != ApprovalSession && mode != ApprovalDenied {
		return fmt.Errorf("invalid approval mode %q", mode)
	}
	if strings.TrimSpace(decidedBy) == "" {
		return fmt.Errorf("approval decider is empty")
	}
	if err := s.runner.DecideApproval(ctx, api.DecideApprovalCommand{
		RunID: run.RunID, ApprovalID: pending.Request.ApprovalID, DecidedBy: decidedBy, Decision: decision,
	}); err != nil {
		return err
	}
	delete(run.pending, callID)
	switch mode {
	case ApprovalOnce:
		run.approvedOnce[callID] = pending.Scope.Fingerprint
	case ApprovalSession:
		s.policy.GrantSession(pending.Scope.Fingerprint)
	}
	return nil
}

func (s *Service) ResolveRecoveredApproval(ctx context.Context, approval api.ApprovalRequest, tokenID, decision string) error {
	mode := ApprovalOnce
	switch decision {
	case "session":
		mode = ApprovalSession
		decision = "approved"
	case "once", "approved", "approve":
		decision = "approved"
	case "denied", "deny", "rejected", "reject":
		mode = ApprovalDenied
		decision = "rejected"
	default:
		return fmt.Errorf("invalid approval decision %q", decision)
	}
	operationID := approval.Metadata[approvalMetadataOperationID]
	if operationID == "" {
		operationID = approval.ActionID
	}
	fingerprint := approval.Metadata[approvalMetadataScope]
	if operationID == "" || fingerprint == "" {
		return fmt.Errorf("recovered approval %s is missing its durable operation scope", approval.ApprovalID)
	}
	if tokenID != "" {
		token, err := s.runner.RecoverResumeToken(ctx, api.RecoverResumeTokenCommand{TokenID: tokenID})
		if err != nil {
			return err
		}
		if token.RunID != approval.RunID || token.ApprovalID != approval.ApprovalID {
			return fmt.Errorf("recovered approval %s does not own resume token %s", approval.ApprovalID, tokenID)
		}
	}
	if err := s.runner.DecideApproval(ctx, api.DecideApprovalCommand{
		RunID: approval.RunID, ApprovalID: approval.ApprovalID, DecidedBy: "user", Decision: decision,
	}); err != nil {
		return err
	}
	if mode == ApprovalSession {
		s.policy.GrantSession(fingerprint)
	} else {
		s.storeRecoveredApproval(approval.RunID, operationID, recoveredApprovalDecision{
			fingerprint: fingerprint,
			approved:    decision == "approved",
		})
	}
	return nil
}

func approvalCallKey(call tool.Call) string {
	if call.OperationID != "" {
		return call.OperationID
	}
	return call.ID
}

func (s *Service) storeRecoveredApproval(runID, operationID string, decision recoveredApprovalDecision) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	byOperation := s.recoveredApprovals[runID]
	if byOperation == nil {
		byOperation = make(map[string]recoveredApprovalDecision)
		s.recoveredApprovals[runID] = byOperation
	}
	byOperation[operationID] = decision
}

func (s *Service) consumeRecoveredApproval(runID, operationID, fingerprint string) (recoveredApprovalDecision, bool) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	byOperation := s.recoveredApprovals[runID]
	decision, found := byOperation[operationID]
	if !found || decision.fingerprint != fingerprint {
		return recoveredApprovalDecision{}, false
	}
	delete(byOperation, operationID)
	if len(byOperation) == 0 {
		delete(s.recoveredApprovals, runID)
	}
	return decision, true
}

func (s *Service) ToolDefinitions() []tool.Definition {
	return s.tools.Definitions()
}

func (s *Service) ToolDrivers() []tool.Driver {
	definitions := s.tools.Definitions()
	drivers := make([]tool.Driver, 0, len(definitions))
	for _, definition := range definitions {
		if driver, ok := s.tools.Driver(definition.Name); ok {
			drivers = append(drivers, driver)
		}
	}
	return drivers
}

func (s *Service) WorkspaceDrivers(ctx context.Context, root string) ([]tool.Driver, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("workspace root is empty")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	workspace := coding.NewLocalWorkspace(absoluteRoot)
	candidates := coding.NewToolSet(workspace)
	isGitRepo := workspaceIsGitRepo(ctx, absoluteRoot)
	drivers := make([]tool.Driver, 0, len(candidates))
	for _, driver := range candidates {
		definition := driver.Definition()
		if definition.Name == coding.ToolGitDiff && !isGitRepo {
			continue
		}
		if !s.allowWrite && definition.EffectType == tool.EffectWrite {
			continue
		}
		if definition.Name == coding.ToolEditHashline {
			definition.Description = hashlineEditToolDescription
			driver = definitionOverrideDriver{Driver: driver, definition: definition}
		}
		drivers = append(drivers, driver)
	}
	if s.shellPolicy != "deny" {
		drivers = append(drivers, newRuntimeShellDriver(absoluteRoot, s.shellPolicy, s.allowNetwork, s.shellRuntime))
	}
	return drivers, nil
}

func workspaceIsGitRepo(ctx context.Context, root string) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	output, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

func (s *Service) CompleteRun(ctx context.Context, run *Run, summary string, failure error) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if err := run.stopRunLeaseHeartbeat(); err != nil {
		return err
	}
	if err := s.runner.HeartbeatTaskExecution(ctx, api.HeartbeatTaskExecutionCommand{
		LeaseID: run.LeaseID, HolderID: run.HolderID, TTL: s.runLeaseTTL,
	}); err != nil {
		return fmt.Errorf("refresh run lease before report: %w", err)
	}
	status := api.ReportStatusSuccess
	target := api.RunStatusCompleted
	kind := ""
	if failure != nil {
		status = api.ReportStatusFailed
		target = api.RunStatusFailed
		kind = "agent_error"
		if summary == "" {
			summary = failure.Error()
		}
	}
	if err := s.runner.SubmitTypedReport(ctx, api.SubmitTypedReportCommand{
		RunID: run.RunID, TaskID: run.TaskID, LeaseID: run.LeaseID, HolderType: api.HolderAgent,
		HolderID: run.HolderID, TaskVersion: run.TaskVersion,
		Report: api.TypedReport{Status: status, Summary: summary, Kind: kind},
	}); err != nil {
		return fmt.Errorf("submit run report: %w", err)
	}
	return s.finalizeReportedRunTo(ctx, run.RunID, target)
}

// finalizeReportedRunTo advances a report authored directly by Azem when
// execution never reached Venat's worker.
func (s *Service) finalizeReportedRunTo(ctx context.Context, runID string, target api.RunStatus) error {
	projection, err := s.runner.Recover(ctx, runID)
	if err != nil {
		return fmt.Errorf("project reported run: %w", err)
	}
	if projection.Run.Status == target {
		return nil
	}
	if target == api.RunStatusCompleted {
		if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: runID, To: api.RunStatusComposingResponse}); err != nil {
			return fmt.Errorf("compose run response from %s: %w", projection.Run.Status, err)
		}
	}
	if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: runID, To: target}); err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

// CancelRun records an explicit user cancellation as terminal. Application
// shutdown uses ReleaseRun instead so an interrupted run remains resumable.
func (s *Service) CancelRun(ctx context.Context, run *Run, cause error) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if err := run.stopRunLeaseHeartbeat(); err != nil {
		return err
	}
	if err := s.runner.HeartbeatTaskExecution(ctx, api.HeartbeatTaskExecutionCommand{
		LeaseID: run.LeaseID, HolderID: run.HolderID, TTL: s.runLeaseTTL,
	}); err != nil {
		return fmt.Errorf("refresh run lease before cancellation: %w", err)
	}
	reason := "cancelled by user"
	if cause != nil {
		reason = cause.Error()
	}
	if err := s.runner.SubmitTypedReport(ctx, api.SubmitTypedReportCommand{
		RunID: run.RunID, TaskID: run.TaskID, LeaseID: run.LeaseID, HolderType: api.HolderAgent,
		HolderID: run.HolderID, TaskVersion: run.TaskVersion,
		Report: api.TypedReport{Status: api.ReportStatusFailed, Summary: reason, Kind: "cancelled"},
	}); err != nil {
		return fmt.Errorf("submit cancelled run report: %w", err)
	}
	projection, err := s.runner.Recover(ctx, run.RunID)
	if err != nil {
		return fmt.Errorf("project cancelled run: %w", err)
	}
	if projection.Run.Status == api.RunStatusCancelled {
		return nil
	}
	if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: run.RunID, To: api.RunStatusCancelled}); err != nil {
		return fmt.Errorf("cancel run: %w", err)
	}
	return nil
}

// FinalizeReportedRun completes the run-level state transition after a crash
// that occurred after the worker report committed and released its lease.
func (s *Service) FinalizeReportedRun(ctx context.Context, runID string) error {
	projection, err := s.runner.Recover(ctx, runID)
	if err != nil {
		return err
	}
	if projection.Run.Status == api.RunStatusCompleted || projection.Run.Status == api.RunStatusFailed ||
		projection.Run.Status == api.RunStatusBlocked || projection.Run.Status == api.RunStatusCancelled {
		return nil
	}
	target := api.RunStatus("")
	for _, task := range projection.Tasks {
		if task.Type != api.TaskTypeWorker || task.Result == nil {
			continue
		}
		switch task.Status {
		case api.TaskStatusCompleted:
			if task.Result.Status == api.ReportStatusSuccess {
				target = api.RunStatusCompleted
			}
		case api.TaskStatusFailed:
			if task.Result.Kind == "cancelled" {
				target = api.RunStatusCancelled
			} else {
				target = api.RunStatusFailed
			}
		case api.TaskStatusBlocked:
			target = api.RunStatusBlocked
		case api.TaskStatusCancelled:
			target = api.RunStatusCancelled
		}
		break
	}
	if target == "" {
		return fmt.Errorf("finalize reported run %s: %w", runID, ErrTerminalReportMissing)
	}
	if target == api.RunStatusCompleted && projection.Run.Status != api.RunStatusComposingResponse {
		if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: runID, To: api.RunStatusComposingResponse}); err != nil {
			return fmt.Errorf("finalize reported run %s composing response: %w", runID, err)
		}
	}
	if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: runID, To: target}); err != nil {
		return fmt.Errorf("finalize reported run %s: %w", runID, err)
	}
	return nil
}

func (s *Service) Recover(ctx context.Context, runID string) (api.Projection, error) {
	return s.runner.Recover(ctx, runID)
}

func (s *Service) Checkpoint(ctx context.Context) error {
	if checkpointer, ok := s.store.(interface{ Checkpoint(context.Context) error }); ok {
		return checkpointer.Checkpoint(ctx)
	}
	return nil
}

func toolCallRequestsNetwork(arguments json.RawMessage) bool {
	var object struct {
		Network bool `json:"network"`
	}
	return json.Unmarshal(arguments, &object) == nil && object.Network
}

func (s *Service) Close(ctx context.Context) error {
	if s.shellRuntime != nil {
		s.shellRuntime.shutdown()
	}
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		if s.shellRuntime != nil {
			s.shellRuntime.wg.Wait()
		}
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	if closer, ok := s.store.(api.ProviderCloser); ok {
		return closer.Close(ctx)
	}
	return nil
}

// ActiveShellExecutions returns a race-safe point-in-time status view.
func (s *Service) ActiveShellExecutions() []ShellExecutionSnapshot {
	if s == nil || s.shellRuntime == nil {
		return nil
	}
	return s.shellRuntime.snapshot()
}

func scopeForCall(definition tool.Definition, call tool.Call) invocationScope {
	target := normalizedTarget(call.Arguments)
	if target == "" {
		target = "workspace"
	}
	risk := definition.RiskLevel
	if risk == "" {
		risk = definition.Security.RiskLevel
	}
	if risk == "" {
		risk = "medium"
	}
	digest := sha256.Sum256([]byte(call.Name + "\x00" + target + "\x00" + risk))
	return invocationScope{Fingerprint: hex.EncodeToString(digest[:]), Target: target, Risk: risk}
}

func normalizedTarget(arguments json.RawMessage) string {
	var object map[string]any
	if json.Unmarshal(arguments, &object) != nil {
		return ""
	}
	for _, key := range []string{"path", "cwd", "command"} {
		if value, ok := object[key].(string); ok && value != "" {
			return filepath.Clean(value)
		}
	}
	for _, key := range []string{"input", "patch"} {
		patch, ok := object[key].(string)
		if !ok {
			continue
		}
		for _, line := range strings.Split(patch, "\n") {
			switch {
			case strings.HasPrefix(line, "¶"):
				if marker := strings.LastIndex(line, "#"); marker > 1 {
					return filepath.Clean(line[len("¶"):marker])
				}
			case strings.HasPrefix(line, "["):
				if marker := strings.LastIndex(line, "#"); marker > 1 {
					return filepath.Clean(line[1:marker])
				}
			}
		}
	}
	return ""
}

func summarizeArguments(arguments json.RawMessage) string {
	var object map[string]any
	if json.Unmarshal(arguments, &object) != nil {
		return "invalid tool arguments"
	}
	for key := range object {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "authorization") || strings.Contains(lower, "header") || strings.Contains(lower, "env") {
			object[key] = "[REDACTED]"
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "tool arguments"
	}
	if len(encoded) > 16*1024 {
		return string(encoded[:16*1024]) + "…"
	}
	return string(encoded)
}

func newID(prefix string) (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}
