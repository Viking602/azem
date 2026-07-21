//go:build windows

package agent

import (
	"fmt"
	"os"
	"os/exec"
)

func configureShellCommand(command *exec.Cmd) {
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		if err := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(command.Process.Pid)).Run(); err == nil {
			return nil
		}
		if err := command.Process.Kill(); err != nil {
			return err
		}
		return os.ErrProcessDone
	}
}
