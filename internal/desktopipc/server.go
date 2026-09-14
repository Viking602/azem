package desktopipc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
)

const (
	eventBatchInterval = 16 * time.Millisecond
	maxEventBatchCount = 128
	maxCommandReceipts = 4096
	commandReceiptTTL  = 24 * time.Hour
)

type ServerOptions struct {
	Listener            net.Listener
	Token               []byte
	WorkspaceID         string
	DaemonEpoch         string
	Hub                 *EventHub
	Dispatcher          RequestDispatcher
	TransferDir         string
	TerminalReplay      *TerminalReplay
	OnDaemonStop        func()
	AuthorizeDaemonStop func(includeActive bool) error
	IdleTimeout         time.Duration
}
type sequencedCommand struct {
	request   Envelope
	transfers *TransferManager
	response  chan Envelope
}
type commandReceipt struct {
	digest   [sha256.Size]byte
	response Envelope
	storedAt time.Time
}

type Server struct {
	listener       net.Listener
	token          []byte
	workspaceID    string
	daemonEpoch    string
	hub            *EventHub
	dispatcher     RequestDispatcher
	transferDir    string
	terminalReplay *TerminalReplay
	onDaemonStop   func()
	authorizeStop  func(bool) error
	idleTimeout    time.Duration
	idleSince      time.Time
	stopping       bool
	commandMu      sync.Mutex
	mu             sync.Mutex
	closeOnce      sync.Once
	closeErr       error
	connections    map[net.Conn]struct{}
	commands       chan sequencedCommand
	done           chan struct{}
	receipts       map[string]commandReceipt
	receiptOrder   []string
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.IdleTimeout > 0 && (options.OnDaemonStop == nil || options.AuthorizeDaemonStop == nil) {
		return nil, errors.New("idle shutdown requires daemon stop authorization and callback")
	}
	if options.Listener == nil || len(options.Token) != tokenBytes || strings.TrimSpace(options.WorkspaceID) == "" || options.Hub == nil || options.Dispatcher == nil || strings.TrimSpace(options.TransferDir) == "" {
		return nil, errors.New("IPC server options are incomplete")
	}
	epoch := strings.TrimSpace(options.DaemonEpoch)
	if epoch == "" {
		var err error
		epoch, err = GenerateNonce()
		if err != nil {
			return nil, fmt.Errorf("generate daemon epoch: %w", err)
		}
	}
	server := &Server{
		listener: options.Listener, token: append([]byte(nil), options.Token...), workspaceID: options.WorkspaceID, daemonEpoch: epoch,
		hub: options.Hub, dispatcher: options.Dispatcher, transferDir: options.TransferDir,
		terminalReplay: options.TerminalReplay, onDaemonStop: options.OnDaemonStop,
		authorizeStop: options.AuthorizeDaemonStop, connections: make(map[net.Conn]struct{}),
		commands: make(chan sequencedCommand), done: make(chan struct{}),
		receipts:    make(map[string]commandReceipt),
		idleTimeout: options.IdleTimeout, idleSince: time.Now(),
	}
	go server.runCommandSequencer()
	return server, nil
}

func (server *Server) DaemonEpoch() string {
	if server == nil {
		return ""
	}
	return server.daemonEpoch
}

func (server *Server) Serve(ctx context.Context) error {
	if server.idleTimeout > 0 {
		go server.watchIdle(ctx)
	}
	go func() {
		<-ctx.Done()
		server.Close()
	}()
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		server.mu.Lock()
		if server.stopping {
			server.mu.Unlock()
			connection.Close()
			return nil
		}
		server.connections[connection] = struct{}{}
		server.mu.Unlock()
		go server.serveConnection(ctx, connection)
	}
}

func (server *Server) watchIdle(ctx context.Context) {
	ticker := time.NewTicker(min(server.idleTimeout, time.Second))
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			if server.stopIfIdle(now) {
				return
			}
		case <-ctx.Done():
			return
		case <-server.done:
			return
		}
	}
}

