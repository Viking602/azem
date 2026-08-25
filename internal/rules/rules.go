package rules

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Viking602/azem/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	maxRuleBytes = 1 << 20
	maxRuleTotal = 4 << 20
)

type Rule struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Content       string   `json:"content"`
	Provider      string   `json:"provider"`
	Level         string   `json:"level"`
	Globs         []string `json:"globs,omitempty"`
	AlwaysApply   bool     `json:"alwaysApply,omitempty"`
	Description   string   `json:"description,omitempty"`
	Conditions    []string `json:"conditions,omitempty"`
	ASTConditions []string `json:"astConditions,omitempty"`
	Scope         []string `json:"scope,omitempty"`
	InterruptMode string   `json:"interruptMode,omitempty"`
}

type Result struct {
	Rules    []Rule   `json:"rules"`
	Warnings []string `json:"warnings,omitempty"`
}

type Options struct {
	Workspace         string
	HomeDir           string
	NativeAgentDir    string
	DisabledProviders map[string]bool
	DisabledRules     map[string]bool
}

type Catalog struct {
	mu    sync.RWMutex
	rules map[string]Rule
}

type source struct {
	provider, level string
	priority        int
	order           int
	path            string
	recursive       bool
	stripSuffix     string
	fixedName       string
	github          bool
	sticky          bool
}

func Discover(ctx context.Context, options Options) (Result, error) {
	workspace, err := filepath.Abs(options.Workspace)
	if err != nil {
		return Result{}, err
	}
	home := options.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	native := options.NativeAgentDir
	if native == "" {
		native = strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	}
	if native == "" && home != "" {
		native = filepath.Join(home, ".omp", "agent")
	}
	repoRoot := repositoryRoot(ctx, workspace)
	boundary := repoRoot
	if boundary == "" {
		boundary = homeBoundary(workspace, home)
	}
	var sources []source
	order := 0
	add := func(item source) {
		if item.path == "" || options.DisabledProviders[item.provider] {
			return
		}
		item.order = order
		order++
		sources = append(sources, item)
	}
	// Native first: project directory, user directory, user sticky, project sticky.
	projectNative := filepath.Join(workspace, ".omp")
	if nonEmptyDir(projectNative) {
		add(source{provider: "native", level: "project", priority: 100, path: filepath.Join(projectNative, "rules"), stripSuffix: ".md"})
	}
	add(source{provider: "native", level: "user", priority: 100, path: filepath.Join(native, "rules"), stripSuffix: ".md"})
	add(source{provider: "native", level: "user", priority: 100, path: filepath.Join(native, "RULES.md"), fixedName: "RULES", sticky: true})
	if nearest := nearestNonEmptyConfigDir(workspace, boundary, ".omp"); nearest != "" {
		add(source{provider: "native", level: "project", priority: 100, path: filepath.Join(nearest, "RULES.md"), fixedName: "RULES@project", sticky: true})
	}
	// AGENTS/Codex-compatible directories walk project ancestors before user roots.
	for current := workspace; ; current = filepath.Dir(current) {
		add(source{provider: "agents", level: "project", priority: 70, path: filepath.Join(current, ".agent", "rules"), stripSuffix: ".md"})
		add(source{provider: "agents", level: "project", priority: 70, path: filepath.Join(current, ".agents", "rules"), stripSuffix: ".md"})
		if samePath(current, boundary) || filepath.Dir(current) == current {
			break
		}
	}
	if home != "" {
		add(source{provider: "agents", level: "user", priority: 70, path: filepath.Join(home, ".agent", "rules"), stripSuffix: ".md"})
		add(source{provider: "agents", level: "user", priority: 70, path: filepath.Join(home, ".agents", "rules"), stripSuffix: ".md"})
		add(source{provider: "cursor", level: "user", priority: 50, path: filepath.Join(home, ".cursor", "rules"), stripSuffix: ".md"})
	}
	add(source{provider: "cursor", level: "project", priority: 50, path: filepath.Join(workspace, ".cursor", "rules"), stripSuffix: ".md"})
	if home != "" {
		add(source{provider: "windsurf", level: "user", priority: 50, path: filepath.Join(home, ".codeium", "windsurf", "memories", "global_rules.md"), fixedName: "global_rules"})
	}
	add(source{provider: "windsurf", level: "project", priority: 50, path: filepath.Join(workspace, ".windsurf", "rules"), stripSuffix: ".md"})
	if clinePath, directory := nearestClineRules(workspace); clinePath != "" {
		if directory {
			add(source{provider: "cline", level: "project", priority: 40, path: clinePath, stripSuffix: ".md"})
		} else {
			add(source{provider: "cline", level: "project", priority: 40, path: clinePath, fixedName: "clinerules"})
		}
	}
	add(source{provider: "github", level: "project", priority: 30, path: filepath.Join(workspace, ".github", "instructions"), recursive: true, stripSuffix: ".instructions.md", github: true})
	for _, dir := range filepath.SplitList(os.Getenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS")) {
		add(source{provider: "github", level: "user", priority: 30, path: filepath.Join(dir, ".github", "instructions"), recursive: true, stripSuffix: ".instructions.md", github: true})
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].priority != sources[j].priority {
			return sources[i].priority > sources[j].priority
		}
		return sources[i].order < sources[j].order
	})

	var warnings []string
	var loaded []Rule
	total := 0
	for _, item := range sources {
		paths, listWarnings := sourcePaths(item)
		warnings = append(warnings, listWarnings...)
		for _, path := range paths {
			payload, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("read %s: %v", path, err))
				continue
			}
			if len(payload) == 0 {
				continue
			}
			if len(payload) > maxRuleBytes {
				warnings = append(warnings, fmt.Sprintf("skip oversized rule %s", path))
				continue
			}
			total += len(payload)
			if total > maxRuleTotal {
				return Result{}, fmt.Errorf("rules exceed %d bytes", maxRuleTotal)
			}
			name := item.fixedName
			if name == "" {
				name = filepath.Base(path)
				if item.stripSuffix != "" && strings.HasSuffix(name, item.stripSuffix) {
					name = strings.TrimSuffix(name, item.stripSuffix)
				} else {
					name = strings.TrimSuffix(strings.TrimSuffix(name, ".mdc"), ".md")
				}
			}
			rule, ruleWarnings := parseRule(name, path, string(payload), item)
			warnings = append(warnings, ruleWarnings...)
			if !options.DisabledRules[rule.Name] {
				loaded = append(loaded, rule)
			}
		}
	}
	seen := make(map[string]bool)
	result := make([]Rule, 0, len(loaded))
	for _, rule := range loaded {
		if rule.Name == "" || seen[rule.Name] {
			continue
		}
		seen[rule.Name] = true
		result = append(result, rule)
	}
	return Result{Rules: result, Warnings: warnings}, nil
}

