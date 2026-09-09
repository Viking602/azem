package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopipc"
)

type Options struct {
	Workspace        string
	ConfigFile       string
	ReplayBytes      int
	ClientQueueBytes int
	TerminalBytes    int
}

type OpenProjectRequest struct {
	Workspace string `json:"workspace"`
	SessionID string `json:"sessionId,omitempty"`
	Sequence  int64  `json:"sequence,omitempty"`
}

type Runtime struct {
	ctx          context.Context
	cancel       context.CancelFunc
	boot         azemapp.BootstrapResult
	bridge       *desktop.Bridge
	hub          *desktopipc.EventHub
	terminal     *desktopipc.TerminalReplay
	server       *desktopipc.Server
	endpoint     desktopipc.Endpoint
	endpointPath string
	tokenPath    string
	closeOnce    sync.Once
	closeErr     error
}

func New(parent context.Context, options Options) (*Runtime, error) {
	if parent == nil {
		return nil, errors.New("daemon context is required")
	}
	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" {
		return nil, errors.New("daemon workspace is required")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	ctx, cancel := context.WithCancel(parent)
	boot, err := azemapp.BootstrapDesktopAtWorkspace(ctx, absolute, options.ConfigFile)
	if err != nil {
		cancel()
		return nil, err
	}
	cleanupBoot := func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = boot.Service.Shutdown(shutdownCtx)
		cancel()
	}
	if err := boot.Validate(); err != nil {
		cleanupBoot()
		return nil, err
	}
	workspaceID, err := desktopipc.WorkspaceID(boot.Paths.Workspace)
	if err != nil {
		cleanupBoot()
		return nil, err
	}
	daemonDir := filepath.Join(boot.Paths.StateDir, "gpui-daemons", workspaceID)
	address := desktopipc.DefaultAddress(boot.Paths.StateDir, workspaceID)
	listener, err := desktopipc.Listen(address)
	if err != nil {
		cleanupBoot()
		return nil, err
	}
	fail := func(err error) (*Runtime, error) {
		listener.Close()
		cleanupBoot()
		return nil, err
	}
	token, err := desktopipc.GenerateToken()
	if err != nil {
		return fail(err)
	}
	tokenPath := filepath.Join(daemonDir, "token")
	if err := desktopipc.WriteTokenFile(tokenPath, token); err != nil {
		return fail(err)
	}
	hub := desktopipc.NewEventHub(options.ReplayBytes, options.ClientQueueBytes)
	emitter := desktopipc.BridgeEmitter(hub)
	bridge := desktop.NewBridge(ctx, boot, emitter, func(workspace, sessionID string, sequence int64) error {
		_, publishErr := hub.Publish(desktopipc.ChannelDaemon, OpenProjectRequest{Workspace: workspace, SessionID: sessionID, Sequence: sequence}, "open_project", false)
		return publishErr
	})
	terminal := desktopipc.NewTerminalReplay(hub, options.TerminalBytes)
	bridge.SetRawTerminalSink(terminal.Sink)
	dispatcher, err := desktopipc.NewDispatcher(bridge)
	if err != nil {
		_ = os.Remove(tokenPath)
		bridge.Close()
		return fail(err)
	}
	runtime := &Runtime{ctx: ctx, cancel: cancel, boot: boot, bridge: bridge, hub: hub, terminal: terminal, endpointPath: filepath.Join(daemonDir, "endpoint.json"), tokenPath: tokenPath}
	server, err := desktopipc.NewServer(desktopipc.ServerOptions{
		Listener: listener, Token: token, WorkspaceID: workspaceID, Hub: hub, Dispatcher: dispatcher,
		TransferDir: filepath.Join(daemonDir, "transfers"), TerminalReplay: terminal, OnDaemonStop: cancel,
		AuthorizeDaemonStop: func(includeActive bool) error {
			_, runID := boot.Service.ActiveRun()
			if runID != "" && !includeActive {
				return fmt.Errorf("daemon has active run %s", runID)
			}
			return nil
		},
	})
	if err != nil {
		_ = os.Remove(tokenPath)
		bridge.Close()
		return fail(err)
	}
	runtime.server = server
	runtime.endpoint = desktopipc.Endpoint{
		Protocol: desktopipc.ProtocolVersion, WorkspaceID: workspaceID, Workspace: boot.Paths.Workspace,
		Address: address, TokenFile: tokenPath, PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	if err := desktopipc.WriteEndpointFile(runtime.endpointPath, runtime.endpoint); err != nil {
		runtime.Close()
		return nil, err
	}
	bridge.StartRuntime()
	_, _ = hub.Publish(desktopipc.ChannelDaemon, map[string]any{"state": "ready", "pid": os.Getpid(), "workspaceId": workspaceID}, "daemon_state", true)
	return runtime, nil
}

func (runtime *Runtime) Run() error {
	if runtime == nil || runtime.server == nil {
		return errors.New("daemon runtime is unavailable")
	}
	serveErr := runtime.server.Serve(runtime.ctx)
	return errors.Join(serveErr, runtime.Close())
}

func (runtime *Runtime) Endpoint() desktopipc.Endpoint {
	if runtime == nil {
		return desktopipc.Endpoint{}
	}
	return runtime.endpoint
}

func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.closeOnce.Do(func() {
		runtime.cancel()
		if runtime.server != nil {
			runtime.closeErr = errors.Join(runtime.closeErr, runtime.server.Close())
		}
		if runtime.bridge != nil {
			runtime.bridge.Close()
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if runtime.boot.Service != nil {
			runtime.closeErr = errors.Join(runtime.closeErr, runtime.boot.Service.Shutdown(shutdownCtx))
		}
		// Keep non-secret endpoint metadata so a cold renderer can restore the
		// last workspace. The token is removed below, so stale metadata cannot
		// authenticate or make a stopped daemon appear live.
		_ = os.Remove(runtime.tokenPath)
	})
	return runtime.closeErr
}

func EndpointPath(stateDir, workspace string) (string, error) {
	workspaceID, err := desktopipc.WorkspaceID(workspace)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(stateDir) == "" {
		return "", fmt.Errorf("state directory is required")
	}
	return filepath.Join(stateDir, "gpui-daemons", workspaceID, "endpoint.json"), nil
}
