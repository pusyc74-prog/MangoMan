package guardian

import (
	"os/exec"
	"strconv"
)

func group(*exec.Cmd) {}

// stopGroup ends the command and everything it started.
func stopGroup(c *exec.Cmd) {
	if c.Process != nil {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid)).Run()
	}
}
