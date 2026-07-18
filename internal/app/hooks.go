package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/go-hydaelyn/agent"
	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/tool"
	"github.com/Viking602/go-hydaelyn/transport/mcpcontract"
)

func hookSources(cfg config.HooksConfig, configDir, homeDir, workspace string) []hooks.Source {
	if !cfg.Enabled {
		return nil
	}
	sources := []hooks.Source{{Path: filepath.Join(configDir, "hooks"), Trusted: true}}
	for _, path := range cfg.AdditionalPaths {
		sources = append(sources, hooks.Source{Path: path, Trusted: true})
	}
	if cfg.ClaudeCompatibility {
		sources = append(sources,
			hooks.Source{Path: filepath.Join(homeDir, ".claude", "settings.json"), Trusted: true},
			hooks.Source{Path: filepath.Join(homeDir, ".claude", "settings.local.json"), Trusted: true},
		)
	}
	if cfg.TrustProject {
		sources = append(sources, hooks.Source{Path: filepath.Join(workspace, ".azem", "hooks"), Trusted: true})
		if cfg.ClaudeCompatibility {
			sources = append(sources,
				hooks.Source{Path: filepath.Join(workspace, ".claude", "settings.json"), Trusted: true},
				hooks.Source{Path: filepath.Join(workspace, ".claude", "settings.local.json"), Trusted: true},
			)
		}
	}
	return sources
}

func (s *Service) AttachHooks(dispatcher hooks.Dispatcher) {
	dispatcher.Runner.Workspace = s.cfg.Workspace.Root
	dispatcher.AsyncContext = s.ctx
	dispatcher.AsyncAdd = s.hookWG.Add
	dispatcher.OnStart = func(info hooks.RunInfo) {
		s.emitHookEvent(Event{Kind: EventHookStarted, SessionID: info.SessionID, RunID: info.RunID,
			AgentID: info.AgentID, ToolCallID: info.ToolCallID, State: "running", Data: map[string]string{
				"event": string(info.Event), "name": info.Name, "source": info.Source, "tool": info.ToolName,
				"statusMessage": info.StatusMessage,
			}})
	}
	dispatcher.OnRun = func(run hooks.RunResult) {
		state := "completed"
		reason := run.Output.Reason
		if run.Denied {
			state = "blocked"
			if reason == "" {
				reason = run.Output.HookSpecificOutput.PermissionDecisionReason
			}
		} else if run.Failure != nil || run.ExitCode != 0 {
			state = "failed"
			if run.Failure != nil {
				reason = run.Failure.Error()
			}
		}
		stdout, stderr := run.Stdout, run.Stderr
		if run.Output.SuppressOutput {
			stdout, stderr = "", ""
		}
		s.emitHookEvent(Event{Kind: EventHookFinished, SessionID: run.SessionID, RunID: run.RunID,
			AgentID: run.AgentID, ToolCallID: run.ToolCallID, State: state, Data: map[string]string{
				"event": string(run.Event), "name": run.Name, "source": run.Source, "tool": run.ToolName,
				"exitCode": strconv.Itoa(run.ExitCode), "durationMS": strconv.FormatInt(run.Duration.Milliseconds(), 10),
				"reason": reason, "stdout": stdout, "stderr": stderr, "systemMessage": run.Output.SystemMessage,
				"stdoutTruncated": strconv.FormatBool(run.StdoutTruncated), "stderrTruncated": strconv.FormatBool(run.StderrTruncated),
			}})
		if len(run.Output.HookSpecificOutput.WatchPaths) > 0 {
			s.ensureHookWatcher().watchFiles(run.SessionID, run.Output.HookSpecificOutput.WatchPaths)
		}
		if run.Async && run.Failure == nil && run.ExitCode == 0 {
			var contextParts []string
			if text := strings.TrimSpace(run.Stdout); text != "" && !strings.HasPrefix(text, "{") {
				contextParts = append(contextParts, text)
			}
			if text := strings.TrimSpace(run.Output.HookSpecificOutput.AdditionalContext); text != "" {
				contextParts = append(contextParts, text)
			}
			if len(contextParts) > 0 {
				s.mu.Lock()
				s.hookAsyncContext[run.SessionID] = append(s.hookAsyncContext[run.SessionID], contextParts...)
				s.mu.Unlock()
			}
		}
	}
	s.hooks = dispatcher
}

