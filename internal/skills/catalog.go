package skills

import (
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/venat/skill"
)

const (
	modelSkillCatalogCharBudget   = 8_000
	maxModelSkillDescriptionChars = 250
	maxConfiguredAdditionalDirs   = 56
	modelSkillCatalogPrefix       = "Available Venat skills:\nWhen a task matches a description, call hydaelyn_activate_skill before proceeding. Skill resources remain unavailable until activation.\n"
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
	ManagedDir   string
	Discovery    config.DiscoveryConfig
}

type Entry struct {
	Name          string
	Description   string
	SourcePath    string
	LogoPath      string
	Bundled       bool
	Managed       bool
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
	mu       sync.RWMutex
	updateMu sync.Mutex
	options  LoadOptions
	state    catalogState
}

func Load(options LoadOptions) (*Catalog, error) {
	options = cloneLoadOptions(options)
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
	c.updateMu.Lock()
	defer c.updateMu.Unlock()
	c.mu.RLock()
	options := cloneLoadOptions(c.options)
	c.mu.RUnlock()
	state, err := buildCatalog(options)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.options = options
	c.state = state
	c.mu.Unlock()
	return nil
}

// UpdateConfig rebuilds the catalog before persisting the new selection. The
// in-memory snapshot is swapped only after persistence succeeds, so callers
// never observe a disabled state that was not durably saved.
func (c *Catalog) UpdateConfig(cfg config.SkillsConfig, persist func() error) error {
	if c == nil {
		return errors.New("skills catalog is nil")
	}
	c.updateMu.Lock()
	defer c.updateMu.Unlock()
	c.mu.RLock()
	options := cloneLoadOptions(c.options)
	c.mu.RUnlock()
	options.Config = cloneSkillsConfig(cfg)
	state, err := buildCatalog(options)
	if err != nil {
		return err
	}
	if persist != nil {
		if err := persist(); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.options = options
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
	managed bool
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
	discovered, err := skill.Discover(skill.DiscoveryOptions{
		UserDir:        "",
		ProjectDir:     "",
		TrustProject:   false,
		AdditionalDirs: roots,
	})
	if err != nil {
		return catalogState{}, err
	}
	state.diagnostics = append(state.diagnostics, discovered.Diagnostics...)
	state.diagnostics = append(state.diagnostics, legacySkillDiagnostics(options)...)

	candidates := make(map[string]catalogCandidate, len(discovered.Skills)+len(bundledSkillPaths))
	for _, current := range discovered.Skills {
		candidates[current.Name] = catalogCandidate{skill: current, managed: pathWithinDirectory(options.ManagedDir, current.SourcePath)}
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
			LogoPath:      skillIconDataURL(candidate.skill.SourcePath, candidate.skill.Metadata),
			Bundled:       candidate.bundled,
			Eager:         isEager && !isDisabled,
			Disabled:      isDisabled,
			ResourceCount: len(candidate.skill.Resources),
			Managed:       candidate.managed,
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
	disabled := make(map[string]bool, len(options.Discovery.DisabledProviders))
	for _, provider := range options.Discovery.DisabledProviders {
		disabled[strings.ToLower(strings.TrimSpace(provider))] = true
	}
	var candidates []rootCandidate
	appendRoot := func(provider, path string) {
		if path != "" && !disabled[provider] {
			candidates = append(candidates, rootCandidate{path: path, optional: true})
		}
	}
	discoveryEnabled := options.Discovery.Skills ||
		!options.Discovery.ContextFiles && !options.Discovery.Rules && !options.Discovery.MCP && !options.Discovery.Hooks &&
			len(options.Discovery.DisabledProviders) == 0
	if options.ManagedDir != "" {
		candidates = append(candidates, rootCandidate{path: options.ManagedDir, optional: true})
	}
	appendRoot("venat", filepath.Join(options.HomeDir, ".venat", "skills"))
	if options.Config.TrustProject {
		appendRoot("venat", filepath.Join(options.WorkspaceDir, ".venat", "skills"))
	}
	if discoveryEnabled {
		if options.Config.TrustProject && options.WorkspaceDir != "" {
			appendRoot("github", filepath.Join(options.WorkspaceDir, ".github", "skills"))
		}
		appendRoot("opencode", filepath.Join(options.HomeDir, ".config", "opencode", "skills"))
		if options.Config.TrustProject {
			appendRoot("opencode", filepath.Join(options.WorkspaceDir, ".opencode", "skills"))
		}
		appendRoot("codex", filepath.Join(options.HomeDir, ".codex", "skills"))
		if options.Config.TrustProject {
			appendRoot("codex", filepath.Join(options.WorkspaceDir, ".codex", "skills"))
		}
		if options.Config.TrustProject {
			ancestors := projectAncestors(options.WorkspaceDir, repositoryRoot(options.WorkspaceDir), options.HomeDir)
			for index := len(ancestors) - 1; index >= 0; index-- {
				appendRoot("agents", filepath.Join(ancestors[index], ".agent", "skills"))
				appendRoot("agents", filepath.Join(ancestors[index], ".agents", "skills"))
			}
		}
		appendRoot("agents", filepath.Join(options.HomeDir, ".agent", "skills"))
		appendRoot("agents", filepath.Join(options.HomeDir, ".agents", "skills"))
		appendRoot("claude", filepath.Join(options.HomeDir, ".claude", "skills"))
		if options.Config.TrustProject {
			appendRoot("claude", filepath.Join(options.WorkspaceDir, ".claude", "skills"))
		}
		nativeAgentDir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
		if nativeAgentDir == "" {
			nativeAgentDir = filepath.Join(options.HomeDir, ".omp", "agent")
		}
		appendRoot("native", filepath.Join(nativeAgentDir, "skills"))
		if options.Config.TrustProject {
			ancestors := projectAncestors(options.WorkspaceDir, repositoryRoot(options.WorkspaceDir), options.HomeDir)
			for index := len(ancestors) - 1; index >= 0; index-- {
				appendRoot("native", filepath.Join(ancestors[index], ".omp", "skills"))
			}
		}
	}
	if options.Config.TrustProject {
		candidates = append(candidates, rootCandidate{path: filepath.Join(options.WorkspaceDir, ".azem", "skills"), optional: true})
	}
	if options.ConfigDir != "" {
		candidates = append(candidates, rootCandidate{path: filepath.Join(options.ConfigDir, "skills"), optional: true})
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
	if len(roots) > 64 {
		roots = roots[len(roots)-64:]
	}
	return roots, nil
}

func repositoryRoot(workspace string) string {
	output, err := exec.Command("git", "-C", workspace, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(string(output)))
}

func projectAncestors(workspace, repoRoot, home string) []string {
	boundary := repoRoot
	if boundary == "" && home != "" {
		boundary = home
	}
	var result []string
	for current := filepath.Clean(workspace); ; current = filepath.Dir(current) {
		result = append(result, current)
		if current == boundary || filepath.Dir(current) == current {
			break
		}
	}
	return result
}

func legacySkillDiagnostics(options LoadOptions) []skill.Diagnostic {
	var diagnostics []skill.Diagnostic
	check := func(base string) {
		if base == "" {
			return
		}
		legacy := filepath.Join(base, ".hydaelyn", "skills")
		if _, err := os.Lstat(legacy); err == nil {
			diagnostics = append(diagnostics, skill.Diagnostic{
				Path: legacy, Message: fmt.Sprintf("legacy discovery root is ignored; move skills to %s", filepath.Join(base, ".venat", "skills")),
			})
		}
	}
	check(options.HomeDir)
	if options.Config.TrustProject {
		check(options.WorkspaceDir)
	}
	return diagnostics
}

func pathWithinDirectory(root, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
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

func cloneLoadOptions(options LoadOptions) LoadOptions {
	options.Config = cloneSkillsConfig(options.Config)
	options.Discovery.DisabledProviders = append([]string(nil), options.Discovery.DisabledProviders...)
	options.Discovery.DisabledRules = append([]string(nil), options.Discovery.DisabledRules...)
	options.Discovery.AdditionalContextFiles = append([]string(nil), options.Discovery.AdditionalContextFiles...)
	return options
}

func cloneSkillsConfig(cfg config.SkillsConfig) config.SkillsConfig {
	cfg.AdditionalDirs = append([]string(nil), cfg.AdditionalDirs...)
	cfg.Eager = append([]string(nil), cfg.Eager...)
	cfg.Disabled = append([]string(nil), cfg.Disabled...)
	return cfg
}

func skillIconDataURL(sourcePath string, metadata map[string]string) string {
	dir := filepath.Dir(strings.TrimSpace(sourcePath))
	if dir == "." || dir == "" {
		return ""
	}
	var candidates []string
	if icon := strings.TrimSpace(metadata["icon"]); icon != "" && !strings.Contains(icon, "..") {
		candidates = append(candidates, filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(icon, "./"))))
	}
	for _, name := range []string{"icon.svg", "icon.png", "icon.webp", "icon.jpg", "icon.jpeg"} {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	for _, path := range candidates {
		if data := boundedImageDataURL(path); data != "" {
			return data
		}
	}
	return ""
}

func boundedImageDataURL(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return ""
	}
	contentType := http.DetectContentType(data)
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		contentType = "image/svg+xml"
	}
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml":
		return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data)
	default:
		return ""
	}
}
