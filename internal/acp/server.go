package acp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

const (
	ProtocolVersion = 2
	maxFrameBytes   = 8 << 20
	sessionPageSize = 50
)

type Options struct {
	Service   *app.Service
	Sessions  *session.Service
	SessionID string
	Workspace string
	Input     io.Reader
	Output    io.Writer
	Version   string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type clientRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type managedSession struct {
	mode      string
	activeRun string
	promptID  json.RawMessage
}

type permissionRequest struct {
	kind       string
	approvalID string
	planID     string
	sessionID  string
}
type elicitationRequest struct {
	sessionID   string
	userInputID string
	questions   []acpQuestion
}

type acpQuestion struct {
	ID            string      `json:"id"`
	Header        string      `json:"header"`
	Question      string      `json:"question"`
	Options       []acpOption `json:"options"`
	AllowMultiple bool        `json:"allow_multiple,omitempty"`
}

type acpOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended,omitempty"`
}

type terminalRun struct {
	kind      app.EventKind
	sessionID string
	text      string
}

type Server struct {
	service   *app.Service
	sessions  *session.Service
	workspace string
	version   string
	input     io.Reader
	writer    *writer

	mu            sync.Mutex
	initialized   bool
	managed       map[string]*managedSession
	permissions   map[string]permissionRequest
	elicitations  map[string]elicitationRequest
	terminal      map[string]terminalRun
	permissionIDs atomic.Uint64
	ctx           context.Context
	cancel        context.CancelFunc
}

func New(options Options) (*Server, error) {
	if options.Service == nil || options.Sessions == nil || options.Input == nil || options.Output == nil || strings.TrimSpace(options.SessionID) == "" {
		return nil, errors.New("ACP server requires service, sessions, session id, input, and output")
	}
	workspace, err := filepath.Abs(options.Workspace)
	if err != nil {
		return nil, err
	}
	if options.Version == "" {
		options.Version = "dev"
	}
	return &Server{
		service: options.Service, sessions: options.Sessions, workspace: filepath.Clean(workspace), version: options.Version,
		input: options.Input, writer: newWriter(options.Output), managed: map[string]*managedSession{options.SessionID: {mode: "default"}},
		permissions: make(map[string]permissionRequest), elicitations: make(map[string]elicitationRequest), terminal: make(map[string]terminalRun),
	}, nil
}

func (server *Server) Serve(ctx context.Context) error {
	server.ctx, server.cancel = context.WithCancel(ctx)
	defer server.cancel()
	eventDone := make(chan error, 1)
	go func() { eventDone <- server.forwardEvents() }()
	scanner := bufio.NewScanner(server.input)
	scanner.Buffer(make([]byte, 64<<10), maxFrameBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var incoming request
		if err := json.Unmarshal([]byte(line), &incoming); err != nil {
			_ = server.writer.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &responseError{Code: -32700, Message: "Parse error"}})
			continue
		}
		if incoming.Method == "" && len(incoming.ID) > 0 {
			server.handleClientResponse(incoming)
			continue
		}
		if incoming.JSONRPC != "2.0" || incoming.Method == "" {
			_ = server.writer.write(response{JSONRPC: "2.0", ID: responseID(incoming.ID), Error: &responseError{Code: -32600, Message: "Invalid Request"}})
			continue
		}
		result, deferred, rpcErr := server.handle(incoming)
		if len(incoming.ID) == 0 || deferred {
			continue
		}
		out := response{JSONRPC: "2.0", ID: responseID(incoming.ID), Result: result, Error: rpcErr}
		if err := server.writer.write(out); err != nil {
			server.cancel()
			break
		}
		if (incoming.Method == "session/resume" || incoming.Method == "session/load") && rpcErr == nil {
			var params sessionIDParams
			_ = json.Unmarshal(incoming.Params, &params)
			go server.replaySession(params.SessionID)
		}
	}
	if err := scanner.Err(); err != nil {
		server.cancel()
		return fmt.Errorf("read ACP frame: %w", err)
	}
	server.cancelAll()
	server.cancel()
	select {
	case err := <-eventDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	case <-time.After(time.Second):
	}
	return server.writer.err()
}

