package rpc

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/headless"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/sessionexport"
)

const (
	ProtocolVersion          = 1
	LosslessProtocolVersion  = 2
	MaxFrameBytes            = 1 << 20
	MaxReassembledFrameBytes = 64 << 20
	chunkPayloadBytes        = 700 << 10
	maxConcurrentCommands    = 32
)

type Options struct {
	Service   *app.Service
	Sessions  *session.Service
	SessionID string
	Input     io.Reader
	Output    io.Writer
}

type Server struct {
	service  *app.Service
	sessions *session.Service
	exporter *sessionexport.Exporter
	input    io.Reader
	writer   *frameWriter

	mu        sync.RWMutex
	sessionID string
	activeRun string
	runDone   map[string]chan struct{}
	completed map[string]struct{}
	claimed   map[string]struct{}
	cursorKey []byte
	sequence  atomic.Int64
}

type ReadyFrame struct {
	Type                      string `json:"type"`
	ProtocolVersion           int    `json:"protocolVersion"`
	SupportedProtocolVersions []int  `json:"supportedProtocolVersions"`
	MaxFrameBytes             int    `json:"maxFrameBytes"`
	MaxReassembledFrameBytes  int    `json:"maxReassembledFrameBytes"`
}

type Response struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

type Command struct {
	ID                string              `json:"id,omitempty"`
	Type              string              `json:"type"`
	ProtocolVersion   int                 `json:"protocolVersion,omitempty"`
	Message           string              `json:"message,omitempty"`
	StreamingBehavior string              `json:"streamingBehavior,omitempty"`
	RunID             string              `json:"runId,omitempty"`
	Provider          string              `json:"provider,omitempty"`
	ModelID           string              `json:"modelId,omitempty"`
	Level             string              `json:"level,omitempty"`
	Mode              string              `json:"mode,omitempty"`
	Enabled           *bool               `json:"enabled,omitempty"`
	EntryID           string              `json:"entryId,omitempty"`
	Name              string              `json:"name,omitempty"`
	OutputPath        string              `json:"outputPath,omitempty"`
	Cursor            string              `json:"cursor,omitempty"`
	Limit             int                 `json:"limit,omitempty"`
	Goal              string              `json:"goal,omitempty"`
	Revision          int64               `json:"revision,omitempty"`
	Phases            []session.TodoPhase `json:"phases,omitempty"`
	ApprovalID        string              `json:"approvalId,omitempty"`
	Decision          string              `json:"decision,omitempty"`
	UserInputID       string              `json:"userInputId,omitempty"`
	PlanID            string              `json:"planId,omitempty"`
	Payload           json.RawMessage     `json:"payload,omitempty"`
}

func New(options Options) (*Server, error) {
	if options.Service == nil || options.Sessions == nil || options.Input == nil || options.Output == nil || strings.TrimSpace(options.SessionID) == "" {
		return nil, errors.New("RPC server requires service, sessions, session id, input, and output")
	}
	cursorKey := make([]byte, 32)
	if _, err := rand.Read(cursorKey); err != nil {
		return nil, err
	}
	return &Server{
		service: options.Service, sessions: options.Sessions, exporter: sessionexport.New(options.Sessions), input: options.Input,
		writer: newFrameWriter(options.Output), sessionID: options.SessionID, runDone: make(map[string]chan struct{}), completed: make(map[string]struct{}), claimed: make(map[string]struct{}), cursorKey: cursorKey,
	}, nil
}

func (server *Server) Serve(ctx context.Context) error {
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready := ReadyFrame{Type: "ready", ProtocolVersion: ProtocolVersion, SupportedProtocolVersions: []int{1, 2}, MaxFrameBytes: MaxFrameBytes, MaxReassembledFrameBytes: MaxReassembledFrameBytes}
	if err := server.writer.write(ready); err != nil {
		return err
	}
	eventDone := make(chan error, 1)
	go func() { eventDone <- server.forwardEvents(serveCtx, cancel) }()
	scanner := bufio.NewScanner(server.input)
	scanner.Buffer(make([]byte, 64<<10), MaxFrameBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var command Command
		if err := json.Unmarshal([]byte(line), &command); err != nil || strings.TrimSpace(command.Type) == "" {
			if err := server.writer.write(Response{Type: "response", Command: "parse", Success: false, Error: "invalid JSON command"}); err != nil {
				cancel()
				break
			}
			continue
		}
		response := server.dispatch(serveCtx, command)
		if err := server.writer.write(response); err != nil {
			cancel()
			break
		}
	}
	if err := scanner.Err(); err != nil {
		cancel()
		return fmt.Errorf("read RPC command: %w", err)
	}
	cancel()
	select {
	case err := <-eventDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	case <-time.After(time.Second):
	}
	return server.writer.err()
}

