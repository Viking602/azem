package skills

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/go-hydaelyn/skill"
)

const (
	modelSkillCatalogCharBudget   = 8_000
	maxModelSkillDescriptionChars = 250
	maxConfiguredAdditionalDirs   = 56
	modelSkillCatalogPrefix       = "Available Hydaelyn skills:\nWhen a task matches a description, call hydaelyn_activate_skill before proceeding. Skill resources remain unavailable until activation.\n"
)

//go:embed bundled/*/SKILL.md
var bundledSkills embed.FS

var bundledSkillPaths = []string{
	"bundled/simplify/SKILL.md",
	"bundled/skill-author/SKILL.md",
	"bundled/verify/SKILL.md",
}

type LoadOptions struct {
	HomeDir      string
	ConfigDir    string
	WorkspaceDir string
	Config       config.SkillsConfig
}

type Entry struct {
	Name          string
	Description   string
	SourcePath    string
	Bundled       bool
	Eager         bool
	Disabled      bool
	ModelVisible  bool
	ResourceCount int
}

type Snapshot struct {
	Registry    *skill.Registry
	Eager       []string
	Available   []string
	Entries     []Entry
	Diagnostics []skill.Diagnostic
}

type catalogState struct {
	registry    *skill.Registry
	eager       []string
	available   []string
	entries     []Entry
	diagnostics []skill.Diagnostic
}

type Catalog struct {
	mu      sync.RWMutex
	options LoadOptions
	state   catalogState
}

func Load(options LoadOptions) (*Catalog, error) {
	state, err := buildCatalog(options)
	if err != nil {
		return nil, err
	}
	return &Catalog{options: options, state: state}, nil
}

func (c *Catalog) Reload() error {
	if c == nil {
		return errors.New("skills catalog is nil")
	}
	state, err := buildCatalog(c.options)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.state = state
	c.mu.Unlock()
	return nil
}

func (c *Catalog) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Snapshot{
		Registry:    c.state.registry,
		Eager:       append([]string(nil), c.state.eager...),
		Available:   append([]string(nil), c.state.available...),
		Entries:     append([]Entry(nil), c.state.entries...),
		Diagnostics: append([]skill.Diagnostic(nil), c.state.diagnostics...),
	}
}

type catalogCandidate struct {
	skill   skill.Skill
	bundled bool
}

func buildCatalog(options LoadOptions) (catalogState, error) {
	state := catalogState{registry: skill.NewRegistry()}
	if !options.Config.Enabled {
		return state, nil
	}
	if err := validateSkillSelection(options.Config); err != nil {
		return catalogState{}, err
	}

	roots, err := discoveryRoots(options)
	if err != nil {
		return catalogState{}, err
	}
	discovered, err := skill.Discover(skill.DiscoveryOptions{AdditionalDirs: roots})
	if err != nil {
		return catalogState{}, err
	}
	state.diagnostics = append(state.diagnostics, discovered.Diagnostics...)

	candidates := make(map[string]catalogCandidate, len(discovered.Skills)+len(bundledSkillPaths))
	for _, current := range discovered.Skills {
		candidates[current.Name] = catalogCandidate{skill: current}
	}
	for _, path := range bundledSkillPaths {
		content, readErr := bundledSkills.ReadFile(path)
		if readErr != nil {
			return catalogState{}, fmt.Errorf("read bundled skill %q: %w", path, readErr)
		}
		current, parseErr := skill.Parse("", content)
		if parseErr != nil {
			return catalogState{}, fmt.Errorf("parse bundled skill %q: %w", path, parseErr)
		}
		if shadowed, exists := candidates[current.Name]; exists {
			state.diagnostics = append(state.diagnostics, skill.Diagnostic{
				Path:    shadowed.skill.SourcePath,
				Message: fmt.Sprintf("skill %q from %s is shadowed by bundled skill", current.Name, shadowed.skill.SourcePath),
			})
		}
		candidates[current.Name] = catalogCandidate{skill: current, bundled: true}
	}

	disabled := make(map[string]struct{}, len(options.Config.Disabled))
	for _, name := range options.Config.Disabled {
		disabled[name] = struct{}{}
	}
	eager := make(map[string]struct{}, len(options.Config.Eager))
	for _, name := range options.Config.Eager {
		eager[name] = struct{}{}
	}

	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	state.entries = make([]Entry, 0, len(names))
	for _, name := range names {
		candidate := candidates[name]
		_, isDisabled := disabled[name]
		_, isEager := eager[name]
		entry := Entry{
			Name:          name,
			Description:   candidate.skill.Description,
			SourcePath:    candidate.skill.SourcePath,
			Bundled:       candidate.bundled,
			Eager:         isEager && !isDisabled,
			Disabled:      isDisabled,
			ResourceCount: len(candidate.skill.Resources),
		}
		state.entries = append(state.entries, entry)
		if isDisabled {
			continue
		}
		modelSkill := candidate.skill
		modelSkill.Description = truncateRunes(modelSkill.Description, maxModelSkillDescriptionChars)
		if err := skill.Register(state.registry, modelSkill); err != nil {
			return catalogState{}, fmt.Errorf("register skill %q: %w", name, err)
		}
	}
	if _, err := state.registry.Resolve(options.Config.Eager...); err != nil {
		return catalogState{}, err
	}
	state.eager = append([]string(nil), options.Config.Eager...)
	sort.Strings(state.eager)
	selectModelVisible(&state, candidates, eager, disabled)
	return state, nil
}

