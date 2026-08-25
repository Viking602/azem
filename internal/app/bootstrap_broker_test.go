package app

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/authbroker"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestBootstrapUsesBrokerBackedReadOnlyCredentialStore(t *testing.T) {
	ctx := context.Background()
	brokerStore, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "broker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer brokerStore.Close(ctx)
	brokerAuth := auth.NewService(brokerStore.DB(), auth.NewSQLiteStore(brokerStore.DB()), nil, nil)
	if _, err := brokerAuth.StoreCredential(ctx, auth.Credential{Provider: "chatgpt", AccountID: "broker-account", AccessToken: "broker-access", RefreshToken: "broker-refresh", Email: "broker@example.com", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	const token = "broker-bootstrap-token-abcdefghijklmnopqrstuvwxyz"
	broker, err := authbroker.New(authbroker.Options{DB: brokerStore.DB(), Auth: brokerAuth, Tokens: []string{token}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker.Handler())
	defer server.Close()
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("AZEM_HOME", home)
	t.Setenv("OMP_AUTH_BROKER_URL", server.URL)
	t.Setenv("OMP_AUTH_BROKER_TOKEN", token)
	configPath := filepath.Join(home, "config.yaml")
	configBody := fmt.Sprintf(`version: 1
defaults:
  provider: chatgpt
  model: gpt-test
  reasoning: minimal
  agent_mode: single
  theme: system
workspace:
  root: %s
  allow_write: true
  shell_policy: prompt
  allow_network: prompt
auth:
  store: sqlite
  import_codex: false
  import_grok: false
providers:
  chatgpt:
    enabled: true
    catalog_ttl: 5m
  grok:
    enabled: false
    catalog_ttl: 5m
  cursor:
    enabled: false
    catalog_ttl: 5m
mcp:
  servers: {}
`, workspace)
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	boot, err := Bootstrap(ctx, workspace, configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = boot.Service.Shutdown(shutdownCtx)
	}()
	accounts, err := boot.Service.Authentication().Accounts(ctx, "chatgpt")
	if err != nil || len(accounts) != 1 || accounts[0].ID != "broker-account" {
		t.Fatalf("broker accounts=%#v error=%v", accounts, err)
	}
	credential, err := boot.Service.Authentication().Credential(ctx, "chatgpt", "broker-account")
	if err != nil || credential.AccessToken != "broker-access" || credential.RefreshToken != "" {
		t.Fatalf("broker credential=%#v error=%v", credential, err)
	}
	if _, err := boot.Service.Authentication().StoreCredential(ctx, auth.Credential{Provider: "chatgpt", AccountID: "local", AccessToken: "local"}); err == nil {
		t.Fatal("broker-backed runtime accepted local credential write")
	}
	if boot.Config.Auth.Broker.Token != "" {
		t.Fatal("resolved broker token remained in runtime config")
	}
}
