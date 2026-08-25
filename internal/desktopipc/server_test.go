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
