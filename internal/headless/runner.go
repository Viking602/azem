package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/app"
)

type Mode string

const (
	ModeText Mode = "text"
	ModeJSON Mode = "json"
)

type Runtime interface {
	StartConfiguredTurn(request app.TurnRequest) (string, error)
	NextEvent(ctx context.Context) (app.Event, error)
	ExecuteAction(ctx context.Context, action app.Action) error
	CancelActiveWithChildren(children bool) bool
}

type Options struct {
	Mode          Mode
	SessionID     string
	Provider      string
	Model         string
	Reasoning     string
	AgentMode     string
	Prompts       []string
	PrintThinking bool
	AutoApprove   bool
	Output        io.Writer
	Diagnostics   io.Writer
}

type Result struct {
	RunIDs []string
	Text   string
}

type JSONFrame struct {
	Type       string            `json:"type"`
	Version    int               `json:"version"`
	Sequence   int64             `json:"sequence,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	SessionID  string            `json:"sessionId,omitempty"`
	RunID      string            `json:"runId,omitempty"`
	AgentID    string            `json:"agentId,omitempty"`
	ToolCallID string            `json:"toolCallId,omitempty"`
	ApprovalID string            `json:"approvalId,omitempty"`
	Text       string            `json:"text,omitempty"`
	TextPhase  string            `json:"textPhase,omitempty"`
	State      string            `json:"state,omitempty"`
	Data       map[string]string `json:"data,omitempty"`
	Payload    map[string]any    `json:"payload,omitempty"`
	At         time.Time         `json:"at,omitempty"`
}

func Run(ctx context.Context, runtime Runtime, options Options) (Result, error) {
	if runtime == nil {
		return Result{}, errors.New("headless runtime is unavailable")
	}
	if options.Mode == "" {
		options.Mode = ModeText
	}
	if options.Mode != ModeText && options.Mode != ModeJSON {
		return Result{}, fmt.Errorf("unsupported headless mode %q", options.Mode)
	}
	if options.Output == nil {
		return Result{}, errors.New("headless output is required")
	}
	prompts := make([]string, 0, len(options.Prompts))
	for _, prompt := range options.Prompts {
		if strings.TrimSpace(prompt) != "" {
			prompts = append(prompts, prompt)
		}
	}
	if len(prompts) == 0 {
		return Result{}, errors.New("headless mode requires at least one prompt")
	}
	writer := bufio.NewWriter(options.Output)
	defer writer.Flush()
	encoder := json.NewEncoder(writer)
	sequence := int64(0)
	if options.Mode == ModeJSON {
		if err := encoder.Encode(JSONFrame{Type: "ready", Version: 1, SessionID: options.SessionID}); err != nil {
			return Result{}, err
		}
		if err := writer.Flush(); err != nil {
			return Result{}, err
		}
	}
	result := Result{}
	for _, prompt := range prompts {
		runID, err := runtime.StartConfiguredTurn(app.TurnRequest{
			SessionID: options.SessionID, Prompt: prompt, Provider: options.Provider, Model: options.Model,
			Reasoning: options.Reasoning, AgentMode: options.AgentMode,
		})
		if err != nil {
			return result, err
		}
		result.RunIDs = append(result.RunIDs, runID)
		terminal, text, nextSequence, err := consumeRun(ctx, runtime, writer, encoder, options, runID, sequence)
		sequence = nextSequence
		result.Text += text
		if err != nil {
			return result, err
		}
		if terminal != app.EventRunFinished {
			return result, fmt.Errorf("run %s ended with %s", runID, terminal)
		}
	}
	return result, writer.Flush()
}

func consumeRun(ctx context.Context, runtime Runtime, writer *bufio.Writer, encoder *json.Encoder, options Options, runID string, sequence int64) (app.EventKind, string, int64, error) {
	var visible strings.Builder
	interactionFailure := error(nil)
	for {
		event, err := runtime.NextEvent(ctx)
		if err != nil {
			if ctx.Err() != nil {
				runtime.CancelActiveWithChildren(true)
				return "", visible.String(), sequence, ctx.Err()
			}
			return "", visible.String(), sequence, err
		}
		if event.RunID != runID {
			continue
		}
		sequence++
		if options.Mode == ModeJSON {
			if err := encoder.Encode(FrameForEvent(sequence, event)); err != nil {
				return "", visible.String(), sequence, err
			}
			if err := writer.Flush(); err != nil {
				return "", visible.String(), sequence, err
			}
		} else {
			switch event.Kind {
			case app.EventTextDelta:
				if event.TextPhase == "commentary" || event.TextPhase == "final_answer" || event.TextPhase == "" {
					if _, err := writer.WriteString(event.Text); err != nil {
						return "", visible.String(), sequence, err
					}
					visible.WriteString(event.Text)
					if err := writer.Flush(); err != nil {
						return "", visible.String(), sequence, err
					}
				}
			case app.EventThinkingDelta:
				if options.PrintThinking {
					if _, err := writer.WriteString(event.Text); err != nil {
						return "", visible.String(), sequence, err
					}
					if err := writer.Flush(); err != nil {
						return "", visible.String(), sequence, err
					}
				}
			}
		}
		switch event.Kind {
		case app.EventApprovalRequested:
			decision := "deny"
			if options.AutoApprove {
				decision = "once"
			} else if interactionFailure == nil {
				interactionFailure = errors.New("headless run requested approval; use explicit auto-approval or an interactive mode")
			}
			if err := runtime.ExecuteAction(ctx, app.Action{Kind: app.ActionResolveApproval, Target: event.ApprovalID, Decision: decision}); err != nil && interactionFailure == nil {
				interactionFailure = err
			}
		case app.EventUserInputRequested, app.EventPlanProposed:
			if interactionFailure == nil {
				interactionFailure = fmt.Errorf("headless run requires unsupported interactive input (%s)", event.Kind)
			}
			runtime.CancelActiveWithChildren(true)
		case app.EventRunFinished, app.EventRunFailed, app.EventRunCancelled:
			if options.Mode == ModeText && visible.Len() > 0 && !strings.HasSuffix(visible.String(), "\n") {
				if _, err := writer.WriteString("\n"); err != nil {
					return event.Kind, visible.String(), sequence, err
				}
				_ = writer.Flush()
			}
			if interactionFailure != nil {
				return event.Kind, visible.String(), sequence, interactionFailure
			}
			if event.Kind == app.EventRunFailed {
				return event.Kind, visible.String(), sequence, errors.New(firstNonempty(event.Text, "provider run failed"))
			}
			if event.Kind == app.EventRunCancelled {
				return event.Kind, visible.String(), sequence, context.Canceled
			}
			return event.Kind, visible.String(), sequence, nil
		}
	}
}

func FrameForEvent(sequence int64, event app.Event) JSONFrame {
	return JSONFrame{
		Type: "event", Version: 1, Sequence: sequence, Kind: string(event.Kind), SessionID: event.SessionID, RunID: event.RunID,
		AgentID: event.AgentID, ToolCallID: event.ToolCallID, ApprovalID: event.ApprovalID,
		Text: event.Text, TextPhase: event.TextPhase, State: event.State, Data: event.Data, Payload: eventPayload(event), At: event.At,
	}
}

func eventPayload(event app.Event) map[string]any {
	payload := make(map[string]any)
	put := func(key string, value any, include bool) {
		if include {
			payload[key] = value
		}
	}
	put("agent", event.Agent, event.Agent != nil)
	put("agentBlocks", event.AgentBlocks, len(event.AgentBlocks) > 0)
	put("agentCatalog", event.AgentCatalog, len(event.AgentCatalog) > 0)
	put("agentSnapshots", event.AgentSnapshots, len(event.AgentSnapshots) > 0)
	put("skillCatalog", event.SkillCatalog, len(event.SkillCatalog) > 0)
	put("skillDiagnostics", event.SkillDiagnostics, len(event.SkillDiagnostics) > 0)
	put("pluginCatalog", event.PluginCatalog, len(event.PluginCatalog) > 0)
	put("pluginDiagnostics", event.PluginDiagnostics, len(event.PluginDiagnostics) > 0)
	put("hookCatalog", event.HookCatalog, event.HookCatalog != nil)
	put("contextProfile", event.ContextProfile, event.ContextProfile != nil)
	put("todo", event.Todo, event.Todo != nil)
	put("memories", event.Memories, len(event.Memories) > 0)
	put("recap", event.Recap, event.Recap != nil)
	put("modelRoutes", event.ModelRoutes, len(event.ModelRoutes) > 0)
	put("modelProviders", event.ModelProviders, len(event.ModelProviders) > 0)
	put("background", event.Background, len(event.Background) > 0)
	put("backgroundLogs", event.BackgroundLogs, event.BackgroundLogs != nil)
	put("gitBranches", event.GitBranches, len(event.GitBranches) > 0)
	put("usageReport", event.UsageReport, event.UsageReport != nil)
	put("securityConfig", event.SecurityConfig, event.SecurityConfig != nil)
	put("security", event.Security, event.Security != nil)
	put("securityScans", event.SecurityScans, len(event.SecurityScans) > 0)
	put("securityFindings", event.SecurityFindings, len(event.SecurityFindings) > 0)
	put("securityFinding", event.SecurityFinding, event.SecurityFinding != nil)
	put("securityPatch", event.SecurityPatch, event.SecurityPatch != nil)
	put("workspaceDirty", event.WorkspaceDirty, event.WorkspaceDirty)
	if len(payload) == 0 {
		return nil
	}
	return payload
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
