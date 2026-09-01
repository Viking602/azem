package desktopipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/desktop"
)

type lifecycleDispatcher struct {
	mu     sync.Mutex
	active bool
}

func (dispatcher *lifecycleDispatcher) Dispatch(method Method, _ json.RawMessage) (any, error) {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if method == MethodStartTurn {
		dispatcher.active = true
		return "run-1", nil
	}
	if method == MethodReconnectSnapshot {
		return map[string]any{"session": map[string]any{"active": dispatcher.active, "runId": "run-1"}}, nil
	}
	return nil, nil
}

func (*lifecycleDispatcher) ImportAttachmentBytes(_, _, _ string, _ []byte) (desktop.Attachment, error) {
	return desktop.Attachment{}, nil
}

type navigationDispatcher struct {
	lifecycleDispatcher
	started chan struct{}
	release chan struct{}
}

func (dispatcher *navigationDispatcher) Dispatch(method Method, payload json.RawMessage) (any, error) {
	if method == MethodPullRequestDashboard {
		dispatcher.started <- struct{}{}
		<-dispatcher.release
		return map[string]bool{"loaded": true}, nil
	}
	return dispatcher.lifecycleDispatcher.Dispatch(method, payload)
}

func TestSessionNavigationDoesNotWaitForPullRequestDashboard(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "azem-ipc-navigation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	workspaceID := "navigation-test"
	address := DefaultAddress(directory, workspaceID)
	listener, err := Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(directory, "token")
	if err := WriteTokenFile(tokenPath, token); err != nil {
		t.Fatal(err)
	}
	dispatcher := &navigationDispatcher{started: make(chan struct{}, 2), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(dispatcher.release) })
	defer release()
	server, err := NewServer(ServerOptions{
		Listener: listener, Token: token, WorkspaceID: workspaceID,
		Hub: NewEventHub(1<<20, 1<<20), Dispatcher: dispatcher,
		TransferDir: filepath.Join(directory, "transfers"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Close()
	go func() { _ = server.Serve(ctx) }()
	client, _, err := Connect(ctx, Endpoint{
		Protocol: ProtocolVersion, WorkspaceID: workspaceID, Address: address, TokenFile: tokenPath,
	}, "navigation-client", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.connection.Close()
	// Use the multiplexed wire contract used by GPUI, not the serial Go CLI helper.
	request := NewEnvelope(FrameRequest)
	request.ID, request.Method, request.Payload = "dashboard", MethodPullRequestDashboard, mustJSON(map[string]string{})
	if err := client.codec.WriteEnvelope(request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dispatcher.started:
	case <-time.After(time.Second):
		t.Fatal("dashboard did not start")
	}
	request.ID = "dashboard-next"
	if err := client.codec.WriteEnvelope(request); err != nil {
		t.Fatal(err)
	}
	request.ID = "dashboard-busy"
	if err := client.codec.WriteEnvelope(request); err != nil {
		t.Fatal(err)
	}
	if err := client.connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	busy, err := client.codec.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if busy.Envelope == nil || busy.Envelope.ID != "dashboard-busy" || busy.Envelope.Error == nil || busy.Envelope.Error.Code != "request_busy" {
		t.Fatalf("dashboard backpressure response = %#v", busy.Envelope)
	}
	started := time.Now()
	request.ID, request.Method = "resume", MethodResumeSession
	request.Payload = mustJSON(map[string]string{"sessionId": "existing-session"})
	if err := client.connection.SetDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := client.codec.WriteEnvelope(request); err != nil {
		t.Fatal(err)
	}
	frame, err := client.codec.ReadFrame()
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("session navigation blocked by background dashboard for %s: %v", elapsed, err)
	}
	if frame.Envelope == nil || frame.Envelope.Kind != FrameResponse || frame.Envelope.ID != "resume" || frame.Envelope.Error != nil {
		t.Fatalf("navigation response = %#v", frame.Envelope)
	}
	t.Logf("session response while dashboard remains blocked: %s", elapsed)
	select {
	case <-dispatcher.started:
		t.Fatal("dashboard queries must stay serial to prevent stale results overtaking new ones")
	default:
	}
	release()
	if err := client.connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"dashboard", "dashboard-next"} {
		frame, err := client.codec.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Envelope == nil || frame.Envelope.ID != id || frame.Envelope.Error != nil {
			t.Fatalf("ordered dashboard response for %s = %#v", id, frame.Envelope)
		}
	}
}

func TestClientDetachLeavesDaemonAvailableForReconnect(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "azem-ipc-server-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	workspaceID := "workspace-test"
	address := DefaultAddress(directory, workspaceID)
	listener, err := Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(directory, "token")
	if err := WriteTokenFile(tokenPath, token); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatcher := &lifecycleDispatcher{}
	server, err := NewServer(ServerOptions{
		Listener: listener, Token: token, WorkspaceID: workspaceID,
		Hub: NewEventHub(1<<20, 1<<20), Dispatcher: dispatcher,
		TransferDir: filepath.Join(directory, "transfers"), OnDaemonStop: cancel,
		AuthorizeDaemonStop: func(includeActive bool) error {
			dispatcher.mu.Lock()
			defer dispatcher.mu.Unlock()
			if dispatcher.active && !includeActive {
				return errors.New("active run")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()
	endpoint := Endpoint{Protocol: ProtocolVersion, WorkspaceID: workspaceID, Address: address, TokenFile: tokenPath}

	connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	first, _, err := Connect(connectCtx, endpoint, "first", 0)
	connectCancel()
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, requestCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := first.Request(requestCtx, MethodStartTurn, map[string]string{"prompt": "keep running"}, nil); err != nil {
		requestCancel()
		t.Fatal(err)
	}
	requestCancel()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	connectCtx, connectCancel = context.WithTimeout(context.Background(), 5*time.Second)
	second, _, err := Connect(connectCtx, endpoint, "second", 0)
	connectCancel()
	if err != nil {
		t.Fatalf("reconnect after UI detach: %v", err)
	}
	requestCtx, requestCancel = context.WithTimeout(context.Background(), 5*time.Second)
	var snapshot struct {
		Session struct {
			Active bool   `json:"active"`
			RunID  string `json:"runId"`
		} `json:"session"`
	}
	if err := second.Request(requestCtx, MethodReconnectSnapshot, map[string]string{}, &snapshot); err != nil {
		requestCancel()
		t.Fatal(err)
	}
	requestCancel()
	if !snapshot.Session.Active || snapshot.Session.RunID != "run-1" {
		t.Fatalf("reconnected snapshot = %#v", snapshot)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := second.StopDaemon(stopCtx, false); err == nil {
		stopCancel()
		t.Fatal("daemon stop bypassed active-run guard")
	}
	stopCancel()
	if err := second.StopDaemon(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after explicit stop frame")
	}
}

func TestWriteEventRecordsSplitsControlFramesByBytes(t *testing.T) {
	payload := append([]byte{'"'}, bytes.Repeat([]byte{'x'}, MaxControlFrameBytes/2+1024)...)
	payload = append(payload, '"')
	records := []EventRecord{
		{Sequence: 1, Channel: ChannelRuntime, Payload: payload},
		{Sequence: 2, Channel: ChannelRuntime, Payload: payload},
	}
	var stream bytes.Buffer
	if err := writeEventRecords(NewCodec(&stream), records); err != nil {
		t.Fatal(err)
	}
	reader := NewCodec(&stream)
	for sequence := uint64(1); sequence <= 2; sequence++ {
		frame, err := reader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Envelope == nil || frame.Envelope.Kind != FrameEventBatch {
			t.Fatalf("frame %d = %#v", sequence, frame)
		}
		var batch EventBatch
		if err := json.Unmarshal(frame.Envelope.Payload, &batch); err != nil {
			t.Fatal(err)
		}
		if batch.FirstSequence != sequence || batch.LastSequence != sequence || len(batch.Events) != 1 {
			t.Fatalf("batch %d = %#v", sequence, batch)
		}
	}
	if stream.Len() != 0 {
		t.Fatalf("unexpected trailing bytes = %d", stream.Len())
	}
}