func (s *Service) emitHookEvent(event Event) {
	event.At = time.Now().UTC()
	select {
	case <-s.ctx.Done():
	case s.events <- event.Clone():
	default:
	}
}

func (s *Service) dispatchLifecycle(ctx context.Context, event hooks.Event, metadata hooks.Metadata, add func(*hooks.Envelope)) error {
	envelope := hooks.Envelope{SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID,
		AgentType: metadata.AgentType, ParentRunID: metadata.ParentRunID, ParentToolCallID: metadata.ParentToolCallID,
		TranscriptPath: metadata.TranscriptPath, CWD: metadata.CWD, HookEventName: event}
	if add != nil {
		add(&envelope)
	}
	result := s.hooks.Dispatch(ctx, envelope)
	if result.PreventContinuation {
		return fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, result.StopReason)
	}
	if result.Denied {
		return fmt.Errorf("%w: %s", hooks.ErrDenied, result.Reason)
	}
	return nil
}

func (s *Service) promptHookContext(ctx context.Context, metadata hooks.Metadata, prompt string) (string, string, error) {
	e := hooks.Envelope{SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID,
		AgentType: metadata.AgentType, TranscriptPath: metadata.TranscriptPath, CWD: metadata.CWD, HookEventName: hooks.UserPromptSubmit, Prompt: prompt}
	result := s.hooks.Dispatch(ctx, e)
	if result.PreventContinuation {
		return "", "", fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, result.StopReason)
	}
	if result.Denied {
		return "", "", fmt.Errorf("%w: %s", hooks.ErrDenied, result.Reason)
	}
	s.mu.Lock()
	initialUser := strings.TrimSpace(s.hookInitialUsers[metadata.SessionID])
	initialContext := strings.TrimSpace(s.hookInitialContext[metadata.SessionID])
	asyncContext := append([]string(nil), s.hookAsyncContext[metadata.SessionID]...)
	delete(s.hookInitialUsers, metadata.SessionID)
	delete(s.hookInitialContext, metadata.SessionID)
	delete(s.hookAsyncContext, metadata.SessionID)
	s.mu.Unlock()
	parts := make([]string, 0, len(result.Runs)*2+1)
	if initialContext != "" {
		parts = append(parts, initialContext)
	}
	parts = append(parts, asyncContext...)
	for _, run := range result.Runs {
		if text := strings.TrimSpace(run.Stdout); text != "" && !strings.HasPrefix(text, "{") {
			parts = append(parts, text)
		}
		if text := strings.TrimSpace(run.Output.HookSpecificOutput.AdditionalContext); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), initialUser, nil
}

func (s *Service) hookMetadata(sessionID, runID string) hooks.Metadata {
	return hooks.Metadata{SessionID: sessionID, TranscriptPath: ensureHookTranscript(sessionID), RunID: runID, AgentID: "main", AgentType: "main", CWD: s.cfg.Workspace.Root}
}

func ensureHookTranscript(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "bootstrap"
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	directory := filepath.Join(cache, "azem", "hook-transcripts")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ""
	}
	digest := sha256.Sum256([]byte(id))
	path := filepath.Join(directory, fmt.Sprintf("%x.jsonl", digest[:12]))
	file, err := os.OpenFile(path, os.O_CREATE, 0o600)
	if err != nil {
		return ""
	}
	_ = file.Close()
	return path
}

