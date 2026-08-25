package collab

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	"github.com/coder/websocket"
)

type relayConnection struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (connection *relayConnection) write(ctx context.Context, kind websocket.MessageType, payload []byte) error {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return connection.conn.Write(writeCtx, kind, payload)
}

type inMemoryRelay struct {
	mu        sync.Mutex
	host      *relayConnection
	guests    map[uint32]*relayConnection
	nextPeer  uint32
	cipherLog [][]byte
}

func newInMemoryRelay() *inMemoryRelay {
	return &inMemoryRelay{guests: make(map[uint32]*relayConnection), nextPeer: 1}
}

func (relay *inMemoryRelay) serve(writer http.ResponseWriter, request *http.Request) {
	conn, err := websocket.Accept(writer, request, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxFrameBytes)
	wrapped := &relayConnection{conn: conn}
	role := request.URL.Query().Get("role")
	var peerID uint32
	relay.mu.Lock()
	if role == string(RoleHost) {
		if relay.host != nil {
			relay.mu.Unlock()
			_ = conn.Close(websocket.StatusCode(4009), "host exists")
			return
		}
		relay.host = wrapped
	} else {
		peerID = relay.nextPeer
		relay.nextPeer++
		relay.guests[peerID] = wrapped
	}
	relay.mu.Unlock()
	defer func() {
		relay.mu.Lock()
		if role == string(RoleHost) {
			relay.host = nil
		} else {
			delete(relay.guests, peerID)
		}
		host := relay.host
		relay.mu.Unlock()
		if role != string(RoleHost) && host != nil {
			control, _ := json.Marshal(map[string]any{"t": "peer-left", "peer": peerID})
			_ = host.write(context.Background(), websocket.MessageText, control)
		}
		_ = conn.CloseNow()
	}()
	for {
		kind, payload, err := conn.Read(request.Context())
		if err != nil {
			return
		}
		if kind != websocket.MessageBinary {
			continue
		}
		relay.mu.Lock()
		relay.cipherLog = append(relay.cipherLog, append([]byte(nil), payload...))
		host := relay.host
		guests := make(map[uint32]*relayConnection, len(relay.guests))
		for id, guest := range relay.guests {
			guests[id] = guest
		}
		relay.mu.Unlock()
		if role == string(RoleHost) {
			envelope, unpackErr := UnpackEnvelope(payload)
			if unpackErr != nil {
				continue
			}
			if envelope.PeerID == 0 {
				for _, guest := range guests {
					_ = guest.write(request.Context(), websocket.MessageBinary, payload)
				}
			} else if guest := guests[envelope.PeerID]; guest != nil {
				_ = guest.write(request.Context(), websocket.MessageBinary, payload)
			}
		} else if host != nil {
			forwarded := append([]byte(nil), payload...)
			if RewriteEnvelopePeer(forwarded, peerID) == nil {
				_ = host.write(request.Context(), websocket.MessageBinary, forwarded)
			}
		}
	}
}

func TestLiveCollaborationSnapshotWritablePromptReadOnlyAndBroadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	service := session.NewService(store.DB(), store.Blobs())
	if _, err := service.Ensure(ctx, session.Session{ID: "session", Title: "Shared"}); err != nil {
		t.Fatal(err)
	}
	for _, block := range []session.Block{{Kind: "user", Content: "question"}, {Kind: "assistant", Content: "answer"}} {
		if _, err := service.AppendBlock(ctx, "session", block); err != nil {
			t.Fatal(err)
		}
	}
	relay := newInMemoryRelay()
	server := httptest.NewServer(http.HandlerFunc(relay.serve))
	defer server.Close()
	prompted := make(chan string, 1)
	host, err := NewHost(HostOptions{RelayURL: server.URL, SessionID: "session", Sessions: service, OnPrompt: func(_ context.Context, _ Participant, text string) error {
		prompted <- text
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer host.Stop("test complete")
	guest := NewGuest(GuestOptions{Name: "writer"})
	if err := guest.Join(ctx, host.Link()); err != nil {
		t.Fatal(err)
	}
	defer guest.Leave("test complete")
	snapshot := guest.Snapshot()
	if snapshot.ReadOnly || snapshot.Session.ID != "session" || len(snapshot.Blocks) != 2 || snapshot.Blocks[0].Content != "question" {
		t.Fatalf("writable snapshot=%#v", snapshot)
	}
	if err := guest.SendPrompt(ctx, "please continue"); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-prompted:
		if text != "please continue" {
			t.Fatalf("prompt=%q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not receive writable prompt")
	}
	liveBlock := session.Block{Sequence: 2, Kind: "assistant", Content: "live update"}
	if err := host.BroadcastBlock(ctx, liveBlock); err != nil {
		t.Fatal(err)
	}
	waitForGuestEvent(t, guest, "entry", func(event GuestEvent) bool { return event.Block != nil && event.Block.Content == "live update" })
	if got := guest.Snapshot().Blocks; len(got) != 3 || got[2].Content != "live update" {
		t.Fatalf("live replica=%#v", got)
	}
	viewGuest := NewGuest(GuestOptions{Name: "viewer"})
	if err := viewGuest.Join(ctx, host.ViewLink()); err != nil {
		t.Fatal(err)
	}
	defer viewGuest.Leave("test complete")
	if !viewGuest.Snapshot().ReadOnly {
		t.Fatal("view link joined writable")
	}
	if err := viewGuest.SendPrompt(ctx, "must fail"); err == nil {
		t.Fatal("read-only guest sent prompt")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(host.Participants()) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	participants := host.Participants()
	if len(participants) != 2 || participants[0].ReadOnly || !participants[1].ReadOnly {
		t.Fatalf("participants=%#v", participants)
	}
	relay.mu.Lock()
	cipherLog := append([][]byte(nil), relay.cipherLog...)
	relay.mu.Unlock()
	for _, envelope := range cipherLog {
		if bytes.Contains(envelope, []byte("question")) || bytes.Contains(envelope, []byte("please continue")) {
			t.Fatalf("relay observed plaintext frame: %q", envelope)
		}
	}
}

func waitForGuestEvent(t *testing.T, guest *Guest, kind string, predicate func(GuestEvent) bool) GuestEvent {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-guest.Events():
			if !ok {
				t.Fatal("guest events closed")
			}
			if event.Kind == kind && predicate(event) {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for guest event %q", kind)
		}
	}
}
