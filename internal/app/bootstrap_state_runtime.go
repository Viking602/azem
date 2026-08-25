package app

import (
	agentservice "github.com/Viking602/azem/internal/agent"
	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/hooks"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/recovery"
	"github.com/Viking602/azem/internal/securityscan"
)

type bootstrapRuntimeState struct {
	coding          *agentservice.Service
	subagentRuns    *agentservice.SQLSubagentRunStore
	authentication  *authservice.Service
	modelCatalog    *catalog.Service
	providerRuntime *ProviderRuntime
	securityStore   *securityscan.SQLStore
	securityService *securityscan.Service
	securityRunner  *securityExecutor
	service         *Service
	manager         *mcpruntime.Manager
	registry        *hooks.Registry
	recoveryService *recovery.Service
}
