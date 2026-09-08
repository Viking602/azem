package desktopipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/session"
)

type clientTestDispatcher struct {
	dispatch func(Method, json.RawMessage) (any, error)
	mu       sync.Mutex
	name     string
	mime     string
	data     []byte
}

func (dispatcher *clientTestDispatcher) Dispatch(method Method, payload json.RawMessage) (any, error) {
	if dispatcher.dispatch != nil {
		return dispatcher.dispatch(method, payload)
	}
	return map[string]any{"method": method}, nil
}

func (dispatcher *clientTestDispatcher) ImportAttachmentBytes(_ string, name, mimeType string, data []byte) (desktop.Attachment, error) {
	dispatcher.mu.Lock()
	dispatcher.name = name
	dispatcher.mime = mimeType
	dispatcher.data = append([]byte(nil), data...)
	dispatcher.mu.Unlock()
	return desktop.Attachment{ID: "attachment-1", Name: name, MIMEType: mimeType, Path: "/daemon/attachment-1", Size: int64(len(data))}, nil
}

type clientServerFixture struct {
	endpoint Endpoint
	hub      *EventHub
	server   *Server
	cancel   context.CancelFunc
}

func newClientServerFixture(t *testing.T, dispatcher RequestDispatcher, replayBytes, queueBytes int) *clientServerFixture {
	t.Helper()
	directory := t.TempDir()
	workspaceID := fmt.Sprintf("client-%d", time.Now().UnixNano())
	stateDir := os.TempDir()
	if runtime.GOOS != "windows" {
		stateDir = "/tmp"
	}
	address := DefaultAddress(stateDir, workspaceID)
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
	hub := NewEventHub(replayBytes, queueBytes)
	server, err := NewServer(ServerOptions{
		Listener: listener, Token: token, WorkspaceID: workspaceID, Hub: hub, Dispatcher: dispatcher,
		TransferDir: filepath.Join(directory, "transfers"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx) }()
	fixture := &clientServerFixture{
		endpoint: Endpoint{
			Protocol: ProtocolVersion, WorkspaceID: workspaceID, Workspace: directory,
			DaemonEpoch: server.DaemonEpoch(), Address: address, TokenFile: tokenPath, PID: os.Getpid(),
		},
		hub: hub, server: server, cancel: cancel,
	}
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return fixture
}

func connectFixtureClient(t *testing.T, fixture *clientServerFixture, lastSequence uint64) (*Client, HelloAck) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, ack, err := Connect(ctx, fixture.endpoint, "client-test", lastSequence)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, ack
}

func TestClientReceivesIdleEventsAndMultiplexesConcurrentRequests(t *testing.T) {
	dashboardStarted := make(chan struct{}, 1)
	releaseDashboard := make(chan struct{})
	dispatcher := &clientTestDispatcher{dispatch: func(method Method, payload json.RawMessage) (any, error) {
		if method == MethodPullRequestDashboard {
			dashboardStarted <- struct{}{}
			<-releaseDashboard
			return map[string]string{"request": "dashboard"}, nil
		}
		var value map[string]string
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		return value, nil
	}}
	fixture := newClientServerFixture(t, dispatcher, 1<<20, 1<<20)
	client, _ := connectFixtureClient(t, fixture, 0)

	dashboardResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var result map[string]string
		dashboardResult <- client.Request(ctx, MethodPullRequestDashboard, map[string]string{"id": "one"}, &result)
	}()
	select {
	case <-dashboardStarted:
	case <-time.After(time.Second):
		t.Fatal("dashboard request did not start")
	}
	if _, err := fixture.hub.Publish(ChannelRuntime, map[string]string{"kind": "idle-event"}, "", true); err != nil {
		t.Fatal(err)
	}
	requestResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var result map[string]string
		err := client.Request(ctx, MethodExecute, map[string]string{"id": "two"}, &result)
		if err == nil && result["id"] != "two" {
			err = errors.New("second response was routed to the wrong request")
		}
		requestResult <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	message, err := client.Next(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if message.Kind != ClientMessageEvent || message.Event.Channel != ChannelRuntime || message.Sequence == 0 {
		t.Fatalf("idle event = %#v", message)
	}
	select {
	case err := <-requestResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("independent request was blocked")
	}
	close(releaseDashboard)
	select {
	case err := <-dashboardResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("dashboard response was not routed")
	}
}

