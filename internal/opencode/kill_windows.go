//go:build windows

package opencode

import (
	"os/exec"
	"strconv"
)

// Group is a no-op on Windows: KillTree finds the children by parent.
func Group(*exec.Cmd) {}

// KillTree stops a command and everything it started (npm installs
// opencode.cmd, which starts OpenCode itself).
func KillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
