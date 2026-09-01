package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// HomeEnv overrides the unified Azem home. Tests and eval use it for isolation.
	HomeEnv = "AZEM_HOME"

	configFileName   = "config.yaml"
	databaseFileName = "azem.db"
	logFileName      = "azem.log"
)

type Paths struct {
	ConfigDir  string
	ConfigFile string
	DataDir    string
	Database   string
	StateDir   string
	LogFile    string
	Workspace  string
}

// RuntimeDatabasePath resolves the unified SQLite path without migrating or
// opening it. Bootstrap uses it to acquire the process recovery fence before
// any database relocation, backup, migration, or read.
func RuntimeDatabasePath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, databaseFileName), nil
}

func ResolvePaths(startupWorkspace string) (Paths, error) {
	return resolvePaths(startupWorkspace, "")
}

// ResolvePathsWithConfig resolves the unified home while preserving an
// explicitly selected legacy config file if the startup migration moves it.
func ResolvePathsWithConfig(startupWorkspace, configFile string) (Paths, error) {
	return resolvePaths(startupWorkspace, configFile)
}

func resolvePaths(startupWorkspace, configFile string) (Paths, error) {
	if configFile != "" {
		absolute, err := filepath.Abs(configFile)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve config file: %w", err)
		}
		configFile = filepath.Clean(absolute)
	}
	dest, err := Home()
	if err != nil {
		return Paths{}, err
	}
	if err := maybeMigrateLegacyHome(dest); err != nil {
		return Paths{}, err
	}
	if configFile != "" {
		configFile = remapMigratedLegacyPath(configFile, dest)
	}
	workspace, err := canonicalDirectory(startupWorkspace)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve workspace: %w", err)
	}
	configDir := dest
	if configFile == "" {
		configFile = filepath.Join(dest, configFileName)
	} else {
		configDir = filepath.Dir(configFile)
	}
	return Paths{
		ConfigDir:  configDir,
		ConfigFile: configFile,
		DataDir:    dest,
		Database:   filepath.Join(dest, databaseFileName),
		StateDir:   dest,
		LogFile:    filepath.Join(dest, logFileName),
		Workspace:  workspace,
	}, nil
}

// Home is the unified directory for configuration, the database, plugins, blobs,
// and runtime state. AZEM_HOME wins; otherwise ~/.azem.
func Home() (string, error) {
	if value := strings.TrimSpace(os.Getenv(HomeEnv)); value != "" {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", HomeEnv, err)
		}
		return filepath.Clean(absolute), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return defaultHome(home), nil
}

// ResolveHome maps a user home directory to the Azem home, honoring AZEM_HOME.
func ResolveHome(userHome string) string {
	if value := strings.TrimSpace(os.Getenv(HomeEnv)); value != "" {
		if absolute, err := filepath.Abs(value); err == nil {
			return filepath.Clean(absolute)
		}
	}
	return defaultHome(userHome)
}

func defaultHome(userHome string) string {
	return filepath.Join(userHome, ".azem")
}

func maybeMigrateLegacyHome(dest string) error {
	if strings.TrimSpace(os.Getenv(HomeEnv)) != "" {
		return nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home directory: %w", err)
	}
	if filepath.Clean(dest) != filepath.Clean(defaultHome(userHome)) {
		return nil
	}
	return migrateLegacyHome(dest, userHome)
}

func EnsureDirectories(paths Paths) error {
	seen := make(map[string]struct{}, 3)
	for _, dir := range []string{paths.ConfigDir, paths.DataDir, paths.StateDir} {
		if dir == "" {
			continue
		}
		if _, exists := seen[dir]; exists {
			continue
		}
		seen[dir] = struct{}{}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %q: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("protect %q: %w", dir, err)
		}
	}
	return nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", resolved)
	}
	return filepath.Clean(resolved), nil
}
