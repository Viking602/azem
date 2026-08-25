package extensions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxExtensionDescriptorBytes = 1 << 20

var extensionNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type DiscoveryOptions struct {
	Workspace      string
	HomeDir        string
	NativeAgentDir string
	AgentDirs      []string
	ThemeDirs      []string
}

type Agent struct {
	Name           string
	Description    string
	SystemPrompt   string
	Models         []string
	Thinking       string
	Tools          []string
	Isolation      string
	Blocking       bool
	AutoloadSkills []string
	Source         string
}

type Theme struct {
	Name    string         `json:"name"`
	Path    string         `json:"path"`
	Vars    map[string]any `json:"vars,omitempty"`
	Colors  map[string]any `json:"colors"`
	Symbols map[string]any `json:"symbols,omitempty"`
	Source  string         `json:"source"`
	Dark    *bool          `json:"dark,omitempty"`
}

type agentFrontmatter struct {
	Name           string   `yaml:"name"`
	Description    string   `yaml:"description"`
	Model          any      `yaml:"model"`
	ThinkingLevel  string   `yaml:"thinking-level"`
	Thinking       string   `yaml:"thinking"`
	Tools          any      `yaml:"tools"`
	Isolation      string   `yaml:"isolation"`
	Blocking       bool     `yaml:"blocking"`
	AutoloadSkills []string `yaml:"autoloadSkills"`
}

type sourceRoot struct {
	path, source string
}

func Discover(options DiscoveryOptions) ([]Agent, []Theme, []string, error) {
	workspace, err := filepath.Abs(options.Workspace)
	if err != nil {
		return nil, nil, nil, err
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
	agentRoots := []sourceRoot{{filepath.Join(workspace, ".omp", "agents"), "project"}, {filepath.Join(native, "agents"), "user"}}
	for _, root := range options.AgentDirs {
		if !filepath.IsAbs(root) {
			root = filepath.Join(workspace, root)
		}
		agentRoots = append(agentRoots, sourceRoot{root, "extension"})
	}
	themeRoots := []sourceRoot{{filepath.Join(workspace, ".omp", "themes"), "project"}, {filepath.Join(native, "themes"), "user"}}
	for _, root := range options.ThemeDirs {
		if !filepath.IsAbs(root) {
			root = filepath.Join(workspace, root)
		}
		themeRoots = append(themeRoots, sourceRoot{root, "extension"})
	}
	agents, agentDiagnostics := discoverAgents(agentRoots)
	themes, themeDiagnostics := discoverThemes(themeRoots)
	return agents, themes, append(agentDiagnostics, themeDiagnostics...), nil
}

func discoverAgents(roots []sourceRoot) ([]Agent, []string) {
	seen := make(map[string]bool)
	var result []Agent
	var diagnostics []string
	for _, root := range roots {
		paths, err := descriptorFiles(root.path, ".md")
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				diagnostics = append(diagnostics, fmt.Sprintf("scan agents %s: %v", root.path, err))
			}
			continue
		}
		for _, path := range paths {
			payload, err := readDescriptor(path)
			if err != nil {
				diagnostics = append(diagnostics, err.Error())
				continue
			}
			frontmatter, body, err := splitMarkdown(payload)
			if err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("agent %s: %v", path, err))
				continue
			}
			var metadata agentFrontmatter
			if err := yaml.Unmarshal(frontmatter, &metadata); err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("agent %s: %v", path, err))
				continue
			}
			if metadata.Name == "" {
				metadata.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			}
			if !extensionNamePattern.MatchString(metadata.Name) || strings.TrimSpace(metadata.Description) == "" || strings.TrimSpace(string(body)) == "" {
				diagnostics = append(diagnostics, fmt.Sprintf("agent %s requires valid name, description, and system prompt", path))
				continue
			}
			if seen[metadata.Name] {
				continue
			}
			seen[metadata.Name] = true
			thinking := first(metadata.ThinkingLevel, metadata.Thinking)
			result = append(result, Agent{
				Name: metadata.Name, Description: strings.TrimSpace(metadata.Description), SystemPrompt: strings.TrimSpace(string(body)),
				Models: stringList(metadata.Model), Thinking: thinking, Tools: stringList(metadata.Tools), Isolation: metadata.Isolation,
				Blocking: metadata.Blocking, AutoloadSkills: append([]string(nil), metadata.AutoloadSkills...), Source: path,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, diagnostics
}

func discoverThemes(roots []sourceRoot) ([]Theme, []string) {
	seen := make(map[string]bool)
	var result []Theme
	var diagnostics []string
	for _, root := range roots {
		paths, err := descriptorFiles(root.path, ".json")
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				diagnostics = append(diagnostics, fmt.Sprintf("scan themes %s: %v", root.path, err))
			}
			continue
		}
		for _, path := range paths {
			payload, err := readDescriptor(path)
			if err != nil {
				diagnostics = append(diagnostics, err.Error())
				continue
			}
			var theme Theme
			decoder := json.NewDecoder(bytes.NewReader(payload))
			decoder.UseNumber()
			if err := decoder.Decode(&theme); err != nil || !extensionNamePattern.MatchString(theme.Name) || len(theme.Colors) == 0 {
				diagnostics = append(diagnostics, fmt.Sprintf("theme %s requires valid name and colors", path))
				continue
			}
			if seen[theme.Name] {
				continue
			}
			seen[theme.Name] = true
			theme.Path, theme.Source = path, root.source
			result = append(result, theme)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, diagnostics
}

func descriptorFiles(directory, suffix string) ([]string, error) {
	if info, statErr := os.Stat(directory); statErr == nil && info.Mode().IsRegular() {
		if strings.HasSuffix(strings.ToLower(directory), suffix) {
			return []string{directory}, nil
		}
		return nil, nil
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(strings.ToLower(entry.Name()), suffix) {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func readDescriptor(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxExtensionDescriptorBytes {
		return nil, fmt.Errorf("extension descriptor %s must be a regular file no larger than 1 MiB", path)
	}
	return os.ReadFile(path)
}

func splitMarkdown(payload []byte) ([]byte, []byte, error) {
	normalized := bytes.ReplaceAll(payload, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		return nil, nil, errors.New("missing YAML frontmatter")
	}
	rest := normalized[4:]
	index := bytes.Index(rest, []byte("\n---\n"))
	if index < 0 {
		return nil, nil, errors.New("unterminated YAML frontmatter")
	}
	return rest[:index], rest[index+5:], nil
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case string:
		var result []string
		for _, item := range strings.Split(typed, ",") {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
		return result
	case []any:
		var result []string
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				result = append(result, strings.TrimSpace(value))
			}
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	default:
		return nil
	}
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
