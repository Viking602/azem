package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/multiagent"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/stream"
	"github.com/Viking602/go-hydaelyn/tool"
)

func (s *Service) providerStreamSink(sessionID, runID, providerID, modelID, reasoning, transport string) stream.Sink {
	return s.providerStreamSinkWithFacts(sessionID, runID, providerID, modelID, reasoning, transport, false)
}

func (s *Service) providerStreamSinkWithFacts(sessionID, runID, providerID, modelID, reasoning, transport string, factMetered bool) stream.Sink {
	return stream.SinkFunc(func(ctx context.Context, frame stream.Frame) error {
		data := map[string]string{}
		switch frame.Kind {
		case stream.FrameText:
			if !s.emit(ctx, Event{Kind: EventTextDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: frame.Text, Data: data}) {
				return eventDeliveryError(ctx)
			}
		case stream.FrameThinking:
			if !s.emit(ctx, Event{Kind: EventThinkingDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: frame.Thinking, Data: data}) {
				return eventDeliveryError(ctx)
			}
		case stream.FrameToolCall:
			if frame.ToolCall != nil {
				data["name"] = frame.ToolCall.Name
				data["arguments"] = string(frame.ToolCall.Arguments)
				if !s.emit(ctx, Event{Kind: EventToolStarted, SessionID: sessionID, RunID: runID, ToolCallID: frame.ToolCall.ID, State: "running", Data: data}) {
					return eventDeliveryError(ctx)
				}
			}
		case stream.FrameToolResult:
			if frame.ToolResult != nil {
				state := "completed"
				if frame.ToolResult.IsError {
					state = "failed"
				}
				data["name"] = frame.ToolResult.Name
				if len(frame.ToolResult.Structured) > 0 {
					data["structured"] = string(frame.ToolResult.Structured)
				}
				if !s.emit(ctx, Event{Kind: EventToolFinished, SessionID: sessionID, RunID: runID, ToolCallID: frame.ToolResult.ToolCallID, State: state, Text: frame.ToolResult.Content, Data: data}) {
					return eventDeliveryError(ctx)
				}
			}
		case stream.FrameDone:
			if factMetered {
				return nil
			}
			if frame.Usage.InputTokens != 0 || frame.Usage.OutputTokens != 0 || frame.Usage.TotalTokens != 0 {
				usage := map[string]string{
					"inputTokens": fmt.Sprint(frame.Usage.InputTokens), "cachedInputTokens": fmt.Sprint(frame.Usage.CachedInputTokens),
					"uncachedInputTokens": fmt.Sprint(max(0, frame.Usage.InputTokens-frame.Usage.CachedInputTokens)),
					"outputTokens":        fmt.Sprint(frame.Usage.OutputTokens), "totalTokens": fmt.Sprint(frame.Usage.TotalTokens),
					"cacheStatus": "reported", "requestKind": "main", "provider": providerID, "model": modelID,
					"reasoning": reasoning, "transport": transport,
				}
				if !s.emit(s.ctx, Event{Kind: EventContextUsage, SessionID: sessionID, RunID: runID, State: "reported", Data: usage}) {
					return eventDeliveryError(ctx)
				}
			} else if !factMetered {
				if !s.emit(s.ctx, Event{Kind: EventContextUsage, SessionID: sessionID, RunID: runID, State: "reported", Data: map[string]string{}}) {
					return eventDeliveryError(ctx)
				}
			}
		case stream.FrameError:
			if frame.Err != nil {
				return frame.Err
			}
		}
		return nil
	})
}

func normalizeTurnRequest(request TurnRequest, defaults config.DefaultsConfig) TurnRequest {
	request.Prompt = strings.TrimSpace(request.Prompt)
	request.Images = CloneAttachments(request.Images)
	if request.SessionID == "" {
		request.SessionID = "default"
	}
	if request.Provider == "" {
		request.Provider = defaults.Provider
	}
	if request.Model == "" {
		request.Model = defaults.Model
	}
	if request.Reasoning == "" {
		request.Reasoning = defaults.Reasoning
	}
	if request.AgentMode == "" {
		request.AgentMode = defaults.AgentMode
	}
	return request
}