func writeSessionHookTranscript(sessionID string, messages any) string {
	path := ensureHookTranscript(sessionID)
	if path == "" {
		return ""
	}
	type transcriptMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type transcriptRecord struct {
		Type      string            `json:"type"`
		SessionID string            `json:"sessionId"`
		Message   transcriptMessage `json:"message"`
	}
	var records []transcriptRecord
	switch values := messages.(type) {
	case []session.Block:
		for _, value := range values {
			role := value.Kind
			if role != "user" && role != "assistant" {
				continue
			}
			records = append(records, transcriptRecord{Type: role, SessionID: sessionID, Message: transcriptMessage{Role: role, Content: value.Content}})
		}
	case []message.Message:
		for _, value := range values {
			role := string(value.Role)
			if role != "user" && role != "assistant" && role != "system" {
				continue
			}
			records = append(records, transcriptRecord{Type: role, SessionID: sessionID, Message: transcriptMessage{Role: role, Content: value.Text}})
		}
	default:
		return ""
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return ""
	}
	var lines []json.RawMessage
	if json.Unmarshal(encoded, &lines) != nil {
		return ""
	}
	var output strings.Builder
	for _, record := range lines {
		output.Write(record)
		output.WriteByte('\n')
	}
	if os.WriteFile(path, []byte(output.String()), 0o600) != nil {
		return ""
	}
	return path
}

func (s *Service) startSessionHooks(ctx context.Context, sessionID, runID, source string, models ...string) error {
	if sessionID == "" {
		return nil
	}
	s.mu.Lock()
	if _, started := s.hookSessions[sessionID]; started {
		s.mu.Unlock()
		return nil
	}
	s.hookSessions[sessionID] = struct{}{}
	s.mu.Unlock()
	model := ""
	if len(models) > 0 {
		model = models[0]
	}
	metadata := s.hookMetadata(sessionID, runID)
	result := s.hooks.Dispatch(ctx, hooks.Envelope{SessionID: sessionID, TranscriptPath: metadata.TranscriptPath,
		RunID: runID, AgentID: metadata.AgentID, AgentType: metadata.AgentType, CWD: metadata.CWD,
		HookEventName: hooks.SessionStart, Source: source, Model: model})
	if result.PreventContinuation {
		s.mu.Lock()
		delete(s.hookSessions, sessionID)
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, result.StopReason)
	}
	var initialUsers, contextParts []string
	for _, run := range result.Runs {
		if text := strings.TrimSpace(run.Output.HookSpecificOutput.InitialUserMessage); text != "" {
			initialUsers = append(initialUsers, text)
		}
		if text := strings.TrimSpace(run.Output.HookSpecificOutput.AdditionalContext); text != "" {
			contextParts = append(contextParts, text)
		}
		if text := strings.TrimSpace(run.Stdout); text != "" && !strings.HasPrefix(text, "{") {
			contextParts = append(contextParts, text)
		}
	}
	if len(initialUsers) > 0 || len(contextParts) > 0 {
		s.mu.Lock()
		s.hookInitialUsers[sessionID] = strings.Join(initialUsers, "\n")
		s.hookInitialContext[sessionID] = strings.Join(contextParts, "\n")
		s.mu.Unlock()
	}
	return nil
}

func (s *Service) endSessionHooks(ctx context.Context, sessionID, reason string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	if _, started := s.hookSessions[sessionID]; !started {
		s.mu.Unlock()
		return
	}
	delete(s.hookSessions, sessionID)
	s.mu.Unlock()
	_ = s.dispatchLifecycle(ctx, hooks.SessionEnd, s.hookMetadata(sessionID, ""), func(e *hooks.Envelope) { e.Reason = reason })
}

func (s *Service) switchSessionHooks(ctx context.Context, sessionID, source, model string) error {
	s.mu.Lock()
	previous := s.currentSession
	s.mu.Unlock()
	if previous != "" && previous != sessionID {
		s.endSessionHooks(ctx, previous, "other")
	}
	return s.startSessionHooks(ctx, sessionID, "", source, model)
}

