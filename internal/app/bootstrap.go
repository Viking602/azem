package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentservice "github.com/Viking602/azem/internal/agent"
	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/recovery"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type BootstrapResult struct {
	Config    config.Config
	Paths     config.Paths
	SessionID string
	Service   *Service
}

func Bootstrap(ctx context.Context, startupWorkspace string, configFile string) (BootstrapResult, error) {
	paths, err := config.ResolvePaths(startupWorkspace)
	if err != nil {
		return BootstrapResult{}, err
	}
	if configFile != "" {
		paths.ConfigFile = configFile
		paths.ConfigDir = directoryOf(configFile)
	}
	cfg, err := config.Load(paths.ConfigFile, paths.Workspace)
	if err != nil {
		return BootstrapResult{}, err
	}
	paths.Workspace = cfg.Workspace.Root
	if err := config.EnsureDirectories(paths); err != nil {
		return BootstrapResult{}, err
	}
	store, err := sqlitestore.Open(ctx, paths.Database)
	if err != nil {
		return BootstrapResult{}, err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, fmt.Errorf("resolve user home for skills: %w", err)
	}
	configDir, err := filepath.Abs(filepath.Dir(paths.ConfigFile))
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, fmt.Errorf("resolve config directory for skills: %w", err)
	}
	skillCatalog, err := skills.Load(skills.LoadOptions{
		HomeDir:      homeDir,
		ConfigDir:    configDir,
		WorkspaceDir: paths.Workspace,
		Config:       cfg.Skills,
	})
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, fmt.Errorf("load skills: %w", err)
	}
	sessions := session.NewService(store.DB())
	startupSessionID, err := randomID("session")
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, fmt.Errorf("create startup session id: %w", err)
	}
	coding, err := agentservice.NewService(store, paths.Workspace,
		agentservice.WithWorkspacePolicy(cfg.Workspace.AllowWrite, cfg.Workspace.ShellPolicy, cfg.Workspace.AllowNetwork),
		agentservice.WithTeamLimits(cfg.Agents.Team.MaxConcurrency, cfg.Agents.Team.MaxTicks),
		agentservice.WithSkills(skillCatalog),
	)
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, err
	}
	subagentRuns, err := agentservice.NewSQLSubagentRunStore(store.DB())
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, err
	}
	fileCredentials, err := authservice.NewFileStore(filepath.Join(paths.StateDir, "credentials.json"))
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, err
	}
	credentials, err := authservice.NewRoutedStore(store.DB(), cfg.Auth.Store, map[string]authservice.CredentialStore{
		"sqlite":  authservice.NewSQLiteStore(store.DB()),
		"keyring": authservice.NewKeyringStore(),
		"file":    fileCredentials,
	})
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, err
	}
	authentication := authservice.NewService(store.DB(), credentials, chatgpt.NewClient(), grok.NewClient())
	importConfiguredCredentials(ctx, cfg, authentication)
	modelCatalog := catalog.NewService(store.DB(), authentication)
	modelCatalog.TTL["chatgpt"] = cfg.Providers.ChatGPT.CatalogTTL
	modelCatalog.TTL["grok"] = cfg.Providers.Grok.CatalogTTL
	providerRuntime, err := NewProviderRuntime(cfg, authentication, modelCatalog, coding, filepath.Join(paths.DataDir, "subagent-worktrees"))
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, err
	}
	service := NewService(ctx, cfg)
	sources := hookSources(cfg.Hooks, configDir, homeDir, paths.Workspace)
	hookOptions := hooks.Options{Sources: sources, DefaultTimeout: cfg.Hooks.DefaultTimeoutParsed, FailurePolicy: hooks.FailurePolicy(cfg.Hooks.FailurePolicy)}
	registry := hooks.Discover(hookOptions)
	service.AttachHooks(hooks.Dispatcher{Registry: registry, Runner: hooks.Runner{Workspace: paths.Workspace}})
	service.hookOptions = hookOptions
	for _, source := range sources {
		if filepath.Ext(source.Path) != ".json" {
			continue
		}
		kind := "user_settings"
		if strings.HasPrefix(filepath.Clean(source.Path), filepath.Clean(paths.Workspace)+string(filepath.Separator)) {
			kind = "project_settings"
		}
		if strings.HasSuffix(source.Path, "settings.local.json") {
			kind = "local_settings"
		}
		service.ensureHookWatcher().watchConfig(source.Path, kind)
	}
	service.SetConfigPath(paths.ConfigFile)
	service.AttachDurable(sessions, coding)
	service.AttachMemory(memory.NewService(store.DB(), cfg.Workspace.Root), recap.NewService(store.DB(), cfg.Workspace.Root))
	service.AttachAuth(authentication, modelCatalog)
	service.AttachSkills(skillCatalog)
	manager := mcpruntime.NewManager(cfg.MCP.Servers, fmt.Sprintf("azem/%d", config.CurrentVersion), func(_ context.Context, reference string) (string, error) {
		return config.ResolveReference(reference, os.LookupEnv, authservice.LookupKeyringSecret)
	}, mcpruntime.Options{Sink: func(event mcpruntime.Event) {
		service.emit(service.ctx, Event{Kind: EventMCPState, State: string(event.State), Text: event.Error, Data: map[string]string{"server": event.Server, "state": string(event.State), "error": event.Error}})
	}, Elicitation: service.handleMCPElicitation})
	service.AttachAgentExtensions(manager, subagentRuns)
	var teamResumer recovery.TeamResumer
	if os.Getenv("AZEM_FAKE_PROVIDER") != "1" {
		service.AttachProviderRuntime(providerRuntime)
		teamResumer = providerRuntime
	}
	recoveryService, err := recovery.NewService(store, coding, subagentRuns, teamResumer)
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, err
	}
	recoverySummary, err := recoveryService.Recover(ctx)
	if err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, err
	}
	service.AttachRecovery(recoverySummary)
	service.AttachReconcileResolver(store)
	if err := service.dispatchLifecycle(ctx, hooks.Setup, service.hookMetadata(startupSessionID, ""), func(e *hooks.Envelope) { e.Trigger = "init" }); err != nil {
		_ = store.Close(ctx)
		return BootstrapResult{}, err
	}
	for _, entry := range skillCatalog.Snapshot().Entries {
		if entry.Eager && !entry.Bundled {
			service.emitInstructionsLoaded(ctx, entry.SourcePath, instructionMemoryType(entry.SourcePath, homeDir, paths.Workspace), "session_start")
		}
	}
	for _, role := range cfg.Agents.Subagents.Roles {
		service.emitInstructionsLoaded(ctx, role.InstructionsFile, instructionMemoryType(role.InstructionsFile, homeDir, paths.Workspace), "session_start")
	}
	for _, persona := range cfg.Agents.Subagents.Personas {
		service.emitInstructionsLoaded(ctx, persona.InstructionsFile, instructionMemoryType(persona.InstructionsFile, homeDir, paths.Workspace), "session_start")
	}
	service.wg.Add(1)
	go func() {
		defer service.wg.Done()
		_ = manager.Start(service.ctx)
		_ = service.emitMCPSnapshot(service.ctx)
	}()
	service.Bootstrap()
	for _, diagnostic := range registry.Diagnostics {
		service.emitHookEvent(Event{Kind: EventHookDiagnostic, State: "failed", Text: diagnostic.Message, Data: map[string]string{"event": string(diagnostic.Event), "source": diagnostic.Source, "reason": diagnostic.Message}})
	}
	service.wg.Add(1)
	go func() {
		defer service.wg.Done()
		service.emitAuthCatalog(service.ctx)
	}()
	return BootstrapResult{Config: cfg, Paths: paths, SessionID: startupSessionID, Service: service}, nil
}

func instructionMemoryType(path, homeDir, workspace string) string {
	path = filepath.Clean(path)
	if relative, err := filepath.Rel(workspace, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "Project"
	}
	if relative, err := filepath.Rel(homeDir, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "User"
	}
	return "Managed"
}

func directoryOf(path string) string {
	for index := len(path) - 1; index >= 0; index-- {
		if path[index] == '/' || path[index] == '\\' {
			if index == 0 {
				return string(path[:1])
			}
			return path[:index]
		}
	}
	return "."
}

func (result BootstrapResult) Validate() error {
	if result.Service == nil {
		return fmt.Errorf("bootstrap service is nil")
	}
	return nil
}

func importConfiguredCredentials(ctx context.Context, cfg config.Config, authentication *authservice.Service) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if cfg.Auth.ImportCodex {
		accounts, _ := authentication.Accounts(ctx, "chatgpt")
		if len(accounts) == 0 {
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
		accounts, _ := authentication.Accounts(ctx, "grok")
		path := filepath.Join(home, ".grok", "auth.json")
		if len(accounts) == 0 {
			if _, statErr := os.Stat(path); statErr == nil {
				_, _ = authentication.ImportGrok(ctx, path)
			}
		}
	}
}