func (s *Service) providerTransport(providerID string) string {
	if providerID != "grok" {
		return "chatgpt-codex-responses"
	}
	if s.cfg.Providers.Grok.Transport == "cli_proxy" {
		return "grok-cli-proxy-responses-experimental"
	}
	return "xai-responses"
}

func (s *Service) runProviderTurn(ctx context.Context, request TurnRequest, run *agentservice.Run, engine hyagent.Engine) {
	defer s.wg.Done()
	defer s.clearRun(run.RunID)
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: request.SessionID, RunID: run.RunID, State: "running"})
	task := api.Task{
		ID: run.TaskID, RunID: run.RunID, Type: api.TaskTypeWorker, Goal: request.Prompt,
		Budget: &api.TaskBudget{
			MaxTokens: s.cfg.Agents.Main.MaxTokens, MaxWallClock: s.cfg.Agents.Main.MaxWallClockDuration,
			MaxToolCalls: s.cfg.Agents.Main.MaxToolCalls,
		},
	}
	result := engine.RunStream(ctx, task, hyagent.OutputPolicy{}, s.providerStreamSinkWithFacts(request.SessionID, run.RunID, request.Provider, request.Model, request.Reasoning, s.providerTransport(request.Provider), s.sessions != nil))
	var runErr error
	if result.Failure != nil {
		runErr = result.Failure
		if errors.Is(runErr, hyagent.ErrBudgetExhausted) && strings.Contains(runErr.Error(), "max tokens") {
			runErr = fmt.Errorf("%w (increase agents.main.max_tokens in config.yaml for unusually large tasks)", runErr)
		}
		if errors.Is(runErr, hyagent.ErrBudgetExhausted) && strings.Contains(runErr.Error(), "max tool calls") {
			runErr = fmt.Errorf("%w (increase agents.main.max_tool_calls, or set it to 0 for unbounded, in config.yaml)", runErr)
		}
	}
	completionCtx, completionCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer completionCancel()
	if err := s.coding.CompleteRun(completionCtx, run, result.Text, runErr); err != nil {
		if runErr == nil {
			runErr = err
		} else {
			runErr = fmt.Errorf("%v; durable completion: %w", runErr, err)
		}
	}
	if ctx.Err() != nil {
		s.observeStop(request.SessionID, run.RunID, hooks.StopFailure, "cancelled", ctx.Err())
		s.emit(s.ctx, Event{Kind: EventRunCancelled, SessionID: request.SessionID, RunID: run.RunID, State: "cancelled"})
		return
	}
	if runErr != nil {
		s.observeStop(request.SessionID, run.RunID, hooks.StopFailure, "failed", runErr)
		s.emit(ctx, Event{Kind: EventRunFailed, SessionID: request.SessionID, RunID: run.RunID, State: "failed", Text: runErr.Error()})
		return
	}
	if s.sessions != nil && (strings.TrimSpace(result.Text) != "" || len(result.Messages) > 0) {
		history := session.ModelHistory{
			ProviderID: request.Provider, ModelID: engine.Model,
			InstructionFingerprint: mainInstructionFingerprint,
			StaticPrefixHash:       mainInstructionFingerprint,
			WireVersion:            session.CurrentWireVersion,
			Messages:               result.Messages,
		}
		if err := s.sessions.CompleteTurn(ctx, request.SessionID, session.Block{
			Kind: "assistant", RunID: run.RunID, Title: "Azem", Content: result.Text, State: "completed",
		}, history); err != nil {
			s.observeStop(request.SessionID, run.RunID, hooks.StopFailure, "persist_failed", err)
			s.emit(ctx, Event{Kind: EventRunFailed, SessionID: request.SessionID, RunID: run.RunID, State: "failed", Text: err.Error()})
			return
		}
	}
	if err := s.persistRecap(ctx, request.SessionID, run.RunID, request.Prompt, result.Text, request.Todo); err != nil {
		s.emit(ctx, Event{Kind: EventRecapState, SessionID: request.SessionID, RunID: run.RunID, State: "failed", Text: err.Error()})
	}
	s.emit(ctx, Event{Kind: EventRunFinished, SessionID: request.SessionID, RunID: run.RunID, State: "completed"})
}