func (server *Server) forwardEvents(ctx context.Context, cancel context.CancelFunc) error {
	for {
		event, err := server.service.NextEvent(ctx)
		if err != nil {
			return err
		}
		sequence := server.sequence.Add(1)
		if event.RunID != "" {
			server.mu.Lock()
			if event.Kind == app.EventRunStarted {
				server.activeRun = event.RunID
				if server.runDone[event.RunID] == nil {
					server.runDone[event.RunID] = make(chan struct{})
				}
			}
			if event.Kind == app.EventRunFinished || event.Kind == app.EventRunFailed || event.Kind == app.EventRunCancelled {
				if server.activeRun == event.RunID {
					server.activeRun = ""
				}
				if done := server.runDone[event.RunID]; done != nil {
					close(done)
					delete(server.runDone, event.RunID)
				}
				if _, claimed := server.claimed[event.RunID]; !claimed {
					server.completed[event.RunID] = struct{}{}
				}
				delete(server.claimed, event.RunID)
			}
			server.mu.Unlock()
		}
		if err := server.writer.write(headless.FrameForEvent(sequence, event)); err != nil {
			cancel()
			return err
		}
	}
}

func (server *Server) dispatch(ctx context.Context, command Command) Response {
	response, recognized := server.handle(ctx, command)
	if !recognized {
		return Response{Type: "response", Command: command.Type, Success: false, Error: "unknown command"}
	}
	response.ID = command.ID
	response.Type = "response"
	response.Command = command.Type
	return response
}

func success(data any) Response  { return Response{Success: true, Data: data} }
func failure(err error) Response { return Response{Success: false, Error: err.Error()} }

