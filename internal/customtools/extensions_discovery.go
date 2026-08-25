package customtools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func DiscoverExtensions(options DiscoveryOptions) ([]string, []string, error) {
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
	add(filepath.Join(workspace, ".omp", "extensions"), "native", 100, true)
	add(filepath.Join(native, "extensions"), "native", 100, false)
	for _, path := range options.AdditionalPaths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		add(path, "omp-plugins", 90, true)
	}
	add(filepath.Join(workspace, ".claude", "extensions"), "claude", 80, true)
	add(filepath.Join(home, ".claude", "extensions"), "claude", 80, false)
	add(filepath.Join(workspace, ".codex", "extensions"), "codex", 70, true)
	add(filepath.Join(home, ".codex", "extensions"), "codex", 70, false)
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].priority > roots[j].priority })
	seen := make(map[string]bool)
	var modules, diagnostics []string
	for _, root := range roots {
		paths, err := modulePaths(root.path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				diagnostics = append(diagnostics, fmt.Sprintf("scan extensions %s: %v", root.path, err))
			}
			continue
		}
		for _, path := range paths {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("resolve extension %s: %v", path, err))
				continue
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			modules = append(modules, resolved)
			if len(modules) > 128 {
				return nil, diagnostics, errors.New("extension discovery exceeds 128 modules")
			}
		}
	}
	return modules, diagnostics, nil
}
