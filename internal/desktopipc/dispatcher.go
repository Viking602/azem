package desktopipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/githubpr"
)

type RequestDispatcher interface {
	Dispatch(Method, json.RawMessage) (any, error)
	ImportAttachmentBytes(sessionID, name, mimeType string, data []byte) (desktop.Attachment, error)
}

type Dispatcher struct {
	bridge *desktop.Bridge
}

func NewDispatcher(bridge *desktop.Bridge) (*Dispatcher, error) {
	if bridge == nil {
		return nil, errors.New("IPC dispatcher requires a desktop bridge")
	}
	return &Dispatcher{bridge: bridge}, nil
}

func (dispatcher *Dispatcher) Dispatch(method Method, payload json.RawMessage) (any, error) {
	switch method {
	case MethodInitialise:
		return dispatcher.bridge.Initialise(), nil
	case MethodReconnectSnapshot:
		params, err := decodeParams[reconnectParams](payload)
		if err != nil {
			return nil, err
		}
		snapshot, err := dispatcher.bridge.ReconnectSnapshot(params.SessionID)
		if err == nil && params.Refresh {
			dispatcher.bridge.RefreshProjection()
		}
		return snapshot, err
	case MethodStartTurn:
		params, err := decodeParams[desktop.TurnRequest](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.StartTurn(params)
	case MethodGuide, MethodFollowUp:
		params, err := decodeParams[messageParams](payload)
		if err != nil {
			return nil, err
		}
		if method == MethodGuide {
			return nil, dispatcher.bridge.Guide(params.SessionID, params.RunID, params.Text, params.Attachments)
		}
		return nil, dispatcher.bridge.FollowUp(params.SessionID, params.RunID, params.Text, params.Attachments)
	case MethodCancelActive:
		params, err := decodeParams[cancelParams](payload)
		if err != nil {
			return nil, err
		}
		return map[string]bool{"cancelled": dispatcher.bridge.CancelActive(params.IncludeChildren)}, nil
	case MethodExecute:
		params, err := decodeParams[desktop.ActionRequest](payload)
		if err != nil {
			return nil, err
		}
		return nil, dispatcher.bridge.Execute(params)
	case MethodImportAttachment:
		params, err := decodeParams[importAttachmentParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.ImportAttachment(params.SessionID, params.Name, params.MIMEType, params.Data)
	case MethodImportClipboardImage:
		params, err := decodeParams[sessionParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.ImportClipboardImage(params.SessionID)
	case MethodAttachmentDataURL:
		params, err := decodeParams[attachmentDataParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.AttachmentDataURL(params.SessionID, params.Attachment)
	case MethodSearchSessions:
		params, err := decodeParams[searchParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.SearchSessions(params.Query, params.Limit)
	case MethodResumeSession:
		params, err := decodeParams[sessionParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.ResumeSession(params.SessionID)
	case MethodSessionTree:
		params, err := decodeParams[sessionParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.SessionTree(params.SessionID)
	case MethodNavigateSessionTree:
		params, err := decodeParams[sessionEntryParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.NavigateSessionTree(params.SessionID, params.EntryID)
	case MethodCreateSessionFork:
		params, err := decodeParams[forkParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.CreateSessionFork(params.SessionID, params.TargetID, params.EntryID)
	case MethodSetSessionEntryLabel:
		params, err := decodeParams[labelParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.SetSessionEntryLabel(params.SessionID, params.EntryID, params.Label)
	case MethodExportSession:
		params, err := decodeParams[exportParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.ExportSession(params.SessionID, params.OutputPath, params.Format, params.AllBranches)
	case MethodShareSession:
		params, err := decodeParams[shareParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.ShareSession(params.SessionID, params.ServerURL, params.Store, params.AllBranches)
	case MethodForkSession:
		params, err := decodeParams[forkSessionParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.ForkSession(params.SessionID, params.Activate)
	case MethodPullRequestDashboard:
		return dispatcher.bridge.PullRequestDashboard()
	case MethodPullRequestDetail:
		params, err := decodeParams[numberParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.PullRequestDetail(params.Number)
	case MethodMutatePullRequest:
		params, err := decodeParams[githubpr.MutationRequest](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.MutatePullRequest(params)
	case MethodSetPullRequestMonitor:
		params, err := decodeParams[monitorParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.SetPullRequestMonitor(params.Number, params.Enabled)
	case MethodSkillCatalog:
		return dispatcher.bridge.SkillCatalog()
	case MethodHookCatalog:
		return dispatcher.bridge.HookCatalog()
	case MethodMarketplaceCatalog:
		return dispatcher.bridge.MarketplaceCatalog()
	case MethodUsageReport:
		params, err := decodeParams[scopeParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.UsageReport(params.Scope)
	case MethodWorkspaceChanges:
		return dispatcher.bridge.WorkspaceChanges()
	case MethodWorkspaceChange:
		params, err := decodeParams[pathParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.WorkspaceChange(params.Path)
	case MethodWorkspaceEntries:
		params, err := decodeParams[pathParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.WorkspaceEntries(params.Path)
	case MethodSearchWorkspaceFiles:
		params, err := decodeParams[searchParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.SearchWorkspaceFiles(params.Query, params.Limit)
	case MethodWorkspaceFile:
		params, err := decodeParams[pathParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.WorkspaceFile(params.Path)
	case MethodCreateProject:
		params, err := decodeParams[createProjectParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.CreateProject(params.Name, params.Location, params.InitialiseGit)
	case MethodOpenProject:
		params, err := decodeParams[pathParams](payload)
		if err != nil {
			return nil, err
		}
		return nil, dispatcher.bridge.OpenProject(params.Path)
	case MethodOpenProjectSession:
		params, err := decodeParams[openProjectSessionParams](payload)
		if err != nil {
			return nil, err
		}
		return nil, dispatcher.bridge.OpenProjectSession(params.Path, params.SessionID, params.Sequence)
	case MethodSystemFonts:
		params, err := decodeParams[languageParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.SystemFonts(params.Language), nil
	case MethodListTerminals:
		return dispatcher.bridge.ListTerminals(), nil
	case MethodCreateTerminal:
		params, err := decodeParams[terminalSizeParams](payload)
		if err != nil {
			return nil, err
		}
		return dispatcher.bridge.CreateTerminal(params.Cols, params.Rows)
	case MethodWriteTerminal:
		params, err := decodeParams[terminalWriteParams](payload)
		if err != nil {
			return nil, err
		}
		return nil, dispatcher.bridge.WriteTerminal(params.ID, params.Data)
	case MethodResizeTerminal:
		params, err := decodeParams[terminalSizeIDParams](payload)
		if err != nil {
			return nil, err
		}
		return nil, dispatcher.bridge.ResizeTerminal(params.ID, params.Cols, params.Rows)
	case MethodCloseTerminal:
		params, err := decodeParams[idParams](payload)
		if err != nil {
			return nil, err
		}
		return nil, dispatcher.bridge.CloseTerminal(params.ID)
	case MethodOpenTerminal:
		return nil, dispatcher.bridge.OpenTerminal()
	case MethodBeginAttachmentTransfer, MethodCommitAttachment, MethodAbortAttachment:
		return nil, fmt.Errorf("method %q is owned by the IPC transfer manager", method)
	default:
		return nil, fmt.Errorf("unsupported IPC method %q", method)
	}
}

func (dispatcher *Dispatcher) ImportAttachmentBytes(sessionID, name, mimeType string, data []byte) (desktop.Attachment, error) {
	return dispatcher.bridge.ImportAttachmentBytes(sessionID, name, mimeType, data)
}

func decodeParams[T any](payload json.RawMessage) (T, error) {
	var value T
	if len(bytes.TrimSpace(payload)) == 0 {
		payload = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode IPC parameters: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return value, errors.New("decode IPC parameters: trailing data")
	}
	return value, nil
}

type reconnectParams struct {
	SessionID string `json:"sessionId"`
	Refresh   bool   `json:"refresh,omitempty"`
}

type sessionParams struct {
	SessionID string `json:"sessionId"`
}
type messageParams struct {
	SessionID   string               `json:"sessionId"`
	RunID       string               `json:"runId"`
	Text        string               `json:"text"`
	Attachments []desktop.Attachment `json:"attachments"`
}
type cancelParams struct {
	IncludeChildren bool `json:"includeChildren"`
}
type importAttachmentParams struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	MIMEType  string `json:"mimeType"`
	Data      string `json:"data"`
}
type attachmentDataParams struct {
	SessionID  string             `json:"sessionId"`
	Attachment desktop.Attachment `json:"attachment"`
}
type searchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}
type sessionEntryParams struct {
	SessionID string `json:"sessionId"`
	EntryID   string `json:"entryId"`
}
type forkParams struct {
	SessionID string `json:"sessionId"`
	TargetID  string `json:"targetId"`
	EntryID   string `json:"entryId"`
}
type labelParams struct {
	SessionID string `json:"sessionId"`
	EntryID   string `json:"entryId"`
	Label     string `json:"label"`
}
type exportParams struct {
	SessionID   string `json:"sessionId"`
	OutputPath  string `json:"outputPath"`
	Format      string `json:"format"`
	AllBranches bool   `json:"allBranches"`
}
type shareParams struct {
	SessionID   string `json:"sessionId"`
	ServerURL   string `json:"serverUrl"`
	Store       string `json:"store"`
	AllBranches bool   `json:"allBranches"`
}
type forkSessionParams struct {
	SessionID string `json:"sessionId"`
	Activate  bool   `json:"activate"`
}
type numberParams struct {
	Number int `json:"number"`
}
type monitorParams struct {
	Number  int  `json:"number"`
	Enabled bool `json:"enabled"`
}
type scopeParams struct {
	Scope string `json:"scope"`
}
type pathParams struct {
	Path string `json:"path"`
}
type createProjectParams struct {
	Name          string `json:"name"`
	Location      string `json:"location"`
	InitialiseGit bool   `json:"initialiseGit"`
}
type openProjectSessionParams struct {
	Path      string `json:"path"`
	SessionID string `json:"sessionId"`
	Sequence  int64  `json:"sequence"`
}
type languageParams struct {
	Language string `json:"language"`
}
type terminalSizeParams struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}
type terminalWriteParams struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}
type terminalSizeIDParams struct {
	ID   string `json:"id"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}
type idParams struct {
	ID string `json:"id"`
}