func (server *Server) handle(incoming request) (result any, deferred bool, rpcErr *responseError) {
	if incoming.Method != "initialize" {
		server.mu.Lock()
		initialized := server.initialized
		server.mu.Unlock()
		if !initialized {
			return nil, false, rpcError(-32002, "Agent is not initialized")
		}
	}
	switch incoming.Method {
	case "initialize":
		var params struct {
			ProtocolVersion int `json:"protocolVersion"`
		}
		if json.Unmarshal(incoming.Params, &params) != nil || params.ProtocolVersion != ProtocolVersion {
			return nil, false, rpcError(-32602, fmt.Sprintf("unsupported protocol version %d", params.ProtocolVersion))
		}
		server.mu.Lock()
		server.initialized = true
		server.mu.Unlock()
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"session": map[string]any{"prompt": map[string]any{"image": map[string]any{}}, "list": map[string]any{}, "resume": map[string]any{}, "close": map[string]any{}}},
			"info":            map[string]string{"name": "azem", "title": "Azem", "version": server.version}, "authMethods": []any{},
		}, false, nil
	case "session/new":
		var params newSessionParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, false, invalidParams(err)
		}
		if len(params.AdditionalDirectories) > 0 {
			return nil, false, invalidParams(errors.New("additionalDirectories are not supported by this scoped ACP process"))
		}
		if err := server.requireWorkspace(params.CWD); err != nil {
			return nil, false, invalidParams(err)
		}
		id, err := randomID("session")
		if err != nil {
			return nil, false, internalError(err)
		}
		if err := server.service.ExecuteAction(server.ctx, app.Action{Kind: app.ActionNewSession, Target: id}); err != nil {
			return nil, false, internalError(err)
		}
		server.mu.Lock()
		server.managed[id] = &managedSession{mode: "default"}
		server.mu.Unlock()
		return sessionSetupResult(id), false, nil
	case "session/list":
		var params listSessionParams
		if len(incoming.Params) > 0 && json.Unmarshal(incoming.Params, &params) != nil {
			return nil, false, invalidParams(errors.New("invalid list parameters"))
		}
		result, err := server.listSessions(params)
		if err != nil {
			return nil, false, internalError(err)
		}
		return result, false, nil
	case "session/resume", "session/load":
		var params sessionIDParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, false, invalidParams(err)
		}
		if err := server.requireWorkspace(params.CWD); err != nil {
			return nil, false, invalidParams(err)
		}
		if _, err := server.sessions.LoadSession(server.ctx, params.SessionID); err != nil {
			return nil, false, rpcError(-32001, err.Error())
		}
		server.mu.Lock()
		server.managed[params.SessionID] = &managedSession{mode: "default"}
		server.mu.Unlock()
		return sessionSetupResult(params.SessionID), false, nil
	case "session/close":
		var params sessionIDParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, false, invalidParams(err)
		}
		server.mu.Lock()
		record := server.managed[params.SessionID]
		if record != nil {
			delete(server.managed, params.SessionID)
		}
		server.mu.Unlock()
		if record == nil {
			return nil, false, rpcError(-32001, "unknown session")
		}
		if record.activeRun != "" {
			server.service.CancelActiveWithChildren(true)
		}
		return map[string]any{}, false, nil
	case "session/prompt":
		var params promptParams
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, false, invalidParams(err)
		}
		text, attachments, err := server.decodePrompt(params)
		if err != nil {
			return nil, false, invalidParams(err)
		}
		server.mu.Lock()
		record := server.managed[params.SessionID]
		if record == nil {
			server.mu.Unlock()
			return nil, false, rpcError(-32001, "unknown session")
		}
		if record.activeRun != "" {
			server.mu.Unlock()
			return nil, false, rpcError(-32000, "session already has an active prompt")
		}
		mode := record.mode
		server.mu.Unlock()
		runID, err := server.service.StartConfiguredTurn(app.TurnRequest{SessionID: params.SessionID, Prompt: text, Images: attachments, PlanMode: mode == "plan"})
		if err != nil {
			return nil, false, internalError(err)
		}
		server.mu.Lock()
		if terminal, finished := server.terminal[runID]; finished {
			delete(server.terminal, runID)
			server.mu.Unlock()
			server.finishPrompt(incoming.ID, terminal)
			return nil, true, nil
		}
		record.activeRun, record.promptID = runID, append([]byte(nil), incoming.ID...)
		server.mu.Unlock()
		return nil, true, nil
	case "session/cancel":
		var params sessionIDParams
		if json.Unmarshal(incoming.Params, &params) == nil {
			server.mu.Lock()
			record := server.managed[params.SessionID]
			server.mu.Unlock()
			if record != nil && record.activeRun != "" {
				server.service.CancelActiveWithChildren(true)
			}
		}
		return map[string]any{}, false, nil
	case "session/set_mode":
		var params struct {
			SessionID string `json:"sessionId"`
			ModeID    string `json:"modeId"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil || (params.ModeID != "default" && params.ModeID != "plan") {
			return nil, false, invalidParams(errors.New("modeId must be default or plan"))
		}
		server.mu.Lock()
		record := server.managed[params.SessionID]
		if record != nil {
			record.mode = params.ModeID
		}
		server.mu.Unlock()
		if record == nil {
			return nil, false, rpcError(-32001, "unknown session")
		}
		return map[string]any{}, false, nil
	case "unstable_session/fork":
		var params struct {
			SessionID string `json:"sessionId"`
			EntryID   string `json:"entryId,omitempty"`
		}
		if err := decodeParams(incoming.Params, &params); err != nil {
			return nil, false, invalidParams(err)
		}
		id, err := randomID("session")
		if err != nil {
			return nil, false, internalError(err)
		}
		if params.EntryID == "" {
			err = server.sessions.Fork(server.ctx, params.SessionID, id)
		} else {
			err = server.sessions.ForkAt(server.ctx, params.SessionID, id, params.EntryID)
		}
		if err != nil {
			return nil, false, internalError(err)
		}
		server.mu.Lock()
		server.managed[id] = &managedSession{mode: "default"}
		server.mu.Unlock()
		return map[string]any{"sessionId": id}, false, nil
	case "speech.models.list":
		return map[string]any{"speechToText": map[string]any{"models": []any{}}, "textToSpeech": map[string]any{"models": []any{}}}, false, nil
	default:
		return nil, false, rpcError(-32601, "Method not found")
	}
}

type newSessionParams struct {
	CWD                   string   `json:"cwd"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

type sessionIDParams struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd,omitempty"`
}

