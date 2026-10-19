//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts a child in its own process group, so Ctrl-C in the terminal
// reaches the foreground tool but not the router started for it.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// background starts a child in its own session, so it keeps running after
// the app that started it has quit.
func background(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
