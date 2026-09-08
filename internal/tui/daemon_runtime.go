package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopclient"
	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/session"
	"github.com/google/uuid"
)

type DaemonRuntime struct {
	client *desktopclient.WorkspaceClient
	mu     sync.RWMutex
	latest desktop.ReconnectSnapshot
}

func NewDaemonRuntime(client *desktopclient.WorkspaceClient, snapshot desktop.ReconnectSnapshot) (*DaemonRuntime, error) {
	if client == nil {
		return nil, fmt.Errorf("workspace client is required")
	}
	if snapshot.DaemonEpoch == "" {
		return nil, fmt.Errorf("initial daemon snapshot is incomplete")
	}
	return &DaemonRuntime{client: client, latest: snapshot}, nil
}

func (runtime *DaemonRuntime) NextEvent(ctx context.Context) (app.Event, error) {
	for {
		event, err := runtime.client.Next(ctx)
		if err != nil {
			return app.Event{}, err
		}
		switch event.Kind {
		case desktopclient.EventRuntime:
			if event.Runtime != nil {
				return appEventFromDesktop(*event.Runtime), nil
			}
		case desktopclient.EventSnapshot:
			if event.Snapshot != nil {
				runtime.ApplySnapshot(*event.Snapshot)
				projection := app.RuntimeProjectionSnapshot{
					Session: event.Snapshot.Session, Runs: event.Snapshot.Runs,
					LiveBlocks: event.Snapshot.LiveBlocks, PendingControls: event.Snapshot.Controls,
					PromptQueues: event.Snapshot.PromptQueues, Recovery: event.Snapshot.RuntimeRecovery,
				}
				return app.Event{
					Kind: app.EventProjectionResync, SessionID: event.Snapshot.SelectedSessionID,
					State: "snapshot", RuntimeProjection: &projection,
					Data: map[string]string{"wireSequence": fmt.Sprint(event.Snapshot.WireSequence)},
				}, nil
			}
		case desktopclient.EventConnection:
			if event.Connection != nil {
				return app.Event{
					Kind: app.EventKind("connection_state"), State: string(event.Connection.State), Text: event.Connection.Error,
					Data: map[string]string{"daemonEpoch": event.Connection.DaemonEpoch, "wireSequence": fmt.Sprint(event.Connection.WireSequence)},
				}, nil
			}
		}
	}
}

func (runtime *DaemonRuntime) StartConfiguredTurn(request app.TurnRequest) (string, error) {
	var receipt desktop.StartTurnReceipt
	err := runtime.client.Request(context.Background(), desktopipc.MethodStartTurn, desktop.TurnRequest{
		MutationID: uuid.NewString(), SessionID: request.SessionID, Prompt: request.Prompt,
		Provider: request.Provider, Model: request.Model, Reasoning: request.Reasoning,
		AgentMode: request.AgentMode, PlanMode: request.PlanMode, Prewalk: request.Prewalk, PlanYolo: request.PlanYolo,
		VibeMode: request.VibeMode, DisableSubagents: request.DisableSubagents,
		ActiveSkills: append([]string(nil), request.ActiveSkills...), Images: desktopAttachments(request.Images),
	}, &receipt)
	return receipt.RunID, err
}

func (runtime *DaemonRuntime) GuideActiveTurnWithAttachments(sessionID, runID, text string, attachments []session.Attachment) error {
	var receipt desktop.TurnControlReceipt
	return runtime.client.Request(context.Background(), desktopipc.MethodGuide, map[string]any{
		"mutationId": uuid.NewString(), "sessionId": sessionID, "runId": runID, "text": text, "attachments": desktopAttachments(attachments),
	}, &receipt)
}

func (runtime *DaemonRuntime) CancelRunWithChildren(sessionID, runID string, includeChildren bool) (bool, error) {
	var response struct {
		Cancelled bool `json:"cancelled"`
	}
	err := runtime.client.Request(context.Background(), desktopipc.MethodCancelActive, map[string]any{
		"sessionId": sessionID, "runId": runID, "includeChildren": includeChildren,
	}, &response)
	return response.Cancelled, err
}

func (runtime *DaemonRuntime) ExecuteAction(ctx context.Context, action Action) error {
	return runtime.client.Request(ctx, desktopipc.MethodExecute, desktop.ActionRequest{
		Kind: string(action.Kind), Target: action.Target, Decision: action.Decision,
		SessionID: action.SessionID, Name: action.Name, CWD: action.CWD,
		Offset: action.Offset, Limit: action.Limit, Payload: action.Payload,
		Route: action.Route, Provider: action.Provider, Secret: action.Secret,
	}, nil)
}

func (runtime *DaemonRuntime) Request(ctx context.Context, method desktopipc.Method, payload any, target any) error {
	if method == desktopipc.MethodResumeSession {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		var request struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(encoded, &request); err != nil {
			return err
		}
		snapshot, err := runtime.client.SelectSession(ctx, request.SessionID)
		if err != nil {
			return err
		}
		runtime.ApplySnapshot(snapshot)
		output, ok := target.(*desktop.ReconnectSnapshot)
		if !ok || output == nil {
			return fmt.Errorf("resume_session requires a reconnect snapshot target")
		}
		*output = snapshot
		return nil
	}
	return runtime.client.Request(ctx, method, payload, target)
}

