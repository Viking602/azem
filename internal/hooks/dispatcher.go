package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Viking602/go-hydaelyn/message"
	"github.com/Viking602/go-hydaelyn/provider"
	"github.com/Viking602/go-hydaelyn/tool"
)

type preToolPermissionKey struct{}

func PreToolPermissionFromContext(ctx context.Context) string {
	value, _ := ctx.Value(preToolPermissionKey{}).(string)
	return value
}

var ErrDenied = errors.New("hook denied operation")
var ErrPreventContinuation = errors.New("hook prevented continuation")

type Callback func(RunResult)
type StartCallback func(RunInfo)
type RunInfo struct {
	Event         Event
	Name          string
	Source        string
	SessionID     string
	RunID         string
	AgentID       string
	ToolCallID    string
	ToolName      string
	StatusMessage string
}
type Dispatcher struct {
	Registry     *Registry
	Runner       Runner
	OnStart      StartCallback
	OnRun        Callback
	AsyncContext context.Context
	AsyncAdd     func(int)
}
type DispatchResult struct {
	Denied              bool
	PreventContinuation bool
	StopReason          string
	Reason              string
	UpdatedInput        json.RawMessage
	PermissionDecision  string
	Runs                []RunResult
}

func (d Dispatcher) Dispatch(ctx context.Context, e Envelope) DispatchResult {
	result := DispatchResult{UpdatedInput: e.ToolInput}
	if d.Registry == nil {
		return result
	}
	gate := blockingEvent(e)
	query := matcherQuery(e)
	for _, command := range d.Registry.Commands(e.HookEventName) {
		if (query != "" && !command.Matches(query)) || !command.MatchesCondition(e) || !d.Registry.Claim(command) {
			continue
		}
		if d.OnStart != nil {
			d.OnStart(RunInfo{
				Event: e.HookEventName, Name: command.Name, Source: command.Source,
				SessionID: e.SessionID, RunID: e.RunID, AgentID: e.AgentID,
				ToolCallID: e.ToolCallID, ToolName: e.ToolName, StatusMessage: command.StatusMessage,
			})
		}
		if command.Async {
			asyncContext := d.AsyncContext
			if asyncContext == nil {
				asyncContext = context.WithoutCancel(ctx)
			}
			if d.AsyncAdd != nil {
				d.AsyncAdd(1)
			}
			go func(command Command, envelope Envelope) {
				if d.AsyncAdd != nil {
					defer d.AsyncAdd(-1)
				}
				run := d.Runner.Run(asyncContext, command, envelope)
				if d.OnRun != nil {
					d.OnRun(run)
				}
			}(command, e)
			continue
		}
		run := d.Runner.Run(ctx, command, e)
		result.Runs = append(result.Runs, run)
		if d.OnRun != nil {
			d.OnRun(run)
		}
		if run.PreventContinuation {
			result.PreventContinuation = true
			if result.StopReason == "" {
				result.StopReason = run.StopReason
			}
			continue
		}
		permission := run.Output.HookSpecificOutput.PermissionDecision
		if permission == "ask" || (permission == "allow" && result.PermissionDecision == "") {
			result.PermissionDecision = permission
		}
		if run.Denied && gate {
			result.Denied = true
			result.Reason = run.Output.Reason
			if result.Reason == "" {
				result.Reason = run.Output.HookSpecificOutput.PermissionDecisionReason
			}
			continue
		}
		if run.Failure != nil && gate && command.FailurePolicy == FailureClosed {
			result.Denied = true
			result.Reason = run.Failure.Error()
			continue
		}
		updated := run.Output.HookSpecificOutput.UpdatedInput
		if len(updated) > 0 && e.HookEventName == PreToolUse {
			var object map[string]any
			if len(updated) > MaxInput || json.Unmarshal(updated, &object) != nil || object == nil {
				if command.FailurePolicy == FailureClosed {
					result.Denied = true
					result.Reason = "hook updatedInput must be a JSON object no larger than 128 KiB"
					continue
				}
				continue
			}
			result.UpdatedInput = append(json.RawMessage(nil), updated...)
			e.ToolInput = result.UpdatedInput
		}
	}
	return result
}

