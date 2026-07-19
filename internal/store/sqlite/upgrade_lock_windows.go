//go:build windows

package sqlite

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func withDatabaseUpgradeLock(ctx context.Context, path string, action func() error) error {
	lock, err := os.OpenFile(path+".upgrade.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open database upgrade lock: %w", err)
	}
	defer lock.Close()
	handle := windows.Handle(lock.Fd())
	var overlapped windows.Overlapped
	for {
		err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			break
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			return fmt.Errorf("lock database upgrade: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
	return action()
}
