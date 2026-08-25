package collab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCollaborationSocketReconnectsAndFlushesBoundedPendingFrames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var connections atomic.Int32
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if connections.Add(1) == 1 {
			_ = conn.Close(websocket.StatusServiceRestart, "restart")
			return
		}
		kind, payload, err := conn.Read(request.Context())
		if err == nil && kind == websocket.MessageBinary {
			received <- payload
		}
	}))
	defer server.Close()
	_, key, _, err := GenerateRoom()
	if err != nil {
		t.Fatal(err)
	}
	socket, err := NewSocket(SocketOptions{
		URL: strings.Replace(server.URL, "http://", "ws://", 1), Role: RoleGuest, Key: key,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond },
	})
	if err != nil {
		t.Fatal(err)
	}
	socket.Start(ctx)
	queued := false
	for !queued {
		select {
		case event := <-socket.Events():
			if event.Kind == "closed" && event.WillReconnect {
				if err := socket.Send(ctx, Frame{Type: "prompt", Text: "queued"}, 0); err != nil {
					t.Fatal(err)
				}
				queued = true
			}
		case <-ctx.Done():
			t.Fatal("socket did not enter reconnect state")
		}
	}
	select {
	case packed := <-received:
		envelope, err := UnpackEnvelope(packed)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := OpenFrame(key, envelope.Payload)
		if err != nil || frame.Type != "prompt" || frame.Text != "queued" {
			t.Fatalf("replayed frame=%#v error=%v", frame, err)
		}
	case <-ctx.Done():
		t.Fatal("pending frame was not flushed after reconnect")
	}
	socket.Close()
	select {
	case <-socket.Done():
	case <-time.After(time.Second):
		t.Fatal("socket did not close")
	}
}
