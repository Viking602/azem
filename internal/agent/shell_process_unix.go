//go:build !windows

package agent

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

type shellProcessOwner struct {
	command *exec.Cmd
	pgid    int
}

func newShellProcessOwner(command *exec.Cmd) (*shellProcessOwner, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &shellProcessOwner{command: command}, nil
}

func (o *shellProcessOwner) Assign(command *exec.Cmd) error {
	if command.Process == nil {
		return errors.New("process not started")
	}
	o.pgid = command.Process.Pid
	return nil
}
func (o *shellProcessOwner) PGID() int     { return o.pgid }
func (o *shellProcessOwner) JobID() string { return "" }
func (o *shellProcessOwner) Close() error  { return nil }
func (o *shellProcessOwner) Terminate() error {
	if o.pgid == 0 {
		return nil
	}
	pgid := o.pgid
	if err := syscall.Kill(-pgid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
		return nil
	} else if err != nil {
		return fmt.Errorf("signal process group %d: %w", pgid, err)
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group %d: %w", pgid, err)
	}
	for i := 0; i < 30; i++ {
		if errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("process group %d still exists after SIGKILL", pgid)
}

func shellProcessExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
