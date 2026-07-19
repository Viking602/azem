package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func ResolvePaths(startupWorkspace string) (Paths, error) {
	platformConfigRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user config directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user home directory: %w", err)
	}
	configRoot := filepath.Join(home, ".config")
	dataRoot, err := userDataDir(platformConfigRoot)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user data directory: %w", err)
	}
	stateRoot, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user state directory: %w", err)
	}
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		dataRoot = value
	}
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		stateRoot = value
	}
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		configRoot = value
	}
	workspace, err := canonicalDirectory(startupWorkspace)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve workspace: %w", err)
	}
	configDir := filepath.Join(configRoot, "azem")
	dataDir := filepath.Join(dataRoot, "azem")
	stateDir := filepath.Join(stateRoot, "azem")
	return Paths{
		ConfigDir:  configDir,
		ConfigFile: filepath.Join(configDir, "config.yaml"),
		DataDir:    dataDir,
		Database:   filepath.Join(configDir, "azem.db"),
		StateDir:   stateDir,
		LogFile:    filepath.Join(stateDir, "azem.log"),
		Workspace:  workspace,
	}, nil
}

func EnsureDirectories(paths Paths) error {
	for _, dir := range []string{paths.ConfigDir, paths.DataDir, paths.StateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %q: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("protect %q: %w", dir, err)
		}
	}
	return nil
}

func userDataDir(configRoot string) (string, error) {
	if runtime.GOOS != "linux" {
		return configRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
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