func (server *Server) stopIfIdle(now time.Time) bool {
	// A disconnected client may still have a mutation in the sequencer.
	server.commandMu.Lock()
	defer server.commandMu.Unlock()
	server.mu.Lock()
	if server.stopping || len(server.connections) != 0 || now.Sub(server.idleSince) < server.idleTimeout {
		server.mu.Unlock()
		return false
	}
	if server.authorizeStop(false) != nil {
		server.mu.Unlock()
		return false
	}
	server.stopping = true
	server.mu.Unlock()
	server.onDaemonStop()
	return true
}

func (server *Server) Close() error {
	server.closeOnce.Do(func() {
		close(server.done)
		server.mu.Lock()
		server.stopping = true
		connections := make([]net.Conn, 0, len(server.connections))
		for connection := range server.connections {
			connections = append(connections, connection)
		}
		server.connections = make(map[net.Conn]struct{})
		server.mu.Unlock()
		for _, connection := range connections {
			server.closeErr = errors.Join(server.closeErr, connection.Close())
		}
		if err := server.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			server.closeErr = errors.Join(server.closeErr, err)
		}
	})
	return server.closeErr
}

func (server *Server) serveConnection(parent context.Context, connection net.Conn) {
	defer func() {
		connection.Close()
		server.mu.Lock()
		delete(server.connections, connection)
		server.idleSince = time.Now()
		server.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(parent)
	if err := connection.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return
	}
	defer cancel()
	codec := NewCodec(connection)
	authentication, err := server.authenticate(codec)
	if err != nil {
		return
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return
	}
	subscription, replay := server.hub.Subscribe(256, authentication.LastSequence)
	defer subscription.Close()
	ack := NewEnvelope(FrameHelloAck)
	ack.ClientID = authentication.ClientID
	ack.WorkspaceID = server.workspaceID
	ack.Sequence = replay.CurrentSequence
	ack.Payload = mustJSON(HelloAck{Protocol: ProtocolVersion, WorkspaceID: server.workspaceID, DaemonEpoch: server.daemonEpoch, CurrentSequence: replay.CurrentSequence, ReplayAvailable: !replay.ResyncRequired})
	if err := codec.WriteEnvelope(ack); err != nil {
		return
	}
	if replay.ResyncRequired {
		if err := codec.WriteEnvelope(resyncEnvelope("event_replay_unavailable", replay.CurrentSequence)); err != nil {
			return
		}
	} else if len(replay.Events) > 0 {
		if err := writeEventRecords(codec, replay.Events); err != nil {
			return
		}
		complete := NewEnvelope(FrameReplayComplete)
		complete.Sequence = replay.CurrentSequence
		if err := codec.WriteEnvelope(complete); err != nil {
			return
		}
	}
	transferManager, err := NewTransferManager(server.transferDir)
	if err != nil {
		return
	}
	defer transferManager.Close()
	eventErrors := make(chan error, 1)
	go func() { eventErrors <- server.forwardEvents(ctx, codec, subscription.Events) }()
	// Startup refreshes use a separate, bounded FIFO so network latency cannot
	// block navigation. Keep one dashboard query in flight per connection.
	dashboards := make(chan Envelope, 1)
	go func() {
		for {
			select {
			case request := <-dashboards:
				if ctx.Err() != nil {
					return
				}
				response := server.dispatchResponse(ctx, request, nil)
				if ctx.Err() != nil {
					return
				}
				if err := codec.WriteEnvelope(response); err != nil {
					_ = connection.Close()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		if err := connection.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
			return
		}
		frame, err := codec.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				_ = writeProtocolError(codec, "", "read_failed", err, server.hub.CurrentSequence())
			}
			return
		}
		if frame.Binary != nil {
			if err := transferManager.WriteChunk(*frame.Binary, frame.Data); err != nil {
				_ = writeProtocolError(codec, frame.Binary.TransferID, "binary_rejected", err, server.hub.CurrentSequence())
				return
			}
			continue
		}
		envelope := frame.Envelope
		if envelope == nil {
			return
		}
		if envelope.ClientID != authentication.ClientID || envelope.WorkspaceID != server.workspaceID {
			_ = writeProtocolError(codec, envelope.ID, "actor_identity_mismatch", ErrAuthentication, server.hub.CurrentSequence())
			return
		}
		switch envelope.Kind {
		case FramePing:
			pong := NewEnvelope(FramePong)
			pong.ID, pong.ClientID, pong.WorkspaceID = envelope.ID, authentication.ClientID, server.workspaceID
			pong.Sequence = server.hub.CurrentSequence()
			if err := codec.WriteEnvelope(pong); err != nil {
				return
			}
		case FrameClientDetach:
			return
		case FrameDaemonStop:
			params, stopErr := decodeParams[DaemonStop](envelope.Payload)
			if stopErr == nil && server.authorizeStop != nil {
				stopErr = server.authorizeStop(params.IncludeActive)
			}
			response := NewEnvelope(FrameResponse)
			response.ID = envelope.ID
			response.Sequence = server.hub.CurrentSequence()
			if stopErr != nil {
				response.Error = &ProtocolError{Code: "daemon_stop_refused", Message: stopErr.Error(), Cursor: response.Sequence}
			} else {
				response.Payload = mustJSON(map[string]bool{"stopping": true})
			}
			if err := codec.WriteEnvelope(response); err != nil {
				return
			}
			if stopErr != nil {
				continue
			}
			if server.onDaemonStop != nil {
				server.onDaemonStop()
			}
			return
		case FrameRequest:
			// Detail reads, mutations, terminal replay and transfers stay ordered.
			if envelope.Method == MethodPullRequestDashboard {
				select {
				case dashboards <- *envelope:
				default:
					if err := writeProtocolError(codec, envelope.ID, "request_busy", errors.New("dashboard refresh already queued"), server.hub.CurrentSequence()); err != nil {
						return
					}
				}
				continue
			}
			if envelope.Method == MethodTerminalReplay {
				params, replayErr := decodeParams[idParams](envelope.Payload)
				response := NewEnvelope(FrameResponse)
				response.ID, response.Method = envelope.ID, envelope.Method
				response.Sequence = server.hub.CurrentSequence()
				chunks := server.terminalReplayChunks(params.ID)
				if replayErr != nil {
					response.Error = &ProtocolError{Code: "request_failed", Message: replayErr.Error(), Cursor: response.Sequence}
				} else {
					response.Payload = mustJSON(map[string]int{"chunks": len(chunks)})
				}
				if replayErr != nil {
					if err := codec.WriteEnvelope(response); err != nil {
						return
					}
				} else if err := codec.WriteTerminalReplay(response, chunks); err != nil {
					return
				}
				continue
			}
			response := server.dispatchResponse(ctx, *envelope, transferManager)
			if err := codec.WriteEnvelope(response); err != nil {
				return
			}
			if envelope.Method == MethodReconnectSnapshot && response.Error == nil && reconnectSnapshotWantsRefresh(envelope.Payload) {
				if refresher, ok := server.dispatcher.(interface{ RefreshProjection() }); ok {
					refresher.RefreshProjection()
				}
			}
		default:
			if err := writeProtocolError(codec, envelope.ID, "unexpected_frame", fmt.Errorf("unexpected client frame %q", envelope.Kind), server.hub.CurrentSequence()); err != nil {
				return
			}
		}
		select {
		case err := <-eventErrors:
			if err != nil {
				return
			}
		default:
		}
	}
}

