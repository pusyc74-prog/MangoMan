//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts a child in its own process group, so Ctrl-C in the terminal
// reaches the foreground tool but not the router started for it.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
