package detach

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// setDetached starts cmd in its own process group with no console, so it outlives
// the process that starts it, ignores that console's Ctrl+C and closing, and
// opens no window of its own.
func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}