func teamPrompt(request TurnRequest) string {
	return request.Prompt
}

type teamExecutionPolicy struct {
	contextTarget  int
	attachmentRoot string
	images         []session.Attachment
	newSummarizer  func(string) func(context.Context, string) (string, error)
}

func (s *Service) teamExecutionPolicy(request TurnRequest, parentRunID string, contextWindow int, tools *tool.Bus) (teamExecutionPolicy, error) {
	toolTokens := 0
	if tools != nil {
		bytes := 0
		for _, definition := range tools.Definitions() {
			if encoded, err := json.Marshal(definition); err == nil {
				bytes += len(encoded)
			}
		}
		toolTokens = (bytes + estimatedBytesPerToken - 1) / estimatedBytesPerToken
	}
	target, err := modelContextTokenTarget(request.Provider, request.Model, contextWindow, toolTokens)
	if err != nil {
		return teamExecutionPolicy{}, err
	}
	policy := teamExecutionPolicy{contextTarget: target, attachmentRoot: s.attachments.Root, images: CloneAttachments(request.Images)}
	compactionRoute, _ := s.providers.modelRouteSnapshot()
	report := s.providers.compactionUsageReporter(s, request.SessionID, parentRunID)
	policy.newSummarizer = func(cacheKey string) func(context.Context, string) (string, error) {
		return lazyCompactionSummarizer(func(ctx context.Context, provider, model, reasoning string) (string, int, hyprovider.Driver, error) {
			_, resolvedModel, window, driver, resolveErr := s.providers.resolveDriver(ctx, provider, model, reasoning)
			if resolveErr == nil && s.sessions != nil {
				driver = &meteredProviderDriver{inner: driver, store: s.sessions, host: s, sessionID: request.SessionID,
					runID: parentRunID, kind: "compaction", provider: provider, model: resolvedModel, transport: driver.Metadata().Name}
			}
			return resolvedModel, window, driver, resolveErr
		}, compactionRoute, request.Provider, request.Model, request.Reasoning, cacheKey, nil, func() compactionUsageReporter {
			if s.sessions != nil {
				return nil
			}
			return report
		}())
	}
	return policy, nil
}

func (s *Service) runProviderTeam(ctx context.Context, request TurnRequest, runID, goal string, resolution teamProviderResolution) {
	defer s.wg.Done()
	defer s.clearRun(runID)
	request.Provider = resolution.providerID
	request.Model = resolution.modelID
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: request.SessionID, RunID: runID, State: "running"})
	models := agentservice.TeamModels{Planner: resolution.modelID, Implementer: resolution.modelID, Reviewer: resolution.modelID, Reporter: resolution.modelID}
	toolBus, toolErr := s.teamToolBus(ctx, request.SessionID, runID, goal)
	if toolErr != nil {
		s.finishProviderTeam(ctx, request.SessionID, runID, request.Prompt, request.Todo, agentservice.TeamExecution{}, toolErr)
		return
	}
	policy, policyErr := s.teamExecutionPolicy(request, runID, resolution.contextWindow, toolBus)
	if policyErr != nil {
		s.finishProviderTeam(ctx, request.SessionID, runID, request.Prompt, request.Todo, agentservice.TeamExecution{}, policyErr)
		return
	}
	execution, err := s.coding.StartTeamWithIDAndToolsMetadataHooks(ctx, runID, goal, models, resolution.resolver, toolBus, map[string]string{
		"session_id":           request.SessionID,
		"provider":             request.Provider,
		"model":                resolution.modelID,
		"reasoning":            request.Reasoning,
		"original_prompt":      request.Prompt,
		"hook_private_context": request.privateContext,
		"attachments":          EncodeAttachmentsMeta(request.Images),
	}, s.teamHooks(request, runID, policy), func(state multiagent.TeamState) {
		for _, instance := range state.Instances {
			s.emit(ctx, Event{
				Kind: EventAgentState, SessionID: request.SessionID, RunID: runID, AgentID: instance.ID, State: string(instance.State),
				Agent: &AgentStatePayload{Type: instance.ClassName, Model: resolution.modelID, ParentRunID: runID, Activity: string(instance.State)},
				Data:  map[string]string{"id": instance.ID, "role": instance.ClassName, "state": string(instance.State)},
			})
		}
	})
	s.finishProviderTeam(ctx, request.SessionID, runID, request.Prompt, request.Todo, execution, err)
}

