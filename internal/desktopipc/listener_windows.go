//go:build windows

package desktopipc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func DefaultAddress(_ string, workspaceID string) string {
	return `\\.\pipe\azem-gpui-` + workspaceID
}

func Listen(address string) (net.Listener, error) {
	digest := sha256.Sum256([]byte(address))
	mutexName, err := windows.UTF16PtrFromString(fmt.Sprintf(`Local\AzemGPUI-%x`, digest[:16]))
	if err != nil {
		return nil, err
	}
	mutex, err := windows.CreateMutex(nil, false, mutexName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(mutex)
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(address, &winio.PipeConfig{
		SecurityDescriptor: `D:P(A;;GA;;;OW)(A;;GA;;;SY)`,
		MessageMode:        false,
		InputBufferSize:    MaxControlFrameBytes,
		OutputBufferSize:   MaxControlFrameBytes,
	})
	if err != nil {
		windows.CloseHandle(mutex)
		return nil, err
	}
	return &namedPipeListener{Listener: listener, mutex: mutex}, nil
}

type namedPipeListener struct {
	net.Listener
	mutex windows.Handle
}

func (listener *namedPipeListener) Close() error {
	err := listener.Listener.Close()
	if listener.mutex != 0 {
		_ = windows.CloseHandle(listener.mutex)
		listener.mutex = 0
	}
	return err
}

func Dial(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}
