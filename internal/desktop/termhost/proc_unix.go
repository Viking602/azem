//go:build unix

package termhost

import (
	"os/exec"
	"syscall"
)

func applyProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGHUP)
	_ = syscall.Kill(-pid, syscall.SIGTERM)
}

func killProcessGroupHard(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