func TestServerCommandSequencerOrdersMutationBeforeSnapshot(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	active := false
	dispatcher := &clientTestDispatcher{dispatch: func(method Method, _ json.RawMessage) (any, error) {
		switch method {
		case MethodStartTurn:
			started <- struct{}{}
			<-release
			active = true
			return map[string]string{"runId": "run-1"}, nil
		case MethodReconnectSnapshot:
			return map[string]bool{"active": active}, nil
		default:
			return nil, nil
		}
	}}
	fixture := newClientServerFixture(t, dispatcher, 1<<20, 1<<20)
	first, _ := connectFixtureClient(t, fixture, 0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	second, _, err := Connect(ctx, fixture.endpoint, "second-sequencer-client", 0)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	startResult := make(chan error, 1)
	go func() {
		startResult <- first.Request(context.Background(), MethodStartTurn, map[string]string{"prompt": "ordered"}, nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("sequenced mutation did not start")
	}
	snapshotResult := make(chan struct {
		active bool
		err    error
	}, 1)
	go func() {
		var snapshot map[string]bool
		err := second.Request(context.Background(), MethodReconnectSnapshot, map[string]string{}, &snapshot)
		snapshotResult <- struct {
			active bool
			err    error
		}{active: snapshot["active"], err: err}
	}()
	select {
	case result := <-snapshotResult:
		t.Fatalf("snapshot overtook mutation: %#v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-startResult; err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-snapshotResult:
		if result.err != nil || !result.active {
			t.Fatalf("snapshot after mutation = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot did not resume after mutation")
	}
}

func TestCommandReceiptCacheDeduplicatesByClientMutationAndDigest(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	dispatcher := &clientTestDispatcher{dispatch: func(_ Method, payload json.RawMessage) (any, error) {
		mu.Lock()
		calls++
		current := calls
		mu.Unlock()
		return map[string]any{"accepted": current, "payload": string(payload)}, nil
	}}
	fixture := newClientServerFixture(t, dispatcher, 1<<20, 1<<20)
	client, _ := connectFixtureClient(t, fixture, 0)
	payload := map[string]any{"mutationId": "mutation-1", "sessionId": "session", "itemId": "item"}
	var first, retried map[string]any
	if err := client.Request(context.Background(), MethodMutatePromptQueue, payload, &first); err != nil {
		t.Fatal(err)
	}
	if err := client.Request(context.Background(), MethodMutatePromptQueue, payload, &retried); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	count := calls
	mu.Unlock()
	if count != 1 || first["accepted"] != retried["accepted"] {
		t.Fatalf("deduplicated calls=%d first=%#v retry=%#v", count, first, retried)
	}
	var ignored map[string]any
	err := client.Request(context.Background(), MethodMutatePromptQueue, map[string]any{
		"mutationId": "mutation-1", "sessionId": "session", "itemId": "different",
	}, &ignored)
	var protocolErr *ProtocolRequestError
	if !errors.As(err, &protocolErr) || protocolErr.Code != "request_duplicate" || protocolErr.Retryable {
		t.Fatalf("duplicate mutation error = %#v, %v", protocolErr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	other, _, err := Connect(ctx, fixture.endpoint, "other-receipt-client", 0)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Request(context.Background(), MethodMutatePromptQueue, payload, &ignored); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	count = calls
	mu.Unlock()
	if count != 2 {
		t.Fatalf("receipt cache leaked across clients: calls=%d", count)
	}
}

func TestProtocolErrorTaxonomyIsTypedAndStable(t *testing.T) {
	tests := []struct {
		err       error
		code      string
		retryable bool
	}{
		{err: azemapp.ErrRunActive, code: "run_active", retryable: true},
		{err: azemapp.ErrStaleRun, code: "stale_run", retryable: true},
		{err: azemapp.ErrGuidanceClosed, code: "guidance_closed"},
		{err: session.ErrPromptQueueRevisionConflict, code: "revision_conflict", retryable: true},
		{err: azemapp.ErrInvalidPromptQueueAction, code: "invalid_action"},
		{err: session.ErrSessionNotFound, code: "not_found"},
	}
	for _, test := range tests {
		projected := protocolError(test.err, 42)
		if projected.Code != test.code || projected.Retryable != test.retryable || projected.Cursor != 42 {
			t.Fatalf("protocol error for %v = %#v", test.err, projected)
		}
	}
}

func TestClientUploadsAndReceivesBinaryWithoutBase64(t *testing.T) {
	dispatcher := &clientTestDispatcher{}
	fixture := newClientServerFixture(t, dispatcher, 1<<20, 1<<20)
	client, _ := connectFixtureClient(t, fixture, 0)
	data := bytes.Repeat([]byte("binary-attachment"), 40_000)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	attachment, err := client.UploadAttachment(ctx, "session-1", "capture.png", "image/png", data)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if attachment.ID != "attachment-1" || attachment.Size != int64(len(data)) {
		t.Fatalf("attachment = %#v", attachment)
	}
	dispatcher.mu.Lock()
	if dispatcher.name != "capture.png" || dispatcher.mime != "image/png" || !bytes.Equal(dispatcher.data, data) {
		t.Fatalf("uploaded attachment mismatch: name=%q mime=%q bytes=%d", dispatcher.name, dispatcher.mime, len(dispatcher.data))
	}
	dispatcher.mu.Unlock()

	binary := []byte("terminal-output")
	fixture.hub.PublishBinary(ChannelTerminal, BinaryMetadata{TransferID: "terminal-1", Purpose: "terminal_output"}, binary, true)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	message, err := client.Next(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if message.Kind != ClientMessageBinary || message.Binary.TransferID != "terminal-1" || !bytes.Equal(message.Data, binary) {
		t.Fatalf("binary event = %#v", message)
	}
}

func TestClientContextCancellationOnlyStopsLocalWaiter(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	dispatcher := &clientTestDispatcher{dispatch: func(method Method, _ json.RawMessage) (any, error) {
		if method == MethodExecute {
			started <- struct{}{}
			<-release
			return map[string]bool{"accepted": true}, nil
		}
		return map[string]bool{"healthy": true}, nil
	}}
	fixture := newClientServerFixture(t, dispatcher, 1<<20, 1<<20)
	client, _ := connectFixtureClient(t, fixture, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	errCh := make(chan error, 1)
	go func() { errCh <- client.Request(ctx, MethodExecute, map[string]string{"work": "accepted"}, nil) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("daemon did not accept request")
	}
	if err := <-errCh; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled request error = %v", err)
	}
	cancel()
	close(release)
	requestCtx, requestCancel := context.WithTimeout(context.Background(), time.Second)
	defer requestCancel()
	var result map[string]bool
	if err := client.Request(requestCtx, MethodUsageReport, map[string]string{}, &result); err != nil {
		t.Fatalf("connection did not survive local cancellation: %v", err)
	}
	if !result["healthy"] {
		t.Fatalf("subsequent response = %#v", result)
	}
}

func TestClientInitialReplayResyncAndEpochValidation(t *testing.T) {
	dispatcher := &clientTestDispatcher{}
	fixture := newClientServerFixture(t, dispatcher, 1<<20, 1<<20)
	sequence, err := fixture.hub.Publish(ChannelRuntime, map[string]string{"text": "replayed"}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	client, ack := connectFixtureClient(t, fixture, 0)
	if !ack.ReplayAvailable || ack.DaemonEpoch != fixture.endpoint.DaemonEpoch || ack.CurrentSequence != sequence {
		t.Fatalf("hello ack = %#v", ack)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	first, err := client.Next(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	complete, err := client.Next(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if first.Kind != ClientMessageEvent || first.Sequence != sequence || complete.Kind != ClientMessageReplayComplete || complete.Sequence != sequence {
		t.Fatalf("initial replay = %#v then %#v", first, complete)
	}

	small := newClientServerFixture(t, dispatcher, 128, 1<<20)
	if _, err := small.hub.Publish(ChannelRuntime, map[string]string{"payload": string(bytes.Repeat([]byte{'x'}, 1024))}, "", true); err != nil {
		t.Fatal(err)
	}
	resyncClient, resyncAck := connectFixtureClient(t, small, 0)
	if resyncAck.ReplayAvailable {
		t.Fatal("evicted replay was reported available")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	resync, err := resyncClient.Next(ctx)
	cancel()
	if err != nil || resync.Kind != ClientMessageResyncRequired {
		t.Fatalf("resync message = %#v, %v", resync, err)
	}

	stale := fixture.endpoint
	stale.DaemonEpoch = "stale-epoch"
	connectCtx, connectCancel := context.WithTimeout(context.Background(), time.Second)
	_, _, err = Connect(connectCtx, stale, "stale-client", sequence)
	connectCancel()
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("stale epoch error = %v", err)
	}
}

func TestClientQueueOverflowRequiresResyncThenDisconnectsIfStillBlocked(t *testing.T) {
	queue := newClientMessageQueue(512)
	large := ClientMessage{Kind: ClientMessageEvent, Sequence: 7, Data: bytes.Repeat([]byte{'x'}, 1024)}
	if !queue.push(large, clientMessageSize(large)) {
		t.Fatal("first overflow must enqueue resync")
	}
	if queue.push(large, clientMessageSize(large)) {
		t.Fatal("second overflow before resync delivery must close the connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	message, err := queue.next(ctx)
	cancel()
	if err != nil || message.Kind != ClientMessageResyncRequired || message.Sequence != 7 {
		t.Fatalf("overflow marker = %#v, %v", message, err)
	}
}

func TestClientReturnsStructuredProtocolErrorWithCursor(t *testing.T) {
	dispatcher := &clientTestDispatcher{dispatch: func(Method, json.RawMessage) (any, error) {
		return nil, errors.New("rejected")
	}}
	fixture := newClientServerFixture(t, dispatcher, 1<<20, 1<<20)
	sequence, err := fixture.hub.Publish(ChannelDaemon, map[string]string{"state": "ready"}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := connectFixtureClient(t, fixture, sequence)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err = client.Request(ctx, MethodExecute, map[string]string{}, nil)
	cancel()
	var protocolErr *ProtocolRequestError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("request error type = %T %v", err, err)
	}
	if protocolErr.Code != "request_failed" || protocolErr.Message != "rejected" || protocolErr.Retryable || protocolErr.Cursor != sequence {
		t.Fatalf("protocol error = %#v", protocolErr)
	}
}

func TestClientKeepaliveAndEOFFanout(t *testing.T) {
	pingSeen := make(chan struct{}, 1)
	endpoint := startRawClientPeer(t, func(codec *Codec, connection net.Conn) {
		defer connection.Close()
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		frame, err := codec.ReadFrame()
		if err != nil {
			return
		}
		if frame.Envelope != nil && frame.Envelope.Kind == FramePing && frame.Envelope.ClientID == "keepalive-client" {
			pingSeen <- struct{}{}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	client, _, err := connectWithOptions(ctx, endpoint, "keepalive-client", 0, clientOptions{queueBytes: 1 << 20, keepaliveInterval: 20 * time.Millisecond})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case <-pingSeen:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("idle client did not send keepalive ping")
	}

	requestsSeen := make(chan struct{}, 1)
	eofEndpoint := startRawClientPeer(t, func(codec *Codec, connection net.Conn) {
		for count := 0; count < 2; count++ {
			frame, readErr := codec.ReadFrame()
			if readErr != nil || frame.Envelope == nil || frame.Envelope.Kind != FrameRequest {
				return
			}
		}
		requestsSeen <- struct{}{}
		_ = connection.Close()
	})
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	eofClient, _, err := Connect(ctx, eofEndpoint, "eof-client", 0)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer eofClient.Close()
	errorsCh := make(chan error, 2)
	for range 2 {
		go func() {
			requestCtx, requestCancel := context.WithTimeout(context.Background(), time.Second)
			defer requestCancel()
			errorsCh <- eofClient.Request(requestCtx, MethodExecute, map[string]string{}, nil)
		}()
	}
	select {
	case <-requestsSeen:
	case <-time.After(time.Second):
		t.Fatal("peer did not receive concurrent requests")
	}
	firstErr, secondErr := <-errorsCh, <-errorsCh
	if firstErr == nil || secondErr == nil || firstErr != secondErr {
		t.Fatalf("pending requests did not receive the same connection error: %v / %v", firstErr, secondErr)
	}
	messageCtx, messageCancel := context.WithTimeout(context.Background(), time.Second)
	disconnected, err := eofClient.Next(messageCtx)
	messageCancel()
	if err != nil || disconnected.Kind != ClientMessageDisconnected || disconnected.Err != firstErr {
		t.Fatalf("disconnect message = %#v, %v", disconnected, err)
	}
}

func TestServerRejectsPostAuthenticationActorMismatch(t *testing.T) {
	fixture := newClientServerFixture(t, &clientTestDispatcher{}, 1<<20, 1<<20)
	client, _ := connectFixtureClient(t, fixture, 0)
	forged := NewEnvelope(FramePing)
	forged.ID = "forged"
	forged.ClientID = "another-client"
	forged.WorkspaceID = fixture.endpoint.WorkspaceID
	if err := client.writeEnvelope(context.Background(), forged); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	message, err := client.Next(ctx)
	cancel()
	if err != nil || message.Kind != ClientMessageDisconnected {
		t.Fatalf("actor mismatch did not close connection: %#v, %v", message, err)
	}
}

func startRawClientPeer(t *testing.T, handler func(*Codec, net.Conn)) Endpoint {
	t.Helper()
	directory := t.TempDir()
	workspaceID := fmt.Sprintf("raw-%d", time.Now().UnixNano())
	stateDir := os.TempDir()
	if runtime.GOOS != "windows" {
		stateDir = "/tmp"
	}
	address := DefaultAddress(stateDir, workspaceID)
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
	epoch := "raw-peer-epoch"
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		codec := NewCodec(connection)
		nonce, nonceErr := GenerateNonce()
		if nonceErr != nil {
			_ = connection.Close()
			return
		}
		hello := NewEnvelope(FrameHello)
		hello.WorkspaceID = workspaceID
		hello.Payload = mustJSON(Challenge{Nonce: nonce, WorkspaceID: workspaceID, Protocol: ProtocolVersion, DaemonEpoch: epoch})
		if codec.WriteEnvelope(hello) != nil {
			_ = connection.Close()
			return
		}
		frame, readErr := codec.ReadFrame()
		if readErr != nil || frame.Envelope == nil {
			_ = connection.Close()
			return
		}
		var authentication Authenticate
		if json.Unmarshal(frame.Envelope.Payload, &authentication) != nil ||
			!VerifyAuthenticationProof(token, nonce, authentication.ClientID, workspaceID, ProtocolVersion, authentication.Proof) {
			_ = connection.Close()
			return
		}
		ack := NewEnvelope(FrameHelloAck)
		ack.Payload = mustJSON(HelloAck{Protocol: ProtocolVersion, WorkspaceID: workspaceID, DaemonEpoch: epoch, ReplayAvailable: true})
		if codec.WriteEnvelope(ack) != nil {
			_ = connection.Close()
			return
		}
		handler(codec, connection)
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return Endpoint{
		Protocol: ProtocolVersion, WorkspaceID: workspaceID, Workspace: directory, DaemonEpoch: epoch,
		Address: address, TokenFile: tokenPath, PID: os.Getpid(),
	}
}
