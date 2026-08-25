package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/daemon"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/operator"
)

func daemonServeCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("daemon serve", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	workspace := flags.String("workspace", "", "workspace owned by this daemon")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		*workspace = cwd
	}
	runtime, err := daemon.New(ctx, daemon.Options{Workspace: *workspace, ConfigFile: *configFile})
	if err != nil {
		return err
	}
	endpoint := runtime.Endpoint()
	_, _ = fmt.Fprintf(streams.Err, "azem daemon ready: workspace=%s address=%s pid=%d\n", endpoint.Workspace, endpoint.Address, endpoint.PID)
	return runtime.Run()
}

func daemonStatusCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	workspace := flags.String("workspace", "", "workspace to inspect")
	configFile := flags.String("config", "", "config file path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	endpoint, client, snapshot, err := connectWorkspaceDaemon(ctx, *workspace, *configFile)
	if err != nil {
		return err
	}
	defer client.Close()
	if *jsonOutput {
		return json.NewEncoder(streams.Out).Encode(map[string]any{"endpoint": endpoint, "snapshot": snapshot})
	}
	activeRun := snapshot.ActiveRunID
	if activeRun == "" && snapshot.Session != nil {
		activeRun = snapshot.Session.Data["globalActiveRunID"]
	}
	_, err = fmt.Fprintf(streams.Out, "workspace: %s\npid: %d\nprotocol: %d\nactive run: %s\n", endpoint.Workspace, endpoint.PID, endpoint.Protocol, firstNonempty(activeRun, "none"))
	return err
}

func daemonStopCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	workspace := flags.String("workspace", "", "workspace daemon to stop")
	configFile := flags.String("config", "", "config file path")
	includeActive := flags.Bool("include-active", false, "allow stopping a daemon with a running session")
	if err := flags.Parse(args); err != nil {
		return err
	}
	endpoint, client, snapshot, err := connectWorkspaceDaemon(ctx, *workspace, *configFile)
	if err != nil {
		return err
	}
	defer client.Close()
	activeRun := snapshot.ActiveRunID
	if activeRun == "" && snapshot.Session != nil {
		activeRun = snapshot.Session.Data["globalActiveRunID"]
	}
	if activeRun != "" && !*includeActive {
		return fmt.Errorf("daemon has active run %s; wait for completion or pass --include-active", activeRun)
	}
	if err := client.StopDaemon(ctx, *includeActive); err != nil {
		return err
	}
	_, err = fmt.Fprintf(streams.Out, "stopping daemon %d for %s\n", endpoint.PID, endpoint.Workspace)
	return err
}

func connectWorkspaceDaemon(ctx context.Context, workspace, configFile string) (desktopipc.Endpoint, *desktopipc.Client, desktop.ReconnectSnapshot, error) {
	var snapshot desktop.ReconnectSnapshot
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return desktopipc.Endpoint{}, nil, snapshot, err
		}
		workspace = cwd
	}
	paths, err := resolveOperatorPaths(workspace, configFile)
	if err != nil {
		return desktopipc.Endpoint{}, nil, snapshot, err
	}
	endpointPath, err := daemon.EndpointPath(paths.StateDir, workspace)
	if err != nil {
		return desktopipc.Endpoint{}, nil, snapshot, err
	}
	endpoint, err := desktopipc.ReadEndpointFile(endpointPath)
	if err != nil {
		return endpoint, nil, snapshot, fmt.Errorf("read daemon endpoint: %w", err)
	}
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, _, err := desktopipc.Connect(connectCtx, endpoint, "azem-cli", 0)
	if err != nil {
		return endpoint, nil, snapshot, err
	}
	if err := client.Request(connectCtx, desktopipc.MethodReconnectSnapshot, map[string]string{}, &snapshot); err != nil {
		client.Close()
		return endpoint, nil, snapshot, err
	}
	return endpoint, client, snapshot, nil
}