func (server *Server) handle(ctx context.Context, command Command) (Response, bool) {
	switch command.Type {
	case "negotiate_protocol":
		if command.ProtocolVersion != LosslessProtocolVersion {
			return failure(fmt.Errorf("unsupported protocol version %d", command.ProtocolVersion)), true
		}
		server.writer.setProtocol(LosslessProtocolVersion)
		return success(map[string]any{"protocolVersion": LosslessProtocolVersion}), true
	case "prompt":
		runID, err := server.startPrompt(command.Message)
		if err != nil {
			return failure(err), true
		}
		return success(map[string]any{"agentInvoked": true, "runId": runID}), true
	case "steer", "follow_up":
		runID := firstNonempty(command.RunID, server.currentRun())
		if runID == "" {
			return failure(errors.New("no active run")), true
		}
		var err error
		if command.Type == "steer" {
			err = server.service.GuideActiveTurnWithAttachments(server.currentSession(), runID, command.Message, nil)
		} else {
			err = server.service.FollowUpActiveTurn(server.currentSession(), runID, command.Message)
		}
		if err != nil {
			return failure(err), true
		}
		return success(nil), true
	case "abort":
		return success(map[string]bool{"cancelled": server.service.CancelActiveWithChildren(true)}), true
	case "abort_and_prompt":
		oldRun := server.currentRun()
		server.service.CancelActiveWithChildren(true)
		if oldRun != "" {
			if err := server.waitRun(ctx, oldRun); err != nil {
				return failure(err), true
			}
		}
		runID, err := server.startPrompt(command.Message)
		if err != nil {
			return failure(err), true
		}
		return success(map[string]string{"runId": runID}), true
	case "get_state":
		state, err := server.state(ctx)
		if err != nil {
			return failure(err), true
		}
		return success(state), true
	case "get_messages":
		projection, err := server.sessions.LoadProjection(ctx, server.currentSession())
		if err != nil {
			return failure(err), true
		}
		return success(map[string]any{"messages": projection.Blocks}), true
	case "get_messages_page":
		page, err := server.messagesPage(ctx, command.Cursor, command.Limit)
		if err != nil {
			result := failure(err)
			if errors.Is(err, errStaleCursor) {
				result.Code = "stale_cursor"
			} else if errors.Is(err, errSessionBusy) {
				result.Code = "session_busy"
			}
			return result, true
		}
		return success(page), true
	case "get_branch_messages":
		entries, err := server.sessions.LoadSessionBranch(ctx, server.currentSession(), "")
		if err != nil {
			return failure(err), true
		}
		projection, err := server.sessions.LoadExportSnapshot(ctx, server.currentSession())
		if err != nil {
			return failure(err), true
		}
		bySequence := make(map[int64]session.Block, len(projection.AllBlocks))
		for _, block := range projection.AllBlocks {
			bySequence[block.Sequence] = block
		}
		messages := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			messages = append(messages, map[string]any{"entryId": entry.ID, "text": bySequence[entry.Sequence].Content, "kind": entry.Kind})
		}
		return success(map[string]any{"messages": messages}), true
	case "branch":
		navigation, err := server.sessions.NavigateSessionTree(ctx, server.currentSession(), command.EntryID)
		if err != nil {
			return failure(err), true
		}
		return success(navigation), true
	case "get_last_assistant_text":
		projection, err := server.sessions.LoadProjection(ctx, server.currentSession())
		if err != nil {
			return failure(err), true
		}
		var text *string
		for index := len(projection.Blocks) - 1; index >= 0; index-- {
			if projection.Blocks[index].Kind == "assistant" {
				value := projection.Blocks[index].Content
				text = &value
				break
			}
		}
		return success(map[string]any{"text": text}), true
	case "set_session_name":
		if err := server.sessions.Rename(ctx, server.currentSession(), command.Name); err != nil {
			return failure(err), true
		}
		return success(nil), true
	case "get_session_stats":
		snapshot, err := server.sessions.LoadExportSnapshot(ctx, server.currentSession())
		if err != nil {
			return failure(err), true
		}
		return success(map[string]any{"sessionId": snapshot.Session.ID, "messageCount": len(snapshot.ActiveBlocks), "totalEntries": len(snapshot.AllBlocks), "branchCount": len(snapshot.Tree.Branches), "createdAt": snapshot.Session.CreatedAt, "updatedAt": snapshot.Session.UpdatedAt}), true
	case "export_html":
		outputPath := strings.TrimSpace(command.OutputPath)
		if outputPath == "" {
			outputPath = filepath.Join(".", "azem-session-"+server.currentSession()+".html")
		}
		resolved, err := server.exporter.ExportFile(ctx, outputPath, server.currentSession(), sessionexport.FormatHTML, sessionexport.Options{})
		if err != nil {
			return failure(err), true
		}
		return success(map[string]string{"path": resolved}), true
	case "set_model":
		if err := server.updatePreferences(ctx, command.Provider, command.ModelID, ""); err != nil {
			return failure(err), true
		}
		return success(map[string]string{"provider": command.Provider, "modelId": command.ModelID}), true
	case "set_thinking_level":
		if err := server.updatePreferences(ctx, "", "", command.Level); err != nil {
			return failure(err), true
		}
		return success(nil), true
	case "set_todos":
		current, err := server.sessions.LoadTodo(ctx, server.currentSession())
		if err != nil {
			return failure(err), true
		}
		updated, err := server.sessions.UpdateTodo(ctx, server.currentSession(), current.Revision, func(todo *session.TodoList) error {
			todo.Goal = command.Goal
			todo.Phases = command.Phases
			return nil
		})
		if err != nil {
			return failure(err), true
		}
		return success(map[string]any{"todo": updated}), true
	case "compact":
		if err := server.service.ExecuteAction(ctx, app.Action{Kind: app.ActionCompact, Target: server.currentSession()}); err != nil {
			return failure(err), true
		}
		return success(nil), true
	case "resolve_approval":
		err := server.service.ExecuteAction(ctx, app.Action{Kind: app.ActionResolveApproval, Target: command.ApprovalID, Decision: command.Decision})
		if err != nil {
			return failure(err), true
		}
		return success(nil), true
	case "resolve_user_input":
		err := server.service.ExecuteAction(ctx, app.Action{Kind: app.ActionResolveUserInput, SessionID: server.currentSession(), Target: command.UserInputID, Payload: command.Payload})
		if err != nil {
			return failure(err), true
		}
		return success(nil), true
	case "resolve_plan":
		err := server.service.ExecuteAction(ctx, app.Action{Kind: app.ActionResolvePlan, SessionID: server.currentSession(), Target: command.PlanID, Decision: command.Decision})
		if err != nil {
			return failure(err), true
		}
		return success(nil), true
	case "new_session":
		id, err := randomID("session")
		if err != nil {
			return failure(err), true
		}
		if err := server.service.ExecuteAction(ctx, app.Action{Kind: app.ActionNewSession, Target: id}); err != nil {
			return failure(err), true
		}
		server.mu.Lock()
		server.sessionID = id
		server.mu.Unlock()
		return success(map[string]any{"sessionId": id, "cancelled": false}), true
	}
	return Response{}, false
}

