package customtools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type DiscoveryOptions struct {
	Workspace         string
	HomeDir           string
	NativeAgentDir    string
	TrustProject      bool
	DisabledProviders map[string]bool
	AdditionalPaths   []string
}

type moduleRoot struct {
	path, provider string
	priority       int
	project        bool
}

func Discover(options DiscoveryOptions) ([]string, []string, error) {
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
	var roots []moduleRoot
	add := func(path, provider string, priority int, project bool) {
		if path != "" && !options.DisabledProviders[provider] && (!project || options.TrustProject || provider == "omp-plugins") {
			roots = append(roots, moduleRoot{path: path, provider: provider, priority: priority, project: project})
		}
	}
	add(filepath.Join(workspace, ".omp", "tools"), "native", 100, true)
	add(filepath.Join(native, "tools"), "native", 100, false)
	for _, path := range options.AdditionalPaths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		add(path, "omp-plugins", 90, true)
	}
	add(filepath.Join(workspace, ".claude", "tools"), "claude", 80, true)
	add(filepath.Join(home, ".claude", "tools"), "claude", 80, false)
	add(filepath.Join(workspace, ".codex", "tools"), "codex", 70, true)
	add(filepath.Join(home, ".codex", "tools"), "codex", 70, false)
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].priority > roots[j].priority })
	seen := make(map[string]bool)
	var modules, diagnostics []string
	for _, root := range roots {
		paths, err := modulePaths(root.path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				diagnostics = append(diagnostics, fmt.Sprintf("scan custom tools %s: %v", root.path, err))
			}
			continue
		}
		for _, path := range paths {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("resolve custom tool %s: %v", path, err))
				continue
			}
			info, err := os.Stat(resolved)
			if err != nil || !info.Mode().IsRegular() {
				diagnostics = append(diagnostics, fmt.Sprintf("custom tool %s is not a regular file", path))
				continue
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			modules = append(modules, resolved)
			if len(modules) > 128 {
				return nil, diagnostics, errors.New("custom tool discovery exceeds 128 modules")
			}
		}
	}
	return modules, diagnostics, nil
}

func modulePaths(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if executableModule(path) {
			return []string{path}, nil
		}
		return nil, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !executableModule(entry.Name()) {
			continue
		}
		result = append(result, filepath.Join(path, entry.Name()))
	}
	sort.Strings(result)
	return result, nil
}

func executableModule(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".js", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}
