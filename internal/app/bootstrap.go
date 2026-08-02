package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	backgroundservice "github.com/Viking602/azem/internal/background"
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
	"github.com/Viking602/venat/api"
)

type BootstrapResult struct {
	Config    config.Config
	Paths     config.Paths
	SessionID string
	Service   *Service
}

type bootstrapAssembly struct {
	ctx              context.Context
	cfg              config.Config
	paths            config.Paths
	homeDir          string
	configDir        string
	startupSessionID string

	store           *sqlitestore.Provider
	skillCatalog    *skills.Catalog
	sessions        *session.Service
	coding          *agentservice.Service
	subagentRuns    *agentservice.SQLSubagentRunStore
	authentication  *authservice.Service
	modelCatalog    *catalog.Service
	providerRuntime *ProviderRuntime

	service         *Service
	manager         *mcpruntime.Manager
	registry        *hooks.Registry
	recoveryService *recovery.Service
}

func Bootstrap(ctx context.Context, startupWorkspace string, configFile string) (BootstrapResult, error) {
	return bootstrap(ctx, startupWorkspace, configFile, false)
}

func BootstrapAtWorkspace(ctx context.Context, startupWorkspace string, configFile string) (BootstrapResult, error) {
	return bootstrap(ctx, startupWorkspace, configFile, true)
}

func bootstrap(ctx context.Context, startupWorkspace, configFile string, forceWorkspace bool) (BootstrapResult, error) {
	assembly := bootstrapAssembly{ctx: ctx}
	result, err := assembly.build(startupWorkspace, configFile, forceWorkspace)
	if err != nil {
		assembly.close()
		return BootstrapResult{}, err
	}
	return result, nil
}

func (b *bootstrapAssembly) build(startupWorkspace, configFile string, forceWorkspace bool) (BootstrapResult, error) {
	if err := b.loadConfiguration(startupWorkspace, configFile, forceWorkspace); err != nil {
		return BootstrapResult{}, err
	}
	if err := b.buildCore(); err != nil {
		return BootstrapResult{}, err
	}
	if err := b.wireService(); err != nil {
		return BootstrapResult{}, err
	}
	if err := b.start(); err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{Config: b.cfg, Paths: b.paths, SessionID: b.startupSessionID, Service: b.service}, nil
}

func (b *bootstrapAssembly) loadConfiguration(startupWorkspace, configFile string, forceWorkspace bool) error {
	paths, err := config.ResolvePaths(startupWorkspace)
	if err != nil {
		return err
	}
	if configFile != "" {
		paths.ConfigFile = configFile
		paths.ConfigDir = directoryOf(configFile)
	}
	cfg, err := config.Load(paths.ConfigFile, paths.Workspace)
	if err != nil {
		return err
	}
	if forceWorkspace {
		cfg.Workspace.Root = paths.Workspace
	}
	paths.Workspace = cfg.Workspace.Root
	if err := config.EnsureDirectories(paths); err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home for skills: %w", err)
	}
	configDir, err := filepath.Abs(filepath.Dir(paths.ConfigFile))
	if err != nil {
		return fmt.Errorf("resolve config directory for skills: %w", err)
	}
	b.cfg, b.paths, b.homeDir, b.configDir = cfg, paths, homeDir, configDir
	return nil
}

