package azem

import (
	"context"
	"errors"
	"io"
	"time"

	azemacp "github.com/Viking602/azem/internal/acp"
	azemrpc "github.com/Viking602/azem/internal/rpc"
)

type ProtocolOptions struct {
	Runtime Options
	Input   io.Reader
	Output  io.Writer
	Version string
}

func ServeRPC(ctx context.Context, options ProtocolOptions) error {
	if options.Input == nil || options.Output == nil {
		return errors.New("azem: RPC input and output are required")
	}
	runtime, err := openRuntime(ctx, options.Runtime, false)
	if err != nil {
		return err
	}
	server, err := azemrpc.New(azemrpc.Options{
		Service: runtime.service, Sessions: runtime.sessions, SessionID: runtime.sessionID,
		Input: options.Input, Output: options.Output,
	})
	if err != nil {
		closeRuntime(runtime)
		return err
	}
	serveErr := server.Serve(ctx)
	return errors.Join(serveErr, closeRuntime(runtime))
}

func ServeACP(ctx context.Context, options ProtocolOptions) error {
	if options.Input == nil || options.Output == nil {
		return errors.New("azem: ACP input and output are required")
	}
	runtime, err := openRuntime(ctx, options.Runtime, false)
	if err != nil {
		return err
	}
	server, err := azemacp.New(azemacp.Options{
		Service: runtime.service, Sessions: runtime.sessions, SessionID: runtime.sessionID, Workspace: runtime.workspace,
		Input: options.Input, Output: options.Output, Version: options.Version,
	})
	if err != nil {
		closeRuntime(runtime)
		return err
	}
	serveErr := server.Serve(ctx)
	return errors.Join(serveErr, closeRuntime(runtime))
}

func closeRuntime(runtime *Runtime) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return runtime.Close(ctx)
}