func (server *Server) dispatchResponse(ctx context.Context, request Envelope, transfers *TransferManager) Envelope {
	if !isSequencedMethod(request.Method) {
		return server.dispatchResponseNow(request, transfers)
	}
	job := sequencedCommand{request: request, transfers: transfers, response: make(chan Envelope, 1)}
	select {
	case server.commands <- job:
	case <-ctx.Done():
		return responseForError(request, "request_cancelled", ctx.Err(), server.hub.CurrentSequence())
	case <-server.done:
		return responseForError(request, "daemon_stopped", net.ErrClosed, server.hub.CurrentSequence())
	}
	select {
	case response := <-job.response:
		return response
	case <-ctx.Done():
		return responseForError(request, "request_cancelled", ctx.Err(), server.hub.CurrentSequence())
	case <-server.done:
		return responseForError(request, "daemon_stopped", net.ErrClosed, server.hub.CurrentSequence())
	}
}

func (server *Server) runCommandSequencer() {
	for {
		select {
		case job := <-server.commands:
			server.commandMu.Lock()
			server.mu.Lock()
			stopping := server.stopping
			server.mu.Unlock()
			if stopping {
				job.response <- responseForError(job.request, "daemon_stopped", net.ErrClosed, server.hub.CurrentSequence())
			} else {
				job.response <- server.dispatchResponseNow(job.request, job.transfers)
			}
			server.commandMu.Unlock()
		case <-server.done:
			return
		}
	}
}

