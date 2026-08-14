package app

import (
	"context"
	"os"
	"path/filepath"

	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
)

// buildProviderServices assembles the credential stores, authentication
// service, model catalog, and provider runtime. It lives in its own file so
// the bootstrap composition root keeps a bounded per-file import fan-out.
func (b *bootstrapAssembly) buildProviderServices() error {
	fileCredentials, err := authservice.NewFileStore(filepath.Join(b.paths.StateDir, "credentials.json"))
	if err != nil {
		return err
	}
	credentials, err := authservice.NewRoutedStore(b.store.DB(), b.cfg.Auth.Store, map[string]authservice.CredentialStore{
		"sqlite":  authservice.NewSQLiteStore(b.store.DB()),
		"keyring": authservice.NewKeyringStore(),
		"file":    fileCredentials,
	})
	if err != nil {
		return err
	}
	b.authentication = authservice.NewService(b.store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	importConfiguredCredentials(b.ctx, b.cfg, b.authentication)
	b.modelCatalog = catalog.NewService(b.store.DB(), b.authentication)
	b.modelCatalog.TTL["chatgpt"] = b.cfg.Providers.ChatGPT.CatalogTTL
	b.modelCatalog.TTL["grok"] = b.cfg.Providers.Grok.CatalogTTL
	b.providerRuntime, err = NewProviderRuntime(b.cfg, b.authentication, b.modelCatalog, b.coding, filepath.Join(b.paths.DataDir, "subagent-worktrees"))
	return err
}

func importConfiguredCredentials(ctx context.Context, cfg config.Config, authentication *authservice.Service) {
	if !cfg.Auth.ImportCodex && !cfg.Auth.ImportGrok {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if cfg.Auth.ImportCodex {
		hasAccount, accountErr := authentication.HasAnyAccount(ctx, "chatgpt")
		if accountErr != nil || !hasAccount {
			codexHome := os.Getenv("CODEX_HOME")
			if codexHome == "" {
				codexHome = filepath.Join(home, ".codex")
			}
			if _, statErr := os.Stat(filepath.Join(codexHome, "auth.json")); statErr == nil {
				_, _ = authentication.ImportChatGPT(ctx, filepath.Join(codexHome, "auth.json"))
			} else if os.IsNotExist(statErr) {
				_, _ = authentication.ImportChatGPTKeyring(ctx, codexHome)
			}
		}
	}
	if cfg.Auth.ImportGrok {
		path := filepath.Join(home, ".grok", "auth.json")
		if _, statErr := os.Stat(path); statErr == nil {
			hasAccount, accountErr := authentication.HasAnyAccount(ctx, "grok")
			if accountErr != nil || !hasAccount {
				_, _ = authentication.ImportGrok(ctx, path)
			}
		}
	}
}
