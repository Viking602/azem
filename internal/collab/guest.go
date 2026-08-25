package collab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/session"
)

type Replica struct {
	Session  session.Session     `json:"session"`
	Tree     session.SessionTree `json:"tree"`
	Blocks   []session.Block     `json:"blocks"`
	ReadOnly bool                `json:"readOnly,omitempty"`
}

type GuestEvent struct {
	Kind    string
	Replica *Replica
	Block   *session.Block
	Event   json.RawMessage
	State   json.RawMessage
	Message string
}

type GuestOptions struct {
	Name       string
	HTTPClient *http.Client
}

type pendingReplica struct {
	replica  Replica
	expected int
}

type Guest struct {
	options GuestOptions
	socket  *Socket
	link    Link

	mu      sync.RWMutex
	replica Replica
	pending *pendingReplica
	ready   chan error
	events  chan GuestEvent
	ctx     context.Context
	cancel  context.CancelFunc
	stop    sync.Once
	joined  bool
}

func NewGuest(options GuestOptions) *Guest {
	return &Guest{options: options, ready: make(chan error, 1), events: make(chan GuestEvent, 512)}
}

func (guest *Guest) Join(ctx context.Context, rawLink string) error {
	parsed, err := ParseLink(rawLink)
	if err != nil {
		return err
	}
	socket, err := NewSocket(SocketOptions{URL: parsed.WebSocketURL, Role: RoleGuest, Key: parsed.Key, HTTPClient: guest.options.HTTPClient})
	if err != nil {
		return err
	}
	guest.ctx, guest.cancel = context.WithCancel(ctx)
	guest.link, guest.socket = parsed, socket
	go guest.run()
	socket.Start(guest.ctx)
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case err := <-guest.ready:
		if err != nil {
			guest.Leave("join failed")
		}
		return err
	case <-timer.C:
		guest.Leave("welcome timeout")
		return errors.New("timed out waiting for collaboration welcome")
	case <-ctx.Done():
		guest.Leave("cancelled")
		return ctx.Err()
	}
}

func (guest *Guest) Events() <-chan GuestEvent { return guest.events }

func (guest *Guest) Snapshot() Replica {
	guest.mu.RLock()
	defer guest.mu.RUnlock()
	result := guest.replica
	result.Blocks = append([]session.Block(nil), result.Blocks...)
	return result
}

func (guest *Guest) SendPrompt(ctx context.Context, text string) error {
	guest.mu.RLock()
	readOnly, joined := guest.replica.ReadOnly, guest.joined
	guest.mu.RUnlock()
	if !joined {
		return errors.New("collaboration guest has not joined")
	}
	if readOnly {
		return errors.New("collaboration link is read-only")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("collaboration prompt is empty")
	}
	return guest.socket.Send(ctx, Frame{Type: "prompt", Text: text}, 0)
}

func (guest *Guest) SendAbort(ctx context.Context) error {
	guest.mu.RLock()
	readOnly, joined := guest.replica.ReadOnly, guest.joined
	guest.mu.RUnlock()
	if !joined {
		return errors.New("collaboration guest has not joined")
	}
	if readOnly {
		return errors.New("collaboration link is read-only")
	}
	return guest.socket.Send(ctx, Frame{Type: "abort"}, 0)
}

func (guest *Guest) Leave(_ string) {
	guest.stop.Do(func() {
		if guest.socket != nil {
			guest.socket.Close()
		}
		if guest.cancel != nil {
			guest.cancel()
		}
	})
}