func (server *Server) dispatchResponseNow(request Envelope, transfers *TransferManager) Envelope {
	receiptKey, digest, receiptBound := commandReceiptIdentity(request)
	if receiptBound {
		server.pruneCommandReceipts(time.Now().UTC())
		if cached, exists := server.receipts[receiptKey]; exists {
			if cached.digest != digest {
				return responseForError(request, "request_duplicate", errors.New("mutationId was already used for a different request"), server.hub.CurrentSequence())
			}
			response := cloneResponseEnvelope(cached.response)
			response.ID = request.ID
			response.Sequence = server.hub.CurrentSequence()
			if response.Error != nil {
				response.Error.Cursor = response.Sequence
			}
			return response
		}
	}
	snapshotBoundary := server.hub.CurrentSequence()
	result, err := server.dispatchRequest(request, transfers)
	if err == nil && (request.Method == MethodReconnectSnapshot || request.Method == MethodResumeSession || request.Method == MethodSelectSession || request.Method == MethodCreateSession) {
		result = bindReconnectSnapshotBoundary(result, server.daemonEpoch, snapshotBoundary)
	}
	response := NewEnvelope(FrameResponse)
	response.ID, response.Method = request.ID, request.Method
	response.Sequence = server.hub.CurrentSequence()
	if err != nil {
		response.Error = protocolError(err, response.Sequence)
	} else {
		response.Payload = mustJSON(result)
	}
	if receiptBound && response.Error == nil {
		server.receipts[receiptKey] = commandReceipt{digest: digest, response: cloneResponseEnvelope(response), storedAt: time.Now().UTC()}
		server.receiptOrder = append(server.receiptOrder, receiptKey)
		server.pruneCommandReceipts(time.Now().UTC())
	}
	return response
}

func commandReceiptIdentity(request Envelope) (string, [sha256.Size]byte, bool) {
	switch request.Method {
	case MethodStartTurn, MethodGuide, MethodFollowUp, MethodMutatePromptQueue:
	default:
		return "", [sha256.Size]byte{}, false
	}
	var identity struct {
		MutationID string `json:"mutationId"`
	}
	if json.Unmarshal(request.Payload, &identity) != nil || strings.TrimSpace(identity.MutationID) == "" {
		return "", [sha256.Size]byte{}, false
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(request.Method))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(request.Payload)
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return request.ClientID + "\x00" + strings.TrimSpace(identity.MutationID), digest, true
}

func (server *Server) pruneCommandReceipts(now time.Time) {
	cutoff := now.Add(-commandReceiptTTL)
	for len(server.receiptOrder) > 0 {
		key := server.receiptOrder[0]
		receipt, exists := server.receipts[key]
		if exists && len(server.receipts) <= maxCommandReceipts && !receipt.storedAt.Before(cutoff) {
			break
		}
		delete(server.receipts, key)
		server.receiptOrder[0] = ""
		server.receiptOrder = server.receiptOrder[1:]
	}
}

func cloneResponseEnvelope(response Envelope) Envelope {
	response.Payload = append(json.RawMessage(nil), response.Payload...)
	if response.Error != nil {
		copy := *response.Error
		response.Error = &copy
	}
	return response
}