func (s *Service) runResumedProviderTeam(ctx context.Context, request TurnRequest, runID, recapGoal string, resolution teamProviderResolution) {
	defer s.wg.Done()
	defer s.clearRun(runID)
	request.Provider = resolution.providerID
	request.Model = resolution.modelID
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: request.SessionID, RunID: runID, State: "resuming", Data: map[string]string{"preserveUsage": "true"}})
	models := agentservice.TeamModels{Planner: resolution.modelID, Implementer: resolution.modelID, Reviewer: resolution.modelID, Reporter: resolution.modelID}
	toolBus, toolErr := s.teamToolBus(ctx, request.SessionID, runID, request.Prompt)
	if toolErr != nil {
		s.finishProviderTeam(ctx, request.SessionID, runID, recapGoal, request.Todo, agentservice.TeamExecution{}, toolErr)
		return
	}
	policy, policyErr := s.teamExecutionPolicy(request, runID, resolution.contextWindow, toolBus)
	if policyErr != nil {
		s.finishProviderTeam(ctx, request.SessionID, runID, recapGoal, request.Todo, agentservice.TeamExecution{}, policyErr)
		return
	}
	execution, err := s.coding.ResumeTeamWithToolsHooks(ctx, runID, models, resolution.resolver, toolBus, s.teamHooks(request, runID, policy), func(state multiagent.TeamState) {
		for _, instance := range state.Instances {
			s.emit(ctx, Event{
				Kind: EventAgentState, SessionID: request.SessionID, RunID: runID, AgentID: instance.ID, State: string(instance.State),
				Agent: &AgentStatePayload{Type: instance.ClassName, Model: resolution.modelID, ParentRunID: runID, Activity: string(instance.State)},
				Data:  map[string]string{"id": instance.ID, "role": instance.ClassName, "state": string(instance.State)},
			})
		}
	})
	s.finishProviderTeam(ctx, request.SessionID, runID, recapGoal, request.Todo, execution, err)
}

