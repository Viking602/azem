package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var startTerminalCommand = func(name string, args ...string) error {
	if err := exec.Command(name, args...).Start(); err != nil {
		return fmt.Errorf("open terminal: %w", err)
	}
	return nil
}

// OpenTerminal opens the system terminal with the current project as its
// working directory. The desktop bridge owns this host-side action so the
// renderer never assembles or executes a shell command.
func (b *Bridge) OpenTerminal() error {
	workspace := strings.TrimSpace(b.workspace)
	if workspace == "" {
		return fmt.Errorf("workspace is empty")
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return fmt.Errorf("inspect workspace: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace is not a directory")
	}
	name, args, err := terminalCommand(runtime.GOOS, workspace)
	if err != nil {
		return err
	}
	return startTerminalCommand(name, args...)
}

func terminalCommand(goos, workspace string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{"-a", "Terminal", workspace}, nil
	case "windows":
		return "cmd.exe", []string{"/C", "start", "", workspace}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "x-terminal-emulator", []string{"--working-directory", workspace}, nil
	default:
		return "", nil, fmt.Errorf("opening a terminal is not supported on %s", goos)
	}
}
