//go:build !windows

package guardian

import (
	"os/exec"
	"syscall"
)

// group starts a command in its own process group, so stopping it also
// stops what it started (npm starts node, Docker starts containers).
func group(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func stopGroup(c *exec.Cmd) {
	if c.Process != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	}
}
