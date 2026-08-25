package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxCommandBytes = 1 << 20

var commandNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type Entry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Path        string `json:"path"`
	Provider    string `json:"provider"`
	Level       string `json:"level"`
}

type Options struct {
	Workspace         string
	HomeDir           string
	NativeAgentDir    string
	DisabledProviders map[string]bool
	AdditionalDirs    []string
}

type Catalog struct {
	entries []Entry
	byName  map[string]Entry
}

type root struct {
	path, provider, level, suffix string
	priority                      int
}

func Discover(options Options) (*Catalog, []string, error) {
	workspace, err := filepath.Abs(options.Workspace)
	if err != nil {
		return nil, nil, err
	}
	home := options.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	native := options.NativeAgentDir
	if native == "" {
		native = strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	}
	if native == "" {
		native = filepath.Join(home, ".omp", "agent")
	}
	var roots []root
	add := func(path, provider, level, suffix string, priority int) {
		if path != "" && !options.DisabledProviders[provider] {
			roots = append(roots, root{path: path, provider: provider, level: level, suffix: suffix, priority: priority})
		}
	}
	add(filepath.Join(workspace, ".omp", "commands"), "native", "project", ".md", 100)
	add(filepath.Join(native, "commands"), "native", "user", ".md", 100)
	for _, directory := range options.AdditionalDirs {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(workspace, directory)
		}
		add(directory, "omp-plugins", "project", ".md", 90)
	}
	add(filepath.Join(workspace, ".claude", "commands"), "claude", "project", ".md", 80)
	add(filepath.Join(home, ".claude", "commands"), "claude", "user", ".md", 80)
	add(filepath.Join(workspace, ".codex", "commands"), "codex", "project", ".md", 70)
	add(filepath.Join(home, ".codex", "commands"), "codex", "user", ".md", 70)
	add(filepath.Join(workspace, ".opencode", "commands"), "opencode", "project", ".md", 55)
	add(filepath.Join(home, ".config", "opencode", "commands"), "opencode", "user", ".md", 55)
	add(filepath.Join(workspace, ".github", "prompts"), "github", "project", ".prompt.md", 30)
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].priority > roots[j].priority })
	catalog := &Catalog{byName: make(map[string]Entry)}
	var diagnostics []string
	for _, root := range roots {
		paths, err := commandFiles(root.path, root.suffix)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				diagnostics = append(diagnostics, fmt.Sprintf("scan %s: %v", root.path, err))
			}
			continue
		}
		for _, path := range paths {
			payload, err := os.ReadFile(path)
			if err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("read %s: %v", path, err))
				continue
			}
			if len(payload) == 0 || len(payload) > maxCommandBytes {
				diagnostics = append(diagnostics, fmt.Sprintf("skip empty or oversized command %s", path))
				continue
			}
			name := strings.TrimSuffix(filepath.Base(path), root.suffix)
			if !commandNamePattern.MatchString(name) {
				diagnostics = append(diagnostics, fmt.Sprintf("invalid command name %q from %s", name, path))
				continue
			}
			if _, exists := catalog.byName[name]; exists {
				continue
			}
			description, body := parseCommand(string(payload))
			entry := Entry{Name: name, Description: description, Content: body, Path: path, Provider: root.provider, Level: root.level}
			catalog.byName[name] = entry
			catalog.entries = append(catalog.entries, entry)
		}
	}
	sort.Slice(catalog.entries, func(i, j int) bool { return catalog.entries[i].Name < catalog.entries[j].Name })
	return catalog, diagnostics, nil
}

func (catalog *Catalog) Entries() []Entry {
	if catalog == nil {
		return nil
	}
	return append([]Entry(nil), catalog.entries...)
}

