package app

import (
	"fmt"
	"sort"

	"github.com/Viking602/venat/tool"
)

type dynamicToolCatalog struct {
	bus     *tool.Bus
	sources map[string]string
}

func newDynamicToolCatalog() *dynamicToolCatalog {
	return &dynamicToolCatalog{bus: tool.NewBus(), sources: make(map[string]string)}
}

func (catalog *dynamicToolCatalog) Register(source string, driver tool.Driver) error {
	if catalog == nil || driver == nil {
		return fmt.Errorf("dynamic tool source and driver are required")
	}
	definition := driver.Definition()
	if previous := catalog.sources[definition.Name]; previous != "" {
		return fmt.Errorf("dynamic tool %q is provided by both %s and %s", definition.Name, previous, source)
	}
	if err := catalog.bus.Register(driver); err != nil {
		return fmt.Errorf("register dynamic tool %s from %s: %w", definition.Name, source, err)
	}
	catalog.sources[definition.Name] = source
	return nil
}

func (catalog *dynamicToolCatalog) RegisterAll(source string, drivers []tool.Driver) error {
	for _, driver := range drivers {
		if err := catalog.Register(source, driver); err != nil {
			return err
		}
	}
	return nil
}

func (catalog *dynamicToolCatalog) Filter(keep func(tool.Definition) bool) *dynamicToolCatalog {
	filtered := newDynamicToolCatalog()
	if catalog == nil || keep == nil {
		return filtered
	}
	for _, definition := range catalog.bus.Definitions() {
		if !keep(definition) {
			continue
		}
		driver, _ := catalog.bus.Driver(definition.Name)
		_ = filtered.Register(catalog.sources[definition.Name], driver)
	}
	return filtered
}

func (catalog *dynamicToolCatalog) Drivers() []tool.Driver {
	if catalog == nil {
		return nil
	}
	definitions := catalog.bus.Definitions()
	drivers := make([]tool.Driver, 0, len(definitions))
	for _, definition := range definitions {
		if driver, ok := catalog.bus.Driver(definition.Name); ok {
			drivers = append(drivers, driver)
		}
	}
	return drivers
}

func (catalog *dynamicToolCatalog) Names() []string {
	if catalog == nil {
		return nil
	}
	definitions := catalog.bus.Definitions()
	names := make([]string, len(definitions))
	for index := range definitions {
		names[index] = definitions[index].Name
	}
	sort.Strings(names)
	return names
}

func dynamicToolSource(name string) string {
	switch {
	case len(name) >= 5 && name[:5] == "mcp__":
		return "mcp"
	case len(name) >= 7 && name[:7] == "coding.":
		return "workspace"
	case name == subagentSpawnTool || name == subagentGetOutputTool || name == subagentKillTool:
		return "subagent"
	case name == "todo" || name == goalToolName || name == checkpointToolName || name == rewindToolName || name == contextReadArtifactTool || name == askToolName || name == submitPlanToolName:
		return "control"
	default:
		return "extension"
	}
}

func (catalog *dynamicToolCatalog) Bus() *tool.Bus {
	if catalog == nil {
		return tool.NewBus()
	}
	return catalog.bus
}
