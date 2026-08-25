package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type sessionOpsRuntime struct {
	sessions *session.Service
	actions  []Action
}

func (runtime *sessionOpsRuntime) NextEvent(context.Context) (app.Event, error) {
	return app.Event{}, errors.New("closed")
}
func (runtime *sessionOpsRuntime) StartTurn(string) (string, error) { return "", nil }
func (runtime *sessionOpsRuntime) CancelActive() bool               { return false }
func (runtime *sessionOpsRuntime) Sessions() *session.Service       { return runtime.sessions }
func (runtime *sessionOpsRuntime) ExecuteAction(_ context.Context, action Action) error {
	runtime.actions = append(runtime.actions, action)
	return nil
}

func TestTUISessionTreeLabelBranchExportAndUsageCommands(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	sessions := session.NewService(store.DB(), store.Blobs())
	if _, err := sessions.Ensure(ctx, session.Session{ID: "session", Title: "TUI"}); err != nil {
		t.Fatal(err)
	}
	for _, block := range []session.Block{{Kind: "user", Content: "one"}, {Kind: "assistant", Content: "two"}} {
		if _, err := sessions.AppendBlock(ctx, "session", block); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &sessionOpsRuntime{sessions: sessions}
	model := NewModel(runtime, t.TempDir(), "chatgpt", "model", "low", "single", "session")
	_, command := model.executeCommand(Command{Name: "tree"})
	if command == nil {
		t.Fatal("tree command returned no work")
	}
	message := command()
	updated, _ := model.Update(message)
	model = updated.(AppModel)
	last := model.transcript[len(model.transcript)-1]
	if last.Title != "Session tree" || !strings.Contains(last.Content, `"activeBranch": "main"`) {
		t.Fatalf("tree transcript=%#v", last)
	}
	entryID := fmt.Sprintf("session:%020d", 0)
	message = runSessionOperation(runtime, "session", Command{Name: "label", Args: []string{entryID, "Checkpoint"}}, model.workspace)()
	modelValue, _ := model.Update(message)
	model = modelValue.(AppModel)
	tree, err := sessions.LoadSessionTree(ctx, "session")
	if err != nil || tree.Roots[0].Entry.Label != "Checkpoint" {
		t.Fatalf("label tree=%#v error=%v", tree, err)
	}
	message = runSessionOperation(runtime, "session", Command{Name: "branch", Args: []string{entryID}}, model.workspace)()
	branchResult := message.(sessionOperationResultMsg)
	if branchResult.Err != nil || !branchResult.Refresh {
		t.Fatalf("branch result=%#v", branchResult)
	}
	tree, _ = sessions.LoadSessionTree(ctx, "session")
	if tree.ActiveLeafEntryID != entryID {
		t.Fatalf("branch leaf=%q", tree.ActiveLeafEntryID)
	}
	exportPath := filepath.Join(t.TempDir(), "tui.json")
	message = runSessionOperation(runtime, "session", Command{Name: "export", Args: []string{exportPath, "json"}}, model.workspace)()
	if result := message.(sessionOperationResultMsg); result.Err != nil || result.Content != exportPath {
		t.Fatalf("export result=%#v", result)
	}
	message = runSessionOperation(runtime, "session", Command{Name: "usage", Args: []string{"all"}}, model.workspace)()
	if result := message.(sessionOperationResultMsg); result.Err != nil || !strings.Contains(result.Content, "Usage") {
		t.Fatalf("usage result=%#v", result)
	}
}

func TestTUISessionCommandsAreDiscoverable(t *testing.T) {
	for _, name := range []string{"tree", "branch", "fork", "label", "export", "share", "import", "usage", "collab"} {
		command, ok, err := ParseCommand("/" + name)
		if err != nil || !ok || command.Name != name {
			t.Fatalf("command %s parsed=%#v ok=%v error=%v", name, command, ok, err)
		}
	}
	if suggestions := commandSuggestions("/bra"); len(suggestions) == 0 || suggestions[0].Name != "branch" {
		t.Fatalf("branch suggestions=%#v", suggestions)
	}
}
