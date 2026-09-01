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
	"maps"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/azem/internal/skills"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/durable"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

const (
	mainAgentID              = "azem-main"
	durableLeaseTTL          = 2 * time.Minute
	durableSettlementTimeout = 35 * time.Second
)

const (
	approvalMetadataExecutionID   = "azem.execution_id"
	approvalMetadataOperationID   = "azem.operation_id"
	approvalMetadataScope         = "azem.scope_fingerprint"
	singleRunMetadataAgentID      = "azem.single_agent_id"
	singleRunMetadataAgentVersion = "azem.single_agent_version"
	singleRunMetadataGovernance   = "azem.single_governance"
)

type recoveredApprovalDecision struct {
	fingerprint string
	approved    bool
}

type runtimeStore interface {
	agentruntime.StoreProvider
	agentruntime.ExecutionBindingRepository
	agentruntime.RunExecutionRepository
	agentruntime.DurableAttemptRepository
	DurableBackend() durable.Backend
}

type activeRun struct {
	executionID string
	cancel      context.CancelCauseFunc
	done        chan struct{}
}

type Service struct {
	store              runtimeStore
	durable            *durable.Runtime
	runtimeOwnerID     string
	workspace          Workspace
	tools              *tool.Bus
	workspaceRoot      string
	externalDrivers    []tool.Driver
	managedSkillDriver tool.Driver
	policy             *ApprovalPolicy
	allowWrite         bool
	shellPolicy        string
	allowNetwork       string
	shellRuntime       *shellRuntime
	toolGovernor       toolPolicyGovernor
	teamMaxConcurrency int
	teamMaxTicks       int
	skills             *skills.Catalog
	memory             *memory.Service
	resources          *resource.Router
	hashlineClipboard  *hashlineClipboard
	ast                *astBridge
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	lifecycleMu        sync.Mutex
	closed             bool
	closeDone          chan struct{}
	closeErr           error
	lsp                *lspBridgeRuntime
	jobs               *backgroundJobManager
	hubPeers           *hubPeerBrokerRef
	fileBroker         *fileMutationBrokerRef
	singleRunMu        sync.Mutex
	singleRuns         map[string]activeRun
	approvalMu         sync.Mutex
	recoveredApprovals map[string]map[string]recoveredApprovalDecision
	externalMu         sync.Mutex
	externalClosers    []func(context.Context) error
}

const hashlineEditToolDescription = `Apply an OMP Hashline patch to existing files. Reuse exact [PATH#TAG] headers and N:TEXT anchors from the latest read/search/edit result. Input is:
*** Begin Patch
[path#ABCD]
PUT N.=M:
+final content
CUT N.=M
PUT <N: or PUT >N: for insertion; PUT >$: for tail insertion; PUT N*: or CUT N* for a syntactic block. CUT may capture @name and colonless PUT may paste it; named registers persist across calls. REM deletes the section file. MV DEST moves it after prior edits.
*** End Patch
All line numbers name the original snapshot. Colon PUT body rows each start with + and contain final content only. Register PUT/CUT/REM/MV have no body. Never send unified @@ hunks, -old/context rows, or widen ranges over lines that remain unchanged. Re-read only unseen or renumbered lines, stale/conflicting tags, or surprising results.`

const hashlineRetryGuidance = `Required OMP Hashline retry format:
*** Begin Patch
[PATH#TAG]
PUT N.=M:
+final content only
*** End Patch
Reuse current [PATH#TAG] and original line numbers when a syntax/no-op rejection left the file unchanged. Re-read only for a stale/file-changed tag or unseen lines. Use PUT/CUT locators; never use @@ hunks, -old rows, or bare context.`

type definitionOverrideDriver struct {
	tool.Driver
	definition tool.Definition
}

func (d definitionOverrideDriver) Definition() tool.Definition { return d.definition }

func (d definitionOverrideDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	descriptor, err := DescribeTool(d.Driver)
	if err != nil {
		return conservativeToolPolicy(d.definition.Name)
	}
	return descriptor.PolicyForCall(call)
}

type goTestStatusDriver struct {
	tool.Driver
}

func (driver goTestStatusDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	result, err := driver.Driver.Execute(ctx, call, sink)
	if err != nil || result.IsError || len(result.Structured) == 0 {
		return result, err
	}
	var status GoTestToolResult
	if json.Unmarshal(result.Structured, &status) == nil && !status.Passed {
		result.IsError = true
	}
	return result, err
}

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
	if recovery == nil || call.Name != ToolEditHashline {
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
			target, ToolReadFile,
		),
		IsError: true,
	}, true
}

func addHashlineRetryGuidance(call tool.Call, result tool.Result) tool.Result {
	if call.Name != ToolEditHashline || !result.IsError || strings.Contains(result.Content, "Required OMP Hashline retry format:") {
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
	case ToolEditHashline:
		if executionErr == nil && !result.IsError {
			return
		}
		if !result.IsError || !hashlineFailureRequiresRead(result.Content) {
			return
		}
		if recovery.readRequired == nil {
			recovery.readRequired = make(map[string]struct{})
		}
		recovery.readRequired[target] = struct{}{}
	case ToolReadFile:
		if executionErr != nil || result.IsError {
			return
		}
		delete(recovery.readRequired, target)
		delete(recovery.readRequired, "workspace")
	}
}

