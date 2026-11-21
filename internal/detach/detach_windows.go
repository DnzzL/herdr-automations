package detach

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// setDetached starts cmd in its own process group with a hidden console, so it
// outlives the process that starts it, ignores that console's Ctrl+C and
// closing, and opens no window. Not DETACHED_PROCESS: that leaves no console,
// so every console child (hc, git) opens its own window.
func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
}