func (s *Service) teamHooks(request TurnRequest, parentRunID string, policy teamExecutionPolicy) agentservice.TeamHooks {
	sessionID := request.SessionID
	teamImages := CloneAttachments(policy.images)
	teamHistory := append([]session.Block(nil), request.History...)
	if s.sessions != nil {
		if projection, err := s.sessions.LoadProjection(s.ctx, sessionID); err == nil {
			for index := len(projection.Blocks) - 1; index >= 0; index-- {
				if projection.Blocks[index].RunID == parentRunID {
					if len(projection.Blocks[index].Attachments) > 0 {
						teamImages = CloneAttachments(projection.Blocks[index].Attachments)
					}
					break
				}
			}
		}
	}
	beforeTask := func(ctx context.Context, dispatch multiagent.Dispatch, class multiagent.AgentClass) error {
		metadata := hooks.Metadata{SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To, AgentType: class.Name, ParentRunID: parentRunID, CWD: s.cfg.Workspace.Root}
		return s.dispatchLifecycle(ctx, hooks.TaskCreated, metadata, func(e *hooks.Envelope) {
			e.TaskID, e.TaskSubject = dispatch.Task.ID, dispatch.Task.Goal
		})
	}
	prepare := func(ctx context.Context, engine hyagent.Engine, dispatch multiagent.Dispatch, class multiagent.AgentClass) (hyagent.Engine, error) {
		metadata := hooks.Metadata{SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To, AgentType: class.Name, ParentRunID: parentRunID, CWD: s.cfg.Workspace.Root}
		roleCacheKey := strings.Join([]string{sessionID, "team", request.Provider, request.Model, class.Name}, ":")
		extraBody := make(map[string]any, len(engine.ExtraBody)+3)
		for key, value := range engine.ExtraBody {
			extraBody[key] = value
		}
		extraBody["prompt_cache_key"] = roleCacheKey
		if strings.TrimSpace(policy.attachmentRoot) != "" {
			extraBody[responses.AttachmentRootExtraKey] = policy.attachmentRoot
		}
		engine.ExtraBody = extraBody
		decision := s.hooks.Dispatch(ctx, hooks.Envelope{
			SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To,
			AgentType: class.Name, ParentRunID: parentRunID, CWD: metadata.CWD, HookEventName: hooks.SubagentStart,
		})
		if decision.PreventContinuation {
			return engine, fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, decision.StopReason)
		}
		var additional []string
		for _, run := range decision.Runs {
			if text := strings.TrimSpace(run.Output.HookSpecificOutput.AdditionalContext); text != "" {
				additional = append(additional, text)
			}
		}
		contextParts := append([]string{strings.TrimSpace(request.privateContext)}, additional...)
		contextText := strings.TrimSpace(strings.Join(contextParts, "\n"))
		historical := ""
		var history []session.Block
		var images []session.Attachment
		if class.Name == agentservice.PlannerClass {
			historical = request.historicalContext
			history = append([]session.Block(nil), teamHistory...)
			images = CloneAttachments(teamImages)
		}
		var loadTodo func(context.Context) (session.TodoList, error)
		if s.sessions != nil {
			loadTodo = func(ctx context.Context) (session.TodoList, error) { return s.sessions.LoadTodo(ctx, sessionID) }
		}
		requestContext := teamHookContext{
			additional: contextText, historical: historical,
			history: history,
		}
		requestPreparer := &teamRequestPreparer{
			context: requestContext, images: images, todo: request.Todo, loadTodo: loadTodo, runID: dispatch.Task.RunID,
			target: policy.contextTarget,
		}
		if policy.newSummarizer != nil {
			requestPreparer.compactor = &turnContext{
				runID: dispatch.Task.RunID, todo: request.Todo, loadTodo: loadTodo,
				summarize:    policy.newSummarizer(roleCacheKey + ":compaction"),
				compactHooks: s.autoCompactHooks(metadata),
				reportContextTokens: func(_ context.Context, tokens int) {
					s.emit(s.ctx, Event{Kind: EventContextUsage, SessionID: sessionID, RunID: parentRunID, State: "estimated", Data: map[string]string{
						"inputTokens": fmt.Sprint(tokens), "outputTokens": "0", "totalTokens": fmt.Sprint(tokens),
						"cacheStatus": "pending", "aggregateOnly": "true", "requestKind": "team", "role": class.Name, "agentID": dispatch.To,
						"provider": request.Provider, "model": request.Model, "reasoning": request.Reasoning, "teamContextTarget": fmt.Sprint(policy.contextTarget),
					}})
				},
			}
		}
		if engine.Provider != nil {
			transport := engine.Provider.Metadata().Name
			inner := engine.Provider
			if s.sessions != nil {
				inner = &meteredProviderDriver{inner: inner, store: s.sessions, host: s, sessionID: sessionID,
					runID: parentRunID, kind: "team", provider: request.Provider, model: request.Model, transport: transport}
			}
			engine.Provider = &teamUsageDriver{inner: inner, prepare: requestPreparer.prepare}
		}
		engine.ExtraBody = extraBody
		return engine, nil
	}
	decorate := func(engine hyagent.Engine, dispatch multiagent.Dispatch, class multiagent.AgentClass) hyagent.Engine {
		metadata := hooks.Metadata{
			SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To,
			AgentType: class.Name, ParentRunID: parentRunID, CWD: s.cfg.Workspace.Root,
		}
		taskGuardrail := func(event hooks.Event, defaultReason string, add func(*hooks.Envelope)) hyagent.OutputGuardrail {
			return hyagent.NewOutputGuardrail("claude-"+strings.ToLower(string(event))+"-hook", func(ctx context.Context, input hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
				envelope := hooks.Envelope{
					SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID,
					AgentType: metadata.AgentType, ParentRunID: metadata.ParentRunID, CWD: metadata.CWD, HookEventName: event,
					LastAssistantMessage: strings.TrimSpace(input.Output.Text),
				}
				add(&envelope)
				decision := s.hooks.Dispatch(ctx, envelope)
				if decision.PreventContinuation {
					return hyagent.OutputGuardrailResult{}, fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, decision.StopReason)
				}
				if !decision.Denied {
					return hyagent.AllowOutput(), nil
				}
				return hyagent.RetryOutputWithPolicy(hyagent.RetryPolicy{IncludeRejectedOutput: true},
					message.NewText(message.RoleUser, firstNonempty(strings.TrimSpace(decision.Reason), defaultReason))), nil
			})
		}
		guardrails := []hyagent.OutputGuardrail{
			s.stopHookGuardrail(metadata, hooks.SubagentStop, func(input hyagent.OutputGuardrailInput) string {
				transcript, err := json.Marshal(append(append([]message.Message(nil), input.Messages...), input.Output))
				if err != nil {
					return ""
				}
				path, _ := writeSubagentHookTranscript(s.cfg.Workspace.Root, dispatch.To, transcript)
				return path
			}),
			taskGuardrail(hooks.TaskCompleted, "A TaskCompleted hook requested more work before completion.", func(e *hooks.Envelope) {
				e.TaskID, e.TaskSubject = dispatch.Task.ID, dispatch.Task.Goal
			}),
			taskGuardrail(hooks.TeammateIdle, "A TeammateIdle hook requested that this teammate continue working.", func(e *hooks.Envelope) {
				e.TeammateName, e.TeamName = firstNonempty(class.Name, dispatch.To), parentRunID
			}),
		}
		if class.Name == agentservice.ReporterClass {
			guardrails = append(guardrails, s.stopHookGuardrail(s.hookMetadata(sessionID, parentRunID), hooks.Stop, func(input hyagent.OutputGuardrailInput) string {
				return writeSessionHookTranscript(sessionID, append(append([]message.Message(nil), input.Messages...), input.Output))
			}))
		}
		engine.OutputGuardrails = append(engine.OutputGuardrails, guardrails...)
		return engine
	}
	return agentservice.TeamHooks{BeforeTask: beforeTask, PrepareEngine: prepare, DecorateEngine: decorate}
}