func hashlineFailureRequiresRead(content string) bool {
	if index := strings.Index(content, "Required OMP Hashline retry format:"); index >= 0 {
		content = content[:index]
	}
	content = strings.ToLower(content)
	for _, marker := range []string{
		"tag is stale",
		"snapshot tag does not match",
		"stale edit conflicts",
		"file changed since you read it",
		" is stale; current tag is ",
	} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

type RunExecutionPolicy struct {
	AgentID           string
	AgentVersion      string
	Governance        agentruntime.GovernancePolicy
	Budget            *agentruntime.TaskBudget
	OutputSchema      json.RawMessage
	RetryPolicy       agentruntime.RetryPolicy
	ResourceClaims    []agentruntime.ResourceClaimSpec
	ExecutableProfile *agentruntime.ExecutableProfile
}

type Run struct {
	RunID        string
	Goal         string
	TaskID       string
	EnvelopeID   string
	LeaseID      string
	ExecutionID  string
	TaskVersion  int
	HolderID     string
	binding      agentruntime.ExecutionBinding
	resumeTarget durable.ResumeTarget
	pending      map[string]PendingApproval
	approvedOnce map[string]string
	editRecovery EditRecovery
}

type PendingApproval struct {
	Request    agentruntime.ApprovalRequest
	Token      agentruntime.ResumeToken
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
	resources          *resource.Router
	memory             *memory.Service
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

func WithResourceRouter(router *resource.Router) ServiceOption {
	return func(options *serviceOptions) {
		options.resources = router
	}
}

func WithMemory(service *memory.Service) ServiceOption {
	return func(options *serviceOptions) {
		options.memory = service
	}
}

func WithShellOptions(options ShellOptions) ServiceOption {
	return func(settings *serviceOptions) { settings.shellOptions = options }
}

func NewService(store runtimeStore, workspaceRoot string, options ...ServiceOption) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("agent runtime store is nil")
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, fmt.Errorf("workspace root is empty")
	}
	settings := serviceOptions{allowWrite: true, shellPolicy: "prompt", network: "prompt", teamMaxConcurrency: 2, teamMaxTicks: 12}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	policy := NewApprovalPolicy()
	workspace := NewLocalWorkspace(workspaceRoot)
	serviceCtx, serviceCancel := context.WithCancel(context.Background())
	service := &Service{
		store: store, workspace: workspace, workspaceRoot: workspace.Root(), policy: policy,
		allowWrite: settings.allowWrite, shellPolicy: settings.shellPolicy, allowNetwork: settings.network,
		teamMaxConcurrency: settings.teamMaxConcurrency, teamMaxTicks: settings.teamMaxTicks,
		skills: settings.skills, resources: settings.resources, memory: settings.memory, hashlineClipboard: newHashlineClipboard(), ast: newASTBridge(), lsp: newLSPBridgeRuntime(), jobs: newBackgroundJobManager(serviceCtx), hubPeers: &hubPeerBrokerRef{}, fileBroker: &fileMutationBrokerRef{},
		ctx: serviceCtx, cancel: serviceCancel,
		singleRuns:         make(map[string]activeRun),
		recoveredApprovals: make(map[string]map[string]recoveredApprovalDecision),
		closeDone:          make(chan struct{}),
	}
	constructed := false
	defer func() {
		if constructed {
			return
		}
		serviceCancel()
		if service.shellRuntime != nil {
			service.shellRuntime.shutdown()
		}
		_ = service.jobs.shutdown(context.Background())
		_ = service.lsp.Close(context.Background())
	}()
	if settings.resources != nil && settings.resources.Handler("xd") == nil {
		if err := settings.resources.Register("xd", newASTXDevHandler(service.ast, settings.resources)); err != nil {
			return nil, fmt.Errorf("register xd resources: %w", err)
		}
	}
	if settings.resources != nil && settings.resources.Handler("ssh") == nil {
		if err := settings.resources.Register("ssh", newSSHResourceHandler(service.lsp, settings.network)); err != nil {
			return nil, fmt.Errorf("register ssh resources: %w", err)
		}
	}
	if settings.resources != nil && settings.memory != nil && settings.resources.Handler("memory") == nil {
		if err := settings.resources.Register("memory", memoryResourceHandler{memory: settings.memory}); err != nil {
			return nil, fmt.Errorf("register memory resources: %w", err)
		}
	}
	service.shellRuntime = newShellRuntime(serviceCtx, settings.shellOptions)
	drivers, err := service.WorkspaceDrivers(context.Background(), service.workspaceRoot)
	if err != nil {
		return nil, err
	}
	service.tools = tool.NewBus(drivers...)
	service.runtimeOwnerID, err = newID("runtime_owner")
	if err != nil {
		return nil, fmt.Errorf("create durable runtime owner: %w", err)
	}
	service.durable, err = durable.New(store.DurableBackend(), durable.Options{
		OwnerID:           service.runtimeOwnerID,
		LeaseTTL:          durableLeaseTTL,
		SettlementTimeout: durableSettlementTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create durable agent runtime: %w", err)
	}
	constructed = true
	return service, nil
}

func (s *Service) beginRuntimeWork() error {
	if s == nil {
		return fmt.Errorf("agent service is nil")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed {
		return fmt.Errorf("agent service: %w", durable.ErrClosed)
	}
	s.wg.Add(1)
	return nil
}

func (s *Service) endRuntimeWork() {
	s.wg.Done()
}

func (s *Service) SetHubPeerBroker(broker HubPeerBroker) {
	if s != nil {
		s.hubPeers.set(broker)
	}
}

func (s *Service) SetFileMutationBroker(broker FileMutationBroker) {
	if s != nil {
		s.fileBroker.set(broker)
	}
}

type reconciliationEvidence struct {
	ModelEvents       []hyprovider.Event     `json:"model_events,omitempty"`
	ToolResult        *tool.Result           `json:"tool_result,omitempty"`
	Failure           *durable.FailureRecord `json:"failure,omitempty"`
	ExternalResultRef string                 `json:"external_result_ref,omitempty"`
}

func (s *Service) ResolveReconcileAttempt(ctx context.Context, attemptID string, status agentruntime.ActionAttemptStatus, payload json.RawMessage) error {
	attempt, err := s.store.LoadDurableReconcileAttempt(ctx, attemptID)
	if err == nil {
		return s.resolveDurableReconcileAttempt(ctx, attempt, status, payload)
	}
	if !errors.Is(err, agentruntime.ErrNotFound) {
		return err
	}
	return s.resolveLegacyReconcileAttempt(ctx, attemptID, status, payload)
}

func (s *Service) resolveDurableReconcileAttempt(ctx context.Context, attempt agentruntime.ActionAttempt, status agentruntime.ActionAttemptStatus, payload json.RawMessage) error {
	evidence, err := decodeReconciliationEvidence(payload)
	if err != nil {
		return err
	}
	reconciliation := durable.Reconciliation{
		AttemptNumber:  attempt.AttemptNumber,
		AttemptVersion: attempt.AttemptVersion,
	}
	switch status {
	case agentruntime.ActionAttemptSucceeded:
		reconciliation.Resolution = durable.ReconcileResolutionSucceed
		switch attempt.AttemptKind {
		case string(durable.AttemptKindModel):
			if len(evidence.ModelEvents) == 0 || evidence.ToolResult != nil || evidence.Failure != nil {
				return fmt.Errorf("reconcile model attempt: a complete model event sequence is required")
			}
			reconciliation.ModelEvents = evidence.ModelEvents
		case string(durable.AttemptKindTool):
			if evidence.ToolResult == nil || len(evidence.ModelEvents) != 0 || evidence.Failure != nil {
				return fmt.Errorf("reconcile tool attempt: a canonical tool result is required")
			}
			reconciliation.ToolResult = evidence.ToolResult
		default:
			return fmt.Errorf("reconcile attempt: unknown attempt kind %q", attempt.AttemptKind)
		}
	case agentruntime.ActionAttemptFailed, agentruntime.ActionAttemptTimeout, agentruntime.ActionAttemptCancelled:
		if len(evidence.ModelEvents) != 0 || evidence.ToolResult != nil {
			return fmt.Errorf("reconcile failed attempt: success evidence is not allowed")
		}
		reconciliation.Resolution = durable.ReconcileResolutionFail
		reconciliation.Failure = evidence.Failure
		if reconciliation.Failure == nil {
			reconciliation.Failure = &durable.FailureRecord{
				Code: string(status), Message: "operator reconciled the durable attempt as " + string(status),
			}
		}
	case agentruntime.ActionAttemptRetry:
		if len(evidence.ModelEvents) != 0 || evidence.ToolResult != nil || evidence.Failure != nil {
			return fmt.Errorf("reconcile retry attempt: evidence is not allowed")
		}
		reconciliation.Resolution = durable.ReconcileResolutionRetry
	default:
		return fmt.Errorf("reconcile action attempt: succeed, fail, or retry resolution required")
	}
	if err := s.durable.Reconcile(ctx, durable.ExecutionID(attempt.ExecutionID), attempt.OperationID, reconciliation); err != nil {
		return err
	}
	remaining, err := s.store.ListDurableReconcileAttempts(ctx)
	if err != nil {
		return err
	}
	for _, current := range remaining {
		if current.ExecutionID == attempt.ExecutionID {
			return nil
		}
	}
	binding, err := s.store.LoadExecutionBinding(ctx, attempt.ExecutionID)
	if err != nil {
		return err
	}
	if binding.State == agentruntime.ExecutionBindingReconcileRequired {
		if err := s.finishExecutionBinding(ctx, attempt.ExecutionID, agentruntime.ExecutionBindingSuspended); err != nil {
			return err
		}
	}
	return s.clearRunReconciliation(ctx, attempt.RunID, attempt.TaskID)
}

func (s *Service) resolveLegacyReconcileAttempt(ctx context.Context, attemptID string, status agentruntime.ActionAttemptStatus, payload json.RawMessage) error {
	switch status {
	case agentruntime.ActionAttemptSucceeded, agentruntime.ActionAttemptFailed, agentruntime.ActionAttemptTimeout, agentruntime.ActionAttemptCancelled:
	default:
		return fmt.Errorf("reconcile legacy action attempt: terminal status required")
	}
	evidence, err := decodeReconciliationEvidence(payload)
	if err != nil {
		return err
	}
	if len(evidence.ModelEvents) != 0 || evidence.ToolResult != nil || evidence.Failure != nil {
		return fmt.Errorf("reconcile legacy action attempt: durable evidence is not supported")
	}
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	attempt, err := work.ActionAttempts().LoadActionAttempt(ctx, attemptID)
	if err != nil {
		return err
	}
	if attempt.Status != agentruntime.ActionAttemptUnknown || !attempt.RequiresReconcile {
		return agentruntime.ErrConflict
	}
	attempt.Status = status
	attempt.RequiresReconcile = false
	attempt.ExternalResultRef = evidence.ExternalResultRef
	if attempt.ExternalResultRef == "" {
		attempt.ExternalResultRef = "operator-reconciled"
	}
	resolved, err := work.ActionAttempts().ResolveActionAttempt(ctx, attempt)
	if err != nil {
		return err
	}
	if !resolved {
		return agentruntime.ErrConflict
	}
	if err := clearReconciliationInWork(ctx, work, attempt.RunID, attempt.TaskID); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func decodeReconciliationEvidence(payload json.RawMessage) (reconciliationEvidence, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return reconciliationEvidence{}, nil
	}
	var evidence reconciliationEvidence
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return reconciliationEvidence{}, fmt.Errorf("decode reconciliation evidence: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return reconciliationEvidence{}, fmt.Errorf("decode reconciliation evidence: multiple JSON values")
		}
		return reconciliationEvidence{}, fmt.Errorf("decode reconciliation evidence: %w", err)
	}
	return evidence, nil
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
	if err := s.beginRuntimeWork(); err != nil {
		return nil, err
	}
	defer s.endRuntimeWork()
	runID, err := newID("run")
	if err != nil {
		return nil, err
	}
	rootID, err := newID("root")
	if err != nil {
		return nil, err
	}
	taskID, err := newID("task")
	if err != nil {
		return nil, err
	}
	envelopeID, err := newID("envelope")
	if err != nil {
		return nil, err
	}
	var executionPolicy RunExecutionPolicy
	if len(policies) > 0 {
		executionPolicy = policies[0]
	}
	agentID := strings.TrimSpace(executionPolicy.AgentID)
	if agentID == "" {
		agentID = mainAgentID
	}
	agentVersion := strings.TrimSpace(executionPolicy.AgentVersion)
	if agentVersion == "" {
		agentVersion = "runtime"
	}
	governance, err := json.Marshal(executionPolicy.Governance)
	if err != nil {
		return nil, fmt.Errorf("encode coding run governance: %w", err)
	}
	durableMetadata := maps.Clone(metadata)
	if durableMetadata == nil {
		durableMetadata = make(map[string]string, 3)
	}
	durableMetadata[singleRunMetadataAgentID] = agentID
	durableMetadata[singleRunMetadataAgentVersion] = agentVersion
	durableMetadata[singleRunMetadataGovernance] = string(governance)
	durableMetadata["task_id"] = taskID
	kind := executionKind(agentID, durableMetadata)
	executionID, err := agentruntime.ExecutionID(kind, runID, 0)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	budget := taskBudget(executionPolicy.Budget)
	manifest := agentruntime.ExecutionManifest{
		Version: agentruntime.ExecutionManifestVersion, SessionID: durableMetadata["session_id"], RunID: runID,
		AgentID: agentID, StableID: runID, AgentVersion: agentVersion, Kind: kind, Segment: 0, Prompt: request,
		Budget: budget, OutputSchema: append(json.RawMessage(nil), executionPolicy.OutputSchema...),
		Governance: executionPolicy.Governance, RetryPolicy: executionPolicy.RetryPolicy,
		ResourceClaims: append([]agentruntime.ResourceClaimSpec(nil), executionPolicy.ResourceClaims...),
		Metadata:       maps.Clone(durableMetadata), WorkspaceAnchor: s.workspaceRoot, StartedAt: now,
	}
	if executionPolicy.ExecutableProfile != nil {
		applyExecutableProfile(&manifest, *executionPolicy.ExecutableProfile)
		manifest.Sealed = true
		if err := validateExecutableProfile(manifest); err != nil {
			return nil, err
		}
	}
	manifest.ProfileHash, err = executionProfileHash(manifest)
	if err != nil {
		return nil, err
	}
	binding := agentruntime.ExecutionBinding{
		ExecutionID: executionID, SessionID: manifest.SessionID, RunID: runID, StableID: runID, AgentID: agentID, Kind: kind,
		Segment: 0, Manifest: manifest, ProfileHash: manifest.ProfileHash, State: agentruntime.ExecutionBindingPending,
	}
	runRecord := agentruntime.Run{
		ID: runID, Status: agentruntime.RunStatusRunning, Request: request, RootTaskID: rootID,
		AgentVersion: agentVersion, Metadata: durableMetadata, CreatedAt: now, UpdatedAt: now,
	}
	taskRecord := agentruntime.Task{
		ID: taskID, RunID: runID, Type: agentruntime.TaskTypeWorker, Goal: request, AssignedAgentID: agentID,
		OwnerAgentID: agentID, OwnerComponent: "azem", Status: agentruntime.TaskStatusDispatched, Version: 1,
		AllowsAction: true, Budget: cloneTaskBudget(executionPolicy.Budget),
		OutputSchema: append(json.RawMessage(nil), executionPolicy.OutputSchema...),
		RetryPolicy:  executionPolicy.RetryPolicy, ResourceClaims: append([]agentruntime.ResourceClaimSpec(nil), executionPolicy.ResourceClaims...),
		CreatedAt: now, UpdatedAt: now,
	}
	envelope := agentruntime.TaskEnvelope{
		ID: envelopeID, RunID: runID, TaskID: taskID, TargetAgentID: agentID, TargetComponent: "azem",
		TaskVersion: 1, Status: agentruntime.EnvelopeStatusAcked, CreatedAt: now, DeliveredAt: now,
	}
	if err := s.store.CreateRunExecution(ctx, runRecord, taskRecord, envelope, binding); err != nil {
		return nil, err
	}
	binding.Version = 1
	binding.UpdatedAt = now
	return runFromBinding(binding, taskRecord, envelope), nil
}

func (s *Service) ResumeRun(ctx context.Context, runID string) (*Run, error) {
	return s.resumeRun(ctx, runID, "")
}

// ClassifyRunRecovery validates whether a non-terminal application run has an
// exact v1 durable execution to replay. Legacy runs and invalid bindings are
// moved to explicit reconciliation before any provider or tool can run.
func (s *Service) ClassifyRunRecovery(ctx context.Context, runID string) (kind string, replay bool, err error) {
	runRecord, _, _, err := s.loadRunAggregate(ctx, runID)
	if err != nil {
		return "", false, err
	}
	agentID := strings.TrimSpace(runRecord.Metadata[singleRunMetadataAgentID])
	if agentID == "" {
		agentID = mainAgentID
	}
	kind = executionKind(agentID, runRecord.Metadata)
	binding, err := s.store.LoadLatestExecutionBinding(ctx, runID, agentID, kind)
	if errors.Is(err, agentruntime.ErrNotFound) {
		if err := s.RequireRunReconciliation(ctx, runID, "legacy run has no v1 continuation"); err != nil {
			return kind, false, err
		}
		return kind, false, nil
	}
	if err != nil {
		return kind, false, err
	}
	reconcile := func(reason string) (string, bool, error) {
		if err := s.RequireRunReconciliation(ctx, runID, reason); err != nil {
			return kind, false, err
		}
		return kind, false, nil
	}
	if binding.State == agentruntime.ExecutionBindingReconcileRequired {
		return kind, false, nil
	}
	if binding.RunID != runID || binding.AgentID != agentID || binding.Kind != kind ||
		binding.Manifest.RunID != runID || binding.Manifest.AgentID != agentID ||
		binding.Manifest.Kind != kind || binding.Manifest.Version != agentruntime.ExecutionManifestVersion {
		return reconcile("durable execution identity does not match the recoverable run")
	}
	if !binding.Manifest.Sealed {
		return reconcile("durable execution profile is not sealed")
	}
	if err := validateExecutableProfile(binding.Manifest); err != nil {
		return reconcile(err.Error())
	}
	profileHash, err := executionProfileHash(binding.Manifest)
	if err != nil {
		return kind, false, err
	}
	if binding.ProfileHash == "" || binding.ProfileHash != binding.Manifest.ProfileHash || profileHash != binding.ProfileHash {
		return reconcile("durable execution profile hash changed")
	}
	if binding.State == agentruntime.ExecutionBindingPending {
		return kind, true, nil
	}
	execution, err := s.store.DurableBackend().LoadExecution(ctx, durable.ExecutionID(binding.ExecutionID))
	if errors.Is(err, durable.ErrNotFound) {
		return reconcile("durable execution state is missing")
	}
	if err != nil {
		return kind, false, err
	}
	if execution.ID != durable.ExecutionID(binding.ExecutionID) {
		return reconcile("durable execution identity changed")
	}
	switch binding.State {
	case agentruntime.ExecutionBindingRunning, agentruntime.ExecutionBindingSuspended:
		if execution.Status != durable.ExecutionStatusRunning && execution.Status != durable.ExecutionStatusSuspended {
			return reconcile("durable execution state disagrees with the recoverable run")
		}
	case agentruntime.ExecutionBindingCompleted:
		if execution.Status != durable.ExecutionStatusCompleted {
			return reconcile("durable terminal state disagrees with the completed binding")
		}
	case agentruntime.ExecutionBindingFailed:
		if execution.Status != durable.ExecutionStatusFailed {
			return reconcile("durable terminal state disagrees with the failed binding")
		}
	case agentruntime.ExecutionBindingCancelled:
		return kind, false, nil
	default:
		return reconcile(fmt.Sprintf("durable execution has unsupported binding state %q", binding.State))
	}
	return kind, true, nil
}

// ResumeChildRun rebuilds an app-owned child execution. A child that has not
// started yet uses the ordinary pending path. Approval suspension resumes the
// selected operation; other suspension replays the checkpoint normally.
func (s *Service) ResumeChildRun(ctx context.Context, runID string) (*Run, error) {
	runRecord, _, _, err := s.loadRunAggregate(ctx, runID)
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(runRecord.Metadata[singleRunMetadataAgentID])
	if agentID == "" {
		agentID = mainAgentID
	}
	binding, err := s.store.LoadLatestExecutionBinding(ctx, runID, agentID, executionKind(agentID, runRecord.Metadata))
	if err != nil {
		return nil, err
	}
	if binding.State == agentruntime.ExecutionBindingPending {
		return s.ResumeRun(ctx, runID)
	}
	execution, err := s.store.DurableBackend().LoadExecution(ctx, durable.ExecutionID(binding.ExecutionID))
	if err != nil {
		return nil, err
	}
	if execution.Checkpoint == nil || execution.Checkpoint.Continuation.Phase != hyagent.ContinuationModelComplete {
		return s.ResumeRun(ctx, runID)
	}
	operationID, err := s.resolvedApprovalOperation(ctx, runID, binding.ExecutionID, execution)
	if err != nil {
		if errors.Is(err, agentruntime.ErrNotFound) {
			return s.ResumeRun(ctx, runID)
		}
		return nil, err
	}
	return s.ResumeRunAtOperation(ctx, runID, operationID)
}

// ResumeRunAtOperation rebuilds a suspended execution at the exact operation
// that was approved. Venat validates the checkpoint sequence, phase, and
// operation before opening another model stream or invoking a tool.
func (s *Service) ResumeRunAtOperation(ctx context.Context, runID, operationID string) (*Run, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return nil, fmt.Errorf("resume operation id is empty")
	}
	return s.resumeRun(ctx, runID, operationID)
}