func protocolError(err error, cursor uint64) *ProtocolError {
	code := "request_failed"
	retryable := false
	switch {
	case errors.Is(err, azemapp.ErrRunActive):
		code, retryable = "run_active", true
	case errors.Is(err, azemapp.ErrStaleRun):
		code, retryable = "stale_run", true
	case errors.Is(err, azemapp.ErrGuidanceClosed):
		code = "guidance_closed"
	case errors.Is(err, session.ErrPromptQueueRevisionConflict):
		code, retryable = "revision_conflict", true
	case errors.Is(err, azemapp.ErrInvalidPromptQueueAction):
		code = "invalid_action"
	case errors.Is(err, session.ErrSessionNotFound):
		code = "not_found"
	}
	return &ProtocolError{Code: code, Message: err.Error(), Retryable: retryable, Cursor: cursor}
}

func protocolErrorForCode(code string, err error, cursor uint64) *ProtocolError {
	retryable := code == "request_busy" || code == "resync_required"
	return &ProtocolError{Code: code, Message: err.Error(), Retryable: retryable, Cursor: cursor}
}

func responseForError(request Envelope, code string, err error, cursor uint64) Envelope {
	response := NewEnvelope(FrameResponse)
	response.ID, response.Method, response.Sequence = request.ID, request.Method, cursor
	response.Error = protocolErrorForCode(code, err, cursor)
	return response
}

func isSequencedMethod(method Method) bool {
	switch method {
	case MethodReconnectSnapshot, MethodStartTurn, MethodGuide, MethodFollowUp, MethodCancelActive,
		MethodExecute, MethodImportAttachment, MethodImportClipboardImage, MethodResumeSession,
		MethodSelectSession, MethodCreateSession, MethodNavigateSessionTree, MethodCreateSessionFork,
		MethodSetSessionEntryLabel, MethodForkSession, MethodImportSession, MethodExpandSkillInvocation,
		MethodCollaboration, MethodMutatePromptQueue, MethodCreateProject, MethodOpenProject, MethodOpenProjectSession,
		MethodBeginAttachmentTransfer, MethodCommitAttachment, MethodAbortAttachment,
		MethodCreateTerminal, MethodWriteTerminal, MethodResizeTerminal, MethodCloseTerminal:
		return true
	default:
		return false
	}
}

func (server *Server) authenticate(codec *Codec) (Authenticate, error) {
	nonce, err := GenerateNonce()
	if err != nil {
		return Authenticate{}, err
	}
	challenge := NewEnvelope(FrameHello)
	challenge.WorkspaceID = server.workspaceID
	challenge.Payload = mustJSON(Challenge{Nonce: nonce, WorkspaceID: server.workspaceID, Protocol: ProtocolVersion, DaemonEpoch: server.daemonEpoch})
	if err := codec.WriteEnvelope(challenge); err != nil {
		return Authenticate{}, err
	}
	frame, err := codec.ReadFrame()
	if err != nil {
		return Authenticate{}, err
	}
	if frame.Envelope == nil || frame.Envelope.Kind != FrameHelloAck {
		return Authenticate{}, ErrAuthentication
	}
	var authentication Authenticate
	if err := json.Unmarshal(frame.Envelope.Payload, &authentication); err != nil {
		return Authenticate{}, ErrAuthentication
	}
	if authentication.Protocol != ProtocolVersion || strings.TrimSpace(authentication.ClientID) == "" ||
		frame.Envelope.ClientID != authentication.ClientID || frame.Envelope.WorkspaceID != server.workspaceID ||
		!VerifyAuthenticationProof(server.token, nonce, authentication.ClientID, server.workspaceID, authentication.Protocol, authentication.Proof) {
		return Authenticate{}, ErrAuthentication
	}
	return authentication, nil
}

