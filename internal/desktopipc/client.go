package desktopipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/desktop"
	"github.com/google/uuid"
)

const (
	defaultClientKeepalive = 30 * time.Second
	clientWriteTimeout     = 10 * time.Second
)

// ProtocolRequestError preserves the daemon's stable error taxonomy and the
// event cursor at which the request was rejected.
type ProtocolRequestError struct {
	Code      string
	Message   string
	Retryable bool
	Cursor    uint64
}

func (err *ProtocolRequestError) Error() string {
	if err == nil {
		return ""
	}
	if strings.TrimSpace(err.Code) == "" {
		return err.Message
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Message)
}

type clientResponse struct {
	envelope Envelope
	err      error
}

type clientOptions struct {
	queueBytes          int
	keepaliveInterval   time.Duration
	protocol            int
	allowLegacyProtocol bool
	allowMissingEpoch   bool
}

type Client struct {
	connection net.Conn
	codec      *Codec
	clientID   string
	workspace  string
	protocol   int
	epoch      string

	writeMu sync.Mutex
	stateMu sync.Mutex
	pending map[string]chan clientResponse
	ended   error

	messages *clientMessageQueue
	done     chan struct{}
	endOnce  sync.Once
}

func Connect(ctx context.Context, endpoint Endpoint, clientID string, lastSequence uint64) (*Client, HelloAck, error) {
	return connectWithOptions(ctx, endpoint, clientID, lastSequence, clientOptions{
		queueBytes:        DefaultClientQueueBytes,
		keepaliveInterval: defaultClientKeepalive,
	})
}

// StopLegacyDaemonForUpgrade authenticates to the pre-epoch protocol only to
// request a graceful idle-daemon shutdown. It must never be used for runtime
// requests or replay.
func StopLegacyDaemonForUpgrade(ctx context.Context, endpoint Endpoint, clientID string) (bool, error) {
	legacyProtocol := endpoint.Protocol > 0 && endpoint.Protocol < ProtocolVersion
	preEpochCurrent := endpoint.Protocol == ProtocolVersion && strings.TrimSpace(endpoint.DaemonEpoch) == ""
	if (!legacyProtocol && !preEpochCurrent) || strings.TrimSpace(endpoint.WorkspaceID) == "" {
		return false, errors.New("endpoint is not a supported legacy daemon")
	}
	client, _, err := connectWithOptions(ctx, endpoint, clientID, 0, clientOptions{
		queueBytes:          DefaultClientQueueBytes,
		keepaliveInterval:   defaultClientKeepalive,
		protocol:            endpoint.Protocol,
		allowLegacyProtocol: legacyProtocol,
		allowMissingEpoch:   strings.TrimSpace(endpoint.DaemonEpoch) == "",
	})
	if err != nil {
		return false, err
	}
	defer client.Close()
	return true, client.StopDaemon(ctx, false)
}

