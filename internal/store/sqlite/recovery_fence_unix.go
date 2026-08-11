//go:build !windows

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

const (
	recoveryFencePreparing = "recovering\n"
	recoveryFenceReady     = "ready\n"
)

type fileRecoveryFence struct {
	file      *os.File
	exclusive bool
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
	err := syscall.Flock(int(f.file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		f.exclusive = true
		return true, f.writeState(recoveryFencePreparing)
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, fmt.Errorf("lock runtime recovery exclusively: %w", err)
}

func (f *fileRecoveryFence) waitForCompletedRecovery(ctx context.Context) (bool, error) {
	if err := f.waitForLock(ctx, syscall.LOCK_SH); err != nil {
		return false, err
	}
	state, err := f.readState()
	if err != nil {
		return false, err
	}
	if state == recoveryFenceReady {
		return true, nil
	}
	// The recovery owner exited before publishing ready. Drop the shared
	// lock and compete to become the next recovery owner.
	if err := syscall.Flock(int(f.file.Fd()), syscall.LOCK_UN); err != nil {
		return false, fmt.Errorf("unlock incomplete runtime recovery: %w", err)
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
	if err := syscall.Flock(int(f.file.Fd()), syscall.LOCK_SH); err != nil {
		return fmt.Errorf("downgrade runtime recovery lock: %w", err)
	}
	f.exclusive = false
	return nil
}

func (f *fileRecoveryFence) Close() error {
	if f == nil || f.file == nil {
		return nil
	}
	file := f.file
	f.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

func (f *fileRecoveryFence) waitForLock(ctx context.Context, mode int) error {
	for {
		err := syscall.Flock(int(f.file.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("lock shared runtime recovery: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
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
	if err != nil && !errors.Is(err, os.ErrClosed) && read == 0 {
		return "", fmt.Errorf("read runtime recovery state: %w", err)
	}
	return string(buffer[:read]), nil
}