func NewCatalog(result Result) *Catalog {
	catalog := &Catalog{rules: make(map[string]Rule)}
	for _, rule := range result.Rules {
		if isActive(rule) {
			catalog.rules[rule.Name] = cloneRule(rule)
		}
	}
	return catalog
}

func (catalog *Catalog) Get(name string) (Rule, bool) {
	if catalog == nil {
		return Rule{}, false
	}
	catalog.mu.RLock()
	rule, ok := catalog.rules[name]
	catalog.mu.RUnlock()
	return cloneRule(rule), ok
}

func (catalog *Catalog) Names() []string {
	if catalog == nil {
		return nil
	}
	catalog.mu.RLock()
	names := make([]string, 0, len(catalog.rules))
	for name := range catalog.rules {
		names = append(names, name)
	}
	catalog.mu.RUnlock()
	sort.Strings(names)
	return names
}

func Render(result Result, loadedContext string) string {
	var always []Rule
	var rulebook []Rule
	for _, rule := range result.Rules {
		if len(rule.Conditions) > 0 || len(rule.ASTConditions) > 0 {
			continue
		}
		if rule.AlwaysApply {
			if !containsNormalized(loadedContext, rule.Content) {
				always = append(always, rule)
			}
		} else if rule.Description != "" {
			rulebook = append(rulebook, rule)
		}
	}
	if len(always) == 0 && len(rulebook) == 0 {
		return ""
	}
	var output strings.Builder
	if len(always) > 0 {
		output.WriteString("<generic-rules>\nYou MUST follow these always-apply rules:\n")
		for _, rule := range always {
			fmt.Fprintf(&output, "<rule name=%q path=%q>\n%s\n</rule>\n", rule.Name, rule.Path, rule.Content)
		}
		output.WriteString("</generic-rules>")
	}
	if len(rulebook) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString("<domain-rules>\nRead applicable rules with coding.read_file on rule://<name> before working in their domain:\n")
		for _, rule := range rulebook {
			fmt.Fprintf(&output, "- %s", rule.Name)
			if len(rule.Globs) > 0 {
				fmt.Fprintf(&output, " (%s)", strings.Join(rule.Globs, ", "))
			}
			fmt.Fprintf(&output, ": %s\n", rule.Description)
		}
		output.WriteString("</domain-rules>")
	}
	return output.String()
}

