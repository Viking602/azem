package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/config"
	evalpkg "github.com/Viking602/azem/internal/eval"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestDrainTurnReportsSuspensionWithoutWaitingForTimeout(t *testing.T) {
	for _, kind := range []string{"reconciliation", "requested", ""} {
		t.Run(kind, func(t *testing.T) {
			service := app.NewService(t.Context(), config.Default())
			defer service.Shutdown(context.Background())
			const reason = "2 tool call(s) failed; requires reconciliation for 2 attempt(s)"
			service.EmitEvent(t.Context(), app.Event{
				Kind: app.EventRecoveryState, RunID: "eval-run", State: "suspended",
				Text: reason, Data: map[string]string{"kind": kind},
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := drainTurn(ctx, service, "eval-run", false)
			if err == nil || !strings.Contains(err.Error(), "suspended") ||
				!strings.Contains(err.Error(), reason) || ctx.Err() != nil {
				t.Fatalf("suspended run: error=%v context=%v", err, ctx.Err())
			}
		})
	}
}

func TestDrainTurnIgnoresUnrelatedRecovery(t *testing.T) {
	service := app.NewService(t.Context(), config.Default())
	defer service.Shutdown(context.Background())
	for _, event := range []app.Event{
		{Kind: app.EventRecoveryState, RunID: "other-run", State: "suspended"},
		{Kind: app.EventRecoveryState, State: "suspended"},
		{Kind: app.EventRecoveryState, State: "recovered"},
		{Kind: app.EventRunFinished, RunID: "eval-run", State: "completed"},
	} {
		service.EmitEvent(t.Context(), event)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := drainTurn(ctx, service, "eval-run", false); err != nil {
		t.Fatal(err)
	}
}

func TestWriteEvalConfigLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := writeEvalConfig(path, dir, "chatgpt", "gpt-5.6-sol", "high"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadAtWorkspace(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.ApprovalMode != "yolo" || cfg.Workspace.ShellPolicy != "allow" || cfg.Workspace.AllowNetwork != "allow" {
		t.Fatalf("eval config = %+v", cfg)
	}
}

func TestWriteEvalConfigEnablesLLMuxProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := writeEvalConfig(path, dir, "openrouter", "stealth/ox-alpha", "max"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadAtWorkspace(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := cfg.Providers.LLMux["openrouter"]
	if !ok || !provider.Enabled || cfg.Defaults.Provider != "openrouter" ||
		cfg.Defaults.Model != "stealth/ox-alpha" || cfg.Defaults.Reasoning != "max" {
		t.Fatalf("eval llmux config = %+v", cfg)
	}
}

func TestLoadPromptRequiresExactlyOneSource(t *testing.T) {
	if _, err := loadPrompt("hello", "x"); err == nil {
		t.Fatal("expected both-source error")
	}
	if _, err := loadPrompt("", ""); err == nil {
		t.Fatal("expected empty prompt error")
	}
	got, err := loadPrompt("fix the server", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Terminal-Bench") || !strings.Contains(got, "fix the server") {
		t.Fatalf("prompt = %q", got)
	}
}

func TestExportAuthCopiesAccountsAndCredentials(t *testing.T) {
	ctx := t.Context()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	src, err := sqlitestore.Open(ctx, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO accounts(id, provider_id, email, display_name, plan, credential_ref, status, created_at, updated_at)
		VALUES('acct', 'chatgpt', 'eval@example.com', 'Eval', 'plus', 'ref', 'active', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO auth_credentials(provider_id, account_id, data, created_at, updated_at)
		VALUES('chatgpt', 'acct', '{"token":"secret"}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := src.DB().ExecContext(ctx, `INSERT INTO llmux_provider_models(provider_id, model_id, payload, updated_at)
		VALUES('openrouter', 'stealth/ox-alpha', '{"id":"stealth/ox-alpha","contextWindow":1048576}', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(ctx); err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(t.TempDir(), "auth.db")
	if err := exportAuth(sourcePath, destPath); err != nil {
		t.Fatal(err)
	}
	dest, err := sqlitestore.Open(ctx, destPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close(ctx)
	var email, data, modelPayload string
	if err := dest.DB().QueryRowContext(ctx, `SELECT email FROM accounts WHERE id = 'acct'`).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if err := dest.DB().QueryRowContext(ctx, `SELECT data FROM auth_credentials WHERE account_id = 'acct'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if err := dest.DB().QueryRowContext(ctx, `SELECT CAST(payload AS TEXT) FROM llmux_provider_models WHERE provider_id = 'openrouter' AND model_id = 'stealth/ox-alpha'`).Scan(&modelPayload); err != nil {
		t.Fatal(err)
	}
	if email != "eval@example.com" || !strings.Contains(data, "secret") || !strings.Contains(modelPayload, "1048576") {
		t.Fatalf("email=%q data=%q model=%q", email, data, modelPayload)
	}
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 8<<20 {
		t.Fatalf("slim auth database too large: %d", info.Size())
	}
}

func TestExportTrajectoryWritesReadOnlySessionSnapshot(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	createdPath := filepath.Join(directory, "source.db")
	source, err := sqlitestore.Open(ctx, createdPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.DB().ExecContext(ctx, `INSERT INTO sessions(id, title, created_at, updated_at) VALUES('session-1', 'one', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(ctx); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(directory, "source?#.db")
	if err := os.Rename(createdPath, sourcePath); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(directory, "trajectory.json")
	if err := exportTrajectory(sourcePath, "", "session-1", outputPath); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	trajectory, err := evalpkg.ReadTrajectory(file)
	if err != nil {
		t.Fatal(err)
	}
	if trajectory.SessionID != "session-1" {
		t.Fatalf("session id = %q", trajectory.SessionID)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("trajectory mode = %o, want 600", info.Mode().Perm())
	}
}
