//go:build unix

// Package detach starts processes that outlive the one starting them: `run
// --bg`, the scheduler the login entry starts on Windows, and its re-exec there.
package detach

import (
	"os/exec"
	"syscall"
)

// setDetached puts cmd in its own session, so it outlives the process that starts
// it and the terminal or app around it.
func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
