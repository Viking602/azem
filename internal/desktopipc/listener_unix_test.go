//go:build !windows

package desktopipc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixListenerIsOwnerOnlyAndReplacesStaleSocket(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "azem-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	address := filepath.Join(directory, "runtime.sock")
	if err := os.WriteFile(address, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if second, secondErr := Listen(address); second != nil || !errors.Is(secondErr, ErrAlreadyRunning) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("second listener = %#v, error = %v", second, secondErr)
	}
	info, err := os.Stat(address)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("socket permissions = %o, want 600", permissions)
	}
}
