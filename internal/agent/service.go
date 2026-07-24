package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Viking602/azem/internal/skills"
	"github.com/Viking602/go-hydaelyn"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/coding"
	"github.com/Viking602/go-hydaelyn/tool"
	"github.com/Viking602/go-hydaelyn/worker"
)

const mainAgentID = "azem-main"

const (
	defaultRunLeaseTTL               = 10 * time.Minute
	defaultRunLeaseHeartbeatInterval = 30 * time.Second
)

type Service struct {
	runner               *hydaelyn.Runner
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
}

type succeededActionAttemptLister interface {
	ListSucceededActionAttempts(context.Context, string, string) ([]api.ActionAttempt, error)
}

var ErrTerminalReportMissing = errors.New("terminal worker report is missing")

type toolCallJournal interface {
	RecordToolCallCharge(context.Context, string, string, string, string, string) (bool, error)
	CountToolCallCharges(context.Context, string, string) (int, error)
}

type executionArgumentsDriver struct {
	inner     tool.Driver
	arguments json.RawMessage
}

func (d executionArgumentsDriver) Definition() tool.Definition { return d.inner.Definition() }

type definitionOverrideDriver struct {
	tool.Driver
	definition tool.Definition
}

func (d definitionOverrideDriver) Definition() tool.Definition { return d.definition }

func (d executionArgumentsDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	call.Arguments = append(json.RawMessage(nil), d.arguments...)
	return d.inner.Execute(ctx, call, sink)
}

