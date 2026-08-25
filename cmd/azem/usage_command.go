package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/Viking602/azem/internal/operator"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/usageview"
)

func usageCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("usage", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	scope := flags.String("scope", session.UsageScopeProject, "project or all")
	workspace := flags.String("workspace", "", "project workspace")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	configFile := flags.String("config", "", "config file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *scope != session.UsageScopeProject && *scope != session.UsageScopeAll {
		return errors.New("usage scope must be project or all")
	}
	paths, store, sessions, err := openOperatorSessions(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	if *workspace == "" && *scope == session.UsageScopeProject {
		*workspace = paths.Workspace
	}
	report, err := sessions.UsageReport(ctx, session.UsageReportQuery{Scope: *scope, Workspace: *workspace})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(streams.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	_, err = io.WriteString(streams.Out, usageview.Text(report))
	return err
}
