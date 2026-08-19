//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMigrateFailsWhenLegacyDatabaseLocked(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "")
	legacy := filepath.Join(home, ".config", "azem")
	writeFile(t, filepath.Join(legacy, "azem.db"), "db")
	lockPath := filepath.Join(legacy, "azem.db.runtime.lock")
	writeFile(t, lockPath, "ready\n")
	lock, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) })

	if _, err := ResolvePaths(workspace); err == nil {
		t.Fatal("locked legacy database was migrated")
	}
	assertFile(t, filepath.Join(legacy, "azem.db"), "db")
}
