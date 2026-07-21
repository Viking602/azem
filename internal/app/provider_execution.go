package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/multiagent"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/stream"
)

func (s *Service) providerStreamSink(sessionID, runID, providerID, modelID, reasoning, transport string) stream.Sink {
	return stream.SinkFunc(func(ctx context.Context, frame stream.Frame) error {
		data := map[string]string{}
		switch frame.Kind {
		case stream.FrameText:
			s.emit(ctx, Event{Kind: EventTextDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: frame.Text, Data: data})
		case stream.FrameThinking:
			s.emit(ctx, Event{Kind: EventThinkingDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: frame.Thinking, Data: data})
		case stream.FrameToolCall:
			if frame.ToolCall != nil {
				data["name"] = frame.ToolCall.Name
				data["arguments"] = string(frame.ToolCall.Arguments)
				s.emit(ctx, Event{Kind: EventToolStarted, SessionID: sessionID, RunID: runID, ToolCallID: frame.ToolCall.ID, State: "running", Data: data})
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
				s.emit(ctx, Event{Kind: EventToolFinished, SessionID: sessionID, RunID: runID, ToolCallID: frame.ToolResult.ToolCallID, State: state, Text: frame.ToolResult.Content, Data: data})
			}
		case stream.FrameDone:
			usage := map[string]string{}
			if frame.Usage.InputTokens != 0 || frame.Usage.OutputTokens != 0 || frame.Usage.TotalTokens != 0 {
				usage["inputTokens"] = fmt.Sprint(frame.Usage.InputTokens)
				usage["cachedInputTokens"] = fmt.Sprint(frame.Usage.CachedInputTokens)
				usage["uncachedInputTokens"] = fmt.Sprint(max(0, frame.Usage.InputTokens-frame.Usage.CachedInputTokens))
				usage["outputTokens"] = fmt.Sprint(frame.Usage.OutputTokens)
				usage["totalTokens"] = fmt.Sprint(frame.Usage.TotalTokens)
				usage["cacheStatus"] = "reported"
				usage["requestKind"] = "main"
				usage["provider"] = providerID
				usage["model"] = modelID
				usage["reasoning"] = reasoning
				usage["transport"] = transport
			}
			s.emit(s.ctx, Event{Kind: EventContextUsage, SessionID: sessionID, RunID: runID, State: "reported", Data: usage})
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
	result := engine.RunStream(ctx, task, hyagent.OutputPolicy{}, s.providerStreamSink(request.SessionID, run.RunID, request.Provider, request.Model, request.Reasoning, s.providerTransport(request.Provider)))
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
	reminder := todoReminder(request.Todo)
	if len(request.History) == 0 && reminder == "" {
		return request.Prompt
	}
	var context strings.Builder
	if len(request.History) > 0 {
		context.WriteString("Continue this coding session using the prior conversation as context.\n\n")
	}
	for _, block := range request.History {
		text := strings.TrimSpace(block.Content)
		if text == "" {
			continue
		}
		fmt.Fprintf(&context, "%s: %s\n", firstNonempty(block.Title, block.Kind), text)
	}
	if reminder != "" {
		context.WriteString("\n")
		context.WriteString(reminder)
		context.WriteString("\n")
	}
	context.WriteString("\nCurrent request: ")
	context.WriteString(request.Prompt)
	return context.String()
}

func (s *Service) runProviderTeam(ctx context.Context, request TurnRequest, runID, goal, modelID string, resolver hyprovider.Resolver) {
	defer s.wg.Done()
	defer s.clearRun(runID)
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: request.SessionID, RunID: runID, State: "running"})
	models := agentservice.TeamModels{Planner: modelID, Implementer: modelID, Reviewer: modelID, Reporter: modelID}
	toolBus, toolErr := s.teamToolBus(ctx, request.SessionID, runID, goal)
	if toolErr != nil {
		s.finishProviderTeam(ctx, request.SessionID, runID, request.Prompt, request.Todo, agentservice.TeamExecution{}, toolErr)
		return
	}
	execution, err := s.coding.StartTeamWithIDAndToolsMetadataHooks(ctx, runID, goal, models, resolver, toolBus, map[string]string{
		"session_id":           request.SessionID,
		"provider":             request.Provider,
		"model":                modelID,
		"reasoning":            request.Reasoning,
		"original_prompt":      request.Prompt,
		"hook_private_context": request.privateContext,
	}, s.teamHooks(request.SessionID, runID, request.privateContext, request.historicalContext), func(state multiagent.TeamState) {
		for _, instance := range state.Instances {
			s.emit(ctx, Event{
				Kind: EventAgentState, SessionID: request.SessionID, RunID: runID, AgentID: instance.ID, State: string(instance.State),
				Agent: &AgentStatePayload{Type: instance.ClassName, Model: modelID, ParentRunID: runID, Activity: string(instance.State)},
				Data:  map[string]string{"id": instance.ID, "role": instance.ClassName, "state": string(instance.State)},
			})
		}
	})
	s.finishProviderTeam(ctx, request.SessionID, runID, request.Prompt, request.Todo, execution, err)
}

func (s *Service) runResumedProviderTeam(ctx context.Context, sessionID, runID, durableGoal, recapGoal, modelID, privateContext, historicalContext string, resolver hyprovider.Resolver) {
	defer s.wg.Done()
	defer s.clearRun(runID)
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: sessionID, RunID: runID, State: "resuming"})
	models := agentservice.TeamModels{Planner: modelID, Implementer: modelID, Reviewer: modelID, Reporter: modelID}
	toolBus, toolErr := s.teamToolBus(ctx, sessionID, runID, durableGoal)
	if toolErr != nil {
		s.finishProviderTeam(ctx, sessionID, runID, recapGoal, session.TodoList{}, agentservice.TeamExecution{}, toolErr)
		return
	}
	execution, err := s.coding.ResumeTeamWithToolsHooks(ctx, runID, models, resolver, toolBus, s.teamHooks(sessionID, runID, privateContext, historicalContext), func(state multiagent.TeamState) {
		for _, instance := range state.Instances {
			s.emit(ctx, Event{
				Kind: EventAgentState, SessionID: sessionID, RunID: runID, AgentID: instance.ID, State: string(instance.State),
				Agent: &AgentStatePayload{Type: instance.ClassName, Model: modelID, ParentRunID: runID, Activity: string(instance.State)},
				Data:  map[string]string{"id": instance.ID, "role": instance.ClassName, "state": string(instance.State)},
			})
		}
	})
	s.finishProviderTeam(ctx, sessionID, runID, recapGoal, session.TodoList{}, execution, err)
}