// ResumeRunAfterApproval resumes the operation selected by the latest durable
// approval decision in the current model-complete checkpoint. It is used by
// independently owned child executions whose controller observes the durable
// decision asynchronously.
func (s *Service) ResumeRunAfterApproval(ctx context.Context, runID string) (*Run, error) {
	runRecord, _, _, err := s.loadRunAggregate(ctx, runID)
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(runRecord.Metadata[singleRunMetadataAgentID])
	if agentID == "" {
		agentID = mainAgentID
	}
	binding, err := s.store.LoadLatestExecutionBinding(ctx, runID, agentID, executionKind(agentID, runRecord.Metadata))
	if err != nil {
		return nil, err
	}
	execution, err := s.store.DurableBackend().LoadExecution(ctx, durable.ExecutionID(binding.ExecutionID))
	if err != nil {
		return nil, err
	}
	operationID, err := s.resolvedApprovalOperation(ctx, runID, binding.ExecutionID, execution)
	if err != nil {
		return nil, err
	}
	return s.ResumeRunAtOperation(ctx, runID, operationID)
}

func (s *Service) resumeRun(ctx context.Context, runID, operationID string) (*Run, error) {
	if err := s.beginRuntimeWork(); err != nil {
		return nil, err
	}
	defer s.endRuntimeWork()
	runRecord, taskRecord, envelope, err := s.loadRunAggregate(ctx, runID)
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(runRecord.Metadata[singleRunMetadataAgentID])
	if agentID == "" {
		agentID = mainAgentID
	}
	kind := executionKind(agentID, runRecord.Metadata)
	binding, err := s.store.LoadLatestExecutionBinding(ctx, runID, agentID, kind)
	if err != nil {
		if errors.Is(err, agentruntime.ErrNotFound) {
			_ = s.markLegacyReconciliation(ctx, runRecord, taskRecord, "legacy run has no v1 continuation")
		}
		return nil, err
	}
	resumed := runFromBinding(binding, taskRecord, envelope)
	if binding.State == agentruntime.ExecutionBindingPending {
		if operationID != "" {
			return nil, fmt.Errorf("run %s has no durable checkpoint for operation %s: %w", runID, operationID, agentruntime.ErrConflict)
		}
		return resumed, nil
	}
	execution, err := s.store.DurableBackend().LoadExecution(ctx, durable.ExecutionID(binding.ExecutionID))
	if err != nil {
		return nil, err
	}
	resumed.resumeTarget, err = resumeTargetForOperation(execution, operationID)
	if err != nil {
		return nil, err
	}
	return resumed, nil
}

