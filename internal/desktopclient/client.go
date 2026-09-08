package desktopclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/daemon"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/githubpr"
)

type EventKind string

const (
	EventRuntime     EventKind = "runtime"
	EventPullRequest EventKind = "pull_request"
	EventTerminal    EventKind = "terminal"
	EventDaemon      EventKind = "daemon"
	EventConnection  EventKind = "connection"
	EventSnapshot    EventKind = "snapshot"
)

type ConnectionState string

const (
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionReconnecting ConnectionState = "reconnecting"
	ConnectionResyncing    ConnectionState = "resyncing"
	ConnectionOffline      ConnectionState = "offline"
)

type ConnectionProjection struct {
	State        ConnectionState `json:"state"`
	DaemonEpoch  string          `json:"daemonEpoch,omitempty"`
	WireSequence uint64          `json:"wireSequence"`
	Error        string          `json:"error,omitempty"`
}

type BinaryEvent struct {
	Metadata desktopipc.BinaryMetadata `json:"metadata"`
	Data     []byte                    `json:"-"`
}

type Event struct {
	Kind        EventKind                  `json:"kind"`
	Epoch       string                     `json:"epoch,omitempty"`
	WireCursor  uint64                     `json:"wireCursor"`
	Channel     desktopipc.Channel         `json:"channel,omitempty"`
	Runtime     *desktop.Event             `json:"runtime,omitempty"`
	PullRequest *githubpr.MonitorState     `json:"pullRequest,omitempty"`
	Terminal    *desktop.TerminalEvent     `json:"terminal,omitempty"`
	Daemon      json.RawMessage            `json:"daemon,omitempty"`
	Binary      *BinaryEvent               `json:"binary,omitempty"`
	Connection  *ConnectionProjection      `json:"connection,omitempty"`
	Snapshot    *desktop.ReconnectSnapshot `json:"snapshot,omitempty"`
}

var ErrConnectionUnavailable = errors.New("workspace daemon connection is unavailable")

type WorkspaceClient struct {
	ctx     context.Context
	cancel  context.CancelFunc
	options daemon.ClientOptions

	mu              sync.Mutex
	client          *desktopipc.Client
	endpoint        desktopipc.Endpoint
	ack             desktopipc.HelloAck
	selectedSession string
	deliveredCursor uint64
	epoch           string
	state           ConnectionProjection
	lastConnection  *ConnectionProjection
	readerStarted   bool
	closed          bool

	applyMu sync.Mutex
	events  chan Event
	done    chan struct{}
	close   sync.Once
}

func NewWorkspaceClient(parent context.Context, options daemon.ClientOptions) (*WorkspaceClient, error) {
	if parent == nil {
		parent = context.Background()
	}
	connectCtx, cancel := context.WithCancel(parent)
	connection, endpoint, ack, err := daemon.ConnectOrStart(connectCtx, options)
	if err != nil {
		cancel()
		return nil, err
	}
	return newWorkspaceClientWithConnection(connectCtx, cancel, options, connection, endpoint, ack), nil
}

func newWorkspaceClientWithConnection(
	ctx context.Context,
	cancel context.CancelFunc,
	options daemon.ClientOptions,
	connection *desktopipc.Client,
	endpoint desktopipc.Endpoint,
	ack desktopipc.HelloAck,
) *WorkspaceClient {
	workspace := &WorkspaceClient{
		ctx: ctx, cancel: cancel, options: options,
		client: connection, endpoint: endpoint, ack: ack, epoch: ack.DaemonEpoch,
		state:  ConnectionProjection{State: ConnectionConnecting, DaemonEpoch: ack.DaemonEpoch, WireSequence: ack.CurrentSequence},
		events: make(chan Event, 256), done: make(chan struct{}),
	}
	workspace.options.Workspace = endpoint.Workspace
	if workspace.options.StateDir == "" {
		workspace.options.StateDir = endpointStateDirectory(endpoint)
	}
	return workspace
}