type teamHookContext struct {
	inner      hyagent.ContextManager
	additional string
	historical string
	history    []session.Block
}

func (c teamHookContext) Build(ctx context.Context, task api.Task) ([]message.Message, error) {
	messages, err := c.inner.Build(ctx, task)
	if err != nil {
		return nil, err
	}
	return c.enrich(ctx, messages)
}

func (c teamHookContext) enrich(ctx context.Context, messages []message.Message) ([]message.Message, error) {
	systemEnd := 0
	for systemEnd < len(messages) && messages[systemEnd].Role == message.RoleSystem {
		systemEnd++
	}
	prefix := make([]message.Message, 0, systemEnd+2)
	if c.additional != "" {
		value := message.NewText(message.RoleSystem, "[Trusted SubagentStart hook context]\n"+c.additional)
		value.Visibility = message.VisibilityPrivate
		value.CreatedAt = time.Time{}
		prefix = append(prefix, value)
	}
	if c.historical != "" || len(c.history) > 0 {
		policy := message.NewText(message.RoleSystem, historicalEvidencePolicy)
		policy.Visibility = message.VisibilityPrivate
		policy.CreatedAt = time.Time{}
		prefix = append(prefix, policy)
	}
	prefix = append(prefix, messages[:systemEnd]...)
	contextMessages := make([]message.Message, 0, 3)
	if len(c.history) > 0 {
		encoded, encodeErr := json.Marshal(c.history)
		if encodeErr != nil {
			return nil, fmt.Errorf("encode team session history: %w", encodeErr)
		}
		data := message.NewText(message.RoleUser, "<session-history-json>\n"+string(encoded)+"\n</session-history-json>")
		data.Visibility = message.VisibilityPrivate
		data.CreatedAt = time.Time{}
		contextMessages = append(contextMessages, data)
	}
	if c.historical != "" {
		data := message.NewText(message.RoleUser, "<historical-evidence-json>\n"+c.historical+"\n</historical-evidence-json>")
		data.Visibility = message.VisibilityPrivate
		data.CreatedAt = time.Time{}
		contextMessages = append(contextMessages, data)
	}
	result := make([]message.Message, 0, len(prefix)+len(contextMessages)+len(messages)-systemEnd)
	result = append(result, prefix...)
	result = append(result, contextMessages...)
	result = append(result, messages[systemEnd:]...)
	return result, nil
}