func compactDescription(before, after int) string {
	return fmt.Sprintf("Context compacted from %d to %d messages; %d messages omitted.", before, after, max(0, before-after))
}

func (s *Service) autoCompactHooks(metadata hooks.Metadata) func(context.Context, []message.Message, []message.Message, error) error {
	return func(ctx context.Context, before, after []message.Message, compactErr error) error {
		if after == nil && compactErr == nil {
			return s.dispatchLifecycle(ctx, hooks.PreCompact, metadata, func(e *hooks.Envelope) { e.Trigger = "auto" })
		}
		if compactErr == nil {
			_ = s.dispatchLifecycle(ctx, hooks.PostCompact, metadata, func(e *hooks.Envelope) {
				e.Trigger = "auto"
				e.CompactSummary = compactDescription(len(before), len(after))
			})
		}
		return compactErr
	}
}

func wrapHookDriver(host *Service, metadata hooks.Metadata, driver tool.Driver) tool.Driver {
	if host == nil {
		return driver
	}
	return hooks.WrapDriver(host.hooks, metadata, driver)
}

func (s *Service) emitTodoUpdated(sessionID string, todo session.TodoList) bool {
	snapshot := todo.Clone()
	ok := s.emit(s.ctx, Event{Kind: EventTodoUpdated, SessionID: sessionID, Todo: &snapshot})
	_ = s.dispatchLifecycle(s.ctx, hooks.TodoUpdated, s.hookMetadata(sessionID, ""), func(e *hooks.Envelope) { e.Todo = snapshot })
	return ok
}

func (s *Service) notifyHook(ctx context.Context, metadata hooks.Metadata, notificationType, title, message string) {
	_ = s.dispatchLifecycle(ctx, hooks.Notification, metadata, func(e *hooks.Envelope) {
		e.NotificationType, e.Title, e.Message = notificationType, title, message
	})
}

func (s *Service) stopHookGuardrail(metadata hooks.Metadata, event hooks.Event, transcriptPath func(hyagent.OutputGuardrailInput) string) hyagent.OutputGuardrail {
	stopHookActive := false
	return hyagent.NewOutputGuardrail("claude-stop-hook", func(ctx context.Context, input hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
		envelope := hooks.Envelope{
			SessionID: metadata.SessionID, RunID: metadata.RunID, AgentID: metadata.AgentID, AgentType: metadata.AgentType,
			ParentRunID: metadata.ParentRunID, ParentToolCallID: metadata.ParentToolCallID,
			TranscriptPath: firstNonempty(metadata.TranscriptPath, ensureHookTranscript(metadata.SessionID)),
			CWD:            metadata.CWD, HookEventName: event, StopHookActive: stopHookActive,
			LastAssistantMessage: strings.TrimSpace(input.Output.Text),
		}
		if transcriptPath != nil {
			path := transcriptPath(input)
			if event == hooks.SubagentStop {
				envelope.AgentTranscriptPath = path
			} else {
				envelope.TranscriptPath = path
			}
		}
		result := s.hooks.Dispatch(ctx, envelope)
		if result.PreventContinuation {
			stopHookActive = false
			return hyagent.AllowOutput(), nil
		}
		var additional []string
		for _, run := range result.Runs {
			if text := strings.TrimSpace(run.Output.HookSpecificOutput.AdditionalContext); text != "" {
				additional = append(additional, text)
			}
		}
		if !result.Denied && len(additional) == 0 {
			stopHookActive = false
			return hyagent.AllowOutput(), nil
		}
		stopHookActive = true
		reason := strings.TrimSpace(result.Reason)
		if len(additional) > 0 {
			reason = strings.TrimSpace(strings.Join(append([]string{reason}, additional...), "\n"))
		}
		if reason == "" {
			reason = "A Stop hook requested that the agent continue working."
		}
		return hyagent.RetryOutputWithPolicy(
			hyagent.RetryPolicy{IncludeRejectedOutput: true},
			message.NewText(message.RoleUser, reason),
		), nil
	})
}

