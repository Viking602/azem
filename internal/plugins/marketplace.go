package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/netproxy"
	"golang.org/x/mod/semver"
)

const (
	marketplaceRegistryVersion = 1
	maxMarketplaceBytes        = 1 << 20
)

var marketplaceNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,62}[a-z0-9])?$`)

type MarketplaceScope string

const (
	MarketplaceScopeUser    MarketplaceScope = "user"
	MarketplaceScopeProject MarketplaceScope = "project"
)

type MarketplaceManagerOptions struct {
	DataDir      string
	WorkspaceDir string
	HTTPClient   *http.Client
}

type MarketplaceManager struct {
	options   MarketplaceManagerOptions
	operation sync.Mutex
}

type MarketplaceRecord struct {
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	Type      string    `json:"type"`
	CachePath string    `json:"cachePath"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type MarketplaceCatalog struct {
	Name  string `json:"name"`
	Owner struct {
		Name  string `json:"name"`
		Email string `json:"email,omitempty"`
	} `json:"owner"`
	Metadata struct {
		Description string `json:"description,omitempty"`
		Version     string `json:"version,omitempty"`
		PluginRoot  string `json:"pluginRoot,omitempty"`
	} `json:"metadata,omitempty"`
	Plugins []MarketplacePlugin `json:"plugins"`
}

type MarketplacePlugin struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Version     string          `json:"version,omitempty"`
	Source      json.RawMessage `json:"source"`
	Category    string          `json:"category,omitempty"`
	Homepage    string          `json:"homepage,omitempty"`
	License     string          `json:"license,omitempty"`
	Keywords    []string        `json:"keywords,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
}

type MarketplacePluginView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Marketplace string   `json:"marketplace"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type MarketplaceInstalledPlugin struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Marketplace string           `json:"marketplace"`
	Version     string           `json:"version"`
	Scope       MarketplaceScope `json:"scope"`
	Enabled     bool             `json:"enabled"`
	Path        string           `json:"path"`
}

type MarketplaceUpgrade struct {
	Plugin  MarketplaceInstalledPlugin `json:"plugin"`
	Current string                     `json:"current"`
	Latest  string                     `json:"latest"`
}

type marketplaceRegistry struct {
	Version      int                 `json:"version"`
	Marketplaces []MarketplaceRecord `json:"marketplaces"`
}

type marketplaceSourceObject struct {
	Source  string `json:"source"`
	URL     string `json:"url"`
	Repo    string `json:"repo"`
	Path    string `json:"path"`
	Ref     string `json:"ref"`
	SHA     string `json:"sha"`
	Package string `json:"package"`
	Version string `json:"version"`
}

func NewMarketplaceManager(options MarketplaceManagerOptions) (*MarketplaceManager, error) {
	if strings.TrimSpace(options.DataDir) == "" {
		return nil, errors.New("marketplace requires an Azem data directory")
	}
	options.DataDir = filepath.Clean(options.DataDir)
	if options.WorkspaceDir != "" {
		options.WorkspaceDir = filepath.Clean(options.WorkspaceDir)
	}
	if err := os.MkdirAll(filepath.Join(options.DataDir, "plugin-marketplaces"), 0o700); err != nil {
		return nil, err
	}
	return &MarketplaceManager{options: options}, nil
}

func (manager *MarketplaceManager) Add(ctx context.Context, source string) (MarketplaceRecord, error) {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.add(ctx, source)
}

func (manager *MarketplaceManager) add(ctx context.Context, source string) (MarketplaceRecord, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return MarketplaceRecord{}, errors.New("marketplace source is empty")
	}
	sourceType, normalized, err := manager.classifySource(source)
	if err != nil {
		return MarketplaceRecord{}, err
	}
	stagedPath, catalog, cleanup, err := manager.stageMarketplace(ctx, sourceType, normalized, "")
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return MarketplaceRecord{}, err
	}
	if err := validateMarketplaceCatalog(catalog); err != nil {
		return MarketplaceRecord{}, err
	}
	registry, err := manager.loadRegistry()
	if err != nil {
		return MarketplaceRecord{}, err
	}
	for _, existing := range registry.Marketplaces {
		if existing.Name == catalog.Name {
			return MarketplaceRecord{}, fmt.Errorf("marketplace %q already exists", catalog.Name)
		}
	}
	record := MarketplaceRecord{Name: catalog.Name, Source: normalized, Type: sourceType, UpdatedAt: time.Now().UTC()}
	if sourceType == "local" {
		record.CachePath = stagedPath
	} else {
		destination := manager.marketplaceCachePath(catalog.Name)
		if err := activateMarketplaceCache(stagedPath, destination); err != nil {
			return MarketplaceRecord{}, err
		}
		record.CachePath = destination
	}
	registry.Marketplaces = append(registry.Marketplaces, record)
	sort.Slice(registry.Marketplaces, func(i, j int) bool { return registry.Marketplaces[i].Name < registry.Marketplaces[j].Name })
	if err := manager.saveRegistry(registry); err != nil {
		if sourceType != "local" {
			_ = os.RemoveAll(record.CachePath)
		}
		return MarketplaceRecord{}, err
	}
	return record, nil
}

