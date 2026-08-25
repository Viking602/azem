package desktopipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	eventBatchInterval = 16 * time.Millisecond
	maxEventBatchCount = 128
)

type ServerOptions struct {
	Listener            net.Listener
	Token               []byte
	WorkspaceID         string
	Hub                 *EventHub
	Dispatcher          RequestDispatcher
	TransferDir         string
	TerminalReplay      *TerminalReplay
	OnDaemonStop        func()
	AuthorizeDaemonStop func(includeActive bool) error
}

type Server struct {
	listener       net.Listener
	token          []byte
	workspaceID    string
	hub            *EventHub
	dispatcher     RequestDispatcher
	transferDir    string
	terminalReplay *TerminalReplay
	onDaemonStop   func()
	authorizeStop  func(bool) error
	mu             sync.Mutex
	closeOnce      sync.Once
	closeErr       error
	connections    map[net.Conn]struct{}
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Listener == nil || len(options.Token) != tokenBytes || strings.TrimSpace(options.WorkspaceID) == "" || options.Hub == nil || options.Dispatcher == nil || strings.TrimSpace(options.TransferDir) == "" {
		return nil, errors.New("IPC server options are incomplete")
	}
	return &Server{
		listener: options.Listener, token: append([]byte(nil), options.Token...), workspaceID: options.WorkspaceID,
		hub: options.Hub, dispatcher: options.Dispatcher, transferDir: options.TransferDir,
		terminalReplay: options.TerminalReplay, onDaemonStop: options.OnDaemonStop,
		authorizeStop: options.AuthorizeDaemonStop, connections: make(map[net.Conn]struct{}),
	}, nil
}

func (server *Server) Serve(ctx context.Context) error {
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
		server.connections[connection] = struct{}{}
		server.mu.Unlock()
		go server.serveConnection(ctx, connection)
	}
}

func (server *Server) Close() error {
	server.closeOnce.Do(func() {
		server.mu.Lock()
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
	ack.Payload = mustJSON(HelloAck{Protocol: ProtocolVersion, WorkspaceID: server.workspaceID, CurrentSequence: replay.CurrentSequence, ReplayAvailable: !replay.ResyncRequired})
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
	for {
		if err := connection.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
			return
		}
		frame, err := codec.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				_ = writeProtocolError(codec, "", "read_failed", err)
			}
			return
		}
		if frame.Binary != nil {
			if err := transferManager.WriteChunk(*frame.Binary, frame.Data); err != nil {
				_ = writeProtocolError(codec, frame.Binary.TransferID, "binary_rejected", err)
				return
			}
			continue
		}
		envelope := frame.Envelope
		switch envelope.Kind {
		case FramePing:
			pong := NewEnvelope(FramePong)
			pong.ID = envelope.ID
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
			if stopErr != nil {
				response.Error = &ProtocolError{Code: "daemon_stop_refused", Message: stopErr.Error()}
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
			if envelope.Method == MethodTerminalReplay {
				params, replayErr := decodeParams[idParams](envelope.Payload)
				response := NewEnvelope(FrameResponse)
				response.ID, response.Method = envelope.ID, envelope.Method
				chunks := server.terminalReplayChunks(params.ID)
				if replayErr != nil {
					response.Error = &ProtocolError{Code: "request_failed", Message: replayErr.Error()}
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
			result, dispatchErr := server.dispatchRequest(*envelope, transferManager)
			response := NewEnvelope(FrameResponse)
			response.ID = envelope.ID
			response.Method = envelope.Method
			if dispatchErr != nil {
				response.Error = &ProtocolError{Code: "request_failed", Message: dispatchErr.Error()}
			} else {
				response.Payload = mustJSON(result)
			}
			if err := codec.WriteEnvelope(response); err != nil {
				return
			}
		default:
			if err := writeProtocolError(codec, envelope.ID, "unexpected_frame", fmt.Errorf("unexpected client frame %q", envelope.Kind)); err != nil {
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

func (server *Server) authenticate(codec *Codec) (Authenticate, error) {
	nonce, err := GenerateNonce()
	if err != nil {
		return Authenticate{}, err
	}
	challenge := NewEnvelope(FrameHello)
	challenge.WorkspaceID = server.workspaceID
	challenge.Payload = mustJSON(Challenge{Nonce: nonce, WorkspaceID: server.workspaceID, Protocol: ProtocolVersion})
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
	if authentication.Protocol != ProtocolVersion || strings.TrimSpace(authentication.ClientID) == "" || !VerifyAuthenticationProof(server.token, nonce, authentication.ClientID, server.workspaceID, authentication.Protocol, authentication.Proof) {
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

func writeProtocolError(codec *Codec, id, code string, err error) error {
	envelope := NewEnvelope(FrameResponse)
	envelope.ID = id
	if envelope.ID == "" {
		envelope.ID = "transport"
	}
	envelope.Error = &ProtocolError{Code: code, Message: err.Error()}
	return codec.WriteEnvelope(envelope)
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
