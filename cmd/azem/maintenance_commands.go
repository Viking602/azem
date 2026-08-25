package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Viking602/azem/internal/maintenance"
	"github.com/Viking602/azem/internal/operator"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func setupCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	workspace := flags.String("workspace", "", "workspace directory")
	configFile := flags.String("config", "", "config file path")
	force := flags.Bool("force", false, "replace the existing config")
	dryRun := flags.Bool("dry-run", false, "show changes without writing")
	jsonOutput := flags.Bool("json", false, "emit JSON")
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
	report, err := maintenance.Setup(ctx, maintenance.SetupOptions{Workspace: *workspace, ConfigFile: *configFile, Force: *force, DryRun: *dryRun})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(streams.Out).Encode(report)
	}
	_, err = fmt.Fprintf(streams.Out, "config: %s\ncreated: %d\nexisting: %d\n", report.Paths.ConfigFile, len(report.Created), len(report.Existing))
	return err
}

func updateCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	apply := flags.Bool("apply", false, "download and install the verified update")
	apiURL := flags.String("api-url", "", "release API URL")
	repository := flags.String("repository", "Viking602/azem", "GitHub repository")
	executable := flags.String("executable", "", "executable to replace")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	updater, err := maintenance.NewUpdater(maintenance.UpdateOptions{CurrentVersion: version, Repository: *repository, APIURL: *apiURL, Executable: *executable})
	if err != nil {
		return err
	}
	check, err := updater.Check(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		if err := json.NewEncoder(streams.Out).Encode(check); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(streams.Out, "current: %s\nlatest: %s\navailable: %t\n", check.Current, check.Latest, check.Available)
	}
	if !*apply {
		return nil
	}
	if !check.Available {
		return errors.New("no update is available")
	}
	if err := updater.Apply(ctx, check); err != nil {
		return err
	}
	if !*jsonOutput {
		_, err = fmt.Fprintln(streams.Out, "update installed")
	}
	return err
}

func gcCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("gc", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	apply := flags.Bool("apply", false, "remove reported candidates")
	minimumAge := flags.Duration("minimum-age", 24*time.Hour, "minimum candidate age")
	logRetention := flags.Duration("log-retention", 30*24*time.Hour, "log retention")
	vacuum := flags.Bool("vacuum", false, "vacuum SQLite after applying")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	paths, err := resolveOperatorPaths(cwd, *configFile)
	if err != nil {
		return err
	}
	blobRoot := filepath.Join(paths.DataDir, "blobs")
	store, err := sqlitestore.Open(ctx, paths.Database, sqlitestore.WithBlobRoot(blobRoot))
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	report, err := maintenance.Collect(ctx, maintenance.GCOptions{
		DB: store.DB(), BlobRoot: blobRoot, AttachmentRoot: filepath.Join(paths.StateDir, "attachments"),
		LogRoot: filepath.Dir(paths.LogFile), MinimumAge: *minimumAge, LogRetention: *logRetention, Apply: *apply, Vacuum: *vacuum,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(streams.Out).Encode(report)
	}
	_, err = fmt.Fprintf(streams.Out, "candidates: %d\nbytes: %d\nremoved: %d\n", len(report.Candidates), report.BytesReclaim, report.Removed)
	return err
}