func (runtime *DaemonRuntime) ImportImage(sessionID, path string) (session.Attachment, error) {
	path = strings.TrimSpace(path)
	file, err := os.Open(path)
	if err != nil {
		return session.Attachment{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return session.Attachment{}, err
	}
	if !info.Mode().IsRegular() {
		return session.Attachment{}, fmt.Errorf("attachment %q is not a regular file", path)
	}
	if info.Size() > desktopipc.MaxReassembledBinary {
		return session.Attachment{}, fmt.Errorf("attachment exceeds %d bytes", desktopipc.MaxReassembledBinary)
	}
	data, err := io.ReadAll(io.LimitReader(file, desktopipc.MaxReassembledBinary+1))
	if err != nil {
		return session.Attachment{}, err
	}
	mimeType := http.DetectContentType(data)
	return runtime.ImportImageBytes(sessionID, filepath.Base(path), mimeType, data)
}

func (runtime *DaemonRuntime) ImportImageBytes(sessionID, name, mimeType string, data []byte) (session.Attachment, error) {
	attachment, err := runtime.client.UploadAttachment(context.Background(), sessionID, name, mimeType, data)
	if err != nil {
		return session.Attachment{}, err
	}
	return session.Attachment{ID: attachment.ID, Name: attachment.Name, MIME: attachment.MIMEType, Path: attachment.Path, Size: attachment.Size}, nil
}

func (runtime *DaemonRuntime) HasActiveChildren() bool {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	if runtime.latest.Session == nil {
		return false
	}
	for _, agent := range runtime.latest.Session.AgentSnapshots {
		switch agent.State {
		case "completed", "failed", "cancelled", "interrupted":
		default:
			return true
		}
	}
	return false
}

func (runtime *DaemonRuntime) ApprovalModeState() (ApprovalMode, bool) {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	mode := ApprovalMode(runtime.latest.Base.ApprovalMode)
	return mode, runtime.latest.Base.AutoReviewAvailable
}

func (runtime *DaemonRuntime) ActiveShellExecutions() []agentservice.ShellExecutionSnapshot {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	result := make([]agentservice.ShellExecutionSnapshot, 0)
	for _, run := range runtime.latest.Runs {
		for _, operation := range run.ActiveOperations {
			if operation.Name != agentservice.ToolShell {
				continue
			}
			result = append(result, agentservice.ShellExecutionSnapshot{
				SessionID: run.SessionID, RunID: run.RunID, AgentID: operation.AgentID,
				ToolCallID: operation.ToolCallID, StartedAt: operation.StartedAt, State: operation.State,
			})
		}
	}
	return result
}

func (runtime *DaemonRuntime) Detach() error {
	return runtime.client.Close()
}

func (runtime *DaemonRuntime) ApplySnapshot(snapshot desktop.ReconnectSnapshot) {
	runtime.mu.Lock()
	runtime.latest = snapshot
	runtime.mu.Unlock()
}

func (runtime *DaemonRuntime) Snapshot() desktop.ReconnectSnapshot {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.latest
}

func desktopAttachments(items []session.Attachment) []desktop.Attachment {
	result := make([]desktop.Attachment, len(items))
	for index, item := range items {
		result[index] = desktop.Attachment{ID: item.ID, Name: item.Name, MIMEType: item.MIME, Path: item.Path, Size: item.Size}
	}
	return result
}

func appEventFromDesktop(event desktop.Event) app.Event {
	result := app.Event{
		Kind: app.EventKind(event.Kind), SessionID: event.SessionID, RunID: event.RunID,
		AgentID: event.AgentID, ToolCallID: event.ToolCallID, ApprovalID: event.ApprovalID,
		UserInputID: event.UserInputID, PlanID: event.PlanID, Text: event.Text,
		TextPhase: event.TextPhase, State: event.State, Data: event.Data,
		Agent: event.Agent, AgentBlocks: event.AgentBlocks, AgentCatalog: event.AgentCatalog,
		AgentSnapshots: event.AgentSnapshots, SkillCatalog: event.SkillCatalog, SkillDiagnostics: event.SkillDiagnostics,
		PluginCatalog: event.PluginCatalog, PluginDiagnostics: event.PluginDiagnostics,
		MarketplaceCatalog: event.MarketplaceCatalog, HookCatalog: event.HookCatalog,
		ContextProfile: event.ContextProfile,
		ModelRoutes:    event.ModelRoutes, ModelProviders: event.ModelProviders,
		GitBranches: event.GitBranches, UsageReport: event.UsageReport,
		WorkspaceDirty: event.WorkspaceDirty, SecurityConfig: event.SecurityConfig, Security: event.Security,
		SessionProjection: event.SessionProjection, RunProjection: event.RunProjection,
		PromptQueue: event.PromptQueue, At: event.At,
		SecurityFinding: event.SecurityFinding, SecurityPatch: event.SecurityPatch,
	}
	copyJSONValue(event.Todo, &result.Todo)
	copyJSONValue(event.Memories, &result.Memories)
	copyJSONValue(event.Recap, &result.Recap)
	copyJSONValue(event.Background, &result.Background)
	copyJSONValue(event.BackgroundLogs, &result.BackgroundLogs)
	return result
}

func copyJSONValue(source, target any) {
	if source == nil {
		return
	}
	encoded, err := json.Marshal(source)
	if err == nil {
		_ = json.Unmarshal(encoded, target)
	}
}
