package app

import (
	"github.com/Viking602/azem/internal/commands"
	"github.com/Viking602/azem/internal/customtools"
	"github.com/Viking602/azem/internal/extensions"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/plugins"
	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/azem/internal/skills"
)

type bootstrapExtensionState struct {
	skillCatalog         *skills.Catalog
	pluginCatalog        plugins.Integration
	commandCatalog       *commands.Catalog
	commandDiagnostics   []string
	customTools          *customtools.Host
	customDiagnostics    []string
	extensionThemes      []extensions.Theme
	extensionDiagnostics []string
	managedSkillsDir     string
	resources            *resource.Router
	mcpDiscovery         []mcpruntime.Diagnostic
}
