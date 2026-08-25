package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/authbroker"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	cursordriver "github.com/Viking602/azem/internal/provider/cursor"
)

// buildProviderServices assembles the credential stores, authentication
// service, model catalog, and provider runtime. It lives in its own file so
// the bootstrap composition root keeps a bounded per-file import fan-out.
func (b *bootstrapAssembly) buildProviderServices() error {
	var credentials authservice.CredentialStore
	brokerURL := firstNonempty(os.Getenv("AZEM_AUTH_BROKER_URL"), os.Getenv("OMP_AUTH_BROKER_URL"), b.cfg.Auth.Broker.URL)
	if strings.TrimSpace(brokerURL) != "" {
		token, err := resolveBrokerToken(b.cfg, b.paths.ConfigDir)
		if err != nil {
			return err
		}
		poolPath := firstNonempty(os.Getenv("AZEM_AUTH_BROKER_ACCOUNT_POOL_FILE"), os.Getenv("OMP_AUTH_BROKER_ACCOUNT_POOL_FILE"), b.cfg.Auth.Broker.AccountPoolFile)
		if poolPath != "" && !filepath.IsAbs(poolPath) {
			poolPath = filepath.Join(b.paths.ConfigDir, poolPath)
		}
		pool, err := authbroker.LoadAccountPool(poolPath)
		if err != nil {
			return err
		}
		cachePath := firstNonempty(os.Getenv("AZEM_AUTH_BROKER_SNAPSHOT_CACHE"), os.Getenv("OMP_AUTH_BROKER_SNAPSHOT_CACHE"), b.cfg.Auth.Broker.SnapshotCache)
		if cachePath == "" {
			cachePath = filepath.Join(b.paths.StateDir, "auth-broker-snapshot.enc")
		} else if !filepath.IsAbs(cachePath) {
			cachePath = filepath.Join(b.paths.ConfigDir, cachePath)
		}
		ttl := b.cfg.Auth.Broker.SnapshotTTLParsed
		if raw := firstNonempty(os.Getenv("AZEM_AUTH_BROKER_SNAPSHOT_TTL_MS"), os.Getenv("OMP_AUTH_BROKER_SNAPSHOT_TTL_MS")); raw != "" {
			milliseconds, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || milliseconds < 0 {
				return fmt.Errorf("auth broker snapshot TTL milliseconds are invalid")
			}
			ttl = time.Duration(milliseconds) * time.Millisecond
		}
		client, err := authbroker.NewClient(authbroker.ClientOptions{
			BaseURL: brokerURL, Token: token, AccountPool: pool,
			Cache: authbroker.SnapshotCache{Path: cachePath, TTL: ttl},
		})
		if err != nil {
			return err
		}
		remote := &authbroker.RemoteStore{Client: client, DB: b.store.DB()}
		if _, err := remote.Sync(b.ctx); err != nil {
			return fmt.Errorf("sync auth broker snapshot: %w", err)
		}
		go remote.RunSync(b.ctx, 15*time.Second)
		credentials = remote
		b.cfg.Auth.ImportCodex, b.cfg.Auth.ImportGrok = false, false
		b.cfg.Auth.Broker.Token = ""
	} else {
		fileCredentials, err := authservice.NewFileStore(filepath.Join(b.paths.StateDir, "credentials.json"))
		if err != nil {
			return err
		}
		routed, err := authservice.NewRoutedStore(b.store.DB(), b.cfg.Auth.Store, map[string]authservice.CredentialStore{
			"sqlite": authservice.NewSQLiteStore(b.store.DB()), "keyring": authservice.NewKeyringStore(), "file": fileCredentials,
		})
		if err != nil {
			return err
		}
		credentials = routed
	}
	b.authentication = authservice.NewService(b.store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	if strings.TrimSpace(brokerURL) == "" {
		importConfiguredCredentials(b.ctx, b.cfg, b.authentication)
	}
	b.modelCatalog = catalog.NewService(b.store.DB(), b.authentication)
	b.modelCatalog.TTL["chatgpt"] = b.cfg.Providers.ChatGPT.CatalogTTL
	b.modelCatalog.TTL["grok"] = b.cfg.Providers.Grok.CatalogTTL
	b.modelCatalog.TTL["cursor"] = b.cfg.Providers.Cursor.CatalogTTL
	b.modelCatalog.Fetchers["cursor"] = func(ctx context.Context, accountID string) ([]catalog.Model, error) {
		credential, err := b.authentication.Credential(ctx, "cursor", accountID)
		if err != nil {
			return nil, err
		}
		return cursordriver.FetchUsableModels(ctx, cursordriver.CatalogConfig{AccessToken: credential.AccessToken})
	}
	providerRuntime, err := NewProviderRuntime(b.cfg, b.authentication, b.modelCatalog, b.coding, filepath.Join(b.paths.DataDir, "subagent-worktrees"))
	b.providerRuntime = providerRuntime
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

func resolveBrokerToken(cfg config.Config, configDir string) (string, error) {
	if token := firstNonempty(os.Getenv("AZEM_AUTH_BROKER_TOKEN"), os.Getenv("OMP_AUTH_BROKER_TOKEN")); token != "" {
		return token, nil
	}
	if reference := strings.TrimSpace(cfg.Auth.Broker.Token); reference != "" {
		token, err := config.ResolveReference(reference, os.LookupEnv, authservice.LookupKeyringSecret)
		if err != nil {
			return "", fmt.Errorf("resolve auth broker token: %w", err)
		}
		if len(strings.TrimSpace(token)) < 32 {
			return "", fmt.Errorf("resolved auth broker token is too short")
		}
		return strings.TrimSpace(token), nil
	}
	token, err := authbroker.LoadToken(filepath.Join(configDir, "auth-broker.token"))
	if err != nil {
		return "", fmt.Errorf("auth broker URL is configured but no token is available: %w", err)
	}
	return token, nil
}