func validateSkillSelection(cfg config.SkillsConfig) error {
	if len(cfg.AdditionalDirs) > maxConfiguredAdditionalDirs {
		return fmt.Errorf("skills.additional_dirs must contain at most %d entries", maxConfiguredAdditionalDirs)
	}
	eager := make(map[string]struct{}, len(cfg.Eager))
	for _, name := range cfg.Eager {
		if strings.TrimSpace(name) == "" {
			return errors.New("skills.eager contains an empty skill name")
		}
		if _, exists := eager[name]; exists {
			return fmt.Errorf("skills.eager contains duplicate skill %q", name)
		}
		eager[name] = struct{}{}
	}
	disabled := make(map[string]struct{}, len(cfg.Disabled))
	for _, name := range cfg.Disabled {
		if strings.TrimSpace(name) == "" {
			return errors.New("skills.disabled contains an empty skill name")
		}
		if _, exists := disabled[name]; exists {
			return fmt.Errorf("skills.disabled contains duplicate skill %q", name)
		}
		if _, exists := eager[name]; exists {
			return fmt.Errorf("skill %q cannot be both eager and disabled", name)
		}
		disabled[name] = struct{}{}
	}
	return nil
}

func discoveryRoots(options LoadOptions) ([]string, error) {
	type rootCandidate struct {
		path     string
		optional bool
	}
	var candidates []rootCandidate
	appendConventional := func(base string, directories ...string) {
		if base == "" {
			return
		}
		for _, directory := range directories {
			candidates = append(candidates, rootCandidate{path: filepath.Join(base, directory, "skills"), optional: true})
		}
	}
	appendConventional(options.HomeDir, ".agents", ".hydaelyn", ".claude")
	if options.ConfigDir != "" {
		candidates = append(candidates, rootCandidate{path: filepath.Join(options.ConfigDir, "skills"), optional: true})
	}
	if options.Config.TrustProject && options.WorkspaceDir != "" {
		appendConventional(options.WorkspaceDir, ".agents", ".hydaelyn", ".claude", ".azem")
	}
	for _, directory := range options.Config.AdditionalDirs {
		candidates = append(candidates, rootCandidate{path: directory})
	}

	roots := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.path == "" {
			continue
		}
		absolute, err := filepath.Abs(candidate.path)
		if err != nil {
			return nil, err
		}
		absolute = filepath.Clean(absolute)
		canonical, err := filepath.EvalSymlinks(absolute)
		if err == nil {
			absolute = filepath.Clean(canonical)
		} else if errors.Is(err, os.ErrNotExist) && candidate.optional {
			continue
		}
		if _, exists := seen[absolute]; exists {
			continue
		}
		seen[absolute] = struct{}{}
		roots = append(roots, absolute)
	}
	return roots, nil
}

func selectModelVisible(state *catalogState, candidates map[string]catalogCandidate, eager, disabled map[string]struct{}) {
	selectionOrder := make([]string, 0, len(candidates))
	for name, candidate := range candidates {
		if candidate.bundled {
			selectionOrder = append(selectionOrder, name)
		}
	}
	sort.Strings(selectionOrder)
	disk := make([]string, 0, len(candidates))
	for name, candidate := range candidates {
		if !candidate.bundled {
			disk = append(disk, name)
		}
	}
	sort.Strings(disk)
	selectionOrder = append(selectionOrder, disk...)

	entryIndex := make(map[string]int, len(state.entries))
	for i := range state.entries {
		entryIndex[state.entries[i].Name] = i
	}
	renderedChars := utf8.RuneCountInString(modelSkillCatalogPrefix) - 1
	for _, name := range selectionOrder {
		if _, excluded := eager[name]; excluded {
			continue
		}
		if _, excluded := disabled[name]; excluded {
			continue
		}
		registered, exists := state.registry.Get(name)
		if !exists {
			continue
		}
		line := "- " + registered.Name + ": " + registered.Description + "\n"
		lineChars := utf8.RuneCountInString(line)
		if renderedChars+lineChars > modelSkillCatalogCharBudget {
			continue
		}
		renderedChars += lineChars
		state.available = append(state.available, name)
		state.entries[entryIndex[name]].ModelVisible = true
	}
	sort.Strings(state.available)
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
