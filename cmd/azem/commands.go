package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/authbroker"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/operator"
)

func operatorStreams() operator.IO { return operator.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr} }

func buildOperatorRegistry(launch func([]string) error) (*operator.Registry, error) {
	registry := operator.New()
	register := func(command operator.Command) error { return registry.Register(command) }
	commands := []operator.Command{
		{Path: []string{"help"}, Aliases: [][]string{{"h"}}, Summary: "Show command help", Handler: func(_ context.Context, _ []string, streams operator.IO) error {
			return registry.WriteHelp(streams.Out, "azem")
		}},
		{Path: []string{"completion"}, Aliases: [][]string{{"completions"}}, Summary: "Generate bash, zsh, or fish completions", Handler: completionCommand(registry)},
		{Path: []string{"version"}, Summary: "Print version information", Handler: func(_ context.Context, _ []string, streams operator.IO) error { printVersion(streams.Out); return nil }},
		{Path: []string{"run"}, Summary: "Start the coding agent", Hidden: true, Handler: func(_ context.Context, args []string, _ operator.IO) error { return launch(args) }},
		{Path: []string{"rpc"}, Summary: "Serve Azem JSON-RPC over stdio", Handler: func(_ context.Context, args []string, _ operator.IO) error {
			return launch(append([]string{"--mode", "rpc"}, args...))
		}},
		{Path: []string{"rpc-ui"}, Summary: "Serve JSON-RPC with UI event support", Handler: func(_ context.Context, args []string, _ operator.IO) error {
			return launch(append([]string{"--mode", "rpc-ui"}, args...))
		}},
		{Path: []string{"acp"}, Summary: "Serve Agent Client Protocol v2 over stdio", Handler: func(_ context.Context, args []string, _ operator.IO) error {
			return launch(append([]string{"--mode", "acp"}, args...))
		}},
		{Path: []string{"session", "list"}, Summary: "List durable sessions", Handler: sessionListCommand},
		{Path: []string{"session", "export"}, Summary: "Export a session to HTML, text, or JSON", Handler: sessionExportCommand},
		{Path: []string{"session", "import"}, Summary: "Import a Claude or Codex JSONL session", Handler: sessionImportCommand},
		{Path: []string{"session", "share"}, Summary: "Publish an encrypted session snapshot", Handler: sessionShareCommand},
		{Path: []string{"session", "fork"}, Summary: "Fork a durable session", Handler: sessionForkCommand},
		{Path: []string{"session", "tree"}, Summary: "Print the session graph", Handler: sessionTreeCommand},
		{Path: []string{"daemon", "serve"}, Summary: "Serve the persistent GPUI workspace runtime", Handler: daemonServeCommand},
		{Path: []string{"daemon", "status"}, Summary: "Inspect a workspace daemon", Handler: daemonStatusCommand},
		{Path: []string{"daemon", "stop"}, Summary: "Stop a workspace daemon", Handler: daemonStopCommand},
		{Path: []string{"bench"}, Summary: "Benchmark models through the auth gateway", Handler: benchCommand},
		{Path: []string{"webhook", "serve"}, Summary: "Serve verified GitHub repair webhooks", Handler: webhookServeCommand},
		{Path: []string{"usage"}, Aliases: [][]string{{"stats"}}, Summary: "Show the local usage statistics dashboard", Handler: usageCommand},
		{Path: []string{"setup"}, Summary: "Initialize Azem configuration and data directories", Handler: setupCommand},
		{Path: []string{"update"}, Summary: "Check or apply a verified Azem update", Handler: updateCommand},
		{Path: []string{"gc"}, Summary: "Report or remove orphaned Azem data", Handler: gcCommand},
		{Path: []string{"session", "label"}, Summary: "Set or clear a session entry label", Handler: sessionLabelCommand},
		{Path: []string{"auth-broker", "serve"}, Summary: "Serve the credential broker", Handler: authBrokerServeCommand},
		{Path: []string{"auth-broker", "token"}, Summary: "Print or rotate the auth broker token", Handler: tokenCommand("auth-broker.token")},
		{Path: []string{"auth-broker", "status"}, Summary: "Check auth broker health", Handler: brokerStatusCommand},
		{Path: []string{"auth-gateway", "serve"}, Summary: "Serve the broker-backed provider gateway", Handler: authGatewayServeCommand},
		{Path: []string{"auth-gateway", "token"}, Summary: "Print or rotate the auth gateway token", Handler: tokenCommand("auth-gateway.token")},
		{Path: []string{"auth-gateway", "status"}, Summary: "Check auth gateway health", Handler: gatewayStatusCommand},
	}
	for _, command := range commands {
		if err := register(command); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func tokenCommand(filename string) operator.Handler {
	return func(_ context.Context, args []string, streams operator.IO) error {
		flags := flag.NewFlagSet(strings.TrimSuffix(filename, ".token")+" token", flag.ContinueOnError)
		flags.SetOutput(streams.Err)
		regenerate := flags.Bool("regenerate", false, "rotate the token")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		pathOverride := flags.String("path", "", "token file path")
		if err := flags.Parse(args); err != nil {
			return err
		}
		if len(flags.Args()) > 0 {
			return errors.New("token command accepts flags only")
		}
		path := strings.TrimSpace(*pathOverride)
		if path == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			paths, err := config.ResolvePaths(cwd)
			if err != nil {
				return err
			}
			path = filepath.Join(paths.ConfigDir, filename)
		}
		token, err := authbroker.EnsureToken(path, *regenerate)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(streams.Out).Encode(map[string]string{"token": token, "path": path})
		}
		_, err = fmt.Fprintln(streams.Out, token)
		return err
	}
}

func brokerStatusCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("auth-broker status", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	baseURL := flags.String("url", firstNonempty(os.Getenv("AZEM_AUTH_BROKER_URL"), os.Getenv("OMP_AUTH_BROKER_URL")), "broker base URL")
	token := flags.String("token", firstNonempty(os.Getenv("AZEM_AUTH_BROKER_TOKEN"), os.Getenv("OMP_AUTH_BROKER_TOKEN")), "broker bearer token")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	client, err := authbroker.NewClient(authbroker.ClientOptions{BaseURL: *baseURL, Token: *token})
	if err != nil {
		return err
	}
	err = client.Health(ctx)
	if *jsonOutput {
		status := map[string]any{"ok": err == nil, "url": *baseURL}
		if err != nil {
			status["error"] = err.Error()
		}
		return json.NewEncoder(streams.Out).Encode(status)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, "auth broker is healthy")
	return err
}

func gatewayStatusCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("auth-gateway status", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	baseURL := flags.String("url", "http://127.0.0.1:4000", "gateway base URL")
	token := flags.String("token", "", "gateway bearer token")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	parsed, err := url.Parse(strings.TrimSpace(*baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isOperatorLoopback(parsed.Hostname()))) {
		return errors.New("gateway URL must use HTTPS outside loopback")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/healthz"
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if strings.TrimSpace(*token) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(*token))
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	ok := err == nil && response.StatusCode == http.StatusOK
	if response != nil {
		response.Body.Close()
	}
	if *jsonOutput {
		status := map[string]any{"ok": ok, "url": *baseURL}
		if err != nil {
			status["error"] = err.Error()
		}
		return json.NewEncoder(streams.Out).Encode(status)
	}
	if !ok {
		if err != nil {
			return err
		}
		return fmt.Errorf("auth gateway returned HTTP %d", response.StatusCode)
	}
	_, err = io.WriteString(streams.Out, "auth gateway is healthy\n")
	return err
}

func isOperatorLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func completionCommand(registry *operator.Registry) operator.Handler {
	return func(_ context.Context, args []string, streams operator.IO) error {
		if len(args) != 1 {
			return errors.New("usage: azem completion bash|zsh|fish")
		}
		return operator.WriteCompletion(streams.Out, operator.Shell(args[0]), registry, operator.CompletionOptions{
			Program: "azem",
			GlobalFlags: []string{
				"--config", "--version", "--print", "-p", "--mode", "--max-time", "--print-thoughts",
				"--auto-approve", "--yolo", "--provider", "--model", "--reasoning", "--agent-mode",
			},
		})
	}
}