func (client *WorkspaceClient) InitialSnapshot(ctx context.Context, sessionID string) (desktop.ReconnectSnapshot, error) {
	if client == nil {
		return desktop.ReconnectSnapshot{}, ErrConnectionUnavailable
	}
	client.applyMu.Lock()
	snapshot, err := client.requestSnapshot(ctx, sessionID)
	if err == nil {
		err = client.applySnapshotLocked(snapshot)
	}
	client.applyMu.Unlock()
	if err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	client.mu.Lock()
	startReader := !client.readerStarted
	if startReader {
		client.readerStarted = true
	}
	client.mu.Unlock()
	if startReader {
		go client.supervise()
	}
	client.emitConnection(ConnectionConnected, "")
	return snapshot, nil
}

func (client *WorkspaceClient) Next(ctx context.Context) (Event, error) {
	if client == nil {
		return Event{}, ErrConnectionUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case event := <-client.events:
		return event, nil
	case <-client.done:
		return Event{}, ErrConnectionUnavailable
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
}

func (client *WorkspaceClient) Request(ctx context.Context, method desktopipc.Method, payload any, target any) error {
	connection, state, err := client.connectedClient()
	if err != nil {
		return err
	}
	if state != ConnectionConnected {
		return fmt.Errorf("%w: %s", ErrConnectionUnavailable, state)
	}
	err = connection.Request(ctx, method, payload, target)
	var protocolErr *desktopipc.ProtocolRequestError
	if errors.As(err, &protocolErr) && (protocolErr.Code == "resync_required" || protocolErr.Code == "revision_conflict") {
		client.emitConnection(ConnectionResyncing, protocolErr.Error())
	}
	return err
}

func (client *WorkspaceClient) UploadAttachment(ctx context.Context, sessionID, name, mimeType string, data []byte) (desktop.Attachment, error) {
	connection, state, err := client.connectedClient()
	if err != nil {
		return desktop.Attachment{}, err
	}
	if state != ConnectionConnected {
		return desktop.Attachment{}, fmt.Errorf("%w: %s", ErrConnectionUnavailable, state)
	}
	return connection.UploadAttachment(ctx, sessionID, name, mimeType, data)
}

func (client *WorkspaceClient) SelectSession(ctx context.Context, sessionID string) (desktop.ReconnectSnapshot, error) {
	client.applyMu.Lock()
	defer client.applyMu.Unlock()
	connection, state, err := client.connectedClient()
	if err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	if state != ConnectionConnected {
		return desktop.ReconnectSnapshot{}, fmt.Errorf("%w: %s", ErrConnectionUnavailable, state)
	}
	var snapshot desktop.ReconnectSnapshot
	if err := connection.Request(ctx, desktopipc.MethodResumeSession, map[string]string{"sessionId": sessionID}, &snapshot); err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	if err := client.applySnapshotLocked(snapshot); err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	return snapshot, nil
}

func (client *WorkspaceClient) SelectSessionFast(ctx context.Context, sessionID string) (desktop.SessionSelectionSnapshot, error) {
	return client.requestSessionSelection(ctx, desktopipc.MethodSelectSession, map[string]string{"sessionId": sessionID})
}

func (client *WorkspaceClient) CreateSession(ctx context.Context, title string) (desktop.SessionSelectionSnapshot, error) {
	return client.requestSessionSelection(ctx, desktopipc.MethodCreateSession, map[string]string{"title": title})
}

func (client *WorkspaceClient) requestSessionSelection(ctx context.Context, method desktopipc.Method, payload any) (desktop.SessionSelectionSnapshot, error) {
	client.applyMu.Lock()
	defer client.applyMu.Unlock()
	connection, state, err := client.connectedClient()
	if err != nil {
		return desktop.SessionSelectionSnapshot{}, err
	}
	if state != ConnectionConnected {
		return desktop.SessionSelectionSnapshot{}, fmt.Errorf("%w: %s", ErrConnectionUnavailable, state)
	}
	var snapshot desktop.SessionSelectionSnapshot
	if err := connection.Request(ctx, method, payload, &snapshot); err != nil {
		return desktop.SessionSelectionSnapshot{}, err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if snapshot.DaemonEpoch == "" || snapshot.DaemonEpoch != client.epoch || snapshot.SelectedSessionID == "" || snapshot.Session == nil {
		return desktop.SessionSelectionSnapshot{}, errors.New("session selection snapshot does not match the connected daemon")
	}
	client.selectedSession = snapshot.SelectedSessionID
	return snapshot, nil
}

func (client *WorkspaceClient) RebindWorkspace(ctx context.Context, workspace, sessionID string) (desktop.ReconnectSnapshot, error) {
	if client == nil {
		return desktop.ReconnectSnapshot{}, ErrConnectionUnavailable
	}
	client.emitConnection(ConnectionReconnecting, "")
	client.mu.Lock()
	options := client.options
	oldConnection := client.client
	client.mu.Unlock()
	options.Workspace = workspace
	connection, endpoint, ack, err := daemon.ConnectOrStart(ctx, options)
	if err != nil {
		client.emitConnection(ConnectionConnected, "")
		return desktop.ReconnectSnapshot{}, err
	}
	var snapshot desktop.ReconnectSnapshot
	if err := connection.Request(ctx, desktopipc.MethodReconnectSnapshot, map[string]any{
		"sessionId": sessionID, "refresh": false,
	}, &snapshot); err != nil {
		_ = connection.Close()
		client.emitConnection(ConnectionConnected, "")
		return desktop.ReconnectSnapshot{}, err
	}
	if snapshot.DaemonEpoch == "" || snapshot.DaemonEpoch != ack.DaemonEpoch {
		_ = connection.Close()
		client.emitConnection(ConnectionConnected, "")
		return desktop.ReconnectSnapshot{}, errors.New("workspace rebind snapshot does not match daemon epoch")
	}
	client.applyMu.Lock()
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		client.applyMu.Unlock()
		_ = connection.Close()
		return desktop.ReconnectSnapshot{}, ErrConnectionUnavailable
	}
	client.options = options
	client.client, client.endpoint, client.ack = connection, endpoint, ack
	client.epoch = ack.DaemonEpoch
	client.mu.Unlock()
	err = client.applySnapshotLocked(snapshot)
	client.applyMu.Unlock()
	if err != nil {
		_ = connection.Close()
		client.mu.Lock()
		client.client = oldConnection
		client.mu.Unlock()
		client.emitConnection(ConnectionConnected, "")
		return desktop.ReconnectSnapshot{}, err
	}
	if oldConnection != nil {
		_ = oldConnection.Close()
	}
	client.emit(Event{Kind: EventSnapshot, Epoch: snapshot.DaemonEpoch, WireCursor: snapshot.WireSequence, Snapshot: &snapshot})
	client.emitConnection(ConnectionConnected, "")
	return snapshot, nil
}

func (client *WorkspaceClient) Retry(ctx context.Context) (desktop.ReconnectSnapshot, error) {
	if client == nil {
		return desktop.ReconnectSnapshot{}, ErrConnectionUnavailable
	}
	if err := client.reconnectOnce(ctx, true); err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	client.mu.Lock()
	sessionID := client.selectedSession
	client.mu.Unlock()
	client.applyMu.Lock()
	defer client.applyMu.Unlock()
	snapshot, err := client.requestSnapshot(ctx, sessionID)
	if err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	if err := client.applySnapshotLocked(snapshot); err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	client.emit(Event{Kind: EventSnapshot, Epoch: snapshot.DaemonEpoch, WireCursor: snapshot.WireSequence, Snapshot: &snapshot})
	client.emitConnection(ConnectionConnected, "")
	return snapshot, nil
}

func (client *WorkspaceClient) Close() error {
	if client == nil {
		return nil
	}
	var closeErr error
	client.close.Do(func() {
		client.mu.Lock()
		client.closed = true
		connection := client.client
		client.mu.Unlock()
		client.cancel()
		if connection != nil {
			closeErr = connection.Close()
		}
		close(client.done)
	})
	return closeErr
}

func (client *WorkspaceClient) supervise() {
	for {
		connection, _, err := client.connectedClient()
		if err != nil {
			return
		}
		message, err := connection.Next(client.ctx)
		if client.ctx.Err() != nil {
			return
		}
		if !client.isCurrentConnection(connection) {
			continue
		}
		if err != nil || message.Kind == desktopipc.ClientMessageDisconnected {
			if message.Err != nil {
				err = message.Err
			}
			if reconnectErr := client.reconnect(err); reconnectErr != nil && client.ctx.Err() == nil {
				client.emitConnection(ConnectionOffline, reconnectErr.Error())
			}
			continue
		}
		switch message.Kind {
		case desktopipc.ClientMessageResyncRequired:
			client.mu.Lock()
			covered := message.Sequence != 0 && message.Sequence <= client.deliveredCursor
			client.mu.Unlock()
			if !covered {
				if err := client.resync(); err != nil {
					client.emitConnection(ConnectionOffline, err.Error())
				}
			}
		case desktopipc.ClientMessageReplayComplete:
			client.emitConnection(ConnectionConnected, "")
		case desktopipc.ClientMessageEvent, desktopipc.ClientMessageBinary:
			if err := client.deliver(message); err != nil {
				if resyncErr := client.resync(); resyncErr != nil {
					client.emitConnection(ConnectionOffline, errors.Join(err, resyncErr).Error())
				}
			}
		}
	}
}

func (client *WorkspaceClient) deliver(message desktopipc.ClientMessage) error {
	client.applyMu.Lock()
	defer client.applyMu.Unlock()
	client.mu.Lock()
	cursor := client.deliveredCursor
	epoch := client.epoch
	client.mu.Unlock()
	if message.Sequence <= cursor {
		return nil
	}
	event := Event{Epoch: epoch, WireCursor: message.Sequence}
	if message.Kind == desktopipc.ClientMessageBinary {
		if message.Binary.Channel != desktopipc.ChannelTerminal {
			return fmt.Errorf("unexpected binary event channel %q", message.Binary.Channel)
		}
		event.Kind, event.Channel = EventTerminal, desktopipc.ChannelTerminal
		event.Binary = &BinaryEvent{Metadata: message.Binary, Data: append([]byte(nil), message.Data...)}
	} else {
		event.Channel = message.Event.Channel
		switch message.Event.Channel {
		case desktopipc.ChannelRuntime:
			var payload desktop.Event
			if err := json.Unmarshal(message.Event.Payload, &payload); err != nil {
				return err
			}
			event.Kind, event.Runtime = EventRuntime, &payload
		case desktopipc.ChannelPullRequest:
			var payload githubpr.MonitorState
			if err := json.Unmarshal(message.Event.Payload, &payload); err != nil {
				return err
			}
			event.Kind, event.PullRequest = EventPullRequest, &payload
		case desktopipc.ChannelTerminal:
			var payload desktop.TerminalEvent
			if err := json.Unmarshal(message.Event.Payload, &payload); err != nil {
				return err
			}
			event.Kind, event.Terminal = EventTerminal, &payload
		case desktopipc.ChannelDaemon:
			event.Kind = EventDaemon
			event.Daemon = append(json.RawMessage(nil), message.Event.Payload...)
		default:
			return fmt.Errorf("unexpected workspace event channel %q", message.Event.Channel)
		}
	}
	client.mu.Lock()
	client.deliveredCursor = message.Sequence
	client.state.WireSequence = message.Sequence
	client.mu.Unlock()
	client.emit(event)
	return nil
}

func (client *WorkspaceClient) resync() error {
	client.emitConnection(ConnectionResyncing, "")
	client.applyMu.Lock()
	defer client.applyMu.Unlock()
	client.mu.Lock()
	sessionID := client.selectedSession
	client.mu.Unlock()
	snapshot, err := client.requestSnapshot(client.ctx, sessionID)
	if err != nil {
		return err
	}
	if err := client.applySnapshotLocked(snapshot); err != nil {
		return err
	}
	client.emit(Event{Kind: EventSnapshot, Epoch: snapshot.DaemonEpoch, WireCursor: snapshot.WireSequence, Snapshot: &snapshot})
	client.emitConnection(ConnectionConnected, "")
	return nil
}

func (client *WorkspaceClient) reconnect(cause error) error {
	client.emitConnection(ConnectionReconnecting, errorText(cause))
	backoff := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second}
	attempt := 0
	for client.ctx.Err() == nil {
		delay := backoff[min(attempt, len(backoff)-1)]
		timer := time.NewTimer(delay)
		select {
		case <-client.ctx.Done():
			timer.Stop()
			return client.ctx.Err()
		case <-timer.C:
		}
		attemptCtx, cancel := context.WithTimeout(client.ctx, 8*time.Second)
		err := client.reconnectOnce(attemptCtx, false)
		cancel()
		if err == nil {
			return nil
		}
		attempt++
		if attempt >= len(backoff) {
			client.emitConnection(ConnectionOffline, err.Error())
		}
	}
	return client.ctx.Err()
}

