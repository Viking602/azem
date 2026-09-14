package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/commands"
	"github.com/Viking602/azem/internal/config"
)

func TestConfiguredTurnExpandsCustomSlashCommandBeforePersistence(t *testing.T) {
	commandRoot := t.TempDir()
	commandPath := filepath.Join(commandRoot, ".omp", "commands", "review.md")
	if err := os.MkdirAll(filepath.Dir(commandPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commandPath, []byte("---\\ndescription: Review target\\n---\\nReview $1 with $@[2:]\\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, diagnostics, err := commands.Discover(commands.Options{Workspace: commandRoot, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	harness := newSkillRuntimeHarness(t, "---\\nname: demo\\ndescription: stable catalog\\n---\\nstable body\\n", nil, func(_ int, body string, writer http.ResponseWriter) {
		if !strings.Contains(body, "Review src/main.go with carefully now") || strings.Contains(body, "/review") {
			t.Errorf("custom command was not expanded before provider request: %s", body)
		}
		writeProviderText(writer, "command-response", "reviewed")
	})
	harness.service.AttachCommands(catalog, diagnostics)
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{SessionID: "custom-command", Prompt: `/review src/main.go carefully now`, Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	projection, err := harness.service.sessions.LoadProjection(context.Background(), "custom-command")
	if err != nil {
		t.Fatal(err)
	}
	var sawExpanded bool
	for _, block := range projection.Blocks {
		if strings.Contains(block.Content, "Review src/main.go with carefully now") {
			sawExpanded = true
		}
		if strings.Contains(block.Content, "/review") {
			t.Fatalf("raw custom command persisted: %#v", block)
		}
	}
	if !sawExpanded {
		t.Fatalf("expanded command missing from durable projection: %#v", projection.Blocks)
	}
}

func TestListCustomCommandsActionProjectsCatalog(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".omp", "commands", "review.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ndescription: Review\n---\nReview\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, diagnostics, err := commands.Discover(commands.Options{Workspace: root, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(context.Background(), config.Default())
	service.AttachCommands(catalog, diagnostics)
	if err := service.ExecuteAction(context.Background(), Action{Kind: ActionListCustomCommands}); err != nil {
		t.Fatal(err)
	}
	event, err := service.NextEvent(context.Background())
	if err != nil || event.Kind != EventCommandCatalog || !strings.Contains(event.Data["commands"], `"name":"review"`) {
		t.Fatalf("command catalog event = %#v, %v", event, err)
	}
}