func (manager *MarketplaceManager) Remove(name string) error {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.remove(name)
}

func (manager *MarketplaceManager) remove(name string) error {
	name = strings.TrimSpace(name)
	registry, err := manager.loadRegistry()
	if err != nil {
		return err
	}
	index := -1
	var record MarketplaceRecord
	for current, item := range registry.Marketplaces {
		if item.Name == name {
			index, record = current, item
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("marketplace %q not found", name)
	}
	registry.Marketplaces = append(registry.Marketplaces[:index], registry.Marketplaces[index+1:]...)
	if err := manager.saveRegistry(registry); err != nil {
		return err
	}
	if record.Type != "local" && pathWithinRoot(filepath.Join(manager.options.DataDir, "plugin-marketplaces"), record.CachePath) {
		return os.RemoveAll(record.CachePath)
	}
	return nil
}

func (manager *MarketplaceManager) Update(ctx context.Context, name string) ([]MarketplaceRecord, error) {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.update(ctx, name)
}

func (manager *MarketplaceManager) update(ctx context.Context, name string) ([]MarketplaceRecord, error) {
	registry, err := manager.loadRegistry()
	if err != nil {
		return nil, err
	}
	var updated []MarketplaceRecord
	for index := range registry.Marketplaces {
		record := registry.Marketplaces[index]
		if name != "" && record.Name != name {
			continue
		}
		stagedPath, catalog, cleanup, updateErr := manager.stageMarketplace(ctx, record.Type, record.Source, record.Name)
		if cleanup != nil {
			defer cleanup()
		}
		if updateErr != nil {
			return updated, fmt.Errorf("update marketplace %s: %w", record.Name, updateErr)
		}
		if validateErr := validateMarketplaceCatalog(catalog); validateErr != nil {
			return updated, validateErr
		}
		if catalog.Name != record.Name {
			return updated, fmt.Errorf("marketplace %q changed identity to %q", record.Name, catalog.Name)
		}
		if record.Type != "local" {
			if err := activateMarketplaceCache(stagedPath, record.CachePath); err != nil {
				return updated, err
			}
		} else {
			record.CachePath = stagedPath
		}
		record.UpdatedAt = time.Now().UTC()
		registry.Marketplaces[index] = record
		updated = append(updated, record)
	}
	if name != "" && len(updated) == 0 {
		return nil, fmt.Errorf("marketplace %q not found", name)
	}
	if err := manager.saveRegistry(registry); err != nil {
		return nil, err
	}
	return updated, nil
}

func (manager *MarketplaceManager) List() ([]MarketplaceRecord, error) {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.list()
}

func (manager *MarketplaceManager) list() ([]MarketplaceRecord, error) {
	registry, err := manager.loadRegistry()
	if err != nil {
		return nil, err
	}
	return append([]MarketplaceRecord(nil), registry.Marketplaces...), nil
}

func (manager *MarketplaceManager) Discover(marketplace string) ([]MarketplacePluginView, error) {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.discover(marketplace)
}

func (manager *MarketplaceManager) discover(marketplace string) ([]MarketplacePluginView, error) {
	records, err := manager.list()
	if err != nil {
		return nil, err
	}
	var result []MarketplacePluginView
	for _, record := range records {
		if marketplace != "" && record.Name != marketplace {
			continue
		}
		catalog, err := loadMarketplaceCatalog(record.CachePath, record.Type == "direct")
		if err != nil {
			return nil, fmt.Errorf("load marketplace %s: %w", record.Name, err)
		}
		for _, plugin := range catalog.Plugins {
			if !validMarketplaceName(plugin.Name) {
				continue
			}
			result = append(result, MarketplacePluginView{
				ID: plugin.Name + "@" + record.Name, Name: plugin.Name, Marketplace: record.Name,
				Version: firstNonEmpty(plugin.Version, "0.0.0"), Description: plugin.Description,
				Category: plugin.Category, Homepage: plugin.Homepage, License: plugin.License,
				Keywords: append([]string(nil), plugin.Keywords...), Tags: append([]string(nil), plugin.Tags...),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (manager *MarketplaceManager) Install(ctx context.Context, id string, scope MarketplaceScope, force bool) (MarketplaceInstalledPlugin, error) {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.install(ctx, id, scope, force)
}

func (manager *MarketplaceManager) install(ctx context.Context, id string, scope MarketplaceScope, force bool) (MarketplaceInstalledPlugin, error) {
	name, marketplace, err := parseMarketplacePluginID(id)
	if err != nil {
		return MarketplaceInstalledPlugin{}, err
	}
	if err := manager.validateScope(scope); err != nil {
		return MarketplaceInstalledPlugin{}, err
	}
	record, catalog, plugin, err := manager.findPlugin(name, marketplace)
	if err != nil {
		return MarketplaceInstalledPlugin{}, err
	}
	version := firstNonEmpty(plugin.Version, "0.0.0")
	destination := manager.installedPath(scope, marketplace, name)
	if !force && packageMatchesVersion(destination, version) {
		return installedMarketplacePlugin(destination)
	}
	sourceRoot, cleanup, err := manager.resolvePluginSource(ctx, record, catalog, plugin)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return MarketplaceInstalledPlugin{}, err
	}
	adaptedRoot, adaptedCleanup, err := adaptMarketplacePlugin(sourceRoot, plugin, version)
	if adaptedCleanup != nil {
		defer adaptedCleanup()
	}
	if err != nil {
		return MarketplaceInstalledPlugin{}, err
	}
	enabled := true
	var previous packageMetadata
	if err := decodeJSONFile(filepath.Join(destination, packageMetadataName), &previous); err == nil {
		enabled = previous.Enabled
	}
	metadata := packageMetadata{PluginID: id, Marketplace: marketplace, Version: version, Enabled: enabled, Origin: "marketplace", Scope: string(scope)}
	if err := replacePackageCopy(adaptedRoot, destination, metadata); err != nil {
		return MarketplaceInstalledPlugin{}, err
	}
	return installedMarketplacePlugin(destination)
}

func (manager *MarketplaceManager) Uninstall(id string, scope MarketplaceScope) error {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.uninstall(id, scope)
}

func (manager *MarketplaceManager) uninstall(id string, scope MarketplaceScope) error {
	name, marketplace, err := parseMarketplacePluginID(id)
	if err != nil {
		return err
	}
	if err := manager.validateScope(scope); err != nil {
		return err
	}
	destination := manager.installedPath(scope, marketplace, name)
	root := manager.scopePackageRoot(scope)
	if !pathWithinRoot(root, destination) {
		return errors.New("marketplace plugin path escapes its scope root")
	}
	return os.RemoveAll(destination)
}

func (manager *MarketplaceManager) SetEnabled(id string, scope MarketplaceScope, enabled bool) error {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.setEnabled(id, scope, enabled)
}

func (manager *MarketplaceManager) setEnabled(id string, scope MarketplaceScope, enabled bool) error {
	name, marketplace, err := parseMarketplacePluginID(id)
	if err != nil {
		return err
	}
	if err := manager.validateScope(scope); err != nil {
		return err
	}
	destination := manager.installedPath(scope, marketplace, name)
	var metadata packageMetadata
	if err := decodeJSONFile(filepath.Join(destination, packageMetadataName), &metadata); err != nil {
		return err
	}
	metadata.Enabled = enabled
	return writePackageMetadata(destination, metadata)
}

func (manager *MarketplaceManager) Installed() ([]MarketplaceInstalledPlugin, error) {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.installed()
}

func (manager *MarketplaceManager) installed() ([]MarketplaceInstalledPlugin, error) {
	var result []MarketplaceInstalledPlugin
	for _, scope := range []MarketplaceScope{MarketplaceScopeUser, MarketplaceScopeProject} {
		if scope == MarketplaceScopeProject && manager.options.WorkspaceDir == "" {
			continue
		}
		root := manager.scopePackageRoot(scope)
		if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		packages, diagnostics := installedPackages(root)
		if len(diagnostics) > 0 {
			return nil, errors.New(diagnostics[0].Message)
		}
		for _, plugin := range packages {
			if plugin.Origin != "marketplace" {
				continue
			}
			installed, err := installedMarketplacePlugin(plugin.Source.Path)
			if err != nil {
				return nil, err
			}
			result = append(result, installed)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID != result[j].ID {
			return result[i].ID < result[j].ID
		}
		return result[i].Scope < result[j].Scope
	})
	return result, nil
}

func (manager *MarketplaceManager) AvailableUpgrades() ([]MarketplaceUpgrade, error) {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.availableUpgrades()
}

func (manager *MarketplaceManager) availableUpgrades() ([]MarketplaceUpgrade, error) {
	installed, err := manager.installed()
	if err != nil {
		return nil, err
	}
	var result []MarketplaceUpgrade
	for _, current := range installed {
		_, _, plugin, err := manager.findPlugin(current.Name, current.Marketplace)
		if err != nil || !marketplaceVersionUpgrade(current.Version, plugin.Version) {
			continue
		}
		result = append(result, MarketplaceUpgrade{Plugin: current, Current: current.Version, Latest: plugin.Version})
	}
	return result, nil
}

func (manager *MarketplaceManager) Upgrade(ctx context.Context, id string, scope MarketplaceScope) ([]MarketplaceInstalledPlugin, error) {
	manager.operation.Lock()
	defer manager.operation.Unlock()
	return manager.upgrade(ctx, id, scope)
}

func (manager *MarketplaceManager) upgrade(ctx context.Context, id string, scope MarketplaceScope) ([]MarketplaceInstalledPlugin, error) {
	installed, err := manager.installed()
	if err != nil {
		return nil, err
	}
	var upgraded []MarketplaceInstalledPlugin
	var upgradeErr error
	for _, current := range installed {
		if id != "" && current.ID != id || scope != "" && current.Scope != scope {
			continue
		}
		_, _, latest, findErr := manager.findPlugin(current.Name, current.Marketplace)
		if findErr != nil {
			if id != "" {
				return upgraded, findErr
			}
			upgradeErr = errors.Join(upgradeErr, findErr)
			continue
		}
		if !marketplaceVersionUpgrade(current.Version, latest.Version) {
			continue
		}
		next, installErr := manager.install(ctx, current.ID, current.Scope, true)
		if installErr != nil {
			if id != "" {
				return upgraded, installErr
			}
			upgradeErr = errors.Join(upgradeErr, installErr)
			continue
		}
		upgraded = append(upgraded, next)
	}
	return upgraded, upgradeErr
}

func (manager *MarketplaceManager) findPlugin(name, marketplace string) (MarketplaceRecord, MarketplaceCatalog, MarketplacePlugin, error) {
	records, err := manager.list()
	if err != nil {
		return MarketplaceRecord{}, MarketplaceCatalog{}, MarketplacePlugin{}, err
	}
	for _, record := range records {
		if record.Name != marketplace {
			continue
		}
		catalog, err := loadMarketplaceCatalog(record.CachePath, record.Type == "direct")
		if err != nil {
			return record, catalog, MarketplacePlugin{}, err
		}
		for _, plugin := range catalog.Plugins {
			if plugin.Name == name {
				return record, catalog, plugin, nil
			}
		}
		return record, catalog, MarketplacePlugin{}, fmt.Errorf("plugin %q not found in marketplace %q", name, marketplace)
	}
	return MarketplaceRecord{}, MarketplaceCatalog{}, MarketplacePlugin{}, fmt.Errorf("marketplace %q not found", marketplace)
}

func (manager *MarketplaceManager) resolvePluginSource(ctx context.Context, record MarketplaceRecord, catalog MarketplaceCatalog, plugin MarketplacePlugin) (string, func(), error) {
	var relative string
	if err := json.Unmarshal(plugin.Source, &relative); err == nil {
		if !strings.HasPrefix(relative, "./") {
			return "", nil, errors.New("relative marketplace plugin sources must begin with ./")
		}
		if record.Type == "direct" {
			return "", nil, errors.New("direct catalog plugins cannot use relative sources")
		}
		root := record.CachePath
		candidate := filepath.Join(root, filepath.FromSlash(catalog.Metadata.PluginRoot), filepath.FromSlash(strings.TrimPrefix(relative, "./")))
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", nil, err
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", nil, err
		}
		if !pathWithinRoot(resolvedRoot, resolved) {
			return "", nil, errors.New("relative marketplace plugin source escapes the marketplace")
		}
		return resolved, nil, nil
	}
	var source marketplaceSourceObject
	if err := json.Unmarshal(plugin.Source, &source); err != nil {
		return "", nil, fmt.Errorf("decode plugin source: %w", err)
	}
	if source.Source == "npm" {
		return "", nil, errors.New("npm plugin sources are not supported")
	}
	gitURL := source.URL
	if source.Source == "github" {
		gitURL = "https://github.com/" + strings.TrimSuffix(source.Repo, ".git") + ".git"
	}
	if gitURL == "" {
		return "", nil, errors.New("plugin source URL is empty")
	}
	temporary, err := os.MkdirTemp(filepath.Join(manager.options.DataDir, "plugin-marketplaces"), ".plugin-source-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(temporary) }
	if err := cloneGit(ctx, gitURL, source.Ref, temporary); err != nil {
		cleanup()
		return "", nil, err
	}
	if source.SHA != "" {
		command := exec.CommandContext(ctx, "git", "-C", temporary, "rev-parse", "HEAD")
		output, verifyErr := command.Output()
		if verifyErr != nil || !strings.HasPrefix(strings.TrimSpace(string(output)), strings.ToLower(strings.TrimSpace(source.SHA))) {
			cleanup()
			return "", nil, errors.New("plugin source SHA does not match the catalog")
		}
	}
	root := temporary
	if source.Path != "" {
		resolvedClone, cloneErr := filepath.EvalSymlinks(temporary)
		candidate := filepath.Join(temporary, filepath.FromSlash(source.Path))
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if cloneErr != nil || resolveErr != nil || !pathWithinRoot(resolvedClone, resolved) {
			cleanup()
			return "", nil, errors.New("git-subdir plugin path escapes the cloned repository")
		}
		root = resolved
	}
	return root, cleanup, nil
}

func adaptMarketplacePlugin(sourceRoot string, plugin MarketplacePlugin, version string) (string, func(), error) {
	sourceManifest := filepath.Join(sourceRoot, ".codex-plugin", "plugin.json")
	if info, err := os.Stat(sourceManifest); err != nil || !info.Mode().IsRegular() {
		sourceManifest = filepath.Join(sourceRoot, ".omp-plugin", "plugin.json")
		if info, err := os.Stat(sourceManifest); err != nil || !info.Mode().IsRegular() {
			sourceManifest = filepath.Join(sourceRoot, ".claude-plugin", "plugin.json")
			if info, err := os.Stat(sourceManifest); err != nil || !info.Mode().IsRegular() {
				return "", nil, errors.New("marketplace plugin requires .omp-plugin, .claude-plugin, or .codex-plugin/plugin.json")
			}
		}
	}
	temporary, err := os.MkdirTemp("", "azem-marketplace-plugin-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(temporary) }
	if err := copyPackageTree(sourceRoot, temporary); err != nil {
		cleanup()
		return "", nil, err
	}
	var descriptor map[string]any
	if err := decodeJSONFile(filepath.Join(temporary, filepath.Base(filepath.Dir(sourceManifest)), "plugin.json"), &descriptor); err != nil {
		cleanup()
		return "", nil, err
	}
	descriptor["name"] = firstNonEmpty(stringValueFromAny(descriptor["name"]), plugin.Name)
	descriptor["version"] = firstNonEmpty(version, stringValueFromAny(descriptor["version"]), "0.0.0")
	descriptor["description"] = firstNonEmpty(stringValueFromAny(descriptor["description"]), plugin.Description, plugin.Name)
	codexDir := filepath.Join(temporary, ".codex-plugin")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	encoded, _ := json.MarshalIndent(descriptor, "", "  ")
	if err := os.WriteFile(filepath.Join(codexDir, "plugin.json"), encoded, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return temporary, cleanup, nil
}

func (manager *MarketplaceManager) stageMarketplace(ctx context.Context, sourceType, source, name string) (string, MarketplaceCatalog, func(), error) {
	if sourceType == "local" {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return "", MarketplaceCatalog{}, nil, err
		}
		catalog, err := loadMarketplaceCatalog(resolved, false)
		return resolved, catalog, nil, err
	}
	temporary, err := os.MkdirTemp(filepath.Join(manager.options.DataDir, "plugin-marketplaces"), ".marketplace-*")
	if err != nil {
		return "", MarketplaceCatalog{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(temporary) }
	if sourceType == "direct" {
		payload, err := manager.fetchCatalog(ctx, source)
		if err != nil {
			cleanup()
			return "", MarketplaceCatalog{}, nil, err
		}
		if err := os.WriteFile(filepath.Join(temporary, "marketplace.json"), payload, 0o600); err != nil {
			cleanup()
			return "", MarketplaceCatalog{}, nil, err
		}
	} else if err := cloneGit(ctx, source, "", temporary); err != nil {
		cleanup()
		return "", MarketplaceCatalog{}, nil, err
	}
	catalog, err := loadMarketplaceCatalog(temporary, sourceType == "direct")
	if err != nil {
		cleanup()
		return "", MarketplaceCatalog{}, nil, err
	}
	return temporary, catalog, cleanup, nil
}

func (manager *MarketplaceManager) classifySource(source string) (string, string, error) {
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "~/") || filepath.IsAbs(source) {
		if strings.HasPrefix(source, "~/") {
			home, _ := os.UserHomeDir()
			source = filepath.Join(home, source[2:])
		} else if !filepath.IsAbs(source) {
			source = filepath.Join(manager.options.WorkspaceDir, source)
		}
		absolute, err := filepath.Abs(source)
		return "local", absolute, err
	}
	if matched, _ := regexp.MatchString(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`, source); matched {
		return "git", "https://github.com/" + strings.TrimSuffix(source, ".git") + ".git", nil
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" {
		return "", "", errors.New("unsupported marketplace source")
	}
	if (parsed.Scheme == "https" || parsed.Scheme == "http") && strings.HasSuffix(strings.ToLower(parsed.Path), ".json") {
		return "direct", source, nil
	}
	if parsed.Scheme == "https" || parsed.Scheme == "http" || parsed.Scheme == "ssh" || strings.HasPrefix(source, "git@") {
		return "git", source, nil
	}
	return "", "", errors.New("unsupported marketplace source")
}

func (manager *MarketplaceManager) fetchCatalog(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	client := manager.options.HTTPClient
	if client == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			base = &http.Transport{}
		}
		transport := base.Clone()
		netproxy.ConfigureTransport(transport)
		client = &http.Client{Transport: transport, Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxMarketplaceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxMarketplaceBytes {
		return nil, errors.New("marketplace catalog exceeds 1 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("marketplace catalog HTTP %d", response.StatusCode)
	}
	return payload, nil
}

func cloneGit(ctx context.Context, source, ref, destination string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", source, destination)
	command := exec.CommandContext(ctx, "git", args...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone marketplace: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func loadMarketplaceCatalog(root string, direct bool) (MarketplaceCatalog, error) {
	paths := []string{filepath.Join(root, ".omp-plugin", "marketplace.json"), filepath.Join(root, ".claude-plugin", "marketplace.json")}
	if direct {
		paths = []string{filepath.Join(root, "marketplace.json")}
	}
	var lastErr error
	for _, path := range paths {
		var catalog MarketplaceCatalog
		if err := decodeJSONFile(path, &catalog); err == nil {
			return catalog, nil
		} else {
			lastErr = err
		}
	}
	return MarketplaceCatalog{}, lastErr
}

func validateMarketplaceCatalog(catalog MarketplaceCatalog) error {
	if !validMarketplaceName(catalog.Name) {
		return fmt.Errorf("invalid marketplace name %q", catalog.Name)
	}
	if strings.TrimSpace(catalog.Owner.Name) == "" {
		return errors.New("marketplace owner.name is required")
	}
	if len(catalog.Plugins) > 5000 {
		return errors.New("marketplace contains more than 5000 plugins")
	}
	return nil
}

func validMarketplaceName(name string) bool {
	return len(name) <= 64 && marketplaceNamePattern.MatchString(name)
}

func parseMarketplacePluginID(id string) (string, string, error) {
	name, marketplace, found := strings.Cut(strings.TrimSpace(id), "@")
	if !found || !validMarketplaceName(name) || !validMarketplaceName(marketplace) || len(id) > 128 {
		return "", "", fmt.Errorf("invalid marketplace plugin id %q", id)
	}
	return name, marketplace, nil
}

func activateMarketplaceCache(staged, destination string) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	backup := destination + ".previous"
	_ = os.RemoveAll(backup)
	hadExisting := false
	if _, err := os.Stat(destination); err == nil {
		hadExisting = true
		if err := os.Rename(destination, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, destination); err != nil {
		if hadExisting {
			_ = os.Rename(backup, destination)
		}
		return err
	}
	if hadExisting {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func (manager *MarketplaceManager) loadRegistry() (marketplaceRegistry, error) {
	path := manager.registryPath()
	var registry marketplaceRegistry
	if err := decodeJSONFile(path, &registry); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return marketplaceRegistry{Version: marketplaceRegistryVersion}, nil
		}
		return marketplaceRegistry{}, err
	}
	if registry.Version != marketplaceRegistryVersion {
		return marketplaceRegistry{}, fmt.Errorf("unsupported marketplace registry version %d", registry.Version)
	}
	return registry, nil
}

func (manager *MarketplaceManager) saveRegistry(registry marketplaceRegistry) error {
	registry.Version = marketplaceRegistryVersion
	encoded, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	path := manager.registryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".marketplaces-*.json")
	if err != nil {
		return err
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
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (manager *MarketplaceManager) registryPath() string {
	return filepath.Join(manager.options.DataDir, "marketplaces.json")
}

func (manager *MarketplaceManager) marketplaceCachePath(name string) string {
	return filepath.Join(manager.options.DataDir, "plugin-marketplaces", "catalogs", name)
}

func (manager *MarketplaceManager) scopePackageRoot(scope MarketplaceScope) string {
	if scope == MarketplaceScopeProject {
		return filepath.Join(manager.options.WorkspaceDir, ".azem", "plugin-packages", "marketplace")
	}
	return filepath.Join(manager.options.DataDir, "plugin-packages", "marketplace")
}

func (manager *MarketplaceManager) installedPath(scope MarketplaceScope, marketplace, name string) string {
	return filepath.Join(manager.scopePackageRoot(scope), marketplace, name)
}

func (manager *MarketplaceManager) validateScope(scope MarketplaceScope) error {
	if scope != MarketplaceScopeUser && scope != MarketplaceScopeProject {
		return fmt.Errorf("invalid marketplace scope %q", scope)
	}
	if scope == MarketplaceScopeProject && manager.options.WorkspaceDir == "" {
		return errors.New("project marketplace scope requires a workspace")
	}
	return nil
}

func installedMarketplacePlugin(root string) (MarketplaceInstalledPlugin, error) {
	var metadata packageMetadata
	if err := decodeJSONFile(filepath.Join(root, packageMetadataName), &metadata); err != nil {
		return MarketplaceInstalledPlugin{}, err
	}
	name, marketplace, err := parseMarketplacePluginID(metadata.PluginID)
	if err != nil {
		return MarketplaceInstalledPlugin{}, err
	}
	return MarketplaceInstalledPlugin{
		ID: metadata.PluginID, Name: name, Marketplace: marketplace, Version: metadata.Version,
		Scope: MarketplaceScope(metadata.Scope), Enabled: metadata.Enabled, Path: root,
	}, nil
}

func marketplaceVersionUpgrade(current, latest string) bool {
	current, latest = strings.TrimSpace(current), strings.TrimSpace(latest)
	if current == "" || latest == "" || current == latest {
		return false
	}
	currentSemver, latestSemver := current, latest
	if !strings.HasPrefix(currentSemver, "v") {
		currentSemver = "v" + currentSemver
	}
	if !strings.HasPrefix(latestSemver, "v") {
		latestSemver = "v" + latestSemver
	}
	if semver.IsValid(currentSemver) && semver.IsValid(latestSemver) {
		return semver.Compare(latestSemver, currentSemver) > 0
	}
	return true
}

func stringValueFromAny(value any) string {
	text, _ := value.(string)
	return text
}
