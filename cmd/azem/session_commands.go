package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/operator"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/sessionexport"
	"github.com/Viking602/azem/internal/sessionimport"
	"github.com/Viking602/azem/internal/sessionshare"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func sessionListCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("session list", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	limit := flags.Int("limit", 100, "maximum sessions")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	_, store, sessions, err := openOperatorSessions(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	values, err := sessions.List(ctx, *limit)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(streams.Out).Encode(values)
	}
	for _, value := range values {
		if _, err := fmt.Fprintf(streams.Out, "%s\t%s\t%s\n", value.ID, value.UpdatedAt.Format("2006-01-02 15:04:05"), value.Title); err != nil {
			return err
		}
	}
	return nil
}

func sessionExportCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("session export", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	sessionID := flags.String("session", "", "session id")
	output := flags.String("output", "", "output file")
	format := flags.String("format", "html", "html, text, or json")
	allBranches := flags.Bool("all-branches", false, "include abandoned branches")
	excludeTools := flags.Bool("exclude-tools", false, "omit tool records")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" || *output == "" {
		return errors.New("session export requires --session and --output")
	}
	_, store, sessions, err := openOperatorSessions(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	resolved, err := sessionexport.New(sessions).ExportFile(ctx, *output, *sessionID, sessionexport.Format(*format), sessionexport.Options{AllBranches: *allBranches, ExcludeTools: *excludeTools})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, resolved)
	return err
}

func sessionForkCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("session fork", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	source := flags.String("source", "", "source session id")
	target := flags.String("target", "", "target session id")
	entry := flags.String("entry", "", "optional source graph entry")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *source == "" || *target == "" {
		return errors.New("session fork requires --source and --target")
	}
	_, store, sessions, err := openOperatorSessions(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	if *entry == "" {
		err = sessions.Fork(ctx, *source, *target)
	} else {
		err = sessions.ForkAt(ctx, *source, *target, *entry)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, *target)
	return err
}

func sessionTreeCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("session tree", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	sessionID := flags.String("session", "", "session id")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return errors.New("session tree requires --session")
	}
	_, store, sessions, err := openOperatorSessions(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	tree, err := sessions.LoadSessionTree(ctx, *sessionID)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(streams.Out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(tree)
}

func sessionLabelCommand(ctx context.Context, args []string, _ operator.IO) error {
	flags := flag.NewFlagSet("session label", flag.ContinueOnError)
	sessionID := flags.String("session", "", "session id")
	entry := flags.String("entry", "", "entry id")
	label := flags.String("label", "", "label; empty clears")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" || *entry == "" {
		return errors.New("session label requires --session and --entry")
	}
	_, store, sessions, err := openOperatorSessions(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	return sessions.SetSessionEntryLabel(ctx, *sessionID, *entry, *label)
}

func sessionImportCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("session import", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	source := flags.String("source", "", "claude or codex")
	path := flags.String("path", "", "source JSONL")
	target := flags.String("target", "", "target session id")
	workspace := flags.String("workspace", "", "fallback workspace")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*source != "claude" && *source != "codex") || *path == "" || *target == "" {
		return errors.New("session import requires --source claude|codex, --path, and --target")
	}
	absolute, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	stat, err := os.Stat(absolute)
	if err != nil {
		return err
	}
	paths, store, sessions, err := openOperatorSessions(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	info := sessionimport.Info{Source: sessionimport.Source(*source), ID: strings.TrimSuffix(filepath.Base(absolute), filepath.Ext(absolute)), Path: absolute, Workspace: *workspace, CreatedAt: stat.ModTime(), UpdatedAt: stat.ModTime()}
	importer := sessionimport.New(sessions, app.AttachmentStore{Root: filepath.Join(paths.StateDir, "attachments")})
	loaded, err := importer.Import(ctx, info, *target, *workspace)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, loaded.ID)
	return err
}

func sessionShareCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("session share", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	sessionID := flags.String("session", "", "session id")
	serverURL := flags.String("server-url", "", "share server URL")
	storeKind := flags.String("store", "blob", "blob or gist")
	allBranches := flags.Bool("all-branches", false, "include abandoned branches")
	raw := flags.Bool("no-redact", false, "disable default secret redaction")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" || *serverURL == "" {
		return errors.New("session share requires --session and --server-url")
	}
	_, store, sessions, err := openOperatorSessions(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	result, err := sessionshare.New(sessions).Share(ctx, *sessionID, sessionshare.Options{ServerURL: *serverURL, Store: sessionshare.Store(*storeKind), AllBranches: *allBranches, DisableRedaction: *raw})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, result.URL)
	return err
}

func openOperatorSessions(ctx context.Context, configFile string) (config.Paths, *sqlitestore.Provider, *session.Service, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.Paths{}, nil, nil, err
	}
	paths, err := resolveOperatorPaths(cwd, configFile)
	if err != nil {
		return config.Paths{}, nil, nil, err
	}
	store, err := sqlitestore.Open(ctx, paths.Database)
	if err != nil {
		return config.Paths{}, nil, nil, err
	}
	return paths, store, session.NewService(store.DB(), store.Blobs()), nil
}
