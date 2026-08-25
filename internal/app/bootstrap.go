package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/contextfiles"
	"github.com/Viking602/azem/internal/hooks"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/plugins"
	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/recovery"
	"github.com/Viking602/azem/internal/rules"
	"github.com/Viking602/azem/internal/securityscan"
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

func Bootstrap(ctx context.Context, startupWorkspace string, configFile string) (BootstrapResult, error) {
	return bootstrap(ctx, startupWorkspace, configFile, false, false)
}

func BootstrapAtWorkspace(ctx context.Context, startupWorkspace string, configFile string) (BootstrapResult, error) {
	return bootstrap(ctx, startupWorkspace, configFile, true, false)
}

func BootstrapDesktop(ctx context.Context, startupWorkspace string, configFile string) (BootstrapResult, error) {
	return bootstrap(ctx, startupWorkspace, configFile, false, true)
}

func BootstrapDesktopAtWorkspace(ctx context.Context, startupWorkspace string, configFile string) (BootstrapResult, error) {
	return bootstrap(ctx, startupWorkspace, configFile, true, true)
}

func (b *bootstrapAssembly) build(startupWorkspace, configFile string, forceWorkspace, desktopMode bool) (BootstrapResult, error) {
	if err := b.loadConfiguration(startupWorkspace, configFile, forceWorkspace, desktopMode); err != nil {
		return BootstrapResult{}, err
	}
	var err error
	b.recoveryFence, b.shouldRecover, err = sqlitestore.AcquireRecoveryFence(b.ctx, b.paths.Database)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := b.buildCore(forceWorkspace, desktopMode); err != nil {
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

func (b *bootstrapAssembly) loadConfiguration(startupWorkspace, configFile string, forceWorkspace, desktopMode bool) error {
	paths, err := config.ResolvePathsWithConfig(startupWorkspace, configFile)
	if err != nil {
		return err
	}
	if desktopMode && !forceWorkspace {
		if err := b.restoreDesktopWorkspace(&paths); err != nil {
			return err
		}
	}
	loadConfig := config.Load
	if forceWorkspace || desktopMode {
		loadConfig = config.LoadAtWorkspace
	}
	cfg, err := loadConfig(paths.ConfigFile, paths.Workspace)
	if err != nil {
		return err
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
	return b.loadPlugins(desktopMode)
}

func (b *bootstrapAssembly) restoreDesktopWorkspace(paths *config.Paths) error {
	var err error
	b.store, err = sqlitestore.Open(b.ctx, paths.Database, sqlitestore.WithBlobRoot(filepath.Join(paths.DataDir, "blobs")))
	if err != nil {
		return err
	}
	b.sessions = session.NewService(b.store.DB(), b.store.Blobs())
	if workspace, restoreErr := b.sessions.LastProject(b.ctx); restoreErr == nil {
		paths.Workspace = workspace
		return nil
	} else if !errors.Is(restoreErr, sql.ErrNoRows) {
		return fmt.Errorf("restore desktop project: %w", restoreErr)
	}
	if filepath.Dir(filepath.Clean(paths.Workspace)) != filepath.Clean(paths.Workspace) {
		return nil
	}
	paths.Workspace, err = os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve desktop fallback workspace: %w", err)
	}
	return nil
}

func (b *bootstrapAssembly) buildCore(forceWorkspace, desktopMode bool) error {
	var err error
	if b.store == nil {
		b.store, err = sqlitestore.Open(b.ctx, b.paths.Database, sqlitestore.WithBlobRoot(filepath.Join(b.paths.DataDir, "blobs")))
		if err != nil {
			return err
		}
	}
	if b.sessions == nil {
		b.sessions = session.NewService(b.store.DB(), b.store.Blobs())
	}
	b.memory = memory.NewService(b.store.DB(), b.paths.Workspace)
	if b.cfg.Discovery.ContextFiles {
		disabled := make(map[string]bool, len(b.cfg.Discovery.DisabledProviders))
		for _, provider := range b.cfg.Discovery.DisabledProviders {
			disabled[strings.ToLower(strings.TrimSpace(provider))] = true
		}
		b.contextFiles, err = contextfiles.Discover(b.ctx, contextfiles.Options{
			Workspace: b.paths.Workspace, HomeDir: b.homeDir, DisabledProviders: disabled,
			AdditionalFiles: append([]string(nil), b.cfg.Discovery.AdditionalContextFiles...),
		})
		if err != nil {
			return fmt.Errorf("discover context files: %w", err)
		}
	}
	if b.cfg.Discovery.Rules {
		disabledProviders := make(map[string]bool, len(b.cfg.Discovery.DisabledProviders))
		for _, provider := range b.cfg.Discovery.DisabledProviders {
			disabledProviders[strings.ToLower(strings.TrimSpace(provider))] = true
		}
		disabledRules := make(map[string]bool, len(b.cfg.Discovery.DisabledRules))
		for _, name := range b.cfg.Discovery.DisabledRules {
			disabledRules[strings.TrimSpace(name)] = true
		}
		b.ruleResult, err = rules.Discover(b.ctx, rules.Options{
			Workspace: b.paths.Workspace, HomeDir: b.homeDir,
			DisabledProviders: disabledProviders, DisabledRules: disabledRules,
		})
		if err != nil {
			return fmt.Errorf("discover rules: %w", err)
		}
		discoveredRules := rules.TTSRRules(b.ruleResult)
		if len(discoveredRules) > 0 {
			seen := make(map[string]bool, len(b.cfg.TTSR.Rules))
			for _, rule := range b.cfg.TTSR.Rules {
				seen[rule.Name] = true
			}
			for _, rule := range discoveredRules {
				if !seen[rule.Name] {
					b.cfg.TTSR.Rules = append(b.cfg.TTSR.Rules, rule)
					seen[rule.Name] = true
				}
			}
			b.cfg.TTSR.Enabled = true
		}
	}
	b.ruleCatalog = rules.NewCatalog(b.ruleResult)
	b.managedSkillsDir = filepath.Join(b.homeDir, ".omp", "agent", "managed-skills")
	if nativeDir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); nativeDir != "" {
		b.managedSkillsDir = filepath.Join(nativeDir, "managed-skills")
	}
	if b.cfg.Discovery.MCP {
		disabledProviders := make(map[string]bool, len(b.cfg.Discovery.DisabledProviders))
		for _, provider := range b.cfg.Discovery.DisabledProviders {
			disabledProviders[strings.ToLower(strings.TrimSpace(provider))] = true
		}
		discovered := mcpruntime.DiscoverConfig(b.ctx, mcpruntime.DiscoveryOptions{
			Workspace: b.paths.Workspace, HomeDir: b.homeDir, DisabledProviders: disabledProviders,
		})
		b.mcpDiscovery = append([]mcpruntime.Diagnostic(nil), discovered.Diagnostics...)
		mergeDiscoveredMCP(&b.cfg, discovered.Servers)
	}
	b.skillCatalog, err = skills.Load(skills.LoadOptions{
		HomeDir:      b.homeDir,
		ConfigDir:    b.configDir,
		ManagedDir:   b.managedSkillsDir,
		WorkspaceDir: b.paths.Workspace,
		Discovery:    b.cfg.Discovery,
		Config:       b.cfg.Skills,
	})
	if err != nil {
		return fmt.Errorf("load skills: %w", err)
	}
	b.resources, err = buildResourceRouter(b.sessions, b.skillCatalog, b.ruleCatalog)
	if err != nil {
		return err
	}
	if err := b.loadExtensions(); err != nil {
		return err
	}
	if desktopMode {
		if err := b.sessions.TouchProject(b.ctx, b.paths.Workspace); err != nil {
			if forceWorkspace || filepath.Dir(filepath.Clean(b.paths.Workspace)) != filepath.Clean(b.paths.Workspace) {
				return err
			}
		} else if err := b.sessions.AdoptUnassignedSessions(b.ctx); err != nil {
			return err
		}
	}
	b.startupSessionID, err = randomID("session")
	if err != nil {
		return fmt.Errorf("create startup session id: %w", err)
	}
	shellOptions := agentservice.ShellOptions{
		MaxContextOutputBytes: b.cfg.Workspace.Shell.MaxContextOutputBytes, MaxArtifactOutputBytes: b.cfg.Workspace.Shell.MaxArtifactOutputBytes,
		StopOnOutputLimit: b.cfg.Workspace.Shell.StopOnOutputLimit, MaxConcurrency: b.cfg.Workspace.Shell.MaxConcurrency,
		MaxWallClockDuration: b.cfg.Workspace.Shell.MaxWallClockDuration,
		ArtifactSink:         newShellArtifactSink(b.sessions),
	}
	b.coding, err = agentservice.NewService(b.store, b.paths.Workspace,
		agentservice.WithWorkspacePolicy(b.cfg.Workspace.AllowWrite, b.cfg.Workspace.ShellPolicy, b.cfg.Workspace.AllowNetwork),
		agentservice.WithShellOptions(shellOptions),
		agentservice.WithTeamLimits(b.cfg.Agents.Team.MaxConcurrency, b.cfg.Agents.Team.MaxTicks),
		agentservice.WithSkills(b.skillCatalog),
		agentservice.WithResourceRouter(b.resources),
		agentservice.WithMemory(b.memory),
	)
	if err != nil {
		return err
	}
	if b.customTools != nil {
		b.coding.SetFileMutationBroker(b.customTools)
		drivers, driverErr := b.customTools.Drivers()
		if driverErr != nil {
			_ = b.customTools.Close(context.Background())
			return driverErr
		}
		if err := b.coding.AttachExternalTools(drivers, b.customTools.Close); err != nil {
			_ = b.customTools.Close(context.Background())
			return err
		}
	}
	b.subagentRuns, err = agentservice.NewSQLSubagentRunStore(b.store.DB(), b.store.Blobs())
	if err != nil {
		return err
	}
	return b.buildProviderServices()
}

func (b *bootstrapAssembly) wireService() error {
	b.service = NewService(b.ctx, b.cfg)
	b.service.attachRuntimeFence(b.recoveryFence)
	b.recoveryFence = nil
	b.attachHooks()
	b.service.SetConfigPath(b.paths.ConfigFile)
	b.service.AttachDurable(b.sessions, b.coding)
	b.service.SetWorkspaceAnchor(canonicalWorkspaceAnchor(b.paths.Workspace))
	b.service.AttachAttachments(filepath.Join(b.paths.DataDir, "attachments"))
	b.service.AttachMemory(b.memory, recap.NewService(b.store.DB(), b.cfg.Workspace.Root))
	b.service.AttachContextFiles(b.contextFiles)
	b.service.AttachRules(b.ruleResult)
	b.service.AttachAuth(b.authentication, b.modelCatalog)
	b.service.AttachSkills(b.skillCatalog)
	if b.cfg.AutoLearn.Enabled {
		managedSkills := skills.NewManagedSkillManager(b.managedSkillsDir, b.skillCatalog)
		managedSkills.SetReloadCallback(func() { _ = b.service.emitSkillCatalog(b.service.ctx, "managed") })
		if err := b.coding.AttachManagedSkillTool(managedSkills.Driver()); err != nil {
			return err
		}
	}
	b.service.AttachPlugins(pluginCatalogEntries(b.pluginCatalog), pluginDiagnostics(b.pluginCatalog))
	b.service.AttachCommands(b.commandCatalog, append(append(append([]string(nil), b.commandDiagnostics...), b.customDiagnostics...), b.extensionDiagnostics...))
	b.service.AttachExtensionHost(b.customTools)
	b.service.AttachThemes(b.extensionThemes, b.extensionDiagnostics)
	b.service.AttachPluginRuntime(plugins.Options{
		HomeDir: b.homeDir, DataDir: b.paths.DataDir, WorkspaceDir: b.paths.Workspace,
		ImportCodex: b.cfg.Plugins.ImportCodex, TrustHooks: b.cfg.Plugins.TrustHooks,
	}, b.pluginCatalog)
	marketplace, err := plugins.NewMarketplaceManager(plugins.MarketplaceManagerOptions{
		DataDir: b.paths.DataDir, WorkspaceDir: b.paths.Workspace,
	})
	if err != nil {
		return err
	}
	b.service.marketplace = marketplace

	b.manager = mcpruntime.NewManager(b.cfg.MCP.Servers, fmt.Sprintf("azem/%d", config.CurrentVersion), func(_ context.Context, reference string) (string, error) {
		return config.ResolveReference(reference, os.LookupEnv, authservice.LookupKeyringSecret)
	}, mcpruntime.Options{Sink: func(event mcpruntime.Event) {
		b.service.emit(b.service.ctx, Event{Kind: EventMCPState, State: string(event.State), Text: event.Error, Data: map[string]string{"server": event.Server, "state": string(event.State), "error": event.Error}})
	}, Elicitation: b.service.handleMCPElicitation, OAuth: &mcpruntime.OAuthBroker{
		Store: mcpOAuthStore{auth: b.authentication}, ResolveSecret: func(ctx context.Context, reference string) (string, error) {
			return config.ResolveReference(reference, os.LookupEnv, authservice.LookupKeyringSecret)
		},
	}, Notification: func(notification mcpruntime.Notification) {
		encoded, _ := json.Marshal(notification.Data)
		if len(encoded) > 64<<10 {
			encoded = encoded[:64<<10]
		}
		b.service.emit(b.service.ctx, Event{Kind: EventMCPState, State: "notification", Text: notification.Message, Data: map[string]string{
			"server": notification.Server, "notification": notification.Kind, "uri": notification.URI,
			"level": notification.Level, "logger": notification.Logger, "progressToken": notification.ProgressToken,
			"progress": strconv.FormatFloat(notification.Progress, 'f', -1, 64), "total": strconv.FormatFloat(notification.Total, 'f', -1, 64),
			"payload": string(encoded),
		}})
	}})
	if b.cfg.AutoLearn.Enabled {
		b.service.AttachAutoLearnInstructions()
	}
	if err := b.resources.Register("mcp", mcpResourceHandler{manager: b.manager}); err != nil {
		return err
	}
	b.service.AttachResources(b.resources)
	capabilities, err := buildCapabilityRegistry(b.cfg, b.coding, b.skillCatalog, b.manager, b.resources)
	if err != nil {
		return err
	}
	b.service.AttachCapabilities(capabilities)
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
	securityStore, err := securityscan.NewSQLStore(b.store.DB())
	if err != nil {
		return err
	}
	finalizer, err := securityscan.NewFinalizer()
	if err != nil {
		return err
	}
	b.securityStore = securityStore
	b.securityRunner = &securityExecutor{runtime: b.providerRuntime, coding: b.coding}
	b.securityService, err = securityscan.NewService(securityscan.ServiceOptions{
		Store: securityStore, Executor: b.securityRunner, BaseContext: b.service.ctx,
		Snapshotter: securityscan.Snapshotter{DataRoot: b.paths.DataDir}, Finalizer: finalizer,
		Emit: func(projection securityscan.Projection) {
			b.service.emit(b.service.ctx, Event{Kind: EventKind("security_scan_state"), State: string(projection.Scan.Status), Security: &projection})
		},
	})
	if err != nil {
		return err
	}
	b.securityRunner.service = b.securityService
	b.service.AttachSecurity(b.securityService)
	return b.attachRecovery(teamResumer, runResumer)
}

func (b *bootstrapAssembly) attachHooks() {
	sources := hookSourcesForDiscovery(b.cfg.Hooks, b.cfg.Discovery, b.configDir, b.homeDir, b.paths.Workspace)
	if b.cfg.Plugins.TrustHooks {
		for _, source := range b.pluginCatalog.HookSources {
			if dataDir := source.Environment["PLUGIN_DATA"]; dataDir != "" {
				_ = os.MkdirAll(dataDir, 0o700)
			}
			sources = append(sources, hooks.Source{Path: source.Path, Trusted: true, Environment: source.Environment})
		}
	}
	var scriptDiagnostics []hooks.Diagnostic
	var scripts []hooks.ScriptSource
	if b.cfg.Discovery.Hooks {
		disabledProviders := make(map[string]bool, len(b.cfg.Discovery.DisabledProviders))
		for _, provider := range b.cfg.Discovery.DisabledProviders {
			disabledProviders[strings.ToLower(strings.TrimSpace(provider))] = true
		}
		scripts, scriptDiagnostics = hooks.DiscoverHarnessScripts(hooks.HarnessDiscoveryOptions{
			Workspace: b.paths.Workspace, HomeDir: b.homeDir, TrustProject: b.cfg.Hooks.TrustProject,
			DisabledProviders: disabledProviders,
		})
	}
	hookOptions := hooks.Options{
		Sources: sources, Scripts: scripts, DefaultTimeout: b.cfg.Hooks.DefaultTimeoutParsed,
		FailurePolicy: hooks.FailurePolicy(b.cfg.Hooks.FailurePolicy),
		Disabled:      append([]string(nil), b.cfg.Hooks.Disabled...),
	}
	b.registry = hooks.Discover(hookOptions)
	b.registry.Diagnostics = append(b.registry.Diagnostics, scriptDiagnostics...)
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
	if b.securityService != nil {
		if err := b.securityService.Recover(b.ctx, b.cfg.Workspace.Root); err != nil {
			return err
		}
	}
	b.emitStartupInstructions()
	b.startBackgroundRuntimes()
	return nil
}

func (b *bootstrapAssembly) recover() error {
	if !b.shouldRecover {
		b.service.AttachReconcileResolver(b.coding)
		return nil
	}
	summary, err := b.recoveryService.Recover(b.ctx)
	if err != nil {
		return err
	}
	b.service.AttachRecovery(summary)
	b.service.emitRecoveryState()
	b.service.AttachReconcileResolver(b.coding)
	return b.service.finishRuntimeRecovery()
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
	b.service.wg.Add(1)
	go func() {
		defer b.service.wg.Done()
		b.service.runMarketplaceAutoUpdate(b.service.ctx)
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
	defer closeRecoveryFence(b.recoveryFence)
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

func closeRecoveryFence(fence sqlitestore.RecoveryFence) {
	if fence != nil {
		_ = fence.Close()
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