type teamRequestPreparer struct {
	mu        sync.Mutex
	context   teamHookContext
	images    []session.Attachment
	todo      session.TodoList
	loadTodo  func(context.Context) (session.TodoList, error)
	runID     string
	lastTodo  string
	lastRaw   []message.Message
	prepared  []message.Message
	target    int
	compactor *turnContext
}

func (p *teamRequestPreparer) prepare(ctx context.Context, request hyprovider.Request) (hyprovider.Request, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	original := append([]message.Message(nil), request.Messages...)
	if len(p.images) > 0 {
		for index := 0; index < len(original); index++ {
			if original[index].Role != message.RoleUser {
				continue
			}
			metadata := make(map[string]string, len(original[index].Metadata)+1)
			for key, value := range original[index].Metadata {
				metadata[key] = value
			}
			metadata[attachmentMetaKey] = EncodeAttachmentsMeta(p.images)
			original[index].Metadata = metadata
			break
		}
	}
	enriched, err := p.context.enrich(ctx, original)
	if err != nil {
		return hyprovider.Request{}, err
	}
	current := p.todo
	if p.loadTodo != nil {
		current, err = p.loadTodo(ctx)
		if err != nil {
			return hyprovider.Request{}, err
		}
	}
	reminder := todoReminder(current)
	if reminder == "" && p.lastTodo != "" {
		reminder = fmt.Sprintf("%s revision=%d %s", todoReminderPrefix, current.Revision, todoReminderCleared)
	}
	extendsPrevious := len(p.lastRaw) > 0 && len(enriched) >= len(p.lastRaw)
	if extendsPrevious {
		for index := range p.lastRaw {
			if !reflect.DeepEqual(p.lastRaw[index], enriched[index]) {
				extendsPrevious = false
				break
			}
		}
	}
	prepared := append([]message.Message(nil), enriched...)
	if extendsPrevious {
		prepared = append(append([]message.Message(nil), p.prepared...), enriched[len(p.lastRaw):]...)
	}
	if reminder != "" && reminder != p.lastTodo {
		update := (turnContext{runID: p.runID}).todoReminderMessage(reminder)
		update.CreatedAt = time.Time{}
		prepared = append(prepared, update)
		p.lastTodo = reminder
	}
	if p.compactor != nil {
		prepared, err = p.compactor.CompactTo(ctx, prepared, p.target)
		if err != nil {
			return hyprovider.Request{}, err
		}
	}
	p.lastRaw = append([]message.Message(nil), enriched...)
	p.prepared = append([]message.Message(nil), prepared...)
	request.Messages = prepared
	return request, nil
}

type teamUsageDriver struct {
	inner   hyprovider.Driver
	prepare func(context.Context, hyprovider.Request) (hyprovider.Request, error)
	report  func(hyprovider.Usage)
}

func (d *teamUsageDriver) Metadata() hyprovider.Metadata { return d.inner.Metadata() }

func (d *teamUsageDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	if d.prepare != nil {
		var err error
		request, err = d.prepare(ctx, request)
		if err != nil {
			return nil, err
		}
	}
	value, err := d.inner.Stream(ctx, request)
	if err != nil {
		return nil, err
	}
	return &teamUsageStream{Stream: value, report: d.report}, nil
}

type teamUsageStream struct {
	hyprovider.Stream
	report func(hyprovider.Usage)
}

func (s *teamUsageStream) Recv() (hyprovider.Event, error) {
	event, err := s.Stream.Recv()
	if err == nil && event.Kind == hyprovider.EventDone && s.report != nil {
		s.report(event.Usage)
	}
	return event, err
}

