package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/desktopipc"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/sessionexport"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type sessionOpsRuntime struct {
	inertRuntime
	sessions *session.Service
	actions  []Action
}

func (runtime *sessionOpsRuntime) Request(ctx context.Context, method desktopipc.Method, payload any, target any) error {
	encoded, _ := json.Marshal(payload)
	var params map[string]any
	_ = json.Unmarshal(encoded, &params)
	sessionID, _ := params["sessionId"].(string)
	var result any
	var err error
	switch method {
	case desktopipc.MethodSessionTree:
		result, err = runtime.sessions.LoadSessionTree(ctx, sessionID)
	case desktopipc.MethodSetSessionEntryLabel:
		err = runtime.sessions.SetSessionEntryLabel(ctx, sessionID, params["entryId"].(string), params["label"].(string))
		if err == nil {
			result, err = runtime.sessions.LoadSessionTree(ctx, sessionID)
		}
	case desktopipc.MethodNavigateSessionTree:
		_, err = runtime.sessions.NavigateSessionTree(ctx, sessionID, params["entryId"].(string))
		result = map[string]any{}
	case desktopipc.MethodExportSession:
		result, err = sessionexport.New(runtime.sessions).ExportFile(ctx, params["outputPath"].(string), sessionID, sessionexport.Format(params["format"].(string)), sessionexport.Options{})
	case desktopipc.MethodUsageReport:
		result, err = runtime.sessions.UsageReport(ctx, session.UsageReportQuery{Scope: session.UsageScopeAll})
	default:
		return errActionUnsupported
	}
	if err != nil || target == nil {
		return err
	}
	encoded, _ = json.Marshal(result)
	return json.Unmarshal(encoded, target)
}

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