func (catalog *Catalog) Expand(input string) (string, bool) {
	if catalog == nil || !strings.HasPrefix(input, "/") {
		return input, false
	}
	nameAndArgs := strings.TrimPrefix(input, "/")
	name := nameAndArgs
	argsText := ""
	if index := strings.IndexAny(nameAndArgs, " \t"); index >= 0 {
		name, argsText = nameAndArgs[:index], strings.TrimSpace(nameAndArgs[index+1:])
	}
	entry, exists := catalog.byName[name]
	if !exists {
		return input, false
	}
	args := parseArgs(argsText)
	content := substituteArgs(entry.Content, args)
	content = renderArgumentTemplates(content, args)
	if argsText != "" && !templateUsesArgs(entry.Content) {
		content = strings.TrimSpace(content) + "\n\n" + strings.Join(args, " ")
	}
	return strings.TrimSpace(content), true
}

func commandFiles(directory, suffix string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), suffix) {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func parseCommand(content string) (string, string) {
	metadata := map[string]any{}
	body := content
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if strings.HasPrefix(normalized, "---\n") {
		if end := strings.Index(normalized[4:], "\n---"); end >= 0 {
			_ = yaml.Unmarshal([]byte(normalized[4:4+end]), &metadata)
			body = strings.TrimPrefix(normalized[4+end+4:], "\n")
		}
	}
	description, _ := metadata["description"].(string)
	description = strings.TrimSpace(description)
	body = strings.TrimSpace(body)
	if description == "" {
		for _, line := range strings.Split(body, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				description = line
				if len([]rune(description)) > 60 {
					description = string([]rune(description)[:60]) + "..."
				}
				break
			}
		}
	}
	return description, body
}

func parseArgs(input string) []string {
	var result []string
	var current strings.Builder
	quote := rune(0)
	for _, character := range input {
		if quote != 0 {
			if character == quote {
				quote = 0
			} else {
				current.WriteRune(character)
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote = character
		case ' ', '\t':
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(character)
		}
	}
	if current.Len() > 0 {
		result = append(result, current.String())
	}
	return result
}

func substituteArgs(content string, args []string) string {
	all := strings.Join(args, " ")
	pattern := regexp.MustCompile(`\$@\[(\d+)(?::(\d*)?)?\]|\$ARGUMENTS|\$@|\$(\d+)`)
	return pattern.ReplaceAllStringFunc(content, func(match string) string {
		if match == "$ARGUMENTS" || match == "$@" {
			return all
		}
		if strings.HasPrefix(match, "$@[") {
			value := strings.TrimSuffix(strings.TrimPrefix(match, "$@["), "]")
			startText, lengthText, hasLength := strings.Cut(value, ":")
			start, _ := strconv.Atoi(startText)
			if start < 1 || start > len(args) {
				return ""
			}
			end := len(args)
			if hasLength && lengthText != "" {
				length, _ := strconv.Atoi(lengthText)
				if length <= 0 {
					return ""
				}
				end = min(len(args), start-1+length)
			}
			return strings.Join(args[start-1:end], " ")
		}
		index, _ := strconv.Atoi(strings.TrimPrefix(match, "$"))
		if index < 1 || index > len(args) {
			return ""
		}
		return args[index-1]
	})
}

func renderArgumentTemplates(content string, args []string) string {
	all := strings.Join(args, " ")
	for _, token := range []string{"{{arguments}}", "{{ARGUMENTS}}", "{{args}}"} {
		content = strings.ReplaceAll(content, token, all)
	}
	pattern := regexp.MustCompile(`\{\{\s*arg\s+(\d+)\s*\}\}`)
	return pattern.ReplaceAllStringFunc(content, func(match string) string {
		parts := pattern.FindStringSubmatch(match)
		index, _ := strconv.Atoi(parts[1])
		if index < 1 || index > len(args) {
			return ""
		}
		return args[index-1]
	})
}

func templateUsesArgs(content string) bool {
	return strings.Contains(content, "$ARGUMENTS") || strings.Contains(content, "$@") || regexp.MustCompile(`\$\d+|\{\{[^}]*\b(?:arguments|ARGUMENTS|args|arg)\b`).MatchString(content)
}
