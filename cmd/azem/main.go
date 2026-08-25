package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	azemacp "github.com/Viking602/azem/internal/acp"
	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/headless"
	azemrpc "github.com/Viking602/azem/internal/rpc"
	"github.com/Viking602/azem/internal/tui"
)

var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "azem: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	return runWithArgs(os.Args[1:])
}

func runWithArgs(args []string) error {
	launch := func(args []string) error { return runLaunch(args) }
	registry, err := buildOperatorRegistry(launch)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if matched, err := registry.Run(ctx, args, operatorStreams()); matched {
		return err
	}
	return runLaunch(args)
}

func runLaunch(args []string) error {
	options, err := parseCLI(args, os.Stderr)
	if err != nil {
		return err
	}
	if options.showVersion {
		printVersion(os.Stdout)
		return nil
	}
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get startup workspace: %w", err)
	}
	protocolMode := options.mode == "rpc" || options.mode == "rpc-ui" || options.mode == "acp"
	pipedInput := ""
	if !protocolMode {
		pipedInput, err = readPipedInput(os.Stdin, 16<<20)
		if err != nil {
			return err
		}
	}
	autoPrint := pipedInput != "" && !options.print && options.mode == ""
	headlessMode := options.print || autoPrint || options.mode == "text" || options.mode == "json"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	boot, err := app.Bootstrap(ctx, workspace, options.configFile)
	if err != nil {
		return err
	}
	if err := boot.Validate(); err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			cancel()
			boot.Service.CancelActiveWithChildren(true)
		case <-ctx.Done():
		}
	}()
	if options.mode == "rpc" || options.mode == "rpc-ui" {
		if len(options.prompts) > 0 {
			return errors.New("RPC mode owns stdin and does not accept positional prompts")
		}
		boot.Service.SetDesktopSurface(false)
		server, err := azemrpc.New(azemrpc.Options{
			Service: boot.Service, Sessions: boot.Service.Sessions(), SessionID: boot.SessionID,
			Input: os.Stdin, Output: os.Stdout,
		})
		if err != nil {
			return err
		}
		serveErr := server.Serve(ctx)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return errors.Join(serveErr, boot.Service.Shutdown(shutdownCtx))
	}
	if options.mode == "acp" {
		if len(options.prompts) > 0 {
			return errors.New("ACP mode owns stdin and does not accept positional prompts")
		}
		if info, statErr := os.Stdin.Stat(); statErr == nil && info.Mode()&os.ModeCharDevice != 0 {
			_, _ = fmt.Fprintln(os.Stderr, "azem acp: ACP v2 server speaking JSON-RPC over stdio; waiting for a client")
		}
		boot.Service.SetDesktopSurface(false)
		server, err := azemacp.New(azemacp.Options{
			Service: boot.Service, Sessions: boot.Service.Sessions(), SessionID: boot.SessionID,
			Workspace: boot.Paths.Workspace, Input: os.Stdin, Output: os.Stdout, Version: version,
		})
		if err != nil {
			return err
		}
		serveErr := server.Serve(ctx)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return errors.Join(serveErr, boot.Service.Shutdown(shutdownCtx))
	}
	if headlessMode {
		boot.Service.SetDesktopSurface(false)
		runCtx := ctx
		runCancel := func() {}
		if options.maxTime > 0 {
			runCtx, runCancel = context.WithTimeout(ctx, options.maxTime)
		}
		defer runCancel()
		prompts := combinePrompts(pipedInput, options.prompts)
		mode := headless.ModeText
		if options.mode == "json" {
			mode = headless.ModeJSON
		}
		_, runErr := headless.Run(runCtx, boot.Service, headless.Options{
			Mode: mode, SessionID: boot.SessionID, Provider: firstNonempty(options.provider, boot.Config.Defaults.Provider),
			Model: firstNonempty(options.model, boot.Config.Defaults.Model), Reasoning: firstNonempty(options.reasoning, boot.Config.Defaults.Reasoning),
			AgentMode: firstNonempty(options.agentMode, boot.Config.Defaults.AgentMode), Prompts: prompts,
			PrintThinking: options.printThinking, AutoApprove: options.autoApprove, Output: os.Stdout, Diagnostics: os.Stderr,
		})
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		shutdownErr := boot.Service.Shutdown(shutdownCtx)
		return errors.Join(runErr, shutdownErr)
	}
	model := tui.NewModel(
		boot.Service,
		boot.Paths.Workspace,
		boot.Config.Defaults.Provider,
		boot.Config.Defaults.Model,
		boot.Config.Defaults.Reasoning,
		boot.Config.Defaults.AgentMode,
		boot.SessionID,
	)
	if err := model.SetLanguage(boot.Config.Defaults.Language); err != nil {
		return err
	}
	program := tea.NewProgram(model, tea.WithoutSignalHandler())
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = boot.Service.Shutdown(shutdownCtx)
		program.Quit()
	}()
	_, runErr := program.Run()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	shutdownErr := boot.Service.Shutdown(shutdownCtx)
	if runErr != nil && !errors.Is(runErr, tea.ErrInterrupted) {
		return errors.Join(runErr, shutdownErr)
	}
	return shutdownErr
}