func (guest *Guest) run() {
	defer close(guest.events)
	for event := range guest.socket.Events() {
		switch event.Kind {
		case "open":
			writeToken := ""
			if len(guest.link.WriteToken) > 0 {
				writeToken = base64.RawURLEncoding.EncodeToString(guest.link.WriteToken)
			}
			name := strings.TrimSpace(guest.options.Name)
			if runes := []rune(name); len(runes) > 64 {
				name = string(runes[:64])
			}
			_ = guest.socket.Send(guest.ctx, Frame{Type: "hello", Proto: ProtocolVersion, Name: name, WriteToken: writeToken}, 0)
		case "frame":
			guest.handleFrame(event.Frame)
		case "closed":
			if !event.WillReconnect {
				guest.failJoin(errors.New(event.Reason))
				guest.emit(GuestEvent{Kind: "closed", Message: event.Reason})
			}
		}
	}
}

func (guest *Guest) handleFrame(frame Frame) {
	switch frame.Type {
	case "welcome":
		if frame.Proto != ProtocolVersion || frame.Session == nil || frame.Tree == nil || frame.EntryCount < 0 {
			guest.failJoin(errors.New("collaboration welcome is malformed or uses an unsupported protocol"))
			return
		}
		guest.mu.Lock()
		guest.pending = &pendingReplica{replica: Replica{Session: *frame.Session, Tree: *frame.Tree, ReadOnly: frame.ReadOnly}, expected: frame.EntryCount}
		guest.joined = false
		guest.mu.Unlock()
		if frame.EntryCount == 0 {
			guest.finalizePending()
		}
	case "snapshot-chunk":
		guest.mu.Lock()
		pending := guest.pending
		if pending != nil {
			pending.replica.Blocks = append(pending.replica.Blocks, frame.Blocks...)
		}
		complete := pending != nil && (frame.Final || len(pending.replica.Blocks) >= pending.expected)
		invalid := pending != nil && len(pending.replica.Blocks) > pending.expected
		guest.mu.Unlock()
		if invalid {
			guest.failJoin(errors.New("collaboration snapshot exceeded its declared entry count"))
			return
		}
		if complete {
			guest.finalizePending()
		}
	case "entry":
		if frame.Block == nil {
			return
		}
		block := *frame.Block
		guest.mu.Lock()
		if guest.joined {
			guest.replica.Blocks = append(guest.replica.Blocks, block)
		}
		joined := guest.joined
		guest.mu.Unlock()
		if joined {
			guest.emit(GuestEvent{Kind: "entry", Block: &block})
		}
	case "event":
		if json.Valid(frame.Event) {
			guest.emit(GuestEvent{Kind: "event", Event: append([]byte(nil), frame.Event...)})
		}
	case "state":
		if json.Valid(frame.State) {
			guest.emit(GuestEvent{Kind: "state", State: append([]byte(nil), frame.State...)})
		}
	case "error":
		err := errors.New(frame.Message)
		guest.failJoin(err)
		guest.emit(GuestEvent{Kind: "error", Message: frame.Message})
	case "bye":
		guest.emit(GuestEvent{Kind: "closed", Message: frame.Reason})
		guest.Leave(frame.Reason)
	}
}

func (guest *Guest) finalizePending() {
	guest.mu.Lock()
	pending := guest.pending
	if pending == nil {
		guest.mu.Unlock()
		return
	}
	if len(pending.replica.Blocks) != pending.expected {
		guest.pending = nil
		guest.mu.Unlock()
		guest.failJoin(errors.New("collaboration snapshot ended before all entries arrived"))
		return
	}
	guest.replica = pending.replica
	guest.pending = nil
	first := !guest.joined
	guest.joined = true
	result := guest.replica
	result.Blocks = append([]session.Block(nil), result.Blocks...)
	guest.mu.Unlock()
	if first {
		select {
		case guest.ready <- nil:
		default:
		}
	}
	guest.emit(GuestEvent{Kind: "snapshot", Replica: &result})
}

func (guest *Guest) failJoin(err error) {
	guest.mu.RLock()
	joined := guest.joined
	guest.mu.RUnlock()
	if joined {
		return
	}
	select {
	case guest.ready <- err:
	default:
	}
}

func (guest *Guest) emit(event GuestEvent) {
	select {
	case guest.events <- event:
	case <-guest.ctx.Done():
	}
}
