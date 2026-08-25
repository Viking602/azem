package collab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	maxPendingFrames = 256
	connectTimeout   = 10 * time.Second
	writeTimeout     = 10 * time.Second
)

type Role string

const (
	RoleHost  Role = "host"
	RoleGuest Role = "guest"
)

type SocketEvent struct {
	Kind          string
	Frame         Frame
	PeerID        uint32
	Control       json.RawMessage
	Reason        string
	WillReconnect bool
}

type SocketOptions struct {
	URL        string
	Role       Role
	Key        []byte
	HTTPClient *http.Client
	Backoff    func(attempt int) time.Duration
}

type outgoingFrame struct {
	peerID uint32
	frame  Frame
}

type socketRead struct {
	kind    websocket.MessageType
	payload []byte
	err     error
}

type Socket struct {
	options SocketOptions
	send    chan outgoingFrame
	events  chan SocketEvent
	cancel  context.CancelFunc
	done    chan struct{}
	start   sync.Once
	stop    sync.Once
}

func NewSocket(options SocketOptions) (*Socket, error) {
	if options.Role != RoleHost && options.Role != RoleGuest {
		return nil, errors.New("collaboration socket role must be host or guest")
	}
	if len(options.Key) != RoomKeyBytes {
		return nil, fmt.Errorf("collaboration room key must contain %d bytes", RoomKeyBytes)
	}
	parsed, err := url.Parse(options.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Fragment != "" {
		return nil, errors.New("collaboration socket URL must be absolute ws:// or wss://")
	}
	if parsed.Scheme == "ws" && !isLocalHost(parsed.Hostname()) {
		return nil, errors.New("collaboration socket must use WSS outside localhost")
	}
	options.Key = append([]byte(nil), options.Key...)
	if options.Backoff == nil {
		options.Backoff = func(attempt int) time.Duration {
			delay := time.Second << min(attempt, 5)
			if delay > 30*time.Second {
				return 30 * time.Second
			}
			return delay
		}
	}
	return &Socket{options: options, send: make(chan outgoingFrame, maxPendingFrames), events: make(chan SocketEvent, 512), done: make(chan struct{})}, nil
}

func (socket *Socket) Start(ctx context.Context) {
	socket.start.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		socket.cancel = cancel
		go socket.run(runCtx)
	})
}

func (socket *Socket) Send(ctx context.Context, frame Frame, peerID uint32) error {
	if frame.Type == "" {
		return errors.New("collaboration frame type is required")
	}
	select {
	case <-socket.done:
		return errors.New("collaboration socket is closed")
	case socket.send <- outgoingFrame{peerID: peerID, frame: frame}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errors.New("collaboration reconnect buffer is full")
	}
}

func (socket *Socket) Events() <-chan SocketEvent { return socket.events }
func (socket *Socket) Done() <-chan struct{}      { return socket.done }

func (socket *Socket) Close() {
	socket.stop.Do(func() {
		if socket.cancel != nil {
			socket.cancel()
		}
	})
}

func (socket *Socket) run(ctx context.Context) {
	defer close(socket.done)
	defer close(socket.events)
	pending := make([][]byte, 0, maxPendingFrames)
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		conn, err := socket.connect(ctx)
		if err != nil {
			if !socket.waitReconnect(ctx, &pending, attempt, err.Error()) {
				return
			}
			attempt++
			continue
		}
		attempt = 0
		if !socket.emit(ctx, SocketEvent{Kind: "open"}) {
			_ = conn.CloseNow()
			return
		}
		reason, fatal := socket.runConnection(ctx, conn, &pending)
		_ = conn.CloseNow()
		if fatal {
			_ = socket.emit(ctx, SocketEvent{Kind: "closed", Reason: reason})
			return
		}
		if !socket.waitReconnect(ctx, &pending, attempt, reason) {
			return
		}
		attempt++
	}
}

