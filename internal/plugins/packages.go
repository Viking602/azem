package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const packageMetadataName = ".azem-plugin.json"

type packageMetadata struct {
	PluginID    string `json:"plugin_id"`
	Marketplace string `json:"marketplace"`
	Version     string `json:"version"`
	Enabled     bool   `json:"enabled"`
	Origin      string `json:"origin"`
	Scope       string `json:"scope,omitempty"`
}

func ensurePackageDirectory(options Options) (string, error) {
	dataDir := strings.TrimSpace(options.DataDir)
	if dataDir == "" {
		if strings.TrimSpace(options.HomeDir) == "" {
			return "", errors.New("plugin discovery requires an Azem data directory")
		}
		dataDir = filepath.Join(options.HomeDir, ".azem")
	}
	root := filepath.Join(dataDir, "plugin-packages")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create Azem plugin directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", fmt.Errorf("protect Azem plugin directory: %w", err)
	}
	return root, nil
}

func syncSelectedCodexPlugins(ctx context.Context, options Options, packageDir string) ([]installedPlugin, []Diagnostic) {
	list := options.ListPlugins
	if list == nil {
		list = listWithCodex
	}
	encoded, err := list(ctx)
	if err != nil || len(bytes.TrimSpace(encoded)) == 0 {
		if len(options.FallbackCatalog) > 0 {
			encoded = options.FallbackCatalog
			err = nil
		}
	}
	if err != nil {
		return importSelectedCodexPlugins(options, packageDir, nil, []Diagnostic{{
			Message: fmt.Sprintf("Codex plugin catalog unavailable: %v", err),
		}})
	}
	var catalog installedCatalog
	if err := json.Unmarshal(encoded, &catalog); err != nil {
		if len(options.FallbackCatalog) > 0 && !bytes.Equal(encoded, options.FallbackCatalog) {
			if fallbackErr := json.Unmarshal(options.FallbackCatalog, &catalog); fallbackErr == nil {
				return importSelectedCodexPlugins(options, packageDir, catalog.Installed, nil)
			}
		}
		return importSelectedCodexPlugins(options, packageDir, nil, []Diagnostic{{
			Message: fmt.Sprintf("decode Codex plugin catalog: %v", err),
		}})
	}
	return importSelectedCodexPlugins(options, packageDir, catalog.Installed, nil)
}

func importSelectedCodexPlugins(options Options, packageDir string, catalog []installedPlugin, diagnostics []Diagnostic) ([]installedPlugin, []Diagnostic) {
	selected := stringSet(options.CodexImports)
	available := make([]installedPlugin, 0, len(catalog)+len(options.CodexImports))
	seen := make(map[string]struct{}, len(catalog)+len(options.CodexImports))
	remember := func(installed installedPlugin) {
		id := firstNonEmpty(installed.PluginID, pluginImportIdentity(installed.Name, installed.Marketplace))
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		if installed.PluginID == "" {
			installed.PluginID = id
		}
		available = append(available, installed)
	}
	for _, installed := range catalog {
		if !installed.Installed {
			continue
		}
		remember(installed)
		if !codexPluginSelected(selected, installed) {
			continue
		}
		if err := importCodexPlugin(options.HomeDir, packageDir, installed); err != nil {
			diagnostics = append(diagnostics, Diagnostic{PluginID: firstNonEmpty(installed.PluginID, installed.Name), Message: err.Error()})
		}
	}
	for _, pluginID := range options.CodexImports {
		pluginID = strings.TrimSpace(pluginID)
		if pluginID == "" {
			continue
		}
		if _, exists := seen[pluginID]; exists {
			continue
		}
		installed := installedPluginFromImportID(pluginID)
		remember(installed)
		if err := importCodexPlugin(options.HomeDir, packageDir, installed); err != nil {
			diagnostics = append(diagnostics, Diagnostic{PluginID: pluginID, Message: err.Error()})
		}
	}
	return available, diagnostics
}

func codexPluginSelected(selected map[string]struct{}, installed installedPlugin) bool {
	if _, ok := selected[strings.TrimSpace(installed.PluginID)]; ok {
		return true
	}
	identity := pluginImportIdentity(installed.Name, installed.Marketplace)
	if identity == "" {
		return false
	}
	_, ok := selected[identity]
	return ok
}