func blockingEvent(e Envelope) bool {
	switch e.HookEventName {
	case PreToolUse, UserPromptSubmit, PreCompact, PermissionRequest, Stop, SubagentStop,
		TeammateIdle, TaskCreated, TaskCompleted, Elicitation, ElicitationResult, WorktreeCreate:
		return true
	case ConfigChange:
		return e.Source != "policy_settings"
	default:
		return false
	}
}

func matcherQuery(e Envelope) string {
	switch e.HookEventName {
	case PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest, PermissionDenied:
		return e.ToolName
	case SessionStart, ConfigChange:
		return e.Source
	case Setup, PreCompact, PostCompact:
		return e.Trigger
	case Notification:
		return e.NotificationType
	case SessionEnd:
		return e.Reason
	case StopFailure:
		if value, ok := e.Error.(string); ok {
			return value
		}
	case SubagentStart, SubagentStop:
		return e.AgentType
	case Elicitation, ElicitationResult:
		return e.MCPServerName
	case InstructionsLoaded:
		return e.LoadReason
	case FileChanged:
		return filepath.Base(e.FilePath)
	}
	return ""
}

type Metadata struct{ SessionID, TranscriptPath, RunID, AgentID, AgentType, ParentRunID, ParentToolCallID, CWD string }
type Handler struct {
	dispatcher Dispatcher
	metadata   Metadata
}

func NewHandler(dispatcher Dispatcher, metadata Metadata) *Handler {
	return &Handler{dispatcher: dispatcher, metadata: metadata}
}
func (h *Handler) envelope(event Event) Envelope {
	return Envelope{SessionID: h.metadata.SessionID, TranscriptPath: h.metadata.TranscriptPath, RunID: h.metadata.RunID, AgentID: h.metadata.AgentID, AgentType: h.metadata.AgentType, ParentRunID: h.metadata.ParentRunID, ParentToolCallID: h.metadata.ParentToolCallID, CWD: h.metadata.CWD, HookEventName: event}
}
func (h *Handler) BeforeToolCall(ctx context.Context, call *tool.Call) error {
	if call == nil {
		return nil
	}
	e := h.envelope(PreToolUse)
	e.ToolName = call.Name
	e.ToolCallID = call.ID
	e.ToolUseID = call.ID
	e.ToolInput = call.Arguments
	r := h.dispatcher.Dispatch(ctx, e)
	if r.Denied {
		return fmt.Errorf("%w: %s", ErrDenied, r.Reason)
	}
	if len(r.UpdatedInput) > 0 {
		call.Arguments = r.UpdatedInput
	}
	return nil
}
func (h *Handler) AfterToolCall(ctx context.Context, result *tool.Result) error {
	if result == nil {
		return nil
	}
	event := PostToolUse
	if result.IsError {
		event = PostToolUseFailure
	}
	e := h.envelope(event)
	e.ToolName = result.Name
	e.ToolCallID = result.ToolCallID
	e.ToolUseID = result.ToolCallID
	e.ToolResponse = result
	if result.IsError {
		e.Error = result.Content
	}
	h.dispatcher.Dispatch(ctx, e)
	return nil
}
func (*Handler) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	return messages, nil
}
func (*Handler) BeforeModelCall(context.Context, *provider.Request) error { return nil }
func (*Handler) OnEvent(context.Context, provider.Event) error            { return nil }

type hookedDriver struct {
	inner      tool.Driver
	dispatcher Dispatcher
	metadata   Metadata
}

// WrapDriver installs command hooks inside the tool execution boundary. A hook
// denial becomes a tool result rather than an engine error, so the model can
// recover without bypassing Azem's approval and durable action-attempt layers.
func WrapDriver(dispatcher Dispatcher, metadata Metadata, inner tool.Driver) tool.Driver {
	if inner == nil {
		return nil
	}
	return &hookedDriver{inner: inner, dispatcher: dispatcher, metadata: metadata}
}

func (d *hookedDriver) Definition() tool.Definition { return d.inner.Definition() }

