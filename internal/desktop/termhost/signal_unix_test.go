//go:build unix

package termhost

import "syscall"

func continueProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGCONT)
}
