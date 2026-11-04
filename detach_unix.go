//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// detach puts cmd in its own session, so it outlives the process that starts
// it and the terminal or app around it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
