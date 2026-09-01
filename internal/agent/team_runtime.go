package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/durable"
	"github.com/Viking602/venat/orchestration"
	"github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

const teamDispatchMetadataKey = "azem.team_dispatch"

func (s *Service) StartTeam(ctx context.Context, prompt string, models TeamModels, providers provider.Resolver) (TeamExecution, error) {
	runID, err := newID("team")
	if err != nil {
		return TeamExecution{}, err
	}
	return s.StartTeamWithID(ctx, runID, prompt, models, providers, nil)
}

func (s *Service) StartTeamWithID(ctx context.Context, runID, prompt string, models TeamModels, providers provider.Resolver, afterTick func(agentruntime.TeamState)) (TeamExecution, error) {
	var tools *tool.Bus
	if s != nil {
		tools = s.tools
	}
	return s.StartTeamWithIDAndTools(ctx, runID, prompt, models, providers, tools, afterTick)
}

func (s *Service) StartTeamWithIDAndTools(ctx context.Context, runID, prompt string, models TeamModels, providers provider.Resolver, tools *tool.Bus, afterTick func(agentruntime.TeamState)) (TeamExecution, error) {
	return s.StartTeamWithIDAndToolsMetadata(ctx, runID, prompt, models, providers, tools, nil, afterTick)
}

func (s *Service) StartTeamWithIDAndToolsMetadata(ctx context.Context, runID, prompt string, models TeamModels, providers provider.Resolver, tools *tool.Bus, metadata map[string]string, afterTick func(agentruntime.TeamState)) (TeamExecution, error) {
	return s.StartTeamWithIDAndToolsMetadataHooks(ctx, runID, prompt, models, providers, tools, metadata, TeamHooks{}, afterTick)
}

func (s *Service) StartTeamWithIDAndToolsMetadataHooks(ctx context.Context, runID, prompt string, models TeamModels, providers provider.Resolver, tools *tool.Bus, metadata map[string]string, hooks TeamHooks, afterTick func(agentruntime.TeamState)) (TeamExecution, error) {
	if err := s.beginRuntimeWork(); err != nil {
		return TeamExecution{}, err
	}
	defer s.endRuntimeWork()
	if err := validateTeamStart(s, runID, prompt, providers); err != nil {
		return TeamExecution{}, err
	}
	prompt = strings.TrimSpace(prompt)
	state := agentruntime.TeamState{RunID: runID}
	if err := s.createTeamRun(ctx, runID, prompt, metadata, state); err != nil {
		return TeamExecution{}, err
	}
	result, err := s.driveTeam(ctx, prompt, models, providers, tools, hooks, state, afterTick)
	return TeamExecution{RunID: runID, Result: result}, err
}

func (s *Service) ResumeTeam(ctx context.Context, runID string, models TeamModels, providers provider.Resolver) (TeamExecution, error) {
	var tools *tool.Bus
	if s != nil {
		tools = s.tools
	}
	return s.ResumeTeamWithTools(ctx, runID, models, providers, tools, nil)
}

func (s *Service) ResumeTeamWithTools(ctx context.Context, runID string, models TeamModels, providers provider.Resolver, tools *tool.Bus, afterTick func(agentruntime.TeamState)) (TeamExecution, error) {
	return s.ResumeTeamWithToolsHooks(ctx, runID, models, providers, tools, TeamHooks{}, afterTick)
}

func (s *Service) ResumeTeamWithToolsHooks(ctx context.Context, runID string, models TeamModels, providers provider.Resolver, tools *tool.Bus, hooks TeamHooks, afterTick func(agentruntime.TeamState)) (TeamExecution, error) {
	if err := s.beginRuntimeWork(); err != nil {
		return TeamExecution{}, err
	}
	defer s.endRuntimeWork()
	if s == nil || s.store == nil || s.durable == nil {
		return TeamExecution{}, fmt.Errorf("coding team: service is not initialized")
	}
	if providers == nil {
		return TeamExecution{}, fmt.Errorf("coding team: provider resolver is nil")
	}
	run, err := s.LoadRun(ctx, runID)
	if err != nil {
		return TeamExecution{}, err
	}
	state, err := s.loadTeamState(ctx, runID)
	if err != nil {
		return TeamExecution{}, err
	}
	result, err := s.driveTeam(ctx, run.Request, models, providers, tools, hooks, state, afterTick)
	return TeamExecution{RunID: runID, Result: result}, err
}