func (s *Service) handleMCPElicitation(ctx context.Context, server string, request mcpcontract.Elicitation) (mcpcontract.ElicitationResult, error) {
	s.mu.Lock()
	sessionID := s.currentSession
	s.mu.Unlock()
	metadata := s.hookMetadata(sessionID, "")
	envelope := hooks.Envelope{
		SessionID: sessionID, CWD: metadata.CWD, HookEventName: hooks.Elicitation,
		MCPServerName: server, Message: request.Message, Mode: request.Mode, URL: request.URL,
		ElicitationID: request.ElicitationID, RequestedSchema: request.RequestedSchema,
	}
	decision := s.hooks.Dispatch(ctx, envelope)
	if decision.PreventContinuation {
		return mcpcontract.ElicitationResult{Action: "cancel"}, fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, decision.StopReason)
	}
	result := mcpcontract.ElicitationResult{Action: "cancel"}
	if decision.Denied {
		result.Action = "decline"
	} else {
		result = aggregateElicitationRuns(result, decision.Runs)
	}
	resultEnvelope := envelope
	resultEnvelope.HookEventName, resultEnvelope.Action, resultEnvelope.Content = hooks.ElicitationResult, result.Action, result.Content
	resultDecision := s.hooks.Dispatch(ctx, resultEnvelope)
	if resultDecision.PreventContinuation {
		return mcpcontract.ElicitationResult{Action: "cancel"}, fmt.Errorf("%w: %s", hooks.ErrPreventContinuation, resultDecision.StopReason)
	}
	if resultDecision.Denied {
		return mcpcontract.ElicitationResult{Action: "decline"}, nil
	}
	result = aggregateElicitationRuns(result, resultDecision.Runs)
	return result, nil
}

func aggregateElicitationRuns(current mcpcontract.ElicitationResult, runs []hooks.RunResult) mcpcontract.ElicitationResult {
	priority := map[string]int{"accept": 1, "cancel": 2, "decline": 3}
	if current.Action == "decline" {
		return current
	}
	winnerPriority := 0
	for _, run := range runs {
		action := run.Output.HookSpecificOutput.Action
		if priority[action] < winnerPriority {
			continue
		}
		if priority[action] == 0 {
			continue
		}
		winnerPriority = priority[action]
		current.Action = action
		if content, ok := run.Output.HookSpecificOutput.Content.(map[string]any); ok {
			current.Content = content
		} else if action != "accept" {
			current.Content = nil
		}
	}
	return current
}

func (s *Service) observeStop(sessionID, runID string, event hooks.Event, trigger string, err error, messages ...string) error {
	return s.dispatchLifecycle(s.ctx, event, s.hookMetadata(sessionID, runID), func(e *hooks.Envelope) {
		e.Trigger = trigger
		e.StopHookActive = false
		if len(messages) > 0 {
			e.LastAssistantMessage = messages[0]
		}
		if err != nil {
			if event == hooks.StopFailure {
				e.Error = stopFailureKind(err)
				e.ErrorDetails = err.Error()
			} else {
				e.Error = err.Error()
			}
		}
	})
}

func stopFailureKind(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "auth") || strings.Contains(message, "unauthorized"):
		return "authentication_failed"
	case strings.Contains(message, "billing") || strings.Contains(message, "quota"):
		return "billing_error"
	case strings.Contains(message, "rate limit") || strings.Contains(message, "429"):
		return "rate_limit"
	case strings.Contains(message, "max token") || strings.Contains(message, "output token"):
		return "max_output_tokens"
	case strings.Contains(message, "invalid request") || strings.Contains(message, "400"):
		return "invalid_request"
	case strings.Contains(message, "server error") || strings.Contains(message, "500") || strings.Contains(message, "502") || strings.Contains(message, "503"):
		return "server_error"
	default:
		return "unknown"
	}
}