type listSessionParams struct {
	CWD    string `json:"cwd,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
}

type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

func (server *Server) decodePrompt(params promptParams) (string, []session.Attachment, error) {
	if strings.TrimSpace(params.SessionID) == "" || len(params.Prompt) == 0 {
		return "", nil, errors.New("sessionId and prompt are required")
	}
	texts := make([]string, 0)
	attachments := make([]session.Attachment, 0)
	for index, block := range params.Prompt {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				texts = append(texts, block.Text)
			}
		case "image":
			payload, err := base64.StdEncoding.DecodeString(block.Data)
			if err != nil || len(payload) == 0 || len(payload) > 64<<20 || !strings.HasPrefix(block.MIMEType, "image/") {
				return "", nil, fmt.Errorf("invalid image content block %d", index)
			}
			attachment, err := server.service.ImportImageBytes(params.SessionID, fmt.Sprintf("acp-image-%d", index+1), block.MIMEType, payload)
			if err != nil {
				return "", nil, err
			}
			attachments = append(attachments, attachment)
		case "resource_link", "embedded_resource":
			if block.URI != "" {
				texts = append(texts, "[Resource: "+block.URI+"]")
			}
		default:
			return "", nil, fmt.Errorf("unsupported prompt content block %q", block.Type)
		}
	}
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	if text == "" && len(attachments) == 0 {
		return "", nil, errors.New("prompt contains no usable content")
	}
	return text, attachments, nil
}

func (server *Server) requireWorkspace(cwd string) error {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	absolute, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	if filepath.Clean(absolute) != server.workspace {
		return fmt.Errorf("this ACP process is scoped to %s", server.workspace)
	}
	return nil
}

func sessionSetupResult(sessionID string) map[string]any {
	return map[string]any{
		"sessionId":     sessionID,
		"modes":         map[string]any{"currentModeId": "default", "availableModes": []map[string]string{{"id": "default", "name": "Default"}, {"id": "plan", "name": "Plan"}}},
		"configOptions": []any{},
	}
}

func (server *Server) listSessions(params listSessionParams) (map[string]any, error) {
	offset := 0
	if params.Cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(params.Cursor)
		if err != nil {
			return nil, errors.New("invalid session cursor")
		}
		offset, err = strconv.Atoi(string(decoded))
		if err != nil || offset < 0 {
			return nil, errors.New("invalid session cursor")
		}
	}
	values, err := server.sessions.List(server.ctx, 500)
	if err != nil {
		return nil, err
	}
	results := make([]map[string]any, 0, sessionPageSize)
	filtered := make([]session.Session, 0, len(values))
	for _, value := range values {
		workspace, _ := server.sessions.SessionWorkspace(server.ctx, value.ID)
		if params.CWD == "" || filepath.Clean(params.CWD) == filepath.Clean(workspace) {
			value.Workspace = workspace
			filtered = append(filtered, value)
		}
	}
	if offset > len(filtered) {
		return nil, errors.New("invalid session cursor")
	}
	end := min(offset+sessionPageSize, len(filtered))
	for _, value := range filtered[offset:end] {
		results = append(results, map[string]any{"sessionId": value.ID, "cwd": value.Workspace, "title": value.Title, "updatedAt": value.UpdatedAt.Format(time.RFC3339)})
	}
	result := map[string]any{"sessions": results}
	if end < len(filtered) {
		result["nextCursor"] = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	return result, nil
}

func (server *Server) replaySession(sessionID string) {
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-server.ctx.Done():
		return
	case <-timer.C:
	}
	projection, err := server.sessions.LoadProjection(server.ctx, sessionID)
	if err != nil {
		return
	}
	for _, block := range projection.Blocks {
		updateType := "agent_message_chunk"
		if block.Kind == "user" {
			updateType = "user_message_chunk"
		} else if block.Kind != "assistant" {
			continue
		}
		_ = server.notifyUpdate(sessionID, map[string]any{"sessionUpdate": updateType, "content": map[string]any{"type": "text", "text": block.Content}})
	}
}

func (server *Server) forwardEvents() error {
	for {
		event, err := server.service.NextEvent(server.ctx)
		if err != nil {
			return err
		}
		if event.SessionID == "" {
			continue
		}
		switch event.Kind {
		case app.EventTextDelta:
			if err := server.notifyUpdate(event.SessionID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": event.Text}}); err != nil {
				return err
			}
		case app.EventThinkingDelta:
			if err := server.notifyUpdate(event.SessionID, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": event.Text}}); err != nil {
				return err
			}
		case app.EventToolStarted:
			update := map[string]any{"sessionUpdate": "tool_call", "toolCallId": event.ToolCallID, "title": firstNonempty(event.Data["name"], "Tool call"), "kind": toolKind(event.Data["name"]), "status": "in_progress"}
			if raw := event.Data["arguments"]; json.Valid([]byte(raw)) {
				update["rawInput"] = json.RawMessage(raw)
			}
			if err := server.notifyUpdate(event.SessionID, update); err != nil {
				return err
			}
		case app.EventToolUpdate, app.EventToolFinished:
			status := "in_progress"
			if event.Kind == app.EventToolFinished {
				status = event.State
				if status != "failed" {
					status = "completed"
				}
			}
			update := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": event.ToolCallID, "status": status}
			if event.Text != "" {
				update["content"] = []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": event.Text}}}
			}
			if err := server.notifyUpdate(event.SessionID, update); err != nil {
				return err
			}
		case app.EventTodoUpdated:
			if event.Todo != nil {
				entries := make([]map[string]string, 0)
				for _, phase := range event.Todo.Phases {
					for _, item := range phase.Items {
						entries = append(entries, map[string]string{"content": item.Content, "priority": "medium", "status": string(item.Status)})
					}
				}
				_ = server.notifyUpdate(event.SessionID, map[string]any{"sessionUpdate": "plan_update", "plan": map[string]any{"type": "items", "planId": "session-todo", "entries": entries}})
			}
		case app.EventApprovalRequested:
			server.requestPermission(event)
		case app.EventUserInputRequested:
			server.requestElicitation(event)
		case app.EventPlanProposed:
			server.requestPlanReview(event)
		case app.EventRunFinished, app.EventRunFailed, app.EventRunCancelled:
			server.completeRun(event)
		}
	}
}

func (server *Server) completeRun(event app.Event) {
	server.mu.Lock()
	record := server.managed[event.SessionID]
	if record == nil || record.activeRun != event.RunID || len(record.promptID) == 0 {
		server.terminal[event.RunID] = terminalRun{kind: event.Kind, sessionID: event.SessionID, text: event.Text}
		server.mu.Unlock()
		return
	}
	id := append([]byte(nil), record.promptID...)
	record.activeRun, record.promptID = "", nil
	server.mu.Unlock()
	server.finishPrompt(id, terminalRun{kind: event.Kind, sessionID: event.SessionID, text: event.Text})
}

func (server *Server) finishPrompt(id json.RawMessage, terminal terminalRun) {
	stopReason := "end_turn"
	if terminal.kind == app.EventRunCancelled {
		stopReason = "cancelled"
	}
	if terminal.kind == app.EventRunFailed && terminal.text != "" {
		_ = server.notifyUpdate(terminal.sessionID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": terminal.text}})
	}
	_ = server.writer.write(response{JSONRPC: "2.0", ID: responseID(id), Result: map[string]string{"stopReason": stopReason}})
}

func (server *Server) requestPermission(event app.Event) {
	id := fmt.Sprintf("permission-%d", server.permissionIDs.Add(1))
	server.mu.Lock()
	server.permissions[id] = permissionRequest{kind: "approval", approvalID: event.ApprovalID, sessionID: event.SessionID}
	server.mu.Unlock()
	_ = server.writer.write(clientRequest{
		JSONRPC: "2.0", ID: id, Method: "session/request_permission",
		Params: map[string]any{
			"sessionId": event.SessionID, "title": firstNonempty(event.Text, "Approve tool call?"),
			"subject": map[string]any{"type": "tool_call", "toolCall": map[string]any{"toolCallId": event.ToolCallID, "title": firstNonempty(event.Data["name"], "Tool call")}},
			"options": []map[string]string{{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"}, {"optionId": "reject-once", "name": "Reject", "kind": "reject_once"}},
		},
	})
}

func (server *Server) requestPlanReview(event app.Event) {
	id := fmt.Sprintf("plan-review-%d", server.permissionIDs.Add(1))
	server.mu.Lock()
	server.permissions[id] = permissionRequest{kind: "plan", planID: event.PlanID, sessionID: event.SessionID}
	server.mu.Unlock()
	_ = server.writer.write(clientRequest{
		JSONRPC: "2.0", ID: id, Method: "session/request_permission",
		Params: map[string]any{
			"sessionId": event.SessionID, "title": firstNonempty(event.Data["title"], "Execute this plan?"), "description": event.Text,
			"subject": map[string]any{"type": "tool_call", "toolCall": map[string]any{"toolCallId": event.ToolCallID, "title": "Execute approved plan", "kind": "think", "status": "pending"}},
			"options": []map[string]string{{"optionId": "allow-once", "name": "Approve and execute", "kind": "allow_once"}, {"optionId": "reject-once", "name": "Keep for revision", "kind": "reject_once"}},
		},
	})
}

func (server *Server) requestElicitation(event app.Event) {
	var questions []acpQuestion
	if json.Unmarshal([]byte(event.Data["questions"]), &questions) != nil || len(questions) == 0 {
		server.service.CancelActiveWithChildren(true)
		return
	}
	id := fmt.Sprintf("elicitation-%d", server.permissionIDs.Add(1))
	properties := make(map[string]any, len(questions))
	required := make([]string, 0, len(questions))
	for _, question := range questions {
		values := make([]string, 0, len(question.Options))
		for _, option := range question.Options {
			values = append(values, option.Label)
		}
		property := map[string]any{"title": firstNonempty(question.Header, question.Question), "description": question.Question}
		if question.AllowMultiple {
			property["type"] = "array"
			property["items"] = map[string]any{"type": "string", "enum": values}
			property["uniqueItems"] = true
		} else {
			property["type"] = "string"
			property["enum"] = values
		}
		properties[question.ID] = property
		required = append(required, question.ID)
	}
	server.mu.Lock()
	server.elicitations[id] = elicitationRequest{sessionID: event.SessionID, userInputID: event.UserInputID, questions: questions}
	server.mu.Unlock()
	_ = server.writer.write(clientRequest{
		JSONRPC: "2.0", ID: id, Method: "elicitation/create",
		Params: map[string]any{
			"sessionId": event.SessionID, "toolCallId": event.ToolCallID, "mode": "form",
			"message": "The agent needs your input.", "requestedSchema": map[string]any{"type": "object", "properties": properties, "required": required},
		},
	})
}

func (server *Server) handleClientResponse(incoming request) {
	id := rawIDString(incoming.ID)
	server.mu.Lock()
	pendingPermission, permissionOK := server.permissions[id]
	if permissionOK {
		delete(server.permissions, id)
	}
	pendingElicitation, elicitationOK := server.elicitations[id]
	if elicitationOK {
		delete(server.elicitations, id)
	}
	server.mu.Unlock()
	if permissionOK {
		decision := "deny"
		var result struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		if incoming.Error == nil && json.Unmarshal(incoming.Result, &result) == nil && result.Outcome.Outcome == "selected" && result.Outcome.OptionID == "allow-once" {
			decision = "once"
		}
		if pendingPermission.kind == "plan" {
			if decision == "once" {
				_ = server.service.ExecuteAction(server.ctx, app.Action{Kind: app.ActionResolvePlan, Target: pendingPermission.planID, Decision: "execute", SessionID: pendingPermission.sessionID})
			}
		} else {
			_ = server.service.ExecuteAction(server.ctx, app.Action{Kind: app.ActionResolveApproval, Target: pendingPermission.approvalID, Decision: decision, SessionID: pendingPermission.sessionID})
		}
		return
	}
	if !elicitationOK {
		return
	}
	var result struct {
		Action  string         `json:"action"`
		Content map[string]any `json:"content"`
	}
	if incoming.Error != nil || json.Unmarshal(incoming.Result, &result) != nil || result.Action != "accept" {
		server.service.CancelActiveWithChildren(true)
		return
	}
	answers := make([]map[string]any, 0, len(pendingElicitation.questions))
	for _, question := range pendingElicitation.questions {
		selected := make([]string, 0)
		switch value := result.Content[question.ID].(type) {
		case string:
			if value != "" {
				selected = append(selected, value)
			}
		case []any:
			for _, item := range value {
				if text, ok := item.(string); ok && text != "" {
					selected = append(selected, text)
				}
			}
		}
		answers = append(answers, map[string]any{"question_id": question.ID, "selected": selected})
	}
	payload, _ := json.Marshal(map[string]any{"answers": answers})
	_ = server.service.ExecuteAction(server.ctx, app.Action{Kind: app.ActionResolveUserInput, SessionID: pendingElicitation.sessionID, Target: pendingElicitation.userInputID, Payload: payload})
}

func (server *Server) cancelAll() {
	server.service.CancelActiveWithChildren(true)
	server.mu.Lock()
	pending := server.permissions
	server.permissions = make(map[string]permissionRequest)
	server.elicitations = make(map[string]elicitationRequest)
	server.mu.Unlock()
	for _, value := range pending {
		if value.kind == "approval" {
			_ = server.service.ExecuteAction(context.Background(), app.Action{Kind: app.ActionResolveApproval, Target: value.approvalID, Decision: "deny", SessionID: value.sessionID})
		}
	}
}

func (server *Server) notifyUpdate(sessionID string, update map[string]any) error {
	if sessionID == "" {
		server.mu.Lock()
		for id, record := range server.managed {
			if record.activeRun != "" {
				sessionID = id
				break
			}
		}
		server.mu.Unlock()
	}
	if sessionID == "" {
		return nil
	}
	return server.writer.write(notification{JSONRPC: "2.0", Method: "session/update", Params: map[string]any{"sessionId": sessionID, "update": update}})
}

func toolKind(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "read"):
		return "read"
	case strings.Contains(name, "write"), strings.Contains(name, "edit"):
		return "edit"
	case strings.Contains(name, "delete"):
		return "delete"
	case strings.Contains(name, "move"):
		return "move"
	case strings.Contains(name, "grep"), strings.Contains(name, "glob"), strings.Contains(name, "search"):
		return "search"
	case strings.Contains(name, "shell"), strings.Contains(name, "bash"), strings.Contains(name, "exec"), strings.Contains(name, "eval"):
		return "execute"
	default:
		return "other"
	}
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || json.Unmarshal(raw, target) != nil {
		return errors.New("invalid parameters")
	}
	return nil
}

func responseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return append([]byte(nil), id...)
}

func rawIDString(id json.RawMessage) string {
	var text string
	if json.Unmarshal(id, &text) == nil {
		return text
	}
	return string(id)
}

func rpcError(code int, message string) *responseError {
	return &responseError{Code: code, Message: message}
}
func invalidParams(err error) *responseError { return rpcError(-32602, err.Error()) }
func internalError(err error) *responseError { return rpcError(-32000, err.Error()) }

func randomID(prefix string) (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type writer struct {
	mu       sync.Mutex
	out      *bufio.Writer
	writeErr error
}

func newWriter(output io.Writer) *writer { return &writer{out: bufio.NewWriter(output)} }

func (writer *writer) write(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded)+1 > maxFrameBytes {
		return errors.New("ACP frame exceeds transport limit")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.writeErr != nil {
		return writer.writeErr
	}
	if _, err := writer.out.Write(encoded); err != nil {
		writer.writeErr = err
		return err
	}
	if err := writer.out.WriteByte('\n'); err != nil {
		writer.writeErr = err
		return err
	}
	if err := writer.out.Flush(); err != nil {
		writer.writeErr = err
		return err
	}
	return nil
}

func (writer *writer) err() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writeErr
}
