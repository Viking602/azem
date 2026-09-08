package desktopclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/daemon"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/session"
)

type workspaceClientDispatcher struct{}

func (workspaceClientDispatcher) Dispatch(method desktopipc.Method, payload json.RawMessage) (any, error) {
	sessionID := "session-a"
	var params struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(payload, &params)
	if params.SessionID != "" {
		sessionID = params.SessionID
	}
	switch method {
	case desktopipc.MethodReconnectSnapshot, desktopipc.MethodResumeSession:
		return desktop.ReconnectSnapshot{
			SelectedSessionID: sessionID,
			Base:              desktop.Snapshot{SessionID: "startup"},
			Session: &app.SessionProjection{
				Version: app.SessionProjectionVersion,
				Session: session.Session{ID: sessionID, Title: sessionID},
				Blocks:  []app.TranscriptBlock{}, ToolRecords: []session.ToolRecord{},
				Todo: session.TodoList{Phases: []session.TodoPhase{}}, AgentSnapshots: []app.AgentSnapshotPayload{},
			},
			Terminals: []desktop.TerminalSession{},
			Sessions:  []session.Session{},
			Projects:  []session.Project{},
		}, nil
	case desktopipc.MethodSelectSession, desktopipc.MethodCreateSession:
		if method == desktopipc.MethodCreateSession {
			sessionID = "session-new"
		}
		return desktop.SessionSelectionSnapshot{
			SelectedSessionID: sessionID,
			Session: &app.SessionProjection{
				Version: app.SessionProjectionVersion,
				Session: session.Session{ID: sessionID, Title: sessionID},
				Blocks:  []app.TranscriptBlock{}, ToolRecords: []session.ToolRecord{},
				Todo: session.TodoList{Phases: []session.TodoPhase{}}, AgentSnapshots: []app.AgentSnapshotPayload{},
			},
			Runs: []app.RunProjection{}, LiveBlocks: []app.LiveBlockProjection{},
			Controls: []app.PendingControlProjection{}, PromptQueues: []session.PromptQueueV1{},
			RuntimeRecovery: app.RecoveryProjection{State: "clear", Items: []app.PendingControlProjection{}},
		}, nil
	default:
		return map[string]bool{"ok": true}, nil
	}
}

func (workspaceClientDispatcher) ImportAttachmentBytes(_, name, mimeType string, data []byte) (desktop.Attachment, error) {
	return desktop.Attachment{ID: "attachment", Name: name, MIMEType: mimeType, Size: int64(len(data))}, nil
}

type workspaceClientFixture struct {
	ctx      context.Context
	cancel   context.CancelFunc
	hub      *desktopipc.EventHub
	server   *desktopipc.Server
	endpoint desktopipc.Endpoint
}

