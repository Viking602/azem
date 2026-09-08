package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/desktopipc"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

const daemonTestHelperEnv = "AZEM_DAEMON_TEST_HELPER"

func TestMain(testingMain *testing.M) {
	if os.Getenv(daemonTestHelperEnv) == "1" {
		os.Exit(runDaemonTestHelper(os.Args[1:]))
	}
	os.Exit(testingMain.Run())
}

func runDaemonTestHelper(arguments []string) int {
	workspace := argumentValue(arguments, "--workspace")
	configFile := argumentValue(arguments, "--config")
	runtime, err := New(context.Background(), Options{Workspace: workspace, ConfigFile: configFile})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := runtime.Run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 3
	}
	return 0
}

func argumentValue(arguments []string, name string) string {
	for index, argument := range arguments {
		if argument == name && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func TestConnectOrStartCanonicalizesWorkspaceAndKeepsDaemonDetached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	stateDir, err := os.MkdirTemp("/tmp", "azem-daemon-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	workspaceParent := t.TempDir()
	workspace := filepath.Join(workspaceParent, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(workspaceParent, "workspace-link")
	if err := os.Symlink(workspace, symlink); err != nil {
		t.Fatal(err)
	}
	workspace, err = canonicalWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, workspace)
	if err != nil {
		t.Fatal(err)
	}
	daemonBinary := copyDaemonTestBinary(t)
	t.Setenv(daemonTestHelperEnv, "1")
	t.Setenv(config.HomeEnv, stateDir)

	workspaceID, err := desktopipc.WorkspaceID(workspace)
	if err != nil {
		t.Fatal(err)
	}
	endpointPath, err := EndpointPath(stateDir, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(endpointPath), 0o700); err != nil {
		t.Fatal(err)
	}
	staleToken := filepath.Join(filepath.Dir(endpointPath), "stale-token")
	token, err := desktopipc.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := desktopipc.WriteTokenFile(staleToken, token); err != nil {
		t.Fatal(err)
	}
	if err := desktopipc.WriteEndpointFile(endpointPath, desktopipc.Endpoint{
		Protocol: desktopipc.ProtocolVersion, WorkspaceID: workspaceID, Workspace: workspace,
		DaemonEpoch: "stale", Address: desktopipc.DefaultAddress(stateDir, workspaceID),
		TokenFile: staleToken, PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	inputs := []string{workspace, relative, symlink}
	type result struct {
		client   *desktopipc.Client
		endpoint desktopipc.Endpoint
		err      error
	}
	results := make(chan result, len(inputs))
	var workers sync.WaitGroup
	for index, input := range inputs {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			client, endpoint, _, err := ConnectOrStart(ctx, ClientOptions{
				Workspace: input, StateDir: stateDir, DaemonBinary: daemonBinary,
				ClientID: fmt.Sprintf("launcher-%d", index),
			})
			results <- result{client: client, endpoint: endpoint, err: err}
		}()
	}
	workers.Wait()
	close(results)
	var pid int
	var epoch, address string
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.endpoint.Workspace != workspace {
			t.Fatalf("canonical workspace = %q, want %q", result.endpoint.Workspace, workspace)
		}
		if pid == 0 {
			pid, epoch, address = result.endpoint.PID, result.endpoint.DaemonEpoch, result.endpoint.Address
		} else if result.endpoint.PID != pid || result.endpoint.DaemonEpoch != epoch || result.endpoint.Address != address {
			t.Fatalf("launchers reached different daemons: first pid=%d epoch=%q address=%q, next=%#v", pid, epoch, address, result.endpoint)
		}
		if err := result.client.Close(); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	client, endpoint, _, err := ConnectOrStart(ctx, ClientOptions{
		Workspace: symlink, StateDir: stateDir, DaemonBinary: daemonBinary, ClientID: "after-detach",
	})
	cancel()
	if err != nil {
		t.Fatalf("detached daemon was not reconnectable: %v", err)
	}
	if endpoint.PID != pid || endpoint.DaemonEpoch != epoch {
		t.Fatalf("client detach replaced daemon: before pid=%d epoch=%q, after=%#v", pid, epoch, endpoint)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := client.StopDaemon(stopCtx, true); err != nil {
		stopCancel()
		t.Fatal(err)
	}
	stopCancel()
	_ = client.Close()
}

func TestConnectOrStartRetiresIdlePreEpochDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy Unix socket fixture")
	}
	stateDir, err := os.MkdirTemp("/tmp", "azem-daemon-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	workspace := t.TempDir()
	workspaceID, err := desktopipc.WorkspaceID(workspace)
	if err != nil {
		t.Fatal(err)
	}
	endpointPath, err := EndpointPath(stateDir, workspace)
	if err != nil {
		t.Fatal(err)
	}
	address := desktopipc.DefaultAddress(stateDir, workspaceID)
	tokenPath := filepath.Join(filepath.Dir(endpointPath), "legacy-token")
	token, err := desktopipc.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := desktopipc.WriteTokenFile(tokenPath, token); err != nil {
		t.Fatal(err)
	}
	listener, err := desktopipc.Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	legacy := desktopipc.Endpoint{
		Protocol: desktopipc.ProtocolVersion - 1, WorkspaceID: workspaceID, Workspace: workspace,
		Address: address, TokenFile: tokenPath, PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	if err := desktopipc.WriteEndpointFile(endpointPath, legacy); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go serveIdlePreEpochDaemon(listener, token, tokenPath, workspaceID, stopped)

	daemonBinary := copyDaemonTestBinary(t)
	t.Setenv(daemonTestHelperEnv, "1")
	t.Setenv(config.HomeEnv, stateDir)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, endpoint, _, err := ConnectOrStart(ctx, ClientOptions{
		Workspace: workspace, StateDir: stateDir, DaemonBinary: daemonBinary, ClientID: "react-upgrade",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if endpoint.DaemonEpoch == "" || endpoint.PID == legacy.PID {
		t.Fatalf("replacement endpoint = %#v", endpoint)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := client.StopDaemon(stopCtx, true); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
}

func serveIdlePreEpochDaemon(listener net.Listener, token []byte, tokenPath, workspaceID string, stopped chan<- error) {
	connection, err := listener.Accept()
	if err != nil {
		stopped <- err
		return
	}
	defer connection.Close()
	defer listener.Close()
	protocol := desktopipc.ProtocolVersion - 1
	codec := desktopipc.NewCodecForVersion(connection, protocol)
	nonce, err := desktopipc.GenerateNonce()
	if err != nil {
		stopped <- err
		return
	}
	challenge := desktopipc.Envelope{Version: protocol, Kind: desktopipc.FrameHello}
	challenge.WorkspaceID = workspaceID
	challenge.Payload, _ = json.Marshal(desktopipc.Challenge{Nonce: nonce, WorkspaceID: workspaceID, Protocol: protocol})
	if err := codec.WriteEnvelope(challenge); err != nil {
		stopped <- err
		return
	}
	frame, err := codec.ReadFrame()
	if err != nil {
		stopped <- err
		return
	}
	var authentication desktopipc.Authenticate
	if frame.Envelope == nil || json.Unmarshal(frame.Envelope.Payload, &authentication) != nil ||
		!desktopipc.VerifyAuthenticationProof(token, nonce, authentication.ClientID, workspaceID, protocol, authentication.Proof) {
		stopped <- desktopipc.ErrAuthentication
		return
	}
	ack := desktopipc.Envelope{Version: protocol, Kind: desktopipc.FrameHelloAck}
	ack.Payload, _ = json.Marshal(desktopipc.HelloAck{Protocol: protocol, WorkspaceID: workspaceID, ReplayAvailable: true})
	if err := codec.WriteEnvelope(ack); err != nil {
		stopped <- err
		return
	}
	// A real old daemon can replay queued runtime events before it reads Stop.
	event := desktopipc.Envelope{
		Version: protocol, Kind: desktopipc.FrameEventBatch, Sequence: 1,
		Channel: desktopipc.ChannelRuntime, Payload: json.RawMessage(`{"kind":"session_loaded"}`),
	}
	raw, _ := json.Marshal(event)
	batch := desktopipc.Envelope{Version: protocol, Kind: desktopipc.FrameEventBatch, Sequence: 1}
	batch.Payload, _ = json.Marshal(desktopipc.EventBatch{
		FirstSequence: 1, LastSequence: 1, Events: []json.RawMessage{raw},
	})
	if err := codec.WriteEnvelope(batch); err != nil {
		stopped <- err
		return
	}
	for {
		frame, err = codec.ReadFrame()
		if err != nil {
			stopped <- err
			return
		}
		if frame.Envelope == nil || frame.Envelope.Kind != desktopipc.FrameDaemonStop {
			continue
		}
		var stop desktopipc.DaemonStop
		if err := json.Unmarshal(frame.Envelope.Payload, &stop); err != nil || stop.IncludeActive {
			stopped <- errors.New("upgrade must never force-stop active work")
			return
		}
		response := desktopipc.Envelope{Version: protocol, Kind: desktopipc.FrameResponse}
		response.ID = frame.Envelope.ID
		response.Payload = json.RawMessage(`null`)
		if err := codec.WriteEnvelope(response); err != nil {
			stopped <- err
			return
		}
		if err := os.Remove(tokenPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			stopped <- err
			return
		}
		stopped <- nil
		return
	}
}

func TestResolveInitialWorkspaceReadsLegacyCatalogWithoutMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.HomeEnv, "")
	fallback := t.TempDir()
	recentWorkspace := t.TempDir()
	legacyDatabase := filepath.Join(home, ".config", "azem", "azem.db")
	store, err := sqlitestore.Open(context.Background(), legacyDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO desktop_projects(workspace,updated_at,visible) VALUES(?,?,1)`, recentWorkspace, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveInitialWorkspace(context.Background(), InitialWorkspaceOptions{StartupDirectory: fallback})
	if err != nil {
		t.Fatal(err)
	}
	expectedRecent, err := canonicalWorkspace(recentWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != expectedRecent {
		t.Fatalf("initial workspace = %q, want legacy recent %q", resolved, expectedRecent)
	}
	if _, err := os.Stat(legacyDatabase); err != nil {
		t.Fatalf("legacy database was moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".azem", "azem.db")); !os.IsNotExist(err) {
		t.Fatalf("client workspace resolution migrated the database: %v", err)
	}

	explicit := t.TempDir()
	resolved, err = ResolveInitialWorkspace(context.Background(), InitialWorkspaceOptions{Workspace: explicit, StartupDirectory: fallback})
	if err != nil {
		t.Fatal(err)
	}
	expectedExplicit, err := canonicalWorkspace(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != expectedExplicit {
		t.Fatalf("explicit workspace lost precedence: %q", resolved)
	}
}

func copyDaemonTestBinary(t *testing.T) string {
	t.Helper()
	sourcePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), daemonExecutableName())
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	return targetPath
}