type cliOptions struct {
	configFile    string
	showVersion   bool
	print         bool
	mode          string
	maxTime       time.Duration
	printThinking bool
	autoApprove   bool
	provider      string
	model         string
	reasoning     string
	agentMode     string
	prompts       []string
}

func parseCLI(args []string, diagnostics io.Writer) (cliOptions, error) {
	var options cliOptions
	var maxTime string
	flags := flag.NewFlagSet("azem", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	flags.StringVar(&options.configFile, "config", "", "path to config.yaml")
	flags.BoolVar(&options.showVersion, "version", false, "print version")
	flags.BoolVar(&options.print, "print", false, "run non-interactively and exit")
	flags.BoolVar(&options.print, "p", false, "run non-interactively and exit")
	flags.StringVar(&options.mode, "mode", "", "output mode: text, json, rpc, rpc-ui, or acp")
	flags.StringVar(&maxTime, "max-time", "", "maximum run duration (seconds or Go duration)")
	flags.BoolVar(&options.printThinking, "print-thoughts", false, "include model thinking in text output")
	flags.BoolVar(&options.autoApprove, "auto-approve", false, "approve governed tool calls for this headless run")
	flags.BoolVar(&options.autoApprove, "yolo", false, "approve governed tool calls for this headless run")
	flags.StringVar(&options.provider, "provider", "", "provider override")
	flags.StringVar(&options.model, "model", "", "model override")
	flags.StringVar(&options.reasoning, "reasoning", "", "reasoning effort override")
	flags.StringVar(&options.agentMode, "agent-mode", "", "agent mode override")
	if err := flags.Parse(args); err != nil {
		return cliOptions{}, err
	}
	switch options.mode {
	case "", "text", "json", "rpc", "rpc-ui", "acp":
	default:
		return cliOptions{}, fmt.Errorf("unsupported mode %q", options.mode)
	}
	if strings.TrimSpace(maxTime) != "" {
		duration, err := parseMaxTime(maxTime)
		if err != nil {
			return cliOptions{}, err
		}
		options.maxTime = duration
	}
	options.prompts = append([]string(nil), flags.Args()...)
	return options, nil
}

func parseMaxTime(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0, errors.New("max-time must be positive")
		}
		return time.Duration(seconds) * time.Second, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("invalid max-time %q", value)
	}
	return duration, nil
}

func readPipedInput(input *os.File, limit int64) (string, error) {
	info, err := input.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	payload, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(payload)) > limit {
		return "", fmt.Errorf("piped prompt exceeds %d bytes", limit)
	}
	return strings.TrimSpace(string(payload)), nil
}

func combinePrompts(piped string, positional []string) []string {
	piped = strings.TrimSpace(piped)
	result := append([]string(nil), positional...)
	if piped == "" {
		return result
	}
	if len(result) == 0 {
		return []string{piped}
	}
	result[0] = piped + "\n" + result[0]
	return result
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "azem %s\ngit commit: %s\nbuild time: %s\n", version, gitCommit, buildTime)
}