func (b *bootstrapAssembly) buildCore() error {
	var err error
	b.store, err = sqlitestore.Open(b.ctx, b.paths.Database)
	if err != nil {
		return err
	}
	b.skillCatalog, err = skills.Load(skills.LoadOptions{
		HomeDir:      b.homeDir,
		ConfigDir:    b.configDir,
		WorkspaceDir: b.paths.Workspace,
		Config:       b.cfg.Skills,
	})
	if err != nil {
		return fmt.Errorf("load skills: %w", err)
	}
	b.sessions = session.NewService(b.store.DB())
	b.startupSessionID, err = randomID("session")
	if err != nil {
		return fmt.Errorf("create startup session id: %w", err)
	}
	shellOptions := agentservice.ShellOptions{
		MaxContextOutputBytes: b.cfg.Workspace.Shell.MaxContextOutputBytes, MaxArtifactOutputBytes: b.cfg.Workspace.Shell.MaxArtifactOutputBytes,
		StopOnOutputLimit: b.cfg.Workspace.Shell.StopOnOutputLimit, MaxConcurrency: b.cfg.Workspace.Shell.MaxConcurrency,
		ArtifactSink: newShellArtifactSink(b.sessions),
	}
	b.coding, err = agentservice.NewService(b.store, b.paths.Workspace,
		agentservice.WithWorkspacePolicy(b.cfg.Workspace.AllowWrite, b.cfg.Workspace.ShellPolicy, b.cfg.Workspace.AllowNetwork),
		agentservice.WithShellOptions(shellOptions),
		agentservice.WithTeamLimits(b.cfg.Agents.Team.MaxConcurrency, b.cfg.Agents.Team.MaxTicks),
		agentservice.WithSkills(b.skillCatalog),
	)
	if err != nil {
		return err
	}
	b.subagentRuns, err = agentservice.NewSQLSubagentRunStore(b.store.DB())
	if err != nil {
		return err
	}
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

func (b *bootstrapAssembly) wireService() error {
	b.service = NewService(b.ctx, b.cfg)
	b.attachHooks()
	b.service.SetConfigPath(b.paths.ConfigFile)
	b.service.AttachDurable(b.sessions, b.coding)
	b.service.SetWorkspaceAnchor(canonicalWorkspaceAnchor(b.paths.Workspace))
	b.service.AttachAttachments(filepath.Join(b.paths.DataDir, "attachments"))
	b.service.AttachMemory(memory.NewService(b.store.DB(), b.cfg.Workspace.Root), recap.NewService(b.store.DB(), b.cfg.Workspace.Root))
	b.service.AttachAuth(b.authentication, b.modelCatalog)
	b.service.AttachSkills(b.skillCatalog)

	b.manager = mcpruntime.NewManager(b.cfg.MCP.Servers, fmt.Sprintf("azem/%d", config.CurrentVersion), func(_ context.Context, reference string) (string, error) {
		return config.ResolveReference(reference, os.LookupEnv, authservice.LookupKeyringSecret)
	}, mcpruntime.Options{Sink: func(event mcpruntime.Event) {
		b.service.emit(b.service.ctx, Event{Kind: EventMCPState, State: string(event.State), Text: event.Error, Data: map[string]string{"server": event.Server, "state": string(event.State), "error": event.Error}})
	}, Elicitation: b.service.handleMCPElicitation})
	b.service.AttachAgentExtensions(b.manager, b.subagentRuns)

	var teamResumer recovery.TeamResumer
	var runResumer recovery.RunResumer
	if os.Getenv("AZEM_FAKE_PROVIDER") != "1" {
		b.service.AttachProviderRuntime(b.providerRuntime)
		teamResumer, runResumer = b.providerRuntime, b.providerRuntime
	}
	if err := b.attachBackground(); err != nil {
		return err
	}
	return b.attachRecovery(teamResumer, runResumer)
}

func (b *bootstrapAssembly) attachHooks() {
	sources := hookSources(b.cfg.Hooks, b.configDir, b.homeDir, b.paths.Workspace)
	hookOptions := hooks.Options{Sources: sources, DefaultTimeout: b.cfg.Hooks.DefaultTimeoutParsed, FailurePolicy: hooks.FailurePolicy(b.cfg.Hooks.FailurePolicy)}
	b.registry = hooks.Discover(hookOptions)
	b.service.AttachHooks(hooks.Dispatcher{Registry: b.registry, Runner: hooks.Runner{Workspace: b.paths.Workspace}})
	b.service.hookOptions = hookOptions
	for _, source := range sources {
		if filepath.Ext(source.Path) != ".json" {
			continue
		}
		kind := "user_settings"
		if strings.HasPrefix(filepath.Clean(source.Path), filepath.Clean(b.paths.Workspace)+string(filepath.Separator)) {
			kind = "project_settings"
		}
		if strings.HasSuffix(source.Path, "settings.local.json") {
			kind = "local_settings"
		}
		b.service.ensureHookWatcher().watchConfig(source.Path, kind)
	}
}

func (b *bootstrapAssembly) attachBackground() error {
	manager, err := backgroundservice.NewManager(backgroundservice.Options{
		Root: b.paths.Workspace, LogDir: filepath.Join(b.paths.StateDir, "background"),
	})
	if err != nil {
		return err
	}
	b.service.AttachBackground(manager)
	return nil
}

func (b *bootstrapAssembly) attachRecovery(teamResumer recovery.TeamResumer, runResumer recovery.RunResumer) error {
	var err error
	b.recoveryService, err = recovery.NewService(b.store, b.coding, b.subagentRuns, teamResumer, runResumer)
	if err != nil {
		return err
	}
	b.recoveryService.SetBeforeResume(func(recoveryCtx context.Context, runs []api.Run) error {
		for _, run := range runs {
			if err := b.sessions.InterruptRunningToolRecordsForRun(recoveryCtx, run.ID, time.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	})
	return nil
}

func (b *bootstrapAssembly) start() error {
	b.service.Bootstrap()
	if err := b.service.dispatchLifecycle(b.ctx, hooks.Setup, b.service.hookMetadata(b.startupSessionID, ""), func(e *hooks.Envelope) { e.Trigger = "init" }); err != nil {
		return err
	}
	if err := b.recover(); err != nil {
		return err
	}
	b.emitStartupInstructions()
	b.startBackgroundRuntimes()
	return nil
}

func (b *bootstrapAssembly) recover() error {
	summary, err := b.recoveryService.Recover(b.ctx)
	if err != nil {
		return err
	}
	b.service.AttachRecovery(summary)
	b.service.emitRecoveryState()
	b.service.AttachReconcileResolver(b.coding)
	return nil
}

func (b *bootstrapAssembly) emitStartupInstructions() {
	for _, entry := range b.skillCatalog.Snapshot().Entries {
		if entry.Eager && !entry.Bundled {
			b.service.emitInstructionsLoaded(b.ctx, entry.SourcePath, instructionMemoryType(entry.SourcePath, b.homeDir, b.paths.Workspace), "session_start")
		}
	}
	for _, role := range b.cfg.Agents.Subagents.Roles {
		b.service.emitInstructionsLoaded(b.ctx, role.InstructionsFile, instructionMemoryType(role.InstructionsFile, b.homeDir, b.paths.Workspace), "session_start")
	}
	for _, persona := range b.cfg.Agents.Subagents.Personas {
		b.service.emitInstructionsLoaded(b.ctx, persona.InstructionsFile, instructionMemoryType(persona.InstructionsFile, b.homeDir, b.paths.Workspace), "session_start")
	}
}

func (b *bootstrapAssembly) startBackgroundRuntimes() {
	b.service.wg.Add(1)
	go func() {
		defer b.service.wg.Done()
		_ = b.manager.Start(b.service.ctx)
		_ = b.service.emitMCPSnapshot(b.service.ctx)
	}()
	for _, diagnostic := range b.registry.Diagnostics {
		b.service.emitHookEvent(Event{Kind: EventHookDiagnostic, State: "failed", Text: diagnostic.Message, Data: map[string]string{"event": string(diagnostic.Event), "source": diagnostic.Source, "reason": diagnostic.Message}})
	}
	b.service.wg.Add(1)
	go func() {
		defer b.service.wg.Done()
		b.service.emitAuthCatalog(b.service.ctx)
	}()
}

func (b *bootstrapAssembly) close() {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(b.ctx), 5*time.Second)
	defer cancel()
	if b.service != nil {
		_ = b.service.Shutdown(cleanupCtx)
		return
	}
	if b.authentication != nil {
		_ = b.authentication.Close()
	}
	if b.coding != nil {
		_ = b.coding.Close(cleanupCtx)
		return
	}
	if b.store != nil {
		_ = b.store.Close(cleanupCtx)
	}
}

func newShellArtifactSink(sessions *session.Service) func(context.Context, agentservice.ShellExecutionSnapshot, []byte) (agentservice.ShellArtifactResult, error) {
	return func(ctx context.Context, execution agentservice.ShellExecutionSnapshot, payload []byte) (agentservice.ShellArtifactResult, error) {
		if sessions == nil || strings.TrimSpace(execution.SessionID) == "" {
			return agentservice.ShellArtifactResult{}, fmt.Errorf("persist shell artifact: session is unavailable")
		}
		preview := execution.Output
		if len(preview) > 512 {
			preview = preview[:512]
		}
		preview = strings.ToValidUTF8(preview, "�")
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		artifact, err := sessions.PutArtifact(persistCtx, execution.SessionID, execution.RunID, "shell_output", payload, preview)
		if err != nil {
			return agentservice.ShellArtifactResult{}, err
		}
		return agentservice.ShellArtifactResult{Reference: "artifact:" + artifact.ID}, nil
	}
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

func canonicalWorkspaceAnchor(workspace string) string {
	if absolute, err := filepath.Abs(workspace); err == nil {
		workspace = absolute
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	return filepath.Clean(workspace)
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
