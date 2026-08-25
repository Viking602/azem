package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/authbroker"
	"github.com/Viking602/azem/internal/authgateway"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/operator"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func authBrokerServeCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("auth-broker serve", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	bind := flags.String("bind", "127.0.0.1:8765", "listen address")
	configFile := flags.String("config", "", "config file path")
	tokenPath := flags.String("token-file", "", "bearer token file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	paths, store, authentication, err := openOperatorAuth(ctx, *configFile)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	defer authentication.Close()
	path := strings.TrimSpace(*tokenPath)
	if path == "" {
		path = filepath.Join(paths.ConfigDir, "auth-broker.token")
	}
	token, err := authbroker.EnsureToken(path, false)
	if err != nil {
		return err
	}
	broker, err := authbroker.New(authbroker.Options{DB: store.DB(), Auth: authentication, Tokens: []string{token}, Version: version})
	if err != nil {
		return err
	}
	refresher, err := authbroker.NewRefresher(authbroker.RefresherOptions{DB: store.DB(), Auth: authentication})
	if err != nil {
		return err
	}
	go func() { _ = refresher.Run(ctx) }()
	_, _ = fmt.Fprintf(streams.Err, "auth broker listening on %s\n", *bind)
	return serveHTTP(ctx, *bind, broker.Handler())
}

func authGatewayServeCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("auth-gateway serve", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	bind := flags.String("bind", "127.0.0.1:4000", "listen address")
	configFile := flags.String("config", "", "config file path")
	brokerURL := flags.String("broker-url", firstNonempty(os.Getenv("AZEM_AUTH_BROKER_URL"), os.Getenv("OMP_AUTH_BROKER_URL")), "auth broker URL")
	brokerToken := flags.String("broker-token", firstNonempty(os.Getenv("AZEM_AUTH_BROKER_TOKEN"), os.Getenv("OMP_AUTH_BROKER_TOKEN")), "auth broker token")
	tokenPath := flags.String("token-file", "", "gateway bearer token file")
	noAuth := flags.Bool("no-auth", false, "disable gateway bearer auth on loopback")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	paths, err := resolveOperatorPaths(cwd, *configFile)
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.ConfigFile, cwd)
	if err != nil {
		return err
	}
	if *brokerURL == "" {
		*brokerURL = cfg.Auth.Broker.URL
	}
	if *brokerToken == "" {
		*brokerToken, err = resolveOperatorBrokerToken(cfg, paths.ConfigDir)
		if err != nil {
			return err
		}
	}
	client, err := authbroker.NewClient(authbroker.ClientOptions{BaseURL: *brokerURL, Token: *brokerToken})
	if err != nil {
		return err
	}
	store, err := sqlitestore.Open(ctx, paths.Database)
	if err != nil {
		return err
	}
	defer store.Close(context.Background())
	routes, err := gatewayRoutes(ctx, cfg, store)
	if err != nil {
		return err
	}
	gatewayToken := ""
	if !*noAuth {
		path := strings.TrimSpace(*tokenPath)
		if path == "" {
			path = filepath.Join(paths.ConfigDir, "auth-gateway.token")
		}
		gatewayToken, err = authbroker.EnsureToken(path, false)
		if err != nil {
			return err
		}
	} else if !isBindLoopback(*bind) {
		return errors.New("--no-auth is allowed only on a loopback bind address")
	}
	gateway, err := authgateway.New(authgateway.Options{Broker: client, Token: gatewayToken, NoAuth: *noAuth, Routes: routes, Version: version})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(streams.Err, "auth gateway listening on %s\n", *bind)
	return serveHTTP(ctx, *bind, gateway.Handler())
}

func serveHTTP(ctx context.Context, bind string, handler http.Handler) error {
	listener, err := net.Listen("tcp", bind)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 255 * time.Second}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = server.Shutdown(shutdownCtx)
			cancel()
		case <-done:
		}
	}()
	err = server.Serve(listener)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func openOperatorAuth(ctx context.Context, configFile string) (config.Paths, *sqlitestore.Provider, *auth.Service, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.Paths{}, nil, nil, err
	}
	paths, err := resolveOperatorPaths(cwd, configFile)
	if err != nil {
		return config.Paths{}, nil, nil, err
	}
	store, err := sqlitestore.Open(ctx, paths.Database)
	if err != nil {
		return config.Paths{}, nil, nil, err
	}
	authentication := auth.NewService(store.DB(), auth.NewSQLiteStore(store.DB()), nil, nil)
	return paths, store, authentication, nil
}

func resolveOperatorPaths(cwd, configFile string) (config.Paths, error) {
	if strings.TrimSpace(configFile) != "" {
		return config.ResolvePathsWithConfig(cwd, configFile)
	}
	return config.ResolvePaths(cwd)
}

func resolveOperatorBrokerToken(cfg config.Config, configDir string) (string, error) {
	if token := firstNonempty(os.Getenv("AZEM_AUTH_BROKER_TOKEN"), os.Getenv("OMP_AUTH_BROKER_TOKEN")); token != "" {
		return token, nil
	}
	if reference := strings.TrimSpace(cfg.Auth.Broker.Token); reference != "" {
		return config.ResolveReference(reference, os.LookupEnv, auth.LookupKeyringSecret)
	}
	return authbroker.LoadToken(filepath.Join(configDir, "auth-broker.token"))
}

func gatewayRoutes(ctx context.Context, cfg config.Config, store *sqlitestore.Provider) ([]authgateway.Route, error) {
	models := make(map[string][]string)
	for provider, value := range cfg.Providers.LLMux {
		for _, model := range value.Models {
			if !model.Disabled {
				models[provider] = append(models[provider], model.ID)
			}
		}
	}
	rows, err := store.DB().QueryContext(ctx, `SELECT provider_id,payload FROM llmux_provider_models ORDER BY provider_id,model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var provider string
		var payload []byte
		if err := rows.Scan(&provider, &payload); err != nil {
			return nil, fmt.Errorf("scan llmux gateway model: %w", err)
		}
		var model config.LLMuxModelConfig
		if err := json.Unmarshal(payload, &model); err != nil {
			return nil, fmt.Errorf("decode llmux gateway model for %s: %w", provider, err)
		}
		if model.ID != "" && !model.Disabled {
			models[provider] = append(models[provider], model.ID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate llmux gateway models: %w", err)
	}
	routes := make([]authgateway.Route, 0)
	for provider, value := range cfg.Providers.LLMux {
		providerModels := uniqueSorted(models[provider])
		if !value.Enabled || value.BaseURL == "" || len(providerModels) == 0 {
			continue
		}
		protocols := []string{"openai-chat", "openai-responses"}
		header, prefix := value.APIKeyHeader, value.APIKeyPrefix
		if strings.EqualFold(value.Backend, "anthropic") {
			protocols = []string{"anthropic-messages"}
			if header == "" {
				header = "X-Api-Key"
			}
			prefix = ""
		}
		routes = append(routes, authgateway.Route{Provider: provider, BaseURL: value.BaseURL, Models: providerModels, Protocols: protocols, AuthHeader: header, AuthPrefix: prefix})
	}
	if len(routes) == 0 {
		return nil, errors.New("no enabled llmux provider models are configured for the gateway")
	}
	return routes, nil
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func isBindLoopback(bind string) bool {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
