package app

import (
	"encoding/json"
	"sort"
	"strings"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/capability"
	"github.com/Viking602/azem/internal/config"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/resource"
	"github.com/Viking602/azem/internal/skills"
	llmuxcatalog "github.com/Viking602/llmux/provider/catalog"
)

func buildCapabilityRegistry(
	cfg config.Config,
	coding *agentservice.Service,
	skillCatalog *skills.Catalog,
	manager *mcpruntime.Manager,
	resources *resource.Router,
) (*capability.Registry, error) {
	registry := capability.NewRegistry()
	if err := registry.RegisterSource("modes", capability.Static(
		capability.Descriptor{ID: "single", Kind: capability.KindMode, Enabled: true, Description: "Single durable coding agent"},
		capability.Descriptor{ID: "team", Kind: capability.KindMode, Enabled: true, Description: "Durable multi-agent team"},
		capability.Descriptor{ID: "plan", Kind: capability.KindMode, Enabled: true, Description: "Structured planning mode"},
		capability.Descriptor{ID: "security-scan", Kind: capability.KindMode, Enabled: true, Description: "Native governed security scan"},
	)); err != nil {
		return nil, err
	}
	if err := registry.RegisterSource("providers", configuredProviderCapabilities(cfg)); err != nil {
		return nil, err
	}
	if err := registry.RegisterSource("builtin-tools", func() ([]capability.Descriptor, error) {
		if coding == nil {
			return nil, nil
		}
		definitions := coding.ToolDefinitions()
		result := make([]capability.Descriptor, 0, len(definitions))
		for _, definition := range definitions {
			schema, err := json.Marshal(definition.InputSchema)
			if err != nil {
				return nil, err
			}
			result = append(result, capability.Descriptor{
				ID: definition.Name, Kind: capability.KindTool, Enabled: true,
				Description: definition.Description, Operations: []string{"execute"},
				InputSchema: schema, Metadata: map[string]string{
					"effect": string(definition.EffectType), "origin": "builtin",
				},
			})
		}
		return result, nil
	}); err != nil {
		return nil, err
	}
	if err := registry.RegisterSource("mcp-tools", func() ([]capability.Descriptor, error) {
		if manager == nil {
			return nil, nil
		}
		drivers := manager.Snapshot()
		result := make([]capability.Descriptor, 0, len(drivers))
		for _, driver := range drivers {
			definition := driver.Definition()
			schema, err := json.Marshal(definition.InputSchema)
			if err != nil {
				return nil, err
			}
			result = append(result, capability.Descriptor{
				ID: definition.Name, Kind: capability.KindTool, Enabled: true,
				Description: definition.Description, Operations: []string{"execute"},
				InputSchema: schema, Metadata: map[string]string{
					"effect": string(definition.EffectType), "origin": "mcp",
				},
			})
		}
		return result, nil
	}); err != nil {
		return nil, err
	}
	if err := registry.RegisterSource("skills", func() ([]capability.Descriptor, error) {
		snapshot := skillCatalog.Snapshot()
		result := make([]capability.Descriptor, 0, len(snapshot.Entries))
		for _, entry := range snapshot.Entries {
			result = append(result, capability.Descriptor{
				ID: "skill/" + entry.Name, Kind: capability.KindExtension,
				Enabled: !entry.Disabled, Description: entry.Description,
				Operations: []string{"activate", "read-resource"},
				Metadata:   map[string]string{"origin": "skill", "resources": jsonNumber(entry.ResourceCount)},
			})
		}
		return result, nil
	}); err != nil {
		return nil, err
	}
	if err := registry.RegisterSource("resources", func() ([]capability.Descriptor, error) {
		if resources == nil {
			return nil, nil
		}
		schemes := resources.Schemes()
		result := make([]capability.Descriptor, 0, len(schemes))
		for _, scheme := range schemes {
			result = append(result, capability.Descriptor{
				ID: scheme, Kind: capability.KindResource, Enabled: true,
				Operations: resources.Operations(scheme),
				Metadata:   map[string]string{"uri": scheme + "://"},
			})
		}
		return result, nil
	}); err != nil {
		return nil, err
	}
	return registry, nil
}

func configuredProviderCapabilities(cfg config.Config) capability.Source {
	seen := map[string]struct{}{"chatgpt": {}, "cursor": {}, "grok": {}}
	for id, profile := range cfg.Providers.LLMux {
		if profile.Enabled {
			seen[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return func() ([]capability.Descriptor, error) {
		result := make([]capability.Descriptor, 0, len(ids))
		for _, id := range ids {
			descriptor := capability.Descriptor{
				ID: id, Kind: capability.KindProvider, Enabled: true,
				Operations: []string{"language-model"}, Metadata: map[string]string{"origin": "configured"},
			}
			if portable, ok := llmuxcatalog.Lookup(id); ok {
				compatibility := portable.Descriptor()
				descriptor.Operations = descriptor.Operations[:0]
				for _, supported := range compatibility.Capabilities {
					descriptor.Operations = append(descriptor.Operations, string(supported))
				}
				descriptor.Metadata["wire"] = strings.Join(compatibility.WireProtocols, ",")
			}
			result = append(result, descriptor)
		}
		return result, nil
	}
}

func jsonNumber(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