func (server *Server) startPrompt(message string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("prompt message is required")
	}
	runID, err := server.service.StartConfiguredTurn(app.TurnRequest{SessionID: server.currentSession(), Prompt: message})
	if err != nil {
		return "", err
	}
	server.mu.Lock()
	server.claimed[runID] = struct{}{}
	if _, completed := server.completed[runID]; completed {
		delete(server.completed, runID)
		delete(server.claimed, runID)
	} else {
		server.activeRun = runID
		if server.runDone[runID] == nil {
			server.runDone[runID] = make(chan struct{})
		}
	}
	server.mu.Unlock()
	return runID, nil
}

func (server *Server) currentSession() string {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.sessionID
}

func (server *Server) currentRun() string {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.activeRun
}

func (server *Server) waitRun(ctx context.Context, runID string) error {
	server.mu.RLock()
	done := server.runDone[runID]
	server.mu.RUnlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
		return errors.New("timed out waiting for active run to stop")
	}
}

func (server *Server) updatePreferences(ctx context.Context, provider, model, reasoning string) error {
	current, err := server.sessions.LoadSession(ctx, server.currentSession())
	if err != nil {
		return err
	}
	return server.sessions.UpdatePreferences(ctx, current.ID, firstNonempty(provider, current.ProviderID), firstNonempty(model, current.ModelID), firstNonempty(reasoning, current.Reasoning), firstNonempty(current.AgentMode, "single"))
}

func (server *Server) state(ctx context.Context) (map[string]any, error) {
	projection, err := server.sessions.LoadProjection(ctx, server.currentSession())
	if err != nil {
		return nil, err
	}
	todo, err := server.sessions.LoadTodo(ctx, server.currentSession())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"sessionId": projection.Session.ID, "sessionName": projection.Session.Title,
		"model":         map[string]string{"provider": projection.Session.ProviderID, "id": projection.Session.ModelID},
		"thinkingLevel": projection.Session.Reasoning, "isStreaming": server.currentRun() != "", "messageCount": len(projection.Blocks),
		"todoPhases": todo.Phases, "cacheEpoch": projection.CacheEpoch, "checkpointGeneration": projection.CheckpointGeneration,
	}, nil
}

var (
	errStaleCursor = errors.New("stale RPC message cursor")
	errSessionBusy = errors.New("RPC message snapshot is busy")
)

type messagePage struct {
	Messages      []session.Block `json:"messages"`
	TotalMessages int             `json:"totalMessages"`
	NextCursor    string          `json:"nextCursor,omitempty"`
}

type cursorPayload struct {
	SessionID string `json:"sessionId"`
	Leaf      string `json:"leaf"`
	Count     int    `json:"count"`
	Offset    int    `json:"offset"`
}

func (server *Server) messagesPage(ctx context.Context, cursor string, limit int) (messagePage, error) {
	if server.currentRun() != "" {
		return messagePage{}, errSessionBusy
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 256 {
		limit = 256
	}
	projection, err := server.sessions.LoadProjection(ctx, server.currentSession())
	if err != nil {
		return messagePage{}, err
	}
	tree, err := server.sessions.LoadSessionTree(ctx, server.currentSession())
	if err != nil {
		return messagePage{}, err
	}
	offset := 0
	if cursor != "" {
		payload, err := server.decodeCursor(cursor)
		if err != nil || payload.SessionID != projection.Session.ID || payload.Leaf != tree.ActiveLeafEntryID || payload.Count != len(projection.Blocks) {
			return messagePage{}, errStaleCursor
		}
		offset = payload.Offset
	}
	if offset < 0 || offset > len(projection.Blocks) {
		return messagePage{}, errStaleCursor
	}
	end := min(offset+limit, len(projection.Blocks))
	page := messagePage{Messages: append([]session.Block(nil), projection.Blocks[offset:end]...), TotalMessages: len(projection.Blocks)}
	if end < len(projection.Blocks) {
		page.NextCursor, err = server.encodeCursor(cursorPayload{SessionID: projection.Session.ID, Leaf: tree.ActiveLeafEntryID, Count: len(projection.Blocks), Offset: end})
	}
	return page, err
}

func (server *Server) encodeCursor(payload cursorPayload) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, server.cursorKey)
	_, _ = mac.Write(encoded)
	value := append(encoded, mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (server *Server) decodeCursor(value string) (cursorPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) <= sha256.Size {
		return cursorPayload{}, errStaleCursor
	}
	payload, signature := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, server.cursorKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursorPayload{}, errStaleCursor
	}
	var decoded cursorPayload
	if json.Unmarshal(payload, &decoded) != nil {
		return cursorPayload{}, errStaleCursor
	}
	return decoded, nil
}

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