func (server *Server) dispatchRequest(envelope Envelope, transfers *TransferManager) (any, error) {
	switch envelope.Method {
	case MethodBeginAttachmentTransfer:
		return nil, transfers.Begin(envelope.Payload)
	case MethodCommitAttachment:
		params, err := decodeParams[idParams](envelope.Payload)
		if err != nil {
			return nil, err
		}
		return transfers.Commit(params.ID, server.dispatcher)
	case MethodAbortAttachment:
		params, err := decodeParams[idParams](envelope.Payload)
		if err != nil {
			return nil, err
		}
		transfers.Abort(params.ID)
		return nil, nil
	default:
		return server.dispatcher.Dispatch(envelope.Method, envelope.Payload)
	}
}

func (server *Server) terminalReplayChunks(terminalID string) []terminalChunk {
	if server.terminalReplay == nil {
		return nil
	}
	return server.terminalReplay.Snapshot(strings.TrimSpace(terminalID))
}

func (server *Server) forwardEvents(ctx context.Context, codec *Codec, events <-chan EventRecord) error {
	batch := make([]EventRecord, 0, maxEventBatchCount)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case record, ok := <-events:
			if !ok {
				return nil
			}
			if record.Sequence == 0 {
				if err := codec.WriteEnvelope(resyncEnvelope("client_queue_overflow", server.hub.CurrentSequence())); err != nil {
					return err
				}
				continue
			}
			batch = append(batch, record)
			timer := time.NewTimer(eventBatchInterval)
		collect:
			for len(batch) < maxEventBatchCount {
				select {
				case next, ok := <-events:
					if !ok {
						break collect
					}
					if next.Sequence == 0 {
						if err := codec.WriteEnvelope(resyncEnvelope("client_queue_overflow", server.hub.CurrentSequence())); err != nil {
							timer.Stop()
							return err
						}
						continue
					}
					batch = append(batch, next)
				case <-timer.C:
					break collect
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				}
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if err := writeEventRecords(codec, batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
}

func writeEventRecords(codec *Codec, records []EventRecord) error {
	const maxBatchPayload = MaxControlFrameBytes - (64 << 10)
	events := make([]json.RawMessage, 0, min(len(records), maxEventBatchCount))
	var firstSequence, lastSequence uint64
	batchBytes := 0
	flushControls := func() error {
		if len(events) == 0 {
			return nil
		}
		batch := EventBatch{
			FirstSequence: firstSequence,
			LastSequence:  lastSequence,
			Events:        events,
		}
		envelope := NewEnvelope(FrameEventBatch)
		envelope.Sequence = lastSequence
		envelope.Payload = mustJSON(batch)
		if err := codec.WriteEnvelope(envelope); err != nil {
			return err
		}
		events = events[:0]
		firstSequence, lastSequence, batchBytes = 0, 0, 0
		return nil
	}
	for _, record := range records {
		if record.Binary != nil {
			if err := flushControls(); err != nil {
				return err
			}
			if err := codec.WriteBinary(*record.Binary, record.Data); err != nil {
				return err
			}
			continue
		}
		encoded, err := json.Marshal(eventEnvelope(record))
		if err != nil {
			return err
		}
		if len(encoded) > maxBatchPayload {
			return fmt.Errorf("IPC event %d exceeds the control frame budget", record.Sequence)
		}
		if len(events) > 0 && batchBytes+len(encoded) > maxBatchPayload {
			if err := flushControls(); err != nil {
				return err
			}
		}
		if len(events) == 0 {
			firstSequence = record.Sequence
		}
		events = append(events, encoded)
		lastSequence = record.Sequence
		batchBytes += len(encoded)
	}
	return flushControls()
}

func resyncEnvelope(reason string, sequence uint64) Envelope {
	envelope := NewEnvelope(FrameResyncRequired)
	envelope.Sequence = sequence
	envelope.Payload = mustJSON(map[string]string{"reason": reason})
	return envelope
}

func writeProtocolError(codec *Codec, id, code string, err error, cursor uint64) error {
	envelope := NewEnvelope(FrameResponse)
	envelope.ID = id
	if envelope.ID == "" {
		envelope.ID = "transport"
	}
	envelope.Sequence = cursor
	envelope.Error = protocolErrorForCode(code, err, cursor)
	return codec.WriteEnvelope(envelope)
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