type Run struct {
	RunID            string
	Goal             string
	TaskID           string
	LeaseID          string
	TaskVersion      int
	HolderID         string
	pending          map[string]PendingApproval
	approvedOnce     map[string]string
	completedMu      sync.Mutex
	completedEffects map[string]struct{}
	completedCallIDs map[string]string
	chargedToolCalls atomic.Int64
	leaseCancel      context.CancelFunc
	leaseParentStop  func() bool
	leaseDone        <-chan error
	leaseStopOnce    sync.Once
	leaseErr         error
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
	runner, err := hydaelyn.NewProduction(api.Config{StoreProvider: store, PolicyEngine: policy})
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

func (s *Service) Runner() *hydaelyn.Runner { return s.runner }

func (s *Service) SkillSnapshot() skills.Snapshot {
	if s == nil || s.skills == nil {
		return skills.Snapshot{}
	}
	return s.skills.Snapshot()
}

func (s *Service) StartRun(ctx context.Context, request string) (*Run, error) {
	return s.StartRunWithMetadata(ctx, request, nil)
}

func (s *Service) StartRunWithMetadata(ctx context.Context, request string, metadata map[string]string) (*Run, error) {
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
	task, err := s.runner.CreateTask(ctx, api.CreateTaskCommand{
		RunID: run.ID, TaskID: taskID, ParentTaskID: root.ID, Type: api.TaskTypeWorker,
		Goal: request, OwnerAgentID: mainAgentID, AssignedAgentID: mainAgentID, AllowsAction: true,
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
		RunID: run.ID, Goal: request, TaskID: task.ID, LeaseID: lease.ID, TaskVersion: task.Version, HolderID: mainAgentID,
		pending: make(map[string]PendingApproval), approvedOnce: make(map[string]string), completedEffects: make(map[string]struct{}), completedCallIDs: make(map[string]string),
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
		RunID: runID, Goal: task.Goal, TaskID: task.ID, LeaseID: lease.ID, TaskVersion: lease.TaskVersion, HolderID: mainAgentID,
		pending: make(map[string]PendingApproval), approvedOnce: make(map[string]string), completedEffects: make(map[string]struct{}), completedCallIDs: make(map[string]string),
	}
	if tracked.Goal == "" {
		tracked.Goal = durable.Request
	}
	lister, ok := s.store.(succeededActionAttemptLister)
	if !ok {
		_ = s.runner.ReleaseTaskExecution(ctx, api.ReleaseTaskExecutionCommand{LeaseID: lease.ID, HolderID: mainAgentID})
		return nil, fmt.Errorf("resume coding run %s: durable action replay ledger is unavailable", runID)
	}
	attempts, err := lister.ListSucceededActionAttempts(ctx, runID, task.ID)
	if err != nil {
		_ = s.runner.ReleaseTaskExecution(ctx, api.ReleaseTaskExecutionCommand{LeaseID: lease.ID, HolderID: mainAgentID})
		return nil, err
	}
	for _, attempt := range attempts {
		tracked.RestoreCompletedCall(attempt.ActionID, attempt.ToolName, attempt.InputHash)
	}
	journal, ok := s.store.(toolCallJournal)
	if !ok {
		_ = s.runner.ReleaseTaskExecution(ctx, api.ReleaseTaskExecutionCommand{LeaseID: lease.ID, HolderID: mainAgentID})
		return nil, fmt.Errorf("resume coding run %s: durable tool-call journal is unavailable", runID)
	}
	chargedToolCalls, err := journal.CountToolCallCharges(ctx, runID, task.ID)
	if err != nil {
		_ = s.runner.ReleaseTaskExecution(ctx, api.ReleaseTaskExecutionCommand{LeaseID: lease.ID, HolderID: mainAgentID})
		return nil, err
	}
	tracked.chargedToolCalls.Store(int64(chargedToolCalls))
	s.startRunLeaseHeartbeat(ctx, tracked)
	return tracked, nil
}

// RestoreCompletedEffect installs a host-authored crash checkpoint guard. It
// prevents a resumed model from executing the same non-idempotent tool input
// under a newly generated call ID.
func (run *Run) RestoreCompletedEffect(toolName, argumentsSHA256 string) {
	run.RestoreCompletedCall("", toolName, argumentsSHA256)
}

func (run *Run) RestoreCompletedCall(callID, toolName, argumentsSHA256 string) {
	if run == nil || toolName == "" || argumentsSHA256 == "" {
		return
	}
	if run.completedEffects == nil {
		run.completedEffects = make(map[string]struct{})
	}
	if run.completedCallIDs == nil {
		run.completedCallIDs = make(map[string]string)
	}
	run.completedMu.Lock()
	defer run.completedMu.Unlock()
	effectKey := toolName + "\x00" + argumentsSHA256
	run.completedEffects[effectKey] = struct{}{}
	if callID != "" {
		run.completedCallIDs[callID] = effectKey
	}
}

func (run *Run) ChargedToolCalls() int {
	if run == nil {
		return 0
	}
	return int(run.chargedToolCalls.Load())
}

func (s *Service) ChargedToolCalls(ctx context.Context, runID string) (int, error) {
	projection, err := s.runner.Recover(ctx, runID)
	if err != nil {
		return 0, err
	}
	taskID := ""
	for _, task := range projection.Tasks {
		if task.Type == api.TaskTypeWorker {
			if taskID != "" {
				return 0, fmt.Errorf("run %s has multiple worker tasks", runID)
			}
			taskID = task.ID
		}
	}
	if taskID == "" {
		return 0, fmt.Errorf("run %s worker task is missing", runID)
	}
	journal, ok := s.store.(toolCallJournal)
	if !ok {
		return 0, fmt.Errorf("durable tool-call journal is unavailable")
	}
	return journal.CountToolCallCharges(ctx, runID, taskID)
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

// ExecuteDriver applies the same approval policy and durable action-attempt
// boundary used by built-in coding tools to a turn-scoped external driver.
func (s *Service) ExecuteDriver(ctx context.Context, run *Run, driver tool.Driver, call tool.Call, sink tool.UpdateSink) (ExecutionResult, error) {
	if run == nil {
		return ExecutionResult{}, fmt.Errorf("run is nil")
	}
	if driver == nil {
		return ExecutionResult{}, fmt.Errorf("tool driver is nil")
	}
	definition := driver.Definition()
	if call.Name != definition.Name {
		return ExecutionResult{}, fmt.Errorf("tool call %q does not match driver %q", call.Name, definition.Name)
	}
	executionArguments := append(json.RawMessage(nil), call.Arguments...)
	canonicalArguments, err := canonicalToolArguments(call.Arguments)
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("canonicalize %s arguments: %w", call.Name, err)
	}
	digest := sha256.Sum256(canonicalArguments)
	inputHash := fmt.Sprintf("%x", digest[:])
	journal, ok := s.store.(toolCallJournal)
	if !ok {
		return ExecutionResult{}, fmt.Errorf("durable tool-call journal is unavailable")
	}
	charged, err := journal.RecordToolCallCharge(ctx, run.RunID, run.TaskID, call.ID, call.Name, inputHash)
	if err != nil {
		return ExecutionResult{}, err
	}
	if charged {
		run.chargedToolCalls.Add(1)
	}
	nonIdempotentEffect := !definition.Idempotent && !definition.Security.Idempotent &&
		(definition.EffectType == tool.EffectWrite || definition.EffectType == tool.EffectExternalSideEffect)
	effectKey := call.Name + "\x00" + inputHash
	if nonIdempotentEffect {
		run.completedMu.Lock()
		_, completed := run.completedEffects[effectKey]
		sameCall := run.completedCallIDs[call.ID] == effectKey
		run.completedMu.Unlock()
		if completed && !sameCall {
			return ExecutionResult{}, hydaelyn.ErrActionReconcileRequired
		}
	}
	scope := scopeForCall(definition, call)
	needsApproval := definition.RequiresApproval || definition.Security.RequiresApproval || definition.RequiresActionTask || definition.EffectType == tool.EffectWrite || definition.EffectType == tool.EffectExternalSideEffect
	if definition.Metadata["approval"] == "allow" {
		needsApproval = definition.Metadata["network"] == "prompt" && toolCallRequestsNetwork(call.Arguments)
	}
	if needsApproval && !s.policy.sessionGranted(scope.Fingerprint) && run.approvedOnce[call.ID] != scope.Fingerprint {
		if pending, found := run.pending[call.ID]; found && pending.Scope.Fingerprint == scope.Fingerprint {
			return ExecutionResult{Approval: &pending}, nil
		}
		approval, token, err := s.runner.RequestApproval(ctx, api.RequestApprovalCommand{
			RunID: run.RunID, TaskID: run.TaskID, ActionID: call.ID, RequesterAgentID: run.HolderID,
			Reason: fmt.Sprintf("%s requests %s", run.HolderID, call.Name), RiskSummary: scope.Risk + " · " + scope.Target,
			RequestedAction: summarizeArguments(call.Arguments),
		})
		if err != nil {
			return ExecutionResult{}, err
		}
		pending := PendingApproval{Request: approval, Token: token, Call: call, Scope: scope, Effect: string(definition.EffectType), Replayable: definition.Idempotent || definition.Security.Idempotent}
		run.pending[call.ID] = pending
		return ExecutionResult{Approval: &pending}, nil
	}

	delete(run.approvedOnce, call.ID)
	if nonIdempotentEffect {
		run.completedMu.Lock()
		if _, completed := run.completedEffects[effectKey]; completed {
			run.completedMu.Unlock()
			return ExecutionResult{}, hydaelyn.ErrActionReconcileRequired
		}
		run.completedEffects[effectKey] = struct{}{}
		run.completedMu.Unlock()
	}
	governed := worker.GovernedToolBus{
		Runner: s.runner, Bus: tool.NewBus(executionArgumentsDriver{inner: driver, arguments: executionArguments}), RunID: run.RunID, TaskID: run.TaskID, LeaseID: run.LeaseID,
		HolderType: api.HolderAgent, HolderID: run.HolderID, TaskVersion: run.TaskVersion,
	}
	identityCall := call
	identityCall.Arguments = canonicalArguments
	result, err := governed.Execute(withAuthorizedInvocation(ctx, scope), identityCall, sink)
	if err != nil {
		return ExecutionResult{Result: result}, err
	}
	if nonIdempotentEffect && result.IsError {
		run.completedMu.Lock()
		delete(run.completedEffects, effectKey)
		run.completedMu.Unlock()
	} else if nonIdempotentEffect {
		run.completedMu.Lock()
		run.completedCallIDs[call.ID] = effectKey
		run.completedMu.Unlock()
	}
	return ExecutionResult{Result: result, Executed: true}, nil
}

func canonicalToolArguments(arguments json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(arguments)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(value)
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

func (s *Service) ResolveRecoveredApproval(ctx context.Context, runID, approvalID, tokenID, decision string) error {
	switch decision {
	case "once", "session", "approved", "approve":
		decision = "approved"
	case "denied", "deny", "rejected", "reject":
		decision = "rejected"
	default:
		return fmt.Errorf("invalid approval decision %q", decision)
	}
	if err := s.runner.DecideApproval(ctx, api.DecideApprovalCommand{
		RunID: runID, ApprovalID: approvalID, DecidedBy: "user", Decision: decision,
	}); err != nil {
		return err
	}
	if tokenID != "" {
		if _, err := s.runner.RecoverResumeToken(ctx, api.RecoverResumeTokenCommand{TokenID: tokenID}); err != nil {
			return err
		}
	}
	return nil
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
		// gofmt is inherently idempotent: formatting the same path again either
		// applies the current canonical format or reports no change. Hydaelyn
		// v0.11.9 omits this metadata, which makes the anti-replay guard reject a
		// legitimate second format after another edit as reconciliation-required.
		if definition.Name == coding.ToolGofmt && !definition.Idempotent {
			definition.Idempotent = true
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

// ExecuteTeamDriver records a side-effect attempt for a TeamRunner worker
// before invoking the raw driver. Hydaelyn v0.10.1 drops Task.AllowsAction
// while materializing multi-agent dispatches, so the adapter restores that
// durable task capability before crossing the side-effect boundary.
func (s *Service) ExecuteTeamDriver(ctx context.Context, runID string, driver tool.Driver, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	if s == nil || s.runner == nil || s.store == nil || driver == nil {
		return tool.Result{}, fmt.Errorf("team tool runtime is unavailable")
	}
	if strings.TrimSpace(runID) == "" {
		return tool.Result{}, fmt.Errorf("team tool run ID is missing")
	}
	definition := driver.Definition()
	ctx = withAuthorizedInvocation(ctx, scopeForCall(definition, call))
	task, lease, err := s.enableTeamTaskActions(ctx, runID)
	if err != nil {
		return tool.Result{}, err
	}
	attemptID, err := newID("attempt")
	if err != nil {
		return tool.Result{}, err
	}
	attempt, err := s.runner.StartActionAttempt(ctx, api.StartActionAttemptCommand{
		AttemptID:      attemptID,
		ActionID:       call.ID,
		RunID:          runID,
		TaskID:         task.ID,
		LeaseID:        lease.ID,
		HolderType:     lease.HolderType,
		HolderID:       lease.HolderID,
		TaskVersion:    task.Version,
		ToolName:       call.Name,
		IdempotencyKey: call.ID,
		InputHash:      fmt.Sprintf("%x", sha256.Sum256(call.Arguments)),
	})
	if err != nil {
		return tool.Result{}, err
	}
	if attempt.AttemptID != attemptID || attempt.RequiresReconcile || attempt.Status != api.ActionAttemptRunning {
		return tool.Result{}, hydaelyn.ErrActionReconcileRequired
	}
	result, executeErr := driver.Execute(ctx, call, sink)
	status := api.ActionAttemptSucceeded
	requiresReconcile := false
	switch {
	case executeErr != nil:
		status = api.ActionAttemptUnknown
		requiresReconcile = true
	case result.IsError:
		status = api.ActionAttemptFailed
	}
	_, completeErr := s.runner.CompleteActionAttempt(context.WithoutCancel(ctx), api.CompleteActionAttemptCommand{
		RunID:             runID,
		TaskID:            task.ID,
		LeaseID:           lease.ID,
		HolderType:        lease.HolderType,
		HolderID:          lease.HolderID,
		TaskVersion:       task.Version,
		AttemptID:         attempt.AttemptID,
		Status:            status,
		RequiresReconcile: requiresReconcile,
	})
	if executeErr != nil || completeErr != nil {
		return result, errors.Join(executeErr, completeErr)
	}
	return result, nil
}

func (s *Service) enableTeamTaskActions(ctx context.Context, runID string) (api.Task, api.TaskExecutionLease, error) {
	uow, err := s.store.Begin(ctx)
	if err != nil {
		return api.Task{}, api.TaskExecutionLease{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(context.WithoutCancel(ctx))
		}
	}()
	tasks, err := uow.Tasks().ListTasks(ctx, runID)
	if err != nil {
		return api.Task{}, api.TaskExecutionLease{}, err
	}
	var task api.Task
	var lease api.TaskExecutionLease
	for _, candidate := range tasks {
		candidateLease, found, loadErr := uow.Leases().ActiveLeaseForTask(ctx, runID, candidate.ID)
		if loadErr != nil {
			return api.Task{}, api.TaskExecutionLease{}, loadErr
		}
		if !found || candidateLease.Status != api.LeaseStatusActive || !candidateLease.ExpiresAt.After(time.Now()) || candidateLease.HolderType != api.HolderAgent {
			continue
		}
		if task.ID != "" {
			return api.Task{}, api.TaskExecutionLease{}, fmt.Errorf("team run %q has multiple active agent tasks", runID)
		}
		task, lease = candidate, candidateLease
	}
	if task.ID == "" {
		return api.Task{}, api.TaskExecutionLease{}, api.ErrLeaseNotActive
	}
	if !task.AllowsAction {
		task.AllowsAction = true
		if err := uow.Tasks().SaveTask(ctx, task); err != nil {
			return api.Task{}, api.TaskExecutionLease{}, err
		}
	}
	if err := uow.Commit(ctx); err != nil {
		return api.Task{}, api.TaskExecutionLease{}, err
	}
	committed = true
	return task, lease, nil
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
	projection, err := s.runner.Recover(ctx, run.RunID)
	if err != nil {
		return fmt.Errorf("project reported run: %w", err)
	}
	if projection.Run.Status == target {
		return nil
	}
	if target == api.RunStatusCompleted {
		if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: run.RunID, To: api.RunStatusComposingResponse}); err != nil {
			return fmt.Errorf("compose run response from %s: %w", projection.Run.Status, err)
		}
	}
	if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: run.RunID, To: target}); err != nil {
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
