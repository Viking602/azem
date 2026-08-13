//go:build windows

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	recoveryFencePreparing = "recovering\n"
	recoveryFenceReady     = "ready\n"
)

type fileRecoveryFence struct {
	file       *os.File
	overlapped windows.Overlapped
	exclusive  bool
}

func acquireRecoveryFence(ctx context.Context, path string) (RecoveryFence, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open runtime recovery lock: %w", err)
	}
	fence := &fileRecoveryFence{file: file}
	for {
		owner, err := fence.tryStartRecovery()
		if err != nil {
			_ = fence.Close()
			return nil, false, err
		}
		if owner {
			return fence, true, nil
		}
		ready, err := fence.waitForCompletedRecovery(ctx)
		if err != nil {
			_ = fence.Close()
			return nil, false, err
		}
		if ready {
			return fence, false, nil
		}
	}
}

func (f *fileRecoveryFence) tryStartRecovery() (bool, error) {
	err := f.tryLock(true)
	if err == nil {
		f.exclusive = true
		return true, f.writeState(recoveryFencePreparing)
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, fmt.Errorf("lock runtime recovery exclusively: %w", err)
}

func (f *fileRecoveryFence) waitForCompletedRecovery(ctx context.Context) (bool, error) {
	if err := f.waitForShared(ctx); err != nil {
		return false, err
	}
	state, err := f.readState()
	if err != nil {
		return false, err
	}
	if state == recoveryFenceReady {
		return true, nil
	}
	if err := f.unlock(); err != nil {
		return false, err
	}
	return false, nil
}

func (f *fileRecoveryFence) FinishRecovery() error {
	if f == nil || f.file == nil || !f.exclusive {
		return nil
	}
	if err := f.writeState(recoveryFenceReady); err != nil {
		return err
	}
	if err := f.unlock(); err != nil {
		return err
	}
	if err := f.waitForShared(context.Background()); err != nil {
		return err
	}
	f.exclusive = false
	return nil
}

func (f *fileRecoveryFence) Close() error {
	if f == nil || f.file == nil {
		return nil
	}
	file := f.file
	unlockErr := f.unlock()
	f.file = nil
	return errors.Join(unlockErr, file.Close())
}

func (f *fileRecoveryFence) tryLock(exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	return windows.LockFileEx(windows.Handle(f.file.Fd()), flags, 0, 1, 0, &f.overlapped)
}

func (f *fileRecoveryFence) waitForShared(ctx context.Context) error {
	for {
		err := f.tryLock(false)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return fmt.Errorf("lock shared runtime recovery: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (f *fileRecoveryFence) unlock() error {
	if f == nil || f.file == nil {
		return nil
	}
	if err := windows.UnlockFileEx(windows.Handle(f.file.Fd()), 0, 1, 0, &f.overlapped); err != nil {
		return fmt.Errorf("unlock runtime recovery: %w", err)
	}
	return nil
}

func (f *fileRecoveryFence) writeState(state string) error {
	if err := f.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate runtime recovery state: %w", err)
	}
	if _, err := f.file.WriteAt([]byte(state), 0); err != nil {
		return fmt.Errorf("write runtime recovery state: %w", err)
	}
	return f.file.Sync()
}

func (f *fileRecoveryFence) readState() (string, error) {
	buffer := make([]byte, len(recoveryFencePreparing))
	read, err := f.file.ReadAt(buffer, 0)
	if err != nil && read == 0 {
		return "", fmt.Errorf("read runtime recovery state: %w", err)
	}
	return string(buffer[:read]), nil
}
