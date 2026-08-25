package collab

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/session"
)

type Participant struct {
	PeerID   uint32 `json:"peerId"`
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

type HostOptions struct {
	RelayURL   string
	WebURL     string
	SessionID  string
	Sessions   *session.Service
	HTTPClient *http.Client
	OnPrompt   func(ctx context.Context, peer Participant, text string) error
	OnAbort    func(ctx context.Context, peer Participant) error
}

type Host struct {
	options     HostOptions
	socket      *Socket
	key         []byte
	token       []byte
	roomID      string
	link        string
	viewLink    string
	webLink     string
	webViewLink string

	mu           sync.RWMutex
	participants map[uint32]Participant
	ready        chan error
	stop         sync.Once
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewHost(options HostOptions) (*Host, error) {
	if options.Sessions == nil || strings.TrimSpace(options.SessionID) == "" {
		return nil, errors.New("collaboration host requires a session service and session id")
	}
	if strings.TrimSpace(options.RelayURL) == "" {
		options.RelayURL = DefaultRelayURL
	}
	return &Host{options: options, participants: make(map[uint32]Participant), ready: make(chan error, 1)}, nil
}

func (host *Host) Start(ctx context.Context) error {
	roomID, key, token, err := GenerateRoom()
	if err != nil {
		return err
	}
	fullLink, err := FormatLink(host.options.RelayURL, roomID, key, token)
	if err != nil {
		return err
	}
	viewLink, err := FormatLink(host.options.RelayURL, roomID, key, nil)
	if err != nil {
		return err
	}
	webLink, err := FormatWebLink(host.options.RelayURL, host.options.WebURL, roomID, key, token)
	if err != nil {
		return err
	}
	webViewLink, err := FormatWebLink(host.options.RelayURL, host.options.WebURL, roomID, key, nil)
	if err != nil {
		return err
	}
	parsed, err := ParseLink(fullLink)
	if err != nil {
		return err
	}
	socket, err := NewSocket(SocketOptions{URL: parsed.WebSocketURL, Role: RoleHost, Key: key, HTTPClient: host.options.HTTPClient})
	if err != nil {
		return err
	}
	host.ctx, host.cancel = context.WithCancel(ctx)
	host.socket, host.key, host.token, host.roomID = socket, key, token, roomID
	host.link, host.viewLink, host.webLink, host.webViewLink = fullLink, viewLink, webLink, webViewLink
	go host.run()
	socket.Start(host.ctx)
	timer := time.NewTimer(connectTimeout)
	defer timer.Stop()
	select {
	case err := <-host.ready:
		if err != nil {
			host.Stop("connect failed")
		}
		return err
	case <-timer.C:
		host.Stop("connect timeout")
		return errors.New("timed out connecting collaboration host")
	case <-ctx.Done():
		host.Stop("cancelled")
		return ctx.Err()
	}
}

func (host *Host) Link() string        { return host.link }
func (host *Host) ViewLink() string    { return host.viewLink }
func (host *Host) WebLink() string     { return host.webLink }
func (host *Host) WebViewLink() string { return host.webViewLink }

func (host *Host) Participants() []Participant {
	host.mu.RLock()
	defer host.mu.RUnlock()
	result := make([]Participant, 0, len(host.participants))
	for _, participant := range host.participants {
		result = append(result, participant)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].PeerID < result[right].PeerID })
	return result
}

func (host *Host) BroadcastBlock(ctx context.Context, block session.Block) error {
	if host.socket == nil {
		return errors.New("collaboration host is not running")
	}
	block = sanitizeReplicatedBlock(block)
	return host.socket.Send(ctx, Frame{Type: "entry", Block: &block}, 0)
}

func (host *Host) BroadcastEvent(ctx context.Context, event json.RawMessage) error {
	if host.socket == nil || !json.Valid(event) {
		return errors.New("collaboration event is unavailable or malformed")
	}
	return host.socket.Send(ctx, Frame{Type: "event", Event: append([]byte(nil), event...)}, 0)
}

func (host *Host) BroadcastState(ctx context.Context, state json.RawMessage) error {
	if host.socket == nil || !json.Valid(state) {
		return errors.New("collaboration state is unavailable or malformed")
	}
	return host.socket.Send(ctx, Frame{Type: "state", State: append([]byte(nil), state...)}, 0)
}

func (host *Host) Stop(reason string) {
	host.stop.Do(func() {
		if host.socket != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = host.socket.Send(stopCtx, Frame{Type: "bye", Reason: strings.TrimSpace(reason)}, 0)
			cancel()
			host.socket.Close()
		}
		if host.cancel != nil {
			host.cancel()
		}
	})
}