func connectWithOptions(ctx context.Context, endpoint Endpoint, clientID string, lastSequence uint64, options clientOptions) (*Client, HelloAck, error) {
	protocol := options.protocol
	if protocol == 0 {
		protocol = ProtocolVersion
	}
	if endpoint.Protocol != protocol || (protocol != ProtocolVersion && !options.allowLegacyProtocol) {
		return nil, HelloAck{}, fmt.Errorf("unsupported daemon protocol %d", endpoint.Protocol)
	}
	token, err := ReadTokenFile(endpoint.TokenFile)
	if err != nil {
		return nil, HelloAck{}, err
	}
	connection, err := Dial(ctx, endpoint.Address)
	if err != nil {
		return nil, HelloAck{}, err
	}
	fail := func(err error) (*Client, HelloAck, error) {
		_ = connection.Close()
		return nil, HelloAck{}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetDeadline(time.Now().Add(clientWriteTimeout))
	}
	codec := NewCodecForVersion(connection, protocol)
	frame, err := codec.ReadFrame()
	if err != nil {
		return fail(err)
	}
	if frame.Envelope == nil || frame.Envelope.Kind != FrameHello {
		return fail(ErrAuthentication)
	}
	var challenge Challenge
	if err := json.Unmarshal(frame.Envelope.Payload, &challenge); err != nil ||
		challenge.Protocol != protocol || challenge.WorkspaceID != endpoint.WorkspaceID ||
		(strings.TrimSpace(endpoint.DaemonEpoch) != "" && challenge.DaemonEpoch != endpoint.DaemonEpoch) {
		return fail(ErrAuthentication)
	}
	if options.allowMissingEpoch {
		if strings.TrimSpace(endpoint.DaemonEpoch) != "" || strings.TrimSpace(challenge.DaemonEpoch) != "" {
			return fail(ErrAuthentication)
		}
	} else if strings.TrimSpace(challenge.DaemonEpoch) == "" {
		return fail(ErrAuthentication)
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = uuid.NewString()
	}
	authentication := Authenticate{
		ClientID: clientID, Protocol: protocol, LastSequence: lastSequence,
		Proof: AuthenticationProof(token, challenge.Nonce, clientID, endpoint.WorkspaceID, protocol),
	}
	hello := Envelope{Version: protocol, Kind: FrameHelloAck}
	hello.ClientID, hello.WorkspaceID, hello.Payload = clientID, endpoint.WorkspaceID, mustJSON(authentication)
	if err := codec.WriteEnvelope(hello); err != nil {
		return fail(err)
	}
	frame, err = codec.ReadFrame()
	if err != nil {
		return fail(err)
	}
	if frame.Envelope == nil || frame.Envelope.Kind != FrameHelloAck {
		return fail(ErrAuthentication)
	}
	var ack HelloAck
	if err := json.Unmarshal(frame.Envelope.Payload, &ack); err != nil {
		return fail(err)
	}
	if ack.Protocol != protocol || ack.WorkspaceID != endpoint.WorkspaceID || ack.DaemonEpoch != challenge.DaemonEpoch {
		return fail(ErrAuthentication)
	}
	_ = connection.SetDeadline(time.Time{})
	if options.queueBytes <= 0 {
		options.queueBytes = DefaultClientQueueBytes
	}
	if options.keepaliveInterval <= 0 {
		options.keepaliveInterval = defaultClientKeepalive
	}
	client := &Client{
		connection: connection,
		codec:      codec,
		clientID:   clientID,
		workspace:  endpoint.WorkspaceID,
		protocol:   protocol,
		epoch:      ack.DaemonEpoch,
		pending:    make(map[string]chan clientResponse),
		messages:   newClientMessageQueue(options.queueBytes),
		done:       make(chan struct{}),
	}
	go client.readLoop()
	go client.keepaliveLoop(options.keepaliveInterval)
	return client, ack, nil
}

func (client *Client) newEnvelope(kind FrameKind) Envelope {
	protocol := client.protocol
	if protocol == 0 {
		protocol = ProtocolVersion
	}
	return Envelope{Version: protocol, Kind: kind}
}

func (client *Client) Request(ctx context.Context, method Method, payload any, target any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	envelope := client.newEnvelope(FrameRequest)
	envelope.ID = uuid.NewString()
	envelope.Method = method
	envelope.Payload = encoded
	return client.request(ctx, envelope, target)
}

func (client *Client) Next(ctx context.Context) (ClientMessage, error) {
	if client == nil || client.messages == nil {
		return ClientMessage{}, ErrClientClosed
	}
	return client.messages.next(ctx)
}

func (client *Client) StopDaemon(ctx context.Context, includeActive bool) error {
	if client == nil {
		return nil
	}
	envelope := client.newEnvelope(FrameDaemonStop)
	envelope.ID = uuid.NewString()
	envelope.Payload = mustJSON(DaemonStop{IncludeActive: includeActive})
	return client.request(ctx, envelope, nil)
}

