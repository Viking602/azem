package config

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const movedToName = "MOVED_TO"

var (
	legacyDatabaseNames = []string{
		"azem.db",
		"azem.db-wal",
		"azem.db-shm",
		"azem.db.bak",
		"config.yaml",
	}
	legacySkipNames = map[string]struct{}{
		"azem.db.runtime.lock": {},
		"azem.db.upgrade.lock": {},
		movedToName:            {},
	}
	legacyPreciousNames = map[string]struct{}{
		"azem.db":            {},
		"azem.db-wal":        {},
		"azem.db-shm":        {},
		"azem.db.bak":        {},
		"config.yaml":        {},
		"blobs":              {},
		"plugin-packages":    {},
		"attachments":        {},
		"credentials.json":   {},
		"window.json":        {},
		"agents":             {},
		"hooks":              {},
		"hook-transcripts":   {},
		"subagent-worktrees": {},
		"background":         {},
	}
)

func migrateLegacyHome(dest, userHome string) error {
	dest = filepath.Clean(dest)
	sources := legacyHomeRoots(runtime.GOOS, userHome, dest)
	if !legacyHasPrecious(sources) {
		return nil
	}
	if err := refuseIfLegacyDatabaseBusy(sources, dest); err != nil {
		return err
	}
	if err := refuseIfMigrationConflicts(sources, dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return fmt.Errorf("create azem home %q: %w", dest, err)
	}
	if err := os.Chmod(dest, 0o700); err != nil {
		return fmt.Errorf("protect azem home %q: %w", dest, err)
	}
	for _, source := range sources {
		for _, name := range legacyDatabaseNames {
			if err := relocateIfPresent(filepath.Join(source, name), filepath.Join(dest, name), dest); err != nil {
				return err
			}
		}
	}
	for _, source := range sources {
		if err := migrateRoot(source, dest); err != nil {
			return err
		}
	}
	return nil
}

func legacyHomeRoots(goos, userHome, dest string) []string {
	candidates := []string{filepath.Join(userHome, ".config", "azem")}
	for _, variable := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		if root := strings.TrimSpace(os.Getenv(variable)); root != "" {
			candidates = append(candidates, filepath.Join(root, "azem"))
		}
	}
	switch goos {
	case "darwin":
		candidates = append(candidates,
			filepath.Join(userHome, "Library", "Application Support", "azem"),
			filepath.Join(userHome, "Library", "Caches", "azem"),
		)
	case "windows":
		candidates = append(candidates,
			filepath.Join(userHome, "AppData", "Roaming", "azem"),
			filepath.Join(userHome, "AppData", "Local", "azem"),
		)
	default:
		candidates = append(candidates,
			filepath.Join(userHome, ".local", "share", "azem"),
			filepath.Join(userHome, ".cache", "azem"),
		)
	}
	roots := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	dest = filepath.Clean(dest)
	for _, candidate := range candidates {
		clean := filepath.Clean(candidate)
		if clean == dest {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		roots = append(roots, clean)
	}
	return roots
}

func remapMigratedLegacyPath(path, dest string) string {
	if fileExists(path) {
		return path
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	for _, source := range legacyHomeRoots(runtime.GOOS, userHome, dest) {
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		candidate := filepath.Join(dest, relative)
		if fileExists(candidate) {
			return candidate
		}
	}
	return path
}

func legacyHasPrecious(sources []string) bool {
	for _, source := range sources {
		entries, err := os.ReadDir(source)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if _, ok := legacyPreciousNames[entry.Name()]; ok {
				return true
			}
		}
	}
	return false
}

func refuseIfLegacyDatabaseBusy(sources []string, dest string) error {
	for _, source := range sources {
		lock := filepath.Join(source, "azem.db.runtime.lock")
		busy, err := exclusiveLockHeld(lock)
		if err != nil {
			return fmt.Errorf("check legacy database lock %q: %w", lock, err)
		}
		if busy {
			return fmt.Errorf("legacy database at %q is in use; quit Azem before migrating to %s", source, dest)
		}
	}
	return nil
}