func pluginImportIdentity(name, marketplace string) string {
	name = strings.TrimSpace(name)
	marketplace = strings.TrimSpace(marketplace)
	if name != "" && marketplace != "" {
		return name + "@" + marketplace
	}
	return name
}

func installedPluginFromImportID(pluginID string) installedPlugin {
	name, marketplace, found := strings.Cut(pluginID, "@")
	if !found {
		name = pluginID
		marketplace = ""
	}
	return installedPlugin{
		PluginID: pluginID, Name: name, Marketplace: marketplace,
		Installed: true, Enabled: true,
	}
}

func importCodexPlugin(homeDir, packageDir string, installed installedPlugin) error {
	sourceRoot, err := codexPluginRoot(homeDir, installed)
	if err != nil {
		return err
	}
	marketplace := sanitizeName(firstNonEmpty(installed.Marketplace, "codex"))
	name := sanitizeName(firstNonEmpty(installed.Name, installed.PluginID))
	if marketplace == "" || name == "" {
		return fmt.Errorf("Codex plugin %q has no usable package identity", installed.PluginID)
	}
	destination := filepath.Join(packageDir, "codex", marketplace, name)
	metadata := packageMetadata{PluginID: installed.PluginID, Marketplace: installed.Marketplace, Version: installed.Version, Enabled: installed.Enabled, Origin: "codex"}
	if strings.TrimSpace(installed.Version) == "" {
		var current manifest
		if decodeJSONFile(filepath.Join(destination, ".codex-plugin", "plugin.json"), &current) == nil {
			return nil
		}
	}
	if packageMatchesVersion(destination, installed.Version) {
		return writePackageMetadata(destination, metadata)
	}
	return replacePackageCopy(sourceRoot, destination, metadata)
}