func (s *Service) ExecuteRun(ctx context.Context, run *Run, engine hyagent.Engine, sink hyagent.Sink) (ExecutionOutcome, error) {
	if err := s.beginRuntimeWork(); err != nil {
		return ExecutionOutcome{}, err
	}
	defer s.endRuntimeWork()
	if run == nil {
		return ExecutionOutcome{}, fmt.Errorf("run is nil")
	}
	binding, err := s.store.LoadExecutionBinding(ctx, run.ExecutionID)
	if err != nil {
		return ExecutionOutcome{}, err
	}
	if binding.ProfileHash != binding.Manifest.ProfileHash {
		return ExecutionOutcome{}, fmt.Errorf("run %s execution profile changed: %w", run.RunID, agentruntime.ErrConflict)
	}
	outcome := ExecutionOutcome{RunID: run.RunID, TaskID: run.TaskID, LeaseID: run.ExecutionID}
	switch binding.State {
	case agentruntime.ExecutionBindingCancelled:
		outcome.State = ExecutionCancelled
		return outcome, nil
	case agentruntime.ExecutionBindingReconcileRequired:
		outcome.State = ExecutionSuspended
		outcome.Suspension = &Suspension{
			Kind:   SuspensionReconciliation,
			Reason: "durable execution requires explicit reconciliation",
		}
		return outcome, nil
	case agentruntime.ExecutionBindingPending,
		agentruntime.ExecutionBindingRunning,
		agentruntime.ExecutionBindingSuspended,
		agentruntime.ExecutionBindingCompleted,
		agentruntime.ExecutionBindingFailed:
	default:
		return ExecutionOutcome{}, fmt.Errorf("run %s has unsupported execution state %q: %w", run.RunID, binding.State, agentruntime.ErrConflict)
	}
	if !binding.Manifest.Sealed {
		return ExecutionOutcome{}, fmt.Errorf("run %s execution profile is not sealed: %w", run.RunID, agentruntime.ErrConflict)
	}
	if err := validateExecutableProfile(binding.Manifest); err != nil {
		return ExecutionOutcome{}, err
	}
	profileHash, err := executionProfileHash(binding.Manifest)
	if err != nil {
		return ExecutionOutcome{}, err
	}
	if profileHash != binding.ProfileHash {
		return ExecutionOutcome{}, fmt.Errorf("run %s execution profile hash changed: %w", run.RunID, agentruntime.ErrConflict)
	}

	executionCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	activeDone := make(chan struct{})
	s.singleRunMu.Lock()
	if _, exists := s.singleRuns[run.RunID]; exists {
		s.singleRunMu.Unlock()
		cancel(durable.ErrBusy)
		return ExecutionOutcome{}, &TaskExecutionUnavailableError{TaskID: run.TaskID}
	}
	s.singleRuns[run.RunID] = activeRun{executionID: run.ExecutionID, cancel: cancel, done: activeDone}
	s.singleRunMu.Unlock()
	defer func() {
		s.singleRunMu.Lock()
		delete(s.singleRuns, run.RunID)
		s.singleRunMu.Unlock()
		close(activeDone)
	}()
	if strings.TrimSpace(engine.Model) != binding.Manifest.Model {
		return ExecutionOutcome{}, fmt.Errorf("run %s engine model %q does not match sealed model %q: %w", run.RunID, engine.Model, binding.Manifest.Model, agentruntime.ErrConflict)
	}

	priorState := binding.State
	terminalReplay := priorState == agentruntime.ExecutionBindingCompleted || priorState == agentruntime.ExecutionBindingFailed
	var claimDecision agentruntime.ResourceClaimDecision
	claimsReleased := true
	if !terminalReplay {
		claimDecision, err = s.acquireRunResourceClaims(executionCtx, run, binding.Manifest.ResourceClaims)
		if err != nil {
			return ExecutionOutcome{}, err
		}
		if !claimDecision.Acquired {
			return ExecutionOutcome{}, &TaskExecutionUnavailableError{TaskID: run.TaskID, ResourceClaims: claimDecision}
		}
		claimsReleased = false
		defer func() {
			if !claimsReleased {
				_ = s.releaseRunResourceClaims(context.WithoutCancel(ctx), claimDecision.Claims)
			}
		}()
		binding.State = agentruntime.ExecutionBindingRunning
		binding, err = s.store.SaveExecutionBinding(executionCtx, binding, binding.Version)
		if err != nil {
			releaseErr := s.releaseRunResourceClaims(context.WithoutCancel(ctx), claimDecision.Claims)
			claimsReleased = true
			return ExecutionOutcome{}, errors.Join(err, releaseErr)
		}
	}
	run.binding = binding
	run.TaskVersion = 1
	run.LeaseID = run.ExecutionID
	run.HolderID = binding.AgentID
	request := hyagent.Request{Prompt: binding.Manifest.Prompt}
	if binding.Manifest.Budget != (hyagent.Budget{}) {
		budget := binding.Manifest.Budget
		request.Budget = &budget
	}
	outputPolicy := hyagent.OutputPolicy{
		Schema:   append(json.RawMessage(nil), binding.Manifest.OutputSchema...),
		Validate: len(binding.Manifest.OutputSchema) > 0,
		Repair:   len(binding.Manifest.OutputSchema) > 0,
	}
	var result hyagent.Result
	var runtimeErr error
	if priorState == agentruntime.ExecutionBindingPending {
		result, runtimeErr = s.durable.StartStream(executionCtx, durable.ExecutionID(run.ExecutionID), engine, request, outputPolicy, sink)
	} else {
		result, runtimeErr = s.durable.ResumeStreamWithOptions(
			executionCtx, durable.ExecutionID(run.ExecutionID), engine, sink,
			durable.ResumeOptions{Target: run.resumeTarget},
		)
	}
	var releaseErr error
	if !terminalReplay {
		releaseErr = s.releaseRunResourceClaims(context.WithoutCancel(ctx), claimDecision.Claims)
		claimsReleased = true
	}
	outcome.Result = result
	outcome.Failure = result.Failure
	if errors.Is(runtimeErr, durable.ErrBusy) {
		return outcome, errors.Join(&TaskExecutionUnavailableError{TaskID: run.TaskID}, releaseErr)
	}

	cancelled := false
	var cancellationStateErr error
	if runtimeErr != nil {
		latest, loadErr := s.store.LoadExecutionBinding(context.WithoutCancel(ctx), run.ExecutionID)
		if loadErr != nil {
			cancellationStateErr = loadErr
		} else {
			cancelled = latest.State == agentruntime.ExecutionBindingCancelled
		}
	}
	nextState := agentruntime.ExecutionBindingCompleted
	outcome.State = ExecutionCompleted
	switch {
	case cancelled:
		nextState = agentruntime.ExecutionBindingCancelled
		outcome.State = ExecutionCancelled
	case errors.Is(runtimeErr, durable.ErrSuspended):
		nextState = agentruntime.ExecutionBindingSuspended
		outcome.State = ExecutionSuspended
		outcome.Suspension = &Suspension{Kind: SuspensionRequested, Reason: runtimeErr.Error()}
	case errors.Is(runtimeErr, durable.ErrReconcileRequired):
		nextState = agentruntime.ExecutionBindingReconcileRequired
		outcome.State = ExecutionSuspended
		outcome.Suspension = &Suspension{Kind: SuspensionReconciliation, Reason: runtimeErr.Error()}
	case runtimeErr != nil:
		nextState = agentruntime.ExecutionBindingSuspended
		outcome.State = ExecutionSuspended
		outcome.Suspension = &Suspension{Kind: SuspensionRequested, Reason: runtimeErr.Error()}
	case releaseErr != nil:
		nextState = agentruntime.ExecutionBindingReconcileRequired
		outcome.State = ExecutionSuspended
		outcome.Suspension = &Suspension{Kind: SuspensionReconciliation, Reason: releaseErr.Error()}
	case result.Failure != nil:
		nextState = agentruntime.ExecutionBindingFailed
		outcome.State = ExecutionFailed
	}
	operationErr := errors.Join(runtimeErr, releaseErr, cancellationStateErr)
	if cancelled {
		operationErr = errors.Join(releaseErr, cancellationStateErr)
	}
	if operationErr == nil && (nextState == agentruntime.ExecutionBindingCompleted || nextState == agentruntime.ExecutionBindingFailed) {
		var terminalFailure error
		if result.Failure != nil {
			terminalFailure = result.Failure
		}
		if completionErr := s.saveRunCompletion(context.WithoutCancel(ctx), run, result.Text, terminalFailure, &result); completionErr != nil {
			operationErr = completionErr
			nextState = agentruntime.ExecutionBindingReconcileRequired
			outcome.State = ExecutionSuspended
			outcome.Suspension = &Suspension{Kind: SuspensionReconciliation, Reason: completionErr.Error()}
		}
	}
	if nextState == agentruntime.ExecutionBindingReconcileRequired {
		reason := "durable execution requires reconciliation"
		if operationErr != nil {
			reason = operationErr.Error()
		}
		operationErr = errors.Join(operationErr, s.markExecutionReconciliation(context.WithoutCancel(ctx), run.RunID, reason))
	}
	if transitionErr := s.finishExecutionBinding(context.WithoutCancel(ctx), run.ExecutionID, nextState); transitionErr != nil {
		operationErr = errors.Join(operationErr, transitionErr)
		if outcome.State == ExecutionCompleted || outcome.State == ExecutionFailed {
			outcome.State = ExecutionSuspended
			outcome.Suspension = &Suspension{Kind: SuspensionReconciliation, Reason: transitionErr.Error()}
		}
	}
	return outcome, operationErr
}