func (client *WorkspaceClient) reconnectOnce(ctx context.Context, forceSnapshot bool) error {
	client.mu.Lock()
	oldEpoch := client.epoch
	lastCursor := client.deliveredCursor
	oldClient := client.client
	client.mu.Unlock()
	if oldClient != nil {
		_ = oldClient.Close()
	}
	connection, endpoint, ack, err := daemon.ConnectOrStart(ctx, client.options)
	if err != nil {
		return err
	}
	if !forceSnapshot && ack.DaemonEpoch == oldEpoch && lastCursor > 0 {
		_ = connection.Close()
		connection, ack, err = desktopipc.Connect(ctx, endpoint, client.options.ClientID, lastCursor)
		if err != nil {
			return err
		}
	}
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		_ = connection.Close()
		return ErrConnectionUnavailable
	}
	client.client, client.endpoint, client.ack = connection, endpoint, ack
	client.epoch = ack.DaemonEpoch
	client.mu.Unlock()
	if !forceSnapshot && ack.DaemonEpoch == oldEpoch && ack.ReplayAvailable {
		client.emitConnection(ConnectionConnected, "")
		return nil
	}
	return client.resync()
}

func (client *WorkspaceClient) requestSnapshot(ctx context.Context, sessionID string) (desktop.ReconnectSnapshot, error) {
	connection, _, err := client.connectedClient()
	if err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	var snapshot desktop.ReconnectSnapshot
	if err := connection.Request(ctx, desktopipc.MethodReconnectSnapshot, map[string]any{"sessionId": sessionID, "refresh": false}, &snapshot); err != nil {
		return desktop.ReconnectSnapshot{}, err
	}
	return snapshot, nil
}

