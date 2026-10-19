package main

import (
	"os/exec"
	"syscall"
)

// detach puts a child in its own process group, so Ctrl-C in the console
// reaches the foreground tool but not the router started for it.
func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// background starts a child without a console of its own, so it keeps
// running after the window that started it closes.
func background(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008} // DETACHED_PROCESS
}