func codexPluginRoot(home string, installed installedPlugin) (string, error) {
	for _, candidate := range codexSourceCandidates(home, installed) {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(resolved, ".codex-plugin", "plugin.json")); err == nil && info.Mode().IsRegular() {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("Codex plugin source is unavailable for %s", firstNonEmpty(installed.PluginID, installed.Name))
}

func codexSourceCandidates(home string, installed installedPlugin) []string {
	candidates := uniqueNonEmpty(installed.Source.Path)
	marketplace := strings.TrimSpace(installed.Marketplace)
	name := strings.TrimSpace(installed.Name)
	version := strings.TrimSpace(installed.Version)
	for _, market := range uniqueNonEmpty(marketplace, sanitizeName(marketplace)) {
		for _, pluginName := range uniqueNonEmpty(name, sanitizeName(name)) {
			if version != "" {
				candidates = append(candidates, filepath.Join(home, ".codex", "plugins", "cache", market, pluginName, version))
			}
			candidates = append(candidates, filepath.Join(home, ".codex", ".tmp", "marketplaces", market, "plugins", pluginName))
			candidates = append(candidates, listCodexCacheVersions(home, market, pluginName)...)
		}
	}
	return uniqueNonEmpty(candidates...)
}

func listCodexCacheVersions(home, marketplace, name string) []string {
	root := filepath.Join(home, ".codex", "plugins", "cache", marketplace, name)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			paths = append(paths, filepath.Join(root, entry.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths
}

func uniqueNonEmpty(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func packageMatchesVersion(root, version string) bool {
	if strings.TrimSpace(version) == "" {
		return false
	}
	var value manifest
	return decodeJSONFile(filepath.Join(root, ".codex-plugin", "plugin.json"), &value) == nil && value.Version == version
}

func replacePackageCopy(sourceRoot, destination string, metadata packageMetadata) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create plugin import parent: %w", err)
	}
	temporary, err := os.MkdirTemp(parent, ".import-*")
	if err != nil {
		return fmt.Errorf("create plugin import staging directory: %w", err)
	}
	defer os.RemoveAll(temporary)
	if err := copyPackageTree(sourceRoot, temporary); err != nil {
		return fmt.Errorf("copy plugin into Azem directory: %w", err)
	}
	if err := writePackageMetadata(temporary, metadata); err != nil {
		return err
	}
	var value manifest
	if err := decodeJSONFile(filepath.Join(temporary, ".codex-plugin", "plugin.json"), &value); err != nil {
		return fmt.Errorf("validate copied plugin manifest: %w", err)
	}
	backup := filepath.Join(parent, "."+filepath.Base(destination)+".previous")
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("clear previous plugin backup: %w", err)
	}
	hadExisting := false
	if _, err := os.Stat(destination); err == nil {
		hadExisting = true
		if err := os.Rename(destination, backup); err != nil {
			return fmt.Errorf("stage previous plugin copy: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		if hadExisting {
			_ = os.Rename(backup, destination)
		}
		return fmt.Errorf("activate imported plugin copy: %w", err)
	}
	if hadExisting {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func writePackageMetadata(root string, metadata packageMetadata) error {
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(root, ".azem-plugin-*.json")
	if err != nil {
		return fmt.Errorf("create plugin metadata: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(root, packageMetadataName)); err != nil {
		return fmt.Errorf("activate plugin metadata: %w", err)
	}
	return nil
}

func copyPackageTree(sourceRoot, destinationRoot string) error {
	resolvedRoot, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return err
	}
	return copyPackageNode(resolvedRoot, destinationRoot, resolvedRoot, map[string]bool{})
}

func copyPackageNode(source, destination, sourceRoot string, visiting map[string]bool) error {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	if !pathWithinRoot(sourceRoot, resolved) {
		return fmt.Errorf("path %q escapes the source plugin", source)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if visiting[resolved] {
			return fmt.Errorf("plugin directory symlink cycle at %q", source)
		}
		visiting[resolved] = true
		defer delete(visiting, resolved)
		if err := os.MkdirAll(destination, 0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(resolved)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == packageMetadataName {
				continue
			}
			if err := copyPackageNode(filepath.Join(resolved, entry.Name()), filepath.Join(destination, entry.Name()), sourceRoot, visiting); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported plugin file type at %q", source)
	}
	input, err := os.Open(resolved)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func installedPackages(packageDir string) ([]installedPlugin, []Diagnostic) {
	roots, err := findPluginRoots(packageDir)
	if err != nil {
		return nil, []Diagnostic{{Path: packageDir, Message: fmt.Sprintf("scan Azem plugin directory: %v", err)}}
	}
	installed := make([]installedPlugin, 0, len(roots))
	var diagnostics []Diagnostic
	for _, root := range roots {
		item, err := installedPackage(root)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Path: root, Message: err.Error()})
			continue
		}
		installed = append(installed, item)
	}
	return installed, diagnostics
}

func findPluginRoots(packageDir string) ([]string, error) {
	var roots []string
	err := filepath.WalkDir(packageDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if path != packageDir && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		manifestPath := filepath.Join(path, ".codex-plugin", "plugin.json")
		if info, err := os.Stat(manifestPath); err == nil && info.Mode().IsRegular() {
			roots = append(roots, path)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(roots)
	return roots, err
}

func installedPackage(root string) (installedPlugin, error) {
	var value manifest
	if err := decodeJSONFile(filepath.Join(root, ".codex-plugin", "plugin.json"), &value); err != nil {
		return installedPlugin{}, err
	}
	if value.Name == "" || value.Version == "" {
		return installedPlugin{}, errors.New("plugin manifest requires name and version")
	}
	metadata := packageMetadata{PluginID: value.Name + "@local", Marketplace: "local", Version: value.Version, Enabled: true, Origin: "local"}
	metadataPath := filepath.Join(root, packageMetadataName)
	if _, err := os.Stat(metadataPath); err == nil {
		if err := decodeJSONFile(metadataPath, &metadata); err != nil {
			return installedPlugin{}, fmt.Errorf("decode Azem plugin metadata: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return installedPlugin{}, err
	}
	metadata.PluginID = firstNonEmpty(metadata.PluginID, value.Name+"@local")
	metadata.Marketplace = firstNonEmpty(metadata.Marketplace, "local")
	metadata.Version = firstNonEmpty(metadata.Version, value.Version)
	metadata.Origin = firstNonEmpty(metadata.Origin, "local")
	return installedPlugin{
		PluginID: metadata.PluginID, Name: value.Name, Marketplace: metadata.Marketplace, Version: metadata.Version,
		Installed: true, Enabled: metadata.Enabled, Origin: metadata.Origin, Scope: metadata.Scope, Source: pluginSource{Path: root},
	}, nil
}
