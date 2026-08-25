package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/config"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestGatewayRoutesRejectsMalformedStoredModel(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO llmux_provider_models(provider_id,model_id,payload,updated_at) VALUES('broken','model','{',1)`); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Providers.LLMux["broken"] = config.LLMuxProviderConfig{Enabled: true, BaseURL: "https://example.test/v1"}
	if _, err := gatewayRoutes(ctx, cfg, store); err == nil || !strings.Contains(err.Error(), "decode llmux gateway model") {
		t.Fatalf("gateway routes error = %v", err)
	}
}