func (client *WorkspaceClient) applySnapshotLocked(snapshot desktop.ReconnectSnapshot) error {
	if snapshot.DaemonEpoch == "" {
		return errors.New("reconnect snapshot omitted daemon epoch")
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.ack.DaemonEpoch != "" && snapshot.DaemonEpoch != client.ack.DaemonEpoch {
		return errors.New("reconnect snapshot daemon epoch does not match connection")
	}
	client.epoch = snapshot.DaemonEpoch
	client.deliveredCursor = snapshot.WireSequence
	client.selectedSession = snapshot.SelectedSessionID
	client.state = ConnectionProjection{State: ConnectionConnected, DaemonEpoch: snapshot.DaemonEpoch, WireSequence: snapshot.WireSequence}
	return nil
}

func (client *WorkspaceClient) connectedClient() (*desktopipc.Client, ConnectionState, error) {
	if client == nil {
		return nil, ConnectionOffline, ErrConnectionUnavailable
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed || client.client == nil {
		return nil, client.state.State, ErrConnectionUnavailable
	}
	return client.client, client.state.State, nil
}

func (client *WorkspaceClient) isCurrentConnection(connection *desktopipc.Client) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return !client.closed && client.client == connection
}

func (client *WorkspaceClient) emitConnection(state ConnectionState, detail string) {
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return
	}
	client.state.State = state
	client.state.DaemonEpoch = client.epoch
	client.state.WireSequence = client.deliveredCursor
	client.state.Error = detail
	projection := client.state
	if client.lastConnection != nil && *client.lastConnection == projection {
		client.mu.Unlock()
		return
	}
	client.lastConnection = &projection
	client.mu.Unlock()
	client.emit(Event{Kind: EventConnection, Epoch: projection.DaemonEpoch, WireCursor: projection.WireSequence, Connection: &projection})
}

func (client *WorkspaceClient) emit(event Event) {
	select {
	case client.events <- event:
	case <-client.ctx.Done():
	}
}

func endpointStateDirectory(endpoint desktopipc.Endpoint) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(endpoint.TokenFile)))
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