// SuspendRun requests a checkpointed suspension and waits until ExecuteRun has
// persisted the suspended execution binding. It must be called from outside
// the hook goroutine that is currently blocking the durable execution.
func (s *Service) SuspendRun(ctx context.Context, run *Run) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if err := s.beginRuntimeWork(); err != nil {
		return err
	}
	defer s.endRuntimeWork()
	s.singleRunMu.Lock()
	active, ok := s.singleRuns[run.RunID]
	s.singleRunMu.Unlock()
	if !ok || active.executionID != run.ExecutionID {
		return durable.ErrNotActive
	}
	err := s.durable.Suspend(ctx, durable.ExecutionID(run.ExecutionID))
	if err != nil && !errors.Is(err, durable.ErrNotActive) {
		return err
	}
	select {
	case <-active.done:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	binding, loadErr := s.store.LoadExecutionBinding(ctx, run.ExecutionID)
	if loadErr != nil {
		return loadErr
	}
	if binding.State != agentruntime.ExecutionBindingSuspended {
		return fmt.Errorf("run %s suspension settled in state %q: %w", run.RunID, binding.State, agentruntime.ErrConflict)
	}
	return nil
}

func (s *Service) ReleaseRun(ctx context.Context, run *Run) error {
	if run == nil {
		return nil
	}
	err := s.durable.Suspend(ctx, durable.ExecutionID(run.ExecutionID))
	if err != nil && !errors.Is(err, durable.ErrNotActive) {
		return err
	}
	return s.finishExecutionBinding(ctx, run.ExecutionID, agentruntime.ExecutionBindingSuspended)
}

