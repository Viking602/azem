package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Viking602/azem/internal/daemon"
)

var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("azem-daemon", flag.ContinueOnError)
	workspace := flags.String("workspace", "", "workspace owned by this daemon")
	configFile := flags.String("config", "", "path to config.yaml")
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Printf("azem-daemon %s\ngit commit: %s\nbuild time: %s\n", version, gitCommit, buildTime)
		return err
	}
	if *workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		*workspace = cwd
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runtime, err := daemon.New(ctx, daemon.Options{Workspace: *workspace, ConfigFile: *configFile})
	if err != nil {
		return err
	}
	endpoint := runtime.Endpoint()
	_, _ = fmt.Fprintf(os.Stderr, "azem daemon ready: workspace=%s address=%s pid=%d\n", endpoint.Workspace, endpoint.Address, endpoint.PID)
	return runtime.Run()
}
