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

	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/azem/internal/skills"
	"github.com/Viking602/venat"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/coding"
	"github.com/Viking602/venat/stream"
	"github.com/Viking602/venat/tool"
	hyworker "github.com/Viking602/venat/worker"
)

const mainAgentID = "azem-main"

const defaultRunLeaseTTL = 10 * time.Minute

const (
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

type Service struct {
	runner             *venat.Runner
	store              api.StoreProvider
	workspace          coding.Workspace
	tools              *tool.Bus
	workspaceRoot      string
	externalDrivers    []tool.Driver
	managedSkillDriver tool.Driver
	policy             *ApprovalPolicy
	allowWrite         bool
	shellPolicy        string
	allowNetwork       string
	shellRuntime       *shellRuntime
	teamMaxConcurrency int
	teamMaxTicks       int
	skills             *skills.Catalog
	memory             *memory.Service
	resources          *resource.Router
	hashlineClipboard  *hashlineClipboard
	ast                *astBridge
	runLeaseTTL        time.Duration
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	lsp                *lspBridgeRuntime
	jobs               *backgroundJobManager
	hubPeers           *hubPeerBrokerRef
	fileBroker         *fileMutationBrokerRef
	singleRunMu        sync.Mutex
	singleRuns         map[string]*hyworker.SingleRunner
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

type callDefinitionDriver interface {
	DefinitionForCall(tool.Call) tool.Definition
}

type definitionOverrideDriver struct {
	tool.Driver
	definition tool.Definition
}

func (d definitionOverrideDriver) Definition() tool.Definition { return d.definition }

type goTestStatusDriver struct {
	tool.Driver
}

func (driver goTestStatusDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	result, err := driver.Driver.Execute(ctx, call, sink)
	if err != nil || result.IsError || len(result.Structured) == 0 {
		return result, err
	}
	var status coding.GoTestToolResult
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
	if call.Name != coding.ToolEditHashline || !result.IsError || strings.Contains(result.Content, "Required OMP Hashline retry format:") {
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
		if !result.IsError || !hashlineFailureRequiresRead(result.Content) {
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
	AgentID        string
	AgentVersion   string
	Governance     api.GovernancePolicy
	Budget         *api.TaskBudget
	OutputSchema   json.RawMessage
	RetryPolicy    api.RetryPolicy
	ResourceClaims []api.ResourceClaimSpec
}

type Run struct {
	RunID        string
	Goal         string
	TaskID       string
	EnvelopeID   string
	LeaseID      string
	TaskVersion  int
	HolderID     string
	pending      map[string]PendingApproval
	approvedOnce map[string]string
	editRecovery EditRecovery
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
		runner: runner, store: store, workspace: workspace, workspaceRoot: filepath.Clean(workspaceRoot), policy: policy,
		allowWrite: settings.allowWrite, shellPolicy: settings.shellPolicy, allowNetwork: settings.network,
		teamMaxConcurrency: settings.teamMaxConcurrency, teamMaxTicks: settings.teamMaxTicks,
		skills: settings.skills, resources: settings.resources, memory: settings.memory, hashlineClipboard: newHashlineClipboard(), ast: newASTBridge(), lsp: newLSPBridgeRuntime(), jobs: newBackgroundJobManager(serviceCtx), hubPeers: &hubPeerBrokerRef{}, fileBroker: &fileMutationBrokerRef{}, runLeaseTTL: defaultRunLeaseTTL,
		ctx: serviceCtx, cancel: serviceCancel,
		singleRuns:         make(map[string]*hyworker.SingleRunner),
		recoveredApprovals: make(map[string]map[string]recoveredApprovalDecision),
	}
	if settings.resources != nil && settings.resources.Handler("xd") == nil {
		if err := settings.resources.Register("xd", newASTXDevHandler(service.ast, settings.resources)); err != nil {
			serviceCancel()
			return nil, fmt.Errorf("register xd resources: %w", err)
		}
	}
	if settings.resources != nil && settings.resources.Handler("ssh") == nil {
		if err := settings.resources.Register("ssh", newSSHResourceHandler(service.lsp, settings.network)); err != nil {
			serviceCancel()
			return nil, fmt.Errorf("register ssh resources: %w", err)
		}
	}
	if settings.resources != nil && settings.memory != nil && settings.resources.Handler("memory") == nil {
		if err := settings.resources.Register("memory", memoryResourceHandler{memory: settings.memory}); err != nil {
			serviceCancel()
			return nil, fmt.Errorf("register memory resources: %w", err)
		}
	}
	service.shellRuntime = newShellRuntime(serviceCtx, settings.shellOptions)
	drivers, err := service.WorkspaceDrivers(context.Background(), workspaceRoot)
	if err != nil {
		return nil, err
	}
	service.tools = tool.NewBus(drivers...)
	return service, nil
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

func (s *Service) Runner() *venat.Runner { return s.runner }
func (s *Service) MatchASTSnapshot(ctx context.Context, source, language string, patterns []string) (bool, error) {
	if s == nil || s.ast == nil {
		return false, errors.New("AST matcher is unavailable")
	}
	result, err := s.ast.match(ctx, source, language, append([]string(nil), patterns...))
	return result.TotalMatches > 0, err
}

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
	taskID, err := newID("task")
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
	if agentID != mainAgentID {
		s.runner.RegisterAgent(api.AgentProfile{ID: agentID, Role: agentID})
	}
	coordinator := s.newSingleRunner(agentID, agentVersion, executionPolicy.Governance)
	state, err := coordinator.Start(ctx, hyworker.StartSingleRunRequest{
		RunID: runID, RootTaskID: rootID, TaskID: taskID,
		Request: request, Metadata: durableMetadata, Goal: request, AllowsAction: true,
		Budget: executionPolicy.Budget, RetryPolicy: executionPolicy.RetryPolicy,
		OutputSchema:   append(json.RawMessage(nil), executionPolicy.OutputSchema...),
		ResourceClaims: append([]api.ResourceClaimSpec(nil), executionPolicy.ResourceClaims...),
	})
	if err != nil {
		return nil, err
	}
	s.trackSingleRunner(runID, coordinator)
	return trackedRun(state), nil
}

// ResumeRun delegates recovery, redispatch, and resumability checks to Venat's
// durable single-run coordinator. A lease is acquired only when ExecuteRun
// starts, after the host has rebuilt the immutable execution profile.
func (s *Service) ResumeRun(ctx context.Context, runID string) (*Run, error) {
	coordinator, err := s.singleRunner(ctx, runID)
	if err != nil {
		return nil, err
	}
	state, err := coordinator.Resume(ctx, runID)
	if err != nil {
		return nil, err
	}
	s.trackSingleRunner(runID, coordinator)
	return trackedRun(state), nil
}

// ExecuteRun binds a rebuilt transient engine to the durable single-run
// coordinator. The lease observer publishes lease-scoped authorization to the
// governed Azem tools before the agent can call them.
func (s *Service) ExecuteRun(ctx context.Context, run *Run, engine hyagent.Engine, sink stream.Sink) (hyworker.ExecutionOutcome, error) {
	if run == nil {
		return hyworker.ExecutionOutcome{}, fmt.Errorf("run is nil")
	}
	coordinator, err := s.singleRunner(ctx, run.RunID)
	if err != nil {
		return hyworker.ExecutionOutcome{}, err
	}
	result, executeErr := coordinator.Execute(ctx, hyworker.ExecuteSingleRunRequest{
		RunID: run.RunID, Sink: sink, TTL: s.runLeaseTTL, Engine: &engine,
		OnLeaseAcquired: func(lease api.TaskExecutionLease) error {
			run.EnvelopeID = lease.EnvelopeID
			run.LeaseID = lease.ID
			run.TaskVersion = lease.TaskVersion
			run.HolderID = lease.HolderID
			return nil
		},
	})
	if result.Execution.State != hyworker.ExecutionSuspended {
		s.untrackSingleRunner(run.RunID, coordinator)
	}
	return result.Execution, executeErr
}

// ReleaseRun durably suspends a rebuilt run that cannot safely execute. The
// run remains resumable after its immutable profile mismatch is resolved.
func (s *Service) ReleaseRun(ctx context.Context, run *Run) error {
	if run == nil {
		return nil
	}
	coordinator, err := s.singleRunner(ctx, run.RunID)
	if err != nil {
		return err
	}
	err = coordinator.Suspend(ctx, run.RunID)
	if err == nil {
		s.untrackSingleRunner(run.RunID, coordinator)
	}
	return err
}

func (s *Service) RequireRunReconciliation(ctx context.Context, runID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "reconciliation required"
	}
	if _, err := s.runner.Recover(ctx, runID); err != nil {
		return err
	}
	run, err := s.runner.Run(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == api.RunStatusReconcileRequired {
		s.untrackSingleRunner(runID, nil)
		return nil
	}
	if run.Status == api.RunStatusBlocked || run.Status == api.RunStatusWaitingUserInput {
		if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: runID, To: api.RunStatusRunning}); err != nil {
			return fmt.Errorf("prepare run %s for reconciliation: %w", runID, err)
		}
	}
	if err := s.runner.TransitionRun(ctx, api.TransitionRunCommand{RunID: runID, To: api.RunStatusReconcileRequired}); err != nil {
		return fmt.Errorf("mark run %s reconciliation required from %s: %w", runID, run.Status, err)
	}
	s.untrackSingleRunner(runID, nil)
	return nil
}

func (s *Service) newSingleRunner(agentID, agentVersion string, governance api.GovernancePolicy) *hyworker.SingleRunner {
	return &hyworker.SingleRunner{
		Runner: s.runner,
		Worker: hyworker.AgentWorker{
			Runner: s.runner, AgentID: agentID, TTL: s.runLeaseTTL,
		},
		Admission:    hyworker.StandardAdmissionController{Runner: s.runner},
		AgentVersion: agentVersion,
		Governance:   governance,
	}
}

func (s *Service) singleRunner(ctx context.Context, runID string) (*hyworker.SingleRunner, error) {
	s.singleRunMu.Lock()
	coordinator := s.singleRuns[runID]
	s.singleRunMu.Unlock()
	if coordinator != nil {
		return coordinator, nil
	}
	run, err := s.runner.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(run.Metadata[singleRunMetadataAgentID])
	if agentID == "" {
		agentID = mainAgentID
	}
	agentVersion := strings.TrimSpace(run.Metadata[singleRunMetadataAgentVersion])
	if agentVersion == "" {
		agentVersion = "legacy"
	}
	var governance api.GovernancePolicy
	if encoded := strings.TrimSpace(run.Metadata[singleRunMetadataGovernance]); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &governance); err != nil {
			return nil, fmt.Errorf("decode coding run governance: %w", err)
		}
	}
	coordinator = s.newSingleRunner(agentID, agentVersion, governance)
	s.singleRunMu.Lock()
	if existing := s.singleRuns[runID]; existing != nil {
		s.singleRunMu.Unlock()
		return existing, nil
	}
	s.singleRuns[runID] = coordinator
	s.singleRunMu.Unlock()
	return coordinator, nil
}

