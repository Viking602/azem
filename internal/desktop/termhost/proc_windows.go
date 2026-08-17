//go:build windows

package termhost

import "os/exec"

func applyProcessGroup(*exec.Cmd) {}

func killProcessGroup(int) {}

func killProcessGroupHard(int) {}