func TTSRRules(result Result) []config.StreamRuleConfig {
	var rules []config.StreamRuleConfig
	for _, rule := range result.Rules {
		if len(rule.Conditions) == 0 && len(rule.ASTConditions) == 0 {
			continue
		}
		rules = append(rules, config.StreamRuleConfig{
			Name: rule.Name, Content: rule.Content, Conditions: append([]string(nil), rule.Conditions...), ASTConditions: append([]string(nil), rule.ASTConditions...), Scope: append([]string(nil), rule.Scope...), Globs: append([]string(nil), rule.Globs...), InterruptMode: rule.InterruptMode,
		})
	}
	return rules
}

func parseRule(name, path, content string, item source) (Rule, []string) {
	frontmatter, body, warning := parseFrontmatter(content)
	rule := Rule{Name: name, Path: path, Content: strings.TrimSpace(body), Provider: item.provider, Level: item.level, AlwaysApply: item.sticky}
	rule.Description, _ = frontmatter["description"].(string)
	rule.Globs = stringList(frontmatter["globs"])
	if value, ok := frontmatter["alwaysApply"].(bool); ok && value {
		rule.AlwaysApply = true
	}
	rawCondition := frontmatter["condition"]
	if rawCondition == nil {
		rawCondition = frontmatter["ttsr_trigger"]
	}
	if rawCondition == nil {
		rawCondition = frontmatter["ttsrTrigger"]
	}
	for _, condition := range stringList(rawCondition) {
		if isLikelyFileGlob(condition) {
			rule.Scope = append(rule.Scope, "tool:edit("+condition+")", "tool:write("+condition+")")
		} else {
			rule.Conditions = append(rule.Conditions, condition)
		}
	}
	if len(rule.Conditions) == 0 && len(rule.Scope) > 0 {
		rule.Conditions = []string{".*"}
	}
	rule.ASTConditions = stringList(frontmatter["astCondition"])
	for _, value := range stringList(frontmatter["scope"]) {
		rule.Scope = append(rule.Scope, splitScope(value)...)
	}
	rule.Scope = unique(rule.Scope)
	if mode, _ := frontmatter["interruptMode"].(string); mode == "never" || mode == "prose-only" || mode == "tool-only" || mode == "always" {
		rule.InterruptMode = mode
	}
	if item.github {
		applyTo := stringList(frontmatter["applyTo"])
		if len(applyTo) == 0 {
			if rule.Description == "" {
				rule.Description = "GitHub Copilot instructions without applyTo metadata"
			}
			return rule, appendWarning(warning, "missing applyTo in "+path+"; loaded without GitHub glob scoping")
		}
		var globs []string
		for _, entry := range applyTo {
			globs = append(globs, splitCSV(entry)...)
		}
		for _, glob := range globs {
			if glob == "*" || glob == "**" || glob == "**/*" {
				rule.AlwaysApply = true
				rule.Globs = nil
				return rule, appendWarning(warning)
			}
		}
		rule.AlwaysApply = false
		rule.Globs = unique(globs)
		if rule.Description == "" {
			rule.Description = "GitHub Copilot instructions for " + strings.Join(rule.Globs, ", ")
		}
	}
	return rule, appendWarning(warning)
}