func (client *Client) UploadAttachment(ctx context.Context, sessionID, name, mimeType string, data []byte) (desktop.Attachment, error) {
	var attachment desktop.Attachment
	if client == nil {
		return attachment, ErrClientClosed
	}
	if len(data) > MaxReassembledBinary {
		return attachment, fmt.Errorf("attachment exceeds %d bytes", MaxReassembledBinary)
	}
	transferID := uuid.NewString()
	digest := sha256.Sum256(data)
	chunkCount := (len(data) + MaxBinaryChunkBytes - 1) / MaxBinaryChunkBytes
	params := beginAttachmentParams{
		TransferID: transferID,
		SessionID:  strings.TrimSpace(sessionID),
		Name:       strings.TrimSpace(name),
		MIMEType:   strings.TrimSpace(mimeType),
		ByteLength: int64(len(data)),
		SHA256:     hex.EncodeToString(digest[:]),
		ChunkCount: chunkCount,
	}
	if err := client.Request(ctx, MethodBeginAttachmentTransfer, params, nil); err != nil {
		return attachment, err
	}
	abort := func() {
		abortCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Request(abortCtx, MethodAbortAttachment, idParams{ID: transferID}, nil)
	}
	for index, offset := 0, 0; offset < len(data); index, offset = index+1, offset+MaxBinaryChunkBytes {
		end := min(offset+MaxBinaryChunkBytes, len(data))
		metadata := BinaryMetadata{TransferID: transferID, Purpose: "attachment_upload", Index: index, Count: chunkCount}
		if err := client.writeBinary(ctx, metadata, data[offset:end]); err != nil {
			abort()
			return attachment, err
		}
	}
	if err := client.Request(ctx, MethodCommitAttachment, idParams{ID: transferID}, &attachment); err != nil {
		abort()
		return desktop.Attachment{}, err
	}
	return attachment, nil
}

func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	client.stateMu.Lock()
	ended := client.ended
	client.stateMu.Unlock()
	if ended != nil {
		return nil
	}
	detach := client.newEnvelope(FrameClientDetach)
	detach.ClientID, detach.WorkspaceID = client.clientID, client.workspace
	writeErr := client.writeEnvelope(context.Background(), detach)
	closeErr := client.connection.Close()
	client.end(ErrClientClosed)
	if writeErr != nil && !errors.Is(writeErr, net.ErrClosed) {
		return errors.Join(writeErr, closeErr)
	}
	return closeErr
}

