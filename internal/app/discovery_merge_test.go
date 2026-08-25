package app

import (
	"testing"

	"github.com/Viking602/azem/internal/config"
)

func TestMergeDiscoveredMCPPreservesExplicitServersAndDeletionTombstones(t *testing.T) {
	cfg := config.Default()
	cfg.MCP.Servers["explicit"] = config.MCPServerConfig{Command: "configured"}
	cfg.MCP.RemovedServers = []string{"removed"}
	mergeDiscoveredMCP(&cfg, map[string]config.MCPServerConfig{
		"explicit": {Command: "discovered"},
		"removed":  {Command: "removed-discovered"},
		"new":      {Command: "new-discovered", Managed: true},
	})
	if cfg.MCP.Servers["explicit"].Command != "configured" {
		t.Fatalf("explicit server overwritten: %#v", cfg.MCP.Servers["explicit"])
	}
	if _, exists := cfg.MCP.Servers["removed"]; exists {
		t.Fatalf("removed server resurrected: %#v", cfg.MCP.Servers["removed"])
	}
	if cfg.MCP.Servers["new"].Command != "new-discovered" || !cfg.MCP.Servers["new"].Managed {
		t.Fatalf("discovered server missing: %#v", cfg.MCP.Servers["new"])
	}
}
