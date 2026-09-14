//go:build live

package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/auth"
)

// Uses only an isolated workspace/store; source credentials are read without refresh.
func TestLiveDevinNativeTools(t *testing.T) {
	if os.Getenv("AZEM_LIVE_DEVIN") != "1" {
		t.Skip("set AZEM_LIVE_DEVIN=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root, workspace := t.TempDir(), t.TempDir()
	t.Setenv("AZEM_HOME", root)
	configPath := filepath.Join(root, "config.yaml")
	config := fmt.Sprintf("version: 1\nworkspace:\n  root: %s\n  allow_write: true\nauth:\n  store: file\n  import_codex: false\n  import_grok: false\nmcp:\n  servers: {}\n", workspace)
	config += "agents:\n  fusion:\n    provider: devin\n    model: unavailable-fusion-sidekick\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "seed.txt"), []byte("native_before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	boot, err := Bootstrap(ctx, workspace, configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := boot.Service.Shutdown(c); err != nil {
			t.Error(err)
		}
	}()

	home, _ := os.UserHomeDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, ".azem", "azem.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := auth.NewService(db, auth.NewSQLiteStore(db), nil, nil)
	accounts, err := source.Accounts(ctx, "devin")
	if err != nil {
		t.Fatal(err)
	}
	imported := false
	for _, a := range accounts {
		if a.Status != "active" {
			continue
		}
		credential, err := source.StoredCredential(ctx, "devin", a.ID)
		if err != nil {
			t.Fatal(err)
		}
		credential.SourcePath, credential.SourceKey = "", ""
		if _, err := boot.Service.Authentication().StoreCredential(ctx, credential); err != nil {
			t.Fatal(err)
		}
		imported = true
		break
	}
	if !imported {
		t.Fatal("no active Devin account")
	}
	route := liveFusionRoute(t, ctx, boot.Service, "devin", "swe-2-max")
	active, _ := boot.Service.Authentication().Accounts(ctx, "devin")
	models, err := boot.Service.Catalog().List(ctx, "devin", active[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models.Models {
		if model.ID == route.Model {
			t.Logf("catalog model=%s max_output=%d router=%v parallel=%v", model.ID, model.MaxOutputTokens, model.DevinRouter, model.SupportsParallel)
		}
	}

	prompt := "This is an isolated tool integration test. Use the provided native read, edit, write, and exec tools: read seed.txt; edit native_before to native_after; write copy.txt with exactly native_after plus a newline; then exec a command to read both files and verify they match. Do not use shell to edit or write these files. When verified, answer NATIVE_TOOLS_OK."
	started := time.Now()
	runID, err := boot.Service.StartConfiguredTurn(TurnRequest{SessionID: boot.SessionID, Prompt: prompt, Provider: route.Provider, Model: route.Model, Reasoning: "max", AgentMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("session=%s run=%s model=swe-2-max", boot.SessionID, runID)
	for {
		event, err := boot.Service.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == EventApprovalRequested {
			if err := boot.Service.ExecuteAction(ctx, Action{Kind: ActionResolveApproval, Target: event.ApprovalID, Decision: "once"}); err != nil {
				t.Fatal(err)
			}
		}
		if event.RunID != runID {
			continue
		}
		if event.Kind == EventRunFailed || event.Kind == EventRecoveryState {
			t.Fatalf("run did not complete: %s %s", event.Kind, event.Text)
		}
		if event.Kind == EventRunFinished {
			break
		}
	}
	for deadline := time.Now().Add(3 * time.Second); boot.Service.HasActiveWork() && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	persisted, err := boot.Service.coding.LoadRunProjection(ctx, runID)
	if err != nil || persisted.Run.Status != agentruntime.RunStatusCompleted || boot.Service.HasActiveWork() {
		t.Fatalf("unsettled run: %+v %v", persisted, err)
	}
	for _, name := range []string{"seed.txt", "copy.txt"} {
		content, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil || string(content) != "native_after\n" {
			t.Fatalf("%s=%q %v", name, content, err)
		}
	}
	records, err := boot.Service.sessions.ListToolRecords(ctx, boot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	ids := map[string]bool{}
	for _, record := range records {
		if record.RunID != runID {
			continue
		}
		if ids[record.ToolCallID] {
			t.Fatal("duplicate tool execution")
		}
		ids[record.ToolCallID] = true
		names[record.Name]++
	}
	for _, name := range []string{"coding.read_file", "replace", "coding.write_file", "coding.shell"} {
		if names[name] == 0 {
			t.Fatalf("missing governed tool %s: %v", name, names)
		}
	}
	projection, err := boot.Service.sessions.LoadProjection(ctx, boot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range projection.ModelHistory.Messages {
		if strings.Contains(item.Text, fusionInstructions) {
			t.Fatal("single mode retained Fusion policy")
		}
	}
	if names["sidekick"] != 0 || projection.Session.AgentMode != "single" {
		t.Fatal("single mode entered Fusion")
	}
	found := false
	for _, block := range projection.Blocks {
		if block.RunID == runID && block.Kind == "assistant" && strings.Contains(block.Content, "NATIVE_TOOLS_OK") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing accepted final")
	}
	t.Logf("completed=%s tools=%v active=false", time.Since(started), names)
}