func (s *Service) emitTeamUsage(request TurnRequest, parentRunID, agentID, role, transport string, usage hyprovider.Usage, reasoningTokens, cacheWriteTokens int) {
	s.emit(s.ctx, Event{Kind: EventContextUsage, SessionID: request.SessionID, RunID: parentRunID, State: "reported", Data: map[string]string{
		"inputTokens": fmt.Sprint(usage.InputTokens), "cachedInputTokens": fmt.Sprint(usage.CachedInputTokens),
		"outputTokens": fmt.Sprint(usage.OutputTokens), "totalTokens": fmt.Sprint(usage.TotalTokens),
		"reasoningTokens": fmt.Sprint(reasoningTokens), "cacheWriteTokens": fmt.Sprint(cacheWriteTokens),
		"uncachedInputTokens": fmt.Sprint(max(0, usage.InputTokens-usage.CachedInputTokens)),
		"cacheStatus":         "reported", "aggregateOnly": "true", "requestKind": "team", "role": role, "agentID": agentID,
		"provider": request.Provider, "model": request.Model, "reasoning": request.Reasoning, "transport": transport,
	}})
}

func (c teamHookContext) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	return c.inner.Compact(ctx, history)
}

func (c teamHookContext) CompactTo(ctx context.Context, history []message.Message, target int) ([]message.Message, error) {
	if targeter, ok := c.inner.(hyagent.TargetContextManager); ok {
		return targeter.CompactTo(ctx, history, target)
	}
	return c.inner.Compact(ctx, history)
}

func (s *Service) finishProviderTeam(ctx context.Context, sessionID, runID, goal string, todo session.TodoList, execution agentservice.TeamExecution, err error) {
	if ctx.Err() != nil {
		s.observeStop(sessionID, runID, hooks.StopFailure, "cancelled", ctx.Err())
		s.emit(s.ctx, Event{Kind: EventRunCancelled, SessionID: sessionID, RunID: runID, State: "cancelled"})
		return
	}
	if err != nil {
		s.observeStop(sessionID, runID, hooks.StopFailure, "failed", err)
		s.emit(ctx, Event{Kind: EventRunFailed, SessionID: sessionID, RunID: runID, State: "failed", Text: err.Error()})
		return
	}
	answer := teamAnswer(execution.Result.State)
	if strings.TrimSpace(answer) == "" {
		s.observeStop(sessionID, runID, hooks.StopFailure, "empty_answer", errors.New("coding team completed without a reporter answer"))
		s.emit(ctx, Event{Kind: EventRunFailed, SessionID: sessionID, RunID: runID, State: "failed", Text: "coding team completed without a reporter answer"})
		return
	}
	if !s.emit(ctx, Event{Kind: EventTextDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: answer}) {
		deliveryErr := eventDeliveryError(ctx)
		s.observeStop(sessionID, runID, hooks.StopFailure, "event_backlog", deliveryErr)
		s.emit(ctx, Event{Kind: EventRunFailed, SessionID: sessionID, RunID: runID, State: "failed", Text: deliveryErr.Error()})
		return
	}
	if s.sessions != nil {
		if _, err := s.sessions.AppendBlock(ctx, sessionID, session.Block{Kind: "assistant", RunID: runID, Title: "Azem team", Content: answer, State: "completed"}); err != nil {
			s.observeStop(sessionID, runID, hooks.StopFailure, "persist_failed", err)
			s.emit(ctx, Event{Kind: EventRunFailed, SessionID: sessionID, RunID: runID, State: "failed", Text: err.Error()})
			return
		}
	}
	if err := s.persistRecap(ctx, sessionID, runID, goal, answer, todo); err != nil {
		s.emit(ctx, Event{Kind: EventRecapState, SessionID: sessionID, RunID: runID, State: "failed", Text: err.Error()})
	}
	s.emit(ctx, Event{Kind: EventRunFinished, SessionID: sessionID, RunID: runID, State: "completed"})
}

func teamAnswer(state multiagent.TeamState) string {
	for index := len(state.Tasks) - 1; index >= 0; index-- {
		result := state.Tasks[index].Result
		if result == nil || result.Structured == nil {
			continue
		}
		if answer, ok := result.Structured["answer"].(string); ok && strings.TrimSpace(answer) != "" {
			return answer
		}
	}
	return ""
}