func (client *Client) request(ctx context.Context, envelope Envelope, target any) error {
	if client == nil || client.connection == nil {
		return ErrClientClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	envelope.ClientID, envelope.WorkspaceID = client.clientID, client.workspace
	waiter := make(chan clientResponse, 1)
	client.stateMu.Lock()
	if client.ended != nil {
		err := client.ended
		client.stateMu.Unlock()
		return err
	}
	client.pending[envelope.ID] = waiter
	client.stateMu.Unlock()
	removeWaiter := func() {
		client.stateMu.Lock()
		if client.pending[envelope.ID] == waiter {
			delete(client.pending, envelope.ID)
		}
		client.stateMu.Unlock()
	}
	if err := client.writeEnvelope(ctx, envelope); err != nil {
		removeWaiter()
		if ctx.Err() == nil {
			client.end(err)
		}
		return err
	}
	select {
	case <-ctx.Done():
		removeWaiter()
		return ctx.Err()
	case result := <-waiter:
		if result.err != nil {
			return result.err
		}
		if target != nil && len(result.envelope.Payload) > 0 && string(result.envelope.Payload) != "null" {
			if err := json.Unmarshal(result.envelope.Payload, target); err != nil {
				return err
			}
		}
		return nil
	}
}

func (client *Client) readLoop() {
	for {
		frame, err := client.codec.ReadFrame()
		if err != nil {
			client.end(err)
			return
		}
		if frame.Binary != nil {
			message := ClientMessage{
				Kind: ClientMessageBinary, Binary: *frame.Binary, Data: frame.Data, Sequence: frame.Binary.Sequence,
			}
			if !client.messages.push(message, clientMessageSize(message)) {
				client.end(errors.New("IPC client message queue overflowed during resync"))
				return
			}
			continue
		}
		envelope := frame.Envelope
		if envelope == nil {
			client.end(errors.New("IPC frame is empty"))
			return
		}
		switch envelope.Kind {
		case FrameResponse:
			client.routeResponse(*envelope)
		case FrameEventBatch:
			if err := client.routeEventBatch(*envelope); err != nil {
				client.end(err)
				return
			}
		case FrameReplayComplete:
			message := ClientMessage{Kind: ClientMessageReplayComplete, Event: *envelope, Sequence: envelope.Sequence}
			if !client.messages.push(message, clientMessageSize(message)) {
				client.end(errors.New("IPC client message queue overflowed during resync"))
				return
			}
		case FrameResyncRequired:
			message := ClientMessage{Kind: ClientMessageResyncRequired, Event: *envelope, Sequence: envelope.Sequence}
			if !client.messages.push(message, clientMessageSize(message)) {
				client.end(errors.New("IPC client message queue overflowed during resync"))
				return
			}
		case FramePing:
			pong := client.newEnvelope(FramePong)
			pong.ID, pong.ClientID, pong.WorkspaceID = envelope.ID, client.clientID, client.workspace
			if err := client.writeEnvelope(context.Background(), pong); err != nil {
				client.end(err)
				return
			}
		case FramePong:
		default:
			client.end(fmt.Errorf("unexpected daemon frame %q", envelope.Kind))
			return
		}
	}
}

func (client *Client) routeResponse(envelope Envelope) {
	client.stateMu.Lock()
	waiter := client.pending[envelope.ID]
	if waiter != nil {
		delete(client.pending, envelope.ID)
	}
	client.stateMu.Unlock()
	if waiter == nil {
		return
	}
	result := clientResponse{envelope: envelope}
	if envelope.Error != nil {
		cursor := envelope.Error.Cursor
		if cursor == 0 {
			cursor = envelope.Sequence
		}
		result.err = &ProtocolRequestError{
			Code: envelope.Error.Code, Message: envelope.Error.Message,
			Retryable: envelope.Error.Retryable, Cursor: cursor,
		}
	}
	waiter <- result
}

func (client *Client) routeEventBatch(envelope Envelope) error {
	var batch EventBatch
	if err := json.Unmarshal(envelope.Payload, &batch); err != nil {
		return fmt.Errorf("decode IPC event batch: %w", err)
	}
	if len(batch.Events) == 0 || batch.FirstSequence == 0 || batch.LastSequence < batch.FirstSequence || envelope.Sequence != batch.LastSequence {
		return errors.New("IPC event batch boundary is invalid")
	}
	for _, raw := range batch.Events {
		var event Envelope
		if err := json.Unmarshal(raw, &event); err != nil {
			return fmt.Errorf("decode IPC event envelope: %w", err)
		}
		if err := event.validateVersion(client.protocol); err != nil {
			return err
		}
		if event.Kind != FrameEventBatch || event.Sequence < batch.FirstSequence || event.Sequence > batch.LastSequence {
			return errors.New("IPC event envelope is outside its batch boundary")
		}
		if err := validateEventChannel(event.Channel); err != nil {
			return err
		}
		message := ClientMessage{Kind: ClientMessageEvent, Event: event, Sequence: event.Sequence}
		if !client.messages.push(message, len(raw)+128) {
			return errors.New("IPC client message queue overflowed during resync")
		}
	}
	return nil
}

func (client *Client) keepaliveLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-client.done:
			return
		case <-ticker.C:
			ping := client.newEnvelope(FramePing)
			ping.ID = uuid.NewString()
			ping.ClientID, ping.WorkspaceID = client.clientID, client.workspace
			ctx, cancel := context.WithTimeout(context.Background(), clientWriteTimeout)
			err := client.writeEnvelope(ctx, ping)
			cancel()
			if err != nil {
				client.end(err)
				return
			}
		}
	}
}

func (client *Client) writeEnvelope(ctx context.Context, envelope Envelope) error {
	if ctx == nil {
		ctx = context.Background()
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(clientWriteTimeout)
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	_ = client.connection.SetWriteDeadline(deadline)
	err := client.codec.WriteEnvelope(envelope)
	_ = client.connection.SetWriteDeadline(time.Time{})
	return err
}

func (client *Client) writeBinary(ctx context.Context, metadata BinaryMetadata, data []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(clientWriteTimeout)
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	_ = client.connection.SetWriteDeadline(deadline)
	err := client.codec.WriteBinary(metadata, data)
	_ = client.connection.SetWriteDeadline(time.Time{})
	if err != nil && !errors.Is(err, context.Canceled) {
		client.end(err)
	}
	return err
}

func (client *Client) end(err error) {
	client.endOnce.Do(func() {
		if err == nil {
			err = io.EOF
		}
		client.stateMu.Lock()
		client.ended = err
		pending := client.pending
		client.pending = make(map[string]chan clientResponse)
		client.stateMu.Unlock()
		_ = client.connection.Close()
		for _, waiter := range pending {
			waiter <- clientResponse{err: err}
		}
		client.messages.finish(err)
		close(client.done)
	})
}