func (s *Service) teamHooks(sessionID, parentRunID, rootContext, historicalContext string) agentservice.TeamHooks {
	beforeTask := func(ctx context.Context, dispatch multiagent.Dispatch, class multiagent.AgentClass) error {
		metadata := hooks.Metadata{SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To, AgentType: class.Name, ParentRunID: parentRunID, CWD: s.cfg.Workspace.Root}
		return s.dispatchLifecycle(ctx, hooks.TaskCreated, metadata, func(e *hooks.Envelope) {
			e.TaskID, e.TaskSubject = dispatch.Task.ID, dispatch.Task.Goal
		})
	}
	prepare := func(ctx context.Context, engine hyagent.Engine, dispatch multiagent.Dispatch, class multiagent.AgentClass) (hyagent.Engine, error) {
		metadata := hooks.Metadata{SessionID: sessionID, RunID: dispatch.Task.RunID, AgentID: dispatch.To, AgentType: class.Name, ParentRunID: parentRunID, CWD: s.cfg.Workspace.Root}
		extraBody := make(map[string]any, len(engine.ExtraBody)+1)
		for key, value := range engine.ExtraBody {
			extraBody[key] = value
		}
		extraBody["prompt_cache_key"] = dispatch.Task.RunID
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
		contextParts := append([]string{strings.TrimSpace(rootContext)}, additional...)
		contextText := strings.TrimSpace(strings.Join(contextParts, "\n"))
		historical := ""
		if class.Name == agentservice.PlannerClass {
			historical = historicalContext
		}
		if contextText != "" || historical != "" {
			engine.ContextBuilder = teamHookContext{inner: engine.ContextBuilder, additional: contextText, historical: historical}
		}
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
}

func (c teamHookContext) Build(ctx context.Context, task api.Task) ([]message.Message, error) {
	messages, err := c.inner.Build(ctx, task)
	if err != nil {
		return nil, err
	}
	prefix := make([]message.Message, 0, 2)
	if c.additional != "" {
		value := message.NewText(message.RoleSystem, "[Trusted SubagentStart hook context]\n"+c.additional)
		value.Visibility = message.VisibilityPrivate
		prefix = append(prefix, value)
	}
	if c.historical != "" {
		policy := message.NewText(message.RoleSystem, historicalEvidencePolicy)
		policy.Visibility = message.VisibilityPrivate
		prefix = append(prefix, policy)
	}
	result := append(prefix, messages...)
	if c.historical == "" {
		return result, nil
	}
	insertAt := 0
	for insertAt < len(result) && result[insertAt].Role == message.RoleSystem {
		insertAt++
	}
	data := message.NewText(message.RoleUser, "<historical-evidence-json>\n"+c.historical+"\n</historical-evidence-json>")
	data.Visibility = message.VisibilityPrivate
	result = append(result, message.Message{})
	copy(result[insertAt+1:], result[insertAt:])
	result[insertAt] = data
	return result, nil
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
	s.emit(ctx, Event{Kind: EventTextDelta, SessionID: sessionID, RunID: runID, State: "streaming", Text: answer})
	if s.sessions != nil {
		if err := s.sessions.AppendBlock(ctx, sessionID, session.Block{Kind: "assistant", RunID: runID, Title: "Azem team", Content: answer, State: "completed"}); err != nil {
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