func parseFrontmatter(content string) (map[string]any, string, string) {
	metadata := make(map[string]any)
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return metadata, content, ""
	}
	remaining := normalized[4:]
	index := strings.Index(remaining, "\n---")
	if index < 0 {
		return metadata, content, ""
	}
	front := remaining[:index]
	body := strings.TrimPrefix(remaining[index+4:], "\n")
	if err := yaml.Unmarshal([]byte(front), &metadata); err != nil {
		return make(map[string]any), content, fmt.Sprintf("invalid rule frontmatter: %v", err)
	}
	return metadata, body, ""
}

func sourcePaths(item source) ([]string, []string) {
	info, err := os.Stat(item.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("stat %s: %v", item.path, err)}
	}
	if !info.IsDir() {
		return []string{item.path}, nil
	}
	var paths []string
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != item.path && !item.recursive {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if item.github {
			if strings.HasSuffix(name, ".instructions.md") {
				paths = append(paths, path)
			}
		} else if strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".mdc") {
			paths = append(paths, path)
		}
		return nil
	}
	_ = filepath.WalkDir(item.path, walk)
	sort.Strings(paths)
	return paths, nil
}

func nearestClineRules(start string) (string, bool) {
	for current := start; ; current = filepath.Dir(current) {
		path := filepath.Join(current, ".clinerules")
		if info, err := os.Stat(path); err == nil {
			return path, info.IsDir()
		}
		if filepath.Dir(current) == current {
			return "", false
		}
	}
}

func nearestNonEmptyConfigDir(start, boundary, name string) string {
	for current := start; ; current = filepath.Dir(current) {
		dir := filepath.Join(current, name)
		if nonEmptyDir(dir) {
			return dir
		}
		if samePath(current, boundary) || filepath.Dir(current) == current {
			return ""
		}
	}
}

func nonEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

func repositoryRoot(ctx context.Context, workspace string) string {
	output, err := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(string(output)))
}

func homeBoundary(workspace, home string) string {
	if home != "" && within(workspace, home) {
		return home
	}
	return string(filepath.Separator)
}

func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool { return filepath.Clean(left) == filepath.Clean(right) }

func stringList(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	case []any:
		var result []string
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
		return unique(result)
	case []string:
		return unique(typed)
	default:
		return nil
	}
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func splitScope(value string) []string {
	var result []string
	start, parens, brackets, braces := 0, 0, 0, 0
	quote := byte(0)
	for index := 0; index < len(value); index++ {
		current := value[index]
		if quote != 0 {
			if current == quote && (index == 0 || value[index-1] != '\\') {
				quote = 0
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '(':
			parens++
		case ')':
			if parens > 0 {
				parens--
			}
		case '[':
			brackets++
		case ']':
			if brackets > 0 {
				brackets--
			}
		case '{':
			braces++
		case '}':
			if braces > 0 {
				braces--
			}
		case ',':
			if parens == 0 && brackets == 0 && braces == 0 {
				if token := strings.Trim(strings.TrimSpace(value[start:index]), "\"'"); token != "" {
					result = append(result, token)
				}
				start = index + 1
			}
		}
	}
	if token := strings.Trim(strings.TrimSpace(value[start:]), "\"'"); token != "" {
		result = append(result, token)
	}
	return result
}

func isLikelyFileGlob(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, `\\^$+|()`) || !strings.ContainsAny(value, "?*[]{}") {
		return false
	}
	return strings.Contains(value, "/") || regexp.MustCompile(`^\*\.[^\s/]+$`).MatchString(value)
}

func unique(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func isActive(rule Rule) bool {
	return rule.AlwaysApply || rule.Description != "" || len(rule.Conditions) > 0 || len(rule.ASTConditions) > 0
}

func cloneRule(rule Rule) Rule {
	rule.Globs = append([]string(nil), rule.Globs...)
	rule.Conditions = append([]string(nil), rule.Conditions...)
	rule.ASTConditions = append([]string(nil), rule.ASTConditions...)
	rule.Scope = append([]string(nil), rule.Scope...)
	return rule
}

func containsNormalized(haystack, needle string) bool {
	return strings.TrimSpace(needle) != "" && strings.Contains(strings.TrimSpace(haystack), strings.TrimSpace(needle))
}

func appendWarning(values ...string) []string {
	var result []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}
