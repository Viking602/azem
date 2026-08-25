package app

import (
	"context"

	agentservice "github.com/Viking602/azem/internal/agent"
	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/commands"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/contextfiles"
	"github.com/Viking602/azem/internal/customtools"
	"github.com/Viking602/azem/internal/extensions"
	"github.com/Viking602/azem/internal/hooks"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/plugins"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/recovery"
	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/azem/internal/rules"
	"github.com/Viking602/azem/internal/securityscan"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type bootstrapAssembly struct {
	ctx              context.Context
	cfg              config.Config
	paths            config.Paths
	homeDir          string
	configDir        string
	startupSessionID string

	store                *sqlitestore.Provider
	skillCatalog         *skills.Catalog
	pluginCatalog        plugins.Integration
	sessions             *session.Service
	memory               *memory.Service
	contextFiles         contextfiles.Result
	ruleResult           rules.Result
	ruleCatalog          *rules.Catalog
	commandCatalog       *commands.Catalog
	commandDiagnostics   []string
	customTools          *customtools.Host
	customDiagnostics    []string
	extensionThemes      []extensions.Theme
	extensionDiagnostics []string
	managedSkillsDir     string
	resources            *resource.Router
	coding               *agentservice.Service
	subagentRuns         *agentservice.SQLSubagentRunStore
	authentication       *authservice.Service
	modelCatalog         *catalog.Service
	providerRuntime      *ProviderRuntime
	securityStore        *securityscan.SQLStore
	securityService      *securityscan.Service
	mcpDiscovery         []mcpruntime.Diagnostic
	securityRunner       *securityExecutor

	service         *Service
	manager         *mcpruntime.Manager
	registry        *hooks.Registry
	recoveryService *recovery.Service
	recoveryFence   sqlitestore.RecoveryFence
	shouldRecover   bool
}