func (s *Service) RequireRunReconciliation(ctx context.Context, runID, reason string) error {
	runRecord, taskRecord, _, err := s.loadRunAggregate(ctx, runID)
	if err != nil {
		return err
	}
	if err := s.markLegacyReconciliation(ctx, runRecord, taskRecord, reason); err != nil {
		return err
	}
	agentID := strings.TrimSpace(runRecord.Metadata[singleRunMetadataAgentID])
	if agentID == "" {
		agentID = mainAgentID
	}
	binding, err := s.store.LoadLatestExecutionBinding(ctx, runID, agentID, executionKind(agentID, runRecord.Metadata))
	if errors.Is(err, agentruntime.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	binding.State = agentruntime.ExecutionBindingReconcileRequired
	_, err = s.store.SaveExecutionBinding(ctx, binding, binding.Version)
	return err
}

func runFromBinding(binding agentruntime.ExecutionBinding, task agentruntime.Task, envelope agentruntime.TaskEnvelope) *Run {
	goal := task.Goal
	if goal == "" {
		goal = binding.Manifest.Prompt
	}
	return &Run{
		RunID: binding.RunID, Goal: goal, TaskID: task.ID, EnvelopeID: envelope.ID,
		ExecutionID: binding.ExecutionID, TaskVersion: task.Version, HolderID: binding.AgentID, binding: binding,
		pending: make(map[string]PendingApproval), approvedOnce: make(map[string]string),
	}
}

func resumeTargetForExecution(execution durable.Execution) durable.ResumeTarget {
	if execution.Checkpoint == nil {
		return durable.ResumeTarget{}
	}
	continuation := execution.Checkpoint.Continuation
	target := durable.ResumeTarget{
		CheckpointSequence: execution.Checkpoint.Sequence,
		Phase:              continuation.Phase,
	}
	switch continuation.Phase {
	case hyagent.ContinuationReady:
		target.OperationID = fmt.Sprintf("turn:%d:model", continuation.NextOperationTurn)
	case hyagent.ContinuationModelComplete:
		var operationID string
		for index := len(continuation.Messages) - 1; index >= 0; index-- {
			calls := continuation.Messages[index].ToolCalls
			if len(calls) == 0 {
				continue
			}
			if len(calls) == 1 {
				operationID = calls[0].OperationID
			}
			break
		}
		target.OperationID = operationID
	}
	return target
}

func resumeTargetForOperation(execution durable.Execution, operationID string) (durable.ResumeTarget, error) {
	target := resumeTargetForExecution(execution)
	if operationID == "" {
		return target, nil
	}
	if execution.Checkpoint == nil {
		return durable.ResumeTarget{}, fmt.Errorf("durable execution has no checkpoint for operation %s: %w", operationID, agentruntime.ErrConflict)
	}
	continuation := execution.Checkpoint.Continuation
	if continuation.Phase != hyagent.ContinuationModelComplete {
		return durable.ResumeTarget{}, fmt.Errorf(
			"durable checkpoint %d is in phase %q, not %q for operation %s: %w",
			execution.Checkpoint.Sequence, continuation.Phase, hyagent.ContinuationModelComplete, operationID, agentruntime.ErrConflict,
		)
	}
	for index := len(continuation.Messages) - 1; index >= 0; index-- {
		calls := continuation.Messages[index].ToolCalls
		if len(calls) == 0 {
			continue
		}
		for _, call := range calls {
			if call.OperationID == operationID {
				target.OperationID = operationID
				return target, nil
			}
		}
		break
	}
	return durable.ResumeTarget{}, fmt.Errorf(
		"durable checkpoint %d does not contain operation %s: %w",
		execution.Checkpoint.Sequence, operationID, agentruntime.ErrConflict,
	)
}

func (s *Service) resolvedApprovalOperation(ctx context.Context, runID, executionID string, execution durable.Execution) (string, error) {
	if execution.Checkpoint == nil || execution.Checkpoint.Continuation.Phase != hyagent.ContinuationModelComplete {
		return "", fmt.Errorf("run %s has no model-complete approval checkpoint: %w", runID, agentruntime.ErrConflict)
	}
	var calls []message.ToolCall
	for index := len(execution.Checkpoint.Continuation.Messages) - 1; index >= 0; index-- {
		if current := execution.Checkpoint.Continuation.Messages[index].ToolCalls; len(current) != 0 {
			calls = current
			break
		}
	}
	if len(calls) == 0 {
		return "", fmt.Errorf("run %s approval checkpoint has no tool operation: %w", runID, agentruntime.ErrConflict)
	}
	callOperations := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		if operationID := strings.TrimSpace(call.OperationID); operationID != "" {
			callOperations[operationID] = struct{}{}
		}
	}
	s.approvalMu.Lock()
	recovered := maps.Clone(s.recoveredApprovals[runID])
	s.approvalMu.Unlock()
	if len(recovered) == 1 {
		for operationID := range recovered {
			if _, found := callOperations[operationID]; found {
				return operationID, nil
			}
		}
	}
	work, err := s.store.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer work.Rollback(context.Background())
	var selected string
	var decidedAt time.Time
	for operationID := range callOperations {
		approvalID, _, _, err := approvalRecordIDs(agentruntime.RequestApprovalCommand{Metadata: map[string]string{
			approvalMetadataExecutionID: executionID,
			approvalMetadataOperationID: operationID,
		}})
		if err != nil {
			return "", err
		}
		approval, err := work.Approvals().LoadApproval(ctx, approvalID)
		if errors.Is(err, agentruntime.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		if approval.RunID != runID || approval.ActionID != operationID ||
			approval.Metadata[approvalMetadataExecutionID] != executionID ||
			approval.Metadata[approvalMetadataOperationID] != operationID {
			return "", fmt.Errorf("approval %s does not own operation %s: %w", approval.ApprovalID, operationID, agentruntime.ErrConflict)
		}
		if approval.Status != "approved" && approval.Status != "rejected" {
			continue
		}
		currentDecidedAt, err := time.Parse(time.RFC3339Nano, approval.Metadata["decided_at"])
		if err != nil {
			return "", fmt.Errorf("approval %s has invalid decision time: %w", approval.ApprovalID, agentruntime.ErrConflict)
		}
		if selected == "" || currentDecidedAt.After(decidedAt) {
			selected, decidedAt = operationID, currentDecidedAt
			continue
		}
		if currentDecidedAt.Equal(decidedAt) {
			return "", fmt.Errorf("run %s has ambiguous approval decisions: %w", runID, agentruntime.ErrConflict)
		}
	}
	if selected == "" {
		return "", fmt.Errorf("run %s has no resolved approval operation: %w", runID, agentruntime.ErrNotFound)
	}
	return selected, nil
}

const maxToolArgumentDepth = 128

func validateToolArguments(arguments json.RawMessage) error {
	if len(bytes.TrimSpace(arguments)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if err := validateToolArgumentValue(decoder, 0, true); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateToolArgumentValue(decoder *json.Decoder, depth int, requireObject bool) error {
	if depth > maxToolArgumentDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", maxToolArgumentDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, compound := token.(json.Delim)
	if requireObject && (!compound || delim != '{') {
		return fmt.Errorf("arguments must be a JSON object")
	}
	if !compound {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			keys[key] = struct{}{}
			if err := validateToolArgumentValue(decoder, depth+1, false); err != nil {
				return err
			}
		}

		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("object ended with %q", end)
		}
	case '[':
		for decoder.More() {
			if err := validateToolArgumentValue(decoder, depth+1, false); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("array ended with %q", end)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func operationApprovalCommand(run *Run, operationID, scopeFingerprint string) agentruntime.RequestApprovalCommand {
	return agentruntime.RequestApprovalCommand{
		RunID: run.RunID, TaskID: run.TaskID, ActionID: operationID,
		Metadata: map[string]string{
			approvalMetadataExecutionID: run.ExecutionID,
			approvalMetadataOperationID: operationID,
			approvalMetadataScope:       scopeFingerprint,
		},
	}
}

func (s *Service) loadOperationApproval(ctx context.Context, run *Run, operationID, scopeFingerprint string) (agentruntime.ApprovalRequest, agentruntime.ResumeToken, bool, error) {
	if strings.TrimSpace(run.ExecutionID) == "" {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, false, nil
	}
	command := operationApprovalCommand(run, operationID, scopeFingerprint)
	approvalID, tokenID, deterministic, err := approvalRecordIDs(command)
	if err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, false, err
	}
	if !deterministic {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, false, fmt.Errorf("operation approval identity is not deterministic: %w", agentruntime.ErrConflict)
	}
	work, err := s.store.Begin(ctx)
	if err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, false, err
	}
	defer work.Rollback(context.Background())
	approval, err := work.Approvals().LoadApproval(ctx, approvalID)
	if errors.Is(err, agentruntime.ErrNotFound) {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, false, nil
	}
	if err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, false, err
	}
	token, err := work.ResumeTokens().LoadResumeToken(ctx, tokenID)
	if err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, false, err
	}
	if err := validateApprovalIdentity(command, approval, token); err != nil {
		return agentruntime.ApprovalRequest{}, agentruntime.ResumeToken{}, false, err
	}
	return approval, token, true, nil
}

func invalidToolArguments(call tool.Call, err error) ExecutionResult {
	return ExecutionResult{
		Result: tool.Result{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    fmt.Sprintf("%s rejected: invalid arguments: %v", call.Name, err),
			IsError:    true,
		},
		Executed: true,
	}
}