func (s *Service) trackSingleRunner(runID string, coordinator *hyworker.SingleRunner) {
	s.singleRunMu.Lock()
	s.singleRuns[runID] = coordinator
	s.singleRunMu.Unlock()
}

func (s *Service) untrackSingleRunner(runID string, expected *hyworker.SingleRunner) {
	s.singleRunMu.Lock()
	if expected == nil || s.singleRuns[runID] == expected {
		delete(s.singleRuns, runID)
	}
	s.singleRunMu.Unlock()
}

func trackedRun(state hyworker.SingleRun) *Run {
	goal := state.Task.Goal
	if goal == "" {
		goal = state.Run.Request
	}
	taskVersion := state.Envelope.TaskVersion
	if taskVersion == 0 {
		taskVersion = state.Task.Version
	}
	return &Run{
		RunID: state.Run.ID, Goal: goal, TaskID: state.Task.ID,
		EnvelopeID: state.Envelope.ID, TaskVersion: taskVersion,
		HolderID: state.Task.AssignedAgentID,
		pending:  make(map[string]PendingApproval), approvedOnce: make(map[string]string),
	}
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
	if err := validateToolArguments(call.Arguments); err != nil {
		return invalidToolArguments(call, err), false, nil
	}
	if dynamic, ok := driver.(callDefinitionDriver); ok {
		definition = dynamic.DefinitionForCall(call)
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
	workspace := coding.NewLocalWorkspace(absoluteRoot)
	candidates := coding.NewToolSet(workspace)
	isGitRepo := workspaceIsGitRepo(ctx, absoluteRoot)
	drivers := make([]tool.Driver, 0, len(candidates)+4)
	var readDriver, snapshotReadDriver, editDriver, searchDriver tool.Driver
	for _, driver := range candidates {
		definition := driver.Definition()
		if definition.Name == coding.ToolGitDiff && !isGitRepo {
			continue
		}
		if !s.allowWrite && definition.EffectType == tool.EffectWrite {
			continue
		}
		if definition.Name == coding.ToolReadFile {
			snapshotReadDriver = driver
			driver = newOMPReadDriver(absoluteRoot, driver, s.resources, s.allowNetwork)
		}
		if definition.Name == coding.ToolWriteFile {
			driver = newOMPWriteDriver(absoluteRoot, snapshotReadDriver, s.resources, s.fileBroker)
		}
		if definition.Name == coding.ToolEditHashline {
			definition.Description = hashlineEditToolDescription
			driver = definitionOverrideDriver{Driver: driver, definition: definition}
			driver = newOMPHashlineDriver(absoluteRoot, snapshotReadDriver, s.hashlineClipboard, s.fileBroker)
		}
		if definition.Name == coding.ToolGoTest {
			driver = goTestStatusDriver{Driver: driver}
		}
		switch definition.Name {
		case coding.ToolReadFile:
			readDriver = driver
		case coding.ToolSearch:
			searchDriver = driver
			continue
		case coding.ToolEditHashline:
			editDriver = driver
		}
		drivers = append(drivers, driver)
	}
	if searchDriver != nil {
		if snapshotReadDriver != nil {
			drivers = append(drivers, newReliableSearchDriver(absoluteRoot, workspace, snapshotReadDriver, s.resources))
		} else {
			drivers = append(drivers, searchDriver)
		}
	}
	drivers = append(drivers, newASTGrepDriver(absoluteRoot, s.ast, snapshotReadDriver, s.resources))
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
	if filepath.Clean(absoluteRoot) == s.workspaceRoot {
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
	coordinator, err := s.singleRunner(ctx, run.RunID)
	if err != nil {
		return err
	}
	report := api.TypedReport{Status: api.ReportStatusSuccess, Summary: summary}
	if failure != nil {
		report.Status = api.ReportStatusFailed
		report.Kind = "agent_error"
		if report.Summary == "" {
			report.Summary = failure.Error()
		}
	}
	if _, err := coordinator.Report(ctx, run.RunID, report); err != nil {
		return err
	}
	s.untrackSingleRunner(run.RunID, coordinator)
	return nil
}

// CancelRun records an explicit terminal cancellation through the same
// coordinator that owns execution. Application shutdown cancels the ExecuteRun
// context instead, which produces a resumable suspension.
func (s *Service) CancelRun(ctx context.Context, run *Run, cause error) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	_ = cause
	coordinator, err := s.singleRunner(ctx, run.RunID)
	if err != nil {
		return err
	}
	if err := coordinator.Cancel(ctx, run.RunID); err != nil {
		return err
	}
	s.untrackSingleRunner(run.RunID, coordinator)
	return nil
}

// CancelTrackedRun cancels a locally coordinated single run. false means the
// ID belongs to a different runtime path, such as TeamRunner.
func (s *Service) CancelTrackedRun(ctx context.Context, runID string) (bool, error) {
	s.singleRunMu.Lock()
	coordinator := s.singleRuns[runID]
	s.singleRunMu.Unlock()
	if coordinator == nil {
		return false, nil
	}
	err := coordinator.Cancel(ctx, runID)
	if err == nil {
		s.untrackSingleRunner(runID, coordinator)
	}
	return true, err
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
	var storeErr error
	if closer, ok := s.store.(api.ProviderCloser); ok {
		storeErr = closer.Close(ctx)
	}
	return errors.Join(jobsErr, lspErr, externalErr, storeErr)
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
	if definition.Name == ToolShell {
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
