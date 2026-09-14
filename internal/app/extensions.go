package app

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/extensions"
)

func mergeExtensionAgents(cfg *config.Config, agents []extensions.Agent) []string {
	var diagnostics []string
	for _, agent := range agents {
		if existing, exists := cfg.Agents.Subagents.Roles[agent.Name]; exists && existing.Source != "builtin" {
			continue
		}
		tools := normalizeExtensionAgentTools(agent.Tools)
		role := config.SubagentRoleConfig{
			Description: agent.Description, Instructions: agent.SystemPrompt, Reasoning: agent.Thinking,
			CapabilityMode: agentCapabilityMode(tools), Isolation: normalizeAgentIsolation(agent.Isolation),
			Tools: tools, Source: agent.Source,
		}
		if len(agent.Models) > 0 {
			applyAgentModel(cfg, &role, agent.Models[0])
		}
		cfg.Agents.Subagents.Roles[agent.Name] = role
	}
	return diagnostics
}

func agentCapabilityMode(tools []string) string {
	if len(tools) == 0 {
		return "read-only"
	}
	for _, name := range tools {
		switch name {
		case "coding.eval", "lsp", "browser", "computer", "debug", "hub", "github", "generate_image", "tts", "retain", "learn", "manage_skill":
			return "all"
		}
		if strings.Contains(name, "write") || strings.Contains(name, "edit") || strings.Contains(name, "delete") || name == "coding.shell" || name == "bash" {
			return "all"
		}
	}
	return "read-only"
}

func normalizeAgentIsolation(value string) string {
	if value == "worktree" {
		return "worktree"
	}
	return "none"
}

func normalizeExtensionAgentTools(tools []string) []string {
	aliases := map[string]string{
		"read": "coding.read_file", "grep": "coding.search", "glob": "coding.glob",
		"edit": "coding.edit_hashline", "write": "coding.write_file", "bash": "coding.shell",
		"task": "subagent.spawn",
		"eval": "coding.eval",
	}
	result := make([]string, 0, len(tools))
	seen := make(map[string]bool)
	for _, name := range tools {
		name = strings.TrimSpace(name)
		if alias := aliases[name]; alias != "" {
			name = alias
		}
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

func applyAgentModel(cfg *config.Config, role *config.SubagentRoleConfig, selector string) {
	selector = strings.TrimSpace(selector)
	if strings.HasPrefix(selector, "@") {
		if route, ok := cfg.Agents.Subagents.Routes[strings.TrimPrefix(selector, "@")]; ok {
			role.Provider, role.Model = route.Provider, route.Model
			if role.Reasoning == "" {
				role.Reasoning = route.Reasoning
			}
		}
		return
	}
	if provider, model, found := strings.Cut(selector, "/"); found {
		role.Provider, role.Model = provider, model
	} else {
		role.Model = selector
	}
}

func (s *Service) AttachThemes(themes []extensions.Theme, diagnostics []string) {
	s.extensionThemes = append([]extensions.Theme(nil), themes...)
	s.extensionDiagnostics = append([]string(nil), diagnostics...)
}

func (s *Service) emitThemeCatalog(ctx context.Context, state string) error {
	encoded, err := json.Marshal(s.extensionThemes)
	if err != nil {
		return err
	}
	diagnostics, err := json.Marshal(s.extensionDiagnostics)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventThemeCatalog, State: state, Data: map[string]string{"themes": string(encoded), "diagnostics": string(diagnostics)}})
	return nil
}