func (socket *Socket) connect(ctx context.Context) (*websocket.Conn, error) {
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	parsed, _ := url.Parse(socket.options.URL)
	query := parsed.Query()
	query.Set("role", string(socket.options.Role))
	parsed.RawQuery = query.Encode()
	httpClient := socket.options.HTTPClient
	if httpClient != nil {
		copyClient := *httpClient
		copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		httpClient = &copyClient
	}
	conn, response, err := websocket.Dial(connectCtx, parsed.String(), &websocket.DialOptions{HTTPClient: httpClient})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxFrameBytes)
	return conn, nil
}

func (socket *Socket) runConnection(ctx context.Context, conn *websocket.Conn, pending *[][]byte) (string, bool) {
	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	reads := make(chan socketRead, 1)
	go func() {
		for {
			kind, payload, err := conn.Read(readCtx)
			select {
			case reads <- socketRead{kind: kind, payload: payload, err: err}:
			case <-readCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		if len(*pending) > 0 {
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(writeCtx, websocket.MessageBinary, (*pending)[0])
			cancel()
			if err != nil {
				return closeReason(err)
			}
			*pending = (*pending)[1:]
			continue
		}
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "closed")
			return "closed", true
		case outgoing := <-socket.send:
			sealed, err := SealFrame(socket.options.Key, outgoing.frame)
			if err != nil {
				_ = socket.emit(ctx, SocketEvent{Kind: "error", Reason: err.Error()})
				continue
			}
			envelope, err := PackEnvelope(outgoing.peerID, sealed)
			if err != nil {
				_ = socket.emit(ctx, SocketEvent{Kind: "error", Reason: err.Error()})
				continue
			}
			*pending = append(*pending, envelope)
		case result := <-reads:
			if result.err != nil {
				return closeReason(result.err)
			}
			if result.kind == websocket.MessageText {
				if !json.Valid(result.payload) {
					continue
				}
				if !socket.emit(ctx, SocketEvent{Kind: "control", Control: append([]byte(nil), result.payload...)}) {
					return "closed", true
				}
				continue
			}
			if result.kind != websocket.MessageBinary {
				continue
			}
			envelope, err := UnpackEnvelope(result.payload)
			if err != nil {
				return err.Error(), true
			}
			frame, err := OpenFrame(socket.options.Key, envelope.Payload)
			if err != nil {
				return "bad key or corrupted frame", true
			}
			if !socket.emit(ctx, SocketEvent{Kind: "frame", Frame: frame, PeerID: envelope.PeerID}) {
				return "closed", true
			}
		}
	}
}

func (socket *Socket) waitReconnect(ctx context.Context, pending *[][]byte, attempt int, reason string) bool {
	if fatalCloseReason(reason) {
		_ = socket.emit(ctx, SocketEvent{Kind: "closed", Reason: reason})
		return false
	}
	if !socket.emit(ctx, SocketEvent{Kind: "closed", Reason: reason, WillReconnect: true}) {
		return false
	}
	delay := socket.options.Backoff(attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case outgoing := <-socket.send:
			sealed, err := SealFrame(socket.options.Key, outgoing.frame)
			if err != nil {
				continue
			}
			envelope, err := PackEnvelope(outgoing.peerID, sealed)
			if err != nil {
				continue
			}
			if len(*pending) < maxPendingFrames {
				*pending = append(*pending, envelope)
			}
		}
	}
}

func (socket *Socket) emit(ctx context.Context, event SocketEvent) bool {
	select {
	case socket.events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func closeReason(err error) (string, bool) {
	status := websocket.CloseStatus(err)
	switch int(status) {
	case 4001:
		return "room closed", true
	case 4004:
		return "no such room", true
	case 4009:
		return "a host is already connected for this room", true
	case 4029:
		return "room is full", true
	}
	if status != -1 {
		return "connection closed (code " + strconv.Itoa(int(status)) + ")", false
	}
	return err.Error(), false
}

func fatalCloseReason(reason string) bool {
	return reason == "room closed" || reason == "no such room" || reason == "a host is already connected for this room" || reason == "room is full"
}