func (d *hookedDriver) Execute(ctx context.Context, call tool.Call, sink tool.UpdateSink) (tool.Result, error) {
	input := Envelope{
		SessionID: d.metadata.SessionID, RunID: d.metadata.RunID, AgentID: d.metadata.AgentID,
		AgentType: d.metadata.AgentType, ParentRunID: d.metadata.ParentRunID,
		ParentToolCallID: d.metadata.ParentToolCallID, CWD: d.metadata.CWD,
		HookEventName: PreToolUse, ToolCallID: call.ID, ToolUseID: call.ID, ToolName: call.Name, ToolInput: call.Arguments,
	}
	decision := d.dispatcher.Dispatch(ctx, input)
	if decision.PreventContinuation {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, IsError: true, Content: decision.StopReason},
			fmt.Errorf("%w: %s", ErrPreventContinuation, decision.StopReason)
	}
	if decision.Denied {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "blocked by hook"
		}
		return tool.Result{
			ToolCallID: call.ID, Name: call.Name, IsError: true,
			Content: "Blocked by hook: " + reason,
		}, nil
	}
	if len(decision.UpdatedInput) > 0 {
		call.Arguments = decision.UpdatedInput
	}
	if decision.PermissionDecision != "" {
		ctx = context.WithValue(ctx, preToolPermissionKey{}, decision.PermissionDecision)
	}
	result, err := d.inner.Execute(ctx, call, sink)
	if result.ToolCallID == "" {
		result.ToolCallID = call.ID
	}
	if result.Name == "" {
		result.Name = call.Name
	}
	event := PostToolUse
	if err != nil || result.IsError {
		event = PostToolUseFailure
	}
	envelope := Envelope{
		SessionID: d.metadata.SessionID, RunID: d.metadata.RunID, AgentID: d.metadata.AgentID,
		AgentType: d.metadata.AgentType, ParentRunID: d.metadata.ParentRunID,
		ParentToolCallID: d.metadata.ParentToolCallID, CWD: d.metadata.CWD,
		HookEventName: event, ToolCallID: call.ID, ToolUseID: call.ID, ToolName: call.Name, ToolInput: call.Arguments, ToolResponse: boundedToolResult(result),
	}
	if err != nil {
		envelope.Error = err.Error()
	} else if result.IsError {
		envelope.Error = boundedJSONString(result.Content, 48<<10)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		envelope.IsInterrupt = true
	}
	post := d.dispatcher.Dispatch(ctx, envelope)
	for _, run := range post.Runs {
		if updated := run.Output.HookSpecificOutput.UpdatedMCPToolOutput; updated != nil && strings.HasPrefix(call.Name, "mcp") {
			if encoded, marshalErr := json.Marshal(updated); marshalErr == nil {
				result.Structured = encoded
				result.Content = string(encoded)
			}
		}
		if additional := strings.TrimSpace(run.Output.HookSpecificOutput.AdditionalContext); additional != "" {
			if result.Content != "" {
				result.Content += "\n\n"
			}
			result.Content += additional
		}
		if run.Denied {
			reason := firstNonempty(strings.TrimSpace(run.Output.Reason), strings.TrimSpace(run.Output.HookSpecificOutput.PermissionDecisionReason))
			if reason != "" {
				if result.Content != "" {
					result.Content += "\n\n"
				}
				result.Content += "Post-tool hook feedback: " + reason
			}
		}
	}
	if post.PreventContinuation {
		return result, fmt.Errorf("%w: %s", ErrPreventContinuation, post.StopReason)
	}
	return result, err
}

func boundedToolResult(result tool.Result) tool.Result {
	const fieldLimit = 48 << 10
	result.Content = boundedJSONString(result.Content, fieldLimit)
	if len(result.Structured) > fieldLimit || !json.Valid(result.Structured) {
		// json.RawMessage must remain valid JSON or the enclosing hook envelope
		// cannot be marshaled. The text result still carries bounded context.
		result.Structured = nil
	}
	return result
}

func boundedJSONString(value string, encodedLimit int) string {
	encoded, _ := json.Marshal(value)
	if len(encoded) <= encodedLimit {
		return value
	}
	low, high := 0, len(value)
	for low < high {
		mid := low + (high-low+1)/2
		candidate := strings.ToValidUTF8(value[:mid], "") + "…"
		encoded, _ = json.Marshal(candidate)
		if len(encoded) <= encodedLimit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return strings.ToValidUTF8(value[:low], "") + "…"
}