func refuseIfMigrationConflicts(sources []string, dest string) error {
	for _, source := range sources {
		entries, err := os.ReadDir(source)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read legacy azem directory %q: %w", source, err)
		}
		for _, entry := range entries {
			if _, skip := legacySkipNames[entry.Name()]; skip {
				continue
			}
			if err := migrationConflict(filepath.Join(source, entry.Name()), filepath.Join(dest, entry.Name()), dest); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrationConflict(from, to, dest string) error {
	fromInfo, err := os.Lstat(from)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	toInfo, err := os.Lstat(to)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fromInfo.IsDir() && toInfo.IsDir() {
		entries, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := migrationConflict(filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name()), dest); err != nil {
				return err
			}
		}
		return nil
	}
	if replaceableLegacyPath(from) || replaceableLegacyPath(to) || keepExistingHomeFile(from, dest) {
		return nil
	}
	return fmt.Errorf("cannot migrate %q onto existing %q", from, to)
}

func migrateRoot(source, dest string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read legacy azem directory %q: %w", source, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if _, skip := legacySkipNames[name]; skip {
			continue
		}
		if slices.Contains(legacyDatabaseNames, name) {
			continue
		}
		if err := relocateIfPresent(filepath.Join(source, name), filepath.Join(dest, name), dest); err != nil {
			return err
		}
	}
	return writeMovedTo(source, dest)
}

func relocateIfPresent(from, to, dest string) error {
	info, err := os.Lstat(from)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat legacy path %q: %w", from, err)
	}
	return relocate(from, to, dest, info)
}

func relocate(from, to, dest string, fromInfo os.FileInfo) error {
	toInfo, err := os.Lstat(to)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat azem home path %q: %w", to, err)
		}
		if err := renameOrCopy(from, to, fromInfo); err != nil {
			return fmt.Errorf("migrate %q to %q: %w", from, to, err)
		}
		return nil
	}
	if fromInfo.IsDir() && toInfo.IsDir() {
		return mergeDir(from, to, dest)
	}
	if replaceableLegacyPath(from) || replaceableLegacyPath(to) || keepExistingHomeFile(from, dest) {
		return nil
	}
	return fmt.Errorf("cannot migrate %q onto existing %q", from, to)
}

func mergeDir(from, to, dest string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return fmt.Errorf("read %q: %w", from, err)
	}
	for _, entry := range entries {
		if err := relocateIfPresent(filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name()), dest); err != nil {
			return err
		}
	}
	remaining, err := os.ReadDir(from)
	if err != nil {
		return fmt.Errorf("re-read %q: %w", from, err)
	}
	if len(remaining) == 0 {
		if err := os.Remove(from); err != nil {
			return fmt.Errorf("remove empty legacy directory %q: %w", from, err)
		}
	}
	return nil
}

func renameOrCopy(from, to string, info os.FileInfo) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	} else if err := copyAll(from, to, info); err != nil {
		return err
	}
	return os.RemoveAll(from)
}

func copyAll(from, to string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(target, to)
	}
	if info.IsDir() {
		if err := os.MkdirAll(to, info.Mode().Perm()); err != nil {
			return err
		}
		return os.CopyFS(to, os.DirFS(from))
	}
	return copyFile(from, to, info.Mode().Perm())
}

func copyFile(from, to string, perm fs.FileMode) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		_ = os.Remove(to)
		return err
	}
	if err := destination.Close(); err != nil {
		_ = os.Remove(to)
		return err
	}
	return nil
}

func writeMovedTo(source, dest string) error {
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	payload := []byte("Moved to " + dest + "\n")
	path := filepath.Join(source, movedToName)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func keepExistingHomeFile(from, dest string) bool {
	if !fileExists(filepath.Join(dest, databaseFileName)) {
		return false
	}
	return slices.Contains(legacyDatabaseNames, filepath.Base(from))
}

func replaceableLegacyPath(path string) bool {
	switch filepath.Base(path) {
	case "window.json", "azem.log", movedToName:
		return true
	}
	for current := filepath.Clean(path); current != "." && current != string(filepath.Separator) && filepath.Dir(current) != current; current = filepath.Dir(current) {
		switch filepath.Base(current) {
		case "hook-transcripts", "background":
			return true
		}
	}
	return false
}