func newWorkspaceClientFixture(t *testing.T) *workspaceClientFixture {
	t.Helper()
	stateDir, err := os.MkdirTemp(shortTempRoot(), "azem-workspace-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	workspace := t.TempDir()
	workspaceID, err := desktopipc.WorkspaceID(workspace)
	if err != nil {
		t.Fatal(err)
	}
	address := desktopipc.DefaultAddress(stateDir, workspaceID)
	listener, err := desktopipc.Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	daemonDir := filepath.Join(stateDir, "gpui-daemons", workspaceID)
	token, err := desktopipc.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(daemonDir, "token")
	if err := desktopipc.WriteTokenFile(tokenPath, token); err != nil {
		t.Fatal(err)
	}
	hub := desktopipc.NewEventHub(1<<20, 1<<20)
	server, err := desktopipc.NewServer(desktopipc.ServerOptions{
		Listener: listener, Token: token, WorkspaceID: workspaceID, Hub: hub,
		Dispatcher: workspaceClientDispatcher{}, TransferDir: filepath.Join(daemonDir, "transfers"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx) }()
	fixture := &workspaceClientFixture{
		ctx: ctx, cancel: cancel, hub: hub, server: server,
		endpoint: desktopipc.Endpoint{
			Protocol: desktopipc.ProtocolVersion, WorkspaceID: workspaceID, Workspace: workspace,
			DaemonEpoch: server.DaemonEpoch(), Address: address, TokenFile: tokenPath, PID: os.Getpid(),
		},
	}
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return fixture
}

func (fixture *workspaceClientFixture) client(t *testing.T, id string) *WorkspaceClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	connection, ack, err := desktopipc.Connect(ctx, fixture.endpoint, id, 0)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	clientCtx, clientCancel := context.WithCancel(context.Background())
	client := newWorkspaceClientWithConnection(clientCtx, clientCancel, daemon.ClientOptions{
		Workspace: fixture.endpoint.Workspace, StateDir: endpointStateDirectory(fixture.endpoint), ClientID: id,
	}, connection, fixture.endpoint, ack)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestWorkspaceClientHydratesBeforeForwardingAndSharesRuntimeEvents(t *testing.T) {
	fixture := newWorkspaceClientFixture(t)
	if _, err := fixture.hub.Publish(desktopipc.ChannelRuntime, desktop.Event{Kind: "text_delta", SessionID: "session-a", Text: "before"}, "", true); err != nil {
		t.Fatal(err)
	}
	first := fixture.client(t, "first")
	second := fixture.client(t, "second")
	firstSnapshot, err := first.InitialSnapshot(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot, err := second.InitialSnapshot(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if firstSnapshot.DaemonEpoch == "" || firstSnapshot.DaemonEpoch != secondSnapshot.DaemonEpoch || firstSnapshot.WireSequence == 0 {
		t.Fatalf("snapshot boundaries differ: first=%#v second=%#v", firstSnapshot, secondSnapshot)
	}
	if firstSnapshot.SelectedSessionID != "session-a" || firstSnapshot.Session == nil {
		t.Fatalf("initial snapshot = %#v", firstSnapshot)
	}
	drainConnectionEvent(t, first)
	drainConnectionEvent(t, second)
	if _, err := fixture.hub.Publish(desktopipc.ChannelRuntime, desktop.Event{Kind: "text_delta", SessionID: "session-a", Text: "after"}, "", true); err != nil {
		t.Fatal(err)
	}
	for name, client := range map[string]*WorkspaceClient{"first": first, "second": second} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		event := nextEventKind(t, client, ctx, EventRuntime)
		cancel()
		if event.Kind != EventRuntime || event.Runtime == nil || event.Runtime.Text != "after" || event.WireCursor <= firstSnapshot.WireSequence {
			t.Fatalf("%s runtime event = %#v", name, event)
		}
	}
}

func TestWorkspaceClientSelectionIsLocalAndReconnectStatesDisableMutation(t *testing.T) {
	fixture := newWorkspaceClientFixture(t)
	first := fixture.client(t, "first-selection")
	second := fixture.client(t, "second-selection")
	if _, err := first.InitialSnapshot(context.Background(), "session-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.InitialSnapshot(context.Background(), "session-a"); err != nil {
		t.Fatal(err)
	}
	drainConnectionEvent(t, first)
	drainConnectionEvent(t, second)
	selected, err := first.SelectSessionFast(context.Background(), "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if selected.SelectedSessionID != "session-b" {
		t.Fatalf("selected snapshot = %#v", selected)
	}
	first.mu.Lock()
	firstSelected := first.selectedSession
	first.mu.Unlock()
	second.mu.Lock()
	secondSelected := second.selectedSession
	second.state.State = ConnectionReconnecting
	second.mu.Unlock()
	if firstSelected != "session-b" || secondSelected != "session-a" {
		t.Fatalf("selection leaked across clients: first=%q second=%q", firstSelected, secondSelected)
	}
	if err := second.Request(context.Background(), desktopipc.MethodExecute, map[string]string{}, nil); !errors.Is(err, ErrConnectionUnavailable) {
		t.Fatalf("mutation while reconnecting error = %v", err)
	}
}

func TestWorkspaceClientAppliesResyncSnapshotAtomically(t *testing.T) {
	fixture := newWorkspaceClientFixture(t)
	client := fixture.client(t, "resync-client")
	if _, err := client.InitialSnapshot(context.Background(), "session-a"); err != nil {
		t.Fatal(err)
	}
	drainConnectionEvent(t, client)
	if err := client.resync(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	connection := nextEventKind(t, client, ctx, EventConnection)
	if connection.Connection == nil || connection.Connection.State != ConnectionResyncing {
		cancel()
		t.Fatalf("resync connection event = %#v", connection)
	}
	snapshotEvent := nextEventKind(t, client, ctx, EventSnapshot)
	cancel()
	if snapshotEvent.Snapshot == nil || snapshotEvent.Snapshot.DaemonEpoch != fixture.endpoint.DaemonEpoch {
		t.Fatalf("atomic snapshot event = %#v", snapshotEvent)
	}
}

func TestWorkspaceClientRebindsRendererWithoutStoppingEitherDaemon(t *testing.T) {
	stateDir, err := os.MkdirTemp(shortTempRoot(), "azem-rebind-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	t.Setenv("AZEM_HOME", stateDir)
	firstWorkspace := t.TempDir()
	secondWorkspace := t.TempDir()
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstRuntime, err := daemon.New(parent, daemon.Options{Workspace: firstWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRuntime.Close()
	secondRuntime, err := daemon.New(parent, daemon.Options{Workspace: secondWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	defer secondRuntime.Close()
	go func() { _ = firstRuntime.Run() }()
	go func() { _ = secondRuntime.Run() }()

	connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	connection, ack, err := desktopipc.Connect(connectCtx, firstRuntime.Endpoint(), "rebind-client", 0)
	connectCancel()
	if err != nil {
		t.Fatal(err)
	}
	clientCtx, clientCancel := context.WithCancel(context.Background())
	client := newWorkspaceClientWithConnection(clientCtx, clientCancel, daemon.ClientOptions{
		Workspace: firstRuntime.Endpoint().Workspace, StateDir: stateDir, ClientID: "rebind-client",
	}, connection, firstRuntime.Endpoint(), ack)
	defer client.Close()
	if _, err := client.InitialSnapshot(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.RebindWorkspace(context.Background(), secondWorkspace, "")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Base.Workspace != secondRuntime.Endpoint().Workspace || snapshot.DaemonEpoch != secondRuntime.Endpoint().DaemonEpoch {
		t.Fatalf("rebound snapshot = %#v", snapshot)
	}
	for name, endpoint := range map[string]desktopipc.Endpoint{
		"detached source": firstRuntime.Endpoint(), "selected target": secondRuntime.Endpoint(),
	} {
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		probe, _, err := desktopipc.Connect(ctx, endpoint, "probe-"+name, 0)
		stop()
		if err != nil {
			t.Fatalf("%s daemon stopped during rebind: %v", name, err)
		}
		_ = probe.Close()
	}
}

func drainConnectionEvent(t *testing.T, client *WorkspaceClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event := nextEventKind(t, client, ctx, EventConnection)
	if event.Connection == nil || event.Connection.State != ConnectionConnected {
		t.Fatalf("connection event = %#v", event)
	}
}

func nextEventKind(t *testing.T, client *WorkspaceClient, ctx context.Context, kind EventKind) Event {
	t.Helper()
	for {
		event, err := client.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == kind {
			return event
		}
	}
}

func shortTempRoot() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return "/tmp"
}