func deniedToolExecution(call tool.Call) ExecutionResult {
	return ExecutionResult{Result: tool.Result{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    "Denied by user",
		IsError:    true,
	}}
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
	descriptor, err := DescribeTool(driver)
	if err != nil {
		return ExecutionResult{}, false, err
	}
	definition := descriptor.WireDefinition
	if call.Name != definition.Name {
		return ExecutionResult{}, false, fmt.Errorf("tool call %q does not match driver %q", call.Name, definition.Name)
	}
	if err := validateToolArguments(call.Arguments); err != nil {
		return invalidToolArguments(call, err), false, nil
	}
	policy := descriptor.PolicyForCall(call)
	if blocked, required := run.editRecovery.BlockedEdit(call); required {
		return ExecutionResult{Result: blocked, Executed: true}, false, nil
	}
	scope := scopeForCall(policy, call)
	approvalKey := approvalCallKey(call)
	needsApproval := policy.RequiresApproval || policy.RequiresActionTask ||
		policy.Effect == agentruntime.ToolEffectWrite || policy.Effect == agentruntime.ToolEffectExternalSideEffect
	if policy.Metadata["approval"] == "allow" {
		needsApproval = policy.Metadata["network"] == "prompt" && toolCallRequestsNetwork(call.Arguments)
	}
	approved := s.policy.sessionGranted(scope.Fingerprint) || run.approvedOnce[approvalKey] == scope.Fingerprint
	if needsApproval && !approved {
		if pending, found := run.pending[approvalKey]; found && pending.Scope.Fingerprint == scope.Fingerprint {
			return ExecutionResult{Approval: &pending}, false, nil
		}
		if recovered, found := s.consumeRecoveredApproval(run.RunID, approvalKey, scope.Fingerprint); found {
			if !recovered.approved {
				return deniedToolExecution(call), false, nil
			}
			approved = true
		}
	}
	if needsApproval && !approved {
		approval, token, found, loadErr := s.loadOperationApproval(ctx, run, approvalKey, scope.Fingerprint)
		if loadErr != nil {
			return ExecutionResult{}, false, loadErr
		}
		if found {
			switch approval.Status {
			case "pending":
				command := operationApprovalCommand(run, approvalKey, scope.Fingerprint)
				if err := validateExistingApproval(command, approval, token, time.Now().UTC()); err != nil {
					return ExecutionResult{}, false, err
				}
				pending := PendingApproval{Request: approval, Token: token, Call: call, Scope: scope, Effect: string(policy.Effect), Replayable: policy.Idempotent}
				run.pending[approvalKey] = pending
				return ExecutionResult{Approval: &pending}, false, nil
			case "approved":
				approved = true
			case "rejected":
				return deniedToolExecution(call), false, nil
			default:
				return ExecutionResult{}, false, fmt.Errorf("durable approval %s has invalid status %q: %w", approval.ApprovalID, approval.Status, agentruntime.ErrConflict)
			}
		}
	}
	if needsApproval && !approved {
		command := operationApprovalCommand(run, approvalKey, scope.Fingerprint)
		command.RequesterAgentID = run.HolderID
		command.Reason = fmt.Sprintf("%s requests %s", run.HolderID, call.Name)
		command.RiskSummary = scope.Risk + " · " + scope.Target
		command.RequestedAction = summarizeArguments(call.Arguments)
		approval, token, requestErr := s.requestApproval(ctx, command)
		if requestErr != nil {
			return ExecutionResult{}, false, requestErr
		}
		pending := PendingApproval{Request: approval, Token: token, Call: call, Scope: scope, Effect: string(policy.Effect), Replayable: policy.Idempotent}
		run.pending[approvalKey] = pending
		return ExecutionResult{Approval: &pending}, false, nil
	}
	return ExecutionResult{}, true, nil
}

