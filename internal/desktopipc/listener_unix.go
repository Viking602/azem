//go:build !windows

package desktopipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func DefaultAddress(stateDir, workspaceID string) string {
	return filepath.Join(stateDir, "gpui-daemons", workspaceID, "azem.sock")
}

func Listen(address string) (net.Listener, error) {
	directory := filepath.Dir(address)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	lockPath := address + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile.Chmod(0o600); err != nil {
		lockFile.Close()
		return nil, err
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lockFile.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	releaseLock := func() {
		_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		_ = lockFile.Close()
	}
	if existing, err := net.DialTimeout("unix", address, 150*time.Millisecond); err == nil {
		existing.Close()
		releaseLock()
		return nil, ErrAlreadyRunning
	}
	_ = os.Remove(address)
	listener, err := net.Listen("unix", address)
	if err != nil {
		releaseLock()
		return nil, err
	}
	if err := os.Chmod(address, 0o600); err != nil {
		listener.Close()
		os.Remove(address)
		releaseLock()
		return nil, err
	}
	return &removingListener{Listener: listener, path: address, lock: lockFile}, nil
}

func Dial(ctx context.Context, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", address)
}

type removingListener struct {
	net.Listener
	path string
	lock *os.File
}

func (listener *removingListener) Close() error {
	err := listener.Listener.Close()
	_ = os.Remove(listener.path)
	if listener.lock != nil {
		_ = unix.Flock(int(listener.lock.Fd()), unix.LOCK_UN)
		_ = listener.lock.Close()
	}
	return err
}
