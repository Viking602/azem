//go:build windows

package background

import (
	"os"
	"os/exec"
)

func configureProcess(*exec.Cmd) {}

func terminatePID(pid int) error {
	if pid <= 0 {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

// Windows does not expose a safe os.FindProcess liveness probe. Recovered
// entries become exited after restart instead of risking a reused PID.
func processExists(int) bool { return false }
