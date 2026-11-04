package daemon

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/detach"
)

// StartStandby starts `exe daemon --standby` detached and returns at once. On
// Windows the login entry goes through it, so the scheduler outlives the
// console window the login opened, and that window closes.
func StartStandby(exe string) error {
	return startDetached(exe, []string{"daemon", "--standby"}, os.Environ())
}

// startDetached starts exe detached, its output appended to daemon.log: with
// no console and no service manager, nothing else keeps the log.
func startDetached(exe string, args, env []string) error {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(config.StateDir(), "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logf, logf
	detach.Cmd(cmd)
	return cmd.Start()
}
