package hooks

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type HarnessDiscoveryOptions struct {
	Workspace         string
	HomeDir           string
	NativeAgentDir    string
	TrustProject      bool
	DisabledProviders map[string]bool
}

func DiscoverHarnessScripts(options HarnessDiscoveryOptions) ([]ScriptSource, []Diagnostic) {
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
	type root struct {
		provider string
		path     string
		mode     string
		trusted  bool
	}
	var roots []root
	add := func(provider, path, mode string, trusted bool) {
		if path != "" && !options.DisabledProviders[provider] {
			roots = append(roots, root{provider: provider, path: path, mode: mode, trusted: trusted})
		}
	}
	if options.TrustProject && nonEmptyDirectory(filepath.Join(options.Workspace, ".omp")) {
		add("native", filepath.Join(options.Workspace, ".omp", "hooks"), "typed-dirs", true)
	}
	add("native", filepath.Join(native, "hooks"), "typed-dirs", true)
	add("claude", filepath.Join(home, ".claude", "hooks"), "typed-dirs", true)
	if options.TrustProject {
		add("claude", filepath.Join(options.Workspace, ".claude", "hooks"), "typed-dirs", true)
	}
	add("codex", filepath.Join(home, ".codex", "hooks"), "prefixed", true)
	if options.TrustProject {
		add("codex", filepath.Join(options.Workspace, ".codex", "hooks"), "prefixed", true)
	}
	var scripts []ScriptSource
	var diagnostics []Diagnostic
	for _, candidate := range roots {
		var discovered []ScriptSource
		var err error
		if candidate.mode == "typed-dirs" {
			discovered, err = typedHookScripts(candidate.path, candidate.trusted)
		} else {
			discovered, err = prefixedHookScripts(candidate.path, candidate.trusted)
		}
		if err != nil && !os.IsNotExist(err) {
			diagnostics = append(diagnostics, Diagnostic{Source: candidate.path, Message: err.Error()})
		}
		for _, script := range discovered {
			if len(scripts) >= 512 {
				diagnostics = append(diagnostics, Diagnostic{Source: candidate.path, Message: "hook discovery reached the 512 script limit"})
				return scripts, diagnostics
			}
			scripts = append(scripts, script)
		}
	}
	return scripts, diagnostics
}

func typedHookScripts(root string, trusted bool) ([]ScriptSource, error) {
	var result []ScriptSource
	for _, hookType := range []string{"pre", "post"} {
		entries, err := os.ReadDir(filepath.Join(root, hookType))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			name := entry.Name()
			tool := strings.TrimSuffix(name, filepath.Ext(name))
			result = append(result, ScriptSource{Path: filepath.Join(root, hookType, name), Type: hookType, Tool: tool, Trusted: trusted})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func prefixedHookScripts(root string, trusted bool) ([]ScriptSource, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var result []ScriptSource
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		hookType, tool, found := strings.Cut(base, "-")
		if !found || hookType != "pre" && hookType != "post" || tool == "" {
			continue
		}
		result = append(result, ScriptSource{Path: filepath.Join(root, entry.Name()), Type: hookType, Tool: tool, Trusted: trusted})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func nonEmptyDirectory(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}