func validateTeamStart(s *Service, runID, prompt string, providers provider.Resolver) error {
	if s == nil || s.store == nil || s.durable == nil {
		return fmt.Errorf("coding team: service is not initialized")
	}
	if providers == nil {
		return fmt.Errorf("coding team: provider resolver is nil")
	}
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("coding team: prompt is empty")
	}
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("coding team: run ID is empty")
	}
	return nil
}

func (s *Service) createTeamRun(ctx context.Context, runID, prompt string, metadata map[string]string, state agentruntime.TeamState) error {
	rootID, err := newID("root")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	run := agentruntime.Run{
		ID: runID, RootTaskID: rootID, Request: prompt, Status: agentruntime.RunStatusRunning,
		Metadata: maps.Clone(metadata), CreatedAt: now, UpdatedAt: now,
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	if err := work.Runs().SaveRun(ctx, run); err != nil {
		return err
	}
	if err := work.TeamStates().SaveTeamState(ctx, agentruntime.TeamStateRecord{RunID: runID, State: encoded, UpdatedAt: now}); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func (s *Service) loadTeamState(ctx context.Context, runID string) (agentruntime.TeamState, error) {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return agentruntime.TeamState{}, err
	}
	defer work.Rollback(context.Background())
	record, err := work.TeamStates().LoadTeamState(ctx, runID)
	if err != nil {
		return agentruntime.TeamState{}, err
	}
	var state agentruntime.TeamState
	if err := json.Unmarshal(record.State, &state); err != nil {
		return agentruntime.TeamState{}, fmt.Errorf("decode team state: %w", err)
	}
	if state.RunID != runID {
		return agentruntime.TeamState{}, fmt.Errorf("team state run %q does not match %q", state.RunID, runID)
	}
	return state, nil
}

func (s *Service) driveTeam(ctx context.Context, prompt string, models TeamModels, providers provider.Resolver, tools *tool.Bus, hooks TeamHooks, initial agentruntime.TeamState, afterTick func(agentruntime.TeamState)) (agentruntime.TeamExecutionResult, error) {
	classes, err := CodingTeamClasses(models)
	if err != nil {
		return agentruntime.TeamExecutionResult{State: initial, Ticks: initial.Tick}, err
	}
	skillSnapshot := s.SkillSnapshot()
	if tools == nil {
		tools = tool.NewBus()
	}
	if err := tools.Validate(); err != nil {
		return agentruntime.TeamExecutionResult{State: initial, Ticks: initial.Tick}, err
	}
	available := make(map[string]bool)
	for _, definition := range tools.Definitions() {
		available[definition.Name] = true
	}
	byName := make(map[string]agentruntime.TeamAgentClass, len(classes))
	for index := range classes {
		filtered := classes[index].Tools[:0]
		for _, name := range classes[index].Tools {
			if available[name] {
				filtered = append(filtered, name)
			}
		}
		classes[index].Tools = filtered
		classes[index].Skills = append([]string(nil), skillSnapshot.Eager...)
		classes[index].AvailableSkills = append([]string(nil), skillSnapshot.Available...)
		byName[classes[index].Name] = classes[index]
	}
	scheduler := CodingScheduler{
		Prompt: prompt, Classes: byName, RetryPolicy: hooks.RetryPolicy,
		ResourceClaims: append([]agentruntime.ResourceClaimSpec(nil), hooks.ResourceClaims...),
	}
	state := initial
	maxTicks := s.teamMaxTicks
	if maxTicks <= 0 {
		maxTicks = 64
	}
	for completed := 0; completed < maxTicks; completed++ {
		adapter := teamSchedulerAdapter{base: state, scheduler: scheduler}
		executor := teamDispatchExecutor{
			service: s, classes: byName, providers: providers, tools: tools,
			beforeTask: hooks.BeforeTask, prepareEngine: hooks.PrepareEngine,
			decorateEngine: hooks.DecorateEngine,
			metadata:       s.teamRunMetadata(ctx, state.RunID),
		}
		next, driveErr := orchestration.Drive(ctx, adapter, executor, orchestration.DriveOptions{
			MaxTicks: 1, MaxConcurrency: s.teamMaxConcurrency, InitialState: &state.Orchestration,
		})
		state, err = foldTeamOrchestration(state, next)
		if err != nil {
			return agentruntime.TeamExecutionResult{State: state, Ticks: state.Tick}, err
		}
		if err := s.saveTeamState(ctx, state); err != nil {
			return agentruntime.TeamExecutionResult{State: state, Ticks: state.Tick}, err
		}
		if afterTick != nil {
			afterTick(state)
		}
		if driveErr == nil {
			_ = s.finishTeamRun(ctx, state.RunID, agentruntime.RunStatusCompleted)
			return agentruntime.TeamExecutionResult{State: state, Ticks: state.Tick}, nil
		}
		if !errors.Is(driveErr, orchestration.ErrMaxTicks) {
			_ = s.finishTeamRun(ctx, state.RunID, agentruntime.RunStatusFailed)
			return agentruntime.TeamExecutionResult{State: state, Ticks: state.Tick}, driveErr
		}
	}
	return agentruntime.TeamExecutionResult{State: state, Ticks: state.Tick}, orchestration.ErrMaxTicks
}

type teamSchedulerAdapter struct {
	base      agentruntime.TeamState
	scheduler CodingScheduler
}

func (adapter teamSchedulerAdapter) Next(ctx context.Context, state orchestration.State) ([]orchestration.Dispatch, error) {
	folded, err := foldTeamOrchestration(adapter.base, state)
	if err != nil {
		return nil, err
	}
	dispatches, err := adapter.scheduler.Next(ctx, folded)
	if err != nil {
		return nil, err
	}
	result := make([]orchestration.Dispatch, 0, len(dispatches))
	for _, dispatch := range dispatches {
		converted, err := encodeTeamDispatch(dispatch)
		if err != nil {
			return nil, err
		}
		result = append(result, converted)
	}
	return result, nil
}

func encodeTeamDispatch(dispatch agentruntime.TeamDispatch) (orchestration.Dispatch, error) {
	if err := agentruntime.ValidateTeamDispatch(dispatch); err != nil {
		return orchestration.Dispatch{}, err
	}
	encoded, err := json.Marshal(dispatch)
	if err != nil {
		return orchestration.Dispatch{}, err
	}
	request := hyagent.Request{Prompt: string(dispatch.Input)}
	if dispatch.Task.Budget != nil {
		budget := taskBudget(dispatch.Task.Budget)
		request.Budget = &budget
	}
	converted := orchestration.Dispatch{
		ID: dispatch.Task.ID, Route: dispatch.ClassName, Request: request,
		OutputPolicy: dispatch.OutputPolicy,
		Metadata:     map[string]string{teamDispatchMetadataKey: string(encoded)},
	}
	if dispatch.Handoff != nil {
		converted.Handoff = &orchestration.Handoff{
			From: dispatch.Handoff.From, To: dispatch.ClassName,
			Reason: dispatch.Handoff.Reason, Payload: append(json.RawMessage(nil), dispatch.Handoff.Payload...),
		}
	}
	return converted, nil
}

func decodeTeamDispatch(dispatch orchestration.Dispatch) (agentruntime.TeamDispatch, error) {
	var decoded agentruntime.TeamDispatch
	if err := json.Unmarshal([]byte(dispatch.Metadata[teamDispatchMetadataKey]), &decoded); err != nil {
		return agentruntime.TeamDispatch{}, fmt.Errorf("decode team dispatch %q: %w", dispatch.ID, err)
	}
	if decoded.Task.ID != dispatch.ID || decoded.ClassName != dispatch.Route {
		return agentruntime.TeamDispatch{}, fmt.Errorf("team dispatch %q identity mismatch", dispatch.ID)
	}
	return decoded, agentruntime.ValidateTeamDispatch(decoded)
}

type teamDispatchExecutor struct {
	service        *Service
	classes        map[string]agentruntime.TeamAgentClass
	providers      provider.Resolver
	tools          *tool.Bus
	beforeTask     func(context.Context, agentruntime.TeamDispatch, agentruntime.TeamAgentClass) error
	prepareEngine  func(context.Context, hyagent.Engine, agentruntime.TeamDispatch, agentruntime.TeamAgentClass) (hyagent.Engine, error)
	decorateEngine TeamEngineDecorator
	metadata       map[string]string
}

func (executor teamDispatchExecutor) Execute(ctx context.Context, dispatch orchestration.Dispatch, sink hyagent.Sink) (hyagent.Result, error) {
	teamDispatch, err := decodeTeamDispatch(dispatch)
	if err != nil {
		return hyagent.Result{}, err
	}
	class, ok := executor.classes[teamDispatch.ClassName]
	if !ok {
		return hyagent.Result{}, fmt.Errorf("coding team: class %q is not configured", teamDispatch.ClassName)
	}
	if err := executor.service.persistTeamHandoff(ctx, teamDispatch); err != nil {
		return hyagent.Result{}, err
	}
	if executor.beforeTask != nil {
		if err := executor.beforeTask(ctx, teamDispatch, class); err != nil {
			return hyagent.Result{}, err
		}
	}
	engine, err := hyagent.Build(class.Spec(), hyagent.BuildDeps{
		Providers: executor.providers, Tools: executor.tools, Skills: executor.service.SkillSnapshot().Registry,
	})
	if err != nil {
		return hyagent.Result{}, err
	}
	engine.ToolMode = tool.ModeParallel
	if executor.prepareEngine != nil {
		engine, err = executor.prepareEngine(ctx, engine, teamDispatch, class)
		if err != nil {
			return hyagent.Result{}, err
		}
	}
	if executor.decorateEngine != nil {
		engine = executor.decorateEngine(engine, teamDispatch, class)
	}
	executionID, binding, err := executor.service.ensureTeamExecutionBinding(ctx, teamDispatch, class, engine, dispatch.Request, executor.metadata)
	if err != nil {
		return hyagent.Result{}, err
	}
	if err := executor.service.finishExecutionBinding(ctx, executionID, agentruntime.ExecutionBindingRunning); err != nil {
		return hyagent.Result{}, err
	}
	invocation := Invocation{
		SessionID: binding.SessionID, RunID: binding.RunID, ExecutionID: executionID,
		AgentID: binding.AgentID, TaskID: teamDispatch.Task.ID, TeamRunID: binding.RunID,
		Workspace: executor.service.workspaceRoot,
	}
	result, runErr := executor.service.durable.StartStream(
		WithInvocation(ctx, invocation), durable.ExecutionID(executionID), engine,
		dispatch.Request, dispatch.OutputPolicy, sink,
	)
	state := agentruntime.ExecutionBindingCompleted
	if result.Failure != nil {
		state = agentruntime.ExecutionBindingFailed
	}
	if runErr != nil {
		state = agentruntime.ExecutionBindingSuspended
		var reconcile *durable.ReconcileRequiredError
		if errors.As(runErr, &reconcile) {
			state = agentruntime.ExecutionBindingReconcileRequired
		}
	}
	if bindingErr := executor.service.finishExecutionBinding(context.Background(), executionID, state); bindingErr != nil {
		runErr = errors.Join(runErr, bindingErr)
	}
	return result, runErr
}

func (s *Service) ensureTeamExecutionBinding(ctx context.Context, dispatch agentruntime.TeamDispatch, class agentruntime.TeamAgentClass, engine hyagent.Engine, request hyagent.Request, metadata map[string]string) (string, agentruntime.ExecutionBinding, error) {
	executionID, err := agentruntime.ExecutionID("team", dispatch.Task.ID, 0)
	if err != nil {
		return "", agentruntime.ExecutionBinding{}, err
	}
	profile, err := s.teamExecutableProfile(class, engine, metadata)
	if err != nil {
		return "", agentruntime.ExecutionBinding{}, err
	}
	if existing, loadErr := s.store.LoadExecutionBinding(ctx, executionID); loadErr == nil {
		candidate := existing
		applyExecutableProfile(&candidate.Manifest, profile)
		candidate.Manifest.Sealed = true
		candidate.Manifest.ProfileHash, err = executionProfileHash(candidate.Manifest)
		if err != nil {
			return "", agentruntime.ExecutionBinding{}, err
		}
		if !existing.Manifest.Sealed || candidate.Manifest.ProfileHash != existing.ProfileHash {
			return "", agentruntime.ExecutionBinding{}, fmt.Errorf("team execution profile changed: %w", agentruntime.ErrConflict)
		}
		return executionID, existing, nil
	} else if !errors.Is(loadErr, agentruntime.ErrNotFound) {
		return "", agentruntime.ExecutionBinding{}, loadErr
	}
	now := time.Now().UTC()
	manifest := agentruntime.ExecutionManifest{
		Version: agentruntime.ExecutionManifestVersion, SessionID: metadata["session_id"],
		RunID: dispatch.Task.RunID, AgentID: dispatch.To, StableID: dispatch.Task.ID, AgentVersion: "v1", Kind: "team", Segment: 0,
		Prompt: request.Prompt, OutputSchema: append(json.RawMessage(nil), class.OutputSchema...),
		RetryPolicy:    dispatch.Task.RetryPolicy,
		ResourceClaims: append([]agentruntime.ResourceClaimSpec(nil), dispatch.Task.ResourceClaims...),
		Metadata:       maps.Clone(metadata), WorkspaceAnchor: s.workspaceRoot, StartedAt: now,
	}
	if request.Budget != nil {
		manifest.Budget = *request.Budget
	}
	applyExecutableProfile(&manifest, profile)
	manifest.Sealed = true
	if err := validateExecutableProfile(manifest); err != nil {
		return "", agentruntime.ExecutionBinding{}, err
	}
	manifest.ProfileHash, err = executionProfileHash(manifest)
	if err != nil {
		return "", agentruntime.ExecutionBinding{}, err
	}
	binding := agentruntime.ExecutionBinding{
		ExecutionID: executionID, SessionID: manifest.SessionID, RunID: dispatch.Task.RunID, StableID: dispatch.Task.ID,
		AgentID: dispatch.To, Kind: "team", Segment: 0, Manifest: manifest,
		ProfileHash: manifest.ProfileHash, State: agentruntime.ExecutionBindingPending,
	}
	binding, err = s.store.SaveExecutionBinding(ctx, binding, 0)
	return executionID, binding, err
}

func (s *Service) teamExecutableProfile(class agentruntime.TeamAgentClass, engine hyagent.Engine, metadata map[string]string) (agentruntime.ExecutableProfile, error) {
	spec := class.Spec()
	providerID := strings.TrimSpace(metadata["provider"])
	if providerID == "" {
		providerID = engine.Provider.Metadata().Name
	}
	accountID := strings.TrimSpace(metadata["account_id"])
	if accountID == "" {
		accountID = "direct:" + providerID
	}
	rawModel := strings.TrimSpace(metadata["model"])
	if rawModel == "" {
		rawModel = engine.Model
	}
	reasoning := strings.TrimSpace(metadata["reasoning"])
	if reasoning == "" {
		reasoning = "provider-default"
	}
	var definitions []tool.Definition
	if engine.Tools != nil {
		definitions = engine.Tools.Definitions()
	}
	activeSkills := make([]string, len(engine.Skills))
	for index := range engine.Skills {
		activeSkills[index] = engine.Skills[index].Name
	}
	toolSchemaFingerprint, err := executionValueHash(definitions)
	if err != nil {
		return agentruntime.ExecutableProfile{}, err
	}
	toolSetHash, err := executionValueHash(spec.Tools)
	if err != nil {
		return agentruntime.ExecutableProfile{}, err
	}
	toolProfileHash, err := executionValueHash(struct {
		Definitions  []tool.Definition
		Tools        []string
		OutputSchema json.RawMessage
	}{definitions, spec.Tools, class.OutputSchema})
	if err != nil {
		return agentruntime.ExecutableProfile{}, err
	}
	promptFingerprint, err := executionValueHash(spec.Instructions)
	if err != nil {
		return agentruntime.ExecutableProfile{}, err
	}
	staticIdentity, err := executionValueHash(struct {
		Spec        hyagent.Spec
		Provider    provider.Metadata
		Definitions []tool.Definition
		Skills      any
		Class       string
		AccountID   string
		Reasoning   string
		Workspace   string
	}{spec, engine.Provider.Metadata(), definitions, engine.Skills, class.Name, accountID, reasoning, s.workspaceRoot})
	if err != nil {
		return agentruntime.ExecutableProfile{}, err
	}
	return agentruntime.ExecutableProfile{
		Provider: providerID, AccountID: accountID, RawModel: rawModel,
		Model: engine.Model, Reasoning: reasoning, ActiveSkills: activeSkills,
		ToolSetHash: toolSetHash, ToolProfileHash: toolProfileHash, StaticIdentity: staticIdentity,
		WorkspaceAnchor: s.workspaceRoot, PromptFingerprint: promptFingerprint, ToolSchemaFingerprint: toolSchemaFingerprint,
	}, nil
}

func (s *Service) persistTeamHandoff(ctx context.Context, dispatch agentruntime.TeamDispatch) error {
	if dispatch.Handoff == nil {
		return nil
	}
	handoffID := strings.TrimSpace(dispatch.Handoff.ID)
	if handoffID == "" {
		handoffID = dispatch.Task.ID + ":handoff"
	}
	record := agentruntime.HandoffRecord{
		ID: handoffID, RunID: dispatch.Task.RunID, From: dispatch.Handoff.From, To: dispatch.Handoff.To,
		Reason: dispatch.Handoff.Reason, Payload: append(json.RawMessage(nil), dispatch.Handoff.Payload...),
		EvidenceIDs:          append([]string(nil), dispatch.Handoff.EvidenceIDs...),
		RequiredOutputSchema: append(json.RawMessage(nil), dispatch.Handoff.RequiredOutputSchema...),
		CreatedAt:            time.Now().UTC(),
	}
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	existing, err := work.Handoffs().LoadHandoff(ctx, record.RunID, record.ID)
	if err == nil {
		if existing.RunID != record.RunID || existing.From != record.From || existing.To != record.To ||
			existing.Reason != record.Reason || string(existing.Payload) != string(record.Payload) ||
			string(existing.RequiredOutputSchema) != string(record.RequiredOutputSchema) {
			return fmt.Errorf("team handoff %q changed: %w", record.ID, agentruntime.ErrConflict)
		}
		return nil
	}
	if !errors.Is(err, agentruntime.ErrNotFound) {
		return err
	}
	if err := work.Handoffs().SaveHandoff(ctx, record); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func foldTeamOrchestration(base agentruntime.TeamState, mechanical orchestration.State) (agentruntime.TeamState, error) {
	state := base
	state.Tick = mechanical.Tick
	state.Orchestration = mechanical
	knownTasks := make(map[string]struct{}, len(state.Tasks))
	for _, task := range state.Tasks {
		knownTasks[task.ID] = struct{}{}
	}
	knownInstances := make(map[string]struct{}, len(state.Instances))
	for _, instance := range state.Instances {
		knownInstances[instance.ID] = struct{}{}
	}
	for _, outcome := range mechanical.Outcomes {
		dispatch, err := decodeTeamDispatch(outcome.Dispatch)
		if err != nil {
			return state, err
		}
		if _, exists := knownTasks[dispatch.Task.ID]; !exists {
			task := dispatch.Task
			report := teamResultReport(outcome.Result)
			task.Result = &report
			task.Status = agentruntime.TaskStatusCompleted
			if outcome.Result.Failure != nil {
				task.Status = agentruntime.TaskStatusFailed
				task.Error = outcome.Result.Failure.Error()
			}
			state.Tasks = append(state.Tasks, task)
			knownTasks[task.ID] = struct{}{}
		}
		if _, exists := knownInstances[dispatch.To]; !exists {
			instanceState := agentruntime.TeamInstanceFinished
			if outcome.Result.Failure != nil {
				instanceState = agentruntime.TeamInstanceFailed
			}
			state.Instances = append(state.Instances, agentruntime.TeamInstance{
				ID: dispatch.To, ClassName: dispatch.ClassName, AgentClassName: dispatch.AgentClassName,
				RunID: dispatch.Task.RunID, TaskID: dispatch.Task.ID, State: instanceState,
			})
			knownInstances[dispatch.To] = struct{}{}
		}
	}
	return state, nil
}

func teamResultReport(result hyagent.Result) agentruntime.TypedReport {
	report := agentruntime.TypedReport{Status: agentruntime.ReportStatusSuccess, Summary: result.Text}
	if len(result.Structured) > 0 {
		_ = json.Unmarshal(result.Structured, &report.Structured)
	}
	if result.Failure != nil {
		report.Status = agentruntime.ReportStatusFailed
		report.Kind = string(result.Failure.Kind)
		report.Summary = result.Failure.Error()
	}
	return report
}

func (s *Service) saveTeamState(ctx context.Context, state agentruntime.TeamState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	if err := work.TeamStates().SaveTeamState(ctx, agentruntime.TeamStateRecord{
		RunID: state.RunID, Tick: state.Tick, State: encoded, UpdatedAt: now,
	}); err != nil {
		return err
	}
	for _, task := range state.Tasks {
		if err := work.Tasks().SaveTask(ctx, task); err != nil {
			return err
		}
	}
	for _, instance := range state.Instances {
		if err := work.AgentInstances().SaveAgentInstance(ctx, agentruntime.AgentInstanceRecord{
			ID: instance.ID, ClassName: instance.ClassName, RunID: instance.RunID,
			TaskID: instance.TaskID, State: string(instance.State), CreatedAt: instance.CreatedAt,
		}); err != nil {
			return err
		}
	}
	return work.Commit(ctx)
}

func (s *Service) finishTeamRun(ctx context.Context, runID string, status agentruntime.RunStatus) error {
	work, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer work.Rollback(context.Background())
	run, err := work.Runs().LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	run.Status = status
	run.UpdatedAt = time.Now().UTC()
	if err := work.Runs().SaveRun(ctx, run); err != nil {
		return err
	}
	return work.Commit(ctx)
}

func (s *Service) teamRunMetadata(ctx context.Context, runID string) map[string]string {
	run, err := s.LoadRun(ctx, runID)
	if err != nil {
		return nil
	}
	return maps.Clone(run.Metadata)
}