// ExecutePreparedDriver runs an operation that already passed PrepareDriver.
func (s *Service) ExecutePreparedDriver(ctx context.Context, run *Run, driver tool.Driver, call tool.Call, sink tool.UpdateSink) (ExecutionResult, error) {
	delete(run.approvedOnce, approvalCallKey(call))
	result, err := s.ExecutePolicyCall(ctx, driver, call, sink)
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
		return agentruntime.ErrNotFound
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
	if err := s.decideApproval(ctx, agentruntime.DecideApprovalCommand{
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

func (s *Service) ResolveRecoveredApproval(ctx context.Context, approval agentruntime.ApprovalRequest, tokenID, decision string) error {
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
		token, err := s.recoverResumeToken(ctx, tokenID)
		if err != nil {
			return err
		}
		if token.RunID != approval.RunID || token.ApprovalID != approval.ApprovalID {
			return fmt.Errorf("recovered approval %s does not own resume token %s", approval.ApprovalID, tokenID)
		}
	}
	if err := s.decideApproval(ctx, agentruntime.DecideApprovalCommand{
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

func (s *Service) ToolPolicySnapshot() map[string]agentruntime.ToolPolicy {
	if s == nil || s.tools == nil {
		return nil
	}
	policies := make(map[string]agentruntime.ToolPolicy)
	for _, driver := range s.ToolDrivers() {
		descriptor, err := DescribeTool(driver)
		if err != nil {
			continue
		}
		name := descriptor.WireDefinition.Name
		policies[name] = descriptor.PolicyForCall(tool.Call{Name: name, Arguments: json.RawMessage(`{}`)})
	}
	return policies
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

func (s *Service) AttachExternalTools(drivers []tool.Driver, closer func(context.Context) error) error {
	if s == nil || s.tools == nil {
		return errors.New("tool registry is unavailable")
	}
	seen := make(map[string]bool, len(drivers))
	for _, driver := range drivers {
		if driver == nil {
			return errors.New("external tool driver is nil")
		}
		name := strings.TrimSpace(driver.Definition().Name)
		if name == "" || seen[name] {
			return fmt.Errorf("duplicate or empty external tool name %q", name)
		}
		if _, exists := s.tools.Driver(name); exists {
			return fmt.Errorf("external tool %q conflicts with an existing tool", name)
		}
		seen[name] = true
	}
	if err := tool.NewBus(drivers...).Validate(); err != nil {
		return err
	}
	for _, driver := range drivers {
		if err := s.tools.Register(driver); err != nil {
			return err
		}
	}
	s.externalMu.Lock()
	s.externalDrivers = append(s.externalDrivers, drivers...)
	s.externalMu.Unlock()
	if closer != nil {
		s.externalMu.Lock()
		s.externalClosers = append(s.externalClosers, closer)
		s.externalMu.Unlock()
	}
	return nil
}

func (s *Service) AttachManagedSkillTool(driver tool.Driver) error {
	if driver == nil {
		return nil
	}
	name := driver.Definition().Name
	if _, exists := s.tools.Driver(name); exists {
		return fmt.Errorf("managed skill tool %q conflicts with an existing tool", name)
	}
	if err := s.tools.Register(driver); err != nil {
		return err
	}
	s.externalMu.Lock()
	s.managedSkillDriver = driver
	s.externalMu.Unlock()
	return nil
}

func (s *Service) ManagedSkillDriver() tool.Driver {
	s.externalMu.Lock()
	defer s.externalMu.Unlock()
	return s.managedSkillDriver
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
	workspace := NewLocalWorkspace(absoluteRoot)
	isGitRepo := workspaceIsGitRepo(ctx, absoluteRoot)
	snapshotDriver := snapshotReadDriver{workspace: workspace}
	readDriver := newOMPReadDriver(absoluteRoot, snapshotDriver, s.resources, s.allowNetwork)
	drivers := make([]tool.Driver, 0, 24)
	drivers = append(drivers, listFilesDriver{workspace: workspace}, readDriver)

	var editDriver tool.Driver
	if s.allowWrite {
		editDriver = newOMPHashlineDriver(absoluteRoot, snapshotDriver, s.hashlineClipboard, s.fileBroker)
		drivers = append(drivers,
			editDriver,
			newOMPWriteDriver(absoluteRoot, snapshotDriver, s.resources, s.fileBroker),
			gofmtDriver{workspace: workspace},
		)
	}
	drivers = append(drivers, goTestStatusDriver{Driver: goTestDriver{root: absoluteRoot}})
	if isGitRepo {
		drivers = append(drivers, gitDiffDriver{root: absoluteRoot})
	}
	drivers = append(drivers, newReliableSearchDriver(absoluteRoot, workspace, snapshotDriver, s.resources))
	drivers = append(drivers, newASTGrepDriver(absoluteRoot, s.ast, snapshotDriver, s.resources))
	drivers = append(drivers, newGlobDriver(workspace))
	drivers = append(drivers, newLSPDriver(absoluteRoot, s.lsp, false))
	drivers = append(drivers, newDebugDriver(absoluteRoot, s.lsp, false))
	drivers = append(drivers, newEvalDriver(absoluteRoot, s.lsp))
	drivers = append(drivers, newBrowserDriver(absoluteRoot, s.lsp, s.allowNetwork))
	drivers = append(drivers, newComputerDriver(absoluteRoot, s.lsp))
	drivers = append(drivers, newWebSearchDriver(absoluteRoot, s.lsp, s.allowNetwork))
	drivers = append(drivers, newGitHubDriver(absoluteRoot, s.lsp, s.allowNetwork))
	hub := newHubDriver(absoluteRoot, s.lsp, s.jobs)
	hub.peers = s.hubPeers
	drivers = append(drivers, hub)
	drivers = append(drivers, newImageGenDriver(absoluteRoot, s.lsp, s.allowNetwork))
	drivers = append(drivers, newTTSDriver(absoluteRoot, s.lsp))
	drivers = append(drivers, newMemoryToolDrivers(s.memory)...)
	if s.allowWrite {
		if readDriver != nil && editDriver != nil {
			drivers = append(drivers, newReplaceDriver(readDriver, editDriver))
		}
		drivers = append(drivers, newDeleteFileDriver(absoluteRoot))
	}
	if s.shellPolicy != "deny" {
		drivers = append(drivers, newRuntimeShellDriver(absoluteRoot, s.shellPolicy, s.allowNetwork, s.shellRuntime, s.jobs))
	}
	if workspace.Root() == s.workspaceRoot {
		s.externalMu.Lock()
		drivers = append(drivers, s.externalDrivers...)
		s.externalMu.Unlock()
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
	if err := s.saveRunCompletion(ctx, run, summary, failure, nil); err != nil {
		return err
	}
	bindingState := agentruntime.ExecutionBindingCompleted
	if failure != nil {
		bindingState = agentruntime.ExecutionBindingFailed
	}
	return s.finishExecutionBinding(ctx, run.ExecutionID, bindingState)
}

func (s *Service) CancelRun(ctx context.Context, run *Run, cause error) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if cause == nil {
		cause = context.Canceled
	}
	bindingErr := s.finishExecutionBinding(ctx, run.ExecutionID, agentruntime.ExecutionBindingCancelled)
	runErr := s.markRunCancelled(ctx, run.RunID, run.TaskID, cause)
	s.singleRunMu.Lock()
	active, ok := s.singleRuns[run.RunID]
	s.singleRunMu.Unlock()
	if ok && active.cancel != nil {
		active.cancel(cause)
	}
	return errors.Join(bindingErr, runErr)
}

func (s *Service) CancelTrackedRun(ctx context.Context, runID string) (bool, error) {
	s.singleRunMu.Lock()
	active, ok := s.singleRuns[runID]
	s.singleRunMu.Unlock()
	if !ok {
		return false, nil
	}
	binding, loadErr := s.store.LoadExecutionBinding(ctx, active.executionID)
	var bindingErr, runErr error
	if loadErr == nil {
		bindingErr = s.finishExecutionBinding(ctx, active.executionID, agentruntime.ExecutionBindingCancelled)
		runErr = s.markRunCancelled(ctx, runID, binding.Manifest.Metadata["task_id"], context.Canceled)
	}
	if active.cancel != nil {
		active.cancel(context.Canceled)
	}
	return true, errors.Join(loadErr, bindingErr, runErr)
}

func (s *Service) Recover(ctx context.Context, runID string) (agentruntime.Projection, error) {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return agentruntime.Projection{}, err
	}
	defer work.Rollback(context.Background())
	run, err := work.Runs().LoadRun(ctx, runID)
	if err != nil {
		return agentruntime.Projection{}, err
	}
	tasks, err := work.Tasks().ListTasks(ctx, runID)
	if err != nil {
		return agentruntime.Projection{}, err
	}
	messages, err := work.UserMessages().ListMessages(ctx, runID)
	if err != nil {
		return agentruntime.Projection{}, err
	}
	if err := work.Commit(ctx); err != nil {
		return agentruntime.Projection{}, err
	}
	byID := make(map[string]agentruntime.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	return agentruntime.Projection{Run: run, Tasks: byID, Messages: messages}, nil
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
	if ctx == nil {
		return fmt.Errorf("close agent service: nil context")
	}
	if s == nil {
		return nil
	}
	s.lifecycleMu.Lock()
	if s.closed {
		done := s.closeDone
		s.lifecycleMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			s.lifecycleMu.Lock()
			err := s.closeErr
			s.lifecycleMu.Unlock()
			return err
		}
	}
	s.closed = true
	s.lifecycleMu.Unlock()

	err := s.shutdown(ctx)
	s.lifecycleMu.Lock()
	s.closeErr = err
	close(s.closeDone)
	s.lifecycleMu.Unlock()
	return err
}

func (s *Service) shutdown(ctx context.Context) error {
	s.cancel()
	s.singleRunMu.Lock()
	activeRuns := make([]activeRun, 0, len(s.singleRuns))
	for _, active := range s.singleRuns {
		activeRuns = append(activeRuns, active)
	}
	s.singleRunMu.Unlock()
	for _, active := range activeRuns {
		if active.cancel != nil {
			active.cancel(durable.ErrClosed)
		}
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	durableErr := s.durable.Close(ctx)
	var jobsErr error
	if s.jobs != nil {
		jobsErr = s.jobs.shutdown(ctx)
	}
	var lspErr error
	if s.lsp != nil {
		lspErr = s.lsp.Close(ctx)
	}
	if s.shellRuntime != nil {
		s.shellRuntime.shutdown()
	}
	s.externalMu.Lock()
	externalClosers := append([]func(context.Context) error(nil), s.externalClosers...)
	s.externalClosers = nil
	s.externalMu.Unlock()
	var externalErr error
	for _, closeExternal := range externalClosers {
		externalErr = errors.Join(externalErr, closeExternal(ctx))
	}
	shellDone := make(chan struct{})
	go func() {
		if s.shellRuntime != nil {
			s.shellRuntime.wg.Wait()
		}
		close(shellDone)
	}()
	select {
	case <-ctx.Done():
		return errors.Join(ctx.Err(), durableErr, jobsErr, lspErr, externalErr)
	case <-shellDone:
	}
	var storeErr error
	if closer, ok := s.store.(agentruntime.ProviderCloser); ok {
		storeErr = closer.Close(ctx)
	}
	return errors.Join(durableErr, jobsErr, lspErr, externalErr, storeErr)
}

// ActiveShellExecutions returns a race-safe point-in-time status view.
func (s *Service) ActiveShellExecutions() []ShellExecutionSnapshot {
	if s == nil || s.shellRuntime == nil {
		return nil
	}
	return s.shellRuntime.snapshot()
}

// UpdateShellMaxConcurrency changes the foreground shell admission limit for
// new calls without interrupting commands that are already running.
func (s *Service) UpdateShellMaxConcurrency(maxConcurrency int) {
	if s == nil || s.shellRuntime == nil || maxConcurrency < 1 {
		return
	}
	s.shellRuntime.updateMaxConcurrency(maxConcurrency)
}

// UpdateShellMaxWallClock changes the per-command ceiling for new shell calls
// without interrupting commands that are already running.
func (s *Service) UpdateShellMaxWallClock(wall time.Duration) {
	if s == nil || s.shellRuntime == nil || wall < time.Second {
		return
	}
	s.shellRuntime.updateMaxWallClock(wall)
}

func scopeForCall(policy agentruntime.ToolPolicy, call tool.Call) invocationScope {
	target := normalizedTarget(call.Arguments)
	if target == "" {
		target = "workspace"
	}
	risk := policy.RiskLevel
	if risk == "" {
		risk = "medium"
	}
	if call.Name == ToolShell {
		risk = ClassifyShellRisk(shellCallCommand(call.Arguments), toolCallRequestsNetwork(call.Arguments))
	}
	digest := sha256.Sum256([]byte(call.Name + "\x00" + target + "\x00" + risk))
	return invocationScope{Fingerprint: hex.EncodeToString(digest[:]), Target: target, Risk: risk}
}

func normalizedTarget(arguments json.RawMessage) string {
	var object map[string]any
	if json.Unmarshal(arguments, &object) != nil {
		return ""
	}
	if file, ok := object["file"].(string); ok && file != "" {
		target := filepath.Clean(file)
		if action, ok := object["action"].(string); ok && action != "" {
			target = action + ":" + target
		}
		if destination, ok := object["new_name"].(string); ok && destination != "" {
			target += "->" + filepath.Clean(destination)
		}
		return target
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
