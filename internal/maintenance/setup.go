package maintenance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Viking602/azem/internal/config"
)

type SetupOptions struct {
	Workspace  string
	ConfigFile string
	Force      bool
	DryRun     bool
}

type SetupReport struct {
	Paths       config.Paths    `json:"paths"`
	Created     []string        `json:"created"`
	Existing    []string        `json:"existing"`
	Executables map[string]bool `json:"executables"`
	DryRun      bool            `json:"dryRun"`
}

func Setup(ctx context.Context, options SetupOptions) (SetupReport, error) {
	if err := ctx.Err(); err != nil {
		return SetupReport{}, err
	}
	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" {
		return SetupReport{}, errors.New("setup workspace is required")
	}
	var (
		paths config.Paths
		err   error
	)
	if options.ConfigFile != "" {
		paths, err = config.ResolvePathsWithConfig(workspace, options.ConfigFile)
	} else {
		paths, err = config.ResolvePaths(workspace)
	}
	if err != nil {
		return SetupReport{}, err
	}
	report := SetupReport{Paths: paths, DryRun: options.DryRun, Executables: make(map[string]bool), Created: []string{}, Existing: []string{}}
	for _, name := range []string{"git", "gh", "bun", "go"} {
		_, err := exec.LookPath(name)
		report.Executables[name] = err == nil
	}
	seenDirectories := make(map[string]struct{})
	for _, directory := range []string{paths.ConfigDir, paths.DataDir, paths.StateDir, filepath.Join(paths.DataDir, "blobs"), filepath.Join(paths.DataDir, "plugin-packages"), filepath.Join(paths.StateDir, "attachments")} {
		if _, duplicate := seenDirectories[directory]; duplicate {
			continue
		}
		seenDirectories[directory] = struct{}{}
		if info, statErr := os.Stat(directory); statErr == nil && info.IsDir() {
			report.Existing = append(report.Existing, directory)
			continue
		}
		report.Created = append(report.Created, directory)
		if !options.DryRun {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				return report, err
			}
			if err := os.Chmod(directory, 0o700); err != nil {
				return report, err
			}
		}
	}
	if info, statErr := os.Lstat(paths.ConfigFile); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return report, errors.New("setup config target must be a regular file")
		}
		report.Existing = append(report.Existing, paths.ConfigFile)
		if !options.Force {
			return report, nil
		}
	} else if !os.IsNotExist(statErr) {
		return report, statErr
	}
	report.Created = append(report.Created, paths.ConfigFile)
	if options.DryRun {
		return report, nil
	}
	body := "version: 1\n\nworkspace:\n  root: " + strconv.Quote(paths.Workspace) + "\n  allow_write: true\n  shell_policy: prompt\n  allow_network: prompt\n\nauth:\n  store: sqlite\n  import_codex: true\n  import_grok: true\n"
	if err := atomicWritePrivate(paths.ConfigFile, []byte(body), 0o600); err != nil {
		return report, err
	}
	return report, nil
}

func atomicWritePrivate(path string, payload []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".azem-maintenance-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(name)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to replace symlinked maintenance target")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	committed = true
	return nil
}
