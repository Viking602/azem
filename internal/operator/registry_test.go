package operator

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRegistryResolvesLongestPathAliasesAndDeterministicHelp(t *testing.T) {
	registry := New()
	var called string
	for _, command := range []Command{
		{Path: []string{"auth-broker"}, Summary: "Broker operations", Handler: func(context.Context, []string, IO) error { called = "broker"; return nil }},
		{Path: []string{"auth-broker", "serve"}, Aliases: [][]string{{"broker", "serve"}}, Summary: "Serve broker", Handler: func(_ context.Context, args []string, _ IO) error {
			called = "serve:" + strings.Join(args, ",")
			return nil
		}},
		{Path: []string{"version"}, Summary: "Print version", Handler: func(context.Context, []string, IO) error { called = "version"; return nil }},
	} {
		if err := registry.Register(command); err != nil {
			t.Fatal(err)
		}
	}
	streams := IO{In: &bytes.Buffer{}, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	matched, err := registry.Run(context.Background(), []string{"broker", "serve", "--bind", "127.0.0.1:1"}, streams)
	if err != nil || !matched || called != "serve:--bind,127.0.0.1:1" {
		t.Fatalf("matched=%v called=%q error=%v", matched, called, err)
	}
	if matched, err := registry.Run(context.Background(), []string{"unknown"}, streams); err != nil || matched {
		t.Fatalf("unknown matched=%v error=%v", matched, err)
	}
	var help bytes.Buffer
	if err := registry.WriteHelp(&help, "azem"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), "auth-broker serve") || strings.Index(help.String(), "auth-broker ") > strings.Index(help.String(), "version") {
		t.Fatalf("help=%s", help.String())
	}
}

func TestRegistryRejectsConflictsInvalidPathsAndMissingHandlers(t *testing.T) {
	registry := New()
	handler := func(context.Context, []string, IO) error { return nil }
	if err := registry.Register(Command{Path: []string{"demo"}, Aliases: [][]string{{"d"}}, Summary: "Demo", Handler: handler}); err != nil {
		t.Fatal(err)
	}
	for _, command := range []Command{
		{Path: []string{"demo"}, Summary: "Duplicate", Handler: handler},
		{Path: []string{"d"}, Summary: "Alias collision", Handler: handler},
		{Path: []string{"bad/path"}, Summary: "Invalid", Handler: handler},
		{Path: []string{"missing"}, Summary: "Missing"},
	} {
		if err := registry.Register(command); err == nil {
			t.Fatalf("accepted command=%#v", command)
		}
	}
}
