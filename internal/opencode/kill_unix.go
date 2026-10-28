//go:build !windows

package opencode

import (
	"os/exec"
	"syscall"
)

// Group starts cmd in a process group of its own, so KillTree reaches
// OpenCode itself when cmd is a wrapper (npm installs a node one).
func Group(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// KillTree stops a command and, when it was started with Group, everything
// it started (a command left in the caller's group, so that Ctrl-C in a
// terminal reaches it, is stopped alone).
func KillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if a := cmd.SysProcAttr; a != nil && a.Setpgid {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd.Process.Kill()
}
