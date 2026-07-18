package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/tui"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "azem: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var configFile string
	var showVersion bool
	flag.StringVar(&configFile, "config", "", "path to config.yaml")
	flag.BoolVar(&showVersion, "version", false, "print version")
	flag.Parse()
	if showVersion {
		fmt.Println(version)
		return nil
	}
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get startup workspace: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	boot, err := app.Bootstrap(ctx, workspace, configFile)
	if err != nil {
		return err
	}
	if err := boot.Validate(); err != nil {
		return err
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
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
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
		return runErr
	}
	return shutdownErr
}