func (host *Host) run() {
	opened := false
	for event := range host.socket.Events() {
		switch event.Kind {
		case "open":
			if !opened {
				opened = true
				host.ready <- nil
			}
		case "frame":
			host.handleFrame(event.Frame, event.PeerID)
		case "control":
			var control struct {
				Type string `json:"t"`
				Peer uint32 `json:"peer"`
			}
			if json.Unmarshal(event.Control, &control) == nil && control.Type == "peer-left" {
				host.mu.Lock()
				delete(host.participants, control.Peer)
				host.mu.Unlock()
			}
		case "closed":
			if !event.WillReconnect && !opened {
				select {
				case host.ready <- errors.New(event.Reason):
				default:
				}
			}
		}
	}
}

func (host *Host) handleFrame(frame Frame, peerID uint32) {
	switch frame.Type {
	case "hello":
		host.handleHello(frame, peerID)
	case "prompt", "abort":
		host.mu.RLock()
		participant, ok := host.participants[peerID]
		host.mu.RUnlock()
		if !ok || participant.ReadOnly {
			_ = host.sendError(peerID, frame.Type+" is disabled on a read-only link")
			return
		}
		if frame.Type == "prompt" && host.options.OnPrompt != nil {
			if err := host.options.OnPrompt(host.ctx, participant, frame.Text); err != nil {
				_ = host.sendError(peerID, err.Error())
			}
		} else if frame.Type == "abort" && host.options.OnAbort != nil {
			if err := host.options.OnAbort(host.ctx, participant); err != nil {
				_ = host.sendError(peerID, err.Error())
			}
		}
	}
}

func (host *Host) handleHello(frame Frame, peerID uint32) {
	if frame.Proto != ProtocolVersion {
		_ = host.sendError(peerID, fmt.Sprintf("protocol mismatch: host speaks v%d, guest sent v%d", ProtocolVersion, frame.Proto))
		return
	}
	name := strings.TrimSpace(frame.Name)
	if runes := []rune(name); len(runes) > 64 {
		name = string(runes[:64])
	}
	if name == "" {
		name = fmt.Sprintf("guest-%d", peerID)
	}
	presented, err := base64.RawURLEncoding.DecodeString(frame.WriteToken)
	canWrite := err == nil && len(presented) == len(host.token) && subtle.ConstantTimeCompare(presented, host.token) == 1
	participant := Participant{PeerID: peerID, Name: name, ReadOnly: !canWrite}
	host.mu.Lock()
	host.participants[peerID] = participant
	host.mu.Unlock()
	snapshot, err := host.options.Sessions.LoadExportSnapshot(host.ctx, host.options.SessionID)
	if err != nil {
		_ = host.sendError(peerID, "load collaboration snapshot: "+err.Error())
		return
	}
	blocks := make([]session.Block, len(snapshot.ActiveBlocks))
	for index, block := range snapshot.ActiveBlocks {
		blocks[index] = sanitizeReplicatedBlock(block)
	}
	metadata, tree := snapshot.Session, snapshot.Tree
	if err := host.socket.Send(host.ctx, Frame{Type: "welcome", Proto: ProtocolVersion, Session: &metadata, Tree: &tree, EntryCount: len(blocks), ReadOnly: !canWrite}, peerID); err != nil {
		return
	}
	host.sendSnapshotChunks(blocks, peerID)
}

func (host *Host) sendSnapshotChunks(blocks []session.Block, peerID uint32) {
	if len(blocks) == 0 {
		_ = host.socket.Send(host.ctx, Frame{Type: "snapshot-chunk", Final: true}, peerID)
		return
	}
	for start := 0; start < len(blocks); {
		end := start
		bytes := 0
		for end < len(blocks) {
			encoded, _ := json.Marshal(blocks[end])
			if end > start && bytes+len(encoded) > SnapshotChunkBytes {
				break
			}
			bytes += len(encoded)
			end++
		}
		chunk := append([]session.Block(nil), blocks[start:end]...)
		if host.socket.Send(host.ctx, Frame{Type: "snapshot-chunk", Blocks: chunk, Final: end == len(blocks)}, peerID) != nil {
			return
		}
		start = end
	}
}

func (host *Host) sendError(peerID uint32, message string) error {
	return host.socket.Send(host.ctx, Frame{Type: "error", Message: message}, peerID)
}

func sanitizeReplicatedBlock(block session.Block) session.Block {
	block.Attachments = append([]session.Attachment(nil), block.Attachments...)
	for index := range block.Attachments {
		block.Attachments[index].Path = ""
	}
	return block
}
